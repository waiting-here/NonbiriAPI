package announcements

import (
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

var (
	ErrInvalidRequest  = errors.New(httperr.CodeInvalidRequest)
	ErrUnauthorized    = errors.New(httperr.CodeUnauthorized)
	ErrForbidden       = errors.New(httperr.CodeForbidden)
	ErrNotFound        = errors.New(httperr.CodeNotFound)
	ErrConflict        = errors.New(httperr.CodeConflict)
	ErrPayloadTooLarge = errors.New(httperr.CodePayloadTooLarge)
	ErrResourceLimit   = errors.New(httperr.CodeResourceLimitExceeded)
	ErrUnavailable     = errors.New(httperr.CodeServiceUnavailable)
)
