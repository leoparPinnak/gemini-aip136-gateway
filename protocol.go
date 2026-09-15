package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// Gemini CloudCode AIP-136 Structures
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
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

// ConvertSchemaTypeToGemini cleans and converts JSON Schema types to UPPERCASE
func ConvertSchemaTypeToGemini(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{"type": "OBJECT", "properties": map[string]interface{}{}}
	}
	cloned := make(map[string]interface{})
	for k, v := range schema {
		cloned[k] = v
	}

	if t, ok := cloned["type"].(string); ok {
		cloned["type"] = strings.ToUpper(t)
	} else if arr, ok := cloned["type"].([]interface{}); ok && len(arr) > 0 {
		if firstStr, ok := arr[0].(string); ok {
			cloned["type"] = strings.ToUpper(firstStr)
		}
	}

	delete(cloned, "$schema")
	delete(cloned, "additionalProperties")
	delete(cloned, "default")

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

	if items, ok := cloned["items"].(map[string]interface{}); ok {
		cloned["items"] = ConvertSchemaTypeToGemini(items)
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

// ConvertOpenAiRequestToGemini processes OpenAI JSON request into Google AIP-136 format
func ConvertOpenAiRequestToGemini(rawBody []byte, customSessionID string) (*GeminiAipPayload, bool, string, int, error) {
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
			text := extractStringFromContent(item["content"])
			contents = append(contents, GeminiContent{
				Role:  "user",
				Parts: []GeminiPart{{Text: text}},
			})
			continue
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
					if sig := GlobalThoughtStore.Get(callID, name); sig != "" {
						part.ThoughtSignature = sig
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
			if sig := GlobalThoughtStore.Get(callID, name); sig != "" {
				part.ThoughtSignature = sig
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

			// Google CloudCode internal protocol passes functionResponse under 'model' role
			contents = append(contents, GeminiContent{
				Role: "model",
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
	if toolsArr, ok := body["tools"].([]interface{}); ok && len(toolsArr) > 0 {
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
	var mergedContents []GeminiContent
	for _, c := range contents {
		if len(mergedContents) > 0 && mergedContents[len(mergedContents)-1].Role == c.Role {
			mergedContents[len(mergedContents)-1].Parts = append(mergedContents[len(mergedContents)-1].Parts, c.Parts...)
		} else {
			mergedContents = append(mergedContents, GeminiContent{
				Role:  c.Role,
				Parts: append([]GeminiPart{}, c.Parts...),
			})
		}
	}

	sessionID := customSessionID
	if sessionID == "" {
		sessionID = "-3750763034362895579"
	}

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
	reqID := fmt.Sprintf("agent/dsh-%d-%s/1", time.Now().UnixMilli(), randSuffix)

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
