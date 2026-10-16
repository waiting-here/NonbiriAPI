package auth

import (
	"context"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/totp"
)

func mfaRequest(t *testing.T, f *runtimeFixture, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, f.runtime.UserHandler(), host.StationUser, method, "https://user.example"+path, body, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json"})
}
func setupSeed(t *testing.T, f *runtimeFixture, actor authz.Actor) []byte {
	t.Helper()
	setup, err := f.runtime.setupMFA(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if setup.ExpiresAt != f.clock.Now().Unix()+600 || !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/") {
		t.Fatal("setup shape")
	}
	return seed
}
func enrollMFA(t *testing.T, f *runtimeFixture, actor authz.Actor) []byte {
	t.Helper()
	seed := setupSeed(t, f, actor)
	state, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/confirm", totp.Code(seed, f.clock.Now().Unix()/30))
	if err != nil || !state.Enrolled || !state.Verified || state.VerifiedUntil == nil || *state.VerifiedUntil != f.clock.Now().Unix()+mfaLifetime {
		t.Fatal("confirm", state, err)
	}
	return seed
}

type mfaChangeObserver struct{ revoked, changed []int64 }

func (o *mfaChangeObserver) UserSessionInvalidated(id int64) { o.revoked = append(o.revoked, id) }
func (o *mfaChangeObserver) UserMFAChanged(id int64)         { o.changed = append(o.changed, id) }

func TestMFAChangesNotifyOnlyBrowserConnections(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	observer := &mfaChangeObserver{}
	if err := f.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
		t.Fatal(err)
	}
	cookie := loginUser(t, f, "mfa-observer", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	if _, err := f.store.DB().Exec(`DELETE FROM account_mfa_proofs WHERE user_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.InvalidateUnverifiedMFASessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(30 * time.Second)
	if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/disable", totp.Code(seed, f.clock.Now().Unix()/30)); err != nil {
		t.Fatal(err)
	}
	if len(observer.revoked) != 0 || len(observer.changed) != 3 {
		t.Fatalf("first-factor revocations=%v MFA changes=%v", observer.revoked, observer.changed)
	}
	for _, changed := range observer.changed {
		if changed != id {
			t.Fatalf("changed another account: %v", observer.changed)
		}
	}
}
func TestAccountMFADefaultsAndLiveLevelPolicies(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-policy", "")
	id, _ := userSessionIdentity(t, f, cookie)
	for level := 1; level <= 6; level++ {
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, id); err != nil {
			t.Fatal(err)
		}
		if got := mfaRequest(t, f, cookie, "GET", "/api/me", ""); got.Code != 200 {
			t.Fatalf("default L%d: %d %s", level, got.Code, got.Body)
		}
		key := fmt.Sprintf("mfa_required_level_%d", level)
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/session", "/api/me", "/api/me/usage", "/api/auth/elevate"} {
			method := "GET"
			if path == "/api/auth/elevate" {
				method = "POST"
			}
			got := mfaRequest(t, f, cookie, method, path, "")
			if got.Code != 403 || !strings.Contains(got.Body.String(), `"mfa_required"`) || strings.Contains(got.Body.String(), `"balance"`) {
				t.Fatalf("challenge %s: %d %s", path, got.Code, got.Body)
			}
		}
		got := mfaRequest(t, f, cookie, "GET", "/api/auth/mfa", "")
		var state authz.MFAState
		if err := json.Unmarshal(got.Body.Bytes(), &state); err != nil || got.Code != 200 || !state.Required || state.Enrolled || state.Verified || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("status", got.Code, err, state)
		}
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAccountMFAVoluntaryProofLifetimeReplayAndDisable(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-voluntary", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	defer clear(seed)
	initial := f.clock.Now().Unix()
	if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/verify", totp.Code(seed, initial/30)); !errors.Is(err, errMFAInvalid) {
		t.Fatal("replay", err)
	}
	// Ordinary renewal never extends the fixed proof deadline.
	f.clock.Add(6 * 24 * time.Hour)
	if got := mfaRequest(t, f, cookie, "GET", "/api/me", ""); got.Code != 200 {
		t.Fatal("renew", got.Code, got.Body)
	}
	f.clock.Add(24 * time.Hour)
	if got := mfaRequest(t, f, cookie, "GET", "/api/me", ""); got.Code != 403 || !strings.Contains(got.Body.String(), "mfa_required") {
		t.Fatal("boundary", got.Code, got.Body)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := mfaRequest(t, f, cookie, "GET", "/api/me", ""); got.Code != 403 {
		t.Fatal("downgrade disabled voluntary MFA")
	}
	state, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/verify", totp.Code(seed, f.clock.Now().Unix()/30))
	if err != nil || !state.Verified {
		t.Fatal("verify", err)
	}
	f.clock.Add(30 * time.Second)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='mfa_required_level_5'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/disable", totp.Code(seed, f.clock.Now().Unix()/30)); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("required disable", err)
	}
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='mfa_required_level_5'`); err != nil {
		t.Fatal(err)
	}
	state, err = f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/disable", totp.Code(seed, f.clock.Now().Unix()/30))
	if err != nil || state.Enrolled || state.Verified || state.VerifiedUntil != nil {
		t.Fatal("disable", state, err)
	}
	if got := mfaRequest(t, f, cookie, "GET", "/api/me", ""); got.Code != 200 {
		t.Fatal("disabled access", got.Code, got.Body)
	}
}

