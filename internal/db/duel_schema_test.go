package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestDuelExtensionRequiresExactSourceAndPreservesExistingRows(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(generationTwoWithoutDuelsSchema); err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	for key := range duelConfigDefaults() {
		if _, err := tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
	}
	zero := EncodeU128(U128{})
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('Existing account',?,?,?,?,?,?,?,?,100,101)`, zero, zero, zero, zero, zero, zero, zero, zero); err != nil {
		t.Fatal(err)
	}
	var beforeAccounts, beforeConfig string
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(json_array(id,kind,code,asset_type,balance_sign,hex(balance_mag))) FROM (SELECT * FROM credit_accounts ORDER BY id)`).Scan(&beforeAccounts); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(json_array(key,value,updated_at)) FROM (SELECT * FROM site_config ORDER BY key)`).Scan(&beforeConfig); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDuelExtension(ctx, tx); err != nil {
		t.Fatal(err)
	}
	var afterAccounts, afterConfig, name string
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(json_array(id,kind,code,asset_type,balance_sign,hex(balance_mag))) FROM (SELECT * FROM credit_accounts ORDER BY id)`).Scan(&afterAccounts); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(json_array(key,value,updated_at)) FROM (SELECT * FROM site_config WHERE key NOT LIKE 'game_bidding_%' AND key NOT LIKE 'game_likes_%' ORDER BY key)`).Scan(&afterConfig); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE created_at=100 AND updated_at=101`).Scan(&name); err != nil || name != "Existing account" || beforeAccounts != afterAccounts || beforeConfig != afterConfig {
		t.Fatal("existing account, ledger or configuration changed", err)
	}
	for _, table := range []string{"catalogs", "queue", "sessions", "seats", "user_slots", "rounds", "anonymous", "anonymous_rounds"} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM game_duel_"+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	for key, value := range duelConfigDefaults() {
		var got string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&got); err != nil || got != value {
			t.Fatalf("config %s: %s %v", key, got, err)
		}
	}
	if err := ApplyDuelExtension(ctx, tx); err == nil {
		t.Fatal("partial or already upgraded source accepted")
	}
	var writable int
	if err := tx.QueryRowContext(ctx, `PRAGMA writable_schema`).Scan(&writable); err != nil || writable != 0 {
		t.Fatal("writable schema remained enabled")
	}
	var integrity string
	if err := tx.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s %v", integrity, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
