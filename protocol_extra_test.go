package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// tool_choice → toolConfig.functionCallingConfig eşlemesi
func TestToolChoiceMapping(t *testing.T) {
	base := `{"model":"gemini-3.8-flash-medium","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"do_it","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}}],"tool_choice":%s}`

	cases := []struct {
		choiceJSON string
		wantMode   string
		wantNames  []string
	}{
		{`"none"`, "NONE", nil},
		{`"required"`, "ANY", nil},
		{`{"type":"function","function":{"name":"do_it"}}`, "ANY", []string{"do_it"}},
	}
	for _, tc := range cases {
		body := strings.Replace(base, "%s", tc.choiceJSON, 1)
		payload, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(body), "sess-test")
		if err != nil {
			t.Fatalf("convert error (choice %s): %v", tc.choiceJSON, err)
		}
		fc := payload.Request.ToolConfig
		if fc == nil || fc.FunctionCallingConfig == nil {
			t.Fatalf("toolConfig eksik (choice %s)", tc.choiceJSON)
		}
		if fc.FunctionCallingConfig.Mode != tc.wantMode {
			t.Errorf("choice %s: mode=%s istenen=%s", tc.choiceJSON, fc.FunctionCallingConfig.Mode, tc.wantMode)
		}
		if len(fc.FunctionCallingConfig.AllowedFunctionNames) != len(tc.wantNames) {
			t.Errorf("choice %s: allowed=%v istenen=%v", tc.choiceJSON, fc.FunctionCallingConfig.AllowedFunctionNames, tc.wantNames)
		}
	}

	// auto → toolConfig YOK (eski davranış korunmalı)
	body := strings.Replace(base, "%s", `"auto"`, 1)
	payload, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(body), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Request.ToolConfig != nil {
		t.Errorf("auto tool_choice toolConfig üretmemeli")
	}
}

// response_format (Chat) ve text.format (Responses) → responseMimeType + responseSchema
func TestResponseFormatMapping(t *testing.T) {
	body := `{"model":"gemini-3.8-flash-medium","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"out","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}}}}`
	payload, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(body), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	gc := payload.Request.GenerationConfig
	if gc.ResponseMimeType != "application/json" {
		t.Errorf("responseMimeType=%q", gc.ResponseMimeType)
	}
	if gc.ResponseSchema == nil {
		t.Fatal("responseSchema eksik")
	}
	if gc.ResponseSchema["type"] != "OBJECT" {
		t.Errorf("şema tipi=%v istenen=OBJECT (büyük harf)", gc.ResponseSchema["type"])
	}

	// Responses API text.format eşdeğeri
	body2 := `{"model":"m","input":"hi","text":{"format":{"type":"json_object"}}}`
	p2, isResp, _, _, err := ConvertOpenAiRequestToGemini([]byte(body2), "sess-test")
	if err != nil || !isResp {
		t.Fatalf("responses dönüşümü başarısız: %v", err)
	}
	if p2.Request.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("text.format json_object → responseMimeType=%q", p2.Request.GenerationConfig.ResponseMimeType)
	}
}

// stop / seed / penalty — opt-in passthrough + varsayılan zarf değişmezliği (cache)
func TestStopSeedPenalties(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stop":["END","STOP"],"seed":42,"frequency_penalty":0.5,"presence_penalty":-0.5}`
	payload, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(body), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	gc := payload.Request.GenerationConfig
	if len(gc.StopSequences) != 2 || gc.StopSequences[0] != "END" {
		t.Errorf("stopSequences=%v", gc.StopSequences)
	}
	if gc.Seed == nil || *gc.Seed != 42 {
		t.Errorf("seed=%v", gc.Seed)
	}
	if gc.FrequencyPenalty == nil || *gc.FrequencyPenalty != 0.5 {
		t.Errorf("frequencyPenalty=%v", gc.FrequencyPenalty)
	}
	if gc.PresencePenalty == nil || *gc.PresencePenalty != -0.5 {
		t.Errorf("presencePenalty=%v", gc.PresencePenalty)
	}

	// Opt-in: alanlar yokken zarf bayt-bayt değişmemeli (omitempty → JSON'da görünmez)
	body2 := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	p2, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(body2), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p2.Request.GenerationConfig)
	if strings.Contains(string(b), "stopSequences") || strings.Contains(string(b), `"seed"`) {
		t.Errorf("varsayılan zarf değişmemeli: %s", b)
	}
}

