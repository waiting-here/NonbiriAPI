package host

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type routeGuard struct {
	declarations  map[string]game.RouteDeclaration
	continuations map[string]bool
	bound         map[string]bool
	next          Registrars
}

func routeKey(station, method, pattern string) string { return station + " " + method + " " + pattern }
func newRouteGuard(descriptor game.ModuleDescriptor, next Registrars) *routeGuard {
	guard := &routeGuard{declarations: make(map[string]game.RouteDeclaration), continuations: make(map[string]bool), bound: make(map[string]bool), next: next}
	for _, route := range descriptor.Routes {
		guard.declarations[routeKey(route.Station, route.Method, route.Pattern)] = route
	}
	for _, kind := range descriptor.ContinuationIDs {
		guard.continuations[kind] = true
	}
	return guard
}
func (guard *routeGuard) registrars() Registrars {
	return Registrars{User: guard, Admin: guard, Continuation: guard, Maintenance: guard}
}
func (guard *routeGuard) complete() bool {
	return len(guard.bound) == len(guard.declarations)+len(guard.continuations)
}

func (guard *routeGuard) claim(station, method, pattern string, continuation bool) error {
	key := routeKey(station, method, pattern)
	declared, ok := guard.declarations[key]
	if !ok || declared.Continuation != continuation || guard.bound[key] {
		return game.ErrInvalidContract
	}
	guard.bound[key] = true
	return nil
}
func (guard *routeGuard) RegisterUserRoute(method, pattern string, handler resources.AuthorizedUserHandler) error {
	if handler == nil {
		return game.ErrInvalidContract
	}
	if err := guard.claim("user", method, pattern, false); err != nil {
		return err
	}
	return guard.next.User.RegisterUserRoute(method, pattern, handler)
}
func (guard *routeGuard) RegisterContinuationUserRoute(method, pattern string, handler resources.AuthenticatedContinuationHandler) error {
	if handler == nil {
		return game.ErrInvalidContract
	}
	if err := guard.claim("user", method, pattern, true); err != nil {
		return err
	}
	return guard.next.Continuation.RegisterContinuationUserRoute(method, pattern, handler)
}
func (guard *routeGuard) RegisterAdminRoute(method, pattern string, handler http.Handler) error {
	if handler == nil {
		return game.ErrInvalidContract
	}
	if err := guard.claim("admin", method, pattern, false); err != nil {
		return err
	}
	return guard.next.Admin.RegisterAdminRoute(method, pattern, handler)
}
func (guard *routeGuard) Register(kind maintenance.ContinuationKind, registration maintenance.ContinuationRegistration) error {
	key := "continuation:" + string(kind)
	if !guard.continuations[string(kind)] || guard.bound[key] {
		return game.ErrInvalidContract
	}
	guard.bound[key] = true
	return guard.next.Maintenance.Register(kind, registration)
}

func reservedRoute(route game.RouteDeclaration) bool {
	key := routeKey(route.Station, route.Method, route.Pattern)
	return key == routeKey("user", "GET", RouteGames) || key == routeKey("user", "GET", RouteHomeSummary) || key == routeKey("admin", "GET", RouteAdminActiveCounts) || key == routeKey("admin", "GET", RouteAdminGamesConfig) || key == routeKey("admin", "PATCH", RouteAdminGamesConfig)
}
