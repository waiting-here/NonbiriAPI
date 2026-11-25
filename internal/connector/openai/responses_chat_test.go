package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestResponsesChatTypedTextWithinConversionSubset(t *testing.T) {
	const content = `[{"type":"text","text":"two "},{"type":"text","text":"parts"}]`
	const thought = `[{"type":"thinking","thinking":[{"type":"text","text":"thought"}]}]`
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, content string
			accepted      bool
		}{
			{"text", content, true},
			{"thinking", thought, false},
			{"reference", `[{"type":"text","text":"see note","citations":[12]}]`, false},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				message := `{"role":"assistant","content":` + tc.content + `}`
				data := `{"id":"c","object":"chat.completion","created":1,"choices":[{"index":0,"message":` + message + `,"finish_reason":"stop"}]}`
				if stream {
					data = strings.TrimSpace(strings.TrimPrefix(chatResponseChunk(message, `"stop"`, `null`), "data: "))
				}
				state := responseChatState{model: "public"}
				defer state.clear()
				err := state.consume([]byte(data), stream)
				if (err == nil) != tc.accepted || tc.accepted && (state.text != "two parts" || state.reasoning != "") {
					t.Fatalf("text=%q reasoning=%q error=%v", state.text, state.reasoning, err)
				}
			})
		}
	}
}

func TestResponsesChatRejectsUnreplayableReasoning(t *testing.T) {
	for _, fields := range []string{
		`"content":"answer","reasoning_content":{"text":"thought"}`,
		`"content":"answer","reasoning_content":"thought","reasoning_details":[{"type":"reasoning.text","text":"thought","signature":"signed"}]`,
		`"content":"answer","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]`,
		`"content":"answer","reasoning":"thought","reasoning_content":"thought"`,
		`"content":"answer","reasoning":"first","reasoning_content":"second"`,
		`"content":"answer","reasoning":["thought"]`,
		`"content":[{"type":"thinking","thinking":"","signature":"signed"}]`,
		`"content":[{"type":"thinking","thinking":[]}],"reasoning_content":"thought"`,
	} {
		message := `{"role":"assistant",` + fields + `}`
		for _, stream := range []bool{false, true} {
			data := `{"id":"c","object":"chat.completion","created":1,"choices":[{"index":0,"message":` + message + `,"finish_reason":"stop"}]}`
			if stream {
				data = strings.TrimSpace(strings.TrimPrefix(chatResponseChunk(message, `"stop"`, `null`), "data: "))
			}
			var state responseChatState
			err := state.consume([]byte(data), stream)
			state.clear()
			if err == nil {
				t.Fatalf("stream=%t accepted %s", stream, fields)
			}
		}
	}
}

func TestResponsesChatScalarReasoningRoundTrip(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning"} {
		t.Run(field, func(t *testing.T) { testResponsesChatScalarRoundTrip(t, field) })
	}
}

func TestResponsesChatKeepsSeparateReasoningTurns(t *testing.T) {
	history := `[{"type":"reasoning","id":"rsalias_first","summary":[],"content":[{"type":"reasoning_text","text":"first"}]},
		{"type":"message","id":"msg_second","role":"assistant","content":[{"type":"output_text","text":"second"}]},
		{"type":"reasoning","id":"rs_third","summary":[],"content":[{"type":"reasoning_text","text":"third"}]},
		{"type":"function_call","id":"fc_fourth_0","call_id":"c","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"c","output":"done"}]`
	messages, err := responseChatMessages([]byte(history), nil)
	if err != nil || len(messages) != 5 {
		t.Fatalf("merged independent assistant turns: %v, %v", messages, err)
	}
	if messages[0].(map[string]any)["reasoning"] != "first" || messages[2].(map[string]any)["reasoning_content"] != "third" {
		t.Fatal(messages)
	}
}

