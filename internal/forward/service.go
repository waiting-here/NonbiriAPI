// Package forward provides the mountable CallerKey-only OpenAI-compatible
// platform exit. It resolves caller-owned routes, orchestrates the candidate
// dispatch loop (ordered / random) with the silent-retry boundary and bounded
// backoff, and exposes narrow selector/attempt/usage/failover hooks for later
// accounting, logging, and rate-control layers. Each attempt re-validates
// ownership through the single-attempt runner; no retry crosses the commit
// boundary or touches another connector's algorithm.
package forward

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

const (
	MaxCallerModels    = 1000
	MaxRouteCandidates = 256
	// DefaultForwardTimeout is one aggregate wall-clock budget shared by route
	// resolution, every silent-retry attempt, and retry backoff. It matches the
	// default single-attempt egress ceiling without multiplying that ceiling by
	// the candidate count.
	DefaultForwardTimeout = 5 * time.Minute
	maxRouteOrd           = 1_000_000
)

var (
	ErrModelNotFound = errors.New("forward: model not found")
	ErrUnboundModel  = errors.New("forward: model has no usable binding")
	ErrInternal      = errors.New("forward: internal failure")
	ErrSelector      = errors.New("forward: selector failed")
)

// RouteRepository returns only caller-owned model and candidate projections.
type RouteRepository interface {
	ListCallerModels(context.Context, int64, int) ([]db.CallerModel, error)
	ResolveForwardRoute(context.Context, int64, string, int) (db.ForwardRoute, error)
}

// Selection is the complete, finite caller-owned candidate set. A selector
// chooses an ordered sequence of binding ids from this projection but cannot
// add a globally addressed or cross-user binding.
type Selection struct {
	UserID        int64
	ModelID       int64
	FullName      string
	RouteStrategy string
	SilentRetry   bool
	Candidates    []db.ForwardCandidate
}

// Selector is the route-order interface: it returns the binding ids of one
// caller-owned projection in the dispatch order. The service re-validates each
// returned id against the projection before every attempt, so a selector can
// never dispatch an unprojected or cross-user binding. It performs no network
// I/O and owns no retry policy; the service drives the attempt loop and the
// retry boundary.
type Selector interface {
	Select(context.Context, Selection) ([]int64, error)
}

// Model is the public GET /v1/models projection.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the OpenAI list envelope.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// AttemptRecord is metadata-only. It deliberately contains no base URL,
// ciphertext, credential, request JSON, response JSON, or raw error.
// AttemptID is a random opaque correlation id generated per Forward
// invocation and shared with the UsageRecord of the same attempt; consumers
// use it for at-most-once accounting and must never treat client input as an
// idempotency key.
type AttemptRecord struct {
	AttemptID       string
	AttemptIndex    int
	UserID          int64
	ModelID         int64
	FullName        string
	BindingID       int64
	EndpointID      int64
	EndpointKeyID   int64
	UpstreamModelID string
	Stream          bool
	StartedAt       time.Time
	Duration        time.Duration
	UpstreamStatus  int
	ClientStatus    int
	Committed       bool
	Success         bool
	StableErrorCode string
	SafeDiagnostic  string
}

// UsageRecord is a future accounting input. Usage carries the connector's
// normalized four-bucket token metadata (uncached input / cache write /
// cache read / output, all non-negative) and is passed through unchanged;
// UsageUnknown is set only for a committed failed stream/response where no
// valid usage was available; token values are never fabricated. AttemptID
// correlates with the AttemptRecord of the same Forward invocation; it is
// shared by the successful attempt that committed the response.
type UsageRecord struct {
	AttemptID       string
	UserID          int64
	ModelID         int64
	FullName        string
	BindingID       int64
	EndpointKeyID   int64
	UpstreamModelID string
	Stream          bool
	Usage           openai.Usage
	UsageUnknown    bool
}

