package main

import (
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
	"strings"
	"testing"
)

func TestModelBodyLimitProductionWiringAndLiveConfiguration(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	text := strings.Repeat("x", 2*int(requestbody.MiB))
	for _, configured := range []int{10, 1, 3} {
		if configured != 10 {
			f.exec(t, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, requestbody.ConfigKey, fmt.Sprint(configured))
		}
		for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
			for _, path := range []string{"/v1/chat/completions", "/v1/embeddings"} {
				body := `{"model":"` + model + `","input":"` + text + `"}`
				if path == "/v1/chat/completions" {
					body = `{"model":"` + model + `","messages":[{"role":"user","content":"` + text + `"}]}`
				}
				status, response := f.post(t, path, body)
				want := 200
				if configured == 1 {
					want = 413
				}
				if status != want {
					t.Fatalf("configured=%d model=%s path=%s: %d %s", configured, model, path, status, response)
				}
			}
		}
	}
}
