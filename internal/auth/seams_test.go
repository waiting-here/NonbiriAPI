package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type observedInvalidation struct {
	userID int64
	state  UserSessionBindingState
	err    error
}

func TestAttachedUserLifecycleGatePreservesDeletingBrowserRequest(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	gate, err := lifecyclegate.New(lifecyclegate.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if err := fixture.runtime.AttachUserLifecycleGate(gate); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.AttachUserLifecycleGate(gate); !errors.Is(err, ErrUserLifecycleSet) {
		t.Fatalf("second lifecycle gate attachment err=%v", err)
	}
	if err := fixture.runtime.RegisterUserRoute(http.MethodPost, "/api/lifecycle-probe",
		func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
			retirement, beginErr := gate.BeginUserRetirementExcludingContext(request.Context(), principal.UserID)
			if beginErr != nil {
				t.Errorf("begin retirement: %v", beginErr)
				return
			}
			if request.Context().Err() != nil {
				t.Errorf("deleting request was canceled: %v", request.Context().Err())
			}
			if !retirement.Abort() {
				t.Error("retirement abort failed")
			}
			writer.WriteHeader(http.StatusNoContent)
		}); err != nil {
		t.Fatal(err)
	}
	cookie := loginUser(t, fixture, "lifecycle-gate", "")
	response := request(t, fixture.runtime.UserHandler(), host.StationUser, http.MethodPost,
		"https://user.example/api/lifecycle-probe", "", []*http.Cookie{cookie}, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("lifecycle probe status=%d body=%s", response.Code, response.Body.String())
	}
}

type recordingSessionInvalidationObserver struct {
	mu      sync.Mutex
	runtime *Runtime
	binding string
	records []observedInvalidation
}

func (observer *recordingSessionInvalidationObserver) UserSessionInvalidated(userID int64) {
	observer.mu.Lock()
	binding := observer.binding
	observer.mu.Unlock()
	state, err := observer.runtime.VerifyUserSessionBinding(context.Background(), userID, binding)
	observer.mu.Lock()
	observer.records = append(observer.records, observedInvalidation{userID: userID, state: state, err: err})
	observer.mu.Unlock()
}

func (observer *recordingSessionInvalidationObserver) setBinding(binding string) {
	observer.mu.Lock()
	observer.binding = binding
	observer.mu.Unlock()
}

func (observer *recordingSessionInvalidationObserver) snapshot() []observedInvalidation {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]observedInvalidation(nil), observer.records...)
}

func userSessionIdentity(t *testing.T, fixture *runtimeFixture, cookie *http.Cookie) (int64, authz.Actor) {
	t.Helper()
	binding := sessionLookupHash(cookie.Value)
	var userID int64
	var generation string
	if err := fixture.store.DB().QueryRow(`SELECT user_id,cred_gen FROM sessions WHERE token_hash=?`, binding).Scan(&userID, &generation); err != nil {
		t.Fatalf("read session identity: %v", err)
	}
	return userID, authz.Actor{
		Kind:              authz.ActorUserSession,
		UserID:            userID,
		SessionTokenHash:  binding,
		SessionGeneration: generation,
	}
}

func assertBindingState(t *testing.T, runtime *Runtime, userID int64, binding string, want UserSessionBindingState) {
	t.Helper()
	got, err := runtime.VerifyUserSessionBinding(context.Background(), userID, binding)
	if err != nil || got != want {
		t.Fatalf("VerifyUserSessionBinding = (%q,%v), want (%q,nil)", got, err, want)
	}
}

func TestVerifyUserSessionBindingClosedAuthorityStates(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	cookie := loginUser(t, fixture, "binding-user", "")
	userID, actor := userSessionIdentity(t, fixture, cookie)
	assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingActive)

	otherID, otherToken, _, err := fixture.runtime.registerUser(
		context.Background(),
		DiscordIdentity{ID: "discord-binding-other", Username: "Other"},
		GuildMember{Nick: "Other", Roles: []string{"role-1"}},
		"guild-1", "role-1",
	)
	if err != nil {
		t.Fatalf("register other user: %v", err)
	}
	otherBinding := sessionLookupHash(otherToken)
	assertBindingState(t, fixture.runtime, userID, otherBinding, UserSessionBindingRevoked)
	assertBindingState(t, fixture.runtime, otherID, actor.SessionTokenHash, UserSessionBindingRevoked)

	adminToken, _, err := fixture.runtime.ensureAdminAndSession(context.Background())
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	var adminID int64
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	adminBinding := sessionLookupHash(adminToken)
	assertBindingState(t, fixture.runtime, userID, adminBinding, UserSessionBindingRevoked)
	assertBindingState(t, fixture.runtime, adminID, adminBinding, UserSessionBindingRevoked)

	if _, err := fixture.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, authTestNow+60, userID); err != nil {
		t.Fatalf("set temporary ban: %v", err)
	}
	assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingBanned)
	fixture.clock.Add(time.Minute)
	assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingActive)

	if _, err := fixture.store.DB().Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingRevoked)
	if _, err := fixture.store.DB().Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingDeleted)
}

