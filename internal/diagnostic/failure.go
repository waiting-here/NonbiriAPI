package diagnostic

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Failure contains observations, not a response body or arbitrary error text.
// Missing observations remain absent; transport EOF is distinct from completion.
type Failure struct {
	Category          string        `json:"category"`
	Phase             string        `json:"phase"`
	Code              string        `json:"code"`
	Message           string        `json:"message"`
	Causes            []Cause       `json:"causes,omitempty"`
	Secondary         []Cause       `json:"secondary,omitempty"`
	StartedAt         int64         `json:"started_at_ms,omitempty"`
	FailedAt          int64         `json:"failed_at_ms,omitempty"`
	CompletedAt       int64         `json:"completed_at_ms,omitempty"`
	UpstreamStatus    int           `json:"upstream_status,omitempty"`
	HeadersReceived   bool          `json:"headers_received"`
	FirstBodyAt       int64         `json:"first_body_at_ms,omitempty"`
	ReceivedBytes     int64         `json:"received_bytes"`
	ReceivedEvents    int64         `json:"received_events"`
	ForwardedBytes    int64         `json:"forwarded_bytes"`
	ResponseStarted   bool          `json:"response_started"`
	BodyEOF           bool          `json:"body_eof"`
	ProtocolComplete  bool          `json:"protocol_complete"`
	UsagePresent      bool          `json:"usage_present"`
	ContentType       string        `json:"content_type,omitempty"`
	ContentEncoding   string        `json:"content_encoding,omitempty"`
	BudgetMS          int64         `json:"budget_ms,omitempty"`
	RetryAfterSeconds *int64        `json:"retry_after_seconds,omitempty"`
	LimitBytes        int64         `json:"limit_bytes,omitempty"`
	LimitKind         string        `json:"limit_kind,omitempty"`
	Correlation       []Correlation `json:"correlation,omitempty"`
	Timings           []Timing      `json:"timings,omitempty"`
}

