package lakenotes

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func control(v CastView) ControlInput { return ControlInput{v.Generation, v.Revision} }
func checkpoint(v CastView, n uint64) CheckpointInput {
	return CheckpointInput{ControlInput: control(v), FromTick: v.AckTick + 1, ToTick: v.AckTick + n, InitialHeld: v.State.Held, Edges: []Edge{}}
}
func TestSingleCastReplayGenerationAndCumulativeBudget(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(60), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	c := start.Value.Cast
	draws := f.random.draws.Load()
	repeated, e := f.service.Start(f.ctx(f.user), f.user, testKey(61), StartInput{v.Revision})
	if e != nil || repeated.Value.Cast.ID != c.ID || f.random.draws.Load() != draws {
		t.Fatal(repeated, e)
	}
	if _, e = f.service.Action(f.ctx(f.user), f.user, testKey(62), ActionInput{ExpectedProfileRevision: start.Value.Profile.Revision, Action: rules.Action{Name: "rest"}}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	in := checkpoint(c, 120)
	ack, e := f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(63), in)
	if e != nil || ack.Value.Cast.AckTick != 120 {
		t.Fatal(ack, e)
	}
	retry, e := f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(63), in)
	if e != nil || !retry.Replayed || retry.Value.Cast.Revision != ack.Value.Cast.Revision {
		t.Fatal(retry, e)
	}
	c = ack.Value.Cast
	for i := 0; i < 5; i++ {
		paused, e := f.service.Pause(f.ctx(f.user), f.user, c.ID, testKey(70+i*2), control(c))
		if e != nil {
			t.Fatal(e)
		}
		resumed, e := f.service.Resume(f.ctx(f.user), f.user, c.ID, testKey(71+i*2), control(paused.Value.Cast))
		if e != nil {
			t.Fatal(e)
		}
		if resumed.Value.Cast.Generation == c.Generation || resumed.Value.Cast.State.Held {
			t.Fatal(resumed)
		}
		c = resumed.Value.Cast
	}
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(63), in); !errors.Is(e, ErrConflict) {
		t.Fatal("old generation replay resumed controller", e)
	}
	f.now.Add(int64(time.Second) / 10)
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(90), checkpoint(c, 120)); !errors.Is(e, ErrConflict) {
		t.Fatal("resume created tick budget", e)
	}
	f.now.Add(2 * int64(time.Second))
	ack, e = f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(91), checkpoint(c, 120))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(92), checkpoint(c, 1)); !errors.Is(e, ErrConflict) {
		t.Fatal("old revision accepted", e)
	}
	if _, e = f.service.Cast(f.ctx(f.other), f.other, c.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-user cast", e)
	}
}
func TestLeasePauseRestartAndClosingTime(t *testing.T) {
	f := newFixture(t)
	p := f.period(t, "0")
	v := f.enter(t, p)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(100), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	c := start.Value.Cast
	f.now.Add(30 * int64(time.Second))
	current, e := f.service.Cast(f.ctx(f.user), f.user, c.ID)
	if e != nil || !current.Cast.Paused || current.Cast.AckTick != 0 {
		t.Fatal(current, e)
	}
	var elapsed int64
	if e = f.database.QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", c.ID).Scan(&elapsed); e != nil || elapsed != 6*int64(time.Second) {
		t.Fatal(elapsed, e)
	}
	resumed, e := f.service.Resume(f.ctx(f.user), f.user, c.ID, testKey(101), control(current.Cast))
	if e != nil {
		t.Fatal(e)
	}
	f.now.Add(3600 * int64(time.Second))
	work, e := f.service.RecoverBeforeListener(f.ctx(f.user), f.now.Load()/int64(time.Second), 100, time.Second)
	if e != nil || work.Processed != 1 {
		t.Fatal(work, e)
	}
	if e = f.database.QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", c.ID).Scan(&elapsed); e != nil || elapsed != 6*int64(time.Second) {
		t.Fatal("offline time credited", elapsed, e)
	}
	recovered, e := f.service.Cast(f.ctx(f.user), f.user, c.ID)
	if e != nil || !recovered.Cast.Paused || recovered.Cast.State.Plan != resumed.Value.Cast.State.Plan {
		t.Fatal(recovered, e)
	}
}
func TestTerminalExactlyOnceAndExportDeletion(t *testing.T) {
	f := newFixture(t)
	f.random.value = 0.0
	p := f.period(t, "0")
	v := f.enter(t, p)
	start, e := f.service.Start(f.ctx(f.user), f.user, testKey(120), StartInput{v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	// Track the fish with the same confirmed rule state used by the server.
	c := start.Value.Cast
	profile := start.Value.Profile.Profile
	var terminalInput CheckpointInput
	var terminalKey string
	var terminal CastResult
	for batch := 0; batch < 100 && !c.State.Terminal(); batch++ {
		in := checkpoint(c, 120)
		next := c.State
		pp := profile
		held := c.State.Held
		for tick := in.FromTick; tick <= in.ToTick; tick++ {
			target := false
			if next.Fish != nil {
				target = next.Fish.Y < next.BarY
			}
			if target != held {
				in.Edges = append(in.Edges, Edge{tick, target})
				held = target
			}
			pp, next, e = rules.Advance(pp, next, []bool{held})
			if e != nil {
				t.Fatal(e)
			}
			if next.Terminal() {
				break
			}
		}
		f.now.Add(2 * int64(time.Second))
		k := testKey(130 + batch)
		out, e := f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, k, in)
		if e != nil {
			t.Fatal(e)
		}
		c, profile = out.Value.Cast, out.Value.Profile.Profile
		terminalInput, terminalKey, terminal = in, k, out.Value
	}
	if !c.State.Terminal() || c.Phase != "success" || len(profile.Basket) != 1 {
		t.Fatal(c, profile)
	}
	replay, e := f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, terminalKey, terminalInput)
	if e != nil || !replay.Replayed || replay.Value.Profile.Revision != terminal.Profile.Revision {
		t.Fatal(replay, e)
	}
	f.tx(t, func(tx *sql.Tx) {
		export, e := f.service.ExportUserTx(f.ctx(f.user), tx, f.user, 100)
		if e != nil || len(export.Casts) != 1 || len(export.Entries) != 1 {
			t.Fatal(export, e)
		}
		raw, _ := json.Marshal(export)
		for _, bad := range []string{"input_digest", "operation_key_hash", "active_elapsed_ns", "reward_plan"} {
			if strings.Contains(string(raw), bad) {
				t.Fatal("private field exported", bad)
			}
		}
	})
	f.tx(t, func(tx *sql.Tx) {
		if _, e := f.service.PrepareDeleteTx(f.ctx(f.admin), tx, f.user, f.now.Load()/int64(time.Second)); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec("DELETE FROM users WHERE id=?", f.user); e != nil {
			t.Fatal(e)
		}
	})
	if _, e = f.service.Checkpoint(f.ctx(f.user), f.user, c.ID, testKey(240), terminalInput); e == nil {
		t.Fatal("deleted user mutated")
	}
	var profiles int
	if e = f.database.QueryRow("SELECT count(*) FROM lake_notes_profiles WHERE user_id=?", f.user).Scan(&profiles); e != nil || profiles != 0 {
		t.Fatal(profiles, e)
	}
	if _, e = f.service.Retain(f.ctx(f.admin), f.now.Load()/int64(time.Second)+31*86400, lifecycle.WorkerBatchLimit, time.Second); e != nil {
		t.Fatal(e)
	}
}
