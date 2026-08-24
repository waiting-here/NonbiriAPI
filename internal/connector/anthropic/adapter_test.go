package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type blockingReadCloser struct {
	entered     chan struct{}
	closed      chan struct{}
	enteredOnce sync.Once
	closedOnce  sync.Once
}

type recordingDeadlineWriter struct {
	header    http.Header
	body      bytes.Buffer
	deadlines []time.Time
	writeErr  error
	flushErr  error
}

func (w *recordingDeadlineWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *recordingDeadlineWriter) Write(value []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.body.Write(value)
}

func (w *recordingDeadlineWriter) WriteHeader(int) {}

func (w *recordingDeadlineWriter) FlushError() error { return w.flushErr }

func (w *recordingDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.closed
	return 0, io.EOF
}

func (b *blockingReadCloser) Close() error {
	b.closedOnce.Do(func() { close(b.closed) })
	return nil
}

func TestAttemptNonStreamWireHeadersAndStartedTimestamp(t *testing.T) {
	started := time.Unix(101, 0)
	clock := started
	var capturedBody []byte
	client := &fakeEndpointClient{baseURL: "https://api.example/v1"}
	client.do = func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://api.example/v1/messages" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("X-Api-Key") != "sk-test" || request.Header.Get("Anthropic-Version") != AnthropicVersion || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("headers=%v", request.Header)
		}
		for _, forbidden := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Anthropic-Beta"} {
			if request.Header.Get(forbidden) != "" {
				t.Fatalf("forbidden header %s=%q", forbidden, request.Header.Get(forbidden))
			}
		}
		var err error
		capturedBody, err = io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		// Model a response which arrives well into another second. Caller-visible
		// created must remain the timestamp captured before this outbound call.
		clock = time.Unix(999, 0)
		return fakeResponse(http.StatusOK, "application/json", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":2}}`), nil
	}
	backend := fakeBackend{open: func(baseURL string) (backend.EndpointClient, error) {
		if baseURL != "https://api.example/v1" {
			t.Fatalf("baseURL=%q", baseURL)
		}
		return client, nil
	}}
	adapter, err := NewAdapter(AdapterConfig{Backend: backend, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hi"}]}`)
	plaintext := []byte("sk-test")
	ciphertext := []byte("ciphertext-test")
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder, NewTarget("https://api.example/v1", "claude", NewCredential(plaintext, ciphertext)), request, "nbu_v3_safe")
	defer clear(capturedBody)
	if !result.Success || result.Failure != connectorcontract.FailureNone || result.Usage.OutputTokens != 2 {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"created":101`) || strings.Contains(recorder.Body.String(), `"created":999`) {
		t.Fatalf("created drifted: %s", recorder.Body.String())
	}
	var outbound outboundRequest
	if err := json.Unmarshal(capturedBody, &outbound); err != nil {
		t.Fatalf("outbound body=%s err=%v", capturedBody, err)
	}
	if outbound.Model != "claude" || outbound.MaxTokens != BuiltInDefaultMaxTokens || outbound.Metadata.UserID != "nbu_v3_safe" {
		t.Fatalf("outbound=%+v", outbound)
	}
	if string(plaintext) != strings.Repeat("\x00", len(plaintext)) || string(ciphertext) != strings.Repeat("\x00", len(ciphertext)) {
		t.Fatalf("credential buffers were not cleared")
	}
}

func TestAttemptStreamUsesSinglePreOutboundTimestamp(t *testing.T) {
	clock := time.Unix(202, 0)
	stream := messageStart(`{"input_tokens":1,"output_tokens":0}`) + messageDelta("end_turn", `{"output_tokens":2}`) + messageStop()
	client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("accept=%q", request.Header.Get("Accept"))
		}
		clock = time.Unix(1202, 0)
		return fakeResponse(http.StatusOK, "text/event-stream; charset=utf-8", stream), nil
	}}
	adapter, err := NewAdapter(AdapterConfig{
		Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
		Now:     func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
	if !result.Success {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	if strings.Count(recorder.Body.String(), `"created":202`) != 3 || strings.Contains(recorder.Body.String(), `"created":1202`) {
		t.Fatalf("stream timestamps drifted: %s", recorder.Body.String())
	}
}

func TestAttemptUsesInjectedDefaultMaxTokensProvider(t *testing.T) {
	for _, value := range []int64{1024, 100000} {
		t.Run(strconv.FormatInt(value, 10), func(t *testing.T) {
			var captured int64
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(body)
				var outbound outboundRequest
				if err := json.Unmarshal(body, &outbound); err != nil {
					t.Fatal(err)
				}
				captured = outbound.MaxTokens
				return fakeResponse(http.StatusOK, "application/json", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`), nil
			}}
			provider := maxTokensProviderFunc(func(context.Context) (*int64, error) { return &value, nil })
			adapter, err := NewAdapter(AdapterConfig{
				Backend:   fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
				MaxTokens: provider,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
			result := adapter.Attempt(context.Background(), httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
			if !result.Success || captured != value {
				t.Fatalf("result=%+v captured=%d want=%d", result, captured, value)
			}
		})
	}

	invalid := int64(0)
	var calls int
	client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}}
	adapter, err := NewAdapter(AdapterConfig{
		Backend:   fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
		MaxTokens: maxTokensProviderFunc(func(context.Context) (*int64, error) { return &invalid, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	result := adapter.Attempt(context.Background(), httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
	if result.Success || result.Failure != connectorcontract.FailureInternal || result.Diagnostic != "connector configuration unavailable" || calls != 0 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestAttemptDoesNotReadDefaultProviderForExplicitMaxTokens(t *testing.T) {
	for _, field := range []string{`"max_tokens":321`, `"max_completion_tokens":321`} {
		t.Run(field, func(t *testing.T) {
			var providerCalls int
			var captured int64
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(body)
				var outbound outboundRequest
				if err := json.Unmarshal(body, &outbound); err != nil {
					t.Fatal(err)
				}
				captured = outbound.MaxTokens
				return fakeResponse(http.StatusOK, "application/json", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{
				Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
				MaxTokens: maxTokensProviderFunc(func(context.Context) (*int64, error) {
					providerCalls++
					return nil, errors.New("site setting unavailable")
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],`+field+`}`)
			result := adapter.Attempt(context.Background(), httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
			if !result.Success || captured != 321 || providerCalls != 0 {
				t.Fatalf("result=%+v captured=%d provider_calls=%d", result, captured, providerCalls)
			}
		})
	}
}

func TestAdapterConfigClampsProtocolHardCaps(t *testing.T) {
	adapter, err := NewAdapter(AdapterConfig{
		Backend:              fakeBackend{maxBytes: 1 << 40},
		MaxJSONResponseBytes: DefaultMaxJSONResponseBytes + 1,
		MaxStreamBytes:       DefaultMaxStreamBytes + 1,
		MaxSSELineBytes:      DefaultMaxSSELineBytes + 1,
		MaxSSEEventBytes:     DefaultMaxSSEEventBytes + 1,
		StreamWriteTimeout:   DefaultStreamWriteTimeout + time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.maxJSONResponseBytes != DefaultMaxJSONResponseBytes || adapter.maxStreamBytes != DefaultMaxStreamBytes ||
		adapter.maxSSELineBytes != DefaultMaxSSELineBytes || adapter.maxSSEEventBytes != DefaultMaxSSEEventBytes ||
		adapter.streamWriteTimeout != DefaultStreamWriteTimeout {
		t.Fatalf("adapter limits=%+v", adapter)
	}

	lower, err := NewAdapter(AdapterConfig{
		Backend:              fakeBackend{maxBytes: DefaultMaxStreamBytes},
		MaxJSONResponseBytes: 1024,
		MaxStreamBytes:       2048,
		MaxSSELineBytes:      512,
		MaxSSEEventBytes:     768,
		StreamWriteTimeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lower.maxJSONResponseBytes != 1024 || lower.maxStreamBytes != 2048 || lower.maxSSELineBytes != 512 || lower.maxSSEEventBytes != 768 || lower.streamWriteTimeout != time.Second {
		t.Fatalf("lower limits changed: %+v", lower)
	}
}

func TestStreamWriteDeadlineIsScopedToEachWriteAndFlush(t *testing.T) {
	writeFailure := errors.New("write failed")
	flushFailure := errors.New("flush failed")
	for _, test := range []struct {
		name     string
		writeErr error
		flushErr error
	}{
		{name: "success"},
		{name: "write failure", writeErr: writeFailure},
		{name: "flush failure", flushErr: flushFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingDeadlineWriter{writeErr: test.writeErr, flushErr: test.flushErr}
			adapter := &Adapter{streamWriteTimeout: DefaultStreamWriteTimeout}
			_, err := adapter.writeStreamFrameUnchecked(writer, http.NewResponseController(writer), []byte("data: {}\n\n"))
			wantErr := test.writeErr
			if wantErr == nil {
				wantErr = test.flushErr
			}
			if !errors.Is(err, wantErr) && !(err == nil && wantErr == nil) {
				t.Fatalf("err=%v want=%v", err, wantErr)
			}
			if len(writer.deadlines) != 2 || writer.deadlines[0].IsZero() || !writer.deadlines[1].IsZero() {
				t.Fatalf("deadlines=%v", writer.deadlines)
			}
		})
	}
}

func TestAttemptRejectsSafetyIdentifierReflection(t *testing.T) {
	tests := []struct {
		name   string
		safety string
		body   string
	}{
		{name: "plain text", safety: "nbu_hmac_secret", body: `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"nbu_hmac_secret"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`},
		{name: "escaped text", safety: "nbu_hmac_secret", body: `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"nbu_hmac_\u0073ecret"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`},
		{name: "escaped tool input", safety: "nbu_hmac_secret", body: `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"token":"nbu_hmac_\u0073ecret"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", test.body), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-other"), []byte("cipher-other"))), request, test.safety)
			if result.Success || result.Committed || result.Diagnostic != "upstream response was rejected" || recorder.Body.Len() != 0 || strings.Contains(recorder.Body.String(), test.safety) {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
		})
	}
}

func TestAttemptRejectsSafetyIdentifierInStreamBeforeAndAfterCommit(t *testing.T) {
	const safety = "nbu_hmac_secret"
	fixtures := []struct {
		name          string
		stream        string
		wantCommitted bool
	}{
		{name: "escaped message id", stream: namedEvent("message_start", `{"type":"message_start","message":{"id":"nbu_hmac_\u0073ecret","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)},
		{name: "single text delta", wantCommitted: true, stream: messageStart(`{"input_tokens":1,"output_tokens":0}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) + namedEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"nbu_hmac_secret"}}`)},
		{name: "escaped cross-event tool input", wantCommitted: true, stream: messageStart(`{"input_tokens":1,"output_tokens":0}`) + namedEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`) + inputJSONDelta(`{"token":"nbu_hmac_\u00`) + inputJSONDelta(`73ecret"}`) + namedEvent("content_block_stop", `{"type":"content_block_stop","index":0}`)},
	}
	for _, test := range fixtures {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "text/event-stream", test.stream), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-other"), []byte("cipher-other"))), request, safety)
			if result.Success || result.Committed != test.wantCommitted || result.Diagnostic != "upstream stream was rejected" || strings.Contains(recorder.Body.String(), safety) {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
		})
	}
}

