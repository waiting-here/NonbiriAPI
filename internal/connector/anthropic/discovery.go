package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

const (
	MaxDiscoveryPages      = 20
	MaxDiscoveredModels    = 1000
	MaxModelsResponseBytes = 1 << 20
	MaxCursorBytes         = 512
	maxDiscoveryDiagnostic = 512
)

type ModelDiscoverer struct{}

func (ModelDiscoverer) Discover(ctx context.Context, input connectorcontract.DiscoveryInput) connectorcontract.DiscoveryResult {
	failed := func(message string) connectorcontract.DiscoveryResult {
		return connectorcontract.DiscoveryResult{Diagnostic: diagnostic.BoundTo(message, maxDiscoveryDiagnostic)}
	}
	if ctx == nil || backend.IsNil(input.Backend) || input.Target.Type() != connectorcontract.TypeAnthropicCompatible || input.Credential == nil {
		if input.Credential != nil {
			input.Credential.Clear()
		}
		return failed("model discovery is unavailable")
	}
	if ctx.Err() != nil {
		input.Credential.Clear()
		return failed("model discovery canceled")
	}
	client, err := input.Backend.Open(input.Target.BaseURL())
	if err != nil || nilEndpointClient(client) {
		input.Credential.Clear()
		return failed("egress client unavailable")
	}
	plaintext, ciphertext, ok := input.Credential.Take()
	wireGuard := newSensitiveGuard(plaintext, ciphertext)
	semanticGuard := wireGuard.clone()
	defer wireGuard.Clear()
	defer semanticGuard.Clear()
	clear(ciphertext)
	if !ok {
		return failed("credential unavailable")
	}
	defer clear(plaintext)

	models := make([]connectorcontract.DiscoveredModel, 0, 100)
	seenIDs := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	cursor := ""
	remaining := int64(MaxModelsResponseBytes)
	for page := 0; page < MaxDiscoveryPages; page++ {
		if ctx.Err() != nil {
			return failed("model discovery canceled")
		}
		requestURL, err := modelsPageURL(client.BaseURL(), cursor)
		if err != nil {
			return failed("upstream request could not be built")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return failed("upstream request could not be built")
		}
		request.Header.Set("X-Api-Key", string(plaintext))
		request.Header.Set("Anthropic-Version", AnthropicVersion)
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		request.Header.Del("X-Api-Key")
		if response != nil && response.Request != nil {
			response.Request.Header.Del("X-Api-Key")
		}
		if err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if ctx.Err() != nil {
				return failed("model discovery canceled")
			}
			return failed("upstream request failed")
		}
		if response == nil || response.Body == nil {
			return failed("upstream response was unavailable")
		}
		if ctx.Err() != nil {
			_ = response.Body.Close()
			return failed("model discovery canceled")
		}
		if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
			_ = response.Body.Close()
			return failed(fmt.Sprintf("upstream returned status %d", response.StatusCode))
		}
		if !validResponseMediaType(response, "application/json") {
			_ = response.Body.Close()
			return failed("upstream response content type was invalid")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, remaining+1))
		_ = response.Body.Close()
		if readErr != nil {
			clear(body)
			if ctx.Err() != nil {
				return failed("model discovery canceled")
			}
			return failed("upstream response could not be read")
		}
		if int64(len(body)) > remaining {
			clear(body)
			return failed("upstream models response exceeded its limit")
		}
		if wireGuard.Contains(body) {
			clear(body)
			return failed("upstream models response was rejected")
		}
		remaining -= int64(len(body))
		entries, hasMore, lastID, parseErr := parseModelsPage(body)
		clear(body)
		if parseErr != nil {
			return failed("invalid upstream models response")
		}
		for _, entry := range entries {
			if semanticGuard.Contains([]byte(entry.ID)) || semanticGuard.Contains([]byte(entry.Provider)) {
				return failed("upstream models response was rejected")
			}
			if _, duplicate := seenIDs[entry.ID]; duplicate || len(models) == MaxDiscoveredModels {
				return failed("invalid upstream models response")
			}
			seenIDs[entry.ID] = struct{}{}
			models = append(models, entry)
		}
		if !hasMore {
			return connectorcontract.DiscoveryResult{Models: models}
		}
		if len(models) >= MaxDiscoveredModels || len(entries) == 0 || page+1 == MaxDiscoveryPages {
			return failed("invalid upstream models response")
		}
		next := lastID
		if len([]byte(next)) > MaxCursorBytes || next == cursor {
			return failed("invalid upstream models response")
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return failed("invalid upstream models response")
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
	return failed("invalid upstream models response")
}

