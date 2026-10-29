package resources

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"strings"
	"testing"
)

func TestReaderPoolResourcesKeepOwnershipAndReleaseSnapshots(t *testing.T) {
	e := newResourceTestEnvironment(t)
	user := e.seedUser(t, "reader-owner")
	other := e.seedUser(t, "reader-other")
	endpoint := e.createEndpoint(t, user, strings.Repeat("e", 24))
	endpointID, _ := parseDecimalID(endpoint.ID)
	key := e.createEndpointKey(t, user, endpointID, strings.Repeat("k", 24))
	keyID, _ := parseDecimalID(key.ID)
	model := e.createModel(t, user, strings.Repeat("m", 24), "reader", "model")
	modelID, _ := parseDecimalID(model.ID)
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		if _, err := e.repository.ListEndpoints(ctx, user, 20, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.ListEndpointsPage(ctx, user, pagination.Default()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetEndpoint(ctx, user, endpointID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetEndpoint(ctx, other, endpointID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ownership: %v", err)
		}
		if _, err := e.repository.ListModels(ctx, user, 20, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetModel(ctx, user, modelID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetCatalog(ctx, user, endpointID, keyID, 20, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.ListBindings(ctx, user, modelID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetEndpointCreateOptions(ctx, user); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.GetResourceOperationStatus(ctx, user, strings.Repeat("z", 24)); err != nil {
			t.Fatal(err)
		}
	})
}
