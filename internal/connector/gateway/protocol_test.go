package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

const knownUsage = `{"inputTokens":{"total":9,"noCache":4,"cacheRead":3,"cacheWrite":2},"outputTokens":{"total":5,"text":2,"reasoning":3}}`
const chatOK = `{"content":[{"type":"text","text":"Hello"}],"finishReason":{"unified":"stop","raw":"stop"},"usage":` + knownUsage + `,"warnings":[]}`
const prompt = `{"model":"public-model","messages":[{"role":"system","content":"Short answers"},{"role":"user","content":"Hi"}],"max_tokens":128}`

func chat(t *testing.T, raw string) *openai.ChatRequest {
	t.Helper()
	r, err := openai.DecodeChatRequest(strings.NewReader(raw), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type fakeBackend struct {
	do    func(*http.Request) (*http.Response, error)
	calls int
	limit int64
}

func (b *fakeBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (b *fakeBackend) BaseURL() string                             { return "https://upstream.example/native/v3/ai" }
func (b *fakeBackend) MaxResponseBytes() int64 {
	if b.limit != 0 {
		return b.limit
	}
	return 32 << 20
}
func (b *fakeBackend) Do(r *http.Request) (*http.Response, error) { b.calls++; return b.do(r) }
func response(body, kind string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(body))}
}
func target() contract.Target {
	return contract.NewTarget(contract.TypeAISDKGatewayV3, "https://upstream.example/native/v3/ai", "provider/private-model")
}
func secret() *contract.ShortLivedSecret {
	return contract.NewShortLivedSecret([]byte("sk-test-credential-long"), []byte("encrypted-credential-long"))
}

func TestChatCompilerFidelity(t *testing.T) {
	valid := []string{prompt,
		`{"model":"x","messages":[{"role":"user","content":"hi"}],"temperature":0.1,"top_p":1,"top_k":8,"presence_penalty":-1,"frequency_penalty":2,"seed":12,"stop":["END"],"n":1,"logprobs":false,"logit_bias":{},"response_format":{"type":"text"}}`,
		`{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"Describe"},{"type":"image_url","image_url":{"url":"https://images.example/tiny.png","detail":"auto"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}]}`,
		`{"model":"x","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":"answer"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"strict":true}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
	}
	for i, raw := range valid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			body, err := compileChat(chat(t, raw), "server-label")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if json.Unmarshal(body, &got) != nil {
				t.Fatal(string(body))
			}
			if got["model"] != nil || got["messages"] != nil || got["prompt"] == nil || got["providerOptions"].(map[string]any)["gateway"].(map[string]any)["user"] != "server-label" {
				t.Fatal(string(body))
			}
			if i == 2 && !bytes.Contains(body, []byte(`"mediaType":"image/*"`)) {
				t.Fatal(string(body))
			}
			if i == 3 && !bytes.Contains(body, []byte(`"toolName":"lookup"`)) {
				t.Fatal(string(body))
			}
		})
	}
	for _, fragment := range []string{`"developer", "content":"hello"`, `"user", "name":"alias", "content":"hello"`} {
		if SupportsRequest(chat(t, `{"model":"x","messages":[{"role":`+fragment+`}]}`)) {
			t.Fatal(fragment)
		}
	}
	for _, extra := range []string{`"store":false`, `"max_completion_tokens":128`, `"parallel_tool_calls":false`, `"providerOptions":{"gateway":{"user":"injected"}}`, `"user":"injected"`, `"unknown":null`, `"n":2`, `"logit_bias":{"1":1}`, `"logprobs":true`, `"response_format":{"type":"json_object"}`, `"stop":[null]`, `"stop":[""]`, `"seed":9007199254740992`, `"tools":[{"type":"web_search"}]`, `"tool_choice":"required"`} {
		raw := strings.TrimSuffix(prompt, "}") + "," + extra + "}"
		r, err := openai.DecodeChatRequest(strings.NewReader(raw), 4<<20)
		if err == nil && SupportsRequest(r) {
			t.Fatalf("silently accepted %s", extra)
		}
	}
}

func TestEmbeddingCompilerAndVectors(t *testing.T) {
	for _, input := range []string{`"hello"`, `["hello","world"]`} {
		r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"public-model","input":`+input+`,"encoding_format":"float"}`), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		body, err := compileEmbedding(r, "")
		if err != nil || !bytes.Contains(body, []byte(`"values":`)) || bytes.Contains(body, []byte("providerOptions")) {
			t.Fatalf("%s %v", body, err)
		}
	}
	for _, raw := range []string{`{"model":"x","input":[1,2]}`, `{"model":"x","input":"x","dimensions":3}`, `{"model":"x","input":"x","encoding_format":"base64"}`, `{"model":"x","input":"x","user":"x"}`} {
		r, err := openai.DecodeEmbeddingRequest(strings.NewReader(raw), 1<<20)
		if err == nil && SupportsEmbedding(r) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`{"embeddings":[[null,1]]}`, `{"embeddings":[[]]}`, `{"embeddings":[[1],[1,2]]}`, `{"embeddings":[["1"]]}`, `{"embeddings":[[1e400]]}`} {
		if _, _, err := translateEmbedding([]byte(raw), "x", 1); err == nil {
			t.Fatal(raw)
		}
	}
	body, usage, err := translateEmbedding([]byte(`{"embeddings":[[0.5,-1],[1,2]],"usage":{"tokens":4}}`), "public", 2)
	if err != nil || !usage.Present || usage.UncachedInputTokens != 4 || !bytes.Contains(body, []byte(`"model":"public"`)) {
		t.Fatalf("%s %+v %v", body, usage, err)
	}
	_, usage, err = translateEmbedding([]byte(`{"embeddings":[[1]]}`), "x", 1)
	if err != nil || usage.Present {
		t.Fatalf("%+v %v", usage, err)
	}
}

