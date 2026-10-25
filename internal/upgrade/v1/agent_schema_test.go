package v1

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
)

func TestAgentOfflineSchema36PreservesPolicyRepresentationAndFacts(t *testing.T) {
	vault := testVault(t, 0x61)
	path := filepath.Join(t.TempDir(), "source36.sqlite")
	dbfixture.Materialize(t, path, vault)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	step := storageSchema.versions[len(storageSchema.versions)-1]
	if step.from != 36 || !reflect.DeepEqual(step.impact.controlTables, []string{"steward_permissions"}) {
		t.Fatal("unclassified policy normalization", step.impact)
	}
	for _, table := range step.impact.addedTables {
		mustExec(t, database, "DROP TABLE "+quoteSQLiteIdentifier(table))
	}
	mustExec(t, database, "UPDATE schema_state SET version=36")
	user := hostileInsertUser(t, database, "stored-policy-owner", 0, 0)
	policy := authz.DefaultStewardPermissions(5)
	for key := range policy.Permissions {
		policy.Permissions[key] = false
	}
	policy.Permissions["users.read"] = true
	policy.DonationScope = authz.DonationScopeEnhanced
	for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
		delete(policy.Permissions, key)
	}
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	audit, _ := json.Marshal(policy)
	mustExec(t, database, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,3,?,?,'enhanced',2,3)`, user, string(permissions), string(limits))
	mustExec(t, database, `INSERT INTO steward_permission_audits(user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,2,3,?,?,3)`, user, string(audit), string(audit))
	if hash, err := db.SchemaManifestHash(t.Context(), database); err != nil || hash != db.Schema36ManifestHash {
		t.Fatal(hash, err)
	}
	before := gatewayRows(t, database)
	database.Close()
	result, err := Run(t.Context(), path, vault, Options{})
	if err != nil || result.Status != "ready" || result.SourceSchemaVersion != 36 {
		t.Fatal(result, err)
	}
	result, err = Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB())
	for table, rows := range before {
		if table != "steward_permissions" && !reflect.DeepEqual(rows, after[table]) {
			t.Fatal("unrelated original facts changed", table)
		}
	}
	var got, gotLimits, scope string
	var revision, created, updated int64
	if err = store.DB().QueryRow("SELECT permissions,limits,donation_scope,revision,created_at,updated_at FROM steward_permissions WHERE user_id=?", user).Scan(&got, &gotLimits, &scope, &revision, &created, &updated); err != nil {
		t.Fatal(err)
	}
	current, err := authz.DecodeStewardPermissions(5, revision, got, gotLimits, scope)
	if err != nil || revision != 3 || created != 2 || updated != 3 || gotLimits != string(limits) || scope != "enhanced" {
		t.Fatal("stored metadata changed", current, err)
	}
	for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
		if current.Permissions[key] {
			t.Fatal("old custom acquired new privilege", key)
		}
		delete(current.Permissions, key)
	}
	if !reflect.DeepEqual(current.Permissions, policy.Permissions) {
		t.Fatal("original grants changed")
	}
	for _, table := range step.impact.addedTables {
		var count int
		if err := store.DB().QueryRow("SELECT count(*) FROM " + quoteSQLiteIdentifier(table)).Scan(&count); err != nil || count != 0 {
			t.Fatal("new asset table seeded", table, count, err)
		}
	}
}
