package anthropic

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTranslateNonStreamTextAndFourBucketUsageGolden(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-test",
		"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],
		"stop_reason":"end_turn","stop_sequence":null,
		"usage":{"input_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":5,"output_tokens":7}
	}`)
	translated, usage, semantic, err := translateNonStream(body, "public/model", time.Unix(123, 0))
	if err != nil {
		t.Fatalf("translateNonStream: %v", err)
	}
	defer clear(translated)
	want := `{"id":"msg_1","object":"chat.completion","created":123,"model":"public/model","choices":[{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":3}}}`
	if string(translated) != want {
		t.Fatalf("translated=%s\nwant=%s", translated, want)
	}
	if !usage.Present || usage.UncachedInputTokens != 2 || usage.CacheWriteInputTokens != 3 || usage.CacheReadInputTokens != 5 || usage.OutputTokens != 7 {
		t.Fatalf("usage=%+v", usage)
	}
	if strings.Join(semantic, "|") != "msg_1|hello |world" {
		t.Fatalf("semantic=%q", semantic)
	}
}

func TestTranslateNonStreamToolUseGolden(t *testing.T) {
	body := []byte(`{
		"id":"msg_tool","type":"message","role":"assistant","model":"claude-test",
		"content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x","limit":2}}],
		"stop_reason":"tool_use","stop_sequence":null,
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)
	translated, usage, semantic, err := translateNonStream(body, "public/model", time.Unix(5, 0))
	if err != nil {
		t.Fatalf("translateNonStream: %v", err)
	}
	defer clear(translated)
	want := `{"id":"msg_tool","object":"chat.completion","created":5,"model":"public/model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\",\"limit\":2}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}}`
	if string(translated) != want {
		t.Fatalf("translated=%s\nwant=%s", translated, want)
	}
	if !usage.Present || strings.Join(semantic, "|") != `msg_tool|call_1|lookup|{"q":"x","limit":2}` {
		t.Fatalf("usage=%+v semantic=%q", usage, semantic)
	}
}

func TestTranslateNonStreamPreservesExplicitEmptyTextBesideToolUse(t *testing.T) {
	body := []byte(`{"id":"msg_tool","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":""},{"type":"tool_use","id":"call_1","name":"lookup","input":{}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
	translated, _, _, err := translateNonStream(body, "public/model", time.Unix(5, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(translated)
	if !strings.Contains(string(translated), `"content":""`) || strings.Contains(string(translated), `"content":null`) {
		t.Fatalf("translated=%s", translated)
	}
}

func TestStopReasonMapping(t *testing.T) {
	tests := map[string]string{
		"end_turn":                      "stop",
		"stop_sequence":                 "stop",
		"max_tokens":                    "length",
		"model_context_window_exceeded": "length",
		"tool_use":                      "tool_calls",
		"refusal":                       "content_filter",
	}
	for upstream, want := range tests {
		t.Run(upstream, func(t *testing.T) {
			got, err := parseStopReason([]byte(`"` + upstream + `"`))
			if err != nil || got != want {
				t.Fatalf("got=%q err=%v want=%q", got, err, want)
			}
		})
	}
	for _, unsupported := range []string{`null`, `"pause_turn"`, `"future"`, `1`} {
		if _, err := parseStopReason([]byte(unsupported)); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("unsupported %s err=%v", unsupported, err)
		}
	}
}

func TestUsageMissingFieldsAndInvalidValues(t *testing.T) {
	valid, err := parseAnthropicUsage([]byte(`{"input_tokens":4,"output_tokens":0}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if !valid.Present || valid.UncachedInputTokens != 4 || valid.CacheWriteInputTokens != 0 || valid.CacheReadInputTokens != 0 || valid.OutputTokens != 0 {
		t.Fatalf("optional cache fields did not use protocol zero: %+v", valid)
	}
	for _, raw := range []string{`{}`, `{"input_tokens":4}`, `{"output_tokens":0}`} {
		unknown, err := parseAnthropicUsage([]byte(raw), true)
		if err != nil || unknown.Present {
			t.Fatalf("missing required usage %s was present: %+v err=%v", raw, unknown, err)
		}
	}
	for _, raw := range []string{
		`null`,
		`{"input_tokens":-1}`,
		`{"input_tokens":1.5}`,
		`{"input_tokens":"1"}`,
		`{"input_tokens":null}`,
		`{"input_tokens":` + strconv.FormatInt(math.MaxInt64, 10) + `,"output_tokens":1}`,
	} {
		got, err := parseAnthropicUsage([]byte(raw), true)
		if err != nil || got.Present {
			t.Fatalf("invalid usage %s was present: %+v", raw, got)
		}
	}
	if _, err := parseAnthropicUsage([]byte(`{"input_tokens":1,"unknown":2}`), true); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unknown usage field err=%v", err)
	}
}

func TestTranslateNonStreamRejectsUnrepresentableResponses(t *testing.T) {
	base := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":CONTENT,"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`
	tests := []string{
		`[{"type":"thinking","thinking":"secret"}]`,
		`[{"type":"text","text":"ok","citations":[]}]`,
		`[{"type":"server_tool_use","id":"x","name":"web_search","input":{}}]`,
		`[{"type":"tool_use","id":"call_1","name":"f","input":[]}]`,
		`[{"type":"tool_use","id":"call_1","name":"f","input":{}},{"type":"tool_use","id":"call_1","name":"g","input":{}}]`,
	}
	for _, content := range tests {
		body := []byte(strings.Replace(base, "CONTENT", content, 1))
		translated, _, _, err := translateNonStream(body, "public", time.Unix(1, 0))
		clear(translated)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("content=%s err=%v", content, err)
		}
	}
}

func TestTranslateNonStreamKeepsSuccessWhenUsageUnknown(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":-1}}`)
	translated, usage, _, err := translateNonStream(body, "public", time.Unix(1, 0))
	if err != nil {
		t.Fatalf("translateNonStream: %v", err)
	}
	defer clear(translated)
	if usage.Present || strings.Contains(string(translated), `"usage"`) {
		t.Fatalf("unknown usage leaked as present: usage=%+v body=%s", usage, translated)
	}
}

func TestTranslateNonStreamCurrentMessageMetadata(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude",
		"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,
		"stop_details":null,"container":null,
		"usage":{"input_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":5,"output_tokens":7,
			"cache_creation":{"ephemeral_1h_input_tokens":1,"ephemeral_5m_input_tokens":2},
			"inference_geo":"us","output_tokens_details":{"thinking_tokens":2},
			"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0},"service_tier":"priority"}
	}`)
	translated, usage, _, err := translateNonStream(body, "public", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(translated)
	if !usage.Present || usage.UncachedInputTokens != 2 || usage.CacheWriteInputTokens != 3 || usage.CacheReadInputTokens != 5 || usage.OutputTokens != 7 {
		t.Fatalf("usage=%+v", usage)
	}
	for _, ignored := range []string{"cache_creation", "inference_geo", "output_tokens_details", "server_tool_use", "service_tier"} {
		if strings.Contains(string(translated), ignored) {
			t.Fatalf("validated upstream metadata leaked to caller: %s", translated)
		}
	}
}

func TestTranslateNonStreamRefusalDetailsMapWithoutExplanation(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":""}],"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"general_harms","explanation":"private explanation"},"container":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
	translated, _, _, err := translateNonStream(body, "public", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(translated)
	if !strings.Contains(string(translated), `"finish_reason":"content_filter"`) || strings.Contains(string(translated), "private explanation") || strings.Contains(string(translated), "general_harms") {
		t.Fatalf("translated=%s", translated)
	}
}

func TestTranslateNonStreamRejectsUnsupportedContainer(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"container":{"id":"container_1","expires_at":"2026-08-24T00:00:00Z","skills":[{"skill_id":"xlsx","type":"anthropic","version":"latest"}]},"usage":{}}`)
	translated, _, _, err := translateNonStream(body, "public", time.Unix(1, 0))
	clear(translated)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err=%v", err)
	}
}

func TestContainerCurrentShapeStrictValidation(t *testing.T) {
	present, err := validateContainer([]byte(`{"id":"container_1","expires_at":"2026-08-24T00:00:00Z","skills":[{"skill_id":"xlsx","type":"anthropic","version":"latest"},{"skill_id":"skill_1","type":"custom","version":"skill_version_1"}]}`))
	if err != nil || !present {
		t.Fatalf("present=%v err=%v", present, err)
	}
	for _, raw := range []string{
		`{"id":"container_1","expires_at":"2026-08-24T00:00:00Z","skills":[{"skill_id":"xlsx","type":"future","version":"latest"}]}`,
		`{"id":"container_1","expires_at":"2026-08-24T00:00:00Z","skills":[{"skill_id":"xlsx","type":"anthropic","version":"latest","unknown":1}]}`,
	} {
		if _, err := validateContainer([]byte(raw)); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
}

func TestUsageNullOrMissingRequiredCoreIsUnknown(t *testing.T) {
	for _, raw := range []string{
		`{"input_tokens":null,"output_tokens":1,"output_tokens_details":{"thinking_tokens":1}}`,
		`{"input_tokens":1,"output_tokens":null,"output_tokens_details":{"thinking_tokens":99}}`,
		`{"input_tokens":1,"cache_creation_input_tokens":null,"output_tokens":2}`,
	} {
		usage, err := parseAnthropicUsage([]byte(raw), true)
		if err != nil || usage.Present {
			t.Fatalf("raw=%s usage=%+v err=%v", raw, usage, err)
		}
	}
	for _, raw := range []string{`{}`, `{"input_tokens":1}`, `{"output_tokens":1}`} {
		missing, err := parseAnthropicUsage([]byte(raw), true)
		if err != nil || missing.Present {
			t.Fatalf("raw=%s missing=%+v err=%v", raw, missing, err)
		}
	}
}

func TestNonStreamStopSequenceMustMatchReason(t *testing.T) {
	base := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":REASON,"stop_sequence":SEQUENCE,"usage":{"input_tokens":1,"output_tokens":1}}`
	tests := []struct {
		name     string
		reason   string
		sequence string
		valid    bool
	}{
		{name: "stop sequence", reason: `"stop_sequence"`, sequence: `"END"`, valid: true},
		{name: "stop sequence null", reason: `"stop_sequence"`, sequence: `null`},
		{name: "stop sequence missing", reason: `"stop_sequence"`, sequence: `MISSING`},
		{name: "stop sequence empty", reason: `"stop_sequence"`, sequence: `""`},
		{name: "end turn null", reason: `"end_turn"`, sequence: `null`, valid: true},
		{name: "end turn missing", reason: `"end_turn"`, sequence: `MISSING`, valid: true},
		{name: "end turn has sequence", reason: `"end_turn"`, sequence: `"END"`},
		{name: "refusal has sequence", reason: `"refusal"`, sequence: `"END"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := strings.ReplaceAll(base, "REASON", test.reason)
			if test.sequence == "MISSING" {
				body = strings.Replace(body, `,"stop_sequence":SEQUENCE`, "", 1)
			} else {
				body = strings.Replace(body, "SEQUENCE", test.sequence, 1)
			}
			translated, _, _, err := translateNonStream([]byte(body), "public", time.Unix(1, 0))
			clear(translated)
			if (err == nil) != test.valid {
				t.Fatalf("body=%s err=%v valid=%v", body, err, test.valid)
			}
		})
	}
}

func TestNonStreamRejectsUnpairedSurrogateEscapes(t *testing.T) {
	responseFor := func(text string) []byte {
		return []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"` + text + `"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
	}
	for _, text := range []string{`\ud83d`, `\ude00`, `\ud83dX`, `\ud83d\u0041`} {
		translated, _, _, err := translateNonStream(responseFor(text), "public", time.Unix(1, 0))
		clear(translated)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("text=%q err=%v", text, err)
		}
	}
	for _, text := range []string{`\ud83d\ude00`, "�", `\ufffd`} {
		translated, _, _, err := translateNonStream(responseFor(text), "public", time.Unix(1, 0))
		clear(translated)
		if err != nil {
			t.Fatalf("valid text=%q err=%v", text, err)
		}
	}
}

func TestUsageCurrentMetadataStrictValidation(t *testing.T) {
	invalid := []string{
		`{"cache_creation":{"ephemeral_1h_input_tokens":1}}`,
		`{"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0,"unknown":1}}`,
		`{"output_tokens":1,"output_tokens_details":{"thinking_tokens":2}}`,
		`{"inference_geo":1}`,
		`{"service_tier":"future"}`,
	}
	for _, raw := range invalid {
		if _, err := parseAnthropicUsage([]byte(raw), true); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
}
