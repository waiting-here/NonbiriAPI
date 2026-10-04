package idempotency

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestRecoveryCoveringAccessPathAndPopulatedBatch(t *testing.T) {
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	const decisionNow int64 = 1_800_000_000
	// Keep a substantial live replay tail without reproducing large response
	// storage. Query-plan assertions guard against reading its response pages.
	_, err = database.Exec(`WITH RECURSIVE records(n) AS (
 VALUES(1) UNION ALL SELECT n+1 FROM records WHERE n<25485
) INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,
 state,http_status,response_body,created_at,expires_at)
 SELECT 'control_mutation',zeroblob(32),CAST(printf('%032d',n) AS BLOB),zeroblob(32),
 CASE WHEN n>25470 THEN 'accepted' ELSE 'completed' END,
 CASE WHEN n>25470 THEN 0 ELSE 200 END,
 CASE WHEN n>25470 THEN zeroblob(0) ELSE zeroblob(1024) END,
 CASE WHEN n<=110 THEN ?-86400 ELSE ?+1-86400 END,
 CASE WHEN n<=110 THEN ? ELSE ?+1 END FROM records`, decisionNow, decisionNow, decisionNow, decisionNow)
	if err != nil {
		t.Fatal(err)
	}
	plan := recoveryQueryPlan(t, database, recoveryBatchSelector, decisionNow, 100)
	if strings.Count(plan, "SEARCH idempotency_records USING COVERING INDEX idx_idempotency_recovery") != 2 ||
		!strings.Contains(plan, "MERGE (UNION ALL)") || strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "SCAN idempotency_records") {
		t.Fatalf("unbounded recovery selector plan:\n%s", plan)
	}
	morePlan := recoveryQueryPlan(t, database, recoveryRemainingQuery, decisionNow, decisionNow)
	for _, index := range []string{"idx_idempotency_recovery", "idx_idempotency_expiry", "idx_resource_operation_status_expiry"} {
		if !strings.Contains(morePlan, "USING COVERING INDEX "+index) {
			t.Fatalf("remaining-work query does not use %s:\n%s", index, morePlan)
		}
	}
	if strings.Contains(morePlan, "SCAN idempotency_records") || strings.Contains(morePlan, "SCAN resource_operation_status") {
		t.Fatalf("remaining-work query scans replay tables:\n%s", morePlan)
	}
	want := recoverySelectedRows(t, database, `SELECT rowid FROM idempotency_records
 WHERE state='accepted' OR expires_at<=?
 ORDER BY CASE WHEN state='accepted' THEN 0 ELSE 1 END,expires_at,scope,actor_scope_hash,key_hash LIMIT ?`, decisionNow, 100)
	got := recoverySelectedRows(t, database, recoveryBatchSelector, decisionNow, 100)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("covering selector changed batch membership or order")
	}
	maintenance := NewMaintenance(database)
	for _, expected := range []MaintenanceResult{{Processed: 100, More: true}, {Processed: 25}, {}} {
		// Exercise the existing lifecycle budget; setup and plan inspection are
		// outside the batch and have no elapsed-time assertion.
		result, err := maintenance.Recover(context.Background(), decisionNow, 100, time.Now().Add(2*time.Second))
		if err != nil || result != expected {
			t.Fatalf("populated recovery = (%+v,%v), want %+v", result, err, expected)
		}
	}
	var remaining int
	if err := database.QueryRow(`SELECT count(*) FROM idempotency_records WHERE state='completed' AND expires_at>?`, decisionNow).Scan(&remaining); err != nil || remaining != 25360 {
		t.Fatalf("live replay tail = %d, %v", remaining, err)
	}
	t.Logf("recovery selector plan:\n%s\nremaining-work plan:\n%s", plan, morePlan)
}

func recoveryQueryPlan(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

func recoverySelectedRows(t *testing.T, database *sql.DB, query string, args ...any) []int64 {
	t.Helper()
	rows, err := database.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var selected []int64
	for rows.Next() {
		var row int64
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		selected = append(selected, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return selected
}
