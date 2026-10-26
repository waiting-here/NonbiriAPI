package riskaudit

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func syntheticUserMinutes(count, userCount int) ([]int64, []Minute, map[int64]bool, int64) {
	const now int64 = 1790000640
	users := make([]int64, userCount+1)
	minutes := make([]Minute, 0, count)
	gaps := map[int64]bool{now - 180: true}
	for i := range users {
		users[i] = int64(i + 1)
	}
	for i := range count {
		user := int64(i/(count/userCount) + 1)
		n := i % (count / userCount)
		minute := now - int64(n/2+1)*60
		kind := "total"
		if n%2 == 1 {
			kind = "self"
		}
		value := Minute{Epoch: "a", UserID: user, Minute: minute, Kind: kind, RPMCommitted: 85,
			OccupancyMillis: 54000, Peak: 1, RPMLimit: 100, ConcurrencyLimit: 1, ConfigRevision: 1, UpdatedAt: minute + 60}
		if n%11 == 0 {
			value.Coverage = CoveragePartial
		}
		if n%17 == 0 {
			value.RPMPending = 1
		}
		if n%23 == 0 {
			value.ConfigRevision = 2
		}
		if n%29 == 0 {
			value.Kind = "total"
			value.Epoch = "b"
		}
		minutes = append(minutes, value)
	}
	return users, minutes, gaps, now
}

func TestUserMinuteGroupingPreservesSummaryBoundaries(t *testing.T) {
	users, minutes, gaps, now := syntheticUserMinutes(2000, 20)
	for _, end := range []int{0, 1199, len(minutes)} {
		for _, kind := range []string{"", "total", "self", "charity"} {
			old := make([]UserSummary, 0, len(users))
			for _, user := range users {
				old = append(old, SummarizeMinutes(user, minutes[:end], gaps, DefaultConfig(), now, kind))
			}
			got := summarizeUserMinutes(users, minutes[:end], gaps, DefaultConfig(), now, kind)
			if !reflect.DeepEqual(got, old) {
				t.Fatalf("grouped summary differs at end=%d kind=%q", end, kind)
			}
		}
	}
}

func BenchmarkRiskUserMinuteGrouping(b *testing.B) {
	for _, size := range []struct{ rows, users int }{{10000, 20}, {100000, 100}} {
		users, minutes, gaps, now := syntheticUserMinutes(size.rows, size.users)
		for _, grouped := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows=%d/users=%d/grouped=%t", size.rows, size.users, grouped), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if grouped {
						_ = summarizeUserMinutes(users, minutes, gaps, DefaultConfig(), now, "self")
					} else {
						for _, user := range users {
							_ = SummarizeMinutes(user, minutes, gaps, DefaultConfig(), now, "self")
						}
					}
				}
			})
		}
	}
}

func BenchmarkRiskUsersSQLite(b *testing.B) {
	for _, size := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			f := newAuditFixture(b)
			const users = 100
			ids := make([]int64, users)
			for i := range ids {
				ids[i] = f.user(1)
			}
			tx, err := f.store.DB().Begin()
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			for _, id := range ids {
				_, err = tx.Exec("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO risk_audit_minutes(epoch,user_id,minute,call_kind,rpm_committed,rpm_limit,concurrency_limit,config_revision,updated_at) SELECT 'bench',?,?-x*60,'total',80,100,1,1,?-(x-1)*60 FROM n", size/users, id, f.now, f.now)
				if err != nil {
					b.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				b.Fatal(err)
			}
			actor := Actor{Admin: true, UserID: f.admin}
			window := Window{From: f.now - 86400, To: f.now, Limit: 100}
			b.ReportAllocs()
			for b.Loop() {
				page, err := f.repository.Users(context.Background(), actor, window)
				if err != nil || len(page.Items) != users {
					b.Fatalf("users=%d err=%v", len(page.Items), err)
				}
			}
		})
	}
}

const combinedWatermark = "SELECT COALESCE(MAX(request_log_id),0),COALESCE(MAX(source_id),0) FROM request_source_facts"
const separateWatermarks = "SELECT (SELECT COALESCE(MAX(request_log_id),0) FROM request_source_facts),(SELECT COALESCE(MAX(source_id),0) FROM request_source_facts)"

