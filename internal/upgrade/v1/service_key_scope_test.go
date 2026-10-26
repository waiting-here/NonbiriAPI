package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func restoreSchema39LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, "DROP TABLE discovery_source_keys")
	hostileMustExec(t, database, "DROP TABLE discovery_run_changes")
	hostileMustExec(t, database, db.DonationDiscoveryAuthorizationSQL)
}

func TestServiceKeyScopeSchema39UpgradePreservesApprovedPendingAndCatalog(t *testing.T) {
	path, vault := bootstrapTestPath(t, "service-scope.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	restoreSchema39LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), `UPDATE schema_state SET version=39`)
	user := hostileInsertUser(t, store.DB(), "scope-owner", 0, 0)
	firstService := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO service_connections(user_id,name,created_at,updated_at) VALUES(?,'First',0,0)`, user))
	secondService := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO service_connections(user_id,name,created_at,updated_at) VALUES(?,'Second',0,0)`, user))
	insertSecret := func(marker byte, body string) int64 {
		t.Helper()
		contextID := hostileBlob16(marker)
		credentialContext, _ := secret.NewGenerationTwoEndpointKeyContext(contextID)
		envelope, err := vault.SealForGenerationTwoContext([]byte(body), credentialContext)
		if err != nil {
			t.Fatal(err)
		}
		return hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO endpoint_key_secrets(context_id,encrypted_secret,created_at,key_body_review_hmac) VALUES(?,?,0,zeroblob(32))`, contextID, envelope))
	}
	oldSecret := insertSecret('A', "approved-body")
	newSecret := insertSecret('B', "pending-body")
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	keys := []testseed.KeyIdentity{}
	for _, service := range []int64{firstService, secondService, firstService} {
		endpoint, err := testseed.InsertEndpointTx(t.Context(), tx, testseed.EndpointInput{OwnerUserID: user, ConnectionID: service, Type: contract.TypeOpenAICompatible, BaseURL: "https://same.example/v1"})
		if err != nil {
			t.Fatal(err)
		}
		input := testseed.KeyInput{EndpointID: endpoint, Fingerprint: hostileBlob32('A')}
		if len(keys) == 0 {
			input.SecretRefID = oldSecret
		} else {
			input.CredentialVersionID = keys[0].CredentialVersionID
		}
		key, err := testseed.InsertEndpointKeyTx(t.Context(), tx, input)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	credential := keys[0].CredentialID
	if _, err := tx.ExecContext(t.Context(), `UPDATE credentials SET connection_id=? WHERE id=?`, firstService, credential); err != nil {
		t.Fatal(err)
	}
	catalog, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: keys[0].KeyID, ListFormat: "openai", Models: []testseed.DiscoveryModel{{ID: "shared-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys[1:] {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO endpoint_key_discovery_sources VALUES(?,?,?,?,?)`, key.KeyID, catalog.SourceID, key.GrantRevisionID, catalog.SourceRevisionID, key.CredentialVersionID); err != nil {
			t.Fatal(err)
		}
	}
	modelResult, err := tx.ExecContext(t.Context(), `INSERT INTO models(user_id,provider,model,full_name,route_strategy,created_at,updated_at) VALUES(?,'fixture','private','fixture/private','cache_balanced',0,0)`, user)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := modelResult.LastInsertId()
	group, err := testseed.InsertModelBindingTx(t.Context(), tx, testseed.BindingInput{ModelID: modelID, KeyID: keys[0].KeyID, UpstreamModelID: "shared-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO model_binding_targets(binding_id,endpoint_key_id,upstream_model_id,native_service_mask,selection_origin,ord,created_at,updated_at) VALUES(?,?,'shared-model',1,'legacy',1,0,0)`, group.BindingID, keys[1].KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := testseed.InsertModelBindingTx(t.Context(), tx, testseed.BindingInput{ModelID: modelID, KeyID: keys[2].KeyID, UpstreamModelID: "shared-model", Order: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	donation := hostileInsertDonation(t, store.DB(), user)
	donationKeys := []int64{}
	for _, key := range keys {
		donationKey := hostileInsertDonationKey(t, store.DB(), donation, key.KeyID)
		hostileMustExec(t, store.DB(), `INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,0)`, key.KeyID, donationKey, donation)
		donationKeys = append(donationKeys, donationKey)
	}
	// Seed approved uses through the published schema's normal fixture boundary.
	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i, key := range donationKeys {
		seed := testseed.SeedDonationUsesWithDiscoveryTx
		if i == 2 {
			seed = testseed.SeedDonationUsesTx
		}
		if _, err := seed(t.Context(), tx, key, true, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
 SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,X'00000000000000000000000000000001',X'00000000000000000000000000000002',zeroblob(16),zeroblob(16),100,1,0,0,0
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM endpoint_key_discovery_sources`); err != nil {
		t.Fatal(err)
	}
	versionResult, err := tx.ExecContext(t.Context(), `INSERT INTO credential_versions(credential_id,version,secret_ref_id,created_at) VALUES(?,2,?,1)`, credential, newSecret)
	if err != nil {
		t.Fatal(err)
	}
	newVersion, _ := versionResult.LastInsertId()
	if _, err := tx.ExecContext(t.Context(), `UPDATE credentials SET current_version_id=?,revision=2,updated_at=1 WHERE id=?`, newVersion, credential); err != nil {
		t.Fatal(err)
	}
	// A second grant and pending approval coexist with the approved first grant.
	for _, key := range keys {
		result, err := tx.ExecContext(t.Context(), `INSERT INTO endpoint_key_grant_revisions(endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
 SELECT endpoint_key_id,endpoint_id,owner_user_id,credential_id,?,2,native_service_mask,rerank_format,report_fingerprint,'explicit',1 FROM endpoint_key_grant_revisions WHERE id=?`, newVersion, key.GrantRevisionID)
		if err != nil {
			t.Fatal(err)
		}
		grant, _ := result.LastInsertId()
		if _, err := tx.ExecContext(t.Context(), `UPDATE endpoint_keys SET current_grant_revision_id=? WHERE id=?`, grant, key.KeyID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO donation_use_revisions(use_id,use_revision,grant_revision_id,source_grant_revision_id,credential_id_snapshot,credential_version,grant_revision,report_fingerprint,key_body_review_hmac,origin,review_state,created_at)
 SELECT u.id,2,?,?,?,2,2,g.report_fingerprint,zeroblob(32),'rotation','pending',1 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN endpoint_key_grant_revisions g ON g.id=? WHERE dk.endpoint_key_id=?`, grant, grant, credential, grant, key.KeyID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE donation_uses SET current_pending_revision_id=(SELECT id FROM donation_use_revisions WHERE use_id=donation_uses.id AND use_revision=2),revision=revision+1,updated_at=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A source retained only by approved uses still needs pair isolation.
	hostileMustExec(t, store.DB(), `DELETE FROM endpoint_key_discovery_sources`)
	preserved := gatewayRows(t, store.DB())
	assertRetainedManifest(t, store.DB(), db.Schema39ManifestHash)
	store.Close()
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema39 started without explicit upgrade")
	}
	// Force a failure after the complete transform to prove data and DDL rollback.
	index := migrationIndex(t, 39)
	original := storageSchema.versions[index].apply
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			t.Errorf("migration failed before rollback injection: %v", err)
			return err
		}
		return errors.New("rollback fixture")
	}
	_, err = Run(t.Context(), path, vault, Options{Apply: true})
	storageSchema.versions[index].apply = original
	if err == nil {
		t.Fatal("forced migration failure succeeded")
	}
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema39ManifestHash)
		var n int
		if err := database.QueryRow(`SELECT count(*) FROM credentials`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("rollback left %d credentials", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB())
	for _, table := range []string{"endpoint_keys", "models", "model_bindings", "model_binding_targets", "donations", "donation_keys", "donation_key_memberships", "donation_uses", "donation_quota_rules", "donation_quota_epochs", "donation_quota_periods"} {
		if !reflect.DeepEqual(preserved[table], after[table]) {
			t.Fatalf("migration changed stable identities, ordering or budgets in %s", table)
		}
	}
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM credentials`, 2}, {`SELECT count(*) FROM credential_versions`, 4},
		{`SELECT count(*) FROM endpoint_keys`, 3}, {`SELECT count(*) FROM endpoint_key_grant_revisions`, 6},
		{`SELECT count(*) FROM discovery_source_keys`, 3}, {`SELECT count(*) FROM model_catalog_entries`, 3},
		{`SELECT count(*) FROM donation_uses WHERE current_approved_revision_id IS NOT NULL AND current_pending_revision_id IS NOT NULL`, 9},
		{`SELECT count(*) FROM charity_model_catalog WHERE normalized_model_id='shared-model'`, 3},
		{`SELECT count(*) FROM endpoint_grant_authorizations WHERE available=1`, 6},
		{`SELECT count(*) FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id WHERE h.credential_version_id=g.credential_version_id AND h.retry_after_until=100 AND hex(h.failure_streak)='00000000000000000000000000000002'`, 9},
		{`SELECT count(*) FROM model_bindings`, 2},
		{`SELECT count(*) FROM model_binding_targets`, 3},
		{`SELECT count(*) FROM models WHERE full_name='fixture/private' AND route_strategy='cache_balanced'`, 1},
		{`SELECT count(*) FROM endpoint_key_grant_revisions g JOIN endpoints e ON e.id=g.endpoint_id JOIN credentials c ON c.id=g.credential_id WHERE e.connection_id<>c.connection_id`, 0},
	} {
		var got int
		if err := store.DB().QueryRow(check.query).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s: got %d want %d: %v", check.query, got, check.want, err)
		}
	}
	// Re-encryption kept both version bodies while changing their context IDs.
	rows, err := store.DB().Query(`SELECT s.context_id,s.encrypted_secret,v.version FROM credential_versions v JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id ORDER BY v.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw []byte
		var envelope string
		var version int
		if err := rows.Scan(&raw, &envelope, &version); err != nil {
			t.Fatal(err)
		}
		credentialContext, _ := secret.NewGenerationTwoEndpointKeyContext(raw)
		body, err := vault.OpenForGenerationTwoContext(envelope, credentialContext)
		want := "approved-body"
		if version == 2 {
			want = "pending-body"
		}
		if err != nil || string(body) != want {
			t.Fatalf("version %d body mismatch: %v", version, err)
		}
		clear(body)
	}
	rows.Close()
	hostileMustExec(t, store.DB(), `UPDATE credentials SET enabled=0 WHERE connection_id=?`, firstService)
	var available int
	if err := store.DB().QueryRow(`SELECT count(*) FROM endpoint_grant_authorizations a JOIN endpoints e ON e.id=a.endpoint_id WHERE e.connection_id=? AND a.available=1`, secondService).Scan(&available); err != nil || available != 2 {
		t.Fatalf("cross-service disable affected retained grants: %d %v", available, err)
	}
	if err := foreignKeyCheck(t.Context(), store.DB()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceKeyScopeAssignsLegacyUnscopedEndpointAndSource(t *testing.T) {
	path, vault := bootstrapTestPath(t, "unscoped.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	key := seedEndpointKeyHistory(t, store.DB(), vault, 'U')
	restoreSchema39LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), `UPDATE schema_state SET version=39;
 UPDATE endpoints SET connection_id=NULL;
 UPDATE credentials SET connection_id=NULL;`)
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	source, err := testseed.SeedDiscoverySourceTx(t.Context(), tx, testseed.DiscoverySourceInput{EndpointKeyID: key, ListFormat: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, store.DB(), db.Schema39ManifestHash)
	store.Close()
	explicitUpgrade(t, path, vault)
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	var matches int
	err = store.DB().QueryRow(`SELECT count(*) FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
 JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id JOIN credentials c ON c.id=g.credential_id
 JOIN discovery_sources s ON s.id=? JOIN discovery_source_keys scope ON scope.source_id=s.id
 WHERE k.id=? AND e.connection_id IS NOT NULL AND c.connection_id=e.connection_id AND s.connection_id=e.connection_id AND scope.endpoint_key_id=k.id`, source.SourceID, key).Scan(&matches)
	if err != nil || matches != 1 {
		t.Fatalf("legacy service relationship was not preserved: %d %v", matches, err)
	}
}
