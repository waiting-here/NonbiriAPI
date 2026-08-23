package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/model"
)

type discoveryStatusBackend struct {
	client *discoveryStatusClient
}

func (b discoveryStatusBackend) Open(string) (backend.EndpointClient, error) {
	return b.client, nil
}

func (discoveryStatusBackend) MaxResponseBytes() int64 { return 1 << 20 }

type discoveryStatusClient struct {
	baseURL string
	body    string
}

func (c *discoveryStatusClient) BaseURL() string { return c.baseURL }

func (c *discoveryStatusClient) Do(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Request:    request,
	}, nil
}

func TestModelDiscovererStatusDiagnosticNeverIncludesRawErrorBody(t *testing.T) {
	baseURL := "https://upstream.example/" + strings.Repeat("u", 80)
	boundaryKey := "nb-boundary-key-" + strings.Repeat("K", 80)
	maximumKey := strings.Repeat("M", endpoint.MaxSecretBytes)
	tests := []struct {
		name      string
		key       string
		body      string
		forbidden string
	}{
		{
			name:      "credential crossing diagnostic boundary",
			key:       boundaryKey,
			body:      strings.Repeat("x", 450) + boundaryKey,
			forbidden: boundaryKey[:24],
		},
		{
			name:      "maximum accepted credential length",
			key:       maximumKey,
			body:      maximumKey,
			forbidden: maximumKey[:64],
		},
		{
			name:      "base URL crossing diagnostic boundary",
			key:       "ordinary-test-credential",
			body:      strings.Repeat("x", 450) + baseURL,
			forbidden: baseURL[:32],
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &discoveryStatusClient{baseURL: baseURL, body: test.body}
			result := (openai.ModelDiscoverer{}).Discover(context.Background(), connectorcontract.DiscoveryInput{
				Backend:    discoveryStatusBackend{client: client},
				Target:     connectorcontract.NewTarget(connectorcontract.TypeOpenAICompatible, baseURL, ""),
				Credential: connectorcontract.NewShortLivedSecret([]byte(test.key), nil),
			})
			if result.Diagnostic != "upstream returned status 502" {
				t.Fatal("status diagnostic was not reduced to the stable local category")
			}
			if strings.Contains(result.Diagnostic, test.forbidden) {
				t.Fatal("status diagnostic retained sensitive upstream body material")
			}
		})
	}
}

func TestParseModelsUsesFrozenUnicodeModelIDBoundary(t *testing.T) {
	if openai.MaxDiscoveredModelIDRunes != model.MaxUpstreamModelRunes {
		t.Fatal("model discovery and downstream binding bounds differ")
	}
	tests := []struct {
		name    string
		runes   int
		wantErr bool
	}{
		{name: "511 code points", runes: 511},
		{name: "512 code points", runes: 512},
		{name: "513 code points", runes: 513, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modelID := strings.Repeat("界", test.runes)
			body, err := json.Marshal(map[string]any{
				"object": "list",
				"data":   []map[string]string{{"id": modelID, "owned_by": "provider"}},
			})
			if err != nil {
				t.Fatal("could not encode model boundary fixture")
			}
			models, err := openai.ParseModels(body)
			if test.wantErr {
				if !errors.Is(err, openai.ErrModelIDTooLong) || models != nil {
					t.Fatal("over-limit Unicode model id was accepted")
				}
				return
			}
			if err != nil || len(models) != 1 || utf8.RuneCountInString(models[0].ID) != test.runes {
				t.Fatal("in-range Unicode model id was rejected or changed")
			}
		})
	}
}
