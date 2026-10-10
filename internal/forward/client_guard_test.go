package forward

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/clientguard"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type fakeCharityGuard struct {
	decision clientguard.Decision
	err      error
	calls    int
	times    []int64
}

func (guard *fakeCharityGuard) CheckCharityCall(_ context.Context, _ int64, _ string, now int64) (clientguard.Decision, error) {
	guard.calls++
	guard.times = append(guard.times, now)
	return guard.decision, guard.err
}

func TestClientRuleGuardPrecedesAdmissionAndDryCapture(t *testing.T) {
	for _, tc := range []struct {
		name           string
		decision       clientguard.Decision
		guardError     error
		preflightError error
		status         int
		code           string
		wantAdmission  int
	}{
		{"matched short request", clientguard.Decision{Banned: true}, nil, &charityrouting.ContentTooShortError{Actual: 1, Minimum: 10}, http.StatusForbidden, httperr.CodeForbidden, 0},
		{"matched insufficient balance", clientguard.Decision{Banned: true}, nil, charityrouting.ErrInsufficientCredits, http.StatusForbidden, httperr.CodeForbidden, 0},
		{"matched valid Dry request", clientguard.Decision{Banned: true}, nil, nil, http.StatusForbidden, httperr.CodeForbidden, 0},
		{"guard unavailable", clientguard.Decision{}, errors.New("guard unavailable"), nil, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable, 0},
		{"unmatched valid Dry request", clientguard.Decision{}, nil, nil, http.StatusUnprocessableEntity, httperr.CodeDebugDryRunIntercepted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
			f := newServiceFixture(t, capture)
			guard := &fakeCharityGuard{decision: tc.decision, err: tc.guardError}
			f.service.charityGuard = guard
			f.charity.preErr = tc.preflightError
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"x"}]}`)
			recorder := httptest.NewRecorder()
			f.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
			if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("response=%d %s, want %d %s", recorder.Code, recorder.Body.String(), tc.status, tc.code)
			}
			if f.charity.policyCalls != 1 || guard.calls != 1 || f.charity.preCalls != tc.wantAdmission || capture.calls != tc.wantAdmission {
				t.Fatalf("ordering: policy=%d guard=%d admission=%d capture=%d", f.charity.policyCalls, guard.calls, f.charity.preCalls, capture.calls)
			}
			if f.charity.snapCalls != 0 || len(f.claims.events) != 0 || f.openAI.calls != 0 || f.charges.calls != 0 {
				t.Fatalf("rejection reached execution: snapshots=%d claims=%v connectors=%d charges=%d", f.charity.snapCalls, f.claims.events, f.openAI.calls, f.charges.calls)
			}
			if tc.wantAdmission == 1 && guard.times[0] != f.charity.preTimes[0] {
				t.Fatalf("guard changed request decision time: %v %v", guard.times, f.charity.preTimes)
			}
		})
	}
}

func TestClientRuleGuardRequiresLogicalModelAccess(t *testing.T) {
	for _, denied := range []error{charityrouting.ErrUnauthorized, charityrouting.ErrForbidden, charityrouting.ErrNotFound} {
		t.Run(denied.Error(), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			guard := &fakeCharityGuard{decision: clientguard.Decision{Banned: true}}
			f.service.charityGuard = guard
			f.charity.policyErr = denied
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
			recorder := httptest.NewRecorder()
			f.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
			if guard.calls != 0 || f.charity.preCalls != 0 || recorder.Code != failureForError(denied, true).status {
				t.Fatalf("unresolved or unauthorized model triggered guard: calls=%d preflight=%d status=%d", guard.calls, f.charity.preCalls, recorder.Code)
			}
		})
	}
}
