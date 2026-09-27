package db

import (
	"context"
	"testing"
)

func TestUsageDiscrepancyUpgradePreservesHistoryAndBoundsMarkers(t *testing.T) {
	database := interactionSourceFixture(t)
	user := hostileInsertUser(t, database, "usage", 0, 1)
	request := hostileOID("req_")
	hostileInsertLogicalRequest(t, database, request, user, "openai_chat_completions", 1)
	hostileMustExec(t, database, `UPDATE credit_capacity SET reserved_future_rows=? WHERE id=1`, hostileBlob16(1))
	log := hostileInsertRequestLog(t, database, request, user, "openai_chat_completions")
	hostileMustExec(t, database, `INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,started_at,completed_at) VALUES(?,?,1,'openai-compatible','https://example.test','model','synthetic',1,1)`, hostileOID("clm_"), log)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"request_logs", "request_attempts"} {
		var value int
		if err := database.QueryRow(`SELECT usage_total_mismatch FROM ` + table).Scan(&value); err != nil || value != 0 {
			t.Fatal("upgrade invented an observed mismatch", table, value, err)
		}
		for _, invalid := range []any{-1, 2, 0.5, "invalid", nil} {
			hostileMustFail(t, database, `UPDATE `+table+` SET usage_total_mismatch=?`, invalid)
		}
		hostileMustExec(t, database, `UPDATE `+table+` SET usage_total_mismatch=1`)
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"request_logs", "request_attempts"} {
		var value int
		if err := database.QueryRow(`SELECT usage_total_mismatch FROM ` + table).Scan(&value); err != nil || value != 1 {
			t.Fatal("reentry lost a recorded mismatch", table, value, err)
		}
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
}
