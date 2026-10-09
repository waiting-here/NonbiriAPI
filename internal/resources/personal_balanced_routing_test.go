package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func TestPersonalModelHTTPStrategyDefaultsAndPatchOmission(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	owner := environment.seedUser(t, "model-defaults")
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, environment.repository); err != nil {
		t.Fatal(err)
	}
	principal := UserPrincipal{UserID: owner}
	for index, strategy := range []string{"", "ordered", "random", "cache_balanced"} {
		t.Run(fmt.Sprintf("strategy-%s", strategy), func(t *testing.T) {
			body := map[string]any{"provider": "defaults", "model": fmt.Sprint(index)}
			want := strategy
			if strategy == "" {
				want = "cache_balanced"
			} else {
				body["route_strategy"] = strategy
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			response := resourceHTTPCall(t, registrar.handlers["POST "+routeModels], principal,
				http.MethodPost, routeModels, string(raw), resourceTestKey(byte('a'+index)), nil)
			var created Model
			if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || created.RouteStrategy != want {
				t.Fatalf("create response: %d %s", response.Code, response.Body.String())
			}
			response = resourceHTTPCall(t, registrar.handlers["PATCH "+routeModel], principal,
				http.MethodPatch, "/api/models/"+created.ID, `{"expected_revision":"1","silent_retry":true}`,
				resourceTestKey(byte('e'+index)), map[string]string{"id": created.ID})
			var patched Model
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &patched) != nil || patched.RouteStrategy != want || !patched.SilentRetry {
				t.Fatalf("patch omission changed strategy: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

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
