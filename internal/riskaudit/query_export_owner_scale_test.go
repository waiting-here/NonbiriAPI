package riskaudit

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

var exportOwnerQueries = []struct{ name, previous, selected string }{
	{"grants", `SELECT g.id FROM endpoint_key_grant_revisions g JOIN endpoint_keys k ON k.id=g.endpoint_key_id JOIN credential_versions v ON v.id=g.credential_version_id WHERE g.owner_user_id=? ORDER BY g.endpoint_key_id,g.grant_revision LIMIT ?`,
		`SELECT g.id FROM endpoint_key_grant_revisions g JOIN endpoint_keys k ON k.id=g.endpoint_key_id JOIN credential_versions v ON v.id=g.credential_version_id WHERE g.owner_user_id=?1 AND g.endpoint_key_id IN (SELECT k.id FROM endpoints e JOIN endpoint_keys k ON k.endpoint_id=e.id WHERE e.user_id=?1) ORDER BY g.endpoint_key_id,g.grant_revision LIMIT ?2`},
	{"source_revisions", `SELECT sr.id FROM discovery_source_revisions sr JOIN credential_versions v ON v.id=sr.credential_version_id WHERE sr.owner_user_id=? ORDER BY sr.source_id,sr.source_revision LIMIT ?`,
		`SELECT sr.id FROM discovery_source_revisions sr JOIN credential_versions v ON v.id=sr.credential_version_id WHERE sr.owner_user_id=?1 AND sr.source_id IN (SELECT id FROM discovery_sources WHERE user_id=?1) ORDER BY sr.source_id,sr.source_revision LIMIT ?2`},
	{"native_evidence", `SELECT c.id FROM model_catalog_entries c LEFT JOIN endpoint_keys k ON k.id=c.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN discovery_runs r ON r.id=c.discovery_run_id LEFT JOIN discovery_source_revisions sr ON sr.id=r.source_revision_id WHERE COALESCE(e.user_id,sr.owner_user_id)=? ORDER BY c.id LIMIT ?`,
		`WITH owner AS MATERIALIZED (SELECT ? AS user_id), selected AS MATERIALIZED (
SELECT c.id FROM owner CROSS JOIN endpoints e ON e.user_id=owner.user_id CROSS JOIN endpoint_keys k ON k.endpoint_id=e.id CROSS JOIN model_catalog_entries c ON c.endpoint_key_id=k.id
UNION ALL
SELECT c.id FROM owner CROSS JOIN discovery_sources s ON s.user_id=owner.user_id CROSS JOIN discovery_source_revisions sr ON sr.source_id=s.id AND sr.owner_user_id=owner.user_id CROSS JOIN discovery_runs r ON r.source_revision_id=sr.id CROSS JOIN model_catalog_entries c ON c.discovery_run_id=r.id WHERE c.endpoint_key_id IS NULL
LIMIT ?)
SELECT id FROM selected ORDER BY id`},
}

