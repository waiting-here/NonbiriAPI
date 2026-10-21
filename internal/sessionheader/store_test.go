package sessionheader

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

func sessionDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	mustExec(t, database, `PRAGMA foreign_keys=ON;
 CREATE TABLE users(id INTEGER PRIMARY KEY);
 CREATE TABLE charity_models(id INTEGER PRIMARY KEY,session_headers TEXT NOT NULL DEFAULT '[]');
 CREATE TABLE models(id INTEGER PRIMARY KEY,user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,session_headers TEXT NOT NULL DEFAULT '[]');
 CREATE TABLE logical_requests(id TEXT PRIMARY KEY);
 CREATE TABLE charity_header_sessions(user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,model_id INTEGER REFERENCES charity_models(id) ON DELETE CASCADE,name TEXT,value TEXT,format TEXT,idle_seconds INTEGER,expires_at INTEGER,PRIMARY KEY(user_id,model_id,name));
 CREATE TABLE charity_header_uses(logical_request_id TEXT REFERENCES logical_requests(id) ON DELETE CASCADE,user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,model_id INTEGER REFERENCES charity_models(id) ON DELETE CASCADE,name TEXT,value TEXT,idle_seconds INTEGER,sent INTEGER,PRIMARY KEY(logical_request_id,name)); CREATE TABLE personal_header_sessions(user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,model_id INTEGER REFERENCES models(id) ON DELETE CASCADE,name TEXT,value TEXT,format TEXT,idle_seconds INTEGER,expires_at INTEGER,PRIMARY KEY(user_id,model_id,name));
 CREATE TABLE personal_header_uses(logical_request_id TEXT REFERENCES logical_requests(id) ON DELETE CASCADE,user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,model_id INTEGER REFERENCES models(id) ON DELETE CASCADE,name TEXT,value TEXT,idle_seconds INTEGER,sent INTEGER,PRIMARY KEY(logical_request_id,name));
 INSERT INTO users VALUES(1),(2);
 INSERT INTO models VALUES(1,1,'[{"name":"X-Session-ID"}]'),(2,1,'[{"name":"X-Session-ID"}]'),(3,2,'[{"name":"X-Session-ID"}]');
 INSERT INTO charity_models VALUES(1,'[{"name":"X-Session-ID"},{"name":"X-Cache-ID","format":"hex_32"}]'),(2,'[{"name":"X-Session-ID"}]');
 `)
	return database
}
func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func acquire(t *testing.T, database *sql.DB, id string, user, model, at int64) http.Header {
	t.Helper()
	mustExec(t, database, "INSERT OR IGNORE INTO logical_requests VALUES(?)", id)
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	h, err := Acquire(t.Context(), tx, id, user, model, at)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return h
}
func finish(t *testing.T, database *sql.DB, id string, at int64, sent bool) {
	t.Helper()
	if sent {
		if err := MarkSent(t.Context(), database, id); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = Finish(t.Context(), tx, id, at); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentIdleExpiryAndIsolation(t *testing.T) {
	d := sessionDB(t)
	a := acquire(t, d, "a", 1, 1, 100)
	if len(a.Get("X-Cache-ID")) != 32 || len(a.Get("X-Session-ID")) != 36 || a.Get("X-Session-ID") == a.Get("X-Cache-ID") {
		t.Fatal("independent formats")
	}
	other := acquire(t, d, "other", 2, 1, 100)
	model := acquire(t, d, "model", 1, 2, 100)
	if a.Get("X-Session-ID") == other.Get("X-Session-ID") || a.Get("X-Session-ID") == model.Get("X-Session-ID") {
		t.Fatal("identity crossed")
	}
	b := acquire(t, d, "b", 1, 1, 1000)
	if a.Get("X-Session-ID") != b.Get("X-Session-ID") {
		t.Fatal("active request expired")
	}
	finish(t, d, "a", 1100, true)
	c := acquire(t, d, "c", 1, 1, 1500)
	if a.Get("X-Session-ID") != c.Get("X-Session-ID") {
		t.Fatal("overlap lost")
	}
	finish(t, d, "b", 1700, true)
	finish(t, d, "c", 1800, true)
	// A refused request before dispatch has no use record and cannot refresh.
	finish(t, d, "refused", 1900, false)
	last := acquire(t, d, "last", 1, 1, 2099)
	if a.Get("X-Session-ID") != last.Get("X-Session-ID") {
		t.Fatal("idle value lost")
	}
	// An acquired but unsent request cannot extend expiry either.
	finish(t, d, "last", 2099, false)
	next := acquire(t, d, "next", 1, 1, 2100)
	if next.Get("X-Session-ID") == a.Get("X-Session-ID") {
		t.Fatal("expiry equality must rotate")
	}
	// A retry is pinned even after its nominal idle period.
	retry := acquire(t, d, "next", 1, 1, 5000)
	if next.Get("X-Session-ID") != retry.Get("X-Session-ID") {
		t.Fatal("retry changed value")
	}
}
func TestConfigurationReplacementLateCompletionAndDeletion(t *testing.T) {
	d := sessionDB(t)
	a := acquire(t, d, "a", 1, 1, 100)
	mustExec(t, d, `UPDATE charity_models SET session_headers='[{"name":"x-session-id","format":"hex_32","idle_seconds":30}]' WHERE id=1`)
	b := acquire(t, d, "b", 1, 1, 110)
	if b.Get("X-Session-ID") == a.Get("X-Session-ID") {
		t.Fatal("changed configuration reused")
	}
	finish(t, d, "b", 120, true)
	finish(t, d, "a", 200, true)
	c := acquire(t, d, "c", 1, 1, 201)
	if c.Get("X-Session-ID") == b.Get("X-Session-ID") {
		t.Fatal("old completion extended new value")
	}
	mustExec(t, d, "DELETE FROM users WHERE id=1")
	finish(t, d, "c", 300, true)
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM charity_header_sessions WHERE user_id=1").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestParallelRequestsReuseAndCleanup(t *testing.T) {
	d := sessionDB(t)
	var wg sync.WaitGroup
	values := make(chan string, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a' + i))
			values <- acquire(t, d, id, 1, 1, 100).Get("X-Session-ID")
		}()
	}
	wg.Wait()
	close(values)
	first := ""
	for value := range values {
		if first != "" && first != value {
			t.Fatal("parallel rotation")
		}
		first = value
	}
	tx, _ := d.BeginTx(t.Context(), nil)
	n, err := Cleanup(t.Context(), tx, 1000, 100)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	tx.Commit()
	for i := range 8 {
		finish(t, d, string(rune('a'+i)), 120+int64(i), true)
	}
	tx, _ = d.BeginTx(t.Context(), nil)
	n, err = Cleanup(t.Context(), tx, 427, 1)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	tx.Commit()
}
func TestConfigAndActualTransmission(t *testing.T) {
	for _, input := range []string{`[{"name":"Authorization"}]`, `[{"name":"Host"}]`, `[{"name":"Cookie"}]`, `[{"name":"X-Session","idle_seconds":-1}]`, `[{"name":"X-Session","format":"x"}]`, `[{"name":"X-Session","oops":1}]`, `[{"name":"X-Session","format":null}]`, `[{"name":"x-a"},{"name":"X-A"}]`} {
		var c []Config
		err := json.Unmarshal([]byte(input), &c)
		if err == nil {
			_, err = Normalize(c)
		}
		if err == nil {
			t.Fatal("accepted invalid config", input)
		}
	}
	var written atomic.Int32
	value, _ := randomValue("uuid_v4")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != value || r.Header.Get("Authorization") != "Bearer example" {
			t.Error("wrong precedence")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	request, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	request.Header.Set("X-Custom", "fixed")
	request.Header.Set("Authorization", "Bearer example")
	if err := Apply(request, http.Header{"X-Custom": {value}}, func() { written.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if written.Load() != 0 {
		t.Fatal("counted unsent request")
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if written.Load() != 1 {
		t.Fatal("missed write callback")
	}
	if err = Apply(request, http.Header{"Authorization": {value}}, nil); err == nil {
		t.Fatal("auth override")
	}
	if err = Apply(request, http.Header{"X-Session": {strings.Repeat("a", 32) + "\r\nX:y"}}, nil); err == nil {
		t.Fatal("header injection")
	}
}

func TestSessionSurvivesReopenWithinIdleWindow(t *testing.T) {
	for _, personal := range []bool{false, true} {
		name := "charity"
		get := acquire
		if personal {
			name, get = "personal", acquirePersonal
		}
		t.Run(name, func(t *testing.T) {
			d := sessionDB(t)
			h := get(t, d, "a", 1, 1, 100)
			finish(t, d, "a", 120, true)
			var sequence int
			var name, path string
			if err := d.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			restored.SetMaxOpenConns(1)
			after := get(t, restored, "b", 1, 1, 419)
			if after.Get("X-Session-ID") != h.Get("X-Session-ID") {
				t.Fatal("valid session lost on restart")
			}
		})
	}
}
