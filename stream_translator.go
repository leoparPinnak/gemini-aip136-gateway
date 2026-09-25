package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

type StreamTranslator struct {
	w              http.ResponseWriter
	flusher        http.Flusher
	isResponsesAPI bool
	modelName      string

	responseID          string
	outputItemID        string
	reasoningItemID     string
	callItemID          string

	hasStartedReasoning  bool
	hasEndedReasoning    bool
	hasStartedItem       bool
	hasEmittedHeader     bool
	reasoningOutputIndex int
	textOutputIndex      int
	nextOutputIndex      int
	fullOutputText       strings.Builder
	fullThoughtText      strings.Builder
	activeFnCalls        []map[string]interface{}

	promptTokens   int
	outputTokens   int
	cachedTokens   int
	totalTokens    int
	pid              int
	processName      string
	lastFinishReason string
	lastTraceID      string // A2: Google'ın döndürdüğü traceId (JSONL korelasyonu)
	lastUsageRaw     json.RawMessage // A4: son usageMetadata ham (cached==0 analizi)
	mu               sync.Mutex
}

func NewStreamTranslator(w http.ResponseWriter, isResponsesAPI bool, modelName string, ctx ...ProtocolContext) *StreamTranslator {
	flusher, _ := w.(http.Flusher)
	randSuffix := fmt.Sprintf("%06x", rand.Intn(0xffffff))
	timestamp := time.Now().UnixMilli()

	pid := 0
	procName := "İstemci"
	if len(ctx) > 0 {
		pid = ctx[0].PID
		procName = ctx[0].ProcessName
	}

	return &StreamTranslator{
		w:               w,
		flusher:         flusher,
		isResponsesAPI:  isResponsesAPI,
		modelName:       modelName,
		pid:             pid,
		processName:     procName,
		responseID:      fmt.Sprintf("resp_%d_%s", timestamp, randSuffix),
		outputItemID:    fmt.Sprintf("msg_%d_%s", timestamp, randSuffix),
		reasoningItemID: fmt.Sprintf("rs_%d_%s", timestamp, randSuffix),
		callItemID:      fmt.Sprintf("fc_%d_%s", timestamp, randSuffix),
	}
}

func (st *StreamTranslator) ensureReasoningEnded() {
	if st.isResponsesAPI && st.hasStartedReasoning && !st.hasEndedReasoning {
		st.hasEndedReasoning = true
		st.sendSSE("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"response_id":  st.responseID,
			"output_index": st.reasoningOutputIndex,
			"item": map[string]interface{}{
				"id":     st.reasoningItemID,
				"type":   "reasoning",
				"status": "completed",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": st.fullThoughtText.String(),
					},
				},
			},
		})
	}
}

func (st *StreamTranslator) sendSSE(event string, data interface{}) {
	st.mu.Lock()
	defer st.mu.Unlock()

	b, err := json.Marshal(data)
	if err != nil {
		return
	}

	if event != "" {
		fmt.Fprintf(st.w, "event: %s\n", event)
	}
	fmt.Fprintf(st.w, "data: %s\n\n", string(b))

	if st.flusher != nil {
		st.flusher.Flush()
	}
}

func (st *StreamTranslator) sendRawSSE(data string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	fmt.Fprintf(st.w, "data: %s\n\n", data)
	if st.flusher != nil {
		st.flusher.Flush()
	}
}

