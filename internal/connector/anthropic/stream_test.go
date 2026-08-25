package anthropic

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func namedEvent(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func messageStart(usage string) string {
	return namedEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":`+usage+`}}`)
}

func messageDelta(reason, usage string) string {
	return namedEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+reason+`","stop_sequence":null},"usage":`+usage+`}`)
}

func messageDeltaWithSequence(reason, sequence, usage string) string {
	field := `,"stop_sequence":` + sequence
	if sequence == "MISSING" {
		field = ""
	}
	return namedEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+reason+`"`+field+`},"usage":`+usage+`}`)
}

func messageStop() string {
	return namedEvent("message_stop", `{"type":"message_stop"}`)
}

func inputJSONDelta(partial string) string {
	payload, err := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": partial},
	})
	if err != nil {
		panic(err)
	}
	return namedEvent("content_block_delta", string(payload))
}

func runStreamFixture(t *testing.T, ctx context.Context, input string, credentials ...[]byte) (connectorcontract.AttemptResult, *httptest.ResponseRecorder) {
	t.Helper()
	adapter := &Adapter{
		maxStreamBytes:   DefaultMaxStreamBytes,
		maxSSELineBytes:  DefaultMaxSSELineBytes,
		maxSSEEventBytes: DefaultMaxSSEEventBytes,
	}
	response := fakeResponse(200, "text/event-stream", input)
	recorder := httptest.NewRecorder()
	guard := newSensitiveGuard(credentials...)
	semantic := guard.clone()
	result := adapter.stream(ctx, recorder, response, "public/model", time.Unix(123, 0), guard, semantic)
	guard.Clear()
	semantic.Clear()
	_ = response.Body.Close()
	return result, recorder
}

func textBlockStream(count int) string {
	var stream strings.Builder
	stream.Grow(count * 180)
	stream.WriteString(messageStart(`{"input_tokens":1,"output_tokens":0}`))
	for index := 0; index < count; index++ {
		value := strconv.Itoa(index)
		stream.WriteString(namedEvent("content_block_start", `{"type":"content_block_start","index":`+value+`,"content_block":{"type":"text","text":""}}`))
		stream.WriteString(namedEvent("content_block_stop", `{"type":"content_block_stop","index":`+value+`}`))
	}
	stream.WriteString(messageDelta("end_turn", `{"output_tokens":1}`))
	stream.WriteString(messageStop())
	return stream.String()
}

func TestStreamContentBlockCountMatchesNonStreamLimit(t *testing.T) {
	exact, recorder := runStreamFixture(t, context.Background(), textBlockStream(maxMessages))
	if !exact.Success || !strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("exact limit result=%+v body_len=%d", exact, recorder.Body.Len())
	}

	over, recorder := runStreamFixture(t, context.Background(), textBlockStream(maxMessages+1))
	if over.Success || over.Failure != connectorcontract.FailureUpstream || !over.Committed || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("limit+1 result=%+v body_len=%d", over, recorder.Body.Len())
	}
}

func TestStreamTextAndFourBucketUsageGolden(t *testing.T) {
	input := messageStart(`{"input_tokens":2,"output_tokens":0}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`) +
		namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		messageDelta("end_turn", `{"cache_creation_input_tokens":3,"cache_read_input_tokens":5,"output_tokens":7}`) +
		messageStop()
	result, recorder := runStreamFixture(t, context.Background(), input, []byte("credential-not-present"))
	if !result.Success || result.Failure != connectorcontract.FailureNone || !result.Committed {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	if !result.Usage.Present || result.Usage.UncachedInputTokens != 2 || result.Usage.CacheWriteInputTokens != 3 || result.Usage.CacheReadInputTokens != 5 || result.Usage.OutputTokens != 7 {
		t.Fatalf("usage=%+v", result.Usage)
	}
	want := "" +
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":123,"model":"public/model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":123,"model":"public/model","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":123,"model":"public/model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":123,"model":"public/model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":3}}}` + "\n\n" +
		"data: [DONE]\n\n"
	if recorder.Body.String() != want {
		t.Fatalf("body=%q\nwant=%q", recorder.Body.String(), want)
	}
}

func TestStreamToolUseGolden(t *testing.T) {
	input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`) +
		namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`) +
		namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		messageDelta("tool_use", `{"output_tokens":2}`) +
		messageStop()
	result, recorder := runStreamFixture(t, context.Background(), input)
	if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 2 {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Count(body, `"id":"call_1"`) != 1 || strings.Count(body, `"name":"lookup"`) != 1 || !strings.Contains(body, `"arguments":"{\"q\":\"x\"}"`) || !strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("tool stream=%s", body)
	}
}

