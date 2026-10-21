package forward

import (
	"context"
	"net/http"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

type executionPlan struct {
	logicalAdmission
	candidates      []RouteCandidate
	charityStrategy string
	outputFloor     int64
	purpose         claim.Purpose
	route           claim.RouteKind
}

func (service *Service) snapshot(
	ctx context.Context,
	userID int64,
	identifier string,
	request *validatedRequest,
	admission logicalAdmission,
	decision debug.CaptureDecision,
	inbound http.Header,
) (executionPlan, error) {
	plan := executionPlan{logicalAdmission: admission, route: requestkind.ForOperation(request.operation, admission.charity)}
	if admission.charity {
		physical, err := request.withTransport(admission.transportRule)
		if err != nil {
			return executionPlan{}, err
		}
		connectorTypes := service.supportedCharityConnectorTypes(physical, admission.flatten)
		physical.Clear()
		if len(connectorTypes) == 0 {
			return executionPlan{}, &openai.ValidationError{Detail: requestattempt.NewDetail("request", "no connector supports the required request features")}
		}
		value, err := service.charity.Snapshot(ctx, userID, admission.modelID, admission.decisionNow, connectorTypes)
		if err != nil {
			return executionPlan{}, err
		}
		if !value.ModelTypes.Supports(request.operation) {
			return executionPlan{}, unsupportedModelType()
		}
		if !sameCharitySnapshot(admission, value) {
			return executionPlan{}, ErrInternal
		}
		plan.candidates = append([]RouteCandidate(nil), value.Candidates...)
		plan.charityStrategy = value.RouteStrategy
		plan.purpose = claim.PurposeCharity
	} else {
		value, err := service.personal.Snapshot(ctx, userID, identifier)
		if err != nil {
			return executionPlan{}, err
		}
		if !value.ModelTypes.Supports(request.operation) {
			return executionPlan{}, unsupportedModelType()
		}
		if !samePersonalSnapshot(admission, value, userID) {
			return executionPlan{}, ErrInternal
		}
		plan.candidates = append([]RouteCandidate(nil), value.Candidates...)
		plan.purpose = claim.PurposeSelf
		if decision.Active {
			plan.purpose = claim.PurposeDebugLive
		}
	}
	if decision.Active && decision.ClaimPurpose != plan.purpose {
		return executionPlan{}, ErrInternal
	}

	if err := service.freezeGatewayModels(ctx, &plan); err != nil {
		return executionPlan{}, err
	}
	if err := service.freezeAdaptations(ctx, request, &plan, inbound); err != nil {
		plan.clearPrepared()
		return executionPlan{}, err
	}
	if err := freezeTransport(request, &plan); err != nil {
		plan.clearPrepared()
		return executionPlan{}, err
	}
	capable := make([]RouteCandidate, 0, len(plan.candidates))
	var rejection error
	for _, candidate := range plan.candidates {
		if !service.validCandidate(candidate, admission.charity) {
			plan.clearPrepared()
			return executionPlan{}, ErrInternal
		}
		candidateRequest := candidate.prepared.source(request)
		policy := candidate.Policy
		policy.FlattenToolCalls = request.chat != nil && admission.flatten
		if request.chat == nil {
			policy.ForceStoreFalse = false
		}
		if candidate.prepared != nil {
			policy.NativeExtensions = candidate.prepared.native
		}
		target := connectorcontract.NewTarget(candidate.ConnectorType, candidate.CanonicalBaseURL, candidate.UpstreamModelID)
		rejected := candidateRequest.checkTarget(service.registry, target, policy)
		if rejected == nil {
			candidate.Policy.FlattenToolCalls = request.chat != nil && admission.flatten
			capable = append(capable, candidate)
		} else {
			if rejection == nil {
				rejection = rejected
			}
		}
	}
	plan.clearUnusedPrepared(capable)
	clear(plan.candidates)
	if len(capable) == 0 {
		if rejection != nil {
			return executionPlan{}, rejection
		}
		return executionPlan{}, openai.ErrInvalidRequest
	}
	ordered, err := orderCandidates(plan.strategy, capable)
	if err != nil {
		for _, candidate := range capable {
			if candidate.prepared != nil {
				candidate.prepared.clear()
			}
		}
	}
	clear(capable)
	if err != nil {
		return executionPlan{}, err
	}
	plan.candidates = ordered
	if plan.charity {
		for _, candidate := range ordered {
			if candidate.prepared != nil && candidate.prepared.outputFloor > plan.outputFloor {
				plan.outputFloor = candidate.prepared.outputFloor
			}
		}
		if plan.outputFloor > 0 {
			plan.reservedMilli, err = service.charity.ReserveForOutput(ctx, plan.modelID, plan.reservedMilli, plan.outputFloor)
			if err != nil {
				plan.clearPrepared()
				return executionPlan{}, err
			}
		}
	}
	return plan, nil
}

func (service *Service) supportedCharityConnectorTypes(request *validatedRequest, flatten bool) []connectorcontract.Type {
	if service == nil || service.registry == nil || request == nil {
		return nil
	}
	connectorTypes := make([]connectorcontract.Type, 0, len(service.connectors))
	for connectorType, instance := range service.connectors {
		if instance == nil || request.chat != nil && !request.chat.SupportsRolePassthrough(string(connectorType)) {
			continue
		}
		if service.adaptations != nil {
			descriptor, ok := service.registry.Descriptor(connectorType)
			if ok && (request.chat == nil || descriptor.Capabilities.Has(connectorcontract.CapabilityText)) && (request.embedding == nil || descriptor.Capabilities.Has(connectorcontract.CapabilityEmbeddings)) && (request.image == nil || descriptor.Capabilities.Has(connectorcontract.CapabilityImagesGenerations)) && (request.decision == nil || descriptor.Capabilities.Has(connectorcontract.CapabilityDecisions)) && (request.inference == nil || descriptor.Capabilities.Has(connectorcontract.OperationCapability(request.operation))) {
				connectorTypes = append(connectorTypes, connectorType)
			}
		} else if request.supports(service.registry, connectorType) {
			connectorTypes = append(connectorTypes, connectorType)
		}
	}
	sort.Slice(connectorTypes, func(left, right int) bool { return connectorTypes[left] < connectorTypes[right] })
	return connectorTypes
}
