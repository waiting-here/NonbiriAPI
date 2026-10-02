package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

const controlPrompt = `{"model":"public-model","messages":[{"role":"system","content":"Keep this role"},{"role":"user","content":"Hi"}]`

func controlConfig(t *testing.T, adapter, efforts, storage string) gatewaypolicy.Config {
	t.Helper()
	config, err := gatewaypolicy.Parse(`{"models":[{"base_url":"https://upstream.example/native/v3/ai","model":"provider/private-model","adapter":"` + adapter + `","efforts":` + efforts + `,"storage":"` + storage + `","max_output_tokens":128000}]}`)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestOutputBudgetAliasesPreserveValues(t *testing.T) {
	for _, tc := range []struct {
		fields   string
		want     int64
		rejected bool
	}{
		{"", 0, false},
		{`,"max_completion_tokens":128000`, 128000, false},
		{`,"max_tokens":128000`, 128000, false},
		{`,"max_tokens":128000,"max_completion_tokens":128000`, 128000, false},
		{`,"max_tokens":null,"max_completion_tokens":128000`, 128000, false},
		{`,"max_tokens":null,"max_completion_tokens":null`, 0, false},
		{`,"max_completion_tokens":2147483647`, 2147483647, false},
		{`,"max_tokens":128,"max_completion_tokens":128000`, 0, true},
		{`,"max_completion_tokens":0`, 0, true},
		{`,"max_completion_tokens":-1`, 0, true},
		{`,"max_completion_tokens":1.5`, 0, true},
		{`,"max_completion_tokens":2147483648`, 0, true},
		{`,"max_completion_tokens":"128000"`, 0, true},
		{`,"max_completion_tokens":true`, 0, true},
		{`,"max_completion_tokens":[]`, 0, true},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			body, err := compileChat(chat(t, controlPrompt+tc.fields+"}"), "")
			if (err != nil) != tc.rejected {
				t.Fatalf("rejected=%v want=%v", err, tc.rejected)
			}
			if err != nil {
				return
			}
			var got struct {
				Budget int64 `json:"maxOutputTokens"`
			}
			if json.Unmarshal(body, &got) != nil || got.Budget != tc.want {
				t.Fatalf("budget=%d want=%d", got.Budget, tc.want)
			}
		})
	}
}

func TestReasoningAndStorageCompileOnlyForConfiguredModels(t *testing.T) {
	for _, tc := range []struct {
		adapter, efforts, storage, fields, namespace, effort string
		stored, adaptive, forced                             bool
	}{
		{gatewaypolicy.OpenAIChat, `["low","xhigh"]`, gatewaypolicy.OpenAIStore, `,"reasoning_effort":"xhigh","store":false`, "openai", "xhigh", true, false, true},
		{gatewaypolicy.OpenAIResponses, `["high","max"]`, gatewaypolicy.OpenAIStore, `,"reasoning_effort":"max","store":false`, "openai", "max", true, false, true},
		{gatewaypolicy.AnthropicEffort, `["high","max"]`, gatewaypolicy.OmitFalse, `,"reasoning_effort":"max","store":false`, "anthropic", "max", false, false, false},
		{gatewaypolicy.AnthropicAdaptive, `["high","max"]`, gatewaypolicy.RejectStore, `,"reasoning_effort":"high"`, "anthropic", "high", false, true, false},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			config := controlConfig(t, tc.adapter, tc.efforts, tc.storage)
			req := chat(t, controlPrompt+`,"max_completion_tokens":128000`+tc.fields+"}")
			body, err := CompileTarget(req, "server-label", target(), config, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Budget  int64                                 `json:"maxOutputTokens"`
				Options map[string]map[string]json.RawMessage `json:"providerOptions"`
			}
			if json.Unmarshal(body, &got) != nil || got.Budget != 128000 {
				t.Fatal("budget was changed")
			}
			options := got.Options[tc.namespace]
			effort := options["effort"]
			if tc.namespace == "openai" {
				effort = options["reasoningEffort"]
			}
			if string(effort) != `"`+tc.effort+`"` {
				t.Fatal("effort was changed")
			}
			if tc.stored && string(options["store"]) != "false" || !tc.stored && options["store"] != nil {
				t.Fatal("storage policy changed")
			}
			if tc.adaptive != (options["thinking"] != nil) {
				t.Fatal("thinking mode changed")
			}
			if tc.forced && (string(options["forceReasoning"]) != "true" || string(options["systemMessageMode"]) != `"system"`) {
				t.Fatal("provider could ignore reasoning or rewrite system roles")
			}
			if string(got.Options["gateway"]["user"]) != `"server-label"` {
				t.Fatal("attribution was overwritten")
			}
		})
	}
}

