package duel_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	bidconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes"
	likeconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	likeengine "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type deps struct{}

type registryAuthorizer struct{ registry *maintenance.Registry }

func (a registryAuthorizer) AuthorizeContinuation(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	return a.registry.Authorize(ctx, tx, request)
}

func (deps) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
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
func (deps) AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	return maintenance.ContinuationSnapshot{}, errors.New("unexpected continuation")
}
func (deps) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	key := sha256.Sum256(info)
	return key[:], nil
}
func (deps) WelfareDestination(context.Context, *sql.Tx) (activities.PoolDestination, error) {
	return activities.PoolDestination{}, errors.New("unexpected pool")
}
func (deps) ThursdayDestination(context.Context, *sql.Tx, int64) (activities.PoolDestination, error) {
	return activities.PoolDestination{}, errors.New("unexpected pool")
}
func (deps) RecordPoolTransfers(context.Context, *sql.Tx, int64, ...activities.PoolDestination) (activities.PublishFacts, error) {
	return activities.PublishFacts{}, errors.New("unexpected pool")
}
func (deps) Publish(context.Context, activities.PublishFacts) error { return nil }

type fixture struct {
	t        *testing.T
	ctx      context.Context
	db       *sql.DB
	s        *duel.Service
	clock    atomic.Int64
	users    [2]int64
	rules    duel.Rules
	mode     string
	serial   int
	loadouts [2]json.RawMessage
	admin    *adminAuthorization
	audit    func(duel.AdminAudit)
}

