package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestDormantProgramDataAndBuiltinSelection(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "programs.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY);INSERT INTO users VALUES(1),(2);
CREATE TABLE companion_matches(game TEXT,program TEXT,version INTEGER,official INTEGER,user_id INTEGER,status TEXT);` + db.CompanionProgramsSQL)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	s := New(host.Services{Database: database, ReadDatabase: database, Now: func() time.Time { return now }})
	s.worker.Path = "configured"
	ctx := context.Background()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	one, err := s.save(ctx, tx, 1, "likes", "practice", "first", []byte{1}, "")
	if err != nil || one.Version.Version != 1 {
		t.Fatal(one, err)
	}
	firstID, err := lookupProgram(ctx, tx, 1, "likes", "practice")
	if err != nil || firstID < 1 {
		t.Fatal(firstID, err)
	}
	if _, err = s.save(ctx, tx, 1, "likes", "practice", "bad", nil, "bot.cpp:1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.source(ctx, tx, 2, "likes", "practice", 2); !errors.Is(err, resources.ErrNotFound) {
		t.Fatal("private source visible", err)
	}
	for _, user := range []int64{1, 2} {
		if _, err = s.ResolveTx(ctx, tx, user, "likes", "practice", 1); !errors.Is(err, runner.ErrUploadsDisabled) {
			t.Fatal("private opponents must be unavailable", user, err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO companion_matches VALUES('likes','practice',1,0,1,'active')`); err != nil {
		t.Fatal(err)
	}
	if err = deleteProgram(ctx, tx, 1, "likes", "practice"); !errors.Is(err, resources.ErrConflict) {
		t.Fatal("deleted running version", err)
	}
	two, err := s.save(ctx, tx, 1, "likes", "practice", "new", []byte{3}, "")
	if err != nil || two.Version.Version != 3 {
		t.Fatal(two, err)
	}
	if _, err = s.save(ctx, tx, 0, "likes", "official", "updated", []byte{4}, ""); err != nil {
		t.Fatal(err)
	}
	current, err := s.ResolveTx(ctx, tx, 2, "likes", "official", 1)
	if err != nil || current.Version != 0 {
		t.Fatal("official activated on upload", current, err)
	}
	if err = s.activate(ctx, tx, 0, "likes", "official", 1); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * 24 * time.Hour)
	n, err := s.Retain(ctx, "likes", now.Unix(), 100)
	if err != nil || n != 1 {
		t.Fatal("only failed version should expire", n, err)
	}
	tx, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range []int64{0, 1, 2} {
		items, err := s.overview(ctx, tx, user, "likes")
		if err != nil || !items.Enabled || items.Upload || len(items.Programs) != 0 ||
			items.Official.Version != 0 || len(items.Official.Versions) != 1 || items.Official.Versions[0].Version != 0 {
			t.Fatal(items, err)
		}
	}
	builtin, err := s.ResolveTx(ctx, tx, 2, "likes", "official", 1)
	if err != nil || !builtin.Official || builtin.Version != 0 {
		t.Fatal("stored official selection displaced the built-in", builtin, err)
	}
	if _, err = tx.Exec("UPDATE companion_programs SET enabled=0 WHERE user_id IS NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTx(ctx, tx, 2, "likes", "official", 1); !errors.Is(err, resources.ErrUnavailable) {
		t.Fatal("disabled companion admitted a new match", err)
	}
	items, err := s.overview(ctx, tx, 0, "likes")
	if err != nil || items.Enabled || items.Upload {
		t.Fatal(items, err)
	}
	mine, err := s.ExportTx(ctx, tx, 1, "likes")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(mine)
	var exported []map[string]any
	_ = json.Unmarshal(raw, &exported)
	if len(exported) != 2 || exported[0]["source"] != "first" || exported[1]["active"] != true {
		t.Fatal(string(raw))
	}
	if _, err = tx.Exec("DELETE FROM companion_matches"); err != nil {
		t.Fatal(err)
	}
	if err = deleteProgram(ctx, tx, 1, "likes", "practice"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.save(ctx, tx, 1, "likes", "practice", "replacement", []byte{5}, ""); err != nil {
		t.Fatal(err)
	}
	recreated, err := lookupProgram(ctx, tx, 1, "likes", "practice")
	if err != nil || recreated == firstID {
		t.Fatal("opponent identity reused", recreated, err)
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err = tx.QueryRow("SELECT COUNT(*) FROM companion_program_versions").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal(remaining, err)
	}
}