func TestAccountMFASetupExpiryReplacementAndPersistentBudget(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-setup", "")
	id, actor := userSessionIdentity(t, f, cookie)
	setup, err := f.runtime.setupMFA(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.runtime.setupMFA(t.Context(), actor)
	if err != nil || again.Secret != setup.Secret || again.ExpiresAt != setup.ExpiresAt {
		t.Fatal("same-session setup replaced")
	}
	for range 4 {
		if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/confirm", "xxxxxx"); !errors.Is(err, errMFAInvalid) {
			t.Fatal("guess", err)
		}
	}
	replacement := loginUser(t, f, "mfa-replacement", "")
	_, nextActor := userSessionIdentity(t, f, replacement)
	if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/confirm", "xxxxxx"); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("old session", err)
	}
	seed := setupSeed(t, f, nextActor)
	clear(seed)
	// Reconstructing the runtime leaves the persisted account budget intact.
	second, err := NewRuntime(RuntimeConfig{Store: f.store, Provider: f.provider, DiscordClientID: "client", UserSiteBaseURL: "https://user.example", AdminUsername: "operator", AdminPassword: "correct horse battery staple", CredentialKeyDeriver: f.vault, Authorizer: authz.New(authz.Options{Now: f.clock.Now}), Maintenance: f.gate, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, err = second.changeMFA(t.Context(), nextActor, "/api/auth/mfa/confirm", "xxxxxx")
	var rate mfaRateError
	if !errors.As(err, &rate) || rate.seconds != 900 {
		t.Fatal("fifth failure", err)
	}
	var failures int
	if err = f.store.DB().QueryRow(`SELECT failures FROM account_mfa_attempts WHERE user_id=?`, id).Scan(&failures); err != nil || failures != 5 {
		t.Fatal("budget", failures, err)
	}
	f.clock.Add(15 * time.Minute)
	if _, err = f.runtime.changeMFA(t.Context(), nextActor, "/api/auth/mfa/confirm", "xxxxxx"); !errors.Is(err, errMFAExpired) {
		t.Fatal("expired setup", err)
	}
	result, err := f.runtime.RetainSessionsAt(t.Context(), f.clock.Now().Unix(), 100, time.Now().Add(time.Minute))
	if err != nil || result.Processed != 1 {
		t.Fatal("pending retention", result, err)
	}
}

func TestAccountMFAOptionalContinuationAndFinalRoles(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	if err := f.runtime.RegisterOptionalUserRoute("GET", "/api/mixed-test", func(w http.ResponseWriter, _ *http.Request, p *OptionalUserPrincipal) { w.WriteHeader(204) }); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.RegisterContinuationUserRoute("GET", "/api/continuation-test", func(w http.ResponseWriter, _ *http.Request, p resources.ContinuationUserPrincipal) {
		w.WriteHeader(204)
	}); err != nil {
		t.Fatal(err)
	}
	cookie := loginUser(t, f, "mfa-roles", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	clear(seed)
	if _, err := f.store.DB().Exec(`DELETE FROM account_mfa_proofs WHERE user_id=?`, id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/mixed-test", "/api/continuation-test"} {
		if got := mfaRequest(t, f, cookie, "GET", path, ""); got.Code != 403 || !strings.Contains(got.Body.String(), "mfa_required") {
			t.Fatal("unguarded route", path, got.Code, got.Body)
		}
	}
	anonymous := request(t, f.runtime.UserHandler(), host.StationUser, "GET", "https://user.example/api/mixed-test", "", nil, nil)
	if anonymous.Code != 204 {
		t.Fatal("public route blocked")
	}
	for _, role := range []struct {
		level int
		role  authz.Role
	}{{1, authz.RoleUser}, {5, authz.RoleTrainee}, {6, authz.RoleSteward}} {
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, role.level, id); err != nil {
			t.Fatal(err)
		}
		tx, err := f.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.runtime.authorizer.Authorize(t.Context(), tx, actor, authz.Requirement{Role: role.role})
		_ = tx.Rollback()
		if !errors.Is(err, authz.ErrMFARequired) {
			t.Fatal("final role bypass", role.role, err)
		}
	}
	if _, err := f.runtime.VerifyUserSessionMFA(t.Context(), id, actor.SessionTokenHash); !errors.Is(err, authz.ErrMFARequired) {
		t.Fatal("stream authority bypass", err)
	}
	if got := mfaRequest(t, f, cookie, "POST", "/api/auth/logout", ""); got.Code != 204 {
		t.Fatal("logout blocked", got.Code, got.Body)
	}
}

func TestAccountMFAGuessAndReplayConcurrency(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	f.store.DB().SetMaxOpenConns(4)
	cookie := loginUser(t, f, "mfa-concurrent", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	defer clear(seed)
	results := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := f.runtime.changeMFA(context.Background(), actor, "/api/auth/mfa/verify", "xxxxxx")
			results <- err
		}()
	}
	group.Wait()
	close(results)
	invalid, limited := 0, 0
	for err := range results {
		var rate mfaRateError
		switch {
		case errors.Is(err, errMFAInvalid):
			invalid++
		case errors.As(err, &rate):
			limited++
		default:
			t.Fatalf("concurrent attempt lost: %v", err)
		}
	}
	if invalid != 4 || limited != 8 {
		t.Fatal("failure budget", invalid, limited)
	}
	var failures int
	if err := f.store.DB().QueryRow(`SELECT failures FROM account_mfa_attempts WHERE user_id=?`, id).Scan(&failures); err != nil || failures != 5 {
		t.Fatal("stored failure budget", failures, err)
	}
	f.clock.Add(15 * time.Minute)
	code := totp.Code(seed, f.clock.Now().Unix()/30)
	results = make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := f.runtime.changeMFA(context.Background(), actor, "/api/auth/mfa/verify", code)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, errMFAInvalid) {
			t.Fatal("concurrent verify", err)
		}
	}
	if successes != 1 {
		t.Fatal("replayed successful code", successes)
	}
}

