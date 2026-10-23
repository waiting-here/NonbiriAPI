package egress

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
)

type markerTestKeys struct{}

func (markerTestKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	return bytes.Repeat([]byte{3}, 32), nil
}

func TestReturnMarkerDirectAndWorker(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "worker"}[worker], func(t *testing.T) {
			marker, err := returnmarker.New(markerTestKeys{})
			if err != nil {
				t.Fatal(err)
			}
			seen := make(chan returnmarker.Evidence, 1)
			handler := func(w http.ResponseWriter, r *http.Request) {
				evidence, ok := marker.Verify(r.Header)
				if !ok {
					t.Error("outbound marker missing or user-controlled")
				}
				seen <- evidence
				w.Header().Set(returnmarker.Header, r.Header.Get(returnmarker.Header))
				w.Header().Add("Trailer", returnmarker.Header)
				_, _ = io.WriteString(w, "ok")
				w.Header().Set(returnmarker.Header, r.Header.Get(returnmarker.Header))
			}
			var client *Client
			var target string
			if worker {
				client, _, _ = workerClient(t, handler, nil)
				target = routeSource
			} else {
				server := httptest.NewServer(http.HandlerFunc(handler))
				defer server.Close()
				client = newLoopbackClient(t, server.URL, nil)
				target = server.URL
			}
			client.stack.SetReturnMarker(marker)
			ctx := returnmarker.WithSource(WithCloudflareProxy(t.Context(), worker), returnmarker.Source{EndpointID: 11, EndpointKeyID: 22, DonationKeyID: 33})
			req, _ := http.NewRequestWithContext(ctx, "POST", target, nil)
			req.Header["nonbiri-request-origin"] = []string{"forged", "duplicate"}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || string(body) != "ok" || resp.Header.Get(returnmarker.Header) != "" || resp.Trailer.Get(returnmarker.Header) != "" {
				t.Fatal("response changed or leaked marker", err)
			}
			got := <-seen
			if got.EndpointID != 11 || got.EndpointKeyID != 22 || got.DonationKeyID != 33 {
				t.Fatal("source changed")
			}
			if len(req.Header["nonbiri-request-origin"]) != 2 {
				t.Fatal("mutated caller request")
			}
		})
	}
}
