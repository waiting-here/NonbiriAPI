package endpoint

import (
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// Endpoint keeps compatibility names for persisted connector identifiers,
// while the descriptor registry and execution capability catalog have one
// authoritative implementation in internal/connector.
type ConnectorType = connectorcontract.Type

const ConnectorOpenAICompatible = connectorcontract.TypeOpenAICompatible

type Registry = connector.Registry

// NewRegistry returns the immutable compile-time connector catalog for this
// binary. Unknown and disabled connector types fail closed with no protocol
// fallback.
func NewRegistry() *Registry { return connector.NewDefaultRegistry() }
