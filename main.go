package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed ui.html
var uiHTML string

func handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data, err := os.ReadFile("ui.html"); err == nil {
		w.Write(data)
		return
	}
	w.Write([]byte(uiHTML))
}

var (
	startTime               = time.Now()
	totalRequests           uint64
	responsesApiRequests    uint64
	chatCompletionsRequests uint64
	totalInputTokens        uint64
	totalOutputTokens       uint64
	totalCachedTokens       uint64
)

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	uptimeSeconds := int(time.Since(startTime).Seconds())
	cached := atomic.LoadUint64(&totalCachedTokens)
	input := atomic.LoadUint64(&totalInputTokens)
	cacheRatio := "0%"
	if input > 0 {
		cacheRatio = fmt.Sprintf("%.2f%%", float64(cached)/float64(input)*100)
	}

	res := map[string]interface{}{
		"status":         "online",
		"name":           "DeepSeek Harness (DSH) Go Protocol Gateway",
		"version":        "1.0.0 (Go Native TLS)",
		"engine":         "Go net/http + crypto/tls",
		"uptime_seconds": uptimeSeconds,
		"stats": map[string]interface{}{
			"totalRequests":           atomic.LoadUint64(&totalRequests),
			"responsesApiRequests":    atomic.LoadUint64(&responsesApiRequests),
			"chatCompletionsRequests": atomic.LoadUint64(&chatCompletionsRequests),
			"totalInputTokens":        input,
			"totalOutputTokens":       atomic.LoadUint64(&totalOutputTokens),
			"totalCachedTokens":       cached,
			"cacheHitRatio":           cacheRatio,
		},
		"endpoints": []string{
			"GET /ui (Web Arayüzü / Playground)",
			"POST /v1/responses",
			"POST /v1/chat/completions",
			"GET /v1/models",
			"GET /health",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	createdTimestamp := 1789422000
	models := []map[string]interface{}{
		{"id": "gemini-3.8-flash-low", "object": "model", "created": createdTimestamp, "owned_by": "google"},
		{"id": "gemini-3.8-flash-medium", "object": "model", "created": createdTimestamp, "owned_by": "google"},
		{"id": "gemini-3.8-flash-high", "object": "model", "created": createdTimestamp, "owned_by": "google"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

func handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	atomic.AddUint64(&totalRequests, 1)
	atomic.AddUint64(&responsesApiRequests, 1)

	reqStart := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	payload, _, targetModel, _, err := ConvertOpenAiRequestToGemini(body, "")
	if err != nil {
		http.Error(w, fmt.Sprintf("Protocol conversion error: %v", err), http.StatusBadRequest)
		return
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	modelName, _ := parsed["model"].(string)
	if modelName == "" {
		modelName = "deepseek-v4-flash"
	}

	log.Printf("[POST /v1/responses] Model: %s -> %s\n", modelName, targetModel)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	translator := NewStreamTranslator(w, true, modelName)

	err = GlobalGeminiClient.StreamGenerateContent(payload, func(chunk *GeminiStreamChunk) error {
		translator.HandleGeminiChunk(chunk)
		return nil
	})

	if err != nil {
		log.Printf("[❌ /v1/responses Hata]: %v\n", err)
		errData, _ := json.Marshal(map[string]interface{}{
			"error": err.Error(),
		})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(errData))
		return
	}

	translator.FinishStream()

	atomic.AddUint64(&totalInputTokens, uint64(translator.promptTokens))
	atomic.AddUint64(&totalOutputTokens, uint64(translator.outputTokens))
	atomic.AddUint64(&totalCachedTokens, uint64(translator.cachedTokens))

	elapsed := time.Since(reqStart).Milliseconds()
	log.Printf("[✓ /v1/responses Tamamlandı] Süre: %dms | Prompt: %d | Cache: %d | Output: %d\n",
		elapsed, translator.promptTokens, translator.cachedTokens, translator.outputTokens)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	atomic.AddUint64(&totalRequests, 1)
	atomic.AddUint64(&chatCompletionsRequests, 1)

	reqStart := time.Now()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	payload, _, targetModel, _, err := ConvertOpenAiRequestToGemini(body, "")
	if err != nil {
		http.Error(w, fmt.Sprintf("Protocol conversion error: %v", err), http.StatusBadRequest)
		return
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	modelName, _ := parsed["model"].(string)
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	isStream := true
	if s, ok := parsed["stream"].(bool); ok {
		isStream = s
	}

	log.Printf("[POST /v1/chat/completions] Model: %s -> %s | Stream: %v\n", modelName, targetModel, isStream)

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		translator := NewStreamTranslator(w, false, modelName)

		err = GlobalGeminiClient.StreamGenerateContent(payload, func(chunk *GeminiStreamChunk) error {
			translator.HandleGeminiChunk(chunk)
			return nil
		})

		if err != nil {
			log.Printf("[❌ /v1/chat/completions Hata]: %v\n", err)
			errData, _ := json.Marshal(map[string]interface{}{
				"error": map[string]interface{}{
					"message": err.Error(),
					"type":    "gemini_gateway_error",
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", string(errData))
			return
		}

		translator.FinishStream()

		atomic.AddUint64(&totalInputTokens, uint64(translator.promptTokens))
		atomic.AddUint64(&totalOutputTokens, uint64(translator.outputTokens))
		atomic.AddUint64(&totalCachedTokens, uint64(translator.cachedTokens))

		elapsed := time.Since(reqStart).Milliseconds()
		log.Printf("[✓ /v1/chat/completions Tamamlandı] Süre: %dms | Prompt: %d | Cache: %d | Output: %d\n",
			elapsed, translator.promptTokens, translator.cachedTokens, translator.outputTokens)
	} else {
		// Non-streaming response
		var thoughtText strings.Builder
		var outputText strings.Builder
		var fnCalls []GeminiFunctionCall
		var usage *GeminiUsageMetadata

		err = GlobalGeminiClient.StreamGenerateContent(payload, func(chunk *GeminiStreamChunk) error {
			if chunk.Response.UsageMetadata != nil {
				usage = chunk.Response.UsageMetadata
			} else if chunk.UsageMetadata != nil {
				usage = chunk.UsageMetadata
			}

			var cands []GeminiCandidate
			if len(chunk.Response.Candidates) > 0 {
				cands = chunk.Response.Candidates
			} else if len(chunk.Candidates) > 0 {
				cands = chunk.Candidates
			}

			for _, c := range cands {
				for _, p := range c.Content.Parts {
					if p.Thought && p.Text != "" {
						thoughtText.WriteString(p.Text)
					} else if p.Text != "" {
						outputText.WriteString(p.Text)
					} else if p.FunctionCall != nil {
						fnCalls = append(fnCalls, *p.FunctionCall)
					}
				}
			}
			return nil
		})

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		respID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
		msgObj := map[string]interface{}{
			"role":    "assistant",
			"content": outputText.String(),
		}
		if thoughtText.Len() > 0 {
			msgObj["reasoning_content"] = thoughtText.String()
		}

		if len(fnCalls) > 0 {
			var tcList []map[string]interface{}
			for _, fc := range fnCalls {
				b, _ := json.Marshal(fc.Args)
				tcList = append(tcList, map[string]interface{}{
					"id":   fc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      fc.Name,
						"arguments": string(b),
					},
				})
			}
			msgObj["tool_calls"] = tcList
		}

		finishReason := "stop"
		if len(fnCalls) > 0 {
			finishReason = "tool_calls"
		}

		promptTokens := 0
		candidatesTokens := 0
		cachedTokens := 0
		totalTokens := 0
		if usage != nil {
			promptTokens = usage.PromptTokenCount
			candidatesTokens = usage.CandidatesTokenCount
			cachedTokens = usage.CachedContentTokenCount
			totalTokens = usage.TotalTokenCount
		}

		resp := map[string]interface{}{
			"id":      respID,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       msgObj,
					"finish_reason": finishReason,
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     promptTokens,
				"completion_tokens": candidatesTokens,
				"total_tokens":      totalTokens,
				"prompt_tokens_details": map[string]interface{}{
					"cached_tokens": cachedTokens,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func main() {
	portFlag := flag.Int("port", 0, "Port to listen on")
	flag.Parse()

	port := 8000
	if *portFlag > 0 {
		port = *portFlag
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			port = p
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHealth)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ui", handleUI)
	mux.HandleFunc("/ui/", handleUI)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/models", handleModels)
	mux.HandleFunc("/v1/responses", handleResponses)
	mux.HandleFunc("/responses", handleResponses)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/chat/completions", handleChatCompletions)

	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("⚡ DSH Go Protocol Gateway Aktif!\n")
	fmt.Printf("🌐 Dinleme Adresi     : http://%s\n", addr)
	fmt.Printf("🔒 TLS Parmak İzi     : Go Native crypto/tls (Antigravity CLI ile 1:1)\n")
	fmt.Printf("🖥️  Test Arayüzü (Web) : http://%s/ui\n", addr)
	fmt.Printf("📡 OpenAI Responses   : http://%s/v1/responses\n", addr)
	fmt.Printf("📡 Chat Completions   : http://%s/v1/chat/completions\n", addr)
	fmt.Printf("📊 Sağlık & Metrikler : http://%s/health\n", addr)
	fmt.Println(strings.Repeat("=", 70))

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
