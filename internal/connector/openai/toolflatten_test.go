package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestFlattenCompletionGoldenPreservesArgumentsAndUsage(t *testing.T) {
	const body = `{"id":"cmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"prefix","tool_calls":[{"index":3,"id":"call-1","type":"function","function":{"name":"write_file","arguments":" {\"path\": \"a\"} "}},{"index":7,"type":"function","function":{"name":"read_file","arguments":"not-json"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":5}}`
	got, err := flattenCompletion([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil || len(choices) != 1 {
		t.Fatalf("choices: %v %s", err, root["choices"])
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(choices[0]["message"], &message); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(message["content"], &content); err != nil {
		t.Fatal(err)
	}
	want := "prefix\n\n<mx_tool name=\"write_file\" id=\"call-1\">\n {\"path\": \"a\"} \n</mx_tool>\n<mx_tool name=\"read_file\">\nnot-json\n</mx_tool>"
	if content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
	if _, ok := message["tool_calls"]; ok {
		t.Fatal("tool_calls remained after flatten")
	}
	var finish string
	if err := json.Unmarshal(choices[0]["finish_reason"], &finish); err != nil || finish != "stop" {
		t.Fatalf("finish_reason = %q (%v)", finish, err)
	}
	if !bytes.Equal(root["usage"], []byte(`{"prompt_tokens":4,"completion_tokens":5}`)) {
		t.Fatalf("usage changed: %s", root["usage"])
	}
}

func TestFlattenToolCallsAcceptsGappedIncreasingIndicesAndRejectsMixed(t *testing.T) {
	valid := []byte(`[{"index":3,"type":"function","function":{"name":"a","arguments":""}},{"index":7,"type":"function","function":{"name":"b","arguments":"x"}}]`)
	if _, _, err := flattenToolCalls(valid); err != nil {
		t.Fatalf("gapped indices rejected: %v", err)
	}
	for _, raw := range []string{
		`[{"index":3,"type":"function","function":{"name":"a","arguments":""}},{"type":"function","function":{"name":"b","arguments":""}}]`,
		`[{"index":3,"type":"function","function":{"name":"a","arguments":""}},{"index":3,"type":"function","function":{"name":"b","arguments":""}}]`,
		`[{"index":7,"type":"function","function":{"name":"a","arguments":""}},{"index":3,"type":"function","function":{"name":"b","arguments":""}}]`,
	} {
		if _, _, err := flattenToolCalls([]byte(raw)); err == nil {
			t.Fatalf("invalid index shape accepted: %s", raw)
		}
	}
}

func TestFlattenStreamReconstructsGappedToolIndices(t *testing.T) {
	states := make(map[int]*streamChoiceState)
	chunks := []string{
		`{"id":"chunk-1","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":3,"id":"call-3","type":"function","function":{"name":"a","arguments":"x"}}]},"finish_reason":null}]}`,
		`{"id":"chunk-2","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":7,"type":"function","function":{"name":"b","arguments":"y"}}]},"finish_reason":"tool_calls"}]}`,
	}
	var first map[string]json.RawMessage
	for _, chunk := range chunks {
		root, tools, err := accumulateStreamChunk([]byte(chunk), states)
		if err != nil || !tools {
			t.Fatalf("accumulate = tools=%v err=%v", tools, err)
		}
		if first == nil {
			first = root
		}
	}
	body, err := streamCompletionBody(first, states)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	flattened, err := flattenCompletion(body)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(flattened)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(flattened, &root); err != nil {
		t.Fatal(err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil || len(choices) != 1 {
		t.Fatalf("choices: %v %s", err, root["choices"])
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(choices[0]["message"], &message); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(message["content"], &content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `<mx_tool name="a" id="call-3">`) || !strings.Contains(content, `<mx_tool name="b">`) {
		t.Fatalf("flattened content = %q", content)
	}
}

func TestAdapterFlattenStreamCommitsOrdinaryThenRewritesTerminal(t *testing.T) {
	const ordinary = `{"id":"chunk-ordinary","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"prefix"},"finish_reason":null}]}`
	const toolStart = `{"id":"chunk-tool-1","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":3,"id":"call-1","type":"function","function":{"name":"write_file","arguments":"{\"path\":"}}]},"finish_reason":null}]}`
	const toolEnd = `{"id":"chunk-tool-2","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":3,"function":{"arguments":"\"a\"}"}}]},"finish_reason":null}]}`
	const terminal = `{"id":"chunk-finish","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`
	const usage = `{"id":"chunk-usage","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+ordinary+"\n\n", "data: "+toolStart+"\n\n", "data: "+toolEnd+"\n\n", "data: "+terminal+"\n\n", "data: "+usage+"\n\n", "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-flatten-stream"), []byte("cipher-flatten-stream")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_flatten", FlattenToolCalls: true})
	if !result.Success || !result.Committed || result.Failure != FailureNone {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	if !result.Usage.Present || result.Usage.UncachedInputTokens != 7 || result.Usage.OutputTokens != 11 {
		t.Fatalf("usage=%+v", result.Usage)
	}
	var payloads []string
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			payloads = append(payloads, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(payloads) != 5 || payloads[4] != "[DONE]" {
		t.Fatalf("payloads=%q", payloads)
	}
	if !strings.Contains(payloads[0], `"content":"prefix"`) {
		t.Fatalf("ordinary content was not committed immediately: %q", payloads[0])
	}
	var contentChunk map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payloads[1]), &contentChunk); err != nil {
		t.Fatalf("content chunk JSON: %v", err)
	}
	var contentChoices []map[string]json.RawMessage
	if err := json.Unmarshal(contentChunk["choices"], &contentChoices); err != nil || len(contentChoices) != 1 {
		t.Fatalf("content choices: %v %s", err, contentChunk["choices"])
	}
	var delta map[string]json.RawMessage
	if err := json.Unmarshal(contentChoices[0]["delta"], &delta); err != nil {
		t.Fatalf("content delta: %v", err)
	}
	var flattened string
	if err := json.Unmarshal(delta["content"], &flattened); err != nil || !strings.Contains(flattened, `<mx_tool name="write_file" id="call-1">`) {
		t.Fatalf("flattened content=%q err=%v", flattened, err)
	}
	if _, ok := delta["tool_calls"]; ok {
		t.Fatalf("tool_calls remained in flattened delta: %q", payloads[1])
	}
	if !strings.Contains(payloads[2], `"finish_reason":"stop"`) || !strings.Contains(payloads[2], `"delta":{}`) {
		t.Fatalf("rewritten finish=%q", payloads[2])
	}
	if payloads[3] != `{"id":"chunk-usage","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}` {
		t.Fatalf("usage frame changed or moved: %q", payloads[3])
	}
}

func TestAdapterFlattenStreamMalformedAfterOrdinaryUsesSSEError(t *testing.T) {
	const ordinary = `{"id":"chunk-ordinary","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"prefix"},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+ordinary+"\n\n", "data: {bad}\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-flatten-malformed"), []byte("cipher-flatten-malformed")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_flatten", FlattenToolCalls: true})
	if result.Success || !result.Committed || result.Failure != FailureUpstream {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"content":"prefix"`) || !strings.Contains(body, `"error":{"code":"upstream"`) {
		t.Fatalf("ordinary/error frames missing: %q", body)
	}
	if strings.Contains(body, "[DONE]") || strings.Contains(body, "</mx_tool>") {
		t.Fatalf("malformed stream synthesized terminal output: %q", body)
	}
}

func TestReverseFlattenRejectsMissingAttributeSeparator(t *testing.T) {
	if _, _, ok := parseToolOpen(`<mx_tool name="write_file"id="call-1">`); ok {
		t.Fatal("attribute separator was accepted")
	}
	for _, raw := range []string{
		`<mx_tool  name="write_file">`,
		`<mx_tool name="write_file"  id="call-1">`,
		`<mx_tool name="write_file" >`,
		`<mx_tool name="write<file">`,
	} {
		if _, _, ok := parseToolOpen(raw); ok {
			t.Fatalf("non-canonical attribute grammar was accepted: %s", raw)
		}
	}
}

func TestReverseFlattenConvertsInlineToolResultPairs(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","tools":[{"type":"function","function":{"name":"write_file"}}],"messages":[{"role":"assistant","content":"prefix\n\n<mx_tool name=\"write_file\" id=\"call&amp;1\">\nRAW\n</mx_tool>\n<mx_tool_result id=\"call&amp;1\">result\nline</mx_tool_result>"}]} `), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	transformed, err := request.ReverseFlatten()
	if err != nil {
		t.Fatal(err)
	}
	defer transformed.Clear()
	raw, ok := transformed.RawField("messages")
	if !ok {
		t.Fatal("messages missing")
	}
	defer clear(raw)
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) != 2 {
		t.Fatalf("messages: %v %s", err, raw)
	}
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal(messages[0]["tool_calls"], &calls); err != nil || len(calls) != 1 {
		t.Fatalf("tool_calls: %v %s", err, messages[0]["tool_calls"])
	}
	var id string
	_ = json.Unmarshal(calls[0]["id"], &id)
	if id != "call&1" {
		t.Fatalf("call id = %q", id)
	}
	var resultRole, resultID, resultContent string
	_ = json.Unmarshal(messages[1]["role"], &resultRole)
	_ = json.Unmarshal(messages[1]["tool_call_id"], &resultID)
	_ = json.Unmarshal(messages[1]["content"], &resultContent)
	if resultRole != "tool" || resultID != "call&1" || resultContent != "result\nline" {
		t.Fatalf("result = role=%q id=%q content=%q", resultRole, resultID, resultContent)
	}
}

func TestReverseFlattenMalformedPairRemainsText(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","tools":[{"type":"function","function":{"name":"write_file"}}],"messages":[{"role":"assistant","content":"prefix\n\n<mx_tool name=\"write_file\" id=\"call-1\">\nRAW\n</mx_tool>\n<mx_tool_result id=\"wrong\">result</mx_tool_result>"}]} `), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	original, _ := request.RawField("messages")
	defer clear(original)
	transformed, err := request.ReverseFlatten()
	if err != nil {
		t.Fatal(err)
	}
	defer transformed.Clear()
	got, _ := transformed.RawField("messages")
	defer clear(got)
	if !bytes.Equal(got, original) {
		t.Fatalf("malformed history changed: got=%s original=%s", got, original)
	}
}

func TestFlattenBudgetsSpanChoicesAndAcceptExactBoundaries(t *testing.T) {
	one := func(args string) []string { return []string{args} }
	byCalls := func(groups [][]string) []byte {
		choices := make([]map[string]any, 0, len(groups))
		for choiceIndex, group := range groups {
			calls := make([]map[string]any, 0, len(group))
			for callIndex, args := range group {
				calls = append(calls, map[string]any{
					"index": callIndex, "id": "call-" + strconv.Itoa(choiceIndex) + "-" + strconv.Itoa(callIndex),
					"type": "function", "function": map[string]string{"name": "f", "arguments": args},
				})
			}
			choices = append(choices, map[string]any{
				"index":         choiceIndex,
				"message":       map[string]any{"role": "assistant", "content": "", "tool_calls": calls},
				"finish_reason": "tool_calls",
			})
		}
		body, err := json.Marshal(map[string]any{"id": "budget", "choices": choices})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	// The response-wide call cap is not reset at each choice boundary.
	exactCalls := make([][]string, 0, 32)
	for i := 0; i < 32; i++ {
		exactCalls = append(exactCalls, one(""))
	}
	if _, err := flattenCompletion(byCalls(exactCalls)); err != nil {
		t.Fatalf("exact 32-call choices rejected: %v", err)
	}
	overCalls := append(append([][]string(nil), exactCalls...), one(""))
	if _, err := flattenCompletion(byCalls(overCalls)); err == nil {
		t.Fatal("33 calls across choices were accepted")
	}

	arg64 := strings.Repeat("a", maxFlattenArguments)
	if _, err := flattenCompletion(byCalls([][]string{one(arg64), one(arg64), one(arg64), one(arg64)})); err != nil {
		t.Fatalf("exact aggregate argument budget rejected: %v", err)
	}
	if _, err := flattenCompletion(byCalls([][]string{one(arg64), one(arg64), one(arg64), one(arg64), one("x")})); err == nil {
		t.Fatal("aggregate argument budget +1 was accepted")
	}
}

func TestReverseBudgetsSpanAssistantMessagesAndAcceptExactBoundary(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}
	pair := func(id, args, result string) json.RawMessage {
		content := "<mx_tool name=\"f\" id=\"" + id + "\">\n" + args + "\n</mx_tool>\n<mx_tool_result id=\"" + id + "\">" + result + "</mx_tool_result>"
		raw, err := json.Marshal(map[string]any{"role": "assistant", "content": content})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	arg64 := strings.Repeat("a", maxFlattenArguments)
	result64 := strings.Repeat("b", maxFlattenArguments)
	exact := []json.RawMessage{pair("one", arg64, result64), pair("two", arg64, result64)}
	out, changed := reverseMessages(exact, allowed)
	if !changed || len(out) != 4 {
		t.Fatalf("exact request aggregate not transformed: changed=%v messages=%d", changed, len(out))
	}
	// The third pair is kept as ordinary text once the request-wide budget is
	// already full; the two earlier messages remain fully transformed.
	withOneMore := append(append([]json.RawMessage(nil), exact...), pair("three", "", "x"))
	out, changed = reverseMessages(withOneMore, allowed)
	if !changed || len(out) != 5 {
		t.Fatalf("over-budget message handling: changed=%v messages=%d", changed, len(out))
	}
	var untouched map[string]json.RawMessage
	if err := json.Unmarshal(out[4], &untouched); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(untouched["content"], &content); err != nil || !strings.Contains(content, "three") {
		t.Fatalf("over-budget message was partially consumed: %q (%v)", content, err)
	}
}

func TestReverseFlattenRequiresExactOpeningLFAndCanonicalEntities(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}
	validPair := func(open, args string) string {
		return open + args + "\n</mx_tool>\n<mx_tool_result id=\"call-1\">result</mx_tool_result>"
	}
	for _, content := range []string{
		validPair("<mx_tool name=\"f\" id=\"call-1\">", "args"),
		validPair("<mx_tool name=\"f\" id=\"call-1\">\r\n", "args"),
		validPair("<mx_tool name=\"f\" id=\"call-1\">", "args</mx_tool>suffix"),
	} {
		if _, _, _, ok := parseFlattenedContent(content, allowed); ok {
			t.Fatalf("malformed opening/body was accepted: %q", content[:min(len(content), 80)])
		}
	}
	name, id, ok := parseToolOpen("<mx_tool name=\"f\" id=\"a&amp;lt;\">")
	if !ok || name != "f" || id != "a&lt;" {
		t.Fatalf("canonical entity round-trip: name=%q id=%q ok=%v", name, id, ok)
	}
	for _, open := range []string{
		"<mx_tool name=\"f\" id=\"a&unknown;\">",
		"<mx_tool name=\"f\" id=\"a<\">",
	} {
		if _, _, ok := parseToolOpen(open); ok {
			t.Fatalf("unknown/non-canonical entity accepted: %s", open)
		}
	}
}

func TestReverseFlattenAllOrNoneAndStructuredOrNoToolsRemainText(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}
	good := "<mx_tool name=\"f\" id=\"call-1\">\nargs\n</mx_tool>\n<mx_tool_result id=\"call-1\">result</mx_tool_result>"
	bad := "<mx_tool name=\"f\" id=\"call-2\">\nargs\n</mx_tool>\n<mx_tool_result id=\"wrong\">result</mx_tool_result>"
	rawGood, _ := json.Marshal(map[string]any{"role": "assistant", "content": good})
	rawBad, _ := json.Marshal(map[string]any{"role": "assistant", "content": bad})
	var original map[string]json.RawMessage
	_ = json.Unmarshal(rawBad, &original)
	out, changed := reverseMessages([]json.RawMessage{rawGood, rawBad}, allowed)
	if !changed || len(out) != 3 {
		t.Fatalf("all-or-none transformation: changed=%v messages=%d", changed, len(out))
	}
	var kept map[string]json.RawMessage
	_ = json.Unmarshal(out[2], &kept)
	if !bytes.Equal(kept["content"], original["content"]) {
		t.Fatalf("malformed message was partially consumed: %s", out[2])
	}

	noTools, err := DecodeChatRequest(strings.NewReader("{\"model\":\"p/m\",\"messages\":[{\"role\":\"assistant\",\"content\":"+strconv.Quote(good)+"}]}"), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer noTools.Clear()
	noToolsOut, err := noTools.ReverseFlatten()
	if err != nil {
		t.Fatal(err)
	}
	defer noToolsOut.Clear()
	before, _ := noTools.RawField("messages")
	after, _ := noToolsOut.RawField("messages")
	if !bytes.Equal(before, after) {
		t.Fatalf("message changed without root tools: before=%s after=%s", before, after)
	}
	clear(before)
	clear(after)

	structured, err := DecodeChatRequest(strings.NewReader("{\"model\":\"p/m\",\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"f\"}}],\"messages\":[{\"role\":\"assistant\",\"content\":"+strconv.Quote(good)+",\"tool_calls\":[]}]}"), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer structured.Clear()
	structuredOut, err := structured.ReverseFlatten()
	if err != nil {
		t.Fatal(err)
	}
	defer structuredOut.Clear()
	before, _ = structured.RawField("messages")
	after, _ = structuredOut.RawField("messages")
	if !bytes.Equal(before, after) {
		t.Fatalf("structured assistant message was rewritten")
	}
	clear(before)
	clear(after)
}

func TestFlattenStreamMixedChoicesKeepsOrdinaryTerminalAndBoundsAccumulation(t *testing.T) {
	ordinary := "{\"id\":\"mixed-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ordinary\"},\"finish_reason\":null},{\"index\":1,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-tool\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"a\"}}]},\"finish_reason\":null}]}"
	ordinaryFinish := "{\"id\":\"mixed-2\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"b\"}}]},\"finish_reason\":null}]}"
	toolFinish := "{\"id\":\"mixed-3\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":1,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}"
	states := make(map[int]*streamChoiceState)
	var first map[string]json.RawMessage
	for _, chunk := range []string{ordinary, ordinaryFinish, toolFinish} {
		root, _, err := accumulateStreamChunk([]byte(chunk), states)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = root
		}
	}
	body, err := streamCompletionBodyAfter(first, states)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil || len(choices) != 2 {
		t.Fatalf("mixed choices=%v %s", err, root["choices"])
	}
	for _, choice := range choices {
		var index int
		var finish string
		_ = json.Unmarshal(choice["index"], &index)
		_ = json.Unmarshal(choice["finish_reason"], &finish)
		if (index == 0 && finish != "stop") || (index == 1 && finish != "tool_calls") {
			t.Fatalf("mixed finish index=%d reason=%s", index, finish)
		}
	}

	arg64 := strings.Repeat("x", maxFlattenArguments)
	bounded := make(map[int]*streamChoiceState)
	quoted, _ := json.Marshal(arg64)
	for i := 0; i < 4; i++ {
		chunk := "{\"choices\":[{\"index\":" + strconv.Itoa(i) + ",\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":" + string(quoted) + "}}]},\"finish_reason\":null}]}"
		if _, _, err := accumulateStreamChunk([]byte(chunk), bounded); err != nil {
			t.Fatalf("exact stream aggregate rejected at %d: %v", i, err)
		}
	}
	if got, ok := streamArgumentBytes(bounded); !ok || got != maxFlattenAggregate {
		t.Fatalf("stream aggregate=%d valid=%v", got, ok)
	}
	chunk := "{\"choices\":[{\"index\":4,\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"x\"}}]},\"finish_reason\":null}]}"
	if _, _, err := accumulateStreamChunk([]byte(chunk), bounded); err == nil {
		t.Fatal("stream aggregate +1 was accepted")
	}
	if got := len(bounded[4].Tools[0].Arguments); got != 0 {
		t.Fatalf("over-budget stream argument retained %d bytes", got)
	}
}

