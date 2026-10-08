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

func imagesURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/images/generations"
}

// AttemptImage forwards one image generation attempt through the shared outbound boundary.
func (a *Adapter) AttemptImage(ctx context.Context, writer http.ResponseWriter, target Target, request *ImageRequest, policy connectorcontract.AttemptPolicy) (result AttemptResult) {
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
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, imagesURL(client.BaseURL()), bytes.NewReader(body))
	if err != nil || len(target.credential.bearer) == 0 {
		return result
	}
	if requestadaptation.ApplyAddedHeaders(httpRequest.Header, policy.AdditionalHeaders) != nil {
		return result
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if request.Stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	}
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
	if err := connectorcontract.ObserveUpstreamResponse(writer, response.StatusCode, request.Stream); err != nil {
		return AttemptResult{Failure: FailureInternal, Diagnostic: "upstream response checkpoint failed"}
	}
	if response.StatusCode != http.StatusOK {
		result = connectorcontract.UpstreamFailed(upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode), connectorcontract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, a.maxImageResponseBytes)
		return result
	}
	if request.Stream {
		if !validResponseMediaType(response, "text/event-stream") {
			return upstreamFailure("upstream stream content type was invalid", response.StatusCode)
		}
		return a.imageStream(ctx, writer, response, guard, errorContext, request)
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	if response.ContentLength > a.maxImageResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	raw, err := readResponseBody(response.Body, a.maxImageResponseBytes)
	if err != nil {
		failed := upstreamFailure(classifyReadFailure(err), response.StatusCode)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return connectorcontract.ReadFailed(failed, err, ctx)
	}
	defer clear(raw)
	projected, usage, _, err := projectImageResponse(raw, request, false)
	if err != nil {
		result = upstreamFailure("upstream response was invalid", response.StatusCode)
		if upstreamerror.IsEvent(raw) {
			upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
			result.ErrorDetail = errorContext.Parse(raw)
			result = connectorcontract.UpstreamFailed(result, connectorcontract.OriginUpstreamResponse)
		}
		return result
	}
	defer clear(projected)
	clear(raw)
	if int64(len(projected)) > a.maxImageResponseBytes || guard.containsImageProjection(projected, false) {
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
