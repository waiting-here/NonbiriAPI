package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestCompileRequestTextAndSystemGolden(t *testing.T) {
	request := mustChatRequest(t, `{
		"model":"public/model",
		"messages":[
			{"role":"system","content":"system"},
			{"role":"user","content":"hello"},
			{"role":"developer","content":[{"type":"text","text":"developer"}]}
		],
		"stream":null
	}`)

	body, err := compileRequest(request, "claude-test", "nbu_v3_test", BuiltInDefaultMaxTokens)
	if err != nil {
		t.Fatalf("compileRequest: %v", err)
	}
	defer clear(body)
	want := `{"model":"claude-test","max_tokens":65536,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"system":"system\ndeveloper","metadata":{"user_id":"nbu_v3_test"}}`
	if string(body) != want {
		t.Fatalf("body=%s\nwant=%s", body, want)
	}
}

func TestCompileRequestDoesNotHTMLExpandTranslatedPrompt(t *testing.T) {
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"system","content":"<system>&"},{"role":"user","content":"<prompt>&"}]}`)
	body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	if !strings.Contains(string(body), `"system":"<system>&"`) || !strings.Contains(string(body), `"text":"<prompt>&"`) || strings.Contains(string(body), `\u003c`) || strings.Contains(string(body), `\u003e`) || strings.Contains(string(body), `\u0026`) {
		t.Fatalf("translated prompt unexpectedly HTML-escaped: %s", body)
	}
}

func TestTranslatedOutboundRequestExactAndLimitPlusOne(t *testing.T) {
	request := outboundRequest{
		Model: "claude", MaxTokens: 1,
		Messages: []outboundMessage{{Role: "user", Content: []outboundBlock{newTextBlock("x")}}},
		Metadata: outboundMetadata{UserID: "nbu"}, System: "x",
	}
	seed, err := marshalJSONNoEscapeLimited(request, MaxTranslatedRequestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixed := len(seed) - 1
	clear(seed)
	request.System = string(bytes.Repeat([]byte{'x'}, int(MaxTranslatedRequestBytes)-fixed))
	exact, err := marshalJSONNoEscapeLimited(request, MaxTranslatedRequestBytes)
	if err != nil || int64(len(exact)) != MaxTranslatedRequestBytes {
		clear(exact)
		t.Fatalf("exact translated request len=%d err=%v", len(exact), err)
	}
	clear(exact)
	request.System += "x"
	over, err := marshalJSONNoEscapeLimited(request, MaxTranslatedRequestBytes)
	clear(over)
	if !errors.Is(err, errJSONOutputLimit) {
		t.Fatalf("translated request limit+1 err=%v", err)
	}
}

func TestCompileRequestPreservesExplicitEmptyTextBlocks(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "user text",
			body: `{"model":"p/m","messages":[{"role":"user","content":""}]}`,
			want: `{"model":"claude","max_tokens":65536,"messages":[{"role":"user","content":[{"type":"text","text":""}]}],"metadata":{"user_id":"nbu"}}`,
		},
		{
			name: "assistant and tool result text",
			body: `{"model":"p/m","messages":[{"role":"user","content":"call"},{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{\"x\":1}"}}]},{"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":""}]}]}`,
			want: `{"model":"claude","max_tokens":65536,"messages":[{"role":"user","content":[{"type":"text","text":"call"}]},{"role":"assistant","content":[{"type":"text","text":""},{"type":"tool_use","id":"call_1","name":"f","input":{"x":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":""}]}]}],"metadata":{"user_id":"nbu"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustChatRequest(t, test.body)
			body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(body)
			if string(body) != test.want {
				t.Fatalf("body=%s\nwant=%s", body, test.want)
			}
		})
	}
}

func TestMaxTokensWireMatrix(t *testing.T) {
	tests := []struct {
		name      string
		fields    string
		fallback  int64
		want      int64
		wantError bool
	}{
		{name: "absent uses fallback", fallback: 65536, want: 65536},
		{name: "both null use fallback", fields: `,"max_tokens":null,"max_completion_tokens":null`, fallback: 70000, want: 70000},
		{name: "max tokens", fields: `,"max_tokens":7`, fallback: 65536, want: 7},
		{name: "completion tokens", fields: `,"max_completion_tokens":8`, fallback: 65536, want: 8},
		{name: "equal dual", fields: `,"max_tokens":9,"max_completion_tokens":9`, fallback: 65536, want: 9},
		{name: "one null", fields: `,"max_tokens":null,"max_completion_tokens":10`, fallback: 65536, want: 10},
		{name: "hard maximum", fields: `,"max_tokens":2147483647`, fallback: 65536, want: 2147483647},
		{name: "conflict", fields: `,"max_tokens":9,"max_completion_tokens":10`, fallback: 65536, wantError: true},
		{name: "zero", fields: `,"max_tokens":0`, fallback: 65536, wantError: true},
		{name: "negative", fields: `,"max_tokens":-1`, fallback: 65536, wantError: true},
		{name: "fraction", fields: `,"max_tokens":1.5`, fallback: 65536, wantError: true},
		{name: "exponent", fields: `,"max_tokens":1e2`, fallback: 65536, wantError: true},
		{name: "string", fields: `,"max_tokens":"10"`, fallback: 65536, wantError: true},
		{name: "hard maximum plus one", fields: `,"max_tokens":2147483648`, fallback: 65536, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]`+test.fields+`}`)
			body, err := compileRequest(request, "claude", "nbu", test.fallback)
			defer clear(body)
			if test.wantError {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("err=%v, want invalid request", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compileRequest: %v", err)
			}
			var outbound outboundRequest
			if err := json.Unmarshal(body, &outbound); err != nil {
				t.Fatal(err)
			}
			if outbound.MaxTokens != test.want {
				t.Fatalf("max_tokens=%d want=%d", outbound.MaxTokens, test.want)
			}
		})
	}
}

func TestDefaultMaxTokensProviderMatrix(t *testing.T) {
	value := func(n int64) *int64 { return &n }
	providerError := errors.New("site configuration unavailable")
	tests := []struct {
		name      string
		provider  maxTokensProviderFunc
		want      int64
		wantError bool
	}{
		{name: "nil provider", want: BuiltInDefaultMaxTokens},
		{name: "raw null", provider: func(context.Context) (*int64, error) { return nil, nil }, want: BuiltInDefaultMaxTokens},
		{name: "lower override", provider: func(context.Context) (*int64, error) { return value(1024), nil }, want: 1024},
		{name: "higher override", provider: func(context.Context) (*int64, error) { return value(100000), nil }, want: 100000},
		{name: "maximum override", provider: func(context.Context) (*int64, error) { return value(MaxMaxTokens), nil }, want: MaxMaxTokens},
		{name: "zero rejected", provider: func(context.Context) (*int64, error) { return value(0), nil }, wantError: true},
		{name: "overflow rejected", provider: func(context.Context) (*int64, error) { return value(MaxMaxTokens + 1), nil }, wantError: true},
		{name: "provider error", provider: func(context.Context) (*int64, error) { return nil, providerError }, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var provider connectorcontract.AnthropicDefaultMaxTokensProvider
			if test.provider != nil {
				provider = test.provider
			}
			got, err := resolveDefaultMaxTokens(context.Background(), provider)
			if test.wantError {
				if !errors.Is(err, ErrDefaultTokens) {
					t.Fatalf("err=%v want=%v", err, ErrDefaultTokens)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("got=%d err=%v want=%d", got, err, test.want)
			}
		})
	}
}

func TestCompileRequestToolsImagesAndSampling(t *testing.T) {
	largeImage := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("i", 12<<10)))
	request := mustChatRequest(t, `{
		"model":"p/m",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"look"},
				{"type":"image_url","image_url":{"url":"https://images.example/a.png"}},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,`+largeImage+`"}}
			]},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":"result"}]}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],
		"tool_choice":{"type":"function","function":{"name":"lookup"}},
		"parallel_tool_calls":false,
		"temperature":0.2,
		"top_p":0.8,
		"stop":["END","STOP"],
		"stream_options":{"include_usage":true}
	}`)
	body, err := compileRequest(request, "claude", "nbu", 321)
	if err != nil {
		t.Fatalf("compileRequest: %v", err)
	}
	defer clear(body)
	var outbound outboundRequest
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.MaxTokens != 321 || string(outbound.Temperature) != "0.2" || string(outbound.TopP) != "0.8" {
		t.Fatalf("sampling projection=%+v", outbound)
	}
	if len(outbound.StopSequences) != 2 || len(outbound.Tools) != 1 || outbound.ToolChoice == nil || outbound.ToolChoice.Type != "tool" || !outbound.ToolChoice.DisableParallelToolUse {
		t.Fatalf("tool projection=%+v", outbound)
	}
	if len(outbound.Messages) != 3 || len(outbound.Messages[0].Content) != 3 || outbound.Messages[0].Content[2].Source == nil || outbound.Messages[0].Content[2].Source.Type != "base64" {
		t.Fatalf("message projection=%+v", outbound.Messages)
	}
	if outbound.Messages[1].Content[0].Type != "tool_use" || outbound.Messages[2].Content[0].Type != "tool_result" {
		t.Fatalf("tool history projection=%+v", outbound.Messages)
	}
}

func TestSamplingParametersUseExactDecimalRange(t *testing.T) {
	valid := []string{"0", "-0", "1", "1.0000", "1e0", "10e-1", "1e-10000", "0.999999999999999999999999", "0E+999999"}
	for _, raw := range valid {
		t.Run("valid_"+strings.NewReplacer("-", "neg", ".", "dot", "+", "plus").Replace(raw), func(t *testing.T) {
			got, err := parseUnitNumber([]byte(raw))
			defer clear(got)
			if err != nil || string(got) != raw {
				t.Fatalf("raw=%q got=%q err=%v", raw, got, err)
			}
		})
	}
	invalid := []string{"1.0000000000000000001", "-0.0000000000000000001", "-1e-10000", "1e1", "10.0001e-1", "1e999999", `"0.5"`}
	for _, raw := range invalid {
		t.Run("invalid_"+strings.NewReplacer("-", "neg", ".", "dot", "+", "plus").Replace(raw), func(t *testing.T) {
			got, err := parseUnitNumber([]byte(raw))
			clear(got)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("raw=%q err=%v", raw, err)
			}
		})
	}
}

func TestToolHistoryTracksIDsAndAllowsSystemWhileResultsPending(t *testing.T) {
	accepted := mustChatRequest(t, `{"model":"p/m","messages":[
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"system","content":"mid-system"},
		{"role":"developer","content":"mid-developer"},
		{"role":"tool","tool_call_id":"call_1","content":"done"},
		{"role":"user","content":"continue"}
	]}`)
	body, err := compileRequest(accepted, "claude", "nbu", BuiltInDefaultMaxTokens)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	var outbound outboundRequest
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound.System != "mid-system\nmid-developer" {
		t.Fatalf("system=%q", outbound.System)
	}

	reused := mustChatRequest(t, `{"model":"p/m","messages":[
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"first"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"g","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"second"}
	]}`)
	rejected, err := compileRequest(reused, "claude", "nbu", BuiltInDefaultMaxTokens)
	clear(rejected)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("reused tool id err=%v", err)
	}
}

func TestJSONSurrogateLexicalValidationOnRequest(t *testing.T) {
	for _, text := range []string{`\ud83d`, `\ude00`, `\ud83dX`, `\ud83d\u0041`} {
		request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"`+text+`"}]}`)
		body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
		clear(body)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unpaired surrogate %q err=%v", text, err)
		}
	}
	for _, bodyText := range []string{`\ud83d\ude00`, "�", `\ufffd`} {
		request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"`+bodyText+`"}]}`)
		body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
		clear(body)
		if err != nil {
			t.Fatalf("valid text %q err=%v", bodyText, err)
		}
	}
}

func TestToolChoiceAutoWithoutToolsIsOmitted(t *testing.T) {
	for _, fields := range []string{
		`,"tool_choice":"auto"`,
		`,"tool_choice":"auto","parallel_tool_calls":false`,
		`,"parallel_tool_calls":true`,
		`,"tools":[{"type":"function","function":{"name":"f","parameters":{}}}],"tool_choice":"none","parallel_tool_calls":false`,
	} {
		request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]`+fields+`}`)
		body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
		if err != nil {
			t.Fatalf("fields=%s compileRequest: %v", fields, err)
		}
		var outbound outboundRequest
		if err := json.Unmarshal(body, &outbound); err != nil {
			clear(body)
			t.Fatal(err)
		}
		clear(body)
		if outbound.ToolChoice != nil || len(outbound.Tools) != 0 {
			t.Fatalf("fields=%s no-op tool controls were emitted: choice=%+v tools=%+v", fields, outbound.ToolChoice, outbound.Tools)
		}
	}
}