func (st *StreamTranslator) HandleGeminiChunk(chunk *GeminiStreamChunk) {
	// A2: Google traceId / modelVersion yakala (JSONL korelasyonu)
	if chunk.Response.TraceID != "" {
		st.lastTraceID = chunk.Response.TraceID
	} else if chunk.TraceID != "" {
		st.lastTraceID = chunk.TraceID
	}

	// Extract usage
	var usage *GeminiUsageMetadata
	if chunk.Response.UsageMetadata != nil {
		usage = chunk.Response.UsageMetadata
	} else if chunk.UsageMetadata != nil {
		usage = chunk.UsageMetadata
	}
	if usage != nil {
		if usage.PromptTokenCount > 0 {
			st.promptTokens = usage.PromptTokenCount
		}
		if usage.CandidatesTokenCount > 0 {
			st.outputTokens = usage.CandidatesTokenCount
		}
		if usage.CachedContentTokenCount > 0 {
			st.cachedTokens = usage.CachedContentTokenCount
		}
		if usage.TotalTokenCount > 0 {
			st.totalTokens = usage.TotalTokenCount
		}
		// A4: son usage'ın ham hali — cached==0 satırlarında "gerçek 0" mı
		// "STOP gelmemiş kayıp 0" mı ayrıştırılır.
		if raw, err := json.Marshal(usage); err == nil {
			st.lastUsageRaw = raw
		}
	}

	var candidates []GeminiCandidate
	if len(chunk.Response.Candidates) > 0 {
		candidates = chunk.Response.Candidates
	} else if len(chunk.Candidates) > 0 {
		candidates = chunk.Candidates
	}

	if len(candidates) == 0 {
		return
	}

	cand := candidates[0]
	if cand.FinishReason != "" {
		st.lastFinishReason = cand.FinishReason
		if cand.FinishReason != "STOP" {
			log.Printf("[ℹ️ Google FinishReason]: PID: %d (%s) | Model: %s -> FinishReason: %s\n",
				st.pid, st.processName, st.modelName, cand.FinishReason)
		}
	}
	for _, part := range cand.Content.Parts {
		// 1. Thought / Reasoning
		if part.Thought && part.Text != "" {
			st.fullThoughtText.WriteString(part.Text)

			if st.isResponsesAPI {
				if !st.hasEmittedHeader {
					st.hasEmittedHeader = true
					st.sendSSE("response.created", map[string]interface{}{
						"type":        "response.created",
						"response_id": st.responseID,
						"response": map[string]interface{}{
							"id":     st.responseID,
							"status": "in_progress",
							"model":  st.modelName,
						},
					})
				}
				if !st.hasStartedReasoning {
					st.hasStartedReasoning = true
					st.reasoningOutputIndex = st.nextOutputIndex
					st.nextOutputIndex++
					st.sendSSE("response.output_item.added", map[string]interface{}{
						"type":         "response.output_item.added",
						"response_id":  st.responseID,
						"output_index": st.reasoningOutputIndex,
						"item": map[string]interface{}{
							"id":     st.reasoningItemID,
							"type":   "reasoning",
							"status": "in_progress",
						},
					})
				}
				st.sendSSE("response.reasoning_text.delta", map[string]interface{}{
					"type":         "response.reasoning_text.delta",
					"response_id":  st.responseID,
					"item_id":      st.reasoningItemID,
					"output_index": st.reasoningOutputIndex,
					"delta":        part.Text,
				})
			} else {
				st.sendSSE("", map[string]interface{}{
					"id":      st.responseID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   st.modelName,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"reasoning_content": part.Text,
							},
						},
					},
				})
			}
			continue
		}

		// 2. Output Text
		if part.Text != "" {
			st.ensureReasoningEnded()
			st.fullOutputText.WriteString(part.Text)

			if st.isResponsesAPI {
				if !st.hasEmittedHeader {
					st.hasEmittedHeader = true
					st.sendSSE("response.created", map[string]interface{}{
						"type":        "response.created",
						"response_id": st.responseID,
						"response": map[string]interface{}{
							"id":     st.responseID,
							"status": "in_progress",
							"model":  st.modelName,
						},
					})
				}

				if !st.hasStartedItem {
					st.hasStartedItem = true
					st.textOutputIndex = st.nextOutputIndex
					st.nextOutputIndex++
					st.sendSSE("response.output_item.added", map[string]interface{}{
						"type":         "response.output_item.added",
						"response_id":  st.responseID,
						"output_index": st.textOutputIndex,
						"item": map[string]interface{}{
							"id":      st.outputItemID,
							"type":    "message",
							"status":  "in_progress",
							"role":    "assistant",
							"content": []interface{}{},
						},
					})
				}

				st.sendSSE("response.output_text.delta", map[string]interface{}{
					"type":         "response.output_text.delta",
					"response_id":  st.responseID,
					"item_id":      st.outputItemID,
					"output_index": st.textOutputIndex,
					"delta":        part.Text,
				})
			} else {
				st.sendSSE("", map[string]interface{}{
					"id":      st.responseID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   st.modelName,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"content": part.Text,
							},
						},
					},
				})
			}
			continue
		}

		// 3. Function Call
		if part.FunctionCall != nil {
			st.ensureReasoningEnded()
			fn := part.FunctionCall
			callID := fn.ID
			if callID == "" {
				callID = st.callItemID
			}
			if part.ThoughtSignature != "" {
				GlobalThoughtStore.Store(callID, fn.Name, part.ThoughtSignature)
				GlobalDiagnosticLogger.LogFeedback(
					st.pid,
					st.processName,
					"",
					st.modelName,
					"Kriptografik İmza Depolandı",
					fmt.Sprintf("Google CloudCode tarafından '%s' aracı için üretilen %d baytlık geçerli düşünce imzası yakalandı ve diske yazıldı.", fn.Name, len(part.ThoughtSignature)),
					map[string]interface{}{
						"tool_name":     fn.Name,
						"call_id":       callID,
						"signature_len": len(part.ThoughtSignature),
						"disk_file":     "thought_signatures.json",
					},
				)
			}

			argsBytes, _ := json.Marshal(fn.Args)
			argsStr := string(argsBytes)

			st.activeFnCalls = append(st.activeFnCalls, map[string]interface{}{
				"id":        callID,
				"name":      fn.Name,
				"arguments": argsStr,
			})

			if st.isResponsesAPI {
				fnOutputIdx := st.nextOutputIndex
				st.nextOutputIndex++

				st.sendSSE("response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"response_id":  st.responseID,
					"output_index": fnOutputIdx,
					"item": map[string]interface{}{
						"id":      callID,
						"type":    "function_call",
						"status":  "in_progress",
						"name":    fn.Name,
						"call_id": callID,
					},
				})

				st.sendSSE("response.function_call_arguments.delta", map[string]interface{}{
					"type":         "response.function_call_arguments.delta",
					"response_id":  st.responseID,
					"output_index": fnOutputIdx,
					"item_id":      callID,
					"call_id":      callID,
					"delta":        argsStr,
				})

				st.sendSSE("response.function_call_arguments.done", map[string]interface{}{
					"type":         "response.function_call_arguments.done",
					"response_id":  st.responseID,
					"output_index": fnOutputIdx,
					"item_id":      callID,
					"call_id":      callID,
					"arguments":    argsStr,
				})

				st.sendSSE("response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"response_id":  st.responseID,
					"output_index": fnOutputIdx,
					"item": map[string]interface{}{
						"id":        callID,
						"type":      "function_call",
						"status":    "completed",
						"name":      fn.Name,
						"call_id":   callID,
						"arguments": argsStr,
					},
				})
			} else {
				st.sendSSE("", map[string]interface{}{
					"id":      st.responseID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   st.modelName,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": []map[string]interface{}{
									{
										"index": 0,
										"id":    callID,
										"type":  "function",
										"function": map[string]interface{}{
											"name":      fn.Name,
											"arguments": argsStr,
										},
									},
								},
							},
						},
					},
				})
			}
		}
	}
}

