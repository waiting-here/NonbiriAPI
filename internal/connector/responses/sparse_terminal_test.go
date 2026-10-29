package responses

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSparseTerminalPreservesEncryptedReasoningAndMultipleTools(t *testing.T) {
	for _, kind := range []string{"response.completed", "response.incomplete"} {
		for _, terminal := range []string{"id", "usage", "empty", "conflict"} {
			events := toolEvents()
			for _, event := range events {
				if event["type"] == "response.output_item.done" {
					delete(event, "output_index")
				}
			}
			last := events[len(events)-1]
			last["type"] = kind
			response := map[string]any{"id": "resp_private"}
			if kind == "response.incomplete" {
				response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
			}
			if terminal == "usage" {
				response["usage"] = json.RawMessage(usageJSON)
			}
			if terminal == "empty" {
				response["output"] = []any{}
			}
			if terminal == "conflict" {
				response["status"] = "failed"
			}
			last["response"] = response
			out, usage, err := feed(t, events)
			if (err == nil) != (terminal != "conflict") {
				t.Fatal(kind, terminal, err, out)
			}
			if terminal == "conflict" {
				continue
			}
			for _, token := range []string{`"encrypted_content":"opaque"`, `"id":"a"`, `"id":"b"`, `"finish_reason":`} {
				if !strings.Contains(out, token) {
					t.Fatal(token, out)
				}
			}
			if usage.Present != (terminal == "usage") {
				t.Fatal(terminal, usage)
			}
		}
	}
}

func TestMultipleReasoningItemsEmitOneOpaqueDeltaEachWithStableBinding(t *testing.T) {
	d := NewDecoder("public", time.Unix(1, 0), false).(*decoder)
	d.SetContinuationID(strings.Repeat("A", 43))
	defer d.Clear()
	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "r", "status": "in_progress"}}}
	for i, id := range []string{"one", "two"} {
		events = append(events, map[string]any{"type": "response.output_item.added", "output_index": i, "item": map[string]any{"id": id, "type": "reasoning", "summary": []any{}}}, map[string]any{"type": "response.output_item.done", "output_index": i, "item": map[string]any{"id": id, "type": "reasoning", "summary": []any{}, "encrypted_content": "opaque-" + id, "status": "completed"}})
	}
	events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": "r"}})
	var output strings.Builder
	for i, event := range events {
		event["sequence_number"] = i
		raw, _ := json.Marshal(event)
		frames, err := d.Event(event["type"].(string), raw)
		if err != nil {
			t.Fatal(err, string(raw))
		}
		for _, frame := range frames {
			output.Write(frame)
		}
	}
	frames, err := d.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		output.Write(frame)
	}
	for _, value := range []string{"opaque-one", "opaque-two"} {
		if strings.Count(output.String(), value) != 1 {
			t.Fatal(output.String())
		}
	}
	if strings.Count(output.String(), `"continuation_id":"`+strings.Repeat("A", 43)+`"`) != 2 {
		t.Fatal(output.String())
	}
}
