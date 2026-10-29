package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
)

func TestAdminMFAReadAvoidsWriterAndChecksLiveAuthority(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "admin-mfa-reader", "")
	user, userActor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, userActor)
	defer clear(seed)
	adminRaw, _, err := f.runtime.ensureAdminAndSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.runtime.authenticate(t.Context(), adminRaw, authz.ActorAdminSession, "")
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	waits := f.store.DB().Stats().WaitCount
	read := func(ctx context.Context, actor authz.Actor, target int64, status int, body string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("https://admin.example/admin/api/users/%d/mfa", target), nil)
		req.SetPathValue("id", fmt.Sprint(target))
		req = req.WithContext(withActor(ctx, actor))
		rec := httptest.NewRecorder()
		checkReleased := func() {
			if f.store.ReadDB().Stats().InUse != 0 {
				t.Fatal("MFA response retained reader")
			}
		}
		f.runtime.mfaAdminHTTP(releasedReaderResponse{rec, checkReleased}, req)
		if rec.Code != status || !strings.Contains(rec.Body.String(), body) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if f.store.DB().Stats().WaitCount != waits {
			t.Fatal("MFA read waited for writer")
		}
	}
	read(t.Context(), admin.actor, user, http.StatusOK, `"enrolled":true`)
	read(t.Context(), userActor, user, http.StatusForbidden, "forbidden")
	read(t.Context(), admin.actor, admin.actor.UserID, http.StatusForbidden, "forbidden")
	read(t.Context(), admin.actor, user+admin.actor.UserID+1, http.StatusNotFound, "not_found")
	if _, err := held.ExecContext(t.Context(), `DELETE FROM account_mfa_credentials WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	read(t.Context(), admin.actor, user, http.StatusOK, `"enrolled":false`)
	if _, err := held.ExecContext(t.Context(), `DELETE FROM sessions WHERE token_hash=?`, admin.actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	read(t.Context(), admin.actor, user, http.StatusUnauthorized, "unauthorized")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	read(cancelled, admin.actor, user, http.StatusInternalServerError, `"code":"internal"`)
}

func TestAdminMFAResetCancellationWhileWriterOccupied(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "admin-mfa-cancel", "")
	user, userActor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, userActor)
	defer clear(seed)
	adminRaw, _, err := f.runtime.ensureAdminAndSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.runtime.authenticate(t.Context(), adminRaw, authz.ActorAdminSession, "")
	if err != nil {
		t.Fatal(err)
	}
	admin.actor.ElevationToken, _, err = f.runtime.elevation.IssueBound(admin.actor.UserID, elevation.KindAdmin, admin.actor.SessionTokenHash)
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	waits := f.store.DB().Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("https://admin.example/admin/api/users/%d/mfa/reset", user), nil)
	req.SetPathValue("id", fmt.Sprint(user))
	req = req.WithContext(withActor(ctx, admin.actor))
	rec := httptest.NewRecorder()
	f.runtime.mfaAdminHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || ctx.Err() != context.DeadlineExceeded || f.store.DB().Stats().WaitCount <= waits {
		t.Fatalf("cancelled reset status=%d context=%v waits=%d", rec.Code, ctx.Err(), f.store.DB().Stats().WaitCount-waits)
	}
	var enrolled, session bool
	if err := held.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM account_mfa_credentials WHERE user_id=?),EXISTS(SELECT 1 FROM sessions WHERE token_hash=?)`, user, userActor.SessionTokenHash).Scan(&enrolled, &session); err != nil || !enrolled || !session {
		t.Fatalf("cancelled reset enrolled=%v session=%v err=%v", enrolled, session, err)
	}
	if f.store.ReadDB().Stats().InUse != 0 {
		t.Fatal("cancelled reset retained reader")
	}
}
