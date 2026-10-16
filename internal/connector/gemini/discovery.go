package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type ModelDiscoverer struct{}

func (ModelDiscoverer) Discover(ctx context.Context, input contract.DiscoveryInput) (result contract.DiscoveryResult) {
	defer input.Credential.Clear()
	result = contract.DiscoveryResult{Failure: contract.DiscoveryFailureProtocol, Diagnostic: "model discovery failed"}
	defer func() {
		if ctx != nil {
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				result.Failure = contract.DiscoveryFailureTimeout
			case errors.Is(ctx.Err(), context.Canceled):
				result.Failure = contract.DiscoveryFailureInterrupted
			}
		}
	}()
	if ctx == nil || backend.IsNil(input.Backend) || input.Target.Type() != contract.TypeGoogleGemini {
		return result
	}
	client, err := input.Backend.Open(input.Target.BaseURL())
	if err != nil {
		result.Failure = contract.DiscoveryFailureTransport
		return result
	}
	key, ciphertext, ok := input.Credential.Take()
	if !ok {
		return result
	}
	defer clear(key)
	defer clear(ciphertext)
	guard := openai.NewResponseGuard(key, ciphertext)
	defer guard.Clear()
	var total int64
	count := 0
	pageToken := ""
	seenPages := map[string]bool{}
	seenModels := map[string]bool{}
	models := []contract.DiscoveredModel{}
	for page := 0; page < 20; page++ {
		endpoint := strings.TrimSuffix(client.BaseURL(), "/") + "/models?pageSize=100"
		if pageToken != "" {
			endpoint += "&pageToken=" + url.QueryEscape(pageToken)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return result
		}
		request.Header.Set("X-Goog-Api-Key", string(key))
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		request.Header.Del("X-Goog-Api-Key")
		if response != nil {
			result.ResponseReceived = true
			result.UpstreamStatus = response.StatusCode
			if response.Request != nil {
				response.Request.Header.Del("X-Goog-Api-Key")
			}
		}
		if err != nil || response == nil || response.Body == nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			result.Failure = contract.DiscoveryFailureTransport
			return result
		}
		if response.StatusCode != 200 {
			_ = response.Body.Close()
			switch response.StatusCode {
			case 401, 403:
				result.Failure = contract.DiscoveryFailureAuth
			case 429:
				result.Failure = contract.DiscoveryFailureRateLimit
			}
			return result
		}
		media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || response.Header.Get("Content-Encoding") != "" || len(response.Header.Values("Content-Type")) != 1 {
			_ = response.Body.Close()
			return result
		}
		for name, value := range params {
			if name != "charset" || !strings.EqualFold(value, "utf-8") {
				_ = response.Body.Close()
				return result
			}
		}
		limit := min(int64(openai.MaxModelsBodyBytes), input.Backend.MaxResponseBytes()) - total
		if limit <= 0 {
			_ = response.Body.Close()
			return result
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		total += int64(len(raw))
		if err != nil || int64(len(raw)) > limit || guard.ContainsJSON(raw, raw) {
			clear(raw)
			return result
		}
		root, err := chatwire.Object(raw)
		clear(raw)
		if err != nil {
			return result
		}
		var entries []json.RawMessage
		if json.Unmarshal(root["models"], &entries) != nil || chatwire.Null(root["models"]) {
			return result
		}
		count += len(entries)
		if count > openai.MaxDiscoveredModels {
			return result
		}
		for _, entry := range entries {
			model, err := chatwire.Object(entry)
			if err != nil {
				return result
			}
			name, ok := chatwire.String(model["name"])
			if !ok || !strings.HasPrefix(name, "models/") || !modelPattern.MatchString(strings.TrimPrefix(name, "models/")) {
				return result
			}
			var methods []string
			if json.Unmarshal(model["supportedGenerationMethods"], &methods) != nil {
				return result
			}
			supported := false
			for _, method := range methods {
				supported = supported || method == "generateContent"
			}
			if !supported || seenModels[name] {
				continue
			}
			seenModels[name] = true
			projection, _ := json.Marshal(map[string]any{"data": []any{map[string]string{"id": strings.TrimPrefix(name, "models/"), "owned_by": "google"}}})
			parsed, err := openai.ParseModels(projection)
			clear(projection)
			if err != nil {
				return result
			}
			models = append(models, parsed...)
		}
		pageToken = ""
		if raw, ok := root["nextPageToken"]; ok {
			pageToken, ok = chatwire.String(raw)
			if !ok || len(pageToken) > 4096 {
				return result
			}
		}
		if pageToken == "" {
			result.Models = models
			result.Failure = contract.DiscoveryFailureNone
			result.Diagnostic = ""
			return result
		}
		if seenPages[pageToken] {
			return result
		}
		seenPages[pageToken] = true
	}
	return result
}