func TestUsageRequiresUnambiguousBucketsAndNoDoubleCount(t *testing.T) {
	u, err := parseUsage([]byte(knownUsage))
	if err != nil || !u.Present || u.OutputTokens != 5 || u.UncachedInputTokens != 4 || u.CacheReadInputTokens != 3 || u.CacheWriteInputTokens != 2 {
		t.Fatalf("%+v %v", u, err)
	}
	for _, raw := range []string{`null`, `{}`, `{"inputTokens":{"total":9},"outputTokens":{"total":2}}`, `{"inputTokens":{"total":9,"noCache":9,"cacheRead":0,"cacheWrite":0}}`} {
		u, err := parseUsage([]byte(raw))
		if err != nil || u.Present {
			t.Fatalf("%s %+v %v", raw, u, err)
		}
	}
	for _, raw := range []string{`{"inputTokens":{"total":0},"outputTokens":{"total":0}}`, `{"inputTokens":{"total":9,"noCache":5,"cacheRead":4},"outputTokens":{"total":2}}`} {
		u, err := parseUsage([]byte(raw))
		if err != nil || !u.Present {
			t.Fatalf("%s %+v %v", raw, u, err)
		}
	}
	for _, raw := range []string{`{"inputTokens":{"total":1,"noCache":2}}`, `{"inputTokens":{"noCache":-1}}`, `{"outputTokens":{"total":2,"text":2,"reasoning":1}}`, `{"inputTokens":{"noCache":9223372036854775807,"cacheRead":1,"cacheWrite":0},"outputTokens":{"total":1}}`} {
		if _, err := parseUsage([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}

func TestAdapterHeadersTranslationAndCredentialGuard(t *testing.T) {
	b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/native/v3/ai/language-model" || r.Header.Get("Authorization") != "Bearer sk-test-credential-long" || r.Header.Get("ai-language-model-specification-version") != "3" || r.Header.Get("ai-language-model-id") != "provider/private-model" || r.Header.Get("ai-gateway-protocol-version") != "0.0.1" || r.Header.Get("ai-gateway-auth-method") != "api-key" {
			t.Fatal(r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("public-model")) || !bytes.Contains(body, []byte(`"user":"cost-label-only"`)) {
			t.Fatal(string(body))
		}
		return response(chatOK, "application/json"), nil
	}}
	a, _ := NewAdapter(b)
	w := httptest.NewRecorder()
	res := a.Attempt(context.Background(), w, target(), secret(), chat(t, prompt), nil, "cost-label-only")
	if !res.Success || !res.Usage.Present || res.GatewayUserAttributionSent == nil || !*res.GatewayUserAttributionSent || b.calls != 1 || strings.Contains(w.Body.String(), "private-model") {
		t.Fatalf("%+v %s", res, w.Body)
	}
	for _, reflected := range []string{"sk-test-credential-long", "encrypted-credential-long", "cost-label-only"} {
		b.do = func(*http.Request) (*http.Response, error) {
			return response(strings.Replace(chatOK, "Hello", reflected, 1), "application/json"), nil
		}
		w = httptest.NewRecorder()
		res = a.Attempt(context.Background(), w, target(), secret(), chat(t, prompt), nil, "cost-label-only")
		if res.Success || res.Committed || w.Body.Len() != 0 {
			t.Fatalf("reflection: %+v %s", res, w.Body)
		}
	}
	b.calls = 0
	w = httptest.NewRecorder()
	a.Attempt(context.Background(), w, target(), secret(), chat(t, `{"model":"x","messages":[{"role":"developer","content":"x"}]}`), nil, "")
	if b.calls != 0 {
		t.Fatal("incompatible request dispatched")
	}
}

func sse(parts ...string) string { return "data: " + strings.Join(parts, "\n\ndata: ") + "\n\n" }

var textParts = []string{`{"type":"stream-start","warnings":[]}`, `{"type":"text-start","id":"t"}`, `{"type":"text-delta","id":"t","delta":"Hello"}`, `{"type":"text-end","id":"t"}`, `{"type":"finish","finishReason":{"unified":"stop"},"usage":` + knownUsage + `}`}

func TestStreamTerminalOrderingToolsAndUnknownUsage(t *testing.T) {
	toolParts := []string{`{"type":"tool-input-start","id":"call","toolName":"lookup"}`, `{"type":"tool-input-delta","id":"call","delta":"{\"q\":"}`, `{"type":"tool-input-delta","id":"call","delta":"\"hi\"}"}`, `{"type":"tool-input-end","id":"call"}`, `{"type":"tool-call","toolCallId":"call","toolName":"lookup","input":"{\"q\":\"hi\"}"}`, `{"type":"finish","finishReason":{"unified":"tool-calls"}}`}
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"text", sse(textParts...), true}, {"tool", sse(toolParts...), true},
		{"eof", sse(textParts[:4]...), false}, {"missing-end", sse(textParts[1], textParts[2], textParts[4]), false},
		{"delta-before-start", sse(textParts[2], textParts[3], textParts[4]), false},
		{"after-finish", sse(append(append([]string{}, textParts...), textParts[2])...), false},
		{"truncated-tool", sse(append(append([]string{}, toolParts[:4]...), toolParts[5])...), false},
		{"mismatched-tool", strings.Replace(sse(toolParts...), `hi\"}"}`, `bad\"}"}`, 1), false},
		{"error", sse(textParts[1], textParts[2], `{"type":"error","error":{"message":"secret detail"}}`), false},
		{"unsupported-setting", sse(`{"type":"stream-start","warnings":[{"type":"unsupported","feature":"temperature"}]}`, textParts[1], textParts[3], textParts[4]), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(tc.body, "text/event-stream"), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			r := chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true,"stream_options":{"include_usage":true}}`)
			res := a.Attempt(context.Background(), w, target(), secret(), r, nil, "")
			if res.Success != tc.ok || strings.Contains(w.Body.String(), "[DONE]") != tc.ok {
				t.Fatalf("%+v %s", res, w.Body)
			}
			if tc.name == "tool" && (res.Usage.Present || !strings.Contains(w.Body.String(), "tool_calls")) {
				t.Fatalf("%+v %s", res, w.Body)
			}
			if tc.name == "text" && (!res.Usage.Present || !strings.Contains(w.Body.String(), `"completion_tokens":5`)) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestStreamUsesNativeEventsInsteadOfMIMEDeclaration(t *testing.T) {
	for _, kind := range []string{"text/plain; charset=utf-8", "application/octet-stream", ""} {
		for _, body := range []string{sse(textParts...), sse(textParts[:4]...), "<html>not a stream</html>"} {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(body, kind), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			r := chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`)
			result := a.Attempt(context.Background(), w, target(), secret(), r, nil, "")
			want := body == sse(textParts...)
			if result.Success != want || strings.Contains(w.Body.String(), "[DONE]") != want {
				t.Fatalf("kind=%q body=%q result=%+v", kind, body, result)
			}
		}
	}
}

