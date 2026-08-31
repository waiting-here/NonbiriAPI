// Command nonbiriapi opens a validated Generation 2 database and serves the
// two host-isolated station shells. Domain APIs are registered by their
// Generation 2 owners only after their persistence and lifecycle contracts
// are complete.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/applog"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/web"
)

func main() {
	os.Exit(run())
}

// run owns every startup resource. The listener is created only after db.Open
// has completed the Generation 2 snapshot, manifest, seed, credential and
// pre-listener recovery checks.
func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup configuration error:")
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	logger := applog.New(os.Stdout, applog.ParseLevel(cfg.LogLevel))
	slog.SetDefault(logger)

	secretVault := cfg.TakeSecretVault()
	if secretVault == nil {
		slog.Error("secret vault initialization failed")
		return 1
	}
	defer func() { _ = secretVault.Close() }()

	slog.Info("startup",
		"listen_addr", cfg.ListenAddr,
		"db_path", cfg.DBPath,
		"log_level", cfg.LogLevel,
		"user_host", cfg.UserHost,
		"admin_host", cfg.AdminHost,
		"master_source", cfg.MasterSource,
		"encryption_root_bytes", secret.MasterKeyBytes,
		"trusted_proxies", len(cfg.TrustedProxyCIDRs),
		"smtp_enabled", cfg.SMTP.Enabled,
	)

	store, err := db.Open(cfg.DBPath, secretVault)
	if err != nil {
		slog.Error("database open failed", "err", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	slog.Info("database ready", "path", cfg.DBPath)

	app, err := buildApplication(cfg, store, secretVault)
	if err != nil {
		slog.Error("application wiring failed", "err", err)
		return 1
	}
	defer func() { _ = app.Close() }()

	srv := &http.Server{
		Addr:        cfg.ListenAddr,
		Handler:     app.handler,
		ReadTimeout: 15 * time.Second,
		// Streaming owners install their own bounded write deadlines when their
		// routes are registered. A zero server WriteTimeout remains intentional.
		IdleTimeout: 60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.ListenAddr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped with error", "err", err)
		} else {
			slog.Error("http server stopped before shutdown completed")
		}
		return 1
	case err := <-app.failures:
		slog.Error("application background worker failed", "err", err)
		return 1
	}
	slog.Info("shutdown initiated")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "err", err)
		return 1
	}
	slog.Info("shutdown complete")
	return 0
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func requestCarriesBody(r *http.Request) bool {
	return r != nil && (r.ContentLength != 0 || len(r.TransferEncoding) != 0)
}

func requestHasQuery(r *http.Request) bool {
	return r == nil || r.URL == nil || r.URL.ForceQuery || r.URL.RawQuery != ""
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "not found"))
}

// servePublicConfig is the only Generation 2 API mounted at the atomic
// baseline. The administrator projection is intentionally absent until its
// authenticated owner registers it.
func servePublicConfig(store *db.Store, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	out, err := adminapi.ReadPublicConfig(store)
	if err != nil {
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "service unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, out)
}

func freshSafeMux(cfg *config.Config, store *db.Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)

	userAPI := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store != nil && r.URL.Path == "/api/config" {
			servePublicConfig(store, w, r)
			return
		}
		apiNotFound(w, r)
	}))
	adminAPI := httpmw.API(http.HandlerFunc(apiNotFound))
	mux.Handle("/api", userAPI)
	mux.Handle("/api/", userAPI)
	mux.Handle("/v1", userAPI)
	mux.Handle("/v1/", userAPI)
	mux.Handle("/admin/api", adminAPI)
	mux.Handle("/admin/api/", adminAPI)
	mux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return mux
}

func generationTwoMux(cfg *config.Config, store *db.Store, authRuntime *auth.Runtime) (*http.ServeMux, error) {
	if cfg == nil || store == nil || authRuntime == nil {
		return nil, errors.New("Generation 2 HTTP dependencies are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)

	publicConfig := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		servePublicConfig(store, w, r)
	}))
	mux.Handle("/api/config", publicConfig)

	userAuth := authRuntime.UserHandler()
	mux.Handle("/api", userAuth)
	mux.Handle("/api/", userAuth)

	callerAPI := httpmw.API(http.HandlerFunc(apiNotFound))
	mux.Handle("/v1", callerAPI)
	mux.Handle("/v1/", callerAPI)

	// Administrator routes deliberately bypass the maintenance admission gate.
	// The authentication runtime still enforces the admin host, password,
	// credential generation, live session and final-transaction authorization.
	adminAuth := authRuntime.AdminHandler()
	mux.Handle("/admin/api", adminAuth)
	mux.Handle("/admin/api/", adminAuth)

	mux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return mux, nil
}

