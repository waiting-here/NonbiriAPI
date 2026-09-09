package forward

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestCharityLevelDenialPrecedesDryAndLiveCapture(t *testing.T) {
	for _, mode := range []debug.Mode{debug.ModeDry, debug.ModeLive} {
		t.Run(string(mode), func(t *testing.T) {
			capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: mode, Language: "en"}}
			fixture := newServiceFixture(t, capture)
			fixture.charity.preErr = charityrouting.ErrForbidden
			request := decodeChatForTest(t, "{\"model\":\"[公益]care/model\",\"messages\":[]}")
			recorder := httptest.NewRecorder()
			fixture.service.Chat(context.Background(), recorder, 7, request, []byte("{}"), "application/json", "en")
			if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), httperr.CodeForbidden) {
				t.Fatalf("denial %d %s", recorder.Code, recorder.Body.String())
			}
			if fixture.charity.snapCalls != 0 || len(fixture.claims.events) != 0 || capture.calls != 0 || fixture.openAI.calls != 0 || fixture.charges.calls != 0 {
				t.Fatal("denied caller reached capture, candidates, claims, or accounting")
			}
		})
	}
}

func TestCharityPermissionLostBeforeDispatchReleasesWithoutSending(t *testing.T) {
	for _, failure := range []error{claim.ErrForbidden, claim.ErrModelUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.claims.takeErrors = map[int]error{0: failure}
			request := decodeChatForTest(t, "{\"model\":\"[公益]care/model\",\"messages\":[]}")
			recorder := httptest.NewRecorder()
			fixture.service.Chat(context.Background(), recorder, 1, request, []byte("{}"), "application/json", "en")
			status, code := http.StatusForbidden, httperr.CodeForbidden
			if errors.Is(failure, claim.ErrModelUnavailable) {
				status, code = http.StatusNotFound, httperr.CodeNotFound
			}
			if recorder.Code != status || !strings.Contains(recorder.Body.String(), code) {
				t.Fatalf("denial %d %s", recorder.Code, recorder.Body.String())
			}
			if fixture.openAI.calls != 0 || fixture.charges.calls != 0 || fixture.claims.releaseCall != 1 || len(fixture.claims.requestResults) != 1 {
				t.Fatalf("execution or release mismatch: events=%v charges=%d", fixture.claims.events, fixture.charges.calls)
			}
			if result := fixture.claims.requestResults[0]; result.Disposition != claim.AccountingRelease || result.Caller.Status != status {
				t.Fatalf("terminal %+v", result)
			}
		})
	}
}

func TestModelDiscoveryPassesTheCurrentCallerToCharityRouting(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	if _, err := fixture.service.ListModels(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ListModels(context.Background(), 19); err != nil {
		t.Fatal(err)
	}
	if len(fixture.charity.listUsers) != 2 || fixture.charity.listUsers[0] != 7 || fixture.charity.listUsers[1] != 19 {
		t.Fatalf("callers %v", fixture.charity.listUsers)
	}
}
