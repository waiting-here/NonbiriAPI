package db

import (
	"fmt"
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

	insertEntry := func(id int64, sourceType, sourceIdentity, model string, sourceRevision int64) {
		t.Helper()
		hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(?,?,?,?,?,'provider',?,0,0)`, id, keyID, sourceType, sourceIdentity, model, sourceRevision)
	}
	insertEntry(1, "manual", "provider/model", "provider/model", 1)
	insertEntry(2, "automatic", "1:0", "provider/automatic", 1)
	insertEntry(3, "automatic", "1:9999", "provider/automatic-max", 1)
	insertEntry(4, "automatic", "9223372036854775807:9999", "provider/automatic-revision-max", hostileInt64Max)
	maxModel := strings.Repeat("界", 512)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(5,?,'manual',?,?, '',1,0,0)`, keyID, maxModel, maxModel)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(6,?,'manual','provider-limit','provider-limit',?,1,0,0)`, keyID, strings.Repeat("供", 128))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(7,?,'manual',?,?, '',1,0,0)`, keyID, strings.Repeat("界", 513), strings.Repeat("界", 513))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(8,?,'manual','provider-over','provider-over',?,1,0,0)`, keyID, strings.Repeat("供", 129))

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
			hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(?,?,?,?,?,'provider',?,0,0)`, 10+i, keyID, tc.sourceType, tc.sourceIdentity, tc.model, tc.revision)
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
	hostileMustExec(t, db, `
	INSERT INTO model_bindings(id,model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
	VALUES(?,?,?, ?,0,0,0)`, bindingID, modelID, keyID, "provider/model")
	hostileMustFail(t, db, `
INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,1,0,0)`, modelID, keyID, "provider/missing")
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
