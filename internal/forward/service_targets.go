package forward

import (
	"errors"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// expandServiceTargets keeps authorization separate from wire capability.
// Alternatives exist only during preflight; acceptance sees one per binding.
func expandServiceTargets(candidates []RouteCandidate, operation connectorcontract.Operation) ([]RouteCandidate, error) {
	result := make([]RouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		authorization := candidate.Authorization
		if candidate.BindingID <= 0 || candidate.BindingID != authorization.BindingID || authorization.TargetID <= 0 ||
			authorization.TargetRevision <= 0 || candidate.NativeServices.Empty() {
			return nil, ErrInternal
		}
		routes, err := connector.ResolveServiceRoutes(authorization.ConfiguredType, candidate.NativeServices, authorization.RerankFormat, operation)
		if errors.Is(err, connector.ErrNoServiceRoute) {
			continue
		}
		if err != nil {
			return nil, ErrInternal
		}
		for _, route := range routes {
			// A charity target may have a different approved version for each service.
			if authorization.NativeService != "" && authorization.NativeService != route.NativeService {
				continue
			}
			selected := candidate
			selected.Authorization.NativeService = route.NativeService
			selected.ConnectorType, selected.NativeMatch = route.WireType, route.NativeMatch
			result = append(result, selected)
		}
	}
	// Stable target order breaks ties only within the same logical binding.
	sort.SliceStable(result, func(left, right int) bool {
		a, b := result[left], result[right]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if a.BindingID != b.BindingID {
			return a.BindingID < b.BindingID
		}
		if a.NativeMatch != b.NativeMatch {
			return a.NativeMatch
		}
		return a.TargetOrder < b.TargetOrder
	})
	return result, nil
}

func firstCapableBindings(candidates []RouteCandidate) []RouteCandidate {
	seen := make(map[int64]bool)
	selected := make([]RouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if seen[candidate.BindingID] {
			continue
		}
		seen[candidate.BindingID] = true
		selected = append(selected, candidate)
	}
	return selected
}
