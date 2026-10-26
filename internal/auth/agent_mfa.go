package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"sync"
)

func managedMFATx(ctx context.Context, tx *sql.Tx, target int64, reset bool) (bool, error) {
	var err error
	var isAdmin bool
	if err = tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, target).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = authz.ErrNotFound
		}
		return false, err
	}
	if isAdmin {
		return false, authz.ErrForbidden
	}
	enrolled := false
	if reset {
		for _, query := range []string{`DELETE FROM account_mfa_credentials WHERE user_id=?`, `DELETE FROM account_mfa_pending WHERE user_id=?`, `DELETE FROM sessions WHERE user_id=?`} {
			if _, err = tx.ExecContext(ctx, query, target); err != nil {
				return false, err
			}
		}
	} else if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_mfa_credentials WHERE user_id=?)`, target).Scan(&enrolled); err != nil {
		return false, err
	}
	return enrolled, nil
}

// AgentMFAState exposes only enrollment and an opaque optimistic version. The
// version is derived from random credential context identifiers, never secrets.
type AgentMFAState struct {
	Enrolled bool   `json:"enrolled"`
	Version  string `json:"mfa_version"`
}

func agentMFAVersion(ctx context.Context, tx *sql.Tx, target int64) (string, error) {
	var credential, pending []byte
	err := tx.QueryRowContext(ctx, "SELECT (SELECT context_id FROM account_mfa_credentials WHERE user_id=?),(SELECT context_id FROM account_mfa_pending WHERE user_id=?)", target, target).Scan(&credential, &pending)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("account-mfa-state/v1"))
	h.Write([]byte{byte(len(credential))})
	h.Write(credential)
	h.Write([]byte{byte(len(pending))})
	h.Write(pending)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (r *Runtime) AgentReadMFA(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, target int64, authorize agentplan.Authorize) (AgentMFAState, error) {
	if r == nil || actor.Automatic || actor.Role != "admin" || authorize == nil {
		return AgentMFAState{}, authz.ErrForbidden
	}
	if err := authorize(ctx, tx, actor, "users.mfa_read"); err != nil {
		return AgentMFAState{}, err
	}
	real, ok := ActorFromContext(ctx)
	if !ok || real.UserID != actor.ID {
		return AgentMFAState{}, authz.ErrUnauthorized
	}
	if _, err := r.authorizer.Authorize(ctx, tx, real, authz.Requirement{Role: authz.RoleAdministrator}); err != nil {
		return AgentMFAState{}, err
	}
	enrolled, err := managedMFATx(ctx, tx, target, false)
	if err != nil {
		return AgentMFAState{}, err
	}
	version, err := agentMFAVersion(ctx, tx, target)
	return AgentMFAState{Enrolled: enrolled, Version: version}, err
}
func (r *Runtime) AgentResetMFA(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, target int64, revision db.U128, version string, authorize agentplan.Authorize) (func(bool), error) {
	state, err := r.AgentReadMFA(ctx, tx, actor, target, authorize)
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, tx, actor, "users.mfa_reset"); err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM users WHERE id=?", target).Scan(&raw); err != nil {
		return nil, err
	}
	current, err := db.DecodeU128(raw)
	if err != nil {
		return nil, err
	}
	if current != revision || state.Version != version {
		return nil, errMFAConflict
	}
	if err = authorize(ctx, tx, actor, "fresh_elevation"); err != nil {
		return nil, err
	}
	if _, err = managedMFATx(ctx, tx, target, true); err != nil {
		return nil, err
	}
	var once sync.Once
	return func(committed bool) {
		once.Do(func() {
			if committed {
				r.notifyUserSessionInvalidated(target)
			}
		})
	}, nil
}
