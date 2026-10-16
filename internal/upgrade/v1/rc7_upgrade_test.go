package v1

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// This independent source fixture is the published rc.7 DDL, not a generated
// version of the current schema.
//
//go:embed testdata/schema.sql
var sourceDDL string

func testVault(t *testing.T, value byte) *secret.Vault {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{value}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func mustExec(t *testing.T, q interface {
	Exec(string, ...any) (sql.Result, error)
}, query string, args ...any) {
	t.Helper()
	if _, err := q.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func sourceFixture(t *testing.T) (string, *secret.Vault) {
	return sourceFixtureFromDDL(t, sourceDDL, 16, SourceManifestHash)
}
func sourceFixtureFromDDL(t *testing.T, sourceDDL string, version int, manifest string) (string, *secret.Vault) {
	t.Helper()
	vault := testVault(t, 0x71)
	directory := t.TempDir()
	seed := filepath.Join(directory, "seed.sqlite")
	dbfixture.Materialize(t, seed, vault)
	path := filepath.Join(directory, "source.sqlite")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	// All structure comes from the frozen source. Current startup supplies only
	// seed values and the vault-bound validation material, copied by old columns.
	mustExec(t, database, sourceDDL)
	mustExec(t, database, fmt.Sprintf("PRAGMA application_id=%d;PRAGMA user_version=%d", db.DatabaseApplicationID, db.DatabaseUserVersion))
	mustExec(t, database, "PRAGMA foreign_keys=OFF")
	mustExec(t, database, "ATTACH DATABASE ? AS seed", seed)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT type,name,sql FROM main.sqlite_schema WHERE type IN ('table','trigger') AND name NOT LIKE 'sqlite_%' ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	var tables, triggers []string
	triggerSQL := map[string]string{}
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		if kind == "table" {
			tables = append(tables, name)
		} else {
			triggers = append(triggers, name)
			triggerSQL[name] = ddl
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	for _, name := range triggers {
		mustExec(t, tx, "DROP TRIGGER "+quote(name))
	}
	for _, table := range tables {
		rows, err := tx.Query("SELECT * FROM main." + quote(table) + " LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err := errors.Join(err, rows.Close()); err != nil {
			t.Fatal(err)
		}
		for i := range columns {
			columns[i] = quote(columns[i])
		}
		projection := strings.Join(columns, ",")
		mustExec(t, tx, "DELETE FROM main."+quote(table))
		mustExec(t, tx, "INSERT INTO main."+quote(table)+" ("+projection+") SELECT "+projection+" FROM seed."+quote(table))
	}
	mustExec(t, tx, "DELETE FROM main.sqlite_sequence;INSERT INTO main.sqlite_sequence SELECT * FROM seed.sqlite_sequence WHERE name IN (SELECT name FROM main.sqlite_schema WHERE type='table')")
	mustExec(t, tx, "UPDATE schema_state SET version=?", version)
	mustExec(t, tx, "DELETE FROM site_config WHERE key LIKE 'mfa_required_level_%'")
	if version <= 17 {
		mustExec(t, tx, "DELETE FROM site_config WHERE key='game_fishing_milk_crab_chance_bps'")
	}
	if version == 16 {
		mustExec(t, tx, "DELETE FROM site_config WHERE key IN ('redemption_ban_threshold','redemption_ban_window_seconds','redemption_ban_duration_seconds')")
	}
	for _, name := range triggers {
		mustExec(t, tx, triggerSQL[name])
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, "DETACH DATABASE seed;PRAGMA foreign_keys=ON")
	hash, err := db.SchemaManifestHash(t.Context(), database)
	if err != nil || hash != manifest {
		t.Fatalf("source identity %s: %v", hash, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path, vault
}

func TestConversionChecksThenUpgradesExactSourceAndReenters(t *testing.T) {
	path, vault := sourceFixture(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" {
		t.Fatal(checked, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("check changed source", err)
	}
	if store, err := db.Open(path, vault); err == nil {
		store.Close()
		t.Fatal("normal startup accepted prerelease")
	}
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("rejected startup changed source", err)
	}
	for i := 0; i < 2; i++ {
		result, err := Run(t.Context(), path, vault, Options{Apply: true, LegacyGatewayModels: "malformed stale value"})
		want := "converted"
		if i == 1 {
			want = "current"
		}
		if err != nil || result.Status != want {
			t.Fatal(result, err)
		}
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, err := db.SchemaManifestHash(t.Context(), store.DB())
	if err != nil || hash != db.PinnedGenerationTwoManifestHash {
		t.Fatal(hash, err)
	}
}

func TestConversionRefusesUnknownDriftAndWrongKeyWithoutWrites(t *testing.T) {
	for _, fault := range []string{"version", "manifest", "key", "missing_material"} {
		t.Run(fault, func(t *testing.T) {
			path, vault := sourceFixture(t)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "version":
				mustExec(t, database, "UPDATE schema_state SET version=15")
			case "manifest":
				mustExec(t, database, "CREATE INDEX unexpected_index ON schema_state(version)")
			case "key":
				vault = testVault(t, 0x72)
			case "missing_material":
				mustExec(t, database, "DELETE FROM donation_review_material")
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, apply := range []bool{false, true} {
				if _, err := Run(t.Context(), path, vault, Options{Apply: apply}); err == nil {
					t.Fatal("invalid source accepted")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejection wrote source", err)
			}
		})
	}
}

type conversionValidationCodec struct {
	*secret.Vault
	validate func() error
}

func (codec conversionValidationCodec) ValidateDatabaseMaterial(envelope []byte) error {
	if err := codec.Vault.ValidateDatabaseMaterial(envelope); err != nil {
		return err
	}
	return codec.validate()
}

func TestConversionTransactionRollsBackAllChangesOnFailureOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancelled], func(t *testing.T) {
			path, vault := sourceFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var convertedTx *sql.Tx
			reachedTarget := false
			codec := conversionValidationCodec{Vault: vault, validate: func() error {
				if convertedTx == nil {
					return nil
				}
				// Final material validation runs after both schema stages. Inject
				// the failure here so rollback must also undo the stable migration.
				var version int
				if err := convertedTx.QueryRowContext(ctx, "SELECT version FROM schema_state").Scan(&version); err != nil {
					return err
				}
				if version != db.CurrentSchemaVersion {
					return fmt.Errorf("final validation reached schema %d", version)
				}
				reachedTarget = true
				if cancelled {
					cancel()
					return nil
				}
				return errors.New("injected conversion failure")
			}}
			_, err := ConvertOffline(ctx, path, codec, OfflineSchema{Version: 16, ManifestHash: SourceManifestHash}, true, nil, func(ctx context.Context, database *sql.DB, tx *sql.Tx) error {
				if err := convert(ctx, database, tx, vault, ""); err != nil {
					return err
				}
				convertedTx = tx
				return nil
			})
			if err == nil || !reachedTarget {
				t.Fatal("failure did not roll back the completed target", reachedTarget, err)
			}
			check, err := Run(t.Context(), path, vault, Options{})
			if err != nil || check.Status != "ready" {
				t.Fatal("rollback lost source", check, err)
			}
			result, err := Run(t.Context(), path, vault, Options{Apply: true})
			if err != nil || result.Status != "converted" {
				t.Fatal("retry failed", result, err)
			}
		})
	}
}
