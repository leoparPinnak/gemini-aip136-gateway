package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	Args map[string]interface{} `json:"-"`
	// ArgsRaw, Google'ın ürettiği args JSON'unun BAYT-HALİDİR (cache-sadakat).
	// Geçmişe (history) geri yazılırken anahtar sırası bozulmadan aynen basılır;
	// böylece sonraki turun prefix'i Google'ın önbellekteki token dizisiyle birebir örtüşür.
	ArgsRaw json.RawMessage `json:"-"`
}

// MarshalJSON, args alanını modelin ürettiği ham JSON ile bayt-bayt aynı basar.
// ArgsRaw yoksa Args haritası (alfabetik sıralı) basılır — önceki davranışla birebir.
func (fc GeminiFunctionCall) MarshalJSON() ([]byte, error) {
	type callAlias struct {
		ID   string          `json:"id,omitempty"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	raw := fc.ArgsRaw
	if len(raw) == 0 {
		b, err := json.Marshal(fc.Args)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(callAlias{ID: fc.ID, Name: fc.Name, Args: raw})
}

// UnmarshalJSON, gelen functionCall part'ında args'un ham baytlarını da yakalar.
func (fc *GeminiFunctionCall) UnmarshalJSON(data []byte) error {
	type callAlias struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	var a callAlias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	fc.ID = a.ID
	fc.Name = a.Name
	fc.ArgsRaw = a.Args
	fc.Args = nil
	if len(a.Args) > 0 {
		_ = json.Unmarshal(a.Args, &fc.Args)
	}
	return nil
}

// ArgsJSON, args'un her koşulda JSON metni halini döndürür (ham varsa ham, yoksa sıralı).
func (fc *GeminiFunctionCall) ArgsJSON() string {
	if len(fc.ArgsRaw) > 0 {
		return string(fc.ArgsRaw)
	}
	b, err := json.Marshal(fc.Args)
	if err != nil {
		return "{}"
	}
	return string(b)
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
	// OpenAI istek parametreleri (opt-in passthrough — YALNIZCA istemci gönderirse
	// basılır; varsayılan zarf bayt-bayt değişmez → cache korunur).
	StopSequences    []string               `json:"stopSequences,omitempty"`
	Seed             *int                   `json:"seed,omitempty"`
	FrequencyPenalty *float64               `json:"frequencyPenalty,omitempty"`
	PresencePenalty  *float64               `json:"presencePenalty,omitempty"`
	ResponseMimeType string                 `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]interface{} `json:"responseSchema,omitempty"`
}