// FailoverRecord is bounded metadata emitted on each actual failover (one
// retryable pre-commit upstream failure that advances to the next candidate).
// It deliberately carries no base URL, endpoint URL, request content, raw
// upstream body, credential, or diagnostic text -- only the stable failure
// code and the identifying ids needed by later accounting/log/alert rails.
type FailoverRecord struct {
	AttemptID       string
	UserID          int64
	ModelID         int64
	FullName        string
	BindingID       int64
	StableErrorCode string
	AttemptIndex    int
}

// Hooks are synchronous integration boundaries. Inputs are bounded metadata
// only. Each Forward invocation generates one random opaque AttemptID shared
// by the Attempt, Usage, and Failover records of that invocation; consumers
// use it for at-most-once accounting and must never treat client input as an
// idempotency key. The Attempt hook fires once per actual upstream attempt
// (each dial); the Usage hook fires at most once per invocation, only for a
// committed response (at least one body byte written), success or failure:
// a committed-but-failed stream still counts as a request with usage_unknown
// when no valid usage was captured; the Failover hook fires on each actual
// failover (one retryable pre-commit upstream failure that advances to the
// next candidate) and carries only bounded non-sensitive metadata.
type Hooks struct {
	Attempt  func(AttemptRecord)
	Usage    func(UsageRecord)
	Failover func(FailoverRecord)
}

// ServiceConfig wires the ownership repository, an optional selector
// override, the single-attempt runner, hooks, the deployment-derived safety
// identifier key, and the bounded retry backoff. SafetyIdentifierKey must be
// the exact 32-byte output of the Vault purpose derivation; NewService copies
// it, and the caller should clear its input immediately after construction.
// A nil Selector makes the service choose OrderedSelector or RandomSelector
// per call from the projected route_strategy; a non-nil Selector is used for
// every call (test injection) and its returned ids are still re-validated
// against the projection. A zero BackoffConfig selects the system-default
// exponential backoff; a Base <= 0 disables waiting (tests). A zero
// ForwardTimeout selects DefaultForwardTimeout; negative values are invalid.
type ServiceConfig struct {
	Repository          RouteRepository
	Selector            Selector
	Runner              AttemptRunner
	Hooks               Hooks
	Backoff             BackoffConfig
	ForwardTimeout      time.Duration
	Now                 func() time.Time
	SafetyIdentifierKey []byte
}

// String prevents routine formatting from exposing injected key material.
func (ServiceConfig) String() string { return "[forward service config]" }

// GoString prevents detailed formatting from exposing injected key material.
func (ServiceConfig) GoString() string { return "[forward service config]" }

// LogValue prevents structured logging from exposing injected key material.
func (ServiceConfig) LogValue() slog.Value {
	return slog.StringValue("[forward service config]")
}

// Service resolves one platform model and orchestrates the candidate dispatch
// loop: it asks the selector for the route order, re-validates each selected
// binding against the projection, invokes the single-attempt runner per
// candidate, and applies the silent-retry boundary with bounded backoff.
type Service struct {
	lifecycle         sync.RWMutex
	closed            bool
	repository        RouteRepository
	selector          Selector
	runner            AttemptRunner
	hooks             Hooks
	backoff           BackoffConfig
	timeout           time.Duration
	now               func() time.Time
	safetyIdentifiers *safetyIdentifierGenerator
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("forward: route repository is required")
	}
	if config.Runner == nil {
		return nil, errors.New("forward: attempt runner is required")
	}
	if config.ForwardTimeout < 0 {
		return nil, errors.New("forward: invocation timeout must not be negative")
	}
	identifierGenerator, err := newSafetyIdentifierGenerator(config.SafetyIdentifierKey)
	if err != nil {
		return nil, err
	}
	if config.ForwardTimeout == 0 {
		config.ForwardTimeout = DefaultForwardTimeout
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	backoff := config.Backoff
	if backoff.Base == 0 && backoff.Max == 0 {
		backoff = BackoffConfig{Base: DefaultBackoffBase, Max: DefaultBackoffMax}
	}
	return &Service{
		repository:        config.Repository,
		selector:          config.Selector,
		runner:            config.Runner,
		hooks:             config.Hooks,
		backoff:           backoff,
		timeout:           config.ForwardTimeout,
		now:               config.Now,
		safetyIdentifiers: identifierGenerator,
	}, nil
}

