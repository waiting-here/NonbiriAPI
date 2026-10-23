package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
)

type mfaStreamVerifier struct {
	until int64
	err   error
}

type mfaCaptureIdentity struct{}

func (mfaCaptureIdentity) VerifyDebugIdentity(context.Context, int64, string) (debug.IdentityState, error) {
	return debug.IdentityActive, nil
}
func (mfaCaptureIdentity) VerifyDebugBrowserIdentity(context.Context, int64, string) (debug.IdentityState, error) {
	return debug.IdentityUncertain, authz.ErrMFARequired
}

func TestMFAInvalidationClosesConnectionsAndPreservesCapture(t *testing.T) {
	hub, err := debug.NewHub(mfaCaptureIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	metadata, _, err := hub.Start(7, "binding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.ChangeMode(7, metadata.Revision, debug.ModeLive, true); err != nil {
		t.Fatal(err)
	}
	decision, err := hub.DecideAfterAdmission(t.Context(), debug.CaptureInput{UserID: 7, RouteKind: debug.RouteOpenAIChat, Model: "synthetic", MediaType: "application/json", Body: []byte(`{}`), IdentityCertain: true})
	if err != nil || decision.Trace == nil {
		t.Fatal("capture not admitted", err)
	}
	connections := newAccountEventConnections()
	stream, unregister, err := connections.Register(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	limitsChanged := 0
	fanout := &userSessionInvalidationFanout{debug: hub, connections: connections, limitsChanged: func(int64) { limitsChanged++ }}
	fanout.UserLimitsChanged(7)
	if stream.Err() != nil || decision.Trace.Context().Err() != nil || limitsChanged != 1 {
		t.Fatal("limit change cancelled an existing connection or capture")
	}
	fanout.UserMFAChanged(7)
	if stream.Err() == nil || decision.Trace.Context().Err() != nil || limitsChanged != 1 {
		t.Fatal("MFA change crossed the browser connection boundary")
	}
	metadata, err = hub.Metadata(7)
	if err != nil || !metadata.Active || metadata.Mode != debug.ModeLive {
		t.Fatal(metadata, err)
	}
	fanout.UserSessionInvalidated(7)
	if decision.Trace.Context().Err() == nil || limitsChanged != 2 {
		t.Fatal("first-factor revocation did not stop capture")
	}
}

func (v mfaStreamVerifier) VerifyUserSessionMFA(context.Context, int64, string) (int64, error) {
	return v.until, v.err
}

func TestMFAStreamDeadlineAndChallenge(t *testing.T) {
	parent := context.Background()
	plain, cancel, err := authorizedMFAStream(parent, mfaStreamVerifier{}, 7, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, ok := plain.Deadline(); ok {
		t.Fatal("optional unconfigured account gained a deadline")
	}
	until := time.Now().Add(time.Hour).Unix()
	verified, cancelVerified, err := authorizedMFAStream(parent, mfaStreamVerifier{until: until}, 7, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelVerified()
	if deadline, ok := verified.Deadline(); !ok || deadline.Unix() != until {
		t.Fatalf("proof deadline = %v, %v", deadline, ok)
	}
	expired, cancelExpired, err := authorizedMFAStream(parent, mfaStreamVerifier{until: time.Now().Add(-time.Second).Unix()}, 7, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelExpired()
	if !errors.Is(expired.Err(), context.DeadlineExceeded) {
		t.Fatalf("expired proof context: %v", expired.Err())
	}
	denied, cancelDenied, err := authorizedMFAStream(parent, mfaStreamVerifier{err: authz.ErrMFARequired}, 7, "session")
	if !errors.Is(err, authz.ErrMFARequired) || denied != nil || cancelDenied != nil {
		t.Fatal("challenge admitted a stream")
	}
}

func TestMFASurvivesDomainErrorTranslation(t *testing.T) {
	for name, translate := range map[string]func(error) error{
		"donation": translateDonationHeldReadError,
		"logs":     translateLogHeldReadError,
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(translate(authz.ErrMFARequired), authz.ErrMFARequired) {
				t.Fatal("MFA challenge lost in domain translation")
			}
		})
	}
	for name, write := range map[string]func(http.ResponseWriter, error){
		"handshake":        writeMFAStreamError,
		"maintenance read": func(w http.ResponseWriter, err error) { (&accountEventsHTTP{}).writeRPSError(w, nil, err) },
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			write(response, authz.ErrMFARequired)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"mfa_required"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}
