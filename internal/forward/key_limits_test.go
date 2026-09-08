package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestSharedKeyLimitResponsesDoNotNotifyIngressRPMPolicy(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.claims.claimErrors = map[int]error{0: claim.ErrKeyRateLimited, 1: claim.ErrKeyRateLimited, 2: claim.ErrKeyRateLimited}
	denials := 0
	flow, err := flowcontrol.New(flowcontrol.Config{
		RPM:      ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 100},
		OnDenied: func(context.Context, int64, ratelimit.RPMReason) { denials++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer flow.Close()
	middleware, err := flowcontrol.NewMiddleware(flow, CallerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	handler := WithRPMDenialScope(middleware.Wrap(NewHandler(f.service)))
	for i := 0; i < 3; i++ {
		request := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)), resources.CallerIdentity{UserID: 1, Generation: 1})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d status=%d", i, recorder.Code)
		}
	}
	if denials != 0 || f.openAI.calls != 0 || len(f.claims.requestResults) != 3 {
		t.Fatal("shared key rejection entered ingress RPM policy or upstream")
	}
	for _, result := range f.claims.requestResults {
		if result.Disposition != claim.AccountingRelease || result.ActualChargeMilli != 0 {
			t.Fatal("undispatched key rejection consumed credit")
		}
	}
}

func TestKeyLimitSkipsWithoutRequiringSilentRetry(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, allLimited := range []bool{false, true} {
			name := "personal"
			model := "provider/model"
			if charity {
				name = "charity"
				model = "[公益]care/model"
			}
			if allLimited {
				name += "/all-limited"
			}
			t.Run(name, func(t *testing.T) {
				f := newServiceFixture(t, nil)
				candidate := f.personal.snapshot.Candidates[0]
				if charity {
					candidate = f.charity.snapshot.Candidates[0]
				}
				next := candidate
				next.EndpointID = 41
				next.EndpointKeyID = 42
				if charity {
					f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, next)
				} else {
					f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, next)
				}
				f.claims.claimErrors = map[int]error{0: claim.ErrKeyRateLimited}
				if allLimited {
					f.claims.claimErrors[1] = claim.ErrKeyRateLimited
				} else {
					f.addDispatch(next)
					f.openAI.results = []connectorcontract.AttemptResult{{Success: true, Committed: true, Failure: connectorcontract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}}
				}
				request := decodeChatForTest(t, `{"model":"`+model+`","messages":[]}`)
				recorder := httptest.NewRecorder()
				f.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
				if len(f.claims.claims) != 2 || len(f.claims.requestResults) != 1 {
					t.Fatalf("claims=%d completions=%d", len(f.claims.claims), len(f.claims.requestResults))
				}
				terminal := f.claims.requestResults[0]
				if allLimited {
					if recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), httperr.CodeRateLimited) || f.openAI.calls != 0 || len(f.claims.outcomes) != 0 || terminal.Disposition != claim.AccountingRelease || terminal.ActualChargeMilli != 0 {
						t.Fatalf("limited response=%d terminal=%+v attempts=%d", recorder.Code, terminal, f.openAI.calls)
					}
				} else {
					if recorder.Code != 200 || f.openAI.calls != 1 || terminal.Disposition != claim.AccountingCommit || len(f.openAI.targets) != 1 || f.openAI.targets[0].BaseURL() != next.CanonicalBaseURL {
						t.Fatalf("fallback response=%d terminal=%+v calls=%d", recorder.Code, terminal, f.openAI.calls)
					}
				}
			})
		}
	}
}

func TestLaterKeyLimitKeepsDispatchedFailureAndSettlement(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.preflight.SilentRetry = true
	f.personal.snapshot.PersonalPreflight.SilentRetry = true
	first := f.personal.snapshot.Candidates[0]
	second := first
	second.EndpointKeyID = 42
	f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, second)
	f.addDispatch(first)
	f.claims.claimErrors = map[int]error{1: claim.ErrKeyRateLimited}
	f.openAI.results = []connectorcontract.AttemptResult{{Failure: connectorcontract.FailureUpstream, UpstreamStatus: 503}}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	recorder := httptest.NewRecorder()
	f.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
	if recorder.Code != 502 || len(f.claims.claims) != 2 || len(f.claims.outcomes) != 1 || f.claims.requestResults[0].Disposition != claim.AccountingCommit {
		t.Fatalf("response=%d claims=%d terminal=%+v", recorder.Code, len(f.claims.claims), f.claims.requestResults)
	}
}
