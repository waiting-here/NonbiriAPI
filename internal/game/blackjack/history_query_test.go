package blackjack

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// Keep the user's three entries fixed while other players' retained tables grow.
// Only query fixture records are needed; no game settlement is simulated here.
func historyQueryFixture(t testing.TB, tables int) (*db.Store, int64, int64) {
	t.Helper()
	key := bytes.Repeat([]byte{0x41}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "history.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		r, err := tx.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('history reader',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),0,0)`)
	user, err := r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	exec(`WITH RECURSIVE samples(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM samples WHERE n<?)
INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch,view_json,terminal_at,reason)
SELECT 'bjt_'||printf('%021d',n)||'A',n*30,'cancelled',1,n*30,'{}',n*30+1,'closed' FROM samples`, tables)
	exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at,resolved_at,session_id,seat_no)
SELECT 'bjq_'||substr(id,5),?,'released',1,0,0,0,started_at,terminal_at,id,0 FROM game_blackjack_sessions WHERE started_at<=90`, user)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return store, user, int64(tables*30 + 60)
}

func historyQuery(ctx context.Context, database *sql.DB, user, now int64) ([]string, error) {
	rows, err := database.QueryContext(ctx, historyPageSQL, now-retentionSeconds, now, now, user, 21)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func BenchmarkHistorySparseUser(b *testing.B) {
	for _, tables := range []int{1000, 50000} {
		b.Run(fmt.Sprintf("tables=%d", tables), func(b *testing.B) {
			store, user, now := historyQueryFixture(b, tables)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				ids, err := historyQuery(b.Context(), store.ReadDB(), user, now)
				if err != nil || len(ids) != 3 {
					b.Fatalf("rows=%v err=%v", ids, err)
				}
			}
		})
	}
}

func TestHistoryQueryKeepsOwnershipAndPagination(t *testing.T) {
	store, user, now := historyQueryFixture(t, 1000)
	// Released entries can repeat a session or have no session. Neither should
	// duplicate the table or expose another player's table.
	_, err := store.DB().Exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at,resolved_at,session_id,seat_no)
VALUES('bjq_999999999999999999999A',?,'released',1,0,0,0,0,100,'bjt_000000000000000000001A',0),
('bjq_888888888888888888888A',?,'released',1,0,0,0,0,100,NULL,NULL)`, user, user)
	if err != nil {
		t.Fatal(err)
	}
	const reference = `SELECT g.id,g.started_at FROM game_blackjack_sessions g WHERE g.terminal_at>? AND g.terminal_at<=? AND g.started_at<=? AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=g.id AND e.user_id=?) ORDER BY g.started_at DESC LIMIT ?`
	for _, args := range [][]any{
		{now - retentionSeconds, now, now, user, 21},
		{now - retentionSeconds, now, 60, user, 21},
		{31, now, now, user, 21},
		{0, 61, now, user, 21},
		{0, now, now, user, 1},
		{0, now, now, user + 100, 21},
	} {
		read := func(query string) []string {
			t.Helper()
			rows, err := store.ReadDB().QueryContext(t.Context(), query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			out := []string{}
			for rows.Next() {
				var id string
				var start int64
				if err := rows.Scan(&id, &start); err != nil {
					t.Fatal(err)
				}
				out = append(out, fmt.Sprintf("%s:%d", id, start))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return out
		}
		if got, want := read(historyPageSQL), read(reference); !reflect.DeepEqual(got, want) {
			t.Fatalf("args=%v got=%v want=%v", args, got, want)
		}
	}
	rows, err := store.ReadDB().QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+historyPageSQL, 0, now, now, user, 21)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	details := strings.Join(plan, "\n")
	t.Log(details)
	if strings.Contains(details, "SCAN g") || strings.Contains(details, "CORRELATED") || !strings.Contains(details, "idx_blackjack_entry_history") {
		t.Fatalf("history enumerates unrelated tables: %s", details)
	}
}
