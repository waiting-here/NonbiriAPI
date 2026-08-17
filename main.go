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

	slog.Info("startup",
		"listen_addr", cfg.ListenAddr,
		"db_path", cfg.DBPath,
		"log_level", cfg.LogLevel,
		"user_host", cfg.UserHost,
		"admin_host", cfg.AdminHost,
		"master_source", cfg.MasterSource,
		"trusted_proxies", len(cfg.TrustedProxyCIDRs),
		"smtp_enabled", cfg.SMTP.Enabled,
	)

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("database open failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	slog.Info("database ready", "path", cfg.DBPath)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", web.NewMultiHandler())

	srv := &http.Server{
		Addr:        cfg.ListenAddr,
		Handler:     mux,
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
// balancers can reach it; it never touches the database.
func healthz(w http.ResponseWriter, _ *http.Request) {
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}