func seedExportOwnerRows(t *testing.T, f *auditFixture, user int64, count int) {
	t.Helper()
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execute := func(query string, args ...any) sql.Result {
		result, err := tx.ExecContext(t.Context(), query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	endpoint, err := testseed.InsertEndpointTx(t.Context(), tx, testseed.EndpointInput{OwnerUserID: user, Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://owner-query.example/v1", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := execute(`INSERT INTO endpoint_key_secrets(context_id,encrypted_secret,created_at) VALUES(?,'nbsec:v2:fixture',1)`, bytes.Repeat([]byte{byte(user)}, 16)).LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	key, err := testseed.InsertEndpointKeyTx(t.Context(), tx, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secret, Fingerprint: bytes.Repeat([]byte{byte(user)}, 32), At: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: key.KeyID, ListFormat: "openai", Models: []testseed.DiscoveryModel{{ID: "original"}}, At: 2})
	if err != nil {
		t.Fatal(err)
	}
	if count > 1 {
		execute(`WITH RECURSIVE n(x) AS(VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO endpoint_key_grant_revisions(endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
SELECT g.endpoint_key_id,g.endpoint_id,g.owner_user_id,g.credential_id,g.credential_version_id,n.x,g.native_service_mask,g.rerank_format,g.report_fingerprint,g.origin,g.created_at FROM n CROSS JOIN endpoint_key_grant_revisions g WHERE g.id=?`, count, key.GrantRevisionID)
		execute(`WITH RECURSIVE n(x) AS(VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
SELECT s.source_id,s.owner_user_id,n.x,s.credential_id,s.credential_version_id,s.canonical_base_url,s.list_format,s.created_at FROM n CROSS JOIN discovery_source_revisions s WHERE s.id=?`, count, source.SourceRevisionID)
		// Half manual and half automatic evidence. Automatic rows are spread
		// over runs so every run remains within the 10,000-model contract.
		execute(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,created_at,updated_at)
SELECT ?,'manual',printf('manual-%d',x),printf('manual-%d',x),1,0,2,2 FROM n`, count/2, key.KeyID)
		execute(`WITH RECURSIVE n(x) AS(VALUES(3) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,started_at,completed_at,model_count)
SELECT ?,1,?,x,'self','succeeded',2,2,1 FROM n`, count/2+1, source.SourceRevisionID, key.CredentialVersionID)
		execute(`INSERT INTO model_catalog_entries(discovery_run_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,created_at,updated_at)
SELECT id,'automatic',printf('%d:0',result_generation),'automatic',result_generation,0,2,2 FROM discovery_runs WHERE source_revision_id=? AND id<>?`, source.SourceRevisionID, source.RunID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func readExportOwnerIDs(t *testing.T, database *sql.DB, query string, owner int64) []int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx, query, owner, 10001)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestQueryRiskEdgesExportOwnerScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_RISK_EDGES") != "1" {
		t.Skip("opt-in export owner-selector workload")
	}
	for _, count := range []int{10000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newAuditFixture(t)
			dense, sparse, empty := f.user(1), f.user(2), f.user(3)
			start := time.Now()
			seedExportOwnerRows(t, f, dense, count)
			seedExportOwnerRows(t, f, sparse, 1)
			t.Logf("setup owner_history=%d elapsed=%s", count, time.Since(start))
			for _, query := range exportOwnerQueries {
				for _, owner := range []struct {
					name string
					id   int64
				}{{"sparse", sparse}, {"empty", empty}, {"dense", dense}} {
					var expected []int64
					for _, mode := range []string{"previous-first", "selected-first", "selected-warm", "previous-warm"} {
						sql := query.previous
						if mode == "selected-first" || mode == "selected-warm" {
							sql = query.selected
						}
						start := time.Now()
						ids := readExportOwnerIDs(t, f.store.ReadDB(), sql, owner.id)
						t.Logf("selector=%s owner=%s mode=%s elapsed=%s rows=%d", query.name, owner.name, mode, time.Since(start), len(ids))
						if mode == "previous-first" {
							expected = ids
						} else if len(ids) != len(expected) || len(ids) <= 10000 && !reflect.DeepEqual(ids, expected) {
							t.Fatal("owner selection or order changed", query.name, owner.name)
						}
						// Beyond 10,000, the export rejects the entire collection.
						// Its unreturned sentinel subset need not retain ID order.
						wantDense := min(count, 10001)
						if owner.name == "sparse" && len(ids) != 1 || owner.name == "empty" && len(ids) != 0 || owner.name == "dense" && len(ids) != wantDense {
							t.Fatal("unexpected bounded owner result", query.name, owner.name, len(ids))
						}
					}
				}
				t.Logf("selector=%s previous_plan=%s", query.name, riskEdgesPlan(t, f.store.ReadDB(), query.previous, sparse, 10001))
				t.Logf("selector=%s selected_plan=%s", query.name, riskEdgesPlan(t, f.store.ReadDB(), query.selected, sparse, 10001))
			}
		})
	}
}
