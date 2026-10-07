package contract

type StreakDisposition string
type FailureOrigin string

const (
	StreakSuccess          StreakDisposition = "success"
	StreakUpstreamFailure  StreakDisposition = "upstream_failure"
	StreakNeutral          StreakDisposition = "neutral"
	OriginNone             FailureOrigin     = "none"
	OriginUpstreamResponse FailureOrigin     = "upstream_response"
	OriginUpstreamProtocol FailureOrigin     = "upstream_protocol"
	OriginNetwork          FailureOrigin     = "network"
	OriginTimeout          FailureOrigin     = "timeout"
	OriginClientCancel     FailureOrigin     = "client_cancel"
	OriginDownstream       FailureOrigin     = "downstream"
	OriginPlatform         FailureOrigin     = "platform"
	OriginLegacyUnknown    FailureOrigin     = "legacy_unknown"
	OriginRecoveryUnknown  FailureOrigin     = "recovery_unknown"
)

func ValidOutcome(disposition StreakDisposition, origin FailureOrigin) bool {
	switch disposition {
	case StreakSuccess:
		// An accepted HTTP response may reset key failures while retaining a
		// subsequent protocol, read or delivery error.
		return origin == OriginNone || origin == OriginUpstreamResponse || origin == OriginUpstreamProtocol || origin == OriginNetwork || origin == OriginTimeout || origin == OriginClientCancel || origin == OriginDownstream || origin == OriginPlatform || origin == OriginRecoveryUnknown
	case StreakUpstreamFailure:
		return origin == OriginUpstreamResponse || origin == OriginUpstreamProtocol || origin == OriginNetwork || origin == OriginTimeout
	case StreakNeutral:
		return origin == OriginClientCancel || origin == OriginDownstream || origin == OriginPlatform || origin == OriginLegacyUnknown || origin == OriginRecoveryUnknown
	}
	return false
}

// NormalizeOutcome supplies conservative facts for execution seams that have
// not yet established a protocol result. Confirmed facts are never rewritten.
func NormalizeOutcome(result AttemptResult) AttemptResult {
	if ValidOutcome(result.StreakDisposition, result.FailureOrigin) {
		return result
	}
	result.StreakDisposition = StreakNeutral
	result.FailureOrigin = OriginPlatform
	switch result.Failure {
	case FailureNone:
		if result.Success {
			result.StreakDisposition = StreakSuccess
			result.FailureOrigin = OriginNone
		}
	case FailureCanceled:
		result.FailureOrigin = OriginClientCancel
	case FailureSink:
		result.FailureOrigin = OriginDownstream
	}
	return result
}
func ProtocolSucceeded(result AttemptResult) AttemptResult {
	result.StreakDisposition = StreakSuccess
	result.FailureOrigin = OriginNone
	return result
}
func UpstreamFailed(result AttemptResult, origin FailureOrigin) AttemptResult {
	result.StreakDisposition = StreakUpstreamFailure
	result.FailureOrigin = origin
	return result
}

func ConfirmedSuccess(result AttemptResult, status int) AttemptResult {
	result = ProtocolSucceeded(result)
	result.UpstreamStatus = status
	return result
}
