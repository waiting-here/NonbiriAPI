// Command nonbiriapi loads the security-rooted configuration, wires the shared
// database/egress/identity/forwarding boundaries, and serves the two isolated
// stations from one embedded binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"nonbiriapi/internal/adminapi"
	"nonbiriapi/internal/alertapi"
	"nonbiriapi/internal/applog"
	"nonbiriapi/internal/auth"
	"nonbiriapi/internal/config"
	"nonbiriapi/internal/connector/openai"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/egress"
	"nonbiriapi/internal/elevation"
	"nonbiriapi/internal/endpoint"
	"nonbiriapi/internal/fetch"
	"nonbiriapi/internal/flowcontrol"
	"nonbiriapi/internal/forward"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
	"nonbiriapi/internal/issues"
	"nonbiriapi/internal/lifecycle"
	"nonbiriapi/internal/logapi"
	"nonbiriapi/internal/model"
	"nonbiriapi/internal/ratelimit"
	"nonbiriapi/internal/secret"
	"nonbiriapi/internal/usage"
	"nonbiriapi/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup configuration error:")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	logger := applog.New(os.Stdout, applog.ParseLevel(cfg.LogLevel))
	slog.SetDefault(logger)

	secretVault := cfg.TakeSecretVault()
	if secretVault == nil {
		slog.Error("secret vault initialization failed")
		os.Exit(1)
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
		return
	}
	defer func() { _ = store.Close() }()
	slog.Info("database ready", "path", cfg.DBPath)

	app, err := buildApplication(cfg, store, secretVault)
	if err != nil {
		slog.Error("application wiring failed", "err", err)
		return
	}
	defer func() { _ = app.Close() }()

	srv := &http.Server{
		Addr:        cfg.ListenAddr,
		Handler:     app.handler,
		ReadTimeout: 15 * time.Second,
		// WriteTimeout remains zero: the forwarding exit owns bounded stream
		// write deadlines while the server permits long-lived SSE connections.
		IdleTimeout: 60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("http server listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped with error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown initiated")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "err", err)
	}
	slog.Info("shutdown complete")
}

// healthz is unauthenticated but still runs behind the validated host edge.
func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "not found"))
}

// newHTTPHandler remains the small boundary-only constructor used by unit
// tests and by callers that only need the embedded station shell. Production
// main uses buildApplication below so all business services share one set of
// singletons.
func newHTTPHandler(cfg *config.Config) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	userAPI := httpmw.API(http.HandlerFunc(apiNotFound))
	adminAPI := httpmw.API(http.HandlerFunc(apiNotFound))
	mux.Handle("/api", userAPI)
	mux.Handle("/api/", userAPI)
	mux.Handle("/v1", userAPI)
	mux.Handle("/v1/", userAPI)
	mux.Handle("/admin/api", adminAPI)
	mux.Handle("/admin/api/", adminAPI)
	mux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return httpmw.New(httpmw.Config{
		UserHost:          cfg.UserHost,
		AdminHost:         cfg.AdminHost,
		SiteBaseURL:       cfg.SiteBaseURL,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}, mux)
}

type application struct {
	handler http.Handler
	stop    context.CancelFunc
	wg      sync.WaitGroup
	close   []func() error
	once    sync.Once
}

