// Package host coordinates explicitly registered game capabilities.
package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type WorkResult struct {
	Processed int
	More      bool
}
type Finalizer interface {
	Commit() bool
	Abort() bool
}

type AdminAuthorizer interface {
	AuthorizeAdminMutation(context.Context, *sql.Tx) error
}
type AdminAuthorizerFunc func(context.Context, *sql.Tx) error

func (fn AdminAuthorizerFunc) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx) error {
	return fn(ctx, tx)
}

type AdminRegistrar interface {
	RegisterAdminRoute(string, string, http.Handler) error
}
type ContinuationRegistrar interface {
	Register(maintenance.ContinuationKind, maintenance.ContinuationRegistration) error
}
type Registrars struct {
	User         resources.UserRouteRegistrar
	Admin        AdminRegistrar
	Continuation resources.ContinuationUserRouteRegistrar
	Maintenance  ContinuationRegistrar
}

// Module contains mandatory narrow capabilities. A factory returns no active
// worker: all modules validate and recover before the host starts any worker.
type Module struct {
	ValidatePersistedState func(context.Context) error
	RecoverBeforeListen    func(context.Context, int64, int, time.Time) (WorkResult, error)
	RegisterRoutes         func(Registrars) error
	StartWorker            func(context.Context) error
	Close                  func() error
	Available              func(string, string) bool
	ReadyTx                func(context.Context, *sql.Tx) bool
	UserSnapshotTx         func(context.Context, *sql.Tx, int64, int64, game.ConfigValue) (game.UserSnapshot, error)
	HomeSummaryTx          func(context.Context, *sql.Tx, int64) (game.HomeSummary, error)
	ActiveCountsTx         func(context.Context, *sql.Tx) (game.ActiveCounts, error)
	ExportTx               func(context.Context, *sql.Tx, int64, int64, int) (any, Finalizer, error)
	PrepareDeleteTx        func(context.Context, *sql.Tx, int64, int64) (Finalizer, error)
	Retain                 func(context.Context, int64, int, time.Time) (WorkResult, error)
}

func (module *Module) complete() bool {
	return module != nil && module.ValidatePersistedState != nil && module.RecoverBeforeListen != nil && module.RegisterRoutes != nil && module.StartWorker != nil && module.Close != nil && module.Available != nil && module.ReadyTx != nil && module.UserSnapshotTx != nil && module.HomeSummaryTx != nil && module.ActiveCountsTx != nil && module.ExportTx != nil && module.PrepareDeleteTx != nil && module.Retain != nil
}

// Services are shared by all factories. Module callbacks borrow transactions;
// they must neither commit them nor retain them beyond the synchronous call.
type Services struct {
	Database        *sql.DB
	UserAuthorizer  resources.FinalTxAuthorizer
	AdminAuthorizer AdminAuthorizer
	Limiter         *game.StartLimiter
	Now             func() time.Time
}
type Factory func(Services) (*Module, error)
type Options struct {
	Database        *sql.DB
	Registry        *game.Registry
	Factories       map[string]Factory
	UserAuthorizer  resources.FinalTxAuthorizer
	AdminAuthorizer AdminAuthorizer
	Now             func() time.Time
}
type Service struct {
	services         Services
	registry         *game.Registry
	modules          map[string]*Module
	operationMu      sync.Mutex
	closed           atomic.Bool
	validated        bool
	workerStarted    bool
	routesRegistered bool
	recovered        map[string]bool
}

