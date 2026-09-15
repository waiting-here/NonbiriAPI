package config

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Descriptor() game.ModuleDescriptor {
	return game.ModuleDescriptor{
		ID: ID, Version: Version, StableOrder: 4,
		ResourcePrefixes: []string{"likq_", "lik_"}, Modes: Modes(),
		HomeRouteID: "game-likes", ContinuationIDs: []string{"likes_session"}, Codec: Codec{},
		Routes: []game.RouteDeclaration{
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/likes/history"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/likes/history/{id}"},
			{Station: "admin", Method: "GET", Pattern: "/admin/api/games/likes/history/{id}/rounds"},
			{Station: "admin", Method: "POST", Pattern: "/admin/api/games/likes/history/export"},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/catalog", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/state", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/likes/queue"},
			{Station: "user", Method: "DELETE", Pattern: "/api/games/likes/queue/{id}", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/likes/sessions/{id}/actions", Continuation: true},
			{Station: "user", Method: "POST", Pattern: "/api/games/likes/sessions/{id}/surrender", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/sessions/{id}/rounds", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/history", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/history/{id}", Continuation: true},
			{Station: "user", Method: "GET", Pattern: "/api/games/likes/history/{id}/rounds", Continuation: true},
		},
	}
}
