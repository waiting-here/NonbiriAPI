package queryread_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/queryread"
	_ "github.com/waiting-here/NonbiriAPI/internal/sqliteadapter"
)

func TestPageSnapshotScopeClampAndParameterIsolation(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "page.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, owner INTEGER, tag TEXT); INSERT INTO items VALUES(1,7,'x'),(2,7,'x'),(3,8,'x'),(4,7,'y')`); err != nil {
		t.Fatal(err)
	}
	args := make([]any, 2, 5)
	args[0], args[1] = 7, "x"
	backing := args[:5]
	for i := 2; i < len(backing); i++ {
		backing[i] = "untouched"
	}
	authorized := false
	items, err := queryread.Snapshot(t.Context(), database, time.Second, func(context.Context, *sql.Tx) error {
		authorized = true
		return nil
	}, func(ctx context.Context, tx *sql.Tx) ([]int, error) {
		if !authorized {
			t.Fatal("read preceded authorization")
		}
		request := pagination.Request{Page: pagination.MaxPage, Size: 10}
		statement, params, meta, err := queryread.Page(ctx, tx, `SELECT id FROM items WHERE owner=? AND tag=?`, ` ORDER BY id DESC`, `ORDER BY id DESC`, args, &request, 10)
		if err != nil {
			return nil, err
		}
		if meta.Page != "1" || meta.TotalItems != "2" {
			t.Fatalf("wrong metadata: %+v", meta)
		}
		return queryread.Collect(ctx, tx, statement, params, func(s queryread.Scanner) (int, error) { var id int; err := s.Scan(&id); return id, err })
	})
	if err != nil || !reflect.DeepEqual(items, []int{2, 1}) {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for _, item := range backing[2:] {
		if item != "untouched" {
			t.Fatal("page parameters overwrote caller backing array")
		}
	}
	denied := errors.New("denied")
	_, err = queryread.Snapshot(t.Context(), database, time.Second, func(context.Context, *sql.Tx) error { return denied }, func(context.Context, *sql.Tx) (int, error) { t.Fatal("read after denied authorization"); return 0, nil })
	if !errors.Is(err, denied) {
		t.Fatal(err)
	}
}

func TestSnapshotBudgetIncludesPoolWait(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = queryread.Snapshot(t.Context(), database, 30*time.Millisecond, func(context.Context, *sql.Tx) error { t.Fatal("authorization reached occupied pool"); return nil }, func(context.Context, *sql.Tx) (int, error) { t.Fatal("read reached occupied pool"); return 0, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
