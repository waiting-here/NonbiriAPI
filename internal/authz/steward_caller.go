package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type stewardCallerContextKey struct{}

// StewardCaller is the non-secret identity established by CallerKey ingress.
// Generation must be checked again in every business transaction.
type StewardCaller struct {
	UserID     int64
	Generation int64
}

// WithStewardCaller is only for the dedicated automation request boundary,
// after CallerKey verification and admission to the shared lifecycle gate.
func WithStewardCaller(ctx context.Context, identity StewardCaller) context.Context {
	return context.WithValue(ctx, stewardCallerContextKey{}, identity)
}

func StewardCallerFromContext(ctx context.Context) (StewardCaller, bool) {
	if ctx == nil {
		return StewardCaller{}, false
	}
	identity, ok := ctx.Value(stewardCallerContextKey{}).(StewardCaller)
	return identity, ok && identity.UserID > 0 && identity.Generation >= 0
}

// AuthorizeStewardCaller grants only live steward authority. Browser sessions
// and administrator authority remain in the separate Authorize path.
func (a *Authorizer) AuthorizeStewardCaller(ctx context.Context, tx *sql.Tx, userID int64) (Principal, error) {
	identity, ok := StewardCallerFromContext(ctx)
	if a == nil || tx == nil || !ok || identity.UserID != userID {
		return Principal{}, ErrUnauthorized
	}
	now := a.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return Principal{}, fmt.Errorf("authorize steward caller: invalid decision time")
	}
	var discordID sql.NullString
	var isAdmin, isBanned, autoLevel int
	var bannedUntil, manualLevel sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT u.discord_id,u.is_admin,u.is_banned,u.banned_until,u.level,u.auto_level
FROM users u JOIN caller_keys c ON c.user_id=u.id
WHERE u.id=? AND c.generation=? AND c.key_hash IS NOT NULL`, userID, identity.Generation).
		Scan(&discordID, &isAdmin, &isBanned, &bannedUntil, &manualLevel, &autoLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authorize steward caller: read live authority: %w", err)
	}
	if isAdmin != 0 || isBanned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) ||
		!manualLevel.Valid || manualLevel.Int64 != 6 || autoLevel < 1 || autoLevel > 4 {
		return Principal{}, ErrForbidden
	}
	return Principal{UserID: userID, DiscordID: discordID.String, Role: RoleSteward, EffectiveLevel: 6}, nil
}
