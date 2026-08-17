package egress

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	DefaultSSELineBytes   = 10 << 20
	defaultSSEReadBuffer  = 64 << 10
	defaultSSEEventBuffer = 4
)

// SSEEvent is one parsed Server-Sent Event. Data joins repeated data fields
// with a newline. Protocol-specific completion markers such as [DONE] remain
// data; this layer never invents or classifies a successful terminator.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry int64 // milliseconds; zero when absent or invalid
}

// SSEOptions bound parser memory and cumulative stream input. MaxBytes is
// independent of line size and counts comments and ignored fields too.
type SSEOptions struct {
	MaxBytes     int64
	MaxLineBytes int
	ReadBuffer   int
	EventBuffer  int
}

func normalizeSSEOptions(options SSEOptions) (SSEOptions, error) {
	if options.MaxBytes < 0 || options.MaxLineBytes < 0 || options.ReadBuffer < 0 || options.EventBuffer < 0 {
		return SSEOptions{}, errors.New("SSE limits must not be negative")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultMaxResponseBytes
	}
	if options.MaxLineBytes == 0 {
		options.MaxLineBytes = DefaultSSELineBytes
	}
	if int64(options.MaxLineBytes) > options.MaxBytes {
		options.MaxLineBytes = int(options.MaxBytes)
	}
	if options.MaxLineBytes < 1 {
		return SSEOptions{}, errors.New("SSE line limit must be positive")
	}
	if options.ReadBuffer == 0 {
		options.ReadBuffer = defaultSSEReadBuffer
	}
	if options.ReadBuffer > options.MaxLineBytes {
		options.ReadBuffer = options.MaxLineBytes
	}
	if options.EventBuffer == 0 {
		options.EventBuffer = defaultSSEEventBuffer
	}
	return options, nil
}

// SSELineTooLargeError reports a single line crossing its independent memory
// bound before an event can be assembled.
type SSELineTooLargeError struct {
	Limit int
}

func (e *SSELineTooLargeError) Error() string {
	return "SSE line exceeds the " + strconv.Itoa(e.Limit) + "-byte limit"
}

// StreamSSE parses a response body asynchronously. Cancellation closes the
// body to unblock a network read, and both channels always close. If delivery
// is backpressured, cancellation wins through ParseSSE's context-aware send.
func StreamSSE(ctx context.Context, body io.ReadCloser, options SSEOptions) (<-chan SSEEvent, <-chan error) {
	normalized, err := normalizeSSEOptions(options)
	buffer := defaultSSEEventBuffer
	if err == nil {
		buffer = normalized.EventBuffer
	}
	events := make(chan SSEEvent, buffer)
	errs := make(chan error, 1)

	if ctx == nil {
		close(events)
		errs <- errors.New("SSE context is required")
		close(errs)
		return events, errs
	}
	if body == nil {
		close(events)
		errs <- errors.New("SSE body is required")
		close(errs)
		return events, errs
	}
	if err != nil {
		_ = body.Close()
		close(events)
		errs <- err
		close(errs)
		return events, errs
	}

	go func() {
		defer close(events)
		defer close(errs)

		var closeOnce sync.Once
		closeBody := func() { closeOnce.Do(func() { _ = body.Close() }) }
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				closeBody()
			case <-done:
			}
		}()

		parseErr := ParseSSE(ctx, body, events, normalized)
		close(done)
		closeBody()
		if parseErr != nil && !errors.Is(parseErr, context.Canceled) && !errors.Is(parseErr, context.DeadlineExceeded) {
			select {
			case errs <- parseErr:
			case <-ctx.Done():
			}
		}
	}()

	return events, errs
}

// ParseSSE parses an SSE stream synchronously and sends complete events to
// out. It does not close out. A final event without a blank separator is
// emitted at clean EOF, but successful protocol termination remains the
// caller's responsibility.
func ParseSSE(ctx context.Context, source io.Reader, out chan<- SSEEvent, options SSEOptions) error {
	if ctx == nil {
		return errors.New("SSE context is required")
	}
	if source == nil || out == nil {
		return errors.New("SSE source and output channel are required")
	}
	normalized, err := normalizeSSEOptions(options)
	if err != nil {
		return err
	}

	limited := &cumulativeReader{source: source, remaining: normalized.MaxBytes, limit: normalized.MaxBytes}
	reader := bufio.NewReaderSize(limited, normalized.ReadBuffer)
	builder := sseEventBuilder{}
	firstLine := true

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, eof, readErr := readBoundedLine(reader, normalized.MaxLineBytes)
		if readErr != nil {
			return readErr
		}
		if line != nil {
			if !utf8.Valid(line) {
				return errors.New("SSE stream is not valid UTF-8")
			}
			if firstLine {
				line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
				firstLine = false
			}
			if len(line) == 0 {
				if err := builder.dispatch(ctx, out); err != nil {
					return err
				}
			} else {
				builder.consume(string(line))
			}
		}
		if eof {
			return builder.dispatch(ctx, out)
		}
	}
}

type sseEventBuilder struct {
	event string
	data  []string
	id    string
	retry int64
}

func (b *sseEventBuilder) consume(line string) {
	if strings.HasPrefix(line, ":") {
		return
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	} else if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	switch field {
	case "event":
		b.event = value
	case "data":
		b.data = append(b.data, value)
	case "id":
		if !strings.ContainsRune(value, '\x00') {
			b.id = value
		}
	case "retry":
		if value != "" {
			if retry, err := strconv.ParseInt(value, 10, 64); err == nil && retry >= 0 {
				b.retry = retry
			}
		}
	}
}

func (b *sseEventBuilder) dispatch(ctx context.Context, out chan<- SSEEvent) error {
	if len(b.data) == 0 {
		b.event = ""
		b.retry = 0
		return nil
	}
	eventName := b.event
	if eventName == "" {
		eventName = "message"
	}
	event := SSEEvent{
		Event: eventName,
		Data:  strings.Join(b.data, "\n"),
		ID:    b.id,
		Retry: b.retry,
	}
	b.event = ""
	b.data = nil
	b.retry = 0
	select {
	case out <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	line := make([]byte, 0, min(limit, defaultSSEReadBuffer))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			content := fragment
			if fragment[len(fragment)-1] == '\n' {
				content = fragment[:len(fragment)-1]
			}
			if len(line)+len(content) > limit {
				return nil, false, &SSELineTooLargeError{Limit: limit}
			}
			line = append(line, content...)
		}

		switch {
		case err == nil:
			line = bytes.TrimSuffix(line, []byte{'\r'})
			return line, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, true, nil
			}
			line = bytes.TrimSuffix(line, []byte{'\r'})
			return line, true, nil
		default:
			return nil, false, err
		}
	}
}

type cumulativeReader struct {
	source    io.Reader
	remaining int64
	limit     int64
	exceeded  bool
}

func (r *cumulativeReader) Read(dst []byte) (int, error) {
	if r.exceeded {
		return 0, &ResponseTooLargeError{Limit: r.limit}
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.source.Read(probe[:])
		if n > 0 {
			r.exceeded = true
			return 0, &ResponseTooLargeError{Limit: r.limit}
		}
		return 0, err
	}
	readSize := int64(len(dst))
	if readSize > r.remaining+1 {
		readSize = r.remaining + 1
	}
	n, err := r.source.Read(dst[:int(readSize)])
	if int64(n) > r.remaining {
		allowed := int(r.remaining)
		r.remaining = 0
		r.exceeded = true
		return allowed, &ResponseTooLargeError{Limit: r.limit}
	}
	r.remaining -= int64(n)
	return n, err
}
