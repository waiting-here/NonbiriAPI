package contract

import "net/http"

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
