package v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func allIndexSourceFacts(t *testing.T, database *sql.DB) map[string][]string {
	t.Helper()
	rows, err := database.Query("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name<>'schema_state' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]string, len(tables))
	for _, table := range tables {
		if table == "donation_key_cooldowns" || table == "dispatch_retry_after_observations" {
			var count int
			if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("new retry table %s is not empty: %d %v", table, count, err)
			}
			continue
		}
		rows, err := database.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var records []string
		for rows.Next() {
			values := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, string(encoded))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Strings(records)
		out[table] = records
	}
	return out
}

func TestLogIndexConversionPreservesEverySourceTable(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema19SQLForTest(t), 19, stableSchema19Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, database, "index-preservation", 0, 1)
	contextID := bytes.Repeat([]byte{7}, 16)
	envelope, err := vault.SealAccountMFA(bytes.Repeat([]byte{9}, 20), contextID)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, "INSERT INTO account_mfa_credentials(user_id,context_id,encrypted_secret,last_step) VALUES(?,?,?,?)", user, contextID, envelope, 321)
	mustExec(t, database, "INSERT INTO account_mfa_attempts(user_id,window_start,failures,blocked_until) VALUES(?,?,?,?)", user, 100, 3, 0)
	mustExec(t, database, "UPDATE site_config SET value='1' WHERE key='mfa_required_level_6'")
	mustExec(t, database, "UPDATE site_config SET value='operator terms remain' WHERE key='legal_terms_override_zh'")
	before := allIndexSourceFacts(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.SourceSchemaVersion != 19 || result.TargetSchemaVersion != 22 {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if after := allIndexSourceFacts(t, store.DB()); !reflect.DeepEqual(before, after) {
		t.Fatal("index-only conversion changed stored facts")
	}
}

func TestLogIndexConversionRollsBackPartialDDL(t *testing.T) {
	for _, mode := range []string{"failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			path, vault := sourceFixtureFromDDL(t, schema19SQLForTest(t), 19, stableSchema19Manifest)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := ConvertOffline(ctx, path, vault, OfflineSchema{Version: 19, ManifestHash: stableSchema19Manifest}, true, nil,
				func(ctx context.Context, _ *sql.DB, tx *sql.Tx) error {
					if _, err := tx.ExecContext(ctx, "CREATE INDEX idx_request_attempts_key_log ON request_attempts(endpoint_key_id_snapshot,request_log_id)"); err != nil {
						return err
					}
					if mode == "cancel" {
						cancel()
						return ctx.Err()
					}
					return errors.New("injected failure after index creation")
				})
			if err == nil || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("conversion error=%v", err)
			}
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := db.SchemaManifestHash(t.Context(), database)
			var version int
			versionErr := database.QueryRow("SELECT version FROM schema_state WHERE id=1").Scan(&version)
			closeErr := database.Close()
			if err != nil || versionErr != nil || closeErr != nil || hash != stableSchema19Manifest || version != 19 {
				t.Fatal("partial DDL survived rollback", hash, version, err, versionErr, closeErr)
			}
			if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
		})
	}
}
