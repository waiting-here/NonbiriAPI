package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type discoveryBackend struct {
	client backend.EndpointClient
	err    error
}

func (b discoveryBackend) Open(string) (backend.EndpointClient, error) { return b.client, b.err }
func (discoveryBackend) MaxResponseBytes() int64                       { return MaxModelsBodyBytes }

type discoveryEndpointClient struct {
	baseURL string
	do      func(*http.Request) (*http.Response, error)
}

func (c *discoveryEndpointClient) BaseURL() string { return c.baseURL }
func (c *discoveryEndpointClient) Do(request *http.Request) (*http.Response, error) {
	return c.do(request)
}

type discoveryReadCloser struct {
	read   bool
	closed bool
	err    error
}

func (b *discoveryReadCloser) Read([]byte) (int, error) {
	b.read = true
	if b.err != nil {
		return 0, b.err
	}
	return 0, io.EOF
}

func (b *discoveryReadCloser) Close() error {
	b.closed = true
	return nil
}

func openAIDiscoveryInput(client backend.EndpointClient, plaintext, ciphertext []byte) connectorcontract.DiscoveryInput {
	return connectorcontract.DiscoveryInput{
		Backend: discoveryBackend{client: client},
		Target: connectorcontract.NewTarget(
			connectorcontract.TypeOpenAICompatible,
			"https://api.example/v1",
			"",
		),
		Credential: connectorcontract.NewShortLivedSecret(plaintext, ciphertext),
	}
}

func openAIDiscoveryResponse(status int, body string) *http.Response {
	response := &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
	response.Header.Set("Content-Type", "application/json")
	return response
}

func discoveryBytesCleared(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func TestModelDiscoveryTypedSuccessNonemptyAndEmpty(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantModels []connectorcontract.DiscoveredModel
	}{
		{
			name: "nonempty",
			body: `{"data":[{"id":"model-a","owned_by":"provider-a"},{"id":"model-b"}]}`,
			wantModels: []connectorcontract.DiscoveredModel{
				{ID: "model-a", Provider: "provider-a"},
				{ID: "model-b"},
			},
		},
		{name: "empty", body: `{"data":[]}`, wantModels: []connectorcontract.DiscoveredModel{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requestSeen *http.Request
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				requestSeen = request
				if request.Method != http.MethodGet || request.URL.String() != "https://api.example/v1/models" {
					t.Fatalf("request = %s %s", request.Method, request.URL)
				}
				if request.Header.Get("Authorization") != "Bearer sk-discovery" || request.Header.Get("Accept") != "application/json" {
					t.Fatalf("headers = %v", request.Header)
				}
				response := openAIDiscoveryResponse(http.StatusOK, test.body)
				response.Request = request
				return response, nil
			}}
			plaintext := []byte("sk-discovery")
			ciphertext := []byte("cipher-discovery")
			result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
			if !result.Succeeded() || result.Failure != connectorcontract.DiscoveryFailureNone ||
				!result.ResponseReceived || result.UpstreamStatus != http.StatusOK || result.Diagnostic != "" {
				t.Fatalf("result = %+v", result)
			}
			if len(result.Models) != len(test.wantModels) {
				t.Fatalf("models = %+v, want %+v", result.Models, test.wantModels)
			}
			for index := range test.wantModels {
				if result.Models[index] != test.wantModels[index] {
					t.Fatalf("model %d = %+v, want %+v", index, result.Models[index], test.wantModels[index])
				}
			}
			if requestSeen == nil || requestSeen.Header.Get("Authorization") != "" ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("credential lifetime was not cleared: request=%v plaintext=%v ciphertext=%v", requestSeen, plaintext, ciphertext)
			}
		})
	}
}

func TestModelDiscoveryMapsHTTPFailuresWithoutReadingBody(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		wantFailure connectorcontract.DiscoveryFailureKind
	}{
		{name: "401", status: http.StatusUnauthorized, wantFailure: connectorcontract.DiscoveryFailureAuth},
		{name: "403", status: http.StatusForbidden, wantFailure: connectorcontract.DiscoveryFailureAuth},
		{name: "429", status: http.StatusTooManyRequests, wantFailure: connectorcontract.DiscoveryFailureRateLimit},
		{name: "other non-2xx", status: http.StatusBadGateway, wantFailure: connectorcontract.DiscoveryFailureProtocol},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &discoveryReadCloser{err: errors.New("raw body must not be read")}
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header)}, nil
			}}
			plaintext := []byte("sk-status-secret")
			ciphertext := []byte("cipher-status-secret")
			result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
			if result.Succeeded() || len(result.Models) != 0 || result.Failure != test.wantFailure ||
				!result.ResponseReceived || result.UpstreamStatus != test.status || result.Diagnostic != fmt.Sprintf("upstream returned status %d", test.status) {
				t.Fatalf("result = %+v", result)
			}
			formatted := fmt.Sprintf("%+v", result)
			if body.read || !body.closed || strings.Contains(formatted, "raw body") || strings.Contains(formatted, "status-secret") ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("untrusted failure data or credential leaked: result=%s body=%+v plaintext=%v ciphertext=%v", formatted, body, plaintext, ciphertext)
			}
		})
	}
}

