package charityrouting

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// PreflightInference retains candidate-free admission for direct inference.
// The minimum chat text policy is specific to Chat Completions.
func (s *Service) PreflightInference(ctx context.Context, userID int64, fullName string, request *openai.InferenceRequest, decisionNow int64) (RuntimePreflight, error) {
	if request == nil || request.Model != fullName || !request.Operation().Valid() {
		return RuntimePreflight{}, ErrInvalidRequest
	}
	return s.preflight(ctx, userID, fullName, nil, decisionNow)
}
