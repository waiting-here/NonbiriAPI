package adminalerts

import (
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

var (
	ErrInvalidRequest = errors.New("administrator alerts: invalid request")
	ErrUnauthorized   = errors.New("administrator alerts: unauthorized")
	ErrForbidden      = errors.New("administrator alerts: forbidden")
	ErrNotFound       = errors.New("administrator alerts: not found")
	ErrInvariant      = errors.New("administrator alerts: invariant violation")
)

func classifyAuthorizationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrUnauthorized), errors.Is(err, authz.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, ErrForbidden), errors.Is(err, authz.ErrForbidden):
		return ErrForbidden
	default:
		return fmt.Errorf("administrator alerts: final authorization: %w", err)
	}
}
