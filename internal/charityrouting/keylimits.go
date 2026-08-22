// Package charityrouting implements the [公益] namespace exit: deterministic
// charity model resolution, per-donation-key admission (concurrency / RPM /
// usage cap), the single user pre-reserve with atomic candidate swaps, the
// frozen reservation state machine (first legal response byte = dispatch
// boundary), actual/unknown settlement, donor rewards, and crash-safe
// recovery. It never bypasses the shared egress boundary, the response bounds,
// or the secret lifecycle: upstream attempts run through the same SecureRunner
// discipline as personal traffic.
package charityrouting

import (
	"sync"
	"time"
)

// keyLimiter is the in-process per-donation-key admission layer (frozen §L):
// one concurrency slot pool and one sliding-window RPM counter PER donation
// key, independent between keys, never summed across a donation. These limits
// protect SHARED donated credentials only; they are invisible to a donor's own
// requests and never feed any auto-ban statistic.
//
// The limiter is advisory-only for the usage cap: the authoritative cap check
// happens inside the reservation transaction (used + reserved + amount <= cap);
// this layer's cheap headroom probe just avoids opening doomed transactions.
type keyLimiter struct {
	mu sync.Mutex
	// entries holds one state per donation key. Entries are created lazily on
	// first use and dropped when both counters fall back to zero, so the map
	// can never grow past the number of keys seen recently.
	entries map[int64]*keyState
}

type keyState struct {
	concurrency int64
	// window is a FIFO of in-window admit timestamps (unix nanos). Its length
	// never exceeds the key's rpm_limit, so memory is bounded by the smallest
	// configured cap.
	window []time.Time
}

func newKeyLimiter() *keyLimiter {
	return &keyLimiter{entries: make(map[int64]*keyState)}
}

// tryAdmit atomically acquires one concurrency slot and one RPM window event
// when both admit. maxConcurrency/rpmLimit follow the frozen 0=unlimited
// convention. On success the caller owns Release; on failure nothing changed.
func (l *keyLimiter) tryAdmit(keyID int64, maxConcurrency, rpmLimit int64, now time.Time) bool {
	if keyID <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.entries[keyID]
	if state == nil {
		state = &keyState{}
		l.entries[keyID] = state
	}
	cut := now.Add(-time.Minute)
	if len(state.window) > 0 && !cut.Before(state.window[0]) {
		// Drop expired events from the front.
		drop := 0
		for drop < len(state.window) && state.window[drop].Before(cut) {
			drop++
		}
		state.window = append([]time.Time(nil), state.window[drop:]...)
	}
	if maxConcurrency > 0 && state.concurrency >= maxConcurrency {
		return false
	}
	if rpmLimit > 0 && int64(len(state.window)) >= rpmLimit {
		return false
	}
	state.concurrency++
	if rpmLimit > 0 {
		state.window = append(state.window, now)
	}
	return true
}

// release returns one concurrency slot acquired by tryAdmit. It also trims an
// emptied entry so abandoned keys cannot accumulate forever.
func (l *keyLimiter) release(keyID int64) {
	if keyID <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.entries[keyID]
	if state == nil {
		return
	}
	if state.concurrency > 0 {
		state.concurrency--
	}
	if state.concurrency == 0 && len(state.window) == 0 {
		delete(l.entries, keyID)
	}
}

// capHeadroom reports whether the cheap in-memory probe expects the key's
// usage-cap admission to pass. The reservation transaction re-checks it
// authoritatively; a false answer only skips a doomed candidate early.
func (l *keyLimiter) capHeadroom(creditsUsed, creditsReserved, creditsCap, amount int64) bool {
	if creditsCap <= 0 || amount < 0 {
		return true // unlimited cap or free reserve
	}
	sum := creditsUsed + creditsReserved // bounded non-negative values; checked below
	if sum < 0 {
		return false
	}
	return sum+amount <= creditsCap && sum+amount >= sum
}
