package db

import (
	"context"
	"strings"
	"testing"
)

func TestInteractionScanReasonsAndAccessPageIndex(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	queryPlan := func() string {
		t.Helper()
		rows, err := database.Query(`EXPLAIN QUERY PLAN SELECT id FROM audit_access_events WHERE occurred_at>=? AND occurred_at<? ORDER BY occurred_at DESC,id DESC LIMIT 20`, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(details, ";")
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if after := queryPlan(); !strings.Contains(after, "idx_audit_access_time_page") || strings.Contains(after, "USE TEMP B-TREE") {
		t.Fatalf("unindexed access page: %s", after)
	}
	user := hostileInsertUser(t, database, "scan reasons", 1, 1)
	scan := hostileOID("scn_")
	hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','completed',0,100,'total','',0,0,0,0,0,86400)`, scan, user)
	for _, reason := range []string{"result_limit", "permission_changed", "scan_failed", "candidate_limit", "minute_limit", "source_changed"} {
		hostileMustExec(t, database, `UPDATE risk_client_scans SET reason=? WHERE id=?`, reason, scan)
	}
	if _, err := database.Exec(`UPDATE risk_client_scans SET reason='unrecognized' WHERE id=?`, scan); err == nil {
		t.Fatal("unknown scan reason accepted")
	}
}
