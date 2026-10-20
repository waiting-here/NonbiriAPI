package egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const routeToken = "gateway-token-private-12345"
const routeKey = "provider-key-private-12345/+"
const routeSource = "https://provider.example/v1"

func configuredRoutes(t *testing.T, source, gateway, header, query string) []config.CloudflareRoute {
	t.Helper()
	raw, _ := json.Marshal([]map[string]string{{"source_base_url": source, "gateway_base_url": gateway, "token_env": "GATEWAY_TOKEN", "credential_header": header, "credential_query_parameter": query}})
	routes, err := config.ParseCloudflareRoutes(string(raw), func(string) (string, bool) { return routeToken, true })
	if err != nil {
		t.Fatal(err)
	}
	return routes
}
func routedClient(t *testing.T, handler http.HandlerFunc, change func(*StackOptions)) (*Client, *Stack, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) {
		o.CloudflareRoutes = configuredRoutes(t, routeSource, server.URL+"/gateway/v1", "X-Api-Key", "key")
		if change != nil {
			change(o)
		}
	})
	client, err := stack.NewClient(routeSource)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return client, stack, server
}
func routedRequest(ctx context.Context, path string) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, routeSource+path, nil)
	req.Header.Set("X-Api-Key", routeKey)
	return req
}
func TestCloudflareRoutePathQueryAndPrivateRequest(t *testing.T) {
	var calls atomic.Int32
	client, _, server := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.EscapedPath() != "/gateway/v1/models/a%2Fb:stream" || r.URL.RawQuery != "alt=sse&pageToken=a%2fb&key="+url.QueryEscape(routeKey) {
			t.Error("path/query changed", r.URL.EscapedPath(), r.URL.RawQuery)
		}
		if r.Header.Get("X-Api-Key") != "" || r.Header.Get("Cf-Aig-Authorization") != "Bearer "+routeToken {
			t.Error("private auth adaptation failed")
		}
		for name, want := range map[string]string{"Cf-Aig-Collect-Log": "false", "Cf-Aig-Collect-Log-Payload": "false", "Cf-Aig-Skip-Cache": "true", "Cf-Aig-Max-Attempts": "1", "Cf-Aig-No-Wholesale": "true"} {
			if r.Header.Get(name) != want {
				t.Error(name, "was overridden")
			}
		}
		if r.Header.Get("Cf-Aig-Byok-Alias") != "" || r.Header.Get("Cf-Cache-Ttl") != "" {
			t.Error("caller controls survived")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}, nil)
	req := routedRequest(context.Background(), "/models/a%2Fb:stream?alt=sse&pageToken=a%2fb")
	original := req.URL.String()
	req.Header.Set("Cf-Aig-Authorization", "caller-token")
	req.Header.Set("Cf-Aig-No-Wholesale", "false")
	req.Header.Set("Cf-Aig-Byok-Alias", "saved-key")
	req.Header.Set("Cf-Cache-Ttl", "60")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(data) != `{"ok":true}` || calls.Load() != 1 {
		t.Fatal(string(data), err, calls.Load())
	}
	if req.URL.String() != original || req.Header.Get("X-Api-Key") != routeKey || req.Header.Get("Cf-Aig-Authorization") != "caller-token" {
		t.Fatal("original request was mutated")
	}
	if resp.Request.URL.String() != original || resp.Request.Header.Get("X-Api-Key") != "" || strings.Contains(fmt.Sprint(resp.Request), server.URL) || strings.Contains(fmt.Sprint(resp.Request), routeToken) {
		t.Fatal("private request escaped")
	}
}

