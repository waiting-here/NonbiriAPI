package anthropic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type fakeBackend struct {
	maxBytes int64
	open     func(string) (backend.EndpointClient, error)
}

func (b fakeBackend) Open(baseURL string) (backend.EndpointClient, error) {
	if b.open != nil {
		return b.open(baseURL)
	}
	return nil, nil
}

func (b fakeBackend) MaxResponseBytes() int64 {
	if b.maxBytes > 0 {
		return b.maxBytes
	}
	return DefaultMaxStreamBytes
}

type fakeEndpointClient struct {
	baseURL string
	do      func(*http.Request) (*http.Response, error)
}

func (c *fakeEndpointClient) BaseURL() string { return c.baseURL }

func (c *fakeEndpointClient) Do(request *http.Request) (*http.Response, error) {
	if c.do != nil {
		return c.do(request)
	}
	return nil, nil
}

func fakeResponse(status int, contentType, body string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: -1,
	}
}

func mustChatRequest(t *testing.T, body string) *openai.ChatRequest {
	t.Helper()
	request, err := openai.DecodeChatRequest(strings.NewReader(body), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatalf("DecodeChatRequest: %v", err)
	}
	t.Cleanup(request.Clear)
	return request
}

type maxTokensProviderFunc func(context.Context) (*int64, error)

func (f maxTokensProviderFunc) RawAnthropicDefaultMaxTokens(ctx context.Context) (*int64, error) {
	return f(ctx)
}
