package textprivacy

import "encoding/json"

// MarshalJSON is for read-only display projections such as logs and alerts.
// Configuration editors and successful model responses use ordinary JSON.
func MarshalJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	original := string(body)
	redacted := IPv4(original)
	if redacted == original {
		return body, nil
	}
	return []byte(redacted), nil
}
