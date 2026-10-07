package egress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderPostDoesNotReplayOnReusedConnectionResponseLoss(t *testing.T) {
	for _, body := range []string{"billable request", ""} {
		t.Run(body, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, "warm connection")
					return
				}
				posts.Add(1)
				if r.Header.Get("Idempotency-Key") != "provider-key" {
					t.Error("provider idempotency header was removed")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			defer server.Close()
			stack := newLoopbackStack(t, []string{server.URL}, nil)
			defer stack.CloseIdleConnections()
			client, err := stack.NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			warm, _ := http.NewRequest(http.MethodGet, server.URL, nil)
			response, err := client.Do(warm)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
			request.Header.Set("Idempotency-Key", "provider-key")
			if _, err := client.Do(request); err == nil || posts.Load() != 1 {
				t.Fatalf("ambiguous request replayed: posts=%d error=%v", posts.Load(), err)
			}
		})
	}
}
