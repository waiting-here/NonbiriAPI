package model

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Service-level sentinel errors. The handler maps these (and the db
// sentinels) to the stable httperr envelope; they never carry secret material
// or request content.
var (
	// ErrInvalidRequest is a validation failure (bad provider/model/strategy,
	// out-of-range ord, malformed upstream id, etc.).
	ErrInvalidRequest = errors.New("model: invalid request")
)

// Field bounds. provider/model are bounded to MaxNamePartRunes runes each,
// allowing '/' and regular characters but rejecting control characters and
// leading/trailing whitespace. upstream_model_id is opaque and bounded
// generously (the fetched cache is the authoritative source; the J rail's
// fetch-time bound governs what can actually be cached). ord is non-negative
// and capped so ordering stays a sane integer range.
const (
	MaxNamePartRunes      = 64
	MaxUpstreamModelRunes = 512
	MaxOrd                = 1_000_000
)

// Repository is the persistence boundary the service uses. *db.Store
// satisfies it. Every method takes a user id and enforces ownership in SQL.
type Repository interface {
	CreateModel(ctx context.Context, userID int64, provider, model, routeStrategy string, silentRetry bool, now int64) (db.Model, error)
	ListModels(ctx context.Context, userID int64) ([]db.Model, error)
	GetModel(ctx context.Context, userID, id int64) (db.Model, error)
	UpdateModel(ctx context.Context, userID, id int64, provider, model, routeStrategy *string, silentRetry *bool, now int64) (db.Model, error)
	DeleteModel(ctx context.Context, userID, id int64) error

	ListBindings(ctx context.Context, userID, modelID int64) ([]db.ModelBinding, error)
	CreateBinding(ctx context.Context, userID, modelID, endpointKeyID int64, upstreamModelID string, ord, now int64) (db.ModelBinding, error)
	UpdateBinding(ctx context.Context, userID, modelID, bindingID int64, ord *int64, upstreamModelID *string, now int64) (db.ModelBinding, error)
	ReorderBindings(ctx context.Context, userID, modelID int64, order []int64) ([]db.ModelBinding, error)
	DeleteBinding(ctx context.Context, userID, modelID, bindingID int64) error
}

// Service orchestrates platform-model and binding CRUD with the ownership,
// opaque-name, strategy, and candidate-validation invariants.
type Service struct {
	repo Repository
	now  func() int64
}

// NewService constructs a Service. A nil Now defaults to unix seconds.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: func() int64 { return time.Now().Unix() }}
}

// CreateModel creates a platform model for userID. provider/model are
// validated (non-empty, UTF-8, no control characters, no leading/trailing
// whitespace, at most MaxNamePartRunes runes); routeStrategy defaults to
// ordered when nil and must otherwise be exactly ordered or random. silentRetry
// is the explicit retry switch (nil or false = fail fast, the default; the
// service never silently flips it on). The full name provider/model must be
// unique among the user's models.
func (s *Service) CreateModel(ctx context.Context, userID int64, provider, model string, routeStrategy *string, silentRetry *bool) (db.Model, error) {
	if s == nil || s.repo == nil {
		return db.Model{}, ErrInvalidRequest
	}
	if userID <= 0 {
		return db.Model{}, ErrInvalidRequest
	}
	if err := validateNamePart("provider", provider); err != nil {
		return db.Model{}, err
	}
	if err := validateNamePart("model", model); err != nil {
		return db.Model{}, err
	}
	strategy, err := resolveRouteStrategy(routeStrategy)
	if err != nil {
		return db.Model{}, err
	}
	retry, err := resolveSilentRetry(silentRetry)
	if err != nil {
		return db.Model{}, err
	}
	m, err := s.repo.CreateModel(ctx, userID, provider, model, strategy, retry, s.now())
	if err != nil {
		return db.Model{}, mapRepoError(err)
	}
	return m, nil
}

// ListModels returns the caller's platform models with live binding counts.
func (s *Service) ListModels(ctx context.Context, userID int64) ([]db.Model, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, ErrInvalidRequest
	}
	models, err := s.repo.ListModels(ctx, userID)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return models, nil
}

// GetModel returns one platform model owned by userID.
func (s *Service) GetModel(ctx context.Context, userID, id int64) (db.Model, error) {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.Model{}, db.ErrNotFound
	}
	m, err := s.repo.GetModel(ctx, userID, id)
	if err != nil {
		return db.Model{}, mapRepoError(err)
	}
	return m, nil
}

