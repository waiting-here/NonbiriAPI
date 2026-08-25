// Package flowcontrol provides the shared, server-authoritative concurrency
// and RPM admission boundary for the forwarding exit. One Controller owns one
// per-user concurrency gate and one ratelimit.RPM instance shared by every
// caller, so account concurrency plus the global and per-user RPM windows are
// enforced across all requests.
//
// Admission happens before routing, credential decryption, or any outbound
// I/O: a denied request never touches the Vault, the egress stack, or the
// upstream. The Controller itself creates no HTTP client and owns no
// outbound client. Its per-user concurrency gate is admission-only; the
// egress Stack remains the only outbound boundary and keeps its independent
// global/per-endpoint gate (keyed by canonical base URL) untouched.
//
// A successful Admit returns a Reservation that immediately occupies one
// per-user concurrency permit and one event in each of the global/per-user RPM
// windows. The caller must reach exactly one terminal state: Commit when the
// request is accounted for (a response body byte was written to the client),
// or Release on cancellation, pre-commit failure, or any other path that
// should not consume the budget. Both are idempotent and race-safe.
package flowcontrol

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync/atomic"
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
	// ErrConcurrencyLimited is returned when the user already holds their
	// effective in-flight limit or the bounded active-user map is full. Unlike
	// RPM denials it has no safely predictable Retry-After and never triggers
	// the RPM denial callback.
	ErrConcurrencyLimited = errors.New("flowcontrol: user concurrency limited")
)

const (
	// maxUserConcurrencyLimit aliases the repository's single authoritative
	// hard range so persistence, wire validation, and runtime admission cannot
	// drift independently.
	maxUserConcurrencyLimit = db.MaxUserConcurrencyLimit
	// MaxRetryAfter caps the Retry-After duration emitted to clients so the
	// header value is always finite and bounded.
	MaxRetryAfter = time.Hour
	// DefaultRetryAfter is a defensive fallback only for malformed window
	// decisions. Capacity and concurrency denials deliberately bypass it and
	// omit Retry-After because no safe release time can be predicted.
	DefaultRetryAfter = time.Second
)

// UserLimits is one request-time, server-side snapshot. RPMLimitSet=false
// selects the current administrator default. ConcurrencyLimit is always the
// effective value (the DB resolver maps NULL to the built-in 5).
type UserLimits struct {
	RPMLimit         int
	RPMLimitSet      bool
	ConcurrencyLimit int
}

type UserLimitResolver func(ctx context.Context, userID int64) (UserLimits, error)

// DBUserLimitResolver wires the active account plus its RPM/concurrency fields
// into a Controller through one narrow row read per request. No limit is ever
// taken from a body, header, or stale browser state.
func DBUserLimitResolver(store *db.Store) UserLimitResolver {
	if store == nil {
		return func(context.Context, int64) (UserLimits, error) {
			return UserLimits{}, errors.New("flowcontrol: user limit store is required")
		}
	}
	return func(ctx context.Context, userID int64) (UserLimits, error) {
		limits, err := store.GetUserAdmissionLimits(ctx, userID)
		if errors.Is(err, db.ErrNotFound) {
			return UserLimits{}, ErrInvalidUser
		}
		if err != nil {
			return UserLimits{}, err
		}
		return UserLimits{
			RPMLimit: limits.RPMLimit, RPMLimitSet: limits.RPMLimitSet,
			ConcurrencyLimit: limits.ConcurrencyLimit,
		}, nil
	}
}

// Config wires one shared Controller. A zero RPM value selects the finite
// ratelimit defaults; a partially filled value is normalized by the
// ratelimit package (window and bounded stores filled in, limits validated).
type Config struct {
	RPM ratelimit.RPMConfig
	// UserLimits resolves current account state and explicit limits. Nil is a
	// narrow test/standalone mode using the RPM default and concurrency 5.
	UserLimits UserLimitResolver
	// MaxConcurrentUsers bounds the active-user map. Zero selects the frozen
	// 100000 ceiling; smaller non-zero values are a test/deployment seam only.
	MaxConcurrentUsers int
	// OnDenied is called only after an atomic admission denial has been
	// classified. It is deliberately advisory: the callback must never make
	// the forwarding path proceed without a metered admission. In particular,
	// callers must only attribute RPMUserLimit to the user; global, capacity,
	// and other shared-resource denials are not user violations.
	OnDenied func(context.Context, int64, ratelimit.RPMReason)
}