func (a *application) Close() error {
	if a == nil {
		return nil
	}
	var first error
	a.once.Do(func() {
		if a.stop != nil {
			a.stop()
		}
		a.wg.Wait()
		for i := len(a.close) - 1; i >= 0; i-- {
			if err := a.close[i](); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

func buildApplication(cfg *config.Config, store *db.Store, vault *secret.Vault) (app *application, err error) {
	if cfg == nil || store == nil || vault == nil {
		return nil, errors.New("application dependencies are required")
	}
	cleanup := make([]func() error, 0, 10)
	defer func() {
		if err != nil {
			for i := len(cleanup) - 1; i >= 0; i-- {
				_ = cleanup[i]()
			}
		}
	}()

	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		return nil, fmt.Errorf("egress stack: %w", err)
	}
	cleanup = append(cleanup, func() error { stack.CloseIdleConnections(); return nil })
	if err := stack.AddSelfOrigins(context.Background(), cfg); err != nil {
		return nil, fmt.Errorf("egress self origins: %w", err)
	}

	registry := endpoint.NewRegistry()
	var flowController *flowcontrol.Controller
	sharedElevation, err := elevation.NewManager()
	if err != nil {
		return nil, fmt.Errorf("elevation manager: %w", err)
	}
	cleanup = append(cleanup, sharedElevation.Close)

	provider, err := auth.NewHTTPDiscordProvider(auth.HTTPDiscordProviderConfig{
		ClientID: cfg.DiscordClientID, ClientSecret: cfg.DiscordClientSecret,
		Scopes: cfg.DiscordOAuthScopes,
	})
	if err != nil {
		return nil, fmt.Errorf("discord provider: %w", err)
	}
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: store, Provider: provider, ClientID: cfg.DiscordClientID,
		SiteBaseURL: cfg.SiteBaseURL, Elevation: sharedElevation,
		UserRPMLimitCap: func(context.Context) (int, error) {
			if flowController == nil {
				return 0, errors.New("flow controller is not wired")
			}
			return flowController.Limits().PerUserLimit, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("user auth: %w", err)
	}
	cleanup = append(cleanup, userAuth.Close)
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: store, Username: cfg.AdminUsername, Password: cfg.AdminPassword,
		SiteBaseURL: cfg.SiteBaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("admin auth: %w", err)
	}
	cleanup = append(cleanup, adminAuth.Close)

	modelFetcher, err := fetch.NewFetcher(fetch.FetcherConfig{
		Store: store, Stack: stack, Secrets: vault, Registry: registry,
	})
	if err != nil {
		return nil, fmt.Errorf("model fetcher: %w", err)
	}
	cleanup = append(cleanup, modelFetcher.Close)

	endpointService := endpoint.NewService(endpoint.ServiceDeps{
		Repo: store, URLs: stack, Secrets: vault, Connectors: registry, Hook: modelFetcher,
	})
	modelService := model.NewService(store)
	openAIAdapter, err := openai.NewAdapter(openai.AdapterConfig{Stack: stack})
	if err != nil {
		return nil, fmt.Errorf("openai adapter: %w", err)
	}
	secureRunner, err := forward.NewSecureRunner(forward.SecureRunnerConfig{
		Repository: store, Secrets: vault, Registry: registry,
		Adapters: []forward.Adapter{openAIAdapter},
	})
	if err != nil {
		return nil, fmt.Errorf("secure runner: %w", err)
	}
	usageService, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		return nil, fmt.Errorf("usage service: %w", err)
	}
	forwardService, err := forward.NewService(forward.ServiceConfig{
		Repository: store, Runner: secureRunner,
		Hooks: forward.Hooks{Attempt: usageService.HandleAttempt, Usage: usageService.HandleUsage},
	})
	if err != nil {
		return nil, fmt.Errorf("forward service: %w", err)
	}
	flowController, err = flowcontrol.New(flowcontrol.Config{
		RPM:        ratelimit.DefaultRPMConfig(),
		UserLimits: flowcontrol.DBUserLimitResolver(store),
	})
	if err != nil {
		return nil, fmt.Errorf("flow controller: %w", err)
	}
	cleanup = append(cleanup, flowController.Close)
	runtimeApplier := adminapi.NewRuntimeApplier(flowController, stack)
	if err := applyPersistedRuntimeConfig(store, runtimeApplier); err != nil {
		return nil, fmt.Errorf("apply persisted runtime configuration: %w", err)
	}
	flowMiddleware, err := flowcontrol.NewMiddleware(flowController, forward.CallerIdentity)
	if err != nil {
		return nil, fmt.Errorf("flow middleware: %w", err)
	}

	lifecycleService, err := lifecycle.NewService(lifecycle.Config{
		Store: store, Elevation: sharedElevation, AdminVerifier: adminAuth,
	})
	if err != nil {
		return nil, fmt.Errorf("lifecycle service: %w", err)
	}
	cleanup = append(cleanup, lifecycleService.Close)
	exportHandler := lifecycle.NewHandler(lifecycle.HandlerDeps{
		Store: store,
		Resolve: func(r *http.Request) (*db.User, error) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				return nil, errors.New("user session required")
			}
			return user, nil
		},
		Elevation: userAuth,
	})

	adminControls := adminapi.NewHandler(adminapi.HandlerDeps{
		Store:   store,
		Runtime: runtimeApplier,
	})
	api := buildUserAPI(userAuth, adminAuth, endpointService, modelFetcher, modelService,
		logapi.NewHandler(logapi.HandlerDeps{Store: store}),
		issues.NewHandler(issues.HandlerDeps{Store: store}),
		exportHandler, lifecycleService, forwardService, flowMiddleware, store)
	appHandler := buildAdminAndRootAPI(cfg, userAuth, adminAuth, api, adminControls, alertapi.NewHandler(alertapi.HandlerDeps{Store: store}), logapi.NewHandler(logapi.HandlerDeps{Store: store}), lifecycleService, store, forwardService, flowMiddleware)

	retentionCtx, stopRetention := context.WithCancel(context.Background())
	app = &application{handler: appHandler, stop: stopRetention, close: cleanup}
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-retentionCtx.Done():
				return
			case <-ticker.C:
				if _, cleanupErr := usageService.CleanupRequestLogs(retentionCtx); cleanupErr != nil && retentionCtx.Err() == nil {
					slog.Error("request log retention failed", "err", cleanupErr)
				}
			}
		}
	}()
	return app, nil
}

