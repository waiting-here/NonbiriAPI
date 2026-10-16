package responses

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func request(t *testing.T, body string) *openai.ChatRequest {
	t.Helper()
	r, err := openai.DecodeChatRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}
func equalJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
func TestResponsesRequestStatelessToolLoop(t *testing.T) {
	r := request(t, `{"model":"public","messages":[{"role":"developer","content":"rules"},{"role":"user","content":"lookup"},{"role":"assistant","content":null,"reasoning_content":"checking","extra_content":{"openai":{"reasoning":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"checking"}],"encrypted_content":"opaque"}]}},"tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{\"key\":1}"}},{"id":"b","type":"function","function":{"name":"lookup","arguments":"{\"key\":2}"}}]},{"role":"tool","tool_call_id":"b","content":"two"},{"role":"tool","tool_call_id":"a","content":"one"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"max_completion_tokens":48,"reasoning_effort":"high","parallel_tool_calls":false}`)
	body, err := Compile(r, "private", contract.AttemptPolicy{SafetyIdentifier: "consumer"})
	if err != nil {
		t.Fatal(err)
	}
	equalJSON(t, body, `{"model":"private","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"rules"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"lookup"}]},{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"checking"}],"encrypted_content":"opaque"},{"type":"function_call","call_id":"a","name":"lookup","arguments":"{\"key\":1}"},{"type":"function_call","call_id":"b","name":"lookup","arguments":"{\"key\":2}"},{"type":"function_call_output","call_id":"b","output":"two"},{"type":"function_call_output","call_id":"a","output":"one"}],"store":false,"stream":false,"include":["reasoning.encrypted_content"],"safety_identifier":"consumer","max_output_tokens":48,"reasoning":{"effort":"high"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}],"parallel_tool_calls":false}`)
}
func TestResponsesRejectPersistentOrLossyRequests(t *testing.T) {
	base := `{"model":"m","messages":[{"role":"user","content":"x"}]`
	for _, field := range []string{`,"store":true`, `,"previous_response_id":"x"`, `,"conversation":"x"`, `,"background":true`, `,"stop":"stop"`, `,"n":2`, `,"frequency_penalty":0`, `,"tools":[{"type":"web_search"}]`} {
		if _, err := Compile(request(t, base+field+"}"), "m", contract.AttemptPolicy{}); err == nil {
			t.Fatal(field)
		}
	}
	for _, extension := range []map[string]json.RawMessage{{"/store": json.RawMessage(`true`)}, {"/input": json.RawMessage(`[]`)}, {"/max_output_tokens": json.RawMessage(`1000`)}, {"/reasoning/summary": json.RawMessage(`"unknown"`)}} {
		if _, err := Compile(request(t, base+"}"), "m", contract.AttemptPolicy{NativeExtensions: extension}); err == nil {
			t.Fatal(extension)
		}
	}
	for _, message := range []string{`{"role":"tool","tool_call_id":"orphan","content":"x"}`, `{"role":"assistant","content":"x","reasoning_content":"lost"}`, `{"role":"assistant","content":"x","extra_content":{"google":{"thought_signature":"YWJj"}}}`} {
		if _, err := Compile(request(t, `{"model":"m","messages":[`+message+`]}`), "m", contract.AttemptPolicy{}); err == nil {
			t.Fatal(message)
		}
	}
}

const usageJSON = `{"input_tokens":30,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":2},"output_tokens":9,"output_tokens_details":{"reasoning_tokens":6},"total_tokens":39}`
const reasoningJSON = `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque","status":"completed"}`
const callAJSON = `{"type":"function_call","id":"fc_1","call_id":"a","name":"lookup","arguments":"{\"key\":1}","status":"completed"}`
const callBJSON = `{"type":"function_call","id":"fc_2","call_id":"b","name":"lookup","arguments":"{\"key\":2}","status":"completed"}`

