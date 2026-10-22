package resources

import (
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"net/http"
)

type bindingTargetRequest struct {
	EndpointKeyID   requestField[string]                   `json:"endpoint_key_id"`
	UpstreamModelID requestField[string]                   `json:"upstream_model_id"`
	NativeServices  requestField[[]contract.NativeService] `json:"native_services"`
}
type bindingTargetCanonical struct {
	EndpointKeyID   string                   `json:"endpoint_key_id"`
	UpstreamModelID string                   `json:"upstream_model_id"`
	NativeServices  []contract.NativeService `json:"native_services"`
}

func parseBindingTargetRequests(items []bindingTargetRequest) ([]BindingTargetSelection, []bindingTargetCanonical, bool) {
	targets := make([]BindingTargetSelection, len(items))
	canonical := make([]bindingTargetCanonical, len(items))
	for i, item := range items {
		if !item.EndpointKeyID.Set || !item.UpstreamModelID.Set || !item.NativeServices.Set {
			return nil, nil, false
		}
		keyID, err := parseDecimalID(item.EndpointKeyID.Value)
		if err != nil {
			return nil, nil, false
		}
		targets[i] = BindingTargetSelection{EndpointKeyID: keyID, UpstreamModelID: item.UpstreamModelID.Value, NativeServices: item.NativeServices.Value}
		canonical[i] = bindingTargetCanonical{EndpointKeyID: item.EndpointKeyID.Value, UpstreamModelID: item.UpstreamModelID.Value, NativeServices: item.NativeServices.Value}
	}
	return targets, canonical, validTargetSelections(targets)
}

func (api *httpAPI) replaceBindingTargets(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(writer, request, "bId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		ExpectedBindingRevision requestField[string]                 `json:"expected_binding_revision"`
		Targets                 requestField[[]bindingTargetRequest] `json:"targets"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedGeneration(body.ExpectedBindingRevision)
	targets, canonicalTargets, valid := parseBindingTargetRequests(body.Targets.Value)
	if !ok || !body.Targets.Set || !valid {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		ExpectedBindingRevision string                   `json:"expected_binding_revision"`
		Targets                 []bindingTargetCanonical `json:"targets"`
	}{body.ExpectedBindingRevision.Value, canonicalTargets}
	mutation, ok := controlMutation(writer, request, routeBindingTargets, []int64{modelID, bindingID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.ReplaceBindingTargets(request.Context(), principal.UserID, modelID, bindingID, mutation, revision, targets)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
