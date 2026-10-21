package egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

const workerTestToken = "worker-private-token-01234567890123456789"

func workerClient(t *testing.T, handler http.HandlerFunc, change func(*StackOptions)) (*Client, *Stack, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(workerResponseHeader, "upstream")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	worker, err := config.ParseCloudflareWorker(server.URL+"/private-entry", workerTestToken)
	if err != nil {
		t.Fatal(err)
	}
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) {
		o.CloudflareWorker = worker
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

type workerDialer map[string]string

func (d workerDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if replacement := d[address]; replacement != "" {
		address = replacement
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestCloudflareWorkerMixedModelsUseTheirOwnTransport(t *testing.T) {
	var workerCalls, directCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		if r.Host != "one.example:80" || r.Header.Get("Authorization") != "Bearer "+routeKey || r.Header.Get(workerTokenHeader) != "" || r.Header.Get(workerTargetHeader) != "" {
			t.Error("direct request was proxied or changed")
		}
		io.WriteString(w, `{"path":"direct"}`)
	}))
	defer source.Close()
	_, stack, worker := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
		workerCalls.Add(1)
		target, err := url.Parse(r.Header.Get(workerTargetHeader))
		if err != nil || (target.Host != "one.example:80" && target.Host != "two.example:443") || target.EscapedPath() != "/v1/a%2Fb" || target.RawQuery != "page=a%2fb" || r.URL.Path != "/private-entry" || r.URL.RawQuery != "" || r.Header.Get(workerTokenHeader) != workerTestToken || r.Header.Get("Authorization") != "Bearer "+routeKey || r.Header.Get("X-Nonbiri-Proxy-Forged") != "" || r.Header.Get("Cf-Aig-Authorization") != "" {
			t.Error("Worker path/query/credentials changed")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"input":"hello"}` {
			t.Error("request body changed")
		}
		io.WriteString(w, `{"path":"worker"}`)
	}, func(o *StackOptions) {
		o.Resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
		o.Dialer = workerDialer{"93.184.216.34:80": strings.TrimPrefix(source.URL, "http://")}
		o.CloudflareRoutes = configuredRoutes(t, "http://one.example/v1", "https://unused-legacy.example/private", "Authorization", "")
	})
	var wg sync.WaitGroup
	for index := 0; index < 12; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			enabled := index%3 != 0
			base := "http://one.example/v1"
			if index%3 == 2 {
				base = "https://two.example/v1"
			}
			client, err := stack.NewClient(base)
			if err != nil {
				t.Error(err)
				return
			}
			req, _ := http.NewRequestWithContext(WithCloudflareProxy(t.Context(), enabled), "POST", base+"/a%2Fb?page=a%2fb", strings.NewReader(`{"input":"hello"}`))
			req.Header.Set("Authorization", "Bearer "+routeKey)
			req.Header.Set(workerTokenHeader, "forged")
			req.Header.Set(workerTargetHeader, "https://forged.example")
			req.Header.Set("X-Nonbiri-Proxy-Forged", "bad")
			req.Header.Set("Cf-Aig-Authorization", "forged")
			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := `{"path":"worker"}`
			if !enabled {
				want = `{"path":"direct"}`
			}
			if err != nil || string(data) != want {
				t.Error(string(data), err)
			}
			if req.Header.Get(workerTokenHeader) != "forged" {
				t.Error("original request mutated")
			}
			if enabled && (resp.Request.Header.Get(workerTokenHeader) != "" || resp.Request.Header.Get("Authorization") != "" || strings.Contains(fmt.Sprint(resp.Request), worker.URL)) {
				t.Error("private request escaped")
			}
		}(index)
	}
	wg.Wait()
	if workerCalls.Load() != 8 || directCalls.Load() != 4 {
		t.Fatal(workerCalls.Load(), directCalls.Load())
	}
	status := stack.CloudflareStatus()
	if status.Mode != "worker" || len(status.Routes) != 1 || status.WorkerOrigin != worker.URL {
		t.Fatal(status)
	}
}

func TestCloudflareWorkerFailureIsPlatformAndNeverFallsBack(t *testing.T) {
	for _, marker := range []string{"configuration", "authentication", "target", "redirect", "upstream", "method", "unknown", ""} {
		t.Run(marker, func(t *testing.T) {
			var calls atomic.Int32
			client, stack, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header()[workerErrorHeader] = []string{marker}
				w.Header().Set(workerTokenHeader, workerTestToken)
				w.WriteHeader(402)
				io.WriteString(w, `{"error":{"message":"credit balance is too low","type":"invalid_request_error"}}`)
			}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
			ctx := headerfacts.WithResponse(t.Context(), "worker-failure", 1)
			resp, err := client.Do(routedRequest(ctx, "/models"))
			if resp != nil || err == nil || ExecutionFailure(err) != FailurePlatform || calls.Load() != 1 {
				t.Fatal(resp, err, calls.Load())
			}
			ctx = headerfacts.PrepareResponse(ctx, "worker-failure", 1)
			fact := headerfacts.Response(ctx, "worker-failure", 1)
			if fact.State() == "saved" {
				t.Fatal("internal failure header captured")
			}
			budget, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			permit, err := stack.gate.Acquire(budget, routeSource)
			if err != nil {
				t.Fatal("permit leaked", err)
			}
			permit.Release()
		})
	}
}

func TestCloudflareWorkerSourcePolicyAndNoIPFallback(t *testing.T) {
	var calls atomic.Int32
	client, stack, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{}`) }, nil)
	stack.policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	if _, err := client.Do(routedRequest(t.Context(), "/models")); err == nil || ExecutionFailure(err) != FailurePlatform {
		t.Fatal(err)
	}
	stack.policy.resolver = publicResolver()
	ipClient, err := stack.NewClient("https://93.184.216.34/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", ipClient.BaseURL()+"/models", nil)
	if _, err := ipClient.Do(req); err == nil || ExecutionFailure(err) != FailurePlatform {
		t.Fatal("IP target accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatal("blocked source reached Worker")
	}
	if _, err := NewStack(StackOptions{CloudflareWorker: mustWorkerConfig(t, "https://169.254.169.254/private")}); err == nil {
		t.Fatal("private Worker accepted")
	}
}
func mustWorkerConfig(t *testing.T, target string) config.CloudflareWorker {
	t.Helper()
	worker, err := config.ParseCloudflareWorker(target, workerTestToken)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestCloudflareWorkerReflectionAndInternalHeaders(t *testing.T) {
	for _, credential := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		t.Run(credential, func(t *testing.T) {
			client, _, server := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Nonbiri-Proxy-Extra", "internal")
				w.Header().Set("X-Reflected", workerTestToken)
				io.WriteString(w, `{"message":"provider-key-private-12345%2F%2B"}`)
			}, nil)
			req := routedRequest(t.Context(), "/models")
			req.Header.Del("X-Api-Key")
			req.Header.Set(credential, routeKey)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !errors.Is(err, errRouteReflection) || len(data) != 0 || resp.Header.Get("X-Reflected") != "[redacted]" || resp.Header.Get("X-Nonbiri-Proxy-Extra") != "" {
				t.Fatal("reflection escaped", err)
			}
			client.httpClient.Transport = routeRoundTripper(func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("%s %s %s", server.URL, workerTestToken, routeKey)
			})
			_, err = client.Do(req)
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(fmt.Sprintf("%+v %#v", cause, cause), workerTestToken) || strings.Contains(fmt.Sprint(cause), routeKey) || strings.Contains(fmt.Sprint(cause), server.URL) {
					t.Fatal("private transport error escaped")
				}
			}
		})
	}
}

