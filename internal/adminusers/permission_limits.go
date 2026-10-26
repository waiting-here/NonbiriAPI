package adminusers

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func authorizeProfilePermissions(ctx context.Context, tx *sql.Tx, actorID int64, input ProfileMutation) error {
	p, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return classifyAuthorizationError(err)
	}
	if (input.LangSet || input.EndpointLimitSet) && !p.Permissions["users.profile"] || input.LevelSet && !p.Permissions["users.level"] || (input.RPMLimitSet || input.ConcurrencySet || input.LightRPMLimitSet || input.LightConcurrencySet || input.GlobalRPMLimitSet) && !p.Permissions["users.limits"] {
		return authz.ErrPermissionDenied
	}
	if input.LevelSet && input.Level != nil {
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level) FROM users WHERE id=?`, actorID).Scan(&level); err != nil {
			return err
		}
		if *input.Level >= level {
			return ErrForbidden
		}
	}
	cfg, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		set      bool
		value    *int64
		fallback int64
		limit    string
	}{
		{input.EndpointLimitSet, input.EndpointLimit, cfg.endpointDefault, "users.endpoint_limit_max"},
		{input.RPMLimitSet, input.RPMLimit, cfg.rpmDefault, "users.rpm_limit_max"},
		{input.LightRPMLimitSet, input.LightRPMLimit, cfg.lightRPMDefault, "users.rpm_limit_max"},
		{input.GlobalRPMLimitSet, input.GlobalRPMLimit, cfg.globalRPMDefault, "users.rpm_limit_max"},
		{input.ConcurrencySet, input.Concurrency, cfg.concurrencyDefault, "users.concurrency_limit_max"},
		{input.LightConcurrencySet, input.LightConcurrency, db.DefaultUserLightConcurrencyLimit, "users.concurrency_limit_max"},
	} {
		if !item.set {
			continue
		}
		value := item.fallback
		if item.value != nil {
			value = *item.value
		}
		max, _ := strconv.ParseInt(p.Limits[item.limit], 10, 64)
		// Endpoint zero is unlimited; only the unrestricted maximum permits it.
		if value > max || item.limit == "users.endpoint_limit_max" && value == 0 && max < 10000 {
			return authz.ErrPermissionDenied
		}
	}
	return nil
}

func authorizeEconomyPermissions(ctx context.Context, tx *sql.Tx, actorID int64, input EconomyMutation) error {
	p, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return classifyAuthorizationError(err)
	}
	key := "users." + input.Target
	max, _ := strconv.ParseInt(p.Limits[key+"_adjustment_max_milli"], 10, 64)
	if !p.Permissions[key] || input.AmountMilli <= 0 || input.AmountMilli > max {
		return authz.ErrPermissionDenied
	}
	return nil
}

func authorizeBanPermissions(ctx context.Context, tx *sql.Tx, actorID int64, duration *int64) error {
	p, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return classifyAuthorizationError(err)
	}
	if duration == nil {
		if !p.Permissions["users.ban_permanent"] {
			return authz.ErrPermissionDenied
		}
		return nil
	}
	max, _ := strconv.ParseInt(p.Limits["users.ban_duration_max_seconds"], 10, 64)
	if *duration > max {
		return authz.ErrPermissionDenied
	}
	return nil
}
