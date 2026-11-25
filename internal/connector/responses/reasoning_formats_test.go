package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const signedReasoning = `{"type":"reasoning","id":"r1","summary":[],"content":[{"type":"reasoning_text","text":"step 1 "},{"type":"reasoning_text","text":"step 2"}],"signature":"opaque-signature","encrypted_content":"ciphertext","status":null}`
const messageSnapshot = `{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]},{"type":"refusal","refusal":"refusal text"}]}`

func TestTypedReasoningProjectionAndReplay(t *testing.T) {
	for _, summary := range []string{"", "short summary"} {
		item := signedReasoning
		want := "step 1 step 2"
		if summary != "" {
			item = strings.Replace(item, `"summary":[]`, `"summary":[{"type":"summary_text","text":"short summary"}]`, 1)
			want = summary
		}
		d := NewDecoder("public", time.Unix(1, 0), true).(*decoder)
		d.SetContinuationID(strings.Repeat("A", 43))
		reply, _, err := d.JSON([]byte(`{"status":"completed","output":[` + item + "," + messageSnapshot + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Choices []struct{ Message json.RawMessage }
		}
		if err := json.Unmarshal(reply, &response); err != nil || len(response.Choices) != 1 {
			t.Fatal(string(reply), err)
		}
		msg := response.Choices[0].Message
		var visible map[string]json.RawMessage
		_ = json.Unmarshal(msg, &visible)
		equalJSON(t, visible["reasoning_content"], fmt.Sprintf("%q", want))
		equalJSON(t, visible["content"], `"answer"`)
		equalJSON(t, visible["refusal"], `"refusal text"`)
		if !strings.Contains(string(visible["extra_content"]), `"continuation_id"`) {
			t.Fatal("missing source binding")
		}
		next := request(t, `{"model":"public","messages":[{"role":"user","content":"question"},`+string(msg)+`,{"role":"user","content":"continue"}]}`)
		body, err := Compile(next, "another-name", contract.AttemptPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		var forwarded struct{ Input []json.RawMessage }
		if json.Unmarshal(body, &forwarded) != nil || len(forwarded.Input) != 4 {
			t.Fatalf("unexpected native history %s", body)
		}
		equalJSON(t, forwarded.Input[1], item)
	}
	for _, item := range []string{
		strings.Replace(signedReasoning, "reasoning_text", "output_text", 1),
		strings.Replace(signedReasoning, `"opaque-signature"`, `{"text":"not displayable"}`, 1),
		strings.Replace(signedReasoning, `"summary":[]`, `"summary":null`, 1),
	} {
		if _, _, err := reasoningItem([]byte(item)); err == nil {
			t.Fatal("accepted invalid reasoning item", item)
		}
	}
}

func numberedFeed(t *testing.T, events ...map[string]any) (string, contract.Usage, error) {
	t.Helper()
	for i, event := range events {
		event["sequence_number"] = i
	}
	return feed(t, events)
}

func TestSnapshotOnlyResponsesKeepOutputAndUsageOnce(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(signedReasoning), json.RawMessage(messageSnapshot), json.RawMessage(callAJSON), json.RawMessage(callBJSON)}
	for _, mode := range []string{"terminal", "indexed done", "unindexed done"} {
		t.Run(mode, func(t *testing.T) {
			events := toolEvents()[:1]
			if mode != "terminal" {
				for i, item := range items {
					event := map[string]any{"type": "response.output_item.done", "item": item}
					if mode == "indexed done" {
						event["output_index"] = i
					}
					events = append(events, event)
				}
			}
			final := map[string]any{"id": "resp_private", "status": "completed", "output": items, "usage": json.RawMessage(usageJSON)}
			out, usage, err := numberedFeed(t, append(events, map[string]any{"type": "response.completed", "response": final})...)
			if err != nil || usage.OutputTokens != 9 {
				t.Fatal(usage, err)
			}
			for _, want := range []string{`"reasoning_content":"step 1 step 2"`, `"content":"answer"`, `"refusal":"refusal text"`, `"encrypted_content":"ciphertext"`, `"finish_reason":"tool_calls"`, `"completion_tokens":9`, `"role":"assistant"`, `"id":"a","index":0`, `"id":"b","index":1`} {
				if strings.Count(out, want) != 1 {
					t.Fatalf("want exactly once %s in %s", want, out)
				}
			}
		})
	}
}

func TestReasoningStreamWrappersAndSummary(t *testing.T) {
	for _, wrapper := range []string{"", "response.content_part", "response.reasoning_part"} {
		for _, withSummary := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/summary=%t", wrapper, withSummary), func(t *testing.T) {
				events := toolEvents()[:1]
				record := func(typ string, fields map[string]any) {
					fields["type"] = typ
					fields["output_index"] = 0
					events = append(events, fields)
				}
				record("response.output_item.added", map[string]any{"item": json.RawMessage(`{"type":"reasoning","id":"r1","summary":[],"content":[]}`)})
				part := func(typ, value string) map[string]any { return map[string]any{"type": typ, "text": value} }
				for i, value := range []string{"step 1 ", "step 2"} {
					if wrapper != "" {
						record(wrapper+".added", map[string]any{"item_id": "r1", "content_index": i, "part": part("reasoning_text", "")})
					}
					record("response.reasoning_text.delta", map[string]any{"item_id": "r1", "content_index": i, "delta": value})
					record("response.reasoning_text.done", map[string]any{"item_id": "r1", "content_index": i, "text": value})
					if wrapper != "" {
						record(wrapper+".done", map[string]any{"item_id": "r1", "content_index": i, "part": part("reasoning_text", value)})
					}
				}
				item, want := signedReasoning, "step 1 step 2"
				if withSummary {
					want = "summary"
					record("response.reasoning_summary_part.added", map[string]any{"item_id": "r1", "summary_index": 0, "part": part("summary_text", "")})
					if wrapper != "" { // Also cover the first readable text arriving in a done event.
						record("response.reasoning_summary_text.delta", map[string]any{"item_id": "r1", "summary_index": 0, "delta": want})
					}
					record("response.reasoning_summary_text.done", map[string]any{"item_id": "r1", "summary_index": 0, "text": want})
					record("response.reasoning_summary_part.done", map[string]any{"item_id": "r1", "summary_index": 0, "part": part("summary_text", want)})
					item = strings.Replace(item, `"summary":[]`, `"summary":[{"type":"summary_text","text":"summary"}]`, 1)
				}
				record("response.output_item.done", map[string]any{"item": json.RawMessage(item)})
				final := map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_private", "output": []any{}}}
				out, _, err := numberedFeed(t, append(events, final)...)
				if err != nil || strings.Count(out, `"reasoning_content":`) != 1 || !strings.Contains(out, fmt.Sprintf(`"reasoning_content":%q`, want)) || !strings.Contains(out, `"signature":"opaque-signature"`) {
					t.Fatal(out, err)
				}
				events[len(events)-1]["item"] = json.RawMessage(strings.Replace(item, "step 2", "different", 1))
				if _, _, err := numberedFeed(t, append(events, final)...); err == nil {
					t.Fatal("accepted contradictory last reasoning part")
				}
			})
		}
	}
}

