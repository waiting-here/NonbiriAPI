package v1

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// The original columns are captured before conversion. Relocated fields still
// participate in the comparison through their exact compatibility projection.
func connectionFactQuery(table string, sourceColumns map[string][]string) (string, bool) {
	if sourceColumns == nil {
		if table == "model_discovery_evidence" {
			// A missing legacy row already means unknown at revision 1. The new
			// read-only view exposes that same default for every existing key.
			// Completed legacy results may lack a start; their new run uses the
			// existing completion time as the conservative start boundary.
			return `SELECT k.id AS endpoint_key_id,COALESCE(d.state,'unknown') AS state,
 COALESCE(d.revision,1) AS revision,d.operation_hash,COALESCE(d.safe_class,'none') AS safe_class,
 COALESCE(d.safe_diag,'') AS safe_diag,COALESCE(d.started_at,d.completed_at) AS started_at,d.completed_at,COALESCE(d.fetched_count,0) AS fetched_count
 FROM endpoint_keys k LEFT JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id`, true
		}
		return "SELECT * FROM " + quoteSQLiteIdentifier(table), true
	}
	names, exists := sourceColumns[table]
	if !exists {
		switch table {
		// These are populated relationship tables, not supposedly empty
		// additions. Their permissions and identity mapping have a dedicated
		// populated-source migration test.
		case "service_connections", "credentials", "credential_versions", "endpoint_key_grant_revisions",
			"endpoint_key_discovery_sources", "discovery_sources", "discovery_source_revisions",
			"discovery_source_states", "discovery_runs", "donation_uses", "donation_use_revisions",
			"model_binding_targets", "charity_binding_targets", "dispatch_claim_targets",
			"mainstream_channel_entries":
			return "", false
		}
		return "SELECT * FROM " + quoteSQLiteIdentifier(table), true
	}
	from := quoteSQLiteIdentifier(table) + " b"
	switch table {
	case "endpoint_keys":
		from = "endpoint_key_current_credentials b"
	case "model_catalog_entries":
		// Compare every retained run, including stale catalog generations.
		from = "model_catalog_entries b LEFT JOIN discovery_runs r ON r.id=b.discovery_run_id LEFT JOIN endpoint_key_discovery_sources ks ON ks.source_revision_id=r.source_revision_id"
	case "model_bindings":
		from = "model_bindings b JOIN model_binding_primary_targets t ON t.binding_id=b.id"
	case "charity_model_bindings":
		from = "charity_model_bindings b JOIN charity_binding_primary_targets t ON t.binding_id=b.id JOIN donation_keys dk ON dk.id=t.donation_key_id"
	case "mainstream_channels":
		from = "mainstream_channels b JOIN mainstream_channel_primary_entries t ON t.channel_id=b.id"
	}
	selected := make([]string, len(names))
	for i, name := range names {
		if table == "model_catalog_entries" && name == "endpoint_key_id" {
			selected[i] = "COALESCE(b.endpoint_key_id,ks.endpoint_key_id) AS endpoint_key_id"
			continue
		}
		alias := "b"
		if table == "model_bindings" && (name == "endpoint_key_id" || name == "upstream_model_id") ||
			table == "charity_model_bindings" && (name == "donation_key_id" || name == "upstream_model_id") ||
			table == "mainstream_channels" && (name == "connector_type" || name == "canonical_base_url") {
			alias = "t"
		}
		if table == "charity_model_bindings" && name == "endpoint_key_id" {
			alias = "dk"
		}
		selected[i] = alias + "." + quoteSQLiteIdentifier(name)
	}
	return "SELECT " + strings.Join(selected, ",") + " FROM " + from, true
}

func assertExactPublishedSource(t *testing.T, database *sql.DB, version int, manifest string, rows map[string][]string) {
	t.Helper()
	gotVersion, versionErr := readSchemaVersion(t.Context(), database)
	gotManifest, manifestErr := db.SchemaManifestHash(t.Context(), database)
	if versionErr != nil || manifestErr != nil || gotVersion != version || gotManifest != manifest {
		t.Fatalf("source identity changed: version=%d manifest=%s errors=%v/%v", gotVersion, gotManifest, versionErr, manifestErr)
	}
	if !reflect.DeepEqual(gatewayRows(t, database), rows) {
		t.Fatal("rollback changed original rows or identity high-water marks")
	}
	plan, err := storageSchema.plan(t.Context(), database)
	if err != nil || len(plan.steps) == 0 || plan.steps[0].from != version || plan.target.version != db.CurrentSchemaVersion {
		t.Fatalf("restored source has no published conversion path: %+v, %v", plan, err)
	}
	for _, step := range plan.steps {
		if step.from > 26 && step.from != 33 && step.from != 34 || step.version > 26 && step.version != 33 && step.version != 34 && step.version != 35 {
			t.Fatalf("unreleased intermediate schema became a supported source: %+v", step)
		}
	}
}
