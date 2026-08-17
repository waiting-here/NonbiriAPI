package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type adapterResolver struct{}

func (adapterResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

func adapterStack(t *testing.T, allowed []string, mutate func(*egress.StackOptions)) *egress.Stack {
	t.Helper()
	options := egress.StackOptions{
		AllowedOrigins: allowed,
		Resolver:       adapterResolver{},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	}
	if mutate != nil {
		mutate(&options)
	}
	stack, err := egress.NewStack(options)
	if err != nil {
		t.Fatalf("NewStack: %v", err)
	}
	cfg := &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  "127.0.0.1:1",
	}
	if err := stack.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatalf("AddSelfOrigins: %v", err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	return stack
}

func adapterForServer(t *testing.T, serverURL string, configMutate func(*AdapterConfig), stackMutate func(*egress.StackOptions)) *Adapter {
	t.Helper()
	stack := adapterStack(t, []string{serverURL}, stackMutate)
	adapterConfig := AdapterConfig{Stack: stack}
	if configMutate != nil {
		configMutate(&adapterConfig)
	}
	adapter, err := NewAdapter(adapterConfig)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return adapter
}

func decodeAdapterRequest(t *testing.T, body string) *ChatRequest {
	t.Helper()
	request, err := DecodeChatRequest(strings.NewReader(body), MaxRequestBodyBytes)
	if err != nil {
		t.Fatalf("DecodeChatRequest: %v", err)
	}
	t.Cleanup(request.Clear)
	return request
}

func testTarget(serverURL string, secret, ciphertext []byte) Target {
	return NewTarget(serverURL, "upstream/model", NewCredential(secret, ciphertext))
}

func TestProtocolValuesRedactFormattingAndStructuredLogs(t *testing.T) {
	const secretText = "FORMAT-SECRET-MARKER"
	const ciphertextText = "FORMAT-CIPHERTEXT-MARKER"
	request := decodeAdapterRequest(t, `{"model":"p/m","messages":[{"content":"FORMAT-PROMPT-MARKER"}]}`)
	target := testTarget("https://upstream.example", []byte(secretText), []byte(ciphertextText))
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("values", "target", target, "credential", target.credential, "request", request)
	formatted := fmt.Sprintf("%v %#v %+v", target, target.credential, request)
	combined := logs.String() + formatted
	for _, forbidden := range []string{secretText, ciphertextText, "FORMAT-PROMPT-MARKER", "upstream.example"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("format/log sink leaked %q: %s", forbidden, combined)
		}
	}
	target.credential.clear()
}

