package connector

import (
	"errors"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// DiscoveryModelsURL returns the configured complete catalog address. Only
// protocol pagination adds query parameters when a request is made.
func DiscoveryModelsURL(kind contract.Type, catalogURL string) (string, error) {
	switch kind {
	case contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible,
		contract.TypeGoogleGemini, contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4:
		return catalogURL, nil
	default:
		return "", errors.New("connector: unsupported discovery type")
	}
}
