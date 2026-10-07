package openai

import "github.com/waiting-here/NonbiriAPI/internal/requestattempt"

// ValidationError preserves invalid_request identity and a fixed check.
type ValidationError struct {
	Detail *requestattempt.RejectionDetail
}

func (e *ValidationError) Error() string { return ErrInvalidRequest.Error() }
func (e *ValidationError) Unwrap() error { return ErrInvalidRequest }
func invalidField(field, reason string) error {
	return &ValidationError{Detail: requestattempt.NewDetail(field, reason)}
}
