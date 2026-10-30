package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type connectionTestFixture struct {
	env                          *resourceTestEnvironment
	user, connection, credential int64
	next                         int
}

func newConnectionTestFixture(t *testing.T) *connectionTestFixture {
	t.Helper()
	f := &connectionTestFixture{env: newResourceTestEnvironment(t)}
	f.user = f.env.seedUser(t, "connection-resource-owner")
	created, err := f.env.repository.CreateConnection(context.Background(), f.user, f.mutation(t, "POST", routeConnections, nil, ConnectionInput{Name: "Shared service"}), ConnectionInput{Name: "Shared service"})
	if err != nil {
		t.Fatal(err)
	}
	f.connection = resourceTestID(t, created.Value.ID)
	input := CreateCredentialInput{Secret: []byte("shared-test-credential-body"), OwnershipConfirmed: true}
	credential, err := f.env.repository.CreateCredential(context.Background(), f.user, f.connection, f.mutation(t, "POST", routeConnectionCredentials, []int64{f.connection}, input), input)
	if err != nil {
		t.Fatal(err)
	}
	f.credential = resourceTestID(t, credential.Value.ID)
	return f
}
func (f *connectionTestFixture) mutation(t *testing.T, method, route string, ids []int64, body any) ControlMutation {
	t.Helper()
	f.next++
	return resourceTestMutation(t, fmt.Sprintf("%022d", f.next), method, route, ids, body)
}
func (f *connectionTestFixture) entry(t *testing.T, connection int64, base string, services []contract.NativeService) (Endpoint, EndpointKey) {
	t.Helper()
	ctx := context.Background()
	input := CreateEndpointInput{ConnectionID: connection, Source: "custom", ConnectorType: string(contract.TypeOpenAICompatible), BaseURL: base, NativeServices: &services, Enabled: true}
	endpoint, err := f.env.repository.CreateEndpoint(ctx, f.user, f.mutation(t, "POST", routeEndpoints, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	endpointID := resourceTestID(t, endpoint.Value.ID)
	keyInput := CreateEndpointKeyInput{CredentialID: f.credential, NativeServices: &services, Enabled: true, OwnershipConfirmed: true}
	key, err := f.env.repository.CreateEndpointKey(ctx, f.user, endpointID, f.mutation(t, "POST", routeEndpointKeys, []int64{endpointID}, keyInput), keyInput)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint.Value, key.Value
}
func TestConnectionSharedCredentialRotationAndDeletedMaterial(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	services := []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceResponses}
	first, key := f.entry(t, f.connection, "https://example.com/v1", services)
	second, key2 := f.entry(t, f.connection, "https://other.example/v1", services[:1])
	var bodies, origins int
	if err := f.env.store.DB().QueryRow(`SELECT count(*),count(canonical_base_url) FROM endpoint_key_secrets`).Scan(&bodies, &origins); err != nil || bodies != 1 || origins != 0 {
		t.Fatalf("shared neutral body: %d/%d %v", bodies, origins, err)
	}
	sourceInput := CreateDiscoverySourceInput{Name: "Shared catalog", BaseURL: "https://example.com/v1", ListFormat: "openai", CredentialID: f.credential, Enabled: true}
	source, err := f.env.repository.CreateDiscoverySource(ctx, f.user, f.connection, f.mutation(t, "POST", routeConnectionSources, []int64{f.connection}, sourceInput), sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	other := f.env.seedUser(t, "different-connection-owner")
	if _, err := f.env.repository.GetCredential(ctx, other, f.credential); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential owner isolation: %v", err)
	}
	rotate := RotateCredentialInput{Secret: []byte("replacement-shared-test-body"), OwnershipConfirmed: true, ExpectedRevision: 1}
	mutation := f.mutation(t, "POST", routeCredentialVersions, []int64{f.credential}, rotate)
	rotated, err := f.env.repository.RotateCredential(ctx, f.user, f.credential, mutation, rotate)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.env.repository.RotateCredential(ctx, f.user, f.credential, mutation, rotate)
	if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, rotated.Body) {
		t.Fatalf("rotation replay: %v", err)
	}
	for _, pair := range [][2]string{{first.ID, key.ID}, {second.ID, key2.ID}} {
		got, err := f.env.repository.GetEndpointKey(ctx, f.user, resourceTestID(t, pair[0]), resourceTestID(t, pair[1]))
		if err != nil || got.CredentialVersion != "2" || !got.CredentialAvailable {
			t.Fatalf("rotated entry: %+v %v", got, err)
		}
	}
	sourceID := resourceTestID(t, source.Value.ID)
	gotSource, err := f.env.repository.GetDiscoverySource(ctx, f.user, sourceID)
	if err != nil || gotSource.CredentialVersion != "2" {
		t.Fatalf("rotated source: %+v %v", gotSource, err)
	}
	remove := f.mutation(t, "DELETE", routeCredential, []int64{f.credential}, map[string]string{"expected_revision": "2"})
	if _, err := f.env.repository.DeleteCredential(ctx, f.user, f.credential, remove, 2); err != nil {
		t.Fatal(err)
	}
	got, err := f.env.repository.GetEndpointKey(ctx, f.user, resourceTestID(t, first.ID), resourceTestID(t, key.ID))
	if err != nil || got.CredentialAvailable || !got.Enabled {
		t.Fatalf("deleted credential metadata: %+v %v", got, err)
	}
	// Retained service metadata remains editable, but must never be offered as
	// a new donation candidate after deleting its credential.
	eid := resourceTestID(t, first.ID)
	retained, err := f.env.repository.ListEndpointKeysPage(ctx, f.user, eid, pagination.Default())
	if err != nil || len(retained.Data) != 1 || retained.Data[0].Browse.DonationEligibility != "unavailable" {
		t.Fatalf("deleted credential donation eligibility: %+v %v", retained, err)
	}
	available, err := f.env.repository.FilterEndpointKeysPage(ctx, f.user, eid, EndpointKeyPageFilters{CredentialAvailable: "true"}, pagination.Request{Page: 9, Size: 20})
	if err != nil || len(available.Data) != 0 || available.Pagination.TotalItems != "0" || available.Pagination.Page != "1" {
		t.Fatalf("deleted credential leaked into paged candidates: %+v %v", available, err)
	}
	unavailable, err := f.env.repository.FilterEndpointKeysPage(ctx, f.user, eid, EndpointKeyPageFilters{CredentialAvailable: "false"}, pagination.Default())
	if err != nil || len(unavailable.Data) != 1 || unavailable.Data[0].ID != key.ID {
		t.Fatalf("unavailable credential filter: %+v %v", unavailable, err)
	}
	gotSource, err = f.env.repository.GetDiscoverySource(ctx, f.user, sourceID)
	if err != nil || gotSource.CredentialAvailable {
		t.Fatalf("deleted source material: %+v %v", gotSource, err)
	}
	for _, state := range []string{"available", "no_usable_key"} {
		page, err := f.env.repository.FilterEndpointsPage(ctx, f.user, EndpointPageFilters{State: state}, pagination.Default())
		want := "0"
		if state == "no_usable_key" {
			want = "2"
		}
		if err != nil || page.Pagination.TotalItems != want {
			t.Fatalf("deleted credential endpoint filter %s: %+v %v", state, page, err)
		}
		for _, item := range page.Data {
			if item.Browse.State != state || item.Browse.AvailableKeyCount != "0" {
				t.Fatalf("deleted credential browse: %+v", item.Browse)
			}
		}
	}
	body, _ := json.Marshal(got)
	if bytes.Contains(body, []byte("replacement-shared-test-body")) {
		t.Fatal("credential body exposed")
	}
}

