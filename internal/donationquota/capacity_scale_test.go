package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestPhysicalCapacityBoundaryAndBoundedSettlement(t *testing.T) {
	if os.Getenv("NONBIRI_QUOTA_SCALE") != "1" {
		t.Skip("set NONBIRI_QUOTA_SCALE=1 to allocate the full persistent row budget")
	}
	q := newQuotaDB(t)
	r := rule("calls", "100000000")
	r.Mode, r.Interval, r.Alignment = "sliding", "month", nil
	const ruleCount = 5
	rules := replace(t, q, testNow, r, r, r, r, r)
	const perRule = (MaxRows - 2*ruleCount) / ruleCount
	const now = testNow + perRule + 1
	left, err := calendar.Subtract(now, "month", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	seedStarted := time.Now()
	err = transaction(t, q, func(tx *sql.Tx) error {
		for _, v := range rules {
			for first := 1; first <= perRule; first += 100_000 {
				last := min(first+99_999, perRule)
				_, err := tx.Exec(`WITH RECURSIVE seconds(n) AS (
SELECT ? UNION ALL SELECT n+1 FROM seconds WHERE n<?)
INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag)
SELECT ?,1,?+n,?,zeroblob(16) FROM seconds`, first, last, *v.ID, testNow, db.EncodeU128(mag(1)))
				if err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`UPDATE donation_quota_epochs SET last_observed_at=?,window_left=?,window_at=?,window_used=? WHERE rule_id=? AND epoch=1`, now, left, now, db.EncodeU128(mag(perRule)), *v.ID); err != nil {
				return err
			}
			t.Logf("seeded %d physical buckets for a rule in %s", perRule, time.Since(seedStarted))
		}
		_, err := tx.Exec(`UPDATE donation_quota_capacity SET rows_used=(SELECT COUNT(*) FROM donation_quota_buckets) WHERE id=1`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	capacity := func(wantUsed, wantHeld int) {
		t.Helper()
		var used, held, rows int
		if err := q.QueryRow(`SELECT rows_used,rows_held,(SELECT COUNT(*) FROM donation_quota_buckets)+(SELECT COUNT(*) FROM donation_quota_periods) FROM donation_quota_capacity WHERE id=1`).Scan(&used, &held, &rows); err != nil || used != wantUsed || held != wantHeld || used != rows {
			t.Fatalf("capacity used=%d held=%d physical=%d; want %d/%d: %v", used, held, rows, wantUsed, wantHeld, err)
		}
	}
	validate := func(label string) {
		t.Helper()
		started := time.Now()
		if err := transaction(t, q, func(tx *sql.Tx) error { return ValidateState(context.Background(), tx) }); err != nil {
			t.Fatal(label, err)
		}
		t.Logf("%s persistent validation: %s", label, time.Since(started))
	}
	capacity(MaxRows-2*ruleCount, 0)
	validate("seeded")
	requireIndexedCleanup(t, q, now)
	readStarted := time.Now()
	for i := 0; i < 100; i++ {
		values, err := Views(context.Background(), q, 1, now+1)
		if err != nil || len(values) != ruleCount {
			t.Fatal(values, err)
		}
		for _, value := range values {
			requireUsage(t, value, "999998", "0", "99000002", "available")
		}
	}
	t.Logf("100 complete current-rule reads near capacity: %s", time.Since(readStarted))
	plan, err := q.Query(`EXPLAIN QUERY PLAN SELECT used_mag,reserved_mag FROM donation_quota_buckets WHERE rule_id=? AND epoch=? AND success_at>? AND success_at<=?`, *rules[0].ID, 1, now, now+1)
	if err != nil {
		t.Fatal(err)
	}
	foundIndex := false
	for plan.Next() {
		var id, parent, auxiliary int
		var detail string
		if err := plan.Scan(&id, &parent, &auxiliary, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log("window plan:", detail)
		foundIndex = foundIndex || (strings.Contains(detail, "SEARCH donation_quota_buckets") && strings.Contains(detail, "success_at>?") && strings.Contains(detail, "success_at<?"))
	}
	err = plan.Err()
	plan.Close()
	if err != nil || !foundIndex {
		t.Fatal("window query must use the bounded composite index", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Views(cancelled, q, 1, now); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read", err)
	}
	a := Amounts{Calls: mag(1)}
	claims := make([]string, 2)
	for i := range claims {
		claims[i], err = newClaim(t, q, now, a)
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatch(t, q, claims[i], now); err != nil {
			t.Fatal(err)
		}
	}
	capacity(MaxRows-2*ruleCount, 2*ruleCount)
	rejectStarted := time.Now()
	if _, err := newClaim(t, q, now, a); !errors.Is(err, ErrCapacity) {
		t.Fatal("new reservation at the physical plus held boundary", err)
	}
	t.Logf("capacity rejection and attempted cleanup: %s", time.Since(rejectStarted))
	var claimCount int
	if err := q.QueryRow(`SELECT COUNT(*) FROM dispatch_claims`).Scan(&claimCount); err != nil || claimCount != 2 {
		t.Fatal("failed admission left partial claims", claimCount, err)
	}
	capacity(MaxRows-2*ruleCount, 2*ruleCount)
	settleStarted := time.Now()
	for _, id := range claims {
		start(t, q, id, now+1)
	}
	for _, id := range claims {
		terminal(t, q, id, now+2, a, true)
	}
	t.Logf("two previously dispatched settlements at full capacity: %s", time.Since(settleStarted))
	capacity(MaxRows-ruleCount, 0)
	validate("settled")
	if err := transaction(t, q, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE donation_quota_capacity SET rows_used=rows_used+1 WHERE id=1`); err != nil {
			return err
		}
		return ValidateState(context.Background(), tx)
	}); !errors.Is(err, ErrInvariant) {
		t.Fatal("corrupt counter did not fail closed and roll back", err)
	}
	capacity(MaxRows-ruleCount, 0)
	cleanupStarted := time.Now()
	var removed int
	err = transaction(t, q, func(tx *sql.Tx) error {
		var err error
		removed, err = Cleanup(context.Background(), tx, now+calendar.MaxLookbackSeconds+2, CleanupBatch)
		return err
	})
	if err != nil || removed != CleanupBatch {
		t.Fatal("bounded cleanup", removed, err)
	}
	t.Logf("cleanup removed exactly %d rows: %s", removed, time.Since(cleanupStarted))
	// Ten terminal receipts occupy the first ten positions of this cleanup;
	// only the remaining deleted aggregate rows release physical capacity.
	capacity(MaxRows-ruleCount-(CleanupBatch-2*ruleCount), 0)
	validate("after bounded cleanup")
	validate("repeated startup validation")
	var filename string
	var sequence int
	var name string
	if err := q.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &filename); err == nil {
		if info, err := os.Stat(filename); err == nil {
			t.Logf("database bytes=%d, total elapsed=%s", info.Size(), time.Since(seedStarted))
		}
	}
}
