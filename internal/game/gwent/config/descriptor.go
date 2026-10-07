package config

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{
		ID: ID, Version: Version, StableOrder: 6,
		BoardIDs:         []string{"wins"},
		ResourcePrefixes: []string{"gwtq_", "gwt_", "gaq_"}, Modes: append(Modes(), "ai"),
		HomeRouteID: "game-gwent", ContinuationIDs: []string{"gwent_session"}, Codec: Codec{},
		Routes: []game.RouteDeclaration{
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/ai"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/gwent/ai"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/gwent/ai/settings"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/gwent/ai/policies"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/gwent/ai/bots"},

			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/leaderboard"},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/randomness/{id}", Continuation: true},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/gwent/history"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/gwent/history/{id}"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/gwent/history/{id}/rounds"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/gwent/history/export"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/gwent/history/download"},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/catalog", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/state", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/gwent/queue"},
			{Station: "user", Method: "DELETE", Pattern: "/api/games/gwent/queue/{id}", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/gwent/sessions/{id}/actions", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/gwent/sessions/{id}/surrender", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/sessions/{id}/rounds", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/history", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/history/{id}", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/gwent/history/{id}/rounds", Continuation: true},
		},
	}
}
