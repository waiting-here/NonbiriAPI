package donationquota

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func requireIndexedCleanup(t *testing.T, q *sql.DB, now int64) {
	t.Helper()
	for _, kind := range []string{"period", "bucket"} {
		for _, selection := range cleanupAggregateQueries(kind, now) {
			rows, err := q.Query(`EXPLAIN QUERY PLAN `+selection.sql, append(selection.args, CleanupBatch)...)
			if err != nil {
				t.Fatal(err)
			}
			aggregateRange := false
			for rows.Next() {
				var id, parent, auxiliary int
				var detail string
				if err := rows.Scan(&id, &parent, &auxiliary, &detail); err != nil {
					t.Fatal(err)
				}
				t.Log(kind, "cleanup plan:", detail)
				if strings.Contains(detail, "SCAN a") || strings.Contains(detail, "USE TEMP B-TREE") {
					t.Fatal("cleanup scans or sorts the entire aggregate set:", detail)
				}
				aggregateRange = aggregateRange || strings.Contains(detail, "SEARCH a USING")
			}
			err = rows.Err()
			rows.Close()
			if err != nil || !aggregateRange {
				t.Fatal("cleanup must seek aggregate candidates through an index", err)
			}
		}
	}
}

func TestCleanupUsesTimeAndRetirementIndexes(t *testing.T) {
	q := newQuotaDB(t)
	requireIndexedCleanup(t, q, testNow)
}

func TestCleanupHonorsObservedExpiryDuringClockRollback(t *testing.T) {
	q := newQuotaDB(t)
	replace(t, q, testNow, rule("calls", "5"))
	a := Amounts{Calls: mag(1)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, testNow)
	terminal(t, q, id, testNow+1, a, true)
	// Observe the expired period without collecting it, then roll back the
	// process clock. Cleanup must still collect it using the persisted clock.
	observedAt := testNow + 5*3600 + 1
	if err := transaction(t, q, func(tx *sql.Tx) error {
		e, err := currentEpochs(context.Background(), tx, 1)
		if err != nil || len(e) != 1 {
			t.Fatal(e, err)
		}
		return advance(context.Background(), tx, &e[0], observedAt)
	}); err != nil {
		t.Fatal(err)
	}
	var removed int
	if err := transaction(t, q, func(tx *sql.Tx) error {
		var err error
		removed, err = Cleanup(context.Background(), tx, testNow+2, 10)
		return err
	}); err != nil || removed != 2 {
		t.Fatal("expected one terminal receipt and the observed-expired period", removed, err)
	}
	views(t, q, testNow+2)
}

func TestDisjointWindowRetainsPendingReservationAndLateSettlement(t *testing.T) {
	q := newQuotaDB(t)
	r := rule("calls", "5")
	r.Mode, r.Interval, r.Alignment = "sliding", "day", nil
	replace(t, q, testNow, r)
	a := Amounts{Calls: mag(1)}
	old, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old, pending} {
		if err := dispatch(t, q, id, testNow); err != nil {
			t.Fatal(err)
		}
	}
	start(t, q, old, testNow)
	for _, at := range []int64{testNow + 86400, testNow + 86401} {
		requireUsage(t, views(t, q, at)[0], "0", "1", "4", "available")
	}
	later := testNow + 86402
	terminal(t, q, old, later, a, true)
	requireUsage(t, views(t, q, later)[0], "0", "1", "4", "available")
	start(t, q, pending, later)
	terminal(t, q, pending, later, a, true)
	requireUsage(t, views(t, q, later)[0], "1", "0", "4", "available")
	// The expired fact is retained until normal cleanup, although it no
	// longer occupies the current window or affects the later receipt.
	var count int
	if err := q.QueryRow(`SELECT COUNT(*) FROM donation_quota_buckets`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
