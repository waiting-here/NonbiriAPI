package imageactivity

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestTransportDiagnosticsContainOnlySafeCategories(t *testing.T) {
	private := errors.New("private-secret https://private.example/secret?q=private-token")
	for _, tc := range []struct {
		name   string
		err    error
		reason string
		code   string
	}{
		{"unknown", private, "request_failed", "upstream_failed"},
		{"dns", &net.DNSError{Err: private.Error(), Name: "private.example"}, "dns_failed", "upstream_failed"},
		{"certificate", &tls.CertificateVerificationError{Err: private}, "tls_certificate_invalid", "upstream_failed"},
		{"tls", tls.RecordHeaderError{Msg: private.Error()}, "tls_protocol_failed", "upstream_failed"},
		{"connection", &net.OpError{Op: "dial", Net: "tcp", Err: private}, "connection_failed", "upstream_failed"},
		{"redirect", egress.ErrRedirectBlocked, "redirect_blocked", "upstream_failed"},
		{"timeout", context.DeadlineExceeded, "request_timeout", "execution_timeout"},
		{"cancelled", context.Canceled, "request_cancelled", "upstream_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
				if event.Status() != 0 || event.ContentType() != "application/vnd.nonbiriapi.image-diagnostic+json" {
					t.Fatal("invented response or missing synthetic marker")
				}
				body = append([]byte(nil), event.Bytes()...)
			})
			err := &url.Error{Op: "Get", URL: "https://private.example/?secret=private-token", Err: tc.err}
			captureTransportFailure(ctx, err)
			var got map[string]any
			if json.Unmarshal(body, &got) != nil || len(got) != 3 || got["category"] != "image_transport_failed" || got["reason"] != tc.reason || got["original_body_saved"] != false {
				t.Fatalf("unexpected diagnostic: %s", body)
			}
			wrapped := &transportFailure{cause: err}
			if readFailure(wrapped) != tc.code || !errors.Is(wrapped, tc.err) || strings.Contains(string(body)+fmt.Sprint(wrapped), "private") {
				t.Fatal("failure classification, unwrap or privacy boundary changed")
			}
		})
	}
	if readFailure(io.ErrUnexpectedEOF) != "invalid_result" {
		t.Fatal("response parsing failures changed category")
	}
}

func TestDiscoveryConnectionFailureRetainsSafeDiagnostic(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.server.Close()
	refresh, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		if err != nil || state.State != "failed" {
			return false
		}
		if state.ErrorCode == nil || *state.ErrorCode != "upstream_failed" {
			t.Fatalf("connection error misclassified: %+v", state)
		}
		return true
	})
	var body, mime string
	var status *int
	if err := f.database.QueryRow("SELECT CAST(body AS TEXT),content_type,http_status FROM request_error_bodies WHERE operation_id=?", refresh.Value.Operation.ID).Scan(&body, &mime, &status); err != nil {
		t.Fatal(err)
	}
	if status != nil || mime != "application/vnd.nonbiriapi.image-diagnostic+json" || body != `{"category":"image_transport_failed","reason":"connection_failed","original_body_saved":false}` {
		t.Fatalf("transport diagnostic: status=%v mime=%s body=%s", status, mime, body)
	}
}