func TestBoundReplayLimitIncludesSparseItemsAndBinding(t *testing.T) {
	d := NewDecoder("m", time.Unix(1, 0), false).(*decoder)
	events := toolEvents()[:1]
	for i := 0; i < 17; i++ {
		item := map[string]any{"id": fmt.Sprintf("r%d", i), "type": "reasoning", "summary": []any{}, "encrypted_content": strings.Repeat("a", 64<<10)}
		events = append(events, map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
	}
	events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_private", "output": []any{}}})
	for i, event := range events {
		event["sequence_number"] = i
		raw, _ := json.Marshal(event)
		if _, err := d.Event("", raw); err != nil {
			if i != 16 { // 16 * 64 KiB already leaves no room for the native items and envelope.
				t.Fatalf("unexpected rejection at item event %d: %v", i, err)
			}
			break
		} else if i == len(events)-1 {
			t.Fatal("accepted a sparse response with oversized replay data")
		}
	}
	base := `{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":""}]}`
	extra := `{"openai":{"reasoning":[` + base + `]}}`
	fill := strings.Repeat("a", maxReasoningBytes-len(extra))
	input := []byte(`{"status":"completed","output":[` + strings.Replace(base, `"text":""`, `"text":"`+fill+`"`, 1) + `]}`)
	d = NewDecoder("m", time.Unix(1, 0), false).(*decoder)
	if _, _, err := d.JSON(input); err != nil {
		t.Fatal("unbound extension at exact limit", err)
	}
	d.SetContinuationID(strings.Repeat("A", 43))
	if _, _, err := d.JSON(input); err == nil {
		t.Fatal("accepted a bound extension above the input limit")
	}
}

func TestToolArgumentsFirstSuppliedInDoneSnapshot(t *testing.T) {
	for _, argsDone := range []bool{false, true} {
		source := toolEvents()
		// Use the first call, preserving its added event and omitting argument deltas.
		events := append(source[:4:4], source[7])
		if argsDone {
			events = append(source[:4:4], source[6], source[7])
		}
		events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_private"}})
		out, _, err := numberedFeed(t, events...)
		if err != nil || strings.Count(out, `"arguments":"{\"key\":1}"`) != 1 || strings.Count(out, `"id":"a"`) != 1 {
			t.Fatal(out, err)
		}
	}
}

// Ollama's Responses wire sends summary_text events directly, then repeats
// the full summary and an opaque continuation in output_item.done.
func TestSummaryDeltasWithoutPartWrappersReachChatImmediately(t *testing.T) {
	d := NewDecoder("unbranded", time.Unix(1, 0), false)
	defer d.Clear()
	const final = `{"id":"r","type":"reasoning","summary":[{"type":"summary_text","text":"first second"}],"encrypted_content":"first second"}`
	events := []string{
		`{"type":"response.created","response":{"id":"resp","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"r","type":"reasoning","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"r","summary_index":0,"delta":"first "}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"r","summary_index":0,"delta":"second"}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"item_id":"r","summary_index":0,"text":"first second"}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + final + `}`,
		`{"type":"response.completed","response":{"id":"resp","output":[` + final + `]}}`,
	}
	for i, event := range events {
		raw := []byte(fmt.Sprintf(`{"sequence_number":%d,`, i) + event[1:])
		frames, err := d.Event("", raw)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		switch i {
		case 2, 3:
			if len(frames) != 1 || !strings.Contains(string(frames[0]), `"reasoning_content":`) {
				t.Fatalf("summary delta %d was delayed", i)
			}
		case 5:
			if len(frames) != 1 || !strings.Contains(string(frames[0]), `"encrypted_content"`) || strings.Contains(string(frames[0]), `"reasoning_content"`) {
				t.Fatalf("unexpected completed item %s", frames)
			}
		default:
			if len(frames) != 0 {
				t.Fatalf("duplicate output on event %d", i)
			}
		}
	}
	if _, err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}
