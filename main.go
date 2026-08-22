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

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/alertapi"
	"github.com/waiting-here/NonbiriAPI/internal/applog"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/checkin"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/fetch"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/model"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/steward"
	"github.com/waiting-here/NonbiriAPI/internal/usage"
	"github.com/waiting-here/NonbiriAPI/web"
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
	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		slog.Error("database credential migration failed")
		_ = store.Close()
		_ = secretVault.Close()
		os.Exit(1)
	}
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
	// The single outbound execution boundary: every connector and the model
	// fetcher open their clients through this one wrapper of the one Stack.
	localBackend, err := backend.NewLocal(stack)
	if err != nil {
		return nil, fmt.Errorf("egress backend: %w", err)
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
	oauthStartThrottle, err := ratelimit.NewIPThrottle(ratelimit.IPThrottleConfig{
		Limit:   ratelimit.DefaultOAuthStartRateLimit,
		Window:  time.Duration(ratelimit.DefaultOAuthStartRateWindowSeconds) * time.Second,
		Penalty: time.Duration(ratelimit.DefaultOAuthStartRatePenaltySeconds) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth start throttle: %w", err)
	}
	cleanup = append(cleanup, oauthStartThrottle.Close)
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: store, Provider: provider, ClientID: cfg.DiscordClientID,
		SiteBaseURL: cfg.SiteBaseURL, Elevation: sharedElevation,
		OAuthStartThrottle: oauthStartThrottle,
	})
	if err != nil {
		return nil, fmt.Errorf("user auth: %w", err)
	}
	cleanup = append(cleanup, userAuth.Close)
	credGenSubkey, err := vault.DeriveSubkey([]byte("admin-cred-gen-v1"))
	if err != nil {
		return nil, fmt.Errorf("admin credential subkey: %w", err)
	}
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: store, Username: cfg.AdminUsername, Password: cfg.AdminPassword,
		CredGenSubkey: credGenSubkey, SiteBaseURL: cfg.SiteBaseURL,
	})
	clear(credGenSubkey)
	if err != nil {
		return nil, fmt.Errorf("admin auth: %w", err)
	}
	cleanup = append(cleanup, adminAuth.Close)

	modelFetcher, err := fetch.NewFetcher(fetch.FetcherConfig{
		Store: store, Backend: localBackend, Secrets: vault, Registry: registry,
	})
	if err != nil {
		return nil, fmt.Errorf("model fetcher: %w", err)
	}
	cleanup = append(cleanup, modelFetcher.Close)

	endpointService := endpoint.NewService(endpoint.ServiceDeps{
		Repo: store, URLs: stack, Connectors: registry, Hook: modelFetcher,
	})
	modelService := model.NewService(store)
	openAIAdapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: localBackend})
	if err != nil {
		return nil, fmt.Errorf("openai adapter: %w", err)
	}
	secureRunner, err := forward.NewSecureRunner(forward.SecureRunnerConfig{
		Repository:     store,
		CharityTargets: store,
		Secrets:        vault,
		Registry:       registry,
		Adapters:       []forward.Adapter{openAIAdapter},
	})
	if err != nil {
		return nil, fmt.Errorf("secure runner: %w", err)
	}
	usageService, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		return nil, fmt.Errorf("usage service: %w", err)
	}
	safetyIdentifierKey, err := vault.DeriveSubkey([]byte(forward.SafetyIdentifierSubkeyInfo))
	if err != nil {
		return nil, fmt.Errorf("safety identifier subkey: %w", err)
	}
	safetyIdentifierFactory, err := forward.NewSafetyIdentifierFactory(safetyIdentifierKey)
	if err != nil {
		clear(safetyIdentifierKey)
		return nil, fmt.Errorf("safety identifier factory: %w", err)
	}
	forwardService, err := forward.NewService(forward.ServiceConfig{
		Repository:          store,
		Runner:              secureRunner,
		Hooks:               forward.Hooks{Attempt: usageService.HandleAttempt, Usage: usageService.HandleUsage},
		SafetyIdentifierKey: safetyIdentifierKey,
	})
	clear(safetyIdentifierKey)
	if err != nil {
		return nil, fmt.Errorf("forward service: %w", err)
	}
	cleanup = append(cleanup, forwardService.Close)
	charityService, err := charityrouting.NewService(charityrouting.ServiceConfig{
		Store:             store,
		Runner:            secureRunner,
		SafetyIdentifiers: safetyIdentifierFactory,
	})
	if err != nil {
		return nil, fmt.Errorf("charity routing service: %w", err)
	}
	cleanup = append(cleanup, safetyIdentifierFactory.Close)
	cleanup = append(cleanup, charityService.Close)
	// Startup recovery converges stalled reservations from a previous process
	// before any request can be in flight (frozen §5.4). The periodic sweep
	// re-runs recovery at the start of every 6h maintenance round.
	charityService.RecoverAll(context.Background(), true)
	flowController, err = flowcontrol.New(flowcontrol.Config{
		RPM:        ratelimit.DefaultRPMConfig(),
		UserLimits: flowcontrol.DBUserLimitResolver(store),
	})
	if err != nil {
		return nil, fmt.Errorf("flow controller: %w", err)
	}
	cleanup = append(cleanup, flowController.Close)
	maintenanceGate := maintenance.New()
	runtimeApplier := adminapi.NewRuntimeApplier(flowController, stack, oauthStartThrottle, maintenanceGate)
	if err := applyPersistedRuntimeConfig(store, runtimeApplier); err != nil {
		return nil, fmt.Errorf("apply persisted runtime configuration: %w", err)
	}
	flowMiddleware, err := flowcontrol.NewMiddleware(flowController, forward.CallerIdentity)
	if err != nil {
		return nil, fmt.Errorf("flow middleware: %w", err)
	}

	lifecycleService, err := lifecycle.NewService(lifecycle.Config{
		Store: store, Elevation: sharedElevation, AdminVerifier: adminAuth,
		PreDeleteUser: charityService.CancelUserContexts,
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
	// The level-5 co-management frame: user-station session middleware plus a
	// per-request live level>=5 gate. No business route is registered yet
	// (later rails attach through steward.Handler.Handle); the frame itself
	// still answers the prefix with the stable 403/404 envelopes.
	stewardAPI := steward.New(steward.Deps{UserAuth: userAuth, Store: store})
	checkinAPI := checkin.New(checkin.Deps{UserAuth: userAuth, Store: store})

	// Charity donation rail (user submissions, admin/steward reviews) and the
	// charity model management rail share one service layer per surface; each
	// mounting frame resolves its own identity.
	donationSvc := donation.NewService(donation.ServiceDeps{Store: store, URLs: stack, Connectors: registry})
	charitySvc := charity.NewService(store)
	adminReviewIdentity := func(r *http.Request) (donation.ReviewerIdentity, error) {
		admin, ok := auth.AdminFromContext(r.Context())
		if !ok || admin == nil || admin.ID <= 0 {
			return donation.ReviewerIdentity{}, errors.New("admin session required")
		}
		return donation.ReviewerIdentity{UserID: admin.ID, Role: db.ReviewRoleAdmin}, nil
	}
	adminManagerIdentity := func(r *http.Request) (int64, error) {
		admin, ok := auth.AdminFromContext(r.Context())
		if !ok || admin == nil || admin.ID <= 0 {
			return 0, errors.New("admin session required")
		}
		return admin.ID, nil
	}
	adminDonationReview := donation.NewReviewHandler("/admin/api", donationSvc, adminReviewIdentity)
	adminCharity := charity.NewHandler("/admin/api", charitySvc, adminManagerIdentity)

	// Steward mounts go through the level-5 frame so every request re-resolves
	// the effective level live; the subs receive only the opaque principal.
	stewardReview := donation.NewReviewHandler("/api/steward", donationSvc, nil)
	stewardCharity := charity.NewHandler("/api/steward", charitySvc, nil)
	registerSteward := func(method, suffix string, sub func(w http.ResponseWriter, r *http.Request, p steward.Principal)) {
		stewardAPI.Handle(method, "/api/steward"+suffix, sub)
	}
	registerSteward("GET", "/donations", func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
		stewardReview.ListSub(w, r, donation.ReviewerIdentity{UserID: p.UserID, Role: p.Role})
	})
	registerSteward("GET", "/donations/{id}", func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
		stewardReview.GetSub(w, r, donation.ReviewerIdentity{UserID: p.UserID, Role: p.Role})
	})
	registerSteward("PATCH", "/donations/{id}", func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
		stewardReview.ReviewSub(w, r, donation.ReviewerIdentity{UserID: p.UserID, Role: p.Role})
	})
	registerSteward("DELETE", "/donations/{id}", func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
		stewardReview.DeleteSub(w, r, donation.ReviewerIdentity{UserID: p.UserID, Role: p.Role})
	})
	stewardCharityMounts := []struct {
		method, suffix string
		sub            func(http.ResponseWriter, *http.Request, int64)
	}{
		{"GET", "/charity-models", stewardCharity.ListSub},
		{"POST", "/charity-models", stewardCharity.CreateSub},
		{"GET", "/charity-models/{id}", stewardCharity.GetSub},
		{"PATCH", "/charity-models/{id}", stewardCharity.UpdateSub},
		{"DELETE", "/charity-models/{id}", stewardCharity.DeleteSub},
		{"GET", "/charity-models/{id}/bindings", stewardCharity.ListBindingsSub},
		{"POST", "/charity-models/{id}/bindings", stewardCharity.CreateBindingSub},
		{"PATCH", "/charity-models/{id}/bindings/{bindingId}", stewardCharity.UpdateBindingSub},
		{"DELETE", "/charity-models/{id}/bindings/{bindingId}", stewardCharity.DeleteBindingSub},
	}
	for _, m := range stewardCharityMounts {
		method, suffix, sub := m.method, m.suffix, m.sub
		stewardAPI.Handle(method, "/api/steward"+suffix, func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
			sub(w, r, p.UserID)
		})
	}
	api := buildUserAPI(userAuth, adminAuth, endpointService, modelFetcher, modelService,
		logapi.NewHandler(logapi.HandlerDeps{Store: store}),
		issues.NewHandler(issues.HandlerDeps{Store: store}),
		checkinAPI.Handler(),
		httpmw.API(donation.NewHandler(donationSvc, sessionIdentity)),
		exportHandler, lifecycleService, forwardService, charityService, flowMiddleware, store, stewardAPI.Handler())
	// The maintenance gate sits after the host/station edge (which only lets
	// /api/* and /v1/* reach the user station) and before any auth or business
	// handler. It is live-applied from site_config and loaded from the DB at
	// startup, so a toggle takes effect immediately for already-issued
	// sessions and caller keys; the admin station is never routed through it.
	gatedAPI := maintenance.GateMiddleware(maintenanceGate, api)
	appHandler := buildAdminAndRootAPI(cfg, userAuth, adminAuth, gatedAPI, adminControls, alertapi.NewHandler(alertapi.HandlerDeps{Store: store}), logapi.NewHandler(logapi.HandlerDeps{Store: store}), lifecycleService, store, forwardService, flowMiddleware,
		adminDonationReview.Handler(), adminCharity.Handler())

	maintenanceCtx, stopMaintenance := context.WithCancel(context.Background())
	app = &application{handler: appHandler, stop: stopMaintenance, close: cleanup}
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		runMaintenanceSweep(maintenanceCtx, store, usageService, charityService)
		for {
			select {
			case <-maintenanceCtx.Done():
				return
			case <-ticker.C:
				runMaintenanceSweep(maintenanceCtx, store, usageService, charityService)
			}
		}
	}()
	return app, nil
}

