package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func relationFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	mustExec(t, database, `PRAGMA foreign_keys=ON;
CREATE TABLE root (id INTEGER PRIMARY KEY);
CREATE TABLE parent (a INTEGER, b INTEGER, root INTEGER REFERENCES root(id), PRIMARY KEY(a,b));
CREATE TABLE child (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, FOREIGN KEY(a,b) REFERENCES parent(a,b));
CREATE TABLE grandchild (id INTEGER PRIMARY KEY, child INTEGER REFERENCES child(id));
CREATE TABLE unrelated (id INTEGER PRIMARY KEY, root INTEGER REFERENCES root(id));
INSERT INTO root VALUES(1);
INSERT INTO parent VALUES(1,2,1);
INSERT INTO child VALUES(1,1,2);
INSERT INTO child VALUES(2,NULL,99);`)
	return database
}

func parentRebuildPlan() schemaPlan {
	return schemaPlan{steps: []schemaMigration{{impact: &migrationImpact{rebuiltTables: []string{"parent"}, credentials: credentialsUnchanged}}}}
}

func TestRelationshipScopeIncludesIncomingCompositeButNotGrandchildren(t *testing.T) {
	database := relationFixture(t)
	plan := parentRebuildPlan()
	scope, err := plan.relationshipTables(t.Context(), database)
	if err != nil || !reflect.DeepEqual(scope, map[string]bool{"parent": true, "child": true}) {
		t.Fatal(scope, err)
	}
	// A partially NULL composite key is permitted by SQLite. Unrelated and
	// grandchild damage remains the explicit full audit's responsibility.
	mustExec(t, database, `PRAGMA foreign_keys=OFF; INSERT INTO grandchild VALUES(1,999); INSERT INTO unrelated VALUES(1,999); PRAGMA foreign_keys=ON;`)
	if err := checkRelationships(t.Context(), database, scope, nil); err != nil {
		t.Fatal("scope expanded beyond direct relationships", err)
	}
	if err := checkIntegrity(t.Context(), database); err == nil {
		t.Fatal("explicit audit missed unrelated corruption")
	}
	for _, query := range []string{
		"INSERT INTO parent VALUES(2,3,999)",
		"INSERT INTO child VALUES(3,1,999)",
	} {
		t.Run(query, func(t *testing.T) {
			mustExec(t, database, "PRAGMA foreign_keys=OFF")
			mustExec(t, database, query)
			mustExec(t, database, "PRAGMA foreign_keys=ON")
			if err := checkRelationships(t.Context(), database, scope, nil); err == nil {
				t.Fatal("affected relationship corruption accepted")
			}
			mustExec(t, database, "DELETE FROM parent WHERE a=2; DELETE FROM child WHERE id=3")
		})
	}
}

func TestRelationshipScopeUnitesExactSourceAndTargetMetadata(t *testing.T) {
	database := relationFixture(t)
	plan := parentRebuildPlan()
	source, err := plan.relationshipTables(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `PRAGMA foreign_keys=OFF;
DROP TABLE child;
CREATE TABLE child (id INTEGER PRIMARY KEY, a INTEGER, b INTEGER, root INTEGER REFERENCES root(id));
INSERT INTO child VALUES(1,1,2,999);
CREATE TABLE fresh (a INTEGER, b INTEGER, FOREIGN KEY(a,b) REFERENCES parent(a,b));
INSERT INTO fresh VALUES(1,999);
PRAGMA foreign_keys=ON;`)
	target, err := plan.relationshipTables(t.Context(), database)
	if err != nil || target["child"] || !target["fresh"] {
		t.Fatal(target, err)
	}
	if err := checkRelationships(t.Context(), database, source, target); err == nil {
		t.Fatal("source-only child not checked")
	}
	mustExec(t, database, "UPDATE child SET root=1")
	if err := checkRelationships(t.Context(), database, source, target); err == nil {
		t.Fatal("target-only child not checked")
	}
	mustExec(t, database, "DELETE FROM fresh")
	if err := checkRelationships(t.Context(), database, source, target); err != nil {
		t.Fatal(err)
	}
}

