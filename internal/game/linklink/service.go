package linklink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	workerBatchSize = 100
	leaseTTL        = 15 * time.Second
	summaryWindow   = 30 * 24 * time.Hour
)

type ContinuationAuthorizer interface {
	AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error)
}

type Options struct {
	Store          *db.Store
	UserAuthorizer resources.FinalTxAuthorizer
	Continuation   ContinuationAuthorizer
	Limiter        *game.StartLimiter
	Random         IntSource
	Now            func() time.Time
	GenerateID     func(string) (string, error)
	HealthEpoch    int64
	WorkerInterval time.Duration
}

type leaseBindingKey struct {
	userID    int64
	sessionID string
	binding   [32]byte
}

type leaseBinding struct {
	leaseID   string
	expiresAt int64
}

type Service struct {
	database       *sql.DB
	userAuthorizer resources.FinalTxAuthorizer
	continuation   ContinuationAuthorizer
	limiter        *game.StartLimiter
	random         IntSource
	now            func() time.Time
	generateID     func(string) (string, error)
	healthEpoch    int64
	workerInterval time.Duration

	rngMu   sync.Mutex
	leaseMu sync.RWMutex
	leases  map[leaseBindingKey]leaseBinding

	workerMu          sync.Mutex
	workerCancel      context.CancelFunc
	workerDone        chan struct{}
	recoveryValidated atomic.Bool
	recovered         atomic.Bool
	closed            atomic.Bool

	// Narrow deterministic seam for reshuffle rollback tests.
	beforeReshuffleCommit func() error
}

func New(options Options) (*Service, error) {
	if options.Store == nil || options.Store.DB() == nil || options.UserAuthorizer == nil || options.Continuation == nil || options.Limiter == nil {
		return nil, errors.New("linklink: store, final user authorizer, continuation authorizer, and shared start limiter are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.GenerateID == nil {
		options.GenerateID = db.GenerateOpaqueID
	}
	if options.Random == nil {
		options.Random = CryptoSource{}
	}
	if options.HealthEpoch == 0 {
		epoch, err := randomHealthEpoch()
		if err != nil {
			return nil, fmt.Errorf("linklink: generate health epoch: %w", err)
		}
		options.HealthEpoch = epoch
	}
	if options.HealthEpoch < 1 {
		return nil, errors.New("linklink: invalid health epoch")
	}
	if options.WorkerInterval == 0 {
		options.WorkerInterval = time.Second
	}
	if options.WorkerInterval < time.Millisecond || options.WorkerInterval > time.Minute {
		return nil, errors.New("linklink: invalid worker interval")
	}
	return &Service{
		database: options.Store.DB(), userAuthorizer: options.UserAuthorizer,
		continuation: options.Continuation, limiter: options.Limiter,
		random: options.Random, now: options.Now, generateID: options.GenerateID,
		healthEpoch: options.HealthEpoch, workerInterval: options.WorkerInterval,
		leases: make(map[leaseBindingKey]leaseBinding),
	}, nil
}

func randomHealthEpoch() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	value := int64(binary.BigEndian.Uint64(raw[:]) & uint64(^uint64(0)>>1))
	if value == 0 {
		value = 1
	}
	return value, nil
}

func (service *Service) Close() error {
	if service == nil || !service.closed.CompareAndSwap(false, true) {
		return nil
	}
	service.workerMu.Lock()
	cancel, done := service.workerCancel, service.workerDone
	service.workerCancel, service.workerDone = nil, nil
	service.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	service.leaseMu.Lock()
	clear(service.leases)
	service.leaseMu.Unlock()
	return nil
}

func (service *Service) decisionNow() (int64, error) {
	if service == nil || service.now == nil {
		return 0, ErrClosed
	}
	now := service.now().UTC().Unix()
	if now < 0 || now > 253402300799 {
		return 0, ErrInvariant
	}
	return now, nil
}

func bindingKey(userID int64, sessionID, sessionBinding string) (leaseBindingKey, bool) {
	if userID <= 0 || !db.ValidateOpaqueID(sessionID, "ll_") || sessionBinding == "" || len(sessionBinding) > 256 {
		return leaseBindingKey{}, false
	}
	return leaseBindingKey{userID: userID, sessionID: sessionID, binding: sha256.Sum256([]byte(sessionBinding))}, true
}

