package gateway

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

// cacheControl accepts the Anthropic cache lifetime vocabulary without changing
// the marker's position or choosing a different lifetime.
func cacheControl(raw json.RawMessage, model *gatewaypolicy.Model, field string) (object, error) {
	value, err := parseObject(raw)
	kind, ok := text(value["type"])
	if err != nil || !ok || kind != "ephemeral" || !only(value, "type", "ttl") {
		return nil, reject("native compile", field, "expected an ephemeral cache marker")
	}
	if raw, exists := value["ttl"]; exists {
		ttl, ok := text(raw)
		if !ok || ttl != "5m" && ttl != "1h" {
			return nil, reject("native compile", field, "cache lifetime must be 5m or 1h")
		}
	}
	if model != nil && model.Cache != gatewaypolicy.AnthropicCache {
		return nil, reject("model preflight", field, "cache mapping is not enabled for this model")
	}
	return value, nil
}
func applyCache(entry map[string]any, raw json.RawMessage, model *gatewaypolicy.Model, field string) error {
	if len(raw) == 0 {
		return nil
	}
	cache, err := cacheControl(raw, model, field)
	if err != nil {
		return err
	}
	entry["providerOptions"] = map[string]any{"anthropic": map[string]any{"cacheControl": cache}}
	return nil
}
