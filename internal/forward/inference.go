package forward

import (
	"context"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// Inference uses the same admission, claims and settlement as other model calls.
func (service *Service) Inference(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.InferenceRequest, body []byte, mediaType, language string, headers ...http.Header) {
	var inbound http.Header
	if len(headers) == 1 {
		inbound = headers[0]
		request = request.CloneForAttempt()
		defer request.Clear()
		request.SetOptionHeaders(inbound)
	}
	service.execute(ctx, writer, userID, inferenceRequest(request), body, mediaType, language, inbound)
}

// InferenceCharityRouter provides logical preflight for direct inference.
type InferenceCharityRouter interface {
	PreflightInference(context.Context, int64, string, *openai.InferenceRequest, int64) (CharityPreflight, error)
}

func (adapter *CharityRoutingAdapter) PreflightInference(ctx context.Context, userID int64, fullName string, request *openai.InferenceRequest, now int64) (CharityPreflight, error) {
	if adapter == nil || adapter.service == nil {
		return CharityPreflight{}, ErrInternal
	}
	value, err := adapter.service.PreflightInference(ctx, userID, fullName, request, now)
	if err != nil {
		return CharityPreflight{}, err
	}
	return CharityPreflight{CloudflareProxyEnabled: value.CloudflareProxyEnabled, ModelTypes: value.ModelTypes.Clone(), TransportRule: value.TransportRule, RolePolicy: value.RolePolicy.Clone(), Revision: value.Revision, ModelID: value.ModelID, Provider: value.Provider, Model: value.Model, FullName: value.FullName, FlattenToolCalls: value.FlattenToolCalls, ReservedMilli: value.ReservedMilli}, nil
}