func TestVerifyUserSessionBindingRejectsNoncanonicalAndFailsClosed(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	cookie := loginUser(t, fixture, "binding-canonical", "")
	userID, actor := userSessionIdentity(t, fixture, cookie)
	for name, binding := range map[string]string{
		"empty":      "",
		"short":      strings.Repeat("a", 63),
		"uppercase":  strings.ToUpper(actor.SessionTokenHash),
		"non-hex":    strings.Repeat("g", 64),
		"raw-cookie": cookie.Value,
	} {
		t.Run(name, func(t *testing.T) {
			state, err := fixture.runtime.VerifyUserSessionBinding(context.Background(), userID, binding)
			if state != UserSessionBindingUncertain || !errors.Is(err, ErrInvalidUserSessionBinding) {
				t.Fatalf("state=%q error=%v", state, err)
			}
		})
	}
	state, err := fixture.runtime.VerifyUserSessionBinding(nil, userID, actor.SessionTokenHash)
	if state != UserSessionBindingUncertain || !errors.Is(err, ErrInvalidUserSessionBinding) {
		t.Fatalf("nil context state=%q error=%v", state, err)
	}
}

func TestVerifyUserSessionBindingDetectsPersistedInvariantAndDatabaseFailure(t *testing.T) {
	t.Run("multiple live sessions", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		cookie := loginUser(t, fixture, "binding-invariant", "")
		userID, actor := userSessionIdentity(t, fixture, cookie)
		duplicate := strings.Repeat("a", sha256HexBytes)
		if duplicate == actor.SessionTokenHash {
			duplicate = strings.Repeat("b", sha256HexBytes)
		}
		if _, err := fixture.store.DB().Exec(`
INSERT INTO sessions(token_hash,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,'',?,?,?,?,?)`, duplicate, userID, authTestNow, authTestNow+600, authTestNow+1200, authTestNow, "duplicate"); err != nil {
			t.Fatalf("insert duplicate live session: %v", err)
		}
		state, err := fixture.runtime.VerifyUserSessionBinding(context.Background(), userID, actor.SessionTokenHash)
		if state != UserSessionBindingUncertain || err == nil {
			t.Fatalf("state=%q error=%v", state, err)
		}
	})

	t.Run("database failure", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		cookie := loginUser(t, fixture, "binding-db-failure", "")
		userID, actor := userSessionIdentity(t, fixture, cookie)
		if err := fixture.store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		state, err := fixture.runtime.VerifyUserSessionBinding(context.Background(), userID, actor.SessionTokenHash)
		if state != UserSessionBindingUncertain || err == nil || errors.Is(err, ErrClosed) {
			t.Fatalf("state=%q error=%v", state, err)
		}
	})
}

func TestUserSessionInvalidationObserverAttachRules(t *testing.T) {
	fixture := newRuntimeFixture(t, nil)
	if err := fixture.runtime.AttachUserSessionInvalidationObserver(nil); !errors.Is(err, ErrInvalidUserSessionObserver) {
		t.Fatalf("nil observer error=%v", err)
	}
	first := &recordingSessionInvalidationObserver{runtime: fixture.runtime, binding: strings.Repeat("a", sha256HexBytes)}
	if err := fixture.runtime.AttachUserSessionInvalidationObserver(first); err != nil {
		t.Fatalf("attach first observer: %v", err)
	}
	if err := fixture.runtime.AttachUserSessionInvalidationObserver(&recordingSessionInvalidationObserver{runtime: fixture.runtime}); !errors.Is(err, ErrUserSessionInvalidationObserverSet) {
		t.Fatalf("duplicate observer error=%v", err)
	}

	frozen := newRuntimeFixture(t, nil)
	_ = frozen.runtime.UserHandler()
	if err := frozen.runtime.AttachUserSessionInvalidationObserver(&recordingSessionInvalidationObserver{runtime: frozen.runtime}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen observer error=%v", err)
	}

	closed := newRuntimeFixture(t, nil)
	if err := closed.runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	if err := closed.runtime.AttachUserSessionInvalidationObserver(&recordingSessionInvalidationObserver{runtime: closed.runtime}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed observer error=%v", err)
	}
}

