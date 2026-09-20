package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ProxyConfig struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Protocol    string `json:"protocol"`
	Host        string `json:"host"`
	Username    string `json:"username"`
	Status      string `json:"status"` // "online", "offline", "testing"
	LatencyMs   int64  `json:"latency_ms"`
	PublicIP    string `json:"public_ip"`
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	City        string `json:"city"`
	FlagEmoji   string `json:"flag_emoji"`
	LastChecked int64  `json:"last_checked"`
	ErrorMsg    string `json:"error_msg,omitempty"`
}

func countryCodeToFlag(countryCode string) string {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if len(countryCode) != 2 {
		return "🌐"
	}
	r1 := rune(0x1F1E6 + int(countryCode[0]-'A'))
	r2 := rune(0x1F1E6 + int(countryCode[1]-'A'))
	return string([]rune{r1, r2})
}

func (p *ProxyConfig) Test() error {
	p.Status = "testing"
	p.ErrorMsg = ""

	parsedURL, err := url.Parse(p.URL)
	if err != nil {
		p.Status = "offline"
		p.ErrorMsg = fmt.Sprintf("Geçersiz URL: %v", err)
		p.LastChecked = time.Now().Unix()
		return err
	}

	p.Protocol = parsedURL.Scheme
	p.Host = parsedURL.Host
	if parsedURL.User != nil {
		p.Username = parsedURL.User.Username()
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableKeepAlives: true,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	start := time.Now()
	// Test via ip-api.com
	resp, err := client.Get("http://ip-api.com/json")
	if err != nil {
		p.Status = "offline"
		p.LatencyMs = 0
		p.ErrorMsg = fmt.Sprintf("Bağlantı hatası: %v", err)
		p.LastChecked = time.Now().Unix()
		return err
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()
	p.LatencyMs = latency

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		p.Status = "offline"
		p.ErrorMsg = fmt.Sprintf("Yanıt okunamadı: %v", err)
		p.LastChecked = time.Now().Unix()
		return err
	}

	var geo struct {
		Status      string `json:"status"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
		Query       string `json:"query"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(bodyBytes, &geo); err == nil && geo.Status == "success" {
		p.Status = "online"
		p.PublicIP = geo.Query
		p.CountryCode = geo.CountryCode
		p.CountryName = geo.Country
		p.City = geo.City
		p.FlagEmoji = countryCodeToFlag(geo.CountryCode)
		p.ErrorMsg = ""
	} else {
		p.Status = "online"
		p.PublicIP = geo.Query
		if p.PublicIP == "" {
			p.PublicIP = parsedURL.Hostname()
		}
		if p.FlagEmoji == "" {
			p.FlagEmoji = "🌐"
		}
	}

	p.LastChecked = time.Now().Unix()
	return nil
}

type ProxyManager struct {
	mu       sync.RWMutex
	proxies  map[string]*ProxyConfig
	filePath string
}

var GlobalProxyManager *ProxyManager

func InitProxyManager(baseDir string) (*ProxyManager, error) {
	filePath := filepath.Join(baseDir, "proxies.json")
	mgr := &ProxyManager{
		proxies:  make(map[string]*ProxyConfig),
		filePath: filePath,
	}

	if data, err := os.ReadFile(filePath); err == nil && len(data) > 0 {
		var list []*ProxyConfig
		if err := json.Unmarshal(data, &list); err == nil {
			for _, p := range list {
				mgr.proxies[p.ID] = p
			}
		}
	}

	GlobalProxyManager = mgr

	// Test all proxies in background at startup
	go mgr.TestAll()

	// Periodic background check every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			mgr.TestAll()
		}
	}()

	return mgr, nil
}

func (m *ProxyManager) saveLocked() error {
	var list []*ProxyConfig
	for _, p := range m.proxies {
		list = append(list, p)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0600)
}

func (m *ProxyManager) AddProxy(rawURL string) (*ProxyConfig, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("proxy adresi boş olamaz")
	}

	// Default to socks5:// if scheme is missing
	if !strings.Contains(rawURL, "://") {
		rawURL = "socks5://" + rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" {
		return nil, fmt.Errorf("geçersiz proxy adresi: %v", err)
	}

	m.mu.Lock()
	// Check for duplicate URL
	for _, existing := range m.proxies {
		if existing.URL == rawURL {
			m.mu.Unlock()
			// Retest and return
			_ = existing.Test()
			m.mu.Lock()
			_ = m.saveLocked()
			m.mu.Unlock()
			return existing, nil
		}
	}

	id := fmt.Sprintf("prx_%d", time.Now().UnixNano()/1e6)
	p := &ProxyConfig{
		ID:        id,
		URL:       rawURL,
		Protocol:  parsedURL.Scheme,
		Host:      parsedURL.Host,
		Status:    "testing",
		FlagEmoji: "🌐",
	}
	if parsedURL.User != nil {
		p.Username = parsedURL.User.Username()
	}
	m.proxies[id] = p
	_ = m.saveLocked()
	m.mu.Unlock()

	// Test immediately
	go func() {
		_ = p.Test()
		m.mu.Lock()
		_ = m.saveLocked()
		m.mu.Unlock()
		BroadcastProxyChange()
	}()

	return p, nil
}

func (m *ProxyManager) DeleteProxy(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.proxies[id]; !ok {
		return fmt.Errorf("proxy bulunamadı: %s", id)
	}
	delete(m.proxies, id)

	// Also remove proxy_id from any account using it
	if GlobalAccountStore != nil {
		GlobalAccountStore.mu.Lock()
		for _, acc := range GlobalAccountStore.Accounts {
			if acc.ProxyID == id {
				acc.ProxyID = ""
			}
		}
		_ = GlobalAccountStore.saveLocked()
		GlobalAccountStore.mu.Unlock()
		BroadcastAccountChange()
	}

	err := m.saveLocked()
	BroadcastProxyChange()
	return err
}

func (m *ProxyManager) TestProxy(id string) (*ProxyConfig, error) {
	m.mu.RLock()
	p, ok := m.proxies[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("proxy bulunamadı: %s", id)
	}

	err := p.Test()
	m.mu.Lock()
	_ = m.saveLocked()
	m.mu.Unlock()

	BroadcastProxyChange()
	return p, err
}

func (m *ProxyManager) TestAll() {
	m.mu.RLock()
	var list []*ProxyConfig
	for _, p := range m.proxies {
		list = append(list, p)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, p := range list {
		wg.Add(1)
		go func(target *ProxyConfig) {
			defer wg.Done()
			_ = target.Test()
		}(p)
	}
	wg.Wait()

	m.mu.Lock()
	_ = m.saveLocked()
	m.mu.Unlock()

	BroadcastProxyChange()
}

func (m *ProxyManager) GetAll() []*ProxyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []*ProxyConfig
	for _, p := range m.proxies {
		list = append(list, p)
	}
	return list
}

func (m *ProxyManager) GetProxy(id string) *ProxyConfig {
	if id == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.proxies[id]
}

func (m *ProxyManager) GetHttpClientForProxy(proxyID string, timeout time.Duration) *http.Client {
	if proxyID == "" {
		return &http.Client{Timeout: timeout}
	}
	m.mu.RLock()
	p, ok := m.proxies[proxyID]
	m.mu.RUnlock()
	if !ok || p.URL == "" {
		return &http.Client{Timeout: timeout}
	}

	parsedURL, err := url.Parse(p.URL)
	if err != nil {
		log.Printf("[ProxyManager] Geçersiz proxy URL'si (%s): %v", p.URL, err)
		return &http.Client{Timeout: timeout}
	}

	tr := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
}