func TestResponsesChatIncompleteTextReplay(t *testing.T) {
	for _, tc := range []struct{ finish, detail, field string }{
		{"length", "max_output_tokens", "reasoning_content"},
		{"content_filter", "content_filter", "reasoning"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.finish, stream), func(t *testing.T) {
				message := `{"role":"assistant","content":"partial answer","` + tc.field + `":"thought"}`
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, chatResponseChunk(message, fmt.Sprintf("%q", tc.finish), "null")+"data: [DONE]\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"choices":[{"index":0,"message":%s,"finish_reason":%q}]}`, message, tc.finish)
				}))
				defer server.Close()
				request := inferenceRequestForTest(t, contract.OperationResponses, fmt.Sprintf(`{"model":"public","input":"hi","stream":%t}`, stream))
				defer request.Clear()
				writer := httptest.NewRecorder()
				result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "upstream", NewCredential([]byte("fixture-key"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
				if !result.Success {
					t.Fatal(result)
				}
				response := writer.Body.Bytes()
				if stream {
					events := projectionEvents(t, writer.Body.String())
					if len(events) == 0 || string(events[len(events)-1]["type"]) != `"response.incomplete"` {
						t.Fatal(writer.Body)
					}
					response = events[len(events)-1]["response"]
				}
				var terminal struct {
					Status string
					Output []json.RawMessage
					Detail struct{ Reason string } `json:"incomplete_details"`
				}
				if json.Unmarshal(response, &terminal) != nil || terminal.Status != "incomplete" || terminal.Detail.Reason != tc.detail || len(terminal.Output) != 2 {
					t.Fatal(string(response))
				}
				for _, item := range terminal.Output {
					if !bytes.Contains(item, []byte(`"status":"incomplete"`)) {
						t.Fatal(string(item))
					}
				}
				input := append(terminal.Output, json.RawMessage(`{"role":"user","content":"continue"}`))
				history, _ := json.Marshal(input)
				replay := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":`+string(history)+`}`)
				defer replay.Clear()
				body, err := replay.compileChatResponse("upstream", contract.AttemptPolicy{})
				var chat struct{ Messages []map[string]json.RawMessage }
				if err != nil || json.Unmarshal(body, &chat) != nil || len(chat.Messages) != 2 {
					t.Fatalf("replay failed: %s, %v", body, err)
				}
				if string(chat.Messages[0][tc.field]) != `"thought"` || string(chat.Messages[0]["content"]) != `"partial answer"` || string(chat.Messages[1]["content"]) != `"continue"` {
					t.Fatal(string(body))
				}
			})
		}
	}
}

