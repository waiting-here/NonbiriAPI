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
	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gemini"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestCloudflareGeminiDiscoveryGenerationAndSSE(t *testing.T) {
	const key = "connector-provider-key-private"
	const token = "connector-gateway-token-private"
	const source = "https://native-provider.example/v1beta"
	var calls atomic.Int32
	var mode string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("key") != key || r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Cf-Aig-Authorization") != "Bearer "+token || r.Header.Get("Cf-Aig-No-Wholesale") != "true" {
			t.Error("connector authentication not adapted")
		}
		if !strings.HasPrefix(r.URL.Path, "/gateway/v1beta/models") {
			t.Error("connector suffix changed", r.URL.Path)
		}
		if mode == "invalid" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT"}}`)
			return
		}
		if mode == "reflect" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			io.WriteString(w, `{"message":"`+token+`"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"models":[{"name":"models/test-model","supportedGenerationMethods":["generateContent"]}]}`)
			return
		}
		body := `{"candidates":[{"index":0,"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`
		if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			if r.URL.Query().Get("alt") != "sse" {
				t.Error("SSE query lost")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", body)
			w.(http.Flusher).Flush()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	// The public stack still uses certificate verification. Only this sequential
	// fixture's trusted roots are supplied through the standard cloned transport.
	previous := http.DefaultTransport
	fixture := http.DefaultTransport.(*http.Transport).Clone()
	fixture.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = fixture
	defer func() { http.DefaultTransport = previous; fixture.CloseIdleConnections() }()
	raw, _ := json.Marshal([]map[string]string{{"source_base_url": source, "gateway_base_url": server.URL + "/gateway/v1beta", "token_env": "TOKEN", "credential_header": "X-Goog-Api-Key", "credential_query_parameter": "key"}})
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
	secret := func() *contract.ShortLivedSecret { return contract.NewShortLivedSecret([]byte(key), nil) }
	target := contract.NewTarget(contract.TypeGoogleGemini, source, "test-model")
	discovery := (gemini.ModelDiscoverer{}).Discover(context.Background(), contract.DiscoveryInput{Backend: local, Target: target, Credential: secret()})
	if !discovery.Succeeded() || len(discovery.Models) != 1 || discovery.Models[0].ID != "test-model" {
		t.Fatalf("discovery: %+v", discovery)
	}
	adapter, err := chatwire.New(local, chatwire.Protocol{Type: contract.TypeGoogleGemini, Header: "X-Goog-Api-Key", Compile: gemini.Compile, URL: gemini.URL, Decoder: gemini.NewDecoder})
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []bool{false, true} {
		request, err := openai.DecodeChatRequest(strings.NewReader(fmt.Sprintf(`{"model":"public","messages":[{"role":"user","content":"hello"}],"stream":%t,"stream_options":{"include_usage":true}}`, stream)), 0)
		if err != nil {
			t.Fatal(err)
		}
		sink := httptest.NewRecorder()
		result := adapter.Attempt(context.Background(), sink, target, secret(), request, contract.AttemptPolicy{})
		request.Clear()
		if !result.Success || !result.Usage.Present || result.Usage.UncachedInputTokens != 3 || result.Usage.OutputTokens != 2 || !strings.Contains(sink.Body.String(), "Hello") || strings.Contains(sink.Body.String(), token) {
			t.Fatalf("stream=%v result=%+v output=%s", stream, result, sink.Body.String())
		}
		if stream && !strings.Contains(sink.Body.String(), "[DONE]") {
			t.Fatal("missing protocol completion")
		}
	}
	for _, failure := range []string{"invalid", "reflect"} {
		mode = failure
		before := calls.Load()
		captured := false
		ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
			captured = true
			if strings.Contains(string(event.Bytes()), token) {
				t.Fatal("gateway token persisted")
			}
		})
		discovery = (gemini.ModelDiscoverer{}).Discover(ctx, contract.DiscoveryInput{Backend: local, Target: target, Credential: secret()})
		if discovery.Succeeded() || discovery.UpstreamStatus != 400 || calls.Load() != before+1 || !captured {
			t.Fatal("failure changed status/retried", discovery, calls.Load(), captured)
		}
	}
}
