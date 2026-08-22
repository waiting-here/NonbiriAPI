// Package flowcontrol provides the shared, server-authoritative RPM admission
// boundary for the forwarding exit. One Controller owns one ratelimit.RPM
// instance that is shared by every caller subject to the same global cap, so
// the global window and the per-user windows are enforced atomically across
// all requests.
//
// Admission happens before routing, credential decryption, or any outbound
// I/O: a denied request never touches the Vault, the egress stack, or the
// upstream. The Controller itself creates no HTTP client and owns no
// concurrency gate; the egress Stack from the egress package remains the only
// outbound boundary and keeps its global/per-endpoint gate (keyed by the
// canonical base URL) untouched.
//
// A successful Admit returns a Reservation that counts against both windows
// immediately. The caller must reach exactly one terminal state: Commit when
// the request is accounted for (a response body byte was written to the
// client), or Release on cancellation, pre-commit failure, or any other path
// that should not consume the budget. Both are idempotent and race-safe.
package flowcontrol

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

var (
	// ErrRateLimited is returned when the shared global or per-user window is
	// full, or when the bounded per-user key store cannot admit a new
	// identity. Callers fail closed: no outbound work may start.
	ErrRateLimited = errors.New("flowcontrol: rate limited")
	// ErrClosed is returned once the controller has been shut down. New
	// admissions are rejected; in-flight reservations are force-released.
	ErrClosed = errors.New("flowcontrol: controller is closed")
	// ErrInvalidUser is returned for a non-positive or otherwise unusable
	// user id. A caller key must always resolve to a real server-side
	// identity before admission is attempted.
	ErrInvalidUser = errors.New("flowcontrol: user id is invalid")
)

const (
	// MaxRetryAfter caps the Retry-After duration emitted to clients so the
	// header value is always finite and bounded.
	MaxRetryAfter = time.Hour
	// DefaultRetryAfter is used when a denial has no precise window expiry
	// (for example a bounded-key-store capacity denial). It is never zero so
	// a rejected client cannot hammer in a tight loop.
	DefaultRetryAfter = time.Second
)

// UserLimitResolver returns a user's server-side per-user RPM cap.
// has=false means the user has no custom cap and the administrator default
// applies. The returned value is a hint only: the Controller clamps it to the
// current administrator ceiling before admission.
type UserLimitResolver func(ctx context.Context, userID int64) (limit int, has bool, err error)

// DBUserLimitResolver wires users.rpm_limit into a Controller. The lookup is
// a narrow single-row read per request; the value is server-authoritative and
// never taken from a body, header, or client state.
func DBUserLimitResolver(store *db.Store) UserLimitResolver {
	if store == nil {
		return func(context.Context, int64) (int, bool, error) {
			return 0, false, errors.New("flowcontrol: user limit store is required")
		}
	}
	return func(_ context.Context, userID int64) (int, bool, error) {
		return store.GetUserRPMLimit(userID)
	}
}

// Config wires one shared Controller. A zero RPM value selects the finite
// ratelimit defaults; a partially filled value is normalized by the
// ratelimit package (window and bounded stores filled in, limits validated).
type Config struct {
	RPM ratelimit.RPMConfig
	// UserLimits resolves a user's self-tuned cap; nil means the
	// administrator default applies to every user.
	UserLimits UserLimitResolver
	// OnDenied is called only after an atomic admission denial has been
	// classified. It is deliberately advisory: the callback must never make
	// the forwarding path proceed without a metered admission. In particular,
	// callers must only attribute RPMUserLimit to the user; global, capacity,
	// and other shared-resource denials are not user violations.
	OnDenied func(context.Context, int64, ratelimit.RPMReason)
}

// Controller is the shared, process-wide RPM admission boundary. One instance
// must be shared by every forwarding caller; never construct one per request.
type Controller struct {
	limiter    *ratelimit.RPM
	userLimits UserLimitResolver
	onDenied   func(context.Context, int64, ratelimit.RPMReason)
}

// New constructs one shared Controller.
func New(config Config) (*Controller, error) {
	return newWithClock(config, nil)
}

// newWithClock is the test seam for deterministic window boundaries; nil
// selects the real clock.
func newWithClock(config Config, clock ratelimit.Clock) (*Controller, error) {
	rpmConfig := config.RPM
	if rpmConfig.Window == 0 && rpmConfig.GlobalLimit == 0 && rpmConfig.PerUserLimit == 0 {
		rpmConfig = ratelimit.DefaultRPMConfig()
	}
	// A typed nil (for example a nil *fakeClock wrapped in the Clock
	// interface) must not reach the ratelimit option; detect it the same way
	// the forward package guards adapter/codec nil checks.
	if nilClock(clock) {
		clock = nil
	}
	var limiter *ratelimit.RPM
	var err error
	if clock == nil {
		limiter, err = ratelimit.NewRPM(rpmConfig)
	} else {
		limiter, err = ratelimit.NewRPM(rpmConfig, ratelimit.WithClock(clock))
	}
	if err != nil {
		return nil, err
	}
	return &Controller{limiter: limiter, userLimits: config.UserLimits, onDenied: config.OnDenied}, nil
}

