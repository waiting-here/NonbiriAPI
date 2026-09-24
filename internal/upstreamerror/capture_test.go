package upstreamerror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestCapturePreservesRawBytesAndIndependentSummaryBoundary(t *testing.T) {
	raw := []byte("{\r\n\t\"message\":\"opaque-secret\",\"echo\":\"prompt bytes\"\r\n}")
	var captured []byte
	var metadata Event
	ctx := WithCapture(context.Background(), func(_ context.Context, event Event) {
		captured = append([]byte(nil), event.Bytes()...)
		metadata = event
	})
	response := &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: 1 << 62}
	detail := testContext("opaque-secret").ReadResponse(ctx, response, 16)
	if !bytes.Equal(captured, raw) || detail != (Detail{}) || metadata.Status() != 503 || metadata.Truncated() {
		t.Fatal("raw bytes or independent summary bound changed")
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("diagnostic", "event", metadata)
	encoded, _ := json.Marshal(metadata)
	formatted := fmt.Sprintf("%v %+v %#v", metadata, metadata, metadata) + logs.String() + string(encoded)
	if strings.Contains(formatted, "opaque-secret") || strings.Contains(formatted, "prompt bytes") {
		t.Fatal("generic diagnostics leaked raw bytes")
	}
}

func TestCaptureBoundsUnreadableAndPanicIsolation(t *testing.T) {
	reader := &countReader{Reader: strings.NewReader(strings.Repeat("x", MaxRawBodyBytes+10))}
	ctx := WithCapture(context.Background(), func(_ context.Context, event Event) {
		if len(event.Bytes()) != MaxRawBodyBytes || !event.Truncated() || event.Unavailable() {
			t.Fatal("wrong bounded event")
		}
		panic("diagnostic sink failed")
	})
	if got := testContext().ReadResponse(ctx, &http.Response{StatusCode: 502, Body: io.NopCloser(reader)}); got != (Detail{}) || reader.read != MaxRawBodyBytes+1 {
		t.Fatal("capture exceeded bound or changed summary")
	}
	called := false
	ctx = WithCapture(context.Background(), func(_ context.Context, event Event) {
		called = true
		if !event.Unavailable() || len(event.Bytes()) != 0 {
			t.Fatal("unreadable body was retained")
		}
	})
	_ = testContext().ReadResponse(ctx, &http.Response{StatusCode: 502, Body: io.NopCloser(failedReader{})})
	if !called {
		t.Fatal("unavailable diagnostic was not reported")
	}
}

func TestCaptureExplicitEventDoesNotRetainCallerBuffer(t *testing.T) {
	var copyBody []byte
	ctx := WithCapture(context.Background(), func(_ context.Context, event Event) { copyBody = append([]byte(nil), event.Bytes()...) })
	raw := []byte{0xff, 0, 0xfe}
	CaptureEvent(ctx, 200, "text/event-stream", raw)
	clear(raw)
	if !bytes.Equal(copyBody, []byte{0xff, 0, 0xfe}) {
		t.Fatal("non UTF-8 event bytes changed")
	}
}
