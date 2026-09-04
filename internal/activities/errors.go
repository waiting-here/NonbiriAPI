package activities

import (
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	moderncsqlite "modernc.org/sqlite"
)

var (
	ErrInvalidRequest      = errors.New("activities: invalid request")
	ErrUnauthorized        = errors.New("activities: unauthorized")
	ErrForbidden           = errors.New("activities: forbidden")
	ErrMaintenance         = errors.New("activities: maintenance")
	ErrFeatureDisabled     = errors.New("activities: feature disabled")
	ErrInsufficientCredits = errors.New("activities: insufficient credits")
	ErrResourceLimit       = errors.New("activities: resource limit exceeded")
	ErrNotFound            = errors.New("activities: not found")
	ErrConflict            = errors.New("activities: conflict")
	ErrUnavailable         = errors.New("activities: unavailable")
	ErrRetryable           = errors.New("activities: retryable database error")
	ErrInvariant           = errors.New("activities: invariant violation")
	ErrClosed              = errors.New("activities: settlement worker closed")
)

func classifyLedgerError(operation string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return ErrInsufficientCredits
	case errors.Is(err, ledger.ErrCapacityExhausted):
		return ErrUnavailable
	case errors.Is(err, ledger.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ledger.ErrConflict):
		return ErrConflict
	case errors.Is(err, ledger.ErrRetryable):
		return ErrRetryable
	case errors.Is(err, ledger.ErrInvalidAmount), errors.Is(err, ledger.ErrInvalidPlan), errors.Is(err, ledger.ErrInvalidReservation):
		return fmt.Errorf("%w: %s", ErrInvariant, operation)
	case errors.Is(err, ledger.ErrInvariant):
		return fmt.Errorf("%w: %s", ErrInvariant, operation)
	default:
		return fmt.Errorf("activities: %s: %w", operation, err)
	}
}

func classifyAuthorizationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrUnauthorized), errors.Is(err, authz.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, ErrForbidden), errors.Is(err, authz.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, authz.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrMaintenance):
		return ErrMaintenance
	default:
		return fmt.Errorf("activities: final authorization: %w", err)
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
	return fmt.Errorf("activities: %s: %w", operation, err)
}
