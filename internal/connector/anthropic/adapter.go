package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const (
	DefaultMaxJSONResponseBytes int64 = 8 << 20
	DefaultMaxStreamBytes       int64 = 32 << 20
	DefaultMaxSSELineBytes            = 256 << 10
	DefaultMaxSSEEventBytes           = 1 << 20
	DefaultStreamWriteTimeout         = 15 * time.Second
	AnthropicVersion                  = "2023-06-01"
)

type Credential struct {
	apiKey     []byte
	ciphertext []byte
}

func NewCredential(apiKey, ciphertext []byte) Credential {
	return Credential{apiKey: apiKey, ciphertext: ciphertext}
}

func (c *Credential) clear() {
	if c == nil {
		return
	}
	clear(c.apiKey)
	clear(c.ciphertext)
	c.apiKey = nil
	c.ciphertext = nil
}

func (Credential) String() string   { return "[redacted upstream credential]" }
func (Credential) GoString() string { return "[redacted upstream credential]" }
func (Credential) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream credential]")
}

type Target struct {
	baseURL       string
	upstreamModel string
	credential    Credential
}

func NewTarget(baseURL, upstreamModel string, credential Credential) Target {
	return Target{baseURL: baseURL, upstreamModel: upstreamModel, credential: credential}
}

func (Target) String() string   { return "[redacted upstream target]" }
func (Target) GoString() string { return "[redacted upstream target]" }
func (Target) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream target]")
}

type AdapterConfig struct {
	Backend              backend.Backend
	MaxTokens            connectorcontract.AnthropicDefaultMaxTokensProvider
	Now                  func() time.Time
	MaxJSONResponseBytes int64
	MaxStreamBytes       int64
	MaxSSELineBytes      int
	MaxSSEEventBytes     int
	StreamWriteTimeout   time.Duration
}

type Adapter struct {
	backend              backend.Backend
	maxTokens            connectorcontract.AnthropicDefaultMaxTokensProvider
	now                  func() time.Time
	maxJSONResponseBytes int64
	maxStreamBytes       int64
	maxSSELineBytes      int
	maxSSEEventBytes     int
	streamWriteTimeout   time.Duration
}

func NewAdapter(config AdapterConfig) (*Adapter, error) {
	if backend.IsNil(config.Backend) {
		return nil, errors.New("anthropic connector: egress backend is required")
	}
	if config.MaxJSONResponseBytes < 0 || config.MaxStreamBytes < 0 || config.MaxSSELineBytes < 0 || config.MaxSSEEventBytes < 0 || config.StreamWriteTimeout < 0 {
		return nil, errors.New("anthropic connector: limits must not be negative")
	}
	if config.MaxJSONResponseBytes == 0 {
		config.MaxJSONResponseBytes = DefaultMaxJSONResponseBytes
	}
	if config.MaxStreamBytes == 0 {
		config.MaxStreamBytes = DefaultMaxStreamBytes
	}
	if config.MaxSSELineBytes == 0 {
		config.MaxSSELineBytes = DefaultMaxSSELineBytes
	}
	if config.MaxSSEEventBytes == 0 {
		config.MaxSSEEventBytes = DefaultMaxSSEEventBytes
	}
	if config.StreamWriteTimeout == 0 {
		config.StreamWriteTimeout = DefaultStreamWriteTimeout
	}
	if config.MaxJSONResponseBytes > DefaultMaxJSONResponseBytes {
		config.MaxJSONResponseBytes = DefaultMaxJSONResponseBytes
	}
	if config.MaxStreamBytes > DefaultMaxStreamBytes {
		config.MaxStreamBytes = DefaultMaxStreamBytes
	}
	if config.MaxSSELineBytes > DefaultMaxSSELineBytes {
		config.MaxSSELineBytes = DefaultMaxSSELineBytes
	}
	if config.MaxSSEEventBytes > DefaultMaxSSEEventBytes {
		config.MaxSSEEventBytes = DefaultMaxSSEEventBytes
	}
	if config.StreamWriteTimeout > DefaultStreamWriteTimeout {
		config.StreamWriteTimeout = DefaultStreamWriteTimeout
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	sharedMax := config.Backend.MaxResponseBytes()
	if config.MaxJSONResponseBytes > sharedMax {
		config.MaxJSONResponseBytes = sharedMax
	}
	if config.MaxStreamBytes > sharedMax {
		config.MaxStreamBytes = sharedMax
	}
	if config.MaxJSONResponseBytes < 1 || config.MaxStreamBytes < 1 || config.MaxSSELineBytes < 1 || config.MaxSSEEventBytes < 1 {
		return nil, errors.New("anthropic connector: response limits must be positive")
	}
	if int64(config.MaxSSELineBytes) > config.MaxStreamBytes {
		config.MaxSSELineBytes = int(config.MaxStreamBytes)
	}
	if int64(config.MaxSSEEventBytes) > config.MaxStreamBytes {
		config.MaxSSEEventBytes = int(config.MaxStreamBytes)
	}
	return &Adapter{
		backend: config.Backend, maxTokens: config.MaxTokens, now: config.Now,
		maxJSONResponseBytes: config.MaxJSONResponseBytes, maxStreamBytes: config.MaxStreamBytes,
		maxSSELineBytes: config.MaxSSELineBytes, maxSSEEventBytes: config.MaxSSEEventBytes,
		streamWriteTimeout: config.StreamWriteTimeout,
	}, nil
}

func (*Adapter) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeAnthropicCompatible
}

