package openai

import (
	"bytes"
	"context"
	"mime"
	"net/http"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// AttemptInference forwards a single closed operation through the shared
// backend. It owns no route selection, retries, credentials or persisted files.
func (a *Adapter) AttemptInference(ctx context.Context, writer http.ResponseWriter, target Target, request *InferenceRequest, kind contract.Type, policy contract.AttemptPolicy) (result AttemptResult) {
	defer func() { result = contract.NormalizeOutcome(result) }()
	defer target.credential.clear()
	result = AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	if a == nil || a.backend == nil || ctx == nil || writer == nil || request == nil || request.CheckTarget(kind, policy) != nil {
		return result
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}
	body, contentType, err := request.marshalInference(target.upstreamModel, kind, policy)
	if err != nil {
		return result
	}
	defer clear(body)
	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return contract.NormalizeOutcome(AttemptResult{Failure: FailureUpstream, Diagnostic: "upstream endpoint was refused"})
	}
	path := inferencePath(request.operation)
	if kind == contract.TypeOpenAIChatCompletions {
		path = "/chat/completions"
	}
	url := strings.TrimSuffix(client.BaseURL(), "/") + path
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil || len(target.credential.bearer) == 0 {
		return result
	}
	if requestadaptation.ApplyAddedHeaders(httpRequest.Header, policy.AdditionalHeaders) != nil || sessionheader.Apply(httpRequest, policy.ManagedHeaders, policy.OnRequestSent) != nil {
		return result
	}
	request.applyOptionHeaders(httpRequest.Header)
	httpRequest.Header.Set("Content-Type", contentType)
	httpRequest.Header.Set("Accept", "application/json")
	if request.Stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else if request.operation == contract.OperationAudioSpeech {
		httpRequest.Header.Set("Accept", "audio/*, application/octet-stream")
	} else if request.operation == contract.OperationAudioTranscriptions || request.operation == contract.OperationAudioTranslations {
		httpRequest.Header.Set("Accept", "application/json, text/plain, text/vtt, application/x-subrip")
	}
	// Secret extraction reads the logical JSON fields, never binary uploads.
	logical, err := request.LogicalBody()
	if err != nil {
		return result
	}
	defer clear(logical)
	sentSecrets, err := requestadaptation.SentSecrets(policy.ResponseSecrets, httpRequest.Header, logical)
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
	errorContext := upstreamerror.Context{CharityCall: smartbackoff.IsCharityCall(ctx), Rules: smartbackoff.RulesFromContext(ctx), BaseURL: target.baseURL, PrivateModel: target.upstreamModel, ContainsSecret: func(value []byte) bool {
		scanner := errorGuard.clone()
		defer scanner.Clear()
		return scanner.Contains(value)
	}}
	if policy.HasAdaptation || len(policy.ManagedHeaders) > 0 {
		errorContext.ContainsSecret = func(value []byte) bool { return len(value) > 0 }
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(target.credential.bearer))
	target.credential.clear()
	limit := a.maxJSONResponseBytes
	if request.operation == contract.OperationImagesEdits || request.operation == contract.OperationImagesVariations || request.operation == contract.OperationAudioSpeech {
		limit = a.maxImageResponseBytes
	}
	streamLimit := a.maxStreamBytes
	lineLimit, eventLimit := a.maxSSELineBytes, a.maxSSEEventBytes
	if request.operation == contract.OperationImagesEdits || request.operation == contract.OperationAudioSpeech {
		streamLimit = limit
		lineLimit, eventLimit = int(limit), int(limit)
	}
	httpRequest = egress.WithResponseLimits(httpRequest, egress.ResponseLimits{JSONBytes: limit, StreamBytes: streamLimit, LineBytes: lineLimit, EventBytes: eventLimit})
	response, err := client.Do(httpRequest)
	httpRequest.Header.Del("Authorization")
	clear(body)
	if response != nil {
		defer func() { result.UpstreamStatus = response.StatusCode }()
		if response.Request != nil {
			response.Request.Header.Del("Authorization")
		}
	}
	if response != nil && err != nil {
		if contract.ObserveUpstreamResponse(writer, response.StatusCode, request.Stream, response.Header) != nil {
			return AttemptResult{Failure: FailureInternal, Diagnostic: "upstream response checkpoint failed"}
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
	if contract.ObserveUpstreamResponse(writer, response.StatusCode, request.Stream, response.Header) != nil {
		return AttemptResult{Failure: FailureInternal, Diagnostic: "upstream response checkpoint failed"}
	}
	if response.StatusCode != http.StatusOK {
		result = contract.UpstreamFailed(upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode), contract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, limit)
		return result
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !request.responseMedia(media) {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	if request.Stream {
		if kind == contract.TypeOpenAIChatCompletions {
			return a.responsesChatStream(ctx, writer, response, guard, errorContext, request, streamLimit, lineLimit, eventLimit)
		}
		return a.inferenceStream(ctx, writer, response, guard, errorContext, request, streamLimit, lineLimit, eventLimit)
	}
	if response.ContentLength > limit {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	raw, err := readResponseBody(response.Body, limit)
	if err != nil {
		failed := upstreamFailure(classifyReadFailure(err), response.StatusCode)
		if ctx.Err() != nil {
			failed = canceledFailure()
		}
		return contract.ReadFailed(failed, err, ctx)
	}
	defer clear(raw)
	projected := raw
	usage := Usage{}
	if media == "application/json" {
		if kind == contract.TypeOpenAIChatCompletions {
			projected, usage, err = projectChatAsResponse(raw, request)
		} else {
			switch request.operation {
			case contract.OperationSystemOne:
				projected, usage, err = projectSystemOneResponse(raw, request)
			case contract.OperationRerank:
				projected, usage, err = projectRerankResponse(raw, request, kind)
			case contract.OperationImagesEdits, contract.OperationImagesVariations:
				projected, usage, _, err = projectImageResponse(raw, &ImageRequest{Model: request.Model, Count: request.count}, false)
			default:
				projected, usage, err = nativeResponseProjection(raw, request)
			}
		}
		if err != nil {
			result = upstreamFailure("upstream response was invalid", response.StatusCode)
			result.Usage = usage
			if upstreamerror.IsEvent(raw) {
				upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
				result.ErrorDetail = errorContext.Parse(raw)
				result = contract.UpstreamFailed(result, contract.OriginUpstreamResponse)
			}
			return result
		}
		defer clear(projected)
		if guard.ContainsJSON(projected, projected) {
			result = upstreamFailure("upstream response was rejected", response.StatusCode)
			result.Usage = usage
			return result
		}
	} else if request.operation == contract.OperationAudioSpeech && len(projected) == 0 || guard.ContainsBytes(projected) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	if int64(len(projected)) > limit {
		result = upstreamFailure("upstream response exceeded its limit", response.StatusCode)
		result.Usage = usage
		return result
	}
	defer func() { result = contract.ProtocolSucceeded(result) }()
	if contract.MarkResponseStarted(writer) != nil {
		return sinkFailure(false, usage)
	}
	if ctx.Err() != nil {
		result = canceledFailure()
		result.Usage = usage
		return result
	}
	writer.Header().Set("Content-Type", media)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if len(projected) == 0 {
		writer.WriteHeader(http.StatusOK)
		return AttemptResult{Success: true, Committed: true, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
	}
	n, err := writer.Write(projected)
	if err != nil || n != len(projected) {
		return sinkFailure(n > 0, usage)
	}
	return AttemptResult{Success: true, Committed: n > 0, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
}
