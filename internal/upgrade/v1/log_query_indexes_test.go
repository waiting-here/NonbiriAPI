package v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func allIndexSourceFacts(t *testing.T, database *sql.DB, sourceColumns map[string][]string) map[string][]string {
	t.Helper()
	rows, err := database.Query("SELECT name FROM sqlite_schema WHERE (type='table' OR (type='view' AND name='model_discovery_evidence')) AND name NOT LIKE 'sqlite_%' AND name<>'schema_state' ORDER BY name")
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
		if table == "steward_permissions" || table == "steward_permission_audits" || table == "dispatch_service_health" || table == "donation_service_health" || table == "request_attempt_service_facts" || table == "mainstream_channel_discovery_presets" || table == "mainstream_channel_model_mappings" || table == "dispatch_caller_parameter_errors" || table == "global_pressure_state" || table == "global_pressure_samples" || table == "personal_header_sessions" || table == "personal_header_uses" || table == "endpoint_backoff_policies" || table == "donation_key_cooldowns" || table == "dispatch_retry_after_observations" || table == "request_header_facts" || table == "charity_price_templates" || table == "charity_price_template_audits" || table == "donation_owner_preferences" || table == "donation_key_backoffs" || table == "dispatch_smart_backoff_facts" || table == "channel_charity_binding_presets" || table == "channel_charity_binding_mappings" {
			var count int
			if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("new additive table %s is not empty: %d %v", table, count, err)
			}
			continue
		}
		query, retained := connectionFactQuery(table, sourceColumns)
		if !retained {
			continue
		}
		rows, err := database.Query(query)
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
			if inferenceDefaultRow(t, table, values) {
				continue
			}
			retained := make([]any, 0, len(values))
			for i, column := range columns {
				if table == "models" && column == "session_headers" {
					if values[i] != "[]" {
						t.Fatal("upgrade personal session headers are not disabled by default")
					}
					continue
				}
				if inferenceDefaultColumn(t, table, column, values[i]) {
					continue
				}
				if table == "observability_state" && column == "header_bytes" {
					if values[i] != int64(0) {
						t.Fatal("upgrade header counter is not empty")
					}
					continue
				}
				retained = append(retained, values[i])
			}
			values = retained
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
	columns := legacyConnectionColumns(t, database)
	before := allIndexSourceFacts(t, database, nil)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.SourceSchemaVersion != 19 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if after := allIndexSourceFacts(t, store.DB(), columns); !reflect.DeepEqual(before, after) {
		logFactDifferences(t, store.DB(), before, after)
		t.Fatal("conversion changed original stored facts")
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

// New inference fields have no source value; assert their exact upgrade defaults
// before omitting them from comparisons of historical facts.
func inferenceDefaultColumn(t *testing.T, table, column string, value any) bool {
	t.Helper()
	if table == "users" && (column == "light_rpm_limit" || column == "light_concurrency_limit" || column == "global_rpm_limit") || table == "risk_audit_minutes" && column == "saturation_micros_millis" {
		if value != nil {
			t.Fatalf("upgrade unexpectedly populated %s.%s", table, column)
		}
		return true
	}
	if (table == "request_attempts" || table == "request_logs" || table == "charity_reservations") && column == "usage_details" || table == "donation_usage_reservations" && column == "pricing_details" {
		if value != "{}" {
			t.Fatalf("upgrade %s.%s is not empty: %v", table, column, value)
		}
		return true
	}
	return false
}

func inferenceDefaultRow(t *testing.T, table string, values []any) bool {
	t.Helper()
	if table != "site_config" || values[0] != "default_light_rpm_per_user" && values[0] != "artwork_r18_disabled" {
		return false
	}
	want := "30"
	if values[0] == "artwork_r18_disabled" {
		want = "1"
	}
	if len(values) != 3 || values[1] != want || values[2] != int64(0) {
		t.Fatalf("unexpected new setting default: %v", values)
	}
	return true
}
