package charityrouting

import (
	"sync"
	"time"
)

// Package-internal per-donation-key admission layer (frozen §L):
// one concurrency slot pool and one sliding-window RPM counter PER donation
// key, independent between keys, never summed across a donation. These
// limits protect SHARED donated credentials only; they are invisible to a
// donor's own requests and never feed any auto-ban statistic.
//
// Reclamation (audit SEC-A2-02): the limiter never grows without bound.
//
//   - release is time-aware: it trims the RPM window to the live minute and
//     drops the whole entry as soon as it has no in-flight slot AND no live
//     RPM event, so an idle key is reclaimed at the very release that empties
//     it (or as soon as its window expires).
//   - tryAdmit lazily sweeps idle entries at most once per
//     keyLimiterSweepInterval, so an entry abandoned by a release that could
//     not yet trim it (its window still had live events) is reclaimed within
//     roughly one minute of going idle.
//   - maxKeyLimiterEntries is a hard cap. A brand-new key that would exceed it
//     triggers one more sweep and, if the table is still full of active
//     entries, is refused (fail closed) — the limiter can never grow past it.
//   - ForgetDonationKeys closes a key so no further admit takes a slot against
//     it while preserving the slot accounting of any in-flight call; the entry
//     is reclaimed once its last slot releases and its window expires. This is
//     the lifecycle hook for donation disable / delete / donor deletion.
//
// The limiter is advisory-only for the usage cap: the authoritative cap check
// happens inside the reservation transaction (used + reserved + amount <= cap);
// this layer's cheap headroom probe just avoids opening doomed transactions.
type keyLimiter struct {
	mu        sync.Mutex
	entries   map[int64]*keyState
	lastSweep time.Time
}

type keyState struct {
	concurrency int64
	// closed is set by ForgetDonationKeys: future admits are refused, but an
	// in-flight call still owns its slot and release still decrements and
	// reclaims the entry when it goes idle. The flag therefore never breaks
	// slot accounting.
	closed bool
	// window is a FIFO of in-window admit timestamps (unix nanos). Its length
	// never exceeds the key's rpm_limit, so a single entry's memory is bounded
	// by the smallest configured cap.
	window []time.Time
}

// maxKeyLimiterEntries is the hard cap on tracked donation-key states. It is
// sized well above any realistic approved-key count and only defends a single
// instance against churn that outruns the lazy sweep; on overflow the limiter
// fails closed instead of growing unbounded.
const maxKeyLimiterEntries = 8192

// keyLimiterSweepInterval bounds how often tryAdmit lazily sweeps idle
// entries. The sweep is O(entries) but runs at most once per interval per
// process, so its amortized cost is negligible while guaranteeing reclamation
// of any idle entry within roughly one minute of its last use.
const keyLimiterSweepInterval = time.Minute

func newKeyLimiter() *keyLimiter {
	return &keyLimiter{entries: make(map[int64]*keyState)}
}

// trimWindow drops RPM events older than one minute from now, reclaiming the
// slice's backing array when everything expires.
func (st *keyState) trimWindow(now time.Time) {
	if len(st.window) == 0 {
		return
	}
	cut := now.Add(-time.Minute)
	newest := st.window[len(st.window)-1]
	if !newest.After(cut) {
		// The newest event already expired → every event did.
		st.window = st.window[:0]
		return
	}
	if st.window[0].After(cut) {
		return // nothing expired yet
	}
	drop := 0
	for drop < len(st.window) && st.window[drop].Before(cut) {
		drop++
	}
	if drop > 0 {
		st.window = append([]time.Time(nil), st.window[drop:]...)
	}
}

// tryAdmit atomically acquires one concurrency slot and one RPM window event
// when both admit. maxConcurrency/rpmLimit follow the frozen 0=unlimited
// convention. On success the caller owns Release; on failure nothing changed.
// A closed key (forgotten) refuses new admits without touching its in-flight
// accounting.
func (l *keyLimiter) tryAdmit(keyID int64, maxConcurrency, rpmLimit int64, now time.Time) bool {
	if keyID <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maybeSweepLocked(now)
	state := l.entries[keyID]
	if state != nil && state.closed {
		return false
	}
	if state == nil {
		if len(l.entries) >= maxKeyLimiterEntries {
			// Table is full: sweep idle entries once more, then fail closed
			// rather than grow unbounded. A still-active table means the
			// limiter is protecting the instance against pathological churn.
			l.sweepLocked(now)
			if len(l.entries) >= maxKeyLimiterEntries {
				return false
			}
		}
		state = &keyState{}
		l.entries[keyID] = state
	}
	state.trimWindow(now)
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

// release returns one concurrency slot acquired by tryAdmit. It is time-aware:
// it trims the RPM window to the live minute and drops the whole entry as soon
// as it has no in-flight slot and no live RPM event, so an abandoned or
// disabled key cannot accumulate forever.
func (l *keyLimiter) release(keyID int64, now time.Time) {
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
	state.trimWindow(now)
	if state.concurrency == 0 && len(state.window) == 0 {
		delete(l.entries, keyID)
	}
}

// ForgetDonationKeys marks the given donation keys closed so no further admit
// can take a slot against them, while preserving the slot accounting of any
// in-flight call (release still decrements and reclaims the entry when it
// goes idle). It is the lifecycle hook for donation disable / delete / donor
// account deletion; the time-aware sweep reclaims a closed-but-not-yet-idle
// entry once its last slot releases and its RPM window expires. Forgetting an
// unknown or already-idle key is a no-op.
func (l *keyLimiter) ForgetDonationKeys(keyIDs ...int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range keyIDs {
		if id <= 0 {
			continue
		}
		st := l.entries[id]
		if st == nil {
			continue
		}
		st.closed = true
		if st.concurrency == 0 && len(st.window) == 0 {
			delete(l.entries, id)
		}
	}
}

// maybeSweepLocked runs a sweep at most once per keyLimiterSweepInterval. It
// initializes lastSweep lazily so a process that starts under a zero clock
// (tests) does not sweep on its very first admit.
func (l *keyLimiter) maybeSweepLocked(now time.Time) {
	if l.lastSweep.IsZero() {
		l.lastSweep = now
		return
	}
	if now.Sub(l.lastSweep) >= keyLimiterSweepInterval {
		l.lastSweep = now
		l.sweepLocked(now)
	}
}

// sweepLocked deletes every entry with no in-flight slot and no live RPM
// event. It never touches an entry that still serves an in-flight call, so it
// is safe to run under admission.
func (l *keyLimiter) sweepLocked(now time.Time) {
	for id, st := range l.entries {
		if st.concurrency != 0 {
			continue
		}
		st.trimWindow(now)
		if len(st.window) == 0 {
			delete(l.entries, id)
		}
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
