package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestPrivateRawCaptureIncludesDiscoveryAndStreamErrorsOnly(t *testing.T) {
	const payload = "{\"error\":{\"message\":\"private-diagnostic-body\"}}"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					writeSSE(w, "data: "+validChunk+"\n\n", "event: error\ndata: "+payload+"\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(503)
					_, _ = io.WriteString(w, payload)
				}
			}))
			defer server.Close()
			var bodies [][]byte
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
				bodies = append(bodies, append([]byte(nil), event.Bytes()...))
			})
			adapter := adapterForServer(t, server.URL, nil, nil)
			request := decodeAdapterRequest(t, fmt.Sprintf(`{"model":"public/model","messages":[],"stream":%t}`, stream))
			result := adapter.Attempt(ctx, httptest.NewRecorder(), testTarget(server.URL, []byte("credential"), []byte("ciphertext")), request, "caller")
			if result.Success || len(bodies) != 1 || !bytes.Equal(bodies[0], []byte(payload)) {
				t.Fatal("raw error payload missing or successful stream chunk captured")
			}
		})
	}
	client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	}}
	var body []byte
	ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) { body = append([]byte(nil), event.Bytes()...) })
	result := (ModelDiscoverer{}).Discover(ctx, openAIDiscoveryInput(client, []byte("secret"), []byte("ciphertext")))
	if result.Succeeded() || string(body) != payload || strings.Contains(fmt.Sprintf("%+v", result), "private-diagnostic-body") {
		t.Fatal("discovery raw/safe boundaries failed")
	}
}
