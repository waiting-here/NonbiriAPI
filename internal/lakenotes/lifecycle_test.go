package lakenotes

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func TestAtomicCloseCheckpointAndReopen(t *testing.T) {
	f := newFixture(t)
	settings := f.enable(t)
	v := f.profile(t)
	start, err := f.service.Start(f.ctx(f.user), f.user, testKey(400), StartInput{v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(2 * int64(time.Second))
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		_, err := f.service.Checkpoint(f.ctx(f.user), f.user, start.Value.Cast.ID, testKey(401), checkpoint(start.Value.Cast, 120))
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrClosed) {
			err = nil
		}
		errs <- err
	}()
	go func() { defer wg.Done(); settings.Enabled = false; _, err := f.configure(settings.Wire); errs <- err }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	saved, err := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
	if err != nil || !saved.Cast.Paused || saved.Cast.State.Held || !saved.Profile.Readonly || saved.Cast.RecoveryAction != "closed" {
		t.Fatal(saved, err)
	}
	if saved.Cast.AckTick != 0 && saved.Cast.AckTick != 120 {
		t.Fatal(saved)
	}
	if _, err = f.service.Resume(f.ctx(f.user), f.user, saved.Cast.ID, testKey(403), control(saved.Cast)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	settings.Enabled = true
	if _, err = f.configure(settings.Wire); err != nil {
		t.Fatal(err)
	}
	resumed, err := f.service.Resume(f.ctx(f.user), f.user, saved.Cast.ID, testKey(408), control(saved.Cast))
	if err != nil || resumed.Value.Cast.SourcePeriodID != "" || resumed.Value.Cast.AckTick != saved.Cast.AckTick {
		t.Fatal(resumed, err)
	}
	if _, err = f.service.Checkpoint(f.ctx(f.user), f.user, saved.Cast.ID, testKey(409), checkpoint(saved.Cast, 1)); !errors.Is(err, ErrConflict) {
		t.Fatal("old controller advanced", err)
	}
}
func TestBanAndPauseAreTransactional(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(420), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.database.BeginTx(f.ctx(f.admin), nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.PrepareBanTx(f.ctx(f.admin), tx, f.user, testNow+1); e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	current, e := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
	if e != nil || current.Cast.Paused {
		t.Fatal("rollback paused cast", current, e)
	}
	f.now.Add(int64(time.Second))
	f.tx(t, func(tx *sql.Tx) {
		if _, e = f.service.PrepareBanTx(f.ctx(f.admin), tx, f.user, testNow+1); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("UPDATE users SET is_banned=1 WHERE id=?", f.user); e != nil {
			t.Fatal(e)
		}
	})
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, start.Value.Cast.ID, testKey(421), checkpoint(start.Value.Cast, 1)); !errors.Is(e, authz.ErrForbidden) {
		t.Fatal(e)
	}
	var paused, held bool
	if e = f.database.QueryRow("SELECT paused,held FROM lake_notes_casts WHERE id=?", start.Value.Cast.ID).Scan(&paused, &held); e != nil || !paused || held {
		t.Fatal(paused, held, e)
	}
}
func TestHistoricalPeriodDoesNotClosePermanentGame(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	f.legacyPeriod(t)
	v := f.profile(t)
	start, err := f.service.Start(f.ctx(f.user), f.user, testKey(430), StartInput{v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(2 * int64(time.Second))
	out, err := f.service.Checkpoint(f.ctx(f.user), f.user, start.Value.Cast.ID, testKey(431), checkpoint(start.Value.Cast, 120))
	if err != nil || out.Value.Cast.Paused || out.Value.Cast.SourcePeriodID != "" {
		t.Fatal("historical schedule still gates game", err)
	}
}
func TestAllTypedActionsUseRulesAndBlockedPausedCast(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	in := ActionInput{ExpectedProfileRevision: v.Revision, Action: rules.Action{Name: "rest"}}
	out, e := f.service.Action(f.ctx(f.user), f.user, testKey(440), in)
	if e != nil || out.Value.Profile.Profile.ClockMinutes != 540 {
		t.Fatal(out, e)
	}
	replay, e := f.service.Action(f.ctx(f.user), f.user, testKey(440), in)
	if e != nil || !replay.Replayed {
		t.Fatal(replay, e)
	}
	in.ExpectedProfileRevision = out.Value.Profile.Revision
	in.Name = "accept_contract"
	in.ID = "1-cleanup"
	out, e = f.service.Action(f.ctx(f.user), f.user, testKey(441), in)
	if e != nil || len(out.Value.Profile.Profile.Contracts) != 5 {
		t.Fatal(out, e)
	}
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(442), StartInput{out.Value.Profile.Revision})
	if e != nil {
		t.Fatal(e)
	}
	paused, e := f.service.Pause(f.ctx(f.user), f.user, start.Value.Cast.ID, testKey(443), control(start.Value.Cast))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Action(f.ctx(f.user), f.user, testKey(444), ActionInput{ExpectedProfileRevision: paused.Value.Profile.Revision, Action: rules.Action{Name: "switch_location", ID: "coast"}}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestDisableStopsAccruingTimeAtConfigurationChange(t *testing.T) {
	f := newFixture(t)
	settings := f.enable(t)
	v := f.profile(t)
	started, err := f.service.Start(f.ctx(f.user), f.user, testKey(450), StartInput{v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	f.now.Add(int64(time.Second))
	settings.Enabled = false
	if _, err = f.configure(settings.Wire); err != nil {
		t.Fatal(err)
	}
	f.now.Add(20 * int64(time.Second))
	current, err := f.service.Cast(f.ctx(f.user), f.user, started.Value.Cast.ID)
	if err != nil || !current.Cast.Paused {
		t.Fatal(current, err)
	}
	var elapsed int64
	if err = f.database.QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", started.Value.Cast.ID).Scan(&elapsed); err != nil || elapsed != int64(time.Second) {
		t.Fatal("disabled time accrued", elapsed, err)
	}
}
func TestRetentionSharesLimitBetweenPauseAndTerminalCleanup(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(460), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	f.tx(t, func(tx *sql.Tx) {
		c, e := castTx(f.ctx(f.user), tx, f.user, start.Value.Cast.ID)
		if e != nil {
			t.Fatal(e)
		}
		row, e := profileTx(f.ctx(f.user), tx, f.user, false, testNow)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 1000 && !c.state.Terminal(); i++ {
			held := false
			if c.state.Fish != nil {
				held = c.state.Fish.Y < c.state.BarY
			}
			row.profile, c.state, e = rules.Advance(row.profile, c.state, []bool{held})
			if e != nil {
				t.Fatal(e)
			}
		}
		if !c.state.Terminal() {
			t.Fatal("terminal fixture not reached")
		}
		if e = c.stop(testNow * int64(time.Second)); e != nil {
			t.Fatal(e)
		}
		if e = saveProfileTx(f.ctx(f.user), tx, f.user, &row, testNow); e != nil {
			t.Fatal(e)
		}
		if e = saveCastTx(f.ctx(f.user), tx, &c, time.Unix(testNow, 0), map[string]any{}); e != nil {
			t.Fatal(e)
		}
	})
	for i, user := range []int64{f.user, f.other} {
		entry, e := f.service.Profile(f.ctx(user), user)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.service.Start(f.ctx(user), user, testKey(462+i*2), StartInput{entry.Revision}); e != nil {
			t.Fatal(e)
		}
	}
	for batch := 0; batch < 3; batch++ {
		work, e := f.service.Retain(f.ctx(f.admin), testNow+31*86400, 1, time.Second)
		if e != nil || work.Processed != 1 || work.More != (batch < 2) {
			t.Fatal(batch, work, e)
		}
		var remaining int
		if e = f.database.QueryRow("SELECT count(*) FROM lake_notes_casts WHERE paused=0 OR terminal_at IS NOT NULL").Scan(&remaining); e != nil || remaining != 2-batch {
			t.Fatal("batch exceeded shared limit", batch, remaining, e)
		}
	}
	var paused int
	var elapsed int64
	if e = f.database.QueryRow("SELECT count(*),min(active_elapsed_ns) FROM lake_notes_casts WHERE paused=1").Scan(&paused, &elapsed); e != nil || paused != 2 || elapsed != 6*int64(time.Second) {
		t.Fatal("expired casts not paused at lease", paused, elapsed, e)
	}
}

func TestRetentionCandidatesUseActiveAndExpiryIndexes(t *testing.T) {
	f := newFixture(t)
	rows, err := f.database.Query("EXPLAIN QUERY PLAN "+retentionCandidatesSQL, testNow*int64(time.Second), testNow-30*86400, testNow-86400, 101)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(steps, " | ")
	if !strings.Contains(plan, "idx_lake_notes_active_cast_user") || !strings.Contains(plan, "idx_lake_notes_cast_retention (terminal_at<?)") {
		t.Fatalf("retention must exclude retained terminal history: %s", plan)
	}
}
