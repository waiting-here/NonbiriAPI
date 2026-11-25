package forward

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestCharityChatContentPartsReachStringClients(t *testing.T) {
	const think = `{"type":"thinking","thinking":[{"type":"text","text":"first "},{"type":"text","text":"thought"}]}`
	const closeThink = `{"type":"thinking","thinking":[{"type":"text","text":"."}],"closed":true}`
	const answer = `{"type":"text","text":"答"}`
	const usage = `{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}`
	const previous = `[{"type":"thinking","thinking":[{"type":"text","text":"previous thought"}],"signature":"opaque"},{"type":"text","text":"previous answer"}]`
	for _, tc := range []struct {
		name   string
		rule   transportpolicy.Rule
		stream bool
	}{
		{"native-json", transportpolicy.Passthrough, false},
		{"native-sse", transportpolicy.Passthrough, true},
		{"sse-to-json", transportpolicy.ForceStream, false},
		{"json-to-sse", transportpolicy.ForceNonStream, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t, nil)
			calls := 0
			backend := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("unexpected upstream path %s", r.URL.Path)
				}
				var request struct {
					Messages []struct{ Content json.RawMessage }
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 3 || string(request.Messages[1].Content) != previous {
					t.Fatalf("structured assistant history changed: %v, %s", err, request.Messages)
				}
				if tc.rule.UpstreamStream(tc.stream) {
					var frames strings.Builder
					for _, delta := range []string{
						`{"role":"assistant","content":[` + think + `]}`,
						`{"content":[` + closeThink + `,` + answer + `]}`,
						`{"content":"案"}`,
					} {
						fmt.Fprintf(&frames, "data: "+`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":null}]}`+"\n\n", delta)
					}
					frames.WriteString("data: " + `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":` + usage + "}\n\ndata: [DONE]\n\n")
					return streamResponse(200, frames.String()), nil
				}
				content := `[` + think + `,` + closeThink + `,` + answer + `,{"type":"text","text":"案"}]`
				body := `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":` + content + `},"finish_reason":"stop"}],"usage":` + usage + `}`
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, backend, 0, false)
			forceTransport(f, tc.rule)
			body := fmt.Sprintf(`{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":%s},{"role":"user","content":"continue"}],"stream":%t,"stream_options":{"include_usage":true}}`, previous, tc.stream)
			w := policyIngress(f, "/v1/chat/completions", body, 1)
			if w.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d dispatches=%d body=%s", w.Code, calls, w.Body)
			}
			var text, reasoning, finish string
			var outputTokens int
			consume := func(raw string) {
				t.Helper()
				type message struct {
					Text    string `json:"content"`
					Thought string `json:"reasoning_content"`
				}
				var frame struct {
					Choices []struct {
						Message message
						Delta   message
						Finish  string `json:"finish_reason"`
					}
					Usage struct {
						Output int `json:"completion_tokens"`
					}
				}
				if err := json.Unmarshal([]byte(raw), &frame); err != nil {
					t.Fatalf("incompatible caller frame: %v", err)
				}
				for _, c := range frame.Choices {
					part := c.Message
					if tc.stream {
						part = c.Delta
					}
					text += part.Text
					reasoning += part.Thought
					finish += c.Finish
				}
				outputTokens += frame.Usage.Output
			}
			if tc.stream {
				if !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
					t.Fatalf("missing terminal frame: %s", w.Body)
				}
				for line := range strings.SplitSeq(w.Body.String(), "\n") {
					if data, ok := strings.CutPrefix(line, "data: "); ok && data != "[DONE]" {
						consume(data)
					}
				}
			} else {
				consume(w.Body.String())
			}
			if text != "答案" || reasoning != "first thought." || finish != "stop" || outputTokens != 4 {
				t.Fatalf("answer=%q reasoning=%q finish=%q output_tokens=%d", text, reasoning, finish, outputTokens)
			}
			if len(f.claims.outcomes) != 1 || !f.claims.outcomes[0].ProtocolSuccess || f.claims.outcomes[0].Usage.OutputTokens != 4 {
				t.Fatalf("settled results: %+v", f.claims.outcomes)
			}
		})
	}
}
