package blackjack_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type dependencies struct{ registry *maintenance.Registry }

func (d dependencies) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND is_admin=0 AND is_banned=0`, user).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return resources.ErrUnauthorized
	}
	return nil
}
func (dependencies) AuthorizeAdminMutation(context.Context, *sql.Tx) error { return nil }
func (d dependencies) AuthorizeContinuation(ctx context.Context, tx *sql.Tx, r maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	return d.registry.Authorize(ctx, tx, r)
}
func (dependencies) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	key := sha256.Sum256(info)
	return key[:], nil
}
func pool(ctx context.Context, tx *sql.Tx, kind string) (activities.PoolDestination, error) {
	var p activities.PoolDestination
	err := tx.QueryRowContext(ctx, `SELECT id,pool_type,account_id FROM shared_pools WHERE pool_type=? AND state='open' ORDER BY id LIMIT 1`, kind).Scan(&p.PoolID, &p.PoolType, &p.AccountID)
	return p, err
}
func (dependencies) WelfareDestination(ctx context.Context, tx *sql.Tx) (activities.PoolDestination, error) {
	return pool(ctx, tx, "welfare")
}
func (dependencies) ThursdayDestination(ctx context.Context, tx *sql.Tx, _ int64) (activities.PoolDestination, error) {
	return pool(ctx, tx, "thursday")
}
func (dependencies) RecordPoolTransfers(context.Context, *sql.Tx, int64, ...activities.PoolDestination) (activities.PublishFacts, error) {
	return activities.PublishFacts{Global: true}, nil
}
func (dependencies) Publish(context.Context, activities.PublishFacts) error { return nil }

type fixture struct {
	t       *testing.T
	ctx     context.Context
	db      *sql.DB
	s       *blackjack.Service
	clock   atomic.Int64
	users   []blackjack.Identity
	limiter *game.StartLimiter
}

func (f *fixture) id(prefix string) string {
	f.t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func newFixture(t *testing.T, count int) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background()}
	f.clock.Store(120)
	file := filepath.Join(t.TempDir(), "table.db")
	dbfixture.Materialize(t, file)
	var err error
	f.db, err = sql.Open("sqlite", file+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.exec(`UPDATE site_config SET value='1' WHERE key IN ('games_enabled','game_blackjack_enabled')`)
	f.exec(`UPDATE maintenance_state SET enabled=0 WHERE id=1`)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	for range count {
		r, err := tx.Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, f.id("bja_"), "player", zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		user, err := r.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(strconv.FormatInt(user, 10)))
		identity := blackjack.Identity{UserID: user, SessionBinding: hex.EncodeToString(hash[:])}
		f.users = append(f.users, identity)
		if _, err := tx.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES(?,?,100,3700,7300,100)`, identity.SessionBinding, user); err != nil {
			t.Fatal(err)
		}
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			wallet, err := ledger.CreateUserAssetAccount(f.ctx, tx, user, asset, 100)
			if err != nil {
				t.Fatal(err)
			}
			external, err := ledger.CodedAssetAccount(f.ctx, tx, "external", asset)
			if err != nil {
				t.Fatal(err)
			}
			meta := ledger.Meta{OperationID: f.id("op_"), ActorUserID: user, CreatedAt: 100}
			var plan ledger.Plan
			if asset == ledger.General {
				plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(100_000_000), 0, ledger.Amount{}, "funding")
			} else {
				plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(25_000_000), "funding")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Apply(f.ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.limiter, err = game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.limiter.Close() })
	f.s = f.service()
	return f
}
func (f *fixture) service() *blackjack.Service {
	f.t.Helper()
	capabilities, err := builtinfinance.ForModule(game.BlackjackID)
	if err != nil {
		f.t.Fatal(err)
	}
	registry := maintenance.NewRegistry()
	d := dependencies{registry: registry}
	s, err := blackjack.New(blackjack.Options{Database: f.db, Finance: capabilities.Blackjack, UserAuthorizer: d, AdminAuthorizer: d, Continuation: d, Limiter: f.limiter, Pools: d, Publisher: d, Keys: d, Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }, Random: rand.New(rand.NewSource(4))})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := registry.Register("blackjack_session", s.ContinuationRegistration()); err != nil {
		f.t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		f.t.Fatal(err)
	}
	if err := s.ValidatePersistedState(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	if _, err := s.RecoverBeforeListen(f.ctx, f.clock.Load(), 128, time.Now().Add(time.Minute)); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { s.Close() })
	return s
}
func (f *fixture) read(user int) blackjack.Home {
	f.t.Helper()
	v, err := f.s.Read(f.ctx, f.users[user])
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *fixture) join(user int) blackjack.QueueReceipt {
	f.t.Helper()
	state := f.read(user)
	r, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[user], Key: f.id("op_"), Stake: "5000", ConfigHash: state.ConfigHash})
	if err != nil {
		f.t.Fatal(err)
	}
	var receipt blackjack.QueueReceipt
	if json.Unmarshal(r.Body, &receipt) != nil {
		f.t.Fatal("invalid receipt")
	}
	return receipt
}
func (f *fixture) recovery() {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(f.ctx, tx); err != nil {
		f.t.Fatal(err)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func TestTableFIFOReplacementPersistentQueueAndFixedThirtySeconds(t *testing.T) {
	f := newFixture(t, 12)
	receipts := make([]blackjack.QueueReceipt, 11)
	for i := range receipts {
		receipts[i] = f.join(i)
	}
	h := f.read(11)
	if h.Table == nil || len(h.Table.Fact.Seats) != 9 || h.QueueCount != "2" || h.Deadline != 125 {
		t.Fatalf("table admission: %+v", h)
	}
	for i := 9; i < 11; i++ {
		v := f.read(i)
		if v.You == nil || v.You.Position != strconv.Itoa(i-8) {
			t.Fatal("FIFO rank")
		}
	}
	f.clock.Store(124)
	if _, err := f.s.Leave(f.ctx, f.users[2], f.id("op_"), receipts[2].ID); err != nil {
		t.Fatal(err)
	}
	if v := f.read(9); v.You == nil || v.You.State != "seated" || *v.You.Seat != 2 {
		t.Fatal("replacement missing")
	}
	f.clock.Store(125)
	h = f.read(0)
	if h.Table == nil || h.Table.Fact.Cards == nil || len(h.Table.Fact.Cards.Seats) != 9 {
		t.Fatal("table did not deal")
	}
	if r, err := f.s.Leave(f.ctx, f.users[0], f.id("op_"), receipts[0].ID); err != nil || r.Status != 409 {
		t.Fatal("late departure should not refund", r.Status, err)
	}
	f.clock.Store(145)
	h = f.read(11)
	if h.Phase != "result" || h.NextRoundAt != 150 || h.Table == nil || len(h.Table.Fact.Settlements) != 9 {
		t.Fatalf("deadline result: %+v", h)
	}
	if v := f.read(10); v.You == nil || v.You.State != "waiting" || v.You.Position != "1" {
		t.Fatal("waiter lost position")
	}
	f.join(0)
	if v := f.read(0); v.You.Position != "2" {
		t.Fatal("former player did not join tail")
	}
	f.clock.Store(150)
	h = f.read(10)
	if h.You.State != "seated" || *h.You.Seat != 0 || len(h.Table.Fact.Seats) != 2 || h.Deadline != 155 {
		t.Fatal("next round seat order")
	}
	f.recovery()
}

func TestConcurrentQueueAcceptanceAndDuplicateKeys(t *testing.T) {
	f := newFixture(t, 10)
	cfg := f.read(9).ConfigHash
	var wg sync.WaitGroup
	failures := make(chan error, 10)
	for i := range 10 {
		key := f.id("op_")
		wg.Add(1)
		go func(user int) {
			defer wg.Done()
			_, err := f.s.Enqueue(f.ctx, blackjack.EnqueueInput{Identity: f.users[user], Key: key, Stake: "5000", ConfigHash: cfg})
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	h := f.read(9)
	if len(h.Table.Fact.Seats) != 9 || h.QueueCount != "1" {
		t.Fatal("tenth player raced through table capacity")
	}
	before := f.read(0).You.ID
	key := f.id("op_")
	input := blackjack.EnqueueInput{Identity: f.users[0], Key: key, Stake: "5000", ConfigHash: cfg}
	first, err := f.s.Enqueue(f.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.Enqueue(f.ctx, input)
	if err != nil || !second.Replayed || string(first.Body) != string(second.Body) {
		t.Fatal("retry changed receipt", err)
	}
	if f.read(0).You.ID != before {
		t.Fatal("refresh duplicated reserve")
	}
	f.recovery()
}

func TestRestartCancelsDealtTableButPreservesWaiter(t *testing.T) {
	f := newFixture(t, 10)
	for i := range 10 {
		f.join(i)
	}
	waiter := f.read(9).You.ID
	f.clock.Store(126)
	h := f.read(0)
	if h.Phase != "decision" {
		t.Fatalf("expected reproducible unfinished fixture, got %s", h.Phase)
	}
	session := h.Table.ID
	f.s.Close()
	f.s = f.service()
	h = f.read(9)
	if h.You == nil || h.You.ID != waiter || h.You.State != "waiting" {
		t.Fatal("restart removed candidate")
	}
	var phase string
	if err := f.db.QueryRow(`SELECT phase FROM game_blackjack_sessions WHERE id=?`, session).Scan(&phase); err != nil || phase != "cancelled" {
		t.Fatal("restart did not cancel current table", err)
	}
	for _, identity := range f.users[:9] {
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		wallet, err := ledger.UserAssetAccount(f.ctx, tx, identity.UserID, ledger.Game)
		tx.Rollback()
		if err != nil || wallet.Balance.Big().Int64() != 25_000_000 {
			t.Fatal("refund changed source currency", err)
		}
	}
	f.recovery()
}

func TestActionBatchRevisionAndPrivateProjection(t *testing.T) {
	f := newFixture(t, 3)
	for i := range 3 {
		f.join(i)
	}
	f.clock.Store(125)
	h := f.read(0)
	if h.Phase != "decision" {
		t.Fatalf("expected reproducible unfinished fixture, got %s", h.Phase)
	}
	var actors []int
	for i := range 3 {
		v := f.read(i)
		if len(v.You.Legal["0"]) > 0 {
			actors = append(actors, i)
		}
	}
	if len(actors) < 2 {
		t.Fatal("fixture needs two active hands")
	}
	inputs := []blackjack.ActionInput{}
	for _, i := range actors {
		v := f.read(i)
		seat := *v.YourSeat
		inputs = append(inputs, blackjack.ActionInput{Identity: f.users[i], Key: f.id("op_"), SessionID: v.Table.ID, Hand: 0, Revision: strconv.FormatInt(v.Table.Fact.Cards.Seats[seat].Hands[0].Revision, 10), Action: "stand"})
	}
	for _, input := range inputs {
		r, err := f.s.Act(f.ctx, input)
		if err != nil || r.Status != 202 {
			t.Fatal("independent hand rejected", err)
		}
	}
	h = f.read(0)
	if h.Phase != "decision" || !h.Table.Fact.Cards.HoleHidden || len(h.Table.Fact.Cards.Dealer) != 1 {
		t.Fatal("same-batch intents leaked dealer")
	}
	dupe := inputs[0]
	dupe.Key = f.id("op_")
	if _, err := f.s.Act(f.ctx, dupe); !errors.Is(err, blackjack.ErrConflict) {
		t.Fatal("multiple actions accepted", err)
	}
	f.clock.Store(126)
	h = f.read(0)
	if h.Phase != "result" || h.Table.Fact.Cards.HoleHidden || h.NextRoundAt != 150 {
		t.Fatal("batch did not complete concurrently")
	}
	for _, input := range inputs {
		var seq int64
		if err := f.db.QueryRow(`SELECT activity_seq FROM user_activity_state WHERE user_id=?`, input.UserID).Scan(&seq); err != nil || seq != 2 {
			t.Fatalf("queue/manual/automatic activity=%d err=%v", seq, err)
		}
	}
	result, err := f.s.Act(f.ctx, inputs[0])
	if err != nil || !result.Replayed {
		t.Fatal("lost response cannot replay", err)
	}
	f.recovery()
}
