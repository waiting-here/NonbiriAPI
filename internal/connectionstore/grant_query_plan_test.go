package connectionstore

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGrantQueriesBoundAuthorizationMaterialization(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "db", "generation_two.sql"))
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.ExecContext(t.Context(), string(schema)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{"self", selfGrantsQuery("?,?"), []any{1, 2, 1, 1, 2}},
		{"approved", approvedUsesQuery("?,?", ""), []any{1, 2, 1, 2}},
		{"approved service", approvedUsesQuery("?,?", " AND u.native_service_bit=?"), []any{1, 2, 1, 2, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := database.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			bounded, scanned := false, false
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				bounded = bounded || strings.HasPrefix(detail, "SEARCH g USING INTEGER PRIMARY KEY (rowid=?)")
				scanned = scanned || detail == "SCAN g" || strings.HasPrefix(detail, "SCAN g ")
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !bounded || scanned {
				t.Fatalf("authorization lookup must not visit unrelated grant history: %v", plan)
			}
		})
	}
}
