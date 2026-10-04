package db

import (
	"context"

	"errors"
)

// DuelStoragePresent supports validation of both sides of the exact migration.
// A partial extension is never accepted as an older, empty deployment.
func DuelStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_duel_catalogs','game_duel_queue','game_duel_sessions','game_duel_seats','game_duel_user_slots','game_duel_rounds','game_duel_anonymous','game_duel_anonymous_rounds')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 8 {
		return false, errors.New("partial duel storage")
	}
	return count == 8, nil
}

func duelConfigDefaults() map[string]string {
	values := map[string]string{"game_bidding_enabled": "0", "game_likes_enabled": "0"}
	for _, game := range []string{"bidding", "likes"} {
		modes := []string{"tier1", "tier2", "tier3"}
		tickets := []string{"5000000", "10000000", "50000000"}
		if game == "likes" {
			modes = []string{"quick", "standard"}
			tickets = []string{"5000000", "25000000"}
		}
		for i, mode := range modes {
			prefix := "game_" + game + "_" + mode
			values[prefix+"_enabled"] = "0"
			values[prefix+"_ticket_milli"] = tickets[i]
			for _, destination := range []string{"platform", "welfare", "thursday"} {
				values[prefix+"_rake_"+destination+"_bp"] = "100"
			}
		}
	}
	return values
}
