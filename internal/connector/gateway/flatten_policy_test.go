package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func flattenJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestFlattenPolicyBidirectionalAndIncompleteStream(t *testing.T) {
	for _, tc := range []struct{ stream, incomplete, mapped bool }{{false, false, false}, {true, false, false}, {true, true, false}, {false, false, true}} {
		role := "assistant"
		if tc.mapped {
			role = "developer"
		}
		history := "<mx_tool name=\"lookup\" id=\"call_1\">\n{}\n</mx_tool>\n<mx_tool_result id=\"call_1\">\nanswer\n</mx_tool_result>"
		r := chat(t, flattenJSON(t, map[string]any{"model": "public/model", "stream": tc.stream,
			"messages": []any{map[string]any{"role": role, "content": history}},
			"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}}))
		original := r
		r, restoreErr := original.ReverseFlatten()
		original.Clear()
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if tc.mapped {
			policy := rolepolicy.Default()
			policy.Rules["developer"] = "assistant"
			mapped, err := r.ApplyRolePolicy(policy)
			if err != nil {
				t.Fatal(err)
			}
			r.Clear()
			r = mapped
		}
		defer r.Clear()
		raw := flattenJSON(t, map[string]any{"content": []any{map[string]any{"type": "tool-call", "toolCallId": "call_2", "toolName": "lookup", "input": "{}"}}, "finishReason": map[string]any{"unified": "tool-calls"}, "warnings": []any{}})
		kind := "application/json"
		if tc.stream {
			kind = "text/event-stream"
			parts := []string{
				flattenJSON(t, map[string]any{"type": "stream-start", "warnings": []any{}}),
				flattenJSON(t, map[string]any{"type": "tool-input-start", "id": "call_2", "toolName": "lookup"}),
				flattenJSON(t, map[string]any{"type": "tool-input-delta", "id": "call_2", "delta": "{}"}),
				flattenJSON(t, map[string]any{"type": "tool-input-end", "id": "call_2"}),
				flattenJSON(t, map[string]any{"type": "tool-call", "toolCallId": "call_2", "toolName": "lookup", "input": "{}"}),
			}
			if !tc.incomplete {
				parts = append(parts, flattenJSON(t, map[string]any{"type": "finish", "finishReason": map[string]any{"unified": "tool-calls"}}))
			}
			raw = sse(parts...)
		}
		b := &fakeBackend{do: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer clear(body)
			if tc.mapped {
				if strings.Contains(string(body), "tool-call") || !strings.Contains(string(body), "mx_tool") {
					t.Fatalf("mapped text was reinterpreted as tools: %s", body)
				}
			} else if !strings.Contains(string(body), "tool-call") || !strings.Contains(string(body), "tool-result") || strings.Contains(string(body), "mx_tool") {
				t.Fatalf("history not restored: %s", body)
			}
			return response(raw, kind), nil
		}}
		a, err := NewAdapter(b)
		if err != nil {
			t.Fatal(err)
		}
		out := httptest.NewRecorder()
		result := a.AttemptWithPolicy(context.Background(), out, target(), secret(), r, nil, "", contract.AttemptPolicy{FlattenToolCalls: true})
		if result.Success == tc.incomplete {
			t.Fatalf("facts=%+v output=%s", result, out.Body)
		}
		if tc.incomplete {
			if strings.Contains(out.Body.String(), "mx_tool") || strings.Contains(out.Body.String(), "[DONE]") || result.StreakDisposition != contract.StreakUpstreamFailure {
				t.Fatalf("incomplete facts=%+v output=%s", result, out.Body)
			}
		} else if !strings.Contains(out.Body.String(), "mx_tool") || strings.Contains(out.Body.String(), "tool_calls") {
			t.Fatalf("facts=%+v output=%s", result, out.Body)
		}
	}
}
