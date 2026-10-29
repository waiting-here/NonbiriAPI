package managementlog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	. "github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestSnapshotTriggersCompileAgainstCurrentBusinessSchema(t *testing.T) {
	master := make([]byte, 32)
	master[0] = 1
	vault, err := secret.New(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "schema.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	if _, err = database.Exec("CREATE TABLE IF NOT EXISTS agent_codex_session(id INTEGER PRIMARY KEY,revision INTEGER NOT NULL,encrypted_credentials TEXT NOT NULL,expires_at INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	var installed int
	if err = database.QueryRow("SELECT COUNT(*) FROM pragma_table_info('management_operation_items') WHERE name='details'").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if installed == 0 {
		if _, err = database.Exec(db.ManagementDetailsSQL); err != nil {
			t.Fatal(err)
		}
	}
	pattern := regexp.MustCompile(`CREATE TRIGGER management_capture_(\w+)_(?:insert|update|delete) (?:BEFORE|AFTER) (?:INSERT|UPDATE|DELETE) ON (\w+)`)
	seen := map[string]bool{}
	for _, match := range pattern.FindAllStringSubmatch(db.ManagementDetailsSQL, -1) {
		table := match[2]
		if seen[table] {
			continue
		}
		seen[table] = true
		var column string
		if err = database.QueryRow("SELECT name FROM pragma_table_info(?) ORDER BY cid LIMIT 1", table).Scan(&column); err != nil {
			t.Fatal(table, err)
		}
		for _, query := range []string{"EXPLAIN INSERT INTO " + table + " DEFAULT VALUES", "EXPLAIN UPDATE " + table + " SET " + column + "=" + column + " WHERE 0", "EXPLAIN DELETE FROM " + table + " WHERE 0"} {
			rows, e := database.Query(query)
			if e != nil {
				t.Fatal(table, e)
			}
			rows.Close()
		}
	}
	if len(seen) < 79 {
		t.Fatalf("incomplete schema checks: %d", len(seen))
	}
}

func captureFixture(t *testing.T, tables ...string) *Service {
	t.Helper()
	s := fixture(t)
	source, err := os.ReadFile("../db/generation_two.sql")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := os.ReadFile("../db/agent_drivers.sql")
	if err != nil {
		t.Fatal(err)
	}
	source = append(source, driver...)
	for _, table := range tables {
		pattern := regexp.MustCompile(`(?s)CREATE TABLE ` + table + ` \(.*?;`)
		ddl := pattern.FindString(string(source))
		if ddl == "" {
			t.Fatal(table)
		}
		if _, err = s.DB.Exec(ddl); err != nil {
			t.Fatal(err)
		}
		for _, chunk := range strings.Split(db.ManagementDetailsSQL, "\nCREATE TRIGGER ")[1:] {
			if strings.HasPrefix(chunk, "management_capture_"+table+"_") {
				if _, err = s.DB.Exec("CREATE TRIGGER " + chunk); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return s
}
func captureContext(domain, action string) context.Context {
	return WithOperation(context.Background(), Metadata{Source: SourceWeb, ExecutorKind: "human", Actor: Subject{ID: "1", Name: "Administrator", Role: "admin"}, Domain: domain, Action: action})
}
func captureTx(t *testing.T, s *Service, ctx context.Context) *sql.Tx {
	t.Helper()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	if err = StartCaptureTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	return tx
}
func TestSnapshotsRetainRenameDeleteAndActualValues(t *testing.T) {
	s := captureFixture(t, "mainstream_channels")
	id := "mch_000000000000000000000A"
	// Use a valid opaque identifier with the frozen 26-character shape.
	id = "mch_" + strings.Repeat("a", 21) + "A"
	if _, err := s.DB.Exec("INSERT INTO mainstream_channels(id,name,category,enabled,state,revision,created_at,updated_at) VALUES(?, 'Original channel','api_platform',1,'active',1,1,1)", id); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("mainstream-channels", "update")
	SetTarget(ctx, "mainstream-channels", id, "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec("UPDATE mainstream_channels SET name='Renamed channel',enabled=0,revision=2 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{Filter: Filter{Search: "Original channel"}, View: "operations"})
	if err != nil || page.Total != 1 {
		t.Fatalf("old name search: %+v %v", page, err)
	}
	items, err := s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	item := items.Items[0]
	if item.TargetName != "Renamed channel" || item.Details.PreviousName != "Original channel" || len(item.Changes) != 2 {
		t.Fatalf("rename: %+v", item)
	}
	ctx = captureContext("mainstream-channels", "delete")
	SetTarget(ctx, "mainstream_channel", id, "")
	tx = captureTx(t, s, ctx)
	if _, err = tx.Exec("DELETE FROM mainstream_channels WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err = Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Query(context.Background(), 1, Query{Filter: Filter{Action: "delete"}, View: "operations"})
	if err != nil {
		t.Fatal(err)
	}
	items, err = s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if items.Items[0].TargetName != "Renamed channel" || items.Items[0].Details.State != "deleted" {
		t.Fatalf("delete: %+v", items)
	}
}

func TestCaptureNoChangeRollbackReplayAndNestedAttribution(t *testing.T) {
	s := captureFixture(t, "site_config")
	if _, err := s.DB.Exec("INSERT INTO site_config VALUES('registration_open','1',1)"); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("config", "update")
	SetTarget(ctx, "site_config", "registration_open", "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec("UPDATE site_config SET value='1',updated_at=2 WHERE key='registration_open'"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Operations[0].Outcome != NoChange {
		t.Fatalf("same value: %+v", page)
	}
	ctx = captureContext("config", "update")
	tx = captureTx(t, s, ctx)
	if _, err = tx.Exec("UPDATE site_config SET value='0'"); err != nil {
		t.Fatal(err)
	}
	if err = RecordContextTx(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	var value string
	if err = s.DB.QueryRow("SELECT value FROM site_config").Scan(&value); err != nil || value != "1" {
		t.Fatal(value, err)
	}
	var rows int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM management_capture_context").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	MarkReplay(ctx)
	if CaptureEventID(ctx) != "" {
		t.Fatal("replay captured")
	}
	outer := captureContext("config", "update")
	SetTarget(outer, "site_config", "registration_open", "")
	tx = captureTx(t, s, outer)
	if _, err = tx.Exec("UPDATE site_config SET value='0'"); err != nil {
		t.Fatal(err)
	}
	inner := captureContext("config", "update")
	SetTarget(inner, "site_config", "registration_open", "")
	if err = StartCaptureTx(inner, tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE site_config SET value='1'"); err != nil {
		t.Fatal(err)
	}
	if err = RecordContextTx(inner, tx); err != nil {
		t.Fatal(err)
	}
	if err = Commit(outer, tx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil || page.Total != 3 {
		t.Fatalf("nested: %+v %v", page, err)
	}
}

func TestCaptureBoundedCascadeAndSecretExclusion(t *testing.T) {
	s := captureFixture(t, "site_config", "agent_driver_profiles")
	ctx := captureContext("config", "update")
	tx := captureTx(t, s, ctx)
	for i := 0; i < 300; i++ {
		if _, err := tx.Exec("INSERT INTO site_config VALUES(?,?,1)", fmt.Sprintf("private_setting_%03d", i), "do-not-retain-token"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM management_capture_rows").Scan(&n); err != nil || n != 256 {
		t.Fatal(n, err)
	}
	SetTarget(ctx, "site_config", "private_setting_000", "")
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(items)
	if strings.Contains(string(data), "do-not-retain-token") {
		t.Fatal("private value leaked")
	}
	d := items.Items[0].Details
	if !d.Truncated || len(d.Related) != 32 || len(d.Impacts) != 1 || d.Impacts[0].Created != 300 || d.Impacts[0].Omitted != 44 {
		t.Fatalf("bounds: %+v", d)
	}
	ctx = captureContext("ai", "drivers.create")
	tx = captureTx(t, s, ctx)
	if _, err = tx.Exec(`INSERT INTO agent_driver_profiles(revision,name,settings_json,encrypted_key,updated_at) VALUES(1,'Safe driver','{"provider":"external","model":"test-model","extra_parameters":{"secret":"do-not-retain-token"}}','do-not-retain-key',1)`); err != nil {
		t.Fatal(err)
	}
	SetTarget(ctx, "driver", "1", "")
	if err = Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Query(context.Background(), 1, Query{Filter: Filter{Search: "Safe driver"}, View: "operations"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	items, err = s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(items)
	if strings.Contains(string(data), "do-not-retain") || !strings.Contains(string(data), "test-model") {
		t.Fatalf("unsafe driver snapshot: %s", data)
	}
}

func TestPermissionSnapshotsUseKnownScalarsAndIgnoreRevisionOnly(t *testing.T) {
	s := captureFixture(t, "steward_permissions")
	if _, err := s.DB.Exec(`INSERT INTO steward_permissions VALUES(2,1,'{"users.ban":false,"users.read":true}','{"users.ban_duration_max_seconds":"60"}','standard',1,1)`); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("users", "permissions.update")
	SetTarget(ctx, "users", "2", "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec(`UPDATE steward_permissions SET revision=2,permissions='{"users.ban":true,"users.read":true,"unrecognized":"private-token"}',limits='{"users.ban_duration_max_seconds":"120"}' WHERE user_id=2`); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{Filter: Filter{Search: "permissions.users.ban"}, View: "operations"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	items, err := s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	item := items.Items[0]
	if item.TargetName != "Steward" || len(item.Changes) != 2 {
		t.Fatalf("permissions: %+v", item)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "private-token") || !strings.Contains(string(encoded), `"before":"0","after":"1"`) {
		t.Fatal(string(encoded))
	}
	ctx = captureContext("users", "permissions.update")
	SetTarget(ctx, "users", "2", "")
	tx = captureTx(t, s, ctx)
	if _, err = tx.Exec("UPDATE steward_permissions SET revision=3 WHERE user_id=2"); err != nil {
		t.Fatal(err)
	}
	if err = Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Query(context.Background(), 1, Query{Filter: Filter{Outcome: NoChange}, View: "operations"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
}

func TestCaptureCancellationAndRepeatedBusinessTransactions(t *testing.T) {
	s := captureFixture(t, "site_config")
	ctx := captureContext("config", "update")
	SetTarget(ctx, "site_config", "registration_open", "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec("INSERT INTO site_config VALUES('registration_open','1',1)"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx = captureTx(t, s, ctx)
	if _, err := tx.Exec("UPDATE site_config SET value='0'"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil || page.Total != 1 || page.Operations[0].Targets != 2 {
		t.Fatal(page, err)
	}
	work, cancel := context.WithCancel(captureContext("config", "update"))
	tx = captureTx(t, s, work)
	if _, err = tx.ExecContext(work, "UPDATE site_config SET value='1'"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = RecordContextTx(work, tx); err == nil {
		t.Fatal("canceled capture recorded")
	}
	_ = tx.Rollback()
	check, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	var value string
	var rows int
	if err = s.DB.QueryRowContext(check, "SELECT value FROM site_config").Scan(&value); err != nil || value != "0" {
		t.Fatal(value, err)
	}
	if err = s.DB.QueryRowContext(check, "SELECT (SELECT COUNT(*) FROM management_capture_context)+(SELECT COUNT(*) FROM management_capture_rows)+(SELECT COUNT(*) FROM management_capture_totals)").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
}

func TestLegacySavedNamesStaySearchableWithoutInventedDetails(t *testing.T) {
	s := fixture(t)
	id := insert(t, s, recordValue("legacy", 7200000, 1))
	if _, err := s.DB.Exec("UPDATE management_operation_items SET target_name='Previously saved name',details='{}' WHERE operation_id=?", id); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{Filter: Filter{Search: "Previously saved name"}, View: "operations"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	items, err := s.Items(context.Background(), 1, id, 1, 10)
	if err != nil || items.Items[0].Details.Version != 0 {
		t.Fatal(items, err)
	}
}

func TestRelatedChangesPreventFalseNoChange(t *testing.T) {
	s := captureFixture(t, "site_config")
	if _, err := s.DB.Exec("INSERT INTO site_config VALUES('registration_open','1',1)"); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("config", "update")
	SetTarget(ctx, "site_config", "registration_open", "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec("UPDATE site_config SET updated_at=2 WHERE key='registration_open'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO site_config VALUES('charity_enabled','1',1)"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil || page.Operations[0].Outcome != Succeeded {
		t.Fatal(page, err)
	}
}

func TestCaptureOmittedCountsOnlyActualChangesAfterSampleLimit(t *testing.T) {
	s := captureFixture(t, "site_config")
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 258; i++ {
		if _, err = tx.Exec("INSERT INTO site_config VALUES(?, 'original', 1)", fmt.Sprintf("setting_%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("config", "update")
	SetTarget(ctx, "site_config", "setting_000", "")
	tx = captureTx(t, s, ctx)
	if _, err = tx.Exec("UPDATE site_config SET value='changed' WHERE key='setting_000'"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 257; i++ {
		if _, err = tx.Exec("UPDATE site_config SET value=value WHERE key=?", fmt.Sprintf("setting_%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var sampled, changed, omitted int
	if err = tx.QueryRow("SELECT COUNT(*) FROM management_capture_rows").Scan(&sampled); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT updated,omitted FROM management_capture_totals WHERE kind='site_config'").Scan(&changed, &omitted); err != nil {
		t.Fatal(err)
	}
	if sampled != 256 || changed != 1 || omitted != 0 {
		t.Fatalf("after unchanged overflow: sampled=%d changed=%d omitted=%d", sampled, changed, omitted)
	}
	if _, err = tx.Exec("UPDATE site_config SET value='changed' WHERE key='setting_257'"); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT updated,omitted FROM management_capture_totals WHERE kind='site_config'").Scan(&changed, &omitted); err != nil {
		t.Fatal(err)
	}
	if changed != 2 || omitted != 1 {
		t.Fatalf("after changed overflow: changed=%d omitted=%d", changed, omitted)
	}
	if err = Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
}

func TestActualChangePastSnapshotPrefixCannotBecomeNoChange(t *testing.T) {
	s := captureFixture(t, "donations")
	prefix := strings.Repeat("a", 512)
	if _, err := s.DB.Exec("INSERT INTO donations(id,user_id,status,revision,review_note,created_at,updated_at) VALUES(1,2,'pending',1,?,1,1)", prefix+"before"); err != nil {
		t.Fatal(err)
	}
	ctx := captureContext("donations", "review")
	SetTarget(ctx, "donation", "1", "")
	tx := captureTx(t, s, ctx)
	if _, err := tx.Exec("UPDATE donations SET review_note=? WHERE id=1", prefix+"after"); err != nil {
		t.Fatal(err)
	}
	var same bool
	if err := tx.QueryRow("SELECT before_json=after_json FROM management_capture_rows WHERE kind='donations'").Scan(&same); err != nil || !same {
		t.Fatal(same, err)
	}
	if err := Commit(ctx, tx); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(context.Background(), 1, Query{View: "operations"})
	if err != nil || len(page.Operations) != 1 || page.Operations[0].Outcome != Succeeded {
		t.Fatal(page, err)
	}
	items, err := s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
	if err != nil || items.Items[0].Outcome != Succeeded || items.Items[0].Details.State == "unchanged" || items.Items[0].Details.Impacts[0].Updated != 1 {
		t.Fatal(items, err)
	}
}

func TestCapturedFieldOverflowKeepsTruncationSignal(t *testing.T) {
	for _, related := range []bool{false, true} {
		t.Run(fmt.Sprintf("related_%t", related), func(t *testing.T) {
			s := fixture(t)
			ctx := captureContext("config", "update")
			SetTarget(ctx, "site_config", "primary", "")
			tx := captureTx(t, s, ctx)
			values := map[string]string{"$name": "Saved object"}
			for i := 0; i < 70; i++ {
				values[fmt.Sprintf("field_%02d", i)] = "safe value"
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			id := "primary"
			if related {
				id = "related"
			}
			if _, err = tx.Exec("INSERT INTO management_capture_rows(event_id,kind,target_id,before_json,after_json,facts_json) VALUES(?,'site_config',?,'{}',?,'{}')", CaptureEventID(ctx), id, string(encoded)); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec("INSERT INTO management_capture_totals(event_id,kind,updated) VALUES(?,'site_config',1)", CaptureEventID(ctx)); err != nil {
				t.Fatal(err)
			}
			if err = Commit(ctx, tx); err != nil {
				t.Fatal(err)
			}
			page, err := s.Query(context.Background(), 1, Query{View: "operations"})
			if err != nil {
				t.Fatal(err)
			}
			items, err := s.Items(context.Background(), 1, page.Operations[0].ID, 1, 10)
			if err != nil {
				t.Fatal(err)
			}
			item := items.Items[0]
			if !item.Details.Truncated {
				t.Fatalf("missing dropped-field marker: %+v", item)
			}
			changes := item.Changes
			if related {
				if len(item.Details.Related) != 1 {
					t.Fatal(item.Details)
				}
				changes = item.Details.Related[0].Changes
			}
			if len(changes) != 64 {
				t.Fatalf("retained fields=%d", len(changes))
			}
		})
	}
}
