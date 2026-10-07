package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type catchPort struct{}

func (catchPort) Start(ctx context.Context, tx *sql.Tx, input ports.SoloStart, write ports.SoloStartMutation) error {
	if write == nil || !db.ValidateOpaqueID(input.SessionID, "sc_") || input.UserID <= 0 || input.Meta.ActorUserID != input.UserID || input.Ticket.Sign() < 0 {
		return ledger.ErrInvalidPlan
	}
	general, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, input.UserID, ledger.Game)
	if err != nil {
		return err
	}
	payment, err := ledger.SplitGamePayment(input.Ticket, general.Balance, game.Balance)
	if err != nil {
		return err
	}
	hold := db.U128{}
	if input.Ticket.Sign() > 0 || input.MayReward {
		hold, _ = db.U128FromBig(big.NewInt(1))
	}
	mutation := func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, payment, hold); err != nil {
			return err
		}
		var n int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_catch_sessions WHERE id=? AND user_id=? AND price_milli=? AND general_paid_milli=? AND game_paid_milli=? AND ledger_rows_remaining=? AND status='paused'`, input.SessionID, input.UserID, input.Ticket.Big().Int64(), payment.General.Big().Int64(), payment.Game.Big().Int64(), db.EncodeU128(hold)).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ledger.ErrInvalidPlan
		}
		return nil
	}
	ref, err := ledger.CatchSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	if hold.Big().Sign() > 0 {
		err = ledger.Reserve(ctx, tx, ref, hold, mutation)
	} else {
		err = mutation(ctx, tx)
	}
	if err != nil || input.Ticket.IsZero() {
		return err
	}
	platforms, err := codedAccounts(ctx, tx, "platform")
	if err != nil {
		return err
	}
	plan, err := ledger.NewCatchTicket(input.Meta, input.SessionID, ledger.AccountPair{General: general.ID, Game: game.ID}, platforms, payment)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}

func (catchPort) Finish(ctx context.Context, tx *sql.Tx, input ports.SoloFinish, write ports.SoloTerminalMutation) error {
	if write == nil || !db.ValidateOpaqueID(input.SessionID, "sc_") || input.Meta.ActorUserID != 0 || input.Reward.Sign() < 0 || input.Cancelled && !input.Reward.IsZero() {
		return ledger.ErrInvalidPlan
	}
	var user, price, general, game, maxReward int64
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT user_id,price_milli,general_paid_milli,game_paid_milli,first_reward_milli,ledger_rows_remaining FROM game_catch_sessions WHERE id=? AND status IN ('playing','paused')`, input.SessionID).Scan(&user, &price, &general, &game, &maxReward, &raw)
	if err != nil {
		return err
	}
	if general+game != price || input.Reward.Big().Cmp(big.NewInt(maxReward)) > 0 {
		return ledger.ErrInvalidPlan
	}
	hold, err := db.DecodeU128(raw)
	if err != nil {
		return err
	}
	ref, err := ledger.CatchSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	posting := !input.Reward.IsZero() || input.Cancelled && price > 0
	mutation := func(ctx context.Context, tx *sql.Tx) error {
		op := ""
		if posting {
			op = input.Meta.OperationID
		}
		if err := write(ctx, tx, op); err != nil {
			return err
		}
		var n int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_catch_sessions WHERE id=? AND status NOT IN ('playing','paused') AND ledger_rows_remaining=zeroblob(16) AND reward_milli=? AND (status='cancelled')=? AND COALESCE(terminal_operation_id,'')=?`, input.SessionID, input.Reward.Big().Int64(), input.Cancelled, op).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ledger.ErrInvalidPlan
		}
		if !input.Cancelled && price > 0 {
			return recordRank(ctx, tx, user, "steadycatch", input.SessionID, input.Meta.CreatedAt, big.NewInt(price), new(big.Int))
		}
		return nil
	}
	if !posting {
		if hold.Big().Sign() > 0 {
			return ledger.ReleaseReserved(ctx, tx, ref, hold, mutation)
		}
		return mutation(ctx, tx)
	}
	wallets, err := walletAccounts(ctx, tx, user)
	if err != nil {
		return err
	}
	var plan ledger.Plan
	if input.Cancelled {
		platforms, e := codedAccounts(ctx, tx, "platform")
		if e != nil {
			return e
		}
		plan, err = ledger.NewCatchRefund(input.Meta, input.SessionID, wallets, platforms, ledger.Payment{General: ledger.AmountFromMilli(general), Game: ledger.AmountFromMilli(game)})
	} else {
		external, e := codedAccounts(ctx, tx, "external")
		if e != nil {
			return e
		}
		plan, err = ledger.NewCatchReward(input.Meta, input.SessionID, wallets.Game, external.Game, input.Reward)
	}
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, mutation)
	return err
}
