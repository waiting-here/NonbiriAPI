package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type rpsPort struct{}

func queueSource(ctx context.Context, tx *sql.Tx, id string, userID int64, amount ledger.Amount) (int64, string, error) {
	var owner, account int64
	var operation string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT user_id,account_id,reserved,reservation_operation_id FROM game_rps_queue WHERE id=?`, id).Scan(&owner, &account, &raw, &operation); err != nil {
		return 0, "", err
	}
	reserved, err := db.DecodeU128(raw)
	if err != nil || owner != userID || amount.Big().Cmp(reserved.Big()) != 0 || amount.Big().Sign() <= 0 {
		return 0, "", ledger.ErrInvalidPlan
	}
	return account, operation, nil
}

func (rpsPort) QueueReserve(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.AccountMutation) (int64, error) {
	if !validEntry(ctx, tx, input, "rpsq_") || input.Meta.ActorUserID != input.UserID || write == nil {
		return 0, ledger.ErrInvalidPlan
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return 0, err
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
	if err := ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if err := write(ctx, tx, account.ID); err != nil {
			return err
		}
		actual, operation, err := queueSource(ctx, tx, input.ResourceID, input.UserID, input.Amount)
		if err != nil {
			return err
		}
		if actual != account.ID || operation != input.Meta.OperationID {
			return ledger.ErrInvalidPlan
		}
		return nil
	}); err != nil {
		return 0, err
	}
	plan, err := ledger.NewRPSQueueReserve(input.Meta, input.ResourceID, user.ID, account.ID, input.Amount)
	if err != nil {
		return 0, err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return account.ID, err
}

func (rpsPort) QueueRelease(ctx context.Context, tx *sql.Tx, input ports.Entry, write ports.Mutation) error {
	if !validEntry(ctx, tx, input, "rpsq_") || write == nil {
		return ledger.ErrInvalidPlan
	}
	account, _, err := queueSource(ctx, tx, input.ResourceID, input.UserID, input.Amount)
	if err != nil {
		return err
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	plan, err := ledger.NewRPSQueueRelease(input.Meta, input.ResourceID, account, user.ID, input.Amount)
	if err != nil {
		return err
	}
	ref, err := ledger.RPSQueueReservation(input.ResourceID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func validSession(ctx context.Context, tx *sql.Tx, id string, meta ledger.Meta) bool {
	return ctx != nil && tx != nil && db.ValidateOpaqueID(id, "rps_") && db.ValidateOpaqueID(meta.OperationID, "op_") && meta.CreatedAt >= 0 && meta.CreatedAt <= 253402300799 && meta.ActorUserID >= 0
}

func (rpsPort) SessionStart(ctx context.Context, tx *sql.Tx, input ports.SessionStart, write ports.AccountMutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || input.Meta.ActorUserID != 0 || write == nil || input.FutureRows.Big().Sign() <= 0 {
		return ledger.ErrInvalidPlan
	}
	queues := [3]ledger.RPSQueueInput{}
	seen := map[int64]bool{}
	for i, queue := range input.Queues {
		if queue.UserID <= 0 || seen[queue.UserID] || !db.ValidateOpaqueID(queue.QueueID, "rpsq_") {
			return ledger.ErrInvalidPlan
		}
		seen[queue.UserID] = true
		account, _, err := queueSource(ctx, tx, queue.QueueID, queue.UserID, queue.Amount)
		if err != nil {
			return err
		}
		queues[i] = ledger.RPSQueueInput{QueueID: queue.QueueID, AccountID: account, Amount: queue.Amount}
	}
	account, err := ledger.CreateRPSSessionAccount(ctx, tx, input.SessionID, input.Meta.CreatedAt)
	if err != nil {
		return err
	}
	plan, err := ledger.NewRPSSessionStart(input.Meta, input.SessionID, account.ID, input.FutureRows, queues)
	if err != nil {
		return err
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
		if err := tx.QueryRowContext(ctx, `SELECT account_id FROM game_rps_sessions WHERE id=?`, input.SessionID).Scan(&actual); err != nil {
			return err
		}
		if actual != account.ID {
			return ledger.ErrInvalidPlan
		}
		for seat, queue := range input.Queues {
			var userID int64
			var raw []byte
			if err := tx.QueryRowContext(ctx, `SELECT user_id,starting_balance FROM game_rps_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&userID, &raw); err != nil {
				return err
			}
			amount, err := db.DecodeU128(raw)
			if err != nil || userID != queue.UserID || amount.Big().Cmp(queue.Amount.Big()) != 0 {
				return ledger.ErrInvalidPlan
			}
		}
		return nil
	})
	return err
}

