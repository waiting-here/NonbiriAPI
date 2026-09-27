package fatfish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const fixtureNow = int64(1_800_000_000)

type testAuthority struct{}

func (testAuthority) AuthorizeUserMutation(_ context.Context, _ *sql.Tx, id int64) error {
	if id <= 0 {
		return ErrUnauthorized
	}
	return nil
}
func (testAuthority) AuthorizeAdmin(_ context.Context, _ *sql.Tx, id int64) error {
	if id <= 0 {
		return ErrUnauthorized
	}
	return nil
}

type testGate struct{}

func (testGate) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return nil }

type fishUserRoutes struct {
	routes map[string]limitedactivities.AuthorizedUserHandler
}

func (r *fishUserRoutes) RegisterUserRoute(method, path string, handler limitedactivities.AuthorizedUserHandler) error {
	if r.routes == nil {
		r.routes = make(map[string]limitedactivities.AuthorizedUserHandler)
	}
	r.routes[method+" "+path] = handler
	return nil
}

type fishAdminRoutes struct {
	routes map[string]limitedactivities.AuthorizedAdminHandler
}

func (r *fishAdminRoutes) RegisterAdminRoute(method, path string, handler limitedactivities.AuthorizedAdminHandler) error {
	if r.routes == nil {
		r.routes = make(map[string]limitedactivities.AuthorizedAdminHandler)
	}
	r.routes[method+" "+path] = handler
	return nil
}

type rejectingAuthority struct{ err error }

func (r rejectingAuthority) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error {
	return r.err
}
func (r rejectingAuthority) AuthorizeAdmin(context.Context, *sql.Tx, int64) error { return r.err }

type rejectingGate struct{ err error }

func (r rejectingGate) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return r.err }

type fishFixture struct {
	db           *sql.DB
	s            *Service
	config       Config
	clock        atomic.Int64
	user, admin  int64
	level        json.RawMessage
	terminalTick int
}

func fishKey(n int) string { return fmt.Sprintf("fatfish-idempotency-%08d", n) }
func fishCap(n byte) (string, string) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = n
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(raw), hex.EncodeToString(hash[:])
}

func newFishFixture(t *testing.T) *fishFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "fatfish.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fishFixture{db: store.DB()}
	f.clock.Store(fixtureNow * 1000)
	identity, err := continuity.New(f.db, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close() })
	f.config = Config{DB: f.db, Users: testAuthority{}, Admins: testAuthority{}, Gate: testGate{}, Identity: identity, Keys: vault, Now: func() time.Time { return time.UnixMilli(f.clock.Load()) }}
	f.s, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	zero := make([]byte, 16)
	seed := func(name string, admin int) int64 {
		result, err := f.db.Exec(`INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "fatfish-"+name, name, admin, 1, zero, zero, zero, zero, zero, zero, zero, zero, fixtureNow-100, fixtureNow-100)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.CreateUserAccount(context.Background(), tx, id, fixtureNow); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.admin = seed("admin", 1)
	f.user = seed("user", 0)
	_, err = f.db.Exec(`UPDATE limited_activity_configs SET visible=1,starts_at=?,ends_at=?,paused=0,revision=revision+1 WHERE activity_key='fat-fish'`, fixtureNow-100, fixtureNow+3600)
	if err != nil {
		t.Fatal(err)
	}
	f.fund(t, 10000)
	raw, err := os.ReadFile("../../web/src/shared/fatfish/engine/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Level  json.RawMessage `json:"level"`
			Result struct {
				TerminalTick int `json:"terminal_tick"`
			} `json:"result"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &suite); err != nil || len(suite.Cases) == 0 {
		t.Fatal(err)
	}
	f.level = suite.Cases[0].Level
	f.terminalTick = suite.Cases[0].Result.TerminalTick
	return f
}

