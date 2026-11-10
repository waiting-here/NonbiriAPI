package connector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func TestDiscoveryUsesCompleteAddressThroughProtectedBackend(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeGoogleGemini, contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, path := range []string{"/v1/models", "/v1/config", "/custom/a%2Fb/catalog.json/", "/v1"} {
			t.Run(string(kind)+path, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || r.URL.EscapedPath() != path {
						t.Errorf("configured path changed: %s %s", r.Method, r.URL.EscapedPath())
					}
					w.Header().Set("Content-Type", "application/json")
					switch kind {
					case contract.TypeAnthropicCompatible:
						if r.Header.Get("X-Api-Key") != "catalog-secret" || r.URL.Query().Get("limit") != "100" || calls == 2 && r.URL.Query().Get("after_id") != "first" {
							t.Error("Anthropic authentication or pagination changed")
						}
						fmt.Fprintf(w, `{"data":[{"id":"%s","type":"model"}],"has_more":%t,"first_id":"%s","last_id":"%s"}`, []string{"first", "second"}[calls-1], calls == 1, []string{"first", "second"}[calls-1], []string{"first", "second"}[calls-1])
					case contract.TypeGoogleGemini:
						if r.Header.Get("X-Goog-Api-Key") != "catalog-secret" || r.URL.Query().Get("pageSize") != "100" || calls == 2 && r.URL.Query().Get("pageToken") != "next/page" {
							t.Error("Gemini authentication or pagination changed")
						}
						if calls == 1 {
							fmt.Fprint(w, `{"models":[{"name":"models/first","supportedGenerationMethods":["generateContent"]}],"nextPageToken":"next/page"}`)
						} else {
							fmt.Fprint(w, `{"models":[{"name":"models/second","supportedGenerationMethods":["generateContent"]}]}`)
						}
					default:
						if r.Header.Get("Authorization") != "Bearer catalog-secret" || r.URL.RawQuery != "" {
							t.Error("authentication or query changed")
						}
						if kind.AISDKGateway() {
							fmt.Fprint(w, `{"models":[{"id":"provider/first","modelType":"language"}]}`)
						} else {
							fmt.Fprint(w, `{"data":[{"id":"first"}]}`)
						}
					}
				}))
				defer server.Close()
				stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}})
				if err != nil {
					t.Fatal(err)
				}
				defer stack.CloseIdleConnections()
				if err := stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://127.0.0.2", UserHost: "127.0.0.2", AdminHost: "127.0.0.3", ListenAddr: "127.0.0.1:1"}); err != nil {
					t.Fatal(err)
				}
				outbound, err := backend.NewLocal(stack)
				if err != nil {
					t.Fatal(err)
				}
				address := server.URL + path
				shown, err := DiscoveryModelsURL(kind, address)
				if err != nil || shown != address {
					t.Fatal("display URL differs", shown, err)
				}
				descriptor, _ := NewDefaultRegistry().Descriptor(kind)
				result := descriptor.Discoverer.Discover(t.Context(), contract.DiscoveryInput{Backend: outbound, Target: contract.NewTarget(kind, address, ""), Credential: contract.NewShortLivedSecret([]byte("catalog-secret"), nil)})
				wantCalls := 1
				if kind == contract.TypeGoogleGemini || kind == contract.TypeAnthropicCompatible {
					wantCalls = 2
				}
				if !result.Succeeded() || calls != wantCalls || len(result.Models) != wantCalls {
					t.Fatalf("result=%+v calls=%d", result, calls)
				}
			})
		}
	}
}
