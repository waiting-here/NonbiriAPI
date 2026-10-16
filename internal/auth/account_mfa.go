package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/totp"
)

const mfaLifetime = int64(7 * 24 * 60 * 60)
const mfaSetupLifetime = int64(10 * 60)
const mfaAttemptWindow = int64(15 * 60)

var errMFAConflict = errors.New(httperr.CodeConflict)
var errMFAInvalid = errors.New(httperr.CodeMFAInvalidCode)
var errMFAExpired = errors.New(httperr.CodeMFASetupExpired)

type mfaRateError struct{ seconds int64 }

func (mfaRateError) Error() string { return httperr.CodeRateLimited }

type mfaSetup struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
	ExpiresAt  int64  `json:"expires_at"`
}

func mfaBootstrapRequest(req *http.Request) bool {
	switch req.Method + " " + req.URL.Path {
	case "GET /api/auth/mfa", "POST /api/auth/mfa/setup", "POST /api/auth/mfa/confirm",
		"POST /api/auth/mfa/verify", "POST /api/auth/mfa/disable", "POST /api/auth/logout":
		return true
	}
	return false
}

func (r *Runtime) registerMFARoutes() error {
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodGet, "/api/auth/mfa", r.mfaStatusHTTP},
		{http.MethodPost, "/api/auth/mfa/setup", r.mfaSetupHTTP},
		{http.MethodPost, "/api/auth/mfa/confirm", r.mfaCodeHTTP},
		{http.MethodPost, "/api/auth/mfa/verify", r.mfaCodeHTTP},
		{http.MethodPost, "/api/auth/mfa/disable", r.mfaCodeHTTP},
	} {
		if err := r.registerUserInternal(route.method, route.path, route.handler, false, true); err != nil {
			return err
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/admin/api/users/{id}/mfa"},
		{http.MethodPost, "/admin/api/users/{id}/mfa/reset"},
	} {
		if err := r.registerAdminInternal(route.method, route.path, http.HandlerFunc(r.mfaAdminHTTP), false, false); err != nil {
			return err
		}
	}
	return nil
}

// VerifyUserSessionMFA is the live authority check for long-lived browser
// connections. A zero deadline means that current policy requires no proof.
func (r *Runtime) VerifyUserSessionMFA(ctx context.Context, userID int64, binding string) (int64, error) {
	if r == nil || ctx == nil || userID <= 0 || binding == "" {
		return 0, authz.ErrUnauthorized
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	actor := authz.Actor{Kind: authz.ActorUserSession, UserID: userID, SessionTokenHash: binding}
	if err = tx.QueryRowContext(ctx, `SELECT cred_gen FROM sessions WHERE token_hash=? AND user_id=?`, binding, userID).Scan(&actor.SessionGeneration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, authz.ErrUnauthorized
		}
		return 0, err
	}
	principal, err := r.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleUser})
	return principal.MFAVerifiedUntil, err
}

