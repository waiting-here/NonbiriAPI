package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/imageactivity"
	"github.com/waiting-here/NonbiriAPI/internal/inactivity"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type activityRuntime struct {
	limited     *limitedactivities.Service
	images      *imageactivity.Service
	inactivity  *inactivity.Service
	cancelGames inactivity.CancelUserTx
	now         func() time.Time
	cancel      context.CancelFunc
	workers     sync.WaitGroup
	closeOnce   sync.Once
	recovered   atomic.Bool
}

type activeActivityRecorder struct{}

func (activeActivityRecorder) RecordLimitedActivityTx(ctx context.Context, tx *sql.Tx, user, at int64) error {
	return inactivity.RecordActiveTx(ctx, tx, inactivity.ActiveEvent{UserID: user, At: at, Kind: "activity", Fresh: true})
}

type limitedUserAuthority struct{ runtime *auth.Runtime }

func (a limitedUserAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	err := a.runtime.AuthorizeUserMutation(ctx, tx, user)
	switch {
	case errors.Is(err, resources.ErrUnauthorized):
		return authz.ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden):
		return authz.ErrForbidden
	case errors.Is(err, resources.ErrNotFound):
		return authz.ErrNotFound
	default:
		return err
	}
}

type limitedAdmissionGate struct {
	service *maintenance.Service
	now     func() time.Time
}

func (g limitedAdmissionGate) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, user int64) error {
	return g.service.AuthorizeChatAcceptance(ctx, tx, user, g.now().Unix())
}

func newActivityRuntime(store *db.Store, vault *secret.Vault, sessions *auth.Runtime, roles *roleFinalTxAuthorizer,
	gate *maintenance.Service, outbound *egress.Stack, audits *auditRuntime, invalidator inactivity.Invalidator,
	cancelGames inactivity.CancelUserTx, now func() time.Time) (*activityRuntime, error) {
	if now == nil {
		now = time.Now
	}
	a := &activityRuntime{now: now, cancelGames: cancelGames}
	var reportMu sync.Mutex
	var lastReport time.Time
	reportImageFailure := func(error) {
		reportMu.Lock()
		defer reportMu.Unlock()
		if time.Since(lastReport) < time.Minute {
			return
		}
		lastReport = time.Now()
		slog.Error("image activity worker encountered an operational failure")
	}
	users := limitedUserAuthority{sessions}
	admission := limitedAdmissionGate{gate, now}
	var err error
	a.images, err = imageactivity.New(imageactivity.Config{
		Database: store.DB(), Users: users, Admins: roles, Gate: admission,
		Admission: func(ctx context.Context, tx *sql.Tx, user int64, key string, at int64) (limitedactivities.Detail, error) {
			if a.limited == nil {
				return limitedactivities.Detail{}, limitedactivities.ErrClosed
			}
			return a.limited.CheckAdmissionTx(ctx, tx, user, key, at)
		},
		Vault: vault, Egress: outbound, Sources: audits.observations, Diagnostics: audits.observations,
		Activity: activeActivityRecorder{}, Now: now, ReportError: reportImageFailure,
	})
	if err != nil {
		return nil, err
	}
	a.limited, err = limitedactivities.New(limitedactivities.Config{
		Database: store.DB(), Users: users, Admins: roles, Gate: admission, Keys: vault,
		Registry: limitedactivities.NewRegistry(a.images), Activity: activeActivityRecorder{}, Now: now,
	})
	if err == nil {
		a.inactivity, err = inactivity.New(inactivity.Config{
			Database: store.DB(), FinalAuth: roles, CancelUserTx: a.CancelUserTx,
			Invalidator: invalidator, Now: now,
		})
	}
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

// All account penalties share one transaction and one exactly-once publication
// path, so queue refunds cannot commit separately from the account restriction.
func (a *activityRuntime) CancelUserTx(ctx context.Context, tx *sql.Tx, user int64, reason string, at int64) (func(bool), error) {
	gameFinish, err := a.cancelGames(ctx, tx, user, reason, at)
	if err != nil {
		return gameFinish, err
	}
	if gameFinish == nil {
		return nil, limitedactivities.ErrInvariant
	}
	f, err := a.limited.PrepareBanTx(ctx, tx, user, at)
	if err != nil {
		gameFinish(false)
		return nil, err
	}
	var once sync.Once
	return func(committed bool) {
		once.Do(func() {
			if committed {
				f.Commit()
			} else {
				f.Abort()
			}
			gameFinish(committed)
		})
	}, nil
}

func (a *activityRuntime) PrepareMaintenanceTx(ctx context.Context, tx *sql.Tx, at int64) (func(bool), error) {
	if a == nil {
		return nil, limitedactivities.ErrInvariant
	}
	f, err := a.limited.PrepareMaintenanceTx(ctx, tx, at)
	if err != nil {
		return nil, err
	}
	return func(committed bool) {
		if committed {
			f.Commit()
		} else {
			f.Abort()
		}
	}, nil
}

func (a *activityRuntime) Start(failures chan<- error) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.workers.Add(2)
	go func() {
		defer a.workers.Done()
		if err := a.images.Run(ctx); err != nil && ctx.Err() == nil {
			select {
			case failures <- err:
			default:
			}
		}
	}()
	go func() {
		defer a.workers.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if _, err := a.inactivity.Process(ctx, a.now().Unix(), 100, time.Now().Add(2*time.Second)); err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
				slog.Error("inactivity policy batch failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (a *activityRuntime) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		if a.images != nil {
			_ = a.images.Close()
		}
		a.workers.Wait()
	})
	return nil
}