func (f *fishFixture) fund(t *testing.T, milli int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	user, err := ledger.UserAccount(ctx, tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: f.admin, CreatedAt: fixtureNow}, user.ID, ext.ID, ledger.AmountFromMilli(milli), 0, ledger.Amount{}, "fixture funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func (f *fishFixture) balance(t *testing.T) string {
	t.Helper()
	tx, err := f.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	return account.Balance.Decimal()
}
func (f *fishFixture) tick(ms int64) { f.clock.Add(ms) }
func (f *fishFixture) waitState(t *testing.T, id, want string) ChallengeView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last ChallengeView
	for time.Now().Before(deadline) {
		v, err := f.s.Challenge(context.Background(), f.user, id, "")
		if err != nil {
			t.Fatal(err)
		}
		last = v
		if v.State == want {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("challenge %s did not reach %s: state=%s result=%+v settlement_error=%v", id, want, last.State, last.Result, f.s.settleVerified(context.Background(), id))
	return ChallengeView{}
}

func (f *fishFixture) publishOne(t *testing.T) (string, string) {
	return f.publishWithAmounts(t, Amounts{UnlockCost: "1.5", TicketPrice: "2", FirstClearReward: "3", StarRewards: [3]string{"1", "2", "3"}})
}
func (f *fishFixture) publishWithAmounts(t *testing.T, amounts Amounts) (string, string) {
	t.Helper()
	ctx := context.Background()
	l, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Test level", Draft: f.level}, fishKey(1))
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.s.PublishVersion(ctx, f.admin, l.ID, l.Revision, fishKey(2))
	if err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(1)
	p, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: v.ID, TabCapabilityHash: hash}, fishKey(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.StartPlaytest(ctx, f.admin, p.ID, StartInput{TabCapability: cap}, fishKey(4)); err != nil {
		t.Fatal(err)
	}
	f.tick(3200)
	if _, err = f.s.SubmitPlaytest(ctx, f.admin, p.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(5)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, err := f.s.PlaytestChallenge(ctx, f.admin, p.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if v.State == "settled_pass" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	period, err := f.s.SavePeriod(ctx, f.admin, "", PeriodInput{Title: "Season", Visible: true, StartsAt: fixtureNow - 10, EndsAt: fixtureNow + 1000}, fishKey(6))
	if err != nil {
		t.Fatal(err)
	}
	node, err := f.s.SaveNode(ctx, f.admin, period.ID, "", NodeInput{Title: "First", VersionID: v.ID, Condition: json.RawMessage(`{}`), Amounts: amounts, ExpectedPeriodRevision: period.Revision}, fishKey(7))
	if err != nil {
		t.Fatal(err)
	}
	period, err = f.s.AdminPeriod(ctx, f.admin, period.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ChangePeriodState(ctx, f.admin, period.ID, "publish", period.Revision, fishKey(8)); err != nil {
		t.Fatal(err)
	}
	return period.ID, node.ID
}

func TestPaidChallengeVerifiedRewardsAndReplay(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 1 {
		t.Fatal("missing node")
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(9)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("balance after unlock=%s", got)
	}
	cap, hash := fishCap(2)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(10))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Seed != "" {
		t.Fatal("prepare exposed seed")
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(11))
	if err != nil {
		t.Fatal(err)
	}
	if started.Seed == "" || len(started.Level) == 0 {
		t.Fatal("start omitted recovery material")
	}
	replayed, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(11))
	if err != nil || replayed.Seed != started.Seed || replayed.StartAtMS == nil || *replayed.StartAtMS != *started.StartAtMS {
		t.Fatal("start replay changed ticket", err)
	}
	if got := f.balance(t); got != "6500" {
		t.Fatalf("balance after ticket=%s", got)
	}
	f.tick(3200)
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(12)); err != nil {
		t.Fatal(err)
	}
	terminal := f.waitState(t, prepared.ID, "settled_pass")
	if terminal.Result == nil || !terminal.Result.Passed || terminal.Result.Stars != 3 || terminal.Result.Rewards != "9" {
		t.Fatalf("terminal=%+v", terminal.Result)
	}
	if got := f.balance(t); got != "15500" {
		t.Fatalf("balance after rewards=%s", got)
	}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(13)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got != "15500" {
		t.Fatalf("duplicate submit paid twice: %s", got)
	}
	board, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20)
	if err != nil || board.Total != 1 || len(board.Rows) != 1 || board.Rows[0].Identity.Kind != "anonymous" {
		t.Fatal("board privacy", board, err)
	}
}

func TestSourceAwareDeleteRefundVsSelfAndGuard(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(20)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(7)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(21))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(22)); err != nil {
		t.Fatal(err)
	}
	job, already, err := f.s.reserveJob(prepared.ID, []byte(`[]`), f.terminalTick)
	if err != nil || already {
		t.Fatalf("reserve test job: %v %v", err, already)
	}
	before := f.balance(t)
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	final, err := f.s.LifecycleAdapter().PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: f.user, DecisionNow: f.clock.Load() / 1000, Source: lifecycle.DeleteAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ActivityRuntime().PrepareDeleteTx(ctx, tx, f.user, f.clock.Load()/1000); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	final.Abort()
	if got := f.balance(t); got != before {
		t.Fatal("rollback changed balance", got, before)
	}
	if f.s.jobs[prepared.ID] != job || len(job.inputs) == 0 {
		t.Fatal("rollback discarded the in-flight input")
	}
	tx, err = f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	final, err = f.s.LifecycleAdapter().PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: f.user, DecisionNow: f.clock.Load() / 1000, Source: lifecycle.DeleteAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ActivityRuntime().PrepareDeleteTx(ctx, tx, f.user, f.clock.Load()/1000); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	final.Commit()
	if f.s.jobs[prepared.ID] != nil || f.s.jobBytes != 0 || len(job.inputs) != 0 {
		t.Fatal("committed deletion retained the in-flight input")
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("admin delete refund=%s", got)
	}
	v, err := f.s.Challenge(ctx, f.user, prepared.ID, "")
	if err != nil || v.State != "cancelled_refunded" {
		t.Fatal("admin cancel state", v, err)
	}
}

func TestSelfDeleteAbandonsWithoutTicketRefund(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(70)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(71)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(71))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(72)); err != nil {
		t.Fatal(err)
	}
	before := f.balance(t)
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	final, err := f.s.LifecycleAdapter().PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: f.user, DecisionNow: f.clock.Load() / 1000, Source: lifecycle.DeleteSelf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ActivityRuntime().PrepareDeleteTx(ctx, tx, f.user, f.clock.Load()/1000); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	final.Commit()
	if got := f.balance(t); got != before {
		t.Fatalf("self-delete refunded the ticket: %s instead of %s", got, before)
	}
	v, err := f.s.Challenge(ctx, f.user, prepared.ID, "")
	if err != nil || v.State != "abandoned" {
		t.Fatalf("self-delete state %+v %v", v, err)
	}
}