func TestReasoningContentRemainsSeparateFromAnswer(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"Red"},{"type":"reasoning","text":"A red square."}],"finishReason":{"unified":"stop"}}`
	out, _, err := translateChat([]byte(raw), "visible", 1)
	if err != nil || !strings.Contains(string(out), `"content":"Red"`) || !strings.Contains(string(out), `"reasoning_content":"A red square."`) {
		t.Fatalf("output=%s err=%v", out, err)
	}
	parts := []string{`{"type":"reasoning-start","id":"r"}`, `{"type":"reasoning-delta","id":"r","delta":"A red square."}`, `{"type":"reasoning-end","id":"r"}`}
	for _, complete := range []bool{false, true} {
		prefix := parts
		if !complete {
			prefix = parts[:2]
		}
		body := sse(append(append([]string{}, prefix...), textParts[1:]...)...)
		b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(body, "text/plain"), nil }}
		a, _ := NewAdapter(b)
		w := httptest.NewRecorder()
		result := a.Attempt(context.Background(), w, target(), secret(), chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`), nil, "")
		if result.Success != complete || !strings.Contains(w.Body.String(), `"reasoning_content":"A red square."`) || strings.Contains(w.Body.String(), "[DONE]") != complete {
			t.Fatalf("complete=%v result=%+v output=%s", complete, result, w.Body.String())
		}
	}
}

