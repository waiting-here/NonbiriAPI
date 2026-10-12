package gacha

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type testAuthority struct{ admin int64 }

func (a testAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", id).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return authz.ErrUnauthorized
	}
	return nil
}
func (a testAuthority) AuthorizeAdmin(_ context.Context, _ *sql.Tx, id int64) error {
	if id != a.admin {
		return authz.ErrForbidden
	}
	return nil
}

type testGate struct{ blocked atomic.Bool }

func (g *testGate) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error {
	if g.blocked.Load() {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

type testRecorder struct{ fail atomic.Bool }

func (r *testRecorder) RecordLimitedActivityTx(context.Context, *sql.Tx, int64, int64) error {
	if r.fail.Load() {
		return errors.New("injected rollback")
	}
	return nil
}

type fixture struct {
	db                 *sql.DB
	s                  *Service
	at                 atomic.Int64
	gate               *testGate
	recorder           *testRecorder
	user, other, admin int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "gacha.sqlite")

	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fixture{db: store.DB(), gate: &testGate{}, recorder: &testRecorder{}}
	f.at.Store(1800000000)
	if _, err = f.db.Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name  string
		admin int
		dest  *int64
	}{{"player", 0, &f.user}, {"other", 0, &f.other}, {"admin", 1, &f.admin}} {
		zero := make([]byte, 16)
		result, err := f.db.Exec(`INSERT INTO users(discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"gacha-"+entry.name, entry.name, entry.admin, zero, zero, zero, zero, zero, zero, zero, zero, f.at.Load(), f.at.Load())
		if err != nil {
			t.Fatal(err)
		}
		*entry.dest, err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			if _, err = ledger.CreateUserAssetAccount(t.Context(), tx, *entry.dest, asset, f.at.Load()); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	a := testAuthority{f.admin}
	f.s, err = New(Config{Database: f.db, Users: a, Admins: a, Gate: f.gate, Keys: vault, Activity: f.recorder, Now: func() time.Time { return time.Unix(f.at.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) act(user int64, key string, input Action) (Outcome, error) {
	return f.s.Act(context.Background(), user, "test-"+key+strings.Repeat("x", 16), input)
}
func (f *fixture) enter(t *testing.T) {
	t.Helper()
	if _, err := f.act(f.user, "enter", Action{Action: "enter"}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) scalar(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) fund(t *testing.T, asset ledger.Asset, milli int64) {
	t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAssetAccount(t.Context(), tx, f.user, asset)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(t.Context(), tx, "external", asset)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := db.GenerateOpaqueID("op_")
	meta := ledger.Meta{OperationID: id, ActorUserID: f.admin, CreatedAt: f.at.Load()}
	var p ledger.Plan
	if asset == ledger.Game {
		p, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(milli), "Fixture funding")
	} else {
		p, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(milli), 0, ledger.Amount{}, "Fixture funding")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Apply(t.Context(), tx, p); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestWelcomeRetryDuplicateAndRetention(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.enter(t)
	if n := f.scalar(t, "SELECT permanent FROM gacha_profiles WHERE user_id=?", f.user); n != 10 {
		t.Fatal(n)
	}
	f.s.random = func(n int) (int, error) { return 0, nil }
	first, err := f.act(f.user, "draw", Action{Action: "draw", PoolID: "permanent", Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Draw.Items) != 10 || first.Draw.Items[9].Guarantee != "newcomer" || first.Snapshot.Newcomer || first.Snapshot.Fragments[N] != 8 {
		t.Fatal(first)
	}
	replay, err := f.act(f.user, "draw", Action{Action: "draw", PoolID: "permanent", Quantity: 10})
	if err != nil || replay.Draw.ID != first.Draw.ID || replay.Snapshot.Tickets["permanent"] != 0 {
		t.Fatal(replay, err)
	}
	if _, err = f.act(f.user, "draw", Action{Action: "draw", PoolID: "permanent", Quantity: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	f.at.Add(181 * 86400)
	if _, err = f.s.Retain(t.Context(), f.at.Load(), 100, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	replay, err = f.act(f.user, "draw", Action{Action: "draw", PoolID: "permanent", Quantity: 10})
	if err != nil || replay.Draw != nil || replay.Message != "receipt-expired" || len(replay.Snapshot.Owned) != 2 {
		t.Fatal(replay, err)
	}
	if n := f.scalar(t, "SELECT count(*) FROM gacha_operations WHERE user_id=?", f.user); n != 2 {
		t.Fatal(n)
	}
}
func TestPurchaseFundingAndAtomicFailure(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.fund(t, ledger.Game, 15000000)
	f.fund(t, ledger.General, 50000000)
	out, err := f.act(f.user, "buy", Action{Action: "buy", Ticket: "permanent", Quantity: 2, Revision: 1})
	if err != nil || out.Snapshot.Credits.Game != "0" || out.Snapshot.Credits.General != "45000" || out.Snapshot.Tickets["permanent"] != 12 {
		t.Fatal(out, err)
	}
	f.fund(t, ledger.Game, 10000000)
	out, err = f.act(f.user, "limited", Action{Action: "buy", Ticket: "limited", Quantity: 1, Revision: 1})
	if err != nil || out.Snapshot.Credits.Game != "10000" || out.Snapshot.Credits.General != "35000" {
		t.Fatal(out, err)
	}
	f.recorder.fail.Store(true)
	if _, err = f.act(f.user, "rollback", Action{Action: "buy", Ticket: "limited", Quantity: 1, Revision: 1}); err == nil {
		t.Fatal("expected rollback")
	}
	f.recorder.fail.Store(false)
	out, err = f.act(f.user, "rollback", Action{Action: "buy", Ticket: "limited", Quantity: 1, Revision: 1})
	if err != nil || out.Snapshot.Credits.General != "25000" || out.Snapshot.Tickets["limited"] != 2 {
		t.Fatal(out, err)
	}
	if _, err = f.act(f.user, "price", Action{Action: "buy", Ticket: "limited", Quantity: 1, Revision: 2}); !errors.Is(err, ErrPrice) {
		t.Fatal(err)
	}
}
func TestConcurrentGiftDrawAndDailyCheckin(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	var failed atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, err := f.act(f.user, fmt.Sprint("enter", i), Action{Action: "enter"}); err != nil {
				failed.Add(1)
			}
		})
	}
	wg.Wait()
	if failed.Load() != 0 || f.scalar(t, "SELECT permanent FROM gacha_profiles WHERE user_id=?", f.user) != 10 {
		t.Fatal(failed.Load())
	}
	var wins, losses atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			_, err := f.act(f.user, fmt.Sprint("ten", i), Action{Action: "draw", PoolID: "permanent", Quantity: 10})
			if err == nil {
				wins.Add(1)
			} else if errors.Is(err, ErrTickets) {
				losses.Add(1)
			} else {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 || losses.Load() != 3 {
		t.Fatal(wins.Load(), losses.Load())
	}
	f.at.Store(1800028799)
	if _, err := f.act(f.user, "day1", Action{Action: "check-in"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.act(f.user, "day1-again", Action{Action: "check-in"}); !errors.Is(err, ErrClaimed) {
		t.Fatal(err)
	}
	f.at.Add(86400)
	if _, err := f.act(f.user, "day2", Action{Action: "check-in"}); err != nil {
		t.Fatal(err)
	}
}
func TestShopMergedCapsThemeRewardAndOwnership(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET n_shards=1000,r_shards=1000,sr_shards=1000,ssr_shards=1000 WHERE user_id=?", f.user)
	for i := 0; i < 20; i++ {
		product := "ticket-n"
		if i%2 == 1 {
			product = "ticket-r"
		}
		if _, err := f.act(f.user, fmt.Sprint(i), Action{Action: "redeem", Product: product, Revision: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.act(f.user, "cap", Action{Action: "redeem", Product: "ticket-r", Revision: 1}); !errors.Is(err, ErrMonthly) {
		t.Fatal(err)
	}
	for _, card := range catalog.Cards {
		if card.Theme == "k" {
			if _, err := f.act(f.user, card.ID, Action{Action: "redeem", Product: card.ID, Revision: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.act(f.user, "owned", Action{Action: "redeem", Product: "k-chatgpt", Revision: 1}); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	out, err := f.act(f.user, "theme", Action{Action: "claim", Theme: "k"})
	if err != nil || out.Snapshot.Donation != "10" || out.Snapshot.Tickets["limited"] != 1 {
		t.Fatal(out, err)
	}
	if _, err = f.act(f.user, "theme-again", Action{Action: "claim", Theme: "k"}); !errors.Is(err, ErrClaimed) {
		t.Fatal(err)
	}
	other, err := f.s.State(t.Context(), f.other)
	if err != nil || len(other.Owned) != 0 || other.Donation != "0" {
		t.Fatal(other, err)
	}
	f.gate.blocked.Store(true)
	if _, err = f.act(f.user, "blocked", Action{Action: "check-in"}); !errors.Is(err, maintenance.ErrMaintenanceOn) {
		t.Fatal(err)
	}
}
