package donationquota

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Keep the selected candidate count fixed while changing retained quota history
// and the number of physical keys represented by those candidates.
func BenchmarkBrowseSlidingWindow(b *testing.B) {
	for _, buckets := range []int{1000, 10000} {
		for _, keys := range []int{1, 16} {
			b.Run(fmt.Sprintf("buckets=%d/keys=%d", buckets, keys), func(b *testing.B) {
				q, now := newBrowseWindowFixture(b, buckets, keys)
				for _, offset := range []struct {
					name    string
					seconds int64
				}{{"current", 0}, {"stale_1s", 1}, {"stale_60s", 60}, {"disjoint", 2 * 86400}} {
					b.Run(offset.name, func(b *testing.B) {
						tx, err := q.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
						if err != nil {
							b.Fatal(err)
						}
						defer tx.Rollback()
						query := `WITH RECURSIVE candidates(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM candidates WHERE n<63)
SELECT n,` + AvailabilityPredicate("1+(c.n % ?1)", "?2", "0", "0") + ` FROM candidates c`
						read := func() {
							rows, err := tx.QueryContext(b.Context(), query, keys, now+offset.seconds)
							if err != nil {
								b.Fatal(err)
							}
							count := 0
							for rows.Next() {
								var ordinal int
								var available bool
								if err := rows.Scan(&ordinal, &available); err != nil {
									b.Fatal(err)
								}
								if ordinal != count || !available {
									b.Fatalf("candidate %d available=%v", ordinal, available)
								}
								count++
							}
							if err := rows.Err(); err != nil {
								b.Fatal(err)
							}
							if err := rows.Close(); err != nil {
								b.Fatal(err)
							}
							if count != 64 {
								b.Fatalf("candidate count %d", count)
							}
						}
						read()
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							read()
						}
					})
				}
			})
		}
	}
}

func newBrowseWindowFixture(t testing.TB, buckets, keys int, spread ...bool) (*sql.DB, int64) {
	t.Helper()
	q := newQuotaDB(t)
	now := testNow + int64(buckets) + 1
	step := int64(1)
	if len(spread) > 0 && spread[0] {
		now = testNow + 86400
		step = (86400 - 1) / int64(buckets)
	}
	left, err := calendar.Subtract(now, "day", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := q.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r := rule("calls", fmt.Sprint(buckets+100))
	r.Mode, r.Interval, r.Alignment = "sliding", "day", nil
	for key := 1; key <= keys; key++ {
		if key > 1 {
			_, err := tx.Exec(`INSERT INTO donation_keys(id,donation_id,source_endpoint_key_id,
price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,
ended_at,ended_reason,report_match_until,report_fingerprint)
SELECT ?,donation_id,?,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,
ended_at,ended_reason,report_match_until,report_fingerprint FROM donation_keys WHERE id=1`, key, key)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := Replace(context.Background(), tx, int64(key), testNow, []RuleInput{r}); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := tx.QueryRow(`SELECT id FROM donation_quota_rules WHERE donation_key_id=? AND current_epoch IS NOT NULL`, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(`WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<?)
INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag)
SELECT ?,1,?+1+(n-1)*?, ?,zeroblob(16) FROM samples`, buckets, id, testNow, step, db.EncodeU128(mag(1)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE donation_quota_epochs SET last_observed_at=?,window_left=?,window_at=?,window_used=?,window_reserved=zeroblob(16) WHERE rule_id=? AND epoch=1`,
			now, left, now, db.EncodeU128(mag(int64(buckets))), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE donation_quota_capacity SET rows_used=? WHERE id=1`, buckets*keys); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return q, now
}

func BenchmarkBrowseSlidingWindowMovingEdges(b *testing.B) {
	for _, buckets := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("buckets=%d", buckets), func(b *testing.B) {
			q, now := newBrowseWindowFixture(b, buckets, 1, true)
			for _, offset := range []struct {
				name    string
				seconds int64
			}{{"stale_1s", 1}, {"stale_60s", 60}, {"mostly_expired", 86400 - 60}} {
				b.Run(offset.name, func(b *testing.B) {
					tx, err := q.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
					if err != nil {
						b.Fatal(err)
					}
					defer tx.Rollback()
					query := `WITH RECURSIVE candidates(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM candidates WHERE n<63)
SELECT n,` + AvailabilityPredicate("1+(c.n % ?1)", "?2", "0", "0") + ` FROM candidates c`
					read := func() {
						rows, err := tx.QueryContext(b.Context(), query, 1, now+offset.seconds)
						if err != nil {
							b.Fatal(err)
						}
						count := 0
						for rows.Next() {
							var ordinal int
							var available bool
							if err := rows.Scan(&ordinal, &available); err != nil {
								b.Fatal(err)
							}
							if ordinal != count || !available {
								b.Fatalf("candidate %d available=%v", ordinal, available)
							}
							count++
						}
						if err := rows.Err(); err != nil {
							b.Fatal(err)
						}
						if err := rows.Close(); err != nil {
							b.Fatal(err)
						}
						if count != 64 {
							b.Fatalf("candidate count %d", count)
						}
					}
					read()
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						read()
					}
				})
			}
		})
	}
}