func (service *Service) rememberLease(userID int64, sessionID, sessionBinding, leaseID string, expiresAt int64) error {
	key, ok := bindingKey(userID, sessionID, sessionBinding)
	if !ok || !db.ValidateOpaqueID(leaseID, "gle_") {
		return ErrInvariant
	}
	service.leaseMu.Lock()
	for existing := range service.leases {
		if existing.userID == userID && existing.sessionID == sessionID {
			delete(service.leases, existing)
		}
	}
	service.leases[key] = leaseBinding{leaseID: leaseID, expiresAt: expiresAt}
	service.leaseMu.Unlock()
	return nil
}

func (service *Service) forgetExpiredLeases(now int64) {
	service.leaseMu.Lock()
	for key, lease := range service.leases {
		if lease.expiresAt <= now {
			delete(service.leases, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) boundLease(userID int64, sessionID, sessionBinding string, now int64) (string, bool) {
	key, ok := bindingKey(userID, sessionID, sessionBinding)
	if !ok {
		return "", false
	}
	service.leaseMu.RLock()
	bound, exists := service.leases[key]
	service.leaseMu.RUnlock()
	return bound.leaseID, exists && bound.expiresAt > now
}

func (service *Service) forgetSession(sessionID string) {
	service.leaseMu.Lock()
	for key := range service.leases {
		if key.sessionID == sessionID {
			delete(service.leases, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) forgetUser(userID int64) {
	service.leaseMu.Lock()
	for key := range service.leases {
		if key.userID == userID {
			delete(service.leases, key)
		}
	}
	service.leaseMu.Unlock()
}

func (service *Service) readSnapshot(ctx context.Context, tx *sql.Tx) (game.ConfigSnapshot, error) {
	keys := game.SiteConfigKeys()
	arguments := make([]any, len(keys))
	for index := range keys {
		arguments[index] = keys[index]
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key IN (`+placeholders(len(keys))+`)`, arguments...)
	if err != nil {
		return game.ConfigSnapshot{}, classifyDB(err)
	}
	defer rows.Close()
	values := make(map[string]string, len(keys))
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil || !value.Valid {
			return game.ConfigSnapshot{}, ErrInvariant
		}
		values[key] = value.String
	}
	if err := rows.Err(); err != nil {
		return game.ConfigSnapshot{}, classifyDB(err)
	}
	if len(values) != len(keys) {
		return game.ConfigSnapshot{}, ErrInvariant
	}
	snapshot, err := game.CompileConfig(values)
	if err != nil {
		return game.ConfigSnapshot{}, ErrInvariant
	}
	return snapshot, nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return "?" + strings.Repeat(",?", count-1)
}

func maintenanceEnabled(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return false, classifyDB(err)
	}
	if enabled != 0 && enabled != 1 {
		return false, ErrInvariant
	}
	return enabled == 1, nil
}

func classifyDB(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "database is locked") || strings.Contains(text, "database is busy") || strings.Contains(text, "sqlite_busy") || errors.Is(err, ledger.ErrRetryable) {
		return ErrServiceUnavailable
	}
	return err
}

func mapAuthorization(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, resources.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, resources.ErrMaintenance):
		return ErrMaintenance
	default:
		return classifyDB(err)
	}
}

func mapIdempotency(err error) error {
	switch {
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return ErrConflict
	case errors.Is(err, idempotency.ErrState):
		return ErrInvariant
	default:
		return classifyDB(err)
	}
}

func mapLimiter(err error) error {
	switch {
	case errors.Is(err, game.ErrStartRateLimited):
		return ErrRateLimited
	case errors.Is(err, game.ErrUserDeleting):
		return ErrForbidden
	case errors.Is(err, game.ErrStartCapacity), errors.Is(err, game.ErrStartClosed):
		return ErrServiceUnavailable
	default:
		return err
	}
}

func mapLedger(err error) error {
	switch {
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrInsufficientCredits
	case errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
		return ErrServiceUnavailable
	case errors.Is(err, ledger.ErrInvariant), errors.Is(err, ledger.ErrInvalidPlan), errors.Is(err, ledger.ErrInvalidReservation):
		return ErrInvariant
	default:
		return classifyDB(err)
	}
}

func actorHash(userID int64) ([32]byte, error) {
	return idempotency.ActorScopeHash("user", fmt.Sprintf("%d", userID))
}
