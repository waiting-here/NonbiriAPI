package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func indexedCharityQueryDatabase(tb testing.TB, count int) *sql.DB {
	tb.Helper()
	database := newCharityQueryBenchmarkDatabase(tb, count)
	if _, err := database.Exec(db.CharityLogCoveringIndexSQL); err != nil {
		tb.Fatal(err)
	}
	return database
}

func queryLogIDs(tb testing.TB, reader logReadQueryer, query string, args ...any) []int64 {
	tb.Helper()
	rows, err := reader.QueryContext(context.Background(), query, args...)
	if err != nil {
		tb.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			tb.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	return ids
}

func TestCharityModelLookupMatchesHistoricalRowsAndPageBoundaries(t *testing.T) {
	database := indexedCharityQueryDatabase(t, 6400)
	// The lookup depends on historical log snapshots, without a current model
	// catalog. Empty names must not terminate a route's recursive traversal.
	for _, statement := range []string{
		"UPDATE request_logs SET model='' WHERE id<=32",
		"UPDATE request_logs SET model='模型/Ä/Case%_X' WHERE id BETWEEN 33 AND 64",
		"UPDATE request_logs SET model='模型/ä/case%_X' WHERE id BETWEEN 65 AND 96",
		"UPDATE request_logs SET completed_at=NULL WHERE id%97=0",
		"UPDATE request_logs SET completed_at=1 WHERE id%101=0",
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	plan := charityQueryPlan(t, database, charityModelNamesQuery, []any{"MODEL-001", maxCharityModelPairs + 1})
	if !strings.Contains(plan, "CO-ROUTINE names") ||
		!strings.Contains(plan, "idx_request_logs_charity_model (route_kind=? AND model>?)") {
		t.Fatal("distinct-name enumeration lost bounded index seeks", plan)
	}
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, pattern := range []string{"MODEL-001", "provider/", "not-present", "%_", "模型/Ä/case", "模型/ä/CASE", "model-00"} {
		t.Run(pattern, func(t *testing.T) {
			legacy, legacyArgs := charityQueryWhere(pattern, 0, 0)
			expected := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l "+legacy+" ORDER BY l.started_at DESC,l.id DESC", legacyArgs...)
			predicate, args, err := charityModelFilter(t.Context(), tx, ListFilter{CharityModel: &pattern})
			if err != nil {
				t.Fatal(err)
			}
			where := "WHERE (l.completed_at IS NULL OR l.completed_at>?)" + predicate
			args = append([]any{int64(1_799_000_000)}, args...)
			actual := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l "+where+" ORDER BY l.started_at DESC,l.id DESC", args...)
			if !slices.Equal(actual, expected) {
				t.Fatalf("optimized IDs differ: got %v want %v", actual, expected)
			}
			for _, page := range []int64{1, 2, 999} {
				query, pageArgs, metadata, err := logListPageQuery(t.Context(), tx, "l.id", "", where, args, &pagination.Request{Page: page, Size: 10}, 10)
				if err != nil {
					t.Fatal(err)
				}
				last := max(int64(1), (int64(len(expected))+9)/10)
				selected := min(page, last)
				requireLogPagination(t, metadata, selected, 10, int64(len(expected)))
				start := min(int((selected-1)*10), len(expected))
				want := expected[start:min(start+10, len(expected))]
				if got := queryLogIDs(t, tx, query, pageArgs...); !slices.Equal(got, want) {
					t.Fatalf("page %d got %v want %v", page, got, want)
				}
			}
		})
	}
}

func TestCharityModelLookupBoundsFallBackWithoutDroppingMatches(t *testing.T) {
	for _, tc := range []struct {
		names     int
		pattern   string
		optimized bool
	}{
		{1, "historic", true}, {64, "historic", true}, {65, "historic", false},
		{4096, "historic-04095", true}, {4097, "historic-04096", false},
		{4100, "historic-04099", false}, {4096, "absent", true}, {4097, "absent", false},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.names, tc.pattern), func(t *testing.T) {
			database := indexedCharityQueryDatabase(t, 1)
			if _, err := database.Exec("DELETE FROM request_logs"); err != nil {
				t.Fatal(err)
			}
			_, err := database.Exec(`WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO request_logs(id,logical_request_id,started_at,completed_at,model,route_kind,error_diag)
SELECT x+1,printf('req_%021dA',x+1),1800000000,1800000001,printf('historic-%05d',x),'charity_chat_completions','' FROM n`, tc.names-1)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			predicate, args, err := charityModelFilter(t.Context(), tx, ListFilter{CharityModel: &tc.pattern})
			if err != nil {
				t.Fatal(err)
			}
			if optimized := predicate != charityModelPredicate; optimized != tc.optimized {
				t.Fatalf("optimized=%v want=%v", optimized, tc.optimized)
			}
			got := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l WHERE 1=1"+predicate+" ORDER BY l.id", args...)
			want := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l WHERE 1=1"+charityModelPredicate+" ORDER BY l.id", tc.pattern)
			if !slices.Equal(got, want) {
				t.Fatalf("bounded lookup lost results: got %v want %v", got, want)
			}
		})
	}
}