// Close prevents new operations, waits for in-flight service calls, and
// clears the retained safety-identifier subkey on a best-effort basis.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.safetyIdentifiers == nil {
		return nil
	}
	return s.safetyIdentifiers.close()
}

// String prevents routine formatting from traversing into key-bearing state.
func (*Service) String() string { return "[forward service]" }

// GoString prevents detailed formatting from traversing into key-bearing state.
func (*Service) GoString() string { return "[forward service]" }

// LogValue prevents structured logging from traversing into key-bearing state.
func (*Service) LogValue() slog.Value { return slog.StringValue("[forward service]") }

// ListModels returns only userID's platform models. It never reads a binding,
// fetched upstream model, endpoint key, ciphertext, or Vault.
func (s *Service) ListModels(ctx context.Context, userID int64) (ModelList, error) {
	if s == nil || ctx == nil || userID <= 0 {
		return ModelList{}, ErrInternal
	}
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed || s.repository == nil {
		return ModelList{}, ErrInternal
	}
	models, err := s.repository.ListCallerModels(ctx, userID, MaxCallerModels)
	if err != nil {
		return ModelList{}, ErrInternal
	}
	response := ModelList{Object: "list", Data: make([]Model, 0, len(models))}
	for _, model := range models {
		if !validStoredModel(model) {
			return ModelList{}, ErrInternal
		}
		response.Data = append(response.Data, Model{
			ID:      model.FullName,
			Object:  "model",
			Created: model.CreatedAt,
			OwnedBy: model.Provider,
		})
	}
	return response, nil
}