func TestAccountMFAAdminResetRequiresElevationAndRevokesSessions(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-reset", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	defer clear(seed)
	if _, err := f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/verify", "xxxxxx"); !errors.Is(err, errMFAInvalid) {
		t.Fatal(err)
	}
	var callerBefore []byte
	var generationBefore int64
	if err := f.store.DB().QueryRow(`SELECT key_hash,generation FROM caller_keys WHERE user_id=?`, id).Scan(&callerBefore, &generationBefore); err != nil {
		t.Fatal(err)
	}
	adminRaw, _, err := f.runtime.ensureAdminAndSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var adminID int64
	if err = f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminCookie := &http.Cookie{Name: AdminSessionCookieName, Value: adminRaw}
	target := fmt.Sprintf("https://admin.example/admin/api/users/%d/mfa/reset", id)
	got := request(t, f.runtime.AdminHandler(), host.StationAdmin, "POST", target, "", []*http.Cookie{adminCookie}, nil)
	if got.Code != 403 || !strings.Contains(got.Body.String(), "elevated_required") {
		t.Fatal("reset without elevation", got.Code, got.Body)
	}
	token, _, err := f.runtime.elevation.IssueBound(adminID, elevation.KindAdmin, sessionLookupHash(adminRaw))
	if err != nil {
		t.Fatal(err)
	}
	// A reset may commit after a final read, but the old SQLite snapshot cannot
	// subsequently commit a privileged mutation using that revoked proof.
	f.store.DB().SetMaxOpenConns(2)
	finalTx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.authorizer.Authorize(t.Context(), finalTx, actor, authz.Requirement{Role: authz.RoleUser}); err != nil {
		t.Fatal(err)
	}
	got = request(t, f.runtime.AdminHandler(), host.StationAdmin, "POST", target, "", []*http.Cookie{adminCookie}, map[string]string{"X-Elevated-Token": token})
	if _, err = finalTx.ExecContext(t.Context(), `UPDATE users SET updated_at=updated_at WHERE id=?`, id); err == nil {
		_ = finalTx.Rollback()
		t.Fatal("revoked authorization snapshot acquired a writer")
	}
	_ = finalTx.Rollback()
	if got.Code != 200 || strings.TrimSpace(got.Body.String()) != `{"enrolled":false}` {
		t.Fatal("reset", got.Code, got.Body)
	}
	for _, table := range []string{"sessions", "account_mfa_credentials", "account_mfa_pending", "account_mfa_proofs"} {
		var count int
		if err = f.store.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, id).Scan(&count); err != nil || count != 0 {
			t.Fatal("reset retained", table, count, err)
		}
	}
	var failures int
	if err = f.store.DB().QueryRow(`SELECT failures FROM account_mfa_attempts WHERE user_id=?`, id).Scan(&failures); err != nil || failures != 1 {
		t.Fatal("reset cleared attempt budget", failures, err)
	}
	var callerAfter []byte
	var generationAfter int64
	if err = f.store.DB().QueryRow(`SELECT key_hash,generation FROM caller_keys WHERE user_id=?`, id).Scan(&callerAfter, &generationAfter); err != nil || string(callerBefore) != string(callerAfter) || generationBefore != generationAfter {
		t.Fatal("reset changed CallerKey", err)
	}
	if _, err = f.runtime.changeMFA(t.Context(), actor, "/api/auth/mfa/verify", totp.Code(seed, f.clock.Now().Unix()/30+1)); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("old proof recreated", err)
	}
	newCookie := loginUser(t, f, "mfa-after-reset", "")
	if got = mfaRequest(t, f, newCookie, "GET", "/api/auth/mfa", ""); got.Code != 200 {
		t.Fatal("fresh login", got.Code, got.Body)
	}
}

func TestAccountMFABanAndSessionReplacementRevokeProof(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "mfa-revoke", "")
	id, actor := userSessionIdentity(t, f, cookie)
	seed := enrollMFA(t, f, actor)
	defer clear(seed)
	next := loginUser(t, f, "mfa-new-session", "")
	_, nextActor := userSessionIdentity(t, f, next)
	if _, err := f.runtime.VerifyUserSessionMFA(t.Context(), id, actor.SessionTokenHash); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("replaced stream binding", err)
	}
	if got := mfaRequest(t, f, next, "GET", "/api/me", ""); got.Code != 403 {
		t.Fatal("new session reused proof")
	}
	f.clock.Add(30 * time.Second)
	if _, err := f.runtime.changeMFA(t.Context(), nextActor, "/api/auth/mfa/verify", totp.Code(seed, f.clock.Now().Unix()/30)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.changeMFA(t.Context(), nextActor, "/api/auth/mfa/verify", "xxxxxx"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatal("banned MFA access", err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var until int64
	err := f.store.DB().QueryRow(`SELECT verified_until FROM account_mfa_proofs WHERE user_id=?`, id).Scan(&until)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("ban retained MFA proof", err)
	}
}
