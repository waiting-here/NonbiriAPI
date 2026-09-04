package authz

import (
	"context"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func requireGenerationZeroCallerKey(t *testing.T, tx *sql.Tx, userID int64) {
	t.Helper()
	var (
		generation   int64
		keyHash      []byte
		displayHead  string
		displayTail  string
		keyCreatedAt sql.NullInt64
	)
	if err := tx.QueryRow(`
SELECT generation,key_hash,display_head,display_tail,key_created_at
FROM caller_keys WHERE user_id=?`, userID).Scan(
		&generation, &keyHash, &displayHead, &displayTail, &keyCreatedAt,
	); err != nil {
		t.Fatalf("read generation-zero caller key: %v", err)
	}
	if generation != 0 || keyHash != nil || displayHead != "" || displayTail != "" || keyCreatedAt.Valid {
		t.Fatalf("caller key is not generation zero: generation=%d hash=%x head=%q tail=%q created=%+v",
			generation, keyHash, displayHead, displayTail, keyCreatedAt)
	}
}

func requireRegistrationLedgerState(t *testing.T, tx *sql.Tx, userID int64) int64 {
	t.Helper()
	ctx := context.Background()
	requireGenerationZeroCallerKey(t, tx, userID)
	account, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		t.Fatalf("read registration wallet: %v", err)
	}
	if account.ID <= 0 || account.Kind != ledger.AccountUser || account.UserID != userID ||
		!account.Balance.IsZero() || account.CreatedAt != authTestNow || account.UpdatedAt != authTestNow {
		t.Fatalf("registration wallet=%+v", account)
	}
	if err := ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("validate registration ledger: %v", err)
	}
	if _, err := ledger.RecoverNonterminal(ctx, tx); err != nil {
		t.Fatalf("recover registration ledger: %v", err)
	}
	return account.ID
}

func requireRegistrationRowCounts(t *testing.T, tx *sql.Tx, userID int64, callerKeys, wallets int) {
	t.Helper()
	var callerKeyCount, walletCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM caller_keys WHERE user_id=?`, userID).Scan(&callerKeyCount); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user' AND user_id=?`, userID).Scan(&walletCount); err != nil {
		t.Fatal(err)
	}
	if callerKeyCount != callerKeys || walletCount != wallets {
		t.Fatalf("registration rows=(caller_keys:%d,wallets:%d), want (%d,%d)",
			callerKeyCount, walletCount, callerKeys, wallets)
	}
}

func requireRolledBackRegistration(t *testing.T, database *sql.DB, userID int64) {
	t.Helper()
	tx := beginAuthTx(t, database)
	defer tx.Rollback()
	for table, ownerColumn := range map[string]string{
		"users": "id", "caller_keys": "user_id", "credit_accounts": "user_id",
	} {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+ownerColumn+`=?`, userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rolled-back registration row(s)", table, count)
		}
	}
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatalf("validate ledger after registration rollback: %v", err)
	}
	if _, err := ledger.RecoverNonterminal(context.Background(), tx); err != nil {
		t.Fatalf("recover ledger after registration rollback: %v", err)
	}
}

func TestLedgerWalletRegistrationHookCreatesCallerKeyAndWalletAtomically(t *testing.T) {
	store := openAuthStore(t)
	tx := beginAuthTx(t, store.DB())
	userID := insertRegistrationUser(t, tx, "ledger-registration-success")
	if err := InitializeRegistration(
		context.Background(), tx, userID, authTestNow, LedgerWalletRegistrationHook{},
	); err != nil {
		t.Fatalf("initialize registration with ledger: %v", err)
	}
	accountID := requireRegistrationLedgerState(t, tx, userID)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	verify := beginAuthTx(t, store.DB())
	defer verify.Rollback()
	if persistedID := requireRegistrationLedgerState(t, verify, userID); persistedID != accountID {
		t.Fatalf("persisted wallet id=%d, want %d", persistedID, accountID)
	}
}

func TestLedgerWalletRegistrationHookConvergesOnReplayAndExistingWallet(t *testing.T) {
	t.Run("replay", func(t *testing.T) {
		store := openAuthStore(t)
		tx := beginAuthTx(t, store.DB())
		userID := insertRegistrationUser(t, tx, "ledger-registration-replay")
		hook := LedgerWalletRegistrationHook{}
		if err := InitializeRegistration(context.Background(), tx, userID, authTestNow, hook); err != nil {
			t.Fatal(err)
		}
		firstAccountID := requireRegistrationLedgerState(t, tx, userID)
		if err := InitializeRegistration(context.Background(), tx, userID, authTestNow, hook); err != nil {
			t.Fatalf("replay registration: %v", err)
		}
		if replayedAccountID := requireRegistrationLedgerState(t, tx, userID); replayedAccountID != firstAccountID {
			t.Fatalf("replay wallet id=%d, want %d", replayedAccountID, firstAccountID)
		}
		requireRegistrationRowCounts(t, tx, userID, 1, 1)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("existing wallet", func(t *testing.T) {
		store := openAuthStore(t)
		tx := beginAuthTx(t, store.DB())
		userID := insertRegistrationUser(t, tx, "ledger-registration-existing")
		existing, err := ledger.CreateUserAccount(context.Background(), tx, userID, authTestNow)
		if err != nil {
			t.Fatal(err)
		}
		if err := InitializeRegistration(
			context.Background(), tx, userID, authTestNow, LedgerWalletRegistrationHook{},
		); err != nil {
			t.Fatalf("registration with existing wallet: %v", err)
		}
		if accountID := requireRegistrationLedgerState(t, tx, userID); accountID != existing.ID {
			t.Fatalf("converged wallet id=%d, want %d", accountID, existing.ID)
		}
		requireRegistrationRowCounts(t, tx, userID, 1, 1)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLedgerWalletRegistrationHookFailureRollsBackOuterTransaction(t *testing.T) {
	store := openAuthStore(t)
	if _, err := store.DB().Exec(`
CREATE TRIGGER authz_test_reject_user_wallet
BEFORE INSERT ON credit_accounts WHEN NEW.kind='user'
BEGIN
 SELECT RAISE(ABORT,'injected user wallet failure');
END`); err != nil {
		t.Fatal(err)
	}
	tx := beginAuthTx(t, store.DB())
	userID := insertRegistrationUser(t, tx, "ledger-registration-hook-failure")
	if err := InitializeRegistration(
		context.Background(), tx, userID, authTestNow, LedgerWalletRegistrationHook{},
	); err == nil {
		t.Fatal("real ledger hook failure was accepted")
	}
	// The hook failure aborts only its INSERT; this proves the caller-owned
	// outer rollback is what removes the earlier user and CallerKey writes.
	requireRegistrationRowCounts(t, tx, userID, 1, 0)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	requireRolledBackRegistration(t, store.DB(), userID)
}

func TestLedgerWalletRegistrationLaterTransactionFailureRollsBackEverything(t *testing.T) {
	store := openAuthStore(t)
	tx := beginAuthTx(t, store.DB())
	userID := insertRegistrationUser(t, tx, "ledger-registration-tx-failure")
	if err := InitializeRegistration(
		context.Background(), tx, userID, authTestNow, LedgerWalletRegistrationHook{},
	); err != nil {
		t.Fatal(err)
	}
	requireRegistrationLedgerState(t, tx, userID)
	if _, err := tx.Exec(`
INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at)
VALUES(?,0,NULL,'','',NULL,?)`, userID, authTestNow); err == nil {
		t.Fatal("injected later transaction failure did not fail")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	requireRolledBackRegistration(t, store.DB(), userID)
}
