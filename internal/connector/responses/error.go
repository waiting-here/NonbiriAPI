package responses

import (
	"bytes"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
)

// ErrorEvent extracts only Responses' two explicit SSE failure envelopes.
// The original bytes remain available to the private diagnostic capture.
func ErrorEvent(raw []byte) ([]byte, bool) {
	if !bytes.Contains(raw, []byte(`"error"`)) && !bytes.Contains(raw, []byte(`"response.failed"`)) && !bytes.Contains(raw, []byte(`\u`)) {
		return nil, false
	}
	root, err := chatwire.Object(raw)
	if err != nil {
		return nil, false
	}
	kind, _ := chatwire.String(root["type"])
	switch kind {
	case "error":
		return raw, true
	case "response.failed":
		response, err := chatwire.Object(root["response"])
		if err != nil || chatwire.Null(response["error"]) {
			return nil, false
		}
		return response["error"], true
	}
	return nil, false
}
