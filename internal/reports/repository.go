package reports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type Repository struct {
	db          *sql.DB
	connectors  *connector.Registry
	baseURLs    BaseURLValidator
	authorizer  *authz.Authorizer
	deleteKey   EndpointKeyDeletionFunc
	issues      IssueProjectionHook
	keys        *reportKeys
	cursors     *cursorCodec
	now         func() time.Time
	delay       DelayFunc
	generateID  func(string) (string, error)
	publicSlots chan struct{}
	interval    time.Duration
	workerLimit time.Duration
	kick        chan struct{}

	lifecycleMu sync.Mutex
	active      sync.WaitGroup
	closed      bool
	closeDone   chan struct{}

	workerMu     sync.Mutex
	workerCancel context.CancelFunc
	workerDone   chan struct{}
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func defaultDelay(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return context.Cause(ctx)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func New(config Config) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || config.Connectors == nil || nilInterface(config.BaseURLs) ||
		nilInterface(config.KeyDeriver) || config.Authorizer == nil || config.DeleteKey == nil ||
		nilInterface(config.IssueProjection) {
		return nil, errors.New("reports: complete dependencies are required")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Delay == nil {
		config.Delay = defaultDelay
	}
	if config.GenerateID == nil {
		config.GenerateID = db.GenerateOpaqueID
	}
	if config.WorkerInterval == 0 {
		config.WorkerInterval = defaultWorkerInterval
	}
	if config.WorkerInterval < 0 || config.WorkerInterval > 24*time.Hour {
		return nil, errors.New("reports: invalid worker interval")
	}
	keys, err := newReportKeys(config.KeyDeriver, config.Random)
	if err != nil {
		return nil, err
	}
	cursors, err := newCursorCodec(config.KeyDeriver)
	if err != nil {
		_ = keys.Close()
		return nil, err
	}
	if cursors.key == keys.fingerprint || cursors.key == keys.idempotency || cursors.key == keys.target ||
		cursors.key == keys.sourceIP || cursors.key == keys.rate {
		_ = cursors.Close()
		_ = keys.Close()
		return nil, errors.New("reports: purpose-bound subkeys must be distinct")
	}
	repository := &Repository{
		db: config.Store.DB(), connectors: config.Connectors, baseURLs: config.BaseURLs, authorizer: config.Authorizer,
		deleteKey: config.DeleteKey, issues: config.IssueProjection, keys: keys, cursors: cursors, now: config.Now,
		delay: config.Delay, generateID: config.GenerateID,
		publicSlots: make(chan struct{}, publicConcurrency), interval: config.WorkerInterval,
		workerLimit: workerTransactionLimit,
		kick:        make(chan struct{}, 1), closeDone: make(chan struct{}),
	}
	return repository, nil
}

func (repository *Repository) admit() error {
	if repository == nil {
		return ErrClosed
	}
	repository.lifecycleMu.Lock()
	defer repository.lifecycleMu.Unlock()
	if repository.closed {
		return ErrClosed
	}
	repository.active.Add(1)
	return nil
}

func (repository *Repository) release() { repository.active.Done() }

func (repository *Repository) nowUnix() (int64, error) {
	if repository == nil || repository.now == nil {
		return 0, ErrUnavailable
	}
	now := repository.now().Unix()
	if now < 0 || now > maxUnixSecond-caseRetentionSeconds {
		return 0, ErrUnavailable
	}
	return now, nil
}

func (repository *Repository) beginTx(ctx context.Context) (*sql.Tx, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func rollbackUnlessCommitted(tx *sql.Tx, committed *bool) {
	if tx != nil && committed != nil && !*committed {
		_ = tx.Rollback()
	}
}

func commit(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	*committed = true
	return nil
}

func (repository *Repository) notifyWorker() {
	if repository == nil {
		return
	}
	select {
	case repository.kick <- struct{}{}:
	default:
	}
}

func (repository *Repository) Close() error {
	if repository == nil {
		return nil
	}
	repository.lifecycleMu.Lock()
	if repository.closed {
		done := repository.closeDone
		repository.lifecycleMu.Unlock()
		<-done
		return nil
	}
	repository.closed = true
	repository.lifecycleMu.Unlock()

	repository.workerMu.Lock()
	cancel := repository.workerCancel
	done := repository.workerDone
	repository.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	repository.active.Wait()
	first := repository.keys.Close()
	if err := repository.cursors.Close(); first == nil {
		first = err
	}
	close(repository.closeDone)
	return first
}
