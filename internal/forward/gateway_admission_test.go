package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func TestGatewayAdmissionChecksEachModelBeforeClaim(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, allowed := range []bool{false, true} {
			name := "personal"
			if charity {
				name = "charity"
			}
			if allowed {
				name += "/fallback"
			} else {
				name += "/rejected"
			}
			t.Run(name, func(t *testing.T) {
				f := newServiceFixture(t, nil)
				models, err := gatewaypolicy.Parse(`{"models":[{"base_url":"https://upstream.example/v1","model":"verified","adapter":"openai_responses","efforts":["max"],"storage":"openai"}]}`)
				if err != nil {
					t.Fatal(err)
				}
				f.service.registry = connector.NewDefaultRegistryWithGatewayModels(models)
				first := f.personal.snapshot.Candidates[0]
				model := "provider/model"
				if charity {
					first = f.charity.snapshot.Candidates[0]
					model = "[公益]care/model"
				}
				first.ConnectorType = contract.TypeAISDKGatewayV3
				first.UpstreamModelID = "unverified"
				candidates := []RouteCandidate{first}
				if allowed {
					second := first
					second.EndpointKeyID++
					second.UpstreamModelID = "verified"
					candidates = append(candidates, second)
					f.addDispatch(second)
				}
				if charity {
					f.charity.snapshot.Candidates = candidates
				} else {
					f.personal.snapshot.Candidates = candidates
				}
				gateway := f.service.connectors[contract.TypeAISDKGatewayV3].(*fakeConnector)
				gateway.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
					if input.Target.UpstreamModel() != "verified" {
						t.Error("unverified model dispatched")
					}
					return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}
				}
				raw := `{"model":"` + model + `","messages":[{"role":"user","content":"PRIVATE_PROMPT"}],"max_completion_tokens":128000,"reasoning_effort":"max","store":false}`
				r := httptest.NewRecorder()
				f.service.Chat(context.Background(), r, 1, decodeChatForTest(t, raw), []byte(raw), "application/json", "en")
				if allowed {
					if r.Code != http.StatusOK || gateway.calls != 1 || len(f.claims.claims) != 1 || f.claims.claims[0].Candidate.EndpointKeyID != candidates[1].EndpointKeyID {
						t.Fatalf("candidate selection failed: status=%d body=%s calls=%d claims=%+v", r.Code, r.Body, gateway.calls, f.claims.claims)
					}
				} else {
					if r.Code != 400 || len(f.claims.events) != 0 || gateway.calls != 0 || !strings.Contains(r.Body.String(), "invalid_request") || strings.Contains(r.Body.String(), "PRIVATE_PROMPT") || strings.Contains(r.Body.String(), "unverified") {
						t.Fatalf("rejection crossed admission: status=%d body=%s events=%v calls=%d", r.Code, r.Body, f.claims.events, gateway.calls)
					}
				}
			})
		}
	}
}

func TestGatewayRejectionDiagnosticContainsOnlyFieldAndStage(t *testing.T) {
	err := &contract.RequestRejection{Stage: "model preflight", Field: "reasoning_effort", Reason: "effort is not enabled for this model"}
	failure := failureForError(err, true)
	trace := debugCallerResult(failure, 2000)
	if failure.status != 400 || !strings.Contains(trace.Message, "model preflight: reasoning_effort:") {
		t.Fatalf("missing rejection diagnostic: %+v", trace)
	}
}
