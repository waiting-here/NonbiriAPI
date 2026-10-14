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
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
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
		if len(refs) == 1 && refs[0].Scope == requestadaptation.ScopePersonalModel {
			return map[requestadaptation.Ref]requestadaptation.Snapshot{refs[0]: {Document: requestadaptation.Empty(refs[0].Scope), Revision: "0"}}
		}
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
	if loads != 1 || fixture.personal.preCalls != 1 || fixture.openAI.calls != 1 || string(sent["reasoning_effort"]) != `"low"` || sent["thinking"] == nil {
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

func TestFrozenAdaptationsSharePreparationButNotDispatchData(t *testing.T) {
	for _, bodyRule := range []bool{false, true} {
		t.Run(fmt.Sprint(bodyRule), func(t *testing.T) {
			request := chatRequest(decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"original"}]}`))
			defer request.Clear()
			service := &Service{adaptations: adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
				out := make(map[requestadaptation.Ref]requestadaptation.Snapshot)
				for _, ref := range refs {
					doc := requestadaptation.Empty(ref.Scope)
					doc.FixedHeaders.Values["X-Research"] = "original"
					doc.FixedHeaders.SensitivePaths = map[string][]string{"X-Research": {""}}
					if bodyRule {
						doc.BodyForced.Values["/temperature"] = json.RawMessage(`0.2`)
					}
					out[ref] = requestadaptation.Snapshot{Document: doc, Revision: "1"}
				}
				return out
			})}
			plan := executionPlan{candidates: []RouteCandidate{
				{EndpointID: 1, ConnectorType: connectorcontract.TypeOpenAICompatible},
				{EndpointID: 2, ConnectorType: connectorcontract.TypeOpenAICompatible},
			}}
			if err := service.freezeAdaptations(t.Context(), request, &plan, nil); err != nil {
				t.Fatal(err)
			}
			defer plan.clearPrepared()
			shared := plan.candidates[0].prepared
			if shared != plan.candidates[1].prepared {
				t.Fatal("same effective rules prepared twice")
			}
			if !bodyRule && shared.request != nil {
				t.Fatal("header-only adaptation copied the body")
			}
			plan.transportRule = transportpolicy.ForceStream
			if err := freezeTransport(request, &plan); err != nil {
				t.Fatal(err)
			}
			if plan.candidates[0].prepared != plan.candidates[1].prepared || !shared.source(request).Stream {
				t.Fatal("transport lost shared preparation")
			}
			// Removing one unsupported candidate must not clear the request
			// that a remaining candidate will dispatch.
			plan.clearUnusedPrepared(plan.candidates[1:])
			first := shared.source(request).CloneForAttempt()
			second := shared.source(request).CloneForAttempt()
			first.Clear()
			defer second.Clear()
			if raw, err := second.chat.LogicalBody(); err != nil || !json.Valid(raw) {
				t.Fatal("retry source was cleared", err)
			}
			shared.native = map[string]json.RawMessage{"/thinking": json.RawMessage(`{"budget":2048}`)}
			var a, b connectorcontract.AttemptPolicy
			clearA, clearB := shared.applyPolicy(&a), shared.applyPolicy(&b)
			if len(a.ResponseSecrets) != 1 || string(a.ResponseSecrets[0]) != "original" {
				t.Fatal("marked header not frozen")
			}
			a.ResponseSecrets[0][0] = 'x'
			a.AdditionalHeaders.Set("X-Research", "changed")
			a.NativeExtensions["/thinking"][0] = 'x'
			clearA()
			if string(b.ResponseSecrets[0]) != "original" || string(shared.secrets[0]) != "original" || a.ResponseSecrets != nil {
				t.Fatal("secret lifetime crossed retry ownership")
			}
			if b.AdditionalHeaders.Get("X-Research") != "original" || !json.Valid(b.NativeExtensions["/thinking"]) || !json.Valid(shared.native["/thinking"]) {
				t.Fatal("dispatch mutated shared adaptation")
			}
			clearB()
		})
	}
}

func TestHeaderOnlySensitiveAttemptCleanup(t *testing.T) {
	p := &preparedAttempt{secrets: [][]byte{[]byte("short-lived-secret")}}
	var policy connectorcontract.AttemptPolicy
	cleanup := p.applyPolicy(&policy)
	alias := policy.ResponseSecrets[0]
	cleanup()
	for _, value := range alias {
		if value != 0 {
			t.Fatal("header-only cleanup retained secret")
		}
	}
	if string(p.secrets[0]) != "short-lived-secret" {
		t.Fatal("dispatch cleanup mutated preparation")
	}
	preparedAlias := p.secrets[0]
	p.clear()
	for _, value := range preparedAlias {
		if value != 0 {
			t.Fatal("prepared cleanup retained secret")
		}
	}
}

func TestPreparedBodySecretsReachEachConnectorPolicy(t *testing.T) {
	for _, kind := range []connectorcontract.Type{connectorcontract.TypeOpenAICompatible, connectorcontract.TypeAnthropicCompatible, connectorcontract.TypeAISDKGatewayV3} {
		for _, callerValue := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/caller%v", kind, callerValue), func(t *testing.T) {
				raw := `{"model":"provider/model","messages":[{"role":"user","content":"hello"}]}`
				if callerValue {
					raw = `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"vendor":{"token":"caller-value"}}`
				}
				request := chatRequest(decodeChatForTest(t, raw))
				defer request.Clear()
				doc := requestadaptation.Empty(requestadaptation.ScopeEndpoint)
				doc.BodyDefaults.Values["/vendor"] = json.RawMessage(`{"token":"default-secret"}`)
				doc.BodyDefaults.SensitivePaths = map[string][]string{"/vendor": {"/token"}}
				doc.BodyForced.Values["/extra"] = json.RawMessage(`"forced-secret"`)
				doc.BodyForced.SensitivePaths = map[string][]string{"/extra": {""}}
				if kind != connectorcontract.TypeOpenAICompatible {
					doc.NativeExtensionPaths.Values = []string{"/vendor", "/extra"}
				}
				prepared, err := prepareAdaptedAttempt(request, kind, doc, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.clear()
				var policy connectorcontract.AttemptPolicy
				cleanup := prepared.applyPolicy(&policy)
				defer cleanup()
				want := 2
				if callerValue {
					want = 1
				}
				if len(policy.ResponseSecrets) != want || string(policy.ResponseSecrets[0]) != "forced-secret" || !callerValue && string(policy.ResponseSecrets[1]) != "default-secret" {
					t.Fatal("applied body secrets did not reach dispatch")
				}
				if kind != connectorcontract.TypeOpenAICompatible && string(policy.NativeExtensions["/extra"]) != `"forced-secret"` {
					t.Fatal("native extension was discarded")
				}
			})
		}
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
