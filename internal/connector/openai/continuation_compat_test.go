package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestResponsesGeneratedTextAndRefusalReplay(t *testing.T) {
	for _, message := range []string{`{"role":"assistant","content":"hello"}`, `{"role":"assistant","content":null,"refusal":"cannot help"}`} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":"hello"}`)
		raw := `{"id":"completion","object":"chat.completion","created":1,"choices":[{"index":0,"message":` + message + `,"finish_reason":"stop"}]}`
		projected, _, err := projectChatAsResponse([]byte(raw), request)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]json.RawMessage
		_ = json.Unmarshal(projected, &root)
		replay := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":`+string(root["output"])+`}`)
		out, err := replay.compileChatResponse("upstream", contract.AttemptPolicy{})
		if err != nil || strings.Contains(string(out), "logprobs") || !strings.Contains(string(out), `"role":"assistant"`) {
			t.Fatalf("replay=%s err=%v", out, err)
		}
		if strings.Contains(message, "refusal") && !strings.Contains(string(out), `"refusal":"cannot help"`) {
			t.Fatal(string(out))
		}
	}
	for _, content := range []string{`[{"type":"output_text","text":"x","logprobs":[1]}]`, `[{"type":"refusal","refusal":"x"}]`} {
		if _, err := responseChatContent([]byte(content), "user"); err == nil {
			t.Fatal("unsupported user content accepted")
		}
	}
}

func TestRerankTokenAliasesPreferCurrentBillingMetadata(t *testing.T) {
	r := inferenceRequestForTest(t, contract.OperationRerank, `{"model":"public","query":"q","documents":["one"]}`)
	for _, tc := range []struct {
		name             string
		kind             contract.Type
		fields           string
		input, output    int64
		mismatch, search bool
	}{
		{"cohere billed input", contract.TypeCohereRerank, `"meta":{"billed_units":{"input_tokens":192}}`, 192, 0, false, false},
		{"cohere both units", contract.TypeCohereRerank, `"meta":{"billed_units":{"input_tokens":192,"search_units":2},"tokens":{"input_tokens":192,"output_tokens":0}}`, 192, 0, false, true},
		{"cohere conflict", contract.TypeCohereRerank, `"meta":{"billed_units":{"input_tokens":192},"tokens":{"input_tokens":200,"output_tokens":0}}`, 192, 0, true, false},
		{"siliconflow current", contract.TypeSiliconFlowRerank, `"meta":{"tokens":{"input_tokens":150,"output_tokens":10}}`, 150, 10, false, false},
		{"siliconflow legacy", contract.TypeSiliconFlowRerank, `"tokens":{"input_tokens":150,"output_tokens":10}`, 150, 10, false, false},
		{"siliconflow conflict", contract.TypeSiliconFlowRerank, `"meta":{"tokens":{"input_tokens":150,"output_tokens":10}},"tokens":{"input_tokens":160,"output_tokens":10}`, 150, 10, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, usage, err := projectRerankResponse([]byte(`{"results":[{"index":0,"relevance_score":0.9}],`+tc.fields+`}`), r, tc.kind)
			if err != nil || !usage.Present || usage.UncachedInputTokens != tc.input || usage.OutputTokens != tc.output || usage.TotalMismatch != tc.mismatch || usage.SearchUnitsPresent != tc.search {
				t.Fatalf("usage=%+v err=%v output=%s", usage, err, out)
			}
		})
	}
}

func TestInputTokenCountingAllowsMissingAndNullInlineInput(t *testing.T) {
	for _, operation := range []contract.Operation{contract.OperationResponses, contract.OperationResponsesCompact, contract.OperationResponsesInputTokens} {
		for _, input := range []string{"", `,"input":null`, `,"input":[]`} {
			for _, context := range []string{`"instructions":"Be concise"`, `"tools":[{"type":"function","name":"clock","parameters":{"type":"object"}}]`} {
				r, err := DecodeInferenceRequest(strings.NewReader(`{"model":"public",`+context+input+`}`), operation, 0)
				if r != nil {
					r.Clear()
				}
				if (err == nil) != (operation == contract.OperationResponsesInputTokens || input == `,"input":[]`) {
					t.Fatalf("operation=%s input=%s err=%v", operation, input, err)
				}
			}
		}
	}
}