func TestTerminalTimeBoundaryAndThirtyMinuteReceipt(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(30)); err != nil {
		t.Fatal(err)
	}
	for index, delta := range []int64{-1, 0, 1} {
		cap, hash := fishCap(byte(20 + index))
		prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(31+index*3))
		if err != nil {
			t.Fatal(err)
		}
		started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(32+index*3))
		if err != nil {
			t.Fatal(err)
		}
		at := *started.StartAtMS + int64(f.terminalTick)*1000/60 + delta
		f.tick(at - f.clock.Load())
		if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(33+index*3)); err != nil {
			t.Fatal(err)
		}
		want := "settled_pass"
		if delta < 0 {
			want = "settled_fail"
		}
		result := f.waitState(t, prepared.ID, want)
		if delta < 0 && result.Result.Reason != "premature_terminal" {
			t.Fatalf("before boundary reason=%q", result.Result.Reason)
		}
		if delta >= 0 && (!result.Result.Passed || result.Result.TerminalTick != f.terminalTick) {
			t.Fatalf("at/after boundary result=%+v", result.Result)
		}
	}
	cap, hash := fishCap(25)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(41))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(42))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.SubmitUntilMS - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(43)); err != nil {
		t.Fatal("exact submit_until must be accepted", err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
}

func TestSubmitAfterThirtyMinuteDeadlineDoesNotExtend(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(50)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(26)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(51))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(52))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.SubmitUntilMS + 1 - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(53)); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-deadline submit=%v", err)
	}
	var digestCount int
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_challenges WHERE id=? AND input_digest IS NOT NULL`, prepared.ID).Scan(&digestCount); err != nil || digestCount != 0 {
		t.Fatalf("late input accepted: %d %v", digestCount, err)
	}
}

func TestHiddenPeriodDirectAccessAndExistingEntry(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`UPDATE fatfish_periods SET visible=0 WHERE id=?`, period); err != nil {
		t.Fatal(err)
	}
	listed, err := f.s.ListPeriods(ctx, f.user, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		if item.ID == period {
			t.Fatal("hidden period appeared in directory")
		}
	}
	detail, err := f.s.Period(ctx, f.user, period)
	if err != nil || len(detail.Nodes) != 1 {
		t.Fatalf("hidden direct detail: %+v %v", detail, err)
	}
	if _, err = f.s.Node(ctx, f.user, period, node); err != nil {
		t.Fatalf("hidden direct node: %v", err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: detail.Nodes[0].Revision}, fishKey(61)); err != nil {
		t.Fatalf("hidden direct unlock: %v", err)
	}
	cap, hash := fishCap(31)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: detail.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(62))
	if err != nil {
		t.Fatalf("hidden direct prepare: %v", err)
	}
	if _, err = f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(63)); err != nil {
		t.Fatalf("hidden direct start: %v", err)
	}
}

func TestZeroEconomyPermanentClaimsAndRepeat(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	initial := f.balance(t)
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(81)); err != nil {
		t.Fatal(err)
	}
	var unlockOps int
	if err = f.db.QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='fatfish_unlock'`).Scan(&unlockOps); err != nil || unlockOps != 1 {
		t.Fatalf("zero unlock operation count=%d %v", unlockOps, err)
	}
	for i := 0; i < 2; i++ {
		cap, hash := fishCap(byte(81 + i))
		prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(82+i*3))
		if err != nil {
			t.Fatal(err)
		}
		started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(83+i*3))
		if err != nil {
			t.Fatal(err)
		}
		f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
		if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(84+i*3)); err != nil {
			t.Fatal(err)
		}
		result := f.waitState(t, prepared.ID, "settled_pass")
		if result.Result == nil || result.Result.Rewards != "0" {
			t.Fatalf("zero result %+v", result.Result)
		}
	}
	if got := f.balance(t); got != initial {
		t.Fatalf("zero economy changed balance %s != %s", got, initial)
	}
	var claims, financial int
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_reward_claims WHERE period_id=? AND node_id=? AND operation_id IS NULL`, period, node).Scan(&claims); err != nil || claims != 4 {
		t.Fatalf("zero claims=%d %v", claims, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_financial_receipts`).Scan(&financial); err != nil || financial != 0 {
		t.Fatalf("zero financial receipts=%d %v", financial, err)
	}
}

func TestHiddenNodeProjectsOnlyAfterEligibility(t *testing.T) {
	f := newFishFixture(t)
	period, first := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	condition := json.RawMessage(fmt.Sprintf(`{"passed":%q}`, first))
	hidden, err := f.s.SaveNode(ctx, f.admin, period, "", NodeInput{Title: "Secret objective", Description: "Private description", VersionID: p.Nodes[0].VersionID, Condition: condition, HiddenUntilEligible: true, Amounts: Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}, ExpectedPeriodRevision: p.Revision}, fishKey(91))
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.s.Node(ctx, f.user, period, hidden.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != "" || before.Description != "" || before.VersionID != "" || before.ContentHash != "" || before.Amounts != nil || len(before.Level) != 0 || len(before.Condition) != 0 || before.Revision != "" {
		t.Fatalf("hidden node leaked details: %+v", before)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, first, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(92)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(92)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: first, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(93))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(94))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(95)); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
	after, err := f.s.Node(ctx, f.user, period, hidden.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Secret objective" || after.Amounts == nil || len(after.Level) == 0 || !after.Eligible {
		t.Fatalf("eligible node not revealed: %+v", after)
	}
}