func TestCloudflareWorkerStreamingCancellationAndLimits(t *testing.T) {
	stopped := make(chan struct{})
	client, stack, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"text\":\"ready\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp, err := client.Do(routedRequest(ctx, "/stream"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, err := resp.Body.Read(buf)
	if err != nil || string(buf[:n]) != "data: {\"text\":\"ready\"}\n\n" {
		t.Fatal("stream buffered", err)
	}
	cancel()
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream cancellation lost")
	}
	budget, done := context.WithTimeout(t.Context(), time.Second)
	defer done()
	permit, err := stack.gate.Acquire(budget, routeSource)
	if err != nil {
		t.Fatal(err)
	}
	permit.Release()
	limited, _, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": strings.Repeat("x", 1024)})
	}, nil)
	req := WithResponseLimits(routedRequest(t.Context(), "/models"), ResponseLimits{JSONBytes: 64, StreamBytes: 128, LineBytes: 64, EventBytes: 64})
	resp, err = limited.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	var size *ResponseTooLargeError
	if !errors.As(err, &size) || size.Limit != 64 || len(body) != 0 {
		t.Fatal("budget bypassed", len(body), err)
	}
}

func TestCloudflareWorkerPrivatePayloadReflection(t *testing.T) {
	for _, kind := range []string{"token", "URL"} {
		t.Run(kind, func(t *testing.T) {
			client, _, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
				value := r.Header.Get(workerTokenHeader)
				if kind == "URL" {
					value = "https://" + r.Host + r.URL.EscapedPath()
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"message": url.QueryEscape(value)})
			}, nil)
			response, err := client.Do(routedRequest(t.Context(), "/models"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if !errors.Is(err, errRouteReflection) || len(body) != 0 {
				t.Fatal("private proxy data reflected", err)
			}
		})
	}
}

