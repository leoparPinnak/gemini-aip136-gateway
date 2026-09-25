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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
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
	PIDZero    bool   `json:"pid_zero"` // pid<=0 sabit oturum anahtarı kullanıldı mı (her zaman yazılır)
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
	ConnReused    bool   `json:"conn_reused"`     // false = yeni bağlantı, true = havuz yeniden kullanımı (her zaman yazılır)
	ConnWaitMs    int64  `json:"conn_wait_ms"`    // istek→GotConn (yeniden kullanım gecikmesi; 0 = beklenmedi)
	UpstreamTrace string `json:"up_trace,omitempty"`     // x-request-id / traceId

	LastUsage json.RawMessage `json:"last_usage,omitempty"` // cached==0 iken ham usageMetadata
	ErrorMsg  string          `json:"error,omitempty"`

	// Hibrit içerik loglama (kullanıcı onayı):
	// manifest — her istekte ~1KB: parça bazlı rol/tip/uzunluk/sha256.
	// Prefix diff'i (miss kök-nedeni) manifest ile çözülür.
	ContentManifest []ContentManifestItem `json:"content_manifest,omitempty"`

	// unexported: miss anında tam dump için (Log() hook'u kullanır)
	rawBody    []byte
	payload    *GeminiAipPayload
	upHeaders  map[string]string
}

// ContentManifestItem gönderilen Gemini şablonunun parça özeti.
type ContentManifestItem struct {
	Role string `json:"r,omitempty"` // user | model
	Type string `json:"t"`           // text | media | fc | fr | other
	Len  int    `json:"n"`           // karakter / base64 uzunluğu / JSON baytı
	Sha  string `json:"h"`           // sha256 ilk 12 hex → prefix diff anahtarı
	Mime string `json:"mime,omitempty"`
}

// BuildContentManifest upstream'e giden şablonun parça listesini özetler.
func BuildContentManifest(p *GeminiAipPayload) []ContentManifestItem {
	if p == nil {
		return nil
	}
	var m []ContentManifestItem
	for _, c := range p.Request.Contents {
		for _, part := range c.Parts {
			item := ContentManifestItem{Role: c.Role}
			switch {
			case part.Text != "":
				item.Type = "text"
				item.Len = len(part.Text)
				item.Sha = hash12(part.Text)
			case part.InlineData != nil:
				item.Type = "media"
				item.Mime = part.InlineData.MimeType
				item.Len = len(part.InlineData.Data)
				item.Sha = hash12(part.InlineData.Data)
			case part.FileData != nil:
				item.Type = "media"
				item.Mime = part.FileData.MimeType
				item.Len = len(part.FileData.FileURI)
				item.Sha = hash12(part.FileData.FileURI)
			case part.FunctionCall != nil:
				item.Type = "fc"
				b, _ := json.Marshal(part.FunctionCall)
				item.Len = len(b)
				item.Sha = hash12(string(b))
			case part.FunctionResponse != nil:
				item.Type = "fr"
				b, _ := json.Marshal(part.FunctionResponse)
				item.Len = len(b)
				item.Sha = hash12(string(b))
			default:
				item.Type = "other"
				b, _ := json.Marshal(part)
				item.Len = len(b)
				item.Sha = hash12(string(b))
			}
			m = append(m, item)
		}
	}
	return m
}

func hash12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// payloadDumpMinPrompt: yalnız cache'lenebilir (>=4096) miss'ler dump edilir.
const payloadDumpMinPrompt = 4096

// b64Pattern uzun base64 gömülerini (medya) bulur; dump'ta hash+uzunlukla
// değiştirilir → dosya boyutu sınırlı kalır, içerik yine doğrulanabilir.
var b64Pattern = regexp.MustCompile(`[A-Za-z0-9+/]{400,}={0,2}`)

func shortB64(b []byte) string {
	return b64Pattern.ReplaceAllStringFunc(string(b), func(m string) string {
		sum := sha256.Sum256([]byte(m))
		return fmt.Sprintf("<b64:len=%d:sha256=%s>", len(m), hex.EncodeToString(sum[:])[:12])
	})
}

// dumpPayloadOnMiss, cache'lenebilir ama cache'e ulaşamayan isteğin
// gelen body'sini + normalize Gemini şablonunu (base64 kısaltılmış) yazar:
// logs/payloads-YYYYMMDD/<req_id>.json
func dumpPayloadOnMiss(e *RequestLogEntry) {
	if e.payload == nil || e.Cached > 0 || e.Prompt < payloadDumpMinPrompt {
		return
	}
	dir := filepath.Join(GlobalReqLog.dir, "payloads-"+time.Now().Format("20060102"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	upRaw, err := json.Marshal(e.payload)
	if err != nil {
		upRaw = []byte(`"marshal_error"`)
	}
	doc := map[string]interface{}{
		"ts": e.TS, "req_id": e.ReqID, "endpoint": e.Endpoint, "model": e.Model,
		"session_id": e.SessionID, "pid": e.PID, "status": e.Status,
		"prompt": e.Prompt, "cached": e.Cached, "sys_hash": e.SysHash, "tools_hash": e.ToolsHash,
		"up_trace": e.UpstreamTrace, "manifest": e.ContentManifest,
		"upstream_headers":     e.upHeaders,
		"raw_body":             shortB64(e.rawBody),
		"upstream_payload":     shortB64(upRaw),
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return
	}
	name := e.ReqID
	if name == "" {
		name = fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0644); err == nil {
		GlobalReqLog.LogEvent("payload_dump", map[string]interface{}{
			"req_id": e.ReqID, "prompt": e.Prompt, "file": filepath.Join("payloads-"+time.Now().Format("20060102"), name+".json"),
		})
	}
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
	// Hibrit içerik loglama: yalnız cache'lenebilir miss'lerde tam dump.
	dumpPayloadOnMiss(&e)
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
func newReqLogBase(reqID string, payload *GeminiAipPayload, body []byte, pid int, targetEmail, targetModel, effort, endpoint string, reqStart time.Time) RequestLogEntry {
	e := RequestLogEntry{
		ReqID:      reqID,
		PID:        pid,
		PIDZero:    pid <= 0,
		Account:    targetEmail,
		Model:      targetModel,
		Effort:     effort,
		Endpoint:   endpoint,
		DurationMs: time.Since(reqStart).Milliseconds(),
		rawBody:    body,
		payload:    payload,
	}
	if payload != nil {
		e.UpReqID = payload.RequestID
		e.SessionID = payload.Request.SessionID
		e.Temp = payload.Request.GenerationConfig.Temperature
		e.TopP = payload.Request.GenerationConfig.TopP
		e.MaxTokens = payload.Request.GenerationConfig.MaxOutputTokens
		e.ToolsHash = hashOf(payload.Request.Tools)
		e.SysHash = hashOf(payload.Request.SystemInstruction)
		e.ContentManifest = BuildContentManifest(payload)
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
	e.upHeaders = ci.Headers
	if trace != "" {
		e.UpstreamTrace = trace
	} else {
		e.UpstreamTrace = ci.UpstreamTrace
	}
}
