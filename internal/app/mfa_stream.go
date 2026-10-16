package app

import (
	"context"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// Authorization is rechecked on heartbeats; the deadline closes an idle stream
// at proof expiry even when no heartbeat or business event is due.
func authorizedMFAStream(ctx context.Context, verifier accountEventSessionVerifier, userID int64, binding string) (context.Context, context.CancelFunc, error) {
	until, err := verifier.VerifyUserSessionMFA(ctx, userID, binding)
	if err != nil {
		return nil, nil, err
	}
	if until == 0 {
		return ctx, func() {}, nil
	}
	stream, cancel := context.WithDeadline(ctx, time.Unix(until, 0))
	return stream, cancel, nil
}

func writeMFAStreamError(writer http.ResponseWriter, err error) {
	code, message := authz.StableCode(err), "authentication required"
	if code == "" {
		code, message = httperr.CodeServiceUnavailable, "authentication service unavailable"
	} else if code == httperr.CodeMFARequired {
		message = "MFA verification is required."
	} else if code == httperr.CodeForbidden {
		message = "access forbidden"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}