func TestStreamUsageCumulativeSnapshots(t *testing.T) {
	tests := []struct {
		name        string
		deltas      string
		wantPresent bool
		wantOutput  int64
	}{
		{name: "latest replaces", deltas: messageDelta("end_turn", `{"output_tokens":3}`) + messageDelta("end_turn", `{"output_tokens":5}`), wantPresent: true, wantOutput: 5},
		{name: "regression poisons", deltas: messageDelta("end_turn", `{"output_tokens":3}`) + messageDelta("end_turn", `{"output_tokens":2}`)},
		{name: "checked sum overflow poisons", deltas: messageDelta("end_turn", `{"output_tokens":1}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := `{"input_tokens":1,"output_tokens":0}`
			if test.name == "checked sum overflow poisons" {
				initial = `{"input_tokens":9223372036854775807,"output_tokens":0}`
			}
			input := messageStart(initial) + test.deltas + messageStop()
			result, recorder := runStreamFixture(t, context.Background(), input)
			if !result.Success {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
			if result.Usage.Present != test.wantPresent || result.Usage.OutputTokens != test.wantOutput {
				t.Fatalf("usage=%+v wantPresent=%v wantOutput=%d", result.Usage, test.wantPresent, test.wantOutput)
			}
			if !test.wantPresent && strings.Contains(recorder.Body.String(), `"usage"`) {
				t.Fatalf("unknown usage was emitted: %s", recorder.Body.String())
			}
		})
	}

	state := cumulativeUsage{}
	if err := state.merge([]byte(`{"input_tokens":1,"output_tokens":0}`), true, true); err != nil {
		t.Fatal(err)
	}
	if state.final().Present {
		t.Fatalf("message_start usage was treated as terminal: %+v", state.final())
	}
	if err := state.merge([]byte(`{"output_tokens":9223372036854775807}`), false, false); err != nil {
		t.Fatal(err)
	}
	if state.final().Present {
		t.Fatalf("direct overflow state was present: %+v", state.final())
	}
}

func TestStreamMissingRequiredUsageIsUnknown(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		delta   string
	}{
		{name: "empty initial", initial: `{}`, delta: `{"output_tokens":1}`},
		{name: "initial input only", initial: `{"input_tokens":1}`, delta: `{"output_tokens":1}`},
		{name: "initial output only", initial: `{"output_tokens":0}`, delta: `{"output_tokens":1}`},
		{name: "empty delta", initial: `{"input_tokens":1,"output_tokens":0}`, delta: `{}`},
		{name: "delta input only", initial: `{"input_tokens":1,"output_tokens":0}`, delta: `{"input_tokens":2}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := messageStart(test.initial) + messageDelta("end_turn", test.delta) + messageStop()
			result, recorder := runStreamFixture(t, context.Background(), input)
			if !result.Success || result.Usage.Present || strings.Contains(recorder.Body.String(), `"usage"`) {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
		})
	}
}

func TestStreamStopSequenceMustMatchReason(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		sequence string
		valid    bool
	}{
		{name: "stop sequence", reason: "stop_sequence", sequence: `"END"`, valid: true},
		{name: "stop sequence null", reason: "stop_sequence", sequence: `null`},
		{name: "stop sequence missing", reason: "stop_sequence", sequence: "MISSING"},
		{name: "stop sequence empty", reason: "stop_sequence", sequence: `""`},
		{name: "end turn null", reason: "end_turn", sequence: `null`, valid: true},
		{name: "end turn missing", reason: "end_turn", sequence: "MISSING", valid: true},
		{name: "end turn has sequence", reason: "end_turn", sequence: `"END"`},
		{name: "refusal has sequence", reason: "refusal", sequence: `"END"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
				messageDeltaWithSequence(test.reason, test.sequence, `{"output_tokens":1}`) + messageStop()
			result, recorder := runStreamFixture(t, context.Background(), input)
			if result.Success != test.valid {
				t.Fatalf("result=%+v body=%s valid=%v", result, recorder.Body.String(), test.valid)
			}
		})
	}
}

func TestStreamSurrogateEscapesAreLexicallyStrict(t *testing.T) {
	for _, text := range []string{`\ud83d`, `\ude00`, `\ud83dX`, `\ud83d\u0041`} {
		input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
			namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
			namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+text+`"}}`)
		result, recorder := runStreamFixture(t, context.Background(), input)
		if result.Success || result.Failure != connectorcontract.FailureUpstream || !result.Committed || strings.Contains(recorder.Body.String(), "�") {
			t.Fatalf("text=%q result=%+v body=%s", text, result, recorder.Body.String())
		}
	}
	for _, text := range []string{`\ud83d\ude00`, "�", `\ufffd`} {
		input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
			namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
			namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+text+`"}}`) +
			namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
			messageDelta("end_turn", `{"output_tokens":1}`) + messageStop()
		result, recorder := runStreamFixture(t, context.Background(), input)
		if !result.Success {
			t.Fatalf("valid text=%q result=%+v body=%s", text, result, recorder.Body.String())
		}
	}

	input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`) +
		inputJSONDelta(`{"value":"\ud83d`) + inputJSONDelta(`"}`) +
		namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`)
	result, recorder := runStreamFixture(t, context.Background(), input)
	if result.Success || !result.Committed || strings.Contains(recorder.Body.String(), "�") {
		t.Fatalf("cross-event unpaired surrogate result=%+v body=%s", result, recorder.Body.String())
	}
}

