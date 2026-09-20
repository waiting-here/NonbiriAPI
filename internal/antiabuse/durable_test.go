package antiabuse

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func (f *abuseFixture) restart() {
	f.t.Helper()
	cfg := f.service.config
	if err := f.service.Close(); err != nil {
		f.t.Fatal(err)
	}
	s, err := NewService(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.service = s
}
func TestDurableWindowRestartDoneAndExactExpiry(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	start := f.clock.Load()
	f.set(KeyRPMBanThreshold, "3")
	f.set(KeyRPMBanWindowSeconds, "100")
	f.set(KeyRPMBanDurationSeconds, "1")
	for range 2 {
		if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
			t.Fatal(err)
		}
	}
	f.restart()
	if f.service.events != 2 || f.bans.Load() != 0 {
		t.Fatal("restart lost events or replayed consequences")
	}
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT count(*) FROM abuse_cases WHERE kind='ban'`) != 1 || f.scalar(`SELECT count(*) FROM abuse_evidence`) != 3 {
		t.Fatal("threshold evidence missing")
	}
	f.clock.Add(2)
	f.restart()
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.bans.Load() != 1 || f.scalar(`SELECT count(*) FROM abuse_cases`) != 1 || f.scalar(`SELECT ended_at FROM abuse_cases`) != start+1 {
		t.Fatal("done or authoritative expiry lost")
	}
	var snapshot string
	if err := f.store.DB().QueryRow(`SELECT rules_json FROM abuse_actions WHERE action='trigger'`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var rules map[string]any
	if err := json.Unmarshal([]byte(snapshot), &rules); err != nil || rules["rpm_ban_threshold"] != float64(3) {
		t.Fatal("wrong frozen rule snapshot", snapshot, err)
	}
	f.clock.Store(start + 100)
	f.restart()
	if f.service.events != 1 {
		t.Fatal("exact boundary did not expire the first three events")
	}
	f.set(KeyRPMBanThreshold, "2")
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.bans.Load() != 2 || f.scalar(`SELECT count(*) FROM abuse_cases`) != 2 {
		t.Fatal("new crossing did not create a separate case")
	}
}

func TestSameAttemptCannotReplayShortPenaltyAfterRestart(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyCharityViolationDeductMilli, "1000")
	ctx, id, err := requestattempt.New(context.Background(), user, "POST", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.service.RecordShort(ctx, user, "[公益]a/b", 1)
	if err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.set(KeyCharityMinChars, "50")
	second, err := f.service.RecordShort(ctx, user, "[公益]a/b", 1)
	if err != nil || second.RequestID != id || first.Minimum != second.Minimum {
		t.Fatal("reentry changed rejection", first, second, err)
	}
	if f.history(user).CurrentBalance != "-1" || f.service.events != 1 || f.scalar(`SELECT count(*) FROM request_logs`) != 1 || f.scalar(`SELECT count(*) FROM abuse_cases`) != 1 {
		t.Fatal("reentry applied effects twice")
	}
}

func TestWindowCapacityRejectsWithoutEvictionAndRestorationFailsClosed(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	now := f.clock.Load()
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO abuse_windows VALUES(?,'short_content',0,0,4097,?)`, user, now); err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO abuse_window_events VALUES(?,'short_content',?,?,?,1)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4096; i++ {
		id, err := db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.Exec(user, i, now, id); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.restart()
	if _, err = f.service.RecordShort(context.Background(), user, "[公益]a/b", 1); !errors.Is(err, charityrouting.ErrResourceLimit) {
		t.Fatal("capacity silently evicted", err)
	}
	if f.scalar(`SELECT count(*) FROM abuse_window_events`) != 4096 || f.scalar(`SELECT count(*) FROM request_logs WHERE rejection_reason='resource_limit_exceeded'`) != 1 || f.scalar(`SELECT count(*) FROM abuse_cases`) != 0 {
		t.Fatal("capacity refusal mutated counters or punishment")
	}
	id, _ := db.GenerateOpaqueID("req_")
	if _, err = f.store.DB().Exec(`UPDATE abuse_windows SET next_event_seq=4098 WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().Exec(`INSERT INTO abuse_window_events VALUES(?,'short_content',4097,?,?,1)`, user, now, id); err != nil {
		t.Fatal(err)
	}
	if _, err = NewService(f.service.config); !errors.Is(err, charityrouting.ErrResourceLimit) {
		t.Fatal("over-capacity restore started", err)
	}
	if f.scalar(`SELECT count(*) FROM abuse_window_events`) != 4097 {
		t.Fatal("restore discarded valid evidence")
	}
	f.clock.Add(DefaultViolationWindow.Milliseconds() / 1000)
	f.restart()
	if f.service.events != 0 || f.scalar(`SELECT count(*) FROM abuse_window_events`) != 0 {
		t.Fatal("genuinely expired events retained")
	}
}

func TestRuleShrinkIsDurableAndFailureDoesNotPublishPruning(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyRPMBanThreshold, "4096")
	f.set(KeyRPMBanWindowSeconds, "100")
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(20)
	f.set(KeyRPMBanWindowSeconds, "10")
	if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_window BEFORE INSERT ON abuse_window_events BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err == nil {
		t.Fatal("missing injected failure")
	}
	if f.service.events != 1 || f.scalar(`SELECT count(*) FROM abuse_window_events`) != 1 || f.scalar(`SELECT count(*) FROM request_logs`) != 1 {
		t.Fatal("rollback published a partial window")
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER fail_window`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	f.set(KeyRPMBanWindowSeconds, "100")
	f.restart()
	if f.service.events != 1 {
		t.Fatal("expanded window resurrected deleted facts")
	}
}
