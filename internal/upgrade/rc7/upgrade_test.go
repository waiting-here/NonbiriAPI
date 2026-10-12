package rc7

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
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
	t.Helper()
	vault := testVault(t, 0x71)
	path := filepath.Join(t.TempDir(), "source.sqlite")
	dbfixture.Materialize(t, path, vault)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	mustExec(t, database, "PRAGMA foreign_keys=OFF")
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, table := range []string{"credit_operations", "economy_audit_buckets", "abuse_windows", "abuse_window_events", "abuse_cases", "abuse_actions", "abuse_evidence", "identity_window_events", "idempotency_records"} {
		rows, err := tx.Query("SELECT name FROM sqlite_schema WHERE type='trigger' AND tbl_name=?", table)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			mustExec(t, tx, "DROP TRIGGER "+name)
		}
		mustExec(t, tx, "CREATE TEMP TABLE prior_"+table+" AS SELECT * FROM "+table+";DROP TABLE "+table)
		start := strings.Index(sourceDDL, "CREATE TABLE "+table+" (")
		if start < 0 {
			t.Fatal("source table missing")
		}
		end := strings.Index(sourceDDL[start:], ";") + start + 1
		mustExec(t, tx, sourceDDL[start:end])
		mustExec(t, tx, "INSERT INTO "+table+" SELECT * FROM prior_"+table+";DROP TABLE prior_"+table)
		for offset := 0; offset < len(sourceDDL); {
			next := strings.Index(sourceDDL[offset:], "CREATE ")
			if next < 0 {
				break
			}
			start = offset + next
			lineEnd := strings.Index(sourceDDL[start:], "\n") + start
			line := sourceDDL[start:lineEnd]
			offset = lineEnd + 1
			if !(strings.HasPrefix(line, "CREATE INDEX ") || strings.HasPrefix(line, "CREATE UNIQUE INDEX ") || strings.HasPrefix(line, "CREATE TRIGGER ")) {
				continue
			}
			if !(strings.Contains(line, " ON "+table+"(") || strings.HasSuffix(line, " ON "+table) || strings.Contains(line, " ON "+table+" ")) {
				continue
			}
			terminator := ";"
			if strings.HasPrefix(line, "CREATE TRIGGER ") {
				terminator = "END;"
			}
			end = strings.Index(sourceDDL[start:], terminator) + start + len(terminator)
			mustExec(t, tx, sourceDDL[start:end])
			offset = end
		}
	}
	mustExec(t, tx, "DROP INDEX idx_gacha_config_events_pool;UPDATE schema_state SET version=16;DELETE FROM site_config WHERE key IN ('redemption_ban_threshold','redemption_ban_window_seconds','redemption_ban_duration_seconds')")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	hash, err := db.SchemaManifestHash(t.Context(), database)
	if err != nil || hash != SourceManifestHash {
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

func TestConversionTransactionRollsBackAllChangesOnFailureOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancelled], func(t *testing.T) {
			path, vault := sourceFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := db.ConvertOffline(ctx, path, vault, db.OfflineSchema{Version: 16, ManifestHash: SourceManifestHash}, true, nil, func(ctx context.Context, database *sql.DB, tx *sql.Tx) error {
				if err := convert(ctx, database, tx, vault, ""); err != nil {
					return err
				}
				if cancelled {
					cancel()
					return nil
				}
				return errors.New("injected conversion failure")
			})
			if err == nil {
				t.Fatal("failure committed")
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