func nilClock(clock ratelimit.Clock) bool {
	if clock == nil {
		return true
	}
	value := reflect.ValueOf(clock)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Admit atomically checks the shared global and per-user windows and reserves
// one event when both admit. On success the returned Reservation counts
// immediately and must reach exactly one terminal state (Commit or Release).
// On denial it returns ErrRateLimited with a bounded Retry-After. Capacity
// exhaustion and a closed controller also fail closed (ErrRateLimited /
// ErrClosed); context errors are propagated unchanged.
func (c *Controller) Admit(ctx context.Context, userID int64) (*Reservation, time.Duration, error) {
	if c == nil || c.limiter == nil {
		return nil, 0, ErrClosed
	}
	if ctx == nil {
		return nil, 0, ErrInvalidUser
	}
	if userID <= 0 {
		return nil, 0, ErrInvalidUser
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	userLimit := c.limiter.Limits().PerUserLimit
	if c.userLimits != nil {
		limit, has, err := c.userLimits(ctx, userID)
		if err != nil {
			return nil, 0, err
		}
		if has {
			userLimit = c.clampUserLimit(limit)
		}
	}
	reservation, decision, err := c.limiter.ReserveWithLimit(ctx, userKey(userID), userLimit)
	if err != nil {
		switch {
		case errors.Is(err, ratelimit.ErrClosed):
			return nil, 0, ErrClosed
		case errors.Is(err, ratelimit.ErrCapacity):
			c.notifyDenied(ctx, userID, decision.Reason)
			return nil, boundedRetryAfter(decision.RetryAfter), ErrRateLimited
		default:
			return nil, 0, err
		}
	}
	if reservation == nil || !decision.Allowed {
		c.notifyDenied(ctx, userID, decision.Reason)
		return nil, boundedRetryAfter(decision.RetryAfter), ErrRateLimited
	}
	return &Reservation{inner: reservation}, 0, nil
}

// clampUserLimit caps a user's self-tuned cap at the server's current
// per-user ceiling (the administrator default). An invalid stored value
// (zero, negative, or above the ceiling) falls back to the ceiling rather
// than being trusted; a value can never raise a user's budget above the
// administrator's cap.
func (c *Controller) notifyDenied(ctx context.Context, userID int64, reason ratelimit.RPMReason) {
	if c == nil || c.onDenied == nil || reason == ratelimit.RPMAllowed {
		return
	}
	c.onDenied(ctx, userID, reason)
}

func (c *Controller) clampUserLimit(userLimit int) int {
	ceiling := c.limiter.Limits().PerUserLimit
	if userLimit < 1 || userLimit > ceiling {
		return ceiling
	}
	return userLimit
}

// SetLimits applies runtime administrator settings without replacing the
// shared counters. Existing events are not discarded: if a cap is lowered,
// new admissions wait for old events to leave the window. This is the wiring
// point for site_config updates (the integration rail reads site_config and
// calls this method; the egress Stack's SetConcurrencyLimits is updated
// separately by the same rail).
func (c *Controller) SetLimits(limits ratelimit.RPMLimits) error {
	if c == nil || c.limiter == nil {
		return ErrClosed
	}
	return c.limiter.SetLimits(limits)
}

// Limits returns a consistent snapshot of the current caps.
func (c *Controller) Limits() ratelimit.RPMLimits {
	if c == nil || c.limiter == nil {
		return ratelimit.RPMLimits{}
	}
	return c.limiter.Limits()
}

// Close releases all stored limiter state and rejects new admissions.
// In-flight reservations are force-released; their Commit/Release calls
// become no-ops. Close is idempotent and does not start a background
// goroutine.
func (c *Controller) Close() error {
	if c == nil || c.limiter == nil {
		return nil
	}
	return c.limiter.Close()
}

// Reservation is an idempotent, concurrency-safe handle for one admitted
// event. Exactly one of Commit/Release must win; the loser (and any later
// call) returns false.
type Reservation struct {
	inner *ratelimit.RPMReservation
}

// Commit converts the reservation into a committed hit that consumes the
// window budget until it expires. It returns true only for the first
// successful transition.
func (r *Reservation) Commit() bool {
	return r != nil && r.inner != nil && r.inner.Commit()
}

// Release removes the reservation without consuming the window budget. It
// returns true only for the first successful transition.
func (r *Reservation) Release() bool {
	return r != nil && r.inner != nil && r.inner.Release()
}

// Active reports whether the reservation still needs a terminal action.
func (r *Reservation) Active() bool {
	return r != nil && r.inner != nil && r.inner.Active()
}

func userKey(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// boundedRetryAfter keeps the denial hint finite and non-zero.
func boundedRetryAfter(until time.Duration) time.Duration {
	if until <= 0 {
		until = DefaultRetryAfter
	}
	if until > MaxRetryAfter {
		until = MaxRetryAfter
	}
	return until
}
