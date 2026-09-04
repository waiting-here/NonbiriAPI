package activities

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type allowActivityAuth struct{}

func (allowActivityAuth) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }
func (allowActivityAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error        { return nil }
func (allowActivityAuth) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return nil }

type activityCursorKeys struct{}

func (activityCursorKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	return []byte("0123456789abcdef0123456789abcdef"), nil
}

type activityFixture struct {
	t          *testing.T
	store      *db.Store
	repository *Repository
	clock      atomic.Int64
	adminID    int64
	sequence   atomic.Uint64
}

func newActivityFixture(t *testing.T, now int64) *activityFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "activities.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &activityFixture{t: t, store: store}
	fixture.clock.Store(now)
	repository, err := NewRepository(RepositoryConfig{
		Store: store, UserFinalAuth: allowActivityAuth{}, AdminFinalAuth: allowActivityAuth{},
		UserGate: allowActivityAuth{}, CursorKeys: activityCursorKeys{},
		Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	fixture.repository = repository
	fixture.adminID, _ = fixture.seedUser("admin", true)
	return fixture
}

func (fixture *activityFixture) seedUser(label string, admin bool) (int64, ledger.Account) {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatalf("begin seed user: %v", err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	adminValue := 0
	if admin {
		adminValue = 1
	}
	result, err := tx.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "activity-"+label, label, adminValue, zero, zero, zero, zero, zero, zero, zero, zero,
		fixture.clock.Load(), fixture.clock.Load())
	if err != nil {
		fixture.t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatalf("user id: %v", err)
	}
	account, err := ledger.CreateUserAccount(context.Background(), tx, userID, fixture.clock.Load())
	if err != nil {
		fixture.t.Fatalf("create user account: %v", err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatalf("commit user: %v", err)
	}
	return userID, account
}

func (fixture *activityFixture) operationID() string {
	fixture.t.Helper()
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		fixture.t.Fatalf("operation id: %v", err)
	}
	return id
}

func (fixture *activityFixture) fundUser(userID int64, amount int64) {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatalf("begin fund user: %v", err)
	}
	defer tx.Rollback()
	user, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		fixture.t.Fatalf("user account: %v", err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		fixture.t.Fatalf("external account: %v", err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{
		OperationID: fixture.operationID(), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load(),
	}, user.ID, external.ID, ledger.AmountFromMilli(amount), 0, ledger.AmountFromMilli(0), "test funding")
	if err != nil {
		fixture.t.Fatalf("user funding plan: %v", err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		fixture.t.Fatalf("apply user funding: %v", err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatalf("commit user funding: %v", err)
	}
}

func (fixture *activityFixture) fundPool(poolID string, amount int64) {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatalf("begin fund pool: %v", err)
	}
	defer tx.Rollback()
	pool, err := readPoolRecordTx(context.Background(), tx, poolID)
	if err != nil {
		fixture.t.Fatalf("pool record: %v", err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		fixture.t.Fatalf("external account: %v", err)
	}
	plan, err := ledger.NewAdminPoolAdjustment(ledger.Meta{
		OperationID: fixture.operationID(), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load(),
	}, pool.accountID, external.ID, ledger.AmountFromMilli(amount), "test funding")
	if err != nil {
		fixture.t.Fatalf("pool funding plan: %v", err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		fixture.t.Fatalf("apply pool funding: %v", err)
	}
	if _, err := tx.Exec(`UPDATE shared_pools SET revision=revision+1 WHERE id=?`, poolID); err != nil {
		fixture.t.Fatalf("advance pool revision: %v", err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatalf("commit pool funding: %v", err)
	}
}

func (fixture *activityFixture) setActivityConfig(master, welfare, thursday bool, threshold, cap int64) {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatalf("begin config: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
		configSiteTimezone, "0", fixture.clock.Load()); err != nil {
		fixture.t.Fatalf("set timezone: %v", err)
	}
	settings := map[string]string{
		configActivitiesEnabled: boolConfig(master), configWelfareEnabled: boolConfig(welfare),
		configWelfareThreshold: fmt.Sprintf("%d", threshold), configWelfareCap: fmt.Sprintf("%d", cap),
		configThursdayEnabled: boolConfig(thursday),
	}
	for key, value := range settings {
		if _, err := tx.Exec(`UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, fixture.clock.Load(), key); err != nil {
			fixture.t.Fatalf("set config %s: %v", key, err)
		}
	}
	if _, err := tx.Exec(`UPDATE config_revisions SET revision=revision+1,updated_at=? WHERE domain='activities'`, fixture.clock.Load()); err != nil {
		fixture.t.Fatalf("advance config revision: %v", err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatalf("commit config: %v", err)
	}
}

func (fixture *activityFixture) control(method, route string, canonical any, pathIDs ...string) ControlMutation {
	fixture.t.Helper()
	body := []byte(nil)
	if canonical != nil {
		var err error
		body, err = idempotency.CanonicalJSON(canonical)
		if err != nil {
			fixture.t.Fatalf("canonical JSON: %v", err)
		}
	}
	n := fixture.sequence.Add(1)
	return ControlMutation{
		IdempotencyKey: fmt.Sprintf("activity-idem-%010d", n), Method: method, Route: route,
		PathIDs: pathIDs, CanonicalBody: body,
	}
}

func (fixture *activityFixture) configRevision() int64 {
	fixture.t.Helper()
	var revision int64
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM config_revisions WHERE domain='activities'`).Scan(&revision); err != nil {
		fixture.t.Fatalf("config revision: %v", err)
	}
	return revision
}

func (fixture *activityFixture) period(periodID string) periodRecord {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fixture.t.Fatalf("begin period read: %v", err)
	}
	defer tx.Rollback()
	period, err := readPeriodRecordTx(context.Background(), tx, periodID)
	if err != nil {
		fixture.t.Fatalf("period read: %v", err)
	}
	return period
}

func beijingThursday(year int, month time.Month, day int) int64 {
	return time.Date(year, month, day, 0, 0, 0, 0, beijingLocation).Unix()
}
