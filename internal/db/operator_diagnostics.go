package db

import "strings"

// Published driver DDL stays immutable for earlier upgrade steps. Fresh
// databases create the current table directly without a rename/rebuild cycle.
var CurrentAgentDriversSQL = strings.Replace(AgentDriversSQL,
	"provider IN ('chat_completions','responses','chatgpt')",
	"provider IN ('chat_completions','responses','chatgpt','codex')", 1)

const AgentRequestDriversSQL = `CREATE TABLE agent_request_drivers (
 request_id INTEGER PRIMARY KEY REFERENCES agent_requests(id) ON DELETE CASCADE,
 driver_id INTEGER NOT NULL CHECK(driver_id>=0),
 provider TEXT NOT NULL CHECK(provider IN ('chat_completions','responses','chatgpt','codex')),
 settings_json TEXT NOT NULL CHECK(json_valid(settings_json) AND length(CAST(settings_json AS BLOB))<=65536)
) STRICT;
`

// OperatorDiagnosticsSQL adds only new diagnostic and audit storage. The
// offline upgrader separately rebuilds existing driver snapshots by name.
var OperatorDiagnosticsSQL = CodexSessionSQL + RequestDiagnosticsSQL + ManagementDetailsSQL
