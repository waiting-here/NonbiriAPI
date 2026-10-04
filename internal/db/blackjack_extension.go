package db

func blackjackConfigDefaults() map[string]string {
	return map[string]string{"game_blackjack_enabled": "0", "game_blackjack_min_stake_milli": "1000000", "game_blackjack_max_stake_milli": "50000000", "game_blackjack_stake_step_milli": "1000000", "game_blackjack_default_stake_milli": "5000000", "game_blackjack_rake_platform_bp": "100", "game_blackjack_rake_welfare_bp": "100", "game_blackjack_rake_thursday_bp": "100"}
}
