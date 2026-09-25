package main

// cache_sprint_test.go — Cache-hit sprint (Faz 0+1) regresyon testleri.
//
// Kapsadıkları:
//   - D-1 Determinizm: aynı body ×2 → byte-identik payload (map sıralama riski)
//   - B1   pid<=0 tek sabit SessionID; pid>0 sabit; sessions.json roundtrip
//   - B2   thought_store: alias yok, twin write-once, ilk-değer-korunur,
//          async atomik kayıt, bozuk load, TTL
//   - A3   ws_hub: terminal satır geç running ile ezilemez
//   - C1   proxy transport havuzu: aynı proxyID → aynı transport, URL değişimi → yeni
//   - B3   medya: deterministik base64, stale fallback, hiç yoksa hata

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// D-1: Determinizm regresyonu
// ---------------------------------------------------------------------------

func TestDeterministicMarshal(t *testing.T) {
	req := `{
		"model": "gemini-3.8-flash-medium",
		"messages": [
			{"role": "system", "content": "Sen yardımcı bir asistansın."},
			{"role": "user", "content": "Merhaba, nasılsın?"},
			{"role": "assistant", "content": "İyiyim, size nasıl yardımcı olabilirim?"},
			{"role": "user", "content": [
				{"type": "text", "text": "Bir görev yap ve sonucu ver"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo="}}
			]}
		],
		"tools": [
			{"type": "function", "function": {"name": "run", "description": "çalıştır", "parameters": {"type": "object", "properties": {"cmd": {"type": "string"}, "args": {"type": "array", "items": {"type": "string"}}}, "required": ["cmd"]}}},
			{"type": "function", "function": {"name": "read", "description": "oku", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}}
		],
		"temperature": 0.7,
		"top_p": 0.95,
		"max_tokens": 2048
	}`

	p1, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(req), "")
	if err != nil {
		t.Fatalf("1. dönüşüm hatası: %v", err)
	}
	p2, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(req), "")
	if err != nil {
		t.Fatalf("2. dönüşüm hatası: %v", err)
	}

	// RequestID zaman+rand içerir (upstream correlation) — determinizm dışıdır.
	p1.RequestID = ""
	p2.RequestID = ""

	b1, err := json.Marshal(p1)
	if err != nil {
		t.Fatalf("marshal 1: %v", err)
	}
	b2, err := json.Marshal(p2)
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("DETERMİNİZM BOZUK: iki marshal farklı!\n1: %s\n2: %s", b1, b2)
	}
}

// ---------------------------------------------------------------------------
// B1: sabit SessionID
// ---------------------------------------------------------------------------

func TestStableSessionForZeroPID(t *testing.T) {
	s1 := getStealthSessionID(0, "")
	s2 := getStealthSessionID(0, "")
	if s1 != s2 {
		t.Errorf("pid=0 iki çağrıda farklı SID üretmemeli: %q vs %q", s1, s2)
	}
	s3 := getStealthSessionID(-1, "")
	if s3 != s1 {
		t.Errorf("tüm pid<=0 tek sabit oturumda olmalı: %q vs %q", s3, s1)
	}
	other := getStealthSessionID(777123456, "")
	if other == s1 {
		t.Errorf("farklı pid farklı SID almalı")
	}
	// customSessionID her zaman kazanır
	if got := getStealthSessionID(0, "custom-sid"); got != "custom-sid" {
		t.Errorf("custom session id yok sayıldı: %q", got)
	}
}

func TestSessionPersistRoundtrip(t *testing.T) {
	t.Chdir(t.TempDir()) // sessions.json geçici dizine yazılsın

	stealthSessionMutex.Lock()
	orig := make(map[int]string, len(stealthSessionMap))
	for k, v := range stealthSessionMap {
		orig[k] = v
	}
	stealthSessionMap = map[int]string{
		0:  "-1111111111111111111",
		42: "-2222222222222222222",
	}
	stealthSessionMutex.Unlock()

	saveSessions()

	stealthSessionMutex.Lock()
	stealthSessionMap = map[int]string{}
	stealthSessionMutex.Unlock()

	loadStealthSessions()

	stealthSessionMutex.Lock()
	g0 := stealthSessionMap[0]
	g42 := stealthSessionMap[42]
	stealthSessionMutex.Unlock()

	if g0 != "-1111111111111111111" || g42 != "-2222222222222222222" {
		t.Errorf("roundtrip başarısız: 0=%q 42=%q", g0, g42)
	}

	// Bozuk dosya → yok sayılır, panic yok
	if err := os.WriteFile("sessions.json", []byte("{bozuk"), 0644); err == nil {
		loadStealthSessions() // sessizce atlamalı
		stealthSessionMutex.Lock()
		g0b := stealthSessionMap[0]
		stealthSessionMutex.Unlock()
		if g0b != "-1111111111111111111" {
			t.Errorf("bozuk dosya mevcut kayıtları bozmamalı, 0=%q", g0b)
		}
	}

	// Orijinal haritayı geri koy (diğer testler etkilenmesin)
	stealthSessionMutex.Lock()
	stealthSessionMap = orig
	stealthSessionMutex.Unlock()
}

