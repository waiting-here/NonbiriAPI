package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

const gatewayNativeHistory = `[{"type":"reasoning","text":"consider","providerMetadata":{"anthropic":{"signature":"signed"}}},{"type":"text","text":"answer","providerMetadata":{"google":{"thoughtSignature":"YWJj"}}},{"type":"reasoning","text":"again","providerMetadata":{"openai":{"itemId":"reason","reasoningEncryptedContent":"encrypted"},"anthropic":{"redactedData":"hidden"}}},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{}","providerMetadata":{"google":{"thoughtSignature":"ZGVm"}}}]`

func historyStream() string {
	return sse(`{"type":"reasoning-start","id":"r1"}`, `{"type":"reasoning-delta","id":"r1","delta":"consider"}`, `{"type":"reasoning-end","id":"r1","providerMetadata":{"anthropic":{"signature":"signed"}}}`,
		`{"type":"text-start","id":"t"}`, `{"type":"text-delta","id":"t","delta":"answer"}`, `{"type":"text-end","id":"t","providerMetadata":{"google":{"thoughtSignature":"YWJj"}}}`,
		`{"type":"reasoning-start","id":"r2","providerMetadata":{"openai":{"itemId":"reason"}}}`, `{"type":"reasoning-delta","id":"r2","delta":"again"}`, `{"type":"reasoning-end","id":"r2","providerMetadata":{"openai":{"reasoningEncryptedContent":"encrypted"},"anthropic":{"redactedData":"hidden"}}}`,
		`{"type":"tool-input-start","id":"call","toolName":"clock"}`, `{"type":"tool-input-delta","id":"call","delta":"{}"}`, `{"type":"tool-input-end","id":"call"}`, `{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{}","providerMetadata":{"google":{"thoughtSignature":"ZGVm"}}}`,
		`{"type":"finish","finishReason":{"unified":"tool-calls"},"usage":`+knownUsage+`}`)
}

func responseOutput(t *testing.T, raw string, stream bool) json.RawMessage {
	t.Helper()
	var root map[string]json.RawMessage
	if !stream {
		if json.Unmarshal([]byte(raw), &root) != nil {
			t.Fatal(raw)
		}
		return root["output"]
	}
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &root) != nil {
			t.Fatal(line)
		}
		if string(root["type"]) == `"response.completed"` {
			var terminal map[string]json.RawMessage
			_ = json.Unmarshal(root["response"], &terminal)
			return terminal["output"]
		}
	}
	t.Fatal(raw)
	return nil
}

func TestGatewayContinuationJSONAndStreamReplayThroughResponses(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, stream := range []bool{false, true} {
			upstream := `{"content":` + gatewayNativeHistory + `,"finishReason":{"unified":"tool-calls"},"usage":` + knownUsage + `}`
			if stream {
				upstream = historyStream()
			}
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(upstream, "application/json"), nil }}
			a, _ := NewAdapter(b)
			flag := "false"
			if stream {
				flag = "true"
			}
			request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","input":"hello","stream":`+flag+`}`), contract.OperationResponses, 0)
			if err != nil {
				t.Fatal(err)
			}
			chatRequest, err := request.AsGatewayChatRequest()
			if err != nil {
				t.Fatal(err)
			}
			writer := httptest.NewRecorder()
			projection, err := openai.NewGatewayResponsesChatProjection(context.Background(), writer, request, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			result := a.AttemptWithPolicy(context.Background(), projection, contract.NewTarget(kind, target().BaseURL(), "provider/private-model"), secret(), chatRequest, nil, "", contract.AttemptPolicy{ContinuationID: strings.Repeat("A", 43), GatewayModel: &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}})
			if !result.Success || !projection.Complete() || !result.Usage.Present {
				t.Fatalf("type=%s stream=%v result=%+v body=%s", kind, stream, result, writer.Body.String())
			}
			output := responseOutput(t, writer.Body.String(), stream)
			if strings.Contains(string(output), "private-model") || !strings.Contains(string(output), "continuation_id") {
				t.Fatal(string(output))
			}
			input := strings.TrimSuffix(string(output), "]") + `,{"type":"function_call_output","call_id":"call","output":"done"}]`
			replay, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","input":`+input+`}`), contract.OperationResponses, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replay.AsChatRequest(); err == nil {
				t.Fatal("ordinary bridge accepted Gateway extension")
			}
			replayChat, err := replay.AsGatewayChatRequest()
			if err != nil {
				t.Fatal(err, string(output))
			}
			compiled, err := compileChatForType(kind, replayChat, "", nil)
			if err != nil {
				t.Fatal(err, string(output))
			}
			var prompt struct {
				Prompt []struct {
					Content []map[string]json.RawMessage `json:"content"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(compiled, &prompt)
			got, _ := json.Marshal(prompt.Prompt[0].Content)
			want := strings.ReplaceAll(gatewayNativeHistory, "providerMetadata", "providerOptions")
			want = strings.ReplaceAll(want, `"input":"{}"`, `"input":{}`)
			if !sameJSON(append(append([]byte(`{"content":`), got...), '}'), []byte(`{"content":`+want+`}`)) {
				t.Fatalf("got=%s want=%s", got, want)
			}
			projection.Clear()
			request.Clear()
			chatRequest.Clear()
			replay.Clear()
			replayChat.Clear()
		}
	}
}

func TestGatewayContinuationRejectsChangedProjectionAndRoutingMetadata(t *testing.T) {
	out, _, err := translateChat([]byte(`{"content":`+gatewayNativeHistory+`,"finishReason":{"unified":"tool-calls"}}`), "public", 1)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(out, &result)
	message := result.Choices[0].Message
	for _, mutation := range []string{"content", "metadata", "token"} {
		changed := map[string]json.RawMessage{}
		for k, v := range message {
			changed[k] = v
		}
		switch mutation {
		case "content":
			changed["content"] = json.RawMessage(`"changed"`)
		case "metadata":
			changed["extra_content"] = json.RawMessage(strings.Replace(string(message["extra_content"]), `"signature":"signed"`, `"signature":"signed","apiKey":"override"`, 1))
		case "token":
			changed["extra_content"] = json.RawMessage(strings.TrimSuffix(string(message["extra_content"]), "}") + `,"nonbiri":{"continuation_id":"bad"}}`)
		}
		raw, _ := json.Marshal(changed)
		request := chat(t, `{"model":"public","messages":[`+string(raw)+`,{"role":"tool","tool_call_id":"call","content":"done"}]}`)
		if _, err := compileChat(request, ""); err == nil {
			t.Fatal(mutation)
		}
	}
}

func TestGatewayRawAndDoneDoNotReplaceProtocolCompletion(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, tc := range []struct {
			body  string
			valid bool
		}{
			{sse(append([]string{`{"type":"raw","rawValue":{"debug":true}}`}, textParts...)...) + sse(`{"type":"raw","rawValue":null}`, `[DONE]`), true},
			{sse(append(textParts[:4:4], `[DONE]`)...), false},
			{sse(`{"type":"text-start","id":"t"}`, textParts[4], `[DONE]`), false},
			{sse(append(textParts[:len(textParts):len(textParts)], `{"type":"text-delta","id":"t","delta":"late"}`, `[DONE]`)...), false},
		} {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(tc.body, "text/event-stream"), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			result := a.Attempt(context.Background(), w, contract.NewTarget(kind, target().BaseURL(), "private"), secret(), chat(t, `{"model":"public","messages":[{"role":"user","content":"hi"}],"stream":true}`), nil, "")
			if result.Success != tc.valid || strings.Contains(w.Body.String(), "[DONE]") != tc.valid {
				t.Fatal(kind, tc.valid, result, w.Body.String())
			}
		}
	}
}

func TestGatewayRerankTopAliases(t *testing.T) {
	for _, field := range []string{`"top_n":1`, `"top_k":1`} {
		request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","query":"q","documents":["one","two"],`+field+`}`), contract.OperationRerank, 0)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := compileRerank(request, "")
		request.Clear()
		if err != nil || !strings.Contains(string(compiled), `"topN":1`) {
			t.Fatal(string(compiled), err)
		}
	}
	if _, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","query":"q","documents":["one"],"top_k":1,"top_n":1}`), contract.OperationRerank, 0); err == nil {
		t.Fatal("ambiguous aliases accepted")
	}
}