func TestPrerequisiteLeastFixedPoint(t *testing.T) {
	a, _ := db.GenerateOpaqueID("ffn_")
	b, _ := db.GenerateOpaqueID("ffn_")
	c, _ := db.GenerateOpaqueID("ffn_")
	parse := func(raw string) Condition {
		t.Helper()
		v, err := ParseCondition([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	entry := map[string]Condition{a: parse(fmt.Sprintf(`{"any":[{"passed":%q},{"passed":%q}]}`, b, c)), b: parse(fmt.Sprintf(`{"passed":%q}`, a)), c: parse(`{}`)}
	if reached, err := ReachableNodes(entry, map[string]int{a: 3, b: 3, c: 3}); err != nil || len(reached) != 3 {
		t.Fatalf("entry cycle rejected: %v %v", reached, err)
	}
	sealed := map[string]Condition{a: parse(fmt.Sprintf(`{"passed":%q}`, b)), b: parse(fmt.Sprintf(`{"passed":%q}`, a))}
	if _, err := ReachableNodes(sealed, map[string]int{a: 3, b: 3}); !errors.Is(err, ErrConflict) {
		t.Fatalf("sealed cycle accepted: %v", err)
	}
	for _, raw := range []string{`{"all":[]}`, `{"any":[]}`, `{"stars":{"node":"bad","min":1}}`, fmt.Sprintf(`{"stars":{"node":%q,"min":4}}`, a)} {
		if _, err := ParseCondition([]byte(raw)); err == nil {
			t.Fatalf("invalid prerequisite accepted: %s", raw)
		}
	}
}

func TestTicketRevisionLocksOnlyFutureStarts(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	old := p.Nodes[0]
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: old.Revision}, fishKey(101)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(101)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: old.Revision, TabCapabilityHash: hash}, fishKey(102))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(103))
	if err != nil || started.TicketPrice != "2" {
		t.Fatalf("old ticket %+v %v", started, err)
	}
	updated, err := f.s.SaveNode(ctx, f.admin, period, node, NodeInput{Title: old.Title, Description: old.Description, VersionID: old.VersionID, Condition: json.RawMessage(`{}`), Amounts: Amounts{UnlockCost: "8", TicketPrice: "5", FirstClearReward: "3", StarRewards: [3]string{"1", "2", "3"}}, ExpectedRevision: old.Revision, ExpectedPeriodRevision: p.Revision}, fishKey(104))
	if err != nil {
		t.Fatal(err)
	}
	prior, err := f.s.Challenge(ctx, f.user, prepared.ID, cap)
	if err != nil || prior.TicketPrice != "2" {
		t.Fatalf("old receipt repriced %+v %v", prior, err)
	}
	if _, err = f.s.Abandon(ctx, f.user, prepared.ID, AbandonInput{}, fishKey(105)); err != nil {
		t.Fatal(err)
	}
	cap2, hash2 := fishCap(102)
	next, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: updated.Revision, TabCapabilityHash: hash2}, fishKey(106))
	if err != nil {
		t.Fatal(err)
	}
	started2, err := f.s.Start(ctx, f.user, next.ID, StartInput{TabCapability: cap2}, fishKey(107))
	if err != nil || started2.TicketPrice != "5" {
		t.Fatalf("new ticket %+v %v", started2, err)
	}
	if got := f.balance(t); got != "1500" {
		t.Fatalf("revised cost/backcharge balance=%s", got)
	}
}

func TestConcurrentStartChargesOneTicket(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(111)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(111)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(112))
	if err != nil {
		t.Fatal(err)
	}
	var views [2]ChallengeView
	var errs [2]error
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			views[i], errs[i] = f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(113+i))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if views[0].Seed == "" || views[0].Seed != views[1].Seed || *views[0].StartAtMS != *views[1].StartAtMS {
		t.Fatalf("concurrent starts diverged %+v %+v", views[0], views[1])
	}
	if got := f.balance(t); got != "6500" {
		t.Fatalf("double ticket charged: %s", got)
	}
	var ops int
	if err = f.db.QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='fatfish_ticket'`).Scan(&ops); err != nil || ops != 1 {
		t.Fatalf("ticket ops=%d %v", ops, err)
	}
}

func TestAcceptedDigestRecoversAfterWorkerLoss(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(121)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(121)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(122))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(123))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.workerSlots <- struct{}{}
	f.s.workerSlots <- struct{}{}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(124)); err != nil {
		t.Fatal(err)
	}
	f.s.Close()
	var state string
	var digest []byte
	if err = f.db.QueryRow(`SELECT state,input_digest FROM fatfish_challenges WHERE id=?`, prepared.ID).Scan(&state, &digest); err != nil || state != "verifying" || len(digest) != 32 {
		t.Fatalf("lost accepted receipt %q %d %v", state, len(digest), err)
	}
	f.s, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[{}]`), TerminalTick: f.terminalTick}, fishKey(125)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different recovery payload: %v", err)
	}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(126)); err != nil {
		t.Fatal(err)
	}
	result := f.waitState(t, prepared.ID, "settled_pass")
	if result.Result == nil || result.Result.Rewards != "9" {
		t.Fatalf("recovered result %+v", result.Result)
	}
}

func TestRunningVerificationShutdownPreservesDigest(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(1261)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(126)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(1262))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(1263))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	entered := make(chan struct{})
	f.s.replay = func(_ engine.Level, _ [32]byte, _ []engine.InputTuple, options engine.ReplayOptions) (engine.ReplayResult, error) {
		close(entered)
		<-options.Context.Done()
		return engine.ReplayResult{}, options.Context.Err()
	}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(1264)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("verification did not enter running replay")
	}
	f.s.Close()
	var state string
	var digest []byte
	if err = f.db.QueryRow(`SELECT state,input_digest FROM fatfish_challenges WHERE id=?`, prepared.ID).Scan(&state, &digest); err != nil || state != "verifying" || len(digest) != 32 {
		t.Fatalf("normal close refunded or lost digest %s %d %v", state, len(digest), err)
	}
	if got := f.balance(t); got != "6500" {
		t.Fatalf("normal close refunded ticket: %s", got)
	}
	f.s, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(1265)); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
}

func TestVerificationHardTimeoutRefundsTicket(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(1271)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(127)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(1272))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(1273))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.replay = func(_ engine.Level, _ [32]byte, _ []engine.InputTuple, options engine.ReplayOptions) (engine.ReplayResult, error) {
		<-options.Context.Done()
		return engine.ReplayResult{}, options.Context.Err()
	}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(1274)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, readErr := f.s.Challenge(ctx, f.user, prepared.ID, "")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if v.State == "cancelled_refunded" {
			if got := f.balance(t); got != "8500" {
				t.Fatalf("timeout refund balance=%s", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("verification timeout did not refund")
}

func TestExpiredBanFlagAllowsVerifiedSettlement(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(1281)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(128)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(1282))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(1283))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, fixtureNow-1, f.user); err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(1284)); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
	if got := f.balance(t); got != "15500" {
		t.Fatalf("expired flag incorrectly refunded: %s", got)
	}
}

