package logapi

import "github.com/waiting-here/NonbiriAPI/internal/requestattempt"

type RejectionFields struct {
	RequestedStreaming *bool    `json:"requested_streaming"`
	AutomaticActions   []string `json:"automatic_actions"`
	Phase              string   `json:"phase"`
	RejectionStage     *string  `json:"rejection_stage"`
	RejectionReason    *string  `json:"rejection_reason"`
	RequestMethod      *string  `json:"request_method"`
	RequestPath        *string  `json:"request_path"`
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
	return RejectionFields{RequestedStreaming: streaming, AutomaticActions: actions, Phase: phase, RejectionStage: textPointer(r.rejectionStage), RejectionReason: textPointer(r.rejectionReason), RequestMethod: textPointer(r.requestMethod), RequestPath: textPointer(r.requestPath)}
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
