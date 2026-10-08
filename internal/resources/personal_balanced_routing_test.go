package resources

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func TestPersonalBalancedStrategyCreatePatchFilterAndPreflight(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	owner := environment.seedUser(t, "balanced-model")
	strategy := "cache_balanced"
	input := CreateModelInput{Provider: "provider", Model: "model", RouteStrategy: strategy}
	mutation := resourceTestMutation(t, resourceTestKey('a'), "POST", routeModels, nil, createModelCanonical{
		Provider: input.Provider, Model: input.Model, RouteStrategy: &strategy,
	})
	created, err := environment.repository.CreateModel(context.Background(), owner, mutation, input)
	if err != nil || created.Value.RouteStrategy != strategy {
		t.Fatalf("create balanced model=%+v %v", created, err)
	}
	modelID := resourceTestID(t, created.Value.ID)
	store, err := routing.New(environment.store)
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := store.Preflight(context.Background(), owner, created.Value.FullName)
	if err != nil || preflight.RouteStrategy() != strategy {
		t.Fatalf("balanced preflight=%+v %v", preflight, err)
	}
	filtered, err := environment.repository.FilterModelsPage(context.Background(), owner, ModelPageFilters{RouteStrategy: strategy}, pagination.Default())
	if err != nil || len(filtered.Data) != 1 || filtered.Data[0].ID != created.Value.ID {
		t.Fatalf("balanced filter=%+v %v", filtered, err)
	}
	for index, value := range []string{"random", "cache_balanced"} {
		patch := PatchModelInput{RouteStrategy: &value, ExpectedRevision: int64(index + 1)}
		mutation := resourceTestMutation(t, resourceTestKey(byte('b'+index)), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{
			RouteStrategy: &value, ExpectedRevision: string(rune('1' + index)),
		})
		changed, err := environment.repository.PatchModel(context.Background(), owner, modelID, mutation, patch)
		if err != nil || changed.Value.RouteStrategy != value {
			t.Fatalf("patch strategy=%+v %v", changed, err)
		}
	}
	unknown := "expiry_weighted"
	patch := PatchModelInput{RouteStrategy: &unknown, ExpectedRevision: 3}
	badMutation := resourceTestMutation(t, resourceTestKey('d'), "PATCH", routeModel, []int64{modelID}, patchModelCanonical{RouteStrategy: &unknown, ExpectedRevision: "3"})
	if _, err := environment.repository.PatchModel(context.Background(), owner, modelID, badMutation, patch); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown personal strategy patch=%v", err)
	}
	if _, err := environment.repository.FilterModelsPage(context.Background(), owner, ModelPageFilters{RouteStrategy: unknown}, pagination.Default()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown personal strategy filter=%v", err)
	}
}
