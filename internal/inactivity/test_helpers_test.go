package inactivity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

const testNow int64 = 1800000000

type invalidations struct{ count atomic.Int64 }

func (i *invalidations) InvalidateUserAuthority(int64) { i.count.Add(1) }

type unavailableAuth struct{}

func (unavailableAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error {
	return authz.ErrUnauthorized
}

type testEnv struct {
	store       *db.Store
	vault       *secret.Vault
	s           *Service
	invalidator *invalidations
}

func fixture(t *testing.T) *testEnv {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x73}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inactivity.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = vault.Close() })
	i := &invalidations{}
	s, err := New(Config{Database: store.DB(), FinalAuth: unavailableAuth{}, CancelUserTx: func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error) { return func(bool) {}, nil }, Invalidator: i, Now: func() time.Time { return time.Unix(testNow, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{store, vault, s, i}
}
func (e *testEnv) user(t *testing.T, label string, level int, general, game int64) int64 {
	t.Helper()
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO users(discord_id,username,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, "inactivity-"+label, label, level, testNow-100*day, testNow-100*day)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,'test','key',?,?)`, id, bytes.Repeat([]byte{byte(id)}, 32), testNow, testNow); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, item := range []struct {
		asset  ledger.Asset
		amount int64
	}{{ledger.General, general}, {ledger.Game, game}} {
		wallet, err := ledger.CreateUserAssetAccount(ctx, tx, id, item.asset, testNow-100*day)
		if err != nil {
			t.Fatal(err)
		}
		ext, err := ledger.CodedAssetAccount(ctx, tx, "external", item.asset)
		if err != nil {
			t.Fatal(err)
		}
		op, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		meta := ledger.Meta{OperationID: op, ActorUserID: id, CreatedAt: testNow - 100*day}
		var plan ledger.Plan
		if item.asset == ledger.General {
			plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, ext.ID, ledger.AmountFromMilli(item.amount), 0, ledger.Amount{}, "fixture funding")
		} else {
			plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, ext.ID, ledger.AmountFromMilli(item.amount), "fixture funding")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err = InitializeTx(ctx, tx, id, testNow-100*day); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}
func (e *testEnv) policy(t *testing.T, p Policy) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec(`UPDATE inactivity_policy SET revision=revision+1,config_json=?,decay_grace_until=0,protection_grace_until=0`, string(raw)); err != nil {
		t.Fatal(err)
	}
}
func (e *testEnv) balance(t *testing.T, id int64, asset ledger.Asset) string {
	t.Helper()
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a, err := ledger.UserAssetAccount(context.Background(), tx, id, asset)
	if err != nil {
		t.Fatal(err)
	}
	return a.Balance.Decimal()
}
func scalar(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func runBatch(t *testing.T, s *Service, at int64) BatchResult {
	t.Helper()
	r, err := s.Process(context.Background(), at, 100, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func active(t *testing.T, e *testEnv, id, at int64, fresh bool) {
	t.Helper()
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = RecordActiveTx(context.Background(), tx, ActiveEvent{UserID: id, At: at, Kind: "api", Fresh: fresh}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func label(id int64) string { return strconv.FormatInt(id, 10) }
