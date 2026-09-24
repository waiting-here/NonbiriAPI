package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// The source file is produced by a binary built from the pinned release.
func TestGovernanceUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_DUAL_WALLET_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies the fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := bootstrapTestPath(t, "released-upgrade.sqlite")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, preGovernanceManifestHash)
	before := publishedRetainedImages(t, prior, nil)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM credit_accounts WHERE kind='user' AND balance_sign<>0`: 4,
		`SELECT count(*) FROM game_checkins`:                                         2,
		`SELECT count(*) FROM charity_model_access`:                                  32,
		`SELECT count(*) FROM donations`:                                             1,
		`SELECT count(*) FROM donation_quota_epochs`:                                 2,
		`SELECT count(*) FROM endpoint_key_secrets`:                                  1,
		`SELECT count(*) FROM sessions`:                                              1,
		`SELECT count(*) FROM caller_keys`:                                           1,
		`SELECT count(*) FROM legal_holds`:                                           1,
	} {
		publishedScalar(t, prior, query, want)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()

	// Compare a real fresh database, including every trigger and CHECK.
	fresh, err := Open(bootstrapTestPath(t, "fresh.sqlite"), vault)
	if err != nil {
		t.Fatal(err)
	}
	freshManifest, err := readGenerationManifest(context.Background(), fresh.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Unix()
	var reopened map[string]retainedTableImage
	for attempt := 0; attempt < 2; attempt++ {
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		database := store.DB()
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		manifest, err := readGenerationManifest(context.Background(), database)
		if err != nil || !reflect.DeepEqual(manifest, freshManifest) {
			t.Fatal("fresh and upgraded manifest differ", err)
		}
		if attempt == 0 {
			after := publishedRetainedImages(t, database, before)
			for table, want := range before {
				if !reflect.DeepEqual(after[table], want) {
					t.Errorf("released table %s changed: rows %d -> %d", table, want.Rows, after[table].Rows)
				}
			}
			publishedGovernanceDefaults(t, database, started, time.Now().Unix())
		} else {
			assertRetainedImages(t, database, reopened)
		}
		if err := validateGenerationTwoSeedManifest(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		publishedScalar(t, database, `SELECT count(*) FROM users WHERE username='dual-positive' AND level=6 AND auto_level=4 AND rpm_limit=23 AND concurrency_limit=7 AND endpoint_limit=9`, 1)
		publishedScalar(t, database, `SELECT count(*) FROM users WHERE username='dual-negative' AND level IS NULL AND auto_level=4`, 1)
		publishedScalar(t, database, `SELECT count(*) FROM policy_audits WHERE actor_role='level5'`, 1)
		publishedScalar(t, database, `SELECT count(*) FROM site_config WHERE key='level_display_name_6' AND value='Existing steward title'`, 1)
		for mask := 0; mask < 32; mask++ {
			var got int
			if err := database.QueryRow(`SELECT a.allowed_level_mask FROM charity_model_access a JOIN charity_models m ON m.id=a.model_id WHERE m.model=?`, fmt.Sprintf("model-%02d", mask)).Scan(&got); err != nil {
				t.Fatal(err)
			}
			want := (mask & 15) | ((mask & 16) << 1) | ((mask & 8) << 1)
			if got != want {
				t.Fatalf("mask %d: got %d want %d", mask, got, want)
			}
		}
		var envelope string
		var contextID []byte
		if err := database.QueryRow(`SELECT context_id,encrypted_secret FROM endpoint_key_secrets`).Scan(&contextID, &envelope); err != nil {
			t.Fatal(err)
		}
		keyContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := vault.OpenForGenerationTwoContext(envelope, keyContext)
		if err != nil || string(plain) != "synthetic-upgrade-credential" {
			clear(plain)
			t.Fatal("preserved credential no longer decrypts", err)
		}
		clear(plain)
		var total, achievedSeq []byte
		var achievedAt, ledgerSeq int64
		if err := database.QueryRow(`SELECT u.donation_credit_mag,u.donation_credit_achieved_at,u.donation_credit_achieved_seq,o.ledger_seq FROM users u JOIN credit_operations o ON o.ledger_seq=(SELECT max(ledger_seq) FROM credit_operations WHERE donation_credit_user_id=u.id AND donation_credit_delta_sign<>0) WHERE u.username='dual-positive'`).Scan(&total, &achievedAt, &achievedSeq, &ledgerSeq); err != nil {
			t.Fatal(err)
		}
		amount, err := DecodeU128(total)
		if err != nil || amount.Decimal() != "100000" || achievedAt != 1700000000 {
			t.Fatal("donation achievement", amount, achievedAt, err)
		}
		sequence, err := DecodeU128(achievedSeq)
		if err != nil || sequence.Big().Int64() != ledgerSeq {
			t.Fatal("same-second donation order", sequence, ledgerSeq, err)
		}
		tx, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateAssetLedger(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := validateAssetCapacity(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		reopened = retainedTableImages(t, database, nil)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if destination := os.Getenv("NONBIRI_DUAL_UPGRADED_FIXTURE"); destination != "" {
		upgraded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, upgraded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutation := range map[string]string{
		"conflicting_target_seed": `INSERT INTO site_config(key,value,updated_at) VALUES('level_display_name_6','conflicting seed',1)`,
		"unknown_structure":       `CREATE TABLE unexpected_upgrade_source(id INTEGER PRIMARY KEY)`,
		"unbalanced_ledger":       `UPDATE credit_accounts SET balance_mag=X'00000000000000000000000000000001' WHERE kind='user' AND asset_type='general' AND user_id=(SELECT id FROM users WHERE username='dual-positive')`,
	} {
		t.Run(name, func(t *testing.T) {
			rejected := bootstrapTestPath(t, "rejected.sqlite")
			if err := os.WriteFile(rejected, data, 0600); err != nil {
				t.Fatal(err)
			}
			database, err := openSQLite(rejected, "rw")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(rejected)
			if err != nil {
				t.Fatal(err)
			}
			store, err := Open(rejected, vault)
			if store != nil {
				store.Close()
				t.Fatal("invalid source was accepted")
			}
			if err == nil {
				t.Fatal("invalid source did not fail")
			}
			after, err := os.ReadFile(rejected)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed upgrade mutated its source", err)
			}
			database, err = openSQLite(rejected, "ro")
			if err != nil {
				t.Fatal(err)
			}
			publishedScalar(t, database, `SELECT count(*) FROM sqlite_schema WHERE name='observability_state'`, 0)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func publishedScalar(t *testing.T, database *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := database.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %d want %d: %v", query, got, want, err)
	}
}

func publishedGovernanceDefaults(t *testing.T, database *sql.DB, earliest, latest int64) {
	t.Helper()
	for query, want := range map[string]int64{
		`SELECT count(*) FROM donations WHERE discord_public_thanks IS NULL`:                         1,
		`SELECT count(*) FROM charity_models WHERE is_mainstream=0 AND excluded_request_fields='[]'`: 32,
		`SELECT count(*) FROM donation_keys WHERE input_token_limit_mag IS NULL AND output_token_limit_mag IS NULL AND input_token_reserve IS NULL AND output_token_reserve IS NULL AND input_tokens_used=zeroblob(16) AND output_tokens_used=zeroblob(16) AND input_tokens_reserved=zeroblob(16) AND output_tokens_reserved=zeroblob(16) AND unattributed_total_tokens=tokens_used`: 1,
		`SELECT count(*) FROM credit_accounts WHERE asset_type IN ('sketch_paper','sketch_brush') AND code IN ('external','image_activity_reserve') AND balance_sign=0 AND balance_mag=zeroblob(16)`:                                                                                                                                                                                 4,
		`SELECT count(*) FROM credit_entries WHERE asset_type IN ('sketch_paper','sketch_brush')`:                  0,
		`SELECT count(*) FROM image_activity_state WHERE revision=1 AND upstream_revision IS NULL AND task_rows=0`: 1,
		`SELECT count(*) FROM image_activity_tasks`:                                                                0,
		`SELECT count(*) FROM limited_activity_configs WHERE activity_key='picture-book' AND visible=0 AND starts_at IS NULL AND ends_at IS NULL AND paused=0 AND revision=1 AND module_config='{"paper_price":"1000","brush_price":"10000","brush_cap":"10"}'`: 1,
		`SELECT count(*) FROM activity_exchange_state WHERE total_exchanged_mag=zeroblob(16) AND ((asset_type='sketch_paper' AND cap_mag IS NULL) OR (asset_type='sketch_brush' AND cap_mag=X'0000000000000000000000000000000A'))`:                              2,
		`SELECT count(*) FROM economy_audit_checkpoint WHERE opening_known=1 AND last_ledger_seq=0 AND first_ledger_seq IS NULL AND first_occurred_at IS NULL`:                                                                                                  1,
		`SELECT count(*) FROM economy_audit_buckets`: 0,
		`SELECT count(*) FROM risk_audit_config WHERE threshold_percent=80 AND consecutive_minutes=5 AND shared_ip_hours=24 AND shared_ip_users=3`:               1,
		`SELECT count(*) FROM site_config WHERE key LIKE 'legal_%_override_%' AND value<>''`:                                                                     4,
		`SELECT count(*) FROM site_config a JOIN site_config b ON a.updated_at=b.updated_at WHERE a.key='level_display_name_5' AND b.key='level_display_name_6'`: 1,
		`SELECT count(*) FROM risk_client_rules`:                                                               0,
		`SELECT count(*) FROM site_config WHERE key='level_display_name_5' AND value='见习协管'`:                   1,
		`SELECT count(*) FROM site_config WHERE key='request_error_body_budget_mib' AND value='1024'`:          1,
		`SELECT count(*) FROM site_config WHERE key='game_fishing_blue_fish_chance_bps' AND value='1000'`:      1,
		`SELECT count(*) FROM users WHERE ban_kind<>''`:                                                        0,
		`SELECT count(*) FROM user_activity_state WHERE last_active_at IS NOT NULL OR next_due_at IS NOT NULL`: 0,
		`SELECT count(*) FROM inactivity_policy WHERE config_json='{"enabled":false,"decay":{"enabled":false,"inactive_days":null,"interval_days":null,"assets":{"general":null,"game":null}},"protection":{"enabled":false,"inactive_days":null}}'`: 1,
	} {
		publishedScalar(t, database, query, want)
	}
	for _, query := range []string{
		`SELECT capture_started_at FROM observability_state`,
		`SELECT breakdown_started_at FROM donation_keys`,
		`SELECT updated_at FROM economy_audit_checkpoint`,
		`SELECT min(observation_started_at) FROM user_activity_state`,
		`SELECT max(observation_started_at) FROM user_activity_state`,
	} {
		var at int64
		if err := database.QueryRow(query).Scan(&at); err != nil || at < earliest || at > latest {
			t.Fatal("migration observation start", query, at, earliest, latest, err)
		}
	}
	publishedScalar(t, database, `SELECT (SELECT count(*) FROM user_activity_state)-(SELECT count(*) FROM users)`, 0)
}

// Project every original column; normalize only frozen migration changes.
func publishedRetainedImages(t *testing.T, database *sql.DB, prior map[string]retainedTableImage) map[string]retainedTableImage {
	t.Helper()
	var tables []string
	if prior == nil {
		rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		for name := range prior {
			tables = append(tables, name)
		}
		sort.Strings(tables)
	}
	out := make(map[string]retainedTableImage, len(tables))
	for _, table := range tables {
		columns := prior[table].Columns
		if prior == nil {
			rows, err := database.Query("SELECT * FROM " + hostileQuoteIdent(table) + " LIMIT 0")
			if err != nil {
				t.Fatal(err)
			}
			columns, err = rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		projection := make([]string, len(columns))
		for i, column := range columns {
			projection[i] = hostileQuoteIdent(column)
			if prior == nil {
				switch {
				case table == "users" && column == "level":
					projection[i] = "CASE WHEN level=5 THEN 6 ELSE level END"
				case table == "site_config" && column == "value":
					projection[i] = "CASE WHEN key='level_display_name_5' THEN '见习协管' ELSE value END"
				case table == "charity_model_access" && column == "allowed_level_mask":
					projection[i] = "(allowed_level_mask & 15) | ((allowed_level_mask & 16) << 1) | ((allowed_level_mask & 8) << 1)"
				}
			}
		}
		query := "SELECT " + strings.Join(projection, ",") + " FROM " + hostileQuoteIdent(table)
		var args []any
		if table == "credit_accounts" {
			query += " WHERE asset_type IN ('general','game')"
		}
		if table == "sqlite_sequence" {
			query += " WHERE name<>'credit_accounts'"
		}
		if table == "site_config" && prior != nil {
			marks := make([]string, len(prior[table].Keys))
			for i, key := range prior[table].Keys {
				marks[i] = "?"
				args = append(args, key)
			}
			query += " WHERE key IN (" + strings.Join(marks, ",") + ")"
		}
		rows, err := database.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var encoded, keys []string
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			if table == "site_config" {
				keys = append(keys, values[0].(string))
			}
			typed := make([]any, len(values))
			for i, value := range values {
				typed[i] = []any{fmt.Sprintf("%T", value), value}
			}
			raw, err := json.Marshal(typed)
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
		sort.Strings(keys)
		out[table] = retainedTableImage{Columns: columns, Keys: keys, Rows: len(encoded), Digest: sha256.Sum256([]byte(strings.Join(encoded, "\n")))}
	}
	return out
}
