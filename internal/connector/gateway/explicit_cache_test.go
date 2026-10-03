package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func explicitCacheModel() gatewaypolicy.Model {
	model := cacheModel()
	model.Cache = gatewaypolicy.AnthropicExplicitCache
	return model
}

func compiledTree(t *testing.T, raw string, native map[string]json.RawMessage) map[string]any {
	t.Helper()
	req := chat(t, raw)
	defer req.Clear()
	body, err := CompileWithModel(req, "", explicitCacheModel(), native)
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if json.Unmarshal(body, &tree) != nil {
		t.Fatal(string(body))
	}
	return tree
}

func TestExplicitCacheTargetsFinalBlockAndPreservesEarlierMarkers(t *testing.T) {
	tree := compiledTree(t, `{"model":"x","cache_control":{"type":"ephemeral","ttl":"5m"},"messages":[{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"last"}]}]}`, nil)
	if nativeCache(tree) != nil {
		t.Fatal("root marker was not lowered")
	}
	prompt := tree["prompt"].([]any)
	system := prompt[0].(map[string]any)
	parts := prompt[1].(map[string]any)["content"].([]any)
	if cacheTTL(nativeCache(system)) != "1h" || nativeCache(parts[0].(map[string]any)) != nil || cacheTTL(nativeCache(parts[1].(map[string]any))) != "5m" {
		t.Fatalf("wrong cache locations: %+v", tree)
	}
	for _, content := range []string{`[{"type":"text","text":"prompt"},{"type":"text","text":""}]`, `[{"type":"image_url","image_url":{"url":"https://images.example/a.png"}}]`} {
		tree := compiledTree(t, `{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":`+content+`}]}`, nil)
		parts := tree["prompt"].([]any)[0].(map[string]any)["content"].([]any)
		if nativeCache(parts[0].(map[string]any)) == nil {
			t.Fatalf("eligible block skipped: %+v", tree)
		}
	}
}

func TestExplicitCacheMergeAndBreakpointLimit(t *testing.T) {
	marker := `{"type":"ephemeral","ttl":"5m"}`
	four := `[{"type":"text","text":"a","cache_control":` + marker + `},{"type":"text","text":"b","cache_control":` + marker + `},{"type":"text","text":"c","cache_control":` + marker + `},{"type":"text","text":"d","cache_control":` + marker + `}]`
	compiledTree(t, `{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":`+four+`}]}`, nil)
	for _, raw := range []string{
		`{"model":"x","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":` + four + `}]}`,
		`{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":` + strings.TrimSuffix(four, "]") + `,{"type":"text","text":"e"}]}]}`,
		`{"model":"x","messages":[{"role":"user","content":` + strings.TrimSuffix(four, "]") + `,{"type":"text","text":"e","cache_control":` + marker + `}]}]}`,
		`{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":""}]}`,
	} {
		req := chat(t, raw)
		_, err := CompileWithModel(req, "", explicitCacheModel(), nil)
		req.Clear()
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Stage != "native compile" || rejection.Field != "cache_control" {
			t.Fatalf("missing named rejection: %v", err)
		}
	}
}

const toolHistory = `{"model":"x","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-a","content":`