// GeminiFunctionCallingConfig, OpenAI tool_choice karşılığıdır
// (auto→yok, none→NONE, required→ANY, {"function":{"name"}}→ANY+allowedFunctionNames).
type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type GeminiToolConfig struct {
	FunctionCallingConfig *GeminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
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
	ToolConfig        *GeminiToolConfig        `json:"toolConfig,omitempty"`
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

// ---------------------------------------------------------------------------
// B3: Medya kararlılığı — mediaCache (path/URL → son iyi baytlar)
//
// Eski davranış: her istekte dosya/URL yeniden okunur, hatada nil dönüp parça
// SESSİZCE düşerdi → sonraki turda parça gelince prefix kırılır, cache miss
// olurdu. Yeni davranış:
//   - dosya her zaman okunur (değişiklik algılanır); hash aynıysa base64
//     yeniden hesaplanmaz (determinizm + CPU kazancı),
//   - okuma/indirme hatasında son iyi değer kullanılır (prefix sabit),
//   - hiç alınmamışsa HATA döner (dürüst hata; Convert 400 ile döner).
// ---------------------------------------------------------------------------

type mediaCacheEntry struct {
	Data      []byte // ham bayt
	Mime      string
	Hash      string
	Timestamp time.Time
}

var mediaCache sync.Map // "f:"+path | "u:"+url → mediaCacheEntry

func mediaHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func parseDataURLOrMedia(rawURL string, defaultMime string) (*GeminiInlineData, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("medya kaynağı boş (url/data alanı eksik)")
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
			}, nil
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
			}, nil
		}
	}

	// 4. Remote HTTP/HTTPS URL → parseRemoteMedia (son iyi değer + hata B3)
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return parseRemoteMedia(rawURL, defaultMime, "u:"+rawURL)
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

	cacheKey := "f:" + filePath
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
		hash := mediaHash(fileBytes)
		if v, ok := mediaCache.Load(cacheKey); ok {
			e := v.(mediaCacheEntry)
			if e.Hash == hash && e.Mime == mime && len(e.Data) == len(fileBytes) {
				// Bayt değişmedi → cache'lenmiş base64'ü dön (yeniden hesaplama yok)
				return &GeminiInlineData{MimeType: mime, Data: base64.StdEncoding.EncodeToString(e.Data)}, nil
			}
		}
		mediaCache.Store(cacheKey, mediaCacheEntry{Data: fileBytes, Mime: mime, Hash: hash, Timestamp: time.Now()})
		return &GeminiInlineData{
			MimeType: mime,
			Data:     base64.StdEncoding.EncodeToString(fileBytes),
		}, nil
	}
	// Dosya okunamadı → son iyi değer korunur (prefix sabit); hiç yoksa hata (B3).
	if v, ok := mediaCache.Load(cacheKey); ok {
		e := v.(mediaCacheEntry)
		log.Printf("[Media] ⚠️ dosya okunamadı, SON İYİ DEĞER kullanılıyor: %s", filePath)
		GlobalReqLog.LogEvent("media_stale", map[string]interface{}{"src": filePath, "reason": "file_read_failed"})
		return &GeminiInlineData{MimeType: e.Mime, Data: base64.StdEncoding.EncodeToString(e.Data)}, nil
	}
	return nil, fmt.Errorf("medya dosyası okunamadı: %s", filePath)
}

// parseRemoteMedia 4. Remote HTTP/HTTPS URL — hata ve son-iyi-değer B3 mantığı.
func parseRemoteMedia(rawURL, defaultMime, cacheKey string) (*GeminiInlineData, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rawURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		b, rerr := io.ReadAll(resp.Body)
		if rerr == nil && len(b) > 0 {
			mime := resp.Header.Get("Content-Type")
			if mime == "" {
				mime = defaultMime
			}
			if mime == "" {
				mime = http.DetectContentType(b)
			}
			mediaCache.Store(cacheKey, mediaCacheEntry{Data: b, Mime: mime, Hash: mediaHash(b), Timestamp: time.Now()})
			return &GeminiInlineData{
				MimeType: mime,
				Data:     base64.StdEncoding.EncodeToString(b),
			}, nil
		}
	}
	if v, ok := mediaCache.Load(cacheKey); ok {
		e := v.(mediaCacheEntry)
		log.Printf("[Media] ⚠️ indirme başarısız, SON İYİ DEĞER kullanılıyor: %s", rawURL)
		GlobalReqLog.LogEvent("media_stale", map[string]interface{}{"src": rawURL, "reason": "http_fetch_failed"})
		return &GeminiInlineData{MimeType: e.Mime, Data: base64.StdEncoding.EncodeToString(e.Data)}, nil
	}
	return nil, fmt.Errorf("medya indirilemedi: %s", rawURL)
}

func parseSingleMapToGeminiPart(m map[string]interface{}) (*GeminiPart, error) {
	t, _ := m["type"].(string)

	// 1. Text types
	if t == "text" || t == "input_text" || t == "output_text" {
		text, _ := m["text"].(string)
		if text == "" {
			text, _ = m["input_text"].(string)
		}
		if text != "" {
			return &GeminiPart{Text: text}, nil
		}
		return nil, nil
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

		inline, mErr := parseDataURLOrMedia(rawURL, defaultMime)
		if mErr != nil {
			return nil, mErr
		}
		if inline != nil {
			return &GeminiPart{InlineData: inline}, nil
		}
		return nil, nil
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
		inline, mErr := parseDataURLOrMedia(rawURL, defaultMime)
		if mErr != nil {
			return nil, mErr
		}
		if inline != nil {
			return &GeminiPart{InlineData: inline}, nil
		}
		return nil, nil
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
		inline, mErr := parseDataURLOrMedia(rawURL, defaultMime)
		if mErr != nil {
			return nil, mErr
		}
		if inline != nil {
			return &GeminiPart{InlineData: inline}, nil
		}
		return nil, nil
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
		inline, mErr := parseDataURLOrMedia(rawURL, defaultMime)
		if mErr != nil {
			return nil, mErr
		}
		if inline != nil {
			return &GeminiPart{InlineData: inline}, nil
		}
		return nil, nil
	}

	// Fallback to text if present
	if txt, ok := m["text"].(string); ok && txt != "" {
		return &GeminiPart{Text: txt}, nil
	}

	return nil, nil
}

