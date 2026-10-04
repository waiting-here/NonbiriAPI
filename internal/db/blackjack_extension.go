package db

import (
	"context"

	"errors"
)

func blackjackConfigDefaults() map[string]string {
	return map[string]string{"game_blackjack_enabled": "0", "game_blackjack_min_stake_milli": "1000000", "game_blackjack_max_stake_milli": "50000000", "game_blackjack_stake_step_milli": "1000000", "game_blackjack_default_stake_milli": "5000000", "game_blackjack_rake_platform_bp": "100", "game_blackjack_rake_welfare_bp": "100", "game_blackjack_rake_thursday_bp": "100"}
}

func BlackjackStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_blackjack_clock','game_blackjack_sessions','game_blackjack_entries','game_blackjack_payments','game_blackjack_events','game_blackjack_anonymous')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 6 {
		return false, errors.New("partial blackjack storage")
	}
	return count == 6, nil
}
