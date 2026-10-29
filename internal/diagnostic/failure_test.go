package diagnostic

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestFailureKeepsOriginalCauseWithoutTransportSecrets(t *testing.T) {
	ctx := WithRecorder(t.Context())
	err := &url.Error{Op: "Get", URL: "https://private.invalid/?key=do-not-save", Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}
	RecordRead(ctx, 42, err)
	RecordError(ctx, "close", context.Canceled)
	f := Finish(ctx, false, true, false, 200, "upstream stream was interrupted", "network")
	if f.Code != "connection_reset" || f.Phase != "read_response" || f.ReceivedBytes != 42 || f.UpstreamStatus != 200 || len(f.Causes) != 3 || len(f.Secondary) != 1 {
		t.Fatalf("unexpected facts: %+v", f)
	}
	encoded := Encode(f)
	for _, forbidden := range []string{"private.invalid", "do-not-save", "https://"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("secret reflected: %s", encoded)
		}
	}
	if Decode(encoded) == nil {
		t.Fatal("stored facts did not round trip")
	}
}

func TestFailureSeparatesEncodingEOFAndProtocolCompletion(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{gzip.ErrHeader, "compression_header_invalid"}, {io.ErrUnexpectedEOF, "unexpected_eof"}, {&net.DNSError{Err: "no such host", Name: "secret.invalid", IsNotFound: true}, "dns_not_found"}} {
		ctx := WithRecorder(t.Context())
		RecordRead(ctx, 4, tc.err)
		f := Finish(ctx, false, false, true, 200, "failed", "upstream_protocol")
		if f.Code != tc.code || f.ProtocolComplete || !f.UsagePresent {
			t.Fatalf("bad failure: %+v", f)
		}
	}
	ctx := WithRecorder(t.Context())
	RecordRead(ctx, 4, io.EOF)
	f := Finish(ctx, false, false, false, 200, "missing completion event", "upstream_protocol")
	if !f.BodyEOF || f.ProtocolComplete || f.Code != "invalid_response" {
		t.Fatalf("clean EOF became success: %+v", f)
	}
}

func TestFailureBoundsUnknownChainsAndConcurrentCounters(t *testing.T) {
	err := errors.New("unknown error with credential sk-secret-value")
	for range 20 {
		err = fmt.Errorf("private/path: %w", err)
	}
	ctx := WithRecorder(t.Context())
	RecordError(ctx, "decode_response", err)
	done := make(chan struct{})
	for range 4 {
		go func() {
			for range 100 {
				RecordEvent(ctx)
				RecordWrite(ctx, 1, nil)
			}
			done <- struct{}{}
		}()
	}
	for range 4 {
		<-done
	}
	f := Finish(ctx, false, true, false, 200, "failed", "upstream_protocol")
	if len(f.Causes) != 8 || f.ReceivedEvents != 400 || f.ForwardedBytes != 400 {
		t.Fatalf("bad bounded snapshot: %+v", f)
	}
	if strings.Contains(Encode(f), "sk-secret") || strings.Contains(Encode(f), "private/path") {
		t.Fatal("unknown Error() message was retained")
	}
}
