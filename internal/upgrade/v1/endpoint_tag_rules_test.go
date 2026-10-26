package v1

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Restore the exact published source, including the original six-label CHECK.
func restoreSchema38LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema39LayoutForTest(t, database)
	for _, name := range []string{"effective_endpoint_tags", "endpoint_tag_rule_matches"} {
		hostileMustExec(t, database, "DROP VIEW "+name)
	}
	for _, name := range []string{"endpoint_tag_domains_endpoint_insert", "endpoint_tag_domains_key_insert", "endpoint_tag_domains_manual_insert", "endpoint_tag_domains_endpoint_delete", "endpoint_tag_domains_key_delete", "endpoint_tag_domains_manual_delete"} {
		hostileMustExec(t, database, "DROP TRIGGER "+name)
	}
	for _, name := range []string{"endpoint_tag_rule_tags", "endpoint_tag_rules", "endpoint_tag_domains"} {
		hostileMustExec(t, database, "DROP TABLE "+name)
	}
	hostileMustExec(t, database, "DROP INDEX idx_donation_keys_base_url")
	hostileMustExec(t, database, db.EndpointTagsSQL)
}

func TestEndpointTagRulesSchema38ExplicitUpgradePreservesManualAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial-DDL"}[fail], func(t *testing.T) {
			path, vault := bootstrapTestPath(t, "schema38.sqlite"), bootstrapTestVault(t)
			store, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			key := seedEndpointKeyHistory(t, store.DB(), vault, 'Q')
			// Retained ended donation snapshots outlive the physical endpoint.
			hostileMustExec(t, store.DB(), `DELETE FROM endpoints WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)`, key)
			hostileMustExec(t, store.DB(), `INSERT INTO admin_endpoint_tags VALUES('https://gone.example.test:9443/old','community_charity')`)
			restoreSchema38LayoutForTest(t, store.DB())
			hostileMustExec(t, store.DB(), `UPDATE schema_state SET version=38`)
			assertRetainedManifest(t, store.DB(), db.Schema38ManifestHash)
			store.Close()
			if opened, err := db.Open(path, vault); err == nil {
				opened.Close()
				t.Fatal("old source started without explicit upgrade")
			}
			ready, err := Run(t.Context(), path, vault, Options{})
			if err != nil || ready.Status != "ready" || ready.SourceSchemaVersion != 38 {
				t.Fatal(ready, err)
			}
			if fail {
				i := migrationIndex(t, 38)
				original := storageSchema.versions[i].sql
				storageSchema.versions[i].sql = original + `; SELECT missing_rule_migration_function();`
				_, err = Run(t.Context(), path, vault, Options{Apply: true})
				storageSchema.versions[i].sql = original
				if err == nil {
					t.Fatal("partial migration succeeded")
				}
				if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
					assertRetainedManifest(t, database, db.Schema38ManifestHash)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
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
			var domainCount, manualCount int
			err = store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM endpoint_tag_domains WHERE base_url='https://gone.example.test:9443/old'),(SELECT COUNT(*) FROM admin_endpoint_tags WHERE tag='community_charity')`).Scan(&domainCount, &manualCount)
			if err != nil || domainCount != 3 || manualCount != 1 {
				t.Fatal(domainCount, manualCount, err)
			}
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_tag_domains WHERE base_url='https://upstream.example/v1'`).Scan(&domainCount); err != nil || domainCount != 2 {
				t.Fatal("deleted endpoint's retained donation address was not backfilled", domainCount, err)
			}
			hostileMustExec(t, store.DB(), `INSERT INTO endpoint_tag_rules VALUES('example.test','remove')`)
			var count int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM effective_endpoint_tags WHERE base_url='https://gone.example.test:9443/old'`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			hostileMustExec(t, store.DB(), `DELETE FROM endpoint_tag_rules`)
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM effective_endpoint_tags WHERE base_url='https://gone.example.test:9443/old'`).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if !strings.Contains(db.EndpointTagsSQL, "'invalid_entry'))") {
				t.Fatal("published source definition changed")
			}
		})
	}
}
