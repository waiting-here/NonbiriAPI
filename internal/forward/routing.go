package forward

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"time"

	mrand "math/rand/v2"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Routing bounds. MaxRouteAttempts caps the dispatch loop at the projected
// candidate count (it can never exceed MaxRouteCandidates); it is named here so
// the retry boundary is obviously finite and not an unbounded loop.
const MaxRouteAttempts = MaxRouteCandidates

// DefaultBackoffBase and DefaultBackoffMax are the system-default exponential
// backoff parameters used when a caller does not override them. They are not
// user-controllable: a caller can neither widen the wait nor disable the cap,
// and the default routing strategy (silent_retry off) never waits at all.
const (
	DefaultBackoffBase = 200 * time.Millisecond
	DefaultBackoffMax  = 2 * time.Second
)

// OrderedSelector returns the projected candidates in (ord, id) order, which is
// the order the SQL projection already yields. It holds no state across calls.
type OrderedSelector struct{}

func (OrderedSelector) Select(_ context.Context, selection Selection) ([]int64, error) {
	if len(selection.Candidates) == 0 {
		return nil, ErrUnboundModel
	}
	ids := make([]int64, len(selection.Candidates))
	for i, candidate := range selection.Candidates {
		ids[i] = candidate.BindingID
	}
	return ids, nil
}

// RandomSelector returns a fresh random permutation of the projected candidates
// per call. It holds no cross-request state: each call seeds a local PCG from
// crypto/rand, so concurrent or sequential calls are independent and no shared
// mutable routing state exists. The set of binding ids is exactly the
// projection's; the selector only reorders them.
type RandomSelector struct{}

func (RandomSelector) Select(_ context.Context, selection Selection) ([]int64, error) {
	if len(selection.Candidates) == 0 {
		return nil, ErrUnboundModel
	}
	perm := shuffleCandidates(selection.Candidates)
	ids := make([]int64, len(perm))
	for i, candidate := range perm {
		ids[i] = candidate.BindingID
	}
	return ids, nil
}

// shuffleCandidates returns a randomly permuted copy of candidates. The input
// slice is not mutated. A fresh PCG is seeded from crypto/rand per call so the
// permutation is independent of every other call and of any package-level
// state. A read failure from crypto/rand (extremely unlikely on a working
// host) falls back to a time seed rather than blocking routing.
func shuffleCandidates(candidates []db.ForwardCandidate) []db.ForwardCandidate {
	out := make([]db.ForwardCandidate, len(candidates))
	copy(out, candidates)
	if len(out) <= 1 {
		return out
	}
	var seed [16]byte
	var s1, s2 uint64 = uint64(time.Now().UnixNano()), uint64(time.Now().UnixNano()) ^ uint64(len(out))
	if _, err := rand.Read(seed[:]); err == nil {
		s1 = binary.LittleEndian.Uint64(seed[0:8])
		s2 = binary.LittleEndian.Uint64(seed[8:16])
	}
	// #nosec G404 -- route order carries no cryptographic property; the local
	// PRNG is independently seeded from crypto/rand and only shuffles candidates.
	rng := mrand.New(mrand.NewPCG(s1, s2))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// BackoffConfig controls the bounded exponential wait between failover
// attempts. Base is the initial wait; each subsequent wait doubles, capped at
// Max. A Base <= 0 disables waiting entirely (used by tests that want no
// delay). The wait is never infinite: each step is <= Max, the number of steps
// is bounded by the candidate count, and ctx cancellation aborts an in-flight
// wait immediately. No goroutine is spawned; the wait is a single timer plus a
// select on ctx.Done.
type BackoffConfig struct {
	Base time.Duration
	Max  time.Duration
}

func (b BackoffConfig) normalized() BackoffConfig {
	cfg := b
	if cfg.Base < 0 {
		cfg.Base = 0
	}
	if cfg.Max <= 0 || cfg.Max < cfg.Base {
		cfg.Max = cfg.Base
	}
	return cfg
}

// wait blocks for the bounded exponential backoff before attemptIndex+1, or
// returns false immediately if ctx was already canceled or becomes canceled
// during the wait. It returns true when the wait elapsed and the caller may
// proceed to the next attempt. attemptIndex is 0-based against the dispatch
// order, so the first failover (after attempt 0) waits Base.
func (b BackoffConfig) wait(ctx context.Context, attemptIndex int) bool {
	cfg := b.normalized()
	if cfg.Base <= 0 {
		return ctx.Err() == nil
	}
	wait := cfg.Base << uint(attemptIndex)
	if wait <= 0 || wait > cfg.Max {
		wait = cfg.Max
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isRetryable reports whether a failed attempt may be followed by another
// candidate under silent retry. The retry boundary is exactly "no response-body
// byte has been committed to the client yet" combined with a pre-commit
// upstream failure (connection / DNS / upstream error status / protocol
// pre-body failure, including a target that vanished between selection and
// dispatch). A committed body byte, a sink write failure, a client cancellation,
// an internal error, or silent_retry off all short-circuit to no retry.
func isRetryable(result connectorcontract.AttemptResult, silentRetry bool) bool {
	return silentRetry && !result.Committed && result.Failure == connectorcontract.FailureUpstream
}
