package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestCharityPermissionsUpgradePreservesPoliciesAuditsAndRollback(t *testing.T) {
	path, vault := bootstrapTestPath(t, "charity-permissions.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	database := store.DB()
	user := hostileInsertUser(t, database, "policy-account", 0, 0)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["users.read"] = false
	policy.Permissions["ai.memory.edit"] = true
	added := []string{"charity.request_adaptation", "charity.affinity", "charity.price_templates.manage"}
	for _, key := range added {
		delete(policy.Permissions, key)
	}
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	snapshot, _ := json.Marshal(policy)
	hostileMustExec(t, database, "INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,7,?,?,'enhanced',2,3)", user, string(grants), string(limits))
	hostileMustExec(t, database, "INSERT INTO steward_permission_audits(user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,6,7,?,?,3)", user, string(snapshot), string(snapshot))
	hostileMustExec(t, database, "INSERT INTO request_adaptation_audits(scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at) VALUES('charity_model',42,?,'admin',1,'[]',3)", user)
	restoreSchema50LayoutForTest(t, database)
	hostileMustExec(t, database, "UPDATE schema_state SET version=50")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	index := migrationIndex(t, 50)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			return err
		}
		return errors.New("injected after permission normalization")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("injected migration committed")
	}
	storageSchema.versions[index].apply = original
	err = db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema50ManifestHash)
		var actual string
		if err := database.QueryRow("SELECT permissions FROM steward_permissions WHERE user_id=?", user).Scan(&actual); err != nil {
			return err
		}
		if actual != string(grants) {
			t.Fatal("rollback changed source policy")
		}
		var role string
		if err := database.QueryRow("SELECT actor_role FROM request_adaptation_audits WHERE resource_id=42").Scan(&role); err != nil {
			return err
		}
		if role != "admin" {
			t.Fatal("rollback changed audit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database = store.DB()
	assertRetainedManifest(t, database, db.PinnedGenerationTwoManifestHash)
	var body, gotLimits, scope string
	var revision, created, updated int64
	err = database.QueryRow("SELECT permissions,limits,donation_scope,revision,created_at,updated_at FROM steward_permissions WHERE user_id=?", user).Scan(&body, &gotLimits, &scope, &revision, &created, &updated)
	if err != nil || revision != 7 || created != 2 || updated != 3 || scope != "enhanced" || gotLimits != string(limits) {
		t.Fatal("metadata changed", err)
	}
	got, err := authz.DecodeStewardPermissions(5, revision, body, gotLimits, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range added {
		if got.Permissions[key] {
			t.Fatal("old policy gained grant", key)
		}
		delete(got.Permissions, key)
	}
	if !reflect.DeepEqual(got.Permissions, policy.Permissions) {
		t.Fatal("old grants changed")
	}
	var previous, next string
	if err := database.QueryRow("SELECT previous_policy,next_policy FROM steward_permission_audits WHERE user_id=?", user).Scan(&previous, &next); err != nil || previous != string(snapshot) || next != string(snapshot) {
		t.Fatal("old audit changed", err)
	}
	for i, role := range []string{"trainee5", "level6"} {
		hostileMustExec(t, database, "INSERT INTO request_adaptation_audits(scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at) VALUES('binding',42,?,?,?, '[]',3)", user, role, i+1)
	}
	if _, err := database.Exec("INSERT INTO request_adaptation_audits(scope,resource_id,actor_role,revision,changed_partitions,created_at) VALUES('endpoint',42,'trainee5',1,'[]',3)"); err == nil {
		t.Fatal("personal scope accepted manager role")
	}
	if _, err := database.Exec("UPDATE request_adaptation_audits SET actor_role='admin' WHERE actor_role='trainee5'"); err == nil {
		t.Fatal("retained audit became mutable")
	}
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM request_adaptation_audits WHERE actor_user_id IS NULL").Scan(&count); err != nil || count != 3 {
		t.Fatal("audit actor anonymization", count, err)
	}
	if err := database.QueryRow("SELECT count(*) FROM steward_permissions WHERE user_id=?", user).Scan(&count); err != nil || count != 0 {
		t.Fatal("policy deletion", count, err)
	}
}
