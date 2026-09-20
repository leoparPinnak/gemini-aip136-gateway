package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Gemini CloudCode AIP-136 Structures
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiFileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	InlineData       *GeminiInlineData       `json:"inlineData,omitempty"`
	FileData         *GeminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type GeminiFunctionCall struct {
	ID   string                 `json:"id,omitempty"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type GeminiFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiSystemInstruction struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts"`
	ThinkingBudget  int  `json:"thinkingBudget"`
}

type GeminiGenerationConfig struct {
	MaxOutputTokens int                   `json:"maxOutputTokens"`
	Temperature     float64               `json:"temperature"`
	TopP            float64               `json:"topP"`
	ThinkingConfig  *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type GeminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

type GeminiInnerRequest struct {
	Contents          []GeminiContent          `json:"contents"`
	SystemInstruction *GeminiSystemInstruction `json:"systemInstruction,omitempty"`
	GenerationConfig  GeminiGenerationConfig   `json:"generationConfig"`
	SessionID         string                   `json:"sessionId"`
	Tools             []GeminiTool             `json:"tools,omitempty"`
}

type GeminiAipPayload struct {
	Project     string             `json:"project"`
	RequestID   string             `json:"requestId"`
	Model       string             `json:"model"`
	UserAgent   string             `json:"userAgent"`
	RequestType string             `json:"requestType"`
	Request     GeminiInnerRequest `json:"request"`
}

// ConvertSchemaTypeToGemini recursively sanitizes and converts JSON Schema to Gemini OpenAPI 3.0 / Protobuf Schema
func ConvertSchemaTypeToGemini(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{"type": "OBJECT", "properties": map[string]interface{}{}}
	}
	cloned := make(map[string]interface{})
	for k, v := range schema {
		cloned[k] = v
	}

	// 1. Convert "const" to "enum" + inferred "type"
	if constVal, exists := cloned["const"]; exists {
		if _, hasEnum := cloned["enum"]; !hasEnum {
			cloned["enum"] = []interface{}{fmt.Sprintf("%v", constVal)}
		}
		if _, hasType := cloned["type"]; !hasType {
			switch constVal.(type) {
			case string:
				cloned["type"] = "STRING"
			case bool:
				cloned["type"] = "BOOLEAN"
			case int, int8, int16, int32, int64:
				cloned["type"] = "INTEGER"
			case float32, float64:
				cloned["type"] = "NUMBER"
			default:
				cloned["type"] = "STRING"
			}
		}
		delete(cloned, "const")
	}

	// 2. Type normalization & nullable detection
	if t, ok := cloned["type"].(string); ok {
		cloned["type"] = strings.ToUpper(t)
	} else if arr, ok := cloned["type"].([]interface{}); ok && len(arr) > 0 {
		var nonNullType string
		for _, item := range arr {
			if s, ok := item.(string); ok {
				if strings.ToLower(s) == "null" {
					cloned["nullable"] = true
				} else if nonNullType == "" {
					nonNullType = strings.ToUpper(s)
				}
			}
		}
		if nonNullType != "" {
			cloned["type"] = nonNullType
		} else {
			cloned["type"] = "STRING"
		}
	}

	// Auto-infer type if missing
	if _, hasType := cloned["type"]; !hasType {
		if cloned["properties"] != nil {
			cloned["type"] = "OBJECT"
		} else if cloned["items"] != nil {
			cloned["type"] = "ARRAY"
		}
	}

	// 3. Stringify enum values (Google Protobuf Schema expects repeated string enum)
	if enumArr, ok := cloned["enum"].([]interface{}); ok {
		var strEnum []string
		for _, e := range enumArr {
			strEnum = append(strEnum, fmt.Sprintf("%v", e))
		}
		cloned["enum"] = strEnum
	}

	// 4. Clean empty required array
	if reqArr, ok := cloned["required"].([]interface{}); ok && len(reqArr) == 0 {
		delete(cloned, "required")
	} else if reqStrArr, ok := cloned["required"].([]string); ok && len(reqStrArr) == 0 {
		delete(cloned, "required")
	}

	// 5. Delete unsupported schema keywords in Google Protobuf Schema
	unsupportedKeys := []string{
		"$schema", "$id", "$ref", "$comment", "definitions", "$defs",
		"additionalProperties", "default", "title",
		"exclusiveMaximum", "exclusiveMinimum",
		"minProperties", "maxProperties",
		"patternProperties", "dependencies", "dependentRequired", "dependentSchemas",
		"uniqueItems", "readOnly", "writeOnly", "examples", "deprecated",
	}
	for _, key := range unsupportedKeys {
		delete(cloned, key)
	}

	// 6. Recurse into properties
	if props, ok := cloned["properties"].(map[string]interface{}); ok {
		newProps := make(map[string]interface{})
		for pk, pv := range props {
			if pvMap, ok := pv.(map[string]interface{}); ok {
				newProps[pk] = ConvertSchemaTypeToGemini(pvMap)
			} else {
				newProps[pk] = pv
			}
		}
		cloned["properties"] = newProps
	}

	// 7. Recurse into items
	if items, ok := cloned["items"].(map[string]interface{}); ok {
		cloned["items"] = ConvertSchemaTypeToGemini(items)
	} else if itemsArr, ok := cloned["items"].([]interface{}); ok && len(itemsArr) > 0 {
		if firstMap, ok := itemsArr[0].(map[string]interface{}); ok {
			cloned["items"] = ConvertSchemaTypeToGemini(firstMap)
		}
	}

	// 8. Recurse into oneOf and one_of
	for _, k := range []string{"oneOf", "one_of"} {
		if arr, ok := cloned[k].([]interface{}); ok {
			var newArr []interface{}
			for _, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					newArr = append(newArr, ConvertSchemaTypeToGemini(itemMap))
				} else {
					newArr = append(newArr, item)
				}
			}
			cloned[k] = newArr
		}
	}

	// 9. Recurse into anyOf and any_of
	for _, k := range []string{"anyOf", "any_of"} {
		if arr, ok := cloned[k].([]interface{}); ok {
			var newArr []interface{}
			for _, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					newArr = append(newArr, ConvertSchemaTypeToGemini(itemMap))
				} else {
					newArr = append(newArr, item)
				}
			}
			cloned[k] = newArr
		}
	}

	// 10. Recurse into allOf and all_of
	for _, k := range []string{"allOf", "all_of"} {
		if arr, ok := cloned[k].([]interface{}); ok {
			var newArr []interface{}
			for _, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					newArr = append(newArr, ConvertSchemaTypeToGemini(itemMap))
				} else {
					newArr = append(newArr, item)
				}
			}
			cloned[k] = newArr
		}
	}

	return cloned
}

