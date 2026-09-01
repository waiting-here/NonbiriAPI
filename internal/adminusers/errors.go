package adminusers

import (
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	moderncsqlite "modernc.org/sqlite"
)

var (
	ErrInvalidRequest  = errors.New("adminusers: invalid request")
	ErrUnauthorized    = errors.New("adminusers: unauthorized")
	ErrForbidden       = errors.New("adminusers: forbidden")
	ErrNotFound        = errors.New("adminusers: not found")
	ErrConflict        = errors.New("adminusers: conflict")
	ErrPayloadTooLarge = errors.New("adminusers: payload too large")
	ErrResourceLimit   = errors.New("adminusers: resource limit exceeded")
	ErrUnavailable     = errors.New("adminusers: unavailable")
	ErrRetryable       = errors.New("adminusers: retryable database error")
	ErrInvariant       = errors.New("adminusers: invariant violation")
)

func classifyAuthorizationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnauthorized), errors.Is(err, authz.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, ErrForbidden), errors.Is(err, authz.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, authz.ErrNotFound):
		return ErrNotFound
	default:
		return fmt.Errorf("adminusers: final authorization: %w", err)
	}
}

func classifyLedgerError(operation string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrConflict
	case errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
		return ErrUnavailable
	case errors.Is(err, ledger.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ledger.ErrConflict):
		return ErrConflict
	case errors.Is(err, ledger.ErrInvalidAmount), errors.Is(err, ledger.ErrInvalidPlan), errors.Is(err, ledger.ErrInvalidReservation), errors.Is(err, ledger.ErrInvariant):
		return fmt.Errorf("%w: %s", ErrInvariant, operation)
	default:
		return fmt.Errorf("adminusers: %s: %w", operation, err)
	}
}

func classifyDatabaseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *moderncsqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code() & 0xff
		if code == 5 || code == 6 {
			return fmt.Errorf("%w: %s", ErrRetryable, operation)
		}
	}
	return fmt.Errorf("adminusers: %s: %w", operation, err)
}
