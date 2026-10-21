package openai

import (
	"bytes"
	"context"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

// unchangedCallerParameters is called before clearing the wire body, only for
// native codecs whose fields retain the decoder's original JSON values.
// Configured adaptations and protocol conversions cannot supply this proof.
// Comparing raw values also refuses otherwise equivalent codec normalization.
func unchangedCallerParameters(ctx context.Context, response *http.Response, fields []jsonField, body []byte, policy contract.AttemptPolicy) func(string) bool {
	if !smartbackoff.IsCharityCall(ctx) || response == nil || response.StatusCode != 400 && response.StatusCode != 422 ||
		policy.HasAdaptation || len(policy.AdditionalHeaders) != 0 || len(policy.ManagedHeaders) != 0 || len(policy.NativeExtensions) != 0 {
		return nil
	}
	// body is the native serializer's validated JSON. Borrowing its values
	// avoids copying large inputs again just to inspect a rejected request.
	wire, ok := borrowedObjectFields(body)
	if !ok {
		return nil
	}
	original := fieldsByName(fields)
	matched := make(map[string]bool)
	for _, field := range wire {
		if value, ok := original[field.name]; ok && bytes.Equal(value, field.value) {
			matched[field.name] = true
		}
	}
	return func(name string) bool { return matched[name] }
}

func callerParameterFailure(result AttemptResult) AttemptResult {
	if result.Failure == FailureUpstream && result.FailureOrigin == contract.OriginUpstreamResponse &&
		result.ErrorDetail.CallerFault().Matches(result.UpstreamStatus, result.ErrorDetail.Code()) {
		result.StreakDisposition = contract.StreakNeutral
	}
	return result
}
