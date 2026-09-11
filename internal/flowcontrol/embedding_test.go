package flowcontrol

import (
	"net/http"
	"testing"
)

func TestEmbeddingAndChatShareRPM(t *testing.T) {
	for _, first := range []string{"/v1/embeddings", "/v1/chat/completions"} {
		t.Run(first, func(t *testing.T) {
			config := testRPMConfig()
			config.PerUserLimit = 1
			middleware, _ := newTestMiddleware(t, config, nil, nil)
			handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
			if w := call(handler, "POST", first); w.Code != 200 {
				t.Fatal(w.Code)
			}
			for _, next := range []string{"/v1/embeddings", "/v1/chat/completions"} {
				if w := call(handler, "POST", next); w.Code != 429 {
					t.Fatalf("%s bypassed shared RPM: %d", next, w.Code)
				}
			}
		})
	}
}