func TestUnverifiedControlsAndNativeOverridesRejectBeforeHTTP(t *testing.T) {
	anthropic := controlConfig(t, gatewaypolicy.AnthropicAdaptive, `["low","high","max"]`, gatewaypolicy.OmitFalse)
	effortOnly := controlConfig(t, gatewaypolicy.AnthropicEffort, `["high"]`, gatewaypolicy.RejectStore)
	openai := controlConfig(t, gatewaypolicy.OpenAIResponses, `["high","max"]`, gatewaypolicy.OpenAIStore)
	for _, tc := range []struct {
		fields string
		config gatewaypolicy.Config
		native map[string]json.RawMessage
	}{
		{fields: `,"reasoning_effort":"high"`},
		{fields: `,"store":false`},
		{fields: `,"reasoning_effort":"xhigh"`, config: anthropic},
		{fields: `,"reasoning_effort":"medium"`, config: anthropic},
		{fields: `,"reasoning_effort":null`, config: anthropic},
		{fields: `,"reasoning_effort":42`, config: anthropic},
		{fields: `,"store":true`, config: anthropic},
		{fields: `,"store":null`, config: openai},
		{fields: `,"store":"false"`, config: openai},
		{fields: `,"max_completion_tokens":128001`, config: anthropic},
		{fields: `,"reasoning_effort":"high","temperature":0`, config: anthropic},
		{fields: `,"reasoning_effort":"max","top_p":1`, config: openai},
		{fields: `,"seed":7`, config: openai},
		{fields: `,"temperature":0`, config: openai},
		{fields: `,"presence_penalty":0`, config: anthropic},
		{fields: `,"seed":7`, config: effortOnly},
		{fields: `,"temperature":1.1`, config: effortOnly},
		{fields: `,"temperature":0.5,"top_p":1`, config: effortOnly},
		{fields: `,"max_completion_tokens":128000`, config: openai, native: map[string]json.RawMessage{"/maxOutputTokens": json.RawMessage("64")}},
		{fields: `,"reasoning_effort":"high"`, config: anthropic, native: map[string]json.RawMessage{"/providerOptions/anthropic/thinking": json.RawMessage(`{"type":"enabled","budgetTokens":1024}`)}},
		{fields: `,"store":false`, config: openai, native: map[string]json.RawMessage{"/providerOptions/openai/store": json.RawMessage("true")}},
		{fields: `,"private_unknown_field":"PRIVATE_PAYLOAD"`, config: openai},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
				t.Fatal("unsupported request dispatched")
				return nil, nil
			}}
			adapter, _ := NewAdapterWithModels(b, tc.config)
			result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(), target(), secret(), chat(t, controlPrompt+tc.fields+"}"), nil, "", contract.AttemptPolicy{NativeExtensions: tc.native})
			if result.Success || b.calls != 0 {
				t.Fatal("unsupported request succeeded")
			}
			if strings.Contains(result.Diagnostic, "PRIVATE_PAYLOAD") || strings.Contains(result.Diagnostic, "private_unknown_field") || strings.Contains(result.Diagnostic, "private-model") {
				t.Fatal("diagnostic leaked request data")
			}
			if result.Diagnostic == "gateway attempt unavailable" {
				t.Fatal("missing field/stage diagnostic")
			}
		})
	}
}

