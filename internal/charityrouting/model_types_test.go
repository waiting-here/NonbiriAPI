package charityrouting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestModelTypesDefaultPatchAndOmission(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	model := e.createModel(t, 'a')
	if len(model.ModelTypes) != 2 || !model.ModelTypes.Supports(contract.OperationChatCompletions) || !model.ModelTypes.Supports(contract.OperationResponses) {
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

func TestModelTypesSingleInterfaceCreateReadAndPatch(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		initial, changed         modeltype.Set
		initialMask, changedMask int64
	}{
		{"chat", modeltype.Set{contract.OperationChatCompletions}, modeltype.Set{contract.OperationResponses}, 1, 16},
		{"responses", modeltype.Set{contract.OperationResponses}, modeltype.Set{contract.OperationChatCompletions}, 16, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRoutingTestEnv(t)
			e.seedUser(t, true, nil)
			ctx := context.Background()
			input := testModelCreate()
			input.ModelTypes = tc.initial
			created, err := e.service.CreateAdmin(ctx,
				routingMutation(t, 'a', http.MethodPost, routeAdminModels, nil, map[string]any{"model_types": tc.initial}), input)
			if err != nil || !slices.Equal(created.Value.ModelTypes, tc.initial) {
				t.Fatalf("create=%+v err=%v", created, err)
			}
			id, err := parsePositiveID(created.Value.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertStored := func(want modeltype.Set, wantMask int64) {
				t.Helper()
				read, err := e.service.GetAdmin(ctx, id)
				if err != nil || !slices.Equal(read.ModelTypes, want) {
					t.Fatalf("read=%+v want=%v err=%v", read, want, err)
				}
				var mask int64
				if err := e.store.DB().QueryRow("SELECT model_types FROM charity_models WHERE id=?", id).Scan(&mask); err != nil || mask != wantMask {
					t.Fatalf("stored mask=%d want=%d err=%v", mask, wantMask, err)
				}
			}
			assertStored(tc.initial, tc.initialMask)
			patch := ModelPatch{ModelTypes: &tc.changed, ExpectedRevision: "1"}
			change := routingMutation(t, 'b', http.MethodPatch, routeAdminModel, []int64{id},
				map[string]any{"model_types": tc.changed, "expected_revision": "1"})
			changed, err := e.service.PatchAdmin(ctx, id, change, patch)
			if err != nil || !slices.Equal(changed.Value.ModelTypes, tc.changed) {
				t.Fatalf("patch=%+v err=%v", changed, err)
			}
			assertStored(tc.changed, tc.changedMask)
			replayed, err := e.service.PatchAdmin(ctx, id, change, patch)
			if err != nil || !replayed.Replayed || !bytes.Equal(replayed.Body, changed.Body) {
				t.Fatalf("replay=%+v err=%v", replayed, err)
			}
		})
	}
}
