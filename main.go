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
	"syscall"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/applog"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
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
	handler http.Handler
}

func (a *application) Close() error {
	return nil
}

func buildApplication(cfg *config.Config, store *db.Store, vault *secret.Vault) (*application, error) {
	if cfg == nil || store == nil || vault == nil {
		return nil, errors.New("application dependencies are required")
	}
	handler, err := stationBoundary(cfg, freshSafeMux(cfg, store))
	if err != nil {
		return nil, err
	}
	return &application{handler: handler}, nil
}
