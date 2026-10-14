package forward

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	secrets     [][]byte
	active      bool
	outputFloor int64
}

// source stays immutable until the logical request completes. Each dispatch
// takes its own clone, so cancellation and retry cleanup cannot alter it.
func (p *preparedAttempt) source(original *validatedRequest) *validatedRequest {
	if p != nil && p.request != nil {
		return p.request
	}
	return original
}

func (p *preparedAttempt) applyPolicy(policy *connectorcontract.AttemptPolicy) func() {
	if p == nil {
		return func() {}
	}
	policy.AdditionalHeaders = p.headers.Clone()
	policy.HasAdaptation = p.active || len(policy.ManagedHeaders) > 0
	var native map[string]json.RawMessage
	if len(p.native) > 0 {
		native = make(map[string]json.RawMessage, len(p.native))
	}
	for path, value := range p.native {
		native[path] = bytes.Clone(value)
	}
	policy.NativeExtensions = native
	var secrets [][]byte
	for _, value := range p.secrets {
		secrets = append(secrets, bytes.Clone(value))
	}
	policy.ResponseSecrets = secrets
	return func() {
		for _, value := range secrets {
			clear(value)
		}
		policy.ResponseSecrets = nil
		for path, value := range native {
			clear(value)
			delete(native, path)
		}
	}
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
	for _, value := range p.secrets {
		clear(value)
	}
	*p = preparedAttempt{}
}

func (plan *executionPlan) clearPrepared() {
	plan.clearUnusedPrepared(nil)
}

func (plan *executionPlan) clearUnusedPrepared(retained []RouteCandidate) {
	if plan == nil {
		return
	}
	seen := make(map[*preparedAttempt]bool)
	for _, candidate := range retained {
		seen[candidate.prepared] = true
	}
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
	type preparedKey struct {
		connector connectorcontract.Type
		document  [sha256.Size]byte
	}
	cache := make(map[preparedKey]*preparedAttempt)
	for index := range plan.candidates {
		candidate := &plan.candidates[index]
		var effective requestadaptation.Snapshot
		if plan.charity {
			model := snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: plan.modelID}]
			binding := snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: candidate.BindingID}]
			effective = requestadaptation.Effective(model, binding)
		} else {
			endpoint := snapshots[requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: candidate.EndpointID}]
			model := requestadaptation.Snapshot{Document: requestadaptation.Empty(requestadaptation.ScopePersonalModel), Revision: "0"}
			if request.personalAdaptation != nil {
				model = *request.personalAdaptation
			}
			effective = requestadaptation.EffectivePersonal(endpoint, model)
		}
		encoded, err := json.Marshal(effective.Document)
		if err != nil {
			effective.Clear()
			return err
		}
		key := preparedKey{candidate.ConnectorType, sha256.Sum256(encoded)}
		clear(encoded)
		if prepared, exists := cache[key]; exists {
			candidate.prepared = prepared
			effective.Clear()
			continue
		}
		if requestadaptation.ValidateDocument(effective.Document, requestadaptation.ScopeEndpoint) != nil || requestadaptation.ExcludedConflict(effective.Document, request.excluded) {
			effective.Clear()
			return requestadaptation.ErrConflict
		}
		prepared, err := prepareAdaptedAttempt(request, candidate.ConnectorType, effective.Document, inbound)
		effective.Clear()
		if err != nil {
			return err
		}
		candidate.prepared = prepared
		cache[key] = prepared
	}
	return nil
}

func prepareAdaptedAttempt(request *validatedRequest, connectorType connectorcontract.Type, doc requestadaptation.Document, inbound http.Header) (*preparedAttempt, error) {
	headers, err := requestadaptation.AddedHeaders(inbound, doc)
	if err != nil {
		return nil, err
	}
	prepared := &preparedAttempt{headers: headers, active: adaptationActive(doc), secrets: requestadaptation.HeaderSecrets(headers, doc)}
	prepared.outputFloor, err = requestadaptation.ForcedMaxOutput(doc)
	if err != nil {
		prepared.clear()
		return nil, err
	}
	if len(doc.BodyDefaults.Values)+len(doc.BodyForced.Values) == 0 && (connectorType == connectorcontract.TypeOpenAICompatible || len(doc.NativeExtensionPaths.Values) == 0) {
		return prepared, nil
	}
	var logical []byte
	if request.chat != nil {
		logical, err = request.chat.LogicalBody()
	} else if request.image != nil {
		logical, err = request.image.LogicalBody()
	} else {
		logical, err = request.embedding.LogicalBody()
	}
	if err != nil {
		prepared.clear()
		return nil, err
	}
	defer clear(logical)
	adapted, bodySecrets, err := requestadaptation.ApplyBodyWithSecrets(logical, doc, request.bodyLimit())
	if err != nil {
		prepared.clear()
		return nil, err
	}
	prepared.secrets = append(prepared.secrets, bodySecrets...)
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
	if prepared.request.chat != nil {
		prepared.request.chat.InheritRoleRequirements(request.chat)
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
