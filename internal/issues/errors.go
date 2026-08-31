package issues

import (
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

var (
	ErrInvalidRequest = errors.New(httperr.CodeInvalidRequest)
	ErrUnauthorized   = errors.New(httperr.CodeUnauthorized)
	ErrForbidden      = errors.New(httperr.CodeForbidden)
	ErrNotFound       = errors.New(httperr.CodeNotFound)
	ErrConflict       = errors.New(httperr.CodeConflict)
	ErrUnavailable    = errors.New(httperr.CodeServiceUnavailable)
)
