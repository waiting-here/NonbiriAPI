package checkin

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const checkinTestNow int64 = 1_800_000_000

type callRecorder struct {
	mu     sync.Mutex
	values []string
}

func (recorder *callRecorder) add(value string) {
	recorder.mu.Lock()
	recorder.values = append(recorder.values, value)
	recorder.mu.Unlock()
}

func (recorder *callRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.values...)
}

type checkinTestAuthorizer struct {
	calls    atomic.Int64
	recorder *callRecorder
	mu       sync.RWMutex
	err      error
}

func (authorizer *checkinTestAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls.Add(1)
	if authorizer.recorder != nil {
		authorizer.recorder.add("auth")
	}
	authorizer.mu.RLock()
	injected := authorizer.err
	authorizer.mu.RUnlock()
	if injected != nil {
		return injected
	}
	var isAdmin, isBanned int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned FROM users WHERE id=?`, userID).Scan(&isAdmin, &isBanned); errors.Is(err, sql.ErrNoRows) {
		return resources.ErrNotFound
	} else if err != nil {
		return err
	}
	if isAdmin != 0 || isBanned != 0 {
		return resources.ErrForbidden
	}
	return nil
}

func (authorizer *checkinTestAuthorizer) setError(err error) {
	authorizer.mu.Lock()
	authorizer.err = err
	authorizer.mu.Unlock()
}

type checkinTestMaintenance struct {
	calls    atomic.Int64
	recorder *callRecorder
	mu       sync.RWMutex
	err      error
}

func (gate *checkinTestMaintenance) AuthorizeChatAcceptance(ctx context.Context, tx *sql.Tx, _ int64, _ int64) error {
	gate.calls.Add(1)
	if gate.recorder != nil {
		gate.recorder.add("maintenance")
	}
	gate.mu.RLock()
	injected := gate.err
	gate.mu.RUnlock()
	if injected != nil {
		return injected
	}
	var enabled int
	var mirror string
	if err := tx.QueryRowContext(ctx, `SELECT m.enabled,c.value
		FROM maintenance_state m
		JOIN site_config c ON c.key='maintenance_mode'
		WHERE m.id=1`).Scan(&enabled, &mirror); err != nil {
		return err
	}
	if (enabled != 0 && enabled != 1) || (mirror != "0" && mirror != "1") || (enabled == 1) != (mirror == "1") {
		return maintenance.ErrInvariant
	}
	if enabled == 1 {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

func (gate *checkinTestMaintenance) setError(err error) {
	gate.mu.Lock()
	gate.err = err
	gate.mu.Unlock()
}

type checkinFixture struct {
	t           *testing.T
	store       *db.Store
	database    *sql.DB
	service     *Service
	authorizer  *checkinTestAuthorizer
	maintenance *checkinTestMaintenance
	order       *callRecorder
	clock       atomic.Int64
	adminID     int64
}

func newCheckinFixture(t *testing.T) *checkinFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("new secret codec: %v", err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "checkin.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("open check-in database: %v", err)
	}
	fixture := &checkinFixture{t: t, store: store, database: store.DB(), order: &callRecorder{}}
	fixture.clock.Store(checkinTestNow)
	fixture.authorizer = &checkinTestAuthorizer{recorder: fixture.order}
	fixture.maintenance = &checkinTestMaintenance{recorder: fixture.order}
	service, err := NewService(ServiceConfig{
		Store: store, FinalAuth: fixture.authorizer, Maintenance: fixture.maintenance,
		Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() },
	})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("new check-in service: %v", err)
	}
	fixture.service = service
	fixture.adminID = fixture.seedIdentity("operator", true)
	fixture.setMaintenance(false)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
		if err := vault.Close(); err != nil {
			t.Errorf("close secret codec: %v", err)
		}
	})
	return fixture
}

func (fixture *checkinFixture) seedIdentity(label string, admin bool) int64 {
	fixture.t.Helper()
	zero := db.EncodeU128(db.U128{})
	var discord any = "checkin-" + label
	if admin {
		discord = nil
	}
	result, err := fixture.database.Exec(`INSERT INTO users(
		discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
		total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
		total_unknown_usage_requests,revision,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		discord, label, boolScalar(admin), zero, zero, zero, zero, zero, zero, zero, zero,
		fixture.clock.Load(), fixture.clock.Load())
	if err != nil {
		fixture.t.Fatalf("seed identity %s: %v", label, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatalf("read identity id: %v", err)
	}
	return id
}

func (fixture *checkinFixture) seedUser(label string) int64 {
	fixture.t.Helper()
	userID := fixture.seedIdentity(label, false)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatalf("begin account seed: %v", err)
	}
	defer tx.Rollback()
	if _, err := ledger.CreateUserAccount(context.Background(), tx, userID, fixture.clock.Load()); err != nil {
		fixture.t.Fatalf("create user account: %v", err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatalf("commit account seed: %v", err)
	}
	return userID
}

func (fixture *checkinFixture) fundUser(userID, milli int64) {
	fixture.t.Helper()
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		fixture.t.Fatal(err)
	}
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		fixture.t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{
		OperationID: operationID, ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load(),
	}, wallet.ID, external.ID, ledger.AmountFromMilli(milli), 0, ledger.Amount{}, "check-in fixture funding")
	if err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		fixture.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *checkinFixture) configure(mode, timezone string, minimum, maximum, cap int64) {
	fixture.t.Helper()
	fixture.setConfig(db.SiteTimezoneKey, timezone)
	fixture.setConfig(checkinModeKey, mode)
	fixture.setConfig(awardMinimumKey, big.NewInt(minimum).String())
	fixture.setConfig(awardMaximumKey, big.NewInt(maximum).String())
	fixture.setConfig(balanceCapKey, big.NewInt(cap).String())
}

func (fixture *checkinFixture) setConfig(key, value string) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, fixture.clock.Load()); err != nil {
		fixture.t.Fatalf("set config %s: %v", key, err)
	}
}

func (fixture *checkinFixture) setMaintenance(enabled bool) {
	fixture.t.Helper()
	value := boolScalar(enabled)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE maintenance_state SET enabled=?,revision=revision+1,changed_at=? WHERE id=1`, value, fixture.clock.Load()); err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE site_config SET value=?,updated_at=? WHERE key='maintenance_mode'`, big.NewInt(int64(value)).String(), fixture.clock.Load()); err != nil {
		fixture.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *checkinFixture) scalar(query string, args ...any) int64 {
	fixture.t.Helper()
	var value int64
	if err := fixture.database.QueryRow(query, args...).Scan(&value); err != nil {
		fixture.t.Fatalf("read scalar: %v", err)
	}
	return value
}

func (fixture *checkinFixture) u128(query string, args ...any) *big.Int {
	fixture.t.Helper()
	var raw []byte
	if err := fixture.database.QueryRow(query, args...).Scan(&raw); err != nil {
		fixture.t.Fatalf("read U128: %v", err)
	}
	value, err := db.DecodeU128(raw)
	if err != nil {
		fixture.t.Fatalf("decode U128: %v", err)
	}
	return value.Big()
}

func (fixture *checkinFixture) balance(userID int64) *big.Int {
	fixture.t.Helper()
	tx, err := fixture.database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return wallet.Balance.Big()
}

func boolScalar(value bool) int {
	if value {
		return 1
	}
	return 0
}