// Ham args bayt sadakati: args, modelin ürettiği JSON ile bayt-bayt basılmalı
func TestRawArgsByteStability(t *testing.T) {
	fc := &GeminiFunctionCall{}
	if err := fc.UnmarshalJSON([]byte(`{"name":"run","args":{"path":"/x","command":"ls"}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"args":{"path":"/x","command":"ls"}`) {
		t.Errorf("args bayt-sadık değil (alfabetik sıralanmış?): %s", b)
	}
}

// reasoning item round-trip: encrypted_content → thought part thoughtSignature
func TestReasoningRoundTrip(t *testing.T) {
	sig := strings.Repeat("A", 96)
	req := map[string]interface{}{
		"model": "m",
		"input": []interface{}{
			map[string]interface{}{
				"type":              "reasoning",
				"id":                "rs_1",
				"encrypted_content": sig,
				"content":           []interface{}{map[string]interface{}{"type": "text", "text": "dusunme"}},
			},
			map[string]interface{}{"role": "user", "content": "devam"},
		},
	}
	b, _ := json.Marshal(req)
	payload, _, _, _, err := ConvertOpenAiRequestToGemini(b, "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range payload.Request.Contents {
		for _, p := range c.Parts {
			if p.Thought && p.Text == "dusunme" && p.ThoughtSignature == sig {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("reasoning item thought part + imza geri konamadı; contents=%+v", payload.Request.Contents)
	}
}

// Narration kuralı: istek anında enjeksiyon + idempotentlik + kapatma anahtarı
func TestNarrationHint(t *testing.T) {
	saved := GlobalSettingsManager
	defer func() { GlobalSettingsManager = saved }()

	// 1) Varsayılan (ayar yöneticisi yokken bile AÇIK): kural sistem isteminin
	//    SONUNDA ve TEK KEZ basılır; istemcinin kendi metni önde kalır.
	GlobalSettingsManager = nil
	p1, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(`{"model":"m","input":"hi","instructions":"You are helpful."}`), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	if p1.Request.SystemInstruction == nil {
		t.Fatal("systemInstruction eksik")
	}
	text1 := p1.Request.SystemInstruction.Parts[0].Text
	if strings.Count(text1, NarrationRuleMarker) != 1 {
		t.Errorf("kural tam 1 kez basılmalı, %d kez: %s", strings.Count(text1, NarrationRuleMarker), text1)
	}
	if !strings.HasSuffix(text1, NarrationRuleText) {
		t.Errorf("kural sonda olmalı: %s", text1)
	}
	if !strings.HasPrefix(text1, "You are helpful.") {
		t.Errorf("istemci metni önde kalmalı: %s", text1)
	}

	// 2) İstemci kuralı zaten taşıyorsa (örn. DSH eklentisi): ikinci basım YOK
	req2, _ := json.Marshal(map[string]interface{}{
		"model":        "m",
		"input":        "hi",
		"instructions": "Base prompt.\n\n" + NarrationRuleText,
	})
	p2, _, _, _, err := ConvertOpenAiRequestToGemini(req2, "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	text2 := p2.Request.SystemInstruction.Parts[0].Text
	if strings.Count(text2, NarrationRuleMarker) != 1 {
		t.Errorf("idempotentlik bozuldu (%d basım): %s", strings.Count(text2, NarrationRuleMarker), text2)
	}
	if text2 != "Base prompt.\n\n"+NarrationRuleText {
		t.Errorf("istemci metni değişmemeli: %s", text2)
	}

	// 2b) Eski (araç-başına) kural izi taşıyan istemci: yeni kural ÜSTÜNE
	//     EKLENMEZ — iki zıt kural aynı promptta çelişirdi.
	reqLegacy, _ := json.Marshal(map[string]interface{}{
		"model":        "m",
		"input":        "hi",
		"instructions": "Base prompt.\n\nTool-call status updates: before each tool call, write one sentence.",
	})
	pLegacy, _, _, _, err := ConvertOpenAiRequestToGemini(reqLegacy, "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	textLegacy := pLegacy.Request.SystemInstruction.Parts[0].Text
	if strings.Contains(textLegacy, NarrationRuleMarker) {
		t.Errorf("eski kural izi varken yeni kural eklenmemeli: %s", textLegacy)
	}

	// 3) Kapatma anahtarı: narration_hint=false → kural HİÇ basılmaz
	f := false
	GlobalSettingsManager = &SettingsManager{settings: OverrideSettings{NarrationHint: &f}}
	p3, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(`{"model":"m","input":"hi","instructions":"You are helpful."}`), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	if text3 := p3.Request.SystemInstruction.Parts[0].Text; text3 != "You are helpful." {
		t.Errorf("kapalıyken metin değişmemeli: %s", text3)
	}
	p4, _, _, _, err := ConvertOpenAiRequestToGemini([]byte(`{"model":"m","input":"hi"}`), "sess-test")
	if err != nil {
		t.Fatal(err)
	}
	if p4.Request.SystemInstruction != nil {
		t.Errorf("kapalıyken systemInstruction üretilmemeli: %+v", p4.Request.SystemInstruction)
	}

	// 4) Eski ayar dosyası (alan yok → normalize → AÇIK)
	GlobalSettingsManager = &SettingsManager{settings: OverrideSettings{}}
	GlobalSettingsManager.normalize()
	if !GlobalSettingsManager.NarrationHintEnabled() {
		t.Errorf("alan yokken varsayılan AÇIK olmalı")
	}
}
