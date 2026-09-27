package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/applog"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type processResources struct {
	config    *config.Config
	vault     *secret.Vault
	store     *db.Store
	app       *application
	listener  net.Listener
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

// A deadline bounds the caller's wait; resources remain owned until the
// operation really completes, including non-interruptible system calls.
func (p *processResources) CloseContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("process cleanup context is required")
	}
	p.closeOnce.Do(func() {
		p.closeDone = make(chan struct{})
		go func() {
			defer close(p.closeDone)
			var failures []error
			if p.listener != nil {
				_ = p.listener.Close()
			}
			if p.app != nil {
				if err := p.app.CloseContext(context.Background()); err != nil {
					failures = append(failures, err)
				}
			}
			if p.store != nil {
				if err := p.store.CloseContext(context.Background()); err != nil {
					failures = append(failures, err)
				}
			}
			if p.vault != nil {
				if err := p.vault.Close(); err != nil {
					failures = append(failures, err)
				}
			}
			p.closeErr = errors.Join(failures...)
		}()
	})
	select {
	case <-p.closeDone:
		return p.closeErr
	default:
	}
	select {
	case <-p.closeDone:
		return p.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type startupResult struct {
	resources *processResources
	err       error
}

func (p *processResources) cleanupPending() bool {
	if p == nil || p.closeDone == nil {
		return false
	}
	select {
	case <-p.closeDone:
		return false
	default:
		return true
	}
}

type startupAttempt struct {
	result chan startupResult
	done   chan struct{}
}

func initializeProcess(ctx context.Context) (*processResources, error) {
	p := &processResources{}
	db.RecordStartupStage(ctx, db.StageConfiguration)
	cfg, err := config.Load()
	if err != nil {
		return p, err
	}
	p.config = cfg
	p.vault = cfg.TakeSecretVault()
	if p.vault == nil {
		return p, errors.New("secret vault initialization failed")
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	slog.SetDefault(applog.New(os.Stdout, applog.ParseLevel(cfg.LogLevel)))
	p.store, err = db.OpenContext(ctx, cfg.DBPath, p.vault)
	if err != nil {
		return p, err
	}
	p.app, err = buildApplication(ctx, cfg, p.store, p.vault)
	if err != nil {
		var pending *applicationCleanupError
		if errors.As(err, &pending) {
			p.app = pending.pending
		}
		return p, err
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	p.listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return p, err
	}
	db.RecordStartupStage(ctx, db.StageListenerBound)
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if p.app.readiness.failed.Load() {
		return p, errors.New("critical worker unavailable")
	}
	return p, nil
}

// Late initialization results are cleaned up by their original owner. The
// done channel records completion without pretending a timeout stopped I/O.
func beginStartup(ctx context.Context, initialize func(context.Context) (*processResources, error)) *startupAttempt {
	attempt := &startupAttempt{result: make(chan startupResult), done: make(chan struct{})}
	go func() {
		defer close(attempt.done)
		resources, err := initialize(ctx)
		select {
		case attempt.result <- startupResult{resources: resources, err: err}:
		case <-ctx.Done():
			if err := resources.CloseContext(ctx); err != nil {
				slog.Error("startup cleanup pending", "category", "cleanup_deadline", "cleanup_pending", true)
			}
		}
	}()
	return attempt
}

func logStartupProgress(progress db.StartupProgress) {
	slog.Info("startup progress", "stage", progress.Stage, "elapsed_ms", progress.ElapsedMS,
		"stage_elapsed_ms", progress.StageElapsedMS, "processed_count", progress.ProcessedCount,
		"bytes", progress.ProcessedBytes, "committed_checkpoint", progress.LastCommittedPhase)
}

func logStartupFailure(trace *db.StartupTrace, err error, cleanupPending bool) {
	progress := trace.Snapshot()
	if progress.FailureStage != "" {
		progress.Stage = progress.FailureStage
	}
	category, retryable, secondary := db.StartupFailureDetails(err)
	slog.Error("startup failed", "stage", progress.Stage, "elapsed_ms", progress.ElapsedMS,
		"processed_count", progress.ProcessedCount, "bytes", progress.ProcessedBytes,
		"primary_cause", category, "category", category, "retryability", retryable,
		"secondary", secondary, "last_committed_phase", progress.LastCommittedPhase,
		"cleanup_pending", cleanupPending)
}

func run() int {
	started := time.Now()
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupBudget, shutdownBudget, err := config.LoadTimeouts()
	if err != nil {
		slog.Error("startup configuration rejected", "category", "invalid_timeout", "details", err)
		return 2
	}
	startupContext, cancelStartup := context.WithDeadline(signalContext, started.Add(startupBudget))
	defer cancelStartup()
	trace := db.NewStartupTrace(started, logStartupProgress)
	startupContext = trace.Context(startupContext)
	attempt := beginStartup(startupContext, initializeProcess)
	var resources *processResources
	select {
	case result := <-attempt.result:
		resources, err = result.resources, result.err
		if err == nil {
			err = startupContext.Err()
		}
		if err != nil {
			cleanupErr := resources.CloseContext(startupContext)
			logStartupFailure(trace, db.WithStartupCleanupError(err, cleanupErr), resources.cleanupPending())
			return 1
		}
	case <-startupContext.Done():
		logStartupFailure(trace, startupContext.Err(), true)
		return 1
	}
	app := resources.app
	if app.readiness.failed.Load() {
		cleanupErr := resources.CloseContext(startupContext)
		logStartupFailure(trace, db.WithStartupCleanupError(errors.New("critical worker unavailable"), cleanupErr), resources.cleanupPending())
		return 1
	}
	server := &http.Server{Addr: resources.config.ListenAddr, Handler: app.handler,
		ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	serveErrors := make(chan error, 1)
	app.readiness.ready.Store(true)
	db.RecordStartupStage(startupContext, db.StageReady)
	slog.Info("application_ready")
	cancelStartup()
	go func() { serveErrors <- server.Serve(resources.listener) }()
	exitCode := 0
	select {
	case <-signalContext.Done():
	case <-serveErrors:
		exitCode = 1
		slog.Error("HTTP listener stopped unexpectedly", "category", "listener_stopped")
	case <-app.failures:
		exitCode = 1
		slog.Error("critical application worker stopped", "category", "worker_unavailable")
	}
	app.withdrawReadiness()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancelShutdown()
	if err := app.BeginShutdownContext(shutdownContext); err != nil {
		exitCode = 1
	}
	if err := server.Shutdown(shutdownContext); err != nil {
		// Do not close the Store or codec beneath an HTTP handler that has not
		// drained. The process exits nonzero; this registered owner can finish
		// cleanup if the final handler returns before the process exits.
		app.cancelWorkers()
		go func() {
			_ = server.Shutdown(context.Background())
			_ = resources.CloseContext(context.Background())
		}()
		slog.Error("shutdown incomplete", "stage", "http_drain", "category", "cleanup_deadline",
			"cleanup_pending", true, "unclosed_connections", resources.store.DB().Stats().OpenConnections)
		return 1
	}
	if err := resources.CloseContext(shutdownContext); err != nil {
		connections := resources.store.DB().Stats().OpenConnections
		category := "cleanup_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			category = "cleanup_deadline"
		}
		slog.Error("shutdown incomplete", "category", category, "cleanup_pending", resources.cleanupPending(),
			"unclosed_connections", connections)
		return 1
	}
	if shutdownContext.Err() != nil {
		return 1
	}
	slog.Info("shutdown complete")
	return exitCode
}
