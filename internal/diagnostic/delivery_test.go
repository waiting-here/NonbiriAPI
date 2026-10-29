package diagnostic

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
)

type failingWriter struct{ *httptest.ResponseRecorder }

func (w failingWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

func TestDeliverySeparatesWireStatusFromProtocolAndFailure(t *testing.T) {
	ctx := WithRecorder(t.Context())
	w, observed := ObserveWriter(failingWriter{httptest.NewRecorder()}, ctx)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("event")); !errors.Is(err, syscall.EPIPE) {
		t.Fatal(err)
	}
	ProtocolComplete(ctx, true)
	got := observed.Snapshot()
	if got.HTTPStatus != 200 || !got.Failed || got.Bytes != 0 || got.Failure == nil || !got.Failure.ProtocolComplete || got.Failure.Phase != "write_client" {
		t.Fatalf("incorrect delivery facts: %+v", got)
	}
	if got.Failure.UpstreamStatus != 0 {
		t.Fatal("client status became upstream status")
	}
}

func TestDeliveryHeartbeatDoesNotProveProtocolCompletion(t *testing.T) {
	ctx := WithRecorder(t.Context())
	w, observed := ObserveWriter(httptest.NewRecorder(), ctx)
	w.(http.Flusher).Flush()
	d := observed.Snapshot()
	if d.HTTPStatus != 200 || d.Bytes != 0 || d.Failure != nil {
		t.Fatalf("incorrect header observation: %+v", d)
	}
	f := Finish(ctx, false, true, false, 200, "missing completion event", "upstream_protocol")
	if f.ProtocolComplete || !f.ResponseStarted {
		t.Fatal("wire headers and protocol completion were conflated")
	}
}