func TestUserSessionInvalidationObserverCommittedReplacementAndRollback(t *testing.T) {
	t.Run("replacement notifies after commit and permits reentry", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		observer := &recordingSessionInvalidationObserver{runtime: fixture.runtime}
		if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
			t.Fatal(err)
		}
		cookie := loginUser(t, fixture, "observer-register", "")
		if got := observer.snapshot(); len(got) != 0 {
			t.Fatalf("new registration notifications=%+v", got)
		}
		userID, actor := userSessionIdentity(t, fixture, cookie)
		observer.setBinding(actor.SessionTokenHash)

		type replacementResult struct {
			token string
			err   error
		}
		result := make(chan replacementResult, 1)
		go func() {
			token, _, err := fixture.runtime.refreshExistingUser(
				context.Background(), userID,
				DiscordIdentity{ID: "discord-1", Username: "Replacement", Avatar: "replacement-avatar"}, nil,
			)
			result <- replacementResult{token: token, err: err}
		}()
		var replacement replacementResult
		select {
		case replacement = <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("replacement callback reentry deadlocked")
		}
		if replacement.err != nil {
			t.Fatalf("replace session: %v", replacement.err)
		}
		records := observer.snapshot()
		if len(records) != 1 || records[0].userID != userID || records[0].state != UserSessionBindingRevoked || records[0].err != nil {
			t.Fatalf("replacement notifications=%+v", records)
		}
		assertBindingState(t, fixture.runtime, userID, sessionLookupHash(replacement.token), UserSessionBindingActive)
	})

	t.Run("failed replacement rolls back without notification", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		observer := &recordingSessionInvalidationObserver{runtime: fixture.runtime}
		if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
			t.Fatal(err)
		}
		cookie := loginUser(t, fixture, "observer-rollback", "")
		userID, actor := userSessionIdentity(t, fixture, cookie)
		observer.setBinding(actor.SessionTokenHash)
		if _, err := fixture.store.DB().Exec(`CREATE TRIGGER reject_session_replacement BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT,'reject replacement'); END`); err != nil {
			t.Fatalf("create rejection trigger: %v", err)
		}
		if _, _, err := fixture.runtime.refreshExistingUser(
			context.Background(), userID,
			DiscordIdentity{ID: "discord-1", Username: "Rejected", Avatar: "rejected-avatar"}, nil,
		); err == nil {
			t.Fatal("replacement unexpectedly succeeded")
		}
		if got := observer.snapshot(); len(got) != 0 {
			t.Fatalf("rollback notifications=%+v", got)
		}
		assertBindingState(t, fixture.runtime, userID, actor.SessionTokenHash, UserSessionBindingActive)
	})
}