// UpdateModel updates a platform model owned by userID. Each provided field is
// validated exactly like create; changing provider and/or model recomputes the
// full name, which may collide (conflict). routeStrategy must be exactly
// ordered or random when provided. silentRetry must be a bool when provided; a
// nil leaves it unchanged.
func (s *Service) UpdateModel(ctx context.Context, userID, id int64, provider, model, routeStrategy *string, silentRetry *bool) (db.Model, error) {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.Model{}, db.ErrNotFound
	}
	if provider != nil {
		if err := validateNamePart("provider", *provider); err != nil {
			return db.Model{}, err
		}
	}
	if model != nil {
		if err := validateNamePart("model", *model); err != nil {
			return db.Model{}, err
		}
	}
	var strategy *string
	if routeStrategy != nil {
		resolved, err := resolveRouteStrategy(routeStrategy)
		if err != nil {
			return db.Model{}, err
		}
		strategy = &resolved
	}
	var retry *bool
	if silentRetry != nil {
		resolved, err := resolveSilentRetry(silentRetry)
		if err != nil {
			return db.Model{}, err
		}
		retry = &resolved
	}
	m, err := s.repo.UpdateModel(ctx, userID, id, provider, model, strategy, retry, s.now())
	if err != nil {
		return db.Model{}, mapRepoError(err)
	}
	return m, nil
}

// DeleteModel deletes a platform model owned by userID; its bindings cascade.
func (s *Service) DeleteModel(ctx context.Context, userID, id int64) error {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.ErrNotFound
	}
	if err := s.repo.DeleteModel(ctx, userID, id); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// ListBindings returns the bindings of a platform model owned by userID,
// ordered by (ord, id). A missing or cross-user model id yields ErrNotFound
// rather than an empty list.
func (s *Service) ListBindings(ctx context.Context, userID, modelID int64) ([]db.ModelBinding, error) {
	if s == nil || s.repo == nil || userID <= 0 || modelID <= 0 {
		return nil, db.ErrNotFound
	}
	if _, err := s.repo.GetModel(ctx, userID, modelID); err != nil {
		return nil, mapRepoError(err)
	}
	bindings, err := s.repo.ListBindings(ctx, userID, modelID)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return bindings, nil
}

// CreateBinding binds a platform model owned by userID to an enabled endpoint
// key of an enabled endpoint owned by the same user, referencing an upstream
// id that exists in that key's fetched cache. The repository enforces all of
// this atomically; violations are indistinguishable not_found. A duplicate
// triple is a conflict. ord defaults to 0 and must be within [0, MaxOrd].
func (s *Service) CreateBinding(ctx context.Context, userID, modelID, endpointKeyID int64, upstreamModelID string, ord *int64) (db.ModelBinding, error) {
	if s == nil || s.repo == nil || userID <= 0 || modelID <= 0 || endpointKeyID <= 0 {
		return db.ModelBinding{}, db.ErrNotFound
	}
	if err := validateUpstreamModelID(upstreamModelID); err != nil {
		return db.ModelBinding{}, err
	}
	ordVal, err := resolveOrd(ord)
	if err != nil {
		return db.ModelBinding{}, err
	}
	b, err := s.repo.CreateBinding(ctx, userID, modelID, endpointKeyID, upstreamModelID, ordVal, s.now())
	if err != nil {
		return db.ModelBinding{}, mapRepoError(err)
	}
	return b, nil
}

// UpdateBinding updates ord and/or upstream_model_id of the binding bindingID
// on a model owned by userID. endpoint_key_id never changes through this path.
// The resulting (key, upstream_model_id) must still exist in the key's fetched
// cache and the key/endpoint must still be enabled; violations are not_found.
func (s *Service) UpdateBinding(ctx context.Context, userID, modelID, bindingID int64, ord *int64, upstreamModelID *string) (db.ModelBinding, error) {
	if s == nil || s.repo == nil || userID <= 0 || modelID <= 0 || bindingID <= 0 {
		return db.ModelBinding{}, db.ErrNotFound
	}
	var ordPtr *int64
	if ord != nil {
		ordVal, err := resolveOrd(ord)
		if err != nil {
			return db.ModelBinding{}, err
		}
		ordPtr = &ordVal
	}
	if upstreamModelID != nil {
		if err := validateUpstreamModelID(*upstreamModelID); err != nil {
			return db.ModelBinding{}, err
		}
	}
	b, err := s.repo.UpdateBinding(ctx, userID, modelID, bindingID, ordPtr, upstreamModelID, s.now())
	if err != nil {
		return db.ModelBinding{}, mapRepoError(err)
	}
	return b, nil
}

