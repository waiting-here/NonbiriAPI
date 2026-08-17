// Command nonbiriapi is the Phase 0 backend skeleton: it loads startup
// configuration from environment variables, opens the SQLite database and
// bootstraps the schema, wires a redacting structured logger, and starts an
// HTTP server exposing a health check and the embedded dual-station SPA shell.
//
// No business logic lives here yet: endpoint/key/model CRUD, forwarding,
// routing, fetching, identity, and traffic control are implemented by later
// rails. This entrypoint exists so the process boots, the database schema is
// materialized, and the go:embed SPA shell is reachable.
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

	"nonbiriapi/internal/applog"
	"nonbiriapi/internal/config"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
	"nonbiriapi/internal/secret"
	"nonbiriapi/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Configuration failed before the structured logger exists; print the
		// full field list plainly and exit non-zero to fail fast.
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
		_ = secretVault.Close()
		slog.Error("database open failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = secretVault.Close() }()
	defer func() { _ = store.Close() }()
	slog.Info("database ready", "path", cfg.DBPath)

	rootHandler, err := newHTTPHandler(cfg)
	if err != nil {
		slog.Error("http boundary initialization failed", "err", err)
		return
	}

	srv := &http.Server{
		Addr:        cfg.ListenAddr,
		Handler:     rootHandler,
		ReadTimeout: 15 * time.Second,
		// WriteTimeout is left at 0: the platform exit streams long-lived SSE
		// responses whose duration is bounded per-route by later rails, not by
		// a blanket server write deadline.
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

// healthz is the liveness probe. It is intentionally unauthenticated so load
// balancers can reach it; it never touches the database. The host boundary
// still runs before this handler, so only the two configured stations expose
// the probe.
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

func newHTTPHandler(cfg *config.Config) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)

	// These explicit mounts are the future user/admin handler boundaries. An
	// unimplemented API path returns the same bounded JSON not-found envelope
	// as a real route; it never falls through to an HTML SPA document.
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
