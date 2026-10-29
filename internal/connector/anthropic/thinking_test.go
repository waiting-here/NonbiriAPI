package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestOptionalAnthropicMetadataOnJSONAndSSE(t *testing.T) {
	base := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`
	for _, replacement := range [][2]string{{`"usage":`, `"diagnostics":null,"usage":`}, {`"output_tokens":1`, `"output_tokens":1,"speed":"standard"`}, {`"text":"ok"`, `"text":"ok","citations":null`}, {`"text":"ok"`, `"text":"ok","citations":[]`}} {
		raw := strings.Replace(base, replacement[0], replacement[1], 1)
		if _, usage, _, err := translateNonStream([]byte(raw), "public", time.Unix(1, 0)); err != nil || !usage.Present {
			t.Fatal(err, usage)
		}
	}
	for _, citations := range []string{"null", "[]"} {
		start := strings.Replace(messageStart(`{"input_tokens":1,"output_tokens":0,"speed":"standard"}`), `"usage":`, `"diagnostics":null,"usage":`, 1)
		wire := start + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok","citations":`+citations+`}}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + messageDelta("end_turn", `{"output_tokens":1,"speed":"standard"}`) + messageStop()
		result, w := runStreamFixture(t, context.Background(), wire)
		if !result.Success || !strings.Contains(w.Body.String(), `"content":"ok"`) {
			t.Fatal(result, w.Body.String())
		}
	}
}

func TestAnthropicOptionalUsageFrameRespectsCallerOption(t *testing.T) {
	wire := messageStart(`{"input_tokens":1,"output_tokens":0}`) + messageDelta("end_turn", `{"output_tokens":2}`) + messageStop()
	for _, flag := range []string{"", `,"stream_options":{"include_usage":false}`, `,"stream_options":{"include_usage":true}`} {
		client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) { return fakeResponse(200, "text/event-stream", wire), nil }}
		a, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
		if err != nil {
			t.Fatal(err)
		}
		r := mustChatRequest(t, `{"model":"public","messages":[{"role":"user","content":"hi"}],"stream":true`+flag+`}`)
		w := httptest.NewRecorder()
		result := a.Attempt(context.Background(), w, NewTarget(client.baseURL, "claude", NewCredential([]byte("fixture-secret"), nil)), r, "nbu_fixture")
		if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 2 || strings.Contains(w.Body.String(), `"choices":[]`) != strings.Contains(flag, ":true") {
			t.Fatalf("flag=%s result=%+v body=%s", flag, result, w.Body.String())
		}
	}
}

const thinkingContent = `[{"type":"thinking","thinking":"consider ","signature":"opaque-one"},{"type":"redacted_thinking","data":"opaque-redacted"},{"type":"text","text":"answer"},{"type":"thinking","thinking":"again","signature":"opaque-two"},{"type":"tool_use","id":"call","name":"clock","input":{}}]`

func replayThinkingMessage(t *testing.T, message map[string]json.RawMessage) []outboundMessage {
	t.Helper()
	raw, _ := json.Marshal(message)
	input := `[` + string(raw) + `,{"role":"tool","tool_call_id":"call","content":"done"}]`
	messages, _, err := parseMessages([]byte(input))
	if err != nil {
		t.Fatal(err, input)
	}
	return messages
}

func TestAnthropicThinkingJSONAndStreamReplayKeepCompleteOrderedBlocks(t *testing.T) {
	raw := `{"id":"message","type":"message","role":"assistant","model":"claude","content":` + thinkingContent + `,"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":5}}`
	out, usage, _, err := translateNonStream([]byte(raw), "public", time.Unix(1, 0), strings.Repeat("A", 43))
	if err != nil || !usage.Present {
		t.Fatal(err, usage)
	}
	var result struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(out, &result)
	message := result.Choices[0].Message
	if string(message["content"]) != `"answer"` || string(message["reasoning_content"]) != `"consider again"` || !strings.Contains(string(message["extra_content"]), "continuation_id") {
		t.Fatal(string(out))
	}
	replayed := replayThinkingMessage(t, message)
	replayedRaw, _ := json.Marshal(replayed[0].Content)
	if !sameThinkingJSON(replayedRaw, []byte(thinkingContent)) {
		t.Fatal(string(replayedRaw))
	}
	var blocks []map[string]json.RawMessage
	_ = json.Unmarshal([]byte(thinkingContent), &blocks)
	wire := messageStart(`{"input_tokens":1,"output_tokens":0}`)
	for i, block := range blocks {
		kind := string(block["type"])
		initial := map[string]any{}
		for key, value := range block {
			initial[key] = value
		}
		if kind == `"thinking"` {
			initial["thinking"] = ""
			initial["signature"] = ""
		}
		start, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": i, "content_block": initial})
		wire += namedEvent("content_block_start", string(start))
		if kind == `"thinking"` {
			for _, field := range []string{"thinking", "signature"} {
				delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": field + "_delta", field: block[field]}})
				wire += namedEvent("content_block_delta", string(delta))
			}
		}
		stop, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": i})
		wire += namedEvent("content_block_stop", string(stop))
	}
	wire += messageDelta("tool_use", `{"output_tokens":5}`) + messageStop()
	streamResult, w := runStreamFixture(t, context.Background(), wire)
	if !streamResult.Success {
		t.Fatal(streamResult, w.Body.String())
	}
	var extra json.RawMessage
	var reasoning strings.Builder
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta map[string]json.RawMessage `json:"delta"`
			} `json:"choices"`
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk)
		for _, choice := range chunk.Choices {
			if raw := choice.Delta["extra_content"]; raw != nil {
				extra = raw
			}
			var value string
			_ = json.Unmarshal(choice.Delta["reasoning_content"], &value)
			reasoning.WriteString(value)
		}
	}
	if reasoning.String() != "consider again" || extra == nil {
		t.Fatal(w.Body.String())
	}
	message["extra_content"] = extra
	replayed = replayThinkingMessage(t, message)
	replayedRaw, _ = json.Marshal(replayed[0].Content)
	if !sameThinkingJSON(replayedRaw, []byte(thinkingContent)) {
		t.Fatal(string(replayedRaw))
	}
	message["reasoning_content"] = json.RawMessage(`"changed"`)
	encoded, _ := json.Marshal(message)
	if _, _, err := parseMessages([]byte(`[` + string(encoded) + `,{"role":"tool","tool_call_id":"call","content":"done"}]`)); err == nil {
		t.Fatal("changed signed reasoning accepted")
	}
	delete(message, "extra_content")
	encoded, _ = json.Marshal(message)
	if _, _, err := parseMessages([]byte(`[` + string(encoded) + `,{"role":"tool","tool_call_id":"call","content":"done"}]`)); err == nil {
		t.Fatal("missing tool thinking continuation accepted")
	}
}

func TestThinkingConfigurationUsesKnownModelAndModeConstraints(t *testing.T) {
	for _, tc := range []struct {
		model, thinking, choice string
		valid                   bool
	}{
		{"claude-sonnet-4-6", `{"type":"enabled","budget_tokens":1024}`, `{"type":"auto"}`, true},
		{"claude-sonnet-4-6", `{"type":"enabled","budget_tokens":1024}`, `{"type":"any"}`, false},
		{"claude-sonnet-4-6", `{"type":"enabled","budget_tokens":2048}`, `{"type":"auto"}`, false},
		{"claude-sonnet-4-6", `{"type":"adaptive"}`, `{"type":"any"}`, true},
		{"claude-sonnet-4-5", `{"type":"adaptive"}`, `{"type":"auto"}`, false},
		{"claude-opus-4-7", `{"type":"enabled","budget_tokens":1024}`, `{"type":"auto"}`, false},
	} {
		raw := `{"model":"` + tc.model + `","max_tokens":2048,"thinking":` + tc.thinking + `,"tool_choice":` + tc.choice + `}`
		if err := validateThinkingConfiguration([]byte(raw), nil); (err == nil) != tc.valid {
			t.Fatal(raw, err)
		}
	}
	request := mustChatRequest(t, `{"model":"public","messages":[{"role":"user","content":"hi"}],"max_tokens":2048}`)
	err := ValidateTarget(contract.NewTarget(contract.TypeAnthropicCompatible, "https://example.com", "claude-sonnet-4-6"), request, contract.AttemptPolicy{NativeExtensions: map[string]json.RawMessage{"/thinking": json.RawMessage(`{"type":"enabled","budget_tokens":10}`)}})
	if err == nil || !strings.Contains(err.Error(), "thinking.budget_tokens") {
		t.Fatal(err)
	}
}

func TestOmittedAndRedactedThinkingRemainOpaque(t *testing.T) {
	content := `[{"type":"thinking","signature":"opaque"},{"type":"redacted_thinking","data":"hidden"},{"type":"tool_use","id":"call","name":"clock","input":{}}]`
	raw := `{"id":"message","type":"message","role":"assistant","model":"claude","content":` + content + `,"stop_reason":"tool_use"}`
	out, _, _, err := translateNonStream([]byte(raw), "public", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "reasoning_content") {
		t.Fatal(string(out))
	}
	var result struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(out, &result)
	messages := replayThinkingMessage(t, result.Choices[0].Message)
	got, _ := json.Marshal(messages[0].Content)
	if !sameThinkingJSON(got, []byte(content)) {
		t.Fatal(string(got))
	}
	for _, replacement := range []string{`{"type":"thinking"}`, `{"type":"thinking","thinking":null,"signature":"opaque"}`, `{"type":"thinking","thinking":"x","signature":""}`, `{"type":"thinking","signature":"` + strings.Repeat("a", (64<<10)+1) + `"}`} {
		bad := strings.Replace(raw, `{"type":"thinking","signature":"opaque"}`, replacement, 1)
		if _, _, _, err := translateNonStream([]byte(bad), "public", time.Unix(1, 0)); err == nil {
			t.Fatal("invalid thinking accepted")
		}
	}
	wire := messageStart(`{"input_tokens":1,"output_tokens":0}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`) + namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque"}}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + messageDelta("end_turn", `{"output_tokens":1}`) + messageStop()
	streamResult, w := runStreamFixture(t, context.Background(), wire)
	if !streamResult.Success || strings.Contains(w.Body.String(), "reasoning_content") || !strings.Contains(w.Body.String(), `"signature":"opaque"`) {
		t.Fatal(streamResult, w.Body.String())
	}
	if _, _, err := parseMessages([]byte(`[{"role":"assistant","content":"answer","reasoning_content":"optional old thought"},{"role":"user","content":"next"}]`)); err != nil {
		t.Fatal("ordinary history without thinking must remain valid", err)
	}
}