func testResponsesChatScalarRoundTrip(t *testing.T, field string) {
	t.Helper()
	const answer = `"content":"answer","refusal":"declined"`
	const call = `"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}`
	const usage = `{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":1}}`
	other, expectedID := "reasoning", `"rs_c1"`
	if field == "reasoning" {
		other, expectedID = "reasoning_content", `"rsalias_c1"`
	}
	jsonWire := `{"id":"c1","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant",` + answer + `,"` + field + `":"one two","` + other + `":"","tool_calls":[{` + call + `}]},"finish_reason":"tool_calls"}],"usage":` + usage + `}`
	for _, mode := range []string{"json", "reasoning first", "answer first"} {
		t.Run(mode, func(t *testing.T) {
			stream := mode != "json"
			wire := jsonWire
			if stream {
				reasoning := chatResponseChunk(`{"`+field+`":"one "}`, "null", "null")
				message := chatResponseChunk(`{`+answer+`,"`+other+`":null}`, "null", "null")
				wire = reasoning + message
				if mode == "answer first" {
					wire = message + reasoning
				}
				wire += chatResponseChunk(`{"`+field+`":"two","tool_calls":[{"index":0,`+call+`}]}`, `"tool_calls"`, usage) + "data: [DONE]\n\n"
			}
			replayed := make(chan []map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Messages []map[string]json.RawMessage }
				if json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Path != "/v1/chat/completions" {
					t.Error("invalid upstream Chat request")
				}
				if len(body.Messages) > 1 {
					replayed <- body.Messages
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, jsonWire)
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				io.WriteString(w, wire)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			run := func(input string, stream bool) *httptest.ResponseRecorder {
				t.Helper()
				request := inferenceRequestForTest(t, contract.OperationResponses, fmt.Sprintf(`{"model":"public","input":%s,"stream":%t}`, input, stream))
				defer request.Clear()
				writer := httptest.NewRecorder()
				result := adapter.AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "upstream", NewCredential([]byte("fixture-key"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
				if !result.Success || !result.Usage.Present || result.Usage.OutputTokens != 3 {
					t.Fatalf("result=%+v body=%s", result, writer.Body)
				}
				return writer
			}
			public := run(`"hi"`, stream).Body
			response := json.RawMessage(public.Bytes())
			if stream {
				var deltas strings.Builder
				var done string
				for _, event := range projectionEvents(t, public.String()) {
					switch string(event["type"]) {
					case `"response.reasoning_text.delta"`:
						var delta string
						if json.Unmarshal(event["delta"], &delta) != nil || string(event["item_id"]) != expectedID {
							t.Fatal("invalid reasoning delta", event)
						}
						deltas.WriteString(delta)
					case `"response.reasoning_text.done"`:
						_ = json.Unmarshal(event["text"], &done)
					case `"response.completed"`:
						response = event["response"]
					}
				}
				if deltas.String() != "one two" || done != "one two" {
					t.Fatal(public.String())
				}
			}
			var terminal struct {
				Output []json.RawMessage
				Usage  json.RawMessage
			}
			if json.Unmarshal(response, &terminal) != nil || len(terminal.Output) != 3 || !bytes.Contains(terminal.Usage, []byte(`"reasoning_tokens":1`)) {
				t.Fatal(string(response))
			}
			reasonIndex := 0
			if mode == "answer first" {
				reasonIndex = 1
			}
			if !bytes.Contains(terminal.Output[reasonIndex], []byte(`"text":"one two","type":"reasoning_text"`)) || !bytes.Contains(terminal.Output[reasonIndex], []byte(`"id":`+expectedID)) || !bytes.Contains(terminal.Output[reasonIndex], []byte(`"summary":[]`)) {
				t.Fatal(string(response))
			}
			input := []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"older turn"}`)}
			input = append(input, terminal.Output...)
			input = append(input, json.RawMessage(`{"type":"function_call_output","call_id":"call","output":"done"}`))
			replay, _ := json.Marshal(input)
			run(string(replay), false)
			select {
			case messages := <-replayed:
				if len(messages) != 3 || messages[0][field] != nil || string(messages[1][field]) != `"one two"` || messages[1][other] != nil || string(messages[1]["content"]) != `"answer"` || string(messages[1]["refusal"]) != `"declined"` || !bytes.Contains(messages[1]["tool_calls"], []byte(`"id":"call"`)) || string(messages[2]["tool_call_id"]) != `"call"` {
					t.Fatalf("upstream history: %s", messages)
				}
			default:
				t.Fatal("replayed history did not reach the upstream")
			}
		})
	}
}

func TestResponsesChatFiniteCompilation(t *testing.T) {
	body := `{"model":"public/m","instructions":"system rules","input":[{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"data:image/png;base64,YQ==","detail":"low"}]},{"type":"function_call","call_id":"call1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call1","output":"result"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"lookup"},"parallel_tool_calls":false,"max_output_tokens":13,"temperature":0.4,"top_p":0.9,"text":{"format":{"type":"json_schema","name":"result","schema":{"type":"object","properties":{"answer":{"type":"string"}}}}},"stream":true}`
	request := inferenceRequestForTest(t, contract.OperationResponses, body)
	encoded, err := request.compileChatResponse("private", contract.AttemptPolicy{SafetyIdentifier: "consumer"})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &out)
	if string(out["model"]) != `"private"` || string(out["store"]) != "false" || string(out["max_completion_tokens"]) != "13" || out["input"] != nil || !bytes.Contains(out["stream_options"], []byte(`"include_usage":true`)) {
		t.Fatalf("mapped=%s", encoded)
	}
	if !bytes.Contains(out["tools"], []byte(`"strict":true`)) || !bytes.Contains(out["tools"], []byte(`"additionalProperties":false`)) || !bytes.Contains(out["tools"], []byte(`"required":["query"]`)) || bytes.Contains(out["response_format"], []byte(`"strict"`)) || bytes.Contains(out["response_format"], []byte(`"required"`)) {
		t.Fatal("Responses strict defaults lost")
	}
	if !bytes.Contains(out["messages"], []byte(`"tool_call_id":"call1"`)) || !bytes.Contains(out["messages"], []byte(`"role":"system"`)) || !bytes.Contains(out["messages"], []byte(`"type":"image_url"`)) {
		t.Fatal("history correlation lost")
	}
	var original map[string]json.RawMessage
	logical, _ := request.LogicalBody()
	_ = json.Unmarshal(logical, &original)
	if bytes.Contains(original["tools"], []byte(`"strict"`)) {
		t.Fatal("compilation mutated caller")
	}
}

func TestResponsesChatRejectsUnrepresentableRequests(t *testing.T) {
	for _, tc := range []struct{ extra, field string }{
		{`"reasoning":{"effort":"low","summary":"auto"}`, "reasoning"},
		{`"include":["reasoning.encrypted_content"]`, "include"},
		{`"truncation":"auto"`, "truncation"},
		{`"metadata":{"PRIVATE_NAME":"PRIVATE_VALUE"}`, "metadata"},
		{`"tools":[{"type":"web_search"}]`, "tools"},
		{`"text":{"verbosity":"low"}`, "text"},
		{`"text":{"format":{"type":"PRIVATE_FORMAT"}}`, "text.format"},
		{`"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[]}`, "tool_choice"},
		{`"max_output_tokens":0`, "max_output_tokens"},
		{`"temperature":3`, "temperature"},
		{`"top_p":2`, "top_p"},
		{`"parallel_tool_calls":"PRIVATE_VALUE"`, "parallel_tool_calls"},
		{`"instructions":{}`, "instructions"},
		{`"PRIVATE_OPTION":"PRIVATE_VALUE"`, "request"},
	} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi",`+tc.extra+`}`)
		err := request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Detail == nil || validation.Detail.Field != tc.field || validation.Detail.Reason != "Responses fields cannot be represented by the selected Chat protocol" {
			t.Fatalf("wrong conversion detail for %s: %#v, %v", tc.field, validation, err)
		}
		if strings.Contains(fmt.Sprint(validation.Detail), "PRIVATE") {
			t.Fatal("caller values entered conversion detail")
		}
		if request.CheckTarget(contract.TypeOpenAICompatible, contract.AttemptPolicy{}) != nil {
			t.Fatal("native capability restricted by conversion")
		}
	}
	for _, input := range []string{
		`[{"type":"reasoning","encrypted_content":"opaque"}]`,
		`[{"type":"reasoning","summary":[{"type":"summary_text","text":"summary"}],"content":[{"type":"reasoning_text","text":"thought"}]}]`,
		`[{"type":"reasoning","summary":[],"encrypted_content":"opaque","content":[{"type":"reasoning_text","text":"thought"}]}]`,
		`[{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"thought","signature":"signed"}]}]`,
		`[{"type":"reasoning","status":"in_progress","content":[{"type":"reasoning_text","text":"thought"}]}]`,
		`[{"type":"message","role":"assistant","status":"in_progress","content":"partial"}]`,
		`[{"type":"message","role":"user","status":"incomplete","content":"partial"}]`,
		`[{"type":"function_call","status":"incomplete","call_id":"c","name":"f","arguments":"{"},{"type":"function_call_output","call_id":"c","output":"done"}]`,
		`[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c","status":"incomplete","output":"partial"}]`,
		`[{"role":"user","content":[{"type":"input_file","file_data":"inline","filename":"a.pdf"}]}]`,
		`[{"type":"function_call_output","call_id":"missing","output":"x"}]`,
		`[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}]`,
		`[{"role":"assistant","content":[{"type":"output_text","text":"x","annotations":[{"type":"url_citation"}]}]}]`,
	} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":`+input+`}`)
		if request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{}) == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if InferenceSupported(contract.TypeOpenAIChatCompletions, contract.OperationResponsesCompact) || InferenceSupported(contract.TypeOpenAIChatCompletions, contract.OperationResponsesInputTokens) {
		t.Fatal("unsupported conversion advertised")
	}
}

