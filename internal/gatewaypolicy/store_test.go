package gatewaypolicy

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLoadManyUsesSuppliedReaderAndCurrentPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	if _, err = writer.Exec("PRAGMA journal_mode=WAL; CREATE TABLE gateway_model_capabilities(base_url TEXT NOT NULL,model TEXT NOT NULL,policy_json TEXT NOT NULL,UNIQUE(base_url,model))"); err != nil {
		t.Fatal(err)
	}
	target := Target{"https://gateway.example/native", "model"}
	if _, err = writer.Exec("INSERT INTO gateway_model_capabilities VALUES(?,?,?)", target.BaseURL, target.Model, `{"adapter":"openai_chat","efforts":["high"]}`); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	if _, err = reader.Exec("PRAGMA query_only=1"); err != nil {
		t.Fatal(err)
	}
	store := NewStore(reader)
	held, err := writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	got, err := store.LoadMany(ctx, []Target{target, target, {target.BaseURL, "missing"}})
	cancel()
	held.Close()
	if err != nil || len(got) != 2 || !got[target].AllowsEffort("high") || got[Target{target.BaseURL, "missing"}].HasReasoning() {
		t.Fatal("independent scoped read", err)
	}
	if _, err = writer.Exec("UPDATE gateway_model_capabilities SET policy_json=?", `{"adapter":"openai_chat","efforts":["low"]}`); err != nil {
		t.Fatal(err)
	}
	got, err = store.LoadMany(t.Context(), []Target{target})
	if err != nil || got[target].AllowsEffort("high") || !got[target].AllowsEffort("low") {
		t.Fatal("committed policy invisible", err)
	}
	if _, err = writer.Exec("UPDATE gateway_model_capabilities SET policy_json='{}'"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.LoadMany(t.Context(), []Target{target}); err == nil {
		t.Fatal("invalid persisted policy accepted")
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.LoadMany(t.Context(), []Target{target}); err == nil {
		t.Fatal("closed reader accepted")
	}
}