func stationBoundary(cfg *config.Config, next http.Handler) (http.Handler, error) {
	if cfg == nil || next == nil {
		return nil, errors.New("HTTP boundary dependencies are required")
	}
	return httpmw.New(httpmw.Config{
		UserHost:          cfg.UserHost,
		AdminHost:         cfg.AdminHost,
		SiteBaseURL:       cfg.SiteBaseURL,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}, next)
}

// newHTTPHandler remains the boundary-only constructor used by tests and
// embedded-shell callers. Without a validated store it deliberately exposes
// no API other than healthz.
func newHTTPHandler(cfg *config.Config) (http.Handler, error) {
	if cfg == nil {
		return nil, errors.New("configuration is required")
	}
	return stationBoundary(cfg, freshSafeMux(cfg, nil))
}

type application struct {
	handler         http.Handler
	authRuntime     *auth.Runtime
	bridge          *resourcebridge.Runtime
	claims          *claim.Service
	resourceRepo    *resources.Repository
	discoveryWorker *resources.DiscoveryWorkerPool
	donations       *donation.Service
	charity         *charity.Service
	charityRouting  *charityrouting.Service
	activities      *activities.Service
	activityRepo    *activities.Repository
	activityEvents  *accountstream.Hub
	activityWorker  *activities.SettlementWorker
	activityCancel  context.CancelFunc
	activityDone    <-chan struct{}
	failures        <-chan error
	authorizer      *authz.Authorizer
	elevation       *elevation.Manager
	gate            *maintenance.Gate
	registry        *maintenance.Registry
	maintenance     *maintenance.Service
	egress          *egress.Stack

	closeOnce sync.Once
	closeErr  error
}

func (a *application) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		var closeErrors []error
		if a.activityCancel != nil {
			a.activityCancel()
		}
		if a.activityWorker != nil {
			a.activityWorker.Close()
		}
		if a.activityDone != nil {
			<-a.activityDone
		}
		if a.activityEvents != nil {
			if err := a.activityEvents.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.authRuntime != nil {
			if err := a.authRuntime.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.discoveryWorker != nil {
			a.discoveryWorker.Close()
		}
		if a.bridge != nil {
			if err := a.bridge.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.egress != nil {
			a.egress.CloseIdleConnections()
		}
		a.closeErr = errors.Join(closeErrors...)
	})
	return a.closeErr
}

const (
	discoveryWorkerMaxConcurrent = 4
	discoveryWorkerMaxAdmitted   = 32
	discoveryWorkerTimeout       = egress.DefaultRequestTimeout
)

// roleFinalTxAuthorizer adapts the request actor established by auth.Runtime
// to the exact live role checks owned by authz.Authorizer. It carries no
// cached authorization result: every domain call supplies its own final
// transaction and revalidates the session, account and role in that tx.
type roleFinalTxAuthorizer struct {
	authorizer *authz.Authorizer
}

var _ donation.RoleFinalTxAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ activities.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleSteward)
}

func (authorizer *roleFinalTxAuthorizer) authorize(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind authz.ActorKind,
	role authz.Role,
) error {
	if authorizer == nil || authorizer.authorizer == nil || ctx == nil || tx == nil {
		return errors.New("role final-transaction authorization unavailable")
	}
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != kind || actor.UserID != userID {
		return authz.ErrUnauthorized
	}
	_, err := authorizer.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return err
}

type activityRouteRegistrar struct{ runtime *auth.Runtime }

var _ activities.UserRouteRegistrar = activityRouteRegistrar{}
var _ activities.AdminRouteRegistrar = activityRouteRegistrar{}

func (registrar activityRouteRegistrar) RegisterUserRoute(method, pattern string, handler activities.AuthorizedUserHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
		handler(writer, request, activities.UserPrincipal{UserID: principal.UserID})
	})
}

func (registrar activityRouteRegistrar) RegisterAdminRoute(method, pattern string, handler activities.AuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, activities.AdminPrincipal{UserID: actor.UserID})
	}))
}

type activityMutationGate struct{}

var _ activities.UserMutationGate = activityMutationGate{}

func (activityMutationGate) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, userID int64) error {
	if ctx == nil || tx == nil || userID <= 0 {
		return activities.ErrUnauthorized
	}
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return fmt.Errorf("read maintenance state: %w", err)
	}
	switch enabled {
	case 0:
		return nil
	case 1:
		return activities.ErrMaintenance
	default:
		return activities.ErrInvariant
	}
}

