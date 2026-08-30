// Command nonbiriapi opens a validated Generation 2 database and serves the
// two host-isolated station shells. Domain APIs are registered by their
// Generation 2 owners only after their persistence and lifecycle contracts
// are complete.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/applog"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
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
	handler     http.Handler
	authRuntime *auth.Runtime
	bridge      *resourcebridge.Runtime
	claims      *claim.Service
	authorizer  *authz.Authorizer
	elevation   *elevation.Manager
	gate        *maintenance.Gate
	registry    *maintenance.Registry
	maintenance *maintenance.Service
	egress      *egress.Stack

	closeOnce sync.Once
	closeErr  error
}

func (a *application) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		var closeErrors []error
		if a.authRuntime != nil {
			if err := a.authRuntime.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
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
	var authRuntime *auth.Runtime
	cleanup := func() {
		if authRuntime != nil {
			_ = authRuntime.Close()
		} else {
			_ = elevationManager.Close()
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
	claimService, err := claim.New(claim.Dependencies{DB: store.DB(), Secrets: vault})
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
	return &application{
		handler:     handler,
		authRuntime: authRuntime,
		bridge:      bridgeRuntime,
		claims:      claimService,
		authorizer:  authorizer,
		elevation:   elevationManager,
		gate:        gate,
		registry:    registry,
		maintenance: maintenanceService,
		egress:      outbound,
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