func TestStreamToolArgumentsAggregateBeyondSingleEventLimit(t *testing.T) {
	const partBytes = 200 << 10
	parts := make([]string, 6)
	for index := range parts {
		parts[index] = strings.Repeat("x", partBytes)
	}
	parts[0] = `{"payload":"` + parts[0]
	parts[len(parts)-1] += `"}`
	input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`)
	for _, part := range parts {
		event := inputJSONDelta(part)
		if len(event) > DefaultMaxSSELineBytes {
			t.Fatalf("fixture event line=%d exceeds hard line cap", len(event))
		}
		input += event
	}
	if 6*partBytes <= DefaultMaxSSEEventBytes || int64(len(input)) >= DefaultMaxStreamBytes {
		t.Fatalf("fixture aggregate=%d stream=%d", 6*partBytes, len(input))
	}
	input += namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		messageDelta("tool_use", `{"output_tokens":1}`) + messageStop()
	result, recorder := runStreamFixture(t, context.Background(), input)
	if !result.Success || !strings.Contains(recorder.Body.String(), `"finish_reason":"tool_calls"`) || !strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("result=%+v caller_len=%d", result, recorder.Body.Len())
	}
}

func TestStreamRejectsConfiguredAggregateStreamLimit(t *testing.T) {
	input := messageStart(`{"input_tokens":1,"output_tokens":0}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+strings.Repeat("x", 1024)+`"}}`) +
		namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + messageDelta("end_turn", `{"output_tokens":1}`) + messageStop()
	adapter := &Adapter{maxStreamBytes: int64(len(input) - 1), maxSSELineBytes: DefaultMaxSSELineBytes, maxSSEEventBytes: DefaultMaxSSEEventBytes}
	response := fakeResponse(200, "text/event-stream", input)
	recorder := httptest.NewRecorder()
	guard := newSensitiveGuard()
	semantic := guard.clone()
	result := adapter.stream(context.Background(), recorder, response, "public/model", time.Unix(123, 0), guard, semantic)
	guard.Clear()
	semantic.Clear()
	_ = response.Body.Close()
	if result.Success || result.Failure != connectorcontract.FailureUpstream || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("result=%+v body_len=%d", result, recorder.Body.Len())
	}
}