func TestNodeTieUsesChallengeIDWhilePeriodTieUsesPublicKey(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(1291)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(129)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(1292))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(1293))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(1294)); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
	var score, at int64
	var originalID string
	var originalTie []byte
	if err = f.db.QueryRow(`SELECT best_score_units,best_at_ms,best_challenge_id FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, f.user, period, node).Scan(&score, &at, &originalID); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT public_tie_key FROM fatfish_period_progress WHERE user_id=? AND period_id=?`, f.user, period).Scan(&originalTie); err != nil {
		t.Fatal(err)
	}
	low := "ffc_" + strings.Repeat("-", 21) + "A"
	high := "ffc_" + strings.Repeat("z", 21) + "w"
	secondID := low
	secondTie := bytes.Repeat([]byte{0xff}, 16)
	if !(secondID < originalID && bytes.Compare(secondTie, originalTie) > 0) {
		secondID = high
		secondTie = make([]byte, 16)
	}
	if secondID == originalID || ((secondID < originalID) == (bytes.Compare(secondTie, originalTie) < 0)) {
		t.Fatal("could not force opposite tie orders")
	}
	zero := make([]byte, 16)
	res, err := f.db.Exec(`INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "fatfish-tie-peer", "peer", 0, 1, zero, zero, zero, zero, zero, zero, zero, zero, fixtureNow, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO fatfish_progress(user_id,period_id,node_id,unlocked_at,passed,best_stars,best_score_units,best_version_id,best_at_ms,best_challenge_id,best_confirmed_at_ms) VALUES(?,?,?,?,1,3,?,?,?,?,?)`, peer, period, node, fixtureNow, score, p.Nodes[0].VersionID, at, secondID, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO fatfish_period_progress(user_id,period_id,total_score_units,achieved_at_ms,public_tie_key) VALUES(?,?,?,?,?)`, peer, period, score, f.clock.Load(), secondTie); err != nil {
		t.Fatal(err)
	}
	nodeBoard, err := f.s.Leaderboard(ctx, f.user, period, node, 1, 20)
	if err != nil || len(nodeBoard.Rows) != 2 {
		t.Fatalf("node board %+v %v", nodeBoard, err)
	}
	periodBoard, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20)
	if err != nil || len(periodBoard.Rows) != 2 {
		t.Fatalf("period board %+v %v", periodBoard, err)
	}
	wantSelfFirst := originalID < secondID
	if nodeBoard.Rows[0].IsMe != wantSelfFirst || periodBoard.Rows[0].IsMe == wantSelfFirst {
		t.Fatalf("tie rules mismatched node=%+v period=%+v", nodeBoard.Rows, periodBoard.Rows)
	}
}

func TestLargeValidLevelAndOverlimitRejectedOnSave(t *testing.T) {
	f := newFishFixture(t)
	ctx := context.Background()
	level, err := engine.ParseLevel(f.level)
	if err != nil {
		t.Fatal(err)
	}
	shape := engine.Polygon{Outer: []engine.Point{{X: 0, Y: 0}, {X: 256, Y: 0}, {X: 256, Y: 256}, {X: 0, Y: 256}}}
	for i := 0; i < 24; i++ {
		level.Tools = append(level.Tools, engine.Tool{ID: 100 + i, ResourceKey: "barrier", Polygon: shape})
	}
	valid, err := engine.NormalizedLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(valid), ":") <= 256 {
		t.Fatalf("fixture too small to exercise field budget: %d", strings.Count(string(valid), ":"))
	}
	if _, err = f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Full inventory", Draft: valid}, fishKey(131)); err != nil {
		t.Fatalf("valid bounded level rejected: %v", err)
	}
	level.Tools = append(level.Tools, engine.Tool{ID: 124, ResourceKey: "barrier", Polygon: shape})
	invalid, err := json.Marshal(level)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Too many objects", Draft: invalid}, fishKey(132)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlimit level persisted: %v", err)
	}
}

func TestLostVerificationRefundAfterRecoveryCutoff(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(141)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(141)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(142))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(143))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.workerSlots <- struct{}{}
	f.s.workerSlots <- struct{}{}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(144)); err != nil {
		t.Fatal(err)
	}
	f.s.Close()
	f.s, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.Close)
	f.tick(601000)
	work, err := f.s.recover(ctx, f.clock.Load()/1000, 100, time.Now().Add(time.Second))
	if err != nil || work.Processed < 1 {
		t.Fatalf("recovery %v %+v", err, work)
	}
	v, err := f.s.Challenge(ctx, f.user, prepared.ID, "")
	if err != nil || v.State != "cancelled_refunded" {
		t.Fatalf("unrecoverable state %+v %v", v, err)
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("refund balance=%s", got)
	}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(145)); err != nil {
		t.Fatalf("terminal same digest receipt: %v", err)
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("post refund replay changed balance=%s", got)
	}
}

func TestRetentionKeepsProgressAndClaimFacts(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(151)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(151)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(152))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(153))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(154)); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
	f.tick(int64(31 * 24 * time.Hour / time.Millisecond))
	work, err := f.s.retain(ctx, f.clock.Load()/1000, 100, time.Now().Add(time.Second))
	if err != nil || work.Processed < 1 {
		t.Fatalf("retention %v %+v", err, work)
	}
	if _, err = f.s.Challenge(ctx, f.user, prepared.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired summary remains: %v", err)
	}
	var stars, claims int
	if err = f.db.QueryRow(`SELECT best_stars FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, f.user, period, node).Scan(&stars); err != nil || stars != 3 {
		t.Fatalf("long-lived best %d %v", stars, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_reward_claims WHERE period_id=? AND node_id=?`, period, node).Scan(&claims); err != nil || claims != 4 {
		t.Fatalf("long-lived claims %d %v", claims, err)
	}
}

func TestAdminDeleteCancelsPlaytestInputAfterCommit(t *testing.T) {
	f := newFishFixture(t)
	period, _ := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(161)
	prepared, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: hash}, fishKey(161))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.StartPlaytest(ctx, f.admin, prepared.ID, StartInput{TabCapability: cap}, fishKey(162)); err != nil {
		t.Fatal(err)
	}
	job, _, err := f.s.reserveJob(prepared.ID, []byte(`[]`), f.terminalTick)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	final, err := f.s.LifecycleAdapter().PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: f.admin, DecisionNow: f.clock.Load() / 1000, Source: lifecycle.DeleteAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ActivityRuntime().PrepareDeleteTx(ctx, tx, f.admin, f.clock.Load()/1000); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	final.Commit()
	if len(job.inputs) != 0 || f.s.jobBytes != 0 {
		t.Fatal("deleted admin retained playtest input")
	}
	v, err := f.s.PlaytestChallenge(ctx, f.admin, prepared.ID, "")
	if err != nil || v.State != "abandoned" {
		t.Fatalf("playtest delete state %+v %v", v, err)
	}
}

func TestBanCancellationAndVerificationCannotBothSettle(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(166)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(166)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(167))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(168))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.workerSlots <- struct{}{}
	f.s.workerSlots <- struct{}{}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(169)); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	final, err := f.s.CancelUserTx(ctx, tx, f.user, f.clock.Load()/1000, "account_banned")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, f.user); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	final.Commit()
	var state string
	var claims int
	if err = f.db.QueryRow(`SELECT state FROM fatfish_challenges WHERE id=?`, prepared.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_reward_claims WHERE period_id=? AND node_id=?`, period, node).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled_refunded" || claims != 0 || f.s.jobBytes != 0 {
		t.Fatalf("ban/verify race state=%s claims=%d bytes=%d", state, claims, f.s.jobBytes)
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("ban refund balance=%s", got)
	}
}

