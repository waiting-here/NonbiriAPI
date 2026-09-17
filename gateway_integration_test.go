package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestGatewayHTTPUsesSharedAccountingAndAttributionSnapshot(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]json.RawMessage
	f := newEmbeddingHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/embedding-model":
			if r.Header.Get("ai-embedding-model-specification-version") != "3" || r.Header.Get("ai-model-id") != "private-model" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"embeddings":[[0.25,-0.5]],"usage":{"tokens":3}}`)
		case "/v1/language-model":
			if r.Header.Get("ai-language-model-specification-version") != "3" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"finishReason":{"unified":"stop"},"usage":{"inputTokens":{"total":3,"noCache":3,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":2}}}`)
		default:
			w.WriteHeader(404)
		}
	})
	f.exec(t, "UPDATE endpoint_keys SET force_store_false=0")
	f.exec(t, "UPDATE models SET flatten_tool_calls=0")
	f.exec(t, "UPDATE charity_models SET flatten_tool_calls=0")
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	var previousTag string
	for _, enabled := range []string{"0", "1", "0"} {
		f.exec(t, "UPDATE site_config SET value=? WHERE key='gateway_user_attribution_enabled'", enabled)
		for _, model := range []string{"provider/self", "[公益]provider/per_request", "[公益]provider/per_token"} {
			for _, embedding := range []bool{false, true} {
				path, body := "/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"hello"}],"max_tokens":128}`
				if embedding {
					path, body = "/v1/embeddings", `{"model":"`+model+`","input":"hello"}`
				}
				status, out := f.post(t, path, body)
				if status != 200 {
					t.Fatalf("%s %s status=%d %s", enabled, model, status, out)
				}
				if strings.Contains(string(out), "private-model") || strings.Contains(string(out), "providerOptions") {
					t.Fatal("private metadata escaped")
				}
				mu.Lock()
				sent := bodies[len(bodies)-1]
				mu.Unlock()
				if enabled == "0" {
					if sent["providerOptions"] != nil {
						t.Fatal("default/off attribution sent")
					}
				} else {
					var options struct {
						Gateway struct {
							User string `json:"user"`
						} `json:"gateway"`
					}
					if json.Unmarshal(sent["providerOptions"], &options) != nil || options.Gateway.User == "" {
						t.Fatal("missing attribution")
					}
					if previousTag != "" && options.Gateway.User != previousTag {
						t.Fatal("same user and origin changed attribution")
					}
					previousTag = options.Gateway.User
				}
			}
		}
	}
	var count int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM request_attempts WHERE connector_type='ai-sdk-gateway-v3'").Scan(&count); err != nil || count != 18 {
		t.Fatal(count, err)
	}
	mu.Lock()
	before := len(bodies)
	mu.Unlock()
	for _, body := range []string{`{"model":"provider/self","input":[1,2]}`, `{"model":"provider/self","input":"x","dimensions":2}`} {
		status, _ := f.post(t, "/v1/embeddings", body)
		if status != 400 {
			t.Fatal(status)
		}
	}
	status, _ := f.post(t, "/v1/chat/completions", `{"model":"provider/self","messages":[{"role":"developer","content":"x"}]}`)
	if status != 400 {
		t.Fatal(status)
	}
	mu.Lock()
	after := len(bodies)
	mu.Unlock()
	if before != after {
		t.Fatal("incompatible request dispatched")
	}
	var final int
	f.store.DB().QueryRow("SELECT count(*) FROM request_attempts WHERE connector_type='ai-sdk-gateway-v3'").Scan(&final)
	if final != count {
		t.Fatal("incompatible request reserved")
	}
}
