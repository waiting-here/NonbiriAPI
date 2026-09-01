package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	firstRetryDelay = 120 * time.Second
	workerBatchSize = 100
	rankBatchSize   = 100
	rankBudget      = 2 * time.Second
	rankWindow      = 30 * 24 * time.Hour
)

type Options struct {
	Store             *db.Store
	UserAuthorizer    resources.FinalTxAuthorizer
	AdminAuthorizer   AdminFinalAuthorizer
	Limiter           *game.StartLimiter
	Random            fishing.IntSource
	Now               func() time.Time
	GenerateID        func(string) (string, error)
	LeaderboardTieKey []byte
	Capability        game.RuntimeCapability
	RPSHealth         RPSHealthProbe
	WorkerInterval    time.Duration
	BudgetNow         func() time.Time
}

type Service struct {
	database          *sql.DB
	userAuthorizer    resources.FinalTxAuthorizer
	adminAuthorizer   AdminFinalAuthorizer
	limiter           *game.StartLimiter
	random            fishing.IntSource
	now               func() time.Time
	generateID        func(string) (string, error)
	leaderboardTieKey []byte
	capability        game.RuntimeCapability
	rpsHealth         RPSHealthProbe
	workerInterval    time.Duration
	budgetNow         func() time.Time

	rngMu             sync.Mutex
	workerMu          sync.Mutex
	workerCancel      context.CancelFunc
	workerDone        chan struct{}
	wake              chan struct{}
	recoveryValidated atomic.Bool
	recovered         atomic.Bool
	closed            atomic.Bool

	// Narrow deterministic fault/transition seams used only by package tests.
	beforeSettlement       func(string) error
	afterSettlementFailure func(string)
}

func New(options Options) (*Service, error) {
	if options.Store == nil || options.Store.DB() == nil || options.UserAuthorizer == nil {
		return nil, errors.New("game runtime: store and final user authorizer are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.GenerateID == nil {
		options.GenerateID = db.GenerateOpaqueID
	}
	if options.Random == nil {
		options.Random = fishing.CryptoSource{}
	}
	if options.Limiter == nil {
		limiter, err := game.NewStartLimiter(game.StartLimiterConfig{Now: options.Now})
		if err != nil {
			return nil, err
		}
		options.Limiter = limiter
	}
	if options.WorkerInterval == 0 {
		options.WorkerInterval = time.Second
	}
	if options.WorkerInterval < time.Millisecond || options.WorkerInterval > time.Minute {
		return nil, errors.New("game runtime: invalid worker interval")
	}
	if options.BudgetNow == nil {
		options.BudgetNow = time.Now
	}
	if len(options.LeaderboardTieKey) != 32 {
		return nil, errors.New("game runtime: leaderboard key is required")
	}
	key := append([]byte(nil), options.LeaderboardTieKey...)
	return &Service{
		database: options.Store.DB(), userAuthorizer: options.UserAuthorizer,
		adminAuthorizer: options.AdminAuthorizer, limiter: options.Limiter,
		random: options.Random, now: options.Now, generateID: options.GenerateID,
		leaderboardTieKey: key, capability: options.Capability, rpsHealth: options.RPSHealth,
		workerInterval: options.WorkerInterval, budgetNow: options.BudgetNow,
		wake: make(chan struct{}, 1),
	}, nil
}

func (service *Service) Close() error {
	if service == nil || !service.closed.CompareAndSwap(false, true) {
		return nil
	}
	service.workerMu.Lock()
	cancel, done := service.workerCancel, service.workerDone
	service.workerCancel = nil
	service.workerDone = nil
	service.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if service.limiter != nil {
		service.limiter.Close()
	}
	clear(service.leaderboardTieKey)
	return nil
}

// RecoverBeforeListen validates every capacity reservation and makes all due
// Fishing continuations converge before route listeners are exposed.
func (service *Service) RecoverBeforeListen(ctx context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	service.recovered.Store(false)
	if err := service.ValidatePersistedState(ctx); err != nil {
		return err
	}
	for {
		processed, runErr := service.runDue(ctx, service.now().UTC().Unix(), true)
		if runErr != nil {
			return runErr
		}
		if processed < workerBatchSize {
			break
		}
	}
	service.recovered.Store(true)
	return nil
}

func (service *Service) StartWorker(parent context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	if !service.recovered.Load() {
		return errors.New("game runtime: recovery must complete before worker start")
	}
	service.workerMu.Lock()
	defer service.workerMu.Unlock()
	if service.workerCancel != nil {
		return errors.New("game runtime: worker already started")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	service.workerCancel, service.workerDone = cancel, done
	go service.workerLoop(ctx, done)
	return nil
}

func (service *Service) workerLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(service.workerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-service.wake:
		}
		_, _ = service.runDue(ctx, service.now().UTC().Unix(), true)
		now := service.now().UTC().Unix()
		if err := service.expireRankFacts(ctx, now, service.budgetNow().Add(rankBudget)); err == nil {
			_, _ = service.cleanupTerminalBatches(ctx, now)
		}
	}
}

func (service *Service) signalWorker() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) readSnapshot(ctx context.Context, tx *sql.Tx) (game.ConfigSnapshot, int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM site_config WHERE key IN (`+placeholders(len(game.SiteConfigKeys()))+`)`, stringArgs(game.SiteConfigKeys())...)
	if err != nil {
		return game.ConfigSnapshot{}, 0, classifyDB(err)
	}
	values := make(map[string]string, len(game.SiteConfigKeys()))
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return game.ConfigSnapshot{}, 0, classifyDB(err)
		}
		if !value.Valid {
			rows.Close()
			return game.ConfigSnapshot{}, 0, ErrInvariant
		}
		values[key] = value.String
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return game.ConfigSnapshot{}, 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return game.ConfigSnapshot{}, 0, classifyDB(err)
	}
	if len(values) != len(game.SiteConfigKeys()) {
		return game.ConfigSnapshot{}, 0, ErrInvariant
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM config_revisions WHERE domain='games'`).Scan(&revision); err != nil {
		return game.ConfigSnapshot{}, 0, classifyDB(err)
	}
	snapshot, err := game.CompileConfig(values)
	if err != nil {
		return game.ConfigSnapshot{}, 0, ErrInvariant
	}
	return snapshot, revision, nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	result := "?"
	for index := 1; index < count; index++ {
		result += ",?"
	}
	return result
}

func stringArgs(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
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

func formatWideMilli(value *big.Int) string {
	if value == nil {
		return "0"
	}
	negative := value.Sign() < 0
	absolute := new(big.Int).Abs(new(big.Int).Set(value))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(absolute, big.NewInt(1000), remainder)
	text := quotient.String()
	if remainder.Sign() != 0 {
		fraction := fmt.Sprintf("%03d", remainder.Int64())
		fraction = strings.TrimRight(fraction, "0")
		text += "." + fraction
	}
	if negative {
		return "-" + text
	}
	return text
}