type Cause struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
type Correlation struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}
type Timing struct {
	Phase     string `json:"phase"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type recorderKey struct{}
type Recorder struct {
	mu       sync.Mutex
	start    time.Time
	phase    string
	facts    Failure
	firstErr error
}

// WithRecorder creates one observation scope for one physical attempt.
func WithRecorder(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	r := &Recorder{start: time.Now(), phase: "dispatch"}
	if deadline, ok := ctx.Deadline(); ok {
		r.facts.BudgetMS = max(0, deadline.Sub(r.start).Milliseconds())
	}
	return context.WithValue(ctx, recorderKey{}, r)
}
func RecorderFrom(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	return r
}
func Phase(ctx context.Context, phase string) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.phase = BoundTo(phase, 64)
		if deadline, ok := ctx.Deadline(); ok {
			r.facts.BudgetMS = max(0, deadline.Sub(r.start).Milliseconds())
		}
		if len(r.facts.Timings) < 12 {
			r.facts.Timings = append(r.facts.Timings, Timing{Phase: r.phase, ElapsedMS: max(0, time.Since(r.start).Milliseconds())})
		}
	}
}

// RecordError captures only recognized facts and Go types. In particular, URL,
// DNS and syscall wrappers never contribute their untrusted Error() strings.
func RecordError(ctx context.Context, phase string, err error) {
	r := RecorderFrom(ctx)
	if r == nil || err == nil || errors.Is(err, io.EOF) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if phase == "" {
		phase = r.phase
	}
	category, code, message := classify(err, phase)
	if phase == "write_client" && category != "canceled" && category != "timeout" {
		category = "delivery"
	}
	causes := causesOf(err, phase)
	if r.facts.Code != "" {
		if r.facts.Code != code && len(r.facts.Secondary) < 4 {
			r.facts.Secondary = append(r.facts.Secondary, Cause{Type: safeType(err), Code: code, Message: message})
		}
		return
	}
	r.facts.Category, r.facts.Phase, r.facts.Code, r.facts.Message = category, phase, code, message
	r.facts.Causes = causes
	r.firstErr = err
	r.facts.FailedAt = time.Now().UnixMilli()
}

func RecordLimit(ctx context.Context, kind string, limit int64, err error) {
	RecordError(ctx, "read_response", err)
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.firstErr == nil || errors.Is(r.firstErr, err) {
			r.facts.Category, r.facts.Code, r.facts.Message = "resource_limit", "response_limit", "Response exceeded its configured limit"
		}
		r.facts.LimitKind, r.facts.LimitBytes = BoundTo(kind, 64), max(0, limit)
	}
}

// RecordKnownError refines this same error using a caller-owned classification.
// Callers must supply fixed code/message text, never an upstream Error() value.
func RecordKnownError(ctx context.Context, phase, category, code, message string, err error) {
	RecordError(ctx, phase, err)
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.firstErr != nil && !errors.Is(r.firstErr, err) {
			return
		}
		r.facts.Category, r.facts.Phase, r.facts.Code, r.facts.Message = category, phase, code, BoundTo(message, 512)
	}
}

func ProtocolComplete(ctx context.Context, complete bool) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		r.facts.ProtocolComplete = complete
		r.mu.Unlock()
	}
}

// ResponseMetadata accepts normalized media/encoding names, not arbitrary
// header values. Retry-After is represented as a duration, never raw text.
func ResponseMetadata(ctx context.Context, media, encoding string, retry *int64) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.facts.ContentType, r.facts.ContentEncoding = media, encoding
		r.facts.RetryAfterSeconds = retry
		if deadline, ok := ctx.Deadline(); ok {
			r.facts.BudgetMS = max(0, deadline.Sub(r.start).Milliseconds())
		}
	}
}

func RecordResponse(ctx context.Context, status int, ids []Correlation) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.facts.HeadersReceived = true
		if status >= 100 && status <= 599 {
			r.facts.UpstreamStatus = status
		}
		for _, id := range ids {
			if len(r.facts.Correlation) == 8 {
				break
			}
			r.facts.Correlation = append(r.facts.Correlation, id)
		}
		r.phase = "read_response"
	}
}
func RecordRead(ctx context.Context, n int, err error) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		if n > 0 {
			if r.facts.FirstBodyAt == 0 {
				r.facts.FirstBodyAt = time.Now().UnixMilli()
			}
			r.facts.ReceivedBytes += int64(n)
		}
		if errors.Is(err, io.EOF) {
			r.facts.BodyEOF = true
		}
		r.mu.Unlock()
	}
	RecordError(ctx, "read_response", err)
}
func RecordEvent(ctx context.Context) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		r.facts.ReceivedEvents++
		r.mu.Unlock()
	}
}
func RecordWrite(ctx context.Context, n int, err error) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		r.facts.ResponseStarted = true
		r.facts.ForwardedBytes += int64(max(n, 0))
		r.mu.Unlock()
	}
	RecordError(ctx, "write_client", err)
}

func DeliveryStarted(ctx context.Context) {
	if r := RecorderFrom(ctx); r != nil {
		r.mu.Lock()
		r.facts.ResponseStarted = true
		r.mu.Unlock()
	}
}

// Finish takes a snapshot and preserves the first captured failure. summary is
// an existing locally generated connector category, never raw upstream text.
func Finish(ctx context.Context, success, started, usage bool, status int, summary, origin string) *Failure {
	r := RecorderFrom(ctx)
	var f Failure
	if r != nil {
		r.mu.Lock()
		f = r.facts
		f.Causes = append([]Cause(nil), f.Causes...)
		f.Secondary = append([]Cause(nil), f.Secondary...)
		f.Correlation = append([]Correlation(nil), f.Correlation...)
		f.Timings = append([]Timing(nil), f.Timings...)
		f.StartedAt = r.start.UnixMilli()
		r.mu.Unlock()
	}
	if success {
		return nil
	}
	f.CompletedAt = time.Now().UnixMilli()
	// The legacy started flag can denote an accounting checkpoint. Only a
	// measured write establishes delivery; it is not inferred from HTTP 200.
	f.UsagePresent = usage
	if status >= 100 && status <= 599 {
		f.UpstreamStatus = status
		f.HeadersReceived = true
	}
	if f.Code == "" {
		f.Category, f.Phase, f.Code = "protocol", "decode_response", "invalid_response"
		switch origin {
		case "upstream_response":
			f.Category, f.Phase, f.Code = "upstream", "upstream_response", "upstream_error"
		case "timeout":
			f.Category, f.Phase, f.Code = "timeout", "execute", "deadline_exceeded"
		case "network":
			f.Category, f.Phase, f.Code = "network", "transport", "network_error"
		case "client_cancel":
			f.Category, f.Phase, f.Code = "canceled", "execute", "canceled"
		case "downstream":
			f.Category, f.Phase, f.Code = "delivery", "write_client", "client_write_failed"
		case "platform", "recovery_unknown", "legacy_unknown":
			f.Category, f.Phase, f.Code = "platform", "execute", origin
		}
		f.Message = BoundTo(summary, 512)
		if f.Message == "" {
			f.Message = "No further failure information was recorded"
		}
	}
	return &f
}

func Encode(f *Failure) string {
	if f == nil {
		return ""
	}
	b, err := json.Marshal(f)
	if err != nil || len(b) > 16<<10 {
		return ""
	}
	return string(b)
}
func Decode(value string) *Failure {
	if value == "" || len(value) > 16<<10 {
		return nil
	}
	var f Failure
	if json.Unmarshal([]byte(value), &f) != nil {
		return nil
	}
	return &f
}

func safeType(err error) string {
	if err == nil {
		return ""
	}
	return BoundTo(reflect.TypeOf(err).String(), 128)
}
func causesOf(err error, phase string) []Cause {
	var result []Cause
	queue := []error{err}
	for len(queue) > 0 && len(result) < 8 {
		err, queue = queue[0], queue[1:]
		if err == nil {
			continue
		}
		_, code, message := classify(err, phase)
		result = append(result, Cause{Type: safeType(err), Code: code, Message: message})
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			queue = append(queue, children[:min(len(children), 8-len(result))]...)
		} else if next := errors.Unwrap(err); next != nil {
			queue = append(queue, next)
		}
	}
	return result
}
func classify(err error, phase string) (string, string, string) {
	var dns *net.DNSError
	var cert x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	switch {
	case errors.As(err, &dns):
		if dns.IsTimeout {
			return "timeout", "dns_timeout", "DNS resolution timed out"
		}
		if dns.IsNotFound {
			return "network", "dns_not_found", "DNS name was not found"
		}
		return "network", "dns_failed", "DNS resolution failed"
	case errors.As(err, &cert):
		return "tls", "tls_unknown_authority", "TLS certificate authority is not trusted"
	case errors.As(err, &host):
		return "tls", "tls_hostname_mismatch", "TLS certificate does not match the requested host"
	case errors.As(err, &invalid):
		return "tls", "tls_invalid_certificate", "TLS certificate validation failed"
	case errors.As(err, &record):
		return "tls", "tls_record_invalid", "Invalid TLS record received"
	case errors.Is(err, context.Canceled):
		return "canceled", "canceled", "Request context was canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "deadline_exceeded", "Request deadline was exceeded"
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrHeader):
		return "encoding", "compression_header_invalid", "Compressed response has an invalid header"
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		return "encoding", "compression_checksum_invalid", "Compressed response checksum failed"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "network", "unexpected_eof", "Response ended before all bytes were received"
	case errors.Is(err, syscall.ECONNRESET):
		return "network", "connection_reset", "Connection was reset by the peer"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "network", "connection_refused", "Connection was refused"
	case errors.Is(err, syscall.EPIPE):
		return "network", "broken_pipe", "Connection closed while writing"
	case errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed):
		return "network", "connection_closed", "Connection was closed"
	case errors.As(err, &syntax):
		return "protocol", "invalid_json", fmt.Sprintf("Invalid JSON at byte %d", syntax.Offset)
	case errors.As(err, &shape):
		return "protocol", "json_type_mismatch", fmt.Sprintf("Unexpected JSON value type at byte %d", shape.Offset)
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout", "network_timeout", "Network operation timed out"
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		switch operation.Op {
		case "dial":
			return "network", "connect_failed", "Connection could not be established"
		case "read":
			return "network", "read_failed", "Connection failed while reading"
		case "write":
			return "network", "write_failed", "Connection failed while sending"
		}
	}
	if phase == "write_client" {
		return "delivery", "client_write_failed", "Response could not be written to the client connection"
	}
	if strings.Contains(phase, "decode") || phase == "parse_sse" || phase == "convert_response" {
		return "protocol", "invalid_response", "Response parsing failed"
	}
	return "unknown", "unclassified_error", "Error type recorded; no safe detailed message is available"
}
