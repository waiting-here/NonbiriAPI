package diagnostic

// FailureDisablement records the transition caused by one completed attempt.
// Counts are decimal U128s. Later policy edits do not change this receipt.
type FailureDisablement struct {
	StreakBefore  string `json:"streak_before"`
	StreakAfter   string `json:"streak_after"`
	Threshold     string `json:"threshold"`
	ConnectorType string `json:"connector_type"`
	NativeService string `json:"native_service,omitempty"`
}

func googleReasonStatus(reason string) int {
	switch reason {
	case "google_daily_requests", "google_daily_input_tokens", "google_minute_requests", "google_minute_input_tokens", "google_model_quota":
		return 429
	case "google_access_token_type_unsupported":
		return 401
	case "google_api_key_invalid":
		return 400
	case "google_model_overloaded":
		return 503
	}
	return 0
}

// ValidAttemptFailureReason limits management group labels to locally defined
// classifications. Upstream messages, identifiers and free-form codes stay out.
func ValidAttemptFailureReason(reason string) bool {
	switch reason {
	case "http_error", "attempt_failed", "dns_timeout", "dns_not_found", "dns_failed",
		"tls_unknown_authority", "tls_hostname_mismatch", "tls_invalid_certificate", "tls_record_invalid",
		"canceled", "deadline_exceeded", "compression_header_invalid", "compression_checksum_invalid",
		"unexpected_eof", "connection_reset", "connection_refused", "connection_aborted", "broken_pipe",
		"connection_closed", "invalid_json", "json_type_mismatch", "network_timeout", "connect_failed",
		"read_failed", "write_failed", "client_write_failed", "invalid_response", "network_error",
		"platform", "recovery_unknown", "legacy_unknown", "response_limit":
		return true
	}
	return googleReasonStatus(reason) != 0
}

// AttemptFailureReason classifies one failure using the retained evidence.
// A status without provider evidence can only identify the HTTP failure.
func AttemptFailureReason(f *Failure, status int) string {
	if f != nil {
		if expected := googleReasonStatus(f.ProviderReason); expected != 0 && expected == status {
			return f.ProviderReason
		}
		if googleReasonStatus(f.Code) == 0 && ValidAttemptFailureReason(f.Code) {
			return f.Code
		}
	}
	if status >= 400 && status <= 599 || f != nil && status >= 300 && status < 400 {
		return "http_error"
	}
	if f != nil {
		return "attempt_failed"
	}
	return ""
}