func TestAnthropicRequestRejectionMatrix(t *testing.T) {
	longHTTPS := "https://images.example/" + strings.Repeat("x", maxImageURLBytes)
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown top level", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"n":1}`},
		{name: "store is OpenAI only", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"store":false}`},
		{name: "unknown role", body: `{"model":"p/m","messages":[{"role":"observer","content":"hi"}]}`},
		{name: "message name", body: `{"model":"p/m","messages":[{"role":"user","name":"x","content":"hi"}]}`},
		{name: "orphan tool result", body: `{"model":"p/m","messages":[{"role":"tool","tool_call_id":"call_1","content":"x"}]}`},
		{name: "missing tool result", body: `{"model":"p/m","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}],"content":null}]}`},
		{name: "tool arguments array", body: `{"model":"p/m","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"[]"}}],"content":null},{"role":"tool","tool_call_id":"call_1","content":"x"}]}`},
		{name: "http image", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://images.example/a.png"}}]}]}`},
		{name: "https image over limit", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + longHTTPS + `"}}]}]}`},
		{name: "image detail", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://images.example/a.png","detail":"low"}}]}]}`},
		{name: "bad data image", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%%%"}}]}]}`},
		{name: "unknown content block", body: `{"model":"p/m","messages":[{"role":"user","content":[{"type":"audio","data":"x"}]}]}`},
		{name: "named choice missing tool", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"tools":[],"tool_choice":{"type":"function","function":{"name":"missing"}}}`},
		{name: "unknown stream option", body: `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true,"x":1}}`},
		{name: "duplicate nested key", body: `{"model":"p/m","messages":[{"role":"user","role":"assistant","content":"hi"}]}`},
		{name: "new user before tool result", body: `{"model":"p/m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"user","content":"skip result"}]}`},
		{name: "new assistant before tool result", body: `{"model":"p/m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"assistant","content":"skip result"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustChatRequest(t, test.body)
			if SupportsRequest(request) {
				t.Fatal("request unexpectedly supported")
			}
			body, err := compileRequest(request, "claude", "nbu", BuiltInDefaultMaxTokens)
			clear(body)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err=%v want invalid request", err)
			}
		})
	}
}
