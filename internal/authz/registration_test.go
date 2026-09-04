package authz

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type walletHook struct {
	err error
}

func (hook walletHook) EnsureUserWallet(ctx context.Context, tx *sql.Tx, userID, createdAt int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO registration_wallet_marks(user_id,created_at) VALUES(?,?)`, userID, createdAt); err != nil {
		return err
	}
	return hook.err
}

func insertRegistrationUser(t *testing.T, tx *sql.Tx, discord string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	revision := make([]byte, 16)
	revision[15] = 1
	result, err := tx.Exec(`
INSERT INTO users(
 discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,?, ?,?,?,?,?,?,?,?, ?,?)`,
		discord, "register-"+discord, zero, zero, zero, zero, zero, zero, zero, revision, authTestNow, authTestNow)
	if err != nil {
		t.Fatalf("insert registration user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInitializeRegistrationAtomicSuccessAndFailure(t *testing.T) {
	store := openAuthStore(t)
	if _, err := store.DB().Exec(`CREATE TABLE registration_wallet_marks(user_id INTEGER PRIMARY KEY,created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	tx := beginAuthTx(t, store.DB())
	userID := insertRegistrationUser(t, tx, "5001")
	if err := InitializeRegistration(context.Background(), tx, userID, authTestNow, walletHook{}); err != nil {
		t.Fatalf("initialize registration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := store.DB().QueryRow(`SELECT generation FROM caller_keys WHERE user_id=?`, userID).Scan(&generation); err != nil || generation != 0 {
		t.Fatalf("caller key generation=(%d,%v)", generation, err)
	}
	var marks int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM registration_wallet_marks WHERE user_id=?`, userID).Scan(&marks); err != nil || marks != 1 {
		t.Fatalf("wallet marks=(%d,%v)", marks, err)
	}

	tx = beginAuthTx(t, store.DB())
	failedUserID := insertRegistrationUser(t, tx, "5002")
	walletErr := errors.New("wallet unavailable")
	if err := InitializeRegistration(context.Background(), tx, failedUserID, authTestNow, walletHook{err: walletErr}); !errors.Is(err, walletErr) {
		t.Fatalf("wallet failure=%v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for table := range map[string]struct{}{"users": {}, "caller_keys": {}, "registration_wallet_marks": {}} {
		var count int
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+map[string]string{
			"users": "id", "caller_keys": "user_id", "registration_wallet_marks": "user_id",
		}[table]+`=?`, failedUserID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d failed registration rows", table, count)
		}
	}
}

func TestInitializeRegistrationRejectsNonzeroCallerKeyState(t *testing.T) {
	store := openAuthStore(t)
	if _, err := store.DB().Exec(`CREATE TABLE registration_wallet_marks(user_id INTEGER PRIMARY KEY,created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	tx := beginAuthTx(t, store.DB())
	userID := insertRegistrationUser(t, tx, "5003")
	keyHash := make([]byte, 32)
	keyHash[31] = 1
	if _, err := tx.Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,'head','tail',?,?)`, userID, keyHash, authTestNow, authTestNow); err != nil {
		t.Fatal(err)
	}
	if err := InitializeRegistration(context.Background(), tx, userID, authTestNow, walletHook{}); !errors.Is(err, ErrRegistrationInvariant) {
		t.Fatalf("nonzero state error=%v", err)
	}
	_ = tx.Rollback()
}