func TestFlattenStreamValidatesToolIDAndNameAtDeltaBoundary(t *testing.T) {
	chunk := func(index int, id, name string) []byte {
		body, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"index": index,
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": id, "type": "function",
					"function": map[string]string{"name": name, "arguments": ""},
				}}},
				"finish_reason": nil,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	valid := make(map[int]*streamChoiceState)
	if _, _, err := accumulateStreamChunk(chunk(0, strings.Repeat("i", 128), strings.Repeat("n", 64)), valid); err != nil {
		t.Fatalf("exact id/name boundary rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		id   string
		tool string
	}{
		{"id +1", strings.Repeat("i", 129), "f"},
		{"name +1", "call-1", strings.Repeat("n", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := accumulateStreamChunk(chunk(1, tc.id, tc.tool), make(map[int]*streamChoiceState)); err == nil {
				t.Fatalf("over-limit %s delta accepted", tc.name)
			}
		})
	}
}

func TestMarshalPolicyOverridesStoreWithoutMutatingIngress(t *testing.T) {
	request, err := DecodeChatRequest(strings.NewReader(`{"model":"p/m","store":null,"messages":[]}`), MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	body, err := request.marshalUpstreamWithPolicy("upstream", "safe", connectorcontract.AttemptPolicy{ForceStoreFalse: true})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(body)
	if bytes.Count(body, []byte(`"store"`)) != 1 {
		t.Fatalf("store was duplicated: %s", body)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || !bytes.Equal(root["store"], []byte("false")) {
		t.Fatalf("store projection: %v %s", err, body)
	}
	original, _ := request.RawField("store")
	defer clear(original)
	if !bytes.Equal(original, []byte("null")) {
		t.Fatalf("ingress store mutated: %s", original)
	}
	for _, raw := range []string{
		`{"model":"p/m","messages":[],"store":true}`,
		`{"store":true,"model":"p/m","messages":[]}`,
		`{"model":"p/m","messages":[]}`,
	} {
		r, err := DecodeChatRequest(strings.NewReader(raw), MaxRequestBodyBytes)
		if err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		body, err := r.marshalUpstreamWithPolicy("upstream", "safe", connectorcontract.AttemptPolicy{ForceStoreFalse: true})
		r.Clear()
		if err != nil || bytes.Count(body, []byte(`"store"`)) != 1 {
			clear(body)
			t.Fatalf("policy store projection=%s err=%v", body, err)
		}
		clear(body)
	}
}
