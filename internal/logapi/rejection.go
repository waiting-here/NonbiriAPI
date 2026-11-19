package logapi

import "github.com/waiting-here/NonbiriAPI/internal/requestattempt"

type RejectionFields struct {
	RejectionDetail    *requestattempt.RejectionDetail `json:"rejection_detail,omitempty"`
	RequestedStreaming *bool                           `json:"requested_streaming"`
	AutomaticActions   []string                        `json:"automatic_actions"`
	Phase              string                          `json:"phase"`
	RejectionStage     *string                         `json:"rejection_stage"`
	RejectionReason    *string                         `json:"rejection_reason"`
	RequestMethod      *string                         `json:"request_method"`
	RequestPath        *string                         `json:"request_path"`
}

func rejectionFields(r commonLogRecord) RejectionFields {
	phase := "handler"
	if r.rejectionStage.Valid {
		phase = "pre_handler"
	}
	var streaming *bool
	if r.requestedStreaming.Valid {
		streaming = new(r.requestedStreaming.Bool)
	}
	var actions []string
	if r.automaticActions.Valid {
		actions = []string{}
		for _, action := range []struct {
			bit  int64
			name string
		}{
			{requestattempt.ActionShortDeduction, "short_content_deduction"},
			{requestattempt.ActionShortBan, "short_content_ban"},
			{requestattempt.ActionShortSuspend, "short_content_suspension"},
			{requestattempt.ActionRPMBan, "rpm_ban"},
			{requestattempt.ActionClientBan, "client_rule_ban"},
			{requestattempt.ActionContextPenalty, "context_penalty"},
		} {
			if r.automaticActions.Int64&action.bit != 0 {
				actions = append(actions, action.name)
			}
		}
	}
	return RejectionFields{RejectionDetail: r.rejectionDetail, RequestedStreaming: streaming, AutomaticActions: actions, Phase: phase, RejectionStage: textPointer(r.rejectionStage), RejectionReason: textPointer(r.rejectionReason), RequestMethod: textPointer(r.requestMethod), RequestPath: textPointer(r.requestPath)}
}

// Historical suggestions never prove that a scheduling gate was started or extended.
// Start with the sparse receipt set; IN avoids multiplying a request by its attempts.
const actualBackoffFilter = ` AND l.id IN (SELECT request_log_id FROM request_attempt_backoffs WHERE observed_only=0)`

func validOutcome(role, value string) bool {
	switch value {
	case "", "failed", "success":
		return true
	case "before_dispatch", "actual_backoff":
		return role == "admin" || role == "steward"
	}
	return false
}

func outcomeFilter(outcome string) string {
	switch outcome {
	case "failed":
		return ` AND l.caller_result_class='failed'`
	case "success":
		return ` AND l.caller_result_class='success'`
	case "before_dispatch":
		return ` AND (l.rejection_stage IS NOT NULL OR (l.caller_result_class='failed' AND l.attempt_count=0 AND l.error_source='platform'))`
	case "actual_backoff":
		return actualBackoffFilter
	}
	return ""
}

func phaseFilter(phase string) string {
	if phase == "pre_handler" {
		return ` AND l.rejection_stage IS NOT NULL`
	}
	if phase == "handler" {
		return ` AND l.rejection_stage IS NULL`
	}
	return ""
}

func rejectionCSV(detail *requestattempt.RejectionDetail) []string {
	if detail == nil {
		return []string{"", "", ""}
	}
	return []string{csvSafe(detail.Field), csvSafe(detail.Reason), csvSafe(detail.Suggestion)}
}
