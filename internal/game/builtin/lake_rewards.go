package builtin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// Each account and location has one immutable reward identity.
func lakeCollectionReward(ctx context.Context, tx *sql.Tx, user int64, location string, at int64) error {
	digest := sha256.Sum256([]byte(fmt.Sprintf("lake-collection:%d:%s", user, location)))
	operation := "op_" + base64.RawURLEncoding.EncodeToString(digest[:16])
	wallet, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return err
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		return err
	}
	plan, err := ledger.NewLakeCollectionReward(ledger.Meta{OperationID: operation, ActorUserID: user, CreatedAt: at}, ledger.AccountPair{General: wallet.ID, Game: game.ID}, external.ID)
	if err != nil {
		return err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return err
	}
	return authz.PromoteDonationLevelTx(ctx, tx, user, at)
}
