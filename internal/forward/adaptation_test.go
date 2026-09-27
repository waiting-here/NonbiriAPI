package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestCharityAdaptationCombinedBudgetRejectsBeforeClaim(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.charity.snapshot.Candidates[0].BindingID = 31
	f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
		out := make(map[requestadaptation.Ref]requestadaptation.Snapshot)
		for _, ref := range refs {
			doc := requestadaptation.Empty(ref.Scope)
			if ref.Scope == requestadaptation.ScopeCharityModel {
				for i := 0; i < 64; i++ {
					doc.ForwardHeaders.Values = append(doc.ForwardHeaders.Values, fmt.Sprintf("X-Forward-%d", i))
				}
			} else {
				doc.FixedHeaders.Mode = requestadaptation.ModeReplace
				doc.FixedHeaders.Values["X-Fixed"] = "value"
			}
			out[ref] = requestadaptation.Snapshot{Document: doc, Revision: "1"}
		}
		return out
	})
	raw := `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`
	recorder := httptest.NewRecorder()
	f.service.Chat(context.Background(), recorder, 1, decodeChatForTest(t, raw), []byte(raw), "application/json", "en")
	if recorder.Code != http.StatusConflict || len(f.claims.accepts) != 0 || f.openAI.calls != 0 {
		t.Fatalf("effective configuration conflict was not rejected before acceptance: %d %s", recorder.Code, recorder.Body)
	}
}

type adaptationReaderFunc func([]requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot

func (reader adaptationReaderFunc) LoadMany(_ context.Context, refs []requestadaptation.Ref) (map[requestadaptation.Ref]requestadaptation.Snapshot, error) {
	return reader(refs), nil
}

func TestPersonalAdaptationFrozenBeforeClaimAndAppliedToAttempt(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	ref := requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: fixture.personal.snapshot.Candidates[0].EndpointID}
	loads := 0
	fixture.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
		loads++
		if len(refs) != 1 || refs[0] != ref {
			t.Fatalf("snapshot refs: %+v", refs)
		}
		doc := requestadaptation.Empty(ref.Scope)
		doc.FixedHeaders.Values["X-Research"] = "configured"
		doc.BodyForced.Values["/reasoning_effort"] = json.RawMessage(`"low"`)
		return map[requestadaptation.Ref]requestadaptation.Snapshot{ref: {Document: doc, Revision: "1"}}
	})
	fixture.addDispatch(fixture.personal.snapshot.Candidates[0])
	var sent map[string]json.RawMessage
	fixture.openAI.attempt = func(_ context.Context, input connector.AttemptInput) connectorcontract.AttemptResult {
		body, err := input.Ingress.LogicalBody()
		if err != nil || json.Unmarshal(body, &sent) != nil {
			t.Fatalf("logical outbound: %v, %s", err, body)
		}
		if input.Policy.AdditionalHeaders.Get("X-Research") != "configured" || !input.Policy.HasAdaptation {
			t.Fatalf("attempt policy: %+v", input.Policy)
		}
		return connectorcontract.AttemptResult{Success: true, Committed: true, ClientStatus: http.StatusOK}
	}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":4096}}`)
	fixture.service.Chat(context.Background(), httptest.NewRecorder(), 1, request, []byte(`{}`), "application/json", "en")
	if loads != 1 || fixture.openAI.calls != 1 || string(sent["reasoning_effort"]) != `"low"` || sent["thinking"] == nil {
		t.Fatalf("loads=%d, calls=%d, outbound=%v", loads, fixture.openAI.calls, sent)
	}
	original, err := request.LogicalBody()
	if err != nil || json.Valid(original) == false || string(sent["reasoning_effort"]) != `"low"` {
		t.Fatalf("source request changed: %v, %s", err, original)
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(original, &source) != nil || source["reasoning_effort"] != nil {
		t.Fatalf("adaptation mutated logical source: %s", original)
	}
}

func TestCharityAdaptationExclusionAndOutputFloorReachSameClaim(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.charity.policyFields = []string{"thinking"}
	candidate := &fixture.charity.snapshot.Candidates[0]
	candidate.BindingID = 31
	modelRef := requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: fixture.charity.snapshot.ModelID}
	bindingRef := requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: candidate.BindingID}
	fixture.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
		if len(refs) != 2 || refs[0] != modelRef || refs[1] != bindingRef {
			t.Fatalf("snapshot refs: %+v", refs)
		}
		model := requestadaptation.Empty(modelRef.Scope)
		model.BodyForced.Values["/max_tokens"] = json.RawMessage(`333`)
		binding := requestadaptation.Empty(bindingRef.Scope)
		binding.FixedHeaders = requestadaptation.HeaderSection{Mode: requestadaptation.ModeReplace, Values: map[string]string{"X-Research": "charity"}}
		return map[requestadaptation.Ref]requestadaptation.Snapshot{
			modelRef:   {Document: model, Revision: "2"},
			bindingRef: {Document: binding, Revision: "3"},
		}
	})
	fixture.addDispatch(*candidate)
	var sent map[string]json.RawMessage
	fixture.openAI.attempt = func(_ context.Context, input connector.AttemptInput) connectorcontract.AttemptResult {
		body, err := input.Ingress.LogicalBody()
		if err != nil || json.Unmarshal(body, &sent) != nil {
			t.Fatalf("logical outbound: %v, %s", err, body)
		}
		if input.Policy.AdditionalHeaders.Get("X-Research") != "charity" {
			t.Fatalf("binding header missing: %+v", input.Policy)
		}
		return connectorcontract.AttemptResult{Success: true, Committed: true, ClientStatus: http.StatusOK}
	}
	raw := `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":4096}}`
	request := decodeChatForTest(t, raw)
	recorder := httptest.NewRecorder()
	fixture.service.Chat(context.Background(), recorder, 1, request, []byte(raw), "application/json", "en")
	if fixture.openAI.calls != 1 || sent["thinking"] != nil || string(sent["max_tokens"]) != `333` ||
		len(fixture.charity.outputFloors) != 1 || fixture.charity.outputFloors[0] != 333 ||
		len(fixture.claims.accepts) != 1 || fixture.claims.accepts[0].OutputTokenFloor != 333 ||
		len(fixture.claims.claims) != 1 || fixture.claims.claims[0].OutputTokenFloor != 333 {
		t.Fatalf("status=%d body=%s calls=%d outbound=%v floors=%v accepts=%+v claims=%+v", recorder.Code, recorder.Body, fixture.openAI.calls, sent, fixture.charity.outputFloors, fixture.claims.accepts, fixture.claims.claims)
	}
}
