package forward

import (
	"bytes"
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/modelname"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
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
	limit, err := requestbody.Limit(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	envelope, err := openai.DecodeRequestEnvelope(bytes.NewReader(body), limit, operation)
	if err != nil {
		return nil, nil, false, err
	}
	defer envelope.Clear()
	return s.decodeEnvelope(ctx, user, body, envelope, operation)
}

func (s *Service) decodeEnvelope(ctx context.Context, user int64, body []byte, envelope *openai.RequestEnvelope, operation contract.Operation, multipart ...*openai.InferenceMultipart) (*validatedRequest, []byte, bool, error) {
	limit, err := requestbody.Limit(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	if contract.IsHTTPInference(operation) {
		if err := openai.ValidateStatelessInference(body, operation); err != nil {
			return s.rejectStatefulInference(ctx, user, envelope.Model, err)
		}
	}
	charity := modelname.IsCharity(envelope.Model)
	filtered := body
	var policy CharityRequestPolicy
	var now int64
	var personal PersonalPreflight
	var adaptation *requestadaptation.Snapshot
	if !charity && s.adaptations != nil {
		personal, adaptation, err = s.personalIngressPolicy(ctx, user, envelope.Model)
		if err != nil {
			return nil, nil, false, err
		}
		defer adaptation.Clear()
		filtered, err = envelope.WithoutFields(adaptation.Document.ExcludedRequestFields)
		if err != nil {
			return nil, nil, false, err
		}
	}
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
	var request *validatedRequest
	if len(multipart) == 1 && multipart[0] != nil {
		var decoded *openai.InferenceRequest
		decoded, err = multipart[0].Decode(filtered, operation, limit)
		request = inferenceRequest(decoded)
	} else {
		request, err = decodeRequest(bytes.NewReader(filtered), operation, limit)
	}
	if err == nil && charity {
		err = request.excludeFields(policy.ExcludedRequestFields)
		request.policyModelID, request.policyDecisionNow = policy.ModelID, now
		role := policy.RolePolicy.Clone()
		request.roleSnapshot = &role
		request.policyRevision = policy.Revision
		request.transportRule = policy.TransportRule
	}
	if err == nil && adaptation != nil {
		err = request.excludeFields(adaptation.Document.ExcludedRequestFields)
		request.policyModelID = personal.ModelID
		request.personalPolicy = &personal
		pinned := adaptation.Clone()
		request.personalAdaptation = &pinned
	}
	if err != nil {
		if request != nil {
			request.Clear()
		}
		if charity || adaptation != nil {
			clear(filtered)
		}
		return nil, nil, charity, err
	}
	return request, filtered, charity, nil
}

// A resource-policy refusal still follows access checks and the original
// client audit. Successful requests perform that audit in ordinary preflight.
func (s *Service) rejectStatefulInference(ctx context.Context, user int64, model string, rejection error) (*validatedRequest, []byte, bool, error) {
	charity := modelname.IsCharity(model)
	var now int64
	var err error
	if charity {
		_, now, err = s.ingressPolicy(ctx, user, model)
	} else {
		s.lifecycle.RLock()
		if s.closed {
			err = ErrClosed
		} else {
			var policy PersonalPreflight
			policy, err = s.personal.Preflight(ctx, user, model)
			if err == nil && (policy.ModelID <= 0 || policy.OwnerUserID != user || policy.FullName != model) {
				err = routing.ErrNotFound
			}
			if err == nil {
				now, err = s.nowUnix()
			}
		}
		s.lifecycle.RUnlock()
	}
	if err == nil {
		kind := "self"
		if charity {
			kind = "charity"
		}
		requestattempt.Classify(ctx, kind)
		if s.classify != nil {
			s.classify(ctx, user, kind)
		}
		err = s.checkClientRules(ctx, user, model, now)
	}
	if err == nil {
		err = rejection
	}
	return nil, nil, charity, err
}

// Direct service callers already own a decoded request. They still obtain and
// bind policy once; callers that need exclusions applied before decoding use
// the HTTP ingress boundary above.
func (s *Service) bindDirectPolicy(ctx context.Context, user int64, request *validatedRequest, body []byte) (*validatedRequest, []byte, func(), error) {
	if !modelname.IsCharity(request.Model) {
		return s.bindDirectPersonalPolicy(ctx, user, request, body)
	}
	if request.policyModelID != 0 {
		return request, body, nil, nil
	}
	policy, now, err := s.ingressPolicy(ctx, user, request.Model)
	if err != nil {
		return nil, nil, nil, err
	}
	copy := request.CloneForAttempt()
	copy.policyModelID, copy.policyDecisionNow = policy.ModelID, now
	role := policy.RolePolicy.Clone()
	copy.roleSnapshot = &role
	copy.policyRevision = policy.Revision
	copy.transportRule = policy.TransportRule
	if err = copy.excludeFields(policy.ExcludedRequestFields); err != nil {
		copy.Clear()
		return nil, nil, nil, err
	}
	filtered := body
	if len(policy.ExcludedRequestFields) > 0 {
		envelope, decodeErr := openai.DecodeRequestEnvelope(bytes.NewReader(body), request.bodyLimit(), request.operation)
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

func (s *Service) personalIngressPolicy(ctx context.Context, user int64, model string) (PersonalPreflight, *requestadaptation.Snapshot, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return PersonalPreflight{}, nil, ErrClosed
	}
	policy, err := s.personal.Preflight(ctx, user, model)
	if err != nil {
		return policy, nil, err
	}
	if policy.ModelID <= 0 || policy.OwnerUserID != user {
		return policy, nil, routing.ErrNotFound
	}
	ref := requestadaptation.Ref{Scope: requestadaptation.ScopePersonalModel, ID: policy.ModelID}
	snapshots, err := s.adaptations.LoadMany(ctx, []requestadaptation.Ref{ref})
	if err != nil {
		return policy, nil, err
	}
	snapshot, ok := snapshots[ref]
	if !ok {
		snapshot = requestadaptation.Snapshot{Document: requestadaptation.Empty(ref.Scope), Revision: "0"}
	}
	return policy, &snapshot, nil
}

func (s *Service) bindDirectPersonalPolicy(ctx context.Context, user int64, request *validatedRequest, body []byte) (*validatedRequest, []byte, func(), error) {
	if s.adaptations == nil || request.personalAdaptation != nil {
		return request, body, nil, nil
	}
	policy, snapshot, err := s.personalIngressPolicy(ctx, user, request.Model)
	if err != nil {
		return nil, nil, nil, err
	}
	copy := request.CloneForAttempt()
	copy.personalAdaptation = snapshot
	copy.policyModelID = policy.ModelID
	copy.personalPolicy = &policy
	fields := snapshot.Document.ExcludedRequestFields
	if err := copy.excludeFields(fields); err != nil {
		copy.Clear()
		return nil, nil, nil, err
	}
	filtered := body
	if len(fields) > 0 {
		envelope, err := openai.DecodeRequestEnvelope(bytes.NewReader(body), request.bodyLimit(), request.operation)
		if err != nil {
			copy.Clear()
			return nil, nil, nil, err
		}
		defer envelope.Clear()
		if envelope.Model != request.Model {
			copy.Clear()
			return nil, nil, nil, routing.ErrNotFound
		}
		filtered, err = envelope.WithoutFields(fields)
		if err != nil {
			copy.Clear()
			return nil, nil, nil, err
		}
	}
	return copy, filtered, func() {
		copy.Clear()
		if len(fields) > 0 {
			clear(filtered)
		}
	}, nil
}
