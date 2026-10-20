package egress_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestCloudflareVertexOpenAIRegionalAndGlobal(t *testing.T) {
	for _, endpoint := range []struct {
		name, origin, path string
	}{
		{"regional", "https://us-central1-aiplatform.googleapis.com", "/v1/projects/example-project/locations/us-central1/endpoints/openapi"},
		{"global", "https://aiplatform.googleapis.com", "/v1/projects/example-project/locations/global/endpoints/openapi"},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			const key = "vertex-access-token-private"
			const token = "vertex-gateway-token-private"
			const model = "google/gemini-example"
			const answer = `{"id":"reply","object":"chat.completion","created":1,"model":"google/gemini-example","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
			const chunk = `{"id":"reply","object":"chat.completion.chunk","created":1,"model":"google/gemini-example","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
			var calls atomic.Int32
			var mode atomic.Value
			mode.Store("json")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/gateway/custom-vertex"+endpoint.path+"/chat/completions" || r.URL.RawQuery != "" {
					t.Error("Vertex method, complete project/location path, or query changed")
				}
				if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("Cf-Aig-Authorization") != "Bearer "+token || r.Header.Get("X-Goog-Api-Key") != "" {
					t.Error("provider and gateway authentication were not kept separate")
				}
				if r.Header.Get("Cf-Aig-Max-Attempts") != "1" || r.Header.Get("Cf-Aig-No-Wholesale") != "true" {
					t.Error("gateway retry or billing fallback was enabled")
				}
				var input struct{ Model string }
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != model {
					t.Error("Vertex publisher-qualified model changed")
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode.Load().(string) {
				case "expired":
					w.WriteHeader(http.StatusUnauthorized)
					io.WriteString(w, `{"error":{"code":401,"message":"Access token expired","status":"UNAUTHENTICATED"}}`)
				case "reflect-error":
					w.Header().Set("X-Provider-Detail", key)
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprintf(w, `{"error":{"message":%q}}`, key+" "+token)
				case "reflect-json":
					io.WriteString(w, strings.ReplaceAll(answer, "Hello", token))
				case "sse", "reflect-sse":
					w.Header().Set("Content-Type", "text/event-stream")
					value := chunk
					if mode.Load().(string) == "reflect-sse" {
						value = strings.ReplaceAll(chunk, "Hello", key)
					}
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", value)
					w.(http.Flusher).Flush()
				default:
					io.WriteString(w, answer)
				}
			}))
			defer server.Close()
			// Only the fixture's trusted roots change; requests still traverse the
			// production DNS, exact-base routing, TLS and response guard boundary.
			previous := http.DefaultTransport
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = previous; transport.CloseIdleConnections() }()
			source := endpoint.origin + endpoint.path
			raw, err := json.Marshal([]map[string]string{{"source_base_url": source, "gateway_base_url": server.URL + "/gateway/custom-vertex" + endpoint.path, "token_env": "TOKEN", "credential_header": "Authorization"}})
			if err != nil {
				t.Fatal(err)
			}
			routes, err := config.ParseCloudflareRoutes(string(raw), func(string) (string, bool) { return token, true })
			if err != nil {
				t.Fatal(err)
			}
			stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, CloudflareRoutes: routes, Resolver: fakeResolver{[]netip.Addr{netip.MustParseAddr(publicIPv4)}}, RequestTimeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer stack.CloseIdleConnections()
			if err := stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://site.example", UserHost: "site.example", AdminHost: "admin.site.example", ListenAddr: "127.0.0.1:1"}); err != nil {
				t.Fatal(err)
			}
			local, err := backend.NewLocal(stack)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: local})
			if err != nil {
				t.Fatal(err)
			}
			for _, scenario := range []string{"json", "sse", "expired", "reflect-error", "reflect-json", "reflect-sse"} {
				t.Run(scenario, func(t *testing.T) {
					mode.Store(scenario)
					stream := scenario == "sse" || scenario == "reflect-sse"
					request, err := openai.DecodeChatRequest(strings.NewReader(fmt.Sprintf(`{"model":"public","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream)), 0)
					if err != nil {
						t.Fatal(err)
					}
					defer request.Clear()
					captured := false
					ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
						captured = true
						if strings.Contains(string(event.Bytes()), token) || strings.Contains(string(event.Bytes()), key) {
							t.Error("private credential entered raw diagnostics")
						}
					})
					sink := httptest.NewRecorder()
					before := calls.Load()
					result := adapter.Attempt(ctx, sink, openai.NewTarget(source, model, openai.NewCredential([]byte(key), nil)), request, "nbu_v3_fixture")
					if calls.Load() != before+1 {
						t.Fatalf("connector dispatches = %d; want 1; result=%+v", calls.Load()-before, result)
					}
					if strings.Contains(sink.Body.String(), key) || strings.Contains(sink.Body.String(), token) || sink.Header().Get("X-Provider-Detail") != "" {
						t.Fatal("credential reflection escaped to the caller")
					}
					if scenario == "json" || scenario == "sse" {
						if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 3 || result.Usage.OutputTokens != 2 || !strings.Contains(sink.Body.String(), "Hello") {
							t.Fatalf("valid response failed: %+v; %s", result, sink.Body.String())
						}
						if stream && !strings.Contains(sink.Body.String(), "[DONE]") {
							t.Fatal("SSE completion was lost")
						}
					} else if result.Success {
						t.Fatal("expired or secret-reflecting response succeeded")
					}
					if (scenario == "expired" || scenario == "reflect-error") && (result.UpstreamStatus != http.StatusUnauthorized || !captured) {
						t.Fatalf("401 status or diagnostic capture lost: %+v, captured=%t", result, captured)
					}
				})
			}
		})
	}
}
