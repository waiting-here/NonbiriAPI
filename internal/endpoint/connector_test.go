package endpoint

import "testing"

// TestRegistryAcceptsOnlyOpenAICompatible asserts the alpha registry supports
// exactly openai-compatible and rejects every other type with no silent
// fallback to a different protocol.
func TestRegistryAcceptsOnlyOpenAICompatible(t *testing.T) {
	r := NewRegistry()

	// Ordinary surrounding spaces are normalized, but control characters are
	// rejected before normalization so they cannot disappear at a boundary.
	for _, raw := range []string{
		"openai-compatible",
		" openai-compatible",
		"openai-compatible ",
	} {
		got, err := r.MustValidate(ConnectorType(raw))
		if err != nil {
			t.Errorf("MustValidate(%q) rejected a registered type: %v", raw, err)
			continue
		}
		if got != ConnectorOpenAICompatible {
			t.Errorf("MustValidate(%q) = %q, want %q", raw, got, ConnectorOpenAICompatible)
		}
	}

	// Unknown types are rejected with no substitution. A case variant is
	// unknown: connector types are exact ASCII identifiers, not case-folded, so
	// "OpenAI-Compatible" is not silently treated as openai-compatible.
	for _, raw := range []string{
		"anthropic-compatible",
		"dify-app",
		"openai",
		"OPENAI-COMPATIBLE",
		"OpenAI-Compatible",
		"openai-compatible-v2",
		"openai-compatible\ninject",
		"openai-compatible\t",
		"\r\nopenai-compatible\r\n",
		"",
		"   ",
	} {
		if _, err := r.MustValidate(ConnectorType(raw)); err == nil {
			t.Errorf("MustValidate(%q) accepted an unregistered type (no fallback expected)", raw)
		}
	}
}

// TestRegistryZeroValueRejectsAll ensures a nil registry supports no types, so
// a Service constructed without a registry cannot accept any connector.
func TestRegistryZeroValueRejectsAll(t *testing.T) {
	var r *Registry
	if r.Supported(ConnectorOpenAICompatible) {
		t.Fatal("nil registry should not support any connector type")
	}
	if _, err := r.MustValidate(ConnectorOpenAICompatible); err == nil {
		t.Fatal("nil registry must reject even the canonical connector type")
	}
}