func TestAttemptRejectsCredentialReflectionAfterJSONEscaping(t *testing.T) {
	secret := []byte(`sk-"quoted`)
	client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, "application/json", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"sk-\"quoted"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`), nil
	}}
	adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential(secret, []byte("cipher"))), request, "nbu")
	if result.Success || result.Failure != connectorcontract.FailureUpstream || result.Committed || recorder.Body.Len() != 0 || result.Diagnostic != "upstream response was rejected" {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
}

func TestAttemptRejectsCredentialReflectionInDecodedToolInput(t *testing.T) {
	tests := []struct {
		name       string
		plaintext  string
		ciphertext string
		input      string
	}{
		{name: "quoted plaintext", plaintext: `sk-"quoted`, ciphertext: "cipher-other", input: `{"token":"sk-\"quoted"}`},
		{name: "backslash plaintext", plaintext: `sk-\path`, ciphertext: "cipher-other", input: `{"token":"sk-\\path"}`},
		{name: "unicode escape plaintext", plaintext: "sk-secret", ciphertext: "cipher-other", input: `{"token":"sk-\u0073ecret"}`},
		{name: "surrogate pair plaintext", plaintext: "key-😀", ciphertext: "cipher-other", input: `{"token":"key-\ud83d\ude00"}`},
		{name: "escaped object key", plaintext: "sk-secret", ciphertext: "cipher-other", input: `{"sk-\u0073ecret":"value"}`},
		{name: "unicode escape ciphertext", plaintext: "sk-other", ciphertext: "cipher-secret", input: `{"token":"cipher-\u0073ecret"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":` + test.input + `}],"stop_reason":"tool_use","stop_sequence":null,"usage":{}}`
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", upstream), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential([]byte(test.plaintext), []byte(test.ciphertext))), request, "nbu")
			if result.Success || result.Failure != connectorcontract.FailureUpstream || result.Committed || recorder.Body.Len() != 0 || result.Diagnostic != "upstream response was rejected" {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
		})
	}
}

