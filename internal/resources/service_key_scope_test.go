package resources

import (
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestServiceKeyScopeRejectsCrossServiceAndSharedCatalog(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := t.Context()
	first, key := f.entry(t, f.connection, "https://example.com/v1", []contract.NativeService{contract.NativeServiceChatCompletions})
	second, key2 := f.entry(t, f.connection, "https://other.example/v1", []contract.NativeService{contract.NativeServiceChatCompletions})
	if key.DiscoverySourceID == nil || key2.DiscoverySourceID == nil || *key.DiscoverySourceID == *key2.DiscoverySourceID {
		t.Fatal("key catalogs are not independent")
	}
	otherInput := ConnectionInput{Name: "Other"}
	other, err := f.env.repository.CreateConnection(ctx, f.user, f.mutation(t, "POST", routeConnections, nil, otherInput), otherInput)
	if err != nil {
		t.Fatal(err)
	}
	otherID := resourceTestID(t, other.Value.ID)
	endpointInput := CreateEndpointInput{ConnectionID: otherID, Source: "custom", ConnectorType: string(contract.TypeOpenAICompatible), BaseURL: "https://other.example/v1", Enabled: true}
	endpoint, err := f.env.repository.CreateEndpoint(ctx, f.user, f.mutation(t, "POST", routeEndpoints, nil, endpointInput), endpointInput)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := resourceTestID(t, endpoint.Value.ID)
	keyInput := CreateEndpointKeyInput{CredentialID: f.credential, Enabled: true, OwnershipConfirmed: true}
	if _, err := f.env.repository.CreateEndpointKey(ctx, f.user, endpointID, f.mutation(t, "POST", routeEndpointKeys, []int64{endpointID}, keyInput), keyInput); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-service key accepted: %v", err)
	}
	sourceInput := CreateDiscoverySourceInput{CredentialID: f.credential, BaseURL: "https://other.example/v1", ListFormat: "openai", Enabled: true}
	if _, err := f.env.repository.CreateDiscoverySource(ctx, f.user, otherID, f.mutation(t, "POST", routeConnectionSources, []int64{otherID}, sourceInput), sourceInput); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-service catalog accepted: %v", err)
	}
	page := pagination.Default()
	listed, err := f.env.repository.listConnectionCredentials(ctx, f.user, otherID, "", "", connectionListWindow{Page: &page})
	if err != nil || len(listed.Data) != 0 {
		t.Fatalf("another service's key leaked: %+v %v", listed, err)
	}
	sourceID := resourceTestID(t, *key.DiscoverySourceID)
	selection := &sourceID
	otherCredentialInput := CreateCredentialInput{Note: "Other key", Secret: []byte("synthetic-other-service-key"), OwnershipConfirmed: true}
	otherCredential, err := f.env.repository.CreateCredential(ctx, f.user, otherID, f.mutation(t, "POST", routeConnectionCredentials, []int64{otherID}, otherCredentialInput), otherCredentialInput)
	if err != nil {
		t.Fatal(err)
	}
	otherKeyInput := CreateEndpointKeyInput{CredentialID: resourceTestID(t, otherCredential.Value.ID), Enabled: true, OwnershipConfirmed: true}
	otherKey, err := f.env.repository.CreateEndpointKey(ctx, f.user, endpointID, f.mutation(t, "POST", routeEndpointKeys, []int64{endpointID}, otherKeyInput), otherKeyInput)
	if err != nil {
		t.Fatal(err)
	}
	patch := PatchEndpointKeyInput{DiscoverySourceID: &selection, ExpectedRevision: 1}
	otherKeyID := resourceTestID(t, otherKey.Value.ID)
	if _, err := f.env.repository.PatchEndpointKey(ctx, f.user, endpointID, otherKeyID, f.mutation(t, "PATCH", routeEndpointKey, []int64{endpointID, otherKeyID}, patch), patch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-service catalog attachment accepted: %v", err)
	}
	secondID, key2ID := resourceTestID(t, second.ID), resourceTestID(t, key2.ID)
	if _, err := f.env.repository.PatchEndpointKey(ctx, f.user, secondID, key2ID, f.mutation(t, "PATCH", routeEndpointKey, []int64{secondID, key2ID}, patch), patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("shared catalog attachment accepted: %v", err)
	}
	move := PatchEndpointInput{ConnectionID: &otherID, ExpectedRevision: 1}
	firstID := resourceTestID(t, first.ID)
	if _, err := f.env.repository.PatchEndpoint(ctx, f.user, firstID, f.mutation(t, "PATCH", routeEndpoint, []int64{firstID}, move), move); !errors.Is(err, ErrConflict) {
		t.Fatalf("moving a keyed endpoint crossed service boundary: %v", err)
	}
}