type activityPublishReporter struct{}

func (activityPublishReporter) ReportActivitiesPublishError(err error) {
	if err != nil {
		slog.Error("activity account event publication failed", "err", err)
	}
}

func buildApplication(cfg *config.Config, store *db.Store, vault *secret.Vault) (*application, error) {
	if cfg == nil || store == nil || vault == nil {
		return nil, errors.New("application dependencies are required")
	}

	elevationManager, err := elevation.NewManager()
	if err != nil {
		return nil, fmt.Errorf("create elevation manager: %w", err)
	}
	authorizer := authz.New(authz.Options{Elevation: elevationManager})
	gate := maintenance.NewGate()
	registry := maintenance.NewRegistry()
	maintenanceService, err := maintenance.NewService(maintenance.ServiceOptions{
		Authorizer: authorizer,
		Gate:       gate,
		Registry:   registry,
	})
	if err != nil {
		_ = elevationManager.Close()
		return nil, fmt.Errorf("create maintenance service: %w", err)
	}

	outbound, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		_ = elevationManager.Close()
		return nil, fmt.Errorf("create egress stack: %w", err)
	}
	var bridgeRuntime *resourcebridge.Runtime
	var discoveryWorker *resources.DiscoveryWorkerPool
	var authRuntime *auth.Runtime
	var activityEvents *accountstream.Hub
	var activityWorker *activities.SettlementWorker
	cleanup := func() {
		if activityWorker != nil {
			activityWorker.Close()
		}
		if activityEvents != nil {
			_ = activityEvents.Close()
		}
		if authRuntime != nil {
			_ = authRuntime.Close()
		} else {
			_ = elevationManager.Close()
		}
		if discoveryWorker != nil {
			discoveryWorker.Close()
		}
		if bridgeRuntime != nil {
			_ = bridgeRuntime.Close()
		}
		outbound.CloseIdleConnections()
	}

	startupContext := context.Background()
	if err := outbound.AddSelfOrigins(startupContext, cfg); err != nil {
		cleanup()
		return nil, fmt.Errorf("register egress self origins: %w", err)
	}
	localBackend, err := backend.NewLocal(outbound)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create local backend: %w", err)
	}
	discordProvider, err := auth.NewHTTPDiscordProvider(auth.HTTPDiscordProviderConfig{
		ClientID:     cfg.DiscordClientID,
		ClientSecret: cfg.DiscordClientSecret,
		Scopes:       cfg.DiscordOAuthScopes,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create Discord authentication provider: %w", err)
	}
	authRuntime, err = auth.NewRuntime(auth.RuntimeConfig{
		Store:                store,
		Provider:             discordProvider,
		DiscordClientID:      cfg.DiscordClientID,
		UserSiteBaseURL:      cfg.SiteBaseURL,
		AdminUsername:        cfg.AdminUsername,
		AdminPassword:        cfg.AdminPassword,
		CredentialKeyDeriver: vault,
		Authorizer:           authorizer,
		Maintenance:          gate,
		Elevation:            elevationManager,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create authentication runtime: %w", err)
	}
	roleAuthorizer := &roleFinalTxAuthorizer{authorizer: authorizer}
	donationService, err := donation.New(donation.Config{
		Store:      store,
		OwnerAuth:  authRuntime,
		RoleAuth:   roleAuthorizer,
		CursorKeys: vault,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create donation service: %w", err)
	}
	charityService, err := charity.New(charity.Config{
		Store:       store,
		KeyDeletion: donationService,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create charity service: %w", err)
	}
	claimService, err := claim.New(claim.Dependencies{
		DB:         store.DB(),
		Secrets:    vault,
		Accounting: claim.NewLedgerAccounting(),
		Charity:    charityService,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create claim service: %w", err)
	}
	if err := recoverClaimsBeforeListener(startupContext, claimService); err != nil {
		cleanup()
		return nil, err
	}
	bridgeRuntime, err = resourcebridge.New(resourcebridge.Config{
		Store:   store,
		Vault:   vault,
		Claims:  claimService,
		Backend: localBackend,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create resource bridge: %w", err)
	}
	discoveryWorker, err = resources.NewDiscoveryWorkerPool(
		discoveryWorkerMaxConcurrent,
		discoveryWorkerMaxAdmitted,
		discoveryWorkerTimeout,
	)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create discovery worker: %w", err)
	}
	resourceRepository, err := resources.New(resources.Config{
		Store:           store,
		Connectors:      connector.NewDefaultRegistry(),
		BaseURLs:        outbound,
		Secrets:         bridgeRuntime,
		KeyDeletion:     charityService,
		DiscoveryRail:   bridgeRuntime,
		DiscoveryWorker: discoveryWorker,
		CursorKeys:      vault,
		FinalAuth:       authRuntime,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create resource repository: %w", err)
	}
	if _, err := resourceRepository.RecoverStaleDiscoveries(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("recover stale discoveries: %w", err)
	}
	charityRoutingService, err := charityrouting.New(charityrouting.Config{
		Store:         store,
		RoleAuth:      roleAuthorizer,
		DonationState: donationService,
		CursorKeys:    vault,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create charity routing service: %w", err)
	}
	activityRepository, err := activities.NewRepository(activities.RepositoryConfig{
		Store:          store,
		UserFinalAuth:  authRuntime,
		AdminFinalAuth: roleAuthorizer,
		UserGate:       activityMutationGate{},
		CursorKeys:     vault,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create activities repository: %w", err)
	}
	activityEvents, err = accountstream.New(activityRepository, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create account event hub: %w", err)
	}
	activityPublisher, err := activities.NewAccountstreamPublisher(activityRepository, activityEvents)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create activities publisher: %w", err)
	}
	activityService, err := activities.NewService(activities.ServiceConfig{
		Repository: activityRepository,
		Publisher:  activityPublisher,
		Reporter:   activityPublishReporter{},
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create activities service: %w", err)
	}
	activityWorker, err = activities.NewSettlementWorker(activityService, 0)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create activities settlement worker: %w", err)
	}
	if err := activityWorker.RecoverBeforeListener(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("recover activities before listener: %w", err)
	}
	if err := resources.RegisterRoutes(authRuntime, resourceRepository); err != nil {
		cleanup()
		return nil, fmt.Errorf("register resource routes: %w", err)
	}
	if err := donation.RegisterOwnerRoutes(authRuntime, donationService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register donation owner routes: %w", err)
	}
	if err := donation.RegisterAdminRoutes(authRuntime, donationService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register donation administrator routes: %w", err)
	}
	if err := donation.RegisterStewardRoutes(authRuntime, donationService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register donation steward routes: %w", err)
	}
	if err := charityrouting.RegisterOwnerRoutes(authRuntime, charityRoutingService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register charity capability routes: %w", err)
	}
	if err := charityrouting.RegisterAdminRoutes(authRuntime, charityRoutingService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register charity administrator routes: %w", err)
	}
	if err := charityrouting.RegisterStewardRoutes(authRuntime, charityRoutingService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register charity steward routes: %w", err)
	}
	activityRoutes := activityRouteRegistrar{runtime: authRuntime}
	if err := activities.RegisterRoutes(activityRoutes, activityRoutes, activityService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register activities routes: %w", err)
	}
	if _, err := maintenanceService.PrepareListener(startupContext, store.DB()); err != nil {
		cleanup()
		return nil, fmt.Errorf("prepare maintenance state: %w", err)
	}

	mux, err := generationTwoMux(cfg, store, authRuntime)
	if err != nil {
		cleanup()
		return nil, err
	}
	handler, err := stationBoundary(cfg, mux)
	if err != nil {
		cleanup()
		return nil, err
	}
	activityContext, activityCancel := context.WithCancel(context.Background())
	activityDone := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(activityDone)
		err := activityWorker.Run(activityContext)
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, activities.ErrClosed) {
			return
		}
		failures <- fmt.Errorf("activities settlement worker: %w", err)
	}()
	return &application{
		handler:         handler,
		authRuntime:     authRuntime,
		bridge:          bridgeRuntime,
		claims:          claimService,
		resourceRepo:    resourceRepository,
		discoveryWorker: discoveryWorker,
		donations:       donationService,
		charity:         charityService,
		charityRouting:  charityRoutingService,
		activities:      activityService,
		activityRepo:    activityRepository,
		activityEvents:  activityEvents,
		activityWorker:  activityWorker,
		activityCancel:  activityCancel,
		activityDone:    activityDone,
		failures:        failures,
		authorizer:      authorizer,
		elevation:       elevationManager,
		gate:            gate,
		registry:        registry,
		maintenance:     maintenanceService,
		egress:          outbound,
	}, nil
}

func recoverClaimsBeforeListener(ctx context.Context, service *claim.Service) error {
	if ctx == nil || service == nil {
		return errors.New("claim recovery dependencies are required")
	}
	for {
		report, err := service.RecoverNonterminal(ctx, claim.MaxRecoveryBatch)
		if err != nil {
			return fmt.Errorf("recover nonterminal claims: %w", err)
		}
		if !report.More {
			return nil
		}
	}
}
