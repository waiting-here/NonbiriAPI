package host

import (
	"context"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var (
	ErrInvalidRequest     = errors.New("game host: invalid request")
	ErrUnauthorized       = errors.New("game host: unauthorized")
	ErrForbidden          = errors.New("game host: forbidden")
	ErrNotFound           = errors.New("game host: not found")
	ErrConflict           = errors.New("game host: conflict")
	ErrMaintenance        = errors.New("game host: maintenance")
	ErrResourceLimit      = errors.New("game host: resource limit")
	ErrServiceUnavailable = errors.New("game host: service unavailable")
	ErrInvariant          = errors.New("game host: invariant violation")
	ErrClosed             = errors.New("game host: closed")
)

func classifyDB(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "database is locked") || strings.Contains(message, "database is busy") || strings.Contains(message, "sqlite_busy") || errors.Is(err, ledger.ErrRetryable) {
		return ErrServiceUnavailable
	}
	return err
}

func mapAuthorization(err error) error {
	switch {
	case errors.Is(err, authz.ErrUnauthorized), errors.Is(err, resources.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, authz.ErrElevatedRequired), errors.Is(err, resources.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, authz.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, resources.ErrMaintenance):
		return ErrMaintenance
	default:
		return classifyDB(err)
	}
}
func mapIdempotency(err error) error {
	switch {
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return ErrConflict
	case errors.Is(err, idempotency.ErrState):
		return ErrInvariant
	default:
		return classifyDB(err)
	}
}
func mapIdempotencyComplete(err error) error {
	if errors.Is(err, idempotency.ErrState) {
		return ErrInvariant
	}
	return classifyDB(err)
}
