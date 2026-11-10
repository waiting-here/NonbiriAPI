package db

import (
	"database/sql"
	"strings"
	"testing"
)

// Restore retained source objects before removing columns introduced by the
// current schema, keeping historical identities independent of current DDL.
func restoreSchema50LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	definition := func(source, prefix, next string) string {
		t.Helper()
		start := strings.Index(source, prefix)
		if start < 0 {
			t.Fatal("missing retained definition", prefix)
		}
		end := strings.Index(source[start:], next)
		if end < 0 {
			t.Fatal("missing retained boundary", prefix)
		}
		return source[start : start+end]
	}
	for _, item := range []struct{ kind, name string }{
		{"VIEW", "endpoint_grant_authorizations"},
		{"TRIGGER", "donation_use_pointer_revision_guard"},
		{"TRIGGER", "donation_use_revision_identity_guard"},
	} {
		ddl := definition(generationTwoBaseSchema, "CREATE "+item.kind+" "+item.name+" ", "\nCREATE ")
		hostileMustExec(t, database, "DROP "+item.kind+" "+item.name+";"+ddl)
	}
	for _, body := range strings.Split(ManagementDetailsSQL, "\nCREATE TRIGGER ")[1:] {
		name := strings.Fields(body)[0]
		if strings.HasPrefix(name, "management_capture_steward_permissions_") || name == "management_capture_donation_uses_update" {
			hostileMustExec(t, database, "DROP TRIGGER "+name+"; CREATE TRIGGER "+body)
		}
	}
	for _, item := range []struct{ name, next string }{
		{"request_adaptation_audits", "client_rule_auto_bans"},
		{"dispatch_caller_parameter_errors", "service_connections"},
	} {
		ddl := definition(generationTwoBaseSchema, "CREATE TABLE "+item.name+" (", "\nCREATE TABLE "+item.next+" (")
		hostileMustExec(t, database, "CREATE TABLE retained_audit_copy AS SELECT * FROM "+item.name+"; DROP TABLE "+item.name+";"+ddl+" INSERT INTO "+item.name+" SELECT * FROM retained_audit_copy; DROP TABLE retained_audit_copy;")
	}
	hostileMustExec(t, database, `ALTER TABLE dispatch_claim_targets DROP COLUMN rerank_path;
ALTER TABLE donation_uses DROP COLUMN rerank_path_override;
ALTER TABLE donation_use_revisions DROP COLUMN rerank_path;
ALTER TABLE endpoints DROP COLUMN rerank_path;
ALTER TABLE endpoint_key_limits DROP COLUMN concurrency_overflow;
ALTER TABLE endpoint_key_limits DROP COLUMN rpm_overflow;
ALTER TABLE endpoint_key_limits DROP COLUMN concurrency_queue_limit;
ALTER TABLE endpoint_key_limits DROP COLUMN rpm_queue_limit;`)
	assertRetainedManifest(t, database, Schema50ManifestHash)
}
