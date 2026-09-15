package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type LiveRequestInfo struct {
	ID             string `json:"id"`
	Timestamp      string `json:"timestamp"`
	PID            int    `json:"pid"`
	ProcessName    string `json:"process_name"`
	Protocol       string `json:"protocol"` // "Responses API" or "Chat Completions"
	RequestedModel string `json:"requested_model"`
	AppliedModel   string `json:"applied_model"`
	ThinkingEffort string `json:"thinking_effort"`
	IsOverridden   bool   `json:"is_overridden"`
	Status         string `json:"status"` // "running", "completed", "error"
	DurationMs     int64  `json:"duration_ms"`
	InputTokens    int    `json:"input_tokens"`
	OutputTokens   int    `json:"output_tokens"`
	CachedTokens   int    `json:"cached_tokens"`
	CacheHit       bool   `json:"cache_hit"`
	ErrorMsg       string `json:"error_msg,omitempty"`
}

type WSHub struct {
	clients      map[net.Conn]bool
	sseClients   map[chan []byte]bool
	mu           sync.Mutex
	recentEvents []LiveRequestInfo
	recentMu     sync.RWMutex
}

var GlobalWSHub = &WSHub{
	clients:      make(map[net.Conn]bool),
	sseClients:   make(map[chan []byte]bool),
	recentEvents: make([]LiveRequestInfo, 0, 50),
}

func (h *WSHub) AddRecentRequest(req LiveRequestInfo) {
	h.recentMu.Lock()
	defer h.recentMu.Unlock()

	// Mevcut varsa güncelle
	for i, r := range h.recentEvents {
		if r.ID == req.ID {
			h.recentEvents[i] = req
			return
		}
	}

	if len(h.recentEvents) >= 50 {
		h.recentEvents = h.recentEvents[1:]
	}
	h.recentEvents = append(h.recentEvents, req)
}

func (h *WSHub) GetRecentRequests() []LiveRequestInfo {
	h.recentMu.RLock()
	defer h.recentMu.RUnlock()

	res := make([]LiveRequestInfo, len(h.recentEvents))
	copy(res, h.recentEvents)
	return res
}

func (h *WSHub) Broadcast(msgType string, payload interface{}) {
	msgObj := map[string]interface{}{
		"type":    msgType,
		"data":    payload,
		"time_ms": time.Now().UnixMilli(),
	}

	rawBytes, err := json.Marshal(msgObj)
	if err != nil {
		return
	}

	frame := encodeWebSocketTextFrame(rawBytes)

	h.mu.Lock()
	defer h.mu.Unlock()

	// 1. WebSocket İstemcilerine ilet
	for conn := range h.clients {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(frame); err != nil {
			conn.Close()
			delete(h.clients, conn)
		}
	}

	// 2. SSE İstemcilerine ilet
	ssePayload := []byte("data: " + string(rawBytes) + "\n\n")
	for ch := range h.sseClients {
		select {
		case ch <- ssePayload:
		default:
		}
	}
}

func encodeWebSocketTextFrame(data []byte) []byte {
	length := len(data)
	var frame []byte

	frame = append(frame, 0x81) // FIN + text frame

	if length <= 125 {
		frame = append(frame, byte(length))
	} else if length <= 65535 {
		frame = append(frame, 126, byte(length>>8), byte(length&0xFF))
	} else {
		frame = append(frame, 127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte((length>>(i*8))&0xFF))
		}
	}

	frame = append(frame, data...)
	return frame
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
		http.Error(w, "WebSocket upgrade required", http.StatusBadRequest)
		return
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "Sec-WebSocket-Key required", http.StatusBadRequest)
		return
	}

	// RFC 6455 Handshake
	h := sha1.New()
	h.Write([]byte(key + wsMagicGUID))
	acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Accept: %s\r\n\r\n", acceptKey)

	if _, err := bufrw.WriteString(res); err != nil {
		conn.Close()
		return
	}
	_ = bufrw.Flush()

	GlobalWSHub.mu.Lock()
	GlobalWSHub.clients[conn] = true
	GlobalWSHub.mu.Unlock()

	log.Printf("[WebSocket] Yeni UI istemcisi bağlandı: %s", conn.RemoteAddr().String())

	// İlk bağlantıda anlık sistem durumunu gönder
	initialState := map[string]interface{}{
		"type": "init_state",
		"data": map[string]interface{}{
			"accounts":        GlobalAccountStore.GetAllAccounts(),
			"settings":        GlobalSettingsManager.Get(),
			"recent_requests": GlobalWSHub.GetRecentRequests(),
		},
		"time_ms": time.Now().UnixMilli(),
	}
	if initBytes, err := json.Marshal(initialState); err == nil {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write(encodeWebSocketTextFrame(initBytes))
	}

	// Ping/Pong ve bağlantı bekleme
	go func() {
		defer func() {
			GlobalWSHub.mu.Lock()
			delete(GlobalWSHub.clients, conn)
			GlobalWSHub.mu.Unlock()
			conn.Close()
			log.Printf("[WebSocket] İstemci ayrıldı: %s", conn.RemoteAddr().String())
		}()

		buf := make([]byte, 1024)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
			_, err := conn.Read(buf)
			if err != nil {
				break
			}
		}
	}()
}

func handleSSEEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 10)
	GlobalWSHub.mu.Lock()
	GlobalWSHub.sseClients[ch] = true
	GlobalWSHub.mu.Unlock()

	defer func() {
		GlobalWSHub.mu.Lock()
		delete(GlobalWSHub.sseClients, ch)
		GlobalWSHub.mu.Unlock()
		close(ch)
	}()

	// İlk durum
	initialState := map[string]interface{}{
		"type": "init_state",
		"data": map[string]interface{}{
			"accounts":        GlobalAccountStore.GetAllAccounts(),
			"settings":        GlobalSettingsManager.Get(),
			"recent_requests": GlobalWSHub.GetRecentRequests(),
		},
		"time_ms": time.Now().UnixMilli(),
	}
	if initBytes, err := json.Marshal(initialState); err == nil {
		fmt.Fprintf(w, "data: %s\n\n", string(initBytes))
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}

// Global bildirim yardımcıları (Asenkron ve deadlock korumalı)
func BroadcastAccountChange() {
	go func() {
		if GlobalAccountStore != nil && GlobalWSHub != nil {
			GlobalWSHub.Broadcast("accounts_update", GlobalAccountStore.GetAllAccounts())
		}
	}()
}

func BroadcastSettingsChange() {
	go func() {
		if GlobalSettingsManager != nil && GlobalWSHub != nil {
			GlobalWSHub.Broadcast("settings_update", GlobalSettingsManager.Get())
		}
	}()
}

func BroadcastRequestEvent(info LiveRequestInfo) {
	go func() {
		if GlobalWSHub != nil {
			GlobalWSHub.AddRecentRequest(info)
			GlobalWSHub.Broadcast("request_event", info)
		}
	}()
}
