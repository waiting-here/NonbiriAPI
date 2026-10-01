package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type readinessState struct {
	ready  atomic.Bool
	failed atomic.Bool
}

func (s *readinessState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if requestHasQuery(r) || requestCarriesBody(r) {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s != nil && s.ready.Load() && !s.failed.Load() {
		httperr.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	httperr.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
}

type applicationCleanupError struct {
	cause   error
	primary error
	phase   string
	pending *application
}

func (e *applicationCleanupError) Error() string {
	if e.primary != nil {
		return e.primary.Error()
	}
	return "application cleanup deadline exceeded"
}
func (e *applicationCleanupError) Unwrap() error {
	if e.primary != nil {
		return e.primary
	}
	return e.cause
}
func (e *applicationCleanupError) SecondaryCategories() []string {
	if e.primary == nil {
		return nil
	}
	_, _, secondary := db.StartupFailureDetails(e.primary)
	if len(secondary) < 8 {
		secondary = append(secondary, "cleanup_deadline")
	}
	return secondary
}

func (a *application) setClosePhase(phase string) {
	a.closeMu.Lock()
	a.closePhase = phase
	a.closeMu.Unlock()
}

func (a *application) pendingCleanup(ctx context.Context) error {
	a.closeMu.Lock()
	phase := a.closePhase
	a.closeMu.Unlock()
	return &applicationCleanupError{cause: ctx.Err(), phase: phase, pending: a}
}

func (a *application) withdrawReadiness() {
	if a != nil && a.readiness != nil {
		a.readiness.ready.Store(false)
	}
}

func (a *application) cancelWorkers() {
	a.workerCancelOnce.Do(func() {
		if a.workerCancel != nil {
			a.workerCancel()
		}
		if a.rankingCancel != nil {
			a.rankingCancel()
		}
		if a.lifecycleCancel != nil {
			a.lifecycleCancel()
		}
	})
}

// Admission closes before HTTP drain; worker cancellation belongs to Close.
// All callers wait for the same operation, including after an expired budget.
func (a *application) BeginShutdownContext(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	a.beginShutdown()
	select {
	case <-a.shutdownDone:
		return nil
	default:
	}
	select {
	case <-a.shutdownDone:
		return nil
	case <-ctx.Done():
		return a.pendingCleanup(ctx)
	}
}

func (a *application) beginShutdown() {
	a.withdrawReadiness()
	a.shutdownOnce.Do(func() {
		a.shutdownDone = make(chan struct{})
		a.setClosePhase("admission")
		go func() {
			defer close(a.shutdownDone)
			if a.automation != nil {
				_ = a.automation.Close()
			}
			if a.accountEvents != nil {
				_ = a.accountEvents.Close()
			}
			if a.forward != nil {
				a.forward.BeginShutdown()
			}
			if a.debug != nil {
				_ = a.debug.Close()
			}
		}()
	})
}

func (a *application) BeginShutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), db.DefaultShutdownTimeout)
	defer cancel()
	_ = a.BeginShutdownContext(ctx)
}

func (a *application) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), db.DefaultShutdownTimeout)
	defer cancel()
	return a.CloseContext(ctx)
}

func (a *application) CloseContext(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("close application: context is required")
	}
	a.closeOnce.Do(func() {
		a.closeDone = make(chan struct{})
		// Initiate admission closure even when the caller's budget has expired.
		a.beginShutdown()
		go func() {
			defer close(a.closeDone)
			<-a.shutdownDone
			a.setClosePhase("workers")
			a.cancelWorkers()
			if a.rankingDone != nil {
				<-a.rankingDone
			}
			if a.lifecycleDone != nil {
				<-a.lifecycleDone
			}
			a.setClosePhase("domains")
			var failures []error
			closeResource := func(close func() error) {
				if err := close(); err != nil {
					failures = append(failures, err)
				}
			}
			if a.lifecycle != nil {
				closeResource(a.lifecycle.Close)
			}
			if a.activityRuntime != nil {
				closeResource(a.activityRuntime.Close)
			}
			if a.reports != nil {
				closeResource(a.reports.Close)
			}
			if a.games != nil {
				closeResource(a.games.Close)
			}
			if a.forward != nil {
				closeResource(a.forward.Close)
			}
			if a.audits != nil {
				closeResource(a.audits.Close)
			}
			if a.activityEvents != nil {
				closeResource(a.activityEvents.Close)
			}
			if a.authRuntime != nil {
				closeResource(a.authRuntime.Close)
			} else if a.elevation != nil {
				closeResource(a.elevation.Close)
			}
			if a.discoveryWorker != nil {
				a.discoveryWorker.Close()
			}
			if a.bridge != nil {
				closeResource(a.bridge.Close)
			}
			if a.egress != nil {
				a.egress.CloseIdleConnections()
			}
			a.closeErr = errors.Join(failures...)
			a.setClosePhase("closed")
		}()
	})
	select {
	case <-a.closeDone:
		return a.closeErr
	default:
	}
	select {
	case <-a.closeDone:
		return a.closeErr
	case <-ctx.Done():
		return a.pendingCleanup(ctx)
	}
}
