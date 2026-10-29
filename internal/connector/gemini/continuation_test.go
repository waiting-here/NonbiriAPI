package gemini

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func completionMessageForReplay(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var result struct {
		Choices []struct {
			Message map[string]any `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &result) != nil || len(result.Choices) != 1 {
		t.Fatal(string(body))
	}
	return result.Choices[0].Message
}

func TestGeneratedGeminiToolIDsAndMissingArgumentsReplayAcrossRounds(t *testing.T) {
	messages := []any{map[string]any{"role": "user", "content": "run tools"}}
	seen := map[string]bool{}
	for range 3 {
		d := NewDecoder("public", time.Unix(123, 0), false)
		out, _, err := d.JSON([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"clock"}},{"functionCall":{"name":"clock","args":{}}}]},"finishReason":"STOP"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		message := completionMessageForReplay(t, out)
		messages = append(messages, message)
		for _, raw := range message["tool_calls"].([]any) {
			call := raw.(map[string]any)
			id := call["id"].(string)
			if seen[id] || call["function"].(map[string]any)["arguments"] != "{}" {
				t.Fatal(call)
			}
			seen[id] = true
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id, "content": "done"})
		}
	}
	body, _ := json.Marshal(map[string]any{"model": "public", "messages": messages})
	if _, err := Compile(request(t, string(body)), "gemini", contract.AttemptPolicy{}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{"null", "[]", `"invalid"`} {
		d := NewDecoder("public", time.Unix(0, 0), false)
		if _, _, err := d.JSON([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"clock","args":` + args + `}}]},"finishReason":"STOP"}]}`)); err == nil {
			t.Fatal("invalid args accepted", args)
		}
	}
}

func TestGeminiSignedPartsRemainInOriginalOrderOnJSONAndStreamReplay(t *testing.T) {
	raw := `{"candidates":[{"content":{"parts":[{"text":"A"},{"text":"B","thoughtSignature":"c2ln"},{"functionCall":{"id":"call","name":"clock"},"thoughtSignature":"dG9vbA=="},{"text":"C","thoughtSignature":"ZGVm"}]},"finishReason":"STOP"}]}`
	for _, stream := range []bool{false, true} {
		d := NewDecoder("public", time.Unix(1, 0), false).(*decoder)
		d.SetContinuationID(strings.Repeat("A", 43))
		var message map[string]any
		if !stream {
			body, _, err := d.JSON([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			message = completionMessageForReplay(t, body)
		} else {
			frames, err := d.Event("", []byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			last, err := d.Finish()
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, last...)
			message = map[string]any{"role": "assistant", "content": ""}
			var calls []any
			for _, frame := range frames {
				var chunk struct {
					Choices []struct {
						Delta map[string]any `json:"delta"`
					} `json:"choices"`
				}
				_ = json.Unmarshal(frame, &chunk)
				for _, choice := range chunk.Choices {
					if text, ok := choice.Delta["content"].(string); ok {
						message["content"] = message["content"].(string) + text
					}
					if extra, ok := choice.Delta["extra_content"]; ok {
						message["extra_content"] = extra
					}
					if toolCalls, ok := choice.Delta["tool_calls"].([]any); ok {
						for _, raw := range toolCalls {
							call := raw.(map[string]any)
							delete(call, "index")
							calls = append(calls, call)
						}
					}
				}
			}
			message["tool_calls"] = calls
		}
		messages := []any{message, map[string]any{"role": "tool", "tool_call_id": "call", "content": "done"}}
		body, _ := json.Marshal(map[string]any{"model": "public", "messages": messages})
		compiled, err := Compile(request(t, string(body)), "gemini", contract.AttemptPolicy{})
		if err != nil {
			t.Fatal(err, string(body))
		}
		var result struct {
			Contents []struct {
				Parts []map[string]json.RawMessage `json:"parts"`
			} `json:"contents"`
		}
		_ = json.Unmarshal(compiled, &result)
		parts := result.Contents[0].Parts
		if len(parts) != 4 || string(parts[0]["text"]) != `"A"` || parts[0]["thoughtSignature"] != nil || string(parts[1]["text"]) != `"B"` || string(parts[1]["thoughtSignature"]) != `"c2ln"` || string(parts[3]["text"]) != `"C"` || string(parts[3]["thoughtSignature"]) != `"ZGVm"` {
			t.Fatal(string(compiled))
		}
		message["content"] = "tampered"
		body, _ = json.Marshal(map[string]any{"model": "public", "messages": messages})
		if _, err := Compile(request(t, string(body)), "gemini", contract.AttemptPolicy{}); err == nil {
			t.Fatal("mismatched signed content accepted")
		}
	}
}

func TestGeminiContinuationRequiresExplicitUnsupportedFailureWithUsage(t *testing.T) {
	for _, signal := range []string{`"finishReason":"CONTINUATION"`, `"continuationToken":"opaque-checkpoint"`} {
		raw := `{"candidates":[{"content":{"parts":[{"text":"partial"}]},` + signal + `}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}`
		for _, stream := range []bool{false, true} {
			d := NewDecoder("public", time.Unix(1, 0), false)
			var err error
			if stream {
				_, err = d.Event("", []byte(raw))
			} else {
				_, _, err = d.JSON([]byte(raw))
			}
			if !errors.Is(err, chatwire.ErrContinuationUnsupported) || !d.Usage().Present || d.Usage().OutputTokens != 2 {
				t.Fatalf("stream=%v err=%v usage=%+v", stream, err, d.Usage())
			}
			if _, err := d.Finish(); err == nil {
				t.Fatal("continuation became successful completion")
			}
		}
	}
}
