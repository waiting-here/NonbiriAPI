package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type releasedTableImage struct {
	columns string
	rows    map[string]int
}

func quotedSQLName(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func releasedRows(t *testing.T, database *sql.DB, table, columns string) map[string]int {
	t.Helper()
	rows, err := database.Query("SELECT " + columns + " FROM " + quotedSQLName(table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out[string(encoded)]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func releasedImages(t *testing.T, database *sql.DB) map[string]releasedTableImage {
	t.Helper()
	rows, err := database.Query("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 99 {
		t.Fatalf("released table count=%d", len(tables))
	}
	out := map[string]releasedTableImage{}
	for _, table := range tables {
		rows, err := database.Query("SELECT * FROM " + quotedSQLName(table) + " LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for i := range columns {
			columns[i] = quotedSQLName(columns[i])
		}
		names := strings.Join(columns, ",")
		out[table] = releasedTableImage{names, releasedRows(t, database, table, names)}
	}
	return out
}
func requireReleasedRows(t *testing.T, database *sql.DB, images map[string]releasedTableImage) {
	t.Helper()
	for table, before := range images {
		after := releasedRows(t, database, table, before.columns)
		for row, count := range before.rows {
			if after[row] < count {
				t.Fatalf("upgrade changed an existing row in %s", table)
			}
		}
	}
}
func upgradedReleasedFixture(t *testing.T, variable string, key byte) (*db.Store, *secret.Vault, map[string]releasedTableImage, string) {
	t.Helper()
	source := os.Getenv(variable)
	if source == "" {
		t.Skip("released-source upgrade gate supplies this fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "upgrade.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	before := releasedImages(t, prior)
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{key}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	requireReleasedRows(t, store.DB(), before)
	var games, nonzero int
	if err := store.DB().QueryRow("SELECT COUNT(*),COALESCE(SUM(balance_sign<>0),0) FROM credit_accounts WHERE asset_type='game'").Scan(&games, &nonzero); err != nil || games == 0 || nonzero != 0 {
		t.Fatal("new game accounts", games, nonzero, err)
	}
	return store, vault, before, path
}
func upgradeScalar(t *testing.T, database *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := database.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got %d want %d: %v", query, got, want, err)
	}
}
func checkUpgradedLedger(t *testing.T, store *db.Store) {
	t.Helper()
	tx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}

func TestReleasedGameplayUpgradeAndRecovery(t *testing.T) {
	store, vault, before, path := upgradedReleasedFixture(t, "NONBIRI_GAMEPLAY_FIXTURE", 0x53)
	database := store.DB()
	for _, check := range []struct {
		sql  string
		want int64
	}{
		{"SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved' AND rules_version=1", 1},
		{"SELECT COUNT(*) FROM game_fishing_batches WHERE state='committed' AND rules_version=1", 1},
		{"SELECT COUNT(*) FROM game_linklink_sessions WHERE rules_version=1", 1},
		{"SELECT COUNT(*) FROM game_linklink_summaries WHERE rules_version=1", 1},
		{"SELECT COUNT(*) FROM game_rps_sessions WHERE rules_version=1", 4},
		{"SELECT COUNT(DISTINCT phase) FROM game_rps_sessions", 4},
		{"SELECT COUNT(*) FROM game_rps_pending_results", 3},
		{"SELECT COUNT(*) FROM game_rps_queue", 1},
	} {
		upgradeScalar(t, database, check.sql, check.want)
	}
	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	// Advance only persisted deadlines, without sleeping or changing saved rules.
	var fishingRetry *int64
	if err := database.QueryRow("SELECT MAX(next_attempt_at) FROM game_fishing_batches WHERE state='reserved'").Scan(&fishingRetry); err != nil {
		t.Fatal(err)
	}
	if fishingRetry != nil {
		if _, err := app.games.RecoverModule(context.Background(), "fishing", *fishingRetry, 100, time.Now().Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	service := app.games.AccountContinuation().(*rps.Service)
	for step := 0; ; step++ {
		var deadline *int64
		if err := database.QueryRow(`SELECT MIN(deadline) FROM (
   SELECT COALESCE(phase_deadline,terminal_next_retry_at) AS deadline FROM game_rps_sessions
   UNION ALL SELECT deadline FROM game_rps_queue)`).Scan(&deadline); err != nil {
			t.Fatal(err)
		}
		if deadline == nil {
			break
		}
		if step >= 256 {
			t.Fatal("old RPS work did not converge")
		}
		if _, err := service.RecoverBeforeListenAt(context.Background(), *deadline, 100, time.Now().Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	upgradeScalar(t, database, "SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_onboarding_completions", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_onboarding_holds", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_fishing_batches WHERE net_payout_total_milli<>payout_total_milli OR platform_cut_total_milli<>0 OR welfare_cut_total_milli<>0 OR thursday_cut_total_milli<>0", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_fishing_batches WHERE state='committed'", 2)
	requireReleasedRows(t, database, map[string]releasedTableImage{"credit_entries": before["credit_entries"], "credit_operations": before["credit_operations"], "site_config": before["site_config"]})
	checkUpgradedLedger(t, store)
	var operations int64
	if err := database.QueryRow("SELECT COUNT(*) FROM credit_operations").Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	appAgain, err := buildApplication(auditConfig(), again, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := appAgain.Close(); err != nil {
		t.Fatal(err)
	}
	upgradeScalar(t, again.DB(), "SELECT COUNT(*) FROM credit_operations", operations)
	checkUpgradedLedger(t, again)
}

func TestReleasedBillingUpgradePreservesTerminalAndSettlesActual(t *testing.T) {
	store, vault, before, _ := upgradedReleasedFixture(t, "NONBIRI_BILLING_FIXTURE", 0x63)
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'", 3)
	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	for index, want := range []int64{5, 7, 0, 0} {
		// A dispatched attempt without valid output follows the existing zero-charge rule.
		query := fmt.Sprintf("SELECT c.user_charge_milli FROM charity_reservations c JOIN logical_requests r ON r.id=c.logical_request_id WHERE r.model_snapshot='synthetic/billing-%d'", index)
		upgradeScalar(t, store.DB(), query, want)
	}
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'", 0)
	requireReleasedRows(t, store.DB(), map[string]releasedTableImage{"credit_entries": before["credit_entries"], "credit_operations": before["credit_operations"]})
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'", 0)
	checkUpgradedLedger(t, store)
}
