package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
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
		projection := names
		// Only the explicitly migrated source fields differ; all other old
		// columns, including custom configuration timestamps, remain exact.
		if table == "site_config" {
			projection = strings.Replace(projection, quotedSQLName("value"), `CASE WHEN key='level_display_name_5' THEN '见习协管' ELSE value END`, 1)
		}
		if table == "users" {
			projection = strings.Replace(projection, quotedSQLName("level"), `CASE WHEN level=5 THEN 6 ELSE level END`, 1)
		}
		if table == "charity_model_access" {
			projection = strings.Replace(projection, quotedSQLName("allowed_level_mask"), `(allowed_level_mask & 15) | ((allowed_level_mask & 16) << 1) | ((allowed_level_mask & 8) << 1)`, 1)
		}
		out[table] = releasedTableImage{names, releasedRows(t, database, table, projection)}
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
	if !strings.Contains(variable, "DUAL") && len(before) != 99 {
		t.Fatalf("released table count=%d", len(before))
	}
	if strings.Contains(variable, "DUAL") && (before["game_onboarding_completions"].columns == "" || !strings.Contains(before["credit_accounts"].columns, "asset_type")) {
		t.Fatal("fixture is not from the dual-asset release")
	}
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
	var activityAccounts, nonzero int
	if err := store.DB().QueryRow("SELECT COUNT(*),COALESCE(SUM(balance_sign<>0),0) FROM credit_accounts WHERE asset_type IN ('sketch_paper','sketch_brush')").Scan(&activityAccounts, &nonzero); err != nil || activityAccounts != 4 || nonzero != 0 {
		t.Fatal("new activity accounts", activityAccounts, nonzero, err)
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
	testReleasedGameplayUpgrade(t, "NONBIRI_GAMEPLAY_FIXTURE", 1)
}

func TestReleasedDualAssetGameplayUpgrade(t *testing.T) {
	testReleasedGameplayUpgrade(t, "NONBIRI_DUAL_GAMEPLAY_FIXTURE", 2)
}

func testReleasedGameplayUpgrade(t *testing.T, variable string, rulesVersion int) {
	store, vault, before, path := upgradedReleasedFixture(t, variable, 0x53)
	database := store.DB()
	for _, check := range []struct {
		sql  string
		want int64
	}{
		{fmt.Sprintf("SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved' AND rules_version=%d", rulesVersion), 1},
		{fmt.Sprintf("SELECT COUNT(*) FROM game_fishing_batches WHERE state='committed' AND rules_version=%d", rulesVersion), 1},
		{fmt.Sprintf("SELECT COUNT(*) FROM game_linklink_sessions WHERE rules_version=%d", rulesVersion), 1},
		{fmt.Sprintf("SELECT COUNT(*) FROM game_linklink_summaries WHERE rules_version=%d", rulesVersion), 1},
		{fmt.Sprintf("SELECT COUNT(*) FROM game_rps_sessions WHERE rules_version=%d", rulesVersion), 4},
		{"SELECT COUNT(DISTINCT phase) FROM game_rps_sessions", 4},
		{"SELECT COUNT(*) FROM game_rps_pending_results", 3},
		{"SELECT COUNT(*) FROM game_rps_queue", 1},
	} {
		upgradeScalar(t, database, check.sql, check.want)
	}
	var fixtureAt int64
	if err := database.QueryRow(`SELECT max(created_at) FROM game_linklink_sessions`).Scan(&fixtureAt); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Unix(fixtureAt, 0) }
	app, err := buildApplicationWithGameClock(auditConfig(), store, vault, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	const savedToken = "registered_game_http_session_token_0123456789"
	session := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/api/session", "", []*http.Cookie{{Name: auth.UserSessionCookieName, Value: savedToken}}, nil)
	if session.Code != http.StatusOK {
		t.Fatalf("preserved session rejected: %d %s", session.Code, session.Body.String())
	}
	caller := "nbk_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x4a}, 32))
	models := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/v1/models", "", nil, map[string]string{"Authorization": "Bearer " + caller})
	if models.Code != http.StatusOK {
		t.Fatalf("preserved CallerKey rejected: %d %s", models.Code, models.Body.String())
	}
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
	var priorCompletions int64
	for _, count := range before["game_onboarding_completions"].rows {
		priorCompletions += int64(count)
	}
	// The newer fixture contains thirteen accepted newcomer tasks whose
	// original zero-value awards complete while their games recover.
	if rulesVersion == 2 {
		priorCompletions += 13
	}
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_onboarding_completions", priorCompletions)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM site_config WHERE key IN ('game_bidding_enabled','game_likes_enabled') AND value<>'0'", 0)
	var remainingHolds int64
	if rulesVersion == 2 {
		// The saved LinkLink board remains playable with its original hold.
		remainingHolds = 1
	}
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_onboarding_holds", remainingHolds)
	if rulesVersion == 1 {
		upgradeScalar(t, database, "SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'", 0)
	} else {
		// Dual-asset gameplay writes both payment legs, including zero legs.
		upgradeScalar(t, database, "SELECT COUNT(*) FROM credit_entries WHERE asset_type='game' AND delta_sign<>0", 0)
	}
	if rulesVersion == 1 {
		upgradeScalar(t, database, "SELECT COUNT(*) FROM game_fishing_batches WHERE net_payout_total_milli<>payout_total_milli OR platform_cut_total_milli<>0 OR welfare_cut_total_milli<>0 OR thursday_cut_total_milli<>0", 0)
	}
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_fishing_batches WHERE net_payout_total_milli+platform_cut_total_milli+welfare_cut_total_milli+thursday_cut_total_milli<>payout_total_milli", 0)
	upgradeScalar(t, database, "SELECT COUNT(*) FROM game_fishing_batches WHERE state='committed'", 2)
	requireReleasedRows(t, database, map[string]releasedTableImage{"credit_entries": before["credit_entries"], "credit_operations": before["credit_operations"], "site_config": before["site_config"]})
	requireReleasedRows(t, database, map[string]releasedTableImage{"game_rank_events": before["game_rank_events"], "game_statistics_epoch": before["game_statistics_epoch"]})
	checkReleasedNetTotals(t, database)
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
	appAgain, err := buildApplicationWithGameClock(auditConfig(), again, vault, clock)
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
	testReleasedBillingUpgrade(t, "NONBIRI_BILLING_FIXTURE")
}

