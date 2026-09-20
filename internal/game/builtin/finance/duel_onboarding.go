package finance

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (p duelPort) onboardingPort() onboarding {
	if p.game == "likes" {
		return onboarding{likesconfig.Descriptor()}
	}
	return onboarding{config.Descriptor()}
}

func (p duelPort) onboardingTasks(mode string) ([]string, error) {
	if p.game == "bidding" {
		switch mode {
		case "tier1", "tier2", "tier3":
			return []string{"complete_tier_" + mode[4:], "first_win"}, nil
		}
	} else if p.game == "likes" && (mode == "quick" || mode == "standard") {
		return []string{mode + "_complete", mode + "_win"}, nil
	}
	return nil, ledger.ErrInvalidPlan
}

func (p duelPort) TransferOnboarding(ctx context.Context, tx *sql.Tx, input ports.QueueOnboardingTransfer) error {
	if ctx == nil || tx == nil || !p.validID(input.QueueID, true) || !p.validID(input.SessionID, false) || input.UserID <= 0 || input.SeatNo < 0 || input.SeatNo > 1 {
		return ledger.ErrInvalidPlan
	}
	_, err := tx.ExecContext(ctx, `UPDATE game_onboarding_holds SET duel_queue_id=NULL,duel_session_id=?,seat_no=? WHERE duel_queue_id=? AND user_id=? AND game_key=?`, input.SessionID, input.SeatNo, input.QueueID, input.UserID, p.game)
	return err
}

// The terminal callback has already persisted the authoritative result. Award
// consumption follows its ledger posting in the same caller-owned transaction.
func (p duelPort) finishOnboarding(ctx context.Context, tx *sql.Tx, input ports.DuelFinish) error {
	var mode, outcome, reason string
	var ended int64
	if err := tx.QueryRowContext(ctx, `SELECT mode,outcome,reason,terminal_at FROM game_duel_sessions WHERE id=? AND game_key=? AND state='terminal'`, input.SessionID, p.game).Scan(&mode, &outcome, &reason, &ended); err != nil {
		return err
	}
	if ended != input.Meta.CreatedAt {
		return ledger.ErrInvalidPlan
	}
	possible, err := p.onboardingTasks(mode)
	if err != nil {
		return err
	}
	port := p.onboardingPort()
	for seat := range 2 {
		var user int64
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM game_duel_seats WHERE session_id=? AND seat_no=?`, input.SessionID, seat).Scan(&user); err != nil {
			return err
		}
		parent := onboardingParent{column: "duel_session_id", id: input.SessionID, seat: seat}
		var tasks []string
		if outcome != "system_cancelled" && (reason != "surrender" || input.Winner != nil && *input.Winner == seat) {
			tasks = append(tasks, possible[0])
			if input.Winner != nil && *input.Winner == seat {
				tasks = append(tasks, possible[1])
			}
		}
		if err := port.completeTasks(ctx, tx, user, tasks, parent, ended); err != nil {
			return err
		}
	}
	return nil
}
