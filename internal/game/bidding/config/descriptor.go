package config

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{
		ID: ID, Version: Version, StableOrder: 3,
		ResourcePrefixes: []string{"bidq_", "bid_"}, Modes: Modes(),
		HomeRouteID: "game-bidding", ContinuationIDs: []string{"bidding_session"}, Codec: Codec{},
		Onboarding: []game.OnboardingTask{{Key: "complete_tier_1", RewardMilli: 1000000}, {Key: "complete_tier_2", RewardMilli: 2000000}, {Key: "complete_tier_3", RewardMilli: 5000000}, {Key: "first_win", RewardMilli: 2000000}},
		Routes: []game.RouteDeclaration{
			{Station: "user", Method: "GET", Pattern: "/api/games/bidding/randomness/{id}", Continuation: true},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/bidding/history"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/bidding/history/{id}"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/bidding/history/{id}/rounds"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/bidding/history/export"},
			{Station: "user", Method: "GET", Pattern: "/api/games/bidding/state", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/bidding/queue"},
			{Station: "user", Method: "DELETE", Pattern: "/api/games/bidding/queue/{id}", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/bidding/sessions/{id}/actions", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/bidding/sessions/{id}/surrender", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/bidding/history", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/bidding/history/{id}", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/bidding/history/{id}/rounds", Continuation: true},
		},
	}
}
