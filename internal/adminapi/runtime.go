package adminapi

// Runtime applier: the narrow optional hook that applies a validated
// site_config change to the shared process-wide singletons (the flowcontrol
// RPM controller and the egress concurrency gate). It is wired by the
// integration rail; a nil applier means DB-only persistence (values take
// effect on the next restart).

import (
	"context"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// RuntimeApplier applies one validated site_config change to the matching
// runtime singleton. Implementations must validate before mutating: a failed
// ApplySiteConfig leaves runtime state unchanged, so the caller can fail
// closed without a DB write. RevertSiteConfig restores runtime state for a
// key to an earlier value after a persistence failure; it is best-effort.
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

// NewRuntimeApplier wires the shared process-wide singletons. A nil singleton
// fails closed on the matching key. oauth may be nil when the integration rail
// runs without the OAuth start admission throttle (DB-only persistence; values
// take effect on the next restart).
func NewRuntimeApplier(rpm RPMApplier, gate ConcurrencyApplier, oauth OAuthStartThrottleApplier) RuntimeApplier {
	return &runtimeApplier{rpm: rpm, gate: gate, oauth: oauth}
}

type runtimeApplier struct {
	rpm   RPMApplier
	gate  ConcurrencyApplier
	oauth OAuthStartThrottleApplier
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
	default:
		// No runtime singleton backs this key (text keys, registration gate,
		// alert preferences).
		return nil
	}
}

func (a *runtimeApplier) RevertSiteConfig(ctx context.Context, key, value string) error {
	return a.ApplySiteConfig(ctx, key, value)
}
