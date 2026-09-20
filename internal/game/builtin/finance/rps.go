package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type rpsPort struct{ onboarding }

type queueFunding struct {
	account   int64
	operation string
	task      string
	version   int
	payment   ledger.Payment
}

func queueSource(ctx context.Context, tx *sql.Tx, input ports.Entry) (queueFunding, error) {
	var owner int64
	var raw, gameRaw []byte
	var funding queueFunding
	if err := tx.QueryRowContext(ctx, `SELECT user_id,account_id,reserved,reservation_operation_id,rules_version,game_paid,mode FROM game_rps_queue WHERE id=?`, input.ResourceID).Scan(&owner, &funding.account, &raw, &funding.operation, &funding.version, &gameRaw, &funding.task); err != nil {
		return queueFunding{}, err
	}
	reserved, err := db.DecodeU128(raw)
	gamePaid, gameErr := db.DecodeU128(gameRaw)
	if err != nil || gameErr != nil || owner != input.UserID || input.Amount.Big().Cmp(reserved.Big()) != 0 ||
		input.Amount.Sign() <= 0 || input.GamePaid.Big().Cmp(gamePaid.Big()) != 0 || funding.version < 1 || funding.version > 2 {
		return queueFunding{}, ledger.ErrInvalidPlan
	}
	funding.payment, err = entryPayment(input)
	return funding, err
}

func (port rpsPort) QueueReserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.AccountMutation) (int64, error) {
	if !validEntry(ctx, tx, input, "rpsq_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return 0, ledger.ErrInvalidPlan
	}
	account, err := ledger.CreateRPSQueueAccount(ctx, tx, input.ResourceID, input.Meta.CreatedAt)
	if err != nil {
		return 0, err
	}
	ref, err := ledger.RPSQueueReservation(input.ResourceID)
	if err != nil {
		return 0, err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	var funding queueFunding
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, account.ID); err != nil {
			return err
		}
		var err error
		funding, err = queueSource(ctx, tx, input)
		if err != nil {
			return err
		}
		if funding.account != account.ID || funding.operation != input.Meta.OperationID {
			return ledger.ErrInvalidPlan
		}
		return nil
	}); err != nil {
		return 0, err
	}
	var plan ledger.Plan
	if funding.version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return 0, err
		}
		gameReserve, err := ledger.CreateRPSQueueAssetAccount(ctx, tx, input.ResourceID, ledger.Game, input.Meta.CreatedAt)
		if err != nil {
			return 0, err
		}
		plan, err = ledger.NewRPSQueueReserveWithPayment(input.Meta, input.ResourceID, wallets, ledger.AccountPair{General: account.ID, Game: gameReserve.ID}, funding.payment)
		if err != nil {
			return 0, err
		}
	} else {
		user, err := ledger.UserAccount(ctx, tx, input.UserID)
		if err != nil {
			return 0, err
		}
		plan, err = ledger.NewRPSQueueReserve(input.Meta, input.ResourceID, user.ID, account.ID, input.Amount)
		if err != nil {
			return 0, err
		}
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return 0, err
	}
	if funding.version == 2 {
		if err := port.reserve(ctx, tx, input.UserID, funding.task, onboardingParent{column: "rps_queue_id", id: input.ResourceID}, input.Meta.CreatedAt); err != nil {
			return 0, err
		}
	}
	return account.ID, nil
}

