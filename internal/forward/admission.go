package forward

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/modelname"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

type logicalAdmission struct {
	cloudflareProxyEnabled bool
	transportRule          transportpolicy.Rule
	rolePolicy             rolepolicy.Policy
	modelRevision          int64
	charity                bool
	modelID                int64
	fullName               string
	strategy               string
	silentRetry            bool
	flatten                bool
	reservedMilli          int64
	decisionNow            int64
}

func (service *Service) preflight(ctx context.Context, userID int64, request *validatedRequest) (logicalAdmission, *validatedRequest, func(), error) {
	kind := "self"
	if modelname.IsCharity(request.Model) {
		kind = "charity"
	}
	requestattempt.Classify(ctx, kind)
	if service.classify != nil {
		service.classify(ctx, userID, kind)
	}
	if modelname.IsCharity(request.Model) {
		now := request.policyDecisionNow
		var err error
		if request.policyModelID == 0 {
			now, err = service.nowUnix()
		}
		if err != nil {
			return logicalAdmission{charity: true}, request, nil, err
		}
		if err := service.checkClientRules(ctx, userID, request.Model, now); err != nil {
			return logicalAdmission{charity: true}, request, nil, err
		}
		var value CharityPreflight
		if request.operation == connectorcontract.OperationEmbeddings {
			value, err = service.charity.PreflightEmbedding(ctx, userID, request.Model, request.embedding, now)
		} else if request.image != nil {
			value, err = service.charity.PreflightImage(ctx, userID, request.Model, request.image, now)
		} else if request.decision != nil {
			value, err = service.charity.PreflightDecision(ctx, userID, request.Model, request.decision, now)
		} else {
			value, err = service.charity.Preflight(ctx, userID, request.Model, request.chat, now)
		}
		if err != nil {
			return logicalAdmission{charity: true}, request, nil, err
		}
		if !value.ModelTypes.Supports(request.operation) {
			return logicalAdmission{charity: true}, request, nil, unsupportedModelType()
		}
		if request.policyModelID != 0 && value.ModelID != request.policyModelID {
			return logicalAdmission{charity: true}, request, nil, charityrouting.ErrNotFound
		}
		admission := logicalAdmission{
			cloudflareProxyEnabled: value.CloudflareProxyEnabled,
			transportRule:          value.TransportRule, rolePolicy: value.RolePolicy.Clone(), modelRevision: value.Revision, charity: true, modelID: value.ModelID, fullName: value.FullName, strategy: "ordered",
			silentRetry: true, flatten: value.FlattenToolCalls, reservedMilli: value.ReservedMilli,
			decisionNow: now,
		}
		if request.roleSnapshot != nil {
			admission.rolePolicy = request.roleSnapshot.Clone()
			admission.modelRevision = request.policyRevision
			admission.transportRule = request.transportRule
		}
		return prepareModelPolicy(request, admission)
	}
	var value PersonalPreflight
	if request.personalPolicy != nil {
		value = *request.personalPolicy
	} else {
		var err error
		value, err = service.personal.Preflight(ctx, userID, request.Model)
		if err != nil {
			return logicalAdmission{}, request, nil, err
		}
	}
	if request.policyModelID != 0 && request.policyModelID != value.ModelID {
		return logicalAdmission{}, request, nil, routing.ErrNotFound
	}
	if service.clientGuard != nil {
		now, err := service.nowUnix()
		if err != nil {
			return logicalAdmission{}, request, nil, err
		}
		if err := service.checkClientRules(ctx, userID, request.Model, now); err != nil {
			return logicalAdmission{}, request, nil, err
		}
	}
	if !value.ModelTypes.Supports(request.operation) {
		return logicalAdmission{}, request, nil, unsupportedModelType()
	}
	admission := logicalAdmission{
		cloudflareProxyEnabled: value.CloudflareProxyEnabled,
		transportRule:          value.TransportRule, rolePolicy: value.RolePolicy.Clone(), modelRevision: value.Revision, modelID: value.ModelID, fullName: value.FullName, strategy: value.RouteStrategy,
		silentRetry: value.SilentRetry, flatten: value.FlattenToolCalls,
	}
	if request.roleSnapshot != nil {
		admission.rolePolicy = request.roleSnapshot.Clone()
		admission.modelRevision = request.policyRevision
		admission.transportRule = request.transportRule
	}
	return prepareModelPolicy(request, admission)
}

// checkClientRules runs after ingress authentication, model access and parsing,
// before business rejection, outbound transformations or Debug capture.
// The guard reads the original ingress source for both personal and charity calls.
func (service *Service) checkClientRules(ctx context.Context, userID int64, model string, now int64) error {
	if service.clientGuard == nil {
		return nil
	}
	guarded, err := service.clientGuard.CheckCall(ctx, userID, model, now)
	if err != nil {
		return claim.ErrDependencyUnavailable
	}
	if guarded.Banned {
		return &accountBannedError{until: guarded.Until}
	}
	return nil
}

func prepareModelPolicy(request *validatedRequest, admission logicalAdmission) (logicalAdmission, *validatedRequest, func(), error) {
	if !validAdmission(admission) {
		return admission, request, nil, ErrInternal
	}
	if request.operation != connectorcontract.OperationChatCompletions {
		return admission, request, nil, nil
	}
	var restored *openai.ChatRequest
	var err error
	if admission.flatten {
		restored, err = request.chat.ReverseFlatten()
	} else {
		restored = request.chat
	}
	if err != nil || restored == nil {
		return admission, request, nil, &openai.ValidationError{Detail: requestattempt.NewDetail("messages", "messages could not be restored from flattened tool calls")}
	}
	transformed, err := restored.ApplyRolePolicy(admission.rolePolicy)
	if admission.flatten {
		restored.Clear()
	}
	if err != nil || transformed == nil {
		return admission, request, nil, &openai.ValidationError{Detail: requestattempt.NewDetail("messages", "messages do not match the configured role policy")}
	}
	copy := chatRequest(transformed)
	copy.personalPolicy = request.personalPolicy
	copy.policyModelID, copy.policyDecisionNow = request.policyModelID, request.policyDecisionNow
	copy.policyRevision = admission.modelRevision
	copy.transportRule = admission.transportRule
	policy := admission.rolePolicy.Clone()
	copy.roleSnapshot = &policy
	copy.excluded = append([]string(nil), request.excluded...)
	if request.personalAdaptation != nil {
		snapshot := request.personalAdaptation.Clone()
		copy.personalAdaptation = &snapshot
	}
	return admission, copy, copy.Clear, nil
}
