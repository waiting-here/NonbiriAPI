package lakenotes

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
	"path/filepath"
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

const testNow = int64(1800000000)

type actorContext struct{}
type authority struct{ auth *authz.Authorizer }

func (a authority) check(ctx context.Context, tx *sql.Tx, user int64, role authz.Role) error {
	actor, ok := ctx.Value(actorContext{}).(authz.Actor)
	if !ok || actor.UserID != user {
		return authz.ErrUnauthorized
	}
	_, e := a.auth.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return e
}
func (a authority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	return a.check(ctx, tx, user, authz.RoleUser)
}
func (a authority) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx) error {
	actor, _ := ctx.Value(actorContext{}).(authz.Actor)
	return a.check(ctx, tx, actor.UserID, authz.RoleAdministrator)
}

type admission struct{}

func (admission) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, _ int64) error {
	var enabled bool
	if e := tx.QueryRowContext(ctx, "SELECT enabled FROM maintenance_state WHERE id=1").Scan(&enabled); e != nil {
		return e
	}
	if enabled {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

type fixedRandom struct {
	value float64
	draws atomic.Int64
}

func (r *fixedRandom) Float53() (float64, error) { r.draws.Add(1); return r.value, nil }

type fixture struct {
	database           *sql.DB
	service            *Service
	now                atomic.Int64
	actors             map[int64]authz.Actor
	user, other, admin int64
	random             *fixedRandom
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	vault, e := secret.New(make([]byte, secret.MasterKeyBytes))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { vault.Close() })
	path := filepath.Join(t.TempDir(), "lake.db")
	dbfixture.Materialize(t, path)
	store, e := db.Open(path, vault)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	f := &fixture{database: store.DB(), actors: map[int64]authz.Actor{}, random: &fixedRandom{value: 0.5}}
	f.now.Store(testNow * int64(time.Second))
	now := func() time.Time { return time.Unix(0, f.now.Load()) }
	a := authority{authz.New(authz.Options{Now: now})}
	f.service, e = New(Config{Database: f.database, Users: a, Admins: a, Gate: admission{}, Keys: vault, Now: now, Random: f.random, MotionSeed: func() (uint32, error) { return 123, nil }})
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"UPDATE maintenance_state SET enabled=0 WHERE id=1", "UPDATE site_config SET value='0' WHERE key='maintenance_mode'"} {
		if _, e = f.database.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	seed := func(label string, admin bool) int64 {
		zero := make([]byte, 16)
		r, e := f.database.Exec("INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,1,?,?,?,?,?,?,?,?,?,?)", "lake-"+label, label, admin, zero, zero, zero, zero, zero, zero, zero, zero, testNow-1000, testNow-1000)
		if e != nil {
			t.Fatal(e)
		}
		id, e := r.LastInsertId()
		if e != nil {
			t.Fatal(e)
		}
		token := "lake-session-" + label
		if _, e = f.database.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,'g1')", token, id, testNow, testNow+86400, testNow+86400, testNow); e != nil {
			t.Fatal(e)
		}
		kind := authz.ActorUserSession
		if admin {
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
	f.user = seed("user", false)
	f.other = seed("other", false)
	f.admin = seed("admin", true)
	return f
}
func (f *fixture) ctx(user int64) context.Context {
	return context.WithValue(context.Background(), actorContext{}, f.actors[user])
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
func (f *fixture) fund(t *testing.T, user int64, asset ledger.Asset, value int64) {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		wallet, e := ledger.UserAssetAccount(context.Background(), tx, user, asset)
		if e != nil {
			t.Fatal(e)
		}
		external, e := ledger.CodedAssetAccount(context.Background(), tx, "external", asset)
		if e != nil {
			t.Fatal(e)
		}
		op, _ := db.GenerateOpaqueID("op_")
		meta := ledger.Meta{OperationID: op, ActorUserID: f.admin, CreatedAt: testNow}
		var plan ledger.Plan
		if asset == ledger.General {
			plan, e = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(value), 0, ledger.Amount{}, "test funding")
		} else {
			plan, e = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(value), "test funding")
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ledger.Apply(context.Background(), tx, plan); e != nil {
			t.Fatal(e)
		}
	})
}
func testKey(n int) string { return fmt.Sprintf("lake-operation-%012d", n) }
func (f *fixture) configure(wire lakeconfig.Wire) (Settings, error) {
	ctx := f.ctx(f.admin)
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	old, err := settingsTx(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	before, err := (lakeconfig.Codec{}).CompileWire(game.ConfigJSON(old.Wire), true)
	if err != nil {
		return Settings{}, err
	}
	after, err := (lakeconfig.Codec{}).CompileWire(game.ConfigJSON(wire), true)
	if err != nil {
		return Settings{}, err
	}
	if err = f.service.Module().ConfigurationChangedTx(ctx, tx, before, after); err != nil {
		return Settings{}, err
	}
	raw := after.Raw()
	raw[game.GamesEnabledKey] = "1"
	for key, value := range raw {
		if _, err = tx.Exec("UPDATE site_config SET value=? WHERE key=?", value, key); err != nil {
			return Settings{}, err
		}
	}
	if _, err = tx.Exec("UPDATE config_revisions SET revision=revision+1 WHERE domain='games'"); err != nil {
		return Settings{}, err
	}
	out, err := settingsTx(ctx, tx)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (f *fixture) enable(t *testing.T) Settings {
	t.Helper()
	wire := lakeconfig.Wire{Enabled: true, Exchanges: lakeconfig.Defaults()}
	for _, d := range directions {
		wire.Exchanges[d] = ExchangeSetting{true, "7", "250"}
	}
	out, err := f.configure(wire)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *fixture) profile(t *testing.T) ProfileView {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		if _, err := profileTx(f.ctx(f.user), tx, f.user, true, testNow); err != nil {
			t.Fatal(err)
		}
	})
	out, err := f.service.Profile(f.ctx(f.user), f.user)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func (f *fixture) legacyPeriod(t *testing.T) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("lnp_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,entry_fee_milli,created_at,updated_at) VALUES(?,'Previous waters',1,'published',?,?,0,?,?)", id, testNow-1, testNow+1, testNow-1, testNow-1); err != nil {
		t.Fatal(err)
	}
	return id
}