func TestAdapterNonStreamRequestAuthorityAndSafeResponseHeaders(t *testing.T) {
	const secretText = "sk-upstream-secret-marker"
	const ciphertextText = "nbsec:v1:ciphertext-marker"
	var upstreamBody []byte
	var upstreamHeader http.Header
	var upstreamHost, upstreamPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamPath = request.URL.Path
		upstreamHost = request.Host
		upstreamHeader = request.Header.Clone()
		upstreamBody, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Set-Cookie", "upstream=secret")
		writer.Header().Set("Location", "https://unsafe.example")
		writer.Header().Set("Connection", "keep-alive")
		writer.Header().Set("X-Upstream-Secret", secretText)
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer server.Close()

	adapter := adapterForServer(t, server.URL, nil, nil)
	request := decodeAdapterRequest(t, `{"model":"platform/model","messages":[{"role":"user","content":"private prompt"}],"vendor":{"x":1},"safety_identifier":"forged"}`)
	secret := []byte(secretText)
	ciphertext := []byte(ciphertextText)
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder, testTarget(server.URL+"/mount/", secret, ciphertext), request, "nbu_authoritative")
	if !result.Success || !result.Committed || result.Failure != FailureNone || result.ClientStatus != http.StatusOK {
		t.Fatalf("result = %+v body=%s", result, recorder.Body.String())
	}
	if result.Usage.TotalTokens != 5 || !result.Usage.Present {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if upstreamPath != "/mount/v1/chat/completions" || upstreamHost != strings.TrimPrefix(server.URL, "http://") {
		t.Fatalf("upstream path=%q host=%q", upstreamPath, upstreamHost)
	}
	if upstreamHeader.Get("Authorization") != "Bearer "+secretText || upstreamHeader.Get("Content-Type") != "application/json" || upstreamHeader.Get("Accept") != "application/json" {
		t.Fatalf("upstream headers = %v", upstreamHeader)
	}
	for _, forbidden := range []string{"Cookie", "X-Forwarded-For", "Forwarded", "Proxy-Authorization"} {
		if upstreamHeader.Get(forbidden) != "" {
			t.Errorf("unexpected forwarded header %s=%q", forbidden, upstreamHeader.Get(forbidden))
		}
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(upstreamBody, &sent); err != nil {
		t.Fatalf("sent body: %v %s", err, upstreamBody)
	}
	var model, safety string
	_ = json.Unmarshal(sent["model"], &model)
	_ = json.Unmarshal(sent["safety_identifier"], &safety)
	if model != "upstream/model" || safety != "nbu_authoritative" || !bytes.Contains(sent["vendor"], []byte(`"x":1`)) || !bytes.Contains(sent["messages"], []byte("private prompt")) {
		t.Fatalf("sent body authority/passthrough failed: %s", upstreamBody)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("response headers = %v", recorder.Header())
	}
	for _, forbidden := range []string{"Set-Cookie", "Location", "Connection", "Content-Encoding", "X-Upstream-Secret"} {
		if recorder.Header().Get(forbidden) != "" {
			t.Errorf("unsafe upstream header escaped: %s=%q", forbidden, recorder.Header().Get(forbidden))
		}
	}
	if !bytes.Equal(recorder.Body.Bytes(), []byte(validCompletion)) {
		t.Fatalf("response body changed: %s", recorder.Body.String())
	}
	for i, value := range secret {
		if value != 0 {
			t.Fatalf("secret byte %d was not cleared", i)
		}
	}
	for i, value := range ciphertext {
		if value != 0 {
			t.Fatalf("ciphertext byte %d was not cleared", i)
		}
	}
}

func TestAdapterNonStreamRejectsStatusProtocolTruncationAndSensitiveReflection(t *testing.T) {
	const secretText = "sk-never-leak-status"
	const ciphertextText = "nbsec:v1:never-leak-ciphertext"
	tests := []struct {
		name     string
		status   int
		content  string
		body     string
		length   string
		maxBody  int64
		wantDiag string
	}{
		{
			name: "non-2xx raw body", status: http.StatusServiceUnavailable, content: "text/plain",
			body:     "failure " + secretText + " " + ciphertextText + " https://private.example/request-content",
			wantDiag: "HTTP 503",
		},
		{name: "bad JSON", status: http.StatusOK, content: "application/json", body: `{`, wantDiag: "invalid"},
		{name: "missing shape", status: http.StatusOK, content: "application/json", body: `{"choices":[]}`, wantDiag: "invalid"},
		{name: "wrong content type", status: http.StatusOK, content: "text/html", body: validCompletion, wantDiag: "content type"},
		{name: "declared truncation", status: http.StatusOK, content: "application/json", body: `{"id":"partial"}`, length: "500", wantDiag: "truncated"},
		{name: "body limit", status: http.StatusOK, content: "application/json", body: validCompletion + strings.Repeat(" ", 512), maxBody: 128, wantDiag: "limit"},
		{name: "plaintext reflection", status: http.StatusOK, content: "application/json", body: strings.Replace(validCompletion, `"ok"`, `"`+secretText+`"`, 1), wantDiag: "rejected"},
		{name: "ciphertext reflection", status: http.StatusOK, content: "application/json", body: strings.Replace(validCompletion, `"ok"`, `"`+ciphertextText+`"`, 1), wantDiag: "rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.content != "" {
					writer.Header().Set("Content-Type", test.content)
				}
				if test.length != "" {
					writer.Header().Set("Content-Length", test.length)
				}
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, func(config *AdapterConfig) {
				if test.maxBody != 0 {
					config.MaxJSONResponseBytes = test.maxBody
				}
			}, nil)
			request := decodeAdapterRequest(t, `{"model":"platform/model","messages":[]}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder,
				testTarget(server.URL, []byte(secretText), []byte(ciphertextText)), request, "nbu_safe")
			if result.Success || result.Committed || result.Failure != FailureUpstream {
				t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
			}
			if !strings.Contains(result.Diagnostic, test.wantDiag) {
				t.Fatalf("diagnostic=%q want fragment %q", result.Diagnostic, test.wantDiag)
			}
			combined := result.Diagnostic + recorder.Body.String()
			for _, forbidden := range []string{secretText, ciphertextText, "private.example", "request-content", test.body} {
				if forbidden != "" && strings.Contains(combined, forbidden) {
					t.Fatalf("failure leaked %q: %s", forbidden, combined)
				}
			}
		})
	}
}

func TestAdapterRedirectAndEnvironmentProxyStayInsideEgress(t *testing.T) {
	var redirectHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirectHits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget.URL+request.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	adapter := adapterForServer(t, redirector.URL, nil, nil)
	request := decodeAdapterRequest(t, `{"model":"p/m"}`)
	result := adapter.Attempt(context.Background(), httptest.NewRecorder(), testTarget(redirector.URL, []byte("sk"), []byte("cipher")), request, "nbu_safe")
	if result.Failure != FailureUpstream || !strings.Contains(result.Diagnostic, "redirect") || redirectHits.Load() != 0 {
		t.Fatalf("redirect result=%+v target_hits=%d", result, redirectHits.Load())
	}

	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		http.Error(writer, "proxy", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		directHits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer direct.Close()
	directAdapter := adapterForServer(t, direct.URL, nil, nil)
	directResult := directAdapter.Attempt(context.Background(), httptest.NewRecorder(), testTarget(direct.URL, []byte("sk"), []byte("cipher")), request, "nbu_safe")
	if !directResult.Success || directHits.Load() != 1 || proxyHits.Load() != 0 {
		t.Fatalf("direct result=%+v direct=%d proxy=%d", directResult, directHits.Load(), proxyHits.Load())
	}
}

func TestAdapterRejectsCustomHostThroughEgressClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if net.ParseIP(strings.Split(request.Host, ":")[0]) == nil && !strings.HasPrefix(request.Host, "127.0.0.1:") {
			t.Errorf("unexpected Host %q", request.Host)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	request := decodeAdapterRequest(t, `{"model":"p/m","host":"evil.example\r\nInjected: yes"}`)
	result := adapter.Attempt(context.Background(), httptest.NewRecorder(), testTarget(server.URL, []byte("sk"), []byte("cipher")), request, "nbu_safe")
	if !result.Success {
		t.Fatalf("body field should not affect Host: %+v", result)
	}
}

func TestAdapterNonStreamClientCancelDuringBodyRead(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var startOnce, cancelOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		startOnce.Do(func() { close(started) })
		select {
		case <-request.Context().Done():
			cancelOnce.Do(func() { close(canceled) })
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	adapter := adapterForServer(t, server.URL, nil, func(options *egress.StackOptions) {
		options.RequestTimeout = 30 * time.Second
		options.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	request := decodeAdapterRequest(t, `{"model":"p/m"}`)
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan AttemptResult, 1)
	go func() {
		resultChannel <- adapter.Attempt(ctx, httptest.NewRecorder(),
			testTarget(server.URL, []byte("sk-cancel-body"), []byte("cipher-cancel-body")), request, "nbu_safe")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("non-stream response body did not start")
	}
	cancel()
	select {
	case result := <-resultChannel:
		if result.Failure != FailureCanceled || result.Committed || result.Success {
			t.Fatalf("cancel result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("non-stream cancellation did not return")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("non-stream upstream did not observe cancellation")
	}
}

func TestAdapterConcurrentAttemptsSharePerEndpointGate(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, func(options *egress.StackOptions) {
		options.Concurrency = egress.ConcurrencyLimits{Global: 2, PerEndpoint: 1}
	})
	request := decodeAdapterRequest(t, `{"model":"p/m"}`)
	const attempts = 20
	results := make(chan AttemptResult, attempts)
	var workers sync.WaitGroup
	for i := 0; i < attempts; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			results <- adapter.Attempt(context.Background(), httptest.NewRecorder(),
				testTarget(server.URL, []byte(fmt.Sprintf("sk-%d", index)), []byte(fmt.Sprintf("cipher-%d", index))), request, "nbu_safe")
		}(i)
	}
	workers.Wait()
	close(results)
	for result := range results {
		if !result.Success {
			t.Fatalf("concurrent result=%+v", result)
		}
	}
	if maximum.Load() != 1 || active.Load() != 0 {
		t.Fatalf("per-endpoint gate maximum=%d active=%d", maximum.Load(), active.Load())
	}
}

func TestAdapterTimeoutCancelsUpstreamAndReleasesPermit(t *testing.T) {
	canceled := make(chan struct{})
	release := make(chan struct{})
	var cancelOnce sync.Once
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if calls.Add(1) == 1 {
			select {
			case <-request.Context().Done():
				cancelOnce.Do(func() { close(canceled) })
			case <-release:
			}
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	adapter := adapterForServer(t, server.URL, nil, func(options *egress.StackOptions) {
		options.RequestTimeout = 50 * time.Millisecond
		options.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	request := decodeAdapterRequest(t, `{"model":"p/m"}`)
	result := adapter.Attempt(context.Background(), httptest.NewRecorder(),
		testTarget(server.URL, []byte("sk-timeout"), []byte("cipher-timeout")), request, "nbu_safe")
	if result.Failure != FailureUpstream || result.Committed || !strings.Contains(result.Diagnostic, "timed out") {
		t.Fatalf("timeout result=%+v", result)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe timeout cancellation")
	}
	fast := adapter.Attempt(context.Background(), httptest.NewRecorder(),
		testTarget(server.URL, []byte("sk-fast"), []byte("cipher-fast")), request, "nbu_safe")
	if !fast.Success || calls.Load() != 2 {
		t.Fatalf("timeout leaked permit: result=%+v calls=%d", fast, calls.Load())
	}
}

// deadlineWriter models a backpressured client. ResponseController installs a
// finite deadline, after which Write fails and the adapter closes upstream.
type deadlineWriter struct {
	header   http.Header
	mu       sync.Mutex
	deadline time.Time
}

func (w *deadlineWriter) Header() http.Header { return w.header }
func (w *deadlineWriter) WriteHeader(int)     {}
func (w *deadlineWriter) Flush()              {}
func (w *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	w.deadline = deadline
	w.mu.Unlock()
	return nil
}
func (w *deadlineWriter) Write([]byte) (int, error) {
	w.mu.Lock()
	deadline := w.deadline
	w.mu.Unlock()
	if deadline.IsZero() {
		return 0, errors.New("missing write deadline")
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	return 0, errors.New("write deadline exceeded")
}
