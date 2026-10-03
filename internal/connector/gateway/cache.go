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
	if model != nil && model.Cache != gatewaypolicy.AnthropicCache && model.Cache != gatewaypolicy.AnthropicExplicitCache {
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

// finalizeCache counts native breakpoints and lowers the automatic marker only
// for the exact target configured to require explicit Anthropic caching.
func finalizeCache(root map[string]any, model *gatewaypolicy.Model) error {
	var blocks []map[string]any
	if tools, ok := root["tools"].([]any); ok {
		for _, value := range tools {
			if tool, ok := value.(map[string]any); ok {
				blocks = append(blocks, tool)
			}
		}
	}
	if prompt, ok := root["prompt"].([]any); ok {
		for _, value := range cachePromptOrder(prompt) {
			message, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if message["role"] == "system" {
				blocks = append(blocks, message)
				continue
			}
			if content, ok := message["content"].([]any); ok {
				for _, value := range content {
					if block, ok := value.(map[string]any); ok {
						blocks = append(blocks, block)
					}
				}
			}
		}
	}
	markers := 0
	for _, block := range blocks {
		if nativeCache(block) != nil {
			markers++
		}
	}
	rootOptions, _ := root["providerOptions"].(map[string]any)
	if value, present := root["providerOptions"]; present && value != nil && rootOptions == nil {
		return errRequest
	}
	anthropic, _ := rootOptions["anthropic"].(map[string]any)
	if value, present := rootOptions["anthropic"]; present && value != nil && anthropic == nil {
		return errRequest
	}
	rootCache, rootMarker := anthropic["cacheControl"]
	alias, aliasMarker := anthropic["cache_control"]
	if rootMarker && aliasMarker {
		return reject("native adaptation", "providerOptions.anthropic.cacheControl", "duplicate cache controls")
	}
	if aliasMarker {
		rootCache, rootMarker = alias, true
	}
	if rootMarker {
		raw, err := json.Marshal(rootCache)
		if err != nil {
			return errRequest
		}
		cache, err := cacheControl(raw, model, "providerOptions.anthropic.cacheControl")
		if err != nil {
			return err
		}
		if model != nil && model.Cache == gatewaypolicy.AnthropicExplicitCache {
			last := lastCacheBlock(blocks)
			if last == nil {
				return reject("native compile", "cache_control", "no eligible cache block")
			}
			if prior := nativeCache(last); prior != nil {
				if cacheTTL(prior) != cacheTTL(cache) {
					return reject("native compile", "cache_control", "final cache block has a conflicting lifetime")
				}
			} else {
				options, _ := last["providerOptions"].(map[string]any)
				if options == nil {
					options = map[string]any{}
					last["providerOptions"] = options
				}
				option(options, "anthropic")["cacheControl"] = cache
				markers++
			}
			options := root["providerOptions"].(map[string]any)
			anthropic := options["anthropic"].(map[string]any)
			delete(anthropic, "cacheControl")
			delete(anthropic, "cache_control")
			if len(anthropic) == 0 {
				delete(options, "anthropic")
			}
			if len(options) == 0 {
				delete(root, "providerOptions")
			}
		} else {
			last := lastCacheBlock(blocks)
			if prior := nativeCache(last); prior != nil {
				if cacheTTL(prior) != cacheTTL(cache) {
					return reject("native compile", "cache_control", "final cache block has a conflicting lifetime")
				}
			} else {
				markers++
			}
		}
	}
	if markers > 4 {
		return reject("native compile", "cache_control", "at most four cache breakpoints are allowed")
	}
	shorterTTL := false
	for _, block := range blocks {
		if marker := nativeCache(block); marker != nil {
			if cacheTTL(marker) == "1h" {
				if shorterTTL {
					return reject("native compile", "cache_control", "1h cache breakpoints must precede 5m cache breakpoints")
				}
			} else {
				shorterTTL = true
			}
		}
	}
	// Root-native automatic caching still contributes a final effective marker.
	if rootMarker && (model == nil || model.Cache != gatewaypolicy.AnthropicExplicitCache) && cacheTTL(rootCache) == "1h" && shorterTTL {
		return reject("native compile", "cache_control", "1h cache breakpoints must precede 5m cache breakpoints")
	}
	return nil
}

func nativeCache(block map[string]any) any {
	options, _ := block["providerOptions"].(map[string]any)
	anthropic, _ := options["anthropic"].(map[string]any)
	return anthropic["cacheControl"]
}

func cacheTTL(cache any) string {
	raw, _ := json.Marshal(cache)
	var value struct {
		TTL string `json:"ttl"`
	}
	_ = json.Unmarshal(raw, &value)
	if value.TTL == "" {
		return "5m"
	}
	return value.TTL
}

func lastCacheBlock(blocks []map[string]any) map[string]any {
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if block["role"] == "system" {
			if value, ok := block["content"].(string); ok && value != "" {
				return block
			}
			continue
		}
		switch block["type"] {
		case "text":
			if value, ok := block["text"].(string); ok && value != "" {
				return block
			}
		case "file", "tool-call", "tool-result", "function":
			return block
		}
	}
	return nil
}

// The provider hoists the first plain system block into the top-level system
// prefix. Later system blocks remain in the message sequence.
func cachePromptOrder(prompt []any) []any {
	for first, value := range prompt {
		message, ok := value.(map[string]any)
		if !ok || message["role"] != "system" {
			continue
		}
		if first == 0 {
			return prompt
		}
		end := first + 1
		for end < len(prompt) {
			message, ok := prompt[end].(map[string]any)
			if !ok || message["role"] != "system" {
				break
			}
			end++
		}
		ordered := make([]any, 0, len(prompt))
		ordered = append(ordered, prompt[first:end]...)
		ordered = append(ordered, prompt[:first]...)
		return append(ordered, prompt[end:]...)
	}
	return prompt
}
