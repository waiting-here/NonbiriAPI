package charity

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	v1 "github.com/waiting-here/NonbiriAPI/internal/upgrade/v1"
)

// Copy a small, real dispatch fixture into the exact released schema. Only
// retired settings are supplied; all other values come from live domain calls.
func (e *backoffEnv) upgradeFromConfiguredSchema(t *testing.T, profile string) {
	t.Helper()
	compressed, err := os.ReadFile("../upgrade/v1/testdata/schema25.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(ddl)) != "f2fcd20ed52e4e434228d0ef017f4e2cb76b1ab00d090011297e018a841cca65" {
		t.Fatal("released schema changed")
	}
	var seq int
	var name, seed string
	if err := e.store.DB().QueryRow("PRAGMA database_list").Scan(&seq, &name, &seed); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "configured.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	target.SetMaxOpenConns(1)
	defer target.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := target.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(string(ddl))
	exec(fmt.Sprintf("PRAGMA application_id=%d;PRAGMA user_version=%d;PRAGMA foreign_keys=OFF", db.DatabaseApplicationID, db.DatabaseUserVersion))
	exec("ATTACH DATABASE ? AS seed", seed)
	rows, err := target.Query("SELECT type,name,sql FROM sqlite_schema WHERE type IN ('table','trigger') AND name NOT LIKE 'sqlite_%' ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	triggers := map[string]string{}
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		if kind == "table" {
			tables = append(tables, name)
		} else {
			triggers[name] = ddl
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	for name := range triggers {
		exec("DROP TRIGGER " + quote(name))
	}
	for _, table := range tables {
		rows, err := target.Query("SELECT * FROM " + quote(table) + " LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		projection := make([]string, len(columns))
		for i, column := range columns {
			projection[i] = quote(column)
			if table == "donation_key_backoffs" && column == "profile" {
				projection[i] = "'" + profile + "'"
			}
			if table == "dispatch_smart_backoff_facts" && column == "profile" {
				projection[i] = "'day'"
			}
			if table == "donation_key_backoffs" && column == "rules_json" {
				projection[i] = `'[{"kind":"code","value":"legacy.quota","period":"auto"}]'`
			}
			columns[i] = quote(column)
		}
		exec("DELETE FROM " + quote(table))
		exec("INSERT INTO " + quote(table) + "(" + strings.Join(columns, ",") + ") SELECT " + strings.Join(projection, ",") + " FROM seed." + quote(table))
	}
	exec("DELETE FROM main.sqlite_sequence;INSERT INTO main.sqlite_sequence SELECT * FROM seed.sqlite_sequence WHERE name IN (SELECT name FROM main.sqlite_schema WHERE type='table')")
	exec("UPDATE schema_state SET version=25")
	for _, ddl := range triggers {
		exec(ddl)
	}
	exec("DETACH DATABASE seed;PRAGMA foreign_keys=ON")
	snapshot := backoffUpgradeFacts(t, e.store.DB(), tables)
	expected := ""
	for _, source := range v1.SchemaDescriptor().OfflineConversions {
		if source.SourceSchemaVersion == 25 {
			expected = source.SourceManifestHash
		}
	}
	hash, err := db.SchemaManifestHash(t.Context(), target)
	if err != nil || hash != expected {
		t.Fatal("source identity", hash, err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	result, err := v1.Run(t.Context(), path, vault, v1.Options{Apply: true})
	if err != nil || result.SourceSchemaVersion != 25 || result.TargetSchemaVersion != 26 || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if after := backoffUpgradeFacts(t, store.DB(), tables); !reflect.DeepEqual(snapshot, after) {
		for table, before := range snapshot {
			if !reflect.DeepEqual(before, after[table]) {
				t.Error("runtime facts changed", table)
			}
		}
		t.FailNow()
	}
	e.store = store
	e.donation, err = donation.New(donation.Config{Store: store, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	e.service, err = New(Config{Store: store, KeyDeletion: e.donation, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	e.rail, err = claim.New(claim.Dependencies{DB: store.DB(), Secrets: vault, Charity: e.service, Accounting: claim.NewLedgerAccounting(), Acceptance: billingAcceptance{}, Now: func() time.Time { return time.Unix(e.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
}
func backoffUpgradeFacts(t *testing.T, d *sql.DB, tables []string) map[string][]string {
	t.Helper()
	facts := map[string][]string{}
	for _, table := range append(append([]string(nil), tables...), "sqlite_sequence") {
		if table == "schema_state" {
			continue
		}
		rows, err := d.QueryContext(context.Background(), `SELECT * FROM "`+table+`"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := []string{}
		for rows.Next() {
			fields := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range fields {
				refs[i] = &fields[i]
			}
			if err := rows.Scan(refs...); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(values)
		facts[table] = values
	}
	return facts
}

func TestAutomaticBackoffMigratesUnconfiguredAndWrongProfiles(t *testing.T) {
	for _, legacy := range []string{"absent", "none", "day"} {
		t.Run(legacy, func(t *testing.T) {
			e := newBackoffEnv(t)
			profile := legacy
			if legacy == "absent" {
				profile = "none"
			} else {
				if _, err := e.store.DB().Exec(`INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,stage,updated_at) VALUES(?,7,4,0,?)`, e.donationKey, e.clock.Load()); err != nil {
					t.Fatal(err)
				}
			}
			e.upgradeFromConfiguredSchema(t, profile)
			if s := e.status(t); s != nil {
				t.Fatal("migration manufactured an episode", s)
			}
			h, _ := e.dispatch(t)
			e.complete(t, h, quotaOutcome("month"))
			e.wantWaiting(t, 43200)
			var generation, episode int64
			if err := e.store.DB().QueryRow("SELECT generation,episode FROM donation_key_backoffs WHERE donation_key_id=?", e.donationKey).Scan(&generation, &episode); err != nil {
				t.Fatal(err)
			}
			if legacy == "absent" && (generation != 1 || episode != 1) || legacy != "absent" && (generation != 7 || episode != 5) {
				t.Fatal(generation, episode)
			}
		})
	}
}

func TestAutomaticBackoffMigrationPreservesWaitingAndInFlightRecovery(t *testing.T) {
	for _, state := range []string{"waiting", "ready", "unknown_trial", "accepted_trial"} {
		t.Run(state, func(t *testing.T) {
			e := newBackoffEnv(t)
			h, _ := e.dispatch(t)
			e.complete(t, h, quotaOutcome("five_hour"))
			// Legacy custom-rule attribution survives even after rule text is removed.
			if _, err := e.store.DB().Exec("UPDATE donation_key_backoffs SET source='custom_rule' WHERE donation_key_id=?", e.donationKey); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec("UPDATE dispatch_smart_backoff_facts SET source='custom_rule' WHERE claim_id=?", h.ClaimID()); err != nil {
				t.Fatal(err)
			}
			if state != "waiting" {
				e.ready(t)
			}
			if state == "unknown_trial" || state == "accepted_trial" {
				trial, _ := e.dispatch(t)
				if state == "accepted_trial" {
					if err := e.rail.ObserveResponseHeaders(t.Context(), trial, contract.ResponseObservation{Status: 200, Stream: true, ObservedAt: e.clock.Load()}); err != nil {
						t.Fatal(err)
					}
				}
			}
			headerUntil := e.clock.Load() + 1200
			if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", e.donationKey, headerUntil, e.clock.Load()); err != nil {
				t.Fatal(err)
			}
			e.upgradeFromConfiguredSchema(t, "day")
			before := e.status(t)
			if _, err := e.rail.RecoverNonterminal(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			var actual int64
			if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", e.donationKey).Scan(&actual); err != nil || actual != headerUntil {
				t.Fatal(actual, err)
			}
			switch state {
			case "unknown_trial":
				e.wantWaiting(t, 900)
				if e.status(t).Source != "custom_rule" {
					t.Fatal("legacy attribution lost")
				}
			case "accepted_trial":
				if e.status(t) != nil {
					t.Fatal("accepted trial recovered a cooldown")
				}
			default:
				if !reflect.DeepEqual(before, e.status(t)) {
					t.Fatal("recovery changed idle episode", e.status(t))
				}
			}
		})
	}
}
