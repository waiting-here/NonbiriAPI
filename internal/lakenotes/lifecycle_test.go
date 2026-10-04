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
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

func TestAtomicCloseCheckpointAndCrossPeriodResume(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(400), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	directory, e := limitedactivities.New(limitedactivities.Config{Database: f.database, Users: f.service.users, Admins: f.service.admins, Gate: f.service.gate, Keys: f.service.keys, Registry: limitedactivities.NewRegistry(nil).WithLakeNotes(f.service), Now: f.service.now})
	if e != nil {
		t.Fatal(e)
	}
	detail, e := directory.Detail(f.ctx(f.user), f.user, Key)
	if e != nil || detail.Status != "open" {
		t.Fatal(detail, e)
	}
	f.now.Add(2 * int64(time.Second))
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		_, e := f.service.Checkpoint(f.ctx(f.user), f.user, start.Value.Cast.ID, testKey(401), checkpoint(start.Value.Cast, 120))
		if errors.Is(e, ErrConflict) {
			e = nil
		}
		errs <- e
	}()
	go func() {
		defer wg.Done()
		_, e := directory.UpdateConfig(f.ctx(f.admin), f.admin, Key, testKey(402), limitedactivities.ConfigInput{ExpectedRevision: "1", Visible: false, Paused: true, ModuleConfig: []byte("{}")})
		errs <- e
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	saved, e := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
	if e != nil || !saved.Cast.Paused || saved.Cast.State.Held || !saved.Profile.Readonly || saved.Cast.RecoveryAction != "closed" {
		t.Fatal(saved, e)
	}
	if saved.Cast.AckTick != 0 && saved.Cast.AckTick != 120 {
		t.Fatal(saved)
	}
	if _, e = f.service.Resume(f.ctx(f.user), f.user, saved.Cast.ID, testKey(403), control(saved.Cast)); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if _, e = directory.UpdateConfig(f.ctx(f.admin), f.admin, Key, testKey(404), limitedactivities.ConfigInput{ExpectedRevision: "2", Visible: true, ModuleConfig: []byte("{}")}); e != nil {
		t.Fatal(e)
	}
	// A new period requires a new entitlement before the old cast can continue.
	in := PeriodInput{"0", "Next", "published", p.EndsAt, p.EndsAt + 100, ptr("0"), map[Direction]ExchangeSetting{}}
	next, e := f.service.SavePeriod(f.ctx(f.admin), f.admin, "", testKey(405), in)
	if e != nil {
		t.Fatal(e)
	}
	f.now.Store(p.EndsAt * int64(time.Second))
	if _, e = f.service.Resume(f.ctx(f.user), f.user, saved.Cast.ID, testKey(406), control(saved.Cast)); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if _, e = f.service.Entry(f.ctx(f.user), f.user, testKey(407), EntryInput{next.Value.ID, next.Value.Revision}); e != nil {
		t.Fatal(e)
	}
	resumed, e := f.service.Resume(f.ctx(f.user), f.user, saved.Cast.ID, testKey(408), control(saved.Cast))
	if e != nil || resumed.Value.Cast.SourcePeriodID != p.ID || resumed.Value.Cast.AckTick != saved.Cast.AckTick {
		t.Fatal(resumed, e)
	}
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, saved.Cast.ID, testKey(409), checkpoint(saved.Cast, 1)); !errors.Is(e, ErrConflict) {
		t.Fatal("old device advanced", e)
	}
}
func TestBanAndPauseAreTransactional(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
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
func TestNaturalPeriodEndDoesNotAccrueClosedTime(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	f.now.Store((p.EndsAt - 1) * int64(time.Second))
	v := f.enter(t, p)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(430), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	f.now.Add(20 * int64(time.Second))
	saved, e := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
	if e != nil || !saved.Cast.Paused || saved.Cast.AckTick != 0 {
		t.Fatal(saved, e)
	}
	var elapsed int64
	if e = f.database.QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", saved.Cast.ID).Scan(&elapsed); e != nil || elapsed != int64(time.Second) {
		t.Fatal("closed time accrued", elapsed, e)
	}
}
func TestAllTypedActionsUseRulesAndBlockedPausedCast(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
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

func TestScheduleShorteningClampsExistingLease(t *testing.T) {
	for _, source := range []string{"period", "umbrella"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture(t)
			p := f.period(t, "0")
			v := f.enter(t, p)
			start, e := f.service.Start(f.ctx(f.user), f.user, testKey(450), StartInput{v.Revision})
			if e != nil {
				t.Fatal(e)
			}
			end := testNow + 1
			if source == "period" {
				_, e = f.service.SavePeriod(f.ctx(f.admin), f.admin, p.ID, testKey(451), PeriodInput{p.Revision, p.Name, p.Status, p.StartsAt, end, p.EntryFeeMilli, p.Exchanges})
			} else {
				var directory *limitedactivities.Service
				directory, e = limitedactivities.New(limitedactivities.Config{Database: f.database, Users: f.service.users, Admins: f.service.admins, Gate: f.service.gate, Keys: f.service.keys, Registry: limitedactivities.NewRegistry(nil).WithLakeNotes(f.service), Now: f.service.now})
				if e == nil {
					starts := p.StartsAt
					_, e = directory.UpdateConfig(f.ctx(f.admin), f.admin, Key, testKey(451), limitedactivities.ConfigInput{ExpectedRevision: "1", Visible: true, StartsAt: &starts, EndsAt: &end, ModuleConfig: []byte("{}")})
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			// Still-open activity preserves control while the authoritative deadline changes.
			current, e := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
			if e != nil || current.Cast.Paused || current.Cast.Generation != start.Value.Cast.Generation || current.Cast.Revision != start.Value.Cast.Revision {
				t.Fatal(current, e)
			}
			f.now.Add(3 * int64(time.Second))
			saved, e := f.service.Cast(f.ctx(f.user), f.user, start.Value.Cast.ID)
			if e != nil || !saved.Cast.Paused || saved.Cast.AckTick != 0 || !saved.Profile.Readonly {
				t.Fatal(saved, e)
			}
			var elapsed int64
			if e = f.database.QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", saved.Cast.ID).Scan(&elapsed); e != nil || elapsed != int64(time.Second) {
				t.Fatal("time after revised closing accrued", elapsed, e)
			}
		})
	}
}

func TestRetentionSharesLimitBetweenPauseAndTerminalCleanup(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
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
		entry, e := f.service.Entry(f.ctx(user), user, testKey(461+i*2), EntryInput{p.ID, p.Revision})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.service.Start(f.ctx(user), user, testKey(462+i*2), StartInput{entry.Value.Profile.Revision}); e != nil {
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
