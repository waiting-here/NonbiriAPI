package db

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func restoreSchema41LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type='trigger' AND name GLOB 'management_capture_*' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var ddl strings.Builder
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(&ddl, "DROP TRIGGER %q;\n", name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_codex_session", "request_attempt_diagnostics", "request_delivery_facts", "agent_request_diagnostics", "request_error_diagnostics", "management_operation_terms", "management_capture_rows", "management_capture_totals", "management_capture_context"} {
		fmt.Fprintf(&ddl, "DROP TABLE %q;\n", table)
	}
	ddl.WriteString(`DROP INDEX management_operation_items_name;
ALTER TABLE management_operations DROP COLUMN target_name;
ALTER TABLE management_operation_items DROP COLUMN details;
DROP TABLE agent_request_drivers;
`)
	start, end := strings.Index(AgentDriversSQL, "CREATE TABLE agent_request_drivers ("), strings.Index(AgentDriversSQL, "CREATE TABLE agent_chatgpt_session (")
	if start < 0 || end <= start {
		t.Fatal("published driver schema is missing")
	}
	ddl.WriteString(AgentDriversSQL[start:end])
	hostileMustExec(t, database, ddl.String())
	assertRetainedManifest(t, database, Schema41ManifestHash)
}

func TestOperatorDiagnosticsSchema41Layout(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	restoreSchema41LayoutForTest(t, database)
}
