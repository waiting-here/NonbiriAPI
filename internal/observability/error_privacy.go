package observability

import (
	"encoding/base64"

	"github.com/waiting-here/NonbiriAPI/internal/textprivacy"
)

// RedactErrorIPv4 projects the payload only. Metadata still describes the
// original stored bytes, and binary bodies retain their original encoding.
func RedactErrorIPv4(body ErrorBody) (ErrorBody, error) {
	body.IPv4Redacted = true
	switch body.Encoding {
	case "utf-8":
		body.Body = textprivacy.IPv4(body.Body)
	case "base64":
		raw, err := base64.StdEncoding.DecodeString(body.Body)
		if err != nil {
			return ErrorBody{}, ErrUnavailable
		}
		defer clear(raw)
		body.Body = base64.StdEncoding.EncodeToString([]byte(textprivacy.IPv4(string(raw))))
	default:
		return ErrorBody{}, ErrUnavailable
	}
	return body, nil
}
