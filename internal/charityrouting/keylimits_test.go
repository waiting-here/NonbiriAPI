package charityrouting

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyLimiterReleaseReclaimsIdleEntry(t *testing.T) {
	l := newKeyLimiter()
	now := time.Unix(1000, 0)
	if !l.tryAdmit(1, 1, 10, now) {
		t.Fatal("first admit failed")
	}
	if _, ok := l.entries[1]; !ok {
		t.Fatal("entry not created")
	}
	// Release with a still-live window: concurrency drops to 0 but the RPM
	// event is younger than one minute, so the entry is retained.
	l.release(1, now)
	if _, ok := l.entries[1]; !ok {
		t.Fatal("entry dropped while RPM window still live")
	}
	// Advance past one minute and release again (no-op concurrency, but the
	// time-aware trim drops the expired window and deletes the idle entry).
	later := now.Add(2 * time.Minute)
	l.release(1, later)
	if _, ok := l.entries[1]; ok {
		t.Fatal("idle entry not reclaimed after window expiry")
	}
	if len(l.entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(l.entries))
	}
}

func TestKeyLimiterSweepReclaimsIdleAfterRelease(t *testing.T) {
	l := newKeyLimiter()
	now := time.Unix(5000, 0)
	l.tryAdmit(7, 0, 60, now) // rpm>0, no concurrency cap
	l.release(7, now)         // concurrency 0, window still live
	if _, ok := l.entries[7]; !ok {
		t.Fatal("entry dropped too early")
	}
	// The lazy sweep runs inside the next tryAdmit once the sweep interval has
	// elapsed. Advance the clock past the interval AND past the RPM window.
	future := now.Add(keyLimiterSweepInterval + time.Minute)
	// A different key's admit triggers the sweep, which reclaims key 7.
	l.tryAdmit(8, 0, 0, future)
	if _, ok := l.entries[7]; ok {
		t.Fatal("sweep did not reclaim the idle entry after window expiry")
	}
}

func TestKeyLimiterHardCapFailsClosed(t *testing.T) {
	// Use a limiter with a tiny cap by filling it with active entries that the
	// sweep cannot reclaim (concurrency > 0), then assert a brand-new key is
	// refused instead of growing the table.
	l := newKeyLimiter()
	now := time.Unix(0, 0)
	// Override the cap by filling exactly maxKeyLimiterEntries active entries.
	for i := int64(1); i <= maxKeyLimiterEntries; i++ {
		if !l.tryAdmit(i, 1, 0, now) {
			t.Fatalf("admit %d failed while filling", i)
		}
	}
	if len(l.entries) != maxKeyLimiterEntries {
		t.Fatalf("entries = %d, want %d", len(l.entries), maxKeyLimiterEntries)
	}
	// A brand-new key exceeds the cap and every existing entry is active, so
	// the limiter fails closed rather than growing unbounded.
	if l.tryAdmit(maxKeyLimiterEntries+1, 1, 0, now) {
		t.Fatal("admit succeeded past the hard cap (must fail closed)")
	}
	if len(l.entries) != maxKeyLimiterEntries {
		t.Fatalf("entries = %d, want %d (cap must not grow)", len(l.entries), maxKeyLimiterEntries)
	}
	// Releasing one entry frees room; the next new key admits.
	l.release(1, now)
	if !l.tryAdmit(maxKeyLimiterEntries+1, 1, 0, now) {
		t.Fatal("admit failed after a slot was freed")
	}
}

func TestKeyLimiterHighRPMWindowReclaims(t *testing.T) {
	// A key configured for the maximum rpm_limit (4096) that bursts once and
	// goes idle must still be reclaimed once the window expires, proving the
	// per-entry memory bound (window length <= rpm_limit) does not pin it.
	l := newKeyLimiter()
	now := time.Unix(0, 0)
	const rpm = 4096
	// Admit/release rpm times against a single concurrency slot: the slot
	// returns to zero each time but the RPM window accumulates rpm events.
	for i := 0; i < rpm; i++ {
		ts := now.Add(time.Duration(i) * time.Nanosecond)
		if !l.tryAdmit(1, 1, rpm, ts) {
			t.Fatalf("admit %d failed", i)
		}
		l.release(1, ts)
	}
	if got := len(l.entries[1].window); got != rpm {
		t.Fatalf("window len = %d, want %d", got, rpm)
	}
	if l.entries[1].concurrency != 0 {
		t.Fatalf("concurrency = %d, want 0", l.entries[1].concurrency)
	}
	// The entry is retained while the window is live.
	if _, ok := l.entries[1]; !ok {
		t.Fatal("entry dropped while window live")
	}
	// Advance past the window: a release (or sweep) trims the expired window
	// and deletes the idle entry.
	later := now.Add(2 * time.Minute)
	l.release(1, later)
	if _, ok := l.entries[1]; ok {
		t.Fatal("4096-RPM entry not reclaimed after window expiry")
	}
}

