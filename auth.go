package main

import (
	"encoding/json"
	"fmt"
	"io"
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

type FILETIME struct {
	LowDateTime  uint32
	HighDateTime uint32
}

type CREDENTIALW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        FILETIME
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32      = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW = advapi32.NewProc("CredReadW")
	procCredFree  = advapi32.NewProc("CredFree")
)

const (
	GoogleClientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
)

type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"`
}

type CredBlob struct {
	Token TokenData `json:"token"`
}

type AuthManager struct {
	cachedToken  string
	refreshToken string
	expiryDate   time.Time
	mu           sync.RWMutex
}

var GlobalAuth = &AuthManager{}

func (a *AuthManager) readWindowsCredential() (*TokenData, error) {
	targetUTF16, err := syscall.UTF16PtrFromString("gemini:antigravity")
	if err != nil {
		return nil, err
	}

	var pCred *CREDENTIALW
	r1, _, err := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetUTF16)),
		1, // CRED_TYPE_GENERIC
		0,
		uintptr(unsafe.Pointer(&pCred)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CredReadW failed: %v", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pCred)))

	if pCred == nil || pCred.CredentialBlob == nil || pCred.CredentialBlobSize == 0 {
		return nil, fmt.Errorf("empty credential blob")
	}

	blobBytes := unsafe.Slice(pCred.CredentialBlob, pCred.CredentialBlobSize)
	var blob CredBlob
	if err := json.Unmarshal(blobBytes, &blob); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return &blob.Token, nil
}

func (a *AuthManager) readCapturedHeaders() string {
	execDir, _ := os.Getwd()
	headerPath := filepath.Join(execDir, "..", "last_captured_headers.json")
	if data, err := os.ReadFile(headerPath); err == nil {
		var h map[string]interface{}
		if err := json.Unmarshal(data, &h); err == nil {
			if auth, ok := h["authorization"].(string); ok && strings.HasPrefix(auth, "Bearer ") {
				return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			}
		}
	}
	return ""
}

func (a *AuthManager) readOAuthFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	credsPath := filepath.Join(home, ".gemini", "oauth_creds.json")
	if data, err := os.ReadFile(credsPath); err == nil {
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err == nil {
			if at, ok := m["access_token"].(string); ok && at != "" {
				return at
			}
		}
	}
	return ""
}

func (a *AuthManager) refreshOAuthToken(refreshToken string) (string, error) {
	cid, csec := getOAuthCredentials()

	data := url.Values{}
	data.Set("client_id", cid)
	data.Set("client_secret", csec)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", OfficialAntigravityUserAgent)

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
		return "", fmt.Errorf("refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}

	a.mu.Lock()
	a.cachedToken = res.AccessToken
	a.expiryDate = time.Now().Add(time.Duration(res.ExpiresIn-60) * time.Second)
	a.mu.Unlock()

	return res.AccessToken, nil
}

func (a *AuthManager) GetValidToken() (string, error) {
	a.mu.RLock()
	if a.cachedToken != "" && time.Now().Before(a.expiryDate.Add(-2*time.Minute)) {
		token := a.cachedToken
		a.mu.RUnlock()
		return token, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. Windows Credential Manager'dan oku
	tok, err := a.readWindowsCredential()
	if err == nil && tok != nil && tok.AccessToken != "" {
		a.refreshToken = tok.RefreshToken
		if exp, err := time.Parse(time.RFC3339Nano, tok.Expiry); err == nil {
			a.expiryDate = exp
		} else {
			a.expiryDate = time.Now().Add(45 * time.Minute)
		}

		if time.Now().Before(a.expiryDate.Add(-1 * time.Minute)) {
			a.cachedToken = tok.AccessToken
			return tok.AccessToken, nil
		}

		if a.refreshToken != "" {
			if refTok, err := a.refreshOAuthToken(a.refreshToken); err == nil {
				return refTok, nil
			}
		}
		a.cachedToken = tok.AccessToken
		return tok.AccessToken, nil
	}

	// 2. last_captured_headers.json
	if capTok := a.readCapturedHeaders(); capTok != "" {
		a.cachedToken = capTok
		a.expiryDate = time.Now().Add(30 * time.Minute)
		return capTok, nil
	}

	// 3. ~/.gemini/oauth_creds.json
	if fileTok := a.readOAuthFile(); fileTok != "" {
		a.cachedToken = fileTok
		a.expiryDate = time.Now().Add(30 * time.Minute)
		return fileTok, nil
	}

	return "", fmt.Errorf("no valid Google OAuth credential found")
}
