package requestkind

import "github.com/waiting-here/NonbiriAPI/internal/connector/contract"

// IsLightOperation classifies the fixed ingress operation before reading its
// request body. Model names, capabilities and caller headers cannot change it.
func IsLightOperation(operation contract.Operation) bool {
	switch operation {
	case contract.OperationEmbeddings, contract.OperationRerank, contract.OperationDecisions, contract.OperationSystemOne:
		return true
	default:
		return false
	}
}
