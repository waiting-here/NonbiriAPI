package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestV4StreamKeepsTerminalAndRepresentabilityChecks(t *testing.T) {
	complete := append([]string{}, textParts...)
	complete[0] = `{"type":"stream-start","warnings":[{"type":"deprecated","setting":"old","message":"sk-test-credential-long"}]}`
	for _, tc := range []struct {
		name, body string
		success    bool
	}{
		{"complete", sse(complete...), true},
		{"eof", sse(complete[:4]...), false},
		{"done-only", "data: [DONE]\n\n", false},
		{"error", sse(`{"type":"error","error":{"message":"sk-test-credential-long"}}`), false},
		{"file", sse(`{"type":"file","mediaType":"image/png","data":{"type":"data","data":"aGk="}}`), false},
		{"custom", sse(`{"type":"custom","kind":"provider.opaque"}`), false},
		{"citations", sse(textParts[1], textParts[2], `{"type":"text-end","id":"t","citations":[{"start":0,"end":2}]}`, textParts[4]), false},
		{"text-tool", sse(`{"type":"tool-input-start","id":"x","toolName":"custom","inputEncoding":"text"}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("ai-language-model-specification-version") != "4" || r.Header.Get("ai-language-model-streaming") != "true" {
					t.Fatal(r.Header)
				}
				return response(tc.body, "text/event-stream"), nil
			}}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			result := a.Attempt(context.Background(), w, versionTarget(contract.TypeAISDKGatewayV4), secret(), chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`), nil, "")
			if result.Success != tc.success || strings.Contains(w.Body.String(), "[DONE]") != tc.success || strings.Contains(w.Body.String(), "sk-test") {
				t.Fatalf("%+v %s", result, w.Body.String())
			}
		})
	}
	for _, part := range []string{
		`{"type":"file","mediaType":"image/png","data":{"type":"url","url":"https://images.example/a.png"}}`,
		`{"type":"custom","kind":"provider.opaque"}`,
		`{"type":"reasoning-file","mediaType":"image/png","data":{"type":"data","data":"aGk="}}`,
		`{"type":"text","text":"answer","citations":[{"start":0,"end":1}]}`,
	} {
		if _, _, err := translateChat([]byte(`{"content":[`+part+`],"finishReason":{"unified":"stop"}}`), "public", 1); err == nil {
			t.Fatal(part)
		}
	}
}

func TestImageProjectionRejectsLiteralAndSplitCredentialReflections(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, tc := range []struct{ body, extra string }{
			{`{"images":["abcdefghijklmnop"]}`, ""},
			{`{"images":["abcd\u0065fghijklmnop"]}`, ""},
			{`{"images":["abcdefgh","ijklmnop"]}`, `,"n":2`},
		} {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(tc.body, "application/json"), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			key := contract.NewShortLivedSecret([]byte("abcdefghijklmnop"), []byte("encrypted-secret"))
			result := a.AttemptImageWithPolicy(context.Background(), w, versionTarget(kind), key, imageRequest(t, tc.extra), "", contract.AttemptPolicy{})
			if result.Success || result.Committed || w.Body.Len() != 0 {
				t.Fatalf("%+v %s", result, w.Body.String())
			}
			plain, cipher, ok := key.Take()
			clear(plain)
			clear(cipher)
			if ok {
				t.Fatal("credential retained")
			}
		}
	}
}

func TestNewServiceTransportErrorLimitsAndCancellation(t *testing.T) {
	for _, service := range []string{"image", "reranking"} {
		for _, mode := range []string{"http-error", "body-error", "limit", "cancel"} {
			t.Run(service+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
					switch mode {
					case "http-error":
						resp := response(`{"error":{"message":"sk-test-credential-long","type":"invalid_request_error"}}`, "application/json")
						resp.StatusCode = 400
						return resp, nil
					case "body-error":
						return response(`{"error":{"message":"sk-test-credential-long"}}`, "application/json"), nil
					case "cancel":
						cancel()
						return nil, r.Context().Err()
					default:
						return response(strings.Repeat("x", 64), "application/json"), nil
					}
				}}
				if mode == "limit" {
					b.limit = 32
				}
				a, _ := NewAdapter(b)
				w := httptest.NewRecorder()
				var result contract.AttemptResult
				if service == "image" {
					result = a.AttemptImageWithPolicy(ctx, w, versionTarget(contract.TypeAISDKGatewayV4), secret(), imageRequest(t, ""), "", contract.AttemptPolicy{})
				} else {
					result = a.AttemptInferenceWithPolicy(ctx, w, versionTarget(contract.TypeAISDKGatewayV4), secret(), rerankRequest(t, `["doc"]`, ""), "", contract.AttemptPolicy{})
				}
				if result.Success || result.Committed || w.Body.Len() != 0 || b.calls != 1 {
					t.Fatalf("%+v %s", result, w.Body.String())
				}
				if mode == "cancel" && result.Failure != contract.FailureCanceled {
					t.Fatalf("%+v", result)
				}
			})
		}
	}
}

func TestDiscoverySelectsConfiguredSpecificationAndFourServices(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" || r.URL.Path != "/native/v3/ai/config" {
				t.Fatal(r.URL)
			}
			return response(`{"models":[
				{"id":"p/chat","modelType":"language"},
				{"id":"p/image","modelType":"image"},
				{"id":"p/embed","modelType":"embedding"},
				{"id":"p/rerank","modelType":"reranking"},
				{"id":"p/v3","modelType":"language","specification":{"specificationVersion":"v3"}},
				{"id":"p/v4","modelType":"language","specification":{"specificationVersion":"v4"}},
				{"id":"p/unspecified"},
				{"id":"p/null","modelType":null},
				{"modelType":"future-kind"}
			]}`, "application/json"), nil
		}}
		result := (ModelDiscoverer{}).Discover(context.Background(), contract.DiscoveryInput{Backend: b, Target: versionTarget(kind), Credential: secret()})
		if result.Failure != contract.DiscoveryFailureNone || len(result.Models) != 5 || result.Models[3].ID != "p/rerank" || result.Models[4].ID != "p/v"+specificationVersion(kind) {
			t.Fatalf("%+v", result)
		}
	}
}

// The same read failure path applies to both new nonstream services.
func TestImageBodyClosesAfterProtocolFailure(t *testing.T) {
	closed := false
	b := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
		resp := response("bad-json", "application/json")
		resp.Body = &observedReadCloser{Reader: strings.NewReader("bad-json"), closed: &closed}
		return resp, nil
	}}
	a, _ := NewAdapter(b)
	result := a.AttemptImageWithPolicy(context.Background(), httptest.NewRecorder(), versionTarget(contract.TypeAISDKGatewayV4), secret(), imageRequest(t, ""), "", contract.AttemptPolicy{})
	if result.Success || !closed {
		t.Fatalf("%+v closed=%v", result, closed)
	}
}

type observedReadCloser struct {
	io.Reader
	closed *bool
}

func (r *observedReadCloser) Close() error { *r.closed = true; return nil }
