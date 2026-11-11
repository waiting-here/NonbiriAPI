package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryKeepsSummaryAndEventsSeparate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0700); err != nil {
		t.Fatal(err)
	}
	source := `package sample
func register(r Registrar) {
 r.RegisterAdminRoute(http.MethodGet,"/access-summary",summary)
 r.RegisterAdminRoute(http.MethodGet,"/access-events",events)
}
func summary(ctx context.Context, tx *sql.Tx) { tx.QueryRowContext(ctx,"SELECT count(*) FROM facts WHERE occurred_at>=?",1) }
func events(ctx context.Context, tx *sql.Tx) { rows,_:=tx.QueryContext(ctx,"SELECT id FROM facts ORDER BY id LIMIT ?",20); for rows.Next(){} }
`
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "read.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "inventory.json")
	old := os.Args
	os.Args = []string{"queryaudit", root, out}
	defer func() { os.Args = old }()
	main()
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var result Inventory
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Queries) != 2 || len(result.Routes) != 2 {
		t.Fatalf("queries=%d routes=%d", len(result.Queries), len(result.Routes))
	}
	if result.Routes[0].Path == result.Routes[1].Path {
		t.Fatal("summary and events were collapsed")
	}
}
