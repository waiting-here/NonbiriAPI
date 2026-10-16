package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/clientguard"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

type originalDecisionGuard struct {
	t     *testing.T
	calls int
}

func (g *originalDecisionGuard) CheckCall(ctx context.Context, _ int64, _ string, _ int64) (clientguard.Decision, error) {
	g.calls++
	source, ok := observability.SourceFromContext(ctx)
	if !ok || source.UserAgent != "original-client" {
		g.t.Fatal("original request source was replaced before audit")
	}
	return clientguard.Decision{Banned: true}, nil
}
func TestDecisionsAuditOriginalInputBeforeAdaptationAndIncompatibleModel(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		guard := &originalDecisionGuard{t: t}
		f.service.clientGuard = guard
		f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
			if len(refs) != 1 || refs[0].Scope != requestadaptation.ScopePersonalModel {
				t.Fatal("physical adaptation loaded before matched audit")
			}
			doc := requestadaptation.Empty(refs[0].Scope)
			doc.FixedHeaders.Values["User-Agent"] = "configured-client"
			doc.BodyForced.Values["/prompt_cache_key"] = json.RawMessage(`"configured"`)
			return map[requestadaptation.Ref]requestadaptation.Snapshot{refs[0]: {Document: doc, Revision: "1"}}
		})
		model := "provider/model"
		if charity {
			model = "[公益]care/model"
		}
		body := `{"model":"` + model + `","input":"x","questions":[{"type":"predicate","instructions":"x"}]}`
		request, err := openai.DecodeDecisionRequest(strings.NewReader(body), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		ctx := observability.WithSource(context.Background(), observability.Source{UserAgent: "original-client"})
		writer := httptest.NewRecorder()
		f.service.Decisions(ctx, writer, 1, request, []byte(body), "application/json", "en")
		if writer.Code != 403 || guard.calls != 1 || len(f.claims.accepts) != 0 || f.openAI.calls != 0 || f.personal.snapCalls+f.charity.snapCalls != 0 {
			t.Fatalf("order status=%d guard=%d claims=%v", writer.Code, guard.calls, f.claims.events)
		}
	}
}

func TestDecisionsSelectIndependentOperationAndSkipChatPolicy(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, supported := range []bool{false, true} {
			t.Run(fmt.Sprintf("charity=%t/supported=%t", charity, supported), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationDecisions}
				f.charity.preflight.ModelTypes = modeltype.Set{contract.OperationDecisions}
				f.personal.preflight.TransportRule = transportpolicy.ForceStream
				f.charity.preflight.TransportRule = transportpolicy.ForceStream
				f.personal.snapshot.ModelTypes = f.personal.preflight.ModelTypes.Clone()
				f.charity.snapshot.ModelTypes = f.charity.preflight.ModelTypes.Clone()
				candidate := f.personal.snapshot.Candidates[0]
				model := "provider/model"
				if charity {
					candidate = f.charity.snapshot.Candidates[0]
					model = "[公益]care/model"
				}
				candidate.ConnectorType = contract.TypeOpenAIDecisions
				candidate.Policy = contract.AttemptPolicy{FlattenToolCalls: true, ForceStoreFalse: true}
				descriptor, _ := f.service.registry.Descriptor(contract.TypeOpenAIDecisions)
				driver := &fakeConnector{connectorType: contract.TypeOpenAIDecisions, capabilities: descriptor.Capabilities, consume: true}
				f.service.connectors[contract.TypeOpenAIDecisions] = driver
				var captured *openai.DecisionRequest
				driver.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
					if input.Operation != contract.OperationDecisions || input.Decision == nil || input.Ingress != nil || input.Image != nil || input.Embedding != nil || input.Policy.FlattenToolCalls || input.Policy.ForceStoreFalse {
						t.Fatal("decisions received chat state")
					}
					captured = input.Decision
					_, _ = input.Sink.Write([]byte(`{"model":"` + model + `","answers":[]}`))
					return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200, Usage: contract.Usage{Present: true, UncachedInputTokens: 4}}
				}
				other := candidate
				other.ConnectorType = contract.TypeAnthropicCompatible
				candidates := []RouteCandidate{other}
				if supported {
					candidates = append(candidates, candidate)
				}
				if charity {
					f.charity.snapshot.Candidates = candidates
				} else {
					f.personal.snapshot.Candidates = candidates
				}
				f.addDispatch(candidate)
				body := `{"model":"` + model + `","input":"x","questions":[{"type":"predicate","instructions":"x"}]}`
				request, err := openai.DecodeDecisionRequest(strings.NewReader(body), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer request.Clear()
				writer := httptest.NewRecorder()
				f.service.Decisions(context.Background(), writer, 1, request, []byte(body), "application/json", "en")
				if !supported {
					if writer.Code != 400 || len(f.claims.accepts) != 0 || driver.calls != 0 {
						t.Fatalf("unsupported reached dispatch: %d %v", writer.Code, f.claims.events)
					}
					return
				}
				if writer.Code != 200 || driver.calls != 1 || f.openAI.calls != 0 || f.anthropic.calls != 0 || len(f.claims.accepts) != 1 || !f.claims.outcomes[0].ResponseStarted {
					t.Fatalf("execution %d %v", writer.Code, f.claims.events)
				}
				want := claim.RouteOpenAIDecisions
				if charity {
					want = claim.RouteCharityDecisions
				}
				if f.claims.accepts[0].Route != want || captured.Model != "" || request.Model != model {
					t.Fatal("route/snapshot isolation failed")
				}
			})
		}
	}
}

func TestDecisionsIngressDryRunAndUnsupportedStream(t *testing.T) {
	for _, suffix := range []string{"", `,"stream":true`, `,"stream":null`} {
		capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
		f := newServiceFixture(t, capture)
		f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationDecisions}
		body := `{"model":"provider/model","input":"x","questions":[{"type":"predicate","instructions":"x"}]` + suffix + `}`
		request := withCallerIdentity(httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)), resources.CallerIdentity{UserID: 1, Generation: 1})
		writer := httptest.NewRecorder()
		NewHandler(f.service).ServeHTTP(writer, request)
		want := 422
		if suffix != "" {
			want = 400
		}
		if writer.Code != want || len(f.claims.events) != 0 || f.personal.snapCalls != 0 || f.charity.snapCalls != 0 {
			t.Fatalf("ingress status=%d body=%s events=%v", writer.Code, writer.Body.String(), f.claims.events)
		}
	}
}
