package lakenotes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type countedAdmission struct{ calls int }

func (a *countedAdmission) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, user int64) error {
	a.calls++
	return (admission{}).AuthorizeUserActivity(ctx, tx, user)
}

// These are real service operations against the complete WAL/FULL fixture.
// Counts are deterministic; short wall times are diagnostic, not a throughput gate.
func TestTransactionReuseOperationExperiment(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	f.fund(t, f.user, ledger.General, 100000)
	gate := &countedAdmission{}
	f.service.gate = gate
	measure := func(name string, count int, run func(context.Context, int)) {
		t.Helper()
		observer := observability.NewPerformance()
		ctx := observer.Context(f.ctx(f.user))
		before := gate.calls
		started := time.Now()
		for i := 0; i < count; i++ {
			run(ctx, i)
		}
		elapsed := time.Since(started)
		counts := map[string]int64{}
		for _, row := range observer.Drain() {
			counts[row.Route] = row.Completed
		}
		t.Logf("%s operations=%d gate=%d phase_counts=%v elapsed=%s", name, count, gate.calls-before, counts, elapsed)
	}
	const samples = 48
	measure("rest", samples, func(ctx context.Context, i int) {
		out, err := f.service.Action(ctx, f.user, testKey(14000+i), ActionInput{ExpectedProfileRevision: v.Revision, Action: rules.Action{Name: "rest"}})
		if err != nil {
			t.Fatal(err)
		}
		v = out.Value.Profile
	})
	var c CastView
	measure("start-new", 1, func(ctx context.Context, _ int) {
		out, err := f.service.Start(ctx, f.user, testKey(14100), StartInput{v.Revision})
		if err != nil {
			t.Fatal(err)
		}
		c = out.Value.Cast
	})
	measure("start-existing", samples, func(ctx context.Context, i int) {
		out, err := f.service.Start(ctx, f.user, testKey(14200+i), StartInput{v.Revision})
		if err != nil {
			t.Fatal(err)
		}
		c = out.Value.Cast
	})
	measure("pause-resume", samples, func(ctx context.Context, i int) {
		paused, err := f.service.Pause(ctx, f.user, c.ID, testKey(14300+i*2), control(c))
		if err != nil {
			t.Fatal(err)
		}
		resumed, err := f.service.Resume(ctx, f.user, c.ID, testKey(14301+i*2), control(paused.Value.Cast))
		if err != nil {
			t.Fatal(err)
		}
		c = resumed.Value.Cast
		v = resumed.Value.Profile
	})
	measure("exchange", samples, func(ctx context.Context, i int) {
		out, err := f.service.Exchange(ctx, f.user, testKey(14500+i), ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: v.Settings.Revision, ExpectedProfileRevision: v.Revision})
		if err != nil {
			t.Fatal(err)
		}
		v = out.Value.Profile
	})
}

