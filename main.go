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
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
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
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	gameruntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
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

	exitCode := 0
	serverRunning := true
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		serverRunning = false
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped with error", "err", err)
		} else {
			slog.Error("http server stopped before shutdown completed")
		}
		exitCode = 1
	case err := <-app.failures:
		slog.Error("application background worker failed", "err", err)
		exitCode = 1
	}
	slog.Info("shutdown initiated")
	app.BeginShutdown()
	if serverRunning {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("http shutdown error", "err", err)
			return 1
		}
	}
	slog.Info("shutdown complete")
	return exitCode
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

// servePublicConfig is the anonymous Generation 2 bootstrap projection. All
// other production APIs are mounted by their authenticated domain owners.
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

func generationTwoMux(cfg *config.Config, store *db.Store, authRuntime *auth.Runtime, callerHandler http.Handler) (*http.ServeMux, error) {
	if cfg == nil || store == nil || authRuntime == nil || callerHandler == nil {
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

	callerAPI := httpmw.API(callerHandler)
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
	announcements   *announcements.Service
	issues          *issues.Service
	reports         *reports.Repository
	activities      *activities.Service
	activityRepo    *activities.Repository
	activityEvents  *accountstream.Hub
	adminConfig     *adminapi.SiteConfigRuntime
	adminAlerts     *adminalerts.Repository
	adminUsers      *adminusers.Service
	lifecycle       *lifecycle.Coordinator
	lifecycleCancel context.CancelFunc
	lifecycleDone   <-chan struct{}
	debug           *debug.Hub
	logs            *logapi.Repository
	accountEvents   *accountEventConnections
	forward         *publicForwardRuntime
	games           *gameRuntimeBundle
	failures        <-chan error
	authorizer      *authz.Authorizer
	elevation       *elevation.Manager
	gate            *maintenance.Gate
	registry        *maintenance.Registry
	maintenance     *maintenance.Service
	egress          *egress.Stack

	shutdownOnce sync.Once
	closeOnce    sync.Once
	closeErr     error
}

// BeginShutdown closes process-local streaming and caller admission before
// http.Server.Shutdown starts waiting for active connections. Durable workers
// remain owned by Close and leave unfinished work at their checkpoints.
func (a *application) BeginShutdown() {
	if a == nil {
		return
	}
	a.shutdownOnce.Do(func() {
		if a.accountEvents != nil {
			_ = a.accountEvents.Close()
		}
		if a.forward != nil {
			a.forward.BeginShutdown()
		}
		if a.debug != nil {
			_ = a.debug.Close()
		}
	})
}

func (a *application) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.BeginShutdown()
		var closeErrors []error
		if a.lifecycleCancel != nil {
			a.lifecycleCancel()
		}
		if a.lifecycleDone != nil {
			<-a.lifecycleDone
		}
		if a.lifecycle != nil {
			if err := a.lifecycle.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.reports != nil {
			if err := a.reports.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.games != nil {
			if err := a.games.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		if a.forward != nil {
			if err := a.forward.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
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
var _ announcements.AdminFinalTxAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminapi.SiteConfigFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminalerts.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ adminusers.AdminFinalAuthorizer = (*roleFinalTxAuthorizer)(nil)
var _ logapi.StewardAuthorizer = (*roleFinalTxAuthorizer)(nil)

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeAdminFinalTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorAdminSession, authz.RoleAdministrator)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	return authorizer.authorize(ctx, tx, userID, authz.ActorUserSession, authz.RoleSteward)
}

func (authorizer *roleFinalTxAuthorizer) AuthorizeStewardRead(ctx context.Context, tx *sql.Tx, userID int64) error {
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

type siteConfigRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminapi.SiteConfigRouteRegistrar = siteConfigRouteRegistrar{}

func (registrar siteConfigRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminapi.SiteConfigAuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, adminapi.SiteConfigAdminPrincipal{UserID: actor.UserID})
	}))
}

type adminAlertRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminalerts.AdminRouteRegistrar = adminAlertRouteRegistrar{}

func (registrar adminAlertRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminalerts.AuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, adminalerts.AdminPrincipal{UserID: actor.UserID})
	}))
}

type adminUserRouteRegistrar struct{ runtime *auth.Runtime }

var _ adminusers.AdminRouteRegistrar = adminUserRouteRegistrar{}