func TestStreamWaitsForLegalEOFAndRejectsSemanticEventsAfterStop(t *testing.T) {
	base := messageStart(`{}`) + messageDelta("end_turn", `{"output_tokens":0}`) + messageStop()
	tests := []struct {
		name  string
		extra string
	}{
		{name: "message delta", extra: messageDelta("end_turn", `{"output_tokens":1}`)},
		{name: "text delta", extra: namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"late"}}`)},
		{name: "second stop", extra: messageStop()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, recorder := runStreamFixture(t, context.Background(), base+test.extra)
			if result.Success || result.Failure != connectorcontract.FailureUpstream || !result.Committed {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "[DONE]") || strings.Contains(recorder.Body.String(), `"finish_reason":"stop"`) || !strings.Contains(recorder.Body.String(), `"code":"upstream"`) {
				t.Fatalf("invalid terminal output=%s", recorder.Body.String())
			}
		})
	}
}

func TestStreamOuterUnknownAndPingAreIgnored(t *testing.T) {
	input := namedEvent("future_before_start", `{"type":"future_before_start","value":1}`) +
		messageStart(`{}`) +
		namedEvent("ping", `{"type":"ping"}`) +
		messageDelta("end_turn", `{"output_tokens":0}`) +
		messageStop() +
		namedEvent("future_after_stop", `{"type":"future_after_stop","value":2}`) +
		namedEvent("ping", `{"type":"ping"}`)
	result, recorder := runStreamFixture(t, context.Background(), input)
	if !result.Success || !strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
}

func TestStreamProtocolFailureMatrix(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "upstream error before start", input: namedEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"private"}}`)},
		{name: "upstream error after start", input: messageStart(`{}`) + namedEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"private"}}`)},
		{name: "missing message stop", input: messageStart(`{}`) + messageDelta("end_turn", `{"output_tokens":0}`)},
		{name: "unknown content block", input: messageStart(`{}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"x"}}`)},
		{name: "event type mismatch", input: namedEvent("message_start", `{"type":"ping"}`)},
		{name: "truncated JSON", input: namedEvent("message_start", `{`)},
		{name: "bad tool JSON", input: messageStart(`{}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"f","input":{}}}`) + namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"["}}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`)},
		{name: "duplicate tool id", input: messageStart(`{}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"f","input":{}}}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"g","input":{}}}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, recorder := runStreamFixture(t, context.Background(), test.input)
			if result.Success || result.Failure != connectorcontract.FailureUpstream || strings.Contains(recorder.Body.String(), "[DONE]") {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
		})
	}
}

func TestStreamCancellationAndCredentialReflection(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ := runStreamFixture(t, canceled, "")
	if result.Failure != connectorcontract.FailureCanceled || result.Success {
		t.Fatalf("canceled result=%+v", result)
	}

	secret := []byte(`key"quoted`)
	reflected := messageStart(`{}`) +
		namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"key\"quoted"}}`)
	result, recorder := runStreamFixture(t, context.Background(), reflected, secret)
	if result.Success || result.Failure != connectorcontract.FailureUpstream || strings.Contains(recorder.Body.String(), `key\"quoted`) {
		t.Fatalf("reflection result=%+v body=%s", result, recorder.Body.String())
	}
}