func TestAttemptCallerNonStreamTranslatedBodyExactAndLimitPlusOne(t *testing.T) {
	responseFor := func(text string) string {
		return `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"` + text + `"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`
	}
	empty, _, _, err := translateNonStream([]byte(responseFor("")), "public/model", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	base := len(empty)
	clear(empty)
	for _, extra := range []int{0, 1} {
		name := "exact"
		if extra == 1 {
			name = "limit_plus_one"
		}
		t.Run(name, func(t *testing.T) {
			text := string(bytes.Repeat([]byte{'x'}, int(MaxCallerJSONResponseBytes)-base+extra))
			upstream := responseFor(text)
			if int64(len(upstream)) > DefaultMaxJSONResponseBytes {
				t.Fatalf("fixture upstream len=%d exceeds read limit", len(upstream))
			}
			direct, _, _, directErr := translateNonStream([]byte(upstream), "public/model", time.Unix(1, 0))
			if extra == 0 && (directErr != nil || int64(len(direct)) != MaxCallerJSONResponseBytes) {
				clear(direct)
				t.Fatalf("direct exact translation len=%d err=%v", len(direct), directErr)
			}
			if extra == 1 && !errors.Is(directErr, ErrInvalidResponse) {
				clear(direct)
				t.Fatalf("direct limit+1 err=%v", directErr)
			}
			clear(direct)
			testGuard := newSensitiveGuard([]byte("sk-test"), []byte("cipher"))
			reflected, scanErr := testGuard.ContainsJSONStrings([]byte(upstream))
			testGuard.Clear()
			if scanErr != nil || reflected {
				t.Fatalf("fixture semantic scan reflected=%v err=%v", reflected, scanErr)
			}
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", upstream), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{
				Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
				Now:     func() time.Time { return time.Unix(1, 0) },
			})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"public/model","messages":[{"role":"user","content":"hi"}]}`)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder, NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
			if extra == 0 {
				if !result.Success || int64(recorder.Body.Len()) != MaxCallerJSONResponseBytes {
					t.Fatalf("result=%+v caller_len=%d", result, recorder.Body.Len())
				}
				return
			}
			if result.Success || result.Committed || result.Failure != connectorcontract.FailureUpstream || result.Diagnostic != "upstream response was invalid" || recorder.Body.Len() != 0 {
				t.Fatalf("result=%+v caller_len=%d", result, recorder.Body.Len())
			}
		})
	}
}

func TestAttemptNonStreamResponseLimitAndLimitPlusOne(t *testing.T) {
	upstreamBody := `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{}}`
	for _, test := range []struct {
		name     string
		limit    int64
		wantOK   bool
		wantDiag string
	}{
		{name: "exact limit", limit: int64(len(upstreamBody)), wantOK: true},
		{name: "limit plus one", limit: int64(len(upstreamBody) - 1), wantDiag: "upstream response exceeded its limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return fakeResponse(http.StatusOK, "application/json", upstreamBody), nil
			}}
			adapter, err := NewAdapter(AdapterConfig{
				Backend:              fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }},
				MaxJSONResponseBytes: test.limit,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
			result := adapter.Attempt(context.Background(), httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
			if result.Success != test.wantOK || result.Diagnostic != test.wantDiag {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestAttemptFailureAndCancelMatrix(t *testing.T) {
	tests := []struct {
		name       string
		response   *http.Response
		do         func(*http.Request) (*http.Response, error)
		cancel     bool
		wantStatus int
		wantDiag   string
		wantKind   connectorcontract.FailureKind
	}{
		{name: "upstream status", response: fakeResponse(http.StatusUnauthorized, "application/json", `{"error":"secret body"}`), wantStatus: http.StatusUnauthorized, wantDiag: "upstream returned HTTP 401", wantKind: connectorcontract.FailureUpstream},
		{name: "wrong content type", response: fakeResponse(http.StatusOK, "text/plain", "secret body"), wantStatus: http.StatusOK, wantDiag: "upstream response content type was invalid", wantKind: connectorcontract.FailureUpstream},
		{name: "invalid response", response: fakeResponse(http.StatusOK, "application/json", `{`), wantStatus: http.StatusOK, wantDiag: "upstream response was invalid", wantKind: connectorcontract.FailureUpstream},
		{name: "transport", do: func(*http.Request) (*http.Response, error) { return nil, errors.New("contains secret") }, wantDiag: "upstream request failed", wantKind: connectorcontract.FailureUpstream},
		{name: "canceled", do: func(request *http.Request) (*http.Response, error) { return nil, request.Context().Err() }, cancel: true, wantDiag: "request canceled", wantKind: connectorcontract.FailureCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: test.do}
			if client.do == nil {
				client.do = func(*http.Request) (*http.Response, error) { return test.response, nil }
			}
			adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
			ctx := context.Background()
			if test.cancel {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			result := adapter.Attempt(ctx, httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
			if result.Success || result.Failure != test.wantKind || result.UpstreamStatus != test.wantStatus || result.Diagnostic != test.wantDiag || strings.Contains(result.Diagnostic, "secret") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestAttemptInFlightStreamCancellationClosesUpstreamBody(t *testing.T) {
	body := newBlockingReadCloser()
	client := &fakeEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
		}, nil
	}}
	adapter, err := NewAdapter(AdapterConfig{Backend: fakeBackend{open: func(string) (backend.EndpointClient, error) { return client, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	request := mustChatRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan connectorcontract.AttemptResult, 1)
	go func() {
		results <- adapter.Attempt(ctx, httptest.NewRecorder(), NewTarget(client.baseURL, "claude", NewCredential([]byte("sk-test"), []byte("cipher"))), request, "nbu")
	}()
	select {
	case <-body.entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("upstream stream body was not read")
	}
	cancel()
	select {
	case result := <-results:
		if result.Success || result.Failure != connectorcontract.FailureCanceled || result.Committed {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream cancellation did not return")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("upstream response body was not closed")
	}
}
