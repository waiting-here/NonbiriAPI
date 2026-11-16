package upstreamerror

import (
	"encoding/json"
	"net/url"
	"strings"
)

// TemporaryBackoff is evidence from a complete provider error envelope. Its
// zero value cannot exempt an arbitrary HTTP 503 from failure accounting.
type TemporaryBackoff struct{ googleOverload bool }

func (b TemporaryBackoff) Delay(status int) int64 {
	if b.googleOverload && status == 503 {
		return 60
	}
	return 0
}

func (c Context) temporaryBackoff(fields map[string]json.RawMessage) TemporaryBackoff {
	google := c.GoogleAPI
	if base, err := url.Parse(c.BaseURL); err == nil && base.Hostname() == "generativelanguage.googleapis.com" {
		google = true
	}
	if !google {
		return TemporaryBackoff{}
	}
	var code json.Number
	var status, message string
	if json.Unmarshal(fields["code"], &code) != nil || code.String() != "503" ||
		json.Unmarshal(fields["status"], &status) != nil || status != "UNAVAILABLE" ||
		json.Unmarshal(fields["message"], &message) != nil {
		return TemporaryBackoff{}
	}
	message = strings.ToLower(strings.Join(strings.Fields(message), " "))
	for _, permanent := range []string{"api key", "permission", "unauthorized", "authentication", "expired", "billing", "payment", "balance", "credit", "context length"} {
		if strings.Contains(message, permanent) {
			return TemporaryBackoff{}
		}
	}
	load := strings.HasPrefix(message, "this model is currently experiencing high demand") || strings.HasPrefix(message, "the model is overloaded")
	return TemporaryBackoff{googleOverload: load && strings.Contains(message, "try again later")}
}