func TestConnectionDeletionKeepsOtherServiceCredentialIndependent(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	services := []contract.NativeService{contract.NativeServiceChatCompletions}
	f.entry(t, f.connection, "https://example.com/v1", services)
	input := ConnectionInput{Name: "Another service"}
	created, err := f.env.repository.CreateConnection(ctx, f.user, f.mutation(t, "POST", routeConnections, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	otherGroup := resourceTestID(t, created.Value.ID)
	firstCredential := f.credential
	credentialInput := CreateCredentialInput{Secret: []byte("shared-test-credential-body"), OwnershipConfirmed: true}
	otherCredential, err := f.env.repository.CreateCredential(ctx, f.user, otherGroup, f.mutation(t, "POST", routeConnectionCredentials, []int64{otherGroup}, credentialInput), credentialInput)
	if err != nil {
		t.Fatal(err)
	}
	f.credential = resourceTestID(t, otherCredential.Value.ID)
	endpoint, key := f.entry(t, otherGroup, "https://other.example/v1", services)
	mutation := f.mutation(t, "DELETE", routeConnection, []int64{f.connection}, map[string]string{"expected_revision": "1"})
	if _, err := f.env.repository.DeleteConnection(ctx, f.user, f.connection, mutation, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.repository.GetCredential(ctx, f.user, firstCredential); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted service credential remains visible: %v", err)
	}
	credential, err := f.env.repository.GetCredential(ctx, f.user, f.credential)
	if err != nil || credential.ConnectionID == nil || *credential.ConnectionID != created.Value.ID {
		t.Fatalf("other service credential preserved: %+v %v", credential, err)
	}
	available, err := f.env.repository.GetEndpointKey(ctx, f.user, resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID))
	if err != nil || !available.CredentialAvailable {
		t.Fatalf("other group availability: %+v %v", available, err)
	}
	var membership bool
	if err := f.env.store.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM credentials cr JOIN service_connections sc ON sc.user_id=cr.user_id WHERE cr.id=? AND sc.id=? AND `+connectionCredentialMembership+`)`, f.credential, otherGroup).Scan(&membership); err != nil || !membership {
		t.Fatalf("preserved credential remains discoverable: %v %v", membership, err)
	}
}

func TestConnectionBindingCompactReceiptsAtTargetCapacity(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	endpoint, key := f.entry(t, f.connection, "https://example.com/v1", []contract.NativeService{contract.NativeServiceChatCompletions})
	endpointID, keyID := resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID)
	if _, err := f.env.store.DB().Exec(`UPDATE site_config SET value='256' WHERE key='default_binding_limit'`); err != nil {
		t.Fatal(err)
	}
	selections := make([]BindingTargetSelection, 256)
	for start := 0; start < 256; start += 64 {
		entries := []ManualCatalogInput{}
		for i := start; i < start+64; i++ {
			name := fmt.Sprintf("model-%03d-", i) + string(bytes.Repeat([]byte{'x'}, 160))
			entries = append(entries, ManualCatalogInput{UpstreamModelID: name})
			selections[i] = BindingTargetSelection{EndpointKeyID: keyID, UpstreamModelID: name, NativeServices: []contract.NativeService{contract.NativeServiceChatCompletions}}
		}
		mutation := f.mutation(t, "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: entries})
		if _, err := f.env.repository.CreateManualEntries(ctx, f.user, endpointID, keyID, mutation, entries); err != nil {
			t.Fatal(err)
		}
	}
	model := f.env.createModel(t, f.user, resourceTestKey('z'), "test", "compact")
	modelID := resourceTestID(t, model.ID)
	groups := []BindingSelection{{Targets: selections}}
	mutation := f.mutation(t, "POST", routeBindingBatchReceipt, []int64{modelID}, groups)
	result, err := f.env.repository.addBindings(ctx, f.user, modelID, mutation, 0, groups, true)
	if err != nil {
		t.Fatal(err)
	}
	var receipt BindingBatchReceipt
	if err := json.Unmarshal(result.Body, &receipt); err != nil || receipt.AddedCount != "1" || receipt.BindingCount != "1" || receipt.BindingRevision != "1" || len(result.Body) > 256 {
		t.Fatalf("batch receipt: %s %v", result.Body, err)
	}
	replay, err := f.env.repository.addBindings(ctx, f.user, modelID, mutation, 0, groups, true)
	if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, result.Body) {
		t.Fatalf("batch replay: %v", err)
	}
	full, err := f.env.repository.ListBindings(ctx, f.user, modelID)
	if err != nil || len(full.Bindings) != 1 || len(full.Bindings[0].Targets) != 256 {
		t.Fatalf("full target projection: %v", err)
	}
	body, _ := json.Marshal(full)
	if len(body) <= 65536 {
		t.Fatal("fixture must exceed receipt storage limit")
	}
	bindingID := resourceTestID(t, full.Bindings[0].ID)
	firstID := full.Bindings[0].Targets[0].ID
	for left, right := 0, 255; left < right; left, right = left+1, right-1 {
		selections[left], selections[right] = selections[right], selections[left]
	}
	put := f.mutation(t, "PUT", routeBindingTargets, []int64{modelID, bindingID}, selections)
	replaced, err := f.env.repository.ReplaceBindingTargets(ctx, f.user, modelID, bindingID, put, 1, selections)
	if err != nil || len(replaced.Value.Targets) != 256 || replaced.Value.Targets[255].ID != firstID || len(replaced.Body) > 20000 {
		t.Fatalf("replace receipt preserves IDs: %v", err)
	}
	order := f.mutation(t, "PUT", routeBindingOrder, []int64{modelID}, map[string]string{"response": "receipt", "expected_binding_revision": "2"})
	ordered, err := f.env.repository.orderBindings(ctx, f.user, modelID, order, 2, []int64{bindingID}, true)
	if err != nil || string(ordered.Body) != `{"binding_revision":"3"}` {
		t.Fatalf("order receipt: %s %v", ordered.Body, err)
	}
	remove := f.mutation(t, "DELETE", routeBinding, []int64{modelID, bindingID}, map[string]string{"response": "receipt", "expected_binding_revision": "3"})
	deleted, err := f.env.repository.deleteBinding(ctx, f.user, modelID, bindingID, remove, 3, true)
	if err != nil || string(deleted.Body) != `{"binding_revision":"4"}` {
		t.Fatalf("delete receipt: %s %v", deleted.Body, err)
	}
	final, err := f.env.repository.ListBindings(ctx, f.user, modelID)
	if err != nil || len(final.Bindings) != 0 {
		t.Fatalf("deleted group remains: %+v %v", final, err)
	}
}