func extractPartsFromContent(v interface{}) ([]GeminiPart, error) {
	if v == nil {
		return nil, nil
	}

	var parts []GeminiPart

	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, GeminiPart{Text: s})
		}
		return parts, nil
	}

	if m, ok := v.(map[string]interface{}); ok {
		part, err := parseSingleMapToGeminiPart(m)
		if err != nil {
			return nil, err
		}
		if part != nil {
			parts = append(parts, *part)
		}
		return parts, nil
	}

	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				if strings.TrimSpace(s) != "" {
					parts = append(parts, GeminiPart{Text: s})
				}
			} else if m, ok := item.(map[string]interface{}); ok {
				part, err := parseSingleMapToGeminiPart(m)
				if err != nil {
					return nil, err
				}
				if part != nil {
					parts = append(parts, *part)
				}
			}
		}
	}

	return parts, nil
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

// NarrationRuleText, SEÇİCİ durum güncellemesi (narration) kuralıdır — istek
// anında systemInstruction'a eklenir. Metin SABİTTİR (cache-sadakat: her istekte
// bayt-bayt aynı). Davranış, mimo-v2.6-flash-free oturum analizinden çıkarıldı
// (DSH session-1e5839fb, 88 mesaj): her araçta DEĞİL, yalnız kullanıcı için
// değer taşıyan yerde görünür cümle (plan / kanıt-bulgu / anormallik / tehlike /
// milestone); rutin adım, tekrar ve doğrulama SESSİZ. İSTEMCİ kuralı zaten
// taşıyorsa (yeni marker veya eski iki kuralın izi) ikinci basım engellenir.
const NarrationRuleText = "Selective status updates: do NOT narrate every tool call. Write a short visible message (in the user's language) ONLY when the user genuinely benefits: (1) a brief plan when starting or resuming a multi-step task, (2) a notable finding, anomaly, error, or danger worth warning about, (3) a milestone or result worth confirming (example: \"Bug caught live: the RPC never left the client.\"), or (4) a change of approach with its reason. Routine steps, retries, measurements, and verifications must run SILENTLY - no narration per tool call. Keep it 1-2 sentences as regular visible output text, never inside your thinking block. If nothing notable changed, say nothing."

// NarrationRuleMarker, kuralın varlığını saptayan benzersiz öbeğidir.
const NarrationRuleMarker = "Selective status updates: do NOT narrate every tool call"

// NarrationRuleMarkerLegacy, eski kural izleridir (araç-başına duyuru ve
// reasoning-başına tek plan) — istemci hâlâ onları taşıyorsa yeni kural
// üstüne eklenmez (çelişki önlenir).
const NarrationRuleMarkerLegacy = "Tool-call status updates"

// NarrationRuleMarkerLegacyPlan, ikinci nesil (reasoning-başına tek plan) kuralın izi.
const NarrationRuleMarkerLegacyPlan = "Plan announcement (one per reasoning cycle)"