func (registrar adminUserRouteRegistrar) RegisterAdminRoute(method, pattern string, handler adminusers.AuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, adminusers.AdminPrincipal{UserID: actor.UserID})
	}))
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

type announcementRouteRegistrar struct{ runtime *auth.Runtime }

var _ announcements.UserRouteRegistrar = announcementRouteRegistrar{}
var _ announcements.AdminRouteRegistrar = announcementRouteRegistrar{}

func (registrar announcementRouteRegistrar) RegisterUserRoute(method, pattern string, handler announcements.AuthorizedUserHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
		handler(writer, request, announcements.UserPrincipal{UserID: principal.UserID})
	})
}

func (registrar announcementRouteRegistrar) RegisterAdminRoute(method, pattern string, handler announcements.AuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, announcements.AdminPrincipal{UserID: actor.UserID})
	}))
}

type issueRouteRegistrar struct{ runtime *auth.Runtime }

var _ issues.UserRouteRegistrar = issueRouteRegistrar{}

func (registrar issueRouteRegistrar) RegisterUserRoute(method, pattern string, handler issues.AuthorizedUserHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
		handler(writer, request, issues.UserPrincipal{UserID: principal.UserID})
	})
}

type maintenanceRouteRegistrar struct{ runtime *auth.Runtime }

var _ maintenance.StewardRouteRegistrar = maintenanceRouteRegistrar{}
var _ maintenance.AdminRouteRegistrar = maintenanceRouteRegistrar{}

func (registrar maintenanceRouteRegistrar) RegisterStewardRoute(method, pattern string, handler maintenance.AuthorizedHTTPHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterUserRoute(method, pattern, func(writer http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorUserSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, maintenance.HTTPPrincipal{Actor: actor})
	})
}

func (registrar maintenanceRouteRegistrar) RegisterAdminRoute(method, pattern string, handler maintenance.AuthorizedHTTPHandler) error {
	if registrar.runtime == nil || handler == nil {
		return auth.ErrInvalidRoute
	}
	return registrar.runtime.RegisterAdminRoute(method, pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(writer, request, maintenance.HTTPPrincipal{Actor: actor})
	}))
}

// emptyResourceValidationAuthority is the production authority when no
// explicit endpoint validator feed is configured. Discovery and routing
// remain live authorities; this closed provider contributes no synthetic
// credential/configuration evidence.
type emptyResourceValidationAuthority struct{}

var _ issues.ResourceValidationAuthority = emptyResourceValidationAuthority{}

func (emptyResourceValidationAuthority) Current(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind issues.ResourceKind,
	resourceID int64,
	root issues.RootCause,
) (issues.ResourceValidationState, error) {
	if ctx == nil || tx == nil || userID <= 0 || resourceID <= 0 || kind == "" || root == "" {
		return issues.ResourceValidationState{}, errors.New("resource validation authority received an invalid target")
	}
	return issues.ResourceValidationState{ObservedAt: 0}, nil
}