func newFixture(t *testing.T, kind string) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background(), admin: &adminAuthorization{}}
	f.clock.Store(100)
	path := filepath.Join(t.TempDir(), "game.db")
	dbfixture.Materialize(t, path)
	var err error
	f.db, err = sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	descriptor := bidconfig.Descriptor()
	f.rules = bidding.Rules{}
	f.mode = "tier1"
	if kind == "likes" {
		descriptor = likeconfig.Descriptor()
		f.rules, err = likes.NewRules()
		if err != nil {
			t.Fatal(err)
		}
		f.mode = "quick"
		e, _ := likeengine.New(f.mode)
		c := e.Catalog()
		for seat := range 2 {
			for _, skill := range c.Skills {
				selection := likeengine.Selection{Role: c.Roles[seat].ID, Skills: []string{skill.ID}}
				if e.ValidateSelection(selection) == nil {
					f.loadouts[seat], _ = json.Marshal(selection)
					break
				}
			}
		}
	}
	exec := func(query string, args ...any) sql.Result {
		r, err := tx.ExecContext(f.ctx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('games_enabled',?,?)`, "game_"+kind+"_enabled", "game_"+kind+"_"+f.mode+"_enabled")
	exec(`UPDATE maintenance_state SET enabled=0 WHERE id=1`)
	exec(`UPDATE site_config SET value='5000' WHERE key=?`, "game_"+kind+"_"+f.mode+"_ticket_milli")
	for _, dest := range []string{"platform", "welfare", "thursday"} {
		exec(`UPDATE site_config SET value='0' WHERE key=?`, "game_"+kind+"_"+f.mode+"_rake_"+dest+"_bp")
	}
	zero := db.EncodeU128(db.U128{})
	for seat := range 2 {
		id, _ := db.GenerateOpaqueID("dah_")
		r := exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, "player", zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
		f.users[seat], err = r.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES(?,?,100,3700,7300,100)`, f.identity(seat).SessionBinding, f.users[seat])
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			wallet, err := ledger.CreateUserAssetAccount(f.ctx, tx, f.users[seat], asset, 100)
			if err != nil {
				t.Fatal(err)
			}
			external, err := ledger.CodedAssetAccount(f.ctx, tx, "external", asset)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := db.GenerateOpaqueID("op_")
			meta := ledger.Meta{OperationID: op, ActorUserID: f.users[seat], CreatedAt: 100}
			var plan ledger.Plan
			if asset == ledger.Game {
				plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(int64(3000+1000*seat)), "funding")
			} else {
				plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(20000), 0, ledger.Amount{}, "funding")
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
	limiter, err := game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { limiter.Close() })
	finance, err := builtinfinance.ForModule(kind)
	if err != nil {
		t.Fatal(err)
	}
	registry := maintenance.NewRegistry()
	f.s, err = duel.New(duel.Options{Database: f.db, Descriptor: descriptor, Rules: f.rules, Finance: finance.Duel, UserAuthorizer: deps{}, AdminAuthorizer: f.admin, AdminAudit: func(a duel.AdminAudit) {
		if f.audit != nil {
			f.audit(a)
		}
	}, Continuation: registryAuthorizer{registry}, Limiter: limiter, Pools: deps{}, Publisher: deps{}, Keys: deps{}, Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	if err := registry.Register(maintenance.ContinuationKind(kind+"_session"), f.s.ContinuationRegistration()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) identity(user int) duel.Identity {
	return duel.Identity{UserID: f.users[user], SessionBinding: fmt.Sprintf("participant-session-%d", user)}
}
func (f *fixture) key() string { f.serial++; return fmt.Sprintf("test-command-%022d", f.serial) }
func (f *fixture) enqueue(user, device int) duel.MutationResult {
	f.t.Helper()
	c, err := f.rules.Catalog(f.mode)
	if err != nil {
		f.t.Fatal(err)
	}
	terms := duel.Terms{Game: f.rules.ID(), Mode: f.mode, Ticket: "5", Rake: duel.Rates{}, RulesVersion: 1, ContentHash: c.Hash}
	body, _ := json.Marshal(terms)
	hash := sha256.Sum256(body)
	var raw [32]byte
	raw[0] = byte(device + 1)
	result, err := f.s.Enqueue(f.ctx, duel.EnqueueInput{Identity: f.identity(user), IdempotencyKey: f.key(), Mode: f.mode, ExpectedTermsHash: hex.EncodeToString(hash[:]), DeviceToken: base64.RawURLEncoding.EncodeToString(raw[:]), Loadout: f.loadouts[user]})
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *fixture) read(user int) duel.Home {
	f.t.Helper()
	home, err := f.s.Read(f.ctx, f.identity(user))
	if err != nil {
		f.t.Fatal(err)
	}
	return home
}
func (f *fixture) tick() {
	f.t.Helper()
	if _, err := f.s.Tick(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) matched() duel.State {
	f.t.Helper()
	f.enqueue(0, 0)
	f.enqueue(1, 1)
	f.tick()
	home := f.read(0)
	if home.Current == nil {
		f.t.Fatal("not matched", home)
	}
	return *home.Current
}
func (f *fixture) action(user int, state duel.State, body string) duel.MutationResult {
	f.t.Helper()
	result, err := f.s.Action(f.ctx, duel.ActionInput{Identity: f.identity(user), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq, Action: []byte(body)})
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *fixture) ledger() {
	f.t.Helper()
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(f.ctx, tx); err != nil {
		f.t.Fatal(err)
	}
}

func TestRealBiddingSamePhaseLocksAndDeadlineWins(t *testing.T) {
	f := newFixture(t, "bidding")
	state := f.matched()
	if state.Phase != "joker" {
		t.Fatal(state.Phase)
	}
	f.clock.Store(*state.Deadline)
	f.tick()
	state = *f.read(0).Current
	if state.Phase != "bid" || *state.Deadline != 130 {
		t.Fatal(state)
	}
	f.action(0, state, `{"kind":"bid","card":1}`)
	// The first lock changes revision but leaves the second player's phase valid.
	f.action(1, state, `{"kind":"bid","card":2}`)
	newState := *f.read(0).Current
	if newState.Round != 2 || newState.PhaseSeq == state.PhaseSeq {
		t.Fatal(newState)
	}
	f.clock.Store(*newState.Deadline)
	_, err := f.s.Action(f.ctx, duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: newState.ID, PhaseSeq: newState.PhaseSeq, Action: []byte(`{"kind":"joker","use":true}`)})
	if !errors.Is(err, duel.ErrConflict) {
		t.Fatal(err)
	}
	after := f.read(0).Current
	if after.Phase != "bid" || *after.Deadline != f.clock.Load()+20 {
		t.Fatal(after)
	}
	f.ledger()
}
func TestRealLikesSettlementDeadlineSurrenderAndReplay(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	body := `{"kind":"plan","plan":{"purchases":[],"main":{"skillId":"PUB01"},"extra":[]}}`
	f.action(0, state, body)
	f.action(1, state, body)
	settled := *f.read(0).Current
	if settled.Phase != "settlement" || settled.Resolution == nil || settled.Resolution.Round != 1 || *settled.Deadline != settled.Resolution.EndsAt || *settled.Deadline <= 105 {
		t.Fatal(settled)
	}
	end := *settled.Deadline
	f.clock.Store(end - 1)
	if f.read(1).Current.Phase != "settlement" {
		t.Fatal("shortened presentation")
	}
	f.clock.Store(end)
	next := *f.read(0).Current
	if next.Phase != "plan" || next.Round != 2 || *next.Deadline != end+20 {
		t.Fatal(next)
	}
	f.action(0, next, body)
	f.action(1, next, body)
	settled = *f.read(0).Current
	input := duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: settled.ID, PhaseSeq: settled.PhaseSeq}
	result, err := f.s.Surrender(f.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.Surrender(f.ctx, input)
	if err != nil || !replay.Replayed || string(result.Body) != string(replay.Body) {
		t.Fatal(replay, err)
	}
	home := f.read(0)
	if home.Current != nil || home.LatestResult == nil || home.LatestResult.Outcome != "loss" || home.LatestResult.TerminalAt != end {
		t.Fatal(home)
	}
	other := f.read(1)
	if other.LatestResult.Outcome != "win" || other.LatestResult.PrizeGeneral != "5" || other.LatestResult.OwnRefund.Game != "4" {
		t.Fatal(other.LatestResult)
	}
	f.ledger()
}
func TestSameDeviceWaitsAndRestartRefundsOnce(t *testing.T) {
	f := newFixture(t, "bidding")
	f.enqueue(0, 0)
	f.enqueue(1, 0)
	f.tick()
	if f.read(0).Queue == nil || f.read(1).Queue == nil {
		t.Fatal("same device matched")
	}
	for range 2 {
		if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if f.read(0).Queue != nil || f.read(1).Queue != nil {
		t.Fatal("queue survived restart")
	}
	f.ledger()
	state := f.matched()
	if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	home := f.read(0)
	if home.Current != nil || home.LatestResult.ID != state.ID || home.LatestResult.Outcome != "system_cancelled" || home.LatestResult.Reason != "server_restart" || home.LatestResult.OwnRefund.Game != "3" {
		t.Fatal(home)
	}
	f.ledger()
}

func TestLikesFinalPresentationDoesNotDelayTerminalLedger(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	for round := 0; round < 25; round++ {
		f.action(0, state, basicPlan)
		f.action(1, state, basicPlan)
		home := f.read(0)
		if home.Current == nil {
			result := home.LatestResult
			if result == nil || result.Resolution == nil || result.TerminalAt != f.clock.Load() || result.Resolution.EndsAt <= result.TerminalAt || result.Outcome != "draw" {
				t.Fatal("terminal facts delayed by presentation", result)
			}
			if f.read(1).LatestResult.TerminalAt != result.TerminalAt {
				t.Fatal("players diverged")
			}
			f.ledger()
			return
		}
		f.clock.Store(*home.Current.Deadline)
		state = *f.read(0).Current
	}
	t.Fatal("match never completed")
}
