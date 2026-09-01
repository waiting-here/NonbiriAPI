package adminalerts

type Kind string

const (
	KindFetchFailed               Kind = "fetch_failed"
	KindForwardError              Kind = "forward_error"
	KindRegistrationRejected      Kind = "registration_rejected"
	KindMaintenanceEnabled        Kind = "maintenance_enabled"
	KindDonationFailureDisabled   Kind = "donation_failure_disabled"
	KindIssueProjectionIncomplete Kind = "issue_projection_incomplete"
	KindReportRetryExhausted      Kind = "report_retry_exhausted"
	KindFishingRetryExhausted     Kind = "fishing_retry_exhausted"
	KindRPSTerminalRetrying       Kind = "rps_terminal_retrying"
	KindWorkerCheckpointFailed    Kind = "worker_checkpoint_failed"
	KindInvariantViolation        Kind = "invariant_violation"
)

func validKind(value string) bool {
	switch Kind(value) {
	case KindFetchFailed, KindForwardError, KindRegistrationRejected,
		KindMaintenanceEnabled, KindDonationFailureDisabled,
		KindIssueProjectionIncomplete, KindReportRetryExhausted,
		KindFishingRetryExhausted, KindRPSTerminalRetrying,
		KindWorkerCheckpointFailed, KindInvariantViolation:
		return true
	default:
		return false
	}
}

type AdminAlert struct {
	ID            string  `json:"id"`
	Kind          Kind    `json:"kind"`
	Message       string  `json:"message"`
	Ref           *string `json:"ref"`
	SubjectUserID *string `json:"subject_user_id"`
	CreatedAt     int64   `json:"created_at"`
	Resolved      bool    `json:"resolved"`
	ResolvedAt    *int64  `json:"resolved_at"`
}

type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type ListQuery struct {
	Resolved *bool
	Cursor   string
	Limit    int
}
