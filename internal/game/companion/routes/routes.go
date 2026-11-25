// Package routes declares the companion HTTP surface without persistent dependencies.
package routes

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Match(id string) []game.RouteDeclaration {
	base := "/api/games/" + id + "/companion"
	out := []game.RouteDeclaration{}
	for _, r := range [][2]string{{"GET", ""}, {"PUT", "/preferences"}, {"POST", "/matches"}, {"GET", "/matches/{id}"}, {"POST", "/matches/{id}/actions"}, {"POST", "/matches/{id}/abandon"}, {"GET", "/history"}, {"GET", "/matches/{id}/replay"}} {
		out = append(out, game.RouteDeclaration{Station: "user", Method: r[0], Pattern: base + r[1]})
	}
	return out
}
func All(id string) []game.RouteDeclaration {
	base := "/api/games/" + id + "/companion"
	out := Match(id)
	for _, r := range [][2]string{{"GET", "/programs"}, {"POST", "/programs"}, {"GET", "/kit"}, {"POST", "/programs/activate"}, {"POST", "/programs/delete"}} {
		out = append(out, game.RouteDeclaration{Station: "user", Method: r[0], Pattern: base + r[1]})
	}
	for _, r := range [][2]string{{"GET", ""}, {"POST", "/settings"}, {"POST", "/versions"}, {"POST", "/activate"}} {
		out = append(out, game.RouteDeclaration{Station: "admin", Method: r[0], Pattern: "/admin" + base + r[1]})
	}
	return out
}
