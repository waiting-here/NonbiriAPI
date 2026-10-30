package resources

import (
	"context"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestRemovingProtocolKeyKeepsSharedCredentialUsable(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	services := []contract.NativeService{contract.NativeServiceChatCompletions}
	first, firstKey := f.entry(t, f.connection, "https://example.com/v1", services)
	otherInput := CreateEndpointInput{ConnectionID: f.connection, Source: "custom", ConnectorType: string(contract.TypeAnthropicCompatible), BaseURL: "https://example.com/v1", Enabled: true}
	other, err := f.env.repository.CreateEndpoint(ctx, f.user, f.mutation(t, "POST", routeEndpoints, nil, otherInput), otherInput)
	if err != nil {
		t.Fatal(err)
	}
	otherID := resourceTestID(t, other.Value.ID)
	grant := CreateEndpointKeyInput{CredentialID: f.credential, Enabled: true, OwnershipConfirmed: true}
	otherKey, err := f.env.repository.CreateEndpointKey(ctx, f.user, otherID, f.mutation(t, "POST", routeEndpointKeys, []int64{otherID}, grant), grant)
	if err != nil {
		t.Fatal(err)
	}
	sourceInput := CreateDiscoverySourceInput{Name: "Shared catalog", BaseURL: "https://example.com/v1", ListFormat: "openai", CredentialID: f.credential, Enabled: true}
	source, err := f.env.repository.CreateDiscoverySource(ctx, f.user, f.connection, f.mutation(t, "POST", routeConnectionSources, []int64{f.connection}, sourceInput), sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	firstID, firstKeyID := resourceTestID(t, first.ID), resourceTestID(t, firstKey.ID)
	remove := f.mutation(t, "DELETE", routeEndpointKey, []int64{firstID, firstKeyID}, map[string]string{"expected_revision": "1"})
	if _, err := f.env.repository.DeleteEndpointKey(ctx, f.user, firstID, firstKeyID, remove, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.repository.GetEndpointKey(ctx, f.user, firstID, firstKeyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed protocol grant is still visible: %v", err)
	}
	remaining, err := f.env.repository.GetEndpointKey(ctx, f.user, otherID, resourceTestID(t, otherKey.Value.ID))
	if err != nil || !remaining.CredentialAvailable || !remaining.Enabled {
		t.Fatalf("other protocol lost its credential: %+v %v", remaining, err)
	}
	credential, err := f.env.repository.GetCredential(ctx, f.user, f.credential)
	if err != nil || !credential.Enabled || credential.Revision != "1" {
		t.Fatalf("removing one protocol changed the shared credential: %+v %v", credential, err)
	}
	catalog, err := f.env.repository.GetDiscoverySource(ctx, f.user, resourceTestID(t, source.Value.ID))
	if err != nil || !catalog.CredentialAvailable {
		t.Fatalf("shared discovery credential lost: %+v %v", catalog, err)
	}
	var live bool
	if err := f.env.store.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM credentials cr JOIN credential_versions cv ON cv.id=cr.current_version_id JOIN endpoint_key_secrets s ON s.id=cv.secret_ref_id WHERE cr.id=? AND cr.deleted_at IS NULL AND s.orphaned_at IS NULL)`, f.credential).Scan(&live); err != nil || !live {
		t.Fatalf("shared secret was retired: %v %v", live, err)
	}
}
