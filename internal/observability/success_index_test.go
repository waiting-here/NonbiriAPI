package observability

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// Interleaved outcomes model concurrent requests across one catalogue page.
// Setup and index construction are excluded from the query timings.
func BenchmarkRecentSuccessCoveringIndex(b *testing.B) {
	database, err := sql.Open("sqlite", filepath.Join(b.TempDir(), "outcomes.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`CREATE TABLE observability_state(id INTEGER PRIMARY KEY,capture_started_at INTEGER);
INSERT INTO observability_state VALUES(1,0);
CREATE TABLE charity_request_outcomes(request_log_id INTEGER PRIMARY KEY,model_id INTEGER,completed_at INTEGER,dispatched INTEGER,result TEXT);
CREATE INDEX idx_charity_outcomes_model_time ON charity_request_outcomes(model_id,completed_at,request_log_id);
WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<120000)
INSERT INTO charity_request_outcomes SELECT n,1+(n%100),1000+n/100,n%9<>0,
CASE n%3 WHEN 0 THEN 'success' WHEN 1 THEN 'failure' ELSE 'cancelled' END FROM samples`)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]int64, 50)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	var expected map[int64]SuccessRate
	for _, indexed := range []bool{false, true} {
		name := "existing"
		if indexed {
			name = "covering"
			if _, err := database.Exec(`CREATE INDEX idx_charity_outcomes_success ON charity_request_outcomes(model_id,completed_at,result) WHERE dispatched=1`); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(name, func(b *testing.B) {
			tx, err := database.BeginTx(b.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			got, err := RecentSuccessBatchTx(b.Context(), tx, ids, 4000)
			if err != nil {
				b.Fatal(err)
			}
			if expected == nil {
				expected = got
			} else if !reflect.DeepEqual(got, expected) {
				b.Fatal("covering index changed success rates")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := RecentSuccessBatchTx(b.Context(), tx, ids, 4000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