func extractStringFromContent(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			} else if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					parts = append(parts, t)
				} else if it, ok := m["input_text"].(string); ok && it != "" {
					parts = append(parts, it)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	if m, ok := v.(map[string]interface{}); ok {
		if t, ok := m["text"].(string); ok {
			return t
		}
		if it, ok := m["input_text"].(string); ok {
			return it
		}
	}
	return ""
}

func parseDataURLOrMedia(rawURL string, defaultMime string) *GeminiInlineData {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}

	// 1. data:<mimeType>;base64,<data>
	if strings.HasPrefix(rawURL, "data:") {
		parts := strings.SplitN(rawURL, ",", 2)
		if len(parts) == 2 {
			meta := parts[0]
			data := parts[1]
			mime := defaultMime
			if mime == "" {
				mime = "image/png"
			}
			metaParts := strings.Split(strings.TrimPrefix(meta, "data:"), ";")
			if len(metaParts) > 0 && metaParts[0] != "" {
				mime = metaParts[0]
			}
			return &GeminiInlineData{
				MimeType: mime,
				Data:     strings.TrimSpace(data),
			}
		}
	}

	// 2. Pure base64 data (length >= 100, no spaces, no path separators)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") && len(rawURL) > 100 && !strings.Contains(rawURL, " ") && !strings.Contains(rawURL, "\\") {
		if _, err := base64.StdEncoding.DecodeString(rawURL); err == nil {
			mime := defaultMime
			if mime == "" {
				mime = "image/png"
			}
			return &GeminiInlineData{
				MimeType: mime,
				Data:     rawURL,
			}
		}
	}

	// 3. Local file path (e.g. C:\... or /... or file://...)
	filePath := rawURL
	if strings.HasPrefix(filePath, "file:///") {
		filePath = strings.TrimPrefix(filePath, "file:///")
		filePath = filepath.FromSlash(filePath)
	} else if strings.HasPrefix(filePath, "file://") {
		filePath = strings.TrimPrefix(filePath, "file://")
		filePath = filepath.FromSlash(filePath)
	}

	if fileBytes, err := os.ReadFile(filePath); err == nil && len(fileBytes) > 0 {
		mime := defaultMime
		if mime == "" {
			ext := strings.ToLower(filepath.Ext(filePath))
			switch ext {
			case ".png":
				mime = "image/png"
			case ".jpg", ".jpeg":
				mime = "image/jpeg"
			case ".webp":
				mime = "image/webp"
			case ".gif":
				mime = "image/gif"
			case ".svg":
				mime = "image/svg+xml"
			case ".mp4":
				mime = "video/mp4"
			case ".webm":
				mime = "video/webm"
			case ".mov":
				mime = "video/quicktime"
			case ".pdf":
				mime = "application/pdf"
			case ".mp3":
				mime = "audio/mp3"
			case ".wav":
				mime = "audio/wav"
			default:
				mime = http.DetectContentType(fileBytes)
			}
		}
		return &GeminiInlineData{
			MimeType: mime,
			Data:     base64.StdEncoding.EncodeToString(fileBytes),
		}
	}

	// 4. Remote HTTP/HTTPS URL
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(rawURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err == nil && len(b) > 0 {
				mime := resp.Header.Get("Content-Type")
				if mime == "" {
					mime = defaultMime
				}
				if mime == "" {
					mime = http.DetectContentType(b)
				}
				return &GeminiInlineData{
					MimeType: mime,
					Data:     base64.StdEncoding.EncodeToString(b),
				}
			}
		}
	}

	return nil
}

