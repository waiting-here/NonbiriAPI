package forward

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestTransportAggregatesReasoningWithoutJoiningDistinctBlocks(t *testing.T) {
	upstream := `data: {"choices":[{"index":0,"delta":{"reasoning":"first ","reasoning_content":"canonical ","reasoning_details":[{"type":"reasoning.summary","id":"rs","format":"openai-responses-v1","index":0,"summary":"sum"}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning":"second","reasoning_content":"summary","reasoning_details":[{"type":"reasoning.summary","index":0,"summary":"mary"},{"type":"reasoning.encrypted","id":"encrypted","index":0,"data":"cipher+/="}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","format":"MiniMax-response-v1","text":"legacy "}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","format":"MiniMax-response-v1","text":"thought"}],"thinking_blocks":[{"type":"thinking","index":0,"thinking":"part "}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","id":"next","format":"anthropic-claude-v1","index":0,"text":"signed"}],"thinking_blocks":[{"type":"thinking","index":0,"thinking":"one"}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":null,"signature":"reasoning-sig"}],"thinking_blocks":[{"type":"thinking","index":0,"thinking":"","signature":"thinking-sig"},{"type":"redacted_thinking","index":1,"data":"opaque"}]}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"next unsigned"},{"type":"vendor","text":"keep","data":{"n":9007199254740993}},{"text":"keep this block"},{"text":"and this one"}],"encrypted_content":"qwen+/="}}]}

data: {"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"},{"index":1,"delta":{"reasoning_details":[]},"finish_reason":"stop"}]}

data: [DONE]

`
	result, err := streamToCompletion([]byte(upstream))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"answer","encrypted_content":"qwen+/=","reasoning":"first second","reasoning_content":"canonical summary","reasoning_details":[{"format":"openai-responses-v1","id":"rs","index":0,"summary":"summary","type":"reasoning.summary"},{"data":"cipher+/=","id":"encrypted","index":0,"type":"reasoning.encrypted"},{"format":"MiniMax-response-v1","text":"legacy thought","type":"reasoning.text"},{"format":"anthropic-claude-v1","id":"next","index":0,"signature":"reasoning-sig","text":"signed","type":"reasoning.text"},{"index":0,"text":"next unsigned","type":"reasoning.text"},{"data":{"n":9007199254740993},"text":"keep","type":"vendor"},{"text":"keep this block"},{"text":"and this one"}],"role":"assistant","thinking_blocks":[{"index":0,"signature":"thinking-sig","thinking":"part one","type":"thinking"},{"data":"opaque","index":1,"type":"redacted_thinking"}]}},{"finish_reason":"stop","index":1,"message":{"content":null,"reasoning_details":[],"role":"assistant"}}],"object":"chat.completion"}`
	if string(result) != want {
		t.Fatalf("reasoning completion differs:\n%s\nwant:\n%s", result, want)
	}
}

func TestTransportFragmentsTextAndPreservesReasoningCiphertext(t *testing.T) {
	message := map[string]any{
		"role": "assistant", "content": "answer", "reasoning": "provider alias", "reasoning_content": "visible summary",
		"encrypted_content": "qwen+/=",
		"reasoning_details": []any{
			map[string]any{"type": "reasoning.text", "index": 0, "id": "text", "text": strings.Repeat("思考🙂", 30000), "signature": "only-one-signature"},
			map[string]any{"type": "reasoning.encrypted", "index": 0, "id": "encrypted", "data": "whole-only-data"},
			map[string]any{"type": "reasoning.summary", "index": 0, "id": "summary", "summary": strings.Repeat("summary", 40000)},
		},
		"thinking_blocks": []any{
			map[string]any{"type": "thinking", "index": 0, "thinking": strings.Repeat("想一想🙂", 24000), "signature": "thinking-signature"},
			map[string]any{"type": "redacted_thinking", "index": 1, "data": "thinking-redacted"},
		},
	}
	completion := map[string]any{
		"id": "chat-id", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
	}
	original, err := json.Marshal(completion)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := completionToStream(original, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if len(frame)-2 > openai.DefaultMaxSSELineBytes {
			t.Fatal("generated an oversized SSE line")
		}
	}
	stream := bytes.Join(frames, nil)
	if bytes.Count(stream, []byte(`"signature":"only-one-signature"`)) != 1 || bytes.Count(stream, []byte(`"data":"whole-only-data"`)) != 1 {
		t.Fatal("opaque reasoning fields were split or repeated")
	}
	if bytes.Count(stream, []byte(`"signature":"thinking-signature","thinking":""`)) != 1 || bytes.Count(stream, []byte(`"data":"thinking-redacted"`)) != 1 {
		t.Fatal("thinking signature or redacted block changed")
	}
	before, after, found := bytes.Cut(stream, []byte(`"delta":{"encrypted_content":"qwen+/="}`))
	if !found || !bytes.Contains(before, []byte(`"reasoning_content":"visible summary"`)) || !bytes.Contains(after, []byte(`"content":"answer"`)) {
		t.Fatal("ciphertext must follow reasoning and precede ordinary output")
	}
	restored, err := streamToCompletion(stream)
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatalf("round trip changed message or failed: %v", err)
	}
}

func TestTransportFragmentsReasoningMirrorsTogether(t *testing.T) {
	text := strings.Repeat("镜像🙂", 30000)
	for _, tc := range []struct {
		name            string
		alias           string
		details, blocks bool
	}{
		{name: "scalar alias", alias: text},
		{name: "signed details", details: true},
		{name: "all matching shapes", alias: text, details: true, blocks: true},
		{name: "different alias", alias: "independent provider text", blocks: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := map[string]any{"role": "assistant", "content": "answer", "reasoning_content": text}
			if tc.alias != "" {
				message["reasoning"] = tc.alias
			}
			if tc.details {
				message["reasoning_details"] = []any{
					map[string]any{"type": "reasoning.encrypted", "index": 0, "data": "opaque-before"},
					map[string]any{"type": "reasoning.text", "index": 1, "id": "r", "text": text, "signature": "unique-signature"},
					map[string]any{"type": "reasoning.encrypted", "index": 2, "data": "opaque-after"},
				}
			}
			if tc.blocks {
				message["thinking_blocks"] = []any{map[string]any{"type": "thinking", "index": 0, "thinking": text, "signature": "block-signature"}}
			}
			raw, err := json.Marshal(map[string]any{
				"id": "chat", "created": 1, "object": "chat.completion", "model": "m",
				"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": message}},
			})
			if err != nil {
				t.Fatal(err)
			}
			frames, err := completionToStream(raw, false)
			if err != nil {
				t.Fatal(err)
			}
			var visible strings.Builder
			synchronized := true
			for _, frame := range frames[:len(frames)-1] {
				var event struct {
					Choices []struct {
						Delta struct {
							Reasoning string
							Canonical string                      `json:"reasoning_content"`
							Details   []struct{ Text string }     `json:"reasoning_details"`
							Blocks    []struct{ Thinking string } `json:"thinking_blocks"`
						}
					}
				}
				if err := json.Unmarshal(bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: "))), &event); err != nil {
					t.Fatal(err)
				}
				if len(event.Choices) == 0 {
					continue
				}
				delta := event.Choices[0].Delta
				if delta.Canonical != "" {
					if tc.alias == text && delta.Reasoning != delta.Canonical || tc.alias != text && delta.Reasoning != "" {
						synchronized = false
					}
					if tc.details && (len(delta.Details) != 1 || delta.Details[0].Text != delta.Canonical) {
						synchronized = false
					}
					if tc.blocks && (len(delta.Blocks) != 1 || delta.Blocks[0].Thinking != delta.Canonical) {
						synchronized = false
					}
					visible.WriteString(delta.Canonical)
					continue
				}
				if delta.Reasoning != "" && tc.alias == text {
					visible.WriteString(delta.Reasoning)
				}
				for _, part := range delta.Details {
					visible.WriteString(part.Text)
				}
				for _, part := range delta.Blocks {
					visible.WriteString(part.Thinking)
				}
			}
			if visible.String() != text {
				t.Fatalf("fallback client read %d bytes of reasoning, want %d", visible.Len(), len(text))
			}
			if !synchronized {
				t.Fatal("reasoning fields only share a frame when their text matches exactly")
			}
			stream := bytes.Join(frames, nil)
			if tc.details && (bytes.Count(stream, []byte("unique-signature")) != 1 || bytes.Count(stream, []byte("opaque-before")) != 1 || bytes.Count(stream, []byte("opaque-after")) != 1) {
				t.Fatal("reasoning continuation data was repeated or lost")
			}
			restored, err := streamToCompletion(stream)
			if err != nil || !bytes.Equal(restored, raw) {
				t.Fatalf("round trip changed one or more reasoning fields: %v", err)
			}
		})
	}
}

func TestTransportRejectsUnrepresentableReasoningSnapshots(t *testing.T) {
	for _, field := range []string{"encrypted_content", "reasoning_details", "thinking_blocks"} {
		t.Run(field, func(t *testing.T) {
			opaque := strings.Repeat("x", openai.DefaultMaxSSELineBytes)
			var value any = opaque
			if field != "encrypted_content" {
				value = []any{map[string]any{"type": "vendor", "data": opaque}}
			}
			root, err := readWireObject(transportLimitCompletion(t, "answer"))
			if err != nil {
				t.Fatal(err)
			}
			root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)[field] = value
			raw, _ := json.Marshal(root)
			frames, err := completionToStream(raw, false)
			if !errors.Is(err, errTransportResponse) || len(frames) != 0 {
				t.Fatalf("converted opaque field across frame limit: %v", err)
			}
		})
	}
	conflict := "data: {\"choices\":[{\"index\":0,\"delta\":{\"encrypted_content\":\"one\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"encrypted_content\":\"two\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	if _, err := streamToCompletion([]byte(conflict)); !errors.Is(err, errTransportResponse) {
		t.Fatalf("converted conflicting complete ciphertext: %v", err)
	}
}
