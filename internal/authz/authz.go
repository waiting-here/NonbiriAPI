// Package authz provides final-transaction authorization for Generation 2
// control-plane mutations. Entry-point checks are useful early rejections,
// but they are never authorization tickets: every caller must invoke
// Authorize with the outer business transaction immediately before its final
// writes.
package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const maxUnixSecond = int64(253402300799)

// Stable authorization decisions. Callers may map these directly to the
// corresponding platform error codes without exposing database details.
var (
	ErrUnauthorized       = errors.New(httperr.CodeUnauthorized)
	ErrForbidden          = errors.New(httperr.CodeForbidden)
	ErrElevatedRequired   = errors.New(httperr.CodeElevationRequired)
	ErrNotFound           = errors.New(httperr.CodeNotFound)
	ErrInvalidRequirement = errors.New("invalid final-transaction authorization requirement")
)

// ActorKind keeps user-station and administrator-station sessions in separate
// authority domains. A worker is intentionally not an ActorKind: continuation
// workers use persisted operation authority instead of impersonating a human
// principal.
type ActorKind uint8

const (
	ActorUserSession ActorKind = iota + 1
	ActorAdminSession
)

func (kind ActorKind) valid() bool {
	return kind == ActorUserSession || kind == ActorAdminSession
}

// Actor is the identity established by the request boundary. SessionTokenHash
// is the persisted lookup material, never a raw cookie. SessionGeneration is
// the exact cred_gen observed by the entry check; replacement between the
// entry check and final transaction therefore fails closed. ElevationToken is
// consumed only when the frozen Requirement asks for fresh elevation.
type Actor struct {
	Kind              ActorKind
	UserID            int64
	SessionTokenHash  string
	SessionGeneration string
	ElevationToken    string
}

// Role is the exact live role required by a mutation.
type Role uint8

const (
	RoleUser Role = iota + 1
	RoleSteward
	RoleAdministrator
)

func (role Role) valid() bool {
	return role == RoleUser || role == RoleSteward || role == RoleAdministrator
}

// OwnershipResult deliberately makes a foreign resource indistinguishable
// from a missing one at the public authorization boundary.
type OwnershipResult uint8

const (
	OwnershipOwned OwnershipResult = iota + 1
	OwnershipMissing
	OwnershipForeign
)

// OwnerPredicate re-reads an owner-scoped resource through the caller's outer
// transaction. Domain repositories normally implement this with a narrow
// query that also binds their frozen resource identity.
type OwnerPredicate interface {
	CheckOwner(context.Context, *sql.Tx, int64) (OwnershipResult, error)
}

// OwnerPredicateFunc adapts a function to OwnerPredicate.
type OwnerPredicateFunc func(context.Context, *sql.Tx, int64) (OwnershipResult, error)

func (fn OwnerPredicateFunc) CheckOwner(ctx context.Context, tx *sql.Tx, userID int64) (OwnershipResult, error) {
	return fn(ctx, tx, userID)
}

// Requirement is frozen by the route before Authorize is called. Resource
// revision/CAS checks remain domain-owned and run after this authorization
// decision in the same transaction.
type Requirement struct {
	Role           Role
	Owner          OwnerPredicate
	FreshElevation bool
}

// ElevationConsumer is satisfied by elevation.Manager. Consumption happens
// while the outer transaction is open and after session/role/owner checks, so
// expiry and session binding are checked at the final authorization point.
// A later transaction rollback may conservatively burn the one-use token.
type ElevationConsumer interface {
	ConsumeBound(token string, userID int64, kind elevation.Kind, binding string) error
}

// Options configures the final authorizer. Production uses the real clock;
// tests may supply a deterministic one.
type Options struct {
	Now       func() time.Time
	Elevation ElevationConsumer
}

// Authorizer has no authorization cache. Every decision re-reads the active
// session and account through the supplied transaction.
type Authorizer struct {
	now       func() time.Time
	elevation ElevationConsumer
}

func New(options Options) *Authorizer {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Authorizer{now: now, elevation: options.Elevation}
}

// Principal is the live identity snapshot authorized by the transaction. It
// is safe for same-transaction audit rows; it is not reusable as a later
// authorization ticket.
type Principal struct {
	UserID         int64
	DiscordID      string
	Role           Role
	EffectiveLevel int
	SessionBinding string
}

