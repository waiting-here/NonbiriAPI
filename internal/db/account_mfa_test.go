package db

import (
	"bytes"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type mfaAuditCodec struct {
	*secret.Vault
	calls int
}

func (codec *mfaAuditCodec) OpenAccountMFA(envelope string, contextID []byte) ([]byte, error) {
	codec.calls++
	if envelope != "synthetic-envelope" || !bytes.Equal(contextID, bytes.Repeat([]byte{0x41}, 16)) {
		return nil, errors.New("synthetic authentication failure")
	}
	return bytes.Repeat([]byte{0x51}, 20), nil
}

func TestAccountMFAOfflineAuditCoversBothSecretTablesWithoutStartupScan(t *testing.T) {
	path := bootstrapTestPath(t, "mfa-audit.sqlite")
	codec := &mfaAuditCodec{Vault: bootstrapTestVault(t)}
	store, err := Open(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "mfa-audit", 0, 1)
	hostileMustExec(t, store.DB(), "INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES('synthetic-session',?,1,99,99,1)", user)
	contextID := bytes.Repeat([]byte{0x41}, 16)
	hostileMustExec(t, store.DB(), "INSERT INTO account_mfa_credentials VALUES(?,?,'synthetic-envelope',1)", user, contextID)
	hostileMustExec(t, store.DB(), "INSERT INTO account_mfa_pending VALUES(?,'synthetic-session',?,'synthetic-envelope',99)", user, contextID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	if codec.calls != 0 {
		t.Fatal("ordinary startup scanned MFA secrets", codec.calls)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyContext(t.Context(), path, codec, nil); err != nil {
		t.Fatal(err)
	}
	if codec.calls != 2 {
		t.Fatal("offline audit missed a persisted MFA envelope", codec.calls)
	}
	for _, table := range []string{"account_mfa_credentials", "account_mfa_pending"} {
		store, err = Open(path, codec)
		if err != nil {
			t.Fatal(err)
		}
		hostileMustExec(t, store.DB(), "UPDATE "+table+" SET encrypted_secret='corrupt-envelope'")
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := VerifyContext(t.Context(), path, codec, nil); err == nil {
			t.Fatal("corrupt MFA envelope accepted", table)
		}
		store, err = Open(path, codec)
		if err != nil {
			t.Fatal(err)
		}
		hostileMustExec(t, store.DB(), "UPDATE "+table+" SET encrypted_secret='synthetic-envelope'")
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyContext(t.Context(), path, codec.Vault, nil); err == nil {
		t.Fatal("nonempty MFA material accepted without a purpose codec")
	}
}

func TestAccountMFASchemaRevokesProofsAndCascadesDeletedIdentity(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	user := hostileInsertUser(t, database, "mfa-revoke", 0, 1)
	hostileMustExec(t, database, "INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES('mfa-session',?,1,99,99,1)", user)
	hostileMustExec(t, database, "INSERT INTO account_mfa_credentials VALUES(?,zeroblob(16),'envelope',1)", user)
	hostileMustExec(t, database, "INSERT INTO account_mfa_pending VALUES(?,'mfa-session',zeroblob(16),'pending',99)", user)
	hostileMustExec(t, database, "INSERT INTO account_mfa_proofs VALUES('mfa-session',?,'generation',99)", user)
	hostileMustExec(t, database, "INSERT INTO account_mfa_attempts(user_id) VALUES(?)", user)
	hostileMustExec(t, database, "UPDATE users SET is_banned=1 WHERE id=?", user)
	var proofs, pending, credentials int
	if err := database.QueryRow("SELECT (SELECT count(*) FROM account_mfa_proofs),(SELECT count(*) FROM account_mfa_pending),(SELECT count(*) FROM account_mfa_credentials)").Scan(&proofs, &pending, &credentials); err != nil || proofs != 0 || pending != 0 || credentials != 1 {
		t.Fatal(proofs, pending, credentials, err)
	}
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
	var remaining int
	if err := database.QueryRow("SELECT (SELECT count(*) FROM account_mfa_credentials)+(SELECT count(*) FROM account_mfa_attempts)").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal(remaining, err)
	}
}
