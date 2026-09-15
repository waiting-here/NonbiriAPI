package blackjack

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) reservePayment(ctx context.Context, tx *sql.Tx, e entryRecord, kind string, hand int, now int64) error {
	general, err := ledger.UserAssetAccount(ctx, tx, e.User.Int64, ledger.General)
	if err != nil {
		return err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, e.User.Int64, ledger.Game)
	if err != nil {
		return err
	}
	paid, err := ledger.SplitGamePayment(ledger.AmountFromMilli(e.Stake), general.Balance, game.Balance)
	if err != nil {
		return classify(err)
	}
	meta, err := s.meta(e.User.Int64, now)
	if err != nil {
		return err
	}
	id, err := s.generate("bjp_")
	if err != nil {
		return err
	}
	input := finance.Entry{Meta: meta, ResourceID: id, UserID: e.User.Int64, Amount: ledger.AmountFromMilli(e.Stake), GamePaid: paid.Game}
	one, _ := db.ParseU128Decimal("1")
	return classify(s.finance.Reserve(ctx, tx, input, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_payments(id,entry_id,kind,hand_no,amount_milli,game_paid_milli,general_account_id,game_account_id,reserve_operation_id,state,ledger_rows_remaining,created_at) VALUES(?,?,?,?,?,?,?,?,?,'reserved',?,?)`, id, e.ID, kind, hand, e.Stake, paid.Game.Big().Int64(), accounts.General, accounts.Game, meta.OperationID, db.EncodeU128(one), now)
		return err
	}))
}

type reservedPayment struct {
	ID, Kind     string
	Amount, Game int64
}

func reservedPayments(ctx context.Context, tx *sql.Tx, entry string) ([]reservedPayment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,amount_milli,game_paid_milli FROM game_blackjack_payments WHERE entry_id=? AND state='reserved' ORDER BY ordinal`, entry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []reservedPayment{}
	for rows.Next() {
		var p reservedPayment
		if err := rows.Scan(&p.ID, &p.Kind, &p.Amount, &p.Game); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func paymentTerminal(input finance.Entry, state string) finance.Mutation {
	return func(ctx context.Context, tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE game_blackjack_payments SET state=?,ledger_rows_remaining=?,terminal_operation_id=? WHERE id=? AND state='reserved'`, state, db.EncodeU128(db.U128{}), input.Meta.OperationID, input.ResourceID)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			return ErrConflict
		}
		return nil
	}
}
func (s *Service) releaseEntry(ctx context.Context, tx *sql.Tx, e entryRecord, now int64) error {
	payments, err := reservedPayments(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	if len(payments) < 1 || len(payments) > 4 {
		return ErrInvariant
	}
	for _, p := range payments {
		meta, err := s.meta(0, now)
		if err != nil {
			return err
		}
		input := finance.Entry{Meta: meta, ResourceID: p.ID, UserID: e.User.Int64, Amount: ledger.AmountFromMilli(p.Amount), GamePaid: ledger.AmountFromMilli(p.Game)}
		if err := s.finance.Release(ctx, tx, input, paymentTerminal(input, "released")); err != nil {
			return err
		}
	}
	query := `UPDATE game_blackjack_entries SET state='released',resolved_at=?,pending_json=NULL,pending_batch=NULL WHERE id=?`
	if e.State != "playing" {
		query = `UPDATE game_blackjack_entries SET state='released',resolved_at=?,session_id=NULL,seat_no=NULL,pending_json=NULL,pending_batch=NULL WHERE id=?`
	}
	_, err = tx.ExecContext(ctx, query, now, e.ID)
	return err
}
func (s *Service) settleTable(ctx context.Context, tx *sql.Tx, v *sessionRecord, state engine.State, list []entryRecord, now int64) (activities.PublishFacts, error) {
	facts := activities.PublishFacts{}
	fact, err := factFor(&state, list, now)
	if err != nil {
		return facts, err
	}
	var welfare, thursday activities.PoolDestination
	needWelfare, needThursday := false, false
	summaries := make(map[int][4]int64, len(list))
	for _, e := range list {
		var hands []HandSettlement
		sums := [4]int64{}
		for _, seat := range state.Seats {
			if seat.Number == int(e.Seat.Int64) {
				for _, h := range seat.Hands {
					result, err := SettleHand(e.Stake, h, e.Rates)
					if err != nil {
						return facts, err
					}
					hands = append(hands, result)
					sums[0] += result.Net
					sums[1] += result.Platform
					sums[2] += result.Welfare
					sums[3] += result.Thursday
				}
			}
		}
		if len(hands) == 0 {
			return facts, ErrInvariant
		}
		fact.Settlements = append(fact.Settlements, SeatSettlement{Seat: int(e.Seat.Int64), Hands: hands})
		summaries[int(e.Seat.Int64)] = sums
		needWelfare = needWelfare || sums[2] > 0
		needThursday = needThursday || sums[3] > 0
	}
	if needWelfare {
		welfare, err = s.pools.WelfareDestination(ctx, tx)
		if err != nil {
			return facts, err
		}
	}
	if needThursday {
		thursday, err = s.pools.ThursdayDestination(ctx, tx, now)
		if err != nil {
			return facts, err
		}
	}
	for _, e := range list {
		payments, err := reservedPayments(ctx, tx, e.ID)
		if err != nil {
			return facts, err
		}
		if len(payments) < 1 || len(payments) > 4 {
			return facts, ErrInvariant
		}
		for _, p := range payments {
			meta, err := s.meta(0, now)
			if err != nil {
				return facts, err
			}
			sums := [4]int64{}
			if p.Kind == "base" {
				sums = summaries[int(e.Seat.Int64)]
			}
			input := finance.BlackjackSettlement{Entry: finance.Entry{Meta: meta, ResourceID: p.ID, UserID: e.User.Int64, Amount: ledger.AmountFromMilli(p.Amount), GamePaid: ledger.AmountFromMilli(p.Game)}, Net: ledger.AmountFromMilli(sums[0]), Platform: ledger.AmountFromMilli(sums[1]), Welfare: ledger.AmountFromMilli(sums[2]), Thursday: ledger.AmountFromMilli(sums[3]), WelfareAccountID: welfare.AccountID, ThursdayAccountID: thursday.AccountID}
			if err := s.finance.Settle(ctx, tx, input, paymentTerminal(input.Entry, "settled")); err != nil {
				return facts, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET state='settled',resolved_at=?,pending_json=NULL,pending_batch=NULL WHERE id=? AND state='playing'`, now, e.ID); err != nil {
			return facts, err
		}
		if e.User.Valid {
			facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
		}
	}
	body, err := marshal(fact)
	if err != nil {
		return facts, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_sessions SET phase='result',revision=revision+1,state_json=NULL,view_json=?,terminal_at=?,reason='completed' WHERE id=? AND phase='decision'`, string(body), now, v.ID); err != nil {
		return facts, err
	}
	if err := appendEvent(ctx, tx, v.ID, now, "result", body); err != nil {
		return facts, err
	}
	destinations := []activities.PoolDestination{}
	if needWelfare {
		destinations = append(destinations, welfare)
	}
	if needThursday {
		destinations = append(destinations, thursday)
	}
	if len(destinations) > 0 {
		published, err := s.pools.RecordPoolTransfers(ctx, tx, now, destinations...)
		if err != nil {
			return facts, err
		}
		facts.Global = published.Global
		facts.AccountIDs = append(facts.AccountIDs, published.AccountIDs...)
	}
	v.Phase = "result"
	v.Revision++
	v.TerminalAt = sql.NullInt64{Int64: now, Valid: true}
	v.Reason = sql.NullString{String: "completed", Valid: true}
	v.StateJSON = sql.NullString{}
	v.ViewJSON = sql.NullString{String: string(body), Valid: true}
	return facts, nil
}

func mergeFacts(dst *activities.PublishFacts, src activities.PublishFacts) {
	dst.Global = dst.Global || src.Global
	dst.AccountIDs = append(dst.AccountIDs, src.AccountIDs...)
}
