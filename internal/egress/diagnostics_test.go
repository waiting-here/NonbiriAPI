package egress

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func TestDiagnosticCorrelationRejectsReflectedCredentials(t *testing.T) {
	ctx := diagnostic.WithRecorder(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/?token=query-secret-value", nil)
	req.Header.Set("Authorization", "Bearer auth-secret-value")
	res := &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"auth-secret-value"}, "Request-Id": {"query-secret-value"}, "Cf-Ray": {"abcd1234-ORD"}, "Traceparent": {"00-01234567890123456789012345678901-0123456789012345-01"}}}
	observeResponse(ctx, req, res)
	body := diagnosticBody{ReadCloser: io.NopCloser(strings.NewReader("hello")), ctx: ctx}
	_, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	f := diagnostic.Finish(ctx, false, false, false, 200, "missing terminal", "upstream_protocol")
	if len(f.Correlation) != 2 || f.ReceivedBytes != 5 || !f.BodyEOF {
		t.Fatalf("bad observations: %+v", f)
	}
	encoded := diagnostic.Encode(f)
	if strings.Contains(encoded, "secret-value") {
		t.Fatal("reflected credentials retained")
	}
}

func TestDiagnosticWireFailuresKeepHeadersAndFirstCause(t *testing.T) {
	for _, tc := range []struct {
		name, category, code, encoding string
		write                          http.HandlerFunc
	}{
		{"partial_body", "network", "unexpected_eof", "", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "data: incomplete\n\n")
		}},
		{"gzip_header", "encoding", "compression_header_invalid", "gzip", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = io.WriteString(w, "invalid-compressed-content")
		}},
		{"gzip_checksum", "encoding", "compression_checksum_invalid", "gzip", func(w http.ResponseWriter, _ *http.Request) {
			var encoded strings.Builder
			zw := gzip.NewWriter(&encoded)
			_, _ = io.WriteString(zw, "data: incomplete\n\n")
			_ = zw.Close()
			payload := []byte(encoded.String())
			payload[len(payload)-8] ^= 1
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-ID", "wire-request-id")
				tc.write(w, r)
			}))
			defer server.Close()
			client := newLoopbackClient(t, server.URL, nil)
			ctx := diagnostic.WithRecorder(t.Context())
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err == nil {
				t.Fatal("broken response was accepted")
			}
			diagnostic.RecordError(ctx, "decode_response", fmt.Errorf("later parse: %w", io.ErrUnexpectedEOF))
			facts := diagnostic.Finish(ctx, false, false, false, response.StatusCode, "missing terminal", "upstream_protocol")
			if facts.Category != tc.category || facts.Code != tc.code || facts.ContentEncoding != tc.encoding || !facts.HeadersReceived || facts.UpstreamStatus != 200 || facts.ProtocolComplete || facts.BodyEOF || len(facts.Correlation) != 1 || facts.Correlation[0].Value != "wire-request-id" {
				t.Fatalf("failure facts: %+v", facts)
			}
		})
	}
}

func TestDiagnosticQueueTimeoutHasNoUpstreamResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("queued request was dispatched") }))
	defer server.Close()
	client := newLoopbackClient(t, server.URL, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
	permit, err := client.gate.Acquire(t.Context(), client.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.Release()
	bounded, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	ctx := diagnostic.WithRecorder(bounded)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	_, err = client.Do(request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	facts := diagnostic.Finish(ctx, false, false, false, 0, "queue failed", "platform")
	if facts.Category != "timeout" || facts.Phase != "queue" || facts.HeadersReceived || facts.UpstreamStatus != 0 || facts.BudgetMS <= 0 {
		t.Fatalf("queue failure facts: %+v", facts)
	}
}