func TestToolTextArraysPreserveIdentityAndCachePosition(t *testing.T) {
	for _, content := range []string{
		`[{"type":"text","text":"answer","cache_control":{"type":"ephemeral","ttl":"1h"}}]`,
		`[{"type":"text","text":"a\n"},{"type":"text","text":"b","cache_control":{"type":"ephemeral","ttl":"1h"}}]`,
	} {
		raw := toolHistory + content + `}]}`
		req := chat(t, raw)
		if !SupportsRequest(req) {
			t.Fatal("tool cache capability lost")
		}
		req.Clear()
		tree := compiledTree(t, raw, nil)
		part := tree["prompt"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
		if part["toolCallId"] != "call-a" || part["toolName"] != "lookup" || cacheTTL(nativeCache(part)) != "1h" {
			t.Fatalf("%+v", part)
		}
		output := part["output"].(map[string]any)
		if strings.HasPrefix(content, `[{"type":"text","text":"answer"`) {
			if output["type"] != "text" || output["value"] != "answer" {
				t.Fatalf("%+v", output)
			}
		} else {
			blocks := output["value"].([]any)
			if output["type"] != "content" || len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "a\n" || blocks[1].(map[string]any)["text"] != "b" {
				t.Fatalf("%+v", output)
			}
		}
	}
	raw := toolHistory + `[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}},{"type":"text","text":"b"}]}]}`
	req := chat(t, raw)
	defer req.Clear()
	_, err := CompileWithModel(req, "", explicitCacheModel(), nil)
	var rejection *contract.RequestRejection
	if !errors.As(err, &rejection) || rejection.Field != "messages[role=tool].content[].cache_control" {
		t.Fatalf("%v", err)
	}
	for _, content := range []string{`[{"type":"text","text":"x","extra":true}]`, `[{"type":"image_url","image_url":{"url":"https://images.example/a.png"}}]`} {
		req := chat(t, toolHistory+content+`}]}`)
		if SupportsRequest(req) {
			t.Fatal("unsupported tool content accepted")
		}
		req.Clear()
	}
	tree := compiledTree(t, strings.TrimSuffix(toolHistory+`[{"type":"text","text":"answer"}]}]}`, "}")+`,"cache_control":{"type":"ephemeral"}}`, nil)
	part := tree["prompt"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if nativeCache(part) == nil {
		t.Fatal("tool result was not the final cache block")
	}
}

func TestExplicitNativeCacheLoweringAndProtectedMappings(t *testing.T) {
	native := map[string]json.RawMessage{"/providerOptions/anthropic/cacheControl": json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)}
	tree := compiledTree(t, `{"model":"x","messages":[{"role":"user","content":"hello"}]}`, native)
	part := tree["prompt"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if nativeCache(tree) != nil || cacheTTL(nativeCache(part)) != "1h" {
		t.Fatalf("%+v", tree)
	}
	req := chat(t, `{"model":"x","messages":[{"role":"user","content":"hello"}],"cache_control":{"type":"ephemeral"}}`)
	defer req.Clear()
	for _, native := range []map[string]json.RawMessage{
		native,
		{"/prompt": json.RawMessage(`[]`)},
		{"/tools": json.RawMessage(`[]`)},
	} {
		_, err := CompileWithModel(req, "", explicitCacheModel(), native)
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Stage != "native adaptation" {
			t.Fatalf("%v", err)
		}
	}
	req2 := chat(t, `{"model":"x","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{\"id\":9007199254740993}"}}]}]}`)
	defer req2.Clear()
	body, err := CompileWithModel(req2, "", explicitCacheModel(), map[string]json.RawMessage{"/providerOptions/anthropic/effort": json.RawMessage(`"low"`)})
	if err != nil || !strings.Contains(string(body), "9007199254740993") {
		t.Fatalf("%s %v", body, err)
	}
}

func TestEmptyRefusalPreservesUsageForBothCallerModesAndFlatten(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, flatten := range []bool{false, true} {
			raw, kind := `{"content":[],"finishReason":{"unified":"content-filter","raw":"refusal"},"usage":`+knownUsage+`,"warnings":[]}`, "application/json"
			request := prompt
			if stream {
				raw, kind = sse(`{"type":"stream-start","warnings":[]}`, `{"type":"finish","finishReason":{"unified":"content-filter","raw":"refusal"},"usage":`+knownUsage+`}`), "text/event-stream"
				request = strings.TrimSuffix(prompt, "}") + `,"stream":true,"stream_options":{"include_usage":true}}`
			}
			backend := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(raw, kind), nil }}
			adapter, _ := NewAdapter(backend)
			writer := httptest.NewRecorder()
			req := chat(t, request)
			result := adapter.AttemptWithPolicy(context.Background(), writer, target(), secret(), req, nil, "", contract.AttemptPolicy{FlattenToolCalls: flatten})
			req.Clear()
			if !result.Success || !result.Usage.Present || result.Usage.CacheReadInputTokens != 3 || result.Usage.CacheWriteInputTokens != 2 || result.Usage.OutputTokens != 5 || !strings.Contains(writer.Body.String(), `"finish_reason":"content_filter"`) || !strings.Contains(writer.Body.String(), `"completion_tokens":5`) || strings.Contains(writer.Body.String(), `"content":"Hello"`) {
				t.Fatalf("stream=%v flatten=%v %+v %s", stream, flatten, result, writer.Body)
			}
			if stream && !strings.Contains(writer.Body.String(), "[DONE]") {
				t.Fatal(writer.Body.String())
			}
		}
	}
	for _, reason := range []string{"stop", "length", "tool-calls"} {
		raw := `{"content":[],"finishReason":{"unified":"` + reason + `"},"usage":` + knownUsage + `}`
		if _, _, err := translateChat([]byte(raw), "x", 1); err == nil {
			t.Fatal("empty ordinary generation accepted")
		}
		backend := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
			return response(sse(`{"type":"finish","finishReason":{"unified":"`+reason+`"},"usage":`+knownUsage+`}`), "text/event-stream"), nil
		}}
		adapter, _ := NewAdapter(backend)
		writer := httptest.NewRecorder()
		req := chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`)
		result := adapter.Attempt(context.Background(), writer, target(), secret(), req, nil, "")
		req.Clear()
		if result.Success || result.Committed || writer.Body.Len() != 0 {
			t.Fatalf("%+v %s", result, writer.Body)
		}
	}
}

func TestNativeCacheAliasesCannotBypassPolicyOrMarkers(t *testing.T) {
	request := chat(t, `{"model":"x","messages":[{"role":"user","content":"hello"}]}`)
	defer request.Clear()
	for _, name := range []string{"cacheControl", "cache_control"} {
		path := "/providerOptions/anthropic/" + name
		for _, raw := range []string{`null`, `false`, `{"type":"ephemeral","ttl":"2h"}`, `{"type":"ephemeral","extra":1}`} {
			_, err := CompileWithModel(request, "", explicitCacheModel(), map[string]json.RawMessage{path: json.RawMessage(raw)})
			var rejection *contract.RequestRejection
			if !errors.As(err, &rejection) {
				t.Fatalf("invalid native marker accepted: %s %v", name, err)
			}
		}
		_, err := CompileWithModel(request, "", gatewaypolicy.Model{}, map[string]json.RawMessage{path: json.RawMessage(`{"type":"ephemeral"}`)})
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Stage != "model preflight" {
			t.Fatalf("policy bypassed: %v", err)
		}
	}
	tree := compiledTree(t, `{"model":"x","messages":[{"role":"user","content":"hello"}]}`, map[string]json.RawMessage{"/providerOptions/anthropic/cache_control": json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)})
	part := tree["prompt"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tree["providerOptions"] != nil || cacheTTL(nativeCache(part)) != "1h" {
		t.Fatalf("%+v", tree)
	}
	explicit := chat(t, `{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":"hello"}]}`)
	defer explicit.Clear()
	_, err := CompileWithModel(explicit, "", explicitCacheModel(), map[string]json.RawMessage{"/providerOptions/anthropic/cache_control": json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)})
	var rejection *contract.RequestRejection
	if !errors.As(err, &rejection) || rejection.Stage != "native adaptation" {
		t.Fatalf("translated cache overwritten: %v", err)
	}
}