// Isolate the production watermark query with the source table's key and
// secondary indexes. Other scan joins and actor authorization are not timed.
func watermarkDatabase(tb testing.TB, count int) *sql.DB {
	tb.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = database.Close() })
	for _, statement := range []string{
		"CREATE TABLE request_source_facts(source_id INTEGER PRIMARY KEY AUTOINCREMENT,request_log_id INTEGER NOT NULL UNIQUE,user_id INTEGER,kind TEXT NOT NULL,effective_ip TEXT NOT NULL,ip_quality TEXT NOT NULL,source_json TEXT NOT NULL,occurred_at INTEGER NOT NULL) STRICT",
		"CREATE INDEX idx_request_sources_user_time ON request_source_facts(user_id,occurred_at,request_log_id)",
		"CREATE INDEX idx_request_sources_scan ON request_source_facts(occurred_at,source_id) WHERE kind IN ('self','charity','unclassified')",
		"CREATE INDEX idx_request_sources_ip_time ON request_source_facts(effective_ip,occurred_at,source_id)",
		fmt.Sprintf("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<%d) INSERT INTO request_source_facts SELECT x,(x*17)%%%d+1,x%%100,'self','192.0.2.1','direct_peer','{}',1790000000+x/10 FROM n", count, count),
	} {
		if _, err = database.Exec(statement); err != nil {
			tb.Fatal(err)
		}
	}
	return database
}

func TestRiskSourceWatermarkPlans(t *testing.T) {
	database := watermarkDatabase(t, 10000)
	for _, query := range []string{combinedWatermark, separateWatermarks} {
		var logID, sourceID int64
		if err := database.QueryRow(query).Scan(&logID, &sourceID); err != nil || logID != 10000 || sourceID != 10000 {
			t.Fatal(logID, sourceID, err)
		}
		rows, err := database.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s\n%s", query, strings.Join(plan, "\n"))
	}
	if _, err := database.Exec("DELETE FROM request_source_facts"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{combinedWatermark, separateWatermarks} {
		var a, b int64
		if err := database.QueryRow(query).Scan(&a, &b); err != nil || a != 0 || b != 0 {
			t.Fatal(a, b, err)
		}
	}
}

func BenchmarkRiskSourceWatermarkSQLite(b *testing.B) {
	for _, size := range []int{10000, 100000} {
		database := watermarkDatabase(b, size)
		for name, query := range map[string]string{"combined": combinedWatermark, "separate": separateWatermarks} {
			b.Run(fmt.Sprintf("rows=%d/%s", size, name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var a, c int64
					if err := database.QueryRow(query).Scan(&a, &c); err != nil || a != int64(size) || c != int64(size) {
						b.Fatal(a, c, err)
					}
				}
			})
		}
	}
}

func TestRiskSourceWatermarkSnapshot(t *testing.T) {
	f := newAuditFixture(t)
	user := f.user(1)
	f.source(user, "self", "192.0.2.9", "direct_peer", "Client", "model", "success", 0)
	tx, err := f.store.ReadDB().BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var oldLog, oldSource int64
	if err = tx.QueryRowContext(t.Context(), separateWatermarks).Scan(&oldLog, &oldSource); err != nil {
		t.Fatal(err)
	}
	f.source(user, "self", "192.0.2.9", "direct_peer", "Client", "model", "success", 0)
	var sameLog, sameSource int64
	if err = tx.QueryRowContext(t.Context(), separateWatermarks).Scan(&sameLog, &sameSource); err != nil || sameLog != oldLog || sameSource != oldSource {
		t.Fatalf("watermarks changed within snapshot: (%d,%d) -> (%d,%d), %v", oldLog, oldSource, sameLog, sameSource, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var newLog, newSource int64
	if err = f.store.ReadDB().QueryRowContext(t.Context(), separateWatermarks).Scan(&newLog, &newSource); err != nil || newLog <= oldLog || newSource <= oldSource {
		t.Fatalf("next snapshot missed committed source: (%d,%d), %v", newLog, newSource, err)
	}
}