func sessionSource(ctx context.Context, tx *sql.Tx, id string, actor int64) (int64, error) {
	var account int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM game_rps_sessions WHERE id=?`, id).Scan(&account); err != nil {
		return 0, err
	}
	if actor > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND user_id=? AND deletion_state='active'`, id, actor).Scan(&count); err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ledger.ErrInvalidPlan
		}
	}
	return account, nil
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

func (rpsPort) RoundCut(ctx context.Context, tx *sql.Tx, input ports.RoundCut, write ports.Mutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || write == nil {
		return ledger.ErrInvalidPlan
	}
	account, err := sessionSource(ctx, tx, input.SessionID, input.Meta.ActorUserID)
	if err != nil {
		return err
	}
	if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
		return err
	}
	if err := validatePool(ctx, tx, input.ThursdayAccountID, "thursday"); err != nil {
		return err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return err
	}
	plan, err := ledger.NewRPSRoundCut(input.Meta, input.SessionID, input.Sequence, account, platform.ID, input.WelfareAccountID, input.ThursdayAccountID, input.Amounts)
	if err != nil {
		return err
	}
	ref, err := ledger.RPSSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}

func (rpsPort) Terminal(ctx context.Context, tx *sql.Tx, input ports.Terminal, write ports.Mutation) error {
	if !validSession(ctx, tx, input.SessionID, input.Meta) || input.Meta.ActorUserID != 0 || write == nil || len(input.Payouts) > 3 {
		return ledger.ErrInvalidPlan
	}
	account, err := sessionSource(ctx, tx, input.SessionID, 0)
	if err != nil {
		return err
	}
	if err := validatePool(ctx, tx, input.WelfareAccountID, "welfare"); err != nil {
		return err
	}
	var state, operation string
	var poolRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,terminal_operation_id,player_pool FROM game_rps_sessions WHERE id=?`, input.SessionID).Scan(&state, &operation, &poolRaw); err != nil {
		return err
	}
	pool, err := db.DecodeU128(poolRaw)
	if err != nil || state != "terminal_processing" || operation != input.Meta.OperationID || pool.Big().Cmp(input.Carry.Big()) != 0 {
		return ledger.ErrInvalidPlan
	}
	expected := map[int64]db.U128{}
	deleted := new(big.Int)
	rows, err := tx.QueryContext(ctx, `SELECT user_id,deletion_state,terminal_return FROM game_rps_seats WHERE session_id=? ORDER BY seat_no`, input.SessionID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var userID sql.NullInt64
		var status string
		var raw []byte
		if err := rows.Scan(&userID, &status, &raw); err != nil {
			rows.Close()
			return err
		}
		amount, err := db.DecodeU128(raw)
		if err != nil {
			rows.Close()
			return ledger.ErrInvalidPlan
		}
		count++
		if count > 3 {
			rows.Close()
			return ledger.ErrInvalidPlan
		}
		if status == "active" && userID.Valid {
			expected[userID.Int64] = amount
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
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return err
	}
	plan, err := ledger.NewRPSTerminal(input.Meta, input.SessionID, account, external.ID, input.WelfareAccountID, payouts, input.Deleted, input.Carry)
	if err != nil {
		return err
	}
	ref, err := ledger.RPSSessionReservation(input.SessionID)
	if err != nil {
		return err
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, ledger.ReservationMutation(write))
	return err
}
