// Package lifecycle implements account-deletion self-service and administrator
// destructive actions behind the two-step elevation boundary, plus the
// shared elevated-action capability consume. It is the Phase 4 track T
// service layer over internal/db.DeleteUserAccount and internal/elevation.
//
// Identity comes only from the auth session context (track H): a normal-user
// session for self-service delete, an administrator session for admin delete.
// Both destructive paths require a single-use elevated-action capability
// (internal/elevation) bound to the acting identity; the capability is
// atomically consumed before the delete transaction runs, so at most one
// destructive action can proceed per elevation.
package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// Sentinel errors mapped by the HTTP layer to stable codes. None carries
// request or credential material.
var (
	// ErrElevationRequired means the elevated capability was missing, expired,
	// already consumed, or bound to a different identity.
	ErrElevationRequired = errors.New("elevated capability required")
	// ErrInvalidCredentials means the administrator re-input password did not
	// match (second-factor failure).
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	// ErrAccountGone means the target account was already absent (a concurrent
	// or prior delete won the writer lock); self-service maps this to conflict.
	ErrAccountGone = errors.New("account no longer exists")
)

// AdminPasswordVerifier is the narrow second-factor hook. *auth.AdminAuth
// satisfies it via its constant-time AdminCredentialCheck; the lifecycle
// package never sees the configured password.
type AdminPasswordVerifier interface {
	AdminCredentialCheck(username, password string) bool
}

// Config wires the lifecycle service. Store, Elevation, and AdminVerifier are
// required; Throttle defaults to a bounded in-memory elevate-attempt limiter
// when nil (owned by the service).
type Config struct {
	Store         *db.Store
	Elevation     *elevation.Manager
	AdminVerifier AdminPasswordVerifier
	Throttle      auth.LoginThrottle
	// PreDeleteUser is invoked synchronously BEFORE the account-deletion
	// transaction opens. It lets an in-flight routing rail (the charity exit)
	// cancel the user's active upstream contexts so a late callback linearizes
	// against the delete instead of racing it. It must not block on the DB.
	PreDeleteUser func(userID int64)
}

// Service is the mountable account-lifecycle service. It owns only an
// optional default elevate throttle; the caller owns the store, the elevation
// manager, and the injected verifier/throttle.
type Service struct {
	store         *db.Store
	elevation     *elevation.Manager
	adminVerifier AdminPasswordVerifier
	throttle      auth.LoginThrottle
	ownedThrottle *ratelimit.LoginThrottle
	preDeleteUser func(userID int64)
}

// NewService validates the configuration and returns a mountable service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("lifecycle: store is required")
	}
	if cfg.Elevation == nil {
		return nil, errors.New("lifecycle: elevation manager is required")
	}
	if cfg.AdminVerifier == nil {
		return nil, errors.New("lifecycle: admin verifier is required")
	}
	svc := &Service{
		store:         cfg.Store,
		elevation:     cfg.Elevation,
		adminVerifier: cfg.AdminVerifier,
		throttle:      cfg.Throttle,
		preDeleteUser: cfg.PreDeleteUser,
	}
	if svc.throttle == nil {
		throttle, err := ratelimit.NewLoginThrottle(ratelimit.DefaultLoginThrottleConfig())
		if err != nil {
			return nil, errors.New("lifecycle: elevate throttle unavailable")
		}
		svc.throttle = throttle
		svc.ownedThrottle = throttle
	}
	return svc, nil
}

// Close releases an internally-created elevate throttle. Injected throttles,
// the store, and the elevation manager remain owned by their caller.
func (s *Service) Close() error {
	if s == nil || s.ownedThrottle == nil {
		return nil
	}
	return s.ownedThrottle.Close()
}

// Elevation exposes the shared capability manager so a two-step elevation
// endpoint (track S user-side Discord re-authorization, or tests) can issue
// capabilities that this service consumes.
func (s *Service) Elevation() *elevation.Manager { return s.elevation }

// ElevateAdmin verifies the administrator second-factor password and issues a
// single-use elevated-action capability bound to the administrator identity.
// The caller has already authenticated the admin session; this is the second
// step only. The returned token is shown once and must be sent back as the
// X-Elevated-Token header by the destructive call.
func (s *Service) ElevateAdmin(ctx context.Context, admin *db.User, password string) (string, time.Time, error) {
	return s.ElevateAdminBound(ctx, admin, password, "")
}

