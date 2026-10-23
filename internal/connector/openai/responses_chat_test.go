package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestResponsesChatFiniteCompilation(t *testing.T) {
	body := `{"model":"public/m","instructions":"system rules","input":[{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"data:image/png;base64,YQ==","detail":"low"}]},{"type":"function_call","call_id":"call1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call1","output":"result"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"lookup"},"parallel_tool_calls":false,"max_output_tokens":13,"temperature":0.4,"top_p":0.9,"text":{"format":{"type":"json_schema","name":"result","schema":{"type":"object","properties":{"answer":{"type":"string"}}}}},"stream":true}`
	request := inferenceRequestForTest(t, contract.OperationResponses, body)
	encoded, err := request.compileChatResponse("private", contract.AttemptPolicy{SafetyIdentifier: "consumer"})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &out)
	if string(out["model"]) != `"private"` || string(out["store"]) != "false" || string(out["max_completion_tokens"]) != "13" || out["input"] != nil || !bytes.Contains(out["stream_options"], []byte(`"include_usage":true`)) {
		t.Fatalf("mapped=%s", encoded)
	}
	if !bytes.Contains(out["tools"], []byte(`"strict":true`)) || !bytes.Contains(out["tools"], []byte(`"additionalProperties":false`)) || !bytes.Contains(out["tools"], []byte(`"required":["query"]`)) || bytes.Contains(out["response_format"], []byte(`"strict"`)) || bytes.Contains(out["response_format"], []byte(`"required"`)) {
		t.Fatal("Responses strict defaults lost")
	}
	if !bytes.Contains(out["messages"], []byte(`"tool_call_id":"call1"`)) || !bytes.Contains(out["messages"], []byte(`"role":"system"`)) || !bytes.Contains(out["messages"], []byte(`"type":"image_url"`)) {
		t.Fatal("history correlation lost")
	}
	var original map[string]json.RawMessage
	logical, _ := request.LogicalBody()
	_ = json.Unmarshal(logical, &original)
	if bytes.Contains(original["tools"], []byte(`"strict"`)) {
		t.Fatal("compilation mutated caller")
	}
}

func TestResponsesChatRejectsUnrepresentableRequests(t *testing.T) {
	for _, extra := range []string{`"reasoning":{"effort":"low"}`, `"include":["reasoning.encrypted_content"]`, `"truncation":"auto"`, `"metadata":{"x":"y"}`, `"tools":[{"type":"web_search"}]`, `"text":{"verbosity":"low"}`, `"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[]}`} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi",`+extra+`}`)
		if request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{}) == nil {
			t.Fatalf("accepted %s", extra)
		}
		if request.CheckTarget(contract.TypeOpenAICompatible, contract.AttemptPolicy{}) != nil {
			t.Fatal("native capability restricted by conversion")
		}
	}
	for _, input := range []string{
		`[{"type":"reasoning","encrypted_content":"opaque"}]`,
		`[{"role":"user","content":[{"type":"input_file","file_data":"inline","filename":"a.pdf"}]}]`,
		`[{"type":"function_call_output","call_id":"missing","output":"x"}]`,
		`[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}]`,
		`[{"role":"assistant","content":[{"type":"output_text","text":"x","annotations":[{"type":"url_citation"}]}]}]`,
	} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":`+input+`}`)
		if request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{}) == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if InferenceSupported(contract.TypeOpenAIChatCompletions, contract.OperationResponsesCompact) || InferenceSupported(contract.TypeOpenAIChatCompletions, contract.OperationResponsesInputTokens) {
		t.Fatal("unsupported conversion advertised")
	}
}