func TestUserSessionInvalidationObserverLogoutExpiryAndAdminExclusion(t *testing.T) {
	t.Run("logout", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		observer := &recordingSessionInvalidationObserver{runtime: fixture.runtime}
		if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
			t.Fatal(err)
		}
		cookie := loginUser(t, fixture, "observer-logout", "")
		userID, actor := userSessionIdentity(t, fixture, cookie)
		observer.setBinding(actor.SessionTokenHash)
		response := request(t, fixture.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{cookie}, nil)
		if response.Code != http.StatusNoContent {
			t.Fatalf("logout=%d %s", response.Code, response.Body.String())
		}
		records := observer.snapshot()
		if len(records) != 1 || records[0].userID != userID || records[0].state != UserSessionBindingRevoked || records[0].err != nil {
			t.Fatalf("logout notifications=%+v", records)
		}
	})

	t.Run("authenticate expires user session", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		observer := &recordingSessionInvalidationObserver{runtime: fixture.runtime}
		if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
			t.Fatal(err)
		}
		cookie := loginUser(t, fixture, "observer-expiry", "")
		userID, actor := userSessionIdentity(t, fixture, cookie)
		observer.setBinding(actor.SessionTokenHash)
		if _, err := fixture.store.DB().Exec(`UPDATE sessions SET expires_at=last_seen_at WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
			t.Fatalf("expire session: %v", err)
		}
		response := request(t, fixture.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expired session=%d %s", response.Code, response.Body.String())
		}
		records := observer.snapshot()
		if len(records) != 1 || records[0].userID != userID || records[0].state != UserSessionBindingRevoked || records[0].err != nil {
			t.Fatalf("expiry notifications=%+v", records)
		}
	})

	t.Run("admin session does not notify", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		observer := &recordingSessionInvalidationObserver{runtime: fixture.runtime, binding: strings.Repeat("a", sha256HexBytes)}
		if err := fixture.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
			t.Fatal(err)
		}
		login := request(t, fixture.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
		if login.Code != http.StatusOK {
			t.Fatalf("admin login=%d %s", login.Code, login.Body.String())
		}
		adminCookie := responseCookie(t, login, AdminSessionCookieName)
		logout := request(t, fixture.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/logout", "", []*http.Cookie{adminCookie}, nil)
		if logout.Code != http.StatusNoContent {
			t.Fatalf("admin logout=%d %s", logout.Code, logout.Body.String())
		}
		if got := observer.snapshot(); len(got) != 0 {
			t.Fatalf("admin notifications=%+v", got)
		}
	})
}

func authorizeStewardInNewTx(t *testing.T, fixture *runtimeFixture, ctx context.Context, userID int64) error {
	t.Helper()
	tx, err := fixture.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read transaction: %v", err)
	}
	defer tx.Rollback()
	return fixture.runtime.AuthorizeStewardFinal(ctx, tx, userID)
}

func newStewardActor(t *testing.T, fixture *runtimeFixture, code string) (int64, authz.Actor) {
	t.Helper()
	cookie := loginUser(t, fixture, code, "")
	userID, actor := userSessionIdentity(t, fixture, cookie)
	if _, err := fixture.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, userID); err != nil {
		t.Fatalf("promote steward: %v", err)
	}
	return userID, actor
}

func TestAuthorizeStewardFinalRechecksCallerTransaction(t *testing.T) {
	t.Run("valid and foreign", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		userID, actor := newStewardActor(t, fixture, "steward-valid")
		ctx := withActor(context.Background(), actor)
		if err := authorizeStewardInNewTx(t, fixture, ctx, userID); err != nil {
			t.Fatalf("valid steward: %v", err)
		}
		if err := authorizeStewardInNewTx(t, fixture, ctx, userID+1); !errors.Is(err, authz.ErrUnauthorized) {
			t.Fatalf("foreign user error=%v", err)
		}
	})

	t.Run("live demotion", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		userID, actor := newStewardActor(t, fixture, "steward-demotion")
		if _, err := fixture.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, userID); err != nil {
			t.Fatalf("demote steward: %v", err)
		}
		if err := authorizeStewardInNewTx(t, fixture, withActor(context.Background(), actor), userID); !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("demoted steward error=%v", err)
		}
	})

	t.Run("session revocation", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		userID, actor := newStewardActor(t, fixture, "steward-revoked")
		if _, err := fixture.store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
			t.Fatalf("revoke session: %v", err)
		}
		if err := authorizeStewardInNewTx(t, fixture, withActor(context.Background(), actor), userID); !errors.Is(err, authz.ErrUnauthorized) {
			t.Fatalf("revoked session error=%v", err)
		}
	})

	t.Run("session generation replacement", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		userID, actor := newStewardActor(t, fixture, "steward-generation")
		if _, err := fixture.store.DB().Exec(`UPDATE sessions SET cred_gen='replacement-generation' WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
			t.Fatalf("replace generation: %v", err)
		}
		if err := authorizeStewardInNewTx(t, fixture, withActor(context.Background(), actor), userID); !errors.Is(err, authz.ErrUnauthorized) {
			t.Fatalf("replaced generation error=%v", err)
		}
	})

	t.Run("admin actor", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		token, _, err := fixture.runtime.ensureAdminAndSession(context.Background())
		if err != nil {
			t.Fatalf("create admin session: %v", err)
		}
		binding := sessionLookupHash(token)
		var adminID int64
		var generation string
		if err := fixture.store.DB().QueryRow(`SELECT user_id,cred_gen FROM sessions WHERE token_hash=?`, binding).Scan(&adminID, &generation); err != nil {
			t.Fatalf("read admin actor: %v", err)
		}
		actor := authz.Actor{Kind: authz.ActorAdminSession, UserID: adminID, SessionTokenHash: binding, SessionGeneration: generation}
		if err := authorizeStewardInNewTx(t, fixture, withActor(context.Background(), actor), adminID); !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("admin actor error=%v", err)
		}
	})

	t.Run("database error remains internal", func(t *testing.T) {
		fixture := newRuntimeFixture(t, nil)
		userID, actor := newStewardActor(t, fixture, "steward-db-error")
		ctx := withActor(context.Background(), actor)
		tx, err := fixture.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		err = fixture.runtime.AuthorizeStewardFinal(ctx, tx, userID)
		if err == nil || errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("database error=%v", err)
		}
	})
}
