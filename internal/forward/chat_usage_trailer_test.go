package forward

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestChatUsageTrailerSettlesFinalSnapshotOnce(t *testing.T) {
	const start = `data: {"id":"c1","model":"m","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}` + "\n\n"
	const terminal = `data: {"id":"c1","model":"m","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"content":"","role":"assistant"},"finish_reason":"stop","native_finish_reason":"stop"}]`
	const usage = `,"usage":{"prompt_tokens":7,"completion_tokens":%d,"total_tokens":%d}}` + "\n\n"
	for _, finalTokens := range []int{3, 4} {
		t.Run(fmt.Sprintf("final_output=%d", finalTokens), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			calls := 0
			backend := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
				calls++
				wire := start + terminal + fmt.Sprintf(usage, 3, 10) + terminal + fmt.Sprintf(usage, finalTokens, 7+finalTokens) + "data: [DONE]\n\n"
				return streamResponse(200, wire), nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, backend, 0, false)
			w := policyIngress(f, "/v1/chat/completions", `{"model":"[公益]care/model","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`, 1)
			if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"finish_reason":"stop"`) != 1 || calls != 1 || f.charges.calls != 1 || len(f.claims.outcomes) != 1 || len(f.claims.requestResults) != 1 {
				t.Fatalf("status=%d dispatch=%d charges=%d outcomes=%+v body=%s", w.Code, calls, f.charges.calls, f.claims.outcomes, w.Body)
			}
			outcome := f.claims.outcomes[0]
			if !outcome.ProtocolSuccess || !outcome.Usage.Present || outcome.Usage.UncachedInputTokens != 7 || outcome.Usage.OutputTokens != int64(finalTokens) || f.claims.requestResults[0].Disposition != claim.AccountingCommit {
				t.Fatalf("outcome=%+v terminal=%+v", outcome, f.claims.requestResults)
			}
		})
	}
}
