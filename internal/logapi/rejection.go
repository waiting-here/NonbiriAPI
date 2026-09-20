package logapi

type RejectionFields struct {
	Phase           string  `json:"phase"`
	RejectionStage  *string `json:"rejection_stage"`
	RejectionReason *string `json:"rejection_reason"`
	RequestMethod   *string `json:"request_method"`
	RequestPath     *string `json:"request_path"`
}

func rejectionFields(r commonLogRecord) RejectionFields {
	phase := "handler"
	if r.rejectionStage.Valid {
		phase = "pre_handler"
	}
	return RejectionFields{Phase: phase, RejectionStage: textPointer(r.rejectionStage), RejectionReason: textPointer(r.rejectionReason), RequestMethod: textPointer(r.requestMethod), RequestPath: textPointer(r.requestPath)}
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