// ---------------------------------------------------------------------------
// B2: thought_store
// ---------------------------------------------------------------------------

func TestThoughtStoreAliasRemovedFromGet(t *testing.T) {
	s := NewThoughtStore(filepath.Join(t.TempDir(), "ts.json"))
	s.Store("call1", "pwsh", "sigA")
	s.Store("call2", "pwsh", "sigB")

	if got := s.Get("unknown_call", "pwsh"); got != "" {
		t.Errorf("global ':tool' aliası Get'den kaldırılmalı, got %q", got)
	}
	if got := s.Get("call1", "pwsh"); got != "sigA" {
		t.Errorf("exact arama bozuldu: %q", got)
	}
	if got := s.Get("", ""); got != "" {
		t.Errorf("boş anahtarla çağrı boş dönmeli, got %q", got)
	}
}

func TestThoughtStoreTwinWriteOnce(t *testing.T) {
	s := NewThoughtStore(filepath.Join(t.TempDir(), "ts.json"))
	s.Store("c1", "toolA", "sigOne")
	s.Store("c1", "toolB", "sigTwo") // farklı araç, aynı callId → twin ezilmemeli

	if got := s.Get("c1", ""); got != "sigOne" {
		t.Errorf("twin (callId:) ilk değeri korumalı, got %q", got)
	}
	if got := s.Get("c1", "toolA"); got != "sigOne" {
		t.Errorf("exact toolA bozuldu: %q", got)
	}
	if got := s.Get("c1", "toolB"); got != "sigTwo" {
		t.Errorf("exact toolB bozuldu: %q", got)
	}
}

func TestThoughtStoreExactOverwriteKeepsFirst(t *testing.T) {
	s := NewThoughtStore(filepath.Join(t.TempDir(), "ts.json"))
	s.Store("c2", "toolX", "first_sig")
	s.Store("c2", "toolX", "second_sig") // çakışma → ilk değer korunur + uyarı

	if got := s.Get("c2", "toolX"); got != "first_sig" {
		t.Errorf("ilk-değer-korunur kuralı bozuldu: %q", got)
	}
}

func TestThoughtStoreAsyncAtomicSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ts.json")
	s := NewThoughtStore(path)
	s.Store("ca", "tb", "sig1")

	// debounce 2s + yazım → en fazla 6s bekle
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dosya yazılmadı: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp dosyası kalmış (atomik yazım ihlali)")
	}

	// Yeniden yükle → veri kaybolmamış
	s2 := NewThoughtStore(path)
	if got := s2.Get("ca", "tb"); got != "sig1" {
		t.Errorf("yeniden yüklemede veri kaybı: %q", got)
	}
}

func TestThoughtStoreCorruptLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ts.json")
	if err := os.WriteFile(path, []byte("{bu bir json değil"), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewThoughtStore(path)
	if got := s.Get("x", "y"); got != "" {
		t.Errorf("bozuk load sonrası depo boş başlamalı, got %q", got)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) == 0 {
		t.Errorf("bozuk dosya .corrupt-<ts> olarak kenara alınmalı")
	}
}

func TestThoughtStoreTTLCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ts.json")
	old := time.Now().Add(-31 * 24 * time.Hour)
	fresh := time.Now()
	data, _ := json.Marshal(map[string]ThoughtItem{
		"old:tool": {Signature: "old_sig", Timestamp: old},
		"new:tool": {Signature: "new_sig", Timestamp: fresh},
	})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	s := NewThoughtStore(path)
	if got := s.Get("old", "tool"); got != "" {
		t.Errorf("31 günlük giriş TTL ile düşmeli, got %q", got)
	}
	if got := s.Get("new", "tool"); got != "new_sig" {
		t.Errorf("taze giriş kalmalı, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// A3: ws_hub yarış guard'ı
// ---------------------------------------------------------------------------

func TestCompletedNotOverwrittenByRunning(t *testing.T) {
	h := &WSHub{recentEvents: make([]LiveRequestInfo, 0, 10)}
	h.AddRecentRequest(LiveRequestInfo{ID: "req1", Status: "running"})
	h.AddRecentRequest(LiveRequestInfo{ID: "req1", Status: "completed", CachedTokens: 42})
	h.AddRecentRequest(LiveRequestInfo{ID: "req1", Status: "running"}) // geç running

	got := h.GetRecentRequests()
	if len(got) != 1 {
		t.Fatalf("satır sayısı bozuldu: %d", len(got))
	}
	if got[0].Status != "completed" || got[0].CachedTokens != 42 {
		t.Errorf("terminal satır geç running ile EZİLDİ: %+v", got[0])
	}

	// Terminal → error güncellemesi hâlâ serbest (running hariç)
	h.AddRecentRequest(LiveRequestInfo{ID: "req1", Status: "error", ErrorMsg: "son"})
	got = h.GetRecentRequests()
	if got[0].Status != "error" {
		t.Errorf("terminal durum error'a geçebilmeli: %+v", got[0])
	}
}

// ---------------------------------------------------------------------------
// C1: proxy transport havuzu
// ---------------------------------------------------------------------------

func TestProxyTransportReuse(t *testing.T) {
	m := &ProxyManager{
		proxies: map[string]*ProxyConfig{
			"p1": {ID: "p1", URL: "http://127.0.0.1:19999"},
		},
		transports: make(map[string]*proxyTransport),
	}

	c1 := m.GetHttpClientForProxy("p1", time.Minute)
	c2 := m.GetHttpClientForProxy("p1", 5*time.Second) // timeout farklı
	tr1, ok1 := c1.Transport.(*http.Transport)
	tr2, ok2 := c2.Transport.(*http.Transport)
	if !ok1 || !ok2 {
		t.Fatalf("transport tipi hatalı")
	}
	if tr1 != tr2 {
		t.Errorf("aynı proxyID için transport YENİDEN KULLANILMALI (yeni handshake yok)")
	}
	if c1.Timeout == c2.Timeout {
		t.Errorf("timeout çağrıya özel kalmalı")
	}

	// URL değişimi → yeni transport
	m.mu.Lock()
	m.proxies["p1"].URL = "http://127.0.0.1:19998"
	m.mu.Unlock()
	c3 := m.GetHttpClientForProxy("p1", time.Minute)
	tr3, _ := c3.Transport.(*http.Transport)
	if tr3 == tr1 {
		t.Errorf("proxy URL değişince yeni transport üretilmeli")
	}

	// Bilinmeyen proxy → basit client
	c4 := m.GetHttpClientForProxy("yok", time.Second)
	if _, isTr := c4.Transport.(*http.Transport); isTr {
		t.Errorf("bilinmeyen proxy havuza düşmemeli")
	}
}

// ---------------------------------------------------------------------------
// B3: medya kararlılığı
// ---------------------------------------------------------------------------

func TestMediaDeterministicStaleAndError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3, 4}
	if err := os.WriteFile(path, png, 0644); err != nil {
		t.Fatal(err)
	}

	in1, err := parseDataURLOrMedia(path, "")
	if err != nil {
		t.Fatalf("1. okuma: %v", err)
	}
	in2, err := parseDataURLOrMedia(path, "")
	if err != nil {
		t.Fatalf("2. okuma: %v", err)
	}
	if in1.Data != in2.Data || in1.MimeType != in2.MimeType {
		t.Errorf("aynı dosya farklı base64/mime üretmemeli")
	}

	// Dosya silindi → SON İYİ DEĞER (aynı bayt) döner, hata değil
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	in3, err := parseDataURLOrMedia(path, "")
	if err != nil {
		t.Errorf("stale fallback hataya çevrilmemeli: %v", err)
	} else if in3.Data != in1.Data {
		t.Errorf("stale değer bayt-değişmedi demeli")
	}

	// Hiç alınmamış medya → DÜRÜST HATA (sessiz nil yok)
	_, err = parseDataURLOrMedia(filepath.Join(dir, "never-seen.png"), "")
	if err == nil {
		t.Errorf("hiç alınmamış medya HATA dönmeli (B3 fail-fast)")
	}
}
