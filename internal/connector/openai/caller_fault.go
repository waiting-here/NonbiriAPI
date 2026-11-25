package openai

import contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

func callerParameterFailure(result AttemptResult) AttemptResult {
	if result.Failure == FailureUpstream && result.FailureOrigin == contract.OriginUpstreamResponse &&
		result.ErrorDetail.CallerFault().Matches(result.UpstreamStatus, result.ErrorDetail.Code()) {
		result.StreakDisposition = contract.StreakNeutral
	}
	return result
}
