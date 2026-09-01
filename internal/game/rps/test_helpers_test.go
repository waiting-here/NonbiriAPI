package rps

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"math/big"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	rpsTestNow     int64 = 2_000_000_000
	rpsTestFunding int64 = 100_000
)

type rpsTestAuthorizer struct {
	clock *atomic.Int64
	calls atomic.Int64
}

func (authorizer *rpsTestAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls.Add(1)
	var admin, banned int
	var bannedUntil sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, userID).Scan(&admin, &banned, &bannedUntil); errors.Is(err, sql.ErrNoRows) {
		return resources.ErrUnauthorized
	} else if err != nil {
		return err
	}
	if admin != 0 || banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > authorizer.clock.Load()) {
		return resources.ErrForbidden
	}
	return nil
}

type rpsTestContinuation struct {
	service *Service
}

func (continuation *rpsTestContinuation) AuthorizeContinuation(ctx context.Context, tx *sql.Tx, request maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
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

type rpsTestAccountEvents struct {
	mu            sync.Mutex
	events        map[int64]int
	forgotten     []int64
	discarded     []int64
	purged        []int64
	calls         []string
	frames        map[int64]bool
	subscriptions map[int64]bool
	tombstones    map[int64]bool
	forgetErr     error
	discardErr    error
	purgeErr      error
}

func (events *rpsTestAccountEvents) PublishCommitted(_ context.Context, accountID int64, _ accountstream.PublishedEvent) (accountstream.Frame, error) {
	events.mu.Lock()
	if events.events == nil {
		events.events = map[int64]int{}
	}
	events.events[accountID]++
	events.mu.Unlock()
	return accountstream.Frame{}, nil
}

func (events *rpsTestAccountEvents) ForgetAccounts(_ context.Context, accountIDs []int64) error {
	events.mu.Lock()
	defer events.mu.Unlock()
	events.calls = append(events.calls, "forget")
	events.forgotten = append(events.forgotten, accountIDs...)
	if events.forgetErr != nil {
		return events.forgetErr
	}
	for _, accountID := range accountIDs {
		delete(events.frames, accountID)
		delete(events.subscriptions, accountID)
		if events.tombstones == nil {
			events.tombstones = map[int64]bool{}
		}
		events.tombstones[accountID] = true
	}
	return nil
}

func (events *rpsTestAccountEvents) DiscardAccounts(_ context.Context, accountIDs []int64) error {
	events.mu.Lock()
	defer events.mu.Unlock()
	events.calls = append(events.calls, "discard")
	events.discarded = append(events.discarded, accountIDs...)
	if events.discardErr != nil {
		return events.discardErr
	}
	for _, accountID := range accountIDs {
		delete(events.frames, accountID)
		delete(events.subscriptions, accountID)
	}
	return nil
}

func (events *rpsTestAccountEvents) PurgeAccounts(_ context.Context, accountIDs []int64) error {
	events.mu.Lock()
	defer events.mu.Unlock()
	events.calls = append(events.calls, "purge")
	events.purged = append(events.purged, accountIDs...)
	for _, accountID := range accountIDs {
		if events.tombstones[accountID] {
			return errors.New("test account event sink: purged tombstoned account")
		}
	}
	return events.purgeErr
}

func (events *rpsTestAccountEvents) reconnect(accountID int64) bool {
	events.mu.Lock()
	defer events.mu.Unlock()
	if events.tombstones[accountID] {
		return false
	}
	if events.subscriptions == nil {
		events.subscriptions = map[int64]bool{}
	}
	events.subscriptions[accountID] = true
	return true
}

type rpsTestActivityEvents struct {
	mu    sync.Mutex
	calls atomic.Int64
	facts []activities.PublishFacts
}

func (events *rpsTestActivityEvents) Publish(_ context.Context, facts activities.PublishFacts) error {
	copyFacts := activities.PublishFacts{Global: facts.Global, AccountIDs: append([]int64(nil), facts.AccountIDs...)}
	events.mu.Lock()
	events.facts = append(events.facts, copyFacts)
	events.mu.Unlock()
	events.calls.Add(1)
	return nil
}

type rpsTestPools struct{}

func (rpsTestPools) WelfareDestination(ctx context.Context, tx *sql.Tx) (activities.PoolDestination, error) {
	return rpsTestPool(ctx, tx, "welfare")
}

func (rpsTestPools) ThursdayDestination(ctx context.Context, tx *sql.Tx, _ int64) (activities.PoolDestination, error) {
	return rpsTestPool(ctx, tx, "thursday")
}

func rpsTestPool(ctx context.Context, tx *sql.Tx, kind string) (activities.PoolDestination, error) {
	var result activities.PoolDestination
	result.PoolType = kind
	query := `SELECT id,account_id FROM shared_pools WHERE pool_type=? AND state='open' ORDER BY created_at,id LIMIT 1`
	if err := tx.QueryRowContext(ctx, query, kind).Scan(&result.PoolID, &result.AccountID); err != nil {
		return activities.PoolDestination{}, err
	}
	return result, nil
}

func (rpsTestPools) RecordPoolTransfers(ctx context.Context, tx *sql.Tx, _ int64, destinations ...activities.PoolDestination) (activities.PublishFacts, error) {
	seen := map[string]struct{}{}
	for _, destination := range destinations {
		if _, duplicate := seen[destination.PoolID]; duplicate {
			continue
		}
		seen[destination.PoolID] = struct{}{}
		result, err := tx.ExecContext(ctx, `UPDATE shared_pools SET revision=revision+1
WHERE id=? AND account_id=? AND pool_type=? AND state='open'`, destination.PoolID, destination.AccountID, destination.PoolType)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return activities.PublishFacts{}, ErrConflict
		}
	}
	return activities.PublishFacts{Global: len(seen) != 0}, nil
}

