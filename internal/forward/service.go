// Package forward provides the mountable CallerKey-only OpenAI-compatible
// platform exit. It resolves caller-owned routes, delegates exactly one
// upstream attempt, and exposes narrow selector/attempt/usage hooks for later
// routing, accounting, and rate-control layers.
package forward

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"nonbiriapi/internal/connector/openai"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/diagnostic"
)

const (
	MaxCallerModels    = 1000
	MaxRouteCandidates = 256
	maxRouteOrd        = 1_000_000
	safetyDomain       = "nonbiriapi:safety_identifier:v1\x00"
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
// chooses one binding id but cannot add a globally addressed resource.
type Selection struct {
	UserID        int64
	ModelID       int64
	FullName      string
	RouteStrategy string
	Candidates    []db.ForwardCandidate
}

// Selector is the replaceable boundary for the later ordered/random routing
// layer. It does not perform network I/O or retries.
type Selector interface {
	Select(context.Context, Selection) (int64, error)
}

// FirstSelector is the intentionally minimal runnable selector for the
// single-attempt stage. Candidate SQL is ordered by (ord,id), so it chooses the
// first row. Full ordered failover, random selection, and silent retry belong
// in the routing layer and replace this implementation through Selector.
type FirstSelector struct{}

func (FirstSelector) Select(_ context.Context, selection Selection) (int64, error) {
	if len(selection.Candidates) == 0 {
		return 0, ErrUnboundModel
	}
	return selection.Candidates[0].BindingID, nil
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
type AttemptRecord struct {
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

// UsageRecord is a future accounting input. UsageUnknown is set only for a
// committed failed stream/response where no valid usage was available; token
// values are never fabricated.
type UsageRecord struct {
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

// Hooks are synchronous integration boundaries. Inputs are bounded metadata
// only. The current stage does not persist request logs or usage.
type Hooks struct {
	Attempt func(AttemptRecord)
	Usage   func(UsageRecord)
}

// ServiceConfig wires the ownership repository, replaceable selector, and
// single-attempt runner. A nil Selector uses FirstSelector.
type ServiceConfig struct {
	Repository RouteRepository
	Selector   Selector
	Runner     AttemptRunner
	Hooks      Hooks
	Now        func() time.Time
}

// Service resolves one platform model and executes one selected attempt.
type Service struct {
	repository RouteRepository
	selector   Selector
	runner     AttemptRunner
	hooks      Hooks
	now        func() time.Time
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("forward: route repository is required")
	}
	if config.Runner == nil {
		return nil, errors.New("forward: attempt runner is required")
	}
	if config.Selector == nil {
		config.Selector = FirstSelector{}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{
		repository: config.Repository,
		selector:   config.Selector,
		runner:     config.Runner,
		hooks:      config.Hooks,
		now:        config.Now,
	}, nil
}

// ListModels returns only userID's platform models. It never reads a binding,
// fetched upstream model, endpoint key, ciphertext, or Vault.
func (s *Service) ListModels(ctx context.Context, userID int64) (ModelList, error) {
	if s == nil || s.repository == nil || ctx == nil || userID <= 0 {
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

// Forward resolves one opaque full name, asks the injected selector for one
// candidate, validates that choice against the projected set, and invokes the
// single-attempt runner exactly once. It never performs silent retry.
func (s *Service) Forward(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.ChatRequest) (openai.AttemptResult, error) {
	if s == nil || s.repository == nil || s.selector == nil || s.runner == nil || ctx == nil || writer == nil || userID <= 0 || request == nil {
		return openai.AttemptResult{}, ErrInternal
	}
	route, err := s.repository.ResolveForwardRoute(ctx, userID, request.Model, MaxRouteCandidates)
	if err != nil {
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
		Candidates:    append([]db.ForwardCandidate(nil), route.Candidates...),
	}
	bindingID, err := s.selector.Select(ctx, selection)
	clear(selection.Candidates)
	if err != nil {
		if errors.Is(err, ErrUnboundModel) {
			return openai.AttemptResult{}, ErrUnboundModel
		}
		return openai.AttemptResult{}, ErrSelector
	}
	candidate, ok := exactCandidate(route.Candidates, route.ModelID, bindingID)
	if !ok {
		return openai.AttemptResult{}, ErrSelector
	}

	started := s.now().UTC()
	result := s.runner.Run(ctx, writer, AttemptInput{
		UserID:           userID,
		FullName:         route.FullName,
		BindingID:        candidate.BindingID,
		Request:          request,
		SafetyIdentifier: SafetyIdentifier(userID),
	})
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
	return result, nil
}

// SafetyIdentifier is stable across requests and deliberately domain-separated
// from every other SHA-256 use. The raw numeric id is never included in the
// result. A prefix keeps the value recognizable as a platform-generated token
// without revealing tenant identity.
func SafetyIdentifier(userID int64) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(safetyDomain))
	_, _ = hash.Write([]byte(strconv.FormatInt(userID, 10)))
	sum := hash.Sum(nil)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum)
	// RFC 4648 base32's six digit symbols are mapped to lowercase letters;
	// together with uppercase A-Z this remains a one-to-one 32-symbol alphabet
	// while guaranteeing that a decimal user id cannot appear verbatim.
	encoded = strings.NewReplacer("2", "a", "3", "b", "4", "c", "5", "d", "6", "e", "7", "f").Replace(encoded)
	clear(sum)
	return "nbu_" + encoded
}

func exactCandidate(candidates []db.ForwardCandidate, modelID, bindingID int64) (db.ForwardCandidate, bool) {
	if bindingID <= 0 {
		return db.ForwardCandidate{}, false
	}
	for _, candidate := range candidates {
		if candidate.BindingID == bindingID && candidate.ModelID == modelID && candidate.EndpointID > 0 && candidate.EndpointKeyID > 0 {
			return candidate, true
		}
	}
	return db.ForwardCandidate{}, false
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
