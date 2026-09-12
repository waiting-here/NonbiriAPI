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
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, err := ledger.CreateUserAssetAccount(ctx, tx, userID, asset, createdAt); err != nil {
			return err
		}
	}
	return nil
}