func TestServiceKeyScopeRefreshFailureAndEmptyDoNotTouchOtherPair(t *testing.T) {
	f := newConnectionTestFixture(t)
	first, key := f.entry(t, f.connection, "https://example.com/v1", []contract.NativeService{contract.NativeServiceChatCompletions})
	second, key2 := f.entry(t, f.connection, "https://example.com/v1", []contract.NativeService{contract.NativeServiceChatCompletions})
	refresh := func(endpoint Endpoint, key EndpointKey, result DiscoveryClaimResult) {
		t.Helper()
		f.env.discovery.mu.Lock()
		f.env.discovery.result = result
		f.env.discovery.mu.Unlock()
		endpointID, keyID := resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID)
		accepted, err := f.env.repository.RefreshDiscovery(t.Context(), f.user, endpointID, keyID, f.mutation(t, "POST", routeDiscovery, []int64{endpointID, keyID}, nil))
		if err != nil {
			t.Fatal(err)
		}
		waitForDiscoveryOperationState(t, f.env, accepted.Value.OperationID, "completed")
	}
	refresh(first, key, DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "first"}}})
	firstID, keyID := resourceTestID(t, first.ID), resourceTestID(t, key.ID)
	manual := []ManualCatalogInput{{UpstreamModelID: "first"}}
	if _, err := f.env.repository.CreateManualEntries(t.Context(), f.user, firstID, keyID, f.mutation(t, "POST", routeManualCatalog, []int64{firstID, keyID}, manual), manual); err != nil {
		t.Fatal(err)
	}
	refresh(second, key2, DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "second"}}})
	refresh(first, key, DiscoveryClaimResult{FailureClass: DiscoveryFailureTransport})
	read := func(endpoint Endpoint, key EndpointKey, want, state string) {
		t.Helper()
		catalog, err := f.env.repository.GetCatalog(t.Context(), f.user, resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID), 100, "")
		if err != nil || catalog.Evidence.State != state {
			t.Fatalf("catalog state: %+v %v", catalog, err)
		}
		if want == "" {
			if len(catalog.AutomaticEntries) != 0 {
				t.Fatalf("empty success retained automatic rows: %+v", catalog)
			}
		} else if len(catalog.AutomaticEntries) != 1 || catalog.AutomaticEntries[0].UpstreamModelID != want {
			t.Fatalf("pair catalog changed: %+v", catalog)
		}
	}
	read(first, key, "first", "failed")
	read(second, key2, "second", "succeeded")
	refresh(first, key, DiscoveryClaimResult{Succeeded: true})
	read(first, key, "", "succeeded")
	read(second, key2, "second", "succeeded")
	catalog, err := f.env.repository.GetCatalog(t.Context(), f.user, firstID, keyID, 100, "")
	if err != nil || len(catalog.ManualEntries) != 1 || catalog.Evidence.LastSuccessAt == nil || catalog.Evidence.AddedCount == nil || *catalog.Evidence.AddedCount != "0" || catalog.Evidence.RemovedCount == nil || *catalog.Evidence.RemovedCount != "1" {
		t.Fatalf("empty refresh lost manual overlap or delta: %+v %v", catalog, err)
	}
}
