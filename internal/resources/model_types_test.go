package resources

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestModelTypesDefaultPatchReplayAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "model-types")
	model := e.createModel(t, owner, resourceTestKey('a'), "provider", "model")
	if len(model.ModelTypes) != 2 || !model.ModelTypes.Supports(contract.OperationChatCompletions) || !model.ModelTypes.Supports(contract.OperationResponses) {
		t.Fatalf("default types=%v", model.ModelTypes)
	}
	id := resourceTestID(t, model.ID)
	types := modeltype.Set{contract.OperationChatCompletions, contract.OperationImagesGenerations}
	input := PatchModelInput{ModelTypes: &types, ExpectedRevision: 1}
	mutation := resourceTestMutation(t, resourceTestKey('b'), "PATCH", routeModel, []int64{id}, patchModelCanonical{ModelTypes: &types, ExpectedRevision: "1"})
	changed, err := e.repository.PatchModel(context.Background(), owner, id, mutation, input)
	if err != nil || changed.Value.Revision != "2" || !changed.Value.ModelTypes.Supports(contract.OperationImagesGenerations) || changed.Value.ModelTypes.Supports(contract.OperationEmbeddings) || changed.Value.ModelTypes.Supports(contract.OperationResponses) {
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
			e := newResourceTestEnvironment(t)
			owner := e.seedUser(t, "single-model-interface")
			ctx := context.Background()
			input := CreateModelInput{Provider: "provider", Model: "single", RouteStrategy: "ordered", ModelTypes: tc.initial}
			mutation := resourceTestMutation(t, resourceTestKey('a'), "POST", routeModels, nil, createModelCanonical{
				Provider: input.Provider, Model: input.Model, RouteStrategy: pointer("ordered"), ModelTypes: &tc.initial,
			})
			created, err := e.repository.CreateModel(ctx, owner, mutation, input)
			if err != nil || !slices.Equal(created.Value.ModelTypes, tc.initial) {
				t.Fatalf("create=%+v err=%v", created, err)
			}
			id := resourceTestID(t, created.Value.ID)
			assertStored := func(want modeltype.Set, wantMask int64) {
				t.Helper()
				read, err := e.repository.GetModel(ctx, owner, id)
				if err != nil || !slices.Equal(read.ModelTypes, want) {
					t.Fatalf("read=%+v want=%v err=%v", read, want, err)
				}
				var mask int64
				if err := e.store.DB().QueryRow("SELECT model_types FROM models WHERE id=?", id).Scan(&mask); err != nil || mask != wantMask {
					t.Fatalf("stored mask=%d want=%d err=%v", mask, wantMask, err)
				}
			}
			assertStored(tc.initial, tc.initialMask)
			patch := PatchModelInput{ModelTypes: &tc.changed, ExpectedRevision: 1}
			change := resourceTestMutation(t, resourceTestKey('b'), "PATCH", routeModel, []int64{id},
				patchModelCanonical{ModelTypes: &tc.changed, ExpectedRevision: "1"})
			changed, err := e.repository.PatchModel(ctx, owner, id, change, patch)
			if err != nil || !slices.Equal(changed.Value.ModelTypes, tc.changed) {
				t.Fatalf("patch=%+v err=%v", changed, err)
			}
			assertStored(tc.changed, tc.changedMask)
			replayed, err := e.repository.PatchModel(ctx, owner, id, change, patch)
			if err != nil || !replayed.Replayed || !bytes.Equal(replayed.Body, changed.Body) {
				t.Fatalf("replay=%+v err=%v", replayed, err)
			}
			tx, err := e.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			exported, err := e.repository.ExportLifecycleResources(ctx, tx, owner, resourceTestNow, 100)
			if err != nil || len(exported.Models) != 1 || !slices.Equal(exported.Models[0].ModelTypes, tc.changed) {
				t.Fatalf("export=%+v err=%v", exported, err)
			}
		})
	}
}
