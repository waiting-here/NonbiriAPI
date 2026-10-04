package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestRepairedStreamIdentitySurvivesTransportAndBilling(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		for _, callerStream := range []bool{false, true} {
			for _, known := range []bool{false, true} {
				t.Run(fmt.Sprintf("flatten=%t/stream=%t/known=%t", flatten, callerStream, known), func(t *testing.T) {
					f := newServiceFixture(t, nil)
					calls := 0
					frames := []string{
						`{"id":" leading","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
						`{"id":"c2","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"thought"},"finish_reason":null}]}`,
						`{"id":"c3","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}]}`,
						`{"id":"c4","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
					}
					if known {
						frames = append(frames, `{"id":"c5","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`)
					}
					var stream strings.Builder
					for _, frame := range frames {
						stream.WriteString("data: " + frame + "\n\n")
					}
					stream.WriteString("data: [DONE]\n\n")
					b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
						calls++
						if r.Header.Get("Accept") != "text/event-stream" {
							t.Fatal("physical request stopped streaming")
						}
						return streamResponse(200, stream.String()), nil
					}}
					useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 0, flatten)
					for _, grant := range f.claims.dispatches {
						grant.(*fakeDispatchGrant).policy.FlattenToolCalls = flatten
					}
					forceTransport(f, transportpolicy.ForceStream)
					request := decodeChatForTest(t, fmt.Sprintf(`{"model":"[公益]care/model","stream":%t,"stream_options":{"include_usage":true},"messages":[]}`, callerStream))
					w := httptest.NewRecorder()
					f.service.Chat(context.Background(), w, 1, request, nil, "application/json", "en")
					body := w.Body.String()
					if w.Code != 200 || calls != 1 || !strings.Contains(body, "Hello") || !strings.Contains(body, "thought") || !strings.Contains(body, "lookup") || strings.Contains(body, "leading") {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, body)
					}
					if flatten == strings.Contains(body, `"tool_calls":`) {
						t.Fatalf("tool projection changed: %s", body)
					}
					var ids []string
					if callerStream {
						if !strings.HasSuffix(body, "data: [DONE]\n\n") {
							t.Fatal(body)
						}
						for _, line := range strings.Split(body, "\n") {
							if !strings.HasPrefix(line, "data: {") {
								continue
							}
							var root struct {
								ID string `json:"id"`
							}
							if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &root) != nil {
								t.Fatal(body)
							}
							ids = append(ids, root.ID)
						}
					} else {
						var root struct {
							ID     string `json:"id"`
							Object string `json:"object"`
						}
						if json.Unmarshal(w.Body.Bytes(), &root) != nil || root.Object != "chat.completion" {
							t.Fatal(body)
						}
						ids = append(ids, root.ID)
					}
					if len(ids) == 0 || !strings.HasPrefix(ids[0], "chatcmpl-") {
						t.Fatalf("IDs=%v", ids)
					}
					for _, id := range ids {
						if id != ids[0] {
							t.Fatalf("IDs=%v", ids)
						}
					}
					if len(f.claims.outcomes) != 1 {
						t.Fatalf("outcomes=%+v", f.claims.outcomes)
					}
					out := f.claims.outcomes[0]
					if !out.ResponseStarted || !out.ProtocolSuccess || out.Usage.Present != known {
						t.Fatalf("outcome=%+v", out)
					}
					if known && (out.Usage.UncachedInputTokens != 20 || out.Usage.OutputTokens != 3) {
						t.Fatalf("usage=%+v", out.Usage)
					}
					terminal := f.claims.requestResults[0]
					if terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 10 || f.charges.calls != 1 {
						t.Fatalf("terminal=%+v charges=%d", terminal, f.charges.calls)
					}
				})
			}
		}
	}
}
