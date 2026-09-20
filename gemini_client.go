package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	CloudCodeStreamURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
)

// Gemini SSE Response Structures
type GeminiCandidatePart struct {
	Text             string              `json:"text"`
	Thought          bool                `json:"thought"`
	FunctionCall     *GeminiFunctionCall `json:"functionCall"`
	ThoughtSignature string              `json:"thoughtSignature"`
}

type GeminiCandidateContent struct {
	Role  string                `json:"role"`
	Parts []GeminiCandidatePart `json:"parts"`
}

type GeminiCandidate struct {
	Content      GeminiCandidateContent `json:"content"`
	FinishReason string                 `json:"finishReason"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

type GeminiStreamChunk struct {
	Response struct {
		Candidates    []GeminiCandidate    `json:"candidates"`
		UsageMetadata *GeminiUsageMetadata `json:"usageMetadata"`
	} `json:"response"`
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata"`
}

type GeminiClient struct {
	httpClient *http.Client
}

var GlobalGeminiClient = &GeminiClient{
	httpClient: &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		Timeout: 5 * time.Minute,
	},
}

func (c *GeminiClient) StreamGenerateContent(
	payload *GeminiAipPayload,
	onChunk func(chunk *GeminiStreamChunk) error,
) error {
	return c.StreamGenerateContentWithAccount(payload, nil, onChunk)
}

func (c *GeminiClient) StreamGenerateContentWithAccount(
	payload *GeminiAipPayload,
	acc *Account,
	onChunk func(chunk *GeminiStreamChunk) error,
) error {
	var token string
	var err error
	if acc != nil && GlobalAccountStore != nil {
		token, err = GlobalAccountStore.GetTokenForAccount(acc)
	} else if GlobalAccountStore != nil {
		token, err = GlobalAccountStore.GetActiveToken()
	}
	if token == "" || err != nil {
		token, err = GlobalAuth.GetValidToken()
	}
	if err != nil {
		return fmt.Errorf("auth error: %w", err)
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload error: %w", err)
	}

	req, err := http.NewRequest("POST", CloudCodeStreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", OfficialAntigravityUserAgent)
	req.Header.Set("X-Goog-Api-Client", "google-cloud-code")
	req.Header.Set("Accept", "text/event-stream")

	// 🛡️ Stealth Timing Layer: İnsan benzeri mikro-jitter (15ms - 45ms)
	// İsteklerin mekanik 0ms aralıklarla değil, doğal insan/ağ varyasyonuyla gitmesini sağlar
	jitterMs := 15 + rand.Intn(31)
	time.Sleep(time.Duration(jitterMs) * time.Millisecond)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Google CloudCode API error (HTTP %d): %s", resp.StatusCode, string(errBody))
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("stream read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataStr == "" || dataStr == "[DONE]" {
				continue
			}

			var chunk GeminiStreamChunk
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}

			if err := onChunk(&chunk); err != nil {
				return err
			}
		}
	}

	return nil
}
