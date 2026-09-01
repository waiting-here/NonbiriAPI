package rps

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	workerBatchSize      = 100
	actionLimit          = 30
	actionWindowDuration = time.Minute
)

type ContinuationAuthorizer interface {
	AuthorizeContinuation(context.Context, *sql.Tx, maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error)
}

type PoolRepository interface {
	WelfareDestination(context.Context, *sql.Tx) (activities.PoolDestination, error)
	ThursdayDestination(context.Context, *sql.Tx, int64) (activities.PoolDestination, error)
	RecordPoolTransfers(context.Context, *sql.Tx, int64, ...activities.PoolDestination) (activities.PublishFacts, error)
}

type AccountEventSink interface {
	PublishCommitted(context.Context, int64, accountstream.PublishedEvent) (accountstream.Frame, error)
	// ForgetAccounts permanently tombstones deleted accounts after synchronously
	// closing subscriptions and removing retained frames.
	ForgetAccounts(context.Context, []int64) error
	// DiscardAccounts synchronously closes subscriptions and removes retained
	// frames without tombstoning live accounts, so a later purge may rebuild them.
	DiscardAccounts(context.Context, []int64) error
	PurgeAccounts(context.Context, []int64) error
}

type ActivityPublisher interface {
	Publish(context.Context, activities.PublishFacts) error
}

type PublishErrorReporter interface {
	ReportRPSPublishError(error)
}

type Options struct {
	Store          *db.Store
	UserAuthorizer resources.FinalTxAuthorizer
	Continuation   ContinuationAuthorizer
	Limiter        *game.StartLimiter
	Pools          PoolRepository
	AccountEvents  AccountEventSink
	ActivityEvents ActivityPublisher
	Keys           KeyDeriver
	Random         io.Reader
	Now            func() time.Time
	GenerateID     func(string) (string, error)
	HealthEpoch    int64
	WorkerInterval time.Duration
	PublishErrors  PublishErrorReporter
}

type actionWindow struct {
	events []*actionEvent
}

type actionEvent struct {
	at time.Time
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
	pools          PoolRepository
	accountEvents  AccountEventSink
	activityEvents ActivityPublisher
	publishErrors  PublishErrorReporter
	keys           cryptoKeys
	random         io.Reader
	now            func() time.Time
	generateID     func(string) (string, error)
	healthEpoch    int64
	workerInterval time.Duration

	actionMu      sync.Mutex
	actionWindows map[int64]*actionWindow
	randomMu      sync.Mutex
	leaseMu       sync.RWMutex
	leaseBindings map[leaseBindingKey]leaseBinding

	workerMu          sync.Mutex
	workerCancel      context.CancelFunc
	workerDone        chan struct{}
	recoveryValidated atomic.Bool
	recovered         atomic.Bool
	closed            atomic.Bool

	// Narrow deterministic seams used by transaction rollback tests.
	beforeTerminalCommit func() error
	beforeMatchCommit    func() error
}

