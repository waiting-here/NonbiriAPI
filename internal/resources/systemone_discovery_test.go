package resources

import (
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestSystemOneNewKeyHasOpenAICatalogBeforeMetadataSave(t *testing.T) {
	for _, existingCredential := range []bool{false, true} {
		name := "new credential"
		if existingCredential {
			name = "existing credential"
		}
		t.Run(name, func(t *testing.T) {
			f := newConnectionTestFixture(t)
			ctx := t.Context()
			services := []contract.NativeService{contract.NativeServiceSystemOne}
			input := CreateEndpointInput{ConnectionID: f.connection, Source: "custom", ConnectorType: string(contract.TypeTypeSafeSystemOne), BaseURL: "https://example.com/v1", NativeServices: &services, Enabled: true}
			endpoint, err := f.env.repository.CreateEndpoint(ctx, f.user, f.mutation(t, "POST", routeEndpoints, nil, input), input)
			if err != nil {
				t.Fatal(err)
			}
			endpointID := resourceTestID(t, endpoint.Value.ID)
			keyInput := CreateEndpointKeyInput{NativeServices: &services, Enabled: true, OwnershipConfirmed: true}
			if existingCredential {
				keyInput.CredentialID = f.credential
			} else {
				keyInput.Secret = []byte("synthetic-systemone-key")
			}
			key, err := f.env.repository.CreateEndpointKey(ctx, f.user, endpointID, f.mutation(t, "POST", routeEndpointKeys, []int64{endpointID}, keyInput), keyInput)
			if err != nil {
				t.Fatal(err)
			}
			if key.Value.DiscoverySourceID == nil {
				t.Fatal("new System One key has no default catalog before an extra save")
			}
			sourceID := resourceTestID(t, *key.Value.DiscoverySourceID)
			before, err := f.env.repository.GetDiscoverySource(ctx, f.user, sourceID)
			if err != nil || before.ListFormat != "openai" || before.BaseURL != input.BaseURL {
				t.Fatalf("initial catalog: %+v %v", before, err)
			}
			keyID := resourceTestID(t, key.Value.ID)
			refresh := func(result DiscoveryClaimResult) {
				t.Helper()
				f.env.discovery.mu.Lock()
				f.env.discovery.result = result
				f.env.discovery.mu.Unlock()
				accepted, err := f.env.repository.RefreshDiscovery(ctx, f.user, endpointID, keyID, f.mutation(t, "POST", routeDiscovery, []int64{endpointID, keyID}, nil))
				if err != nil {
					t.Fatal(err)
				}
				waitForDiscoveryOperationState(t, f.env, accepted.Value.OperationID, "completed")
			}
			refresh(DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "jev-latest"}}})
			f.env.discovery.mu.Lock()
			first := f.env.discovery.inputs[0]
			f.env.discovery.mu.Unlock()
			if first.ConnectorType != contract.TypeOpenAICompatible || first.CanonicalBaseURL != input.BaseURL {
				t.Fatalf("default discovery dispatch: %+v", first)
			}
			note := "Named key"
			patch := PatchEndpointKeyInput{Note: &note, ExpectedRevision: 1}
			saved, err := f.env.repository.PatchEndpointKey(ctx, f.user, endpointID, keyID, f.mutation(t, "PATCH", routeEndpointKey, []int64{endpointID, keyID}, patch), patch)
			if err != nil || saved.Value.DiscoverySourceID == nil || *saved.Value.DiscoverySourceID != before.ID {
				t.Fatalf("metadata save changed catalog: %+v %v", saved.Value, err)
			}
			after, err := f.env.repository.GetDiscoverySource(ctx, f.user, sourceID)
			if err != nil || after.ListFormat != before.ListFormat || after.Revision != before.Revision || after.DiscoverySourceRevision != before.DiscoverySourceRevision {
				t.Fatalf("catalog changed after save: %+v %v", after, err)
			}
			refresh(DiscoveryClaimResult{FailureClass: DiscoveryFailureTransport, SafeDiagnostic: "synthetic connection failure"})
			catalog, err := f.env.repository.GetCatalog(ctx, f.user, endpointID, keyID, 100, "")
			if err != nil || catalog.Evidence.State != "failed" || catalog.Evidence.SafeClass != string(DiscoveryFailureTransport) || len(catalog.AutomaticEntries) != 1 || catalog.AutomaticEntries[0].UpstreamModelID != "jev-latest" || catalog.Evidence.LastSuccessAt == nil {
				t.Fatalf("failed refresh lost catalog or diagnosis: %+v %v", catalog, err)
			}
		})
	}
}

func TestSystemOneExplicitCatalogSelectionIsPreserved(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := t.Context()
	services := []contract.NativeService{contract.NativeServiceSystemOne}
	input := CreateEndpointInput{ConnectionID: f.connection, Source: "custom", ConnectorType: string(contract.TypeTypeSafeSystemOne), BaseURL: "https://example.com/v1", NativeServices: &services, Enabled: true}
	endpoint, err := f.env.repository.CreateEndpoint(ctx, f.user, f.mutation(t, "POST", routeEndpoints, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := resourceTestID(t, endpoint.Value.ID)
	sourceInput := CreateDiscoverySourceInput{CredentialID: f.credential, BaseURL: "https://other.example/v1", ListFormat: "anthropic", Enabled: true}
	source, err := f.env.repository.CreateDiscoverySource(ctx, f.user, f.connection, f.mutation(t, "POST", routeConnectionSources, []int64{f.connection}, sourceInput), sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := resourceTestID(t, source.Value.ID)
	for _, selected := range []*int64{nil, &sourceID} {
		keyInput := CreateEndpointKeyInput{CredentialID: f.credential, NativeServices: &services, DiscoverySourceID: &selected, Enabled: true, OwnershipConfirmed: true}
		key, err := f.env.repository.CreateEndpointKey(ctx, f.user, endpointID, f.mutation(t, "POST", routeEndpointKeys, []int64{endpointID}, keyInput), keyInput)
		if err != nil {
			t.Fatal(err)
		}
		if selected == nil {
			if key.Value.DiscoverySourceID != nil {
				t.Fatal("explicitly disabled discovery was replaced by a default")
			}
		} else {
			if key.Value.DiscoverySourceID == nil || *key.Value.DiscoverySourceID != source.Value.ID {
				t.Fatalf("explicit catalog replaced: %+v", key.Value)
			}
			configured, err := f.env.repository.GetDiscoverySource(ctx, f.user, sourceID)
			if err != nil || configured.ListFormat != "anthropic" || configured.BaseURL != sourceInput.BaseURL {
				t.Fatalf("explicit format changed: %+v %v", configured, err)
			}
		}
	}
}
