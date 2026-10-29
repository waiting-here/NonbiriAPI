package egress

import (
	"io"
	"net/http"
	"strings"
	"testing"

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
