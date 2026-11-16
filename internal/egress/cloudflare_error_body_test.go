package egress

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestCloudflareErrorBodyPreservesNonSSEEnvelopes(t *testing.T) {
	for _, worker := range []bool{false, true} {
		name := "gateway"
		if worker {
			name = "worker"
		}
		t.Run(name, func(t *testing.T) {
			for _, payload := range []string{
				"{\n  \"error\": {\n    \"code\": 503,\n    \"message\": \"This model is currently experiencing high demand.\",\n    \"status\": \"UNAVAILABLE\"\n  }\n}\n",
				": provider note\r\nevent: error\r\ndata: {\"error\":{\"code\":503}}\r\n\r\n",
			} {
				handler := func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, payload)
				}
				var client *Client
				if worker {
					client, _, _ = workerClient(t, handler, nil)
				} else {
					client, _, _ = routedClient(t, handler, nil)
				}
				captured := 0
				ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
					captured++
					if event.Status() != 503 || event.ContentType() != "text/event-stream" || event.Unavailable() || event.Truncated() || string(event.Bytes()) != payload {
						t.Errorf("error capture changed: status=%d bytes=%d unavailable=%t", event.Status(), len(event.Bytes()), event.Unavailable())
					}
				})
				response, err := client.Do(routedRequest(ctx, "/models/test:streamGenerateContent?alt=sse"))
				if err != nil {
					t.Fatal(err)
				}
				upstreamerror.InspectResponse(ctx, response, nil)
				response.Body.Close()
				if response.StatusCode != 503 || captured != 1 {
					t.Fatalf("status or capture count changed: %d / %d", response.StatusCode, captured)
				}
			}
		})
	}
}

func TestCloudflareErrorBodyStillBlocksSemanticReflection(t *testing.T) {
	for _, payload := range []string{
		`{"error":{"message":"\u0067ateway-token-private-12345"}}`,
		"data: {\"text\":\"gateway-token-\"}\n\ndata: {\"text\":\"private-12345\"}\n\n",
	} {
		client, _, _ := routedClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, payload)
		}, nil)
		captured := false
		ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
			captured = true
			if !event.Unavailable() || len(event.Bytes()) != 0 {
				t.Fatal("private error body escaped into diagnostics")
			}
		})
		response, err := client.Do(routedRequest(ctx, "/stream"))
		if err != nil {
			t.Fatal(err)
		}
		upstreamerror.InspectResponse(ctx, response, func([]byte) { t.Fatal("private body reached inspector") })
		response.Body.Close()
		if !captured {
			t.Fatal("missing unavailable error capture")
		}
	}
}

func TestCloudflareErrorBodyUsesFiniteResponseBudget(t *testing.T) {
	client, _, _ := routedClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, strings.Repeat("x", 256))
	}, nil)
	request := WithResponseLimits(routedRequest(context.Background(), "/stream"), ResponseLimits{JSONBytes: 64, StreamBytes: 1024})
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err == nil || len(body) != 0 {
		t.Fatalf("oversized error body was accepted: bytes=%d error=%v", len(body), err)
	}
}
