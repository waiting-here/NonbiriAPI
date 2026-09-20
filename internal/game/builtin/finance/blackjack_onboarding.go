package finance

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func blackjackTasks() []string {
	var keys []string
	for _, task := range config.Descriptor().Onboarding {
		keys = append(keys, task.Key)
	}
	return keys
}

func reserveBlackjackOnboarding(ctx context.Context, tx *sql.Tx, user int64, entry string, now int64) error {
	return (onboarding{config.Descriptor()}).reserveTasks(ctx, tx, user, blackjackTasks(), onboardingParent{column: "blackjack_entry_id", id: entry}, now)
}

func blackjackRewardTasks(hands []engine.Hand) []string {
	qualified := map[string]bool{"complete": true}
	for _, hand := range hands {
		qualified["first_win"] = qualified["first_win"] || hand.Outcome == "win" || hand.Outcome == "natural"
		total := engine.Score(hand.Cards).Value
		qualified["first_bust"] = qualified["first_bust"] || total > 21
		qualified["first_21"] = qualified["first_21"] || total == 21
		qualified["first_natural_21"] = qualified["first_natural_21"] || hand.Natural()
	}
	var tasks []string
	for _, task := range blackjackTasks() {
		if qualified[task] {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// Waiting entries survive restart. Add any missing reservations from an older
// installation in bounded batches before making the module available again.
func (blackjackPort) RestoreOnboarding(ctx context.Context, tx *sql.Tx, limit int, now int64) (int, bool, error) {
	if ctx == nil || tx == nil || limit < 1 || now < 0 {
		return 0, false, ledger.ErrInvalidPlan
	}
	started, err := progressionStarted(ctx, tx, now)
	if err != nil || !started {
		return 0, false, err
	}
	limit = min(limit, 128)
	keys, err := json.Marshal(blackjackTasks())
	if err != nil {
		return 0, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.user_id FROM game_blackjack_entries e WHERE e.state='waiting' AND EXISTS(
 SELECT 1 FROM json_each(?) task WHERE
 NOT EXISTS(SELECT 1 FROM game_onboarding_completions c WHERE c.user_id=e.user_id AND c.game_key='blackjack' AND c.task_key=task.value)
 AND NOT EXISTS(SELECT 1 FROM game_onboarding_holds h WHERE h.blackjack_entry_id=e.id AND h.user_id=e.user_id AND h.game_key='blackjack' AND h.task_key=task.value))
 ORDER BY e.ordinal LIMIT ?`, string(keys), limit)
	if err != nil {
		return 0, false, err
	}
	type entry struct {
		id   string
		user int64
	}
	entries := make([]entry, 0, limit)
	for rows.Next() {
		var item entry
		if err := rows.Scan(&item.id, &item.user); err != nil {
			rows.Close()
			return 0, false, err
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, false, err
	}
	if err := rows.Close(); err != nil {
		return 0, false, err
	}
	for _, item := range entries {
		if err := reserveBlackjackOnboarding(ctx, tx, item.user, item.id, now); err != nil {
			return 0, false, err
		}
	}
	return len(entries), len(entries) == limit, nil
}

func (blackjackPort) ReleaseOnboarding(ctx context.Context, tx *sql.Tx, user int64) error {
	if ctx == nil || tx == nil || user <= 0 {
		return ledger.ErrInvalidPlan
	}
	return (onboarding{config.Descriptor()}).releaseUser(ctx, tx, user)
}
