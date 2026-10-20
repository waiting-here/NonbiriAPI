package egress

import (
	"context"
	"net/http"
)

// ResponseLimits carries the protocol consumer's existing admission budget
// into deployment routing. It does not change direct responses. Zero fields
// retain the shared stack defaults; supplied limits can only lower them.
type ResponseLimits struct {
	JSONBytes   int64
	StreamBytes int64
	LineBytes   int
	EventBytes  int
}
type responseLimitsKey struct{}

func WithResponseLimits(request *http.Request, limits ResponseLimits) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), responseLimitsKey{}, limits))
}
func routeResponseLimits(request *http.Request, maximum int64) ResponseLimits {
	limits, _ := request.Context().Value(responseLimitsKey{}).(ResponseLimits)
	bounded := func(value, maximum int64) int64 {
		if value <= 0 || value > maximum {
			return maximum
		}
		return value
	}
	limits.JSONBytes = bounded(limits.JSONBytes, maximum)
	limits.StreamBytes = bounded(limits.StreamBytes, maximum)
	limits.LineBytes = int(bounded(int64(limits.LineBytes), limits.StreamBytes))
	limits.EventBytes = int(bounded(int64(limits.EventBytes), limits.StreamBytes))
	return limits
}
