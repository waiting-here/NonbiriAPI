package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type personalCallerContextKey struct{}

// PersonalCaller grants only the dedicated owner automation purpose after
// CallerKey ingress. It cannot authorize steward management or browser routes.
type PersonalCaller struct{ UserID, Generation int64 }

func WithPersonalCaller(ctx context.Context, identity PersonalCaller) context.Context {
	return context.WithValue(ctx, personalCallerContextKey{}, identity)
}

func PersonalCallerFromContext(ctx context.Context) (PersonalCaller, bool) {
	if ctx == nil {
		return PersonalCaller{}, false
	}
	identity, ok := ctx.Value(personalCallerContextKey{}).(PersonalCaller)
	return identity, ok && identity.UserID > 0 && identity.Generation >= 0
}

// AuthorizePersonalCaller rechecks the live credential generation and account
// in the transaction used for a read, a mutation or receipt replay.
func (a *Authorizer) AuthorizePersonalCaller(ctx context.Context, tx *sql.Tx, userID int64) (Principal, error) {
	identity, ok := PersonalCallerFromContext(ctx)
	if a == nil || tx == nil || !ok || identity.UserID != userID {
		return Principal{}, ErrUnauthorized
	}
	now := a.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return Principal{}, fmt.Errorf("authorize personal caller: invalid decision time")
	}
	var isAdmin, isBanned, autoLevel int
	var bannedUntil, manualLevel sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT u.is_admin,u.is_banned,u.banned_until,u.level,u.auto_level
 FROM users u JOIN caller_keys c ON c.user_id=u.id
 WHERE u.id=? AND c.generation=? AND c.key_hash IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers m WHERE m.user_id=u.id)`, userID, identity.Generation).Scan(&isAdmin, &isBanned, &bannedUntil, &manualLevel, &autoLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authorize personal caller: read live authority: %w", err)
	}
	if isAdmin != 0 || isBanned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) || autoLevel < 1 || autoLevel > 4 {
		return Principal{}, ErrForbidden
	}
	level := autoLevel
	if manualLevel.Valid {
		level = int(manualLevel.Int64)
	}
	if level < 1 || level > 6 {
		return Principal{}, ErrForbidden
	}
	return Principal{UserID: userID, Role: RoleUser, EffectiveLevel: level}, nil
}