func TestScopedTransactionRestoresForeignKeysAndRollsBack(t *testing.T) {
	for _, rebuild := range []bool{false, true} {
		for _, fault := range []string{"success", "error", "cancel", "deadline", "relationship", "commit"} {
			t.Run(map[bool]string{false: "additive/", true: "rebuild/"}[rebuild]+fault, func(t *testing.T) {
				database := relationFixture(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if fault == "commit" {
					var stop func()
					ctx, stop = withProgress(ctx, 25, func(p Progress) {
						if p.Phase == "commit" && p.State == "started" {
							cancel()
						}
					}, 5*time.Second)
					defer stop()
				}
				err := runConversionTransaction(ctx, database, rebuild, func(ctx context.Context, tx *sql.Tx) error {
					var enabled int
					if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
						return err
					}
					if enabled != map[bool]int{false: 1, true: 0}[rebuild] {
						t.Fatal("wrong transaction foreign-key mode", enabled)
					}
					if _, err := tx.ExecContext(ctx, "CREATE TABLE added(id INTEGER PRIMARY KEY)"); err != nil {
						return err
					}
					switch fault {
					case "error":
						return errors.New("injected migration failure")
					case "cancel":
						cancel()
					case "deadline":
						deadline, done := context.WithDeadline(ctx, time.Now().Add(-time.Second))
						defer done()
						_, err := tx.ExecContext(deadline, "INSERT INTO added VALUES(1)")
						return err
					}
					return nil
				}, func(ctx context.Context, tx *sql.Tx) error {
					if fault == "relationship" {
						cancel()
					}
					return checkRelationships(ctx, tx, map[string]bool{"child": true}, nil)
				})
				if (err == nil) != (fault == "success") {
					t.Fatal(fault, err)
				}
				assertForeignKeyEnforcement(t, database)
				var count int
				if err := database.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='added'").Scan(&count); err != nil || count != map[bool]int{false: 0, true: 1}[fault == "success"] {
					t.Fatal("partial transaction survived", count, err)
				}
				if fault != "success" {
					if err := runConversionTransaction(t.Context(), database, rebuild, func(ctx context.Context, tx *sql.Tx) error {
						_, err := tx.ExecContext(ctx, "CREATE TABLE added(id INTEGER PRIMARY KEY)")
						return err
					}, nil); err != nil {
						t.Fatal("retry failed", err)
					}
				}
			})
		}
	}
}

func TestMigrationRejectsMissingImpactAndUndeclaredRebuild(t *testing.T) {
	for _, fault := range []string{"missing", "credentials", "storage"} {
		database, registry := migrationFixture(t, 17)
		if fault == "missing" {
			registry.versions[1].impact = nil
		} else if fault == "credentials" {
			registry.versions[1].impact.credentials = credentialsUnclassified
		} else {
			registry.versions[1].impact = &migrationImpact{credentials: credentialsUnchanged}
		}
		if err := migrateFixture(t.Context(), database, registry, nil); err == nil {
			t.Fatal("unclassified migration accepted", fault)
		}
	}
	database := relationFixture(t)
	ctx := context.WithValue(t.Context(), migrationImpactKey{}, &migrationImpact{credentials: credentialsUnchanged})
	if err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		return rebuildTables(ctx, tx, []tableRebuild{{Name: "parent", SQL: "CREATE TABLE parent (a INTEGER PRIMARY KEY)"}})
	}); err == nil {
		t.Fatal("undeclared rebuild accepted")
	}
	assertForeignKeyEnforcement(t, database)
}

func TestMigrationStatementDeadlineFinishesRollbackAndRestoresConnection(t *testing.T) {
	database := relationFixture(t)
	err := runConversionTransaction(t.Context(), database, true, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE interrupted(value INTEGER)"); err != nil {
			return err
		}
		deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err := tx.ExecContext(deadline, `WITH RECURSIVE numbers(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM numbers WHERE n<100000000) INSERT INTO interrupted SELECT n FROM numbers`)
		return err
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("SQL deadline was lost", err)
	}
	assertForeignKeyEnforcement(t, database)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='interrupted'").Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled statement left partial schema", count, err)
	}
	if err := runConversionTransaction(t.Context(), database, true, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE interrupted(value INTEGER)")
		return err
	}, nil); err != nil {
		t.Fatal("connection could not retry", err)
	}
}

type cancelRelationshipQuery struct {
	queryer
	cancel context.CancelFunc
}

func (q cancelRelationshipQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := q.queryer.QueryContext(ctx, query, args...)
	q.cancel()
	return rows, err
}

func TestCancellationWhileRelationshipCursorIsOpenRollsBack(t *testing.T) {
	database := relationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := runConversionTransaction(ctx, database, true, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE interrupted(value INTEGER)")
		return err
	}, func(ctx context.Context, tx *sql.Tx) error {
		return checkRelationships(ctx, cancelRelationshipQuery{queryer: tx, cancel: cancel}, map[string]bool{"child": true}, nil)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertForeignKeyEnforcement(t, database)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='interrupted'").Scan(&count); err != nil || count != 0 {
		t.Fatal("relation cancellation left partial schema", count, err)
	}
}