type rpsFixture struct {
	t            *testing.T
	store        *db.Store
	database     *sql.DB
	vault        *secret.Vault
	service      *Service
	limiter      *game.StartLimiter
	authorizer   *rpsTestAuthorizer
	continuation *rpsTestContinuation
	account      *rpsTestAccountEvents
	activity     *rpsTestActivityEvents
	clock        atomic.Int64
	adminID      int64
}

func newRPSFixture(t *testing.T) *rpsFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "rps.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	fixture := &rpsFixture{t: t, store: store, database: store.DB(), vault: vault,
		continuation: &rpsTestContinuation{}, account: &rpsTestAccountEvents{}, activity: &rpsTestActivityEvents{}}
	fixture.clock.Store(rpsTestNow)
	fixture.authorizer = &rpsTestAuthorizer{clock: &fixture.clock}
	limiter, err := game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time {
		return time.Unix(fixture.clock.Load(), 0).UTC()
	}})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	fixture.limiter = limiter
	fixture.adminID = fixture.seedIdentity("operator", true)
	fixture.setMaintenance(false)
	fixture.setRPSConfig(true, 1_000, PumpsBP{Platform: 100, Welfare: 100, Thursday: 100})
	fixture.service = fixture.newService(7)
	if err := fixture.service.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.service.Close()
		_ = fixture.limiter.Close()
		_ = fixture.store.Close()
		_ = fixture.vault.Close()
	})
	return fixture
}

func (fixture *rpsFixture) newService(epoch int64) *Service {
	fixture.t.Helper()
	service, err := New(Options{Store: fixture.store, UserAuthorizer: fixture.authorizer,
		Continuation: fixture.continuation, Limiter: fixture.limiter, Pools: rpsTestPools{},
		AccountEvents: fixture.account, ActivityEvents: fixture.activity, Keys: fixture.vault, Random: rand.Reader,
		Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() }, HealthEpoch: epoch, WorkerInterval: time.Minute})
	if err != nil {
		fixture.t.Fatal(err)
	}
	fixture.continuation.service = service
	return service
}

