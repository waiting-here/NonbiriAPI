package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type ModelDiscoverer struct{}

func (ModelDiscoverer) Discover(ctx context.Context, input contract.DiscoveryInput) (result contract.DiscoveryResult) {
	defer input.Credential.Clear()
	result = contract.DiscoveryResult{Failure: contract.DiscoveryFailureProtocol, Diagnostic: "gateway model discovery unavailable"}
	defer func() {
		if ctx != nil && ctx.Err() != nil {
			result.Models = nil
			result.Diagnostic = "model discovery canceled"
			result.Failure = contract.DiscoveryFailureInterrupted
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result.Failure = contract.DiscoveryFailureTimeout
			}
		}
	}()
	if ctx == nil || backend.IsNil(input.Backend) || input.Target.Type() != contract.TypeAISDKGatewayV3 {
		return result
	}
	if ctx.Err() != nil {
		return result
	}
	client, err := input.Backend.Open(input.Target.BaseURL())
	if err != nil {
		result.Failure = contract.DiscoveryFailureTransport
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(client.BaseURL(), "/")+"/config", nil)
	if err != nil {
		return result
	}
	plain, cipher, ok := input.Credential.Take()
	if !ok {
		return result
	}
	guard := openai.NewResponseGuard(plain, cipher)
	defer guard.Clear()
	setRequestHeaders(request, plain)
	clear(plain)
	clear(cipher)
	response, err := client.Do(request)
	request.Header.Del("Authorization")
	if response != nil {
		result.ResponseReceived = true
		result.UpstreamStatus = response.StatusCode
		if response.Request != nil {
			response.Request.Header.Del("Authorization")
		}
		if response.Body != nil {
			defer response.Body.Close()
		}
	}
	if err != nil {
		result.Failure = contract.DiscoveryFailureTransport
		return result
	}
	if response == nil || response.Body == nil {
		return result
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		switch response.StatusCode {
		case 401, 403:
			result.Failure = contract.DiscoveryFailureAuth
		case 429:
			result.Failure = contract.DiscoveryFailureRateLimit
		}
		result.Diagnostic = "upstream model discovery returned an error"
		return result
	}
	if !validContentType(response, "application/json") {
		return result
	}
	body, err := readBounded(response.Body, min(int64(4<<20), input.Backend.MaxResponseBytes()))
	if err != nil {
		return result
	}
	defer clear(body)
	root, err := parseObject(body)
	if err != nil {
		return result
	}
	var models []json.RawMessage
	if json.Unmarshal(root["models"], &models) != nil || models == nil || len(models) > 4096 {
		return result
	}
	result.Models = make([]contract.DiscoveredModel, 0, len(models))
	seen := map[string]bool{}
	for _, raw := range models {
		entry, err := parseObject(raw)
		kind, ok := text(entry["modelType"])
		if err != nil {
			result.Models = nil
			return result
		}
		if len(entry["modelType"]) == 0 || isNull(entry["modelType"]) {
			continue
		}
		if !ok {
			result.Models = nil
			return result
		}
		// Catalogs may contain image, video, speech, transcription, reranking
		// and future model types. They are not chat/embedding candidates.
		if kind != "language" && kind != "embedding" {
			continue
		}
		id, ok := text(entry["id"])
		if !ok || !opaque(id, 512) || seen[id] || len(result.Models) >= 1000 {
			result.Models = nil
			return result
		}
		seen[id] = true
		provider, _, found := strings.Cut(id, "/")
		if !found || !opaque(provider, 64) {
			provider = "gateway"
		}
		projected, _ := json.Marshal(map[string]string{"id": id, "provider": provider})
		reflected := guard.ContainsJSON(projected, projected)
		clear(projected)
		if reflected {
			result.Models = nil
			return result
		}
		result.Models = append(result.Models, contract.DiscoveredModel{ID: id, Provider: provider})
	}
	result.Failure = contract.DiscoveryFailureNone
	result.Diagnostic = ""
	return result
}