func TestCloudflareRouteSelectionAndDuplicateSources(t *testing.T) {
	var routed, direct atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		if r.Header.Get("Cf-Aig-Authorization") != "" || r.Header.Get("X-Api-Key") != routeKey {
			t.Error("unmatched endpoint was adapted")
		}
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	client, stack, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		routed.Add(1)
		if r.URL.Path != "/other/v2/models" || r.Header.Get("Authorization") != "Bearer second-provider-key" || r.URL.RawQuery != "" {
			t.Error("second route not preserved")
		}
		io.WriteString(w, `{}`)
	}, func(o *StackOptions) {
		o.AllowedOrigins = append(o.AllowedOrigins, upstream.URL)
		o.CloudflareRoutes = append(o.CloudflareRoutes, configuredRoutes(t, "https://second.example/v2", o.CloudflareRoutes[0].GatewayBaseURL[:len(o.CloudflareRoutes[0].GatewayBaseURL)-len("/gateway/v1")]+"/other/v2", "Authorization", "")...)
	})
	for _, base := range []string{routeSource + "/", routeSource + "/other", "https://provider.example/v10"} {
		c, err := stack.NewClient(base)
		if err != nil || c.route != nil {
			t.Fatal("partial source matched", base, err)
		}
	}
	second, err := stack.NewClient("https://SECOND.example:443/v2")
	if err != nil || second.route == nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, second.BaseURL()+"/models", nil)
	req.Header.Set("Authorization", "Bearer second-provider-key")
	resp, err := second.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	plain, err := stack.NewClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, upstream.URL+"/models", nil)
	req.Header.Set("X-Api-Key", routeKey)
	resp, err = plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if routed.Load() != 1 || direct.Load() != 1 || client.BaseURL() != routeSource {
		t.Fatal("routing counts")
	}
	options := StackOptions{CloudflareRoutes: configuredRoutes(t, routeSource, "https://gateway.example/private", "X-Api-Key", "")}
	options.CloudflareRoutes = append(options.CloudflareRoutes, configuredRoutes(t, "https://PROVIDER.example:443/v1", "https://gateway.example/other", "X-Api-Key", "")...)
	if _, err := NewStack(options); err == nil {
		t.Fatal("canonical duplicate source accepted")
	}
}

func TestCloudflareRejectsCredentialAndPathAmbiguity(t *testing.T) {
	var calls atomic.Int32
	client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }, nil)
	for _, test := range []struct {
		path   string
		values []string
	}{
		{"/models", nil}, {"/models", []string{""}}, {"/models", []string{"a", "b"}},
		{"/models?key=other", []string{routeKey}}, {"/models?%6bey=other", []string{routeKey}},
		{"/../models", []string{routeKey}}, {"/%2e%2e/models", []string{routeKey}},
	} {
		req := routedRequest(context.Background(), test.path)
		req.Header["X-Api-Key"] = test.values
		if _, err := client.Do(req); err == nil || strings.Contains(err.Error(), routeKey) {
			t.Fatal("invalid request accepted", test.path, err)
		}
	}
	req := routedRequest(context.Background(), "/models")
	req.URL.Path = "/v10/models"
	if _, err := client.Do(req); err == nil {
		t.Fatal("prefix collision accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request was sent")
	}
}

func TestCloudflareSourceAndTargetPolicyAndRedirect(t *testing.T) {
	var calls atomic.Int32
	client, stack, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "https://elsewhere.example/?key="+routeKey, 302)
	}, nil)
	resp, err := client.Do(routedRequest(context.Background(), "/models"))
	if !errors.Is(err, ErrRedirectBlocked) || resp == nil || resp.StatusCode != 302 || strings.Contains(resp.Header.Get("Location"), routeKey) || calls.Load() != 1 {
		t.Fatal("redirect boundary", resp, err)
	}
	stack.policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	if _, err := client.Do(routedRequest(context.Background(), "/models")); err == nil || ExecutionFailure(err) != FailurePlatform {
		t.Fatal("original blocked DNS bypassed", err)
	}
	if calls.Load() != 1 {
		t.Fatal("blocked source reached gateway")
	}
	stack.policy.resolver = publicResolver()
	stack.policy.mu.Lock()
	stack.policy.selfOrigins["https://provider.example:443"] = struct{}{}
	stack.policy.mu.Unlock()
	stack.policy.refusalDelay = func(context.Context) error { return nil }
	if _, err := client.Do(routedRequest(context.Background(), "/models")); err == nil {
		t.Fatal("original self origin bypassed")
	}
	for _, target := range []string{"https://127.0.0.1/private", "https://169.254.169.254/private"} {
		if _, err := NewStack(StackOptions{CloudflareRoutes: configuredRoutes(t, routeSource, target, "X-Api-Key", "")}); err == nil {
			t.Fatal("blocked gateway admitted")
		}
	}
}