type limitedRoutes struct{ sessions *auth.Runtime }

func (r limitedRoutes) RegisterUserRoute(method, path string, handler limitedactivities.AuthorizedUserHandler) error {
	if method == http.MethodGet {
		return r.sessions.RegisterContinuationUserRoute(method, path, func(w http.ResponseWriter, req *http.Request, p resources.ContinuationUserPrincipal) {
			handler(w, req, limitedactivities.UserPrincipal{UserID: p.UserID})
		})
	}
	return r.sessions.RegisterUserRoute(method, path, func(w http.ResponseWriter, req *http.Request, p resources.UserPrincipal) {
		handler(w, req, limitedactivities.UserPrincipal{UserID: p.UserID})
	})
}
func (r limitedRoutes) RegisterAdminRoute(method, path string, handler limitedactivities.AuthorizedAdminHandler) error {
	return registerActivityAdmin(r.sessions, method, path, func(w http.ResponseWriter, req *http.Request, user int64) {
		handler(w, req, limitedactivities.AdminPrincipal{UserID: user})
	})
}

type imageRoutes struct{ sessions *auth.Runtime }

func (r imageRoutes) RegisterUserRoute(method, path string, handler imageactivity.AuthorizedUserHandler) error {
	continuation := method == http.MethodGet || (method == http.MethodPost && strings.HasSuffix(path, "/cancel"))
	if continuation {
		return r.sessions.RegisterContinuationUserRoute(method, path, func(w http.ResponseWriter, req *http.Request, p resources.ContinuationUserPrincipal) {
			handler(w, req, imageactivity.UserPrincipal{UserID: p.UserID})
		})
	}
	return r.sessions.RegisterUserRoute(method, path, func(w http.ResponseWriter, req *http.Request, p resources.UserPrincipal) {
		handler(w, req, imageactivity.UserPrincipal{UserID: p.UserID})
	})
}
func (r imageRoutes) RegisterAdminRoute(method, path string, handler imageactivity.AuthorizedAdminHandler) error {
	return registerActivityAdmin(r.sessions, method, path, func(w http.ResponseWriter, req *http.Request, user int64) {
		handler(w, req, imageactivity.AdminPrincipal{UserID: user})
	})
}
func registerActivityAdmin(sessions *auth.Runtime, method, path string, handler func(http.ResponseWriter, *http.Request, int64)) error {
	return sessions.RegisterAdminRoute(method, path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		actor, ok := auth.ActorFromContext(req.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(w, req, actor.UserID)
	}))
}

func (a *activityRuntime) RegisterRoutes(sessions *auth.Runtime) error {
	l, i := limitedRoutes{sessions}, imageRoutes{sessions}
	if err := limitedactivities.RegisterRoutes(l, l, a.limited); err != nil {
		return err
	}
	if err := imageactivity.RegisterRoutes(i, i, a.images); err != nil {
		return err
	}
	if err := inactivity.RegisterAdminRoutes(sessions, a.inactivity); err != nil {
		return err
	}
	return sessions.RegisterUserRoute(http.MethodGet, "/api/inactivity-policy/status", func(w http.ResponseWriter, r *http.Request, _ resources.UserPrincipal) {
		a.inactivity.StatusHTTP(w, r)
	})
}
