package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestGatewayBedrockReasoningReplay(t *testing.T) {
	const native = `[{"type":"reasoning","text":"think  ","providerMetadata":{"bedrock":{"signature":"signed+/="}}},{"type":"reasoning","text":"","providerMetadata":{"bedrock":{"redactedData":"legacy+/="}}},{"type":"reasoning","text":"","providerMetadata":{"bedrock":{"redactedContent":"current+/="}}},{"type":"text","text":"answer"},{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{}"}]`
	const finish = `{"type":"finish","finishReason":{"unified":"tool-calls"},"usage":` + knownUsage + `}`
	source := strings.Repeat("A", 43)
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", kind, streaming), func(t *testing.T) {
				upstream := `{"content":` + native + `,"finishReason":{"unified":"tool-calls"},"usage":` + knownUsage + `}`
				mediaType := "application/json"
				if streaming {
					mediaType = "text/event-stream"
					upstream = sse(
						`{"type":"reasoning-start","id":"r1"}`, `{"type":"reasoning-delta","id":"r1","delta":"think  "}`, `{"type":"reasoning-delta","id":"r1","delta":"","providerMetadata":{"bedrock":{"signature":"signed+/="}}}`, `{"type":"reasoning-end","id":"r1"}`,
						`{"type":"reasoning-start","id":"r2"}`, `{"type":"reasoning-delta","id":"r2","delta":"","providerMetadata":{"bedrock":{"redactedData":"legacy+/="}}}`, `{"type":"reasoning-end","id":"r2"}`,
						`{"type":"reasoning-start","id":"r3"}`, `{"type":"reasoning-end","id":"r3","providerMetadata":{"bedrock":{"redactedContent":"current+/="}}}`,
						`{"type":"text-start","id":"t"}`, `{"type":"text-delta","id":"t","delta":"answer"}`, `{"type":"text-end","id":"t"}`,
						`{"type":"tool-call","toolCallId":"call","toolName":"clock","input":"{}"}`, finish)
				}
				backend := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(upstream, mediaType), nil }}
				adapter, _ := NewAdapter(backend)
				request := chat(t, fmt.Sprintf(`{"model":"public","messages":[{"role":"user","content":"hi"}],"stream":%t}`, streaming))
				defer request.Clear()
				writer := httptest.NewRecorder()
				result := adapter.AttemptWithPolicy(context.Background(), writer, contract.NewTarget(kind, target().BaseURL(), "private"), secret(), request, nil, "", contract.AttemptPolicy{ContinuationID: source})
				if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 5 || result.Usage.TotalMismatch {
					t.Fatal(result, writer.Body.String())
				}
				raw := gatewayReplayMessage(t, writer.Body.String(), streaming)
				var message object
				if json.Unmarshal(raw, &message) != nil || string(message["content"]) != `"answer"` || string(message["reasoning_content"]) != `"think  "` {
					t.Fatalf("public projection = %s", raw)
				}
				if id, valid := continuation.ID(message["extra_content"]); !valid || id != source {
					t.Fatal("missing Bedrock continuation source", string(raw))
				}
				replay := chat(t, `{"model":"public","messages":[`+string(raw)+`,{"role":"tool","tool_call_id":"call","content":"ok"}]}`)
				defer replay.Clear()
				body, err := compileChatForType(kind, replay, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				want := strings.ReplaceAll(strings.ReplaceAll(native, "providerMetadata", "providerOptions"), `"input":"{}"`, `"input":{}`)
				equalParts(t, compiledHistory(t, body), want)
			})
		}
	}
}

func TestGatewayBedrockLegacyEmptyRedaction(t *testing.T) {
	const native = `{"type":"reasoning","text":"","providerMetadata":{"bedrock":{"redactedData":""}}}`
	out, _, err := translateChat([]byte(`{"content":[`+native+`],"finishReason":{"unified":"stop"}}`), "public", 1)
	if err != nil {
		t.Fatal(err)
	}
	message := gatewayReplayMessage(t, string(out), false)
	if strings.Contains(string(message), `"reasoning_content"`) {
		t.Fatal("empty redaction added visible reasoning", string(message))
	}
	request := chat(t, `{"model":"public","messages":[`+string(message)+`,{"role":"user","content":"continue"}]}`)
	defer request.Clear()
	body, err := compileChat(request, "")
	if err != nil {
		t.Fatal(err)
	}
	equalParts(t, compiledHistory(t, body), "["+strings.ReplaceAll(native, "providerMetadata", "providerOptions")+"]")
}

func TestGatewayBedrockContinuationRejectsInvalidMetadata(t *testing.T) {
	for _, value := range []string{`{"signature":42}`, `{"redactedData":{}}`, `{"redactedContent":"line\nbreak"}`, `{"signature":"ok","apiKey":"secret"}`} {
		part, err := parseObject([]byte(`{"type":"reasoning","text":"","providerMetadata":{"bedrock":` + value + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := continuationPart(part, true); err == nil {
			t.Fatalf("invalid history metadata accepted: %s", value)
		}
	}
	part, _ := parseObject([]byte(`{"type":"text","text":"answer","providerMetadata":{"bedrock":{"signature":"wrong-part"}}}`))
	if _, err := continuationPart(part, true); err == nil {
		t.Fatal("Bedrock reasoning metadata attached to text")
	}
	out, _, err := translateChat([]byte(`{"content":[{"type":"reasoning","text":"","providerMetadata":{"bedrock":{"redactedContent":{}}}}],"finishReason":{"unified":"stop"}}`), "public", 1)
	if err == nil || len(out) != 0 {
		t.Fatal("malformed upstream redaction accepted")
	}
}
