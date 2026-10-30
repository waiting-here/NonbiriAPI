package diagnostic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// ReflectsSecret covers literal, JSON escaped, URL and common base64 echoes.
func ReflectsSecret(value []byte, secrets ...[]byte) bool {
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		encoded, _ := json.Marshal(string(secret))
		for _, candidate := range [][]byte{secret, encoded[1 : len(encoded)-1], []byte(url.QueryEscape(string(secret))), []byte(base64.StdEncoding.EncodeToString(secret)), []byte(base64.RawURLEncoding.EncodeToString(secret))} {
			if bytes.Contains(value, candidate) {
				return true
			}
		}
	}
	return false
}

func RecordHTTPFailure(ctx context.Context, status int, body []byte, secretCheck func([]byte) bool) (string, error) {
	cause := errors.New("upstream returned an unsuccessful status")
	code, message := "upstream_status", "Upstream returned an unsuccessful HTTP status"
	switch status {
	case 401:
		code, message = "upstream_authentication", "The upstream service rejected the credential"
	case 429:
		code, message = "upstream_rate_limited", "The upstream service limited this operation; retry later"
	}
	// Preserve only an explicitly structured error. HTML, opaque objects and
	// OAuth response documents never become administrative diagnostic text.
	detail := upstreamerror.Detail{}
	if len(body) <= upstreamerror.MaxBodyBytes && bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		detail = (upstreamerror.Context{ContainsSecret: secretCheck}).Parse(body)
	}
	switch detail.Code() {
	case "account_deactivated", "workspace_deactivated", "workspace_disabled", "account_suspended", "workspace_member_credits_depleted", "workspace_owner_credits_depleted", "workspace_owner_usage_limit_reached", "workspace_member_usage_limit_reached":
		code, message = "upstream_account_restricted", "The upstream service reported an account or workspace restriction"
	}
	RecordKnownError(ctx, "upstream_response", "upstream", code, message, cause)
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		if detail.Message() != "" || detail.Code() != "" {
			r.facts.Causes = append(r.facts.Causes, Cause{Type: "upstream", Code: detail.Code(), Message: BoundTo(detail.Message(), 1024)})
		}
		r.mu.Unlock()
	}
	return detail.Code(), cause
}

// DropReflectedCorrelation filters all known request secrets after the egress
// allowlist, including OAuth form fields that are not request headers.
func DropReflectedCorrelation(f *Failure, secrets ...[]byte) {
	if f == nil {
		return
	}
	ids := f.Correlation[:0]
	for _, id := range f.Correlation {
		if !ReflectsSecret([]byte(id.Value), secrets...) {
			ids = append(ids, id)
		}
	}
	f.Correlation = ids
	if f.RetryAfterSeconds != nil && ReflectsSecret([]byte(strconv.FormatInt(*f.RetryAfterSeconds, 10)), secrets...) {
		f.RetryAfterSeconds = nil
	}
}
func SafeScalar(value string) string {
	if strings.ContainsAny(value, "<>") {
		return ""
	}
	return BoundTo(value, 512)
}