type routeRoundTripper func(*http.Request) (*http.Response, error)

func (f routeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCloudflareTransportErrorCannotRetainPrivateURL(t *testing.T) {
	client, _, _ := routedClient(t, func(http.ResponseWriter, *http.Request) {}, nil)
	client.httpClient.Transport = routeRoundTripper(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "GET", URL: r.URL.String(), Err: fmt.Errorf("gateway %s credential %s", routeToken, routeKey)}
	})
	_, err := client.Do(routedRequest(context.Background(), "/models"))
	if err == nil || ExecutionFailure(err) != FailureNetwork {
		t.Fatal(err)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(fmt.Sprintf("%+v %#v", cause, cause), routeKey) || strings.Contains(fmt.Sprintf("%+v %#v", cause, cause), routeToken) {
			t.Fatal("private transport cause escaped")
		}
	}
}

func TestCloudflareResponseReflectionBeforeCapture(t *testing.T) {
	cases := []struct{ name, body string }{
		{"plain", `{"message":"` + routeKey + `"}`},
		{"gateway", `{"message":"` + routeToken + `"}`},
		{"query", `{"message":"` + url.QueryEscape(routeKey) + `"}`},
		{"mixed percent", `{"message":"provider%2dkey-private-12345%2F%2b"}`},
		{"JSON escapes", `{"message":"\u0067ateway-token-private-12345"}`},
		{"malformed JSON", `{"message":"\u0067ateway-token-private-12345"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Reflected", routeToken)
				w.Header().Set("X-Json", `{"token":"\u0067ateway-token-private-12345"}`)
				w.Header().Set("X-Query", url.QueryEscape(routeKey))
				w.WriteHeader(400)
				io.WriteString(w, tc.body)
			}, nil)
			ctx := headerfacts.WithResponse(context.Background(), "request", 1)
			captured := false
			ctx = upstreamerror.WithCapture(ctx, func(_ context.Context, event upstreamerror.Event) {
				captured = true
				if len(event.Bytes()) != 0 || !event.Unavailable() {
					t.Fatal("reflected diagnostic persisted", event)
				}
			})
			resp, err := client.Do(routedRequest(ctx, "/models"))
			if err != nil {
				t.Fatal(err)
			}
			if resp.Header.Get("X-Reflected") != "[redacted]" || resp.Header.Get("X-Query") != "[redacted]" || resp.Header.Get("X-Json") != "[redacted]" {
				t.Fatal("reflected header survived")
			}
			upstreamerror.InspectResponse(ctx, resp, func([]byte) { t.Fatal("unsafe envelope inspected") })
			resp.Body.Close()
			if !captured {
				t.Fatal("missing unavailable capture")
			}
			ctx = headerfacts.PrepareResponse(ctx, "request", 1)
			fact := headerfacts.Response(ctx, "request", 1)
			if fact.State() != "saved" {
				t.Fatal("missing header evidence")
			}
			view, decodeErr := headerfacts.DecodeCollection(fact.State(), fact.Compressed(), false)
			encoded, _ := json.Marshal(view)
			if decodeErr != nil || strings.Contains(string(encoded), routeToken) || strings.Contains(string(encoded), routeKey) {
				t.Fatal("header capture leaked", decodeErr)
			}

		})
	}
}

func TestCloudflareSSEIncrementalAndSemanticReflection(t *testing.T) {
	for _, reflectSecret := range []bool{false, true} {
		t.Run(fmt.Sprint(reflectSecret), func(t *testing.T) {
			continueStream := make(chan struct{})
			stopped := make(chan struct{})
			client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
				defer close(stopped)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"text\":\"gateway-token-\"}\r\n\r\n")
				w.(http.Flusher).Flush()
				select {
				case <-continueStream:
				case <-r.Context().Done():
					return
				}
				value := "safe completion"
				if reflectSecret {
					value = "private-12345"
				}
				io.WriteString(w, "data: {\"text\":\""+value+"\"}\n\n")
				w.(http.Flusher).Flush()
			}, nil)
			response, err := client.Do(routedRequest(context.Background(), "/stream?alt=sse"))
			if err != nil {
				t.Fatal(err)
			}
			first := make(chan string, 1)
			go func() {
				buf := make([]byte, 128)
				n, err := response.Body.Read(buf)
				if err != nil {
					first <- err.Error()
				} else {
					first <- string(buf[:n])
				}
			}()
			select {
			case value := <-first:
				if value != "data: {\"text\":\"gateway-token-\"}\n\n" {
					t.Fatal(value)
				}
			case <-time.After(time.Second):
				t.Fatal("stream waited for later event")
			}
			close(continueStream)
			rest, err := io.ReadAll(response.Body)
			response.Body.Close()
			if reflectSecret {
				if !errors.Is(err, errRouteReflection) || len(rest) != 0 {
					t.Fatal("split reflection escaped", string(rest), err)
				}
			} else if err != nil || !strings.Contains(string(rest), "safe completion") {
				t.Fatal(string(rest), err)
			}
			<-stopped
		})
	}
}
func TestCloudflareCancellationAndResponseLimit(t *testing.T) {
	stopped := make(chan struct{})
	client, stack, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"text\":\"ready\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := client.Do(routedRequest(ctx, "/stream"))
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); read <- err }()
	cancel()
	select {
	case err := <-read:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock response")
	}
	resp.Body.Close()
	<-stopped
	permit, err := stack.gate.Acquire(context.Background(), routeSource)
	if err != nil {
		t.Fatal(err)
	}
	permit.Release()
	limited, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 513)) }, func(o *StackOptions) { o.MaxResponseBytes = 512 })
	resp, err = limited.Do(routedRequest(context.Background(), "/models"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil || len(data) != 0 {
		t.Fatal("unbounded response", len(data), err)
	}
}

type countedRouteBody struct {
	source io.Reader
	read   atomic.Int64
}

func (b *countedRouteBody) Read(dst []byte) (int, error) {
	n, err := b.source.Read(dst)
	b.read.Add(int64(n))
	return n, err
}
func (*countedRouteBody) Close() error { return nil }
func TestCloudflareGuardHonorsConsumerResponseBudget(t *testing.T) {
	for _, tc := range []struct {
		stream bool
		limit  int64
	}{{false, 1}, {false, 128}, {true, 128}} {
		t.Run(fmt.Sprintf("%t/%d", tc.stream, tc.limit), func(t *testing.T) {
			stream := tc.stream
			client, _, _ := routedClient(t, func(http.ResponseWriter, *http.Request) {}, nil)
			payload := strings.Repeat("x", 64<<10)
			kind := "application/json"
			if stream {
				payload = strings.Repeat("data: "+strings.Repeat("x", 40)+"\n", 1000)
				kind = "text/event-stream"
			}
			body := &countedRouteBody{source: strings.NewReader(payload)}
			client.httpClient.Transport = routeRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: body}, nil
			})
			req := WithResponseLimits(routedRequest(context.Background(), "/models"), ResponseLimits{JSONBytes: tc.limit, StreamBytes: 2048, LineBytes: 64, EventBytes: 128})
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, 129))
			resp.Body.Close()
			if err == nil || len(data) != 0 {
				t.Fatal("oversized response emitted", len(data), err)
			}
			if stream {
				var limitErr *SSEEventTooLargeError
				if !errors.As(err, &limitErr) || limitErr.Limit != 128 || ExecutionFailure(err) != "" {
					t.Fatal("event limit became a transport failure", err)
				}
			} else {
				var limitErr *ResponseTooLargeError
				if !errors.As(err, &limitErr) || limitErr.Limit != tc.limit {
					t.Fatal("body limit type changed", err)
				}
			}
			maximum := tc.limit + 1
			if stream {
				maximum = 256
			}
			if body.read.Load() > maximum {
				t.Fatalf("consumer budget bypassed: read %d bytes, maximum %d", body.read.Load(), maximum)
			}
		})
	}
}
func TestCloudflareGuardAllowsHarmlessExtensionPaths(t *testing.T) {
	fields := map[string]any{}
	for i := 0; i < 150; i++ {
		fields[fmt.Sprintf("field_%d", i)] = fmt.Sprintf("harmless_%d", i)
	}
	fields[strings.Repeat("field", 1000)] = "harmless long field"
	payload, _ := json.Marshal(map[string]any{"models": []any{map[string]any{"name": "models/one", "extensions": fields}}, "tools": []any{map[string]any{"name": "tool", "arguments": fields}}})
	client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	}, nil)
	resp, err := client.Do(routedRequest(context.Background(), "/models"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(data) != string(payload) {
		t.Fatal("harmless JSON was rejected", err)
	}
}

func TestCloudflareEncodedAndBearerSecretForms(t *testing.T) {
	for _, value := range []string{
		`{"key":"\u0070rovider-key-private-12345/+"}`,
		`{"key":"provider%2dkey-private-12345/+"}`,
		`{"key":"provider-key-private-12345%2f%2b"}`,
	} {
		guard := newRouteSecrets(routeKey)
		if !guard.contains(value) {
			t.Fatal("encoded reflection missed", value)
		}
	}
	client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":"raw-provider-bearer-key"}`)
	}, nil)
	client.route.credentialHeader = "Authorization"
	client.route.credentialQuery = ""
	req := routedRequest(context.Background(), "/models")
	req.Header.Set("Authorization", "Bearer raw-provider-bearer-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, errRouteReflection) || len(data) != 0 {
		t.Fatal("raw bearer reflection escaped", err)
	}
}