func TestModelDiscoveryMapsFailureStatusWithoutBody(t *testing.T) {
	client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header)}, nil
	}}
	plaintext := []byte("sk-status-without-body")
	ciphertext := []byte("cipher-status-without-body")
	result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
	if result.Succeeded() || result.Failure != connectorcontract.DiscoveryFailureAuth || !result.ResponseReceived ||
		result.UpstreamStatus != http.StatusForbidden || result.Diagnostic != "upstream returned status 403" ||
		len(result.Models) != 0 || !discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
		t.Fatalf("result=%+v plaintext=%v ciphertext=%v", result, plaintext, ciphertext)
	}
}

func TestModelDiscoveryRejectsInvalidResponseMediaTypesBeforeBodyRead(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(http.Header)
	}{
		{name: "wrong", mutate: func(header http.Header) { header.Set("Content-Type", "text/plain") }},
		{name: "missing", mutate: func(header http.Header) { header.Del("Content-Type") }},
		{name: "duplicate", mutate: func(header http.Header) { header.Add("Content-Type", "application/json") }},
		{name: "invalid", mutate: func(header http.Header) { header.Set("Content-Type", "application/json; charset=") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &discoveryReadCloser{err: errors.New("untrusted media body must not be read")}
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}
				response.Header.Set("Content-Type", "application/json")
				test.mutate(response.Header)
				return response, nil
			}}
			plaintext := []byte("sk-media-secret")
			ciphertext := []byte("cipher-media-secret")
			result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
			if result.Succeeded() || result.Failure != connectorcontract.DiscoveryFailureProtocol || !result.ResponseReceived ||
				result.UpstreamStatus != http.StatusOK || result.Diagnostic != "upstream response content type was invalid" ||
				len(result.Models) != 0 || body.read || !body.closed ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("result=%+v body=%+v plaintext=%v ciphertext=%v", result, body, plaintext, ciphertext)
			}
		})
	}
}

func TestModelDiscoveryMapsContextAndTransportFailures(t *testing.T) {
	tests := []struct {
		name         string
		context      func() (context.Context, context.CancelFunc)
		do           func(*http.Request) (*http.Response, error)
		wantFailure  connectorcontract.DiscoveryFailureKind
		wantStatus   int
		wantReceived bool
		wantCalls    int
		wantDiag     string
	}{
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			wantFailure: connectorcontract.DiscoveryFailureTimeout,
			wantDiag:    "model discovery timed out",
		},
		{
			name: "cancel",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantFailure: connectorcontract.DiscoveryFailureInterrupted,
			wantDiag:    "model discovery canceled",
		},
		{
			name: "transport",
			do: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("private raw transport sk-transport-secret")
			},
			wantFailure: connectorcontract.DiscoveryFailureTransport,
			wantCalls:   1,
			wantDiag:    "upstream request failed",
		},
		{
			name: "response and error",
			do: func(*http.Request) (*http.Response, error) {
				return openAIDiscoveryResponse(http.StatusBadGateway, "private response"), errors.New("private redirect detail")
			},
			wantFailure:  connectorcontract.DiscoveryFailureProtocol,
			wantStatus:   http.StatusBadGateway,
			wantReceived: true,
			wantCalls:    1,
			wantDiag:     "upstream request failed",
		},
		{
			name: "rate-limit response and error",
			do: func(*http.Request) (*http.Response, error) {
				return openAIDiscoveryResponse(http.StatusTooManyRequests, "private response"), errors.New("private redirect detail")
			},
			wantFailure:  connectorcontract.DiscoveryFailureRateLimit,
			wantStatus:   http.StatusTooManyRequests,
			wantReceived: true,
			wantCalls:    1,
			wantDiag:     "upstream request failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			cancel := func() {}
			if test.context != nil {
				ctx, cancel = test.context()
			}
			defer cancel()
			calls := 0
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(request *http.Request) (*http.Response, error) {
				calls++
				return test.do(request)
			}}
			plaintext := []byte("sk-transport-secret")
			ciphertext := []byte("cipher-transport-secret")
			result := (ModelDiscoverer{}).Discover(ctx, openAIDiscoveryInput(client, plaintext, ciphertext))
			if result.Succeeded() || len(result.Models) != 0 || result.Failure != test.wantFailure ||
				result.UpstreamStatus != test.wantStatus || result.ResponseReceived != test.wantReceived ||
				result.Diagnostic != test.wantDiag || calls != test.wantCalls {
				t.Fatalf("result = %+v, calls = %d", result, calls)
			}
			formatted := fmt.Sprintf("%+v", result)
			if strings.Contains(formatted, "private") || strings.Contains(formatted, "transport-secret") ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("raw error or credential leaked: result=%s plaintext=%v ciphertext=%v", formatted, plaintext, ciphertext)
			}
		})
	}
}

