package gemini

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type discoveryBackend struct {
	do func(*http.Request) (*http.Response, error)
}

func (b discoveryBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (discoveryBackend) MaxResponseBytes() int64                       { return 1 << 20 }
func (discoveryBackend) BaseURL() string                               { return "https://provider.example/v1beta" }
func (b discoveryBackend) Do(r *http.Request) (*http.Response, error)  { return b.do(r) }
func TestGeminiDiscoveryPaginationMethodFilterAndSecretGuard(t *testing.T) {
	for _, mode := range []string{"valid", "cycle", "secret", "oversized", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			outbound := discoveryBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("X-Goog-Api-Key") != "secret-value" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("key") != "" {
					t.Fatal("invalid authentication")
				}
				body := `{"models":[{"name":"models/chat-a","supportedGenerationMethods":["generateContent"]},{"name":"models/embed-a","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"a/b?c"}`
				if calls == 2 {
					if r.URL.Query().Get("pageToken") != "a/b?c" {
						t.Fatal(r.URL)
					}
					body = `{"models":[{"name":"models/chat-b","supportedGenerationMethods":["generateContent"]},{"name":"models/chat-a","supportedGenerationMethods":["generateContent"]}]}`
				}
				switch mode {
				case "cycle":
					body = `{"models":[],"nextPageToken":"a/b?c"}`
				case "secret":
					body = `{"models":[{"name":"models/secret-value","supportedGenerationMethods":["generateContent"]}]}`
				case "oversized":
					body = `{"models":[],"metadata":"` + strings.Repeat("a", 1<<20) + `"}`
				case "unsupported":
					body = `{"models":[{"name":"models/embedding","supportedGenerationMethods":["embedContent"]}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			}}
			result := (ModelDiscoverer{}).Discover(context.Background(), contract.DiscoveryInput{Backend: outbound, Target: contract.NewTarget(contract.TypeGoogleGemini, "https://provider.example/v1beta", ""), Credential: contract.NewShortLivedSecret([]byte("secret-value"), nil)})
			if mode == "valid" {
				if result.Failure != contract.DiscoveryFailureNone || len(result.Models) != 2 || fmt.Sprint(result.Models[0]) == "" || calls != 2 {
					t.Fatal(result, calls)
				}
			} else if mode == "unsupported" {
				if result.Failure != contract.DiscoveryFailureNone || len(result.Models) != 0 {
					t.Fatal(result)
				}
			} else if result.Failure != contract.DiscoveryFailureProtocol || len(result.Models) != 0 {
				t.Fatal(result)
			}
		})
	}
}
