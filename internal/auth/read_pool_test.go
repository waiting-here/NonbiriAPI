package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestSessionPureValidationUsesReaderAndSeesRevocation(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "reader-session", "")
	user, actor := userSessionIdentity(t, f, cookie)
	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if state, err := f.runtime.VerifyUserSessionBinding(ctx, user, actor.SessionTokenHash); err != nil || state != UserSessionBindingActive {
		t.Fatal(state, err)
	}
	if _, err := f.runtime.VerifyUserSessionMFA(ctx, user, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, release, err := f.runtime.admitUserLifecycle(ctx, actor, false); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	tx, err := f.runtime.mfaBegin(ctx, actor, false)
	if err != nil {
		t.Fatal(err)
	}
	var queryOnly int
	err = tx.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly)
	tx.Rollback()
	if err != nil || queryOnly != 1 {
		t.Fatalf("queryOnly=%d err=%v", queryOnly, err)
	}
	held.Close()

	if _, err := f.store.DB().Exec("UPDATE site_config SET value='1' WHERE key='mfa_required_level_1'"); err != nil {
		t.Fatal(err)
	}
	if _, release, err := f.runtime.admitUserLifecycle(t.Context(), actor, false); !errors.Is(err, authz.ErrMFARequired) {
		if release != nil {
			release()
		}
		t.Fatal("new admission ignored MFA policy", err)
	}
	if _, err := f.runtime.VerifyUserSessionMFA(t.Context(), user, actor.SessionTokenHash); !errors.Is(err, authz.ErrMFARequired) {
		t.Fatal("stream check ignored MFA policy", err)
	}
	if state, err := f.runtime.VerifyUserSessionBinding(t.Context(), user, actor.SessionTokenHash); err != nil || state != UserSessionBindingActive {
		t.Fatal("MFA bootstrap identity changed", state, err)
	}

	if _, err := f.runtime.deleteSession(t.Context(), cookie.Value); err != nil {
		t.Fatal(err)
	}
	if state, err := f.runtime.VerifyUserSessionBinding(t.Context(), user, actor.SessionTokenHash); err != nil || state != UserSessionBindingRevoked {
		t.Fatal("revoked binding", state, err)
	}
	if _, err := f.runtime.VerifyUserSessionMFA(t.Context(), user, actor.SessionTokenHash); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("revoked MFA identity", err)
	}
	if _, release, err := f.runtime.admitUserLifecycle(t.Context(), actor, false); err == nil {
		release()
		t.Fatal("revoked admission succeeded")
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := f.runtime.VerifyUserSessionBinding(cancelled, user, actor.SessionTokenHash); err == nil {
		t.Fatal("cancelled identity check succeeded")
	}
}

type statusWriterProbe struct {
	*httptest.ResponseRecorder
	beforeWrite func()
}

func (w statusWriterProbe) Write(body []byte) (int, error) {
	w.beforeWrite()
	return w.ResponseRecorder.Write(body)
}

func TestMFAStatusReleasesSnapshotBeforeResponse(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-reader-output", "")
	_, actor := userSessionIdentity(t, f, cookie)
	writes := 0
	response := httptest.NewRecorder()
	writer := statusWriterProbe{response, func() {
		writes++
		if f.store.DB().Stats().InUse != 0 || f.store.ReadDB().Stats().InUse != 0 {
			t.Fatal("response retained database transaction")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		conn, err := f.store.DB().Conn(ctx)
		if err != nil {
			t.Fatal("response blocked writer", err)
		}
		conn.Close()
	}}
	request := httptest.NewRequest(http.MethodGet, "https://user.example/api/auth/mfa", nil)
	request = request.WithContext(withActor(request.Context(), actor))
	f.runtime.mfaStatusHTTP(writer, request)
	if response.Code != http.StatusOK || writes != 1 {
		t.Fatal(response.Code, writes, response.Body.String())
	}
}
