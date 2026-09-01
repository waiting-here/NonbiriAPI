package runtime

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	fixtureNow     int64 = 2_000_000_000
	fixtureFunding int64 = 1_000_000_000
)

var errInjected = errors.New("injected game runtime failure")

type scriptedSource struct {
	mu     sync.Mutex
	values []uint64
	max    bool
	failAt int
	calls  int
}

func (source *scriptedSource) Uint64n(bound uint64) (uint64, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls++
	if source.failAt > 0 && source.calls == source.failAt {
		return 0, errInjected
	}
	if len(source.values) != 0 {
		value := source.values[0]
		source.values = source.values[1:]
		return value, nil
	}
	if source.max {
		return bound - 1, nil
	}
	return 0, nil
}

func (source *scriptedSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

type testUserAuthorizer struct {
	calls atomic.Int64
	mu    sync.RWMutex
	err   error
}

func (authorizer *testUserAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls.Add(1)
	authorizer.mu.RLock()
	injected := authorizer.err
	authorizer.mu.RUnlock()
	if injected != nil {
		return injected
	}
	var admin, banned int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned FROM users WHERE id=?`, userID).Scan(&admin, &banned); errors.Is(err, sql.ErrNoRows) {
		return authz.ErrUnauthorized
	} else if err != nil {
		return err
	}
	if admin != 0 || banned != 0 {
		return authz.ErrForbidden
	}
	return nil
}

func (authorizer *testUserAuthorizer) setError(err error) {
	authorizer.mu.Lock()
	authorizer.err = err
	authorizer.mu.Unlock()
}

type testAdminAuthorizer struct {
	calls atomic.Int64
	mu    sync.RWMutex
	err   error
}

func (authorizer *testAdminAuthorizer) AuthorizeAdminMutation(context.Context, *sql.Tx) error {
	authorizer.calls.Add(1)
	authorizer.mu.RLock()
	defer authorizer.mu.RUnlock()
	return authorizer.err
}

func (authorizer *testAdminAuthorizer) setError(err error) {
	authorizer.mu.Lock()
	authorizer.err = err
	authorizer.mu.Unlock()
}

type gameFixture struct {
	t         *testing.T
	store     *db.Store
	database  *sql.DB
	service   *Service
	random    *scriptedSource
	userAuth  *testUserAuthorizer
	adminAuth *testAdminAuthorizer
	adminID   int64
	clock     atomic.Int64
}

func newGameFixture(t *testing.T, source *scriptedSource) *gameFixture {
	t.Helper()
	if source == nil {
		source = &scriptedSource{}
	}
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "game.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("open game store: %v", err)
	}
	fixture := &gameFixture{
		t: t, store: store, database: store.DB(), random: source,
		userAuth: &testUserAuthorizer{}, adminAuth: &testAdminAuthorizer{},
	}
	fixture.clock.Store(fixtureNow)
	fixture.adminID = fixture.seedIdentity("operator", true)
	if _, err = fixture.database.Exec(`UPDATE maintenance_state SET enabled=0,revision=revision+1,changed_at=? WHERE id=1`, fixtureNow); err != nil {
		t.Fatalf("disable maintenance: %v", err)
	}
	if _, err = fixture.database.Exec(`UPDATE site_config SET value='1',updated_at=? WHERE key='games_enabled'`, fixtureNow); err != nil {
		t.Fatalf("enable games: %v", err)
	}
	if _, err = fixture.database.Exec(`UPDATE site_config SET value='1',updated_at=? WHERE key='game_fishing_enabled'`, fixtureNow); err != nil {
		t.Fatalf("enable fishing: %v", err)
	}
	service, err := New(Options{
		Store: store, UserAuthorizer: fixture.userAuth, AdminAuthorizer: fixture.adminAuth,
		Random: source, Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() },
		LeaderboardTieKey: []byte("0123456789abcdef0123456789abcdef"),
		WorkerInterval:    time.Minute,
	})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("new game service: %v", err)
	}
	fixture.service = service
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close game service: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close game store: %v", err)
		}
		if err := vault.Close(); err != nil {
			t.Errorf("close vault: %v", err)
		}
	})
	return fixture
}

func (fixture *gameFixture) seedIdentity(label string, admin bool) int64 {
	fixture.t.Helper()
	zero := db.EncodeU128(db.U128{})
	var discord any = "game-" + label
	if admin {
		discord = nil
	}
	result, err := fixture.database.Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, discord, label, boolInteger(admin), zero, zero, zero, zero, zero, zero, zero, zero, fixtureNow, fixtureNow)
	if err != nil {
		fixture.t.Fatalf("seed identity %s: %v", label, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatalf("identity id: %v", err)
	}
	return id
}

func (fixture *gameFixture) seedUser(label string, funding int64) int64 {
	fixture.t.Helper()
	userID := fixture.seedIdentity(label, false)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.CreateUserAccount(context.Background(), tx, userID, fixture.clock.Load())
	if err != nil {
		fixture.t.Fatalf("create wallet: %v", err)
	}
	if funding != 0 {
		external, readErr := ledger.CodedAccount(context.Background(), tx, "external")
		if readErr != nil {
			fixture.t.Fatal(readErr)
		}
		plan, planErr := ledger.NewAdminUserAdjustment(
			ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()},
			wallet.ID, external.ID, ledger.AmountFromMilli(funding), 0, ledger.Amount{}, "fixture funding",
		)
		if planErr != nil {
			fixture.t.Fatal(planErr)
		}
		if _, applyErr := ledger.Apply(context.Background(), tx, plan); applyErr != nil {
			fixture.t.Fatalf("fund wallet: %v", applyErr)
		}
	}
	if err = tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
	return userID
}

func (fixture *gameFixture) mustID(prefix string) string {
	fixture.t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		fixture.t.Fatalf("generate %s id: %v", prefix, err)
	}
	return id
}

func (fixture *gameFixture) scalar(query string, arguments ...any) int64 {
	fixture.t.Helper()
	var value int64
	if err := fixture.database.QueryRow(query, arguments...).Scan(&value); err != nil {
		fixture.t.Fatalf("query scalar: %v", err)
	}
	return value
}

func validTestKey(number int) string {
	return "game_idempotency_key_" + strconv.Itoa(number) + "_xxxxxxxxxxxxxxxx"
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