func applyPersistedRuntimeConfig(store *db.Store, runtime adminapi.RuntimeApplier) error {
	if store == nil || runtime == nil {
		return errors.New("runtime configuration dependencies are required")
	}
	values, err := store.GetAllSiteConfigValues()
	if err != nil {
		return err
	}
	for _, key := range []string{
		adminapi.KeyGlobalRPM,
		adminapi.KeyDefaultRPMPerUser,
		adminapi.KeyEgressGlobalConc,
		adminapi.KeyDefaultPerEndpointConc,
	} {
		if value, ok := values[key]; ok {
			if err := runtime.ApplySiteConfig(context.Background(), key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return nil
}

func buildUserAPI(userAuth *auth.UserAuth, adminAuth *auth.AdminAuth, endpointService *endpoint.Service, fetcher *fetch.Fetcher, modelService *model.Service, logs http.Handler, issueHandler http.Handler, exportHandler http.Handler, lifecycleService *lifecycle.Service, forwardService *forward.Service, flowMiddleware *flowcontrol.Middleware, store *db.Store) http.Handler {
	userAuthHandler := userAuth.Handler()
	identity := func(r *http.Request) (int64, error) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user == nil || !user.IsActive() {
			return 0, errors.New("user session required")
		}
		return user.ID, nil
	}
	endpointHandler := userAuth.Middleware(endpoint.NewHandler(endpoint.HandlerDeps{Service: endpointService, Identity: identity}))
	fetchHandler := userAuth.Middleware(fetch.NewHandler(fetch.HandlerDeps{Fetcher: fetcher, Store: store, Identity: identity}))
	modelHandler := userAuth.Middleware(model.NewHandler(model.HandlerDeps{Service: modelService, Identity: model.SessionIdentity}))
	userLogs := userAuth.Middleware(logs)
	userIssues := userAuth.Middleware(issueHandler)
	userExport := userAuth.Middleware(exportHandler)
	userDelete := userAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.DeleteOwnAccountHandler)))
	forwardHandler := auth.CallerKeyMiddleware(store, flowMiddleware.Wrap(forward.NewHandler(forward.HandlerDeps{Service: forwardService, Identity: forward.CallerIdentity})))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/auth/discord/start" || path == "/api/auth/discord/callback" || path == "/api/auth/elevate" || path == "/api/session" || path == "/api/me" || path == "/api/auth/logout" || path == "/api/caller-key" || path == "/api/caller-key/regenerate":
			userAuthHandler.ServeHTTP(w, r)
		case path == "/api/me/usage":
			userLogs.ServeHTTP(w, r)
		case path == "/api/issues" || strings.HasPrefix(path, "/api/issues/"):
			userIssues.ServeHTTP(w, r)
		case path == "/api/account/export" || path == "/api/account/delete":
			if path == "/api/account/export" {
				userExport.ServeHTTP(w, r)
			} else {
				userDelete.ServeHTTP(w, r)
			}
		case path == "/api/endpoints" || strings.HasPrefix(path, "/api/endpoints/"):
			if strings.HasSuffix(path, "/models") || strings.HasSuffix(path, "/models/refresh") {
				fetchHandler.ServeHTTP(w, r)
			} else {
				endpointHandler.ServeHTTP(w, r)
			}
		case path == "/api/models" || strings.HasPrefix(path, "/api/models/"):
			modelHandler.ServeHTTP(w, r)
		case path == "/v1/models" || path == "/v1/chat/completions":
			forwardHandler.ServeHTTP(w, r)
		default:
			apiNotFound(w, r)
		}
	})
}

