package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func recordRank(ctx context.Context, tx *sql.Tx, user int64, game, source string, at int64, loss, profit *big.Int) error {
	return ranking.RecordTx(ctx, tx, ranking.Contribution{UserID: user, Game: game, Source: source, SettledAt: at, Loss: loss, PositiveProfit: profit})
}

func (p duelPort) finishRanking(ctx context.Context, tx *sql.Tx, input ports.DuelFinish, ticket int64, cuts ledger.DuelCuts) error {
	started, err := progressionStarted(ctx, tx, input.Meta.CreatedAt)
	if err != nil || !started {
		return err
	}
	var outcome string
	if err := tx.QueryRowContext(ctx, `SELECT outcome FROM game_duel_sessions WHERE id=? AND state='terminal'`, input.SessionID).Scan(&outcome); err != nil {
		return err
	}
	if outcome == "system_cancelled" {
		return nil
	}
	for seat := range 2 {
		var user int64
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM game_duel_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&user); err != nil {
			return err
		}
		loss, profit := new(big.Int), new(big.Int)
		if input.Winner != nil {
			loss.SetInt64(ticket)
			if *input.Winner == seat {
				loss.Neg(cuts.Prize.Big())
				if p.game == "bidding" {
					profit.SetInt64(ticket)
				}
			}
		}
		if err := recordRank(ctx, tx, user, p.game, input.SessionID, input.Meta.CreatedAt, loss, profit); err != nil {
			return err
		}
	}
	return nil
}

func blackjackRankAmounts(stake int64, hands []engine.Hand, net *big.Int) (*big.Int, *big.Int) {
	spent, profit := new(big.Int), new(big.Int)
	for _, hand := range hands {
		paid := new(big.Int).Mul(big.NewInt(stake), big.NewInt(int64(hand.Units)))
		gross := new(big.Int).Mul(big.NewInt(stake), big.NewInt(int64(hand.ReturnHalves())))
		gross.Quo(gross, big.NewInt(2))
		spent.Add(spent, paid)
		if gross.Cmp(paid) > 0 {
			profit.Add(profit, gross.Sub(gross, paid))
		}
	}
	return spent.Sub(spent, net), profit
}

func rpsFinishRanking(ctx context.Context, tx *sql.Tx, input ports.Terminal) error {
	started, err := progressionStarted(ctx, tx, input.Meta.CreatedAt)
	if err != nil || !started {
		return err
	}
	// Terminal summary net is cash-out minus the retained initial buy-in,
	// including in-session fees and both assets, before onboarding rewards.
	for _, payout := range input.Payouts {
		var sign int
		var mag []byte
		var at int64
		if err := tx.QueryRowContext(ctx, `SELECT s.wallet_net_sign,s.wallet_net_mag,a.terminal_at FROM game_rps_summary_seats s JOIN game_rps_summaries a ON a.session_id=s.session_id WHERE s.session_id=? AND s.user_id=?`, input.SessionID, payout.UserID).Scan(&sign, &mag, &at); err != nil {
			return err
		}
		value, err := db.NewSM128(sign, mag)
		if err != nil {
			return err
		}
		if at != input.Meta.CreatedAt {
			return ledger.ErrInvalidPlan
		}
		if err := recordRank(ctx, tx, payout.UserID, "rps", input.SessionID, at, new(big.Int).Neg(value.Big()), new(big.Int)); err != nil {
			return err
		}
	}
	return nil
}