func New(options Options) (*Service, error) {
	if options.Store == nil || options.Store.DB() == nil || options.UserAuthorizer == nil || options.Continuation == nil ||
		options.Limiter == nil || options.Pools == nil || options.AccountEvents == nil || options.ActivityEvents == nil || options.Keys == nil {
		return nil, errors.New("rps: store, authorization, continuation, limiter, pool, event, and key dependencies are required")
	}
	keys, err := deriveKeys(options.Keys)
	if err != nil {
		return nil, err
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.GenerateID == nil {
		options.GenerateID = db.GenerateOpaqueID
	}
	if options.HealthEpoch == 0 {
		options.HealthEpoch, err = randomHealthEpoch(options.Random)
		if err != nil {
			return nil, err
		}
	}
	if options.HealthEpoch < 1 {
		return nil, errors.New("rps: invalid health epoch")
	}
	if options.WorkerInterval == 0 {
		options.WorkerInterval = 250 * time.Millisecond
	}
	if options.WorkerInterval < 10*time.Millisecond || options.WorkerInterval > time.Minute {
		return nil, errors.New("rps: invalid worker interval")
	}
	return &Service{
		database: options.Store.DB(), userAuthorizer: options.UserAuthorizer, continuation: options.Continuation,
		limiter: options.Limiter, pools: options.Pools, accountEvents: options.AccountEvents,
		activityEvents: options.ActivityEvents, publishErrors: options.PublishErrors, keys: keys,
		random: options.Random, now: options.Now, generateID: options.GenerateID,
		healthEpoch: options.HealthEpoch, workerInterval: options.WorkerInterval,
		actionWindows: make(map[int64]*actionWindow), leaseBindings: make(map[leaseBindingKey]leaseBinding),
	}, nil
}

func randomHealthEpoch(random io.Reader) (int64, error) {
	var raw [8]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return 0, fmt.Errorf("rps: health epoch: %w", err)
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
	service.actionMu.Lock()
	clear(service.actionWindows)
	service.actionMu.Unlock()
	service.leaseMu.Lock()
	clear(service.leaseBindings)
	service.leaseMu.Unlock()
	clear(service.keys.device[:])
	clear(service.keys.ip[:])
	clear(service.keys.gesture[:])
	clear(service.keys.leaderboard[:])
	return nil
}

func (service *Service) Ready(context.Context, *sql.Tx) bool {
	return service != nil && !service.closed.Load() && service.recovered.Load()
}

func (service *Service) Available(gameID, mode, spec string) bool {
	if service == nil || service.closed.Load() || !service.recovered.Load() || gameID != game.RPSID || spec != "" {
		return false
	}
	return game.ResolveMode(game.RPSID, mode) == nil
}

func (service *Service) decisionNow() (int64, error) {
	if service == nil || service.now == nil || service.closed.Load() {
		return 0, ErrClosed
	}
	now := service.now().UTC().Unix()
	if now < 0 || now > 253402300799 {
		return 0, ErrInvariant
	}
	return now, nil
}

func (service *Service) generate(prefix string) (string, error) {
	value, err := service.generateID(prefix)
	if err != nil || !db.ValidateOpaqueID(value, prefix) {
		return "", ErrServiceUnavailable
	}
	return value, nil
}

func (service *Service) reserveAction(userID int64) (func(bool), error) {
	if userID <= 0 {
		return nil, ErrInvalidRequest
	}
	now := service.now()
	service.actionMu.Lock()
	window := service.actionWindows[userID]
	if window == nil {
		window = &actionWindow{}
		service.actionWindows[userID] = window
	}
	cutoff := now.Add(-actionWindowDuration)
	write := 0
	for _, event := range window.events {
		if event.at.After(cutoff) {
			window.events[write] = event
			write++
		}
	}
	window.events = window.events[:write]
	if len(window.events) >= actionLimit {
		service.actionMu.Unlock()
		return nil, ErrRateLimited
	}
	event := &actionEvent{at: now}
	window.events = append(window.events, event)
	service.actionMu.Unlock()
	var once sync.Once
	return func(commit bool) {
		once.Do(func() {
			if commit {
				return
			}
			service.actionMu.Lock()
			current := service.actionWindows[userID]
			if current != nil {
				for index, candidate := range current.events {
					if candidate != event {
						continue
					}
					copy(current.events[index:], current.events[index+1:])
					current.events = current.events[:len(current.events)-1]
					break
				}
				if len(current.events) == 0 {
					delete(service.actionWindows, userID)
				}
			}
			service.actionMu.Unlock()
		})
	}, nil
}

func (service *Service) forgetUserMemory(userID int64) {
	service.actionMu.Lock()
	delete(service.actionWindows, userID)
	service.actionMu.Unlock()
	service.leaseMu.Lock()
	for key := range service.leaseBindings {
		if key.userID == userID {
			delete(service.leaseBindings, key)
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
	return idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
}

func (service *Service) reportPublish(err error) {
	if err != nil && service.publishErrors != nil {
		service.publishErrors.ReportRPSPublishError(err)
	}
}
