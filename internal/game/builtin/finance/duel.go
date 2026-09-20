package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type duelPort struct{ game string }
type duelFunding struct {
	accounts                        ledger.AccountPair
	user                            int64
	ticket, gamePaid                int64
	operation, mode, terms, content string
}

func (p duelPort) validID(id string, queue bool) bool {
	prefix := "bid_"
	if p.game == "likes" {
		prefix = "lik_"
	} else if p.game != "bidding" {
		return false
	}
	if queue {
		prefix = prefix[:3] + "q_"
	}
	return db.ValidateOpaqueID(id, prefix)
}
func validDuelMeta(meta ledger.Meta) bool {
	return db.ValidateOpaqueID(meta.OperationID, "op_") && meta.ActorUserID >= 0 && meta.CreatedAt >= 0 && meta.CreatedAt <= 253399708799
}

func (p duelPort) queue(ctx context.Context, tx *sql.Tx, input ports.Entry) (duelFunding, error) {
	var f duelFunding
	if !p.validID(input.ResourceID, true) {
		return f, ledger.ErrInvalidPlan
	}
	var game string
	var remaining []byte
	err := tx.QueryRowContext(ctx, `SELECT game_key,user_id,ticket_milli,game_paid_milli,general_account_id,game_account_id,reservation_operation_id,mode,terms_hash,content_hash,ledger_rows_remaining FROM game_duel_queue WHERE id=?`, input.ResourceID).
		Scan(&game, &f.user, &f.ticket, &f.gamePaid, &f.accounts.General, &f.accounts.Game, &f.operation, &f.mode, &f.terms, &f.content, &remaining)
	if err != nil {
		return f, err
	}
	n, err := db.DecodeU128(remaining)
	if err != nil || n.Big().Cmp(big.NewInt(1)) != 0 || game != p.game || f.user != input.UserID || input.Amount.Big().Cmp(big.NewInt(f.ticket)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(f.gamePaid)) != 0 {
		return f, ledger.ErrInvalidPlan
	}
	actual, err := codedAccounts(ctx, tx, "duel-queue:"+input.ResourceID)
	if err != nil || actual != f.accounts {
		return f, ledger.ErrInvalidPlan
	}
	return f, nil
}

func (p duelPort) createEscrow(ctx context.Context, tx *sql.Tx, id string, queue bool, now int64) (ledger.AccountPair, error) {
	var accounts ledger.AccountPair
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		var account ledger.Account
		var err error
		if queue {
			account, err = ledger.CreateDuelQueueAssetAccount(ctx, tx, id, asset, now)
		} else {
			account, err = ledger.CreateDuelSessionAssetAccount(ctx, tx, id, asset, now)
		}
		if err != nil {
			return accounts, err
		}
		if asset == ledger.General {
			accounts.General = account.ID
		} else {
			accounts.Game = account.ID
		}
	}
	return accounts, nil
}

func (p duelPort) QueueReserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.DuelAccountMutation) error {
	if ctx == nil || tx == nil || write == nil || !p.validID(input.ResourceID, true) || !validDuelMeta(input.Meta) || input.UserID <= 0 || input.Meta.ActorUserID != input.UserID {
		return ledger.ErrInvalidPlan
	}
	wallets, err := walletAccounts(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	general, err := ledger.ReadAccount(ctx, tx, wallets.General)
	if err != nil {
		return err
	}
	game, err := ledger.ReadAccount(ctx, tx, wallets.Game)
	if err != nil {
		return err
	}
	payment, err := ledger.SplitGamePayment(input.Amount, general.Balance, game.Balance)
	if err != nil {
		return err
	}
	if payment.Game.Big().Cmp(input.GamePaid.Big()) != 0 {
		return ledger.ErrInvalidPlan
	}
	accounts, err := p.createEscrow(ctx, tx, input.ResourceID, true, input.Meta.CreatedAt)
	if err != nil {
		return err
	}
	ref, err := ledger.DuelQueueReservation(input.ResourceID)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, accounts); err != nil {
			return err
		}
		f, err := p.queue(ctx, tx, input)
		if err != nil {
			return err
		}
		if f.accounts != accounts || f.operation != input.Meta.OperationID {
			return ledger.ErrInvalidPlan
		}
		return nil
	}); err != nil {
		return err
	}
	plan, err := ledger.NewDuelQueueReserve(input.Meta, input.ResourceID, wallets, accounts, payment)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	if err != nil {
		return err
	}
	f, err := p.queue(ctx, tx, input)
	if err != nil {
		return err
	}
	tasks, err := p.onboardingTasks(f.mode)
	if err != nil {
		return err
	}
	return p.onboardingPort().reserveTasks(ctx, tx, input.UserID, tasks, onboardingParent{column: "duel_queue_id", id: input.ResourceID}, input.Meta.CreatedAt)
}

