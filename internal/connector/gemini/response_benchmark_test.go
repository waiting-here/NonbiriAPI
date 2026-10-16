package gemini

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkGeminiSmallChunks(b *testing.B) {
	for _, chunks := range []int{256, 2048} {
		b.Run(fmt.Sprint(chunks), func(b *testing.B) {
			part := []byte(`{"candidates":[{"content":{"parts":[{"text":"` + strings.Repeat("x", 64) + `"}]}}]}`)
			end := []byte(`{"candidates":[{"finishReason":"STOP"}]}`)
			b.ReportAllocs()
			b.SetBytes(int64(chunks * 64))
			for b.Loop() {
				d := NewDecoder("public", time.Unix(1, 0), false)
				for range chunks {
					if _, err := d.Event("", part); err != nil {
						b.Fatal(err)
					}
				}
				if _, err := d.Event("", end); err != nil {
					b.Fatal(err)
				}
				if _, err := d.Finish(); err != nil {
					b.Fatal(err)
				}
				d.Clear()
			}
		})
	}
}

func TestGeminiManyChunksPreserveThoughtAndText(t *testing.T) {
	d := NewDecoder("public", time.Unix(1, 0), false).(*decoder)
	defer d.Clear()
	const chunks = 1024
	for _, thought := range []bool{true, false} {
		for range chunks {
			part := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":"段","thought":%t}]}}]}`, thought)
			if _, err := d.Event("", []byte(part)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := d.Event("", []byte(`{"candidates":[{"finishReason":"STOP"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Finish(); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("段", chunks)
	if d.text.String() != want || d.thought.String() != want {
		t.Fatal("chunk text changed")
	}
}