type observedWriter struct {
	*httptest.ResponseRecorder
	once  sync.Once
	wrote chan struct{}
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}
func TestStreamIsIncrementalAndCancellationClosesUpstream(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
		r := response("", "text/event-stream")
		r.Body = reader
		return r, nil
	}}
	a, _ := NewAdapter(b)
	w := &observedWriter{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
	done := make(chan contract.AttemptResult, 1)
	r := chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`)
	go func() { done <- a.Attempt(ctx, w, target(), secret(), r, nil, "") }()
	if _, err := io.WriteString(writer, sse(textParts[:3]...)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("stream buffered until terminal")
	}
	cancel()
	select {
	case res := <-done:
		if res.Success {
			t.Fatal(res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not unblock stream")
	}
	if _, err := writer.Write([]byte("late")); err == nil {
		t.Fatal("upstream body not closed")
	}
	writer.Close()
}

func TestDiscoverySkipsOtherKindsAndPreservesIDs(t *testing.T) {
	b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/native/v3/ai/config" || r.Method != "GET" {
			t.Fatal(r.URL)
		}
		return response(`{"models":[{"id":"provider/chat","modelType":"language"},{"id":"provider/embed","modelType":"embedding"},{"id":"provider/image","modelType":"image"},{"id":"provider/video","modelType":"video"},{"modelType":"future-kind","pricing":{"output":"0"}}]}`, "application/json"), nil
	}}
	res := (ModelDiscoverer{}).Discover(context.Background(), contract.DiscoveryInput{Backend: b, Target: target(), Credential: secret()})
	if res.Failure != contract.DiscoveryFailureNone || len(res.Models) != 2 || res.Models[0].ID != "provider/chat" || res.Models[1].ID != "provider/embed" {
		t.Fatalf("%+v", res)
	}
	for _, body := range []string{`{"models":[{"id":"sk-test-credential-long","modelType":"language"}]}`, `{"models":[{"id":"x","modelType":"language"},{"id":"x","modelType":"language"}]}`, `{"models":[{"id":"x","modelType":true}]}`} {
		b.do = func(*http.Request) (*http.Response, error) { return response(body, "application/json"), nil }
		res = (ModelDiscoverer{}).Discover(context.Background(), contract.DiscoveryInput{Backend: b, Target: target(), Credential: secret()})
		if res.Failure == contract.DiscoveryFailureNone || len(res.Models) != 0 {
			t.Fatalf("%+v", res)
		}
	}
}