func parseSingleMapToGeminiPart(m map[string]interface{}) *GeminiPart {
	t, _ := m["type"].(string)

	// 1. Text types
	if t == "text" || t == "input_text" || t == "output_text" {
		text, _ := m["text"].(string)
		if text == "" {
			text, _ = m["input_text"].(string)
		}
		if text != "" {
			return &GeminiPart{Text: text}
		}
		return nil
	}

	// 2. Image types: "image_url", "input_image", "image"
	if t == "image_url" || t == "input_image" || t == "image" {
		var rawURL string
		if iuMap, ok := m["image_url"].(map[string]interface{}); ok {
			rawURL, _ = iuMap["url"].(string)
		} else if iuStr, ok := m["image_url"].(string); ok {
			rawURL = iuStr
		} else if u, ok := m["url"].(string); ok {
			rawURL = u
		} else if b64, ok := m["image_bytes"].(string); ok {
			rawURL = b64
		} else if data, ok := m["data"].(string); ok {
			rawURL = data
		}

		defaultMime := "image/png"
		if mime, ok := m["mime_type"].(string); ok && mime != "" {
			defaultMime = mime
		} else if mime, ok := m["mimeType"].(string); ok && mime != "" {
			defaultMime = mime
		}

		inline := parseDataURLOrMedia(rawURL, defaultMime)
		if inline != nil {
			return &GeminiPart{InlineData: inline}
		}
		return nil
	}

	// 3. Video types: "video_url", "input_video", "video"
	if t == "video_url" || t == "input_video" || t == "video" {
		var rawURL string
		if vuMap, ok := m["video_url"].(map[string]interface{}); ok {
			rawURL, _ = vuMap["url"].(string)
		} else if vuStr, ok := m["video_url"].(string); ok {
			rawURL = vuStr
		} else if u, ok := m["url"].(string); ok {
			rawURL = u
		} else if data, ok := m["data"].(string); ok {
			rawURL = data
		}

		defaultMime := "video/mp4"
		if mime, ok := m["mime_type"].(string); ok && mime != "" {
			defaultMime = mime
		}
		inline := parseDataURLOrMedia(rawURL, defaultMime)
		if inline != nil {
			return &GeminiPart{InlineData: inline}
		}
		return nil
	}

	// 4. Audio types: "input_audio", "audio_url", "audio"
	if t == "input_audio" || t == "audio_url" || t == "audio" {
		var rawURL string
		defaultMime := "audio/mp3"
		if auMap, ok := m["input_audio"].(map[string]interface{}); ok {
			rawURL, _ = auMap["data"].(string)
			if fmtStr, ok := auMap["format"].(string); ok && fmtStr != "" {
				defaultMime = "audio/" + fmtStr
			}
		} else if data, ok := m["data"].(string); ok {
			rawURL = data
		}
		inline := parseDataURLOrMedia(rawURL, defaultMime)
		if inline != nil {
			return &GeminiPart{InlineData: inline}
		}
		return nil
	}

	// 5. Generic File / Document: "input_file", "file"
	if t == "input_file" || t == "file" {
		var rawURL string
		if fu, ok := m["file_url"].(string); ok {
			rawURL = fu
		} else if u, ok := m["url"].(string); ok {
			rawURL = u
		} else if data, ok := m["data"].(string); ok {
			rawURL = data
		}
		defaultMime := "application/pdf"
		if mime, ok := m["mime_type"].(string); ok && mime != "" {
			defaultMime = mime
		}
		inline := parseDataURLOrMedia(rawURL, defaultMime)
		if inline != nil {
			return &GeminiPart{InlineData: inline}
		}
		return nil
	}

	// Fallback to text if present
	if txt, ok := m["text"].(string); ok && txt != "" {
		return &GeminiPart{Text: txt}
	}

	return nil
}

