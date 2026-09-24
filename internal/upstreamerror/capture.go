package upstreamerror

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxRawBodyBytes = 1 << 20

// Event is a short-lived capability. Only the diagnostic owner should consume
// its bytes; formatting the event never reveals upstream content.
type Event struct {
	status      int
	contentType string
	body        []byte
	truncated   bool
	unavailable bool
}

func (e Event) Status() int         { return e.status }
func (e Event) ContentType() string { return e.contentType }
func (e Event) Truncated() bool     { return e.truncated }
func (e Event) Unavailable() bool   { return e.unavailable }

// WithoutBody preserves failure metadata after a storage failure.
func (e Event) WithoutBody() Event { e.body = nil; e.unavailable = true; return e }

// Bytes is valid only during the synchronous sink call. Retaining owners must
// copy it into their bounded storage before returning.
func (e Event) Bytes() []byte        { return e.body }
func (Event) String() string         { return "[private upstream diagnostic]" }
func (e Event) GoString() string     { return e.String() }
func (e Event) LogValue() slog.Value { return slog.StringValue(e.String()) }

type Sink func(context.Context, Event)
type captureKey struct{}

func WithCapture(ctx context.Context, sink Sink) context.Context {
	return context.WithValue(ctx, captureKey{}, sink)
}

// ReadResponse is used only after a connector has classified a response as a
// failure. It keeps raw diagnostics separate from the public, safe summary.
func (c Context) ReadResponse(ctx context.Context, response *http.Response, summaryLimit ...int64) Detail {
	if response == nil {
		return Detail{}
	}
	if c.ContainsSecret == nil && !IsCapturing(ctx) {
		return Detail{}
	}
	if response.Body == nil {
		emit(ctx, Event{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), unavailable: true})
		return Detail{}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxRawBodyBytes+1))
	defer clear(body)
	event := Event{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), body: body, unavailable: err != nil}
	if len(body) > MaxRawBodyBytes {
		event.body, event.truncated = body[:MaxRawBodyBytes], true
	}
	if err != nil {
		event.body = nil
	}
	emit(ctx, event)
	if err != nil || event.truncated {
		return Detail{}
	}
	if len(summaryLimit) > 0 && int64(len(body)) > summaryLimit[0] {
		return Detail{}
	}
	return c.Parse(body)
}

func IsCapturing(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	sink, _ := ctx.Value(captureKey{}).(Sink)
	return sink != nil
}

// CaptureEvent captures only an explicit protocol error or an already rejected
// non-stream response. Successful stream chunks must never call this function.
func CaptureEvent(ctx context.Context, status int, contentType string, payload []byte) {
	event := Event{status: status, contentType: contentType, body: payload}
	if len(payload) > MaxRawBodyBytes {
		event.body, event.truncated = payload[:MaxRawBodyBytes], true
	}
	emit(ctx, event)
}

func emit(ctx context.Context, event Event) {
	if ctx == nil {
		return
	}
	sink, _ := ctx.Value(captureKey{}).(Sink)
	if sink == nil {
		return
	}
	if event.status < 100 || event.status > 599 {
		event.status = 0
	}
	event.contentType = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(event.contentType, ""))
	if len(event.contentType) > 256 {
		event.contentType = event.contentType[:256]
		for !utf8.ValidString(event.contentType) {
			event.contentType = event.contentType[:len(event.contentType)-1]
		}
	}
	// Diagnostic failures are deliberately independent of protocol and billing.
	defer func() { _ = recover() }()
	sink(ctx, event)
}
