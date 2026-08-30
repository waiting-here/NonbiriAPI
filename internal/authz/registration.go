package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrRegistrationInvariant = errors.New("registration authority invariant failed")

// WalletRegistrationHook is implemented by the central ledger owner. B3 does
// not know or duplicate wallet/ledger SQL; it only invokes this narrow hook in
// the caller's registration transaction.
type WalletRegistrationHook interface {
	EnsureUserWallet(context.Context, *sql.Tx, int64, int64) error
}

// InitializeRegistration creates or verifies the generation-zero CallerKey
// state and invokes the central wallet hook in the caller's outer transaction.
// The caller must roll the transaction back on any error. No commit is
// performed here, so user, wallet and CallerKey state are all-or-nothing.
func InitializeRegistration(ctx context.Context, tx *sql.Tx, userID, createdAt int64, wallet WalletRegistrationHook) error {
	if ctx == nil || tx == nil || wallet == nil || userID <= 0 || createdAt < 0 || createdAt > maxUnixSecond {
		return ErrRegistrationInvariant
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO caller_keys(
 user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at
) VALUES(?,0,NULL,'','',NULL,?)
ON CONFLICT(user_id) DO NOTHING`, userID, createdAt); err != nil {
		return fmt.Errorf("initialize registration: caller key generation zero: %w", err)
	}

	var (
		generation   int64
		keyHash      []byte
		displayHead  string
		displayTail  string
		keyCreatedAt sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, `
SELECT generation,key_hash,display_head,display_tail,key_created_at
FROM caller_keys WHERE user_id=?`, userID).Scan(
		&generation, &keyHash, &displayHead, &displayTail, &keyCreatedAt,
	); err != nil {
		return fmt.Errorf("initialize registration: verify caller key state: %w", err)
	}
	if generation != 0 || keyHash != nil || displayHead != "" || displayTail != "" || keyCreatedAt.Valid {
		return ErrRegistrationInvariant
	}
	if err := wallet.EnsureUserWallet(ctx, tx, userID, createdAt); err != nil {
		return fmt.Errorf("initialize registration: wallet hook: %w", err)
	}
	return nil
}
