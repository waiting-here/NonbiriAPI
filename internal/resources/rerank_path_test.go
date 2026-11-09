package resources

import (
	"context"
	"errors"
	"testing"
)

func TestEndpointRerankPathOwnerCASReplayAndExport(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "path-owner")
	other := e.seedUser(t, "path-other")
	endpoint := e.createEndpoint(t, owner, resourceTestKey('A'))
	id := resourceTestID(t, endpoint.ID)
	if !endpoint.RerankPathConfigurable || endpoint.RerankPath != "" || endpoint.RerankURL != endpoint.BaseURL+"/rerank" {
		t.Fatalf("default projection: %+v", endpoint)
	}
	path := "/compatible-api/v1/reranks"
	input := PatchEndpointInput{ExpectedRevision: 1, RerankPath: &path}
	mutation := resourceTestMutation(t, resourceTestKey('B'), "PATCH", routeEndpoint, []int64{id}, input)
	if _, err := e.repository.PatchEndpoint(context.Background(), other, id, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	result, err := e.repository.PatchEndpoint(context.Background(), owner, id, mutation, input)
	if err != nil || result.Value.RerankPath != path || result.Value.BaseURL != endpoint.BaseURL {
		t.Fatal(result.Value, err)
	}
	replay, err := e.repository.PatchEndpoint(context.Background(), owner, id, mutation, input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatal(err)
	}
	mutation.IdempotencyKey = resourceTestKey('C')
	if _, err := e.repository.PatchEndpoint(context.Background(), owner, id, mutation, input); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.repository.ExportLifecycleResources(context.Background(), tx, owner, resourceTestNow, 100)
	tx.Rollback()
	if err != nil || len(exported.Endpoints) != 1 || exported.Endpoints[0].RerankPath != path {
		t.Fatal(exported, err)
	}
	input.ExpectedRevision = 2
	invalid := "//other.example/path"
	input.RerankPath = &invalid
	if _, err := e.repository.PatchEndpoint(context.Background(), owner, id, mutation, input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	empty := ""
	input.RerankPath = &empty
	mutation = resourceTestMutation(t, resourceTestKey('D'), "PATCH", routeEndpoint, []int64{id}, input)
	cleared, err := e.repository.PatchEndpoint(context.Background(), owner, id, mutation, input)
	if err != nil || cleared.Value.RerankPath != "" || cleared.Value.RerankURL != endpoint.RerankURL {
		t.Fatal(cleared.Value, err)
	}
}
