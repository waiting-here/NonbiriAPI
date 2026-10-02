package gateway

import (
	"encoding/json"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func cacheModel() gatewaypolicy.Model {
	return gatewaypolicy.Model{Adapter: gatewaypolicy.AnthropicEffort, MaxOutputTokens: 128000, Storage: gatewaypolicy.RejectStore, Cache: gatewaypolicy.AnthropicCache}
}
func TestCacheMarkersPreserveLocationsAndLifetime(t *testing.T) {
	req := chat(t, `{"model":"public","max_completion_tokens":128000,"cache_control":{"type":"ephemeral"},"messages":[{"role":"system","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"second"}]},{"role":"user","content":[{"type":"text","text":"prompt","cache_control":{"type":"ephemeral","ttl":"5m"}}]}],"tools":[{"type":"function","cache_control":{"type":"ephemeral","ttl":"1h"},"function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
	defer req.Clear()
	model := cacheModel()
	raw, err := CompileWithModel(req, "", model, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Max     int64                                   `json:"maxOutputTokens"`
		Options map[string]map[string]map[string]string `json:"providerOptions"`
		Prompt  []struct {
			Role    string                                  `json:"role"`
			Content json.RawMessage                         `json:"content"`
			Options map[string]map[string]map[string]string `json:"providerOptions"`
		} `json:"prompt"`
		Tools []struct {
			Options map[string]map[string]map[string]string `json:"providerOptions"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Max != 128000 || out.Options["anthropic"]["cacheControl"]["type"] != "ephemeral" || len(out.Prompt) != 3 || string(out.Prompt[0].Content) != `"first"` || string(out.Prompt[1].Content) != `"second"` || out.Prompt[1].Options != nil {
		t.Fatalf("root/system mapping: %s", raw)
	}
	// Decode provider options independently: cacheControl is a nested object.
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	prompt := tree["prompt"].([]any)
	getTTL := func(v any) string {
		return v.(map[string]any)["providerOptions"].(map[string]any)["anthropic"].(map[string]any)["cacheControl"].(map[string]any)["ttl"].(string)
	}
	if getTTL(prompt[0]) != "1h" || getTTL(prompt[2].(map[string]any)["content"].([]any)[0]) != "5m" || getTTL(tree["tools"].([]any)[0]) != "1h" {
		t.Fatal("cache lifetime moved")
	}
}
func TestCachePolicyAndInvalidMarkersAreExplicitRejections(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `{"type":"permanent"}`, `{"type":"ephemeral","ttl":"2h"}`, `{"type":"ephemeral","extra":1}`} {
		req := chat(t, controlPrompt+`,"cache_control":`+raw+`}`)
		_, err := CompileWithModel(req, "", cacheModel(), nil)
		req.Clear()
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Field != "cache_control" {
			t.Fatalf("marker %s: %v", raw, err)
		}
	}
	req := chat(t, controlPrompt+`,"cache_control":{"type":"ephemeral"}}`)
	defer req.Clear()
	if !SupportsRequest(req) {
		t.Fatal("syntax preflight rejected named cache capability")
	}
	_, err := CompileWithModel(req, "", gatewaypolicy.Model{}, nil)
	var rejection *contract.RequestRejection
	if !errors.As(err, &rejection) || rejection.Stage != "model preflight" || rejection.Field != "cache_control" {
		t.Fatalf("unverified mapping: %v", err)
	}
}
func TestCallerUsageExposesCacheWriteAndLegacyAlias(t *testing.T) {
	result := callerUsage(contract.Usage{Present: true, UncachedInputTokens: 10, CacheReadInputTokens: 20, CacheWriteInputTokens: 30, OutputTokens: 2})
	raw, _ := json.Marshal(result)
	var got struct {
		Details map[string]int64 `json:"prompt_tokens_details"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Details["cache_write_tokens"] != 30 || got.Details["cache_creation_tokens"] != 30 || got.Details["cached_tokens"] != 20 {
		t.Fatalf("usage %s", raw)
	}
}

func TestNativeCacheExtensionRequiresDeclaredMappingAndCannotOverrideCaller(t *testing.T) {
	native := map[string]json.RawMessage{"/providerOptions/anthropic/cacheControl": json.RawMessage(`{"type":"ephemeral","ttl":"5m"}`)}
	plain := chat(t, controlPrompt+"}")
	defer plain.Clear()
	_, err := CompileWithModel(plain, "", gatewaypolicy.Model{}, native)
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) || rejected.Field != "providerOptions.anthropic.cacheControl" {
		t.Fatalf("undeclared extension: %v", err)
	}
	if _, err := CompileWithModel(plain, "", cacheModel(), native); err != nil {
		t.Fatal(err)
	}
	explicit := chat(t, controlPrompt+`,"cache_control":{"type":"ephemeral","ttl":"1h"}}`)
	defer explicit.Clear()
	if _, err := CompileWithModel(explicit, "", cacheModel(), native); err == nil {
		t.Fatal("native extension replaced caller cache lifetime")
	}
}
