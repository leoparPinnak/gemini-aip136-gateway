package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptrace"
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

type GeminiPromptFeedback struct {
	BlockReason        string `json:"blockReason,omitempty"`
	BlockReasonMessage string `json:"blockReasonMessage,omitempty"`
}

type GeminiStreamChunk struct {
	Response struct {
		Candidates     []GeminiCandidate     `json:"candidates"`
		UsageMetadata  *GeminiUsageMetadata  `json:"usageMetadata"`
		PromptFeedback *GeminiPromptFeedback `json:"promptFeedback"`
		TraceID        string                `json:"traceId,omitempty"`
		ModelVersion   string                `json:"modelVersion,omitempty"`
	} `json:"response"`
	Candidates     []GeminiCandidate     `json:"candidates"`
	UsageMetadata  *GeminiUsageMetadata  `json:"usageMetadata"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback"`
	TraceID        string                `json:"traceId,omitempty"`
	ResponseID     string                `json:"responseId,omitempty"`
	ModelVersion   string                `json:"modelVersion,omitempty"`
	Error          *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// ConnInfo, bir isteğin Google'a giden TCP/TLS bağlantı ölçümüdür (A2).
// httptrace ile doldurulur; nil ise hiçbir ölçüm yapılmaz.
// WaitMs: istek gönderildikten GotConn'a kadar geçen süre (bağlantı kurulum
// veya havuzdan yeniden kullanım gecikmesi — proxy transport havuzu C1 ölçümü).
type ConnInfo struct {
	Reused         bool
	WasIdle        bool
	WaitMs         int64
	UpstreamTrace string // yanıt header'ı x-request-id / server-timing
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
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 50,
			MaxConnsPerHost:     100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
		},
		Timeout: 5 * time.Minute,
	},
}

func (c *GeminiClient) StreamGenerateContent(
	payload *GeminiAipPayload,
	onChunk func(chunk *GeminiStreamChunk) error,
) error {
	return c.StreamGenerateContentWithAccount(payload, nil, nil, onChunk)
}

func (c *GeminiClient) StreamGenerateContentWithAccount(
	payload *GeminiAipPayload,
	acc *Account,
	ci *ConnInfo,
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

	// A2: Bağlantı izi — keep-alive yeniden kullanımı cache affinity hipotezini
	// ölçmek için şart (proxy transport havuzu C1'den sonra ayrıca okunur).
	if ci != nil {
		traceStart := time.Now()
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				ci.Reused = info.Reused
				ci.WasIdle = info.WasIdle
				ci.WaitMs = time.Since(traceStart).Milliseconds()
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(context.Background(), trace))
	}

	// 🛡️ Stealth Timing Layer: İnsan benzeri mikro-jitter (15ms - 45ms)
	// İsteklerin mekanik 0ms aralıklarla değil, doğal insan/ağ varyasyonuyla gitmesini sağlar
	jitterMs := 15 + rand.Intn(31)
	time.Sleep(time.Duration(jitterMs) * time.Millisecond)

	var httpClient *http.Client
	if acc != nil && acc.ProxyID != "" && GlobalProxyManager != nil {
		httpClient = GlobalProxyManager.GetHttpClientForProxy(acc.ProxyID, 5*time.Minute)
	} else {
		httpClient = c.httpClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Google CloudCode API error (HTTP %d): %s", resp.StatusCode, string(errBody))
	}

	if ci != nil {
		if t := resp.Header.Get("x-request-id"); t != "" {
			ci.UpstreamTrace = t
		} else if t := resp.Header.Get("server-timing"); t != "" {
			ci.UpstreamTrace = t
		}
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

			if chunk.Error != nil {
				log.Printf("[❌ Google CloudCode Stream Hatası]: Kod: %d, Mesaj: %s, Durum: %s\n",
					chunk.Error.Code, chunk.Error.Message, chunk.Error.Status)
				return fmt.Errorf("Google CloudCode stream error (%d - %s): %s", chunk.Error.Code, chunk.Error.Status, chunk.Error.Message)
			}

			feedback := chunk.Response.PromptFeedback
			if feedback == nil {
				feedback = chunk.PromptFeedback
			}
			if feedback != nil && feedback.BlockReason != "" {
				log.Printf("[🚫 Google Prompt Filtresi]: İstek Google tarafından engellendi: %s (%s)\n",
					feedback.BlockReason, feedback.BlockReasonMessage)
			}

			if err := onChunk(&chunk); err != nil {
				return err
			}
		}
	}

	return nil
}
