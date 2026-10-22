package connector

import (
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/connector/anthropic"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gateway"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gemini"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// DiscoveryModelsURL returns the model-list endpoint without transient page
// parameters. It never accepts or adds credentials.
func DiscoveryModelsURL(kind contract.Type, canonicalBaseURL string) (string, error) {
	switch kind {
	case contract.TypeOpenAICompatible:
		return openai.ModelsURL(canonicalBaseURL), nil
	case contract.TypeAnthropicCompatible:
		return anthropic.ModelsURL(canonicalBaseURL), nil
	case contract.TypeGoogleGemini:
		return gemini.ModelsURL(canonicalBaseURL), nil
	case contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4:
		return gateway.ModelsURL(canonicalBaseURL), nil
	default:
		return "", errors.New("connector: unsupported discovery type")
	}
}
