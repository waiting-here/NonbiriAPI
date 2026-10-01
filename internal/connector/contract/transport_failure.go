package contract

import (
	"context"
	"errors"
	"net"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// TransportFailed consumes the egress phase, never diagnostic text or status.
func TransportFailed(result AttemptResult, err error, ctx context.Context) AttemptResult {
	// The egress boundary captured these facts before a later caller cancel.
	switch egress.ExecutionFailure(err) {
	case egress.FailureTimeout:
		return UpstreamFailed(result, OriginTimeout)
	case egress.FailureNetwork:
		return UpstreamFailed(result, OriginNetwork)
	case egress.FailureCanceled:
		result.StreakDisposition, result.FailureOrigin = StreakNeutral, OriginClientCancel
		return result
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		result.StreakDisposition = StreakNeutral
		result.FailureOrigin = OriginClientCancel
		return result
	}
	switch egress.ExecutionFailure(err) {
	case egress.FailurePlatform:
		result.StreakDisposition = StreakNeutral
		result.FailureOrigin = OriginPlatform
	default:
		if errors.Is(err, context.DeadlineExceeded) {
			return UpstreamFailed(result, OriginTimeout)
		}
		var network net.Error
		if errors.As(err, &network) {
			if network.Timeout() {
				return UpstreamFailed(result, OriginTimeout)
			}
			return UpstreamFailed(result, OriginNetwork)
		}
		result.StreakDisposition = StreakNeutral
		result.FailureOrigin = OriginPlatform
	}
	return result
}

// ReadFailed distinguishes interruption causes from an invalid upstream payload.
func ReadFailed(result AttemptResult, err error, ctx context.Context) AttemptResult {
	if egress.ExecutionFailure(err) != "" {
		return TransportFailed(result, err, ctx)
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		result.StreakDisposition = StreakNeutral
		result.FailureOrigin = OriginClientCancel
		return result
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) {
		return TransportFailed(result, err, ctx)
	}
	return result
}