// Forward resolves one opaque full name, asks the selector for the candidate
// dispatch order, re-validates each selected binding against the projection,
// and orchestrates the single-attempt runner over that order. The silent-retry
// boundary is exactly "no response-body byte committed yet": only a pre-commit
// upstream failure (connection / DNS / upstream error status / protocol
// pre-body failure, or a target that vanished between selection and dispatch)
// is retried, and only when the model's silent_retry switch is on. A committed
// byte, a sink write failure, a client cancellation, or an internal error
// short-circuits to the final result. The default (silent_retry off) runs
// exactly one attempt, preserving fail-fast behavior. Each actual failover
// emits a bounded metadata hook; the Usage hook fires at most once per
// invocation, only for a committed response (success or failure — a
// committed-but-failed stream still counts as a request with usage_unknown
// when no valid usage was captured). Before every attempt the runner
// re-validates ownership, so a binding deleted/disabled/cache-cleared between
// selection and dispatch fails closed without dialing.
func (s *Service) Forward(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.ChatRequest) (openai.AttemptResult, error) {
	if s == nil || ctx == nil || writer == nil || userID <= 0 || request == nil {
		return openai.AttemptResult{}, ErrInternal
	}
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed || s.repository == nil || s.runner == nil || s.safetyIdentifiers == nil {
		return openai.AttemptResult{}, ErrInternal
	}
	safetyIdentifier, err := s.safetyIdentifiers.generate(userID)
	if err != nil {
		return openai.AttemptResult{}, ErrInternal
	}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(parentCtx, s.timeout)
	defer cancel()
	route, err := s.repository.ResolveForwardRoute(ctx, userID, request.Model, MaxRouteCandidates)
	if err != nil {
		if ctx.Err() != nil {
			return endedContextResult(parentCtx, ctx), nil
		}
		switch {
		case errors.Is(err, db.ErrNotFound):
			return openai.AttemptResult{}, ErrModelNotFound
		default:
			return openai.AttemptResult{}, ErrInternal
		}
	}
	if route.UserID != userID || route.FullName != request.Model || route.ModelID <= 0 || (route.RouteStrategy != "ordered" && route.RouteStrategy != "random") {
		return openai.AttemptResult{}, ErrInternal
	}
	if len(route.Candidates) == 0 {
		return openai.AttemptResult{}, ErrUnboundModel
	}
	for _, candidate := range route.Candidates {
		if candidate.BindingID <= 0 || candidate.ModelID != route.ModelID || candidate.EndpointID <= 0 || candidate.EndpointKeyID <= 0 || candidate.Ord < 0 || candidate.Ord > maxRouteOrd || !validStoredText(candidate.UpstreamModelID, 512) {
			return openai.AttemptResult{}, ErrInternal
		}
	}

	selection := Selection{
		UserID:        userID,
		ModelID:       route.ModelID,
		FullName:      route.FullName,
		RouteStrategy: route.RouteStrategy,
		SilentRetry:   route.SilentRetry,
		Candidates:    append([]db.ForwardCandidate(nil), route.Candidates...),
	}
	selector := s.selector
	if selector == nil {
		selector = strategySelector(route.RouteStrategy)
	}
	order, err := selector.Select(ctx, selection)
	clear(selection.Candidates)
	if err != nil {
		if ctx.Err() != nil {
			return endedContextResult(parentCtx, ctx), nil
		}
		if errors.Is(err, ErrUnboundModel) {
			return openai.AttemptResult{}, ErrUnboundModel
		}
		return openai.AttemptResult{}, ErrSelector
	}
	if len(order) == 0 {
		return openai.AttemptResult{}, ErrUnboundModel
	}
	if len(order) > MaxRouteAttempts {
		return openai.AttemptResult{}, ErrSelector
	}
	candidates := make(map[int64]db.ForwardCandidate, len(route.Candidates))
	for _, candidate := range route.Candidates {
		candidates[candidate.BindingID] = candidate
	}
	seenOrder := make(map[int64]struct{}, len(order))
	for _, bindingID := range order {
		if _, duplicate := seenOrder[bindingID]; duplicate {
			return openai.AttemptResult{}, ErrSelector
		}
		if _, ok := candidates[bindingID]; !ok {
			return openai.AttemptResult{}, ErrSelector
		}
		seenOrder[bindingID] = struct{}{}
	}

	attemptID, err := newAttemptID()
	if err != nil {
		return openai.AttemptResult{}, ErrInternal
	}

	var lastResult openai.AttemptResult
	for index, bindingID := range order {
		if ctx.Err() != nil {
			return endedContextResult(parentCtx, ctx), nil
		}
		candidate := candidates[bindingID]
		started := s.now().UTC()
		result := s.runner.Run(ctx, writer, AttemptInput{
			UserID:           userID,
			FullName:         route.FullName,
			BindingID:        candidate.BindingID,
			Request:          request,
			SafetyIdentifier: safetyIdentifier,
		})
		if ctx.Err() != nil && !result.Success && !result.Committed {
			result = endedContextResult(parentCtx, ctx)
		}
		result.Diagnostic = diagnostic.BoundTo(result.Diagnostic, 512)
		finished := s.now().UTC()
		if finished.Before(started) {
			finished = started
		}
		stableCode, clientStatus := classifyAttemptResult(result)
		if result.ClientStatus == 0 {
			result.ClientStatus = clientStatus
		}
		if s.hooks.Attempt != nil {
			s.hooks.Attempt(AttemptRecord{
				AttemptID:       attemptID,
				AttemptIndex:    index,
				UserID:          userID,
				ModelID:         route.ModelID,
				FullName:        route.FullName,
				BindingID:       candidate.BindingID,
				EndpointID:      candidate.EndpointID,
				EndpointKeyID:   candidate.EndpointKeyID,
				UpstreamModelID: candidate.UpstreamModelID,
				Stream:          request.Stream,
				StartedAt:       started,
				Duration:        finished.Sub(started),
				UpstreamStatus:  result.UpstreamStatus,
				ClientStatus:    result.ClientStatus,
				Committed:       result.Committed,
				Success:         result.Success,
				StableErrorCode: stableCode,
				SafeDiagnostic:  result.Diagnostic,
			})
		}
		if result.Committed && s.hooks.Usage != nil {
			s.hooks.Usage(UsageRecord{
				AttemptID:       attemptID,
				UserID:          userID,
				ModelID:         route.ModelID,
				FullName:        route.FullName,
				BindingID:       candidate.BindingID,
				EndpointKeyID:   candidate.EndpointKeyID,
				UpstreamModelID: candidate.UpstreamModelID,
				Stream:          request.Stream,
				Usage:           result.Usage,
				UsageUnknown:    !result.Success && !result.Usage.Present,
			})
		}
		if result.Success {
			return result, nil
		}
		lastResult = result
		if !isRetryable(result, route.SilentRetry) || index == len(order)-1 {
			return result, nil
		}
		// An actual failover advances to the next candidate. The bounded
		// backoff runs first; if the caller cancels during the wait, no further
		// attempt is made and no failover is recorded for this transition.
		if !s.backoff.wait(ctx, index) {
			return endedContextResult(parentCtx, ctx), nil
		}
		if s.hooks.Failover != nil {
			s.hooks.Failover(FailoverRecord{
				AttemptID:       attemptID,
				UserID:          userID,
				ModelID:         route.ModelID,
				FullName:        route.FullName,
				BindingID:       candidate.BindingID,
				StableErrorCode: stableCode,
				AttemptIndex:    index,
			})
		}
	}
	return lastResult, nil
}