func TestControlsReachNativeHTTPInBothResponseModes(t *testing.T) {
	config := controlConfig(t, gatewaypolicy.OpenAIResponses, `["high","max"]`, gatewaypolicy.OpenAIStore)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				var got map[string]json.RawMessage
				if json.Unmarshal(raw, &got) != nil || string(got["maxOutputTokens"]) != "128000" {
					t.Fatal("native budget changed")
				}
				if !bytes.Contains(got["providerOptions"], []byte(`"reasoningEffort":"max"`)) || !bytes.Contains(got["providerOptions"], []byte(`"store":false`)) {
					t.Fatal("native controls missing")
				}
				if r.Header.Get("ai-language-model-specification-version") != "3" {
					t.Fatal("wrong native protocol")
				}
				if stream {
					return response("data: "+`{"type":"text-start","id":"t"}`+"\n\ndata: "+`{"type":"text-delta","id":"t","delta":"Hello"}`+"\n\ndata: "+`{"type":"text-end","id":"t"}`+"\n\ndata: "+`{"type":"finish","finishReason":{"unified":"stop","raw":"stop"},"usage":`+knownUsage+"}\n\n", "text/event-stream"), nil
				}
				return response(chatOK, "application/json"), nil
			}}
			adapter, _ := NewAdapterWithModels(b, config)
			streamField := ""
			if stream {
				streamField = `,"stream":true,"stream_options":{"include_usage":true}`
			}
			w := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), w, target(), secret(), chat(t, controlPrompt+`,"max_completion_tokens":128000,"reasoning_effort":"max","store":false`+streamField+"}"), nil, "")
			if !result.Success || b.calls != 1 || !result.Usage.Present {
				t.Fatalf("result=%+v calls=%d", result, b.calls)
			}
			if stream && (!strings.Contains(w.Body.String(), "[DONE]") || !strings.Contains(w.Body.String(), `"usage":`)) {
				t.Fatal("stream usage or terminal marker lost")
			}
		})
	}
}

func TestControlRejectionIdentifiesKnownFieldAndStage(t *testing.T) {
	_, err := CompileTarget(chat(t, controlPrompt+`,"reasoning_effort":"PRIVATE_VALUE"}`), "", target(), gatewaypolicy.Config{}, nil)
	var rejection *contract.RequestRejection
	if !errors.As(err, &rejection) || rejection.Field != "reasoning_effort" || rejection.Stage != "native compile" || strings.Contains(err.Error(), "PRIVATE_VALUE") {
		t.Fatal("unsafe or unlocatable rejection")
	}
}

func TestLegacyBudgetUsesConfiguredReasoningPath(t *testing.T) {
	config := controlConfig(t, gatewaypolicy.OpenAIChat, `["high"]`, gatewaypolicy.RejectStore)
	body, err := CompileTarget(chat(t, controlPrompt+`,"max_tokens":128000}`), "", target(), config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"maxOutputTokens":128000`)) || !bytes.Contains(body, []byte(`"forceReasoning":true`)) || !bytes.Contains(body, []byte(`"systemMessageMode":"system"`)) {
		t.Fatal("legacy budget or role semantics could be lost by the configured reasoning provider")
	}
}

func TestAlwaysAdaptiveProviderPreservesEffortAndRejectsLostControls(t *testing.T) {
	config := controlConfig(t, gatewaypolicy.AnthropicAlwaysAdaptive, `["low","medium","high","xhigh","max"]`, gatewaypolicy.OmitFalse)
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		body, err := CompileTarget(chat(t, controlPrompt+`,"max_completion_tokens":128000,"reasoning_effort":"`+effort+`","store":false}`), "", target(), config, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Budget  int64                                 `json:"maxOutputTokens"`
			Options map[string]map[string]json.RawMessage `json:"providerOptions"`
		}
		if json.Unmarshal(body, &got) != nil || got.Budget != 128000 || string(got.Options["anthropic"]["effort"]) != `"`+effort+`"` || string(got.Options["anthropic"]["thinking"]) != `{"type":"adaptive"}` {
			t.Fatal("adaptive controls changed")
		}
	}
	for _, fields := range []string{`,"temperature":1`, `,"top_p":1`, `,"top_k":0`, `,"tool_choice":"required"`, `,"tool_choice":"re\u0071uired"`, `,"tool_choice":{"type":"function","function":{"name":"test"}}`} {
		_, err := CompileTarget(chat(t, controlPrompt+`,"tools":[{"type":"function","function":{"name":"test","parameters":{"type":"object"}}}]`+fields+`}`), "", target(), config, nil)
		if err == nil {
			t.Fatal("provider would discard a control")
		}
	}
	for _, path := range []string{"/providerOptions/bedrock/effort", "/providerOptions/bedrock/thinking"} {
		_, err := CompileTarget(chat(t, controlPrompt+`,"max_tokens":128000,"reasoning_effort":"high"}`), "", target(), config, map[string]json.RawMessage{path: json.RawMessage(`"low"`)})
		if err == nil {
			t.Fatal("custom provider namespace overrides canonical control")
		}
	}
}
