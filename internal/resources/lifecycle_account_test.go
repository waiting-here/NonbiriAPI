package resources

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLifecycleResourceExportLimitAndSecretHostileFields(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "lifecycle-export")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('a'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('b'))
	keyID := resourceTestID(t, key.ID)
	model := environment.createModel(t, userID, resourceTestKey('c'), "provider", "model")
	modelID := resourceTestID(t, model.ID)

	operationHash := bytes.Repeat([]byte{0x6f}, 32)
	if _, err := environment.store.DB().Exec(`
UPDATE model_discovery_evidence
SET state='checking',revision=77,operation_hash=?,safe_class='none',safe_diag='HOSTILE-SAFE-DIAG',
    started_at=?,completed_at=NULL,fetched_count=0
WHERE endpoint_key_id=?`, operationHash, resourceTestNow, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_pair_catalog(
 endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at
) VALUES(?,'upstream-safe',0,1,0,91,?)`, keyID, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_catalog_entries(
 endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(?,'manual','upstream-safe','upstream-safe','provider',83,?,?)`, keyID, resourceTestNow, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?, 'upstream-safe',0,?,?)`, modelID, keyID, resourceTestNow, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	callerHash := bytes.Repeat([]byte{0x63}, 32)
	if _, err := environment.store.DB().Exec(`
UPDATE caller_keys
SET generation=1,key_hash=?,display_head='HEAD',display_tail='TAIL',key_created_at=?,updated_at=?
WHERE user_id=?`, callerHash, resourceTestNow, resourceTestNow, userID); err != nil {
		t.Fatal(err)
	}
	var fingerprint []byte
	if err := environment.store.DB().QueryRow(`SELECT secret_fingerprint FROM endpoint_keys WHERE id=?`, keyID).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := environment.repository.ExportLifecycleResources(context.Background(), tx, userID, resourceTestNow, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(exported.Endpoints) != 1 || len(exported.Endpoints[0].Keys) != 1 ||
		len(exported.CatalogPairs) != 1 || len(exported.CatalogPairs[0].ManualEntries) != 1 ||
		len(exported.Models) != 1 || len(exported.Models[0].Bindings) != 1 || exported.CallerKey == nil {
		t.Fatalf("unexpected lifecycle resource shape: %+v", exported)
	}
	if exported.Endpoints[0].Origin != (EndpointOrigin{Kind: "custom"}) {
		t.Fatalf("lifecycle endpoint origin = %+v, want custom", exported.Endpoints[0].Origin)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"test-only-envelope", "HOSTILE-SAFE-DIAG", base64.StdEncoding.EncodeToString(operationHash),
		base64.StdEncoding.EncodeToString(fingerprint), base64.StdEncoding.EncodeToString(callerHash),
		"secret_ref", "fingerprint", "operation_hash", "safe_diag", "revision",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("forbidden lifecycle resource material %q in %s", forbidden, encoded)
		}
	}

	_ = environment.createEndpoint(t, userID, resourceTestKey('d'))
	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.repository.ExportLifecycleResources(context.Background(), tx, userID, resourceTestNow, 1); !errors.Is(err, ErrResourceLimit) {
		_ = tx.Rollback()
		t.Fatalf("limit+1 error=%v", err)
	}
	_ = tx.Rollback()
	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := environment.repository.ExportLifecycleResources(context.Background(), tx, userID, resourceTestNow, 2)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(exact.Endpoints) != 2 {
		t.Fatalf("exact limit endpoint count=%d", len(exact.Endpoints))
	}
}

func TestLifecycleResourceDeletionRollsBackAndRevokesCallerKey(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "lifecycle-delete")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('e'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('f'))
	keyID := resourceTestID(t, key.ID)
	_ = environment.createModel(t, userID, resourceTestKey('g'), "provider", "model")
	callerHash := bytes.Repeat([]byte{0x71}, 32)
	if _, err := environment.store.DB().Exec(`
UPDATE caller_keys
SET generation=1,key_hash=?,display_head='HEAD',display_tail='TAIL',key_created_at=?,updated_at=?
WHERE user_id=?`, callerHash, resourceTestNow, resourceTestNow, userID); err != nil {
		t.Fatal(err)
	}
	var secretRef int64
	if err := environment.store.DB().QueryRow(`SELECT secret_ref_id FROM endpoint_keys WHERE id=?`, keyID).Scan(&secretRef); err != nil {
		t.Fatal(err)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.repository.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, resourceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	for table, query := range map[string]string{
		"endpoints":   `SELECT count(*) FROM endpoints WHERE user_id=?`,
		"models":      `SELECT count(*) FROM models WHERE user_id=?`,
		"caller_keys": `SELECT count(*) FROM caller_keys WHERE user_id=?`,
	} {
		var count int
		if err := tx.QueryRow(query, userID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("prepared %s count=%d err=%v", table, count, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if environment.rowCount(t, `SELECT count(*) FROM endpoints WHERE user_id=?`, userID) != 1 ||
		environment.rowCount(t, `SELECT count(*) FROM models WHERE user_id=?`, userID) != 1 ||
		environment.rowCount(t, `SELECT count(*) FROM caller_keys WHERE user_id=?`, userID) != 1 {
		t.Fatal("resource deletion escaped rolled-back transaction")
	}
	var orphaned any
	if err := environment.store.DB().QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, secretRef).Scan(&orphaned); err != nil || orphaned != nil {
		t.Fatalf("rolled-back orphaned_at=%v err=%v", orphaned, err)
	}

	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.repository.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, resourceTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if environment.rowCount(t, `SELECT count(*) FROM endpoints WHERE user_id=?`, userID) != 0 ||
		environment.rowCount(t, `SELECT count(*) FROM models WHERE user_id=?`, userID) != 0 ||
		environment.rowCount(t, `SELECT count(*) FROM caller_keys WHERE user_id=?`, userID) != 0 {
		t.Fatal("committed resource deletion left owner-private rows")
	}
	var orphanedAt int64
	if err := environment.store.DB().QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, secretRef).Scan(&orphanedAt); err != nil || orphanedAt != resourceTestNow {
		t.Fatalf("committed orphaned_at=%d err=%v", orphanedAt, err)
	}
}
