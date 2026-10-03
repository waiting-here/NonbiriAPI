package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func transportJSON(kind contract.Type) string {
	switch kind {
	case contract.TypeOpenAICompatible:
		return `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	case contract.TypeAnthropicCompatible:
		return `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`
	default:
		return `{"content":[{"type":"text","text":"Hello"}],"finishReason":{"unified":"stop","raw":"stop"},"usage":{"inputTokens":{"total":1,"noCache":1,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":1,"text":1,"reasoning":0}},"warnings":[]}`
	}
}

func TestModelTransportAcrossConnectorsAndCallerModes(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, rule := range []transportpolicy.Rule{transportpolicy.Passthrough, transportpolicy.ForceNonStream, transportpolicy.ForceStream} {
			for _, callerStream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", kind, rule, callerStream), func(t *testing.T) {
					f := newServiceFixture(t, nil)
					calls := 0
					b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
						calls++
						raw, _ := io.ReadAll(r.Body)
						var body map[string]json.RawMessage
						if json.Unmarshal(raw, &body) != nil {
							t.Fatal("invalid physical body")
						}
						stream := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
						if kind != contract.TypeAISDKGatewayV3 {
							_ = json.Unmarshal(body["stream"], &stream)
						}
						if stream != rule.UpstreamStream(callerStream) {
							t.Fatalf("upstream stream=%t raw=%s", stream, raw)
						}
						response := streamResponse(200, successfulStream(kind))
						if !stream {
							response.Header.Set("Content-Type", "application/json")
							response.Body = io.NopCloser(strings.NewReader(transportJSON(kind)))
						}
						return response, nil
					}}
					useRealStreamConnector(t, f, kind, b, 0, false)
					f.charity.preflight.TransportRule = rule
					f.charity.snapshot.TransportRule = rule
					raw := fmt.Sprintf(`{"model":"[公益]care/model","stream":%t,"stream_options":{"include_usage":true},"max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`, callerStream)
					request := decodeChatForTest(t, raw)
					w := httptest.NewRecorder()
					f.service.Chat(context.Background(), w, 1, request, nil, "application/json", "en")
					if w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), "Hello") {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body)
					}
					if callerStream {
						if !strings.Contains(w.Body.String(), "[DONE]") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
							t.Fatal(w.Body.String())
						}
					} else {
						var result map[string]any
						if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["object"] != "chat.completion" {
							t.Fatal(w.Body.String())
						}
					}
					if len(f.claims.outcomes) != 1 || !f.claims.outcomes[0].ResponseStarted || !f.claims.outcomes[0].ProtocolSuccess {
						t.Fatalf("outcomes=%+v", f.claims.outcomes)
					}
				})
			}
		}
	}
}

func TestTransportCompletionPreservesToolsReasoningRefusalAndUsage(t *testing.T) {
	raw := []byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":"thought","refusal":"reason","tool_calls":[{"id":"t1","type":"function","function":{"name":"lookup","arguments":"{\"x\":9007199254740993}"}}]},"finish_reason":"tool_calls","logprobs":{"content":[{"token":"x","logprob":-1}]}}],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23,"prompt_tokens_details":{"cached_tokens":10,"cache_write_tokens":5}}}`)
	frames, err := completionToStream(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for _, frame := range frames {
		stream.Write(frame)
	}
	if !strings.Contains(stream.String(), `"index":0`) {
		t.Fatal("missing tool index")
	}
	body, err := streamToCompletion([]byte(stream.String()))
	if err != nil {
		t.Fatal(err)
	}
	var original, restored any
	if json.Unmarshal(raw, &original) != nil || json.Unmarshal(body, &restored) != nil {
		t.Fatal("invalid json")
	}
	before, _ := json.Marshal(original)
	after, _ := json.Marshal(restored)
	if string(before) != string(after) {
		t.Fatalf("round trip:\n%s\n%s", before, after)
	}
	frames, err = completionToStream(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if strings.Contains(string(frame), `"usage"`) {
			t.Fatal("unsolicited usage")
		}
	}
}
