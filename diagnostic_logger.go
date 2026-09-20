package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type DiagnosticLogLevel string

const (
	LogLevelError    DiagnosticLogLevel = "error"    // 🔴 Kırmızı: HTTP 400, 429, 500, 503 vb.
	LogLevelRescue   DiagnosticLogLevel = "rescue"   // 🟢 Zümrüt/Yeşil: Akıllı Kurtarma (Thought Signature Graceful Degradation)
	LogLevelFeedback DiagnosticLogLevel = "feedback" // 🟢 Camgöbeği/Cyan: Mekanizma Geri Bildirimi / Başarı
	LogLevelInfo     DiagnosticLogLevel = "info"     // 🟣 Mor: Hesap Yönlendirme, Model Override
	LogLevelWarn     DiagnosticLogLevel = "warn"     // 🟡 Sarı: Uyarı
)

type DiagnosticLog struct {
	ID          string                 `json:"id"`
	Timestamp   string                 `json:"timestamp"`    // "15:04:05"
	TimeMs      int64                  `json:"time_ms"`      // Unix epoch ms
	Level       DiagnosticLogLevel     `json:"level"`        // "error", "rescue", "feedback", "info", "warn"
	Category    string                 `json:"category"`     // "http_error", "thought_rescue", "account_routing", "model_override", "signature_saved", "tool_bridge"
	PID         int                    `json:"pid"`
	ProcessName string                 `json:"process_name"` // "node.exe (bin.ts)", "Cline", "Jackett"
	Account     string                 `json:"account,omitempty"`
	Model       string                 `json:"model,omitempty"`
	HTTPStatus  int                    `json:"http_status,omitempty"` // 400, 429, 503 vb.
	Action      string                 `json:"action"`                // Örn: "Düşünce İmzası Kurtarma (Degradation)", "Google CloudCode 400 Hatası"
	Message     string                 `json:"message"`               // Açıklayıcı Türkçe metin
	Details     map[string]interface{} `json:"details,omitempty"`
}

type DiagnosticLogManager struct {
	logs []DiagnosticLog
	max  int
	mu   sync.RWMutex
}

var GlobalDiagnosticLogger = NewDiagnosticLogManager(300)

func NewDiagnosticLogManager(max int) *DiagnosticLogManager {
	if max <= 0 {
		max = 300
	}
	return &DiagnosticLogManager{
		logs: make([]DiagnosticLog, 0, max),
		max:  max,
	}
}

func (m *DiagnosticLogManager) Add(entry DiagnosticLog) {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("dlog_%d_%04x", time.Now().UnixMilli(), rand.Intn(0xffff))
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format("15:04:05")
	}
	if entry.TimeMs == 0 {
		entry.TimeMs = time.Now().UnixMilli()
	}
	if entry.ProcessName == "" {
		entry.ProcessName = "Bilinmeyen Süreç"
	}

	m.mu.Lock()
	if len(m.logs) >= m.max {
		m.logs = m.logs[1:]
	}
	m.logs = append(m.logs, entry)
	m.mu.Unlock()

	// 1. Konsola renkli/biçimli yazdır
	prefix := "[DIAGNOSTIC]"
	switch entry.Level {
	case LogLevelError:
		prefix = "[🔴 HTTP HATA]"
	case LogLevelRescue:
		prefix = "[🟢 KURTARMA/GERİBİLDİRİM]"
	case LogLevelFeedback:
		prefix = "[✨ GERİ BİLDİRİM]"
	case LogLevelInfo:
		prefix = "[ℹ️ BİLGİ]"
	case LogLevelWarn:
		prefix = "[⚠️ UYARI]"
	}
	log.Printf("%s PID: %d (%s) | %s: %s\n", prefix, entry.PID, entry.ProcessName, entry.Action, entry.Message)

	// 2. WebSocket & SSE üzerinden anlık canlı yayınla
	if GlobalWSHub != nil {
		GlobalWSHub.Broadcast("diagnostic_log", entry)
	}
}

func (m *DiagnosticLogManager) GetAll() []DiagnosticLog {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]DiagnosticLog, len(m.logs))
	copy(res, m.logs)
	return res
}

func (m *DiagnosticLogManager) Clear() {
	m.mu.Lock()
	m.logs = make([]DiagnosticLog, 0, m.max)
	m.mu.Unlock()

	if GlobalWSHub != nil {
		GlobalWSHub.Broadcast("diagnostic_logs_cleared", map[string]bool{"cleared": true})
	}
}

// Yardımcı Hızlı Kayıt Fonksiyonları

func (m *DiagnosticLogManager) LogHttpError(pid int, procName, account, model string, httpStatus int, errorMsg string, details map[string]interface{}) {
	action := fmt.Sprintf("Google CloudCode API Hatası (HTTP %d)", httpStatus)
	if httpStatus == 0 {
		action = "Bağlantı / Ağ Hatası"
	}

	m.Add(DiagnosticLog{
		Level:       LogLevelError,
		Category:    "http_error",
		PID:         pid,
		ProcessName: procName,
		Account:     account,
		Model:       model,
		HTTPStatus:  httpStatus,
		Action:      action,
		Message:     errorMsg,
		Details:     details,
	})
}

func (m *DiagnosticLogManager) LogRescue(pid int, procName, account, model, action, message string, details map[string]interface{}) {
	m.Add(DiagnosticLog{
		Level:       LogLevelRescue,
		Category:    "thought_rescue",
		PID:         pid,
		ProcessName: procName,
		Account:     account,
		Model:       model,
		Action:      action,
		Message:     message,
		Details:     details,
	})
}

func (m *DiagnosticLogManager) LogFeedback(pid int, procName, account, model, action, message string, details map[string]interface{}) {
	m.Add(DiagnosticLog{
		Level:       LogLevelFeedback,
		Category:    "feedback",
		PID:         pid,
		ProcessName: procName,
		Account:     account,
		Model:       model,
		Action:      action,
		Message:     message,
		Details:     details,
	})
}

func (m *DiagnosticLogManager) LogInfo(pid int, procName, account, model, action, message string, details map[string]interface{}) {
	m.Add(DiagnosticLog{
		Level:       LogLevelInfo,
		Category:    "info",
		PID:         pid,
		ProcessName: procName,
		Account:     account,
		Model:       model,
		Action:      action,
		Message:     message,
		Details:     details,
	})
}

// HTTP API Handlers
func handleDiagnosticLogsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodDelete || (r.Method == http.MethodPost && r.URL.Query().Get("action") == "clear") {
		GlobalDiagnosticLogger.Clear()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Teşhis ve olay logları temizlendi",
		})
		return
	}

	if r.Method == http.MethodGet {
		logs := GlobalDiagnosticLogger.GetAll()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"count":   len(logs),
			"logs":    logs,
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
