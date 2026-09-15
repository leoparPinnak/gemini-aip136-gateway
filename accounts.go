package main

import (
	"bytes"
	"encoding/base64"
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
	"syscall"
	"time"
	"unsafe"
)

type QuotaBucket struct {
	BucketID          string  `json:"bucket_id"`
	DisplayName       string  `json:"display_name"`
	Window            string  `json:"window"` // "5h", "weekly"
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetTime         string  `json:"reset_time"`
	Description       string  `json:"description"`
}

type AccountQuota struct {
	Gemini5h     *QuotaBucket `json:"gemini_5h,omitempty"`
	GeminiWeekly *QuotaBucket `json:"gemini_weekly,omitempty"`
	ClaudeWeekly *QuotaBucket `json:"claude_weekly,omitempty"`
	LastUpdated  int64        `json:"last_updated"`
}

type Account struct {
	ID           string        `json:"id"`
	Email        string        `json:"email"`
	Name         string        `json:"name"`
	Picture      string        `json:"picture"`
	RefreshToken string        `json:"refresh_token"`
	AccessToken  string        `json:"access_token"`
	Expiry       string        `json:"expiry"`
	IsActive     bool          `json:"is_active"`
	PlanType     string        `json:"plan_type"` // "PRO"
	Quota        *AccountQuota `json:"quota,omitempty"`
	LastChecked  int64         `json:"last_checked"`
}

type AccountStore struct {
	Accounts []*Account `json:"accounts"`
	mu       sync.RWMutex
	filePath string
}

var GlobalAccountStore *AccountStore

func InitAccountStore() error {
	execDir, err := os.Getwd()
	if err != nil {
		execDir = "."
	}
	filePath := filepath.Join(execDir, "accounts.json")

	store := &AccountStore{
		Accounts: make([]*Account, 0),
		filePath: filePath,
	}
	GlobalAccountStore = store

	// accounts.json varsa yükle
	if data, err := os.ReadFile(filePath); err == nil {
		if err := json.Unmarshal(data, &store.Accounts); err == nil && len(store.Accounts) > 0 {
			log.Printf("[AccountStore] %d hesap accounts.json dosyasından yüklendi.", len(store.Accounts))
			// Aktif hesabı kontrol et, yoksa ilkini aktif yap
			hasActive := false
			for _, acc := range store.Accounts {
				if acc.IsActive {
					hasActive = true
					break
				}
			}
			if !hasActive && len(store.Accounts) > 0 {
				store.Accounts[0].IsActive = true
			}
			// Arka planda kotaları güncelle
			go store.RefreshAllQuotas()
			return nil
		}
	}

	// accounts.json yoksa, Windows Credential Manager'dan ilk hesabı içe aktar
	log.Printf("[AccountStore] accounts.json bulunamadı. Windows Credential Manager taranıyor...")
	acc, err := store.importFromWindowsCredential("acc-1", true)
	if err == nil && acc != nil {
		store.Accounts = append(store.Accounts, acc)
		_ = store.saveLocked()
		log.Printf("[AccountStore] İlk Pro hesap başarıyla içe aktarıldı: %s (%s)", acc.Name, acc.Email)
		go store.RefreshAccountQuota(acc.ID)
	} else {
		log.Printf("[AccountStore] Windows Credential içe aktarılamadı: %v", err)
	}

	return nil
}

func decodeIDTokenClaims(idToken string) (email, name, picture string) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", "", ""
	}
	payload := parts[1]
	// Base64 URL padding
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", ""
		}
	}
	var claims struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	_ = json.Unmarshal(data, &claims)
	return claims.Email, claims.Name, claims.Picture
}

