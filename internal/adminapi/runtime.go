package adminapi

// Runtime applier: the narrow optional hook that applies a validated
// site_config change to the shared process-wide singletons (the flowcontrol
// RPM controller and the egress concurrency gate). It is wired by the
// integration rail; a nil applier means DB-only persistence (values take
// effect on the next restart).

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// RuntimeApplier applies one validated site_config change to the matching
// runtime singleton. Implementations must validate before mutating: a failed
// ApplySiteConfig leaves runtime state unchanged, so the caller can fail
// closed without a DB write. RevertSiteConfig restores runtime state for a
// key to an earlier value after a persistence failure. When the prior database
// row was missing (the read returns the empty string), it restores the frozen
// canonical default for the key instead, so a first-ever write whose
// persistence fails cannot leave the runtime singleton in the just-applied
// state while the database is still empty. It is best-effort: a revert against
// an unwired singleton still fails closed.
type RuntimeApplier interface {
	ApplySiteConfig(ctx context.Context, key, value string) error
	RevertSiteConfig(ctx context.Context, key, value string) error
}

// RPMApplier is the flowcontrol.Controller subset used by the applier.
type RPMApplier interface {
	SetLimits(ratelimit.RPMLimits) error
	Limits() ratelimit.RPMLimits
}

// ConcurrencyApplier is the egress.Stack subset used by the applier.
type ConcurrencyApplier interface {
	SetConcurrencyLimits(egress.ConcurrencyLimits) error
	ConcurrencyLimits() egress.ConcurrencyLimits
}

// OAuthStartThrottleApplier is the ratelimit.IPThrottle subset used by the
// applier to live-apply the per-client-IP OAuth start admission parameters.
// Config reads the current limit/window/penalty so a single-key update can
// preserve the other two; Reconfigure swaps them atomically.
type OAuthStartThrottleApplier interface {
	Config() ratelimit.IPThrottleConfig
	Reconfigure(ratelimit.IPThrottleConfig) error
}

// MaintenanceGate is the maintenance admission singleton subset used by the
// applier. It is the process-wide atomic gate maintained by internal/maintenance;
// a live toggle takes effect for the next request without a restart or a
// per-request DB read.
type MaintenanceGate interface {
	Set(bool)
}

// NewRuntimeApplier wires the shared process-wide singletons. A nil singleton
// fails closed on the matching key. oauth may be nil when the integration rail
// runs without the OAuth start admission throttle (DB-only persistence; values
// take effect on the next restart). maintenance may be nil when the rail runs
// without the server-side maintenance gate (DB-only persistence).
func NewRuntimeApplier(rpm RPMApplier, gate ConcurrencyApplier, oauth OAuthStartThrottleApplier, maintenance MaintenanceGate) RuntimeApplier {
	return &runtimeApplier{rpm: rpm, gate: gate, oauth: oauth, maintenance: maintenance}
}

type runtimeApplier struct {
	rpm         RPMApplier
	gate        ConcurrencyApplier
	oauth       OAuthStartThrottleApplier
	maintenance MaintenanceGate
}

func (a *runtimeApplier) ApplySiteConfig(_ context.Context, key, value string) error {
	switch key {
	case KeyGlobalRPM, KeyDefaultRPMPerUser:
		if a.rpm == nil {
			return errors.New("runtime applier: rpm controller is not wired")
		}
		n, err := parseCanonicalInt(value)
		if err != nil {
			return err
		}
		limits := a.rpm.Limits()
		if key == KeyGlobalRPM {
			limits.GlobalLimit = n
		} else {
			limits.PerUserLimit = n
		}
		return a.rpm.SetLimits(limits)
	case KeyEgressGlobalConc, KeyDefaultPerEndpointConc:
		if a.gate == nil {
			return errors.New("runtime applier: egress gate is not wired")
		}
		n, err := parseCanonicalInt(value)
		if err != nil {
			return err
		}
		limits := a.gate.ConcurrencyLimits()
		if key == KeyEgressGlobalConc {
			limits.Global = n
		} else {
			limits.PerEndpoint = n
		}
		return a.gate.SetConcurrencyLimits(limits)
	case KeyOAuthStartRateLimit, KeyOAuthStartRateWindowSecs, KeyOAuthStartRatePenaltySecs:
		if a.oauth == nil {
			return errors.New("runtime applier: oauth start throttle is not wired")
		}
		n, err := parseCanonicalInt(value)
		if err != nil {
			return err
		}
		current := a.oauth.Config()
		switch key {
		case KeyOAuthStartRateLimit:
			current.Limit = n
		case KeyOAuthStartRateWindowSecs:
			current.Window = time.Duration(n) * time.Second
		case KeyOAuthStartRatePenaltySecs:
			current.Penalty = time.Duration(n) * time.Second
		}
		return a.oauth.Reconfigure(current)
	case KeyMaintenanceMode:
		if a.maintenance == nil {
			return errors.New("runtime applier: maintenance gate is not wired")
		}
		enabled, err := parseCanonicalBool(value)
		if err != nil {
			return err
		}
		a.maintenance.Set(enabled)
		return nil
	default:
		// No runtime singleton backs this key (text keys, registration gate,
		// alert preferences).
		return nil
	}
}

func (a *runtimeApplier) RevertSiteConfig(ctx context.Context, key, previous string) error {
	// A missing prior row (Get returns "") cannot be replayed: the canonical
	// parsers reject the empty string, so without this fallback a persist
	// failure after a first-ever write would fail the revert and leave the
	// runtime singleton in the just-applied state while the database is still
	// empty (DB/runtime drift). Restore the frozen canonical default for the
	// key instead, matching the read path's behavior for a missing row.
	if previous == "" {
		previous = canonicalDefaultStored(key)
	}
	return a.ApplySiteConfig(ctx, key, previous)
}

// canonicalDefaultStored returns the canonical stored-string form of the
// frozen default for a site_config key, used by RevertSiteConfig when the
// prior database row was missing. Only int and bool keys can back a runtime
// singleton; for every other key the result is "" and RevertSiteConfig is a
// no-op anyway (ApplySiteConfig returns nil for keys without a singleton).
func canonicalDefaultStored(key string) string {
	spec, ok := knownSiteConfig[key]
	if !ok {
		return ""
	}
	switch spec.kind {
	case kindInt, kindBool:
		return strconv.Itoa(spec.def)
	default:
		return ""
	}
}