// InvalidateUnverifiedMFASessions closes private streams after a policy
// change. Cookies remain available to complete the authentication challenge.
func (r *Runtime) InvalidateUnverifiedMFASessions(ctx context.Context) error {
	if r == nil || ctx == nil {
		return authz.ErrUnauthorized
	}
	now := r.now().Unix()
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT s.user_id FROM sessions s
 JOIN users u ON u.id=s.user_id
 LEFT JOIN site_config cfg ON cfg.key='mfa_required_level_'||COALESCE(u.level,u.auto_level)
 LEFT JOIN account_mfa_credentials c ON c.user_id=u.id
 LEFT JOIN account_mfa_proofs p ON p.session_hash=s.token_hash AND p.user_id=u.id AND p.session_generation=s.cred_gen
 WHERE u.is_admin=0 AND s.expires_at>? AND s.absolute_expires_at>?
 AND (c.user_id IS NOT NULL OR cfg.value IS NULL OR cfg.value<>'0')
 AND (c.user_id IS NULL OR p.verified_until IS NULL OR p.verified_until<=?)`, now, now, now)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		r.notifyUserSessionInvalidated(id)
	}
	return nil
}

func (r *Runtime) mfaBegin(ctx context.Context, actor authz.Actor, write bool) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if write {
		// Reserve the SQLite writer before reading replay, budget, or proof state.
		// A deferred-read upgrade race must not discard a failed guess.
		_, err = tx.ExecContext(ctx, `UPDATE users SET updated_at=updated_at WHERE id=?`, actor.UserID)
	}
	if err == nil {
		_, err = r.authorizer.AuthorizeMFAIdentity(ctx, tx, actor)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *Runtime) mfaStatusHTTP(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	actor, _ := ActorFromContext(req.Context())
	tx, err := r.mfaBegin(req.Context(), actor, false)
	if err != nil {
		writeMFAError(w, err)
		return
	}
	defer tx.Rollback()
	state, err := authz.ReadMFAState(req.Context(), tx, actor, r.now().Unix())
	if err != nil {
		writeMFAError(w, err)
		return
	}
	httpmwWriteJSON(w, http.StatusOK, state)
}
func (r *Runtime) mfaSetupHTTP(w http.ResponseWriter, req *http.Request) {
	var input struct{}
	if !requireEmptyQuery(w, req) || !decodeJSONBody(w, req, &input, true) {
		return
	}
	actor, _ := ActorFromContext(req.Context())
	result, err := r.setupMFA(req.Context(), actor)
	if err != nil {
		writeMFAError(w, err)
		return
	}
	httpmwWriteJSON(w, http.StatusOK, result)
}
func (r *Runtime) setupMFA(ctx context.Context, actor authz.Actor) (mfaSetup, error) {
	tx, err := r.mfaBegin(ctx, actor, true)
	if err != nil {
		return mfaSetup{}, err
	}
	defer tx.Rollback()
	now := r.now().Unix()
	state, err := authz.ReadMFAState(ctx, tx, actor, now)
	if err != nil {
		return mfaSetup{}, err
	}
	if state.Enrolled {
		return mfaSetup{}, errMFAConflict
	}
	var encoded string
	var contextID []byte
	var expires int64
	err = tx.QueryRowContext(ctx, `SELECT encrypted_secret,context_id,expires_at FROM account_mfa_pending WHERE user_id=? AND session_hash=? AND expires_at>?`, actor.UserID, actor.SessionTokenHash, now).Scan(&encoded, &contextID, &expires)
	var seed []byte
	if errors.Is(err, sql.ErrNoRows) {
		seed = make([]byte, 20)
		contextID = make([]byte, 16)
		if _, err = rand.Read(seed); err != nil {
			return mfaSetup{}, err
		}
		defer clear(seed)
		if _, err = rand.Read(contextID); err != nil {
			return mfaSetup{}, err
		}
		encoded, err = r.mfaCodec.SealAccountMFA(seed, contextID)
		if err != nil {
			return mfaSetup{}, err
		}
		expires = now + mfaSetupLifetime
		_, err = tx.ExecContext(ctx, `INSERT INTO account_mfa_pending(user_id,session_hash,context_id,encrypted_secret,expires_at) VALUES(?,?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET session_hash=excluded.session_hash,context_id=excluded.context_id,encrypted_secret=excluded.encrypted_secret,expires_at=excluded.expires_at`, actor.UserID, actor.SessionTokenHash, contextID, encoded, expires)
	} else if err == nil {
		seed, err = r.mfaCodec.OpenAccountMFA(encoded, contextID)
		defer clear(seed)
	}
	clear(contextID)
	if err != nil {
		return mfaSetup{}, err
	}
	secretText := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)
	label := "NonbiriAPI:" + strconv.FormatInt(actor.UserID, 10)
	query := url.Values{"secret": {secretText}, "issuer": {"NonbiriAPI"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	result := mfaSetup{Secret: secretText, OTPAuthURI: "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode(), ExpiresAt: expires}
	if err = tx.Commit(); err != nil {
		return mfaSetup{}, err
	}
	return result, nil
}

func (r *Runtime) mfaCodeHTTP(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !requireEmptyQuery(w, req) || !decodeJSONBody(w, req, &input, true) {
		return
	}
	actor, _ := ActorFromContext(req.Context())
	state, err := r.changeMFA(req.Context(), actor, req.URL.Path, input.Code)
	if err != nil {
		writeMFAError(w, err)
		return
	}
	httpmwWriteJSON(w, http.StatusOK, state)
}
func (r *Runtime) changeMFA(ctx context.Context, actor authz.Actor, action, code string) (authz.MFAState, error) {
	tx, err := r.mfaBegin(ctx, actor, true)
	if err != nil {
		return authz.MFAState{}, err
	}
	defer tx.Rollback()
	now := r.now().Unix()
	state, err := authz.ReadMFAState(ctx, tx, actor, now)
	if err != nil {
		return state, err
	}
	confirm := action == "/api/auth/mfa/confirm"
	disable := action == "/api/auth/mfa/disable"
	if disable && state.Required {
		return state, authz.ErrForbidden
	}
	if confirm == state.Enrolled {
		return state, errMFAConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_mfa_attempts(user_id) VALUES(?) ON CONFLICT(user_id) DO NOTHING`, actor.UserID); err != nil {
		return state, err
	}
	var start, blocked int64
	var failures int
	if err = tx.QueryRowContext(ctx, `SELECT window_start,failures,blocked_until FROM account_mfa_attempts WHERE user_id=?`, actor.UserID).Scan(&start, &failures, &blocked); err != nil {
		return state, err
	}
	if blocked > now {
		return state, mfaRateError{blocked - now}
	}
	if now-start >= mfaAttemptWindow || blocked != 0 {
		start = now
		failures = 0
	}
	var encoded string
	var contextID []byte
	var lastStep int64 = -1
	if confirm {
		err = tx.QueryRowContext(ctx, `SELECT encrypted_secret,context_id FROM account_mfa_pending WHERE user_id=? AND session_hash=? AND expires_at>?`, actor.UserID, actor.SessionTokenHash, now).Scan(&encoded, &contextID)
		if errors.Is(err, sql.ErrNoRows) {
			return state, errMFAExpired
		}
	} else {
		err = tx.QueryRowContext(ctx, `SELECT encrypted_secret,context_id,last_step FROM account_mfa_credentials WHERE user_id=?`, actor.UserID).Scan(&encoded, &contextID, &lastStep)
	}
	if err != nil {
		return state, err
	}
	defer clear(contextID)
	seed, err := r.mfaCodec.OpenAccountMFA(encoded, contextID)
	if err != nil {
		return state, err
	}
	defer clear(seed)
	step, valid := totp.Match(seed, code, now, lastStep)
	if !valid {
		failures++
		blocked = 0
		var failure error = errMFAInvalid
		if failures >= 5 {
			blocked = now + mfaAttemptWindow
			failure = mfaRateError{mfaAttemptWindow}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE account_mfa_attempts SET window_start=?,failures=?,blocked_until=? WHERE user_id=?`, start, failures, blocked, actor.UserID); err != nil {
			return state, err
		}
		// Failed guesses commit before their public error is returned.
		if err = tx.Commit(); err != nil {
			return state, err
		}
		return state, failure
	}
	if confirm {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_mfa_credentials(user_id,context_id,encrypted_secret,last_step) VALUES(?,?,?,?)`, actor.UserID, contextID, encoded, step)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM account_mfa_pending WHERE user_id=?`, actor.UserID)
		}
	} else if disable {
		_, err = tx.ExecContext(ctx, `DELETE FROM account_mfa_credentials WHERE user_id=?`, actor.UserID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE account_mfa_credentials SET last_step=? WHERE user_id=?`, step, actor.UserID)
	}
	if err != nil {
		return state, err
	}
	if !disable {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_mfa_proofs(session_hash,user_id,session_generation,verified_until) VALUES(?,?,?,?)
 ON CONFLICT(session_hash) DO UPDATE SET user_id=excluded.user_id,session_generation=excluded.session_generation,verified_until=excluded.verified_until`, actor.SessionTokenHash, actor.UserID, actor.SessionGeneration, now+mfaLifetime)
		if err != nil {
			return state, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_mfa_attempts SET window_start=0,failures=0,blocked_until=0 WHERE user_id=?`, actor.UserID); err != nil {
		return state, err
	}
	state, err = authz.ReadMFAState(ctx, tx, actor, now)
	if err != nil {
		return state, err
	}
	if err = tx.Commit(); err != nil {
		return state, err
	}
	if confirm || disable {
		r.notifyUserSessionInvalidated(actor.UserID)
	}
	return state, nil
}

