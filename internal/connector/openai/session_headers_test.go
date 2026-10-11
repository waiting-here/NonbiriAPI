package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestManagedHeadersOverrideFixedAndObserveSend(t *testing.T) {
	value := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cache-Key") != value || r.Header.Get("X-Session-ID") != value || r.Header.Get("Authorization") != "Bearer mock-upstream-key" {
			t.Error("wrong managed headers or protected auth")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, validCompletion)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	result := adapter.AttemptWithPolicy(context.Background(), httptest.NewRecorder(), testTarget(server.URL+"/v1", []byte("mock-upstream-key"), nil), decodeAdapterRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`),
		connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_authoritative", AdditionalHeaders: http.Header{"X-Cache-Key": {"fixed"}}, ManagedHeaders: http.Header{"X-Cache-Key": {value}, "X-Session-ID": {value}}, OnRequestSent: func() { sent.Add(1) }})
	if !result.Success || sent.Load() != 1 {
		t.Fatalf("success %v sent %d failure %v diagnostic %s", result.Success, sent.Load(), result.Failure, result.Diagnostic)
	}
}
