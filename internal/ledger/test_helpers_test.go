package ledger

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const ledgerTestNow int64 = 1_700_000_000

func openLedgerTestStore(t *testing.T) *db.Store {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "ledger.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open generation two store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func beginLedgerTestTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func seedLedgerUser(t *testing.T, tx *sql.Tx, label string) (int64, Account) {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	result, err := tx.Exec(`
INSERT INTO users(
 discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		"ledger-"+label, label, zero, zero, zero, zero, zero, zero, zero, zero, ledgerTestNow, ledgerTestNow)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	account, err := CreateUserAccount(context.Background(), tx, userID, ledgerTestNow)
	if err != nil {
		t.Fatalf("create user account: %v", err)
	}
	return userID, account
}

func mustLedgerID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatalf("generate %s id: %v", prefix, err)
	}
	return id
}

func mustU128(t *testing.T, decimal string) db.U128 {
	t.Helper()
	value, err := db.ParseU128Decimal(decimal)
	if err != nil {
		t.Fatalf("parse U128 %s: %v", decimal, err)
	}
	return value
}

func mustAmount(t *testing.T, decimal string) Amount {
	t.Helper()
	value, err := ParseAmount(decimal)
	if err != nil {
		t.Fatalf("parse amount %s: %v", decimal, err)
	}
	return value
}
