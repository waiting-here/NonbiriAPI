package contract

import "net/http"

// ObserveUpstreamResponse reports received headers before reading the body.
// Accounting sinks decide whether this response establishes their checkpoint.
func ObserveUpstreamResponse(writer http.ResponseWriter, status int, stream bool) error {
	for writer != nil {
		if observer, ok := writer.(interface{ ObserveUpstreamResponse(int, bool) error }); ok {
			return observer.ObserveUpstreamResponse(status, stream)
		}
		wrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = wrapper.Unwrap()
	}
	return nil
}

// MarkResponseStarted checkpoints validated upstream output before delivery
// or buffering. Ordinary sinks have no checkpoint; accounting sinks implement
// the method through any response-writer wrappers.
func MarkResponseStarted(writer http.ResponseWriter) error {
	for writer != nil {
		if marker, ok := writer.(interface{ MarkResponseStarted() error }); ok {
			return marker.MarkResponseStarted()
		}
		wrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = wrapper.Unwrap()
	}
	return nil
}
