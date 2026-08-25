// Independent audit: the single authoritative connector registry must accept
// exactly the registered type and reject every lookalike with no silent
// fallback.
package endpoint_test

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
)

func TestAuditConnectorRegistryStrictness(t *testing.T) {
	registry := endpoint.NewRegistry()
	if !registry.Supported(endpoint.ConnectorOpenAICompatible) {
		t.Fatal("openai-compatible is not registered")
	}
	if !registry.Supported(endpoint.ConnectorAnthropicCompatible) {
		t.Fatal("anthropic-compatible is not registered")
	}
	// Zero-value registry supports nothing (fail closed).
	var empty endpoint.Registry
	if empty.Supported(endpoint.ConnectorOpenAICompatible) {
		t.Fatal("zero-value registry admits a connector")
	}
	if _, err := empty.MustValidate(endpoint.ConnectorOpenAICompatible); err == nil {
		t.Fatal("zero-value registry validates a connector")
	}

	for _, raw := range []string{
		"dify",
		"dify-app-api",
		"openai",
		"OpenAI-Compatible",
		"openai_compatible",
		"",
		"openai-compatible\u0000",
		"openai-compatible\n",
		"openai-compatible\t",
		"\topenai-compatible",
	} {
		t.Run(raw, func(t *testing.T) {
			if registry.Supported(endpoint.ConnectorType(raw)) {
				t.Fatalf("registry accepted %q", raw)
			}
			if _, err := registry.MustValidate(endpoint.ConnectorType(raw)); err == nil {
				t.Fatalf("MustValidate accepted %q", raw)
			}
		})
	}
	// Boundary ASCII spaces are tolerated by design (trim-then-match) and
	// MustValidate returns the canonical spelling, so the stored value is
	// always the registered form; the endpoint service pre-trims before
	// validation and stores the returned canonical type.
	for _, raw := range []string{"openai-compatible ", " openai-compatible", "  openai-compatible  "} {
		validated, err := registry.MustValidate(endpoint.ConnectorType(raw))
		if err != nil || validated != endpoint.ConnectorOpenAICompatible {
			t.Fatalf("padded %q => %q err=%v (canonical expected)", raw, validated, err)
		}
	}
	// The exact registered spelling round-trips.
	validated, err := registry.MustValidate(endpoint.ConnectorOpenAICompatible)
	if err != nil || validated != endpoint.ConnectorOpenAICompatible {
		t.Fatalf("registered type rejected: %v", err)
	}
}