// FinishStream akışı sonlandırır ve durum rozetini döndürür.
// true = Google STOP/final finishReason hiç gelmedi (akış koptu) →
// handler "truncated" yayınlar; usage kaybolmuş olabilir (A4).
// SSE event şeması DEĞİŞMEZ (istemci uyumu); yalnızca sunucu tarafı işaret.
func (st *StreamTranslator) FinishStream() bool {
	fullText := st.fullOutputText.String()
	truncated := st.lastFinishReason == ""

	// Boş Yanıt Koruma Kalkanı (Empty Content Fallback Shield)
	// Eğer model ne nihai metin ne de araç/fonksiyon çağrısı ürettiyse (örneğin düşünme bütçesini tüketip durduysa),
	// istemcinin "completed response with no content" hatasıyla çökmesini engellemek için fallback içeriği enjekte et.
	if fullText == "" && len(st.activeFnCalls) == 0 {
		thought := strings.TrimSpace(st.fullThoughtText.String())
		var fallbackText string
		if thought != "" {
			fallbackText = "*(Bilgilendirme: Model düşünme/akıl yürütme sürecini tamamladı ancak nihai bir yanıt metni veya araç çağrısı üretmeden oturumu sonlandırdı. Lütfen işlemi sürdürmek için 'Devam et' yazın.)*"
		} else if st.lastFinishReason != "" && st.lastFinishReason != "STOP" {
			fallbackText = fmt.Sprintf("*(Bilgilendirme: Model yanıt üretemedi. Google sonlandırma nedeni: '%s'. Lütfen bağlamı daraltıp 'Devam et' yazın.)*", st.lastFinishReason)
		} else {
			fallbackText = "*(Bilgilendirme: Model herhangi bir yanıt çıktısı veya araç çağrısı üretmeden oturumu tamamladı (Google sunucu zaman aşımı veya boş akış). Lütfen işlemi sürdürmek için 'Devam et' yazın veya Pro modele geçin.)*"
		}

		log.Printf("[⚠️ Gateway Fallback] Model '%s' (PID: %d, %s) boş yanıt döndü. Boş yanıt koruma kalkanı devreye girdi (%d karakter).\n",
			st.modelName, st.pid, st.processName, len(fallbackText))

		fullText = fallbackText
		st.fullOutputText.WriteString(fallbackText)
		if st.outputTokens == 0 {
			st.outputTokens = len(fallbackText) / 4
			if st.outputTokens < 1 {
				st.outputTokens = 1
			}
		}

		if st.isResponsesAPI {
			if !st.hasEmittedHeader {
				st.hasEmittedHeader = true
				st.sendSSE("response.created", map[string]interface{}{
					"type":        "response.created",
					"response_id": st.responseID,
					"response": map[string]interface{}{
						"id":     st.responseID,
						"status": "in_progress",
						"model":  st.modelName,
					},
				})
			}

			if !st.hasStartedItem {
				st.hasStartedItem = true
				st.sendSSE("response.output_item.added", map[string]interface{}{
					"type":        "response.output_item.added",
					"response_id": st.responseID,
					"item": map[string]interface{}{
						"id":      st.outputItemID,
						"type":    "message",
						"status":  "in_progress",
						"role":    "assistant",
						"content": []interface{}{},
					},
				})
			}

			st.sendSSE("response.output_text.delta", map[string]interface{}{
				"type":        "response.output_text.delta",
				"response_id": st.responseID,
				"item_id":     st.outputItemID,
				"delta":       fallbackText,
			})
		} else {
			st.sendSSE("", map[string]interface{}{
				"id":      st.responseID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   st.modelName,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"content": fallbackText,
						},
					},
				},
			})
		}
	}

	if st.isResponsesAPI {
		st.ensureReasoningEnded()

		// Crucial fix for DSH: output_item.done MUST contain the accumulated output_text!
		if fullText != "" || st.hasStartedItem {
			st.sendSSE("response.output_item.done", map[string]interface{}{
				"type":         "response.output_item.done",
				"response_id":  st.responseID,
				"output_index": st.textOutputIndex,
				"item": map[string]interface{}{
					"id":     st.outputItemID,
					"type":   "message",
					"status": "completed",
					"role":   "assistant",
					"content": []map[string]interface{}{
						{
							"type": "output_text",
							"text": fullText,
						},
					},
				},
			})
		}

		var outputList []map[string]interface{}
		if st.hasStartedReasoning {
			outputList = append(outputList, map[string]interface{}{
				"id":     st.reasoningItemID,
				"type":   "reasoning",
				"status": "completed",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": st.fullThoughtText.String(),
					},
				},
			})
		}
		if fullText != "" {
			outputList = append(outputList, map[string]interface{}{
				"id":     st.outputItemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]interface{}{
					{
						"type": "output_text",
						"text": fullText,
					},
				},
			})
		}
		for _, fn := range st.activeFnCalls {
			outputList = append(outputList, map[string]interface{}{
				"id":        fn["id"],
				"type":      "function_call",
				"status":    "completed",
				"name":      fn["name"],
				"call_id":   fn["id"],
				"arguments": fn["arguments"],
			})
		}

		approxReasoningTokens := len(st.fullThoughtText.String()) / 4
		respTotalTokens := st.totalTokens
		if respTotalTokens == 0 {
			respTotalTokens = st.promptTokens + st.outputTokens
		}

		st.sendSSE("response.completed", map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     st.responseID,
				"status": "completed",
				"model":  st.modelName,
				"output": outputList,
				"usage": map[string]interface{}{
					"input_tokens":  st.promptTokens,
					"output_tokens": st.outputTokens,
					"total_tokens":  respTotalTokens,
					"input_tokens_details": map[string]interface{}{
						"cached_tokens": st.cachedTokens,
					},
					"output_tokens_details": map[string]interface{}{
						"reasoning_tokens": approxReasoningTokens,
					},
				},
			},
		})
	} else {
		finishReason := "stop"
		if len(st.activeFnCalls) > 0 {
			finishReason = "tool_calls"
		}

		outTok := st.outputTokens
		if outTok == 0 && (st.fullOutputText.Len() > 0 || st.fullThoughtText.Len() > 0) {
			outTok = (st.fullOutputText.Len() + st.fullThoughtText.Len()) / 4
			if outTok < 1 {
				outTok = 1
			}
		}

		totalTok := st.totalTokens
		if totalTok == 0 {
			totalTok = st.promptTokens + outTok
		}

		usageObj := map[string]interface{}{
			"prompt_tokens":     st.promptTokens,
			"completion_tokens": outTok,
			"total_tokens":      totalTok,
			"prompt_tokens_details": map[string]interface{}{
				"cached_tokens": st.cachedTokens,
			},
		}

		// 1. Send stop chunk with usage
		st.sendSSE("", map[string]interface{}{
			"id":      st.responseID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   st.modelName,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]interface{}{},
					"finish_reason": finishReason,
				},
			},
			"usage": usageObj,
		})

		// 2. OpenAI streaming usage standard chunk (choices empty, usage present)
		st.sendSSE("", map[string]interface{}{
			"id":      st.responseID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   st.modelName,
			"choices": []interface{}{},
			"usage":   usageObj,
		})

		st.sendRawSSE("[DONE]")
	}
	return truncated
}