func (p duelPort) QueueRelease(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if ctx == nil || tx == nil || write == nil || !validDuelMeta(input.Meta) || input.Meta.ActorUserID != 0 && input.Meta.ActorUserID != input.UserID {
		return ledger.ErrInvalidPlan
	}
	f, err := p.queue(ctx, tx, input)
	if err != nil {
		return err
	}
	wallets, err := walletAccounts(ctx, tx, f.user)
	if err != nil {
		return err
	}
	payment, err := entryPayment(input)
	if err != nil {
		return err
	}
	plan, err := ledger.NewDuelQueueRelease(input.Meta, input.ResourceID, f.accounts, wallets, payment)
	if err != nil {
		return err
	}
	ref, err := ledger.DuelQueueReservation(input.ResourceID)
	if err != nil {
		return err
	}
	if err := p.onboardingPort().release(ctx, tx, onboardingParent{column: "duel_queue_id", id: input.ResourceID}, input.UserID); err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func (p duelPort) SessionStart(ctx context.Context, tx *sql.Tx, input ports.DuelStart, write ports.DuelAccountMutation) error {
	if ctx == nil || tx == nil || write == nil || !p.validID(input.SessionID, false) || !validDuelMeta(input.Meta) || input.Meta.ActorUserID != 0 || input.Queues[0].UserID == input.Queues[1].UserID {
		return ledger.ErrInvalidPlan
	}
	funding := [2]duelFunding{}
	queues := [2]ledger.DuelQueuePayment{}
	for i, q := range input.Queues {
		f, err := p.queue(ctx, tx, ports.Entry{ResourceID: q.QueueID, UserID: q.UserID, Amount: q.Amount, GamePaid: q.GamePaid})
		if err != nil {
			return err
		}
		if i > 0 && (f.terms != funding[0].terms || f.content != funding[0].content || f.mode != funding[0].mode || f.ticket != funding[0].ticket) {
			return ledger.ErrInvalidPlan
		}
		funding[i] = f
		queues[i] = ledger.DuelQueuePayment{QueueID: q.QueueID, Accounts: f.accounts, Payment: ledger.Payment{General: ledger.AmountFromMilli(f.ticket - f.gamePaid), Game: ledger.AmountFromMilli(f.gamePaid)}}
	}
	accounts, err := p.createEscrow(ctx, tx, input.SessionID, false, input.Meta.CreatedAt)
	if err != nil {
		return err
	}
	plan, err := ledger.NewDuelSessionStart(input.Meta, input.SessionID, accounts, queues)
	if err != nil {
		return err
	}
	primary := input.Queues[0].QueueID
	if input.Queues[1].QueueID < primary {
		primary = input.Queues[1].QueueID
	}
	ref, err := ledger.DuelQueueReservation(primary)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, accounts); err != nil {
			return err
		}
		var game, mode, terms, content, state string
		var actual ledger.AccountPair
		var ticket int64
		if err := tx.QueryRowContext(ctx, `SELECT game_key,mode,terms_hash,content_hash,state,ticket_milli,general_account_id,game_account_id FROM game_duel_sessions WHERE id=?`, input.SessionID).Scan(&game, &mode, &terms, &content, &state, &ticket, &actual.General, &actual.Game); err != nil {
			return err
		}
		if game != p.game || mode != funding[0].mode || terms != funding[0].terms || content != funding[0].content || state != "active" || ticket != funding[0].ticket || actual != accounts {
			return ledger.ErrInvalidPlan
		}
		for seat, f := range funding {
			var user, general, game int64
			if err := tx.QueryRowContext(ctx, `SELECT user_id,general_paid_milli,game_paid_milli FROM game_duel_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&user, &general, &game); err != nil {
				return err
			}
			if user != f.user || game != f.gamePaid || general != f.ticket-f.gamePaid {
				return ledger.ErrInvalidPlan
			}
		}
		return nil
	})
	return err
}

func (p duelPort) Terminal(ctx context.Context, tx *sql.Tx, input ports.DuelFinish, write ports.DuelTerminalMutation) error {
	if ctx == nil || tx == nil || write == nil || !p.validID(input.SessionID, false) || !validDuelMeta(input.Meta) || input.Meta.ActorUserID != 0 {
		return ledger.ErrInvalidPlan
	}
	var game, state string
	var ticket int64
	var accounts ledger.AccountPair
	var rates ledger.DuelRates
	if err := tx.QueryRowContext(ctx, `SELECT game_key,state,ticket_milli,platform_bp,welfare_bp,thursday_bp,general_account_id,game_account_id FROM game_duel_sessions WHERE id=?`, input.SessionID).Scan(&game, &state, &ticket, &rates.Platform, &rates.Welfare, &rates.Thursday, &accounts.General, &accounts.Game); err != nil {
		return err
	}
	if game != p.game || state != "active" {
		return ledger.ErrInvalidPlan
	}
	seats := [2]ledger.DuelSeatPayment{}
	for seat := range 2 {
		var user, general, game int64
		if err := tx.QueryRowContext(ctx, `SELECT user_id,general_paid_milli,game_paid_milli FROM game_duel_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&user, &general, &game); err != nil {
			return err
		}
		if general+game != ticket {
			return ledger.ErrInvalidPlan
		}
		wallets, err := walletAccounts(ctx, tx, user)
		if err != nil {
			return err
		}
		seats[seat] = ledger.DuelSeatPayment{Wallets: wallets, Payment: ledger.Payment{General: ledger.AmountFromMilli(general), Game: ledger.AmountFromMilli(game)}}
	}
	cuts, err := ledger.DuelAmounts(ledger.AmountFromMilli(ticket), rates)
	if err != nil {
		return err
	}
	dest := ledger.DuelDestinations{}
	if input.Winner == nil {
		cuts = ledger.DuelCuts{}
	} else {
		dest.External, err = codedAccounts(ctx, tx, "external")
		if err != nil {
			return err
		}
		if cuts.Platform.Sign() > 0 {
			account, err := ledger.CodedAccount(ctx, tx, "platform")
			if err != nil {
				return err
			}
			dest.Platform = account.ID
		}
		if cuts.Welfare.Sign() > 0 {
			if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
				return err
			}
			dest.Welfare = input.WelfareAccountID
		}
		if cuts.Thursday.Sign() > 0 {
			if err := validatePool(ctx, tx, input.ThursdayAccountID, "thursday"); err != nil {
				return err
			}
			dest.Thursday = input.ThursdayAccountID
		}
	}
	plan, err := ledger.NewDuelTerminal(input.Meta, input.SessionID, accounts, seats, input.Winner, rates, dest)
	if err != nil {
		return err
	}
	ref, err := ledger.DuelSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, cuts); err != nil {
			return err
		}
		var state, operation string
		var winner sql.NullInt64
		var prize, platform, welfare, thursday int64
		if err := tx.QueryRowContext(ctx, `SELECT state,terminal_operation_id,winner_seat,prize_milli,platform_milli,welfare_milli,thursday_milli FROM game_duel_sessions WHERE id=?`, input.SessionID).Scan(&state, &operation, &winner, &prize, &platform, &welfare, &thursday); err != nil {
			return err
		}
		if state != "terminal" || operation != input.Meta.OperationID || winner.Valid != (input.Winner != nil) || input.Winner != nil && winner.Int64 != int64(*input.Winner) || cuts.Prize.Big().Cmp(big.NewInt(prize)) != 0 || cuts.Platform.Big().Cmp(big.NewInt(platform)) != 0 || cuts.Welfare.Big().Cmp(big.NewInt(welfare)) != 0 || cuts.Thursday.Big().Cmp(big.NewInt(thursday)) != 0 {
			return ledger.ErrInvalidPlan
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.finishOnboarding(ctx, tx, input)
}
