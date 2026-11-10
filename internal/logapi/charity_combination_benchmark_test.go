package logapi

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Each pair includes the bounded model lookup, exact count and consumed page.
func benchmarkCombinedCharitySearch(b *testing.B, database *sql.DB, filter ListFilter) {
	var expectedCount string
	var expectedIDs []int64
	for _, optimized := range []bool{false, true} {
		b.Run(fmt.Sprintf("lookup=%v", optimized), func(b *testing.B) {
			var modelsTime, countTime, rowsTime time.Duration
			var countQuery, pageQuery string
			var countArgs, selectionArgs []any
			for b.Loop() {
				tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
				if err != nil {
					b.Fatal(err)
				}
				var namesReader logReadQueryer = database
				if optimized {
					namesReader = tx
				}
				started := time.Now()
				predicate, args, err := charityModelFilter(context.Background(), namesReader, filter)
				if err != nil {
					tx.Rollback()
					b.Fatal(err)
				}
				modelsTime += time.Since(started)
				where := "WHERE (l.completed_at IS NULL OR l.completed_at>?) AND l.caller_status=?" + predicate
				args = append([]any{int64(1_799_000_000), *filter.Status}, args...)
				if filter.From != nil {
					where += " AND l.started_at>=?"
					args = append(args, *filter.From)
				}
				if filter.To != nil {
					where += " AND l.started_at<?"
					args = append(args, *filter.To)
				}
				started = time.Now()
				query, pageArgs, metadata, err := logListPageQuery(context.Background(), tx, "l.id", "", where, args, &pagination.Request{Page: 1, Size: 50}, 50)
				if err != nil {
					tx.Rollback()
					b.Fatal(err)
				}
				countTime += time.Since(started)
				started = time.Now()
				ids := queryLogIDs(b, tx, query, pageArgs...)
				rowsTime += time.Since(started)
				countQuery, pageQuery = "SELECT COUNT(*) FROM request_logs l "+where, query
				countArgs, selectionArgs = args, pageArgs
				if !optimized {
					expectedCount, expectedIDs = metadata.TotalItems, ids
				} else if metadata.TotalItems != expectedCount || !slices.Equal(ids, expectedIDs) {
					tx.Rollback()
					b.Fatal("combined search changed total or page", metadata, ids, expectedCount, expectedIDs)
				}
				if err := tx.Rollback(); err != nil {
					b.Fatal(err)
				}
			}
			b.Logf(`count=%s count_plan:
%s page_plan:
%s`, expectedCount, charityQueryPlan(b, database, countQuery, countArgs), charityQueryPlan(b, database, pageQuery, selectionArgs))
			b.ReportMetric(float64(modelsTime.Nanoseconds())/float64(b.N)/1e6, "models-ms/op")
			b.ReportMetric(float64(countTime.Nanoseconds())/float64(b.N)/1e6, "count-ms/op")
			b.ReportMetric(float64(rowsTime.Nanoseconds())/float64(b.N)/1e6, "rows-ms/op")
		})
	}
}

func BenchmarkCharityModelCombination(b *testing.B) {
	for _, count := range []int{160_000, 1_600_000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			database := indexedCharityQueryDatabase(b, count)
			var version string
			if err := database.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
				b.Fatal(err)
			}
			b.Logf("sqlite=%s rows=%d", version, count)
			if _, err := database.Exec("UPDATE request_logs SET caller_status=CASE WHEN ((id-1)/32)%100 IN (1,11) AND id%3<>0 THEN 400 ELSE 200 END; ANALYZE request_logs"); err != nil {
				b.Fatal(err)
			}
			status := 400
			for _, tc := range []struct {
				name, pattern string
				narrow        bool
			}{
				{"sparse", "model-001", false}, {"wide_time", "model-001", false}, {"narrow_time", "model-001", true},
				{"absent", "not-present", false}, {"broad", "provider/", false},
			} {
				b.Run(tc.name, func(b *testing.B) {
					filter := ListFilter{CharityModel: &tc.pattern, Status: &status}
					if tc.name == "wide_time" || tc.narrow {
						from, to := int64(1_800_000_000), int64(1_800_000_000+count/32)
						if tc.narrow {
							from = to - 100
						}
						filter.From, filter.To = &from, &to
					}
					benchmarkCombinedCharitySearch(b, database, filter)
				})
			}
			b.Run("operation_status", func(b *testing.B) {
				pattern := "model-001"
				benchmarkCombinedCharitySearch(b, database, ListFilter{Operation: "responses", CharityModel: &pattern, Status: &status})
			})
			if _, err := database.Exec("UPDATE request_logs SET model=CASE WHEN id%100<99 THEN 'dominant' ELSE printf('rare-%03d',(id/100)%10) END; ANALYZE request_logs"); err != nil {
				b.Fatal(err)
			}
			b.Run("dominant", func(b *testing.B) {
				pattern, status := "dominant", 200
				benchmarkCombinedCharitySearch(b, database, ListFilter{CharityModel: &pattern, Status: &status})
			})
			if _, err := database.Exec("UPDATE request_logs SET model=printf('historic-%07d',id); ANALYZE request_logs"); err != nil {
				b.Fatal(err)
			}
			b.Run("high_cardinality_narrow_time", func(b *testing.B) {
				pattern := "absent"
				from, to := int64(1_800_000_000+count/32-100), int64(1_800_000_000+count/32)
				benchmarkCombinedCharitySearch(b, database, ListFilter{CharityModel: &pattern, Status: &status, From: &from, To: &to})
			})
		})
	}
}

func BenchmarkCharityModelBroadMatches(b *testing.B) {
	for _, tc := range []struct{ rows, names int }{{160_000, 100}, {160_000, 4096}, {1_600_000, 100}} {
		b.Run(fmt.Sprintf("rows_%d/names_%d", tc.rows, tc.names), func(b *testing.B) {
			database := indexedCharityQueryDatabase(b, tc.rows)
			pattern := "provider/"
			if tc.names == 4096 {
				if _, err := database.Exec("UPDATE request_logs SET model=printf('historic-%04d',(id-1)%4096),route_kind='charity_chat_completions'"); err != nil {
					b.Fatal(err)
				}
				pattern = "historic-"
			}
			if _, err := database.Exec("UPDATE request_logs SET caller_status=CASE WHEN ((id-1)/32)%100 IN (1,11) AND id%3<>0 THEN 400 ELSE 200 END; ANALYZE request_logs"); err != nil {
				b.Fatal(err)
			}
			status := 400
			benchmarkCombinedCharitySearch(b, database, ListFilter{CharityModel: &pattern, Status: &status})
		})
	}
}