// Controller is the shared, process-wide concurrency and RPM admission
// boundary. One instance must be shared by every forwarding caller; never
// construct one per request.
type Controller struct {
	limiter         *ratelimit.RPM
	userConcurrency *userConcurrencyLimiter
	userAdmissions  *userAdmissionGate
	userLimits      UserLimitResolver
	onDenied        func(context.Context, int64, ratelimit.RPMReason)
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
	concurrency, err := newUserConcurrencyLimiter(config.MaxConcurrentUsers)
	if err != nil {
		_ = limiter.Close()
		return nil, err
	}
	return &Controller{
		limiter: limiter, userConcurrency: concurrency,
		userAdmissions: &userAdmissionGate{},
		userLimits:     config.UserLimits, onDenied: config.OnDenied,
	}, nil
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

// Admit resolves one live account snapshot, acquires its shared in-flight
// permit, then reserves the global/per-user RPM event in that exact order.
// A concurrency denial creates no RPM event and never invokes OnDenied. If RPM
// refuses or errors, the permit is released before this method returns.
func (c *Controller) Admit(ctx context.Context, userID int64) (*Reservation, time.Duration, error) {
	if c == nil || c.limiter == nil || c.userConcurrency == nil || c.userAdmissions == nil {
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
	// Hold the user's fixed-stripe read guard across the live DB snapshot and
	// permit acquisition. Ban/deletion takes the matching write guard before
	// changing account state, so a request and that state change have one
	// deterministic order even when a resolver was already in flight.
	guard := c.userAdmissions.beginRead(userID)
	defer guard.release()

	limits := UserLimits{ConcurrencyLimit: db.DefaultUserConcurrencyLimit}
	if c.userLimits != nil {
		resolved, err := c.userLimits(ctx, userID)
		if err != nil {
			return nil, 0, err
		}
		limits = resolved
	}
	if limits.ConcurrencyLimit < 1 || limits.ConcurrencyLimit > maxUserConcurrencyLimit {
		return nil, 0, ErrInvalidUser
	}
	if limits.RPMLimitSet {
		if limits.RPMLimit < 1 || limits.RPMLimit > db.MaxUserRPMLimit {
			return nil, 0, ErrInvalidUser
		}
	}
	permit, err := c.userConcurrency.tryAcquire(userID, limits.ConcurrencyLimit)
	if err != nil {
		if errors.Is(err, errConcurrencyClosed) {
			return nil, 0, ErrClosed
		}
		if errors.Is(err, ErrConcurrencyLimited) {
			return nil, 0, ErrConcurrencyLimited
		}
		return nil, 0, err
	}
	// The permit is now the admission linearization point; release the account
	// guard before touching RPM so a concurrent ban need not wait on the RPM
	// mutex or any denial observer.
	guard.release()

	var reservation *ratelimit.RPMReservation
	var decision ratelimit.RPMDecision
	if limits.RPMLimitSet {
		reservation, decision, err = c.limiter.ReserveWithLimit(ctx, userKey(userID), limits.RPMLimit)
	} else {
		// NULL uses the current site default inside the RPM limiter's own lock.
		// Do not take a separate Limits snapshot: SetLimits and Reserve must not
		// have a TOCTOU gap.
		reservation, decision, err = c.limiter.Reserve(ctx, userKey(userID))
	}
	if err != nil {
		permit.Release()
		switch {
		case errors.Is(err, ratelimit.ErrClosed):
			return nil, 0, ErrClosed
		case errors.Is(err, ratelimit.ErrCapacity):
			c.notifyDenied(ctx, userID, decision.Reason)
			// Bounded-store capacity has no time window whose release can be
			// predicted safely; the HTTP layer omits Retry-After.
			return nil, 0, ErrRateLimited
		default:
			return nil, 0, err
		}
	}
	if reservation == nil || !decision.Allowed {
		permit.Release()
		c.notifyDenied(ctx, userID, decision.Reason)
		return nil, boundedRetryAfter(decision.RetryAfter), ErrRateLimited
	}
	return &Reservation{inner: reservation, concurrency: permit}, 0, nil
}

func (c *Controller) notifyDenied(ctx context.Context, userID int64, reason ratelimit.RPMReason) {
	if c == nil || c.onDenied == nil || reason == ratelimit.RPMAllowed {
		return
	}
	c.onDenied(ctx, userID, reason)
}

// ForgetUser retires an active user's exact counter without splitting it. It
// may be called directly only after an authoritative DB state already blocks
// new requests. Callers that must order a ban/deletion mutation against live
// admission must use BeginUserRetirement instead. Old permits remain attached
// to the retained state until their natural terminal release.
func (c *Controller) ForgetUser(userID int64) {
	if c == nil || c.userConcurrency == nil || c.userAdmissions == nil || userID <= 0 {
		return
	}
	guard := c.userAdmissions.beginWrite(userID)
	c.userConcurrency.forgetUser(userID)
	guard.release()
}

// UserRetirement holds the per-user admission write barrier while a caller
// changes the authoritative DB account state. Commit retires the exact active
// concurrency state after the DB change succeeds; Abort leaves it usable when
// the DB operation fails. Both terminal methods are idempotent.
type UserRetirement struct {
	controller *Controller
	userID     int64
	guard      *userAdmissionWriteGuard
	done       atomic.Bool
}

// BeginUserRetirement prevents a live-account snapshot/concurrency acquire
// from crossing a ban or account-deletion mutation. The caller must invoke
// exactly one of Commit or Abort on the returned handle.
func (c *Controller) BeginUserRetirement(userID int64) (*UserRetirement, error) {
	if c == nil || c.userConcurrency == nil || c.userAdmissions == nil {
		return nil, ErrClosed
	}
	if userID <= 0 {
		return nil, ErrInvalidUser
	}
	return &UserRetirement{
		controller: c,
		userID:     userID,
		guard:      c.userAdmissions.beginWrite(userID),
	}, nil
}

// Commit makes the successful DB state change visible to the concurrency
// gate and releases the write barrier.
func (r *UserRetirement) Commit() bool {
	if r == nil || r.controller == nil || r.guard == nil || !r.done.CompareAndSwap(false, true) {
		return false
	}
	r.controller.userConcurrency.forgetUser(r.userID)
	r.guard.release()
	return true
}

// Abort releases the write barrier without retiring the user counter.
func (r *UserRetirement) Abort() bool {
	if r == nil || r.guard == nil || !r.done.CompareAndSwap(false, true) {
		return false
	}
	r.guard.release()
	return true
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
	if c == nil {
		return nil
	}
	if c.userConcurrency != nil {
		c.userConcurrency.close()
	}
	if c.limiter == nil {
		return nil
	}
	return c.limiter.Close()
}

// Reservation is an idempotent, concurrency-safe handle for one admitted
// event. Exactly one of Commit/Release must win; the loser (and any later
// call) returns false.
type Reservation struct {
	inner       *ratelimit.RPMReservation
	concurrency *userConcurrencyPermit
}

// Commit converts the reservation into a committed hit that consumes the
// window budget until it expires. It returns true only for the first
// successful transition.
func (r *Reservation) Commit() bool {
	if r == nil {
		return false
	}
	committed := r.inner != nil && r.inner.Commit()
	if r.concurrency != nil {
		r.concurrency.Release()
	}
	return committed
}

// Release removes the reservation without consuming the window budget. It
// returns true only for the first successful transition.
func (r *Reservation) Release() bool {
	if r == nil {
		return false
	}
	released := r.inner != nil && r.inner.Release()
	if r.concurrency != nil {
		r.concurrency.Release()
	}
	return released
}

// Active reports whether the reservation still needs a terminal action.
func (r *Reservation) Active() bool {
	if r == nil {
		return false
	}
	return (r.inner != nil && r.inner.Active()) ||
		(r.concurrency != nil && r.concurrency.Active())
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