func TestReleasedDualAssetBillingUpgrade(t *testing.T) {
	testReleasedBillingUpgrade(t, "NONBIRI_DUAL_BILLING_FIXTURE")
}

func testReleasedBillingUpgrade(t *testing.T, variable string) {
	store, vault, before, _ := upgradedReleasedFixture(t, variable, 0x63)
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'", 3)
	if strings.Contains(variable, "DUAL") {
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_quota_receipts`, 4)
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_quota_receipts WHERE state<>'settled'`, 2)
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_usage_reservations WHERE input_tokens_reserved IS NOT NULL OR output_tokens_reserved IS NOT NULL OR input_tokens_actual IS NOT NULL OR output_tokens_actual IS NOT NULL`, 0)
	}

	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	settledCharge := int64(5)
	if strings.Contains(variable, "DUAL") {
		settledCharge = 7
	}
	for index, want := range []int64{settledCharge, 7, 0, 0} {
		// A dispatched attempt without valid output follows the existing zero-charge rule.
		query := fmt.Sprintf("SELECT c.user_charge_milli FROM charity_reservations c JOIN logical_requests r ON r.id=c.logical_request_id WHERE r.model_snapshot='synthetic/billing-%d'", index)
		upgradeScalar(t, store.DB(), query, want)
	}
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'", 0)
	requireReleasedRows(t, store.DB(), map[string]releasedTableImage{"credit_entries": before["credit_entries"], "credit_operations": before["credit_operations"]})
	upgradeScalar(t, store.DB(), "SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'", 0)

	if strings.Contains(variable, "DUAL") {
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_quota_receipts WHERE state<>'settled'`, 0)
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_usage_reservations WHERE state='reserved'`, 0)
		upgradeScalar(t, store.DB(), `SELECT count(*) FROM donation_keys WHERE tokens_reserved<>zeroblob(16)`, 0)
	}
	checkUpgradedLedger(t, store)
}

// Every retained seven-day event remains a source fact for the new boards.
func checkReleasedNetTotals(t *testing.T, database *sql.DB) {
	t.Helper()
	upgradeScalar(t, database, `SELECT phase FROM game_rank_net_rebuild WHERE id=1`, 2)
	expected := map[string]*big.Int{}
	rows, err := database.Query(`SELECT user_id,game_key,loss_sign,loss_mag FROM game_rank_events WHERE loss_sign IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	for rows.Next() {
		var user int64
		var game string
		var sign int
		var raw []byte
		if err := rows.Scan(&user, &game, &sign, &raw); err != nil {
			t.Fatal(err)
		}
		events++
		amount := new(big.Int).SetBytes(raw)
		amount.Mul(amount, big.NewInt(int64(-sign)))
		boards := []string{"game_net_profit"}
		if game == "fishing" {
			boards = append(boards, "fishing_net_profit")
		}
		if game == "blackjack" {
			boards = append(boards, "blackjack_net_profit")
		}
		for _, board := range boards {
			key := fmt.Sprintf("%d/%s", user, board)
			if expected[key] == nil {
				expected[key] = new(big.Int)
			}
			expected[key].Add(expected[key], amount)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("source fixture has no seven-day net facts")
	}
	rows, err = database.Query(`SELECT user_id,board,amount_sign,amount_mag FROM game_rank_totals WHERE board IN ('game_net_profit','fishing_net_profit','blackjack_net_profit') AND window='7d'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var user int64
		var board string
		var sign int
		var raw []byte
		if err := rows.Scan(&user, &board, &sign, &raw); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("%d/%s", user, board)
		amount := new(big.Int).SetBytes(raw)
		amount.Mul(amount, big.NewInt(int64(sign)))
		want := expected[key]
		if want == nil {
			want = new(big.Int)
		}
		if amount.Cmp(want) != 0 {
			t.Fatalf("retained net total %s: got %s want %s", key, amount, want)
		}
		delete(expected, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for key, amount := range expected {
		if amount.Sign() != 0 {
			t.Fatalf("retained net total missing: %s", key)
		}
	}
}
