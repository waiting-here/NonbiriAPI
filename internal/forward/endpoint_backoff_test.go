package forward

import (
	"context"
	"net/http/httptest"
	"testing"

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
