package forward

import (
	"context"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

// ClaimServiceAdapter is the exact Generation 2 claim state-machine bridge.
// It adds no retry, credential access, or accounting behavior.
type ClaimServiceAdapter struct{ service *claim.Service }

func NewClaimServiceAdapter(service *claim.Service) (*ClaimServiceAdapter, error) {
	if service == nil {
		return nil, ErrInvalidConfiguration
	}
	return &ClaimServiceAdapter{service: service}, nil
}

func (adapter *ClaimServiceAdapter) Accept(ctx context.Context, input claim.AcceptInput) (claim.Request, error) {
	return adapter.service.Accept(ctx, input)
}

func (adapter *ClaimServiceAdapter) Claim(ctx context.Context, input claim.ClaimInput) (claim.Handle, error) {
	return adapter.service.Claim(ctx, input)
}

func (adapter *ClaimServiceAdapter) TakeForDispatch(ctx context.Context, handle claim.Handle) (DispatchGrant, error) {
	return adapter.service.TakeForDispatch(ctx, handle)
}

func (adapter *ClaimServiceAdapter) MarkResponseStarted(ctx context.Context, handle claim.Handle) error {
	return adapter.service.MarkResponseStarted(ctx, handle)
}

func (adapter *ClaimServiceAdapter) ReleaseUndispatched(ctx context.Context, handle claim.Handle) (claim.Attempt, error) {
	return adapter.service.ReleaseUndispatched(ctx, handle)
}

func (adapter *ClaimServiceAdapter) CompleteAttempt(ctx context.Context, handle claim.Handle, outcome claim.AttemptOutcome) (claim.Attempt, error) {
	return adapter.service.CompleteAttempt(ctx, handle, outcome)
}

func (adapter *ClaimServiceAdapter) CompleteRequest(ctx context.Context, input claim.CompleteRequestInput) (claim.Request, error) {
	return adapter.service.CompleteRequest(ctx, input)
}

// PersonalRoutingAdapter converts the resource owner's safe routing values to
// the forward-owned immutable projection.
type PersonalRoutingAdapter struct{ store *routing.Store }

func NewPersonalRoutingAdapter(store *routing.Store) (*PersonalRoutingAdapter, error) {
	if store == nil {
		return nil, ErrInvalidConfiguration
	}
	return &PersonalRoutingAdapter{store: store}, nil
}

func (adapter *PersonalRoutingAdapter) Preflight(ctx context.Context, userID int64, identifier string) (PersonalPreflight, error) {
	if adapter == nil || adapter.store == nil {
		return PersonalPreflight{}, ErrInternal
	}
	value, err := adapter.store.Preflight(ctx, userID, identifier)
	if err != nil {
		return PersonalPreflight{}, err
	}
	return PersonalPreflight{
		ModelID: value.ModelID(), OwnerUserID: value.OwnerUserID(), Provider: value.Provider(),
		Model: value.Model(), FullName: value.FullName(), RouteStrategy: value.RouteStrategy(),
		SilentRetry: value.SilentRetry(), FlattenToolCalls: value.FlattenToolCalls(),
		Revision: value.Revision(), BindingRevision: value.BindingRevision(),
	}, nil
}

func (adapter *PersonalRoutingAdapter) Snapshot(ctx context.Context, userID int64, identifier string) (PersonalSnapshot, error) {
	if adapter == nil || adapter.store == nil {
		return PersonalSnapshot{}, ErrInternal
	}
	identity := routing.Identity{FullName: identifier}
	if canonicalPositiveDecimal(identifier) {
		identity = routing.Identity{ModelID: identifier}
	}
	value, err := adapter.store.Snapshot(ctx, userID, identity)
	if err != nil {
		return PersonalSnapshot{}, err
	}
	out := PersonalSnapshot{PersonalPreflight: PersonalPreflight{
		ModelID: value.ModelID(), OwnerUserID: value.OwnerUserID(), Provider: value.Provider(),
		Model: value.Model(), FullName: value.FullName(), RouteStrategy: value.RouteStrategy(),
		SilentRetry: value.SilentRetry(), FlattenToolCalls: value.FlattenToolCalls(),
		Revision: value.Revision(), BindingRevision: value.BindingRevision(),
	}}
	for _, candidate := range value.Candidates() {
		connectorType := connectorcontract.Type(candidate.ConnectorType())
		out.Candidates = append(out.Candidates, RouteCandidate{
			EndpointID: candidate.EndpointID(), EndpointKeyID: candidate.EndpointKeyID(),
			ConnectorType: connectorType, CanonicalBaseURL: candidate.CanonicalBaseURL(),
			UpstreamModelID: candidate.UpstreamModelID(), Order: candidate.Order(),
			Policy: connectorcontract.AttemptPolicy{
				ForceStoreFalse: candidate.ForceStoreFalse(), FlattenToolCalls: value.FlattenToolCalls(),
			},
		})
	}
	return out, nil
}

func (adapter *PersonalRoutingAdapter) ListRoutableModels(ctx context.Context, userID int64, limit int) ([]ListedModel, error) {
	if adapter == nil || adapter.store == nil {
		return nil, ErrInternal
	}
	values, err := adapter.store.ListRoutableModels(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ListedModel, len(values))
	for index, value := range values {
		out[index] = ListedModel{ModelID: value.ModelID, Provider: value.Provider, FullName: value.FullName, CreatedAt: value.CreatedAt}
	}
	return out, nil
}

type CharityRoutingAdapter struct{ service *charityrouting.Service }

func NewCharityRoutingAdapter(service *charityrouting.Service) (*CharityRoutingAdapter, error) {
	if service == nil {
		return nil, ErrInvalidConfiguration
	}
	return &CharityRoutingAdapter{service: service}, nil
}

func (adapter *CharityRoutingAdapter) Preflight(ctx context.Context, userID int64, fullName string, request *openai.ChatRequest, now int64) (CharityPreflight, error) {
	if adapter == nil || adapter.service == nil {
		return CharityPreflight{}, ErrInternal
	}
	value, err := adapter.service.Preflight(ctx, userID, fullName, request, now)
	if err != nil {
		return CharityPreflight{}, err
	}
	return CharityPreflight{
		ModelID: value.ModelID, Provider: value.Provider, Model: value.Model, FullName: value.FullName,
		FlattenToolCalls: value.FlattenToolCalls, ReservedMilli: value.ReservedMilli,
	}, nil
}

func (adapter *CharityRoutingAdapter) Snapshot(ctx context.Context, modelID, now int64, connectorTypes []connectorcontract.Type) (CharitySnapshot, error) {
	if adapter == nil || adapter.service == nil {
		return CharitySnapshot{}, ErrInternal
	}
	value, err := adapter.service.Snapshot(ctx, modelID, now, connectorTypes)
	if err != nil {
		return CharitySnapshot{}, err
	}
	out := CharitySnapshot{CharityPreflight: CharityPreflight{
		ModelID: value.ModelID, Provider: value.Provider, Model: value.Model, FullName: value.FullName,
		FlattenToolCalls: value.FlattenToolCalls, ReservedMilli: value.ReservedMilli,
	}}
	for index, candidate := range value.Candidates() {
		out.Candidates = append(out.Candidates, RouteCandidate{
			EndpointID: candidate.EndpointID, EndpointKeyID: candidate.EndpointKeyID,
			DonationKeyID: candidate.DonationKeyID, ConnectorType: candidate.ConnectorType,
			CanonicalBaseURL: candidate.CanonicalBaseURL, UpstreamModelID: candidate.UpstreamModelID,
			Policy: candidate.Policy, Order: index,
		})
	}
	return out, nil
}

func (adapter *CharityRoutingAdapter) ListAvailableModels(ctx context.Context, now int64, limit int) ([]ListedModel, error) {
	if adapter == nil || adapter.service == nil {
		return nil, ErrInternal
	}
	values, err := adapter.service.ListAvailableModels(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ListedModel, len(values))
	for index, value := range values {
		out[index] = ListedModel{ModelID: value.ModelID, Provider: value.Provider, FullName: value.FullName, CreatedAt: value.CreatedAt}
	}
	return out, nil
}

func canonicalPositiveDecimal(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

var (
	_ ClaimRail      = (*ClaimServiceAdapter)(nil)
	_ PersonalRouter = (*PersonalRoutingAdapter)(nil)
	_ CharityRouter  = (*CharityRoutingAdapter)(nil)
)
