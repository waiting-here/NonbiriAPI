package db

import (
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationTwoHostileAutomaticCatalogIdentity(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
	uid := hostileInsertUser(t, db, "catalog", 0, 0)
	endpointID := hostileInsertEndpoint(t, db, uid, "https://upstream.example/v1")
	secretID := hostileInsertSecret(t, db, "https://upstream.example/v1", 0)
	keyID := hostileInsertEndpointKey(t, db, endpointID, secretID)

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := testseed.SeedDiscoverySourceTx(t.Context(), tx, testseed.DiscoverySourceInput{EndpointKeyID: keyID, ListFormat: "openai", At: 0})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var version int64
	if err = tx.QueryRowContext(t.Context(), "SELECT credential_version_id FROM discovery_source_revisions WHERE id=?", source.SourceRevisionID).Scan(&version); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	runs := map[int64]int64{}
	for _, generation := range []int64{1, hostileInt64Max} {
		result, err := tx.ExecContext(t.Context(), "INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,started_at,completed_at) VALUES(?,1,?,?,'legacy','succeeded',0,0)", source.SourceRevisionID, version, generation)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		runs[generation], err = result.LastInsertId()
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	_, err = tx.ExecContext(t.Context(), "UPDATE discovery_source_states SET result_generation=?,current_run_id=?,last_successful_run_id=? WHERE source_revision_id=?", hostileInt64Max, runs[hostileInt64Max], runs[hostileInt64Max], source.SourceRevisionID)
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	entryQuery := "INSERT INTO model_catalog_entries(id,endpoint_key_id,discovery_run_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,created_at,updated_at) VALUES(?,?,?,?,?,?,'provider',?,0,0,0)"
	entryArgs := func(id int64, sourceType, identity, model string, revision int64) []any {
		var key, run any
		if sourceType == "manual" {
			key = keyID
		} else {
			runID := runs[revision]
			if runID == 0 {
				runID = runs[1]
			}
			run = runID
		}
		return []any{id, key, run, sourceType, identity, model, revision}
	}

	insertEntry := func(id int64, sourceType, sourceIdentity, model string, sourceRevision int64) {
		t.Helper()
		hostileMustExec(t, db, entryQuery, entryArgs(id, sourceType, sourceIdentity, model, sourceRevision)...)
	}

	insertEntry(1, "manual", "provider/model", "provider/model", 1)
	insertEntry(2, "automatic", "1:0", "provider/automatic", 1)
	insertEntry(3, "automatic", "1:9999", "provider/automatic-max", 1)
	insertEntry(4, "automatic", "9223372036854775807:9999", "provider/automatic-revision-max", hostileInt64Max)
	maxModel := strings.Repeat("界", 512)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,created_at,updated_at
) VALUES(5,?,'manual',?,?, '',1,0,0,0)`, keyID, maxModel, maxModel)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,created_at,updated_at
) VALUES(6,?,'manual','provider-limit','provider-limit',?,1,0,0,0)`, keyID, strings.Repeat("供", 128))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,created_at,updated_at
) VALUES(7,?,'manual',?,?, '',1,0,0,0)`, keyID, strings.Repeat("界", 513), strings.Repeat("界", 513))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,created_at,updated_at
) VALUES(8,?,'manual','provider-over','provider-over',?,1,0,0,0)`, keyID, strings.Repeat("供", 129))

	for i, tc := range []struct {
		name, sourceType, sourceIdentity, model string
		revision                                int64
	}{
		{"manual-identity-mismatch", "manual", "provider/other", "provider/model-mismatch", 1},
		{"automatic-leading-revision-zero", "automatic", "01:0", "provider/auto-leading", 1},
		{"automatic-leading-ordinal-zero", "automatic", "1:00", "provider/auto-leading-ordinal", 1},
		{"automatic-revision-mismatch", "automatic", "2:0", "provider/auto-mismatch", 1},
		{"automatic-ordinal-overflow", "automatic", "1:10000", "provider/auto-overflow", 1},
		{"automatic-extra-colon", "automatic", "1:0:1", "provider/auto-colon", 1},
		{"automatic-negative", "automatic", "-1:0", "provider/auto-negative", 1},
		{"automatic-zero-revision", "automatic", "0:0", "provider/auto-zero", 0},
	} {
		t.Run(fmt.Sprintf("%02d-%s", i, tc.name), func(t *testing.T) {
			hostileMustFail(t, db, entryQuery, entryArgs(int64(10+i), tc.sourceType, tc.sourceIdentity, tc.model, tc.revision)...)
		})
	}

	// A pair row is the second half of the catalog identity.  Its support
	// counters are non-negative and its revision is a positive checked scalar.
	hostileMustExec(t, db, `
INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?, 'provider/model', 0, 1, 0, 1, 0)`, keyID)
	hostileMustExec(t, db, `UPDATE model_pair_catalog SET automatic_revision=?,pair_revision=? WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, hostileInt64Max, hostileInt64Max, keyID)
	hostileMustFail(t, db, `UPDATE model_pair_catalog SET automatic_supports=-1 WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, keyID)
	hostileMustFail(t, db, `UPDATE model_pair_catalog SET pair_revision=0 WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, keyID)

	modelResult := hostileMustExec(t, db, `
INSERT INTO models(user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at)
VALUES(?,'provider','model','provider/model','ordered',0,0,1,0,0,0)`, uid)
	modelID := hostileMustLastID(t, modelResult)
	hostileMustExec(t, db, `UPDATE models SET revision=?,binding_revision=? WHERE id=?`, hostileInt64Max, hostileInt64Max, modelID)
	hostileMustFail(t, db, `UPDATE models SET revision=0 WHERE id=?`, modelID)
	hostileMustFail(t, db, `UPDATE models SET binding_revision=? WHERE id=?`, "9223372036854775808", modelID)
	bindingID := hostileNextPK64(t, db, "model_bindings")
	hostileInsertBinding(t, db, false, bindingID, modelID, keyID, "provider/model", 0, 0, 0)
	// An explicit authorized target no longer depends on catalog presence.
	hostileInsertBinding(t, db, false, bindingID+1, modelID, keyID, "provider/missing", 1, 0, 0)
	hostileMustFail(t, db, "UPDATE model_binding_targets SET native_service_mask=0 WHERE binding_id=?", bindingID)

	hostileMustExec(t, db, `
INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,binding_revision,created_at,updated_at)
VALUES('provider','charity','[公益]provider/charity','per_request',?, ?,0,0)`, hostileInt64Max, hostileInt64Max)
}

func TestGenerationTwoHostilePublicSiteConfigWriteBoundary(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "config-boundary.sqlite"))
	db := store.DB()

	// The public Store entry point is intentionally narrower than the typed
	// catalog validator.  Maintenance, announcement identity, and every
	// activity/game key are owned by their respective state/configuration
	// mutation paths; accepting them here would let a generic caller bypass
	// those gates.  A rejected write must not bump the site revision or touch
	// the row's timestamp/value.
	var beforeRevision int64
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	var beforeEpoch string
	if err := db.QueryRow(`SELECT value FROM site_config WHERE key='announcement_epoch'`).Scan(&beforeEpoch); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maintenance_mode", "announcement_epoch"} {
		var beforeValue string
		var beforeUpdatedAt int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&beforeValue, &beforeUpdatedAt); err != nil {
			t.Fatalf("read protected config %s: %v", key, err)
		}
		if err := store.SetSiteConfigValue(key, beforeValue); err == nil {
			t.Fatalf("public config setter accepted protected key %q", key)
		}
		var afterValue string
		var afterUpdatedAt, afterRevision int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&afterValue, &afterUpdatedAt); err != nil {
			t.Fatalf("read protected config %s after rejection: %v", key, err)
		}
		if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&afterRevision); err != nil {
			t.Fatal(err)
		}
		if afterValue != beforeValue || afterUpdatedAt != beforeUpdatedAt || afterRevision != beforeRevision {
			t.Fatalf("protected config %s changed after rejected write: value=%q/%q updated_at=%d/%d revision=%d/%d", key, beforeValue, afterValue, beforeUpdatedAt, afterUpdatedAt, beforeRevision, afterRevision)
		}
	}
	if beforeEpoch == "" {
		t.Fatal("fresh announcement epoch is unexpectedly empty")
	}

	var protectedKeys []string
	rows, err := db.Query(`SELECT key FROM site_config WHERE key LIKE 'activity_%' OR key LIKE 'activities_%' OR key LIKE 'game_%' OR key='games_enabled' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		protectedKeys = append(protectedKeys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(protectedKeys) == 0 {
		t.Fatal("fresh seed has no activity/game config keys to protect")
	}
	for _, key := range protectedKeys {
		var beforeValue string
		var beforeUpdatedAt int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&beforeValue, &beforeUpdatedAt); err != nil {
			t.Fatalf("read protected config %s: %v", key, err)
		}
		if err := store.SetSiteConfigValue(key, beforeValue); err == nil {
			t.Fatalf("public config setter accepted protected key %q", key)
		}
		var afterValue string
		var afterUpdatedAt, afterRevision int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&afterValue, &afterUpdatedAt); err != nil {
			t.Fatalf("read protected config %s after rejection: %v", key, err)
		}
		if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&afterRevision); err != nil {
			t.Fatal(err)
		}
		if afterValue != beforeValue || afterUpdatedAt != beforeUpdatedAt || afterRevision != beforeRevision {
			t.Fatalf("protected config %s changed after rejected write: value=%q/%q updated_at=%d/%d revision=%d/%d", key, beforeValue, afterValue, beforeUpdatedAt, afterUpdatedAt, beforeRevision, afterRevision)
		}
	}

	// A regular catalog key remains writable through this entry point and
	// advances exactly the site revision once.
	var ordinaryBeforeRevision int64
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&ordinaryBeforeRevision); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSiteConfigValue("site_name", "hostile-config-write"); err != nil {
		t.Fatalf("ordinary site config write rejected: %v", err)
	}
	var gotName string
	var ordinaryAfterRevision int64
	if err := db.QueryRow(`SELECT value FROM site_config WHERE key='site_name'`).Scan(&gotName); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&ordinaryAfterRevision); err != nil {
		t.Fatal(err)
	}
	if gotName != "hostile-config-write" || ordinaryAfterRevision != ordinaryBeforeRevision+1 {
		t.Fatalf("ordinary site config write mismatch: value=%q revision=%d want %q/%d", gotName, ordinaryAfterRevision, "hostile-config-write", ordinaryBeforeRevision+1)
	}
}
