package forward

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type policyDispatchGrant struct {
	DispatchGrant
	rules []smartbackoff.Rule
}

func (g policyDispatchGrant) BackoffRules() []smartbackoff.Rule {
	return append([]smartbackoff.Rule(nil), g.rules...)
}

func TestEndpointBackoffRulesReachCharityAttemptAndCompletion(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.addDispatch(f.charity.snapshot.Candidates[0])
	f.claims.dispatches[0] = policyDispatchGrant{DispatchGrant: f.claims.dispatches[0], rules: []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "auto"}}}
	f.openAI.attempt = func(ctx context.Context, _ connector.AttemptInput) contract.AttemptResult {
		c := upstreamerror.Context{CharityCall: smartbackoff.IsCharityCall(ctx), Rules: smartbackoff.RulesFromContext(ctx), ContainsSecret: func([]byte) bool { return false }}
		detail := c.Parse([]byte(`{"error":{"code":"insufficient_credits","message":"Insufficient credits"}}`))
		if detail.Backoff().Source != "custom_rule" {
			t.Fatal("dispatch snapshot missing from connector context")
		}
		return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 402, ErrorDetail: detail, FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakUpstreamFailure}
	}
	r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
	f.service.Chat(t.Context(), httptest.NewRecorder(), 1, r, []byte(`{}`), "application/json", "en")
	if len(f.claims.outcomes) != 1 || f.claims.outcomes[0].Backoff.Source != "custom_rule" || f.claims.outcomes[0].Backoff.RuleIndex != 1 {
		t.Fatal(f.claims.outcomes)
	}
}

func TestGoogleOverloadEvidenceReachesCharityCompletion(t *testing.T) {
	for _, test := range []struct {
		name      string
		heartbeat bool
		status    int
	}{{"ordinary", false, 503}, {"after_json_heartbeat", true, 200}} {
		t.Run(test.name, func(t *testing.T) {
			f := newServiceFixture(t, nil)
			f.addDispatch(f.charity.snapshot.Candidates[0])
			w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
			if test.heartbeat {
				f.service.jsonKeepalive = func() time.Duration { return 5 * time.Millisecond }
			}
			f.openAI.attempt = func(ctx context.Context, _ connector.AttemptInput) contract.AttemptResult {
				if test.heartbeat {
					if err := awaitJSONHeartbeat(ctx, w); err != nil {
						t.Fatal(err)
					}
				}
				c := upstreamerror.Context{CharityCall: smartbackoff.IsCharityCall(ctx), GoogleAPI: true, ContainsSecret: func([]byte) bool { return false }}
				d := c.Parse([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"The model is overloaded. Please try again later."}}`))
				return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503, ErrorDetail: d, FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakUpstreamFailure}
			}
			r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
			defer r.Clear()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			f.service.Chat(ctx, w, 1, r, []byte(`{}`), "application/json", "en")
			if len(f.claims.outcomes) != 1 || w.Code != test.status || !json.Valid(w.Body.Bytes()) {
				t.Fatal("overload evidence or caller status lost", f.claims.outcomes, w.Code, w.Body.String())
			}
			outcome := f.claims.outcomes[0]
			if outcome.TemporaryBackoff.Delay(503) != 60 || outcome.UpstreamStatus != 503 || outcome.ResponseStarted || outcome.ProtocolSuccess {
				t.Fatal("heartbeat changed upstream overload facts", outcome)
			}
		})
	}
}
