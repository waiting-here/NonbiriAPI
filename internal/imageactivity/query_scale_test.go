package imageactivity

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const oldImageQueryIndexes = `DROP INDEX idx_image_activity_tasks_user;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,created_at,id);
DROP INDEX idx_image_activity_tasks_unfinished;
CREATE INDEX idx_image_activity_tasks_unfinished ON image_activity_tasks(user_id,finance_state,accepted_seq);`

func seedTaskQueryRows(t *testing.T, f *fixture, first, last int) {
	t.Helper()
	_, err := f.database.Exec(`WITH RECURSIVE seq(n) AS (VALUES(?) UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds,completed_at)
SELECT printf('img_%021dA',n),CASE WHEN n%10000=0 THEN ? ELSE ? END,?,1,1,zeroblob(16),zeroblob(16),'failed','refunded','none',zeroblob(16),?,?,?,60,CASE WHEN n%1000<20 THEN ? ELSE ? END FROM seq`, first, last, f.user, f.other, f.model, testNow-taskLifetime-10, testNow, testNow, testNow-1, testNow-taskLifetime)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskHistoryQueryIndexesPreserveOwnerAndRetention(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	seedTaskQueryRows(t, f, 1, 50)
	page, err := f.service.ListTasks(f.ctx(f.other), f.other, 10, "")
	if err != nil || len(page.Data) != 10 || page.NextCursor == nil || page.Data[0].ID != fmt.Sprintf("img_%021dA", 19) {
		t.Fatal(page, err)
	}
	next, err := f.service.ListTasks(f.ctx(f.other), f.other, 10, *page.NextCursor)
	if err != nil || len(next.Data) != 9 || next.NextCursor != nil || next.Data[0].ID != fmt.Sprintf("img_%021dA", 9) {
		t.Fatal(next, err)
	}
	foreign, err := f.service.ListTasks(f.ctx(f.user), f.user, 10, "")
	if err != nil || len(foreign.Data) != 0 {
		t.Fatal(foreign, err)
	}
	f.tx(t, func(tx *sql.Tx) {
		export, err := f.service.ExportUserTx(t.Context(), tx, f.other, testNow, 100)
		if err != nil || len(export.Tasks) != 19 || export.Tasks[0].ID != fmt.Sprintf("img_%021dA", 1) {
			t.Fatal(export, err)
		}
	})
}

func TestTaskHistoryQueryScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_SCALE") != "1" {
		t.Skip("opt-in full-schema query measurement")
	}
	f := newFixture(t)
	f.configure(t)
	previous := 0
	for _, size := range []int{10_000, 100_000} {
		seedTaskQueryRows(t, f, previous+1, size)
		if previous == 0 {
			_, err := f.database.Exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<10)
INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds)
SELECT printf('img_%021dA',1000000+n),CASE WHEN n<=5 THEN ? ELSE ? END,?,1,1,zeroblob(16),zeroblob(16),'queued','reserved','none',X'00000000000000000000000000000001',?,?,?,60 FROM seq`, f.user, f.other, f.model, testNow, testNow, testNow+60)
			if err != nil {
				t.Fatal(err)
			}
		}
		previous = size
		baseline := make(map[int64]Page[Task])
		for _, candidate := range []bool{false, true} {
			ddl := oldImageQueryIndexes
			if candidate {
				ddl = db.ImageTaskQueryIndexesSQL
			}
			if _, err := f.database.Exec(ddl + `ANALYZE image_activity_tasks;`); err != nil {
				t.Fatal(err)
			}
			for _, owner := range []int64{f.user, f.other, f.trainee} {
				for run := range 2 {
					started := time.Now()
					page, err := f.service.ListTasks(f.ctx(owner), owner, 20, "")
					if err != nil {
						t.Fatal(err)
					}
					elapsed := time.Since(started)
					// Cursor authentication uses a fresh nonce; compare exact rows.
					page.NextCursor = nil
					if !candidate && run == 0 {
						baseline[owner] = page
					} else if !reflect.DeepEqual(page, baseline[owner]) {
						t.Fatal("owner page changed", owner, page, baseline[owner])
					}
					t.Logf("rows=%d candidate=%v owner=%d run=%d returned=%d elapsed=%s", size, candidate, owner, run, len(page.Data), elapsed)
				}
			}
			const admissionQuery = `SELECT count(*),COALESCE(sum(user_id=?),0) FROM image_activity_tasks WHERE finance_state='reserved'`
			started := time.Now()
			var all, own int
			if err := f.database.QueryRow(admissionQuery, f.user).Scan(&all, &own); err != nil || all != 10 || own != 5 {
				t.Fatal(all, own, err)
			}
			t.Logf("rows=%d candidate=%v admission_10_active=%s", size, candidate, time.Since(started))
			for _, query := range []struct {
				sql  string
				args []any
			}{
				{`SELECT accepted_seq FROM image_activity_tasks WHERE user_id=? AND accepted_seq<9223372036854775807 AND (completed_at IS NULL OR completed_at>?) ORDER BY accepted_seq DESC LIMIT 21`, []any{f.user, testNow - taskLifetime}},
				{admissionQuery, []any{f.user}},
			} {
				rows, err := f.database.Query("EXPLAIN QUERY PLAN "+query.sql, query.args...)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					t.Log("plan:", detail)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			var bytes int64
			if err := f.database.QueryRow(`SELECT sum(pgsize) FROM dbstat WHERE name IN ('idx_image_activity_tasks_user','idx_image_activity_tasks_unfinished')`).Scan(&bytes); err != nil {
				t.Fatal(err)
			}
			t.Logf("rows=%d candidate=%v index_bytes=%d", size, candidate, bytes)
		}
	}
}