func (s *AccountStore) importFromWindowsCredential(accountID string, makeActive bool) (*Account, error) {
	tok, err := GlobalAuth.readWindowsCredential()
	if err != nil {
		return nil, fmt.Errorf("Windows Credential okunamadı: %w", err)
	}

	email := "Google Pro Kullanıcısı"
	name := "Gemini Pro Hesabı"
	picture := ""

	// Eğer id_token varsa çöz
	targetUTF16, _ := syscall.UTF16PtrFromString("gemini:antigravity")
	var pCred *CREDENTIALW
	r1, _, _ := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetUTF16)),
		1,
		0,
		uintptr(unsafe.Pointer(&pCred)),
	)
	if r1 != 0 && pCred != nil && pCred.CredentialBlob != nil {
		defer procCredFree.Call(uintptr(unsafe.Pointer(pCred)))
		blobBytes := unsafe.Slice(pCred.CredentialBlob, pCred.CredentialBlobSize)
		var fullBlob struct {
			IDToken string `json:"id_token"`
		}
		if json.Unmarshal(blobBytes, &fullBlob) == nil && fullBlob.IDToken != "" {
			e, n, p := decodeIDTokenClaims(fullBlob.IDToken)
			if e != "" {
				email = e
			}
			if n != "" {
				name = n
			}
			if p != "" {
				picture = p
			}
		}
	}

	acc := &Account{
		ID:           accountID,
		Email:        email,
		Name:         name,
		Picture:      picture,
		RefreshToken: tok.RefreshToken,
		AccessToken:  tok.AccessToken,
		Expiry:       tok.Expiry,
		IsActive:     makeActive,
		PlanType:     "PRO",
		LastChecked:  time.Now().Unix(),
	}

	return acc, nil
}

func (s *AccountStore) saveLocked() error {
	data, err := json.MarshalIndent(s.Accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *AccountStore) GetAllAccounts() []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Account, len(s.Accounts))
	copy(result, s.Accounts)
	return result
}

func (s *AccountStore) GetActiveAccount() *Account {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, acc := range s.Accounts {
		if acc.IsActive {
			return acc
		}
	}
	if len(s.Accounts) > 0 {
		return s.Accounts[0]
	}
	return nil
}

func (s *AccountStore) SetActiveAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	for _, acc := range s.Accounts {
		if acc.ID == id {
			acc.IsActive = true
			found = true
		} else {
			acc.IsActive = false
		}
	}
	if !found {
		return fmt.Errorf("hesap bulunamadı: %s", id)
	}

	_ = s.saveLocked()
	BroadcastAccountChange()
	return nil
}

func (s *AccountStore) ImportCurrentWindowsAccount() (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newID := fmt.Sprintf("acc-%d", time.Now().Unix())
	acc, err := s.importFromWindowsCredential(newID, false)
	if err != nil {
		return nil, err
	}

	// Eğer bu e-posta zaten varsa, tokenlarını güncelle
	for _, existing := range s.Accounts {
		if existing.Email == acc.Email && acc.Email != "Google Pro Kullanıcısı" {
			existing.RefreshToken = acc.RefreshToken
			existing.AccessToken = acc.AccessToken
			existing.Expiry = acc.Expiry
			existing.LastChecked = time.Now().Unix()
			_ = s.saveLocked()
			go s.RefreshAccountQuota(existing.ID)
			BroadcastAccountChange()
			return existing, nil
		}
	}

	// Yeni hesap olarak ekle
	s.Accounts = append(s.Accounts, acc)
	_ = s.saveLocked()
	go s.RefreshAccountQuota(acc.ID)
	BroadcastAccountChange()
	return acc, nil
}

func (s *AccountStore) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newAccounts := make([]*Account, 0)
	wasActive := false
	for _, acc := range s.Accounts {
		if acc.ID == id {
			if acc.IsActive {
				wasActive = true
			}
			continue
		}
		newAccounts = append(newAccounts, acc)
	}
	if len(newAccounts) == len(s.Accounts) {
		return fmt.Errorf("hesap bulunamadı: %s", id)
	}

	s.Accounts = newAccounts
	if wasActive && len(s.Accounts) > 0 {
		s.Accounts[0].IsActive = true
	}

	_ = s.saveLocked()
	BroadcastAccountChange()
	return nil
}

