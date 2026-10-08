package resources

import (
	"context"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestModelTypesDefaultPatchReplayAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "model-types")
	model := e.createModel(t, owner, resourceTestKey('a'), "provider", "model")
	if len(model.ModelTypes) != 1 || !model.ModelTypes.Supports(contract.OperationChatCompletions) {
		t.Fatalf("default types=%v", model.ModelTypes)
	}
	id := resourceTestID(t, model.ID)
	types := modeltype.Set{contract.OperationChatCompletions, contract.OperationImagesGenerations}
	input := PatchModelInput{ModelTypes: &types, ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('b'), "PATCH", routeModel, []int64{id}, patchModelCanonical{ModelTypes: &types, ExpectedRevision: "1"})
	changed, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
	if err != nil || changed.Value.Revision != "2" || !changed.Value.ModelTypes.Supports(contract.OperationImagesGenerations) || changed.Value.ModelTypes.Supports(contract.OperationEmbeddings) {
		t.Fatalf("patch=%+v err=%v", changed, err)
	}
	replay, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	rename := "renamed"
	omitted := resourceTestMutation(t, resourceTestKey('c'), "PATCH", routeModel, []int64{id}, patchModelCanonical{Provider: &rename, ExpectedRevision: "2"})
	preserved, err := e.repository.PatchModel(context.Background(), owner, id, omitted, PatchModelInput{Provider: &rename, ExpectedRevision: 2})
	if err != nil || !preserved.Value.ModelTypes.Supports(contract.OperationImagesGenerations) {
		t.Fatalf("preserved=%+v err=%v", preserved, err)
	}
	for i, invalid := range []modeltype.Set{{}, {contract.OperationImagesGenerations, contract.OperationImagesGenerations}, {"unknown"}} {
		patch := PatchModelInput{ModelTypes: &invalid, ExpectedRevision: 3}
		mutation := resourceTestMutation(t, resourceTestKey(byte('d'+i)), "PATCH", routeModel, []int64{id}, patchModelCanonical{ModelTypes: &invalid, ExpectedRevision: "3"})
		if _, err := e.repository.PatchModel(context.Background(), owner, id, mutation, patch); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid=%v err=%v", invalid, err)
		}
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	if err != nil || len(exported.Models) != 1 || !exported.Models[0].ModelTypes.Supports(contract.OperationImagesGenerations) {
		t.Fatalf("export=%+v err=%v", exported, err)
	}
}
