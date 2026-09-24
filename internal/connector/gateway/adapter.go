package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const maxJSONBytes int64 = 8 << 20
const maxStreamBytes int64 = 32 << 20
const maxEventBytes = 1 << 20
const maxLineBytes = 256 << 10

type responseGuard interface {
	ContainsJSON([]byte, []byte) bool
	ContainsBytes([]byte) bool
	Clear()
}

type Adapter struct {
	backend backend.Backend
	now     func() time.Time
}

func NewAdapter(outbound backend.Backend) (*Adapter, error) {
	if backend.IsNil(outbound) || outbound.MaxResponseBytes() <= 0 {
		return nil, errors.New("gateway: outbound backend required")
	}
	return &Adapter{backend: outbound, now: time.Now}, nil
}

// Attempt executes one upstream attempt. Routing alone owns retries and billing.
func (a *Adapter) Attempt(ctx context.Context, w http.ResponseWriter, target contract.Target, credential *contract.ShortLivedSecret, chat *openai.ChatRequest, embedding *openai.EmbeddingRequest, attribution string) (result contract.AttemptResult) {
	result = contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "gateway attempt unavailable"}
	defer credential.Clear()
	sent := false
	defer func() { result.GatewayUserAttributionSent = &sent }()
	if a == nil || ctx == nil || w == nil || (chat == nil) == (embedding == nil) || target.Type() != contract.TypeAISDKGatewayV3 {
		return result
	}
	if ctx.Err() != nil {
		return canceled()
	}
	stream := chat != nil && chat.Stream
	if stream {
		if _, ok := w.(http.Flusher); !ok {
			return result
		}
	}
	var body []byte
	var err error
	path := "/language-model"
	if embedding != nil {
		path = "/embedding-model"
		body, err = compileEmbedding(embedding, attribution)
	} else {
		body, err = compileChat(chat, attribution)
	}
	if err != nil {
		return result
	}
	defer clear(body)
	client, err := a.backend.Open(target.BaseURL())
	if err != nil {
		return upstreamFailure("upstream endpoint was refused", 0)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(client.BaseURL(), "/")+path, bytes.NewReader(body))
	if err != nil {
		return result
	}
	plain, cipher, ok := credential.Take()
	if !ok {
		return result
	}
	defer clear(plain)
	defer clear(cipher)
	label := []byte(attribution)
	guard := openai.NewResponseGuard(plain, cipher, label)
	errorGuard := openai.NewResponseGuard(plain, cipher, label)
	defer guard.Clear()
	defer errorGuard.Clear()
	clear(label)
	errorContext := upstreamerror.Context{BaseURL: target.BaseURL(), PrivateModel: target.UpstreamModel(), ContainsSecret: errorGuard.ContainsBytes}
	setRequestHeaders(request, plain)
	request.Header.Set("Content-Type", "application/json")
	if embedding != nil {
		request.Header.Set("ai-embedding-model-specification-version", "3")
		request.Header.Set("ai-model-id", target.UpstreamModel())
	} else {
		request.Header.Set("ai-language-model-specification-version", "3")
		request.Header.Set("ai-language-model-id", target.UpstreamModel())
		request.Header.Set("ai-language-model-streaming", strconv.FormatBool(stream))
	}
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	clear(plain)
	clear(cipher)
	sent = attribution != ""
	response, err := client.Do(request)
	request.Header.Del("Authorization")
	if response != nil && response.Request != nil {
		response.Request.Header.Del("Authorization")
	}
	clear(body)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if ctx.Err() != nil {
		return canceled()
	}
	if err != nil {
		return upstreamFailure("upstream transport failed", 0)
	}
	if response == nil || response.Body == nil {
		return upstreamFailure("upstream response was unavailable", 0)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 || stream && response.StatusCode != 200 {
		result = upstreamFailure("upstream returned an error status", response.StatusCode)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, min(maxJSONBytes, a.backend.MaxResponseBytes()))
		return result
	}
	if stream {
		// The native SDK parses SSE independently of its advertised MIME type.
		// The bounded parser below still requires valid v3 events and a terminal.
		if response.Header.Get("Content-Encoding") != "" {
			return upstreamFailure("upstream stream encoding was invalid", response.StatusCode)
		}
		return a.stream(ctx, w, response, chat, guard, errorContext)
	}
	if !validContentType(response, "application/json") {
		_ = errorContext.ReadResponse(ctx, response)
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	raw, err := readBounded(response.Body, min(maxJSONBytes, a.backend.MaxResponseBytes()))
	if ctx.Err() != nil {
		clear(raw)
		return canceled()
	}
	if err != nil {
		return upstreamFailure("upstream response exceeded its limit or was interrupted", response.StatusCode)
	}
	defer clear(raw)
	if upstreamerror.IsEvent(raw) {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		result = upstreamFailure("upstream response reported an error", response.StatusCode)
		result.ErrorDetail = errorContext.Parse(raw)
		return result
	}
	var translated []byte
	var usage contract.Usage
	if embedding != nil {
		translated, usage, err = translateEmbedding(raw, embedding.Model, embedding.InputCount)
	} else {
		translated, usage, err = translateChat(raw, chat.Model, a.now().Unix())
	}
	defer clear(translated)
	if err != nil {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	if guard.ContainsJSON(translated, translated) {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	if err := contract.MarkResponseStarted(w); err != nil {
		return sinkFailure(false, usage)
	}
	if ctx.Err() != nil {
		return canceled()
	}
	setResponseHeaders(w.Header(), false)
	n, err := w.Write(translated)
	if err != nil || n != len(translated) {
		return sinkFailure(n > 0, usage)
	}
	return contract.AttemptResult{Success: true, Committed: n > 0, Failure: contract.FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: 200, Usage: usage}
}

func setRequestHeaders(request *http.Request, key []byte) {
	request.Header.Set("Authorization", "Bearer "+string(key))
	request.Header.Set("ai-gateway-auth-method", "api-key")
	request.Header.Set("ai-gateway-protocol-version", "0.0.1")
	request.Header.Set("Accept", "application/json")
}
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, errResponse
	}
	return data, nil
}
func validContentType(response *http.Response, want string) bool {
	if response.Header.Get("Content-Encoding") != "" || len(response.Header.Values("Content-Type")) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(media, want) {
		return false
	}
	for k, v := range params {
		if !strings.EqualFold(k, "charset") || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}
func setResponseHeaders(headers http.Header, stream bool) {
	for _, name := range []string{"Connection", "Content-Encoding", "Content-Length", "Keep-Alive", "Location", "Proxy-Authenticate", "Proxy-Authorization", "Set-Cookie", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Set("Cache-Control", "no-store")
	if stream {
		headers.Set("Content-Type", "text/event-stream")
		headers.Set("X-Accel-Buffering", "no")
	}
}
func upstreamFailure(message string, status int) contract.AttemptResult {
	return contract.AttemptResult{Failure: contract.FailureUpstream, Diagnostic: message, UpstreamStatus: status}
}
func canceled() contract.AttemptResult {
	return contract.AttemptResult{Failure: contract.FailureCanceled, Diagnostic: "request canceled"}
}
func sinkFailure(committed bool, usage contract.Usage) contract.AttemptResult {
	status := 0
	if committed {
		status = 200
	}
	return contract.AttemptResult{Failure: contract.FailureSink, Diagnostic: "client response write failed", Committed: committed, SinkFailed: true, ClientStatus: status, Usage: usage}
}
