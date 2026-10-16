package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestTableRebuildPreservesDataReferencesDDLAndSequence(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rebuild.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	_, err = database.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE parent (id INTEGER PRIMARY KEY AUTOINCREMENT, payload BLOB NOT NULL, kind TEXT CHECK(kind IN ('old')));
CREATE TABLE child (id INTEGER PRIMARY KEY, parent INTEGER REFERENCES parent(id) ON DELETE CASCADE);
CREATE INDEX parent_kind ON parent(kind);
CREATE VIEW parent_view AS SELECT id,payload FROM parent;
CREATE TRIGGER child_guard BEFORE INSERT ON child WHEN NOT EXISTS(SELECT 1 FROM parent WHERE id=NEW.parent) BEGIN SELECT RAISE(ABORT,'missing parent'); END;
CREATE TRIGGER parent_guard BEFORE UPDATE ON parent WHEN length(NEW.payload)=0 BEGIN SELECT RAISE(ABORT,'empty payload'); END;
INSERT INTO parent VALUES(100,X'00FF0100','old'); INSERT INTO parent VALUES(200,X'AB','old'); DELETE FROM parent WHERE id=200;
INSERT INTO child VALUES(1,100);`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	target := "CREATE TABLE parent (id INTEGER PRIMARY KEY AUTOINCREMENT, payload BLOB NOT NULL, kind TEXT CHECK(kind IN ('old','new')), added INTEGER NOT NULL DEFAULT 7)"
	change := func(ctx context.Context, tx *sql.Tx) error {
		return rebuildTables(ctx, tx, []tableRebuild{{Name: "parent", SQL: target}})
	}
	if err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		if err := change(ctx, tx); err != nil {
			return err
		}
		return errors.New("rollback probe")
	}); err == nil {
		t.Fatal("rollback expected")
	}
	after, err := readGenerationManifest(ctx, database)
	if err != nil || generationManifestDigest(after) != generationManifestDigest(before) {
		t.Fatal("rollback changed manifest", err)
	}
	if err := runGenerationTwoExtension(ctx, database, change); err != nil {
		t.Fatal(err)
	}
	var payload, stored string
	var count, added int
	if err := database.QueryRow(`SELECT hex(payload),added FROM parent WHERE id=100`).Scan(&payload, &added); err != nil || payload != "00FF0100" || added != 7 {
		t.Fatalf("data %q %d: %v", payload, added, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM child`).Scan(&count); err != nil || count != 1 {
		t.Fatal("child lost", err)
	}
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='parent'`).Scan(&stored); err != nil || stored != target {
		t.Fatal("target DDL changed", err, stored)
	}
	result, err := database.Exec(`INSERT INTO parent(payload,kind) VALUES(X'01','new')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	if id != 201 {
		t.Fatalf("sequence lost: %d", id)
	}
	if _, err := database.Exec(`UPDATE parent SET payload=X'' WHERE id=100`); err == nil {
		t.Fatal("table trigger lost")
	}
	if _, err := database.Exec(`INSERT INTO child VALUES(2,999)`); err == nil {
		t.Fatal("dependent trigger lost")
	}
	if err := database.QueryRow(`SELECT count(*) FROM parent_view`).Scan(&count); err != nil || count != 2 {
		t.Fatal("view lost", err)
	}
	assertForeignKeyEnforcement(t, database)
}