func TestVerifiedFactCancelledBeforeSettlementProjectsRefundOnly(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(171)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(171)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(172))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(173))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.workerSlots <- struct{}{}
	f.s.workerSlots <- struct{}{}
	if _, err = f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: cap, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(174)); err != nil {
		t.Fatal(err)
	}
	readTx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := readChallengeTx(ctx, readTx, prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = readTx.Commit(); err != nil {
		t.Fatal(err)
	}
	level, err := engine.ParseLevel([]byte(c.levelJSON.String))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := engine.ParseInputs([]byte(`[]`), level)
	if err != nil {
		t.Fatal(err)
	}
	var seed [32]byte
	copy(seed[:], c.seed)
	replay, err := engine.Replay(level, seed, inputs, engine.ReplayOptions{Context: ctx})
	if err != nil || !replay.Passed {
		t.Fatalf("fixture replay %+v %v", replay, err)
	}
	if err = f.s.recordVerificationFact(ctx, c, replay, 1); err != nil {
		t.Fatal(err)
	}
	pending, err := f.s.Challenge(ctx, f.user, prepared.ID, "")
	if err != nil || pending.State != "verifying" || pending.Result != nil {
		t.Fatalf("unsettled fact exposed success %+v %v", pending, err)
	}
	if _, err = f.s.AdminCancelChallenge(ctx, f.admin, prepared.ID, CancelInput{Reason: "operator_cancelled"}, fishKey(175)); err != nil {
		t.Fatal(err)
	}
	v, err := f.s.Challenge(ctx, f.user, prepared.ID, "")
	if err != nil || v.Result == nil || v.Result.Passed || v.Result.Stars != 0 || v.Result.Rewards != "0" || v.Result.TicketRefund != "2" {
		t.Fatalf("cancelled fact public view %+v %v", v, err)
	}
	history, err := f.s.History(ctx, f.user, 20, 1)
	if err != nil || len(history.Items) != 1 || history.Items[0].Result == nil || history.Items[0].Result.Passed || history.Items[0].Result.Rewards != "0" {
		t.Fatalf("cancelled fact history %+v %v", history, err)
	}
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.LifecycleAdapter().ExportFatFish(ctx, tx, lifecycle.ExportRequest{UserID: f.user, DecisionNow: f.clock.Load() / 1000, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(exported.Summaries) != 1 || exported.Summaries[0].Passed || exported.Summaries[0].Rewards != "0" || exported.Summaries[0].TicketRefund != "2" {
		t.Fatalf("cancelled fact export %+v", exported)
	}
	if got := f.balance(t); got != "8500" {
		t.Fatalf("cancel refund balance=%s", got)
	}
	var facts, claims, passed int
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_challenges WHERE id=? AND verified_result_json IS NOT NULL`, prepared.ID).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM fatfish_reward_claims WHERE period_id=? AND node_id=?`, period, node).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT passed FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, f.user, period, node).Scan(&passed); err != nil {
		t.Fatal(err)
	}
	if facts != 1 || claims != 0 || passed != 0 {
		t.Fatalf("fact/claims/progress %d/%d/%d", facts, claims, passed)
	}
}

