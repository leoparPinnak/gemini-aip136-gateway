package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

func parseStatusCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	s := err.Error()
	for _, code := range []int{400, 401, 403, 404, 408, 429, 500, 502, 503, 504} {
		if strings.Contains(s, fmt.Sprintf("HTTP %d", code)) ||
			strings.Contains(s, fmt.Sprintf("\"code\": %d", code)) ||
			strings.Contains(s, fmt.Sprintf("\"code\":%d", code)) {
			return code
		}
	}
	return 500
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
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

	activeAcc := GlobalAccountStore.GetActiveAccount()
	activeEmail := "Bilinmiyor"
	activeName := "Tanımsız"
	if activeAcc != nil {
		activeEmail = activeAcc.Email
		activeName = activeAcc.Name
	}

	res := map[string]interface{}{
		"status":         "online",
		"name":           "Gemini AIP-136 Control Center & Protocol Gateway",
		"version":        "2.0.0 (Multi-Account & Process Inspector)",
		"engine":         "Go net/http + crypto/tls",
		"uptime_seconds": uptimeSeconds,
		"active_account": map[string]string{
			"email": activeEmail,
			"name":  activeName,
		},
		"override_settings": GlobalSettingsManager.Get(),
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
			"GET /ui (Liquid Glass Dashboard & Playground)",
			"GET /ws (Canlı WebSocket Akışı)",
			"POST /v1/responses",
			"POST /v1/chat/completions",
			"GET /v1/models",
			"GET /api/accounts",
			"GET /api/settings",
			"GET /health",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	createdTimestamp := 1789422000
	models := []map[string]interface{}{
		{
			"id":               "gemini-3.8-flash-low",
			"object":           "model",
			"created":          createdTimestamp,
			"owned_by":         "google",
			"input_modalities": []string{"text", "image", "video", "audio"},
			"modalities":       []string{"text", "image", "video", "audio"},
		},
		{
			"id":               "gemini-3.8-flash-medium",
			"object":           "model",
			"created":          createdTimestamp,
			"owned_by":         "google",
			"input_modalities": []string{"text", "image", "video", "audio"},
			"modalities":       []string{"text", "image", "video", "audio"},
		},
		{
			"id":               "gemini-3.8-flash-high",
			"object":           "model",
			"created":          createdTimestamp,
			"owned_by":         "google",
			"input_modalities": []string{"text", "image", "video", "audio"},
			"modalities":       []string{"text", "image", "video", "audio"},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

func handleAccountsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts")
	path = strings.TrimPrefix(path, "/")

	if path == "auth-url" {
		authURL, state, err := GenerateAuthURL()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"auth_url": authURL, "state": state})
		return
	}

	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accounts": GlobalAccountStore.GetAllAccounts(),
			"active":   GlobalAccountStore.GetActiveAccount(),
		})
		return
	}

	if r.Method == http.MethodPost {
		bodyBytes, _ := io.ReadAll(r.Body)

		if path == "exchange-code" {
			var body struct {
				Code  string `json:"code"`
				State string `json:"state"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			acc, err := GlobalAccountStore.ExchangeOAuthCode(body.Code, body.State)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "account": acc})
			return
		}

		if path == "add-token" {
			var body struct {
				RefreshToken string `json:"refresh_token"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			acc, err := GlobalAccountStore.AddRefreshToken(body.RefreshToken)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "account": acc})
			return
		}

		if path == "active" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			if err := GlobalAccountStore.SetActiveAccount(body.ID); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "active_id": body.ID})
			return
		}

		if path == "import-windows" {
			acc, err := GlobalAccountStore.ImportCurrentWindowsAccount()
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "account": acc})
			return
		}

		if path == "refresh-quota" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			quota, err := GlobalAccountStore.RefreshAccountQuota(body.ID)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "quota": quota})
			return
		}

		if path == "delete" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			if err := GlobalAccountStore.DeleteAccount(body.ID); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "deleted_id": body.ID})
			return
		}

		if path == "refresh-all-quotas" {
			go GlobalAccountStore.RefreshAllQuotas()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "message": "Tüm kotalar yenileniyor"})
			return
		}

		if path == "set-proxy" {
			var body struct {
				AccountID string `json:"account_id"`
				ProxyID   string `json:"proxy_id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			if err := GlobalAccountStore.SetAccountProxy(body.AccountID, body.ProxyID); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "account_id": body.AccountID, "proxy_id": body.ProxyID})
			return
		}
	}

	http.Error(w, "Not found", http.StatusNotFound)
}

func handleProxiesAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	path := strings.TrimPrefix(r.URL.Path, "/api/proxies")
	path = strings.TrimPrefix(path, "/")

	if r.Method == http.MethodGet {
		var list []*ProxyConfig
		if GlobalProxyManager != nil {
			list = GlobalProxyManager.GetAll()
		}
		_ = json.NewEncoder(w).Encode(list)
		return
	}

	if r.Method == http.MethodPost {
		bodyBytes, _ := io.ReadAll(r.Body)

		if path == "test" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			if body.ID == "" || body.ID == "all" {
				go GlobalProxyManager.TestAll()
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "testing_all"})
				return
			}
			p, err := GlobalProxyManager.TestProxy(body.ID)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "error", "error": err.Error(), "proxy": p})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "proxy": p})
			return
		}

		if path == "delete" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(bodyBytes, &body)
			if err := GlobalProxyManager.DeleteProxy(body.ID); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// Varsayılan POST: Yeni proxy ekle
		var body struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		p, err := GlobalProxyManager.AddProxy(body.URL)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "proxy": p})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(GlobalSettingsManager.Get())
		return
	}
	if r.Method == http.MethodPost {
		bodyBytes, _ := io.ReadAll(r.Body)
		var s OverrideSettings
		if err := json.Unmarshal(bodyBytes, &s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		GlobalSettingsManager.Update(s)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "settings": s})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleRequestsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"requests": GlobalWSHub.GetRecentRequests(),
	})
}

func handleProcessInspectAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pidStr := r.URL.Query().Get("pid")
	if pidStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "pid parametresi gerekli"})
		return
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "geçersiz pid"})
		return
	}

	report, err := InspectPIDNetwork(pid)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(report)
}

func handleProgramRulesAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		rules := GlobalProgramRouter.GetRules()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"rules": rules,
		})
		return
	}
	if r.Method == http.MethodPost {
		var rule ProgramRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if rule.AccountID != "" && GlobalAccountStore != nil {
			if acc := GlobalAccountStore.GetAccountByID(rule.AccountID); acc != nil {
				rule.AccountEmail = acc.Email
				rule.AccountName = acc.Name
			}
		}
		if err := GlobalProgramRouter.AddOrUpdateRule(rule); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"rules":  GlobalProgramRouter.GetRules(),
		})
		return
	}
	if r.Method == http.MethodDelete {
		id := r.URL.Query().Get("id")
		if id != "" {
			_ = GlobalProgramRouter.DeleteRule(id)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"rules":  GlobalProgramRouter.GetRules(),
		})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleDetectedProgramsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if GlobalProcessInspector == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"programs": []ProcessDetail{}})
		return
	}

	if r.Method == http.MethodDelete || (r.Method == http.MethodPost && r.URL.Query().Get("action") == "clear") {
		GlobalProcessInspector.ClearDetectedPrograms()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"message":  "Tespit edilen programlar temizlendi",
			"programs": []ProcessDetail{},
		})
		return
	}

	procs := GlobalProcessInspector.GetDetectedPrograms()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"programs": procs,
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
	reqID := fmt.Sprintf("resp_%d", reqStart.UnixMilli())

	// İstek atan sürecin PID, exe ve zenginleştirilmiş görünen adını bul
	pid, exeName, displayName := GlobalProcessInspector.ResolveClientProcess(r.RemoteAddr)
	targetAcc, ruleName := GlobalProgramRouter.RouteAccount(pid, exeName, displayName)

	targetEmail := ""
	targetName := ""
	if targetAcc != nil {
		targetEmail = targetAcc.Email
		targetName = targetAcc.Name
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	pCtx := ProtocolContext{PID: pid, ProcessName: displayName, Account: targetEmail}

	payload, _, targetModel, thinkingBudget, err := ConvertOpenAiRequestToGemini(body, "", pCtx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Protocol conversion error: %v", err), http.StatusBadRequest)
		GlobalDiagnosticLogger.LogHttpError(pid, displayName, targetEmail, "", 400, fmt.Sprintf("Protokol dönüştürme hatası: %v", err), map[string]interface{}{
			"error":    err.Error(),
			"endpoint": "/v1/responses",
		})
		return
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	modelName, _ := parsed["model"].(string)
	if modelName == "" {
		modelName = "gemini-3.8-flash-medium"
	}

	// Override yapıldı mı?
	ovSettings := GlobalSettingsManager.Get()
	isOverridden := ovSettings.OverrideEnabled

	effortStr := "dynamic"
	if thinkingBudget == 0 {
		effortStr = "off"
	} else if thinkingBudget > 0 {
		effortStr = fmt.Sprintf("budget: %d", thinkingBudget)
	} else if thinkingBudget == -1 {
		effortStr = "high / auto"
	}

	// Canlı İstek Başlangıç Bildirimi
	reqInfo := LiveRequestInfo{
		ID:                  reqID,
		Timestamp:           reqStart.Format("15:04:05"),
		PID:                 pid,
		ProcessName:         displayName,
		SessionID:           payload.Request.SessionID,
		Protocol:            "OpenAI Responses API",
		RequestedModel:      modelName,
		AppliedModel:        targetModel,
		ThinkingEffort:      effortStr,
		IsOverridden:        isOverridden,
		AssignedAccount:     targetEmail,
		AssignedAccountName: targetName,
		RoutingRule:         ruleName,
		Status:              "running",
	}
	BroadcastRequestEvent(reqInfo)

	log.Printf("[POST /v1/responses] PID: %d (%s) | Hesap: %s [%s] | Model: %s -> %s\n",
		pid, displayName, targetEmail, ruleName, modelName, targetModel)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	translator := NewStreamTranslator(w, true, modelName, pCtx)

	err = GlobalGeminiClient.StreamGenerateContentWithAccount(payload, targetAcc, func(chunk *GeminiStreamChunk) error {
		translator.HandleGeminiChunk(chunk)
		return nil
	})

	elapsed := time.Since(reqStart).Milliseconds()

	if err != nil {
		log.Printf("[❌ /v1/responses Hata]: %v\n", err)
		statusCode := parseStatusCodeFromError(err)
		GlobalDiagnosticLogger.LogHttpError(pid, displayName, targetEmail, targetModel, statusCode, err.Error(), map[string]interface{}{
			"endpoint":    "/v1/responses",
			"applied_model": targetModel,
			"duration_ms": elapsed,
		})

		errData, _ := json.Marshal(map[string]interface{}{
			"error": err.Error(),
		})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(errData))

		reqInfo.Status = "error"
		reqInfo.DurationMs = elapsed
		reqInfo.ErrorMsg = err.Error()
		BroadcastRequestEvent(reqInfo)
		return
	}

	translator.FinishStream()

	atomic.AddUint64(&totalInputTokens, uint64(translator.promptTokens))
	atomic.AddUint64(&totalOutputTokens, uint64(translator.outputTokens))
	atomic.AddUint64(&totalCachedTokens, uint64(translator.cachedTokens))

	if GlobalAccountStore != nil && targetEmail != "" {
		GlobalAccountStore.RecordTokenUsage(targetEmail, &GeminiUsageMetadata{
			PromptTokenCount:        translator.promptTokens,
			CandidatesTokenCount:    translator.outputTokens,
			CachedContentTokenCount: translator.cachedTokens,
			TotalTokenCount:         translator.totalTokens,
		})
	}

	// Tamamlanma Bildirimi
	reqInfo.Status = "completed"
	reqInfo.DurationMs = elapsed
	reqInfo.InputTokens = translator.promptTokens
	reqInfo.OutputTokens = translator.outputTokens
	reqInfo.CachedTokens = translator.cachedTokens
	reqInfo.CacheHit = translator.cachedTokens > 0
	BroadcastRequestEvent(reqInfo)

	log.Printf("[✓ /v1/responses Tamamlandı] PID: %d | Süre: %dms | Prompt: %d | Cache: %d | Output: %d\n",
		pid, elapsed, translator.promptTokens, translator.cachedTokens, translator.outputTokens)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	atomic.AddUint64(&totalRequests, 1)
	atomic.AddUint64(&chatCompletionsRequests, 1)

	reqStart := time.Now()
	reqID := fmt.Sprintf("chatcmpl_%d", reqStart.UnixMilli())

	// İstek atan sürecin PID, exe ve zenginleştirilmiş görünen adını bul
	pid, exeName, displayName := GlobalProcessInspector.ResolveClientProcess(r.RemoteAddr)
	targetAcc, ruleName := GlobalProgramRouter.RouteAccount(pid, exeName, displayName)

	targetEmail := ""
	targetName := ""
	if targetAcc != nil {
		targetEmail = targetAcc.Email
		targetName = targetAcc.Name
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	pCtx := ProtocolContext{PID: pid, ProcessName: displayName, Account: targetEmail}

	payload, _, targetModel, thinkingBudget, err := ConvertOpenAiRequestToGemini(body, "", pCtx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Protocol conversion error: %v", err), http.StatusBadRequest)
		GlobalDiagnosticLogger.LogHttpError(pid, displayName, targetEmail, "", 400, fmt.Sprintf("Protokol dönüştürme hatası: %v", err), map[string]interface{}{
			"error":    err.Error(),
			"endpoint": "/v1/chat/completions",
		})
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

	ovSettings := GlobalSettingsManager.Get()
	isOverridden := ovSettings.OverrideEnabled

	effortStr := "dynamic"
	if thinkingBudget == 0 {
		effortStr = "off"
	} else if thinkingBudget > 0 {
		effortStr = fmt.Sprintf("budget: %d", thinkingBudget)
	} else if thinkingBudget == -1 {
		effortStr = "high / auto"
	}

	reqInfo := LiveRequestInfo{
		ID:                  reqID,
		Timestamp:           reqStart.Format("15:04:05"),
		PID:                 pid,
		ProcessName:         displayName,
		SessionID:           payload.Request.SessionID,
		Protocol:            "Chat Completions",
		RequestedModel:      modelName,
		AppliedModel:        targetModel,
		ThinkingEffort:      effortStr,
		IsOverridden:        isOverridden,
		AssignedAccount:     targetEmail,
		AssignedAccountName: targetName,
		RoutingRule:         ruleName,
		Status:              "running",
	}
	BroadcastRequestEvent(reqInfo)

	log.Printf("[POST /v1/chat/completions] PID: %d (%s) | Hesap: %s [%s] | Model: %s -> %s | Stream: %v\n",
		pid, displayName, targetEmail, ruleName, modelName, targetModel, isStream)

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		translator := NewStreamTranslator(w, false, modelName, pCtx)

		err = GlobalGeminiClient.StreamGenerateContentWithAccount(payload, targetAcc, func(chunk *GeminiStreamChunk) error {
			translator.HandleGeminiChunk(chunk)
			return nil
		})

		elapsed := time.Since(reqStart).Milliseconds()

		if err != nil {
			log.Printf("[❌ /v1/chat/completions Hata]: %v\n", err)
			statusCode := parseStatusCodeFromError(err)
			GlobalDiagnosticLogger.LogHttpError(pid, displayName, targetEmail, targetModel, statusCode, err.Error(), map[string]interface{}{
				"endpoint":      "/v1/chat/completions",
				"applied_model": targetModel,
				"duration_ms":   elapsed,
			})

			errData, _ := json.Marshal(map[string]interface{}{
				"error": map[string]interface{}{
					"message": err.Error(),
					"type":    "gemini_gateway_error",
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", string(errData))

			reqInfo.Status = "error"
			reqInfo.DurationMs = elapsed
			reqInfo.ErrorMsg = err.Error()
			BroadcastRequestEvent(reqInfo)
			return
		}

		translator.FinishStream()

		atomic.AddUint64(&totalInputTokens, uint64(translator.promptTokens))
		atomic.AddUint64(&totalOutputTokens, uint64(translator.outputTokens))
		atomic.AddUint64(&totalCachedTokens, uint64(translator.cachedTokens))

		if GlobalAccountStore != nil && targetEmail != "" {
			GlobalAccountStore.RecordTokenUsage(targetEmail, &GeminiUsageMetadata{
				PromptTokenCount:        translator.promptTokens,
				CandidatesTokenCount:    translator.outputTokens,
				CachedContentTokenCount: translator.cachedTokens,
				TotalTokenCount:         translator.totalTokens,
			})
		}

		reqInfo.Status = "completed"
		reqInfo.DurationMs = elapsed
		reqInfo.InputTokens = translator.promptTokens
		reqInfo.OutputTokens = translator.outputTokens
		reqInfo.CachedTokens = translator.cachedTokens
		reqInfo.CacheHit = translator.cachedTokens > 0
		BroadcastRequestEvent(reqInfo)

		log.Printf("[✓ /v1/chat/completions Tamamlandı] PID: %d | Süre: %dms | Prompt: %d | Cache: %d | Output: %d\n",
			pid, elapsed, translator.promptTokens, translator.cachedTokens, translator.outputTokens)
	} else {
		// Non-streaming response
		var thoughtText strings.Builder
		var outputText strings.Builder
		var fnCalls []GeminiFunctionCall
		var usage *GeminiUsageMetadata

		err = GlobalGeminiClient.StreamGenerateContentWithAccount(payload, targetAcc, func(chunk *GeminiStreamChunk) error {
			if chunk.Response.UsageMetadata != nil {
				usage = chunk.Response.UsageMetadata
			} else if chunk.UsageMetadata != nil {
				usage = chunk.UsageMetadata
			}

			candidates := chunk.Response.Candidates
			if len(candidates) == 0 {
				candidates = chunk.Candidates
			}

			for _, c := range candidates {
				for _, p := range c.Content.Parts {
					if p.Thought {
						thoughtText.WriteString(p.Text)
					} else if p.Text != "" {
						outputText.WriteString(p.Text)
					}
					if p.FunctionCall != nil {
						fnCalls = append(fnCalls, *p.FunctionCall)
					}
				}
			}
			return nil
		})

		elapsed := time.Since(reqStart).Milliseconds()

		if err != nil {
			log.Printf("[❌ /v1/chat/completions Hata]: %v\n", err)
			statusCode := parseStatusCodeFromError(err)
			GlobalDiagnosticLogger.LogHttpError(pid, displayName, targetEmail, targetModel, statusCode, err.Error(), map[string]interface{}{
				"endpoint":      "/v1/chat/completions (non-stream)",
				"applied_model": targetModel,
				"duration_ms":   elapsed,
			})

			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": err.Error(),
					"type":    "gemini_gateway_error",
				},
			})
			reqInfo.Status = "error"
			reqInfo.DurationMs = elapsed
			reqInfo.ErrorMsg = err.Error()
			BroadcastRequestEvent(reqInfo)
			return
		}

		promptTokens := 0
		outputTokens := 0
		cachedTokens := 0
		if usage != nil {
			promptTokens = usage.PromptTokenCount
			outputTokens = usage.CandidatesTokenCount
			cachedTokens = usage.CachedContentTokenCount
		}

		atomic.AddUint64(&totalInputTokens, uint64(promptTokens))
		atomic.AddUint64(&totalOutputTokens, uint64(outputTokens))
		atomic.AddUint64(&totalCachedTokens, uint64(cachedTokens))

		if GlobalAccountStore != nil && targetEmail != "" {
			GlobalAccountStore.RecordTokenUsage(targetEmail, &GeminiUsageMetadata{
				PromptTokenCount:        promptTokens,
				CandidatesTokenCount:    outputTokens,
				CachedContentTokenCount: cachedTokens,
				TotalTokenCount:         promptTokens + outputTokens,
			})
		}

		reqInfo.Status = "completed"
		reqInfo.DurationMs = elapsed
		reqInfo.InputTokens = promptTokens
		reqInfo.OutputTokens = outputTokens
		reqInfo.CachedTokens = cachedTokens
		reqInfo.CacheHit = cachedTokens > 0
		BroadcastRequestEvent(reqInfo)

		resp := map[string]interface{}{
			"id":      reqID,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   modelName,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": outputText.String(),
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     promptTokens,
				"completion_tokens": outputTokens,
				"total_tokens":      promptTokens + outputTokens,
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

	// 1. Çoklu Hesap Yönetimini Başlat (Mevcut Windows hesabını otomatik yükler)
	if err := InitAccountStore(); err != nil {
		log.Printf("[UYARI] Hesap yöneticisi başlatılamadı: %v", err)
	}

	// 2. Model & Düşünme Override Yöneticisini Başlat
	InitSettingsManager()

	// 3. İstek Yapan Program / PID İzleyicisini Başlat
	InitProcessInspector()

	// 4. Program Bazlı Akıllı Hesap Yönlendiricisini Başlat
	InitProgramRouter("program_rules.json")

	// 5. Proxy & IP İzolasyon Yöneticisini Başlat
	if _, err := InitProxyManager("."); err != nil {
		log.Printf("[UYARI] Proxy yöneticisi başlatılamadı: %v", err)
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

	// Canlı WebSocket & SSE Uç Noktaları
	mux.HandleFunc("/ws", handleWebSocket)
	mux.HandleFunc("/api/events", handleSSEEvents)

	// Çoklu Hesap REST API
	mux.HandleFunc("/api/accounts", handleAccountsAPI)
	mux.HandleFunc("/api/accounts/", handleAccountsAPI)

	// Proxy & IP İzolasyon REST API
	mux.HandleFunc("/api/proxies", handleProxiesAPI)
	mux.HandleFunc("/api/proxies/", handleProxiesAPI)

	// Override Ayarları REST API
	mux.HandleFunc("/api/settings", handleSettingsAPI)

	// Canlı İstek Geçmişi
	mux.HandleFunc("/api/requests", handleRequestsAPI)

	// Süreç ve Ağ Port İnceleme API
	mux.HandleFunc("/api/process/inspect", handleProcessInspectAPI)

	// Program Bazlı Hesap Yönlendirme API
	mux.HandleFunc("/api/program-rules", handleProgramRulesAPI)
	mux.HandleFunc("/api/detected-programs", handleDetectedProgramsAPI)

	// Teşhis ve Olay Günlüğü REST API
	mux.HandleFunc("/api/logs", handleDiagnosticLogsAPI)
	mux.HandleFunc("/api/logs/", handleDiagnosticLogsAPI)

	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("⚡ Gemini AIP-136 Control Center & Protocol Gateway (v2.0)\n")
	fmt.Printf("🌐 Dinleme Adresi       : http://%s\n", addr)
	fmt.Printf("🔒 TLS Parmak İzi       : Go Native crypto/tls (Antigravity CLI ile 1:1)\n")
	fmt.Printf("🖥️  Modern Dashboard UI  : http://%s/ui\n", addr)
	fmt.Printf("🔌 Canlı WebSocket       : ws://%s/ws\n", addr)
	fmt.Printf("👥 Çoklu Hesap Kasası   : /api/accounts\n")
	fmt.Printf("🎯 Program Yönlendirme  : /api/program-rules\n")
	fmt.Printf("📡 OpenAI Responses     : http://%s/v1/responses\n", addr)
	fmt.Printf("📡 Chat Completions     : http://%s/v1/chat/completions\n", addr)
	fmt.Printf("📊 Sağlık & Metrikler   : http://%s/health\n", addr)
	fmt.Println(strings.Repeat("=", 70))

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateNew || state == http.StateActive {
				if GlobalProcessInspector != nil {
					GlobalProcessInspector.RegisterSocket(conn.RemoteAddr().String())
				}
			}
		},
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