// newAttemptID returns a random opaque correlation id for one Forward
// invocation. It is never derived from client input and never exposed to
// clients or upstreams; the same id is shared by the Attempt and Usage hook
// records of that invocation.
func newAttemptID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	clear(raw[:])
	return encoded, nil
}

// strategySelector returns the default route-order selector for a strategy.
// ordered yields the projected (ord, id) order; random yields a fresh
// stateless permutation per call.
func strategySelector(strategy string) Selector {
	if strategy == "random" {
		return RandomSelector{}
	}
	return OrderedSelector{}
}

// canceledAttemptResult is the final result returned when the caller context
// is canceled before an attempt or during backoff. No further attempt is made
// and no body byte is committed.
func canceledAttemptResult() openai.AttemptResult {
	return openai.AttemptResult{Failure: openai.FailureCanceled, Diagnostic: "request canceled"}
}

// endedContextResult distinguishes caller cancellation from the service-owned
// aggregate deadline. Before commit, the latter is a stable upstream failure
// so the handler emits 502 instead of returning an empty response. After
// commit, callers retain the runner's committed result and no fresh envelope
// is attempted.
func endedContextResult(parent, bounded context.Context) openai.AttemptResult {
	if parent != nil && parent.Err() == nil && bounded != nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return openai.AttemptResult{
			Failure:      openai.FailureUpstream,
			ClientStatus: http.StatusBadGateway,
			Diagnostic:   "forward request timed out",
		}
	}
	return canceledAttemptResult()
}

func classifyAttemptResult(result openai.AttemptResult) (string, int) {
	if result.Success {
		return "", http.StatusOK
	}
	switch result.Failure {
	case openai.FailureUpstream:
		return "upstream", http.StatusBadGateway
	case openai.FailureCanceled, openai.FailureSink:
		return "", 499
	default:
		return "internal", http.StatusInternalServerError
	}
}

func validStoredModel(model db.CallerModel) bool {
	return model.CreatedAt >= 0 && validStoredText(model.Provider, 64) && validStoredText(model.FullName, 129)
}

func validStoredText(value string, maxRunes int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}
