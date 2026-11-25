package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeReasoningProjectionRespectsGuardAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, message, secret, diagnostic string
		inputSizeLimit                    bool
	}{
		{"projected-secret", `{"reasoning_details":[{"type":"reasoning.summary","summary":"split-"},{"type":"reasoning.text","text":"key"}]}`, "split-key", "rejected", false},
		{"projected-size", `{"reasoning":"` + strings.Repeat("r", 1024) + `"}`, "mock-secret", "limit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(validCompletion, `{"role":"assistant","content":"ok"}`, tc.message, 1)
			response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(raw))}
			sink := httptest.NewRecorder()
			adapter := Adapter{maxJSONResponseBytes: DefaultMaxJSONResponseBytes}
			if tc.inputSizeLimit {
				adapter.maxJSONResponseBytes = int64(len(raw))
			}
			guard := newResponseGuard([]byte(tc.secret))
			defer guard.Clear()
			result := adapter.nonStream(context.Background(), sink, response, guard)
			if result.Success || result.Committed || sink.Body.Len() != 0 || !strings.Contains(result.Diagnostic, tc.diagnostic) {
				t.Fatalf("result=%+v, wire=%s", result, sink.Body)
			}
		})
	}
}

func TestNativeChatReasoningFields(t *testing.T) {
	const signed = `{"type":"reasoning.text","text":"step","signature":"sig","index":1}`
	const summary = `{"type":"reasoning.summary","summary":"overview: ","format":"openai-responses-v1","index":0}`
	const encrypted = `{"type":"reasoning.encrypted","data":"cipher","id":"rs_1","index":2}`
	for _, tc := range []struct {
		name, message, reasoning, content string
		changed                           bool
	}{
		{"scalar", `{"content":"answer","reasoning":" private "}`, `" private "`, `"answer"`, true},
		{"canonical", `{"content":"answer","reasoning":"legacy","reasoning_content":"canonical"}`, `"canonical"`, `"answer"`, false},
		{"canonical-unknown", `{"reasoning_content":{"opaque":true},"reasoning":"legacy"}`, `{"opaque":true}`, "", false},
		{"scalar-null", `{"content":"answer","reasoning":null}`, "", `"answer"`, false},
		{"typed-details", `{"reasoning":"scalar-mirror","reasoning_details":[` + summary + "," + encrypted + "," + signed + `],"content":null}`, `"overview: step"`, "null", true},
		{"minimax", `{"reasoning_details":[{"type":"reasoning.text","text":"idea","format":"MiniMax-response-v1","index":0}],"content":"answer"}`, `"idea"`, `"answer"`, true},
		{"summary-only", `{"reasoning_details":[` + summary + `]}`, `"overview: "`, "", true},
		{"absent-details", `{"reasoning_details":[],"thinking_blocks":null,"reasoning":"plain","reasoning_content":null}`, `"plain"`, "", true},
		{"signed-thinking-sidecar", `{"thinking_blocks":[{"type":"thinking","thinking":"思","signature":"sig"},{"type":"redacted_thinking","data":"hidden"}]}`, `"思"`, "", true},
		{"encrypted-mirror", `{"reasoning_details":[` + encrypted + `],"reasoning":"[REDACTED]"}`, "", "", false},
		{"late-signature", `{"reasoning_details":[{"type":"reasoning.text","signature":"opaque"}],"reasoning":"prior text"}`, "", "", false},
		{"redacted-sidecar", `{"thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}],"reasoning_content":""}`, `""`, "", false},
		{"unknown-detail", `{"reasoning_details":[{"type":"future","text":"metadata"}]}`, "", "", false},
		{"untyped-detail", `{"reasoning_details":[{"text":"metadata"}]}`, "", "", false},
		{"duplicate-detail-key", `{"reasoning_details":[{"type":"reasoning.encrypted","type":"reasoning.text","text":"metadata"}]}`, "", "", false},
		{"non-string", `{"reasoning":{"foo":"bar"},"reasoning_details":[{"type":"reasoning.text","text":[{"text":"x"}]}]}`, "", "", false},
		{"flat-thinking", `{"content":[{"type":"thinking","thinking":"idea"},{"type":"text","text":"answer"}]}`, `"idea"`, `"answer"`, true},
		{"flat-signed", `{"content":[{"type":"thinking","thinking":"idea","signature":"sig"}]}`, "", `[{"type":"thinking","thinking":"idea","signature":"sig"}]`, false},
		{"flat-conflict", `{"reasoning":"other","content":[{"type":"thinking","thinking":"idea"}]}`, `"other"`, `[{"type":"thinking","thinking":"idea"}]`, true},
		{"tagged-prose", `{"content":"<think>quoted input</think>"}`, "", `"<think>quoted input</think>"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(validCompletion, `{"role":"assistant","content":"ok"}`, tc.message, 1)
			usage, projected, err := validateCompletion([]byte(raw))
			if err != nil || (projected != nil) != tc.changed || !usage.Present || usage.OutputTokens != 3 {
				t.Fatalf("changed=%t usage=%+v error=%v", projected != nil, usage, err)
			}
			if projected == nil {
				projected = []byte(raw)
			}
			var completion struct {
				Choices []struct{ Message map[string]json.RawMessage }
			}
			if err := json.Unmarshal(projected, &completion); err != nil || len(completion.Choices) != 1 {
				t.Fatalf("completion=%s error=%v", projected, err)
			}
			msg := completion.Choices[0].Message
			if string(msg["reasoning_content"]) != tc.reasoning || string(msg["content"]) != tc.content {
				t.Fatalf("reasoning=%s content=%s", msg["reasoning_content"], msg["content"])
			}
			var source map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.message), &source); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"reasoning", "reasoning_details", "thinking_blocks"} {
				if string(msg[key]) != string(source[key]) {
					t.Fatalf("%s changed to %s", key, msg[key])
				}
			}
		})
	}
}

func TestNativeReasoningStreamPreservesDeltasAndLateSignatures(t *testing.T) {
	deltas := []string{
		`{"role":"assistant","reasoning":"mirror","reasoning_details":[{"type":"reasoning.summary","summary":"first ","index":0}]}`,
		`{"reasoning":"mirror","reasoning_details":[{"type":"reasoning.text","text":"step ","index":0}]}`,
		`{"reasoning":"step "}`,
		`{"thinking_blocks":[{"type":"thinking","thinking":"last","index":1}],"reasoning_content":"last"}`,
		`{"content":"answer"}`,
		`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"sig","index":1}]}`,
		`{"reasoning":"old text","reasoning_details":[{"type":"reasoning.text","signature":"sig","index":0}]}`,
		`{"reasoning_details":[{"type":"reasoning.encrypted","data":"cipher","index":0}]}`,
	}
	frames := make([]string, 0, len(deltas)+1)
	for i, delta := range deltas {
		finish := "null"
		if i == len(deltas)-1 {
			finish = `"stop"`
		}
		frames = append(frames, "data: "+responseIdentityChunk("id", delta, finish)+"\n\n")
	}
	frames = append(frames, "data: [DONE]\n\n")
	for _, flatten := range []bool{false, true} {
		result, wire := responseIdentityAttempt(t, frames, flatten, nil, "mock-credential")
		if !result.Success || !strings.HasSuffix(wire, "data: [DONE]\n\n") {
			t.Fatalf("flatten=%t result=%+v", flatten, result)
		}
		var visible, reasoning strings.Builder
		for _, payload := range strings.Split(wire, "\n\n") {
			if !strings.HasPrefix(payload, "data: {") {
				continue
			}
			var message struct {
				Choices []struct {
					Delta struct {
						Content   string
						Reasoning string `json:"reasoning_content"`
					}
				}
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(payload, "data: ")), &message); err != nil || len(message.Choices) != 1 {
				t.Fatal(payload)
			}
			visible.WriteString(message.Choices[0].Delta.Content)
			reasoning.WriteString(message.Choices[0].Delta.Reasoning)
		}
		if visible.String() != "answer" || reasoning.String() != "first step step last" ||
			!strings.Contains(wire, `"signature":"sig"`) || !strings.Contains(wire, `"data":"cipher"`) {
			t.Fatalf("flatten=%t wire=%s", flatten, wire)
		}
	}
}