func (r *Runtime) mfaAdminHTTP(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) {
		return
	}
	target, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || target <= 0 || strconv.FormatInt(target, 10) != req.PathValue("id") {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	reset := req.Method == http.MethodPost
	if !requireEmptyBody(w, req) {
		return
	}
	actor, _ := ActorFromContext(req.Context())
	tx, err := r.db.BeginTx(req.Context(), nil)
	if err != nil {
		writeMFAError(w, err)
		return
	}
	defer tx.Rollback()
	if reset {
		_, err = tx.ExecContext(req.Context(), `UPDATE users SET updated_at=updated_at WHERE id=?`, target)
	}
	if err == nil {
		_, err = r.authorizer.Authorize(req.Context(), tx, actor, authz.Requirement{Role: authz.RoleAdministrator, FreshElevation: reset})
	}
	if err != nil {
		writeMFAError(w, err)
		return
	}
	var isAdmin bool
	if err = tx.QueryRowContext(req.Context(), `SELECT is_admin FROM users WHERE id=?`, target).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = authz.ErrNotFound
		}
		writeMFAError(w, err)
		return
	}
	if isAdmin {
		writeMFAError(w, authz.ErrForbidden)
		return
	}
	enrolled := false
	if reset {
		for _, query := range []string{`DELETE FROM account_mfa_credentials WHERE user_id=?`, `DELETE FROM account_mfa_pending WHERE user_id=?`, `DELETE FROM sessions WHERE user_id=?`} {
			if _, err = tx.ExecContext(req.Context(), query, target); err != nil {
				writeMFAError(w, err)
				return
			}
		}
	} else if err = tx.QueryRowContext(req.Context(), `SELECT EXISTS(SELECT 1 FROM account_mfa_credentials WHERE user_id=?)`, target).Scan(&enrolled); err != nil {
		writeMFAError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeMFAError(w, err)
		return
	}
	if reset {
		r.notifyUserSessionInvalidated(target)
	}
	httpmwWriteJSON(w, http.StatusOK, struct {
		Enrolled bool `json:"enrolled"`
	}{enrolled})
}
func writeMFAError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	var rate mfaRateError
	switch {
	case errors.As(err, &rate):
		w.Header().Set("Retry-After", strconv.FormatInt(rate.seconds, 10))
		writeStableError(w, httperr.CodeRateLimited, "too many requests")
	case errors.Is(err, errMFAInvalid):
		writeStableError(w, httperr.CodeMFAInvalidCode, "The verification code is invalid or has already been used.")
	case errors.Is(err, errMFAExpired):
		writeStableError(w, httperr.CodeMFASetupExpired, "Authenticator setup expired. Start setup again.")
	case errors.Is(err, errMFAConflict):
		writeStableError(w, httperr.CodeConflict, "MFA state changed. Refresh and try again.")
	case errors.Is(err, authz.ErrMFARequired):
		writeStableError(w, httperr.CodeMFARequired, "Verify your authenticator to use your account.")
	default:
		if code := authz.StableCode(err); code != "" {
			writeStableError(w, code, "authentication required")
		} else {
			writeStableError(w, httperr.CodeInternal, "authentication failed")
		}
	}
}
