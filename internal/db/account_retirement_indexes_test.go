package db

import (
	"strings"
	"testing"
)

func TestAccountRetirementLookupsUseOwnerIndexes(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	planFor := func(query string, args ...any) string {
		t.Helper()
		rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
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
		return strings.Join(details, "\n")
	}
	// Foreign-key actions must find this account's history without scanning
	// unrelated requests, ledger operations, receipts or completed games.
	plan := planFor(`DELETE FROM users WHERE id=?`, 1)
	for _, index := range []string{
		"idx_accepted_operations_actor", "idx_dispatch_claims_receiver",
		"idx_credit_operations_actor", "idx_credit_operations_donation_user",
		"idx_charity_dispatch_receipts_user", "idx_resource_operation_status_user",
		"idx_lake_notes_cast_user", "idx_rps_summary_seats_user",
	} {
		if !strings.Contains(plan, index) {
			t.Fatalf("account retirement does not use %s:\n%s", index, plan)
		}
	}
	// Deidentification repeats this update for each historical session. A
	// global pending-results scan here multiplies two independent workloads.
	plan = planFor(`UPDATE game_rps_pending_results SET seat0_result='deidentified'
WHERE session_id_text=? AND user_id<>? AND seat0_result<>'deidentified'`, "rps_"+strings.Repeat("A", 22), 1)
	if !strings.Contains(plan, "idx_rps_pending_results_session") || strings.Contains(plan, "SCAN ") {
		t.Fatalf("pending result deidentification scans unrelated sessions:\n%s", plan)
	}
	plan = planFor(`SELECT own.session_id,own.seat_no,peer.user_id
FROM game_rps_summary_seats own JOIN game_rps_summary_seats peer ON peer.session_id=own.session_id
WHERE own.user_id=? ORDER BY own.session_id,peer.seat_no`, 1)
	if !strings.Contains(plan, "idx_rps_summary_seats_user") || strings.Contains(plan, "SCAN ") {
		t.Fatalf("history deidentification scans unrelated accounts:\n%s", plan)
	}
}
