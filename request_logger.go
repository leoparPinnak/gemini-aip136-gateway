package main

// request_logger.go — Cache-hit sprint'i (A1) kalıcı istek logu.
// Amaç: 0-cache-hit olaylarını RETRO korele edebilmek. Bellek ring'leri
// (diagnostic 300 / ws 50) saatlerce dolduğu için geçmiş olaylar siliniyordu;
// bu dosya her isteği logs/requests-YYYYMMDD.jsonl dosyasına yazar.
//
// Tasarım kuralları:
//   - Hot-path asla bloklanmaz: kanal dolarsa satır düşülür (dropped sayacı).
//   - Gün değişince dosya yeniden açılır (requests-<gun>.jsonl).
//   - Yan olaylar (token refresh, store overwrite...) aynı kanala kind ile gider.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RequestLogEntry bir isteğin bitiminde yazılan kalıcı satır.
// cached==0 satırlarında LastUsage (son usageMetadata ham hali) saklanır;
// böylece "kayıp 0" (STOP chunk'ı gelmedi) ile "gerçek 0" ayrıştırılabilir.
type RequestLogEntry struct {
	TS         string `json:"ts"` // RFC3339Nano
	ReqID      string `json:"req_id"`
	UpReqID    string `json:"up_req_id,omitempty"` // protocol.go upstream requestId
	SessionID  string `json:"session_id,omitempty"`
	PID        int    `json:"pid"`
	PIDZero    bool   `json:"pid_zero,omitempty"`
	Account    string `json:"account,omitempty"`
	ProxyID    string `json:"proxy_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Status     string `json:"status"` // completed | error | truncated
	Endpoint   string `json:"endpoint"`
	DurationMs int64  `json:"duration_ms"`

	Prompt int `json:"prompt"`
	Cached int `json:"cached"`
	Total  int `json:"total"`
	Output int `json:"output"`

	// generationConfig kırılganlığı ölçümü (B4)
	Temp      float64 `json:"temp,omitempty"`
	TopP      float64 `json:"top_p,omitempty"`
	MaxTokens int     `json:"max_tokens,omitempty"`
	ToolsHash string  `json:"tools_hash,omitempty"` // sha256(marshal(tools))
	SysHash   string  `json:"sys_hash,omitempty"`   // sha256(systemInstruction)

	// bağlantı ölçümü (A2 httptrace)
	ConnReused    bool   `json:"conn_reused,omitempty"`
	ConnWaitMs    int64  `json:"conn_wait_ms,omitempty"` // istek→GotConn (yeniden kullanım gecikmesi)
	UpstreamTrace string `json:"up_trace,omitempty"`     // x-request-id / traceId

	LastUsage json.RawMessage `json:"last_usage,omitempty"` // cached==0 iken ham usageMetadata
	ErrorMsg  string          `json:"error,omitempty"`
}

// StoreLogEntry yan olay satırı (kind ile).
type StoreLogEntry struct {
	TS     string                 `json:"ts"`
	Kind   string                 `json:"kind"` // token_refresh | store_overwrite | store_corrupt | media_stale | rescue | ...
	Fields map[string]interface{} `json:"fields"`
}

type RequestLogger struct {
	mu      sync.Mutex
	file    *os.File
	curDay  string
	dir     string
	ch      chan []byte
	dropped uint64
}

var GlobalReqLog = NewRequestLogger("logs")

func NewRequestLogger(dir string) *RequestLogger {
	rl := &RequestLogger{
		dir: dir,
		ch:  make(chan []byte, 1000),
	}
	go rl.loop()
	return rl
}

// Log bir istek satırını kuyruğa alır. Asla bloklamaz.
func (rl *RequestLogger) Log(e RequestLogEntry) {
	if e.TS == "" {
		e.TS = time.Now().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	select {
	case rl.ch <- raw:
	default:
		rl.incDropped()
	}
}

// LogEvent yan olay (token refresh / store çakışması / media stale / rescue) yazar.
func (rl *RequestLogger) LogEvent(kind string, fields map[string]interface{}) {
	e := StoreLogEntry{
		TS:     time.Now().Format(time.RFC3339Nano),
		Kind:   kind,
		Fields: fields,
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	select {
	case rl.ch <- raw:
	default:
		rl.incDropped()
	}
}

func (rl *RequestLogger) incDropped() {
	rl.mu.Lock()
	rl.dropped++
	rl.mu.Unlock()
}

func (rl *RequestLogger) loop() {
	for raw := range rl.ch {
		rl.mu.Lock()
		if err := rl.ensureFileLocked(); err != nil {
			rl.mu.Unlock()
			// Dosya açılamıyorsa sessizce atla ama bir kez duyur.
			log.Printf("[ReqLog] dosya açılamadı: %v", err)
			continue
		}
		_, _ = rl.file.Write(append(raw, '\n'))
		rl.mu.Unlock()
	}
}

func (rl *RequestLogger) ensureFileLocked() error {
	day := time.Now().Format("20060102")
	if rl.file != nil && rl.curDay == day {
		return nil
	}
	if rl.file != nil {
		_ = rl.file.Close()
		rl.file = nil
	}
	if err := os.MkdirAll(rl.dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(rl.dir, "requests-"+day+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	rl.file = f
	rl.curDay = day
	return nil
}

// Dropped, kuyruk doluğu için düşürülen satır sayısını döner (test/monitoring).
func (rl *RequestLogger) Dropped() uint64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.dropped
}

// hashOf, payload alanlarının istekler arası değişip değişmediğini ölçmek için
// (tools/systemInstruction) 12 karakterlik sha256 üretir.
func hashOf(v interface{}) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// newReqLogBase, altı handler çağrısının ortak alanlarını kurar.
// payload nil olabilir (dönüşüm öncesi hata vb.).
func newReqLogBase(reqID string, payload *GeminiAipPayload, pid int, targetEmail, targetModel, effort, endpoint string, reqStart time.Time) RequestLogEntry {
	e := RequestLogEntry{
		ReqID:      reqID,
		PID:        pid,
		PIDZero:    pid <= 0,
		Account:    targetEmail,
		Model:      targetModel,
		Effort:     effort,
		Endpoint:   endpoint,
		DurationMs: time.Since(reqStart).Milliseconds(),
	}
	if payload != nil {
		e.UpReqID = payload.RequestID
		e.SessionID = payload.Request.SessionID
		e.Temp = payload.Request.GenerationConfig.Temperature
		e.TopP = payload.Request.GenerationConfig.TopP
		e.MaxTokens = payload.Request.GenerationConfig.MaxOutputTokens
		e.ToolsHash = hashOf(payload.Request.Tools)
		e.SysHash = hashOf(payload.Request.SystemInstruction)
	}
	// Proxy kırılımı (miss analizinde proxy değişimleri görünür olsun)
	if targetEmail != "" && GlobalAccountStore != nil {
		if a := GlobalAccountStore.GetAccountByID(targetEmail); a != nil {
			e.ProxyID = a.ProxyID
		}
	}
	return e
}

// tokHash, token değişim olaylarında token'ın kendisini yazmadan
// 12 karakterlik sha256 ön eki ile korelasyon sağlar.
func tokHash(t string) string {
	if t == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])[:12]
}

// attachConn, bağlantı ölçümünü istek satırına aktarır.
// trace boşsa yanıt header'ındaki x-request-id kullanılır.
func attachConn(e *RequestLogEntry, ci *ConnInfo, trace string) {
	if ci == nil {
		return
	}
	e.ConnReused = ci.Reused
	e.ConnWaitMs = ci.WaitMs
	if trace != "" {
		e.UpstreamTrace = trace
	} else {
		e.UpstreamTrace = ci.UpstreamTrace
	}
}