func TestRootNativeCacheKeepsRootAndMergesFinalBreakpointSlot(t *testing.T) {
	req := chat(t, `{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}},{"type":"text","text":"b","cache_control":{"type":"ephemeral"}},{"type":"text","text":"c","cache_control":{"type":"ephemeral"}},{"type":"text","text":"d","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
	defer req.Clear()
	body, err := CompileWithModel(req, "", cacheModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if json.Unmarshal(body, &tree) != nil || nativeCache(tree) == nil {
		t.Fatal("existing root-native mode changed")
	}
}

func TestMixedCacheTTLsFollowEffectiveBreakpointOrder(t *testing.T) {
	for _, model := range []gatewaypolicy.Model{explicitCacheModel(), cacheModel()} {
		for _, tc := range []struct {
			name, raw string
			valid     bool
		}{
			{"long-before-short", `{"model":"x","cache_control":{"type":"ephemeral"},"messages":[{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"user","content":"last"}]}`, true},
			{"short-before-long", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"last","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, false},
			{"default-short-before-long", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral"}},{"type":"text","text":"last","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, false},
			{"automatic-long-after-short", `{"model":"x","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"5m"}}]},{"role":"user","content":"last"}]}`, false},
			{"late-first-system-long-before-user-short", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"user","cache_control":{"type":"ephemeral"}}]},{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, true},
			{"late-first-system-short-before-user-long", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"user","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}]}]}`, false},
			{"tools-long-before-prompt-short", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"last","cache_control":{"type":"ephemeral","ttl":"5m"}}]}],"tools":[{"type":"function","cache_control":{"type":"ephemeral","ttl":"1h"},"function":{"name":"lookup","parameters":{"type":"object"}}}]}`, true},
			{"tools-short-before-prompt-long", `{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"last","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],"tools":[{"type":"function","cache_control":{"type":"ephemeral"},"function":{"name":"lookup","parameters":{"type":"object"}}}]}`, false},
		} {
			t.Run(model.Cache+"/"+tc.name, func(t *testing.T) {
				req := chat(t, tc.raw)
				defer req.Clear()
				_, err := CompileWithModel(req, "", model, nil)
				if tc.valid {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var rejection *contract.RequestRejection
					if !errors.As(err, &rejection) || rejection.Stage != "native compile" || rejection.Field != "cache_control" || rejection.Reason != "1h cache breakpoints must precede 5m cache breakpoints" {
						t.Fatalf("missing TTL-order rejection: %v", err)
					}
				}
			})
		}
	}
	// Native defaults are checked after lowering too.
	req := chat(t, `{"model":"x","messages":[{"role":"system","content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":"last"}]}`)
	defer req.Clear()
	for _, name := range []string{"cacheControl", "cache_control"} {
		_, err := CompileWithModel(req, "", explicitCacheModel(), map[string]json.RawMessage{"/providerOptions/anthropic/" + name: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)})
		var rejection *contract.RequestRejection
		if !errors.As(err, &rejection) || rejection.Stage != "native compile" || rejection.Field != "cache_control" {
			t.Fatalf("native TTL-order bypass: %v", err)
		}
	}
}