func extractPartsFromContent(v interface{}) []GeminiPart {
	if v == nil {
		return nil
	}

	var parts []GeminiPart

	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, GeminiPart{Text: s})
		}
		return parts
	}

	if m, ok := v.(map[string]interface{}); ok {
		part := parseSingleMapToGeminiPart(m)
		if part != nil {
			parts = append(parts, *part)
		}
		return parts
	}

	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				if strings.TrimSpace(s) != "" {
					parts = append(parts, GeminiPart{Text: s})
				}
			} else if m, ok := item.(map[string]interface{}); ok {
				part := parseSingleMapToGeminiPart(m)
				if part != nil {
					parts = append(parts, *part)
				}
			}
		}
	}

	return parts
}

func hasFunctionCall(parts []GeminiPart) bool {
	for _, p := range parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}

func hasFunctionResponse(parts []GeminiPart) bool {
	for _, p := range parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

type ProtocolContext struct {
	PID         int
	ProcessName string
	Account     string
}

const OfficialAntigravityUserAgent = "antigravity/cli/1.2.7 (aidev_client; os_type=windows; arch=amd64; cl=980147163; auth_method=consumer)"

var (
	stealthSessionMutex sync.Mutex
	stealthSessionMap   = make(map[int]string)
)

// getStealthSessionID, istemci süreci (PID) için tutarlı ancak hesaplar arasında çakışmayan gerçekçi
// 64-bitlik negatif bir int64 oturum kimliği üretir (Google telemetri korelasyonunu engeller).
func getStealthSessionID(pid int, customSessionID string) string {
	if customSessionID != "" {
		return customSessionID
	}
	if pid > 0 {
		stealthSessionMutex.Lock()
		defer stealthSessionMutex.Unlock()
		if sID, ok := stealthSessionMap[pid]; ok && sID != "" {
			return sID
		}
		newSID := fmt.Sprintf("-%d", 1000000000000000000+rand.Int63n(8000000000000000000))
		stealthSessionMap[pid] = newSID
		return newSID
	}
	return fmt.Sprintf("-%d", 1000000000000000000+rand.Int63n(8000000000000000000))
}

// GetStealthSessionID, istemci PID'si için kayıtlı veya yeni üretilen oturum kimliğini döner.
func GetStealthSessionID(pid int) string {
	return getStealthSessionID(pid, "")
}

// GetAllStealthSessionIDs, sistemdeki tüm aktif PID -> SessionID eşleşmelerini döner.
func GetAllStealthSessionIDs() map[int]string {
	stealthSessionMutex.Lock()
	defer stealthSessionMutex.Unlock()
	copyMap := make(map[int]string, len(stealthSessionMap))
	for k, v := range stealthSessionMap {
		copyMap[k] = v
	}
	return copyMap
}

// ConvertOpenAiRequestToGemini processes OpenAI JSON request into Google AIP-136 format
func ConvertOpenAiRequestToGemini(rawBody []byte, customSessionID string, ctx ...ProtocolContext) (*GeminiAipPayload, bool, string, int, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, false, "", 0, fmt.Errorf("invalid JSON body: %w", err)
	}

	isResponsesAPI := false
	var incomingItems []interface{}

	if input, ok := body["input"]; ok && input != nil {
		isResponsesAPI = true
		if arr, ok := input.([]interface{}); ok {
			incomingItems = arr
		} else if s, ok := input.(string); ok {
			incomingItems = []interface{}{
				map[string]interface{}{"role": "user", "content": s},
			}
		}
	} else if msgs, ok := body["messages"].([]interface{}); ok {
		incomingItems = msgs
	}

	var extractedSystemPrompt string
	if inst, ok := body["instructions"].(string); ok && strings.TrimSpace(inst) != "" {
		extractedSystemPrompt = strings.TrimSpace(inst)
	}

	var contents []GeminiContent
	toolCallNames := make(map[string]string)

	for _, rawItem := range incomingItems {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := item["role"].(string)
		itemType, _ := item["type"].(string)

		// 1. System / Developer Prompt
		if role == "system" || role == "developer" {
			text := extractStringFromContent(item["content"])
			if text != "" {
				if extractedSystemPrompt != "" {
					extractedSystemPrompt += "\n\n" + text
				} else {
					extractedSystemPrompt = text
				}
			}
			continue
		}

		// 2. User Message
		if role == "user" {
			parts := extractPartsFromContent(item["content"])
			if len(parts) == 0 {
				text := extractStringFromContent(item["content"])
				if text != "" {
					parts = []GeminiPart{{Text: text}}
				}
			}
			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  "user",
					Parts: parts,
				})
			}
			continue
		}

		// Direct Responses API input item (input_text, input_image, input_video, etc.)
		if itemType == "input_text" || itemType == "input_image" || itemType == "image_url" ||
			itemType == "input_video" || itemType == "video_url" || itemType == "input_file" ||
			itemType == "file" || itemType == "input_audio" || itemType == "audio_url" {
			if part := parseSingleMapToGeminiPart(item); part != nil {
				contents = append(contents, GeminiContent{
					Role:  "user",
					Parts: []GeminiPart{*part},
				})
				continue
			}
		}

		// 3. Assistant / Model Message
		if role == "assistant" || role == "model" {
			var parts []GeminiPart
			if content := item["content"]; content != nil {
				text := extractStringFromContent(content)
				if text != "" {
					parts = append(parts, GeminiPart{Text: text})
				}
			}

			// Tool calls in chat completions
			if tcArr, ok := item["tool_calls"].([]interface{}); ok {
				for _, rawTc := range tcArr {
					tc, ok := rawTc.(map[string]interface{})
					if !ok {
						continue
					}
					callID, _ := tc["id"].(string)
					fn, _ := tc["function"].(map[string]interface{})
					name, _ := fn["name"].(string)
					toolCallNames[callID] = name

					argsMap := make(map[string]interface{})
					if argStr, ok := fn["arguments"].(string); ok {
						_ = json.Unmarshal([]byte(argStr), &argsMap)
					}

					part := GeminiPart{
						FunctionCall: &GeminiFunctionCall{
							ID:   callID,
							Name: name,
							Args: argsMap,
						},
					}
					clientSig, _ := fn["thought_signature"].(string)
					if clientSig == "" {
						clientSig, _ = fn["thoughtSignature"].(string)
					}
					sig := clientSig
					if sig == "" {
						sig = GlobalThoughtStore.Get(callID, name)
					}

					// 🛡️ AKILLI KURTARMA: Eğer gerçek bir Google kriptografik imzası yoksa (>= 80 karakter),
					// Google CloudCode 400 'Corrupted thought signature' veya 'missing thought_signature' hatası verir.
					// Bu eski adımı modelin geçmiş metin çıktısı olarak güvenle temsil ediyoruz:
					if len(sig) >= 80 {
						part.ThoughtSignature = sig
					} else {
						argsBytes, _ := json.Marshal(argsMap)
						part = GeminiPart{
							Text: fmt.Sprintf("[Araç Çağrısı: %s(%s)]", name, string(argsBytes)),
						}

						// 🟢 Geri Bildirim / Kurtarma Logu
						cPID := 0
						cProc := "İstemci"
						cAcc := ""
						if len(ctx) > 0 {
							cPID = ctx[0].PID
							cProc = ctx[0].ProcessName
							cAcc = ctx[0].Account
						}
						GlobalDiagnosticLogger.LogRescue(
							cPID,
							cProc,
							cAcc,
							"",
							"Düşünce İmzası Kurtarma (Degradation)",
							fmt.Sprintf("'%s' araç çağrısı geçerli kriptografik imza içermediği için güvenli metin formatına dönüştürüldü (HTTP 400 engellendi).", name),
							map[string]interface{}{
								"tool_name": name,
								"call_id":   callID,
								"mechanism": "Graceful Text Degradation",
							},
						)
					}
					parts = append(parts, part)
				}
			}

			if len(parts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  "model",
					Parts: parts,
				})
			}
			continue
		}

		// 4. OpenAI Responses API function_call item
		if itemType == "function_call" {
			callID, _ := item["call_id"].(string)
			if callID == "" {
				callID, _ = item["id"].(string)
			}
			name, _ := item["name"].(string)
			toolCallNames[callID] = name

			argsMap := make(map[string]interface{})
			if argStr, ok := item["arguments"].(string); ok {
				_ = json.Unmarshal([]byte(argStr), &argsMap)
			}

			part := GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					ID:   callID,
					Name: name,
					Args: argsMap,
				},
			}
			clientSig, _ := item["thought_signature"].(string)
			if clientSig == "" {
				clientSig, _ = item["thoughtSignature"].(string)
			}
			sig := clientSig
			if sig == "" {
				sig = GlobalThoughtStore.Get(callID, name)
			}

			// 🛡️ AKILLI KURTARMA: Gerçek Google imzası yoksa metne dönüştür
			if len(sig) >= 80 {
				part.ThoughtSignature = sig
			} else {
				argsBytes, _ := json.Marshal(argsMap)
				part = GeminiPart{
					Text: fmt.Sprintf("[Araç Çağrısı: %s(%s)]", name, string(argsBytes)),
				}

				// 🟢 Geri Bildirim / Kurtarma Logu
				cPID := 0
				cProc := "İstemci"
				cAcc := ""
				if len(ctx) > 0 {
					cPID = ctx[0].PID
					cProc = ctx[0].ProcessName
					cAcc = ctx[0].Account
				}
				GlobalDiagnosticLogger.LogRescue(
					cPID,
					cProc,
					cAcc,
					"",
					"Düşünce İmzası Kurtarma (Degradation)",
					fmt.Sprintf("'%s' araç çağrısı geçerli kriptografik imza içermediği için güvenli metin formatına dönüştürüldü (HTTP 400 engellendi).", name),
					map[string]interface{}{
						"tool_name": name,
						"call_id":   callID,
						"mechanism": "Graceful Text Degradation",
					},
				)
			}

			contents = append(contents, GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{part},
			})
			continue
		}

		// 5. OpenAI Responses API function_call_output OR Chat Completions 'tool'
		if itemType == "function_call_output" || role == "tool" {
			callID, _ := item["call_id"].(string)
			if callID == "" {
				callID, _ = item["tool_call_id"].(string)
			}
			if callID == "" {
				callID, _ = item["id"].(string)
			}

			outputStr := ""
			var toolMediaParts []GeminiPart

			rawOutput := item["output"]
			if rawOutput == nil {
				rawOutput = item["content"]
			}

			if arr, ok := rawOutput.([]interface{}); ok {
				for _, p := range extractPartsFromContent(arr) {
					if p.InlineData != nil || p.FileData != nil {
						toolMediaParts = append(toolMediaParts, p)
					}
				}
			} else if m, ok := rawOutput.(map[string]interface{}); ok {
				if p := parseSingleMapToGeminiPart(m); p != nil && (p.InlineData != nil || p.FileData != nil) {
					toolMediaParts = append(toolMediaParts, *p)
				}
			}

			if out, ok := item["output"].(string); ok {
				outputStr = out
			} else if out, ok := item["content"].(string); ok {
				outputStr = out
			} else if item["output"] != nil {
				b, _ := json.Marshal(item["output"])
				outputStr = string(b)
			} else if item["content"] != nil {
				b, _ := json.Marshal(item["content"])
				outputStr = string(b)
			}

			name := toolCallNames[callID]
			if name == "" {
				name, _ = item["name"].(string)
			}
			if name == "" {
				name = "tool"
			}

			sig := GlobalThoughtStore.Get(callID, name)
			// Eğer ilgili araç çağrısı imzasızdıysa, sonucunu da güvenle metin olarak ekle
			if len(sig) < 80 {
				contents = append(contents, GeminiContent{
					Role: "user",
					Parts: []GeminiPart{
						{
							Text: fmt.Sprintf("[Araç Çıktısı (%s)]:\n%s", name, outputStr),
						},
					},
				})
				if len(toolMediaParts) > 0 {
					contents = append(contents, GeminiContent{
						Role:  "user",
						Parts: toolMediaParts,
					})
				}
				continue
			}

			// Google CloudCode / Gemini API: Araç yanıtları (functionResponse) istemci tarafından 'user' rolü ile iletilir
			contents = append(contents, GeminiContent{
				Role: "user",
				Parts: []GeminiPart{
					{
						FunctionResponse: &GeminiFunctionResponse{
							ID:       callID,
							Name:     name,
							Response: map[string]interface{}{"output": outputStr},
						},
					},
				},
			})

			// Araç bir görsel/medya ürettiyse (örn: ekran görüntüsü veya resim okuma), bunu bir sonraki kullanıcı turunda modele ilet
			if len(toolMediaParts) > 0 {
				contents = append(contents, GeminiContent{
					Role:  "user",
					Parts: toolMediaParts,
				})
			}
			continue
		}
	}

	// Final System Prompt: Sadece istekte sistem istemi varsa iletilir (Harici hiçbir enjeksiyon yapılmaz)
	finalSystemPrompt := extractedSystemPrompt

	// Reasoning Effort: 4 Farklı Mod
	// 1. high: thinkingBudget = -1, includeThoughts = true
	// 2. low: thinkingBudget = -1, includeThoughts = true (veya model low seçilebilir)
	// 3. off: thinkingBudget = 0, includeThoughts = false
	// 4. dynamic / auto: reasoning bloğu yoksa thinkingConfig gönderilmez veya varsayılan bırakılır
	hasReasoningConfig := false
	effort := ""
	if r, ok := body["reasoning"].(map[string]interface{}); ok {
		hasReasoningConfig = true
		if eff, ok := r["effort"].(string); ok && eff != "" {
			effort = strings.ToLower(strings.TrimSpace(eff))
		}
	} else if eff, ok := body["reasoning_effort"].(string); ok && eff != "" {
		hasReasoningConfig = true
		effort = strings.ToLower(strings.TrimSpace(eff))
	}

	if th, ok := body["thinking"].(map[string]interface{}); ok {
		hasReasoningConfig = true
		if tType, ok := th["type"].(string); ok && tType == "disabled" {
			effort = "off"
		}
	}

	maxTokens := 65536
	if mt, ok := body["max_output_tokens"].(float64); ok && mt > 0 {
		maxTokens = int(mt)
	} else if mt, ok := body["max_tokens"].(float64); ok && mt > 0 {
		maxTokens = int(mt)
	}
	if maxTokens > 65536 {
		maxTokens = 65536
	}
	if maxTokens < 128 {
		effort = "off"
		hasReasoningConfig = true
	}

	// Target Model: İstekten gelen modele sadık kal, belirtilmemişse varsayılan gemini-3.8-flash-medium
	reqModel, _ := body["model"].(string)
	reqModelLower := strings.ToLower(strings.TrimSpace(reqModel))
	targetModel := "gemini-3.8-flash-medium"

	if strings.Contains(reqModelLower, "flash-high") {
		targetModel = "gemini-3.8-flash-high"
	} else if strings.Contains(reqModelLower, "flash-low") {
		targetModel = "gemini-3.8-flash-low"
	} else if strings.Contains(reqModelLower, "flash-medium") {
		targetModel = "gemini-3.8-flash-medium"
	} else if reqModelLower != "" {
		targetModel = reqModel
	}

	// ⚡ DASHBOARD OVERRIDE KONTROLÜ:
	// Eğer kullanıcı Dashboard'dan "Zorunlu Override" seçtiyse, gelen OpenAI isteğindeki model ve effort çöpe atılır
	if GlobalSettingsManager != nil {
		ov := GlobalSettingsManager.Get()
		if ov.OverrideEnabled {
			if ov.TargetModel != "" {
				targetModel = ov.TargetModel
			}
			if ov.ThinkingEffort != "" {
				effort = ov.ThinkingEffort
				hasReasoningConfig = true
			}
		}
	}

	// 4 Düşünme Modunun Yapılandırılması:
	var thinkingConfig *GeminiThinkingConfig
	if hasReasoningConfig && effort != "" && effort != "auto" && effort != "dynamic" {
		if effort == "off" || effort == "none" || effort == "disabled" {
			thinkingConfig = &GeminiThinkingConfig{
				IncludeThoughts: false,
				ThinkingBudget:  0,
			}
		} else {
			// high, medium, low
			thinkingConfig = &GeminiThinkingConfig{
				IncludeThoughts: true,
				ThinkingBudget:  -1,
			}
		}
	} else {
		// Dinamik Düşünme (Varsayılan / Modelin Serbest Kararı): reasoning bloğu yoksa thinkingConfig eklenmez
		thinkingConfig = nil
	}

	// Tools conversion
	var geminiTools []GeminiTool
	toolsRaw := body["tools"]
	if toolsRaw == nil {
		toolsRaw = body["functions"]
	}
	if toolsArr, ok := toolsRaw.([]interface{}); ok && len(toolsArr) > 0 {
		var decls []GeminiFunctionDeclaration
		for _, rawT := range toolsArr {
			t, ok := rawT.(map[string]interface{})
			if !ok {
				continue
			}
			fn, _ := t["function"].(map[string]interface{})
			if fn == nil {
				fn = t
			}
			name, _ := fn["name"].(string)
			if name == "" {
				continue
			}
			desc, _ := fn["description"].(string)
			params, _ := fn["parameters"].(map[string]interface{})
			if params == nil {
				params, _ = fn["input_schema"].(map[string]interface{})
			}

			decls = append(decls, GeminiFunctionDeclaration{
				Name:        name,
				Description: desc,
				Parameters:  ConvertSchemaTypeToGemini(params),
			})
		}

		// Sort declarations alphabetically (RFC 8785)
		sort.Slice(decls, func(i, j int) bool {
			return decls[i].Name < decls[j].Name
		})

		if len(decls) > 0 {
			geminiTools = []GeminiTool{{FunctionDeclarations: decls}}
		}
	}

	// Merge adjacent turns with identical roles (AIP-136 requirement)
	// Important: Do not merge a turn with FunctionCall and a turn with FunctionResponse
	var mergedContents []GeminiContent
	for _, c := range contents {
		if len(mergedContents) > 0 && mergedContents[len(mergedContents)-1].Role == c.Role {
			prev := mergedContents[len(mergedContents)-1]
			if (hasFunctionCall(prev.Parts) && hasFunctionResponse(c.Parts)) ||
				(hasFunctionResponse(prev.Parts) && hasFunctionCall(c.Parts)) {
				mergedContents = append(mergedContents, GeminiContent{
					Role:  c.Role,
					Parts: append([]GeminiPart{}, c.Parts...),
				})
			} else {
				mergedContents[len(mergedContents)-1].Parts = append(mergedContents[len(mergedContents)-1].Parts, c.Parts...)
			}
		} else {
			mergedContents = append(mergedContents, GeminiContent{
				Role:  c.Role,
				Parts: append([]GeminiPart{}, c.Parts...),
			})
		}
	}

	// 🛡️ Fail-Safe Guards: AIP-136 ve Google CloudCode Kural Denetimleri
	// 1. İstek asla 'model' turu ile başlayamaz (Google kuralı: İlk tur 'user' olmalıdır)
	if len(mergedContents) > 0 && mergedContents[0].Role == "model" {
		mergedContents = append([]GeminiContent{
			{
				Role:  "user",
				Parts: []GeminiPart{{Text: "Hello."}},
			},
		}, mergedContents...)
	}

	// 2. İstek ASLA 'model' turu ile bitemez (Google 400: "Requests ending with a model turn are not supported")
	if len(mergedContents) == 0 {
		mergedContents = append(mergedContents, GeminiContent{
			Role:  "user",
			Parts: []GeminiPart{{Text: "Hello"}},
		})
	} else if mergedContents[len(mergedContents)-1].Role == "model" {
		lastIdx := len(mergedContents) - 1
		// Eğer son tur yalnızca functionResponse içeriyorsa, rolünü 'user' yap
		if hasFunctionResponse(mergedContents[lastIdx].Parts) && !hasFunctionCall(mergedContents[lastIdx].Parts) {
			mergedContents[lastIdx].Role = "user"
			if lastIdx > 0 && mergedContents[lastIdx-1].Role == "user" {
				mergedContents[lastIdx-1].Parts = append(mergedContents[lastIdx-1].Parts, mergedContents[lastIdx].Parts...)
				mergedContents = mergedContents[:lastIdx]
			}
		} else {
			// Model metni, düşünce veya functionCall ile bitiyorsa, modelin yanıt üretebilmesi için kullanıcı devam turu ekle
			mergedContents = append(mergedContents, GeminiContent{
				Role:  "user",
				Parts: []GeminiPart{{Text: "Continue."}},
			})
		}

		// Kurtarma Logu: HTTP 400'ün nasıl önlendiğini panele bildir
		cPID := 0
		cProc := "İstemci"
		cAcc := ""
		if len(ctx) > 0 {
			cPID = ctx[0].PID
			cProc = ctx[0].ProcessName
			cAcc = ctx[0].Account
		}
		if GlobalDiagnosticLogger != nil {
			GlobalDiagnosticLogger.LogRescue(
				cPID,
				cProc,
				cAcc,
				"",
				"Model Turu Bitişi Düzeltme (Role Guard)",
				"İstek 'model' turu ile sonlandığı için Google CloudCode HTTP 400 hatası önlendi ve 'user' devam turu ile güvenli hale getirildi.",
				map[string]interface{}{
					"mechanism": "Model Turn Termination Guard",
				},
			)
		}
	}

	pid := 0
	if len(ctx) > 0 {
		pid = ctx[0].PID
	}
	sessionID := getStealthSessionID(pid, customSessionID)

	temp := 0.7
	if t, ok := body["temperature"].(float64); ok {
		temp = t
	}
	topP := 0.95
	if tp, ok := body["top_p"].(float64); ok {
		topP = tp
	}

	reqObj := GeminiInnerRequest{
		Contents: mergedContents,
		GenerationConfig: GeminiGenerationConfig{
			MaxOutputTokens: maxTokens,
			Temperature:     temp,
			TopP:            topP,
			ThinkingConfig:  thinkingConfig,
		},
		SessionID: sessionID,
		Tools:     geminiTools,
	}

	if strings.TrimSpace(finalSystemPrompt) != "" {
		reqObj.SystemInstruction = &GeminiSystemInstruction{
			Role:  "user",
			Parts: []GeminiPart{{Text: finalSystemPrompt}},
		}
	}

	randSuffix := fmt.Sprintf("%06x", rand.Intn(0xffffff))
	reqID := fmt.Sprintf("agent/antigravity-%d-%s/1", time.Now().UnixMilli(), randSuffix)

	aipPayload := &GeminiAipPayload{
		Project:     "aicode-consumers",
		RequestID:   reqID,
		Model:       targetModel,
		UserAgent:   "antigravity",
		RequestType: "agent",
		Request:     reqObj,
	}

	thinkingBudgetInt := -1
	if thinkingConfig != nil {
		thinkingBudgetInt = thinkingConfig.ThinkingBudget
	}

	return aipPayload, isResponsesAPI, targetModel, thinkingBudgetInt, nil
}