func TestModelDiscoveryMapsNilMalformedAndOversizeResponses(t *testing.T) {
	tests := []struct {
		name         string
		do           func(*http.Request) (*http.Response, error)
		wantFailure  connectorcontract.DiscoveryFailureKind
		wantStatus   int
		wantReceived bool
		wantDiag     string
	}{
		{
			name:        "nil response",
			do:          func(*http.Request) (*http.Response, error) { return nil, nil },
			wantFailure: connectorcontract.DiscoveryFailureTransport,
			wantDiag:    "upstream response was unavailable",
		},
		{
			name: "nil body",
			do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, nil
			},
			wantFailure:  connectorcontract.DiscoveryFailureProtocol,
			wantStatus:   http.StatusOK,
			wantReceived: true,
			wantDiag:     "upstream response was unavailable",
		},
		{
			name: "malformed JSON",
			do: func(*http.Request) (*http.Response, error) {
				return openAIDiscoveryResponse(http.StatusOK, `{"data":`), nil
			},
			wantFailure:  connectorcontract.DiscoveryFailureProtocol,
			wantStatus:   http.StatusOK,
			wantReceived: true,
			wantDiag:     "invalid upstream models response",
		},
		{
			name: "oversize body",
			do: func(*http.Request) (*http.Response, error) {
				return openAIDiscoveryResponse(http.StatusOK, strings.Repeat("x", MaxModelsBodyBytes+1)), nil
			},
			wantFailure:  connectorcontract.DiscoveryFailureProtocol,
			wantStatus:   http.StatusOK,
			wantReceived: true,
			wantDiag:     ErrModelsResponseTruncated.Error(),
		},
		{
			name: "body read error",
			do: func(*http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &discoveryReadCloser{err: errors.New("private body read error")}}
				response.Header.Set("Content-Type", "application/json")
				return response, nil
			},
			wantFailure:  connectorcontract.DiscoveryFailureProtocol,
			wantStatus:   http.StatusOK,
			wantReceived: true,
			wantDiag:     "upstream response could not be read",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: test.do}
			plaintext := []byte("sk-malformed-secret")
			ciphertext := []byte("cipher-malformed-secret")
			result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
			if result.Succeeded() || len(result.Models) != 0 || result.Failure != test.wantFailure ||
				result.UpstreamStatus != test.wantStatus || result.ResponseReceived != test.wantReceived || result.Diagnostic != test.wantDiag {
				t.Fatalf("result = %+v", result)
			}
			formatted := fmt.Sprintf("%+v", result)
			if strings.Contains(formatted, "private") || strings.Contains(formatted, "malformed-secret") ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("raw response failure or credential leaked: result=%s plaintext=%v ciphertext=%v", formatted, plaintext, ciphertext)
			}
		})
	}
}

func TestModelDiscoveryRejectsCredentialReflection(t *testing.T) {
	tests := []struct {
		name       string
		plaintext  string
		ciphertext string
		body       string
	}{
		{
			name:       "literal plaintext",
			plaintext:  "sk-reflected",
			ciphertext: "cipher-safe",
			body:       `{"data":[{"id":"prefix-sk-reflected-suffix"}]}`,
		},
		{
			name:       "decoded plaintext",
			plaintext:  "sk-reflected",
			ciphertext: "cipher-safe",
			body:       `{"data":[{"id":"sk-\u0072eflected"}]}`,
		},
		{
			name:       "decoded ciphertext",
			plaintext:  "sk-safe",
			ciphertext: "cipher-reflected",
			body:       `{"data":[{"id":"cipher-\u0072eflected"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(*http.Request) (*http.Response, error) {
				return openAIDiscoveryResponse(http.StatusOK, test.body), nil
			}}
			plaintext := []byte(test.plaintext)
			ciphertext := []byte(test.ciphertext)
			result := (ModelDiscoverer{}).Discover(context.Background(), openAIDiscoveryInput(client, plaintext, ciphertext))
			if result.Succeeded() || len(result.Models) != 0 || result.Failure != connectorcontract.DiscoveryFailureProtocol ||
				!result.ResponseReceived || result.UpstreamStatus != http.StatusOK || result.Diagnostic != "upstream models response was rejected" {
				t.Fatalf("result = %+v", result)
			}
			formatted := fmt.Sprintf("%+v", result)
			if strings.Contains(formatted, test.plaintext) || strings.Contains(formatted, test.ciphertext) ||
				!discoveryBytesCleared(plaintext) || !discoveryBytesCleared(ciphertext) {
				t.Fatalf("credential reflection leaked: result=%s plaintext=%v ciphertext=%v", formatted, plaintext, ciphertext)
			}
		})
	}
}