func buildAdminAndRootAPI(cfg *config.Config, userAuth *auth.UserAuth, adminAuth *auth.AdminAuth, userAPI http.Handler, adminControls http.Handler, alerts http.Handler, logs http.Handler, lifecycleService *lifecycle.Service, store *db.Store, _ *forward.Service, _ *flowcontrol.Middleware) http.Handler {
	adminLogs := adminAuth.Middleware(logs)
	adminControlsHandler := adminAuth.Middleware(adminControls)
	adminAlerts := adminAuth.Middleware(alerts)
	adminElevate := adminAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.ElevateAdminHandler)))
	adminDelete := adminAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.DeleteUserHandler)))
	adminAuthHandler := adminAuth.Handler()
	adminBoundary := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/api/login" || r.URL.Path == "/admin/api/logout" || r.URL.Path == "/admin/api/session":
			adminAuthHandler.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/auth/elevate":
			adminElevate.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/users" || strings.HasPrefix(r.URL.Path, "/admin/api/users/"):
			if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/api/users/") {
				adminDelete.ServeHTTP(w, r)
			} else {
				adminControlsHandler.ServeHTTP(w, r)
			}
		case r.URL.Path == "/admin/api/site-config" || strings.HasPrefix(r.URL.Path, "/admin/api/site-config/"):
			adminControlsHandler.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/logs" || r.URL.Path == "/admin/api/usage" || strings.HasPrefix(r.URL.Path, "/admin/api/overview/"):
			adminLogs.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/alerts" || strings.HasPrefix(r.URL.Path, "/admin/api/alerts/"):
			adminAlerts.ServeHTTP(w, r)
		default:
			apiNotFound(w, r)
		}
	}))
	rootMux := http.NewServeMux()
	rootMux.HandleFunc("/healthz", healthz)
	rootMux.Handle("/api", httpmw.API(userAPI))
	rootMux.Handle("/api/", httpmw.API(userAPI))
	rootMux.Handle("/v1", httpmw.API(userAPI))
	rootMux.Handle("/v1/", httpmw.API(userAPI))
	rootMux.Handle("/admin/api", adminBoundary)
	rootMux.Handle("/admin/api/", adminBoundary)
	rootMux.Handle("/", web.NewMultiHandler(cfg.UserHost, cfg.AdminHost))
	return mustHTTPBoundary(cfg, rootMux)
}

func mustHTTPBoundary(cfg *config.Config, next http.Handler) http.Handler {
	wrapped, err := httpmw.New(httpmw.Config{
		UserHost: cfg.UserHost, AdminHost: cfg.AdminHost, SiteBaseURL: cfg.SiteBaseURL,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}, next)
	if err != nil {
		panic(err)
	}
	return wrapped
}
