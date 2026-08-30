package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

const (
	MaxDiscoveredModels        = 1000
	MaxDiscoveredModelIDRunes  = 512
	MaxDiscoveredProviderRunes = 128
	MaxModelsBodyBytes         = 1 << 20
	maxDiscoveryDiagnostic     = 512
)

var (
	ErrModelsDataMissing       = errors.New("models data is missing or not an array")
	ErrTooManyModels           = errors.New("model list is too large")
	ErrEmptyModelID            = errors.New("model id is empty")
	ErrModelIDTooLong          = errors.New("model id is too long")
	ErrInvalidModelID          = errors.New("model id contains invalid characters")
	ErrProviderTooLong         = errors.New("model provider is too long")
	ErrInvalidProvider         = errors.New("model provider contains invalid characters")
	ErrDuplicateModelID        = errors.New("duplicate model id in model list")
	ErrModelsResponseTruncated = errors.New("models response is truncated")
	ErrMalformedModelsJSON     = errors.New("malformed models JSON")
)

// ModelDiscoverer is the OpenAI-compatible registry capability. It performs
// one GET /models through the supplied shared backend, parses a bounded strict
// response, and never reads or writes the model cache itself.
type ModelDiscoverer struct{}

func (ModelDiscoverer) Discover(ctx context.Context, input connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
	if ctx == nil {
		if input.Credential != nil {
			input.Credential.Clear()
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, nil, "model discovery is unavailable")
	}
	if kind, failed := discoveryContextFailure(ctx, nil); failed {
		if input.Credential != nil {
			input.Credential.Clear()
		}
		return failedDiscoveryResult(kind, nil, discoveryContextDiagnostic(kind))
	}
	if backend.IsNil(input.Backend) || input.Target.Type() != connectorcontract.TypeOpenAICompatible || input.Credential == nil {
		if input.Credential != nil {
			input.Credential.Clear()
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, nil, "model discovery is unavailable")
	}
	client, err := input.Backend.Open(input.Target.BaseURL())
	if err != nil || nilEndpointClient(client) {
		input.Credential.Clear()
		if kind, failed := discoveryContextFailure(ctx, err); failed {
			return failedDiscoveryResult(kind, nil, discoveryContextDiagnostic(kind))
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureTransport, nil, "egress client unavailable")
	}
	plaintext, ciphertext, ok := input.Credential.Take()
	if !ok {
		clear(ciphertext)
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, nil, "credential unavailable")
	}
	wireGuard := newSensitiveGuard(plaintext, ciphertext)
	semanticGuard := wireGuard.clone()
	defer wireGuard.Clear()
	defer semanticGuard.Clear()
	clear(ciphertext)
	defer clear(plaintext)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsURL(client.BaseURL()), nil)
	if err != nil {
		if kind, failed := discoveryContextFailure(ctx, err); failed {
			return failedDiscoveryResult(kind, nil, discoveryContextDiagnostic(kind))
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, nil, "upstream request could not be built")
	}
	request.Header.Set("Authorization", "Bearer "+string(plaintext))
	request.Header.Set("Accept", "application/json")
	clear(plaintext)
	response, err := client.Do(request)
	request.Header.Del("Authorization")
	if response != nil && response.Request != nil {
		response.Request.Header.Del("Authorization")
	}
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if kind, failed := discoveryContextFailure(ctx, err); failed {
			return failedDiscoveryResult(kind, response, discoveryContextDiagnostic(kind))
		}
		if response != nil {
			kind := connectorcontract.DiscoveryFailureProtocol
			if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
				kind = discoveryStatusFailure(response.StatusCode)
			}
			return failedDiscoveryResult(kind, response, "upstream request failed")
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureTransport, nil, "upstream request failed")
	}
	if kind, failed := discoveryContextFailure(ctx, nil); failed {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return failedDiscoveryResult(kind, response, discoveryContextDiagnostic(kind))
	}
	if response == nil {
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureTransport, nil, "upstream response was unavailable")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		// Error bodies are fully untrusted and commonly echo Authorization,
		// request URLs, or other owner-only values. Do not transform, truncate,
		// inspect, or persist any fragment: searching after a byte/rune boundary
		// can miss a sensitive value that straddles the boundary and leak its
		// prefix. The local status category is sufficient for the issue rail.
		return failedDiscoveryResult(discoveryStatusFailure(response.StatusCode), response, fmt.Sprintf("upstream returned status %d", response.StatusCode))
	}
	if response.Body == nil {
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "upstream response was unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if !validResponseMediaType(response, "application/json") {
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "upstream response content type was invalid")
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, MaxModelsBodyBytes+1))
	if err != nil {
		clear(body)
		if kind, failed := discoveryContextFailure(ctx, err); failed {
			return failedDiscoveryResult(kind, response, discoveryContextDiagnostic(kind))
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "upstream response could not be read")
	}
	defer clear(body)
	if kind, failed := discoveryContextFailure(ctx, nil); failed {
		return failedDiscoveryResult(kind, response, discoveryContextDiagnostic(kind))
	}
	if wireGuard.Contains(body) {
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "upstream models response was rejected")
	}
	models, err := ParseModels(body)
	if err != nil {
		if errors.Is(err, ErrModelsResponseTruncated) {
			return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, ErrModelsResponseTruncated.Error())
		}
		return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "invalid upstream models response")
	}
	for _, model := range models {
		if containsSensitiveDiscoveryText(semanticGuard, model.ID) || containsSensitiveDiscoveryText(semanticGuard, model.Provider) {
			return failedDiscoveryResult(connectorcontract.DiscoveryFailureProtocol, response, "upstream models response was rejected")
		}
	}
	return connectorcontract.DiscoveryResult{
		Models:           models,
		Failure:          connectorcontract.DiscoveryFailureNone,
		UpstreamStatus:   response.StatusCode,
		ResponseReceived: true,
	}
}