func toolEvents() []map[string]any {
	decode := func(s string) any { var value any; _ = json.Unmarshal([]byte(s), &value); return value }
	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_private", "status": "in_progress"}}, {"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "rs_1", "type": "reasoning", "summary": []any{}}}, {"type": "response.output_item.done", "output_index": 0, "item": decode(reasoningJSON)}}
	for index, item := range []struct{ id, call, args, final string }{{"fc_1", "a", `{"key":1}`, callAJSON}, {"fc_2", "b", `{"key":2}`, callBJSON}} {
		events = append(events, map[string]any{"type": "response.output_item.added", "output_index": index + 1, "item": map[string]any{"id": item.id, "type": "function_call", "call_id": item.call, "name": "lookup", "arguments": ""}}, map[string]any{"type": "response.function_call_arguments.delta", "output_index": index + 1, "item_id": item.id, "delta": item.args[:4]}, map[string]any{"type": "response.function_call_arguments.delta", "output_index": index + 1, "item_id": item.id, "delta": item.args[4:]}, map[string]any{"type": "response.function_call_arguments.done", "output_index": index + 1, "item_id": item.id, "arguments": item.args}, map[string]any{"type": "response.output_item.done", "output_index": index + 1, "item": decode(item.final)})
	}
	events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_private", "status": "completed", "output": []any{decode(reasoningJSON), decode(callAJSON), decode(callBJSON)}, "usage": decode(usageJSON)}})
	for i, event := range events {
		event["sequence_number"] = i
	}
	return events
}
func feed(t *testing.T, events []map[string]any) (string, contract.Usage, error) {
	t.Helper()
	d := NewDecoder("public", time.Unix(123, 0), true)
	defer d.Clear()
	var output string
	for _, event := range events {
		raw, _ := json.Marshal(event)
		frames, err := d.Event(event["type"].(string), raw)
		if err != nil {
			return output, d.Usage(), err
		}
		for _, frame := range frames {
			output += string(frame)
		}
	}
	frames, err := d.Finish()
	for _, frame := range frames {
		output += string(frame)
	}
	return output, d.Usage(), err
}
func TestResponsesStreamTwoToolsAndEncryptedReasoning(t *testing.T) {
	out, usage, err := feed(t, toolEvents())
	if err != nil {
		t.Fatal(err)
	}
	if usage != (contract.Usage{Present: true, UncachedInputTokens: 21, CacheReadInputTokens: 7, CacheWriteInputTokens: 2, OutputTokens: 9}) {
		t.Fatal(usage)
	}
	for _, want := range []string{`"reasoning":[{"encrypted_content":"opaque"`, `"index":0`, `"id":"a"`, `"index":1`, `"id":"b"`, `"finish_reason":"tool_calls"`, `"completion_tokens":9`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	d := NewDecoder("public", time.Unix(123, 0), true)
	body, _, err := d.JSON([]byte(`{"id":"resp_private","model":"private","status":"completed","output":[` + reasoningJSON + `,` + callAJSON + `,` + callBJSON + `],"usage":` + usageJSON + `}`))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(body, &root)
	var choices []map[string]json.RawMessage
	_ = json.Unmarshal(root["choices"], &choices)
	equalJSON(t, choices[0]["message"], `{"role":"assistant","content":null,"extra_content":{"openai":{"reasoning":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque","status":"completed"}]}},"tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{\"key\":1}"}},{"id":"b","type":"function","function":{"name":"lookup","arguments":"{\"key\":2}"}}]}`)
}
func TestResponsesRejectBrokenStreamingLifecycles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]map[string]any) []map[string]any
	}{
		{"missing terminal", func(e []map[string]any) []map[string]any { return e[:len(e)-1] }},
		{"duplicate sequence", func(e []map[string]any) []map[string]any { e[3]["sequence_number"] = 2; return e }},
		{"wrong item id", func(e []map[string]any) []map[string]any { e[4]["item_id"] = "other"; return e }},
		{"wrong output index", func(e []map[string]any) []map[string]any { e[4]["output_index"] = 2; return e }},
		{"arguments done mismatch", func(e []map[string]any) []map[string]any { e[6]["arguments"] = `{"key":99}`; return e }},
		{"missing arguments done", func(e []map[string]any) []map[string]any { return append(e[:6], e[7:]...) }},
		{"missing item done", func(e []map[string]any) []map[string]any { return append(e[:7], e[8:]...) }},
		{"failed terminal", func(e []map[string]any) []map[string]any { e[len(e)-1]["type"] = "response.failed"; return e }},
		{"response identity changed", func(e []map[string]any) []map[string]any {
			e[len(e)-1]["response"].(map[string]any)["id"] = "other"
			return e
		}},
		{"terminal snapshot changed", func(e []map[string]any) []map[string]any {
			e[len(e)-1]["response"].(map[string]any)["output"].([]any)[1].(map[string]any)["arguments"] = "{}"
			return e
		}},
		{"data after terminal", func(e []map[string]any) []map[string]any {
			return append(e, map[string]any{"type": "response.in_progress", "sequence_number": 100, "response": map[string]string{"id": "resp_private", "status": "in_progress"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := feed(t, tc.mutate(toolEvents())); err == nil {
				t.Fatal("broken lifecycle accepted")
			}
		})
	}
}
func TestResponsesTextRefusalSummaryAndIncomplete(t *testing.T) {
	for _, tc := range []struct{ status, details, finish string }{{"completed", "null", "stop"}, {"incomplete", `{"reason":"max_output_tokens"}`, "length"}, {"incomplete", `{"reason":"content_filter"}`, "content_filter"}} {
		d := NewDecoder("public", time.Unix(1, 0), false)
		out, usage, err := d.JSON([]byte(`{"status":"` + tc.status + `","incomplete_details":` + tc.details + `,"output":[{"type":"reasoning","id":"r","summary":[{"type":"summary_text","text":"plan"}]},{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]},{"type":"refusal","refusal":"no"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":99}}`))
		if err != nil || !usage.TotalMismatch || !strings.Contains(string(out), `"finish_reason":"`+tc.finish+`"`) || !strings.Contains(string(out), `"reasoning_content":"plan"`) || !strings.Contains(string(out), `"refusal":"no"`) {
			t.Fatal(string(out), usage, err)
		}
	}
	for _, output := range []string{`[{"type":"web_search_call","id":"x"}]`, `[{"type":"message","id":"x","role":"assistant","content":[]},{"type":"reasoning","id":"r","summary":[]}]`, `[{"type":"message","id":"x","role":"assistant","content":[{"type":"output_text","text":"x","annotations":[{"type":"file_citation"}]}]}]`} {
		if _, _, err := NewDecoder("m", time.Unix(1, 0), false).JSON([]byte(`{"status":"completed","output":` + output + `}`)); err == nil {
			t.Fatal(output)
		}
	}
}