func TestHistoryPagesReachEveryTerminalChallengeAndReflectRetention(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(2000)); err != nil {
		t.Fatal(err)
	}
	created := make(map[string]bool)
	for i := 0; i < 45; i++ {
		_, hash := fishCap(byte(200 + i))
		prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(2001+i*2))
		if err != nil {
			t.Fatalf("prepare %d: %v", i, err)
		}
		terminal, err := f.s.Abandon(ctx, f.user, prepared.ID, AbandonInput{}, fishKey(2002+i*2))
		if err != nil || terminal.State != "abandoned" {
			t.Fatalf("abandon %d: %+v %v", i, terminal, err)
		}
		created[prepared.ID] = true
	}
	seen := make(map[string]bool)
	for page := 1; page <= 4; page++ {
		result, err := f.s.History(ctx, f.user, 20, page)
		if err != nil {
			t.Fatal(err)
		}
		want := 20
		if page == 3 {
			want = 5
		} else if page == 4 {
			want = 0
		}
		if len(result.Items) != want || result.Page != page || result.PageSize != 20 || result.HasMore != (page < 3) {
			t.Fatalf("history page %d: %+v", page, result)
		}
		for _, item := range result.Items {
			if !created[item.ID] || seen[item.ID] || item.State != "abandoned" {
				t.Fatalf("missing/duplicate/unexpected item on page %d: %+v", page, item)
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 45 {
		t.Fatalf("history reached %d of 45 rows", len(seen))
	}
	if _, err := f.s.History(ctx, f.user, 20, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("page zero: %v", err)
	}
	if _, err := f.s.History(ctx, f.user, 20, 1000001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded page: %v", err)
	}
	routes := &fishUserRoutes{}
	if err := RegisterUserRoutes(routes, f.s); err != nil {
		t.Fatal(err)
	}
	handler := routes.routes[http.MethodGet+" "+userPrefix+"/history"]
	if handler == nil {
		t.Fatal("history route missing")
	}
	for target, status := range map[string]int{
		userPrefix + "/history?limit=20&page=2":        http.StatusOK,
		userPrefix + "/history?limit=20&page=0":        http.StatusBadRequest,
		userPrefix + "/history?limit=20&page=2&page=3": http.StatusBadRequest,
		userPrefix + "/history?unexpected=1":           http.StatusBadRequest,
	} {
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodGet, target, nil), limitedactivities.UserPrincipal{UserID: f.user})
		if recorder.Code != status {
			t.Fatalf("GET %s returned %d, want %d: %s", target, recorder.Code, status, recorder.Body.String())
		}
		if status == http.StatusOK {
			var result HistoryPage
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || len(result.Items) != 20 || result.Page != 2 || !result.HasMore {
				t.Fatalf("GET history page 2: %+v %v", result, err)
			}
		}
	}
	var removed string
	for id := range created {
		removed = id
		break
	}
	if _, err := f.db.Exec(`DELETE FROM fatfish_challenges WHERE id=?`, removed); err != nil {
		t.Fatal(err)
	}
	seen = make(map[string]bool)
	for page := 1; page <= 3; page++ {
		result, err := f.s.History(ctx, f.user, 20, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			if item.ID == removed || seen[item.ID] {
				t.Fatalf("deleted/duplicate item on page %d: %s", page, item.ID)
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 44 {
		t.Fatalf("history after retention reached %d of 44 rows", len(seen))
	}
}

func TestOpenGraphEditCannotRevokeEarnedUnlockOpportunity(t *testing.T) {
	f := newFishFixture(t)
	period, first := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.SaveNode(ctx, f.admin, period, "", NodeInput{Title: "Second", VersionID: p.Nodes[0].VersionID, Condition: json.RawMessage(fmt.Sprintf(`{"passed":%q}`, first)), Amounts: Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}, ExpectedPeriodRevision: p.Revision}, fishKey(181))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, first, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(182)); err != nil {
		t.Fatal(err)
	}
	bestID, err := db.GenerateOpaqueID("ffc_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE fatfish_progress SET passed=1,best_stars=1,best_score_units=100,best_version_id=?,best_at_ms=?,best_challenge_id=?,best_confirmed_at_ms=? WHERE user_id=? AND period_id=? AND node_id=?`, p.Nodes[0].VersionID, f.clock.Load(), bestID, f.clock.Load(), f.user, period, first); err != nil {
		t.Fatal(err)
	}
	p, err = f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	stricter := NodeInput{Title: "Second", VersionID: second.VersionID, Condition: json.RawMessage(fmt.Sprintf(`{"stars":{"node":%q,"min":3}}`, first)), Amounts: Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}, ExpectedRevision: second.Revision, ExpectedPeriodRevision: p.Revision}
	if _, err = f.s.SaveNode(ctx, f.admin, period, second.ID, stricter, fishKey(183)); !errors.Is(err, ErrConflict) {
		t.Fatalf("one-star opportunity revoked: %v", err)
	}
	still, err := f.s.Node(ctx, f.user, period, second.ID)
	if err != nil || !still.Eligible || still.Revision != second.Revision {
		t.Fatalf("failed edit mutated graph %+v %v", still, err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, second.ID, UnlockInput{ExpectedRevision: second.Revision}, fishKey(184)); err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, 16)
	res, err := f.db.Exec(`INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "fatfish-unqualified", "unqualified", 0, 1, zero, zero, zero, zero, zero, zero, zero, zero, fixtureNow, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO fatfish_progress(user_id,period_id,node_id,unlocked_at,passed,best_stars,best_score_units) VALUES(?,?,?,?,0,0,0)`, peer, period, first, fixtureNow); err != nil {
		t.Fatal(err)
	}
	updated, err := f.s.SaveNode(ctx, f.admin, period, second.ID, stricter, fishKey(185))
	if err != nil {
		t.Fatalf("already paid unlock was blocked: %v", err)
	}
	unqualified, err := f.s.Node(ctx, peer, period, second.ID)
	if err != nil || unqualified.Eligible {
		t.Fatalf("unqualified account unexpectedly gained entry %+v %v", unqualified, err)
	}
	cap, hash := fishCap(185)
	if _, err = f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: second.ID, ExpectedRevision: updated.Revision, TabCapabilityHash: hash}, fishKey(186)); err != nil {
		t.Fatalf("paid unlock lost entry: %v (%s)", err, cap)
	}
}