func (s *AccountStore) RefreshAccountToken(acc *Account) (string, error) {
	if acc.RefreshToken == "" {
		return acc.AccessToken, nil
	}

	data := url.Values{}
	data.Set("client_id", GoogleClientID)
	data.Set("refresh_token", acc.RefreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}

	s.mu.Lock()
	acc.AccessToken = res.AccessToken
	acc.Expiry = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second).Format(time.RFC3339Nano)
	_ = s.saveLocked()
	s.mu.Unlock()

	return res.AccessToken, nil
}

func (s *AccountStore) RefreshAccountQuota(id string) (*AccountQuota, error) {
	s.mu.RLock()
	var acc *Account
	for _, a := range s.Accounts {
		if a.ID == id {
			acc = a
			break
		}
	}
	s.mu.RUnlock()

	if acc == nil {
		return nil, fmt.Errorf("hesap bulunamadı: %s", id)
	}

	// Token geçerliliğini sağla
	token := acc.AccessToken
	if token == "" || acc.RefreshToken != "" {
		if t, err := s.RefreshAccountToken(acc); err == nil && t != "" {
			token = t
		}
	}

	// :retrieveUserQuotaSummary çağrısı
	reqBody := []byte(`{"project":"aicode-consumers"}`)
	req, err := http.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/cli/1.2.2 (aidev_client; os_type=windows; arch=amd64; cl=980147163; auth_method=consumer)")
	req.Header.Set("X-Goog-Api-Client", "google-cloud-code")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("quota endpoint failed (%d): %s", resp.StatusCode, string(body))
	}

	var quotaRes struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				BucketID          string  `json:"bucketId"`
				DisplayName       string  `json:"displayName"`
				Window            string  `json:"window"`
				ResetTime         string  `json:"resetTime"`
				Description       string  `json:"description"`
				RemainingFraction float64 `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&quotaRes); err != nil {
		return nil, err
	}

	newQuota := &AccountQuota{
		LastUpdated: time.Now().Unix(),
	}

	for _, g := range quotaRes.Groups {
		for _, b := range g.Buckets {
			bucket := &QuotaBucket{
				BucketID:          b.BucketID,
				DisplayName:       b.DisplayName,
				Window:            b.Window,
				RemainingFraction: b.RemainingFraction,
				ResetTime:         b.ResetTime,
				Description:       b.Description,
			}
			if b.BucketID == "gemini-5h" {
				newQuota.Gemini5h = bucket
			} else if b.BucketID == "gemini-weekly" {
				newQuota.GeminiWeekly = bucket
			} else if b.BucketID == "3p-weekly" || b.BucketID == "3p-5h" {
				newQuota.ClaudeWeekly = bucket
			}
		}
	}

	s.mu.Lock()
	acc.Quota = newQuota
	_ = s.saveLocked()
	s.mu.Unlock()

	BroadcastAccountChange()
	return newQuota, nil
}

func (s *AccountStore) RefreshAllQuotas() {
	accounts := s.GetAllAccounts()
	for _, acc := range accounts {
		_, _ = s.RefreshAccountQuota(acc.ID)
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *AccountStore) GetActiveToken() (string, error) {
	acc := s.GetActiveAccount()
	if acc == nil {
		// Fallback GlobalAuth
		return GlobalAuth.GetValidToken()
	}

	// Süresi dolmuş mu?
	if exp, err := time.Parse(time.RFC3339Nano, acc.Expiry); err == nil {
		if time.Now().After(exp.Add(-2 * time.Minute)) {
			return s.RefreshAccountToken(acc)
		}
	}

	if acc.AccessToken != "" {
		return acc.AccessToken, nil
	}

	return s.RefreshAccountToken(acc)
}
