package upstreamerror

import (
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const googleRPC = "type.googleapis.com/google.rpc."

var googleDuration = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,9})?s$`)

// RetryInfo is a protobuf duration. Round a positive fraction up to the
// scheduler's next whole second so admission never resumes early.
func googleRetrySeconds(raw json.RawMessage) int64 {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0
	}
	parts := googleDuration.FindStringSubmatch(s)
	if parts == nil {
		return 0
	}
	seconds, _ := strconv.ParseInt(parts[1], 10, 64)
	if strings.Trim(parts[2], ".0") != "" {
		seconds++
	}
	if seconds > 253402300799 {
		return 0
	}
	return seconds
}

func (c Context) googleEvidence(fields map[string]json.RawMessage, backoff TemporaryBackoff) (string, TemporaryBackoff) {
	status := c.googleHTTPStatus
	if (status != 400 && status != 401 && status != 429 && status != 503) || !IsGoogleAPI(c.GoogleAPI, c.BaseURL) ||
		string(fields["code"]) != strconv.Itoa(status) {
		return "", backoff
	}
	if status == 503 && backoff.googleOverload {
		return "google_model_overloaded", backoff
	}
	var name string
	var details []map[string]json.RawMessage
	if json.Unmarshal(fields["status"], &name) != nil || json.Unmarshal(fields["details"], &details) != nil {
		return "", backoff
	}
	var reason string
	var delay int64
	for _, detail := range details {
		var kind string
		if json.Unmarshal(detail["@type"], &kind) != nil {
			return "", backoff
		}
		switch kind {
		case googleRPC + "ErrorInfo":
			var v, domain string
			if reason != "" || json.Unmarshal(detail["reason"], &v) != nil ||
				(detail["domain"] != nil && (json.Unmarshal(detail["domain"], &domain) != nil || domain != "googleapis.com")) {
				return "", backoff
			}
			switch {
			case status == 400 && name == "INVALID_ARGUMENT" && v == "API_KEY_INVALID":
				reason = "google_api_key_invalid"
			case status == 401 && name == "UNAUTHENTICATED" && v == "ACCESS_TOKEN_TYPE_UNSUPPORTED":
				reason = "google_access_token_type_unsupported"
			default:
				return "", backoff
			}
		case googleRPC + "QuotaFailure":
			if status != 429 || name != "RESOURCE_EXHAUSTED" || reason != "" {
				return "", backoff
			}
			reason = c.googleQuotaReason(detail["violations"])
			if reason == "" {
				return "", backoff
			}
		case googleRPC + "RetryInfo":
			if status != 429 || delay != 0 {
				return "", backoff
			}
			delay = googleRetrySeconds(detail["retryDelay"])
			if delay == 0 {
				return "", backoff
			}
		}
	}
	if status == 429 {
		if reason == "" {
			return "", backoff
		}
		backoff = TemporaryBackoff{quotaReason: reason, retrySeconds: delay, modelHash: sha256.Sum256([]byte(c.PrivateModel))}
	}
	return reason, backoff
}

func (c Context) googleQuotaReason(raw json.RawMessage) string {
	var violations []map[string]json.RawMessage
	if c.PrivateModel == "" || json.Unmarshal(raw, &violations) != nil {
		return ""
	}
	model := c.PrivateModel
	if c.GoogleAPI {
		model = strings.TrimPrefix(model, "models/")
	}
	var reason string
	for _, v := range violations {
		var id, metric string
		var dimensions map[string]string
		var limit json.Number
		if json.Unmarshal(v["quotaId"], &id) != nil || json.Unmarshal(v["quotaMetric"], &metric) != nil ||
			json.Unmarshal(v["quotaValue"], &limit) != nil {
			return ""
		}
		// A documented per-model quota identifies the single model on this
		// dispatch. When Google supplies dimensions, they must agree.
		if raw, ok := v["quotaDimensions"]; ok && (json.Unmarshal(raw, &dimensions) != nil || dimensions == nil) {
			return ""
		}
		for k, value := range dimensions {
			if (k == "model" && value != model) || (k != "model" && (k != "location" || value != "global")) {
				return ""
			}
		}
		if n, err := limit.Int64(); err != nil || n <= 0 {
			return ""
		}
		next := googleQuotaWindow(id, metric)
		if next == "" {
			return ""
		}
		if reason != "" && reason != next {
			reason = "google_model_quota"
		} else {
			reason = next
		}
	}
	return reason
}

func googleQuotaWindow(id, metric string) string {
	id, tier, hasTier := strings.Cut(id, "-")
	if hasTier && tier == "" {
		return ""
	}
	prefix := "generativelanguage.googleapis.com/generate_content_"
	switch tier {
	case "FreeTier":
		prefix += "free_tier_"
	case "PaidTier":
		prefix += "paid_tier_"
	case "":
	default:
		return ""
	}
	switch metric {
	case prefix + "requests":
		switch id {
		case "GenerateRequestsPerDayPerProjectPerModel":
			return "google_daily_requests"
		case "GenerateRequestsPerMinutePerProjectPerModel":
			return "google_minute_requests"
		}
	case prefix + "input_token_count":
		switch id {
		case "GenerateContentInputTokensPerModelPerMinute", "GenerateContentInputTokensPerMinutePerProjectPerModel":
			return "google_minute_input_tokens"
		case "GenerateContentInputTokensPerModelPerDay", "GenerateContentInputTokensPerDayPerProjectPerModel":
			return "google_daily_input_tokens"
		}
	}
	return ""
}