func TestTransactionProjectionMatchesStorageAndHistoricalCurrentCast(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	v := f.profile(t)
	f.fund(t, f.user, ledger.General, 100000)
	ctx := f.ctx(f.user)
	action, err := f.service.Action(ctx, f.user, testKey(1300), ActionInput{ExpectedProfileRevision: v.Revision, Action: rules.Action{Name: "rest"}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := f.service.Profile(ctx, f.user)
	if err != nil || !reflect.DeepEqual(action.Value.Profile, fresh) {
		t.Fatal("action projection differs", err)
	}
	started, err := f.service.Start(ctx, f.user, testKey(1301), StartInput{fresh.Revision})
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string, out CastResult) {
		t.Helper()
		stored, err := f.service.Cast(ctx, f.user, out.Cast.ID)
		if err != nil || !reflect.DeepEqual(out, stored) {
			t.Fatalf("%s projection differs from stored cast/profile: %v", label, err)
		}
	}
	check("start", started.Value)
	paused, err := f.service.Pause(ctx, f.user, started.Value.Cast.ID, testKey(1302), control(started.Value.Cast))
	if err != nil {
		t.Fatal(err)
	}
	check("pause", paused.Value)
	resumed, err := f.service.Resume(ctx, f.user, paused.Value.Cast.ID, testKey(1303), control(paused.Value.Cast))
	if err != nil {
		t.Fatal(err)
	}
	check("resume", resumed.Value)
	f.now.Add(2 * int64(time.Second))
	in := checkpoint(resumed.Value.Cast, 120)
	advanced, err := f.service.Checkpoint(ctx, f.user, resumed.Value.Cast.ID, testKey(1304), in)
	if err != nil {
		t.Fatal(err)
	}
	check("checkpoint", advanced.Value)
	// The stored plain input hash must retain its byte-for-byte typed JSON domain.
	raw, _ := json.Marshal(in)
	digest := sha256.Sum256(raw)
	var ack struct {
		InputDigest string `json:"input_digest"`
	}
	var encoded string
	if err := f.database.QueryRow("SELECT last_ack_json FROM lake_notes_casts WHERE id=?", advanced.Value.Cast.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &ack); err != nil || ack.InputDigest != hex.EncodeToString(digest[:]) {
		t.Fatal("checkpoint input digest changed", ack, err)
	}
	exchanged, err := f.service.Exchange(ctx, f.user, testKey(1305), ExchangeInput{QuoteInput: QuoteInput{GeneralToCoins, "1"}, ExpectedSettingsRevision: advanced.Value.Profile.Settings.Revision, ExpectedProfileRevision: advanced.Value.Profile.Revision})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = f.service.Profile(ctx, f.user)
	if err != nil || exchanged.Value.Receipt.Quote.Wallet != fresh.Wallet || !reflect.DeepEqual(exchanged.Value.Profile, fresh) {
		t.Fatal("post-ledger projection differs", err)
	}
	oldID := advanced.Value.Cast.ID
	// End the actual rules state, then start another current cast for the same user.
	f.tx(t, func(tx *sql.Tx) {
		c, err := castTx(ctx, tx, f.user, oldID)
		if err != nil {
			t.Fatal(err)
		}
		row, err := profileTx(ctx, tx, f.user, false, testNow)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5000 && !c.state.Terminal(); i++ {
			held := c.state.Fish != nil && c.state.Fish.Y < c.state.BarY
			row.profile, c.state, err = rules.Advance(row.profile, c.state, []bool{held})
			if err != nil {
				t.Fatal(err)
			}
		}
		if !c.state.Terminal() {
			t.Fatal("rules did not reach terminal")
		}
		if err = c.stop(f.now.Load()); err != nil {
			t.Fatal(err)
		}
		if err = saveProfileTx(ctx, tx, f.user, &row, testNow); err != nil {
			t.Fatal(err)
		}
		if err = saveCastTx(ctx, tx, &c, time.Unix(0, f.now.Load()), map[string]any{}); err != nil {
			t.Fatal(err)
		}
	})
	fresh, err = f.service.Profile(ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.service.Start(ctx, f.user, testKey(1306), StartInput{fresh.Revision})
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.service.Cast(ctx, f.user, oldID)
	if err != nil || !old.Cast.State.Terminal() || old.Profile.Cast == nil || old.Profile.Cast.ID != next.Value.Cast.ID {
		t.Fatal("historical target replaced current cast", old, err)
	}
	for i, run := range []func(context.Context, int64, string, string, ControlInput) (MutationResult[CastResult], error){f.service.Pause, f.service.Resume} {
		out, err := run(ctx, f.user, oldID, testKey(1307+i), control(old.Cast))
		if err != nil || out.Value.Cast.ID != oldID || out.Value.Profile.Cast == nil || out.Value.Profile.Cast.ID != next.Value.Cast.ID {
			t.Fatal("historical control replaced current cast", out, err)
		}
	}
	replayed, err := f.service.Start(ctx, f.user, testKey(1301), StartInput{action.Value.Profile.Revision})
	if err != nil || !replayed.Replayed || replayed.Value.Cast.ID != oldID || replayed.Value.Profile.Cast == nil || replayed.Value.Profile.Cast.ID != next.Value.Cast.ID {
		t.Fatal("historical replay lost current cast", replayed, err)
	}
}

func TestCollectionHookRefreshesWalletAndAdmission(t *testing.T) {
	f := collectionFixture(t)
	completeCollection(t, f, "lake")
	before := f.profile(t)
	reward := f.service.reward
	f.service.reward = func(ctx context.Context, tx *sql.Tx, user int64, location string, at int64) error {
		if err := reward(ctx, tx, user, location, at); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE maintenance_state SET enabled=1 WHERE id=1")
		return err
	}
	out, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(1310), CollectionInput{Location: "lake"})
	if err != nil || !out.Value.Readonly || out.Value.Wallet.GameMilli != "100000000" || out.Value.Profile.Coins != before.Profile.Coins || out.Value.Profile.CollectionClaims["lake"] != testNow {
		t.Fatal("reward reused stale projection", out, err)
	}
}
