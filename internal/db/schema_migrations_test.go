package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// These are synthetic storage revisions, not unpublished release fixtures.
const migrationTestBaseline = `
CREATE TABLE schema_state(id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL CHECK(version>=1)) STRICT;
CREATE TABLE saves(id INTEGER PRIMARY KEY,coins INTEGER NOT NULL,inventory TEXT NOT NULL) STRICT;
CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL) STRICT;
INSERT INTO schema_state VALUES(1,1);
INSERT INTO saves VALUES(1,37,'rod,bait');
INSERT INTO settings VALUES('theme','dark');`

func migrationFixture(t *testing.T, source int) (*sql.DB, schemaRegistry) {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	hostileMustExec(t, database, migrationTestBaseline)
	hash := func() string {
		t.Helper()
		manifest, err := readGenerationManifest(t.Context(), database)
		if err != nil {
			t.Fatal(err)
		}
		return generationManifestDigest(manifest)
	}
	registry := schemaRegistry{versions: []schemaMigration{{version: 1, manifest: hash()}}}
	for i, query := range []string{
		"ALTER TABLE saves ADD COLUMN title TEXT NOT NULL DEFAULT '';",
		"UPDATE saves SET title=inventory || ':converted'; INSERT INTO settings VALUES('activity_enabled','0');",
	} {
		hostileMustExec(t, database, query)
		registry.versions = append(registry.versions, schemaMigration{version: i + 2, manifest: hash(), sql: query})
	}
	// The final revision is a Go-owned data transform with no DDL change.
	registry.versions = append(registry.versions, schemaMigration{version: 4, manifest: hash(), apply: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE saves SET title=title || ':final'")
		return err
	}})
	// Rebuild the requested source; no backwards migration enters production.
	hostileMustExec(t, database, "DROP TABLE saves; DROP TABLE settings; DROP TABLE schema_state;"+migrationTestBaseline)
	for _, step := range registry.versions[1:source] {
		hostileMustExec(t, database, step.sql)
	}
	hostileMustExec(t, database, "UPDATE schema_state SET version=?", source)
	return database, registry
}

func migrateFixture(ctx context.Context, database *sql.DB, registry schemaRegistry, validate func(*sql.Tx) error) error {
	return runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		plan, err := registry.plan(ctx, tx)
		if err != nil {
			return err
		}
		if err := plan.apply(ctx, tx); err != nil {
			return err
		}
		if validate != nil {
			return validate(tx)
		}
		return nil
	})
}

func TestSchemaMigrationsUpgradeEveryPriorRevisionOnce(t *testing.T) {
	for source := 1; source < 4; source++ {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			database, registry := migrationFixture(t, source)
			for range 2 {
				err := migrateFixture(t.Context(), database, registry, func(tx *sql.Tx) error {
					var enabled string
					if err := tx.QueryRow("SELECT value FROM settings WHERE key='activity_enabled'").Scan(&enabled); err != nil || enabled != "0" {
						return errors.New("new setting was not installed before validation")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				var coins, version int
				var inventory, title, theme string
				if err := database.QueryRow("SELECT coins,inventory,title,(SELECT version FROM schema_state),(SELECT value FROM settings WHERE key='theme') FROM saves").
					Scan(&coins, &inventory, &title, &version, &theme); err != nil {
					t.Fatal(err)
				}
				if coins != 37 || inventory != "rod,bait" || title != "rod,bait:converted:final" || version != 4 || theme != "dark" {
					t.Fatal("saved facts or migration count changed", coins, inventory, title, version, theme)
				}
			}
		})
	}
}

func TestSchemaMigrationsRollbackWholeChainAndRetry(t *testing.T) {
	for _, fault := range []string{"statement", "transform", "cancel", "validation", "target"} {
		t.Run(fault, func(t *testing.T) {
			database, registry := migrationFixture(t, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			broken := schemaRegistry{versions: append([]schemaMigration(nil), registry.versions...)}
			var validate func(*sql.Tx) error
			switch fault {
			case "statement":
				broken.versions[2].sql += "INSERT INTO absent_table VALUES(1);"
			case "transform":
				broken.versions[3].apply = func(context.Context, *sql.Tx) error { return errors.New("conversion failed") }
			case "cancel":
				broken.versions[3].apply = func(context.Context, *sql.Tx) error { cancel(); return nil }
			case "validation":
				validate = func(*sql.Tx) error { return errors.New("invalid target configuration") }
			case "target":
				broken.versions[3].manifest = registry.versions[0].manifest
			}
			if err := migrateFixture(ctx, database, broken, validate); err == nil {
				t.Fatal("failed migration committed")
			}
			plan, err := registry.plan(t.Context(), database)
			if err != nil || len(plan.steps) != 3 {
				t.Fatal("source schema/version not restored", err)
			}
			var coins, count int
			var inventory string
			if err := database.QueryRow("SELECT coins,inventory,(SELECT count(*) FROM settings) FROM saves").Scan(&coins, &inventory, &count); err != nil ||
				coins != 37 || inventory != "rod,bait" || count != 1 {
				t.Fatal("source data not restored", coins, inventory, count, err)
			}
			if err := migrateFixture(t.Context(), database, registry, nil); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
}

func TestSchemaMigrationsRejectUnknownSourcesAndIncompleteChains(t *testing.T) {
	for _, fault := range []string{"future", "missing", "drift", "gap", "empty-step"} {
		t.Run(fault, func(t *testing.T) {
			database, registry := migrationFixture(t, 1)
			switch fault {
			case "future":
				hostileMustExec(t, database, "UPDATE schema_state SET version=5")
			case "missing":
				hostileMustExec(t, database, "DELETE FROM schema_state")
			case "drift":
				hostileMustExec(t, database, "ALTER TABLE saves ADD COLUMN unexpected TEXT")
			case "gap":
				registry.versions[1].version = 3
			case "empty-step":
				registry.versions[1].sql = ""
			}
			if err := migrateFixture(t.Context(), database, registry, nil); err == nil {
				t.Fatal("unknown source/chain accepted")
			}
			var inventory string
			if err := database.QueryRow("SELECT inventory FROM saves").Scan(&inventory); err != nil || inventory != "rod,bait" {
				t.Fatal("rejected source was transformed", inventory, err)
			}
		})
	}
}

func TestStartupUpgradeValidatesConfigurationBeforeCommit(t *testing.T) {
	database := supportedSourceFixture(t)
	hostileMustExec(t, database, "DELETE FROM site_config WHERE key='site_name'")
	if err := upgradeStartupSchema(t.Context(), database, bootstrapTestVault(t)); err == nil {
		t.Fatal("invalid configuration committed with its schema")
	}
	assertRetainedManifest(t, database, preLedgerRetentionManifestHash)
	hostileMustExec(t, database, "INSERT INTO site_config(key,value,updated_at) VALUES('site_name','preserved',0)")
	if err := upgradeStartupSchema(t.Context(), database, bootstrapTestVault(t)); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := database.QueryRow("SELECT value FROM site_config WHERE key='site_name'").Scan(&name); err != nil || name != "preserved" {
		t.Fatal("upgrade reset configuration", name, err)
	}
}
