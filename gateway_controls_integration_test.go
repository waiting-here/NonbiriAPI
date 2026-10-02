package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func TestGatewayHTTPPreservesModelControlsBeforeAccounting(t *testing.T) {
	var calls atomic.Int32
	f := newEmbeddingHTTPFixtureWithConfig(t, func(cfg *config.Config, base string) {
		var err error
		cfg.GatewayModels, err = gatewaypolicy.Parse(fmt.Sprintf(`{"models":[{"base_url":%q,"model":"private-model","adapter":"openai_responses","efforts":["max"],"max_output_tokens":128000,"storage":"openai"}]}`, base))
		if err != nil {
			t.Fatal(err)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Budget  int64                                 `json:"maxOutputTokens"`
			Options map[string]map[string]json.RawMessage `json:"providerOptions"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Budget != 128000 || string(body.Options["openai"]["reasoningEffort"]) != `"max"` || string(body.Options["openai"]["store"]) != "false" || string(body.Options["openai"]["forceReasoning"]) != "true" {
			t.Error("budget or provider controls did not reach the native wire")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path != "/v1/language-model" || r.Header.Get("ai-language-model-specification-version") != "3" {
			t.Error("wrong native protocol")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"finishReason":{"unified":"stop"},"usage":{"inputTokens":{"total":3,"noCache":3,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":2}}}`)
	})
	f.exec(t, "UPDATE endpoint_keys SET force_store_false=0")
	f.exec(t, "UPDATE models SET flatten_tool_calls=0")
	f.exec(t, "UPDATE charity_models SET flatten_tool_calls=0")
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	for _, model := range []string{"provider/self", "[公益]provider/per_request", "[公益]provider/per_token"} {
		raw := `{"model":"` + model + `","messages":[{"role":"user","content":"PRIVATE_PROMPT"}],"max_completion_tokens":128000,"reasoning_effort":"max","store":false}`
		status, out := f.post(t, "/v1/chat/completions", raw)
		if status != 200 {
			t.Fatalf("%s: %d %s", model, status, out)
		}
		for _, bad := range []string{
			strings.Replace(raw, `"max_completion_tokens":128000`, `"max_completion_tokens":128000,"max_tokens":64`, 1),
			strings.Replace(raw, `"reasoning_effort":"max"`, `"reasoning_effort":"xhigh"`, 1),
			strings.Replace(raw, `"store":false`, `"store":"false"`, 1),
			strings.Replace(raw, `"store":false`, `"store":false,"PRIVATE_UNKNOWN":1`, 1),
		} {
			status, out = f.post(t, "/v1/chat/completions", bad)
			if status != 400 || !strings.Contains(string(out), "invalid_request") || strings.Contains(string(out), "PRIVATE_") {
				t.Fatalf("bad admission: %d %s", status, out)
			}
		}
	}
	var attempts int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM request_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || attempts != 3 {
		t.Fatalf("rejection reserved or dispatched: calls=%d attempts=%d", calls.Load(), attempts)
	}
}