func (port rpsPort) QueueRelease(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "rpsq_") || write == nil {
		return ledger.ErrInvalidPlan
	}
	funding, err := queueSource(ctx, tx, input)
	if err != nil {
		return err
	}
	var plan ledger.Plan
	if funding.version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		reserves, err := codedAccounts(ctx, tx, "rps-queue:"+input.ResourceID)
		if err != nil {
			return err
		}
		if reserves.General != funding.account {
			return ledger.ErrInvalidPlan
		}
		plan, err = ledger.NewRPSQueueReleaseWithPayment(input.Meta, input.ResourceID, reserves, wallets, funding.payment)
		if err != nil {
			return err
		}
	} else {
		user, err := ledger.UserAccount(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		plan, err = ledger.NewRPSQueueRelease(input.Meta, input.ResourceID, funding.account, user.ID, input.Amount)
		if err != nil {
			return err
		}
	}
	ref, err := ledger.RPSQueueReservation(input.ResourceID)
	if err != nil {
		return err
	}
	if funding.version == 2 {
		if err := port.release(ctx, tx, onboardingParent{column: "rps_queue_id", id: input.ResourceID}, input.UserID); err != nil {
			return err
		}
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func validSession(ctx context.Context, tx *sql.Tx, id string, meta ledger.Meta) bool {
	return ctx != nil && tx != nil && db.ValidateOpaqueID(id, "rps_") && db.ValidateOpaqueID(meta.OperationID, "op_") && meta.CreatedAt >= 0 && meta.CreatedAt <= 253402300799 && meta.ActorUserID >= 0
}

func (port rpsPort) SessionStart(ctx context.Context, tx *sql.Tx, input ports.SessionStart, write ports.AccountMutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || input.Meta.ActorUserID != 0 || write == nil || input.FutureRows.Big().Sign() <= 0 {
		return ledger.ErrInvalidPlan
	}
	queues := [3]ledger.RPSQueueInput{}
	payments := [3]ledger.RPSQueuePayment{}
	version := 0
	seen := map[int64]bool{}
	for i, queue := range input.Queues {
		if queue.UserID <= 0 || seen[queue.UserID] || !db.ValidateOpaqueID(queue.QueueID, "rpsq_") {
			return ledger.ErrInvalidPlan
		}
		seen[queue.UserID] = true
		funding, err := queueSource(ctx, tx, ports.Entry{ResourceID: queue.QueueID, UserID: queue.UserID, Amount: queue.Amount, GamePaid: queue.GamePaid})
		if err != nil {
			return err
		}
		if i > 0 && version != funding.version {
			return ledger.ErrInvalidPlan
		}
		version = funding.version
		queues[i] = ledger.RPSQueueInput{QueueID: queue.QueueID, AccountID: funding.account, Amount: queue.Amount}
		if version == 2 {
			accounts, err := codedAccounts(ctx, tx, "rps-queue:"+queue.QueueID)
			if err != nil {
				return err
			}
			if accounts.General != funding.account {
				return ledger.ErrInvalidPlan
			}
			payments[i] = ledger.RPSQueuePayment{QueueID: queue.QueueID, Accounts: accounts, Payment: funding.payment}
		}
	}
	account, err := ledger.CreateRPSSessionAccount(ctx, tx, input.SessionID, input.Meta.CreatedAt)
	if err != nil {
		return err
	}
	var plan ledger.Plan
	if version == 2 {
		gameAccount, err := ledger.CreateRPSSessionAssetAccount(ctx, tx, input.SessionID, ledger.Game, input.Meta.CreatedAt)
		if err != nil {
			return err
		}
		plan, err = ledger.NewRPSSessionStartWithPayments(input.Meta, input.SessionID, ledger.AccountPair{General: account.ID, Game: gameAccount.ID}, input.FutureRows, payments)
		if err != nil {
			return err
		}
	} else {
		plan, err = ledger.NewRPSSessionStart(input.Meta, input.SessionID, account.ID, input.FutureRows, queues)
		if err != nil {
			return err
		}
	}
	primary := queues[0].QueueID
	for _, queue := range queues[1:] {
		if queue.QueueID < primary {
			primary = queue.QueueID
		}
	}
	ref, err := ledger.RPSQueueReservation(primary)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, account.ID); err != nil {
			return err
		}
		var actual int64
		var actualVersion int
		if err := tx.QueryRowContext(ctx, `SELECT account_id,rules_version FROM game_rps_sessions WHERE id=?`, input.SessionID).Scan(&actual, &actualVersion); err != nil {
			return err
		}
		if actual != account.ID || actualVersion != version {
			return ledger.ErrInvalidPlan
		}
		for seat, queue := range input.Queues {
			var userID int64
			var raw, gameRaw, remainingRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT user_id,starting_balance,game_buy_in,game_remaining FROM game_rps_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&userID, &raw, &gameRaw, &remainingRaw); err != nil {
				return err
			}
			amount, err := db.DecodeU128(raw)
			gamePaid, gameErr := db.DecodeU128(gameRaw)
			remaining, remainingErr := db.DecodeU128(remainingRaw)
			if err != nil || gameErr != nil || remainingErr != nil || gamePaid != remaining || gamePaid.Big().Cmp(queue.GamePaid.Big()) != 0 || userID != queue.UserID || amount.Big().Cmp(queue.Amount.Big()) != 0 {
				return ledger.ErrInvalidPlan
			}
		}
		return nil
	})
	return err
}

