package v1

import (
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestModelCloudflareUpgradeDefaultsExistingModels(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema26SQLForTest(t), 26, stableSchema26Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	owner := hostileInsertUser(t, database, "cloudflare", 0, 1)
	mustExec(t, database, `INSERT INTO models(user_id,provider,model,full_name,revision,created_at,updated_at) VALUES(?,'provider','self','provider/self',7,100,200)`, owner)
	mustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,created_at,updated_at) VALUES('provider','charity','[公益]provider/charity','per_request',9,100,200)`)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct {
		table    string
		enabled  bool
		revision int
	}{{"models", true, 7}, {"charity_models", false, 9}} {
		var enabled bool
		var revision, created, updated int
		if err := store.DB().QueryRow("SELECT cloudflare_proxy_enabled,revision,created_at,updated_at FROM "+tc.table).Scan(&enabled, &revision, &created, &updated); err != nil || enabled != tc.enabled || revision != tc.revision || created != 100 || updated != 200 {
			t.Fatal(tc, enabled, revision, created, updated, err)
		}
		if _, err := store.DB().Exec("UPDATE " + tc.table + " SET cloudflare_proxy_enabled=2"); err == nil {
			t.Fatal("invalid boolean accepted")
		}
	}
}
