package upstreamerror

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
)

// TemporaryBackoff is evidence from a complete provider error envelope. Its
// zero value cannot exempt an arbitrary HTTP 503 from failure accounting.
type TemporaryBackoff struct {
	googleOverload bool
	quotaReason    string
	modelHash      [32]byte
	retrySeconds   int64
}

// ModelQuota is bound to the exact dispatched model. Only normalized
// evidence from a complete Google response can create this capability.
func (b TemporaryBackoff) ModelQuota(status int, model string) (reason string, seconds int64) {
	if status == 429 && b.quotaReason != "" && b.modelHash == sha256.Sum256([]byte(model)) {
		return b.quotaReason, b.retrySeconds
	}
	return "", 0
}

func (b TemporaryBackoff) Delay(status int) int64 {
	if b.googleOverload && status == 503 {
		return 60
	}
	return 0
}

func IsGoogleAPI(native bool, baseURL string) bool {
	if native {
		return true
	}
	base, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(base.Hostname(), "generativelanguage.googleapis.com")
}

func (c Context) temporaryBackoff(fields map[string]json.RawMessage) TemporaryBackoff {
	if !IsGoogleAPI(c.GoogleAPI, c.BaseURL) {
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