// Authorize performs the final transaction check. The caller must invoke it
// after opening the outer business transaction and immediately before domain
// revision/CAS and final writes. Authorization is deliberately not cached.
func (a *Authorizer) Authorize(ctx context.Context, tx *sql.Tx, actor Actor, requirement Requirement) (Principal, error) {
	if a == nil || tx == nil || ctx == nil || !actor.Kind.valid() || actor.UserID <= 0 || actor.SessionTokenHash == "" {
		return Principal{}, ErrUnauthorized
	}
	if !requirement.Role.valid() {
		return Principal{}, ErrInvalidRequirement
	}
	now := a.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return Principal{}, fmt.Errorf("authorize final transaction: invalid decision time")
	}

	var (
		storedGeneration string
		discordID        sql.NullString
		isAdmin          int
		isBanned         int
		bannedUntil      sql.NullInt64
		manualLevel      sql.NullInt64
		autoLevel        int64
	)
	err := tx.QueryRowContext(ctx, `
SELECT s.cred_gen,u.discord_id,u.is_admin,u.is_banned,u.banned_until,u.level,u.auto_level
FROM sessions s
JOIN users u ON u.id=s.user_id
WHERE s.token_hash=? AND s.user_id=?
  AND s.expires_at>? AND s.absolute_expires_at>?`,
		actor.SessionTokenHash, actor.UserID, now, now,
	).Scan(&storedGeneration, &discordID, &isAdmin, &isBanned, &bannedUntil, &manualLevel, &autoLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authorize final transaction: read live principal: %w", err)
	}
	if storedGeneration != actor.SessionGeneration {
		return Principal{}, ErrUnauthorized
	}
	if isBanned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) {
		return Principal{}, ErrForbidden
	}

	effectiveLevel := autoLevel
	if manualLevel.Valid {
		effectiveLevel = manualLevel.Int64
	}
	if effectiveLevel < 1 || effectiveLevel > 5 || autoLevel < 1 || autoLevel > 4 {
		return Principal{}, fmt.Errorf("authorize final transaction: invalid live level state")
	}

	switch requirement.Role {
	case RoleUser:
		if actor.Kind != ActorUserSession || isAdmin != 0 {
			return Principal{}, ErrForbidden
		}
	case RoleSteward:
		if actor.Kind != ActorUserSession || isAdmin != 0 || effectiveLevel != 5 {
			return Principal{}, ErrForbidden
		}
	case RoleAdministrator:
		if actor.Kind != ActorAdminSession || isAdmin != 1 {
			return Principal{}, ErrForbidden
		}
	}

	if requirement.Owner != nil {
		result, ownerErr := requirement.Owner.CheckOwner(ctx, tx, actor.UserID)
		if ownerErr != nil {
			return Principal{}, fmt.Errorf("authorize final transaction: owner predicate: %w", ownerErr)
		}
		switch result {
		case OwnershipOwned:
		case OwnershipMissing, OwnershipForeign:
			return Principal{}, ErrNotFound
		default:
			return Principal{}, fmt.Errorf("authorize final transaction: invalid owner result")
		}
	}

	if requirement.FreshElevation {
		if a.elevation == nil || actor.ElevationToken == "" {
			return Principal{}, ErrElevatedRequired
		}
		kind := elevation.KindUser
		if actor.Kind == ActorAdminSession {
			kind = elevation.KindAdmin
		}
		if err := a.elevation.ConsumeBound(actor.ElevationToken, actor.UserID, kind, actor.SessionTokenHash); err != nil {
			return Principal{}, ErrElevatedRequired
		}
	}

	return Principal{
		UserID:         actor.UserID,
		DiscordID:      discordID.String,
		Role:           requirement.Role,
		EffectiveLevel: int(effectiveLevel),
		SessionBinding: actor.SessionTokenHash,
	}, nil
}

// StableCode returns the frozen platform code for an authorization decision.
// Non-authorization failures return an empty string and must be handled as
// internal errors by the caller.
func StableCode(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return httperr.CodeUnauthorized
	case errors.Is(err, ErrForbidden):
		return httperr.CodeForbidden
	case errors.Is(err, ErrElevatedRequired):
		return httperr.CodeElevationRequired
	case errors.Is(err, ErrNotFound):
		return httperr.CodeNotFound
	default:
		return ""
	}
}
