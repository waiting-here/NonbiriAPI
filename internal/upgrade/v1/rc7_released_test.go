package v1

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// The gate supplies a database populated by the pinned rc.7 program. It is
// converted in place so the economy projection gate also consumes its facts.
func TestConvertReleasedDatabase(t *testing.T) {
	path := os.Getenv("NONBIRI_UPGRADE_FIXTURE")
	if path == "" {
		t.Skip("the released-source gate supplies the fixture")
	}
	vault := testVault(t, 0x42)
	read, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	before := retainedFacts(t, read)
	var operations, keys, users int
	if err := read.QueryRow("SELECT (SELECT count(*) FROM credit_operations),(SELECT count(*) FROM endpoint_key_secrets),(SELECT count(*) FROM users WHERE is_admin=0)").Scan(&operations, &keys, &users); err != nil {
		t.Fatal(err)
	}
	if operations < 10 || keys == 0 || users < 2 {
		t.Fatal("released fixture did not populate business facts", operations, keys, users)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	check, err := Run(t.Context(), path, vault, Options{})
	if err != nil || check.Status != "ready" {
		t.Fatal(check, err)
	}
	converted, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || converted.Status != "converted" {
		t.Fatal(converted, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := retainedFacts(t, store.DB())
	for table, digest := range before {
		if after[table] != digest {
			t.Errorf("conversion changed retained facts in %s", table)
		}
	}
	if len(after) != len(before) {
		t.Fatal("conversion changed the table set")
	}
	var bindings, reviewed int
	if err := store.DB().QueryRow("SELECT (SELECT count(*) FROM user_continuity_identities),(SELECT count(*) FROM endpoint_key_secrets WHERE key_body_review_hmac IS NOT NULL)").Scan(&bindings, &reviewed); err != nil {
		t.Fatal(err)
	}
	if bindings != users || reviewed != keys {
		t.Fatal("historical backfill incomplete", bindings, users, reviewed, keys)
	}
	var actionHighWater int64
	if err := store.DB().QueryRow("SELECT seq FROM sqlite_sequence WHERE name='abuse_actions'").Scan(&actionHighWater); err != nil || actionHighWater != 9001 {
		t.Fatal("pruned action high water changed", actionHighWater, err)
	}
	var lastCase string
	if err := store.DB().QueryRow("SELECT id FROM abuse_cases ORDER BY id LIMIT 1").Scan(&lastCase); err != nil {
		t.Fatal(err)
	}
	probe, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := probe.Exec("INSERT INTO abuse_actions(case_id,action,occurred_at,reason_code,rules_json,statistics_json,evidence_count,evidence_bytes) VALUES(?,'expire',1,'expired','{}','{}',0,0)", lastCase)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := inserted.LastInsertId(); err != nil || id != 9002 {
		t.Fatal("deleted action sequence was reused", id, err)
	}
	if err := probe.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.ValidateRecovery(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	t.Logf("preserved %d table projections, %d ledger operations and %d encrypted credentials", len(before), operations, keys)
}

func retainedFacts(t *testing.T, database *sql.DB) map[string][32]byte {
	t.Helper()
	rows, err := database.Query("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
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
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	ignored := map[string]bool{"schema_state": true, "user_continuity_identities": true, "gateway_model_capabilities_state": true, "site_usage_totals": true}
	result := map[string][32]byte{}
	for _, table := range tables {
		if table == "donation_key_cooldowns" || table == "dispatch_retry_after_observations" || table == "request_header_facts" || table == "charity_price_templates" || table == "charity_price_template_audits" || table == "donation_owner_preferences" {
			var count int
			if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("new diagnostic table %s is not empty: %d %v", table, count, err)
			}
			continue
		}
		if ignored[table] || strings.HasPrefix(table, "account_mfa_") {
			continue
		}
		query := "SELECT * FROM " + table
		if table == "site_config" {
			query += " WHERE key NOT IN ('redemption_ban_threshold','redemption_ban_window_seconds','redemption_ban_duration_seconds','game_fishing_milk_crab_chance_bps','request_header_budget_mib') AND key NOT LIKE 'mfa_required_level_%'"
		}
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var encoded []string
		for rows.Next() {
			values := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				t.Fatal(err)
			}
			var retained []any
			for i, column := range columns {
				if table == "observability_state" && column == "header_bytes" {
					if values[i] != int64(0) {
						t.Fatal("upgrade header counter is not empty")
					}
					continue
				}
				if table == "users" && strings.HasPrefix(column, "total_") {
					continue
				}
				if table == "endpoint_key_secrets" && column == "key_body_review_hmac" {
					continue
				}
				if table == "donation_keys" && (column == "key_body_review_hmac" || column == "review_revision") {
					continue
				}
				// New presentation fields have no source value to compare. Every
				// original probability, length and other business column remains.
				if table == "game_fishing_batches" && column == "milk_crab_chance_bps" ||
					(table == "game_fishing_outcome_lengths" || table == "game_fishing_best_lengths") && column == "kind" ||
					table == "game_fishing_length_facts" && column == "milk_crab_length_cm" {
					continue
				}
				kind := "null"
				if values[i] != nil {
					kind = reflect.TypeOf(values[i]).String()
				}
				retained = append(retained, []any{column, kind, values[i]})
			}
			raw, err := json.Marshal(retained)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(encoded)
		result[table] = sha256.Sum256([]byte(strings.Join(encoded, "\n")))
	}
	return result
}
