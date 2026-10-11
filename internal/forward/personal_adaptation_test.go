package forward

import (
	"context"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"net/http"
	"testing"
)

func TestPersonalModelAdaptationFiltersAndRemainsFrozenAcrossRetries(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.preflight.SilentRetry = true
	f.personal.snapshot.SilentRetry = true
	candidate := f.personal.snapshot.Candidates[0]
	second := candidate
	second.EndpointID = 31
	second.EndpointKeyID = 32
	f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, second)
	f.addDispatch(candidate)
	f.addDispatch(second)
	modelLoads := 0
	f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
		out := map[requestadaptation.Ref]requestadaptation.Snapshot{}
		for _, ref := range refs {
			d := requestadaptation.Empty(ref.Scope)
			if ref.Scope == requestadaptation.ScopePersonalModel {
				modelLoads++
				d.ExcludedRequestFields = []string{"temperature"}
				d.FixedHeaders = requestadaptation.HeaderSection{Mode: requestadaptation.ModeReplace, Values: map[string]string{"X-Model": "selected"}}
				d.BodyForced = requestadaptation.BodySection{Mode: requestadaptation.ModeReplace, Values: map[string]json.RawMessage{"/reasoning_effort": json.RawMessage(`"low"`)}}
			} else {
				d.FixedHeaders.Values["X-Service"] = "must-be-replaced"
				d.BodyDefaults.Values["/top_p"] = json.RawMessage("0.5")
			}
			out[ref] = requestadaptation.Snapshot{Document: d, Revision: "1"}
		}
		return out
	})
	attempts := 0
	f.openAI.attempt = func(_ context.Context, in connector.AttemptInput) contract.AttemptResult {
		attempts++
		raw, err := in.Ingress.LogicalBody()
		if err != nil {
			t.Fatal(err)
		}
		defer clear(raw)
		var body map[string]json.RawMessage
		if err = json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["temperature"] != nil || string(body["reasoning_effort"]) != `"low"` || string(body["top_p"]) != "0.5" || in.Policy.AdditionalHeaders.Get("X-Model") != "selected" || in.Policy.AdditionalHeaders.Get("X-Service") != "" {
			t.Fatalf("wrong effective request: %s", raw)
		}
		if attempts == 1 {
			return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503, Diagnostic: "upstream unavailable"}
		}
		_, _ = in.Sink.Write([]byte(`{"id":"ok"}`))
		return contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200}
	}
	w := policyIngress(f, "/v1/chat/completions", `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"temperature":{"invalid":"removed"}}`, 1)
	if w.Code != http.StatusOK || attempts != 2 || modelLoads != 1 {
		t.Fatalf("status=%d attempts=%d modelLoads=%d: %s", w.Code, attempts, modelLoads, w.Body)
	}
}

func TestPersonalExclusionConflictWithInheritedEndpointRejectsBeforeAcceptance(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
		out := map[requestadaptation.Ref]requestadaptation.Snapshot{}
		for _, ref := range refs {
			d := requestadaptation.Empty(ref.Scope)
			if ref.Scope == requestadaptation.ScopePersonalModel {
				d.ExcludedRequestFields = []string{"temperature"}
			} else {
				d.BodyForced.Values["/temperature"] = json.RawMessage("0.8")
			}
			out[ref] = requestadaptation.Snapshot{Document: d, Revision: "1"}
		}
		return out
	})
	w := policyIngress(f, "/v1/chat/completions", `{"model":"provider/model","messages":[],"temperature":0.7}`, 1)
	if w.Code != 409 || len(f.claims.accepts) != 0 || f.openAI.calls != 0 {
		t.Fatalf("accepted conflicting configuration: %d %s", w.Code, w.Body)
	}
}
