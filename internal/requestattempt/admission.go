package requestattempt

import "errors"

// AdmissionCause describes a platform decision without retaining candidate identities or caller values.
type AdmissionCause string

const (
	AdmissionRPM              AdmissionCause = "the model connection has reached its per-minute request limit"
	AdmissionConcurrency      AdmissionCause = "the model connection has reached its concurrent request limit"
	AdmissionBoth             AdmissionCause = "the model connection has reached both its per-minute and concurrent request limits"
	AdmissionQueueFull        AdmissionCause = "the waiting queue for the model connection is full"
	AdmissionQueueTimeout     AdmissionCause = "the request deadline expired while waiting for a model connection"
	AdmissionBudget           AdmissionCause = "the model usage budget could not cover this request"
	AdmissionCooldown         AdmissionCause = "the model connection is temporarily in cooldown"
	AdmissionMixed            AdmissionCause = "the model connections were unavailable for different reasons"
	AdmissionRouting          AdmissionCause = "the platform could not reserve routing space for this request"
	AdmissionQuotaStore       AdmissionCause = "the platform could not record a new usage reservation"
	AdmissionLedgerBusy       AdmissionCause = "the accounting service was busy while processing this request"
	AdmissionLedgerSpace      AdmissionCause = "the accounting service has no room for a new reservation"
	AdmissionUnavailable      AdmissionCause = "no usable model resource is available for this request"
	AdmissionUnspecifiedLimit AdmissionCause = "shared model resources are temporarily at capacity"
)

func (c AdmissionCause) RouteReason() string {
	switch c {
	case AdmissionRPM:
		return "key_rpm"
	case AdmissionConcurrency:
		return "key_concurrency"
	case AdmissionBoth:
		return "key_concurrency_rpm"
	case AdmissionQueueFull:
		return "key_queue_full"
	case AdmissionQueueTimeout:
		return "key_queue_timeout"
	case AdmissionBudget:
		return "quota"
	case AdmissionCooldown:
		return "cooldown"
	case AdmissionMixed:
		return "mixed_admission"
	case AdmissionRouting:
		return "routing_busy"
	case AdmissionQuotaStore:
		return "quota_storage_full"
	case AdmissionLedgerBusy:
		return "ledger_busy"
	case AdmissionLedgerSpace:
		return "ledger_full"
	case AdmissionUnavailable:
		return "unavailable"
	case AdmissionUnspecifiedLimit:
		return "key_rate_limit"
	default:
		return ""
	}
}

// IsServiceAdmissionDetail restricts zero-attempt service and internal failures to known admission decisions.
func IsServiceAdmissionDetail(d *RejectionDetail) bool {
	if d == nil {
		return false
	}
	switch AdmissionCause(d.Reason) {
	case AdmissionQueueTimeout:
		return d.Field == "model"
	case AdmissionRouting, AdmissionQuotaStore, AdmissionLedgerBusy, AdmissionLedgerSpace:
		return d.Field == "request"
	default:
		return false
	}
}

// Combine preserves a specific explanation only while the examined candidates agree.
func (c AdmissionCause) Combine(other AdmissionCause) AdmissionCause {
	if c == "" {
		return other
	}
	if other == "" || c == other {
		return c
	}
	return AdmissionMixed
}

type admissionError struct {
	error
	cause AdmissionCause
}

func (e admissionError) Unwrap() error { return e.error }

// Wrap preserves errors.Is routing and retry decisions.
func (c AdmissionCause) Wrap(err error) error {
	if err == nil || c.RouteReason() == "" {
		return err
	}
	return admissionError{err, c}
}

func AdmissionCauseOf(err error) AdmissionCause {
	var marked admissionError
	if errors.As(err, &marked) {
		return marked.cause
	}
	return ""
}
