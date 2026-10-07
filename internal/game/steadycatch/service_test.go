package steadycatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type testAuth struct{}

func (testAuth) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND is_admin=0 AND is_banned=0`, user).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return resources.ErrUnauthorized
	}
	return nil
}
func (testAuth) AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	return maintenance.ContinuationSnapshot{}, maintenance.ErrContinuationDenied
}
func (testAuth) DeriveGenerationTwoSubkey(b []byte) ([]byte, error) {
	h := sha256.Sum256(b)
	return h[:], nil
}

type fixture struct {
	t    *testing.T
	s    *Service
	db   *sql.DB
	user int64
	now  atomic.Int64
	ctx  context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background()}
	f.now.Store(100_000)
	path := filepath.Join(t.TempDir(), "game.db")
	dbfixture.Materialize(t, path)
	var err error
	f.db, err = sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	t.Cleanup(func() { f.db.Close() })
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, a ...any) sql.Result {
		r, err := tx.Exec(q, a...)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('games_enabled','game_steadycatch_enabled')`)
	exec(`UPDATE site_config SET value='5000' WHERE key='game_steadycatch_price_milli'`)
	exec(`UPDATE site_config SET value='1000' WHERE key='game_steadycatch_first_reward_milli'`)
	exec(`UPDATE maintenance_state SET enabled=0 WHERE id=1`)
	zero := db.EncodeU128(db.U128{})
	r := exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('catch-test','player',?,?,?,?,?,?,?,?,100,100)`, zero, zero, zero, zero, zero, zero, zero, zero)
	f.user, err = r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	c, err := continuity.New(f.db, testAuth{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.BindUserTx(f.ctx, tx, f.user); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		wallet, err := ledger.CreateUserAssetAccount(f.ctx, tx, f.user, asset, 100)
		if err != nil {
			t.Fatal(err)
		}
		external, err := ledger.CodedAssetAccount(f.ctx, tx, "external", asset)
		if err != nil {
			t.Fatal(err)
		}
		op, _ := db.GenerateOpaqueID("op_")
		meta := ledger.Meta{OperationID: op, ActorUserID: f.user, CreatedAt: 100}
		var plan ledger.Plan
		if asset == ledger.Game {
			plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(3000), "funding")
		} else {
			plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(100000), 0, ledger.Amount{}, "funding")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.Apply(f.ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	limiter, _ := game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time { return time.UnixMilli(f.now.Load()) }})
	t.Cleanup(func() { limiter.Close() })
	ports, err := builtinfinance.ForModule(game.SteadyCatchID)
	if err != nil {
		t.Fatal(err)
	}
	f.s, err = New(Options{Shared: host.Services{Database: f.db, UserAuthorizer: testAuth{}, Limiter: limiter, Now: func() time.Time { return time.UnixMilli(f.now.Load()) }}, Finance: ports.Solo, Continuation: testAuth{}})
	if err != nil {
		t.Fatal(err)
	}
	f.s.seed = func() (uint32, error) { return ^uint32(0), nil }
	t.Cleanup(func() { f.s.Close() })
	return f
}
func (f *fixture) start(key string) View {
	f.t.Helper()
	v, err := f.s.Start(f.ctx, f.user, "catch-test-key-"+key)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *fixture) control(v View, action string, until int, inputs []engine.Input) View {
	f.t.Helper()
	out, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, Controls{Revision: v.Revision, Action: action, Until: until, Inputs: inputs})
	if err != nil {
		f.t.Fatal(action, err)
	}
	return out
}
func (f *fixture) balances() (int64, int64) {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	a, e := ledger.UserAccount(f.ctx, tx, f.user)
	if e != nil {
		f.t.Fatal(e)
	}
	b, e := ledger.UserAssetAccount(f.ctx, tx, f.user, ledger.Game)
	if e != nil {
		f.t.Fatal(e)
	}
	return a.Balance.Big().Int64(), b.Balance.Big().Int64()
}
func greedyBatch(s engine.State) (engine.State, []engine.Input) {
	var inputs []engine.Input
	end := min(s.Tick+300, engine.LastTick)
	for s.Tick < end && !s.Finished() {
		target, maxY := s.Target, -1000000
		for _, item := range s.Items {
			if !item.Checked && item.Kind != "hazard" && item.Y > maxY {
				maxY, target = item.Y, item.X
			}
		}
		var controls []engine.Input
		if target != s.Target || s.Charge == 10 {
			v := engine.Input{Tick: s.Tick + 1, Target: target, Shield: s.Charge == 10}
			inputs = append(inputs, v)
			controls = []engine.Input{v}
		}
		var err error
		s, err = engine.Advance(s, controls, s.Tick+1)
		if err != nil {
			panic(err)
		}
	}
	return s, inputs
}
func (f *fixture) win(v View) View {
	f.t.Helper()
	v = f.control(v, "resume", v.State.Tick, nil)
	anchor := f.now.Load()
	for !v.State.Finished() {
		predicted, inputs := greedyBatch(v.State)
		f.now.Store(anchor + int64(predicted.Tick)*1000/60 + 1)
		v = f.control(v, "advance", predicted.Tick, inputs)
		if !reflect.DeepEqual(v.State, predicted) {
			f.t.Fatal("server prediction diverged")
		}
	}
	if v.Status != "completed" || v.State.Score != 2350 {
		f.t.Fatalf("unexpected finish %+v", v)
	}
	return v
}
func TestTicketReplayAndOneFirstClear(t *testing.T) {
	f := newFixture(t)
	v := f.start("first-game")
	if v.Status != "paused" || v.Payment.General != "2" || v.Payment.Game != "3" {
		t.Fatalf("entry %+v", v)
	}
	prior := f.start("first-game")
	if prior.ID != v.ID {
		t.Fatal("replayed start changed game")
	}
	if a, b := f.balances(); a != 98000 || b != 0 {
		t.Fatalf("fee %d %d", a, b)
	}
	v = f.win(v)
	if !v.FirstClear || v.Reward != "1" {
		t.Fatalf("first clear %+v", v)
	}
	if a, b := f.balances(); a != 98000 || b != 1000 {
		t.Fatalf("reward %d %d", a, b)
	}
	prior = f.start("first-game")
	if prior.Status != "completed" {
		t.Fatal("replay lost terminal result")
	}
	f.now.Add(1000)
	second := f.win(f.start("second-game"))
	if second.FirstClear || second.Reward != "0" {
		t.Fatal("repeat clear rewarded")
	}
	board, err := f.s.Leaderboard(f.ctx, f.user, "7d")
	if err != nil || len(board.Rows) != 1 || board.Rows[0].Score != 2350 || board.Rows[0].AchievedAt != *v.TerminalAt {
		t.Fatalf("ranking %+v %v", board, err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	value, err := f.s.export(f.ctx, tx, f.user, f.now.Load()/1000, 1000)
	if err != nil || !value.FirstCleared || len(value.Sessions) != 2 {
		t.Fatalf("export %+v %v", value, err)
	}
	for _, session := range value.Sessions {
		state := engine.New(session.Seed)
		for _, b := range session.Batches {
			state, err = engine.Advance(state, b.Inputs, b.Until)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(state, session.State) {
			t.Fatal("stored inputs do not reproduce final state")
		}
	}
}
func TestRejectFastForwardOwnershipAndStaleControls(t *testing.T) {
	f := newFixture(t)
	v := f.start("controls-game")
	v = f.control(v, "resume", 0, nil)
	command := Controls{Revision: v.Revision, Action: "advance", Until: 300}
	if _, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, command); !errors.Is(err, ErrInvalid) {
		t.Fatal("fast forward", err)
	}
	if _, err := f.s.Control(f.ctx, Identity{UserID: f.user + 1}, v.ID, command); !errors.Is(err, resources.ErrUnauthorized) {
		t.Fatal("ownership", err)
	}
	f.now.Add(5000)
	next := f.control(v, "advance", 300, nil)
	if _, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, command); err != nil {
		t.Fatal("exact retry", err)
	}
	command.Inputs = []engine.Input{{Tick: 1, Target: 0}}
	if _, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, command); !errors.Is(err, ErrConflict) {
		t.Fatal("stale changed retry", err)
	}
	paused := f.control(next, "pause", 300, nil)
	resumed := f.control(paused, "resume", 300, nil)
	if _, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, Controls{Revision: resumed.Revision, Action: "advance", Until: 301}); !errors.Is(err, ErrInvalid) {
		t.Fatal("pause/resume minted game time", err)
	}
	if _, err := f.db.Exec(`UPDATE site_config SET value='0' WHERE key='game_steadycatch_enabled'`); err != nil {
		t.Fatal(err)
	}
	end := f.control(resumed, "abandon", 300, nil)
	if end.Status != "abandoned" {
		t.Fatal(end.Status)
	}
	board, err := f.s.Leaderboard(f.ctx, f.user, "30d")
	if err != nil || len(board.Rows) != 0 {
		t.Fatal("abandoned score ranked", err)
	}
	if _, err = f.s.Start(f.ctx, f.user, "catch-test-key-disabled-game"); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled start", err)
	}
}
func TestRestartPauseDeleteRefundAndRollback(t *testing.T) {
	f := newFixture(t)
	v := f.start("recover-game")
	v = f.control(v, "resume", 0, nil)
	f.now.Add(5000)
	v = f.control(v, "advance", 300, nil)
	r, err := f.s.Module().RecoverBeforeListen(f.ctx, f.now.Load()/1000, 10, time.Now().Add(time.Second))
	if err != nil || r.Processed != 1 {
		t.Fatal(r, err)
	}
	current, err := f.s.Read(f.ctx, Identity{UserID: f.user})
	if err != nil || current.Status != "paused" || current.State.Tick != 300 {
		t.Fatal(current, err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.Module().PrepareDeleteTx(f.ctx, tx, f.user, f.now.Load()/1000); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		a, b := f.balances()
		if commit {
			if a != 100000 || b != 3000 {
				t.Fatalf("refund %d %d", a, b)
			}
		} else if a != 98000 || b != 0 {
			t.Fatal("rollback changed wallets")
		}
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM game_catch_sessions WHERE user_id=?`, f.user).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM game_catch_inputs`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestTerminalConcurrentRetryAndExpiry(t *testing.T) {
	f := newFixture(t)
	v := f.start("concurrent-game")
	c := Controls{Revision: v.Revision, Action: "abandon", Until: 0}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := f.s.Control(f.ctx, Identity{UserID: f.user}, v.ID, c); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a, b := f.balances(); a != 98000 || b != 0 {
		t.Fatalf("terminal charged twice %d %d", a, b)
	}
	if _, err := f.db.Exec(`UPDATE site_config SET value='0' WHERE key IN ('game_steadycatch_price_milli','game_steadycatch_first_reward_milli')`); err != nil {
		t.Fatal(err)
	}
	v = f.start("free-game")
	f.now.Add(sessionSeconds * 1000)
	r, err := f.s.work(f.ctx, f.now.Load()/1000, 10, time.Now().Add(time.Second), false)
	if err != nil || r.Processed != 1 {
		t.Fatal(r, err)
	}
	current, err := f.s.Read(f.ctx, Identity{UserID: f.user})
	if err != nil || current.Status != "abandoned" {
		t.Fatal(current, err)
	}
	f.now.Add(retentionSeconds * 1000)
	r, err = f.s.retain(f.ctx, f.now.Load()/1000, 10, time.Now().Add(time.Second))
	if err != nil || r.Processed != 2 {
		t.Fatal(r, err)
	}
}
