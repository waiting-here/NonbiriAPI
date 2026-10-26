package db

import (
	"database/sql"
	"testing"
)

func restoreSchema36LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	dropCharityModelIndexesForTest(t, database)
	for _, table := range []string{"ai_agent_plans", "ai_agent_model_results", "ai_agent_review_events", "ai_agent_jobs", "ai_agent_messages", "ai_agent_conversations", "ai_agent_memory_history", "ai_agent_memories", "ai_agent_settings", "agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
}
