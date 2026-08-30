package authz

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// LedgerWalletRegistrationHook is the production registration adapter for
// the central ledger. Its only capability is ensuring the registering user's
// wallet inside the caller-owned outer transaction.
type LedgerWalletRegistrationHook struct{}

var _ WalletRegistrationHook = LedgerWalletRegistrationHook{}

func (LedgerWalletRegistrationHook) EnsureUserWallet(ctx context.Context, tx *sql.Tx, userID, createdAt int64) error {
	_, err := ledger.CreateUserAccount(ctx, tx, userID, createdAt)
	return err
}
