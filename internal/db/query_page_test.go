package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMaterializedPageCancelsSparseTailBeforeReturningRows(t *testing.T) {
	database := openDMLTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	// The first row is immediate. The next match needs a long CPU scan, which
	// must finish under QueryContext rather than an uninterruptible later Next.
	selection := `SELECT 1 AS value UNION ALL SELECT (WITH RECURSIVE n(x) AS
		(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000000)
		SELECT SUM(x) FROM n) LIMIT 2`
	rows, err := database.QueryContext(ctx, MaterializePage(selection, `ORDER BY value`))
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sparse page returned before its bounded selection completed: %v", err)
	}
	var value int
	if err := database.QueryRowContext(t.Context(), `SELECT 7`).Scan(&value); err != nil || value != 7 {
		t.Fatalf("connection was not reusable: value=%d err=%v", value, err)
	}
}

func TestMaterializedPagePreservesExplicitOrderAndWindow(t *testing.T) {
	database := openDMLTestDB(t)
	rows, err := database.QueryContext(t.Context(), MaterializePage(
		`SELECT 1 AS value UNION ALL SELECT 3 UNION ALL SELECT 2 ORDER BY value DESC LIMIT ? OFFSET ?`,
		`ORDER BY value DESC`), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range []int{2, 1} {
		if !rows.Next() {
			t.Fatal("page ended early", rows.Err())
		}
		var value int
		if err := rows.Scan(&value); err != nil || value != want {
			t.Fatalf("value=%d want=%d err=%v", value, want, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected page tail", rows.Err())
	}
}
