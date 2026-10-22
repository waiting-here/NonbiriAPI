package charityrouting

import (
	"errors"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type bindingTargetWire struct {
	DonationKeyID   requiredField[string]                   `json:"donation_key_id"`
	UpstreamModelID requiredField[string]                   `json:"upstream_model_id"`
	NativeServices  requiredField[[]contract.NativeService] `json:"native_services"`
}
type bindingTargetsWire struct {
	ExpectedBindingRevision requiredField[string]              `json:"expected_binding_revision"`
	Targets                 requiredField[[]bindingTargetWire] `json:"targets"`
}

func parseBindingTargetWires(wire []bindingTargetWire) ([]BindingTargetInput, error) {
	if len(wire) == 0 || len(wire) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	items := make([]BindingTargetInput, 0, len(wire))
	for _, item := range wire {
		if !item.DonationKeyID.Set || !item.UpstreamModelID.Set || !item.NativeServices.Set {
			return nil, ErrInvalidRequest
		}
		services, err := contract.ParseNativeServices(item.NativeServices.Value)
		if err != nil || services.Empty() {
			return nil, ErrInvalidRequest
		}
		items = append(items, BindingTargetInput{item.DonationKeyID.Value, item.UpstreamModelID.Value, services.Services()})
	}
	if _, err := targetInputs(items, false); err != nil {
		return nil, err
	}
	return items, nil
}

func (api *httpAPI) putAdminBindingTargets(w http.ResponseWriter, r *http.Request) {
	api.putBindingTargets(w, r, roleAdmin, 0)
}
func (api *httpAPI) putStewardBindingTargets(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.putBindingTargets(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) putBindingTargets(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(w, r, "bindingId")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var wire bindingTargetsWire
	body, err := httpapi.ReadJSON(w, r, &wire, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, Validate: func(body []byte) error { return strictjson.ValidateObjectWithFieldLimit(body, 2+3*maxBindingBatch) }})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeRoutingError(w, ErrInvalidRequest)
		}
		return
	}
	clear(body)
	if !wire.ExpectedBindingRevision.Set || !wire.Targets.Set {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	if _, err := parseNonnegativeRevision(wire.ExpectedBindingRevision.Value); err != nil {
		writeRoutingError(w, err)
		return
	}
	targets, err := parseBindingTargetWires(wire.Targets.Value)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	input := BindingTargetsUpdate{wire.ExpectedBindingRevision.Value, targets}
	route := routeAdminBindingTargets
	if role == roleSteward {
		route = routeStewardBindingTargets
	}
	mutation, ok := mutationFor(w, r, route, []int64{modelID, bindingID}, map[string]any{"expected_binding_revision": input.ExpectedBindingRevision, "targets": targets})
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.PutBindingTargetsAdmin(r.Context(), modelID, bindingID, mutation, input)
		if err != nil {
			writeRoutingError(w, err)
			return
		}
		writeMutation(w, result)
		return
	}
	result, err := api.service.PutBindingTargetsSteward(r.Context(), actorID, modelID, bindingID, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, result)
}