func runMaintenanceSweep(ctx context.Context, store *db.Store, usageService *usage.Service, charityService *charityrouting.Service) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	// Recovery runs FIRST (frozen §5.4): converge stalled charity reservations
	// before any retention sweep so a crashed in-flight call is settled
	// before its log row could be aged out.
	if charityService != nil && ctx.Err() == nil {
		charityService.RecoverAll(ctx, false)
	}
	if store != nil {
		if _, purgeErr := store.PurgeExpiredSessions(); purgeErr != nil && ctx.Err() == nil {
			slog.Error("session retention failed", "err", purgeErr)
		}
	}
	if usageService != nil && ctx.Err() == nil {
		if _, cleanupErr := usageService.CleanupRequestLogs(ctx); cleanupErr != nil && ctx.Err() == nil {
			slog.Error("request log retention failed", "err", cleanupErr)
		}
	}
	if store != nil && ctx.Err() == nil {
		if _, alertErr := store.CleanupResolvedAlerts(ctx, db.ResolvedAlertRetention); alertErr != nil && ctx.Err() == nil {
			slog.Error("resolved alert retention failed", "err", alertErr)
		}
	}
	if store != nil && ctx.Err() == nil {
		// Activity retention: day keys older than the frozen 400-day window are
		// removed in bounded batches. An unset site timezone skips the sweep —
		// no activity row can exist without a configured offset.
		cutoff, cutoffErr := store.ActivityRetentionCutoffDay(time.Now().Unix())
		switch {
		case errors.Is(cutoffErr, db.ErrTimezoneUnavailable):
			// Activity disabled: nothing to sweep.
		case cutoffErr != nil:
			slog.Error("activity retention cutoff failed", "err", cutoffErr)
		default:
			if _, early, actErr := store.CleanupActivityBefore(ctx, cutoff); actErr != nil && ctx.Err() == nil {
				slog.Error("activity retention failed", "err", actErr)
			} else if early && ctx.Err() == nil {
				slog.Info("activity retention stopped early; resuming next sweep")
			}
		}
	}
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
		adminapi.KeyOAuthStartRateLimit,
		adminapi.KeyOAuthStartRateWindowSecs,
		adminapi.KeyOAuthStartRatePenaltySecs,
		adminapi.KeyMaintenanceMode,
	} {
		if value, ok := values[key]; ok {
			if err := runtime.ApplySiteConfig(context.Background(), key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return nil
}

// servePublicConfig handles GET /api/config: an unauthenticated,
// display/legal subset of site_config plus the public maintenance and
// registration toggles. The allowlist lives in adminapi.ReadPublicConfig so the
// admin registry is the single source of truth and operational keys can
// never leak here. The response is no-store so an operator's change takes
// effect on the next page load instead of from a stale cache.
func servePublicConfig(store *db.Store, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
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

// sessionIdentity is the shared user-session resolver for rails mounted with
// their own middleware but a plain identity function.
func sessionIdentity(r *http.Request) (int64, error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || !user.IsActive() {
		return 0, errors.New("user session required")
	}
	return user.ID, nil
}

func buildUserAPI(userAuth *auth.UserAuth, adminAuth *auth.AdminAuth, endpointService *endpoint.Service, fetcher *fetch.Fetcher, modelService *model.Service, logs http.Handler, issueHandler http.Handler, checkinHandler http.Handler, donationsHandler http.Handler, exportHandler http.Handler, lifecycleService *lifecycle.Service, forwardService *forward.Service, charityService *charityrouting.Service, flowMiddleware *flowcontrol.Middleware, store *db.Store, stewardHandler http.Handler) http.Handler {
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
	// The check-in tree wires its own user-session middleware (steward-style):
	// its handlers still re-check the station and the session principal.
	userCheckin := checkinHandler
	userExport := userAuth.Middleware(exportHandler)
	userDelete := userAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.DeleteOwnAccountHandler)))
	userCharityModels := userAuth.Middleware(httpmw.API(charity.NewUserModelsHandler(charity.UserModelsDeps{Store: store})))
	forwardHandler := auth.CallerKeyMiddleware(store, flowMiddleware.Wrap(forward.NewHandler(forward.HandlerDeps{Service: forwardService, Charity: charityService, Identity: forward.CallerIdentity})))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/config":
			servePublicConfig(store, w, r)
		case path == "/api/auth/discord/start" || path == "/api/auth/discord/callback" || path == "/api/auth/elevate" || path == "/api/session" || path == "/api/me" || path == "/api/auth/logout" || path == "/api/caller-key" || path == "/api/caller-key/regenerate":
			userAuthHandler.ServeHTTP(w, r)
		case path == "/api/me/usage" || path == "/api/logs" || path == "/api/logs/options":
			userLogs.ServeHTTP(w, r)
		case path == "/api/issues" || strings.HasPrefix(path, "/api/issues/"):
			userIssues.ServeHTTP(w, r)
		case path == "/api/checkin":
			// Daily check-in (user station only; the shared user-session
			// middleware enforces the station and the session).
			userCheckin.ServeHTTP(w, r)
		case path == "/api/charity/models":
			// Charity price table (user station only; the shared user-session
			// middleware enforces the station and the session).
			userCharityModels.ServeHTTP(w, r)
		case path == "/api/donations" || strings.HasPrefix(path, "/api/donations/"):
			// Charity donation self-service (user station only).
			donationsHandler.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/api/steward/"):
			// Level-5 co-management prefix (user station only; the frame
			// itself re-checks the station, the session, and the live level).
			stewardHandler.ServeHTTP(w, r)
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

func buildAdminAndRootAPI(cfg *config.Config, userAuth *auth.UserAuth, adminAuth *auth.AdminAuth, userAPI http.Handler, adminControls http.Handler, alerts http.Handler, logs http.Handler, lifecycleService *lifecycle.Service, store *db.Store, _ *forward.Service, _ *flowcontrol.Middleware, adminDonations http.Handler, adminCharityModels http.Handler) http.Handler {
	adminLogs := adminAuth.Middleware(logs)
	adminDonationsWrapped := adminAuth.Middleware(httpmw.API(adminDonations))
	adminCharityWrapped := adminAuth.Middleware(httpmw.API(adminCharityModels))
	adminControlsHandler := adminAuth.Middleware(adminControls)
	adminAlerts := adminAuth.Middleware(alerts)
	adminElevate := adminAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.ElevateAdminHandler)))
	adminDelete := adminAuth.Middleware(httpmw.API(http.HandlerFunc(lifecycleService.DeleteUserHandler)))
	adminAuthHandler := adminAuth.Handler()
	adminBoundary := httpmw.API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/api/config":
			servePublicConfig(store, w, r)
		case r.URL.Path == "/admin/api/login" || r.URL.Path == "/admin/api/logout" || r.URL.Path == "/admin/api/session":
			adminAuthHandler.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/auth/elevate":
			adminElevate.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/users" || strings.HasPrefix(r.URL.Path, "/admin/api/users/"):
			if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/api/users/") {
				request := r.Clone(r.Context())
				request.SetPathValue("id", strings.TrimPrefix(r.URL.Path, "/admin/api/users/"))
				adminDelete.ServeHTTP(w, request)
			} else {
				adminControlsHandler.ServeHTTP(w, r)
			}
		case r.URL.Path == "/admin/api/site-config" || strings.HasPrefix(r.URL.Path, "/admin/api/site-config/"):
			adminControlsHandler.ServeHTTP(w, r)
		case r.URL.Path == "/admin/api/logs" || strings.HasPrefix(r.URL.Path, "/admin/api/logs/") || r.URL.Path == "/admin/api/usage" || strings.HasPrefix(r.URL.Path, "/admin/api/overview/"):
			adminLogs.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/admin/api/donations"):
			adminDonationsWrapped.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/admin/api/charity-models"):
			adminCharityWrapped.ServeHTTP(w, r)
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
