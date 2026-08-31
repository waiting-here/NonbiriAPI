package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// UserSessionBindingState is the closed authority result for an irreversible
// user-session binding. Callers must fail closed on UserSessionBindingUncertain.
type UserSessionBindingState string

const (
	UserSessionBindingActive    UserSessionBindingState = "active"
	UserSessionBindingRevoked   UserSessionBindingState = "revoked"
	UserSessionBindingBanned    UserSessionBindingState = "banned"
	UserSessionBindingDeleted   UserSessionBindingState = "deleted"
	UserSessionBindingUncertain UserSessionBindingState = "uncertain"
)

var (
	ErrInvalidUserSessionBinding          = errors.New("user session binding is invalid")
	ErrInvalidUserSessionObserver         = errors.New("user session invalidation observer is invalid")
	ErrUserSessionInvalidationObserverSet = errors.New("user session invalidation observer is already attached")
)

// UserSessionInvalidationObserver receives committed user-session
// invalidations. The callback never receives raw session material.
type UserSessionInvalidationObserver interface {
	UserSessionInvalidated(userID int64)
}

// AttachUserSessionInvalidationObserver installs the one process-local
// observer before route registration is frozen.
func (r *Runtime) AttachUserSessionInvalidationObserver(observer UserSessionInvalidationObserver) error {
	if r == nil || observer == nil {
		return ErrInvalidUserSessionObserver
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.frozen {
		return ErrFrozen
	}
	if r.userSessionInvalidationObserver != nil {
		return ErrUserSessionInvalidationObserverSet
	}
	r.userSessionInvalidationObserver = observer
	return nil
}

func (r *Runtime) notifyUserSessionInvalidated(userID int64) {
	if r == nil || userID <= 0 {
		return
	}
	r.mu.Lock()
	observer := r.userSessionInvalidationObserver
	r.mu.Unlock()
	if observer != nil {
		observer.UserSessionInvalidated(userID)
	}
}

// VerifyUserSessionBinding re-reads the current non-administrator browser
// session and account authority using only irreversible lookup material.
func (r *Runtime) VerifyUserSessionBinding(ctx context.Context, userID int64, irreversibleBinding string) (UserSessionBindingState, error) {
	if r == nil || ctx == nil || userID <= 0 || !canonicalSessionBinding(irreversibleBinding) {
		return UserSessionBindingUncertain, ErrInvalidUserSessionBinding
	}
	if r.isClosed() {
		return UserSessionBindingUncertain, ErrClosed
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return UserSessionBindingUncertain, fmt.Errorf("verify user session binding: invalid decision time")
	}

	var (
		isAdmin      int
		isBanned     int
		bannedUntil  sql.NullInt64
		liveSessions int64
		matches      int64
	)
	err := r.db.QueryRowContext(ctx, `
SELECT u.is_admin,u.is_banned,u.banned_until,
       (SELECT COUNT(*) FROM sessions s
         WHERE s.user_id=u.id AND s.expires_at>? AND s.absolute_expires_at>?),
       (SELECT COUNT(*) FROM sessions s
         WHERE s.user_id=u.id AND s.token_hash=?
           AND s.expires_at>? AND s.absolute_expires_at>?)
FROM users u
WHERE u.id=?`, now, now, irreversibleBinding, now, now, userID).Scan(
		&isAdmin, &isBanned, &bannedUntil, &liveSessions, &matches,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UserSessionBindingDeleted, nil
	}
	if err != nil {
		return UserSessionBindingUncertain, fmt.Errorf("verify user session binding: read authority: %w", err)
	}
	if (isAdmin != 0 && isAdmin != 1) || (isBanned != 0 && isBanned != 1) || liveSessions < 0 || matches < 0 || matches > liveSessions || matches > 1 {
		return UserSessionBindingUncertain, fmt.Errorf("verify user session binding: invalid persisted authority")
	}
	if isBanned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) {
		return UserSessionBindingBanned, nil
	}
	if isAdmin == 1 {
		return UserSessionBindingRevoked, nil
	}
	if liveSessions > 1 {
		return UserSessionBindingUncertain, fmt.Errorf("verify user session binding: multiple live user sessions")
	}
	if liveSessions == 1 && matches == 1 {
		return UserSessionBindingActive, nil
	}
	return UserSessionBindingRevoked, nil
}

func canonicalSessionBinding(value string) bool {
	if len(value) != sha256HexBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return true
}

// AuthorizeStewardFinal re-reads the exact user-session actor and live L5 role
// through the caller's final read transaction.
func (r *Runtime) AuthorizeStewardFinal(ctx context.Context, tx *sql.Tx, userID int64) error {
	if r == nil {
		return authz.ErrUnauthorized
	}
	actor, ok := ActorFromContext(ctx)
	if !ok || actor.UserID != userID {
		return authz.ErrUnauthorized
	}
	_, err := r.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleSteward})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authz.ErrUnauthorized), errors.Is(err, authz.ErrForbidden):
		return err
	default:
		return fmt.Errorf("authorize steward final: %w", err)
	}
}
