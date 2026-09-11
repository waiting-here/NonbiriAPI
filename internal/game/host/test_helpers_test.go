package host

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	builtinconfig "github.com/waiting-here/NonbiriAPI/internal/game/builtin/config"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const fixtureNow int64 = 2_000_000_000

type testAdminAuthorizer struct {
	mu  sync.Mutex
	err error
}

func (a *testAdminAuthorizer) AuthorizeAdminMutation(context.Context, *sql.Tx) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}
func (a *testAdminAuthorizer) setError(err error) { a.mu.Lock(); defer a.mu.Unlock(); a.err = err }

type testUserAuthorizer struct{}

func (testUserAuthorizer) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }

var _ resources.FinalTxAuthorizer = testUserAuthorizer{}

type gameFixture struct {
	service   *Service
	database  *sql.DB
	adminAuth *testAdminAuthorizer
	adminID   int64
	clock     atomic.Int64
}

func newGameFixture(t *testing.T, _ any) *gameFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "games.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &gameFixture{database: store.DB(), adminAuth: &testAdminAuthorizer{}}
	fixture.clock.Store(fixtureNow)
	zero := db.EncodeU128(db.U128{})
	result, err := fixture.database.Exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('operator',1,?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, fixtureNow, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	fixture.adminID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.database.Exec(`UPDATE site_config SET value='1' WHERE key IN ('games_enabled','game_fishing_enabled')`); err != nil {
		t.Fatal(err)
	}
	registry, err := builtinconfig.Registry()
	if err != nil {
		t.Fatal(err)
	}
	factories := map[string]Factory{}
	for _, descriptor := range registry.Descriptors() {
		factories[descriptor.ID] = func(Services) (*Module, error) { return inertModule(), nil }
	}
	fixture.service, err = New(Options{Database: fixture.database, Registry: registry, Factories: factories, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth, Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.Close() })
	return fixture
}

// Inert capabilities isolate configuration transaction tests from workers.
func inertModule() *Module {
	return &Module{
		ValidatePersistedState: func(context.Context) error { return nil },
		RecoverBeforeListen:    func(context.Context, int64, int, time.Time) (WorkResult, error) { return WorkResult{}, nil },
		RegisterRoutes:         func(Registrars) error { return nil },
		StartWorker:            func(context.Context) error { return nil }, Close: func() error { return nil },
		Available: func(string, string) bool { return true }, ReadyTx: func(context.Context, *sql.Tx) bool { return false },
		UserSnapshotTx: func(_ context.Context, _ *sql.Tx, _ int64, _ int64, value game.ConfigValue) (game.UserSnapshot, error) {
			return game.UserSnapshot{Config: value.UserWire(func(string, string) bool { return true })}, nil
		},
		HomeSummaryTx:   func(context.Context, *sql.Tx, int64) (game.HomeSummary, error) { return game.HomeSummary{}, nil },
		ActiveCountsTx:  func(context.Context, *sql.Tx) (game.ActiveCounts, error) { return game.ActiveCounts{}, nil },
		ExportTx:        func(context.Context, *sql.Tx, int64, int64, int) (any, Finalizer, error) { return nil, nil, nil },
		PrepareDeleteTx: func(context.Context, *sql.Tx, int64, int64) (Finalizer, error) { return nil, nil },
		Retain:          func(context.Context, int64, int, time.Time) (WorkResult, error) { return WorkResult{}, nil },
	}
}

func validTestKey(number int) string {
	return "game_idempotency_key_" + strconv.Itoa(number) + "_xxxxxxxxxxxxxxxx"
}
