package charityrouting

import (
	"slices"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

// Each source is a saved target in a visible binding. Counts describe resource
// health before an individual request's parameters and policies are checked.
type InterfaceCoverage struct {
	Operation          contract.Operation       `json:"operation"`
	NativeTargets      int                      `json:"native_targets"`
	ConvertedTargets   int                      `json:"converted_targets"`
	ConversionServices []contract.NativeService `json:"conversion_services"`
}

func visibleInterfaceCoverage(interfaces modeltype.Set, bindings []AdminBinding) []InterfaceCoverage {
	out := make([]InterfaceCoverage, len(interfaces))
	indices := make(map[contract.Operation]int, len(interfaces))
	for i, op := range interfaces {
		out[i] = InterfaceCoverage{Operation: op, ConversionServices: []contract.NativeService{}}
		indices[op] = i
	}
	for _, binding := range bindings {
		for _, target := range binding.Targets {
			if target.Availability == nil || target.Availability.State != "available" {
				continue
			}
			healthy := map[contract.NativeService]bool{}
			for _, status := range target.Availability.ServiceStatuses {
				healthy[status.NativeService] = status.State == "available"
			}
			type routeMode struct {
				op   contract.Operation
				mode string
			}
			counted := map[routeMode]bool{}
			for _, route := range target.ServiceRoutes {
				i, relevant := indices[route.Operation]
				if !relevant || !healthy[route.NativeService] {
					continue
				}
				row := &out[i]
				key := routeMode{route.Operation, route.Mode}
				if route.Mode == "converted" && !slices.Contains(row.ConversionServices, route.NativeService) {
					row.ConversionServices = append(row.ConversionServices, route.NativeService)
				}
				if counted[key] {
					continue
				}
				counted[key] = true
				if route.Mode == "native" {
					row.NativeTargets++
				} else {
					row.ConvertedTargets++
				}
			}
		}
	}
	return out
}
