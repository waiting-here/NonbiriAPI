package logapi

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestOperationFilterUsesOneCoveredOrderedRange(t *testing.T) {
	f := newOperationFilterFixture(t)
	f.mustExec(db.RequestLogOperationIndexSQL)
	where := ` WHERE (l.completed_at IS NULL OR l.completed_at>?) AND ` + logOperationSQL + `=?`
	args := []any{int64(1000), "responses"}
	for _, query := range []string{
		"SELECT COUNT(*) FROM request_logs l" + where,
		"SELECT l.id FROM request_logs l" + where + " ORDER BY l.started_at DESC,l.id DESC LIMIT 2 OFFSET 2",
		"SELECT l.id FROM request_logs l" + where + " AND l.started_at>=2000 ORDER BY l.started_at,l.id LIMIT 2",
	} {
		plan := charityQueryPlan(t, f.db, query, args)
		if !strings.Contains(plan, "idx_request_logs_operation_started") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatal("operation range must be ordered by its index", plan)
		}
		t.Log("plan:", plan)
		assertOperationQueryDoesNotReadTable(t, f.db, query, args)
	}
	// Retention and deletion both change the covering index's visible results.
	f.mustExec(`UPDATE request_logs SET completed_at=900 WHERE route_kind='openai_responses'`)
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM request_logs l"+where, args...).Scan(&count); err != nil || count != 3 {
		t.Fatal("completed_at index maintenance", count, err)
	}
	f.mustExec(`DELETE FROM request_logs WHERE route_kind='charity_responses'`)
	if err := f.db.QueryRow("SELECT COUNT(*) FROM request_logs l"+where, args...).Scan(&count); err != nil || count != 0 {
		t.Fatal("delete index maintenance", count, err)
	}
}

func TestOperationCharityModelKeepsCoveredRange(t *testing.T) {
	measureOperationCharityModel(t, 1000, 1)
}

func measureOperationCharityModel(t *testing.T, size, repeats int) {
	t.Helper()
	f := newLifecycleLogDatabase(t)
	database := f.store.DB()
	owner := seedLifecycleLogUser(t, database, "narrow-operation-owner", false)
	seedOperationScale(t, database, 1, size, owner, false)
	if _, err := database.Exec("ANALYZE request_logs"); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	model := "needle-model"
	predicate, args, err := charityModelFilter(t.Context(), tx, ListFilter{Operation: "responses", CharityModel: &model})
	if err != nil {
		t.Fatal(err)
	}
	where := "WHERE (l.completed_at IS NULL OR l.completed_at>?)" + predicate
	args = append([]any{int64(1_799_000_000)}, args...)
	countQuery := "SELECT COUNT(*) FROM request_logs l " + where
	start := time.Now()
	var count int64
	for range repeats {
		if err := tx.QueryRow(countQuery, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
	}
	if count != int64(size/1000) {
		t.Fatal("narrow model count", count)
	}
	t.Logf("narrow_model rows=%d count=%d repeat=%d count_total=%s", size, count, repeats, time.Since(start))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	plan := charityQueryPlan(t, database, countQuery, args)
	if !strings.Contains(plan, "COVERING INDEX idx_request_logs_charity_model") {
		t.Fatal("narrow model lost covered index", plan)
	}
	t.Log("count plan:", plan)
	for _, page := range []int64{1, max(1, (count+19)/20)} {
		start = time.Now()
		var query string
		var pageArgs []any
		var ids []int64
		for range repeats {
			query, pageArgs, _, err = logListPageQuery(t.Context(), database, commonListColumns+","+callerIdentityColumns+",l.user_id", callerIdentityJoin, where, args, &pagination.Request{Page: page, Size: 20}, 20)
			if err != nil {
				t.Fatal(err)
			}
			ids = consumeOperationScalePage(t, database, query, pageArgs)
		}
		t.Logf("narrow_model rows=%d page=%d returned=%d repeat=%d count_plus_rows_total=%s", size, page, len(ids), repeats, time.Since(start))
		for i, id := range ids {
			if want := int64(110) + (count-1-(page-1)*20-int64(i))*1000; id != want {
				t.Fatal("narrow page order", id, want)
			}
		}
		plan = charityQueryPlan(t, database, query, pageArgs)
		if !strings.Contains(plan, "COVERING INDEX idx_request_logs_charity_model") {
			t.Fatal("narrow page lost covered range", plan)
		}
		t.Log("page plan:", plan)
	}
}

func assertOperationQueryDoesNotReadTable(t *testing.T, database *sql.DB, query string, args []any) {
	t.Helper()
	var tableRoot int
	if err := database.QueryRow("SELECT rootpage FROM sqlite_schema WHERE name='request_logs'").Scan(&tableRoot); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query("EXPLAIN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tableCursors := map[int]bool{}
	for rows.Next() {
		var address, p1, p2, p3, p5 int
		var opcode string
		var p4, comment sql.NullString
		if err := rows.Scan(&address, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if opcode == "OpenRead" && p2 == tableRoot {
			tableCursors[p1] = true
		}
		if (opcode == "Column" || opcode == "SeekRowid" || opcode == "Rowid") && tableCursors[p1] {
			t.Fatalf("query reads request_logs table: %d %s %d %d %d", address, opcode, p1, p2, p3)
		}
		if opcode == "SorterOpen" || opcode == "Sort" {
			t.Fatal("query sorts candidate rows", opcode)
		}
		t.Logf("vm: %d %s %d %d %d %s %d", address, opcode, p1, p2, p3, p4.String, p5)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
}
