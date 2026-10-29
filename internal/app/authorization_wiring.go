package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/timeapi"
)

// roleFinalTxAuthorizer adapts the request actor established by auth.Runtime
// to the exact live role checks owned by authz.Authorizer. It carries no
// cached authorization result: every domain call supplies its own final
// transaction and revalidates the session, account and role in that tx.
type roleFinalTxAuthorizer struct {
	authorizer *authz.Authorizer
}

func (authorizer *roleFinalTxAuthorizer) SessionActor(ctx context.Context) (authz.Actor, bool) {
	return auth.ActorFromContext(ctx)
}

var _ donation.RoleFinalTxAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ activities.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ announcements.AdminFinalTxAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminapi.SiteConfigFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminalerts.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminusers.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ logapi.StewardAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ logapi.ReadAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ resources.AdminFinalTxAuthorizer = (*roleFinalTxAuthorizer)(nil)

// timeContextResolver is the narrow bridge from the station-aware time API
// to the authoritative site offset. Each request is authorized and reads the
// offset in one transaction; the resolver never returns the broader site
// configuration snapshot.
type timeContextResolver struct {
	store     *db.Store
	authority *roleFinalTxAuthorizer
}

var _ timeapi.ContextResolver = (*timeContextResolver)(nil)

func (resolver *timeContextResolver) AdminTimeContext(ctx context.Context, userID int64) (timeapi.TimeContext, error) {
	return resolver.read(ctx, userID, func(ctx context.Context, tx *sql.Tx, userID int64) error {
		return resolver.authority.AuthorizeAdmin(ctx, tx, userID)
	})
}

func (resolver *timeContextResolver) StewardTimeContext(ctx context.Context, userID int64) (timeapi.TimeContext, error) {
	return resolver.read(ctx, userID, func(ctx context.Context, tx *sql.Tx, userID int64) error {
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level) FROM users WHERE id=?`, userID).Scan(&level); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return authz.ErrUnauthorized
			}
			return err
		}
		if level == 5 {
			return resolver.authority.AuthorizeTraineeMutation(ctx, tx, userID)
		}
		if level == 6 {
			return resolver.authority.AuthorizeStewardRead(ctx, tx, userID)
		}
		return authz.ErrForbidden
	})
}

func (resolver *timeContextResolver) read(ctx context.Context, userID int64, authorize func(context.Context, *sql.Tx, int64) error) (timeapi.TimeContext, error) {
	if resolver == nil || resolver.store == nil || resolver.authority == nil || authorize == nil || userID <= 0 {
		return timeapi.TimeContext{}, authz.ErrUnauthorized
	}
	tx, err := resolver.store.ReadDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return timeapi.TimeContext{}, err
	}
	defer tx.Rollback()
	if err := authorize(ctx, tx, userID); err != nil {
		return timeapi.TimeContext{}, err
	}
	offset, err := db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		if errors.Is(err, db.ErrTimezoneUnavailable) {
			return timeapi.TimeContext{Configured: false}, nil
		}
		return timeapi.TimeContext{}, err
	}
	return timeapi.TimeContext{Configured: true, OffsetMinutes: offset}, nil
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeLogRead(ctx context.Context, tx *sql.Tx, role string, userID int64) error {
	switch role {
	case "admin":
		actor, ok := auth.ActorFromContext(ctx)
		if !ok || actor.Kind != authz.ActorAdminSession {
			return authz.ErrUnauthorized
		}
		return authorizer.AuthorizeAdmin(ctx, tx, actor.UserID)
	case "steward":
		_, err := authorizer.AuthorizeStewardLogRead(ctx, tx, userID)
		return err
	case "user":
		if _, ok := authz.PersonalCallerFromContext(ctx); ok {
			_, err := authorizer.authorizer.AuthorizePersonalCaller(ctx, tx, userID)
			return err
		}
		if _, ok := authz.StewardCallerFromContext(ctx); ok {
			_, err := authorizer.authorizer.AuthorizeStewardCaller(ctx, tx, userID)
			return err
		}
		return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleUser)
	default:
		return authz.ErrForbidden
	}
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdminFinalTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleSteward)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardRead(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleSteward)
}

// Log access has a separate permission and projection from management actions.
// The returned flag is valid only for data read in this same transaction.
func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardLogRead(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	if _, ok := authz.StewardCallerFromContext(ctx); ok {
		return false, authz.ErrForbidden
	}
	if authz.StewardPermissionFromContext(ctx) == "" {
		ctx = authz.WithStewardPermission(ctx, "logs.read")
	}
	_, err := authorizer.authorizeStewardSession(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	policy, err := authz.ReadStewardPermissions(ctx, tx, userID)
	return !policy.Permissions["logs.unredacted"], err
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeUserManagement(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	if _, ok := authz.StewardCallerFromContext(ctx); ok {
		principal, err := authorizer.authorizer.AuthorizeStewardCaller(ctx, tx, userID)
		return principal.EffectiveLevel == 5, err
	}
	return authorizer.authorizeStewardSession(ctx, tx, userID)
}

func (authorizer *roleFinalTxAuthorizer) authorizeStewardSession(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	if authorizer == nil || authorizer.authorizer == nil || ctx == nil || tx == nil {
		return false, authz.ErrUnauthorized
	}
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorUserSession || actor.UserID != userID {
		return false, authz.ErrUnauthorized
	}
	principal, err := authorizer.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleUser})
	if err != nil {
		return false, err
	}
	if principal.EffectiveLevel != 5 && principal.EffectiveLevel != 6 {
		return false, authz.ErrForbidden
	}
	return principal.EffectiveLevel == 5, nil
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeTraineeMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleTrainee)
}

func (authorizer *roleFinalTxAuthorizer) authorize(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind authz.ActorKind,
	role authz.Role,
) error {
	if authorizer == nil || authorizer.authorizer == nil || ctx == nil || tx == nil {
		return errors.New("role final-transaction authorization unavailable")
	}
	if _, ok := authz.StewardCallerFromContext(ctx); ok {
		if role != authz.RoleSteward && role != authz.RoleTrainee || kind != authz.ActorUserSession {
			return authz.ErrForbidden
		}
		principal, err := authorizer.authorizer.AuthorizeStewardCaller(ctx, tx, userID)
		if err == nil && role == authz.RoleTrainee && principal.EffectiveLevel != 5 {
			return authz.ErrForbidden
		}
		return err
	}
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != kind || actor.UserID != userID {
		return authz.ErrUnauthorized
	}
	_, err := authorizer.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return err
}