func TestStreamBuffersToolArgumentsUntilDeepSecretScan(t *testing.T) {
	tests := []struct {
		name       string
		plaintext  string
		ciphertext string
		parts      []string
	}{
		{name: "unicode plaintext", plaintext: "sk-secret", ciphertext: "cipher-other", parts: []string{`{"token":"sk-\u00`, `73ecret"}`}},
		{name: "unicode ciphertext", plaintext: "sk-other", ciphertext: "cipher-secret", parts: []string{`{"token":"cipher-\u`, `0073ecret"}`}},
		{name: "surrogate plaintext", plaintext: "key-😀", ciphertext: "cipher-other", parts: []string{`{"token":"key-\ud83d`, `\ude00"}`}},
		{name: "escaped object key", plaintext: "sk-secret", ciphertext: "cipher-other", parts: []string{`{"sk-\u00`, `73ecret":"value"}`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := messageStart(`{}`) +
				namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`)
			for _, part := range test.parts {
				input += inputJSONDelta(part)
			}
			input += namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`)
			result, recorder := runStreamFixture(t, context.Background(), input, []byte(test.plaintext), []byte(test.ciphertext))
			body := recorder.Body.String()
			if result.Success || result.Failure != connectorcontract.FailureUpstream || !result.Committed || !strings.Contains(body, `"code":"upstream"`) {
				t.Fatalf("result=%+v body=%s", result, body)
			}
			if strings.Contains(body, test.plaintext) || strings.Contains(body, test.ciphertext) || strings.Count(body, `"arguments":"`) != 1 || !strings.Contains(body, `"arguments":""`) {
				t.Fatalf("tool arguments leaked before validation: %s", body)
			}
		})
	}
}

func TestStreamRejectsSecretMessageIDBeforeCallerCommit(t *testing.T) {
	input := namedEvent("message_start", `{"type":"message_start","message":{"id":"sk-\u0073ecret","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{}}}`)
	result, recorder := runStreamFixture(t, context.Background(), input, []byte("sk-secret"), []byte("cipher-other"))
	if result.Success || result.Failure != connectorcontract.FailureUpstream || result.Committed || recorder.Body.Len() != 0 || result.Diagnostic != "upstream stream was rejected" {
		t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
	}
}

func TestStreamCurrentMessageMetadataAndRefusalMapping(t *testing.T) {
	start := namedEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"container":null,"usage":{"input_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":5,"output_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":1,"ephemeral_5m_input_tokens":2},"inference_geo":"us","output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0},"service_tier":"standard"}}}`)
	delta := namedEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"general_harms","explanation":"private refusal explanation"},"container":null},"usage":{"output_tokens":7,"output_tokens_details":{"thinking_tokens":2},"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0}}}`)
	result, recorder := runStreamFixture(t, context.Background(), start+delta+messageStop())
	body := recorder.Body.String()
	if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 2 || result.Usage.CacheWriteInputTokens != 3 || result.Usage.CacheReadInputTokens != 5 || result.Usage.OutputTokens != 7 {
		t.Fatalf("result=%+v body=%s", result, body)
	}
	if !strings.Contains(body, `"finish_reason":"content_filter"`) || strings.Contains(body, "private refusal explanation") || strings.Contains(body, "general_harms") {
		t.Fatalf("refusal details leaked or mapping missing: %s", body)
	}
}

func TestStreamRejectsUnsupportedContainerAndUnknownCurrentUsageFields(t *testing.T) {
	validContainer := `{"id":"container_1","expires_at":"2026-08-24T00:00:00Z","skills":[]}`
	tests := []struct {
		name  string
		input string
	}{
		{name: "non-null container", input: namedEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"container":`+validContainer+`,"usage":{}}}`)},
		{name: "full-only usage field in delta", input: messageStart(`{}`) + namedEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1,"service_tier":"standard"}}`)},
		{name: "unknown usage field", input: messageStart(`{"unknown":1}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, recorder := runStreamFixture(t, context.Background(), test.input)
			if result.Success || result.Failure != connectorcontract.FailureUpstream || strings.Contains(recorder.Body.String(), "standard") {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
		})
	}
}
