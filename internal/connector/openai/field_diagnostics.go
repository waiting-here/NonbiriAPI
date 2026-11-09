package openai

import (
	"slices"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// CheckTopLevelFields preserves the first rejected field in request order.
func (r *ChatRequest) CheckTopLevelFields(allowed ...string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	for index, name := range r.requirements.topLevel {
		if !slices.Contains(allowed, name) {
			return &contract.RequestRejection{Stage: "protocol preflight", Field: requestattempt.TopLevelField(name, index+1), Reason: "unsupported top-level field"}
		}
	}
	return nil
}

// DiagnosticField never returns an arbitrary caller-controlled field name.
func (r *ChatRequest) DiagnosticField(name string) string {
	if r != nil {
		for index, field := range r.requirements.topLevel {
			if name == field {
				return requestattempt.TopLevelField(name, index+1)
			}
		}
	}
	return "request"
}