type cancelModelQuery struct {
	logReadQueryer
	cancel context.CancelFunc
}

func (r cancelModelQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := r.logReadQueryer.QueryContext(ctx, query, args...)
	r.cancel()
	return rows, err
}

func TestCharityModelLookupCancellationReturnsReader(t *testing.T) {
	database := indexedCharityQueryDatabase(t, 6400)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, complete, err := matchingCharityModelNames(ctx, cancelModelQuery{tx, cancel}, "model-001")
	if err == nil || complete || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enumeration=%v complete=%v", err, complete)
	}
	_ = tx.Rollback()
	var one int
	if err := database.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("reader not reusable after cancellation: %v", err)
	}
}

func TestCharityModelLookupKeepsDirectReaderAndDatePaths(t *testing.T) {
	database := indexedCharityQueryDatabase(t, 3200)
	pattern := "model-001"
	predicate, _, err := charityModelFilter(t.Context(), database, ListFilter{CharityModel: &pattern})
	if err != nil || predicate != charityModelPredicate {
		t.Fatalf("direct reader performed a separate-snapshot lookup: %q %v", predicate, err)
	}
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	from := int64(1_800_000_050)
	predicate, _, err = charityModelFilter(t.Context(), tx, ListFilter{CharityModel: &pattern, From: &from})
	if err != nil || predicate != charityModelPredicate {
		t.Fatalf("date search lost its selective path: %q %v", predicate, err)
	}
}

func benchmarkCharitySearch(b *testing.B, database *sql.DB, pattern string) {
	for _, optimized := range []bool{false, true} {
		b.Run(fmt.Sprintf("optimized=%v", optimized), func(b *testing.B) {
			var modelsTime, countTime, rowsTime time.Duration
			for b.Loop() {
				tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
				if err != nil {
					b.Fatal(err)
				}
				predicate, args := charityModelPredicate, []any{pattern}
				start := time.Now()
				if optimized {
					predicate, args, err = charityModelFilter(context.Background(), tx, ListFilter{CharityModel: &pattern})
					if err != nil {
						b.Fatal(err)
					}
				}
				modelsTime += time.Since(start)
				where := "WHERE (l.completed_at IS NULL OR l.completed_at>1799000000)" + predicate
				start = time.Now()
				query, pageArgs, _, err := logListPageQuery(context.Background(), tx, "l.id", "", where, args, &pagination.Request{Page: 1, Size: 50}, 50)
				if err != nil {
					b.Fatal(err)
				}
				countTime += time.Since(start)
				start = time.Now()
				queryLogIDs(b, tx, query, pageArgs...)
				rowsTime += time.Since(start)
				if err := tx.Rollback(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(modelsTime.Nanoseconds())/float64(b.N)/1e6, "models-ms/op")
			b.ReportMetric(float64(countTime.Nanoseconds())/float64(b.N)/1e6, "count-ms/op")
			b.ReportMetric(float64(rowsTime.Nanoseconds())/float64(b.N)/1e6, "rows-ms/op")
		})
	}
}

func BenchmarkCharityModelSearch(b *testing.B) {
	for _, count := range []int{160_000, 1_600_000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			database := indexedCharityQueryDatabase(b, count)
			if _, err := database.Exec("UPDATE request_logs SET model='[公益]历史综合渠道/abcdefghijklmnopqrstuvwxyz/'||CASE WHEN ((id-1)/32)%100=2 THEN 'literal%_' ELSE printf('model-%03d',((id-1)/32)%100) END||'/聊天'"); err != nil {
				b.Fatal(err)
			}
			for _, pattern := range []string{"MODEL-001", "model-00", "absent", "[公益]"} {
				b.Run(pattern, func(b *testing.B) { benchmarkCharitySearch(b, database, pattern) })
			}
		})
	}
}

func BenchmarkCharityModelSearchDistribution(b *testing.B) {
	database := indexedCharityQueryDatabase(b, 160_000)
	for _, names := range []int{1, 64, 65} {
		b.Run(fmt.Sprintf("all_%d_names", names), func(b *testing.B) {
			if _, err := database.Exec("UPDATE request_logs SET model=printf('historic-model-%05d',((id-1)/32)%?)", names); err != nil {
				b.Fatal(err)
			}
			benchmarkCharitySearch(b, database, "historic")
		})
	}
	b.Run("dominant_model", func(b *testing.B) {
		if _, err := database.Exec("UPDATE request_logs SET model=CASE WHEN id%3200<3168 THEN 'dominant' ELSE printf('other-%05d',id) END"); err != nil {
			b.Fatal(err)
		}
		benchmarkCharitySearch(b, database, "dominant")
	})
	b.Run("high_cardinality", func(b *testing.B) {
		if _, err := database.Exec("UPDATE request_logs SET model=printf('historic-%07d',id)"); err != nil {
			b.Fatal(err)
		}
		benchmarkCharitySearch(b, database, "absent")
	})
}
