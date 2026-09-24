package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func policyIngress(f *serviceFixture, path, body string, user int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), callerIdentityContextKey{}, resources.CallerIdentity{UserID: user}))
	w := httptest.NewRecorder()
	NewHandler(f.service).ServeHTTP(w, r)
	return w
}

func TestCharityExclusionPrecedesOptionalValidationAndDebug(t *testing.T) {
	for _, test := range []struct{ name, path, field, input string }{
		{"chat", "/v1/chat/completions", "temperature", `"messages":[{"role":"user","content":"hello"}],"temperature":{"invalid":"excluded"}`},
		{"embedding", "/v1/embeddings", "dimensions", `"input":"hello","dimensions":{"invalid":"excluded"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
			f := newServiceFixture(t, capture)
			f.charity.policyFields = []string{test.field}
			body := `{"model":"[公益]care/model",` + test.input + `}`
			w := policyIngress(f, test.path, body, 17)
			if w.Code != http.StatusUnprocessableEntity || capture.calls != 1 || f.charity.policyCalls != 1 || f.charity.snapCalls != 0 || len(f.claims.events) != 0 {
				t.Fatalf("filtered dry ingress: %d %s, policy=%d capture=%d claims=%v", w.Code, w.Body.String(), f.charity.policyCalls, capture.calls, f.claims.events)
			}
			var filtered map[string]json.RawMessage
			if json.Unmarshal(capture.body, &filtered) != nil || filtered[test.field] != nil || bytes.Contains(capture.body, []byte("excluded")) {
				t.Fatalf("unfiltered optional value reached debug: %s", capture.body)
			}
			personal := strings.Replace(body, "[公益]care/model", "provider/model", 1)
			w = policyIngress(f, test.path, personal, 17)
			if test.name == "embedding" {
				if w.Code != http.StatusBadRequest || capture.calls != 1 || f.charity.policyCalls != 1 {
					t.Fatalf("personal validation unexpectedly changed: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != http.StatusUnprocessableEntity || capture.calls != 2 || f.charity.policyCalls != 1 || !bytes.Contains(capture.body, []byte("temperature")) {
				// Chat's optional parameters retain their existing pass-through
				// semantics; only charity requests apply model exclusions.
				t.Fatalf("personal chat filtering unexpectedly changed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCharityExclusionIsFrozenAcrossRetriesAndCallerScoped(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.charity.policyFields = []string{"temperature", "store", "safety_identifier", "user"}
	f.charity.snapshot.CharityPreflight = f.charity.preflight
	second := f.charity.snapshot.Candidates[0]
	second.EndpointID, second.EndpointKeyID, second.DonationKeyID = 31, 32, 33
	f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, second)
	f.addDispatch(f.charity.snapshot.Candidates[0])
	f.addDispatch(second)
	attempts := 0
	f.openAI.attempt = func(_ context.Context, in connector.AttemptInput) contract.AttemptResult {
		attempts++
		for _, field := range []string{"temperature", "store", "safety_identifier", "user"} {
			if _, present := in.Ingress.RawField(field); present || !in.Ingress.FieldExcluded(field) {
				t.Fatalf("attempt %d restored excluded %s", attempts, field)
			}
		}
		if attempts == 1 {
			f.charity.policyFields = nil
			return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503, Diagnostic: "upstream returned HTTP 503"}
		}
		_, _ = in.Sink.Write([]byte(`{"id":"filtered-success"}`))
		return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}
	}
	w := policyIngress(f, "/v1/chat/completions", `{"model":"[公益]care/model","messages":[],"temperature":0.7,"store":true,"safety_identifier":"caller","user":"caller"}`, 37)
	if w.Code != http.StatusOK || attempts != 2 || f.charity.policyCalls != 1 || len(f.charity.snapUsers) != 1 || f.charity.snapUsers[0] != 37 {
		t.Fatalf("retry scope: %d %s attempts=%d policy=%d users=%v", w.Code, w.Body.String(), attempts, f.charity.policyCalls, f.charity.snapUsers)
	}
}

func TestCharityExclusionCannotCrossModelReplacementOrDeniedPolicy(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "denied"}[denied], func(t *testing.T) {
			capture := &fakeDebugCapture{}
			f := newServiceFixture(t, capture)
			want := http.StatusNotFound
			if denied {
				f.charity.policyErr = charityrouting.ErrForbidden
				want = http.StatusForbidden
			} else {
				f.charity.policyModelID = 99
			}
			w := policyIngress(f, "/v1/chat/completions", `{"model":"[公益]care/model","messages":[]}`, 1)
			if w.Code != want || capture.calls != 0 || f.charity.snapCalls != 0 || len(f.claims.events) != 0 || f.openAI.calls != 0 {
				t.Fatalf("policy boundary: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCharityDirectPolicyFiltersIndependentCopy(t *testing.T) {
	capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
	f := newServiceFixture(t, capture)
	f.charity.policyFields = []string{"temperature"}
	body := []byte(`{"model":"[公益]care/model","messages":[],"temperature":0.7}`)
	original := append([]byte(nil), body...)
	request := decodeChatForTest(t, string(body))
	w := httptest.NewRecorder()
	f.service.Chat(context.Background(), w, 1, request, body, "application/json", "en")
	if w.Code != http.StatusUnprocessableEntity || bytes.Contains(capture.body, []byte("temperature")) || !bytes.Equal(body, original) {
		t.Fatalf("direct policy: %d %s", w.Code, w.Body.String())
	}
	if _, present := request.RawField("temperature"); !present || request.FieldExcluded("temperature") {
		t.Fatal("direct caller's request was mutated")
	}
}
