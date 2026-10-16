package openai

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// AttemptDecision sends one native Decisions request through shared egress.
func (a *Adapter) AttemptDecision(ctx context.Context, writer http.ResponseWriter, target Target, request *DecisionRequest, policy contract.AttemptPolicy) (result AttemptResult) {
	defer func() { result = contract.NormalizeOutcome(result) }()
	result = AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || a.backend == nil || ctx == nil || writer == nil || request == nil || len(policy.NativeExtensions) != 0 || policy.FlattenToolCalls || policy.ForceStoreFalse {
		return result
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}
	body, err := request.marshalUpstream(target.upstreamModel, policy.SafetyIdentifier)
	if err != nil {
		return result
	}
	defer clear(body)
	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return contract.NormalizeOutcome(AttemptResult{Failure: FailureUpstream, Diagnostic: "upstream endpoint was refused"})
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(client.BaseURL(), "/")+"/decisions", bytes.NewReader(body))
	if err != nil || len(target.credential.bearer) == 0 {
		return result
	}
	if requestadaptation.ApplyAddedHeaders(httpRequest.Header, policy.AdditionalHeaders) != nil || sessionheader.Apply(httpRequest, policy.ManagedHeaders, policy.OnRequestSent) != nil {
		return result
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	sentSecrets, err := requestadaptation.SentSecrets(policy.ResponseSecrets, httpRequest.Header, body)
	if err != nil {
		return result
	}
	identifier := []byte(policy.SafetyIdentifier)
	materials := append([][]byte{target.credential.bearer, target.credential.ciphertext, identifier}, sentSecrets...)
	guard := newResponseGuard(materials...)
	defer guard.Clear()
	errorGuard := newSensitiveGuard(materials...)
	defer errorGuard.Clear()
	clear(identifier)
	errorContext := upstreamerror.Context{BaseURL: target.baseURL, PrivateModel: target.upstreamModel, ContainsSecret: func(value []byte) bool {
		scanner := errorGuard.clone()
		defer scanner.Clear()
		return scanner.Contains(value)
	}}
	if policy.HasAdaptation || len(policy.ManagedHeaders) > 0 {
		errorContext.ContainsSecret = func(value []byte) bool { return len(value) > 0 }
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(target.credential.bearer))
	target.credential.clear()
	response, err := client.Do(httpRequest)
	httpRequest.Header.Del("Authorization")
	clear(body)
	if response != nil {
		defer func() { result.UpstreamStatus = response.StatusCode }()
		if response.Request != nil {
			response.Request.Header.Del("Authorization")
		}
	}
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		failed := upstreamFailure(classifyTransportFailure(err), 0)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return contract.TransportFailed(failed, err, ctx)
	}
	if response == nil || response.Body == nil {
		return upstreamFailure("upstream response was unavailable", 0)
	}
	defer response.Body.Close()
	if err := contract.ObserveUpstreamResponse(writer, response.StatusCode, false); err != nil {
		return AttemptResult{Failure: FailureInternal, Diagnostic: "upstream response checkpoint failed"}
	}
	if response.StatusCode != http.StatusOK {
		result = contract.UpstreamFailed(upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode), contract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, a.maxJSONResponseBytes)
		return result
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	if response.ContentLength > a.maxJSONResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	raw, err := readResponseBody(response.Body, a.maxJSONResponseBytes)
	if err != nil {
		failed := upstreamFailure(classifyReadFailure(err), response.StatusCode)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return contract.ReadFailed(failed, err, ctx)
	}
	defer clear(raw)
	projected, usage, err := projectDecisionResponse(raw, request)
	if err != nil {
		result = upstreamFailure("upstream response was invalid", response.StatusCode)
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		result.ErrorDetail = errorContext.Parse(raw)
		if upstreamerror.IsEvent(raw) {
			result = contract.UpstreamFailed(result, contract.OriginUpstreamResponse)
		}
		return result
	}
	defer clear(projected)
	if int64(len(projected)) > a.maxJSONResponseBytes || guard.ContainsJSON(projected, projected) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	defer func() { result = contract.ProtocolSucceeded(result) }()
	if err := contract.MarkResponseStarted(writer); err != nil {
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
	return AttemptResult{Success: true, Committed: n > 0, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
}