func TestCloudflareSSEPrefixStateUsesEventBudget(t *testing.T) {
	client, _, _ := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 30; i++ {
			fmt.Fprintf(w, "data: {\"extra_%d\":\"g\"}\n\n", i)
		}
	}, nil)
	req := WithResponseLimits(routedRequest(context.Background(), "/stream"), ResponseLimits{StreamBytes: 4096, LineBytes: 128, EventBytes: 256})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	guard := resp.Body.(*routeResponseBody).secrets
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	var limitErr *routeGuardStateTooLargeError
	if !errors.As(err, &limitErr) || limitErr.Limit != 256 || guard.stateBytes > 256 || len(data) > 256 {
		t.Fatal("prefix state was not bounded by event budget", len(data), guard.stateBytes, err)
	}
}
func TestCloudflareSafeLimitErrorClassification(t *testing.T) {
	for _, failure := range []error{&SSELineTooLargeError{Limit: 64}, &SSEEventTooLargeError{Limit: 128}} {
		safe := safeRouteError(failure)
		if fmt.Sprintf("%T", safe) != fmt.Sprintf("%T", failure) || ExecutionFailure(safe) != "" {
			t.Fatal("protocol limit became network failure", safe)
		}
	}
}

func TestCloudflareSSEHarmlessPrefixSuffixDoesNotAccumulate(t *testing.T) {
	guard := newRouteSecrets(routeToken)
	guard.stateLimit = 128
	for range 30 {
		payload := []byte(`{"text":"` + strings.Repeat("z", 80) + `g"}`)
		if guard.inspectJSON(payload) {
			t.Fatal("harmless suffix retained unrelated history", guard.failure)
		}
	}
	if guard.stateBytes > 80 {
		t.Fatal("retained state exceeds actual prefix need", guard.stateBytes)
	}
	guard = newRouteSecrets(routeToken)
	if guard.inspectJSON([]byte(`{"text":"gateway-token-%7"}`)) || !guard.inspectJSON([]byte(`{"text":"0rivate-12345"}`)) {
		t.Fatal("split percent escape lost semantic prefix")
	}
}
