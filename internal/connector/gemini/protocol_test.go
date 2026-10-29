package gemini

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func request(t *testing.T, body string) *openai.ChatRequest {
	t.Helper()
	r, err := openai.DecodeChatRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}
func equalJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
func TestGeminiRequestAndTwoToolRoundTrip(t *testing.T) {
	input := `{"model":"public","messages":[{"role":"system","content":"rules"},{"role":"user","content":"weather"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"weather","arguments":"{\"city\":\"A\"}"},"extra_content":{"google":{"thought_signature":"YWJj"}}},{"id":"call_b","type":"function","function":{"name":"weather","arguments":"{\"city\":\"B\"}"}}]},{"role":"tool","tool_call_id":"call_b","content":"sunny"},{"role":"tool","tool_call_id":"call_a","content":"{\"degrees\":18}"}],"max_tokens":42,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"tool_choice":"required"}`
	body, err := Compile(request(t, input), "gemini-model", contract.AttemptPolicy{SafetyIdentifier: "NBU_CONSUMER"})
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, body, `{"contents":[{"role":"user","parts":[{"text":"weather"}]},{"role":"model","parts":[{"functionCall":{"id":"call_a","name":"weather","args":{"city":"A"}},"thoughtSignature":"YWJj"},{"functionCall":{"id":"call_b","name":"weather","args":{"city":"B"}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_b","name":"weather","response":{"result":"sunny"}}},{"functionResponse":{"id":"call_a","name":"weather","response":{"degrees":18}}}]}],"systemInstruction":{"parts":[{"text":"rules"}]},"generationConfig":{"maxOutputTokens":42},"labels":{"safety_identifier":"nbu_consumer"},"tools":[{"functionDeclarations":[{"name":"weather","parametersJsonSchema":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`)
	url, err := URL("https://example.com/v1beta/", "models/gemini-model", true)
	if err != nil || url != "https://example.com/v1beta/models/gemini-model:streamGenerateContent?alt=sse" {
		t.Fatal(url, err)
	}
	for _, model := range []string{"../secret", "models/models/x", "a/b", "x?key=secret"} {
		if _, err := URL("https://example.com", model, false); err == nil {
			t.Fatalf("model accepted %s", model)
		}
	}
}
func TestGeminiIncompatibleFieldsAndNativeBudget(t *testing.T) {
	base := `{"model":"m","messages":[{"role":"user","content":"x"}]`
	for _, field := range []string{`,"store":false`, `,"reasoning_effort":"high"`, `,"n":2`, `,"response_format":{"type":"json_schema","json_schema":{}}`, `,"tools":[{"type":"function","function":{"name":"f","strict":true}}]`, `,"tools":[{"type":"function","function":{"name":"f"}}],"parallel_tool_calls":false`} {
		if body, err := Compile(request(t, base+field+"}"), "m", contract.AttemptPolicy{}); err == nil {
			t.Fatalf("accepted %s: %s", field, body)
		}
	}
	for _, extension := range []map[string]json.RawMessage{{"/generationConfig/maxOutputTokens": json.RawMessage(`999`)}, {"/contents": json.RawMessage(`[]`)}, {"/generationConfig/thinkingConfig": json.RawMessage(`{"unknown":true}`)}} {
		if _, err := Compile(request(t, base+"}"), "m", contract.AttemptPolicy{NativeExtensions: extension}); err == nil {
			t.Fatalf("extension accepted: %v", extension)
		}
	}
	body, err := Compile(request(t, base+"}"), "m", contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/generationConfig/thinkingConfig": json.RawMessage(`{"thinkingBudget":128,"includeThoughts":true}`)}})
	if err != nil || !strings.Contains(string(body), `"thinkingBudget":128`) {
		t.Fatal(string(body), err)
	}
}
func TestGeminiJSONProjectionPreservesSignaturesAndUsage(t *testing.T) {
	d := NewDecoder("public", time.Unix(123, 0), true)
	raw := `{"responseId":"private-id","modelVersion":"private-model","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"checking","thought":true},{"functionCall":{"id":"a","name":"weather","args":{"city":"A"}},"thoughtSignature":"YWJj"},{"functionCall":{"id":"b","name":"weather","args":{"city":"B"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":4,"candidatesTokenCount":3,"thoughtsTokenCount":5,"totalTokenCount":28}}`
	body, usage, err := d.JSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if usage != (contract.Usage{Present: true, UncachedInputTokens: 16, CacheReadInputTokens: 4, OutputTokens: 8}) {
		t.Fatal(usage)
	}
	var result map[string]json.RawMessage
	_ = json.Unmarshal(body, &result)
	delete(result, "id")
	var choices []map[string]json.RawMessage
	_ = json.Unmarshal(result["choices"], &choices)
	var message map[string]json.RawMessage
	_ = json.Unmarshal(choices[0]["message"], &message)
	if !strings.Contains(string(message["extra_content"]), `"parts"`) {
		t.Fatal("ordered continuation parts missing")
	}
	delete(message, "extra_content")
	choices[0]["message"], _ = json.Marshal(message)
	result["choices"], _ = json.Marshal(choices)
	normalized, _ := json.Marshal(result)
	equalJSON(t, normalized, `{"object":"chat.completion","created":123,"model":"public","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":"checking","tool_calls":[{"id":"a","type":"function","function":{"name":"weather","arguments":"{\"city\":\"A\"}"},"extra_content":{"google":{"thought_signature":"YWJj"}}},{"id":"b","type":"function","function":{"name":"weather","arguments":"{\"city\":\"B\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":0}}}`)
}
func TestGeminiStreamSemanticEndAndUsageSnapshot(t *testing.T) {
	frames := []string{`{"candidates":[{"index":0,"content":{"parts":[{"text":"Hel"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}}`, `{"candidates":[{"index":0,"content":{"parts":[{"text":"lo","thoughtSignature":"YWJj"}]},"finishReason":"STOP"}]}`, `{"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":4,"totalTokenCount":17}}`}
	d := NewDecoder("public", time.Unix(1, 0), true)
	var output string
	for _, frame := range frames {
		chunks, err := d.Event("", []byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range chunks {
			output += string(chunk)
		}
	}
	chunks, err := d.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		output += string(chunk)
	}
	if !strings.Contains(output, `"content":"Hel"`) || !strings.Contains(output, `"thoughtSignature":"YWJj"`) || !strings.Contains(output, `"finish_reason":"stop"`) || !strings.Contains(output, `"completion_tokens":7`) || d.Usage().UncachedInputTokens != 8 {
		t.Fatal(output, d.Usage())
	}
	if _, err := d.Event("", []byte(frames[2])); err == nil {
		t.Fatal("event after Finish accepted")
	}
	for _, tc := range []struct {
		name         string
		frames       []string
		eventFailure bool
	}{
		{"empty", nil, false}, {"text without terminal", frames[:1], false}, {"unknown terminal", []string{`{"candidates":[{"finishReason":"CONTINUATION"}]}`}, true}, {"second candidate", []string{`{"candidates":[{},{}]}`}, true}, {"late text", append(append([]string{}, frames...), frames[0]), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder("m", time.Unix(1, 0), false)
			failed := false
			for _, frame := range tc.frames {
				if _, err := d.Event("", []byte(frame)); err != nil {
					failed = true
					break
				}
			}
			if failed != tc.eventFailure {
				t.Fatalf("event failure=%v", failed)
			}
			if !failed {
				if _, err := d.Finish(); err == nil {
					t.Fatal("missing terminal accepted")
				}
			}
		})
	}
}
func TestGeminiUsageUnknownIsSticky(t *testing.T) {
	for _, bad := range []string{`{"promptTokenCount":10,"candidatesTokenCount":-1,"totalTokenCount":10}`, `{"promptTokenCount":9,"candidatesTokenCount":1,"totalTokenCount":10}`, `{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11,"toolUsePromptTokenCount":1}`} {
		d := NewDecoder("m", time.Unix(1, 0), true)
		for _, frame := range []string{`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}`, `{"usageMetadata":` + bad + `}`, `{"candidates":[{"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}`} {
			if _, err := d.Event("", []byte(frame)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := d.Finish(); err != nil || d.Usage().Present {
			t.Fatal(err, d.Usage())
		}
	}
}
