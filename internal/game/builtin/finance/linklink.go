package finance

import (
	"context"
	"database/sql"
	"math/big"

	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type linkLinkPort struct{}

func (linkLinkPort) Entry(ctx context.Context, tx *sql.Tx, input ports.Entry) error {
	if !validEntry(ctx, tx, input, "ll_") || input.Meta.ActorUserID != input.UserID {
		return ledger.ErrInvalidPlan
	}
	var userID, price, gamePaid int64
	var version int
	var operation, state string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,price_milli,operation_id,state,rules_version,game_paid_milli FROM game_linklink_sessions WHERE id=?`, input.ResourceID).Scan(&userID, &price, &operation, &state, &version, &gamePaid); err != nil {
		return err
	}
	if userID != input.UserID || operation != input.Meta.OperationID || state != "active" || input.Amount.Big().Cmp(big.NewInt(price)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(gamePaid)) != 0 {
		return ledger.ErrInvalidPlan
	}
	if version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		platforms, err := codedAccounts(ctx, tx, "platform")
		if err != nil {
			return err
		}
		payment, err := entryPayment(input)
		if err != nil {
			return err
		}
		plan, err := ledger.NewLinkLinkEntryWithPayment(input.Meta, input.ResourceID, wallets, platforms, payment)
		if err != nil {
			return err
		}
		_, err = ledger.Apply(ctx, tx, plan)
		return err
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return err
	}
	plan, err := ledger.NewLinkLinkEntry(input.Meta, input.ResourceID, user.ID, platform.ID, input.Amount)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}