// ElevateAdminBound issues a capability bound to the active admin session.
// Production handlers use this form; the unbound wrapper remains for narrow
// compatibility tests and must not be used as a browser integration boundary.
func (s *Service) ElevateAdminBound(ctx context.Context, admin *db.User, password, sessionBinding string) (string, time.Time, error) {
	if s == nil || s.elevation == nil || s.adminVerifier == nil {
		return "", time.Time{}, errors.New("lifecycle: service is misconfigured")
	}
	if admin == nil || !admin.IsAdmin {
		return "", time.Time{}, ErrInvalidCredentials
	}
	if !s.adminVerifier.AdminCredentialCheck(admin.Username, password) {
		return "", time.Time{}, ErrInvalidCredentials
	}
	var token string
	var expires time.Time
	var err error
	if sessionBinding == "" {
		token, expires, err = s.elevation.Issue(admin.ID, elevation.KindAdmin)
	} else {
		token, expires, err = s.elevation.IssueBound(admin.ID, elevation.KindAdmin, sessionBinding)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// DeleteOwnAccount atomically consumes the user's elevated-action capability
// and deletes the account. The confirm token ("DELETE") is validated by the
// HTTP layer before this call. ErrAccountGone is returned when a concurrent
// delete already removed the account (self-service maps it to conflict).
func (s *Service) DeleteOwnAccount(ctx context.Context, user *db.User, elevatedToken string) error {
	return s.DeleteOwnAccountBound(ctx, user, elevatedToken, "")
}

// DeleteOwnAccountBound consumes a user capability bound to the active user
// session before deleting the account.
func (s *Service) DeleteOwnAccountBound(ctx context.Context, user *db.User, elevatedToken, sessionBinding string) error {
	if s == nil || s.elevation == nil || s.store == nil {
		return errors.New("lifecycle: service is misconfigured")
	}
	if user == nil || user.IsAdmin {
		return ErrElevationRequired
	}
	var consumeErr error
	if sessionBinding == "" {
		consumeErr = s.elevation.Consume(elevatedToken, user.ID, elevation.KindUser)
	} else {
		consumeErr = s.elevation.ConsumeBound(elevatedToken, user.ID, elevation.KindUser, sessionBinding)
	}
	if consumeErr != nil {
		return ErrElevationRequired
	}
	if s.preDeleteUser != nil {
		s.preDeleteUser(user.ID)
	}
	if err := s.store.DeleteUserAccount(ctx, user.ID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrAccountGone
		}
		// A normal user can never be ErrAdminProtected through this path; map
		// any unexpected protected sentinel to elevation-required so the
		// caller re-authenticates instead of seeing an internal leak.
		if errors.Is(err, db.ErrAdminProtected) {
			return ErrElevationRequired
		}
		return err
	}
	return nil
}

// DeleteUserAsAdmin atomically consumes the administrator's elevated-action
// capability and deletes the target account. ErrAdminProtected and
// ErrNotFound from the repository are returned distinctly so the HTTP layer
// can map them to forbidden / not_found.
func (s *Service) DeleteUserAsAdmin(ctx context.Context, admin *db.User, targetUserID int64, elevatedToken string) error {
	return s.DeleteUserAsAdminBound(ctx, admin, targetUserID, elevatedToken, "")
}

// DeleteUserAsAdminBound consumes a capability bound to the active admin
// session before deleting the target normal user.
func (s *Service) DeleteUserAsAdminBound(ctx context.Context, admin *db.User, targetUserID int64, elevatedToken, sessionBinding string) error {
	if s == nil || s.elevation == nil || s.store == nil {
		return errors.New("lifecycle: service is misconfigured")
	}
	if admin == nil || !admin.IsAdmin {
		return ErrElevationRequired
	}
	if targetUserID <= 0 {
		return db.ErrNotFound
	}
	var consumeErr error
	if sessionBinding == "" {
		consumeErr = s.elevation.Consume(elevatedToken, admin.ID, elevation.KindAdmin)
	} else {
		consumeErr = s.elevation.ConsumeBound(elevatedToken, admin.ID, elevation.KindAdmin, sessionBinding)
	}
	if consumeErr != nil {
		return ErrElevationRequired
	}
	if s.preDeleteUser != nil {
		s.preDeleteUser(targetUserID)
	}
	return s.store.DeleteUserAccount(ctx, targetUserID)
}