// isCompactionSummaryRequest, DSH compaction özetleyici çağrısını tanır.
// Motor talimatı ("acting as a compaction engine") yalnızca özetleyici
// isteğinde bulunur; geçmişe (checkpoint metni) YAZILMAZ — bu yüzden
// normal turlarda tetiklenmez. Kanıt: dump resp_1790641020670'de kural bu
// isteğin systemInstruction'ında, içerikte özetleyici talimatı vardı.
func isCompactionSummaryRequest(contents []GeminiContent) bool {
	if len(contents) == 0 {
		return false
	}
	for _, p := range contents[0].Parts {
		if strings.Contains(p.Text, "acting as a compaction engine") ||
			strings.Contains(p.Text, "Output EXACTLY the Markdown structure") {
			return true
		}
	}
	return false
}

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
	key := pid
	if pid <= 0 {
		// B1: PID çözülemediğinde HER istekte rastgele SID üretilmesi,
		// oturum/worker affinitesini kırıp cache miss üretiyordu (A1 hipotezi).
		// pid=0 için tek sabit giriş kullanılır.
		key = 0
	}
	stealthSessionMutex.Lock()
	defer stealthSessionMutex.Unlock()
	if sID, ok := stealthSessionMap[key]; ok && sID != "" {
		return sID
	}
	newSID := fmt.Sprintf("-%d", 1000000000000000000+rand.Int63n(8000000000000000000))
	stealthSessionMap[key] = newSID
	sessionsDirty() // B1 kalıcılığı: restart SID'leri korur
	return newSID
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
			parts, contentErr := extractPartsFromContent(item["content"])
			if contentErr != nil {
				return nil, false, "", 0, fmt.Errorf("medya ayrıştırma hatası: %w", contentErr)
			}
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
			if part, partErr := parseSingleMapToGeminiPart(item); partErr != nil {
				return nil, false, "", 0, fmt.Errorf("medya ayrıştırma hatası: %w", partErr)
			} else if part != nil {
				contents = append(contents, GeminiContent{
					Role:  "user",
					Parts: []GeminiPart{*part},
				})
				continue
			}
		}

		// 2b. OpenAI Responses API reasoning item — düşünce geri beslemesi (round-trip).
		// DSH/pi-ai reasoning item'ını output_item.done'daki haliyle VERBATIM geri gönderir;
		// thoughtSignature burada 'encrypted_content' alanında taşınır (bkz. stream_translator).
		if itemType == "reasoning" {
			sig, _ := item["encrypted_content"].(string)
			if sig == "" {
				sig, _ = item["thought_signature"].(string)
			}
			if sig == "" {
				sig, _ = item["thoughtSignature"].(string)
			}

			var sb strings.Builder
			extracted := false
			for _, key := range []string{"summary", "content"} {
				if extracted {
					break
				}
				if arr, ok := item[key].([]interface{}); ok {
					for _, rawSeg := range arr {
						seg, ok := rawSeg.(map[string]interface{})
						if !ok {
							continue
						}
						if txt, ok := seg["text"].(string); ok && txt != "" {
							sb.WriteString(txt)
							extracted = true
						}
					}
				}
			}
			thoughtText := sb.String()
			if thoughtText == "" {
				thoughtText, _ = item["text"].(string)
			}

			if thoughtText != "" {
				if len(sig) >= 80 {
					// İmzalı düşünce: geçmişe model turunun thought part'ı olarak birebir geri koy
					contents = append(contents, GeminiContent{
						Role: "model",
						Parts: []GeminiPart{{
							Text:             thoughtText,
							Thought:          true,
							ThoughtSignature: sig,
						}},
					})
				} else {
					// İmzasız düşünce parçası Google güvenlik duvarınca reddedilir → güvenle düşür.
					// (Düşünce metni bağlam için gerekli değildir; modelin çıktısı zaten sonraki part'lardadır.)
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
							cPID, cProc, cAcc, "",
							"Düşünce Geri Beslemesi (Reasoning Round-Trip)",
							"Reasoning item geçerli thoughtSignature (encrypted_content) içermediği için imzasız düşünce parçası güvenle düşürüldü (HTTP 400 engellendi).",
							map[string]interface{}{
								"item_id":   item["id"],
								"mechanism": "Reasoning Round-Trip",
							},
						)
					}
				}
			}
			continue
		}

		// 3. Assistant / Model Message
		if role == "assistant" || role == "model" {
			var parts []GeminiPart
			if content := item["content"]; content != nil {
				text := extractStringFromContent(content)
				if text != "" {
					tp := GeminiPart{Text: text}
					// Metin parçası thoughtSignature geri koyması: stream_translator imzayı
					// "txt:<mesaj-item-id>" anahtarıyla saklar; DSH mesaj item id'sini birebir
					// geri gönderdiğinden Google'ın imzası geçmişteki yerine geri konur
					// (KV-cache prefix sadakati + imza zinciri).
					if msgID, ok := item["id"].(string); ok && msgID != "" {
						if ts := GlobalThoughtStore.Get("txt:"+msgID, "text"); len(ts) >= 80 {
							tp.ThoughtSignature = ts
						}
					}
					parts = append(parts, tp)
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
					var rawArgs json.RawMessage
					if argStr, ok := fn["arguments"].(string); ok {
						rawArgs = json.RawMessage(argStr)
						_ = json.Unmarshal([]byte(argStr), &argsMap)
					}

					part := GeminiPart{
						FunctionCall: &GeminiFunctionCall{
							ID:      callID,
							Name:    name,
							Args:    argsMap,
							ArgsRaw: rawArgs,
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
						argsText := (&GeminiFunctionCall{Args: argsMap, ArgsRaw: rawArgs}).ArgsJSON()
						part = GeminiPart{
							Text: fmt.Sprintf("[Araç Çağrısı: %s(%s)]", name, argsText),
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
			var rawArgs json.RawMessage
			if argStr, ok := item["arguments"].(string); ok {
				rawArgs = json.RawMessage(argStr)
				_ = json.Unmarshal([]byte(argStr), &argsMap)
			}

			part := GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					ID:      callID,
					Name:    name,
					Args:    argsMap,
					ArgsRaw: rawArgs,
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
				argsText := (&GeminiFunctionCall{Args: argsMap, ArgsRaw: rawArgs}).ArgsJSON()
				part = GeminiPart{
					Text: fmt.Sprintf("[Araç Çağrısı: %s(%s)]", name, argsText),
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
				outParts, outErr := extractPartsFromContent(arr)
				if outErr != nil {
					return nil, false, "", 0, fmt.Errorf("araç çıktısı medya hatası: %w", outErr)
				}
				for _, p := range outParts {
					if p.InlineData != nil || p.FileData != nil {
						toolMediaParts = append(toolMediaParts, p)
					}
				}
			} else if m, ok := rawOutput.(map[string]interface{}); ok {
				p, pErr := parseSingleMapToGeminiPart(m)
				if pErr != nil {
					return nil, false, "", 0, fmt.Errorf("araç çıktısı medya hatası: %w", pErr)
				}
				if p != nil && (p.InlineData != nil || p.FileData != nil) {
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

	// 6. Durumlu Responses: previous_response_id ile sunucu tarafı sohbet durumu birleştirme.
	// Kayıtlı içerik (önceki turlar + model çıktısı; thoughtSignature'lar ve ham args ile
	// bayt-bayt sadık) yeni input item'larının ÖNÜNE eklenir, sessionId sürdürülür (cache affinity).
	prevRespID, _ := body["previous_response_id"].(string)
	if strings.TrimSpace(prevRespID) != "" {
		prevRespID = strings.TrimSpace(prevRespID)
		if st, ok := GlobalResponseStore.Get(prevRespID); ok {
			contents = append(append([]GeminiContent{}, st.Contents...), contents...)
			if customSessionID == "" {
				customSessionID = st.SessionID
			}
			if extractedSystemPrompt == "" {
				extractedSystemPrompt = st.SystemPrompt
			}
		} else {
			log.Printf("[⚠️ previous_response_id] '%s' bulunamadı (süre dolmuş/silinmiş olabilir); istek yalnızca yeni input ile işlendi.", prevRespID)
		}
	}

	// Final System Prompt: İstekten gelen sistem istemi (kalıcı enjeksiyon yok;
	// tek istisna isteğe bağlı narration kuralıdır — aşağıda, idempotent).
	finalSystemPrompt := extractedSystemPrompt

	// (Narration kuralının enjeksiyonu aşağıda — araç dönüşümünden SONRA;
	//  çünkü kural yalnız araç taşıyan normal ajan turlarına basılır:
	//  araçsız istekler ve compaction özetleyicileri hariç tutulur.)

	// Target Model: İstekten gelen modele sadık kal, belirtilmemişse varsayılan gemini-3.8-flash-medium
	reqModel, _ := body["model"].(string)
	reqModelLower := strings.ToLower(strings.TrimSpace(reqModel))
	targetModel := "gemini-3.8-flash-medium"

	isNoThinking := strings.Contains(reqModelLower, "nothink") ||
		strings.Contains(reqModelLower, "no-think") ||
		strings.Contains(reqModelLower, "no_think") ||
		strings.Contains(reqModelLower, "-off")

	if isNoThinking {
		targetModel = "gemini-3.8-flash-medium"
	} else if strings.Contains(reqModelLower, "flash-high") || strings.Contains(reqModelLower, "high") {
		targetModel = "gemini-3.8-flash-high"
	} else if strings.Contains(reqModelLower, "flash-low") || strings.Contains(reqModelLower, "low") {
		targetModel = "gemini-3.8-flash-low"
	} else if strings.Contains(reqModelLower, "flash-medium") || strings.Contains(reqModelLower, "medium") {
		targetModel = "gemini-3.8-flash-medium"
	} else if reqModelLower != "" {
		targetModel = reqModel
	}

	// Model adından reasoning effort çıkarımı (-high, -medium, -low, nothinking):
	inferredEffort := ""
	if isNoThinking {
		inferredEffort = "off"
	} else if strings.Contains(reqModelLower, "high") {
		inferredEffort = "high"
	} else if strings.Contains(reqModelLower, "low") {
		inferredEffort = "low"
	} else if strings.Contains(reqModelLower, "medium") {
		inferredEffort = "medium"
	}

	// Reasoning Effort: İstekten gelen parametreleri çöz
	effort := ""
	if r, ok := body["reasoning"].(map[string]interface{}); ok {
		if eff, ok := r["effort"].(string); ok && eff != "" {
			effort = strings.ToLower(strings.TrimSpace(eff))
		}
	} else if eff, ok := body["reasoning_effort"].(string); ok && eff != "" {
		effort = strings.ToLower(strings.TrimSpace(eff))
	}

	if th, ok := body["thinking"].(map[string]interface{}); ok {
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
	}

	// 🧠 AKILLI DÜŞÜNME MODU ÇÖZÜMLEME:
	// Eğer model adında seviye belirtilmişse (örn: gemini-3.8-flash-high veya nothinking),
	// modelin kimliğindeki seviye önceliklidir.
	if inferredEffort != "" {
		effort = inferredEffort
	} else if effort == "" {
		// Model adında seviye yoksa ve reasoning parametresi verilmemişse, Gemini 3.8 / 2.5 için
		// düşünmeyi varsayılan olarak açık tut (medium).
		if strings.Contains(targetModel, "gemini-3.8") || strings.Contains(targetModel, "gemini-2.5") {
			effort = "medium"
		}
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
			}
		}
	}

	// 4 Düşünme Modunun Yapılandırılması:
	var thinkingConfig *GeminiThinkingConfig
	if effort == "off" || effort == "none" || effort == "disabled" {
		thinkingConfig = &GeminiThinkingConfig{
			IncludeThoughts: false,
			ThinkingBudget:  0,
		}
	} else {
		// high, medium, low veya modelin varsayılan düşünmesi
		thinkingConfig = &GeminiThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  -1,
		}
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

	// ── OpenAI ek parametreler (opt-in passthrough) ──────────────────────────
	// Yalnızca istemci gönderirse basılır; varsayılan zarf bayt-bayt değişmez → cache korunur.

	// stop → stopSequences
	var stopSequences []string
	switch stopVal := body["stop"].(type) {
	case string:
		if strings.TrimSpace(stopVal) != "" {
			stopSequences = []string{stopVal}
		}
	case []interface{}:
		for _, s := range stopVal {
			if sv, ok := s.(string); ok && strings.TrimSpace(sv) != "" {
				stopSequences = append(stopSequences, sv)
			}
		}
	}

	// seed → seed
	var seedPtr *int
	if sv, ok := body["seed"].(float64); ok {
		si := int(sv)
		seedPtr = &si
	}

	// frequency_penalty / presence_penalty
	var freqPtr, presPtr *float64
	if fv, ok := body["frequency_penalty"].(float64); ok {
		freqPtr = &fv
	}
	if pv, ok := body["presence_penalty"].(float64); ok {
		presPtr = &pv
	}

	// response_format (Chat) veya text.format (Responses) → responseMimeType + responseSchema
	var respMime string
	var respSchema map[string]interface{}
	rf, _ := body["response_format"].(map[string]interface{})
	if rf == nil {
		if txtCfg, ok := body["text"].(map[string]interface{}); ok {
			rf, _ = txtCfg["format"].(map[string]interface{})
		}
	}
	if rf != nil {
		rfType, _ := rf["type"].(string)
		switch rfType {
		case "json_object":
			respMime = "application/json"
		case "json_schema":
			respMime = "application/json"
			if js, ok := rf["json_schema"].(map[string]interface{}); ok {
				if sc, ok := js["schema"].(map[string]interface{}); ok {
					respSchema = ConvertSchemaTypeToGemini(sc)
				}
			}
		}
	}

	// tool_choice → toolConfig.functionCallingConfig (auto → yok; eski davranış korunur)
	var toolCfg *GeminiToolConfig
	if len(geminiTools) > 0 {
		switch tc := body["tool_choice"].(type) {
		case string:
			switch tc {
			case "none":
				toolCfg = &GeminiToolConfig{FunctionCallingConfig: &GeminiFunctionCallingConfig{Mode: "NONE"}}
			case "required", "any":
				toolCfg = &GeminiToolConfig{FunctionCallingConfig: &GeminiFunctionCallingConfig{Mode: "ANY"}}
			}
		case map[string]interface{}:
			if tType, _ := tc["type"].(string); tType == "function" {
				if fn, ok := tc["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						toolCfg = &GeminiToolConfig{FunctionCallingConfig: &GeminiFunctionCallingConfig{
							Mode:                 "ANY",
							AllowedFunctionNames: []string{name},
						}}
					}
				}
			}
		}
	}

	// ── Seçici Durum Güncellemesi (Narration) Kuralı — istek anında enjeksiyon ──
	// Gemini modelleri anlatıyı düşünce kanalına yazma eğilimindedir; bu kural
	// flash-free analizindeki SEÇİCİ davranışı ister: yalnız kanıt/anormallik/
	// tehlike/milestone/plan anlarında görünür cümle, rutin adım sessiz.
	// Hariç tutmalar (kanıtlanmış):
	//  - araçsız istekler: kuralın konusu araç anlatımıdır, anlamsız;
	//  - compaction ÖZETLEYİCİLERİ: kural özet metnine plan/duyuru satırı
	//    karıştırırsa DSH'ın "summary is not smaller" validator'ı sıkıştırmayı
	//    reddeder (09-29 03:16-03:31: 13 hata; kural yayın ÖNCESİ bu hata tipi 0).
	// İSTEMCİ kuralı zaten taşıyorsa (yeni marker veya eski iki kuralın izi)
	// ikinci basım YOK — çelişki de önlenir. Varsayılan AÇIK;
	// override_settings.json → "narration_hint": false ile kapatılır.
	if GlobalSettingsManager.NarrationHintEnabled() &&
		len(geminiTools) > 0 &&
		!isCompactionSummaryRequest(contents) &&
		!strings.Contains(finalSystemPrompt, NarrationRuleMarker) &&
		!strings.Contains(finalSystemPrompt, NarrationRuleMarkerLegacy) &&
		!strings.Contains(finalSystemPrompt, NarrationRuleMarkerLegacyPlan) {
		if strings.TrimSpace(finalSystemPrompt) == "" {
			finalSystemPrompt = NarrationRuleText
		} else {
			finalSystemPrompt = finalSystemPrompt + "\n\n" + NarrationRuleText
		}
	}

	reqObj := GeminiInnerRequest{
		Contents: mergedContents,
		GenerationConfig: GeminiGenerationConfig{
			MaxOutputTokens:  maxTokens,
			Temperature:      temp,
			TopP:             topP,
			ThinkingConfig:   thinkingConfig,
			StopSequences:    stopSequences,
			Seed:             seedPtr,
			FrequencyPenalty: freqPtr,
			PresencePenalty:  presPtr,
			ResponseMimeType: respMime,
			ResponseSchema:   respSchema,
		},
		SessionID:  sessionID,
		Tools:      geminiTools,
		ToolConfig: toolCfg,
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
