package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// UserLimitDefaults is the site/built-in fallback snapshot used to project
// nullable per-user limits. These values are configuration, not additional
// runtime gates: an explicit user value replaces the corresponding fallback.
type UserLimitDefaults struct {
	Endpoint    int
	RPM         int
	Concurrency int
}

// UserLimitProjection keeps the stored nullable values separate from their
// effective fallbacks. Independent global RPM and egress gates are
// intentionally absent; they must never be written back as a smaller user
// configuration.
type UserLimitProjection struct {
	EndpointLimit             *int
	EffectiveEndpointLimit    int
	RPMLimit                  *int
	EffectiveRPMLimit         int
	ConcurrencyLimit          *int
	EffectiveConcurrencyLimit int
}

// GetUserLimitDefaults reads both configurable fallbacks as one bounded
// snapshot. A malformed persisted value fails closed rather than fabricating
// a user-facing effective value that differs from the runtime configuration.
func (s *Store) GetUserLimitDefaults(ctx context.Context) (UserLimitDefaults, error) {
	if s == nil || ctx == nil {
		return UserLimitDefaults{}, ErrInvalidSiteConfig
	}
	out := UserLimitDefaults{
		Endpoint:    DefaultEndpointLimit,
		RPM:         ratelimit.DefaultRPMPerUserLimit,
		Concurrency: DefaultUserConcurrencyLimit,
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT key, value FROM site_config
WHERE key IN ('default_endpoint_limit','default_rpm_per_user')`)
	if err != nil {
		return UserLimitDefaults{}, fmt.Errorf("read user limit defaults: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return UserLimitDefaults{}, fmt.Errorf("read user limit defaults: %w", err)
		}
		n, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(n) != raw {
			return UserLimitDefaults{}, ErrInvalidSiteConfig
		}
		switch key {
		case siteConfigKeyDefaultEndpointLimit:
			if n < 0 || n > MaxUserEndpointLimit {
				return UserLimitDefaults{}, ErrInvalidSiteConfig
			}
			out.Endpoint = n
		case "default_rpm_per_user":
			if n < 1 || n > MaxUserRPMLimit {
				return UserLimitDefaults{}, ErrInvalidSiteConfig
			}
			out.RPM = n
		}
	}
	if err := rows.Err(); err != nil {
		return UserLimitDefaults{}, fmt.Errorf("read user limit defaults: %w", err)
	}
	return out, nil
}

// ProjectUserLimits combines one persisted user row with a validated fallback
// snapshot. Pointer values are copied so callers cannot mutate repository
// objects through a response projection.
func ProjectUserLimits(user *User, defaults UserLimitDefaults) UserLimitProjection {
	out := UserLimitProjection{
		EffectiveEndpointLimit:    defaults.Endpoint,
		EffectiveRPMLimit:         defaults.RPM,
		EffectiveConcurrencyLimit: defaults.Concurrency,
	}
	if user == nil {
		return out
	}
	if user.EndpointLimit != nil {
		value := *user.EndpointLimit
		out.EndpointLimit = &value
		out.EffectiveEndpointLimit = value
	}
	if user.RPMLimit != nil {
		value := *user.RPMLimit
		out.RPMLimit = &value
		out.EffectiveRPMLimit = value
	}
	if user.ConcurrencyLimit != nil {
		value := *user.ConcurrencyLimit
		out.ConcurrencyLimit = &value
		out.EffectiveConcurrencyLimit = value
	}
	return out
}