func TestGatewayOmittedReasoningAndToolOnlyContinuationReplay(t *testing.T) {
	tool := `{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{}","providerMetadata":{"google":{"thoughtSignature":"YWJj"}}}`
	reasoning := `{"type":"reasoning","text":"","providerMetadata":{"anthropic":{"signature":"opaque"}}}`
	for _, native := range []string{`[` + tool + `]`, `[` + reasoning + `,` + tool + `]`, `[` + reasoning + `]`} {
		for _, stream := range []bool{false, true} {
			finish := "stop"
			if strings.Contains(native, "tool-call") {
				finish = "tool-calls"
			}
			body := `{"content":` + native + `,"finishReason":{"unified":"` + finish + `"}}`
			if stream {
				events := []string{}
				if strings.Contains(native, "reasoning") {
					events = append(events, `{"type":"reasoning-start","id":"r"}`, `{"type":"reasoning-end","id":"r","providerMetadata":{"anthropic":{"signature":"opaque"}}}`)
				}
				if finish == "tool-calls" {
					events = append(events, tool)
				}
				events = append(events, `{"type":"finish","finishReason":{"unified":"`+finish+`"}}`)
				body = sse(events...)
			}
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(body, "application/json"), nil }}
			a, _ := NewAdapter(b)
			flag := "false"
			if stream {
				flag = "true"
			}
			request, _ := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","input":"hi","stream":`+flag+`}`), contract.OperationResponses, 0)
			chatRequest, err := request.AsGatewayChatRequest()
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			p, _ := openai.NewGatewayResponsesChatProjection(context.Background(), w, request, 8<<20)
			result := a.AttemptWithPolicy(context.Background(), p, target(), secret(), chatRequest, nil, "", contract.AttemptPolicy{GatewayModel: &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}})
			if !result.Success || !p.Complete() {
				t.Fatal(stream, native, result, w.Body.String())
			}
			output := responseOutput(t, w.Body.String(), stream)
			if finish == "tool-calls" {
				output = json.RawMessage(strings.TrimSuffix(string(output), "]") + `,{"type":"function_call_output","call_id":"call","output":"done"}]`)
			}
			replay, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public","input":`+string(output)+`}`), contract.OperationResponses, 0)
			if err != nil {
				t.Fatal(err)
			}
			replayChat, err := replay.AsGatewayChatRequest()
			if err != nil {
				t.Fatal(err, string(output))
			}
			compiled, err := compileChatForType(contract.TypeAISDKGatewayV3, replayChat, "", nil)
			if err != nil || !strings.Contains(string(compiled), `"providerOptions"`) {
				t.Fatal(err, string(compiled), string(output))
			}
			request.Clear()
			chatRequest.Clear()
			replay.Clear()
			replayChat.Clear()
			p.Clear()
		}
	}
}
