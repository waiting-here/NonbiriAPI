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

func restoreSchema53LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, db.StewardCharityPermissionsSQL)
	assertRetainedManifest(t, database, db.Schema53ManifestHash)
}

func TestResourcePermissionUpgradePreservesCustomPolicyAndRollsBack(t *testing.T) {
	path, vault := bootstrapTestPath(t, "resource-permission.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "resource-policy", 0, 0)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["users.read"], p.Permissions["ai.memory.edit"] = false, true
	delete(p.Permissions, "users.resources")
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,7,?,?,'enhanced',2,3)`, user, string(grants), string(limits))
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permission_audits(user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,6,7,?,?,3)`, user, string(grants), string(grants))
	restoreSchema53LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=53")
	store.Close()
	index := migrationIndex(t, 53)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			return err
		}
		return errors.New("injected after resource permission migration")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("failed migration committed")
	}
	storageSchema.versions[index].apply = original
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema53ManifestHash)
		var actual string
		if err := database.QueryRow("SELECT permissions FROM steward_permissions WHERE user_id=?", user).Scan(&actual); err != nil {
			return err
		}
		if actual != string(grants) {
			t.Fatal("rollback changed policy")
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
	var body, savedLimits, scope, previous, next string
	var revision, created, updated int64
	if err := store.DB().QueryRow(`SELECT permissions,limits,donation_scope,revision,created_at,updated_at FROM steward_permissions WHERE user_id=?`, user).Scan(&body, &savedLimits, &scope, &revision, &created, &updated); err != nil {
		t.Fatal(err)
	}
	var current map[string]bool
	if err := json.Unmarshal([]byte(body), &current); err != nil {
		t.Fatal(err)
	}
	p.Permissions["users.resources"] = false
	if !reflect.DeepEqual(current, p.Permissions) || savedLimits != string(limits) || scope != "enhanced" || revision != 7 || created != 2 || updated != 3 {
		t.Fatal("saved policy changed", body)
	}
	if err := store.DB().QueryRow(`SELECT previous_policy,next_policy FROM steward_permission_audits WHERE user_id=?`, user).Scan(&previous, &next); err != nil || previous != string(grants) || next != string(grants) {
		t.Fatal("historical audit changed", err)
	}
}
