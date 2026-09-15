package config

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{
		ID: ID, Version: Version, StableOrder: 5, ResourcePrefixes: []string{"bjq_", "bjt_", "bjp_"},
		Modes: []string{"table"}, HomeRouteID: "game-blackjack", ContinuationIDs: []string{"blackjack_session"}, Codec: Codec{},
		Routes: []game.RouteDeclaration{
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/blackjack/history"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/blackjack/history/{id}"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/blackjack/history/export"},
			{Station: "user", Method: "GET", Pattern: "/api/games/blackjack/state", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/blackjack/queue"},
			{Station: "user", Method: "DELETE", Pattern: "/api/games/blackjack/queue/{id}", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/blackjack/sessions/{id}/actions", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/blackjack/sessions/{id}/emotes", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/blackjack/history", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/blackjack/history/{id}", Continuation: true},
		},
	}
}
