package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/economyaudit"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// Runtime observers are installed before admission opens and share the existing
// request identity. They never create a second request or consume an API quota.
type auditRuntime struct {
	observations *observability.Repository
	risk         *riskaudit.Repository
	collector    *riskaudit.Collector
	economy      *economyaudit.Service
	access       *observability.AccessObserver
	flow         *flowcontrol.Controller
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
}

func newAuditRuntime(store *db.Store, vault *secret.Vault, authorizer *roleFinalTxAuthorizer) (*auditRuntime, error) {
	a := &auditRuntime{}
	var err error
	a.observations, err = observability.NewRepository(store.DB())
	if err != nil {
		return nil, err
	}
	a.risk, err = riskaudit.NewRepository(store.DB(), riskaudit.RepositoryOptions{
		FinalAuth:     authorizer,
		ConfigChanged: func(config riskaudit.Config) { a.collector.ApplyConfig(config) },
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := a.risk.CurrentConfig(ctx)
	if err != nil {
		return nil, err
	}
	a.collector, err = riskaudit.NewCollector(a.risk, riskaudit.CollectorOptions{Config: config, Boundary: func() {
		if a.flow != nil {
			a.flow.ObserveAuditBoundary()
		}
	}})
	if err != nil {
		return nil, err
	}
	a.economy, err = economyaudit.New(economyaudit.Config{Database: store.DB(), FinalAuth: authorizer, CursorKeys: vault})
	if err != nil {
		return nil, err
	}
	if err = a.observations.ReconcileCounters(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *auditRuntime) attachAccess(repository *resources.Repository) error {
	var err error
	a.access, err = observability.NewAccessObserver(a.observations, func(ctx context.Context, key string) (observability.AccessIdentity, error) {
		identity, err := repository.ResolveCallerKey(ctx, key)
		return observability.AccessIdentity{UserID: identity.UserID, Generation: identity.Generation}, err
	})
	return err
}

func (a *auditRuntime) classify(ctx context.Context, userID int64, kind string) {
	a.collector.Bind(requestattempt.CurrentID(ctx), userID, kind)
}

func (a *auditRuntime) configurationChanged(keys []string) {
	for _, key := range keys {
		if key == "global_rpm" || key == "default_rpm_per_user" {
			if a.flow != nil {
				a.flow.NotifyConfigurationChanged()
			}
			return
		}
	}
}

// Wrap belongs inside the host/proxy validation boundary. CaptureSource reads
// the already validated effective address and bounded, allowlisted headers.
func (a *auditRuntime) Wrap(next http.Handler) http.Handler {
	observed := a.access.Wrap(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := observability.WithSource(r.Context(), observability.CaptureSource(r))
		observed.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *auditRuntime) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.workers.Add(2)
	go func() { defer a.workers.Done(); a.collector.Run(ctx) }()
	go func() { defer a.workers.Done(); a.economy.Run(ctx) }()
}

func (a *auditRuntime) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.workers.Wait()
		if a.access != nil {
			a.closeErr = a.access.Close()
		}
	})
	return a.closeErr
}

func (a *auditRuntime) registerRoutes(runtime *auth.Runtime, logs *logapi.Repository, authorizer *roleFinalTxAuthorizer) error {
	for _, register := range []func() error{
		func() error { return logapi.RegisterAdminDiagnosticRoutes(runtime, logs, authorizer) },
		func() error { return logapi.RegisterStewardDiagnosticRoutes(runtime, logs, authorizer) },
		func() error { return riskaudit.RegisterAdminRoutes(runtime, a.risk) },
		func() error { return riskaudit.RegisterStewardRoutes(runtime, a.risk) },
		func() error { return economyaudit.RegisterRoutes(economyRouteRegistrar{runtime}, a.economy) },
	} {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}

type economyRouteRegistrar struct{ runtime *auth.Runtime }

func (r economyRouteRegistrar) RegisterAdminRoute(method, path string, handler economyaudit.AuthorizedAdminHandler) error {
	return r.runtime.RegisterAdminRoute(method, path, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession {
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "administrator session required"))
			return
		}
		handler(w, request, economyaudit.AdminPrincipal{UserID: actor.UserID})
	}))
}

type diagnosticRetention struct{ repository *observability.Repository }

func (r diagnosticRetention) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	result, err := r.repository.Retain(ctx, now, limit, deadline, nil)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

type riskRetention struct{ repository *riskaudit.Repository }

func (r riskRetention) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := r.repository.CleanupBatch(bounded, time.Unix(now, 0), limit)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

var _ lifecycle.RetentionAdapter = diagnosticRetention{}
var _ lifecycle.RetentionAdapter = riskRetention{}
