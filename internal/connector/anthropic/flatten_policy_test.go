package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func flattenJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestFlattenPolicyBidirectionalAndIncompleteStream(t *testing.T) {
	for _, tc := range []struct{ stream, incomplete, mapped bool }{{false, false, false}, {true, false, false}, {true, true, false}, {false, false, true}} {
		role := "assistant"
		if tc.mapped {
			role = "developer"
		}
		history := "<mx_tool name=\"lookup\" id=\"call_1\">\n{}\n</mx_tool>\n<mx_tool_result id=\"call_1\">\nanswer\n</mx_tool_result>"
		raw := flattenJSON(t, map[string]any{"model": "public/model", "max_tokens": 128, "stream": tc.stream,
			"messages": []any{map[string]any{"role": role, "content": history}},
			"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}})
		request := mustChatRequest(t, raw)
		original := request
		request, restoreErr := original.ReverseFlatten()
		original.Clear()
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if tc.mapped {
			policy := rolepolicy.Default()
			policy.Rules["developer"] = "assistant"
			mapped, err := request.ApplyRolePolicy(policy)
			if err != nil {
				t.Fatal(err)
			}
			request.Clear()
			request = mapped
		}
		defer request.Clear()
		responseBody := flattenJSON(t, map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "claude",
			"content":     []any{map[string]any{"type": "tool_use", "id": "call_2", "name": "lookup", "input": map[string]any{}}},
			"stop_reason": "tool_use", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 2}})
		kind := "application/json"
		if tc.stream {
			kind = "text/event-stream"
			responseBody = messageStart("{\"input_tokens\":1,\"output_tokens\":0}") +
				namedEvent("content_block_start", flattenJSON(t, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "call_2", "name": "lookup", "input": map[string]any{}}})) +
				inputJSONDelta("{}") +
				namedEvent("content_block_stop", "{\"type\":\"content_block_stop\",\"index\":0}") +
				messageDelta("tool_use", "{\"output_tokens\":2}")
			if !tc.incomplete {
				responseBody += messageStop()
			}
		}
		client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			defer clear(body)
			if tc.mapped {
				if strings.Contains(string(body), "tool_use") || !strings.Contains(string(body), "mx_tool") {
					t.Fatalf("mapped text was reinterpreted as tools: %s", body)
				}
			} else if !strings.Contains(string(body), "tool_use") || !strings.Contains(string(body), "tool_result") || strings.Contains(string(body), "mx_tool") {
				t.Fatalf("history not restored: %s", body)
			}
			return fakeResponse(200, kind, responseBody), nil
		}}
		a, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
		if err != nil {
			t.Fatal(err)
		}
		out := httptest.NewRecorder()
		result := a.AttemptWithPolicy(context.Background(), out, NewTarget(client.baseURL, "claude", NewCredential([]byte("credential-long"), nil)), request, contract.AttemptPolicy{FlattenToolCalls: true, SafetyIdentifier: "nbu_test"})
		if result.Success == tc.incomplete {
			t.Fatalf("result=%+v output=%s", result, out.Body)
		}
		if tc.incomplete {
			if strings.Contains(out.Body.String(), "mx_tool") || strings.Contains(out.Body.String(), "[DONE]") || result.StreakDisposition != contract.StreakUpstreamFailure {
				t.Fatalf("incomplete output=%s facts=%+v", out.Body, result)
			}
		} else if !strings.Contains(out.Body.String(), "mx_tool") || strings.Contains(out.Body.String(), "tool_calls") || !result.Usage.Present || result.Usage.OutputTokens != 2 {
			t.Fatalf("output=%s facts=%+v", out.Body, result)
		}
	}
}