func TestVisibleNodeHidesUnrevealedPrerequisiteIdentity(t *testing.T) {
	f := newFishFixture(t)
	period, entry := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	zero := Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}
	hidden, err := f.s.SaveNode(ctx, f.admin, period, "", NodeInput{Title: "Hidden", VersionID: p.Nodes[0].VersionID, Condition: json.RawMessage(fmt.Sprintf(`{"passed":%q}`, entry)), HiddenUntilEligible: true, Amounts: zero, ExpectedPeriodRevision: p.Revision}, fishKey(191))
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := f.s.SaveNode(ctx, f.admin, period, "", NodeInput{Title: "Visible", VersionID: p.Nodes[0].VersionID, Condition: json.RawMessage(fmt.Sprintf(`{"passed":%q}`, hidden.ID)), Amounts: zero, ExpectedPeriodRevision: p.Revision}, fishKey(192))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.s.Node(ctx, f.user, period, visible.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ConditionHint == nil || detail.ConditionHint.Kind != "hidden" || detail.ConditionHint.HiddenCount != 1 || detail.ConditionHint.NodeID != "" {
		t.Fatalf("hidden prerequisite hint %+v", detail.ConditionHint)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), hidden.ID) {
		t.Fatalf("hidden prerequisite ID leaked in visible detail: %s", raw)
	}
	periodDetail, err := f.s.Period(ctx, f.user, period)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range periodDetail.Nodes {
		if len(item.Condition) != 0 || len(item.Level) != 0 {
			t.Fatalf("period preview included detailed node payload: %+v", item)
		}
		if item.ID == hidden.ID && item.ConditionHint != nil {
			t.Fatal("hidden node exposed its prerequisite configuration")
		}
		if item.ID == visible.ID && !reflect.DeepEqual(item.ConditionHint, detail.ConditionHint) {
			t.Fatalf("period prerequisite projection differs from node detail: %+v", item.ConditionHint)
		}
		if item.ID == entry && (item.ConditionHint == nil || item.ConditionHint.Kind != "none") {
			t.Fatal("visible entry node omitted its safe prerequisite projection")
		}
	}
}

func TestCollectionPagesStayMetadataOnlyAtNodeLimit(t *testing.T) {
	f := newFishFixture(t)
	period, _ := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, 16)
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1; i < 128; i++ {
		id, makeErr := db.GenerateOpaqueID("ffn_")
		if makeErr != nil {
			t.Fatal(makeErr)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO fatfish_nodes(id,period_id,title,description,map_x,map_y,ord,current_revision) VALUES(?,?,?,?,?,?,?,1)`, id, period, "Node", strings.Repeat("d", 4096), i, 0, i); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO fatfish_node_revisions(node_id,revision,version_id,condition_json,hidden_until_eligible,unlock_cost_mag,ticket_price_mag,first_clear_reward_mag,star1_reward_mag,star2_reward_mag,star3_reward_mag,created_at) VALUES(?,1,?,'{}',0,?,?,?,?,?,?,?)`, id, p.Nodes[0].VersionID, zero, zero, zero, zero, zero, zero, fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id, makeErr := db.GenerateOpaqueID("ffp_")
		if makeErr != nil {
			t.Fatal(makeErr)
		}
		if _, err = f.db.Exec(`INSERT INTO fatfish_periods(id,title,description,state,visible,paused,past_public,starts_at,ends_at,revision,created_at,updated_at) VALUES(?,?,?,'closed',1,0,1,?,?,1,?,?)`, id, "Old", "", fixtureNow-100-int64(i*100), fixtureNow-50-int64(i*100), fixtureNow, fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	userPage, err := f.s.ListPeriods(ctx, f.user, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondPage, err := f.s.ListPeriods(ctx, f.user, 2)
	if err != nil {
		t.Fatal(err)
	}
	adminPage, err := f.s.AdminPeriods(ctx, f.admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	levels, err := f.s.Levels(ctx, f.admin, 1)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := f.s.Versions(ctx, f.admin, levels.Items[0].ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(userPage.Items) != 20 || !userPage.HasMore || len(secondPage.Items) != 1 || secondPage.HasMore || len(adminPage.Items) != 20 || !adminPage.HasMore || len(levels.Items) != 1 || len(versions.Items) != 1 || len(userPage.Items[0].Nodes) != 0 || len(adminPage.Items[0].Nodes) != 0 || len(levels.Items[0].Draft) != 0 || len(versions.Items[0].Content) != 0 {
		t.Fatalf("unbounded collection projection: %+v %+v %+v %+v", userPage, adminPage, levels, versions)
	}
	for name, value := range map[string]any{"user periods": userPage, "admin periods": adminPage, "levels": levels, "versions": versions} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil || len(raw) > 32768 {
			t.Fatalf("%s response %d bytes %v", name, len(raw), marshalErr)
		}
	}
	detail, err := f.s.Period(ctx, f.user, period)
	if err != nil || len(detail.Nodes) != 128 {
		t.Fatalf("period nodes=%d %v", len(detail.Nodes), err)
	}
	raw, err := json.Marshal(detail)
	if err != nil || len(raw) > 4<<20 {
		t.Fatalf("period detail body %d %v", len(raw), err)
	}
	for _, item := range detail.Nodes {
		if len(item.Level) != 0 || len(item.Condition) != 0 {
			t.Fatal("period multiplied full level payload")
		}
	}
}
