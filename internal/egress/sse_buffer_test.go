package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSSELineBufferBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		limit             int
		eof, tooLarge     bool
	}{
		{"empty", "\n", "", 16, false, false},
		{"buffer_end", strings.Repeat("x", 15) + "\n", strings.Repeat("x", 15), 15, false, false},
		{"fragmented_lf", strings.Repeat("x", 16) + "\n", strings.Repeat("x", 16), 16, false, false},
		{"split_crlf", strings.Repeat("x", 15) + "\r\n", strings.Repeat("x", 15), 16, false, false},
		{"cr_counts_toward_limit", strings.Repeat("x", 15) + "\r\n", "", 15, false, true},
		{"short_eof", "last\r", "last", 16, true, false},
		{"fragmented_eof", strings.Repeat("x", 32), strings.Repeat("x", 32), 32, true, false},
		{"fragmented_over_limit", strings.Repeat("x", 33), "", 32, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := bufio.NewReaderSize(strings.NewReader(tc.input), 16)
			line, eof, err := readBoundedLine(reader, tc.limit)
			if tc.tooLarge {
				var limitErr *SSELineTooLargeError
				if !errors.As(err, &limitErr) || limitErr.Limit != tc.limit {
					t.Fatalf("line limit = %v, want %d", err, tc.limit)
				}
				return
			}
			if err != nil || string(line) != tc.want || eof != tc.eof {
				t.Fatalf("line = %q, EOF = %v, error = %v", line, eof, err)
			}
		})
	}
}

// Both consumers must own retained values before the next reader refill.
func TestSSEBufferedConsumersKeepData(t *testing.T) {
	const count = 12
	var input strings.Builder
	input.WriteString("\ufeffid: retained-id\r\n")
	for i := range count {
		fmt.Fprintf(&input, "event: event-%02d\r\ndata: alpha-%02d\r\ndata: beta-%02d\r\n", i, i, i)
		if i != count-1 {
			input.WriteString("\r\n")
		}
	}
	options := SSEOptions{MaxBytes: 4096, MaxLineBytes: 128, ReadBuffer: 32}
	check := func(t *testing.T, source io.Reader) {
		t.Helper()
		events := make(chan SSEEvent, count)
		if err := ParseSSE(context.Background(), source, events, options); err != nil {
			t.Fatal(err)
		}
		if len(events) != count {
			t.Fatalf("event count = %d, want %d", len(events), count)
		}
		for i := range count {
			event := <-events
			if event.Event != fmt.Sprintf("event-%02d", i) || event.Data != fmt.Sprintf("alpha-%02d\nbeta-%02d", i, i) || event.ID != "retained-id" {
				t.Fatalf("event %d changed after reader refill: %#v", i, event)
			}
		}
	}
	t.Run("parser", func(t *testing.T) { check(t, strings.NewReader(input.String())) })
	t.Run("routed_response", func(t *testing.T) {
		response := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}}
		body := newRouteResponseBody(io.NopCloser(strings.NewReader(input.String())), response,
			newRouteSecrets("unrelated-private-token"), ResponseLimits{LineBytes: 32, EventBytes: 4096})
		defer body.Close()
		check(t, body)
	})
}

func BenchmarkParseSSE(b *testing.B) {
	for _, tc := range []struct {
		name       string
		bytes      int
		lines      int
		terminator string
	}{
		{"short", 128, 1, "\n\n"},
		{"medium", 512, 1, "\n\n"},
		{"multiline", 128, 3, "\r\n\r\n"},
		{"fragmented", 70 << 10, 1, "\r\n\r\n"},
	} {
		const count = 64
		data := strings.Repeat("x", tc.bytes)
		lines := make([]string, tc.lines)
		for i := range lines {
			lines[i] = "data: " + data
		}
		frame := strings.Join(lines, "\n") + tc.terminator
		input := strings.Repeat(frame, count)
		want := strings.Repeat(data+"\n", tc.lines-1) + data
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				events := make(chan SSEEvent, count)
				if err := ParseSSE(context.Background(), strings.NewReader(input), events, SSEOptions{}); err != nil {
					b.Fatal(err)
				}
				if len(events) != count {
					b.Fatalf("event count = %d, want %d", len(events), count)
				}
				for range count {
					if event := <-events; event.Data != want {
						b.Fatal("event data changed")
					}
				}
			}
		})
	}
}