func ModelsURL(baseURL string) string { return strings.TrimSuffix(baseURL, "/") + "/models" }

func modelsPageURL(baseURL, cursor string) (string, error) {
	parsed, err := url.Parse(ModelsURL(baseURL))
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("limit", "100")
	if cursor != "" {
		query.Set("after_id", cursor)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func parseModelsPage(body []byte) ([]connectorcontract.DiscoveredModel, bool, string, error) {
	root, err := strictObject(body)
	if err != nil || !onlyKeys(root, "data", "has_more", "first_id", "last_id") || len(root) != 4 {
		return nil, false, "", ErrInvalidResponse
	}
	var hasMore bool
	if json.Unmarshal(root["has_more"], &hasMore) != nil {
		return nil, false, "", ErrInvalidResponse
	}
	firstID, firstPresent, err := parsePageAnchor(root["first_id"])
	if err != nil {
		return nil, false, "", err
	}
	lastID, lastPresent, err := parsePageAnchor(root["last_id"])
	if err != nil {
		return nil, false, "", err
	}
	var entries []json.RawMessage
	if json.Unmarshal(root["data"], &entries) != nil || len(entries) > 100 {
		return nil, false, "", ErrInvalidResponse
	}
	if len(entries) == 0 {
		if firstPresent || lastPresent || hasMore {
			return nil, false, "", ErrInvalidResponse
		}
		return []connectorcontract.DiscoveredModel{}, false, "", nil
	}
	if !firstPresent || !lastPresent {
		return nil, false, "", ErrInvalidResponse
	}
	models := make([]connectorcontract.DiscoveredModel, 0, len(entries))
	for _, raw := range entries {
		entry, err := strictObject(raw)
		if err != nil || !onlyKeys(entry, "id", "type", "display_name", "created_at", "capabilities", "max_input_tokens", "max_tokens") {
			return nil, false, "", ErrInvalidResponse
		}
		var id, kind string
		if json.Unmarshal(entry["id"], &id) != nil || !validOpaqueRunes(id, 512, true) ||
			json.Unmarshal(entry["type"], &kind) != nil || kind != "model" {
			return nil, false, "", ErrInvalidResponse
		}
		if rawDisplay, ok := entry["display_name"]; ok {
			if bytes.Equal(bytes.TrimSpace(rawDisplay), []byte("null")) {
				return nil, false, "", ErrInvalidResponse
			}
			var display string
			if json.Unmarshal(rawDisplay, &display) != nil || !validOpaqueRunes(display, 512, false) {
				return nil, false, "", ErrInvalidResponse
			}
		}
		if rawCreated, ok := entry["created_at"]; ok {
			if bytes.Equal(bytes.TrimSpace(rawCreated), []byte("null")) {
				return nil, false, "", ErrInvalidResponse
			}
			var created string
			if json.Unmarshal(rawCreated, &created) != nil || created == "" || len(created) > 128 {
				return nil, false, "", ErrInvalidResponse
			}
			if _, err := time.Parse(time.RFC3339, created); err != nil {
				return nil, false, "", ErrInvalidResponse
			}
		}
		if validateModelCapabilities(entry["capabilities"]) != nil || validateNullableNonnegativeInt(entry["max_input_tokens"]) != nil || validateNullableNonnegativeInt(entry["max_tokens"]) != nil {
			return nil, false, "", ErrInvalidResponse
		}
		models = append(models, connectorcontract.DiscoveredModel{ID: id, Provider: "anthropic"})
	}
	if models[0].ID != firstID || models[len(models)-1].ID != lastID {
		return nil, false, "", ErrInvalidResponse
	}
	return models, hasMore, lastID, nil
}

func parsePageAnchor(raw []byte) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	var value string
	if len(trimmed) == 0 || json.Unmarshal(trimmed, &value) != nil || !validOpaqueRunes(value, 512, true) || len([]byte(value)) > MaxCursorBytes {
		return "", false, ErrInvalidResponse
	}
	return value, true, nil
}

func validateNullableNonnegativeInt(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var value int64
	if json.Unmarshal(trimmed, &value) != nil || value < 0 {
		return ErrInvalidResponse
	}
	return nil
}

func validateModelCapabilities(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	object, err := strictObject(trimmed)
	if err != nil || !onlyKeys(object, "batch", "citations", "code_execution", "context_management", "effort", "image_input", "pdf_input", "structured_outputs", "thinking") || len(object) != 9 {
		return ErrInvalidResponse
	}
	for _, name := range []string{"batch", "citations", "code_execution", "image_input", "pdf_input", "structured_outputs"} {
		if validateCapabilitySupport(object[name]) != nil {
			return ErrInvalidResponse
		}
	}
	contextObject, err := strictObject(object["context_management"])
	if err != nil || !onlyKeys(contextObject, "clear_thinking_20251015", "clear_tool_uses_20250919", "compact_20260112", "supported") {
		return ErrInvalidResponse
	}
	var contextSupported bool
	if json.Unmarshal(contextObject["supported"], &contextSupported) != nil {
		return ErrInvalidResponse
	}
	for _, name := range []string{"clear_thinking_20251015", "clear_tool_uses_20250919", "compact_20260112"} {
		if rawValue, ok := contextObject[name]; ok && validateOptionalCapabilitySupport(rawValue) != nil {
			return ErrInvalidResponse
		}
	}
	effortObject, err := strictObject(object["effort"])
	if err != nil || !onlyKeys(effortObject, "high", "low", "max", "medium", "xhigh", "supported") {
		return ErrInvalidResponse
	}
	var effortSupported bool
	if json.Unmarshal(effortObject["supported"], &effortSupported) != nil {
		return ErrInvalidResponse
	}
	for _, name := range []string{"high", "low", "max", "medium"} {
		if validateCapabilitySupport(effortObject[name]) != nil {
			return ErrInvalidResponse
		}
	}
	if rawXHigh, ok := effortObject["xhigh"]; ok && validateOptionalCapabilitySupport(rawXHigh) != nil {
		return ErrInvalidResponse
	}
	thinkingObject, err := strictObject(object["thinking"])
	if err != nil || !onlyKeys(thinkingObject, "supported", "types") || len(thinkingObject) != 2 {
		return ErrInvalidResponse
	}
	var thinkingSupported bool
	if json.Unmarshal(thinkingObject["supported"], &thinkingSupported) != nil {
		return ErrInvalidResponse
	}
	typesObject, err := strictObject(thinkingObject["types"])
	if err != nil || !onlyKeys(typesObject, "adaptive", "enabled") || len(typesObject) != 2 {
		return ErrInvalidResponse
	}
	for _, name := range []string{"adaptive", "enabled"} {
		if validateCapabilitySupport(typesObject[name]) != nil {
			return ErrInvalidResponse
		}
	}
	return nil
}

func validateOptionalCapabilitySupport(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return validateCapabilitySupport(raw)
}

func validateCapabilitySupport(raw []byte) error {
	object, err := strictObject(raw)
	if err != nil || !onlyKeys(object, "supported") || len(object) != 1 {
		return ErrInvalidResponse
	}
	var supported bool
	if json.Unmarshal(object["supported"], &supported) != nil {
		return ErrInvalidResponse
	}
	return nil
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