func TestCloudflareWorkerImageProfileSelection(t *testing.T) {
	var directCalls, workerCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directCalls.Add(1); io.WriteString(w, `{}`) }))
	defer source.Close()
	_, stack, worker := workerClient(t, func(w http.ResponseWriter, r *http.Request) { workerCalls.Add(1); io.WriteString(w, `{}`) }, func(o *StackOptions) {
		o.Dialer = workerDialer{"93.184.216.34:80": strings.TrimPrefix(source.URL, "http://")}
	})
	const base = "http://images.example/v1"
	client, err := stack.NewImageClient(base, ImageJSON)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport.(*http.Transport).TLSClientConfig = worker.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	for _, enabled := range []bool{true, false} {
		req, _ := http.NewRequestWithContext(WithCloudflareProxy(t.Context(), enabled), "POST", base+"/images/generations", strings.NewReader(`{}`))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if directCalls.Load() != 1 || workerCalls.Load() != 1 {
		t.Fatal(directCalls.Load(), workerCalls.Load())
	}
	direct, err := stack.newImageClient(base, ImageJSON, false)
	if err != nil {
		t.Fatal(err)
	}
	transport := direct.httpClient.Transport.(*http.Transport)
	if direct.timeout != 24*time.Hour || direct.maxResponseBytes != 96<<20 || !direct.disableReplay || !transport.DisableKeepAlives || transport.ForceAttemptHTTP2 {
		t.Fatal("direct image profile changed")
	}
}

func TestCloudflareWorkerRequiresOneUpstreamConfirmation(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"edge"}, {"upstream", "upstream"}, {"upstream, upstream"}} {
		t.Run(fmt.Sprint(values), func(t *testing.T) {
			client, _, _ := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
				delete(w.Header(), workerResponseHeader)
				if values != nil {
					w.Header()[workerResponseHeader] = values
				}
				w.WriteHeader(503)
				io.WriteString(w, `{"error":"edge unavailable"}`)
			}, nil)
			resp, err := client.Do(routedRequest(t.Context(), "/models"))
			if resp != nil || ExecutionFailure(err) != FailurePlatform {
				t.Fatal("unconfirmed response treated as upstream", resp, err)
			}
		})
	}
}

func TestCloudflareWorkerTransportFailuresStayNeutral(t *testing.T) {
	for _, kind := range []string{"connect", "TLS", "header timeout", "body timeout", "body interruption", "caller cancel", "invalid gzip"} {
		t.Run(kind, func(t *testing.T) {
			client, stack, server := workerClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "header timeout":
					<-r.Context().Done()
				case "body timeout":
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "body interruption":
					w.Header().Set("Content-Length", "1000")
					io.WriteString(w, `{"partial":`)
				case "invalid gzip":
					w.Header().Set("Content-Encoding", "gzip")
					io.WriteString(w, "invalid gzip content")
				default:
					io.WriteString(w, `{}`)
				}
			}, func(o *StackOptions) {
				o.RequestTimeout = 75 * time.Millisecond
				o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1}
			})
			if kind == "connect" {
				server.Close()
			}
			if kind == "TLS" {
				client.httpClient.Transport.(*http.Transport).TLSClientConfig = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "caller cancel" {
				cancel()
			}
			resp, err := client.Do(routedRequest(ctx, "/models"))
			if err == nil && resp != nil {
				_, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			want := FailurePlatform
			if kind == "caller cancel" {
				want = FailureCanceled
				if !errors.Is(err, context.Canceled) {
					t.Fatal("caller cancellation identity lost", err)
				}
			}
			if kind == "invalid gzip" {
				if err == nil || ExecutionFailure(err) == FailurePlatform {
					t.Fatal("complete upstream encoding error became proxy failure", err)
				}
			} else if ExecutionFailure(err) != want {
				t.Fatal("transport penalized upstream key", kind, err, ExecutionFailure(err))
			}
			budget, done := context.WithTimeout(t.Context(), time.Second)
			defer done()
			permit, err := stack.gate.Acquire(budget, routeSource)
			if err != nil {
				t.Fatal("permit leaked", err)
			}
			permit.Release()
		})
	}
}
