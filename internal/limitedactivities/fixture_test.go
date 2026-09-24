package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const testNow int64 = 1_800_000_000

type actorContext struct{}
type authority struct{ auth *authz.Authorizer }

func (a authority) check(ctx context.Context, tx *sql.Tx, id int64, role authz.Role) error {
	actor, ok := ctx.Value(actorContext{}).(authz.Actor)
	if !ok || actor.UserID != id {
		return authz.ErrUnauthorized
	}
	_, err := a.auth.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return err
}
func (a authority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, authz.RoleUser)
}
func (a authority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.check(ctx, tx, id, authz.RoleAdministrator)
}

type admission struct{}

func (admission) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, _ int64) error {
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM maintenance_state WHERE id=1").Scan(&enabled); err != nil {
		return err
	}
	if enabled {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

type testFinalizer struct {
	commits, aborts *atomic.Int64
	done            atomic.Bool
}

func (f *testFinalizer) Commit() bool {
	if !f.done.CompareAndSwap(false, true) {
		return false
	}
	f.commits.Add(1)
	return true
}
func (f *testFinalizer) Abort() bool {
	if !f.done.CompareAndSwap(false, true) {
		return false
	}
	f.aborts.Add(1)
	return true
}

type runtimeStub struct {
	ready, fail               atomic.Bool
	prepared, commits, aborts atomic.Int64
}

func (r *runtimeStub) ReadyTx(context.Context, *sql.Tx) (bool, error) { return r.ready.Load(), nil }
func (r *runtimeStub) prepare() (Finalizer, error) {
	r.prepared.Add(1)
	f := &testFinalizer{commits: &r.commits, aborts: &r.aborts}
	if r.fail.Load() {
		return f, errors.New("fixture cancellation failure")
	}
	return f, nil
}
func (r *runtimeStub) PreparePauseTx(context.Context, *sql.Tx, int64) (Finalizer, error) {
	return r.prepare()
}
func (r *runtimeStub) PrepareMaintenanceTx(context.Context, *sql.Tx, int64) (Finalizer, error) {
	return r.prepare()
}
func (r *runtimeStub) PrepareBanTx(context.Context, *sql.Tx, int64, int64) (Finalizer, error) {
	return r.prepare()
}
func (r *runtimeStub) PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (Finalizer, error) {
	return r.prepare()
}
func (r *runtimeStub) RecoverBeforeListener(context.Context, int64, int, time.Duration) (lifecycle.WorkResult, error) {
	return lifecycle.WorkResult{Processed: 1}, nil
}
func (r *runtimeStub) Retain(context.Context, int64, int, time.Duration) (lifecycle.WorkResult, error) {
	return lifecycle.WorkResult{}, nil
}

type recorder struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (r *recorder) RecordLimitedActivityTx(context.Context, *sql.Tx, int64, int64) error {
	r.calls.Add(1)
	if r.fail.Load() {
		return errors.New("recorder failed")
	}
	return nil
}

type fixture struct {
	database                             *sql.DB
	service                              *Service
	runtime                              *runtimeStub
	recorder                             *recorder
	now                                  atomic.Int64
	actors                               map[int64]authz.Actor
	user, other, admin, steward, trainee int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "limited.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fixture{database: store.DB(), runtime: &runtimeStub{}, recorder: &recorder{}, actors: map[int64]authz.Actor{}}
	if _, err = store.DB().Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec("UPDATE site_config SET value='0' WHERE key='maintenance_mode'"); err != nil {
		t.Fatal(err)
	}
	f.now.Store(testNow)
	f.runtime.ready.Store(true)
	now := func() time.Time { return time.Unix(f.now.Load(), 0) }
	a := authority{authz.New(authz.Options{Now: now})}
	f.service, err = New(Config{Database: f.database, Users: a, Admins: a, Gate: admission{}, Keys: vault, Registry: NewRegistry(f.runtime), Activity: f.recorder, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(label string, admin, level int) int64 {
		zero := make([]byte, 16)
		row, e := f.database.Exec("INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", "limited-"+label, label, admin, level, zero, zero, zero, zero, zero, zero, zero, zero, testNow-1000, testNow-1000)
		if e != nil {
			t.Fatal(e)
		}
		id, e := row.LastInsertId()
		if e != nil {
			t.Fatal(e)
		}
		token := "limited-session-" + label
		_, e = f.database.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)", token, id, testNow, testNow+86400, testNow+86400, testNow, "g1")
		if e != nil {
			t.Fatal(e)
		}
		kind := authz.ActorUserSession
		if admin == 1 {
			kind = authz.ActorAdminSession
		}
		f.actors[id] = authz.Actor{Kind: kind, UserID: id, SessionTokenHash: token, SessionGeneration: "g1"}
		f.tx(t, func(tx *sql.Tx) {
			for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
				if _, e := ledger.CreateUserAssetAccount(context.Background(), tx, id, asset, testNow); e != nil {
					t.Fatal(e)
				}
			}
		})
		return id
	}
	f.admin = seed("admin", 1, 1)
	f.user = seed("user", 0, 1)
	f.other = seed("other", 0, 1)
	f.steward = seed("steward", 0, 6)
	f.trainee = seed("trainee", 0, 5)
	return f
}
func (f *fixture) ctx(id int64) context.Context {
	return context.WithValue(context.Background(), actorContext{}, f.actors[id])
}
func (f *fixture) tx(t *testing.T, fn func(*sql.Tx)) {
	t.Helper()
	tx, e := f.database.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	fn(tx)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func key(n int) string { return fmt.Sprintf("limited-operation-%06d", n) }
func module(t *testing.T, paper, brush, cap string) json.RawMessage {
	t.Helper()
	raw, e := json.Marshal(ExchangeSettings{paper, brush, cap})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func (f *fixture) config(t *testing.T, visible bool, start, end *int64, pause bool, cap string) Detail {
	t.Helper()
	old, e := f.service.AdminConfig(f.ctx(f.admin), f.admin, PictureBook)
	if e != nil {
		t.Fatal(e)
	}
	in := ConfigInput{old.Revision, visible, start, end, pause, module(t, "1000", "10000", cap)}
	result, e := f.service.UpdateConfig(f.ctx(f.admin), f.admin, PictureBook, key(1000)+old.Revision, in)
	if e != nil {
		t.Fatal(e)
	}
	return result.Value
}
func (f *fixture) open(t *testing.T, cap string) Detail {
	start, end := testNow-100, testNow+100
	return f.config(t, true, &start, &end, false, cap)
}
func (f *fixture) fund(t *testing.T, id, points int64) {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		user, e := ledger.UserAccount(context.Background(), tx, id)
		if e != nil {
			t.Fatal(e)
		}
		external, e := ledger.CodedAssetAccount(context.Background(), tx, "external", ledger.General)
		if e != nil {
			t.Fatal(e)
		}
		op, e := db.GenerateOpaqueID("op_")
		if e != nil {
			t.Fatal(e)
		}
		p, e := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: op, ActorUserID: f.admin, CreatedAt: testNow}, user.ID, external.ID, ledger.AmountFromMilli(points*1000), 0, ledger.Amount{}, "fixture funding")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ledger.Apply(context.Background(), tx, p); e != nil {
			t.Fatal(e)
		}
	})
}
func (f *fixture) exchange(id int64, n int, asset ledger.Asset, quantity string) (MutationResult[ExchangeResult], error) {
	return f.service.Exchange(f.ctx(id), id, key(n), ExchangeInput{asset, quantity})
}
func supply(t *testing.T, d Detail) ExchangeSupply {
	t.Helper()
	var v ExchangeSupply
	if e := json.Unmarshal(d.ModuleConfig, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