func sessionSource(ctx context.Context, tx *sql.Tx, id string, actor int64) (int64, int, error) {
	var account int64
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT account_id,rules_version FROM game_rps_sessions WHERE id=?`, id).Scan(&account, &version); err != nil {
		return 0, 0, err
	}
	if actor > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND user_id=? AND deletion_state='active'`, id, actor).Scan(&count); err != nil {
			return 0, 0, err
		}
		if count != 1 {
			return 0, 0, ledger.ErrInvalidPlan
		}
	}
	if version < 1 || version > 2 {
		return 0, 0, ledger.ErrInvalidPlan
	}
	return account, version, nil
}

func validatePool(ctx context.Context, tx *sql.Tx, accountID int64, kind string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shared_pools WHERE account_id=? AND pool_type=? AND state='open'`, accountID, kind).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ledger.ErrInvalidPlan
	}
	return nil
}

func (port rpsPort) RoundCut(ctx context.Context, tx *sql.Tx, input ports.RoundCut, write ports.Mutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || write == nil || input.GameInput.Sign() < 0 {
		return ledger.ErrInvalidPlan
	}
	account, version, err := sessionSource(ctx, tx, input.SessionID, input.Meta.ActorUserID)
	if err != nil {
		return err
	}
	if version == 1 || !input.Amounts.Welfare.IsZero() {
		if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
			return err
		}
	}
	if version == 1 || !input.Amounts.Thursday.IsZero() {
		if err := validatePool(ctx, tx, input.ThursdayAccountID, "thursday"); err != nil {
			return err
		}
	}
	var platformID int64
	if version == 1 || !input.Amounts.Platform.IsZero() {
		platform, err := ledger.CodedAccount(ctx, tx, "platform")
		if err != nil {
			return err
		}
		platformID = platform.ID
	}
	var plan ledger.Plan
	var beforeGame ledger.Amount
	if version == 2 {
		accounts, err := codedAccounts(ctx, tx, "rps-session:"+input.SessionID)
		if err != nil {
			return err
		}
		if accounts.General != account {
			return ledger.ErrInvalidPlan
		}
		var external ledger.AccountPair
		if !input.GameInput.IsZero() {
			external, err = codedAccounts(ctx, tx, "external")
			if err != nil {
				return err
			}
		}
		beforeGame, err = sessionGameRemaining(ctx, tx, input.SessionID)
		if err != nil {
			return err
		}
		if input.GameInput.Big().Cmp(beforeGame.Big()) > 0 {
			return ledger.ErrInvalidPlan
		}
		plan, err = ledger.NewRPSRoundCutWithGame(input.Meta, input.SessionID, input.Sequence, accounts, external, platformID, input.WelfareAccountID, input.ThursdayAccountID, input.GameInput, input.Amounts)
		if err != nil {
			return err
		}
	} else {
		if !input.GameInput.IsZero() {
			return ledger.ErrInvalidPlan
		}
		plan, err = ledger.NewRPSRoundCut(input.Meta, input.SessionID, input.Sequence, account, platformID, input.WelfareAccountID, input.ThursdayAccountID, input.Amounts)
		if err != nil {
			return err
		}
	}
	ref, err := ledger.RPSSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx); err != nil {
			return err
		}
		if version == 2 {
			afterGame, err := sessionGameRemaining(ctx, tx, input.SessionID)
			if err != nil {
				return err
			}
			expected := new(big.Int).Sub(beforeGame.Big(), input.GameInput.Big())
			if afterGame.Big().Cmp(expected) != 0 {
				return ledger.ErrInvalidPlan
			}
		}
		return nil
	})
	return err
}

func (port rpsPort) Terminal(ctx context.Context, tx *sql.Tx, input ports.Terminal, write ports.Mutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || input.Meta.ActorUserID != 0 || write == nil || len(input.Payouts) > 3 {
		return ledger.ErrInvalidPlan
	}
	account, version, err := sessionSource(ctx, tx, input.SessionID, 0)
	if err != nil {
		return err
	}
	if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
		return err
	}
	var state, operation, task string
	var poolRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,terminal_operation_id,player_pool,mode FROM game_rps_sessions WHERE id=?`, input.SessionID).Scan(&state, &operation, &poolRaw, &task); err != nil {
		return err
	}
	pool, err := db.DecodeU128(poolRaw)
	if err != nil || state != "terminal_processing" || operation != input.Meta.OperationID || pool.Big().Cmp(input.Carry.Big()) != 0 {
		return ledger.ErrInvalidPlan
	}
	expected := map[int64]db.U128{}
	seats := map[int64]int{}
	deleted := new(big.Int)
	gameRemaining := new(big.Int)
	rows, err := tx.QueryContext(ctx, `SELECT user_id,deletion_state,terminal_return,game_remaining FROM game_rps_seats WHERE session_id=? ORDER BY seat_no`, input.SessionID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var userID sql.NullInt64
		var status string
		var raw, gameRaw []byte
		if err := rows.Scan(&userID, &status, &raw, &gameRaw); err != nil {
			rows.Close()
			return err
		}
		amount, err := db.DecodeU128(raw)
		game, gameErr := db.DecodeU128(gameRaw)
		if err != nil || gameErr != nil || game.Big().Cmp(amount.Big()) > 0 {
			rows.Close()
			return ledger.ErrInvalidPlan
		}
		gameRemaining.Add(gameRemaining, game.Big())
		count++
		if count > 3 {
			rows.Close()
			return ledger.ErrInvalidPlan
		}
		if status == "active" && userID.Valid {
			expected[userID.Int64] = amount
			seats[userID.Int64] = count - 1
		} else {
			deleted.Add(deleted, amount.Big())
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if count != 3 || len(expected) != len(input.Payouts) || deleted.Cmp(input.Deleted.Big()) != 0 {
		return ledger.ErrInvalidPlan
	}
	payouts := make([]ledger.RPSTerminalPayout, 0, len(input.Payouts))
	for _, payout := range input.Payouts {
		amount, ok := expected[payout.UserID]
		if !ok || amount.Big().Cmp(payout.Amount.Big()) != 0 {
			return ledger.ErrInvalidPlan
		}
		delete(expected, payout.UserID)
		user, err := ledger.UserAccount(ctx, tx, payout.UserID)
		if err != nil {
			return err
		}
		payouts = append(payouts, ledger.RPSTerminalPayout{UserAccountID: user.ID, Amount: payout.Amount})
	}
	var plan ledger.Plan
	if version == 2 {
		accounts, err := codedAccounts(ctx, tx, "rps-session:"+input.SessionID)
		if err != nil {
			return err
		}
		if accounts.General != account {
			return ledger.ErrInvalidPlan
		}
		external, err := codedAccounts(ctx, tx, "external")
		if err != nil {
			return err
		}
		converted, err := ledger.AmountFromBig(gameRemaining)
		if err != nil {
			return err
		}
		plan, err = ledger.NewRPSTerminalWithGame(input.Meta, input.SessionID, accounts, external, input.WelfareAccountID, payouts, input.Deleted, input.Carry, converted)
		if err != nil {
			return err
		}
	} else {
		if gameRemaining.Sign() != 0 {
			return ledger.ErrInvalidPlan
		}
		external, err := ledger.CodedAccount(ctx, tx, "external")
		if err != nil {
			return err
		}
		plan, err = ledger.NewRPSTerminal(input.Meta, input.SessionID, account, external.ID, input.WelfareAccountID, payouts, input.Deleted, input.Carry)
		if err != nil {
			return err
		}
	}
	ref, err := ledger.RPSSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	if version == 2 {
		for _, payout := range input.Payouts {
			if err := port.complete(ctx, tx, payout.UserID, task, onboardingParent{column: "rps_session_id", id: input.SessionID, seat: seats[payout.UserID]}, input.Meta.CreatedAt); err != nil {
				return err
			}
		}
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func (port rpsPort) TransferOnboarding(ctx context.Context, tx *sql.Tx, input ports.QueueOnboardingTransfer) error {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(input.QueueID, "rpsq_") || !db.ValidateOpaqueID(input.SessionID, "rps_") ||
		input.UserID <= 0 || input.SeatNo < 0 || input.SeatNo > 2 {
		return ledger.ErrInvalidPlan
	}
	// The parent guard verifies the matched user's mode and version. No capacity
	// changes here: the same hold survives queue deletion under its new seat.
	_, err := tx.ExecContext(ctx, `UPDATE game_onboarding_holds SET rps_queue_id=NULL,rps_session_id=?,seat_no=?
WHERE game_key='rps' AND rps_queue_id=? AND user_id=?`, input.SessionID, input.SeatNo, input.QueueID, input.UserID)
	return err
}

func (port rpsPort) ReleaseOnboarding(ctx context.Context, tx *sql.Tx, userID int64) error {
	if ctx == nil || tx == nil || userID <= 0 {
		return ledger.ErrInvalidPlan
	}
	return port.releaseUser(ctx, tx, userID)
}
