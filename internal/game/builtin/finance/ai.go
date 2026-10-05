package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (p duelPort) AIStart(ctx context.Context, tx *sql.Tx, input ports.AIStart, write ports.AIStartMutation) error {
	if p.game != "bidding" || write == nil || !p.validID(input.SessionID, false) || !validDuelMeta(input.Meta) || input.UserID <= 0 || input.Meta.ActorUserID != input.UserID || input.Ticket.Sign() < 0 {
		return ledger.ErrInvalidPlan
	}
	wallets, err := walletAccounts(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	payment := ledger.Payment{}
	if input.Ticket.Sign() > 0 {
		general, err := ledger.ReadAccount(ctx, tx, wallets.General)
		if err != nil {
			return err
		}
		game, err := ledger.ReadAccount(ctx, tx, wallets.Game)
		if err != nil {
			return err
		}
		payment, err = ledger.SplitGamePayment(input.Ticket, general.Balance, game.Balance)
		if err != nil {
			return err
		}
	}
	accounts, err := p.createEscrow(ctx, tx, input.SessionID, false, input.Meta.CreatedAt)
	if err != nil {
		return err
	}
	hold := db.U128{}
	if input.Ticket.Sign() > 0 || input.MayReward {
		hold, _ = db.U128FromBig(big.NewInt(1))
	}
	mutation := func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, accounts, payment, hold); err != nil {
			return err
		}
		var n int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_duel_sessions s JOIN game_duel_seats h ON h.session_id=s.id JOIN game_duel_seats b ON b.session_id=s.id AND b.seat_no<>h.seat_no WHERE s.id=? AND s.game_key='bidding' AND s.economy='ai_challenge' AND s.state='active' AND s.ticket_milli=? AND s.general_account_id=? AND s.game_account_id=? AND s.ledger_rows_remaining=? AND h.participant_kind='human' AND h.user_id=? AND h.general_paid_milli=? AND h.game_paid_milli=? AND b.participant_kind='bot'`, input.SessionID, input.Ticket.Big().Int64(), accounts.General, accounts.Game, db.EncodeU128(hold), input.UserID, payment.General.Big().Int64(), payment.Game.Big().Int64()).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ledger.ErrInvalidPlan
		}
		return nil
	}
	if hold.Big().Sign() > 0 {
		ref, err := ledger.DuelSessionReservation(input.SessionID)
		if err != nil {
			return err
		}
		if err := ledger.Reserve(ctx, tx, ref, hold, mutation); err != nil {
			return err
		}
	} else if err := mutation(ctx, tx); err != nil {
		return err
	}
	if input.Ticket.IsZero() {
		return nil
	}
	plan, err := ledger.NewAITicket(input.Meta, input.SessionID, wallets, accounts, payment)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}

func (p duelPort) AITerminal(ctx context.Context, tx *sql.Tx, input ports.AIFinish, write ports.AITerminalMutation) error {
	if p.game != "bidding" || write == nil || !p.validID(input.SessionID, false) || !validDuelMeta(input.Meta) || input.Meta.ActorUserID != 0 || input.Reward.Sign() < 0 {
		return ledger.ErrInvalidPlan
	}
	var user, ticket, general, game int64
	var accounts ledger.AccountPair
	var remaining []byte
	err := tx.QueryRowContext(ctx, `SELECT h.user_id,s.ticket_milli,h.general_paid_milli,h.game_paid_milli,s.general_account_id,s.game_account_id,s.ledger_rows_remaining FROM game_duel_sessions s JOIN game_duel_seats h ON h.session_id=s.id AND h.participant_kind='human' WHERE s.id=? AND s.game_key='bidding' AND s.economy='ai_challenge' AND s.state='active'`, input.SessionID).Scan(&user, &ticket, &general, &game, &accounts.General, &accounts.Game, &remaining)
	if err != nil {
		return err
	}
	if general+game != ticket {
		return ledger.ErrInvalidPlan
	}
	hold, err := db.DecodeU128(remaining)
	if err != nil {
		return err
	}
	ref, err := ledger.DuelSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	if ticket == 0 && input.Reward.IsZero() {
		mutation := func(ctx context.Context, tx *sql.Tx) error { return write(ctx, tx, "") }
		if hold.Big().Sign() > 0 {
			return ledger.ReleaseReserved(ctx, tx, ref, hold, mutation)
		}
		return mutation(ctx, tx)
	}
	wallets, err := walletAccounts(ctx, tx, user)
	if err != nil {
		return err
	}
	var platforms ledger.AccountPair
	if ticket > 0 && !input.Cancelled {
		platforms, err = codedAccounts(ctx, tx, "platform")
		if err != nil {
			return err
		}
	}
	externalGame := int64(0)
	if input.Reward.Sign() > 0 {
		external, err := codedAccounts(ctx, tx, "external")
		if err != nil {
			return err
		}
		externalGame = external.Game
	}
	payment := ledger.Payment{General: ledger.AmountFromMilli(general), Game: ledger.AmountFromMilli(game)}
	plan, err := ledger.NewAITerminal(input.Meta, input.SessionID, accounts, wallets, platforms, externalGame, payment, input.Reward, input.Cancelled)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, input.Meta.OperationID); err != nil {
			return err
		}
		var n int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_duel_sessions s JOIN game_ai_sessions a ON a.session_id=s.id WHERE s.id=? AND s.state='terminal' AND s.terminal_operation_id=? AND a.reward_milli=? AND (s.outcome='system_cancelled')=?`, input.SessionID, input.Meta.OperationID, input.Reward.Big().Int64(), input.Cancelled).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ledger.ErrInvalidPlan
		}
		return nil
	})
	return err
}