func failedDiscoveryResult(kind connectorcontract.DiscoveryFailureKind, response *http.Response, message string) connectorcontract.DiscoveryResult {
	status := 0
	received := response != nil
	if received {
		status = response.StatusCode
	}
	return connectorcontract.DiscoveryResult{
		Failure:          kind,
		UpstreamStatus:   status,
		ResponseReceived: received,
		Diagnostic:       diagnostic.BoundTo(message, maxDiscoveryDiagnostic),
	}
}

func discoveryStatusFailure(status int) connectorcontract.DiscoveryFailureKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return connectorcontract.DiscoveryFailureAuth
	case http.StatusTooManyRequests:
		return connectorcontract.DiscoveryFailureRateLimit
	default:
		return connectorcontract.DiscoveryFailureProtocol
	}
}

func discoveryContextFailure(ctx context.Context, err error) (connectorcontract.DiscoveryFailureKind, bool) {
	if ctx != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return connectorcontract.DiscoveryFailureTimeout, true
	case errors.Is(err, context.Canceled):
		return connectorcontract.DiscoveryFailureInterrupted, true
	default:
		return connectorcontract.DiscoveryFailureNone, false
	}
}

func discoveryContextDiagnostic(kind connectorcontract.DiscoveryFailureKind) string {
	if kind == connectorcontract.DiscoveryFailureTimeout {
		return "model discovery timed out"
	}
	return "model discovery canceled"
}

func containsSensitiveDiscoveryText(guard *sensitiveGuard, value string) bool {
	data := []byte(value)
	matched := guard.Contains(data)
	clear(data)
	return matched
}

func ModelsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/models"
}

// ParseModels strictly validates an OpenAI-compatible list-models response.
// It returns connector-neutral cache rows and never a partial list.
func ParseModels(body []byte) ([]connectorcontract.DiscoveredModel, error) {
	if len(body) > MaxModelsBodyBytes {
		return nil, ErrModelsResponseTruncated
	}
	var root struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedModelsJSON, err)
	}
	if len(root.Data) == 0 {
		return nil, ErrModelsDataMissing
	}
	trimmed := bytes.TrimSpace(root.Data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, ErrModelsDataMissing
	}
	var entries []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedModelsJSON, err)
	}
	if len(entries) > MaxDiscoveredModels {
		return nil, ErrTooManyModels
	}
	seen := make(map[string]struct{}, len(entries))
	models := make([]connectorcontract.DiscoveredModel, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			return nil, ErrEmptyModelID
		}
		if utf8.RuneCountInString(id) > MaxDiscoveredModelIDRunes {
			return nil, ErrModelIDTooLong
		}
		if !validDiscoveryText(id) {
			return nil, ErrInvalidModelID
		}
		provider := strings.TrimSpace(entry.OwnedBy)
		if utf8.RuneCountInString(provider) > MaxDiscoveredProviderRunes {
			return nil, ErrProviderTooLong
		}
		if provider != "" && !validDiscoveryText(provider) {
			return nil, ErrInvalidProvider
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrDuplicateModelID
		}
		seen[id] = struct{}{}
		models = append(models, connectorcontract.DiscoveredModel{ID: id, Provider: provider})
	}
	return models, nil
}

func validDiscoveryText(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return false
		}
	}
	return true
}

func nilEndpointClient(client backend.EndpointClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
