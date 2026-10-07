package openai

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func embeddingsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/embeddings"
}

// AttemptEmbedding makes one non-streaming attempt through the same outbound
// boundary as chat. Chat-only policies never alter embedding parameters.
func (a *Adapter) AttemptEmbedding(ctx context.Context, writer http.ResponseWriter, target Target, request *EmbeddingRequest, policy connectorcontract.AttemptPolicy) (result AttemptResult) {
	defer func() { result = connectorcontract.NormalizeOutcome(result) }()
	result = AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || a.backend == nil || ctx == nil || writer == nil || request == nil {
		return result
	}
	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return connectorcontract.NormalizeOutcome(AttemptResult{Failure: connectorcontract.FailureUpstream, Diagnostic: "upstream endpoint was refused"})
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
	if requestadaptation.ApplyAddedHeaders(httpRequest.Header, policy.AdditionalHeaders) != nil {
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
	if policy.HasAdaptation {
		errorContext.ContainsSecret = func(value []byte) bool { return len(value) != 0 }
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(target.credential.bearer))
	target.credential.clear()
	response, err := client.Do(httpRequest)
	if response != nil {
		defer func() { result.UpstreamStatus = response.StatusCode }()
	}
	httpRequest.Header.Del("Authorization")
	clear(body)
	if response != nil && response.Request != nil {
		response.Request.Header.Del("Authorization")
	}
	if err != nil {
		failed := upstreamFailure(classifyTransportFailure(err), 0)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return connectorcontract.TransportFailed(failed, err, ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		result = connectorcontract.UpstreamFailed(upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode), connectorcontract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, a.maxEmbeddingResponseBytes)
		return result
	}
	if !validResponseMediaType(response, "application/json") {
		_ = errorContext.ReadResponse(ctx, response)
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	if response.ContentLength > a.maxEmbeddingResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	raw, err := readResponseBody(response.Body, a.maxEmbeddingResponseBytes)
	if err != nil {
		failed := upstreamFailure(classifyReadFailure(err), response.StatusCode)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return connectorcontract.ReadFailed(failed, err, ctx)
	}
	defer clear(raw)
	projected, usage, err := projectEmbeddingResponse(raw, request)
	if err != nil {
		result = upstreamFailure("upstream response was invalid", response.StatusCode)
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		result.ErrorDetail = errorContext.Parse(raw)
		if upstreamerror.IsEvent(raw) {
			result = connectorcontract.UpstreamFailed(result, connectorcontract.OriginUpstreamResponse)
		}
		return result
	}
	defer clear(projected)
	clear(raw)
	if int64(len(projected)) > a.maxEmbeddingResponseBytes || guard.containsEmbeddingProjection(projected) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	defer func() {
		result = connectorcontract.ProtocolSucceeded(result)
		result.UpstreamStatus = response.StatusCode
	}()
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