func (a *Adapter) Attempt(ctx context.Context, writer http.ResponseWriter, target Target, request *openai.ChatRequest, safetyIdentifier string) connectorcontract.AttemptResult {
	result := connectorcontract.AttemptResult{Failure: connectorcontract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || backend.IsNil(a.backend) || ctx == nil || writer == nil || request == nil {
		return result
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}
	if request.Stream {
		if _, ok := writer.(http.Flusher); !ok {
			result.Diagnostic = "streaming response is not supported"
			return result
		}
	}
	attemptStarted := a.now().UTC()
	body, err := compileRequestWithDefaultResolver(request, target.upstreamModel, safetyIdentifier, func() (int64, error) {
		return resolveDefaultMaxTokens(ctx, a.maxTokens)
	})
	if err != nil {
		if errors.Is(err, ErrDefaultTokens) {
			result.Diagnostic = "connector configuration unavailable"
			return result
		}
		result.Diagnostic = "request translation failed"
		return result
	}
	defer clear(body)
	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return upstreamFailure("upstream endpoint was refused", 0)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, MessagesURL(client.BaseURL()), bytes.NewReader(body))
	if err != nil {
		return result
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Anthropic-Version", AnthropicVersion)
	if request.Stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else {
		httpRequest.Header.Set("Accept", "application/json")
	}
	if len(target.credential.apiKey) == 0 {
		return result
	}
	safetyMaterial := []byte(safetyIdentifier)
	wireGuard := newSensitiveGuard(target.credential.apiKey, target.credential.ciphertext, safetyMaterial)
	clear(safetyMaterial)
	semanticGuard := wireGuard.clone()
	defer wireGuard.Clear()
	defer semanticGuard.Clear()
	httpRequest.Header.Set("X-Api-Key", string(target.credential.apiKey))
	target.credential.clear()

	response, err := client.Do(httpRequest)
	httpRequest.Header.Del("X-Api-Key")
	clear(body)
	if response != nil && response.Request != nil {
		response.Request.Header.Del("X-Api-Key")
	}
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyTransportFailure(err), 0)
	}
	defer func() { _ = response.Body.Close() }()
	if ctx.Err() != nil {
		return canceledFailure()
	}
	if request.Stream {
		if response.StatusCode != http.StatusOK {
			return upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
		}
		if !validResponseMediaType(response, "text/event-stream") {
			return upstreamFailure("upstream stream content type was invalid", response.StatusCode)
		}
		return a.stream(ctx, writer, response, request.Model, attemptStarted, wireGuard, semanticGuard)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	return a.nonStream(ctx, writer, response, request.Model, attemptStarted, wireGuard, semanticGuard)
}

func (a *Adapter) nonStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, publicModel string, attemptStarted time.Time, wireGuard, semanticGuard *sensitiveGuard) connectorcontract.AttemptResult {
	if response.ContentLength > a.maxJSONResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	body, err := readResponseBody(response.Body, a.maxJSONResponseBytes)
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyReadFailure(err), response.StatusCode)
	}
	defer clear(body)
	reflected, scanErr := semanticGuard.ContainsJSONStrings(body)
	if scanErr != nil {
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	if reflected {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	translated, usage, _, err := translateNonStream(body, publicModel, attemptStarted)
	if err != nil {
		clear(translated)
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	defer clear(translated)
	if wireGuard.Contains(translated) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}
	setJSONResponseHeaders(writer.Header())
	n, writeErr := writer.Write(translated)
	committed := n > 0
	if writeErr != nil || n != len(translated) {
		return sinkFailure(committed, usage)
	}
	return connectorcontract.AttemptResult{Success: true, Committed: committed, Failure: connectorcontract.FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
}

func MessagesURL(baseURL string) string { return strings.TrimSuffix(baseURL, "/") + "/messages" }

func readResponseBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		clear(body)
		return nil, err
	}
	if int64(len(body)) > limit {
		clear(body)
		return nil, &egress.ResponseTooLargeError{Limit: limit}
	}
	return body, nil
}

func validResponseMediaType(response *http.Response, expected string) bool {
	if response == nil || response.Header.Get("Content-Encoding") != "" {
		return false
	}
	values := response.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, expected) {
		return false
	}
	for key, value := range params {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func setJSONResponseHeaders(header http.Header) {
	clearUnsafeResponseHeaders(header)
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
}

func setSSEHeaders(header http.Header) {
	clearUnsafeResponseHeaders(header)
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
}

func clearUnsafeResponseHeaders(header http.Header) {
	for _, name := range []string{"Connection", "Content-Encoding", "Content-Length", "Keep-Alive", "Location", "Proxy-Authenticate", "Proxy-Authorization", "Set-Cookie", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func upstreamFailure(diagnostic string, status int) connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureUpstream, Diagnostic: diagnostic, UpstreamStatus: status}
}

func canceledFailure() connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Failure: connectorcontract.FailureCanceled, Diagnostic: "request canceled"}
}

func sinkFailure(committed bool, usage connectorcontract.Usage) connectorcontract.AttemptResult {
	return connectorcontract.AttemptResult{Committed: committed, SinkFailed: true, Failure: connectorcontract.FailureSink, Diagnostic: "client response write failed", ClientStatus: committedStatus(committed), Usage: usage}
}

func committedStatus(committed bool) int {
	if committed {
		return http.StatusOK
	}
	return 0
}

func statusDiagnostic(status int) string {
	if status < 100 || status > 999 {
		return "upstream returned an invalid status"
	}
	return fmt.Sprintf("upstream returned HTTP %d", status)
}

func classifyTransportFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "upstream request timed out"
	case errors.Is(err, egress.ErrRedirectBlocked):
		return "upstream redirect was refused"
	default:
		return "upstream request failed"
	}
}

func classifyReadFailure(err error) string {
	var tooLarge *egress.ResponseTooLargeError
	if errors.As(err, &tooLarge) {
		return "upstream response exceeded its limit"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "upstream response timed out"
	}
	return "upstream response was truncated"
}
