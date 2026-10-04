package inactivity

import (
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func instant(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Unix()
}

func TestExecutionTimeUsesSiteDayWithoutWorkerDrift(t *testing.T) {
	offset := 480
	p := testPolicy()
	p.ExecutionTime = "12:00"
	p.Decay.InactiveDays = days(1)
	p.Decay.IntervalDays = days(1)
	p.Protection = ProtectionPolicy{true, days(1)}
	c := Configuration{Policy: p, SiteTimezoneOffsetMinutes: &offset}
	for _, test := range []struct{ name, observed, last, want string }{
		{"before noon", "2026-10-01T03:59:00Z", "", "2026-10-02T04:00:00Z"},
		{"exact noon", "2026-10-01T04:00:00Z", "", "2026-10-02T04:00:00Z"},
		{"past noon", "2026-10-01T04:00:01Z", "", "2026-10-03T04:00:00Z"},
		{"delayed batch", "2026-09-01T00:00:00Z", "2026-10-01T04:00:49Z", "2026-10-02T04:00:00Z"},
		{"missed periods", "2026-09-01T00:00:00Z", "2026-10-09T18:00:00Z", "2026-10-11T04:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := ActivityState{ObservationStartedAt: instant(t, test.observed)}
			if test.last != "" {
				value := instant(t, test.last)
				state.LastDecayAt = &value
			}
			decay, protection := due(c, state)
			if decay == nil || *decay != instant(t, test.want) {
				t.Fatalf("due=%v, want %s", decay, test.want)
			}
			if test.last == "" && (protection == nil || *protection != *decay) {
				t.Fatal("protection used another clock")
			}
		})
	}
	c.ExecutionTime = "00:15"
	state := ActivityState{ObservationStartedAt: instant(t, "2026-10-01T15:59:00Z")}
	decay, _ := due(c, state)
	if *decay != instant(t, "2026-10-02T16:15:00Z") {
		t.Fatal("site midnight crossed the wrong UTC date")
	}
	c.ExecutionTime = "12:00"
	c.Decay.IntervalDays = days(3)
	last := instant(t, "2026-10-01T04:00:49Z")
	state = ActivityState{ObservationStartedAt: last - 100*day, LastDecayAt: &last}
	decay, _ = due(c, state)
	if *decay != instant(t, "2026-10-04T04:00:00Z") {
		t.Fatal("multi-day period drifted")
	}
	c.DecayGraceUntil = instant(t, "2026-10-04T04:00:01Z")
	decay, _ = due(c, state)
	if *decay != instant(t, "2026-10-05T04:00:00Z") {
		t.Fatal("execution preceded grace")
	}
}

func TestExecutionTimeValidationAndGrace(t *testing.T) {
	p := testPolicy()
	for _, clock := range []string{"24:00", "1:00", "12:60", "12:00:00", " 12:00"} {
		p.ExecutionTime = clock
		if Validate(p) == nil {
			t.Fatal("accepted clock", clock)
		}
	}
	p.ExecutionTime = "12:00"
	if Validate(p) != nil {
		t.Fatal("valid clock rejected")
	}
	old := Configuration{Policy: p}
	p.ExecutionTime = ""
	d, _ := grace(old, p, testNow)
	if d != testNow+graceSeconds {
		t.Fatal("schedule change removed grace")
	}
	d, _ = grace(old, old.Policy, testNow)
	if d != 0 {
		t.Fatal("unchanged clock created grace")
	}
}

func TestScheduledDecayRepeatsAtNoonAndCatchesUpOnlyOnce(t *testing.T) {
	e := fixture(t)
	if err := e.store.SetSiteTimezoneOffsetMinutes(480); err != nil {
		t.Fatal(err)
	}
	id := e.user(t, "scheduled", 1, 10000, 2000)
	p := testPolicy()
	p.ExecutionTime = "12:00"
	p.Decay.IntervalDays = days(1)
	e.policy(t, p)
	noon := db.SiteDayKey(testNow, 480) + 12*3600
	if runBatch(t, e.s, noon+49).Decayed != 1 {
		t.Fatal("first decay missing")
	}
	if got := scalar(t, e.store.DB(), `SELECT next_due_at FROM user_activity_state WHERE user_id=?`, id); got != noon+day {
		t.Fatal("late batch skipped the next noon", got)
	}
	if runBatch(t, e.s, noon+50).Decayed != 0 || runBatch(t, e.s, noon+day-1).Decayed != 0 {
		t.Fatal("charged twice or before noon")
	}
	if runBatch(t, e.s, noon+day).Decayed != 1 {
		t.Fatal("next noon skipped")
	}
	if runBatch(t, e.s, noon+20*day+30).Decayed != 1 || e.balance(t, id, ledger.General) != "7290" {
		t.Fatal("missed periods charged more than once")
	}
}
