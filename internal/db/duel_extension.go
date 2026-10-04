package db

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
