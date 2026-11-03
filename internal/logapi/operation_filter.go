package logapi

import (
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

// OperationDiscovery identifies model discovery outside inference operations.
const OperationDiscovery contract.Operation = "model_discovery"

var logOperationSQL = requestkind.LogOperationSQL()

func validLogOperation(operation contract.Operation) bool {
	return operation == OperationDiscovery || operation.Valid()
}

func useOperationRange(filter ListFilter) bool {
	// Charity model filtering supplies an exact charity kind for this operation.
	// Its narrower model index should not compete with a redundant CASE range.
	return filter.Operation != "" && filter.CharityModel == nil
}