// DeleteBinding deletes the binding bindingID on a model owned by userID.
func (s *Service) DeleteBinding(ctx context.Context, userID, modelID, bindingID int64) error {
	if s == nil || s.repo == nil || userID <= 0 || modelID <= 0 || bindingID <= 0 {
		return db.ErrNotFound
	}
	if err := s.repo.DeleteBinding(ctx, userID, modelID, bindingID); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// ReorderBindings reassigns the ord of every binding on a model owned by
// userID to the position of its id in order. The order array must be an exact
// permutation of the model's current binding ids; a stale or mismatched array
// is a conflict. The store reassigns all positions in one transaction.
func (s *Service) ReorderBindings(ctx context.Context, userID, modelID int64, order []int64) ([]db.ModelBinding, error) {
	if s == nil || s.repo == nil || userID <= 0 || modelID <= 0 {
		return nil, db.ErrNotFound
	}
	if _, err := s.repo.GetModel(ctx, userID, modelID); err != nil {
		return nil, mapRepoError(err)
	}
	bindings, err := s.repo.ReorderBindings(ctx, userID, modelID, order)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return bindings, nil
}

// --- validation -------------------------------------------------------------

// validateNamePart bounds one opaque name half (provider or model): required,
// valid UTF-8, no control characters, no leading/trailing whitespace (Unicode
// aware), at most MaxNamePartRunes runes. '/' and interior whitespace are
// allowed; the string is never interpreted as a path.
func validateNamePart(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidRequest, field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidRequest, field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains control characters", ErrInvalidRequest, field)
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return fmt.Errorf("%w: %s must not start or end with whitespace", ErrInvalidRequest, field)
	}
	if count := utf8.RuneCountInString(value); count > MaxNamePartRunes {
		return fmt.Errorf("%w: %s exceeds %d runes", ErrInvalidRequest, field, MaxNamePartRunes)
	}
	return nil
}

// validateUpstreamModelID bounds the opaque upstream model id. The fetched
// cache is the authoritative existence check; this only defends the DB and
// response paths with a generous bound.
func validateUpstreamModelID(value string) error {
	if value == "" {
		return fmt.Errorf("%w: upstream_model_id is required", ErrInvalidRequest)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: upstream_model_id is not valid UTF-8", ErrInvalidRequest)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: upstream_model_id contains control characters", ErrInvalidRequest)
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return fmt.Errorf("%w: upstream_model_id must not start or end with whitespace", ErrInvalidRequest)
	}
	if count := utf8.RuneCountInString(value); count > MaxUpstreamModelRunes {
		return fmt.Errorf("%w: upstream_model_id exceeds %d runes", ErrInvalidRequest, MaxUpstreamModelRunes)
	}
	return nil
}

// resolveRouteStrategy maps a nil strategy to the ordered default and strictly
// rejects anything other than ordered|random. Unknown strategies are refused,
// never silently defaulted or normalized.
func resolveRouteStrategy(strategy *string) (string, error) {
	if strategy == nil || *strategy == "" {
		return "ordered", nil
	}
	switch *strategy {
	case "ordered", "random":
		return *strategy, nil
	default:
		return "", fmt.Errorf("%w: route_strategy must be ordered or random", ErrInvalidRequest)
	}
}

// resolveSilentRetry maps a nil switch to the fail-fast default (false) and
// accepts only an actual bool. The caller's *bool is JSON-decoded, so a
// non-bool value (e.g. the string "true") is rejected earlier at decode time;
// this helper keeps the service boundary explicit and never silently flips the
// default on.
func resolveSilentRetry(silentRetry *bool) (bool, error) {
	if silentRetry == nil {
		return false, nil
	}
	return *silentRetry, nil
}

// resolveOrd maps a nil ord to 0 and bounds it to [0, MaxOrd].
func resolveOrd(ord *int64) (int64, error) {
	if ord == nil {
		return 0, nil
	}
	if *ord < 0 {
		return 0, fmt.Errorf("%w: ord must be non-negative", ErrInvalidRequest)
	}
	if *ord > MaxOrd {
		return 0, fmt.Errorf("%w: ord exceeds %d", ErrInvalidRequest, MaxOrd)
	}
	return *ord, nil
}

// mapRepoError translates repository sentinels into the service error set
// while passing through unknown errors wrapped for diagnostics. It never
// includes secret material.
func mapRepoError(err error) error {
	var capErr *db.CapError
	if errors.As(err, &capErr) {
		// A per-parent resource cap refusal carries the resource name and the
		// exact effective cap; surface it as-is so the handler can build the
		// resource_limit_exceeded envelope without a second, racy read.
		return capErr
	}
	switch {
	case errors.Is(err, db.ErrNotFound):
		return db.ErrNotFound
	case errors.Is(err, db.ErrConflict):
		return db.ErrConflict
	case errors.Is(err, db.ErrInvalidSiteConfig):
		return fmt.Errorf("%w: site config", ErrInvalidRequest)
	default:
		return fmt.Errorf("model: repository error: %w", err)
	}
}