func New(options Options) (*Service, error) {
	if options.Database == nil || !options.Registry.Sealed() || options.UserAuthorizer == nil || options.AdminAuthorizer == nil {
		return nil, ErrInvalidRequest
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if !validTime(options.Now().UTC().Unix()) {
		return nil, ErrInvalidRequest
	}
	if len(options.Factories) != len(options.Registry.Descriptors()) {
		return nil, game.ErrInvalidContract
	}
	for _, descriptor := range options.Registry.Descriptors() {
		if options.Factories[descriptor.ID] == nil {
			return nil, game.ErrInvalidContract
		}
		for _, route := range descriptor.Routes {
			if reservedRoute(route) {
				return nil, game.ErrInvalidContract
			}
		}
	}
	limiter, err := game.NewStartLimiter(game.StartLimiterConfig{Now: options.Now})
	if err != nil {
		return nil, err
	}
	service := &Service{services: Services{Database: options.Database, UserAuthorizer: options.UserAuthorizer, AdminAuthorizer: options.AdminAuthorizer, Limiter: limiter, Now: options.Now}, registry: options.Registry, modules: make(map[string]*Module), recovered: make(map[string]bool)}
	for _, descriptor := range options.Registry.Descriptors() {
		module, err := options.Factories[descriptor.ID](service.services)
		if err != nil || !module.complete() {
			if module != nil && module.Close != nil {
				_ = module.Close()
			}
			_ = service.Close()
			if err == nil {
				err = game.ErrInvalidContract
			}
			return nil, fmt.Errorf("game host: construct %s: %w", descriptor.ID, err)
		}
		owned := *module
		service.modules[descriptor.ID] = &owned
	}
	return service, nil
}

func (service *Service) Limiter() *game.StartLimiter {
	if service == nil {
		return nil
	}
	return service.services.Limiter
}

func (service *Service) ValidatePersistedState(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalidRequest
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	if service.closed.Load() {
		return ErrClosed
	}
	for _, descriptor := range service.registry.Descriptors() {
		if err := service.modules[descriptor.ID].ValidatePersistedState(ctx); err != nil {
			return fmt.Errorf("game host: validate %s: %w", descriptor.ID, err)
		}
	}
	service.validated = true
	return nil
}

// RecoverModule retains the coordinator's bounded, resumable recovery calls.
// Workers can start only once every registered module has reported completion.
func (service *Service) RecoverModule(ctx context.Context, id string, now int64, limit int, deadline time.Time) (WorkResult, error) {
	if service == nil || ctx == nil {
		return WorkResult{}, ErrInvalidRequest
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	if service.closed.Load() || !service.validated {
		return WorkResult{}, ErrClosed
	}
	module, ok := service.modules[id]
	if !ok || !validWork(now, limit, deadline) {
		return WorkResult{}, ErrInvalidRequest
	}
	result, err := module.RecoverBeforeListen(ctx, now, limit, deadline)
	if err != nil {
		return WorkResult{}, err
	}
	if result.Processed < 0 || result.Processed > limit {
		return WorkResult{}, ErrInvariant
	}
	service.recovered[id] = !result.More
	return result, nil
}

func (service *Service) StartWorker(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalidRequest
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	if service.closed.Load() || service.workerStarted || !service.validated {
		return ErrClosed
	}
	for _, descriptor := range service.registry.Descriptors() {
		if !service.recovered[descriptor.ID] {
			return ErrServiceUnavailable
		}
	}
	for _, descriptor := range service.registry.Descriptors() {
		if err := service.modules[descriptor.ID].StartWorker(ctx); err != nil {
			_ = service.closeLocked()
			return err
		}
	}
	service.workerStarted = true
	return nil
}

func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.closeLocked()
}

func (service *Service) closeLocked() error {
	if service.closed.Load() {
		return nil
	}
	service.closed.Store(true)
	var failures []error
	descriptors := service.registry.Descriptors()
	for i := len(descriptors) - 1; i >= 0; i-- {
		if module := service.modules[descriptors[i].ID]; module != nil {
			failures = append(failures, module.Close())
		}
	}
	failures = append(failures, service.services.Limiter.Close())
	return errors.Join(failures...)
}

func validTime(value int64) bool { return value >= 0 && value <= 253402300799 }
func validWork(now int64, limit int, deadline time.Time) bool {
	return validTime(now) && limit > 0 && limit <= 10000 && !deadline.IsZero()
}