func (emptyResourceValidationAuthority) Scan(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	cursor string,
	limit int,
) (issues.ResourceValidationBatch, error) {
	if ctx == nil || tx == nil || userID <= 0 || cursor != "" || limit < 1 || limit > 100 {
		return issues.ResourceValidationBatch{}, errors.New("resource validation authority received an invalid scan")
	}
	return issues.ResourceValidationBatch{Items: []issues.ResourceValidationTarget{}, Done: true}, nil
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
	var resourceRepository *resources.Repository
	var reportRepository *reports.Repository
	var authRuntime *auth.Runtime
	var activityEvents *accountstream.Hub
	var lifecycleCoordinator *lifecycle.Coordinator
	var debugHub *debug.Hub
	var accountConnections *accountEventConnections
	var forwardRuntime *publicForwardRuntime
	var gameRuntimes *gameRuntimeBundle
	cleanup := func() {
		if accountConnections != nil {
			_ = accountConnections.Close()
		}
		if debugHub != nil {
			_ = debugHub.Close()
		}
		if reportRepository != nil {
			_ = reportRepository.Close()
		}
		if lifecycleCoordinator != nil {
			_ = lifecycleCoordinator.Close()
		}
		if gameRuntimes != nil {
			_ = gameRuntimes.Close()
		}
		if forwardRuntime != nil {
			_ = forwardRuntime.Close()
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
	rpmLimits, concurrencyLimits, err := loadRuntimeLimits(store)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("load runtime limits: %w", err)
	}
	if err := outbound.SetConcurrencyLimits(concurrencyLimits); err != nil {
		cleanup()
		return nil, fmt.Errorf("apply egress concurrency limits: %w", err)
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
	adminConfigRepository, err := adminapi.NewSiteConfigRepository(adminapi.SiteConfigRepositoryOptions{
		Store: store, FinalAuthorizer: roleAuthorizer,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create administrator site configuration repository: %w", err)
	}
	adminConfigRuntime, err := adminapi.NewSiteConfigRuntime(adminConfigRepository)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create administrator site configuration runtime: %w", err)
	}
	adminAlertRepository, err := adminalerts.NewRepository(adminalerts.Config{
		Store: store, CursorKeys: vault, FinalAuth: roleAuthorizer,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create administrator alert repository: %w", err)
	}
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
		Acceptance: maintenanceService,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create claim service: %w", err)
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
	connectorRegistry := connector.NewDefaultRegistry()
	announcementRepository, err := announcements.NewRepository(announcements.Config{
		Store: store, CursorKeys: vault, FinalAuth: roleAuthorizer,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create announcement repository: %w", err)
	}
	announcementService, err := announcements.NewService(announcementRepository)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create announcement service: %w", err)
	}
	issueRepository, err := issues.NewRepository(issues.Config{
		Store: store, CursorKeys: vault, ResourceValidation: emptyResourceValidationAuthority{},
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create issue repository: %w", err)
	}
	issueService, err := issues.NewService(issueRepository)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create issue service: %w", err)
	}
	reportRepository, err = reports.New(reports.Config{
		Store: store, Connectors: connectorRegistry, BaseURLs: outbound,
		KeyDeriver: vault, Authorizer: authorizer, IssueProjection: issueService.Sources(),
		DeleteKey: func(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID, decisionNow int64) error {
			if resourceRepository == nil {
				return errors.New("resource deletion capability unavailable")
			}
			return resourceRepository.DeleteEndpointKeyForReport(ctx, tx, ownerUserID, endpointKeyID, decisionNow)
		},
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create report repository: %w", err)
	}
	resourceRepository, err = resources.New(resources.Config{
		Store:           store,
		Connectors:      connectorRegistry,
		BaseURLs:        outbound,
		Secrets:         bridgeRuntime,
		KeyDeletion:     charityService,
		KeyCreation:     reportRepository,
		Projection:      issueService.Sources(),
		DiscoveryRail:   bridgeRuntime,
		DiscoveryWorker: discoveryWorker,
		CursorKeys:      vault,
		FinalAuth:       authRuntime,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create resource repository: %w", err)
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
	accountSources, err := newAccountEventSources(activityRepository)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create account event sources: %w", err)
	}
	activityEvents, err = accountstream.New(accountSources, accountSources)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create account event hub: %w", err)
	}
	accountConnections = newAccountEventConnections()
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
	if err := recoverAnnouncementsBeforeListener(startupContext, announcementService); err != nil {
		cleanup()
		return nil, err
	}
	if err := recoverIssuesBeforeListener(startupContext, issueService); err != nil {
		cleanup()
		return nil, err
	}
	debugHub, err = debug.NewHub(debugIdentityAuthority{runtime: authRuntime})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create Debug hub: %w", err)
	}
	debugMutations, err := debug.NewMutationRepository(store.DB())
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create Debug mutation repository: %w", err)
	}
	logRepository, err := logapi.NewRepository(store.DB(), vault)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create log repository: %w", err)
	}
	gameRuntimes, err = newGameRuntimeBundle(
		store, vault, authRuntime, roleAuthorizer, maintenanceService, activityRepository,
		activityPublisher, activityEvents, accountSources,
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := linklink.RegisterContinuation(registry, gameRuntimes.linklink); err != nil {
		cleanup()
		return nil, fmt.Errorf("register LinkLink maintenance continuation: %w", err)
	}
	if err := rps.RegisterContinuation(registry, gameRuntimes.rps); err != nil {
		cleanup()
		return nil, fmt.Errorf("register RPS maintenance continuation: %w", err)
	}
	userInvalidations := &userSessionInvalidationFanout{
		debug: debugHub, connections: accountConnections,
	}
	if err := authRuntime.AttachUserSessionInvalidationObserver(userInvalidations); err != nil {
		cleanup()
		return nil, fmt.Errorf("attach user-session invalidation observer: %w", err)
	}
	adminUserService, err := adminusers.NewService(adminusers.ServiceConfig{
		Database: store.DB(), CursorKeys: vault, FinalAuth: roleAuthorizer, Invalidator: userInvalidations,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create administrator user service: %w", err)
	}
	forwardRuntime, err = newPublicForwardRuntime(
		store, vault, claimService, charityService, charityRoutingService, resourceRepository,
		connectorRegistry, localBackend, debugHub, gate, rpmLimits,
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := authRuntime.AttachUserLifecycleGate(forwardRuntime.lifecycle); err != nil {
		cleanup()
		return nil, fmt.Errorf("attach shared user lifecycle gate: %w", err)
	}
	lifecycleCoordinator, err = newLifecycleCoordinator(
		store, vault, authRuntime, roleAuthorizer, forwardRuntime, gameRuntimes,
		claimService, resourceRepository, issueService, logRepository,
		activityService, activityRepository, donationService, charityService,
		reportRepository, announcementRepository, maintenanceService,
		activityEvents, debugHub,
	)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create account lifecycle coordinator: %w", err)
	}
	if err := adminapi.RegisterSiteConfigRoutes(siteConfigRouteRegistrar{runtime: authRuntime}, adminConfigRuntime); err != nil {
		cleanup()
		return nil, fmt.Errorf("register administrator site configuration routes: %w", err)
	}
	if err := adminusers.RegisterRoutes(adminUserRouteRegistrar{runtime: authRuntime}, adminUserService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register administrator user routes: %w", err)
	}
	if err := adminalerts.RegisterRoutes(adminAlertRouteRegistrar{runtime: authRuntime}, adminAlertRepository); err != nil {
		cleanup()
		return nil, fmt.Errorf("register administrator alert routes: %w", err)
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
	announcementRoutes := announcementRouteRegistrar{runtime: authRuntime}
	if err := announcements.RegisterRoutes(announcementRoutes, announcementRoutes, announcementService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register announcement routes: %w", err)
	}
	if err := issues.RegisterRoutes(issueRouteRegistrar{runtime: authRuntime}, issueService); err != nil {
		cleanup()
		return nil, fmt.Errorf("register issue routes: %w", err)
	}
	if err := reportRepository.RegisterRoutes(authRuntime); err != nil {
		cleanup()
		return nil, fmt.Errorf("register report routes: %w", err)
	}
	maintenanceRoutes := maintenanceRouteRegistrar{runtime: authRuntime}
	if err := maintenance.RegisterRoutes(maintenanceRoutes, maintenanceRoutes, maintenance.HTTPOptions{
		Database: store.DB(), Service: maintenanceService,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("register maintenance routes: %w", err)
	}
	if err := debug.RegisterRoutes(debugRouteRegistrar{runtime: authRuntime}, debugHub, debugMutations); err != nil {
		cleanup()
		return nil, fmt.Errorf("register Debug routes: %w", err)
	}
	if err := logapi.RegisterUserRoutes(authRuntime, logRepository); err != nil {
		cleanup()
		return nil, fmt.Errorf("register user log routes: %w", err)
	}
	if err := logapi.RegisterStewardRoutes(authRuntime, logRepository, roleAuthorizer); err != nil {
		cleanup()
		return nil, fmt.Errorf("register steward log routes: %w", err)
	}
	if err := logapi.RegisterAdminRoutes(authRuntime, logRepository); err != nil {
		cleanup()
		return nil, fmt.Errorf("register administrator log routes: %w", err)
	}
	if err := gameruntime.RegisterUserRoutes(authRuntime, gameRuntimes.fishing); err != nil {
		cleanup()
		return nil, fmt.Errorf("register Fishing routes: %w", err)
	}
	if err := gameruntime.RegisterAdminRoutes(authRuntime, gameRuntimes.fishing); err != nil {
		cleanup()
		return nil, fmt.Errorf("register game administrator routes: %w", err)
	}
	if err := linklink.RegisterRoutes(authRuntime, authRuntime, gameRuntimes.linklink); err != nil {
		cleanup()
		return nil, fmt.Errorf("register LinkLink routes: %w", err)
	}
	if err := rps.RegisterRoutes(authRuntime, authRuntime, gameRuntimes.rps); err != nil {
		cleanup()
		return nil, fmt.Errorf("register RPS routes: %w", err)
	}
	lifecycleRoutes := lifecycleRouteRegistrar{runtime: authRuntime}
	if err := lifecycle.RegisterRoutes(lifecycleRoutes, lifecycleRoutes, lifecycleCoordinator); err != nil {
		cleanup()
		return nil, fmt.Errorf("register account lifecycle routes: %w", err)
	}
	if err := registerAccountEventRoute(authRuntime, gate, gameRuntimes.rps, activityEvents, accountConnections); err != nil {
		cleanup()
		return nil, fmt.Errorf("register account event route: %w", err)
	}
	if _, err := maintenanceService.PrepareListener(startupContext, store.DB()); err != nil {
		cleanup()
		return nil, fmt.Errorf("prepare maintenance state: %w", err)
	}
	if err := gameRuntimes.linklink.ValidatePersistedState(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("validate LinkLink persisted state: %w", err)
	}
	if err := gameRuntimes.rps.ValidatePersistedState(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("validate RPS persisted state: %w", err)
	}
	if err := gameRuntimes.fishing.ValidatePersistedState(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("validate Fishing persisted state: %w", err)
	}
	if err := lifecycleCoordinator.RecoverBeforeListener(startupContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("recover account lifecycle before listener: %w", err)
	}

	mux, err := generationTwoMux(cfg, store, authRuntime, forwardRuntime.handler)
	if err != nil {
		cleanup()
		return nil, err
	}
	handler, err := stationBoundary(cfg, mux)
	if err != nil {
		cleanup()
		return nil, err
	}
	gameWorkerContext := context.Background()
	if err := gameRuntimes.linklink.StartWorker(gameWorkerContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("start LinkLink worker: %w", err)
	}
	if err := gameRuntimes.rps.StartWorker(gameWorkerContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("start RPS worker: %w", err)
	}
	if err := gameRuntimes.fishing.StartWorker(gameWorkerContext); err != nil {
		cleanup()
		return nil, fmt.Errorf("start Fishing worker: %w", err)
	}
	lifecycleCancel, lifecycleDone, err := startLifecycleWorker(context.Background(), lifecycleCoordinator)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("start account lifecycle worker: %w", err)
	}
	failures := make(chan error, 1)
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
		announcements:   announcementService,
		issues:          issueService,
		reports:         reportRepository,
		activities:      activityService,
		activityRepo:    activityRepository,
		activityEvents:  activityEvents,
		adminConfig:     adminConfigRuntime,
		adminAlerts:     adminAlertRepository,
		adminUsers:      adminUserService,
		lifecycle:       lifecycleCoordinator,
		lifecycleCancel: lifecycleCancel,
		lifecycleDone:   lifecycleDone,
		debug:           debugHub,
		logs:            logRepository,
		accountEvents:   accountConnections,
		forward:         forwardRuntime,
		games:           gameRuntimes,
		failures:        failures,
		authorizer:      authorizer,
		elevation:       elevationManager,
		gate:            gate,
		registry:        registry,
		maintenance:     maintenanceService,
		egress:          outbound,
	}, nil
}

const lifecycleRecoveryBatch = 100

func recoverAnnouncementsBeforeListener(ctx context.Context, service *announcements.Service) error {
	if ctx == nil || service == nil {
		return errors.New("announcement recovery dependencies are required")
	}
	for {
		result, err := service.RecoverBeforeListener(ctx, lifecycleRecoveryBatch)
		if err != nil {
			return fmt.Errorf("recover announcements before listener: %w", err)
		}
		if result.Expired < lifecycleRecoveryBatch && result.ActorsDeidentified < lifecycleRecoveryBatch &&
			result.AuditsDeleted < lifecycleRecoveryBatch {
			return nil
		}
	}
}

func recoverIssuesBeforeListener(ctx context.Context, service *issues.Service) error {
	if ctx == nil || service == nil {
		return errors.New("issue recovery dependencies are required")
	}
	if _, _, err := service.RecoverBeforeListener(ctx, lifecycleRecoveryBatch); err != nil {
		return fmt.Errorf("recover issues before listener: %w", err)
	}
	return nil
}
