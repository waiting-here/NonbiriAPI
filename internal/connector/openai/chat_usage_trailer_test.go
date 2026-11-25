package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const trailerUsage = `{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18,"cost":0.005}`
const trailerDelta = `{"content":"","role":"assistant"}`

func usageTrailerFrame(delta, finish, native, usage, extra string) string {
	wire := fmt.Sprintf(`data: {"id":"c1","model":"upstream","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":%s,"finish_reason":%s,"native_finish_reason":%s}]`, delta, finish, native)
	if usage != "" {
		wire += `,"usage":` + usage
	}
	return wire + extra + "}\n\n"
}

func TestChatUsageTrailerNativeAndToolStreams(t *testing.T) {
	for _, tc := range []struct {
		name             string
		flatten, tools   bool
		first, tail      string
		extra            string
		publicUsageCount int
	}{
		{"native", false, false, "", trailerUsage, "", 1},
		{"flatten_text", true, false, "", trailerUsage, "", 1},
		{"flatten_tools", true, true, "", trailerUsage, "", 1},
		{"combined_only", false, false, trailerUsage, "", "", 1},
		{"identical_combined_and_tail", false, false, trailerUsage, trailerUsage, "", 1},
		{"identical_flattened_tools", true, true, trailerUsage, trailerUsage, "", 1},
		{"new_usage_bucket", false, false, trailerUsage, strings.Replace(trailerUsage, "0.005", "0.01", 1), "", 2},
		{"late_metadata", false, false, trailerUsage, trailerUsage, `,"service_tier":"flex"`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delta, reason := `{"role":"assistant","content":"hello"}`, `"stop"`
			if tc.tools {
				delta, reason = `{"role":"assistant","tool_calls":[{"index":0,"type":"function","id":"call","function":{"name":"lookup","arguments":"{}"}}]}`, `"tool_calls"`
			}
			frames := []string{usageTrailerFrame(delta, "null", "null", "", ""), usageTrailerFrame(trailerDelta, reason, reason, tc.first, "")}
			if tc.tail != "" {
				frames = append(frames, usageTrailerFrame(trailerDelta, reason, reason, tc.tail, tc.extra))
			}
			frames = append(frames, "data: [DONE]\n\n")
			result, body := responseIdentityAttempt(t, frames, tc.flatten, nil, "fixture-usage-key")
			if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 7 || result.Usage.OutputTokens != 11 {
				t.Fatalf("result=%+v body=%s", result, body)
			}
			if strings.Count(body, `"finish_reason":"stop"`) != 1 || strings.Count(body, `"prompt_tokens":7`) != tc.publicUsageCount || !strings.HasSuffix(body, "data: [DONE]\n\n") {
				t.Fatal(body)
			}
			if tc.tools && (!strings.Contains(body, "mx_tool") || strings.Contains(body, `"tool_calls":`)) || tc.extra != "" && !strings.Contains(body, `"service_tier":"flex"`) || tc.name == "new_usage_bucket" && !strings.Contains(body, `"cost":0.01`) {
				t.Fatal(body)
			}
			if tc.first == "" && !strings.Contains(body, `"choices":[]`) {
				t.Fatal("usage did not get its own frame", body)
			}
		})
	}
}

func TestResponsesChatRepeatedFinishUsageTrailer(t *testing.T) {
	finish := usageTrailerFrame(`{"content":"hello"}`, `"stop"`, `"end_turn"`, "", "")
	tail := usageTrailerFrame(trailerDelta, `"stop"`, `"end_turn"`, trailerUsage, "")
	for _, tc := range []struct {
		name, tail string
		accepted   bool
	}{
		{"documented", tail, true},
		{"different choice", strings.Replace(tail, `"index":0`, `"index":1`, 1), false},
		{"different reason", strings.Replace(tail, `"stop"`, `"length"`, 1), false},
		{"different native reason", strings.Replace(tail, `"end_turn"`, `"max_tokens"`, 1), false},
		{"late answer", strings.Replace(tail, trailerDelta, `{"content":"more"}`, 1), false},
		{"late tools", strings.Replace(tail, trailerDelta, `{"tool_calls":[]}`, 1), false},
		{"late reasoning", strings.Replace(tail, trailerDelta, `{"reasoning_content":"more"}`, 1), false},
		{"unknown choice metadata", strings.Replace(tail, `"index":0`, `"index":0,"vendor":"new"`, 1), false},
		{"missing usage", usageTrailerFrame(trailerDelta, `"stop"`, `"end_turn"`, "", ""), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSSE(w, finish, tc.tail, "data: [DONE]\n\n")
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public","input":"hi","stream":true}`)
			defer request.Clear()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "upstream", NewCredential([]byte("fixture-usage-key"), nil)), request, contract.TypeOpenAIChatCompletions, contract.AttemptPolicy{})
			if result.Success != tc.accepted || (!tc.accepted && strings.Contains(writer.Body.String(), "event: response.completed")) {
				t.Fatalf("result=%+v body=%s", result, writer.Body)
			}
			if tc.accepted {
				events := projectionEvents(t, writer.Body.String())
				var response struct {
					Usage struct {
						Input  int `json:"input_tokens"`
						Output int `json:"output_tokens"`
					}
				}
				last := events[len(events)-1]
				if string(last["type"]) != `"response.completed"` || json.Unmarshal(last["response"], &response) != nil || response.Usage.Input != 7 || response.Usage.Output != 11 || !result.Usage.Present || result.Usage.UncachedInputTokens != 7 {
					t.Fatal(writer.Body)
				}
			}
		})
	}
}

func TestChatUsageTrailerLeavesOtherNativeChoicesIntact(t *testing.T) {
	finish := usageTrailerFrame(trailerDelta, `"stop"`, `"end_turn"`, "", "")
	for _, tc := range []struct{ delta, native, field string }{
		{`{"content":"more"}`, `"end_turn"`, `"content":"more"`},
		{`{"tool_calls":[{"index":0,"id":"late"}]}`, `"end_turn"`, `"id":"late"`},
		{trailerDelta, `"different"`, `"native_finish_reason":"different"`},
	} {
		frames := []string{finish, usageTrailerFrame(tc.delta, `"stop"`, tc.native, trailerUsage, ""), "data: [DONE]\n\n"}
		result, body := responseIdentityAttempt(t, frames, false, nil, "fixture-usage-key")
		if !result.Success || !strings.Contains(body, tc.field) || strings.Contains(body, `"choices":[]`) {
			t.Fatal(result, body)
		}
	}
}
