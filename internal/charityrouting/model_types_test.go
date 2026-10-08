package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestModelTypesDefaultPatchAndOmission(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	model := e.createModel(t, 'a')
	if len(model.ModelTypes) != 1 || !model.ModelTypes.Supports(contract.OperationChatCompletions) {
		t.Fatalf("default=%v", model.ModelTypes)
	}
	id, _ := parsePositiveID(model.ID)
	types := modeltype.Set{contract.OperationEmbeddings, contract.OperationImagesGenerations}
	input := ModelPatch{ExpectedRevision: "1", ModelTypes: &types}
	mutation := routingMutation(t, 'b', http.MethodPatch, routeAdminModel, []int64{id}, map[string]any{"model_types": types})
	changed, err := e.service.PatchAdmin(context.Background(), id, mutation, input)
	if err != nil || changed.Value.ModelTypes.Supports(contract.OperationChatCompletions) || !changed.Value.ModelTypes.Supports(contract.OperationImagesGenerations) {
		t.Fatalf("patch=%+v err=%v", changed, err)
	}
	replayed, err := e.service.PatchAdmin(context.Background(), id, mutation, input)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	enabled := false
	preserved, err := e.service.PatchAdmin(context.Background(), id, routingMutation(t, 'c', http.MethodPatch, routeAdminModel, []int64{id}, map[string]any{"enabled": false}), ModelPatch{ExpectedRevision: "2", Enabled: &enabled})
	if err != nil || !preserved.Value.ModelTypes.Supports(contract.OperationImagesGenerations) {
		t.Fatalf("omission=%+v err=%v", preserved, err)
	}
	empty := modeltype.Set{}
	_, err = e.service.PatchAdmin(context.Background(), id, routingMutation(t, 'd', http.MethodPatch, routeAdminModel, []int64{id}, map[string]any{"model_types": empty}), ModelPatch{ExpectedRevision: "3", ModelTypes: &empty})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty types=%v", err)
	}
}

func TestModelTypesStrictHTTPParsing(t *testing.T) {
	for _, raw := range []string{
		`{"expected_revision":"1","model_types":null}`,
		`{"expected_revision":"1","model_types":[]}`,
		`{"expected_revision":"1","model_types":["images_generations","images_generations"]}`,
		`{"expected_revision":"1","model_types":["unknown"]}`,
	} {
		var wire modelPatchWire
		err := json.Unmarshal([]byte(raw), &wire)
		if err == nil {
			_, _, err = parseModelPatch(wire)
		}
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
