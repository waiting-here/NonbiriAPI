package linklink

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

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	testNow     int64 = 2_000_000_000
	testFunding int64 = 1_000_000
)

var errInjected = errors.New("injected linklink failure")

type scriptedSource struct {
	mu       sync.Mutex
	values   []uint64
	calls    int
	failAt   int
	noSwap   bool
	sequence uint64
}

func (source *scriptedSource) Uint64n(bound uint64) (uint64, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls++
	if source.failAt > 0 && source.calls == source.failAt {
		return 0, errInjected
	}
	if bound == 0 {
		return 0, errInjected
	}
	if len(source.values) > 0 {
		value := source.values[0]
		source.values = source.values[1:]
		return value % bound, nil
	}
	if source.noSwap {
		return bound - 1, nil
	}
	source.sequence = source.sequence*6364136223846793005 + 1442695040888963407
	return source.sequence % bound, nil
}

func (source *scriptedSource) failNext() {
	source.mu.Lock()
	source.failAt = source.calls + 1
	source.mu.Unlock()
}

func (source *scriptedSource) setNoSwap(value bool) {
	source.mu.Lock()
	source.noSwap = value
	source.mu.Unlock()
}

func (source *scriptedSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

type testAuthorizer struct{ calls atomic.Int64 }

func (authorizer *testAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls.Add(1)
	var admin, banned int
	var bannedUntil sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, userID).Scan(&admin, &banned, &bannedUntil); errors.Is(err, sql.ErrNoRows) {
		return resources.ErrUnauthorized
	} else if err != nil {
		return err
	}
	if admin != 0 || banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > testNow) {
		return resources.ErrForbidden
	}
	return nil
}

type testContinuation struct {
	service *Service
	calls   atomic.Int64
}

func (continuation *testContinuation) AuthorizeContinuation(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
	continuation.calls.Add(1)
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	if enabled != 1 {
		return maintenance.ContinuationSnapshot{}, maintenance.ErrNotInMaintenance
	}
	allowed, err := continuation.service.continuationAuthority(ctx, tx, request)
	if err != nil {
		return maintenance.ContinuationSnapshot{}, err
	}
	if !allowed {
		return maintenance.ContinuationSnapshot{}, maintenance.ErrContinuationDenied
	}
	return continuation.service.continuationSnapshot(ctx, tx, request)
}

type fixture struct {
	t            *testing.T
	store        *db.Store
	database     *sql.DB
	vault        *secret.Vault
	service      *Service
	random       *scriptedSource
	authorizer   *testAuthorizer
	continuation *testContinuation
	limiter      *game.StartLimiter
	clock        atomic.Int64
	adminID      int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "linklink.db"), vault)
	if err != nil {
		vault.Close()
		t.Fatal(err)
	}
	value := &fixture{
		t: t, store: store, database: store.DB(), vault: vault,
		random: &scriptedSource{}, authorizer: &testAuthorizer{}, continuation: &testContinuation{},
	}
	value.clock.Store(testNow)
	limiter, err := game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time { return time.Unix(value.clock.Load(), 0).UTC() }})
	if err != nil {
		store.Close()
		vault.Close()
		t.Fatal(err)
	}
	value.limiter = limiter
	value.adminID = value.seedIdentity("operator", true)
	value.setMaintenance(false)
	value.setConfig(true, true, map[string]bool{"6x8": true, "8x8": true, "10x10": true}, 1000)
	service, err := New(Options{
		Store: store, UserAuthorizer: value.authorizer, Continuation: value.continuation,
		Limiter: limiter,
		Random:  value.random, Now: func() time.Time { return time.Unix(value.clock.Load(), 0).UTC() },
		HealthEpoch: 7, WorkerInterval: time.Minute,
	})
	if err != nil {
		store.Close()
		vault.Close()
		t.Fatal(err)
	}
	value.service = service
	value.continuation.service = service
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
		limiter.Close()
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
		if err := vault.Close(); err != nil {
			t.Errorf("close vault: %v", err)
		}
	})
	return value
}

func (fixture *fixture) seedIdentity(label string, admin bool) int64 {
	fixture.t.Helper()
	zero := db.EncodeU128(db.U128{})
	var discord any = "linklink-" + label
	if admin {
		discord = nil
	}
	result, err := fixture.database.Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, discord, label, boolInt(admin), zero, zero, zero, zero, zero, zero, zero, zero, testNow, testNow)
	if err != nil {
		fixture.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatal(err)
	}
	return id
}

func (fixture *fixture) seedUser(label string, funding int64) (int64, string) {
	fixture.t.Helper()
	userID := fixture.seedIdentity(label, false)
	binding := "linklink-session-binding-" + label
	if _, err := fixture.database.Exec(`
INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, binding, userID, testNow, testNow+86400, testNow+172800, testNow, "generation-1"); err != nil {
		fixture.t.Fatal(err)
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.CreateUserAccount(context.Background(), tx, userID, fixture.clock.Load())
	if err != nil {
		fixture.t.Fatal(err)
	}
	if funding > 0 {
		external, err := ledger.CodedAccount(context.Background(), tx, "external")
		if err != nil {
			fixture.t.Fatal(err)
		}
		plan, err := ledger.NewAdminUserAdjustment(
			ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()},
			wallet.ID, external.ID, ledger.AmountFromMilli(funding), 0, ledger.Amount{}, "fixture funding",
		)
		if err != nil {
			fixture.t.Fatal(err)
		}
		if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
			fixture.t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
	return userID, binding
}

func (fixture *fixture) mustID(prefix string) string {
	fixture.t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return id
}

func (fixture *fixture) key(number int) string {
	return "linklink_idempotency_key_" + strconv.Itoa(number) + "_xxxxxxxxxxxxxxxx"
}

func (fixture *fixture) setMaintenance(enabled bool) {
	fixture.t.Helper()
	value := 0
	if enabled {
		value = 1
	}
	if _, err := fixture.database.Exec(`UPDATE maintenance_state SET enabled=?,revision=revision+1,changed_at=? WHERE id=1`, value, fixture.clock.Load()); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *fixture) setConfig(master, link bool, specs map[string]bool, price int64) {
	fixture.t.Helper()
	updates := map[string]string{
		game.GamesEnabledKey: strconv.Itoa(boolInt(master)), game.LinkLinkEnabledKey: strconv.Itoa(boolInt(link)),
	}
	for _, spec := range []string{game.LinkLinkSpec6x8, game.LinkLinkSpec8x8, game.LinkLinkSpec10x10} {
		updates[game.LinkLinkSpecEnabledKey(spec)] = strconv.Itoa(boolInt(specs[spec]))
		updates[game.LinkLinkSpecPriceKey(spec)] = strconv.FormatInt(price, 10)
	}
	for key, value := range updates {
		if _, err := fixture.database.Exec(`UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, fixture.clock.Load(), key); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

func (fixture *fixture) balance(userID int64) string {
	fixture.t.Helper()
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return account.Balance.Decimal()
}

func (fixture *fixture) scalar(query string, arguments ...any) int64 {
	fixture.t.Helper()
	var value int64
	if err := fixture.database.QueryRow(query, arguments...).Scan(&value); err != nil {
		fixture.t.Fatal(err)
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