func TestCompletionEmptyPromptBatchesRemainDistinctFromEmbeddings(t *testing.T) {
	for _, prompt := range []string{`""`, `[""]`, `["","hi"]`, `["a","b"]`} {
		r, err := DecodeInferenceRequest(strings.NewReader(`{"model":"public","prompt":`+prompt+`}`), contract.OperationCompletions, 0)
		if err != nil {
			t.Fatal(prompt, err)
		}
		r.Clear()
	}
	if r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"public","input":["","hi"]}`), 0); err == nil {
		r.Clear()
		t.Fatal("empty embedding accepted")
	}
}

func TestWhisperStreamRequestAcceptsOnlyItsKnownJSONFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if r.FormValue("stream") != "true" {
			t.Error("stream flag lost")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"complete transcript"}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		model, format string
		valid         bool
	}{{"whisper-1", "", true}, {"whisper-1", "json", true}, {"whisper-1", "text", false}, {"gpt-4o-transcribe", "", false}} {
		fields := map[string]string{"model": "public", "stream": "true"}
		if tc.format != "" {
			fields["response_format"] = tc.format
		}
		r := uploadForTest(t, contract.OperationAudioTranscriptions, fields, map[string][]byte{"file": []byte("small fixture")})
		w := httptest.NewRecorder()
		result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), w, NewTarget(server.URL, tc.model, NewCredential([]byte("fixture-key"), nil)), r, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
		if result.Success != tc.valid {
			t.Fatalf("model=%s format=%s result=%+v", tc.model, tc.format, result)
		}
		if result.Success && (w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "complete transcript")) {
			t.Fatal(w.Body.String())
		}
	}
}

func TestNativeSparseResponsesTerminalPreservesCompletedItems(t *testing.T) {
	items := []string{
		`{"id":"reason","type":"reasoning","summary":[],"encrypted_content":"opaque-state"}`,
		`{"id":"message","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}`,
		`{"id":"function-a","type":"function_call","call_id":"call-a","name":"a","arguments":"{}","status":"completed"}`,
		`{"id":"function-b","type":"function_call","call_id":"call-b","name":"b","arguments":"{}","status":"completed"}`,
	}
	fullOutput := "[" + strings.Join(items, ",") + "]"
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses} {
		for _, tc := range []struct {
			name, terminal string
			success        bool
			empty          bool
		}{
			{"id only", `{"id":"response"}`, true, false},
			{"unindexed items", `{"id":"response"}`, true, false},
			{"response id conflict", `{"id":"response"}`, false, false},
			{"usage", `{"id":"response","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`, true, false},
			{"full", `{"id":"response","object":"response","status":"completed","output":` + fullOutput + `}`, true, false},
			{"explicit empty", `{"id":"response","output":[]}`, true, true},
			{"status conflict", `{"id":"response","status":"in_progress"}`, false, false},
			{"identity conflict", `{"id":"other"}`, false, false},
			{"content conflict", `{"id":"response","output":` + strings.Replace(fullOutput, "answer", "changed", 1) + `}`, false, false},
			{"truncated", ``, false, false},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				wire := `data: {"type":"response.created","response":{"id":"response","object":"response","status":"in_progress"}}` + "\n\n"
				for i, item := range items {
					if tc.name == "unindexed items" {
						wire += fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"item\":%s}\n\n", item)
					} else {
						wire += fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":%s}\n\n", i, item)
					}
				}
				if tc.name == "response id conflict" {
					wire += "data: {\"type\":\"response.in_progress\",\"response_id\":\"other\"}\n\n"
				}
				if tc.terminal != "" {
					wire += `data: {"type":"response.completed","response":` + tc.terminal + "}\n\n"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, wire)
				}))
				defer server.Close()
				r := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":"x","stream":true}`)
				w := httptest.NewRecorder()
				result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), w, NewTarget(server.URL, "private", NewCredential([]byte("fixture-key"), nil)), r, kind, contract.AttemptPolicy{})
				if result.Success != tc.success {
					t.Fatalf("result=%+v body=%s", result, w.Body.String())
				}
				if tc.success && (!strings.Contains(w.Body.String(), "opaque-state") || !strings.Contains(w.Body.String(), "call-b")) {
					t.Fatal(w.Body.String())
				}
				if tc.empty && !strings.Contains(w.Body.String(), `"output":[]`) {
					t.Fatal("explicit output was replaced")
				}
				if tc.name == "usage" && (!result.Usage.Present || result.Usage.UncachedInputTokens != 10) {
					t.Fatal(result.Usage)
				}
			})
		}
	}
}

func TestSparseSnapshotsRequireUnambiguousIdentity(t *testing.T) {
	for _, tc := range []struct {
		events []string
		valid  bool
	}{
		{[]string{`{"item":{"id":"a","type":"message"}}`, `{"item":{"id":"b","type":"message"}}`}, true},
		{[]string{`{"item":{"id":"a","type":"message"}}`, `{"item":{"id":"a","type":"message"}}`}, false},
		{[]string{`{"output_index":null,"item":{"id":"a","type":"message"}}`}, false},
		{[]string{`{"output_index":1,"item":{"id":"a","type":"message"}}`}, false},
	} {
		s := responseStreamSnapshot{}
		valid := true
		for _, event := range tc.events {
			var root map[string]json.RawMessage
			_ = json.Unmarshal([]byte(event), &root)
			if !s.observe("response.output_item.done", root) {
				valid = false
				break
			}
		}
		if valid != tc.valid {
			t.Fatal(tc.events, valid)
		}
		s.clear()
	}
	for _, done := range []string{`{"item":{"id":"b","type":"message"}}`, `{"item":{"id":"a","type":"reasoning"}}`} {
		s := responseStreamSnapshot{}
		var added, final map[string]json.RawMessage
		_ = json.Unmarshal([]byte(`{"output_index":0,"item":{"id":"a","type":"message"}}`), &added)
		_ = json.Unmarshal([]byte(done), &final)
		if !s.observe("response.output_item.added", added) || s.observe("response.output_item.done", final) {
			t.Fatal(done)
		}
		s.clear()
	}
}
