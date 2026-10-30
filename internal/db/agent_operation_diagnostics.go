package db

import _ "embed"

// AgentOperationDiagnosticsSQL stores bounded administrator-only operation errors.
//
//go:embed agent_operation_diagnostics.sql
var AgentOperationDiagnosticsSQL string

// OperatorFollowupSQL is additive to the published schema42 layout.
var OperatorFollowupSQL = AgentOperationDiagnosticsSQL + "\n" + ManagementServiceHealthSQL + "\n" + ReadPerformanceIndexesSQL