func TestResponsesChatPreservesExplicitStrictAndTextDefaults(t *testing.T) {
	for _, strict := range []string{"true", "false"} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","strict":`+strict+`,"parameters":{"type":"object","properties":{"x":{"type":"string","minLength":2}}}}],"text":{"format":{"type":"json_schema","name":"result","strict":`+strict+`,"schema":{"type":"object","properties":{"x":{"type":"string"}}}}}}`)
		body, err := request.compileChatResponse("m", contract.AttemptPolicy{})
		if err != nil || bytes.Contains(body, []byte(`"required"`)) || bytes.Contains(body, []byte(`"additionalProperties"`)) || bytes.Count(body, []byte(`"strict":`+strict)) != 2 {
			t.Fatalf("explicit strict altered: %s %v", body, err)
		}
	}
	request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"x":{"type":"string","minLength":2}}}}]}`)
	if request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{}) == nil {
		t.Fatal("guessed automatic strict fallback outside supported subset")
	}
}

func TestResponsesChatJSONUsesExplicitPathAndPreservesUsage(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.URL.Path != "/v1/chat/completions" {
					t.Error("conversion used wrong path")
				}
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["messages"] == nil || string(body["model"]) != `"private"` {
					t.Error("wrong protocol body")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"private","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":%q}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2}}}`, reason)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public/m","input":"hi"}`)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
			if !result.Success || !result.Usage.Present || result.Usage.CacheReadInputTokens != 2 || count != 1 || writer.started != 1 || !strings.Contains(writer.Body.String(), `"object":"response"`) || strings.Contains(writer.Body.String(), "private") {
				t.Fatalf("result=%+v body=%s", result, writer.Body.String())
			}
			if reason != "stop" && !strings.Contains(writer.Body.String(), `"status":"incomplete"`) {
				t.Fatal("incomplete output reported complete")
			}
		})
	}
	request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi"}`)
	bad := []byte(`{"id":"c1","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","audio":{}},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	if _, usage, err := projectChatAsResponse(bad, request); err == nil || !usage.Present || usage.OutputTokens != 1 {
		t.Fatal("failed output lost standard usage")
	}
}

func chatResponseChunk(delta, finish, usage string) string {
	return `data: {"id":"c1","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}],"usage":` + usage + "}\n\n"
}
func TestResponsesChatSSETerminationToolsAndUsage(t *testing.T) {
	usage := `data: {"id":"c1","object":"chat.completion.chunk","created":1,"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n"
	text := chatResponseChunk(`{"role":"assistant","content":"hello"}`, `null`, `null`)
	tool := chatResponseChunk(`{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"look","arguments":"{"}}]}`, `null`, `null`) + chatResponseChunk(`{"tool_calls":[{"index":0,"function":{"name":"up","arguments":"}"}}]}`, `"tool_calls"`, `null`)
	tests := []struct {
		name, wire     string
		success, known bool
	}{
		{"text", text + chatResponseChunk(`{}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", true, true},
		{"tools", tool + usage + "data: [DONE]\n\n", true, true},
		{"partial", text, false, false},
		{"finish_without_done", text + chatResponseChunk(`{}`, `"stop"`, `null`) + usage, false, true},
		{"done_without_finish", text + "data: [DONE]\n\n", false, false},
		{"extra_after_finish", text + chatResponseChunk(`{}`, `"stop"`, `null`) + chatResponseChunk(`{"content":"extra"}`, `null`, `null`) + "data: [DONE]\n\n", false, false},
		{"reported_error", text + `data: {"error":{"message":"failed"},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n", false, true},
		{"secret_split", chatResponseChunk(`{"content":"secret-"}`, `null`, `null`) + chatResponseChunk(`{"content":"material"}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.wire)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public/m","input":"hi","stream":true}`)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
			if result.Success != test.success || result.Usage.Present != test.known || strings.Contains(writer.Body.String(), "secret-material") {
				t.Fatalf("result=%+v body=%s", result, writer.Body.String())
			}
			if test.success && (!strings.Contains(writer.Body.String(), "event: response.completed") || strings.Contains(writer.Body.String(), "[DONE]")) {
				t.Fatal("wrong public termination")
			}
			if test.name == "tools" && (!strings.Contains(writer.Body.String(), `"call_id":"call1"`) || !strings.Contains(writer.Body.String(), `"name":"lookup"`) || !strings.Contains(writer.Body.String(), `"arguments":"{}"`)) {
				t.Fatal("tool correlation lost")
			}
		})
	}
}