func (fixture *rpsFixture) seedIdentity(label string, admin bool) int64 {
	fixture.t.Helper()
	zero := db.EncodeU128(db.U128{})
	var discord any = "rps-" + label
	if admin {
		discord = nil
	}
	result, err := fixture.database.Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, discord, label, boolIntRPS(admin), zero, zero, zero, zero, zero, zero, zero, zero,
		fixture.clock.Load(), fixture.clock.Load())
	if err != nil {
		fixture.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatal(err)
	}
	return id
}

func (fixture *rpsFixture) seedUser(label string, funding int64) (int64, string) {
	fixture.t.Helper()
	userID := fixture.seedIdentity(label, false)
	binding := "rps-session-binding-" + label
	if _, err := fixture.database.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, binding, userID, fixture.clock.Load(), fixture.clock.Load()+86400, fixture.clock.Load()+172800,
		fixture.clock.Load(), "generation-1"); err != nil {
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
		plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID,
			CreatedAt: fixture.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(funding), 0, ledger.Amount{}, "fixture funding")
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

func (fixture *rpsFixture) setMaintenance(enabled bool) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(`UPDATE maintenance_state SET enabled=?,revision=revision+1,changed_at=? WHERE id=1`,
		boolIntRPS(enabled), fixture.clock.Load()); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *rpsFixture) setRPSConfig(enabled bool, base int64, pumps PumpsBP) {
	fixture.t.Helper()
	updates := map[string]string{game.GamesEnabledKey: strconv.Itoa(boolIntRPS(enabled)), game.RPSEnabledKey: strconv.Itoa(boolIntRPS(enabled))}
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard, game.RPSModeDeathmatch} {
		updates[game.RPSModeEnabledKey(mode)] = strconv.Itoa(boolIntRPS(enabled))
		updates[game.RPSModeBaseKey(mode)] = strconv.FormatInt(base, 10)
		updates[game.RPSModeBPKey(mode, "platform")] = strconv.Itoa(pumps.Platform)
		updates[game.RPSModeBPKey(mode, "welfare")] = strconv.Itoa(pumps.Welfare)
		updates[game.RPSModeBPKey(mode, "thursday")] = strconv.Itoa(pumps.Thursday)
		updates[game.RPSModeTimeKey(mode, "queue")] = "120"
		updates[game.RPSModeTimeKey(mode, "gesture")] = "20"
		updates[game.RPSModeTimeKey(mode, "dealer")] = "15"
		updates[game.RPSModeTimeKey(mode, "follower")] = "15"
	}
	for key, value := range updates {
		if _, err := fixture.database.Exec(`UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, fixture.clock.Load(), key); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

func (fixture *rpsFixture) enqueue(userID int64, mode string, deviceByte, ipByte byte, key int) QueueMutationResult {
	fixture.t.Helper()
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = deviceByte
	}
	var ip [16]byte
	ip[15] = ipByte
	result, err := fixture.service.Enqueue(context.Background(), EnqueueInput{UserID: userID, Mode: mode,
		DeviceToken: base64.RawURLEncoding.EncodeToString(raw), CanonicalSourceIP: ip,
		DeathmatchConfirmed: mode == game.RPSModeDeathmatch, IdempotencyKey: fixture.key(key)})
	if err != nil {
		fixture.t.Fatal(err)
	}
	return result
}

func (fixture *rpsFixture) mustID(prefix string) string {
	fixture.t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return id
}

func (fixture *rpsFixture) key(number int) string {
	return "rps_idempotency_key_" + strconv.Itoa(number) + "_xxxxxxxxxxxxxxxx"
}

func (fixture *rpsFixture) scalar(query string, args ...any) int64 {
	fixture.t.Helper()
	var value int64
	if err := fixture.database.QueryRow(query, args...).Scan(&value); err != nil {
		fixture.t.Fatal(err)
	}
	return value
}

func (fixture *rpsFixture) balance(userID int64) *big.Int {
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
	return account.Balance.Big()
}

func boolIntRPS(value bool) int {
	if value {
		return 1
	}
	return 0
}