func TestKeyLimiterForgetClosesButPreservesInFlight(t *testing.T) {
	l := newKeyLimiter()
	now := time.Unix(0, 0)
	if !l.tryAdmit(1, 1, 10, now) {
		t.Fatal("admit failed")
	}
	// Forget the key while a call is in flight: new admits must refuse, but
	// the in-flight slot is still accounted (release decrements it).
	l.ForgetDonationKeys(1)
	if l.tryAdmit(1, 1, 10, now) {
		t.Fatal("admit succeeded against a forgotten key (must refuse)")
	}
	if st := l.entries[1]; st == nil || !st.closed || st.concurrency != 1 {
		t.Fatalf("forgotten entry state = %+v, want closed/concurrency=1", st)
	}
	// Releasing the in-flight slot reclaims the now-closed idle entry.
	l.release(1, now.Add(2*time.Minute))
	if _, ok := l.entries[1]; ok {
		t.Fatal("closed entry not reclaimed after its slot released")
	}
	// Forgetting an unknown key is a no-op.
	l.ForgetDonationKeys(999)
}

func TestKeyLimiterRPMAdmitsAndRejects(t *testing.T) {
	l := newKeyLimiter()
	now := time.Unix(0, 0)
	for i := 0; i < 3; i++ {
		if !l.tryAdmit(1, 0, 3, now) {
			t.Fatalf("admit %d failed under rpm=3", i)
		}
	}
	if l.tryAdmit(1, 0, 3, now) {
		t.Fatal("admit exceeded rpm limit")
	}
	// Releasing concurrency does NOT free RPM events (concurrency was 0
	// already), and the window is still live, so the next admit still rejects.
	l.release(1, now)
	if l.tryAdmit(1, 0, 3, now) {
		t.Fatal("admit succeeded before the RPM window expired")
	}
	// After the window expires, admit works again.
	if !l.tryAdmit(1, 0, 3, now.Add(2*time.Minute)) {
		t.Fatal("admit failed after RPM window expired")
	}
}

func TestKeyLimiterConcurrencyRace(t *testing.T) {
	// Many goroutines admit/release against the same key with a tight
	// concurrency cap; the limiter must never over-admit and must leak no slot.
	// Over-admission is detected through an atomic outstanding-counter (the
	// only safe way to observe the cap from outside the limiter's lock), so the
	// test never reads internal state without the limiter's own lock.
	l := newKeyLimiter()
	const concurrency = 4
	var wg sync.WaitGroup
	var active, over int64
	now := time.Unix(0, 0)
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if l.tryAdmit(1, concurrency, 0, now) {
					cur := atomic.AddInt64(&active, 1)
					if cur > concurrency {
						atomic.AddInt64(&over, 1)
					}
					atomic.AddInt64(&active, -1)
					l.release(1, now)
				}
			}
		}()
	}
	wg.Wait()
	if over != 0 {
		t.Fatalf("over-admitted %d times past concurrency cap", over)
	}
	// No leaked slot: after the storm, the full concurrency cap admits again
	// (a leaked slot would make the last admits fail). Release them and the
	// idle entry is reclaimed.
	for i := 0; i < concurrency; i++ {
		if !l.tryAdmit(1, concurrency, 0, now) {
			t.Fatalf("admit %d after race failed (leaked slot)", i)
		}
	}
	for i := 0; i < concurrency; i++ {
		l.release(1, now)
	}
	l.release(1, now.Add(2*time.Minute)) // trim expired window (rpm=0 → no window)
	if _, ok := l.entries[1]; ok {
		t.Fatal("entry not reclaimed after the race")
	}
}
