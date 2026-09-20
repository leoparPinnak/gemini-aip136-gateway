package main

import (
	"encoding/json"
	"testing"
)

func TestConvertSchemaTypeToGemini(t *testing.T) {
	// Exact scenario from user's error:
	// function_declarations[1].parameters.properties[2].value.one_of[0].properties[1].value has "const"
	rawJSON := `{
		"type": "object",
		"properties": {
			"query": { "type": "string" },
			"limit": { "type": "integer" },
			"condition": {
				"oneOf": [
					{
						"type": "object",
						"properties": {
							"id": { "type": "string" },
							"mode": { "const": "exact" }
						},
						"additionalProperties": false
					},
					{
						"type": "object",
						"properties": {
							"regex": { "const": true }
						}
					}
				]
			}
		},
		"required": []
	}`

	var rawSchema map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawSchema); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	converted := ConvertSchemaTypeToGemini(rawSchema)
	outBytes, _ := json.MarshalIndent(converted, "", "  ")
	outStr := string(outBytes)

	// Verify "const" is gone
	if jsonBytes, err := json.Marshal(converted); err == nil {
		if string(jsonBytes) != "" {
			var checkMap map[string]interface{}
			_ = json.Unmarshal(jsonBytes, &checkMap)
		}
	}

	// Ensure no "const" or "additionalProperties" or empty "required"
	if jsonStr := outStr; len(jsonStr) > 0 {
		if testing.Verbose() {
			t.Logf("Converted: %s", outStr)
		}
	}

	// Check condition.oneOf[0].properties.mode
	cond, _ := converted["properties"].(map[string]interface{})["condition"].(map[string]interface{})
	if cond == nil {
		t.Fatalf("condition property missing")
	}
	oneOf, _ := cond["oneOf"].([]interface{})
	if len(oneOf) != 2 {
		t.Fatalf("oneOf length mismatch: %d", len(oneOf))
	}
	branch0, _ := oneOf[0].(map[string]interface{})
	props0, _ := branch0["properties"].(map[string]interface{})
	mode0, _ := props0["mode"].(map[string]interface{})

	if _, hasConst := mode0["const"]; hasConst {
		t.Errorf("FAIL: mode0 still has 'const'!")
	}
	if enumArr, ok := mode0["enum"].([]string); !ok || len(enumArr) != 1 || enumArr[0] != "exact" {
		t.Errorf("FAIL: mode0 enum mismatch: %v", mode0["enum"])
	}
	if mode0["type"] != "STRING" {
		t.Errorf("FAIL: mode0 type mismatch: %v", mode0["type"])
	}

	// Check branch 1 (bool const)
	branch1, _ := oneOf[1].(map[string]interface{})
	props1, _ := branch1["properties"].(map[string]interface{})
	regex1, _ := props1["regex"].(map[string]interface{})

	if _, hasConst := regex1["const"]; hasConst {
		t.Errorf("FAIL: regex1 still has 'const'!")
	}
	if regex1["type"] != "BOOLEAN" {
		t.Errorf("FAIL: regex1 type mismatch: %v", regex1["type"])
	}

	// Check empty required was deleted
	if _, hasReq := converted["required"]; hasReq {
		t.Errorf("FAIL: empty 'required' was not deleted!")
	}
}

func TestEndingWithModelTurnGuards(t *testing.T) {
	// Scenario 1: Request ends with assistant message
	req1 := `{
		"model": "gemini-3.8-flash-medium",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi, how can I help?"}
		]
	}`
	payload1, _, _, _, err1 := ConvertOpenAiRequestToGemini([]byte(req1), "")
	if err1 != nil {
		t.Fatalf("Convert error: %v", err1)
	}
	if len(payload1.Request.Contents) == 0 {
		t.Fatalf("Contents empty")
	}
	last1 := payload1.Request.Contents[len(payload1.Request.Contents)-1]
	if last1.Role != "user" {
		t.Errorf("FAIL Scenario 1: Expected last turn role 'user', got '%s'", last1.Role)
	}

	// Scenario 2: Request ends with tool output (function_call_output / role: tool)
	req2 := `{
		"model": "gemini-3.8-flash-medium",
		"messages": [
			{"role": "user", "content": "What is the weather?"},
			{
				"role": "assistant",
				"tool_calls": [
					{
						"id": "call_123",
						"type": "function",
						"function": {"name": "get_weather", "arguments": "{\"city\":\"Izmir\"}"}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_123",
				"content": "Sunny, 24C"
			}
		]
	}`
	payload2, _, _, _, err2 := ConvertOpenAiRequestToGemini([]byte(req2), "")
	if err2 != nil {
		t.Fatalf("Convert error: %v", err2)
	}
	if len(payload2.Request.Contents) == 0 {
		t.Fatalf("Contents empty")
	}
	last2 := payload2.Request.Contents[len(payload2.Request.Contents)-1]
	if last2.Role != "user" {
		t.Errorf("FAIL Scenario 2: Expected last turn role 'user', got '%s'", last2.Role)
	}

	// Scenario 3: Request starts with assistant message
	req3 := `{
		"model": "gemini-3.8-flash-medium",
		"messages": [
			{"role": "assistant", "content": "System greeting"},
			{"role": "user", "content": "Hi"}
		]
	}`
	payload3, _, _, _, err3 := ConvertOpenAiRequestToGemini([]byte(req3), "")
	if err3 != nil {
		t.Fatalf("Convert error: %v", err3)
	}
	first3 := payload3.Request.Contents[0]
	if first3.Role != "user" {
		t.Errorf("FAIL Scenario 3: Expected first turn role 'user', got '%s'", first3.Role)
	}
}

