package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

// AdaptationReader supplies one consistent, read-only configuration snapshot
// for every candidate in a logical request. It has no crypto or DB dependency.
type AdaptationReader interface {
	LoadMany(context.Context, []requestadaptation.Ref) (map[requestadaptation.Ref]requestadaptation.Snapshot, error)
}

type preparedAttempt struct {
	request     *validatedRequest
	headers     http.Header
	native      map[string]json.RawMessage
	active      bool
	outputFloor int64
}

func outputFloor(candidate RouteCandidate) int64 {
	if candidate.prepared == nil {
		return 0
	}
	return candidate.prepared.outputFloor
}

func (p *preparedAttempt) clear() {
	if p == nil {
		return
	}
	p.request.Clear()
	for name := range p.headers {
		delete(p.headers, name)
	}
	for path, raw := range p.native {
		clear(raw)
		delete(p.native, path)
	}
	*p = preparedAttempt{}
}

func (plan *executionPlan) clearPrepared() {
	if plan == nil {
		return
	}
	seen := make(map[*preparedAttempt]bool)
	for _, candidate := range plan.candidates {
		if candidate.prepared != nil && !seen[candidate.prepared] {
			candidate.prepared.clear()
			seen[candidate.prepared] = true
		}
	}
}

func adaptationActive(d requestadaptation.Document) bool {
	return len(d.ForwardHeaders.Values)+len(d.FixedHeaders.Values)+len(d.BodyDefaults.Values)+len(d.BodyForced.Values)+len(d.NativeExtensionPaths.Values) != 0
}

func (service *Service) freezeAdaptations(ctx context.Context, request *validatedRequest, plan *executionPlan, inbound http.Header) error {
	if service.adaptations == nil || len(plan.candidates) == 0 {
		return nil
	}
	refs := make([]requestadaptation.Ref, 0, len(plan.candidates)+1)
	if plan.charity {
		refs = append(refs, requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: plan.modelID})
	}
	for _, candidate := range plan.candidates {
		if plan.charity {
			if candidate.BindingID <= 0 {
				return ErrInternal
			}
			refs = append(refs, requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: candidate.BindingID})
		} else {
			refs = append(refs, requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: candidate.EndpointID})
		}
	}
	snapshots, err := service.adaptations.LoadMany(ctx, refs)
	if err != nil {
		return err
	}
	defer func() {
		for _, snapshot := range snapshots {
			snapshot.Clear()
		}
	}()
	for index := range plan.candidates {
		candidate := &plan.candidates[index]
		var effective requestadaptation.Snapshot
		if plan.charity {
			model := snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: plan.modelID}]
			binding := snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: candidate.BindingID}]
			effective = requestadaptation.Effective(model, binding)
		} else {
			effective = snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: candidate.EndpointID}]
		}
		if requestadaptation.ValidateDocument(effective.Document, requestadaptation.ScopeEndpoint) != nil || requestadaptation.ExcludedConflict(effective.Document, request.excluded) {
			if plan.charity {
				effective.Clear()
			}
			return requestadaptation.ErrConflict
		}
		prepared, err := prepareAdaptedAttempt(request, candidate.ConnectorType, effective.Document, inbound)
		if plan.charity {
			effective.Clear()
		}
		if err != nil {
			return err
		}
		candidate.prepared = prepared
	}
	return nil
}

func prepareAdaptedAttempt(request *validatedRequest, connectorType connectorcontract.Type, doc requestadaptation.Document, inbound http.Header) (*preparedAttempt, error) {
	headers, err := requestadaptation.AddedHeaders(inbound, doc)
	if err != nil {
		return nil, err
	}
	prepared := &preparedAttempt{headers: headers, active: adaptationActive(doc)}
	prepared.outputFloor, err = requestadaptation.ForcedMaxOutput(doc)
	if err != nil {
		return nil, err
	}
	if !prepared.active {
		prepared.request = request.CloneForAttempt()
		return prepared, nil
	}
	var logical []byte
	if request.chat != nil {
		logical, err = request.chat.LogicalBody()
	} else {
		logical, err = request.embedding.LogicalBody()
	}
	if err != nil {
		return nil, err
	}
	defer clear(logical)
	adapted, err := requestadaptation.ApplyBody(logical, doc, request.bodyLimit())
	if err != nil {
		return nil, err
	}
	defer func() { clear(adapted) }()
	if connectorType != connectorcontract.TypeOpenAICompatible {
		var filtered []byte
		filtered, prepared.native, err = requestadaptation.ExtractNative(adapted, doc.NativeExtensionPaths.Values, request.bodyLimit())
		if err != nil {
			prepared.clear()
			return nil, err
		}
		clear(adapted)
		adapted = filtered
	}
	prepared.request, err = decodeRequest(bytes.NewReader(adapted), request.operation, request.bodyLimit())
	if err != nil {
		prepared.clear()
		return nil, err
	}
	if len(request.excluded) != 0 {
		if err := prepared.request.excludeFields(request.excluded); err != nil {
			prepared.clear()
			return nil, err
		}
	}
	if prepared.request.Model != request.Model || prepared.request.Stream != request.Stream {
		prepared.clear()
		return nil, openai.ErrInvalidRequest
	}
	return prepared, nil
}
