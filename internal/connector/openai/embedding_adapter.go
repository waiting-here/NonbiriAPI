package openai

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func embeddingsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/embeddings"
}

// AttemptEmbedding makes one non-streaming attempt through the same outbound
// boundary as chat. Chat-only policies never alter embedding parameters.
func (a *Adapter) AttemptEmbedding(ctx context.Context, writer http.ResponseWriter, target Target, request *EmbeddingRequest, policy connectorcontract.AttemptPolicy) AttemptResult {
	result := AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || a.backend == nil || ctx == nil || writer == nil || request == nil {
		return result
	}
	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return upstreamFailure("upstream endpoint was refused", 0)
	}
	body, err := request.marshalUpstream(target.upstreamModel, policy.SafetyIdentifier)
	if err != nil {
		return result
	}
	defer clear(body)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsURL(client.BaseURL()), bytes.NewReader(body))
	if err != nil || len(target.credential.bearer) == 0 {
		return result
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	guard := newResponseGuard(target.credential.bearer, target.credential.ciphertext)
	defer guard.Clear()
	identifier := []byte(policy.SafetyIdentifier)
	errorGuard := newSensitiveGuard(target.credential.bearer, target.credential.ciphertext, identifier)
	clear(identifier)
	defer errorGuard.Clear()
	errorContext := upstreamerror.Context{
		BaseURL: target.baseURL, PrivateModel: target.upstreamModel,
		ContainsSecret: func(value []byte) bool {
			scanner := errorGuard.clone()
			defer scanner.Clear()
			return scanner.Contains(value)
		},
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(target.credential.bearer))
	target.credential.clear()
	response, err := client.Do(httpRequest)
	httpRequest.Header.Del("Authorization")
	clear(body)
	if response != nil && response.Request != nil {
		response.Request.Header.Del("Authorization")
	}
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyTransportFailure(err), 0)
	}
	defer response.Body.Close()
	if ctx.Err() != nil {
		return canceledFailure()
	}
	if response.StatusCode != http.StatusOK {
		result = upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
		result.ErrorDetail = errorContext.Read(response.Body, a.maxEmbeddingResponseBytes)
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return result
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	if response.ContentLength > a.maxEmbeddingResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	raw, err := readResponseBody(response.Body, a.maxEmbeddingResponseBytes)
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyReadFailure(err), response.StatusCode)
	}
	defer clear(raw)
	projected, usage, err := projectEmbeddingResponse(raw, request)
	if err != nil {
		result = upstreamFailure("upstream response was invalid", response.StatusCode)
		result.ErrorDetail = errorContext.Parse(raw)
		return result
	}
	defer clear(projected)
	clear(raw)
	if int64(len(projected)) > a.maxEmbeddingResponseBytes || guard.containsEmbeddingProjection(projected) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}
	if err := connectorcontract.MarkResponseStarted(writer); err != nil {
		return sinkFailure(false, usage)
	}
	if ctx.Err() != nil {
		result = canceledFailure()
		result.Usage = usage
		return result
	}
	setJSONResponseHeaders(writer.Header())
	n, err := writer.Write(projected)
	if err != nil || n != len(projected) {
		return sinkFailure(n > 0, usage)
	}
	return AttemptResult{Success: true, Committed: n > 0, Failure: FailureNone,
		UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
}