func TestResponsesChatPreservesExplicitStrictAndTextDefaults(t *testing.T) {
	for _, strict := range []string{"true", "false"} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","strict":`+strict+`,"parameters":{"type":"object","properties":{"x":{"type":"string","minLength":2}}}}],"text":{"format":{"type":"json_schema","name":"result","strict":`+strict+`,"schema":{"type":"object","properties":{"x":{"type":"string"}}}}}}`)
		body, err := request.compileChatResponse("m", contract.AttemptPolicy{})
		if err != nil || bytes.Contains(body, []byte(`"required"`)) || bytes.Contains(body, []byte(`"additionalProperties"`)) || bytes.Count(body, []byte(`"strict":`+strict)) != 2 {
			t.Fatalf("explicit strict altered: %s %v", body, err)
		}
	}
	request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"x":{"type":"string","minLength":2}}}}]}`)
	if request.CheckTarget(contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{}) == nil {
		t.Fatal("guessed automatic strict fallback outside supported subset")
	}
}

func TestResponsesChatJSONUsesExplicitPathAndPreservesUsage(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.URL.Path != "/v1/chat/completions" {
					t.Error("conversion used wrong path")
				}
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["messages"] == nil || string(body["model"]) != `"private"` {
					t.Error("wrong protocol body")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"private","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":%q}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2}}}`, reason)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public/m","input":"hi"}`)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
			if !result.Success || !result.Usage.Present || result.Usage.CacheReadInputTokens != 2 || count != 1 || writer.started != 1 || !strings.Contains(writer.Body.String(), `"object":"response"`) || strings.Contains(writer.Body.String(), "private") {
				t.Fatalf("result=%+v body=%s", result, writer.Body.String())
			}
			if reason != "stop" && !strings.Contains(writer.Body.String(), `"status":"incomplete"`) {
				t.Fatal("incomplete output reported complete")
			}
		})
	}
	request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi"}`)
	bad := []byte(`{"id":"c1","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","audio":{}},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	if _, usage, err := projectChatAsResponse(bad, request); err == nil || !usage.Present || usage.OutputTokens != 1 {
		t.Fatal("failed output lost standard usage")
	}
}

func chatResponseChunk(delta, finish, usage string) string {
	return `data: {"id":"c1","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}],"usage":` + usage + "}\n\n"
}
func TestResponsesChatSSETerminationToolsAndUsage(t *testing.T) {
	usage := `data: {"id":"c1","object":"chat.completion.chunk","created":1,"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n"
	text := chatResponseChunk(`{"role":"assistant","content":"hello"}`, `null`, `null`)
	tool := chatResponseChunk(`{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"look","arguments":"{"}}]}`, `null`, `null`) + chatResponseChunk(`{"tool_calls":[{"index":0,"function":{"name":"up","arguments":"}"}}]}`, `"tool_calls"`, `null`)
	tests := []struct {
		name, wire     string
		success, known bool
	}{
		{"text", text + chatResponseChunk(`{}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", true, true},
		{"tools", tool + usage + "data: [DONE]\n\n", true, true},
		{"partial", text, false, false},
		{"finish_without_done", text + chatResponseChunk(`{}`, `"stop"`, `null`) + usage, false, true},
		{"done_without_finish", text + "data: [DONE]\n\n", false, false},
		{"extra_after_finish", text + chatResponseChunk(`{}`, `"stop"`, `null`) + chatResponseChunk(`{"content":"extra"}`, `null`, `null`) + "data: [DONE]\n\n", false, false},
		{"reported_error", text + `data: {"error":{"message":"failed"},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n", false, true},
		{"secret_split", chatResponseChunk(`{"content":"secret-"}`, `null`, `null`) + chatResponseChunk(`{"content":"material"}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", false, false},
		{"reasoning_secret_split", chatResponseChunk(`{"reasoning_content":"secret-"}`, `null`, `null`) + chatResponseChunk(`{"reasoning_content":"material"}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", false, false},
		{"reasoning_changes_source", chatResponseChunk(`{"reasoning":"first"}`, `null`, `null`) + chatResponseChunk(`{"reasoning_content":"second"}`, `"stop"`, `null`) + usage + "data: [DONE]\n\n", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.wire)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public/m","input":"hi","stream":true}`)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
			if result.Success != test.success || result.Usage.Present != test.known || strings.Contains(writer.Body.String(), "secret-material") {
				t.Fatalf("result=%+v body=%s", result, writer.Body.String())
			}
			if test.success && (!strings.Contains(writer.Body.String(), "event: response.completed") || strings.Contains(writer.Body.String(), "[DONE]")) {
				t.Fatal("wrong public termination")
			}
			if test.name == "tools" && (!strings.Contains(writer.Body.String(), `"call_id":"call1"`) || !strings.Contains(writer.Body.String(), `"name":"lookup"`) || !strings.Contains(writer.Body.String(), `"arguments":"{}"`)) {
				t.Fatal("tool correlation lost")
			}
		})
	}
}
