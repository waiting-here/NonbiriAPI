package responses

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The terminal repeats the entire message, exercising the retained consistency
// checks as well as incremental argument/text assembly.
func manyTextEvents(chunks int) [][]byte {
	part := strings.Repeat("x", 64)
	full := strings.Repeat(part, chunks)
	events := []string{
		`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"m","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"item_id":"m","content_index":0,"part":{"type":"output_text","text":""}}`,
	}
	for range chunks {
		events = append(events, `{"type":"response.output_text.delta","output_index":0,"item_id":"m","content_index":0,"delta":"`+part+`"}`)
	}
	item := `{"id":"m","type":"message","role":"assistant","content":[{"type":"output_text","text":"` + full + `"}]}`
	events = append(events,
		`{"type":"response.output_text.done","output_index":0,"item_id":"m","content_index":0,"text":"`+full+`"}`,
		`{"type":"response.content_part.done","output_index":0,"item_id":"m","content_index":0,"part":{"type":"output_text","text":"`+full+`"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[`+item+`]}}`,
	)
	out := make([][]byte, len(events))
	for i, event := range events {
		out[i] = []byte(fmt.Sprintf(`{"sequence_number":%d,`, i) + event[1:])
	}
	return out
}
func BenchmarkResponsesSmallChunks(b *testing.B) {
	for _, chunks := range []int{256, 2048} {
		b.Run(fmt.Sprint(chunks), func(b *testing.B) {
			events := manyTextEvents(chunks)
			b.ReportAllocs()
			b.SetBytes(int64(chunks * 64))
			for b.Loop() {
				d := NewDecoder("public", time.Unix(1, 0), false)
				for _, event := range events {
					if _, err := d.Event("", event); err != nil {
						b.Fatal(err)
					}
				}
				if _, err := d.Finish(); err != nil {
					b.Fatal(err)
				}
				d.Clear()
			}
		})
	}
}
func TestResponsesManyChunksStillValidateTerminalText(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		d := NewDecoder("public", time.Unix(1, 0), false)
		events := manyTextEvents(512)
		for _, event := range events[:len(events)-1] {
			if _, err := d.Event("", event); err != nil {
				t.Fatal(err)
			}
		}
		last := events[len(events)-1]
		if wrong {
			last = []byte(strings.Replace(string(last), strings.Repeat("x", 512*64), "changed", 1))
		}
		_, err := d.Event("", last)
		if (err != nil) != wrong {
			t.Fatalf("wrong=%t: %v", wrong, err)
		}
		if !wrong {
			if _, err := d.Finish(); err != nil {
				t.Fatal(err)
			}
		}
		d.Clear()
	}
}
