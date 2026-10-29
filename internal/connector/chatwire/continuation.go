package chatwire

import "errors"

// ErrContinuationUnsupported marks a valid provider continuation checkpoint,
// whose output is not a complete result of this single-request connector.
var ErrContinuationUnsupported = errors.New("automatic output continuation is not supported")

func protocolDiagnostic(err error, fallback string) string {
	if errors.Is(err, ErrContinuationUnsupported) {
		return "upstream requires output continuation; automatic continuation is not supported"
	}
	return fallback
}
