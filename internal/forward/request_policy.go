package forward

import (
	"bytes"
	"context"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func (s *Service) ingressPolicy(ctx context.Context, user int64, model string) (CharityRequestPolicy, int64, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return CharityRequestPolicy{}, 0, ErrClosed
	}
	now, err := s.nowUnix()
	if err != nil {
		return CharityRequestPolicy{}, 0, err
	}
	policy, err := s.charity.RequestPolicy(ctx, user, model, now)
	if err == nil && (policy.ModelID <= 0 || policy.FullName != model) {
		err = ErrInternal
	}
	return policy, now, err
}

// decodeIngress resolves only the logical model policy before parsing optional
// parameters. Debug and every dispatch receive the same independently filtered
// body; unfiltered request data remains confined to this call's memory.
func (s *Service) decodeIngress(ctx context.Context, user int64, body []byte, operation contract.Operation) (*validatedRequest, []byte, bool, error) {
	envelope, err := openai.DecodeRequestEnvelope(bytes.NewReader(body), openai.MaxRequestBodyBytes, operation)
	if err != nil {
		return nil, nil, false, err
	}
	defer envelope.Clear()
	charity := strings.HasPrefix(envelope.Model, charityModelPrefix)
	filtered := body
	var policy CharityRequestPolicy
	var now int64
	if charity {
		policy, now, err = s.ingressPolicy(ctx, user, envelope.Model)
		if err != nil {
			return nil, nil, true, err
		}
		filtered, err = envelope.WithoutFields(policy.ExcludedRequestFields)
		if err != nil {
			return nil, nil, true, err
		}
	}
	request, err := decodeRequest(bytes.NewReader(filtered), operation)
	if err == nil && charity {
		err = request.excludeFields(policy.ExcludedRequestFields)
		request.policyModelID, request.policyDecisionNow = policy.ModelID, now
	}
	if err != nil {
		if request != nil {
			request.Clear()
		}
		if charity {
			clear(filtered)
		}
		return nil, nil, charity, err
	}
	return request, filtered, charity, nil
}

// Direct service callers already own a decoded request. They still obtain and
// bind policy once; callers that need exclusions applied before decoding use
// the HTTP ingress boundary above.
func (s *Service) bindDirectPolicy(ctx context.Context, user int64, request *validatedRequest, body []byte) (*validatedRequest, []byte, func(), error) {
	if !strings.HasPrefix(request.Model, charityModelPrefix) || request.policyModelID != 0 {
		return request, body, nil, nil
	}
	policy, now, err := s.ingressPolicy(ctx, user, request.Model)
	if err != nil {
		return nil, nil, nil, err
	}
	copy := request.CloneForAttempt()
	copy.policyModelID, copy.policyDecisionNow = policy.ModelID, now
	if err = copy.excludeFields(policy.ExcludedRequestFields); err != nil {
		copy.Clear()
		return nil, nil, nil, err
	}
	filtered := body
	if len(policy.ExcludedRequestFields) > 0 {
		envelope, decodeErr := openai.DecodeRequestEnvelope(bytes.NewReader(body), openai.MaxRequestBodyBytes, request.operation)
		if decodeErr != nil {
			copy.Clear()
			return nil, nil, nil, decodeErr
		}
		defer envelope.Clear()
		if envelope.Model != request.Model {
			copy.Clear()
			return nil, nil, nil, charityrouting.ErrNotFound
		}
		filtered, err = envelope.WithoutFields(policy.ExcludedRequestFields)
		if err != nil {
			copy.Clear()
			return nil, nil, nil, err
		}
	}
	return copy, filtered, func() {
		copy.Clear()
		if len(policy.ExcludedRequestFields) > 0 {
			clear(filtered)
		}
	}, nil
}
