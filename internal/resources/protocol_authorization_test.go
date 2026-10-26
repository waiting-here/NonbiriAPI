package resources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestProtocolServiceEditCreatesOwnedFullGrantsAndKeepsLegacyRows(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	limited := []contract.NativeService{contract.NativeServiceChatCompletions}
	endpoint, key := f.entry(t, f.connection, "https://example.com/v1", limited)
	_, unrelated := f.entry(t, f.connection, "https://other.example/v1", limited)
	endpointID, keyID := resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID)
	var oldGrant int64
	if err := f.env.store.DB().QueryRow("SELECT current_grant_revision_id FROM endpoint_keys WHERE id=?", keyID).Scan(&oldGrant); err != nil {
		t.Fatal(err)
	}
	donationID, memberID := seedDeletionTestMembership(t, f.env, f.user, keyID, string(contract.TypeOpenAICompatible))
	// The resource-only secret writer omits material used by donation review.
	if _, err := f.env.store.DB().Exec(`UPDATE endpoint_key_secrets SET key_body_review_hmac=zeroblob(32) WHERE id IN (SELECT v.secret_ref_id FROM endpoint_key_grant_revisions g JOIN credential_versions v ON v.id=g.credential_version_id WHERE g.id=?)`, oldGrant); err != nil {
		t.Fatal(err)
	}
	txSeed, err := f.env.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txSeed.Rollback()
	inserted, err := txSeed.ExecContext(ctx, `INSERT INTO donation_uses(donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at) VALUES(?,1,0,0,1,?,?)`, memberID, resourceTestNow, resourceTestNow)
	if err != nil {
		t.Fatal(err)
	}
	useID, _ := inserted.LastInsertId()
	if _, err := connectionstore.StagePendingUseRevisionTx(ctx, txSeed, connectionstore.StagePendingUseRevisionInput{UseID: useID, ExpectedRevision: 1, GrantRevisionID: oldGrant, Origin: connectionstore.UseOriginServiceExpansion, At: resourceTestNow}); err != nil {
		t.Fatal(err)
	}
	if err := txSeed.Commit(); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	f.env.repository.pendingSubmission = func(ctx context.Context, tx *sql.Tx, id int64) error {
		callbacks++
		var count, enabled, discovery int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),sum(enabled),sum(discovery_enabled) FROM donation_uses WHERE donation_key_id=? AND current_pending_revision_id IS NOT NULL`, memberID).Scan(&count, &enabled, &discovery); err != nil {
			return err
		}
		if id != donationID || count != 15 || enabled != 0 || discovery != 0 {
			t.Fatalf("pending callback/state: donation=%d count=%d enabled=%d discovery=%d", id, count, enabled, discovery)
		}
		return nil
	}
	name := "Updated service"
	nameOnly := PatchEndpointInput{Note: &name, ExpectedRevision: 1}
	if _, err := f.env.repository.PatchEndpoint(ctx, f.user, endpointID, f.mutation(t, "PATCH", routeEndpoint, []int64{endpointID}, nameOnly), nameOnly); err != nil {
		t.Fatal(err)
	}
	var mask int64
	if err := f.env.store.DB().QueryRow("SELECT native_service_mask FROM endpoint_key_grant_revisions WHERE id=?", oldGrant).Scan(&mask); err != nil || mask != 1 {
		t.Fatalf("name-only changed legacy grant: %d %v", mask, err)
	}
	full, _ := contract.SupportedNativeServices(contract.TypeOpenAICompatible)
	services := full.Services()
	input := PatchEndpointInput{NativeServices: &services, ExpectedRevision: 2}
	mutation := f.mutation(t, "PATCH", routeEndpoint, []int64{endpointID}, input)
	if _, err := f.env.repository.PatchEndpoint(ctx, f.user, endpointID, mutation, input); err != nil {
		t.Fatal(err)
	}
	current, err := f.env.repository.GetEndpointKey(ctx, f.user, endpointID, keyID)
	if err != nil || len(current.NativeServices) != len(services) || current.GrantRevision != "2" {
		t.Fatalf("full grant: %+v %v", current, err)
	}
	if err := f.env.store.DB().QueryRow("SELECT native_service_mask FROM endpoint_key_grant_revisions WHERE id=?", oldGrant).Scan(&mask); err != nil || mask != 1 {
		t.Fatalf("historical grant expanded: %d %v", mask, err)
	}
	if err := f.env.store.DB().QueryRow("SELECT g.native_service_mask FROM endpoint_keys k JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id WHERE k.id=?", unrelated.ID).Scan(&mask); err != nil || mask != 1 {
		t.Fatalf("unrelated key expanded: %d %v", mask, err)
	}
	if replay, err := f.env.repository.PatchEndpoint(ctx, f.user, endpointID, mutation, input); err != nil || !replay.Replayed {
		t.Fatalf("edit replay: %v", err)
	}
	if callbacks != 1 {
		t.Fatalf("pending callback duplicated or omitted: %d", callbacks)
	}
	other := f.env.seedUser(t, "unrelated-protocol-owner")
	tx, err := f.env.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := AuthorizeProtocolServicesTx(ctx, tx, other, keyID, f.env.clock.Load(), false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner expansion: %v", err)
	}
}

func TestProtocolBindingRequiresOneUsableRouteAndAllowsExtraServices(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	services := []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings}
	endpoint, key := f.entry(t, f.connection, "https://example.com/v1", services)
	endpointID, keyID := resourceTestID(t, endpoint.ID), resourceTestID(t, key.ID)
	entries := []ManualCatalogInput{{UpstreamModelID: "synthetic-model"}}
	if _, err := f.env.repository.CreateManualEntries(ctx, f.user, endpointID, keyID, f.mutation(t, "POST", routeManualCatalog, []int64{endpointID, keyID}, entries), entries); err != nil {
		t.Fatal(err)
	}
	model := f.env.createModel(t, f.user, resourceTestKey('m'), "protocol", "converted")
	modelID := resourceTestID(t, model.ID)
	if _, err := f.env.store.DB().Exec("UPDATE models SET model_types=? WHERE id=?", modeltype.Set{contract.OperationResponses}, modelID); err != nil {
		t.Fatal(err)
	}
	target := BindingTargetSelection{EndpointKeyID: keyID, UpstreamModelID: "synthetic-model", NativeServices: []contract.NativeService{contract.NativeServiceEmbeddings}}
	groups := []BindingSelection{{Targets: []BindingTargetSelection{target}}}
	if _, err := f.env.repository.AddBindings(ctx, f.user, modelID, f.mutation(t, "POST", routeBindingBatch, []int64{modelID}, groups), 0, groups); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("useless binding accepted: %v", err)
	}
	target.NativeServices = services
	groups[0].Targets[0] = target
	result, err := f.env.repository.AddBindings(ctx, f.user, modelID, f.mutation(t, "POST", routeBindingBatch, []int64{modelID}, groups), 0, groups)
	if err != nil {
		t.Fatalf("converted Chat route plus unused embeddings rejected: %v", err)
	}
	bindingID := resourceTestID(t, result.Value.Bindings[0].ID)
	target.NativeServices = []contract.NativeService{contract.NativeServiceEmbeddings}
	if _, err := f.env.repository.ReplaceBindingTargets(ctx, f.user, modelID, bindingID, f.mutation(t, "PUT", routeBindingTargets, []int64{modelID, bindingID}, target), 1, []BindingTargetSelection{target}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("useless replacement accepted: %v", err)
	}
	current, err := f.env.repository.ListBindings(ctx, f.user, modelID)
	if err != nil || current.BindingRevision != "1" || len(current.Bindings[0].Targets[0].NativeServices) != 2 {
		t.Fatalf("failed replacement changed saved binding: %+v %v", current, err)
	}
}
