package endpoint

import "testing"

// TestRegistryAcceptsFirstPartyConnectors asserts the registry supports
// exactly its two compiled connector types and never falls back for unknowns.
func TestRegistryAcceptsFirstPartyConnectors(t *testing.T) {
	r := NewRegistry()

	// Ordinary surrounding spaces are normalized, but control characters are
	// rejected before normalization so they cannot disappear at a boundary.
	for _, test := range []struct {
		raw  string
		want ConnectorType
	}{
		{"openai-compatible", ConnectorOpenAICompatible},
		{" openai-compatible", ConnectorOpenAICompatible},
		{"openai-compatible ", ConnectorOpenAICompatible},
		{"anthropic-compatible", ConnectorAnthropicCompatible},
		{" anthropic-compatible ", ConnectorAnthropicCompatible},
	} {
		got, err := r.MustValidate(ConnectorType(test.raw))
		if err != nil {
			t.Errorf("MustValidate(%q) rejected a registered type: %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("MustValidate(%q) = %q, want %q", test.raw, got, test.want)
		}
	}

	// Unknown types are rejected with no substitution. A case variant is
	// unknown: connector types are exact ASCII identifiers, not case-folded, so
	// "OpenAI-Compatible" is not silently treated as openai-compatible.
	for _, raw := range []string{
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
