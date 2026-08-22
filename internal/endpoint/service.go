package endpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Service-level sentinel errors. The handler maps these (and the db sentinels)
// to the stable httperr envelope; they never carry secret material.
var (
	// ErrInvalidRequest is a validation failure (bad connector type, bad note,
	// empty/base-url rejected by the egress boundary, etc.).
	ErrInvalidRequest = errors.New("endpoint: invalid request")
	// ErrPayloadTooLarge is an over-limit secret (the request body size limit
	// itself is enforced in the handler via http.MaxBytesReader).
	ErrPayloadTooLarge = errors.New("endpoint: payload too large")
	// ErrConnectorImmutable is returned when a PATCH attempts to change the
	// connector type of an existing endpoint.
	ErrConnectorImmutable = errors.New("endpoint: connector type is immutable")
)

// Field bounds. Secret and note limits are enforced before any cryptographic
// or SQL work. The secret ceiling is well under secret.MaxPlaintextBytes so
// the codec's own allocation guard can never be the first thing to trip on a
// hostile body; the note ceiling keeps listings and logs bounded.
const (
	MaxSecretBytes = 4096
	MaxNoteRunes   = 256
	DisplayFragLen = 4
)

// BaseURLValidator canonicalizes and validates a user-supplied base URL. The
// production implementation is *egress.EgressPolicy; this narrow interface
// keeps the service testable and guarantees the canonicalization boundary is
// never reimplemented or simplified inside this package.
type BaseURLValidator interface {
	ValidateBaseURL(raw string) (string, error)
}

// FetchHook is the narrow boundary the model-fetch rail (J) implements to
// fetch upstream models after an endpoint save/edit or key add. The service
// invokes it only after the endpoint/key transaction has committed, so a hook
// failure can never leave a half-committed secret or endpoint. Implementations
// may run the fetch asynchronously and return nil once it is initiated; a
// returned error is logged and never fails the already-committed save. A nil
// hook is a no-op (used until J lands).
type FetchHook interface {
	FetchModels(ctx context.Context, userID, endpointID, keyID int64) error
}

// Repository is the persistence boundary the service uses. *db.Store satisfies
// it. Methods that take a user id enforce ownership in SQL.
type Repository interface {
	CreateEndpoint(ctx context.Context, userID int64, connectorType, baseURL, note string, enabled bool, now int64) (db.Endpoint, error)
	ListEndpoints(ctx context.Context, userID int64) ([]db.Endpoint, error)
	GetEndpoint(ctx context.Context, userID, id int64) (db.Endpoint, error)
	UpdateEndpoint(ctx context.Context, userID, id int64, baseURL *string, note *string, enabled *bool, now int64) (db.Endpoint, db.EndpointChangeMask, error)
	DeleteEndpoint(ctx context.Context, userID, id int64) error
	EndpointCap(ctx context.Context, userID int64) (int, error)

	CreateEndpointKey(ctx context.Context, userID, endpointID int64, secretPlaintext []byte, displayHead, displayTail, note string, enabled bool, now int64) (db.EndpointKey, error)
	ListEndpointKeys(ctx context.Context, userID, endpointID int64) ([]db.EndpointKey, error)
	ListEnabledEndpointKeys(ctx context.Context, userID, endpointID int64) ([]db.EndpointKey, error)
	UpdateEndpointKey(ctx context.Context, userID, endpointID, keyID int64, note *string, enabled *bool, now int64) (db.EndpointKey, error)
	DeleteEndpointKey(ctx context.Context, userID, endpointID, keyID int64) error
}

// ServiceDeps are the collaborators a Service needs. All are injected so the
// identity rail wires its session resolver, main wires the egress policy and
// secret vault, and tests wire fakes.
type ServiceDeps struct {
	Repo       Repository
	URLs       BaseURLValidator // *egress.EgressPolicy
	Connectors *Registry
	Hook       FetchHook // nil = no-op
	Now        func() int64
}

// Service orchestrates Endpoint and EndpointKey CRUD with the ownership,
// SSRF, connector, secret, and cap invariants.
type Service struct {
	repo       Repository
	urls       BaseURLValidator
	connectors *Registry
	hook       FetchHook
	now        func() int64
}

// NewService constructs a Service. A nil Now defaults to unix seconds. A nil
// Hook is treated as a no-op.
func NewService(deps ServiceDeps) *Service {
	if deps.Now == nil {
		deps.Now = func() int64 { return time.Now().Unix() }
	}
	return &Service{
		repo:       deps.Repo,
		urls:       deps.URLs,
		connectors: deps.Connectors,
		hook:       deps.Hook,
		now:        deps.Now,
	}
}

// CreateEndpoint creates an endpoint for userID. connectorType defaults to
// openai-compatible when empty and is validated against the registry (unknown
// types rejected, no fallback). baseURL is canonicalized by the egress
// boundary. The cap is checked atomically inside the repository transaction.
func (s *Service) CreateEndpoint(ctx context.Context, userID int64, connectorType string, baseURL string, note *string, enabled *bool) (db.Endpoint, error) {
	if s == nil || s.repo == nil {
		return db.Endpoint{}, ErrInvalidRequest
	}
	if userID <= 0 {
		return db.Endpoint{}, ErrInvalidRequest
	}
	connector, err := s.resolveConnector(connectorType)
	if err != nil {
		return db.Endpoint{}, err
	}
	canonical, err := s.canonicalURL(baseURL)
	if err != nil {
		return db.Endpoint{}, err
	}
	noteStr, err := resolveNote(note)
	if err != nil {
		return db.Endpoint{}, err
	}
	enabledVal := true
	if enabled != nil {
		enabledVal = *enabled
	}
	now := s.now()

	ep, err := s.repo.CreateEndpoint(ctx, userID, string(connector), canonical, noteStr, enabledVal, now)
	if err != nil {
		return db.Endpoint{}, mapRepoError(err)
	}
	// A freshly created endpoint has no keys, so there is nothing to fetch.
	return ep, nil
}

// ListEndpoints returns the caller's endpoints.
func (s *Service) ListEndpoints(ctx context.Context, userID int64) ([]db.Endpoint, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, ErrInvalidRequest
	}
	eps, err := s.repo.ListEndpoints(ctx, userID)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return eps, nil
}

// GetEndpoint returns one endpoint owned by userID.
func (s *Service) GetEndpoint(ctx context.Context, userID, id int64) (db.Endpoint, error) {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.Endpoint{}, db.ErrNotFound
	}
	ep, err := s.repo.GetEndpoint(ctx, userID, id)
	if err != nil {
		return db.Endpoint{}, mapRepoError(err)
	}
	return ep, nil
}

// UpdateEndpoint updates an endpoint owned by userID. connectorType must be nil
// (the connector type is immutable). When baseURL is non-nil it is
// re-canonicalized by the egress boundary. The repository compares values,
// checks key existence, and applies the update in one transaction. Empty and
// wholly unchanged patches are rejected. After commit, fetching occurs only
// for a same-origin upstream path change or a disabled-to-enabled transition.
func (s *Service) UpdateEndpoint(ctx context.Context, userID, id int64, baseURL *string, note *string, enabled *bool, connectorType *string) (db.Endpoint, error) {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.Endpoint{}, db.ErrNotFound
	}
	if connectorType != nil {
		return db.Endpoint{}, ErrConnectorImmutable
	}
	if baseURL == nil && note == nil && enabled == nil {
		return db.Endpoint{}, ErrInvalidRequest
	}
	var canonical *string
	if baseURL != nil {
		c, err := s.canonicalURL(*baseURL)
		if err != nil {
			return db.Endpoint{}, err
		}
		canonical = &c
	}
	var notePtr *string
	if note != nil {
		noteStr, err := resolveNote(note)
		if err != nil {
			return db.Endpoint{}, err
		}
		notePtr = &noteStr
	}
	now := s.now()

	ep, changes, err := s.repo.UpdateEndpoint(ctx, userID, id, canonical, notePtr, enabled, now)
	if err != nil {
		return db.Endpoint{}, mapRepoError(err)
	}
	if changes == 0 {
		return db.Endpoint{}, ErrInvalidRequest
	}
	if changes.Has(db.EndpointChangeUpstreamPath) ||
		(changes.Has(db.EndpointChangeEnabled) && ep.Enabled) {
		s.triggerFetchForEnabledKeys(ctx, userID, ep.ID)
	}
	return ep, nil
}

// DeleteEndpoint deletes an endpoint owned by userID. Schema cascades remove
// its keys, fetched_models, and model_bindings immediately.
func (s *Service) DeleteEndpoint(ctx context.Context, userID, id int64) error {
	if s == nil || s.repo == nil || userID <= 0 || id <= 0 {
		return db.ErrNotFound
	}
	if err := s.repo.DeleteEndpoint(ctx, userID, id); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// CreateEndpointKey adds a key to endpointID owned by userID. The repository
// consumes the plaintext and performs row allocation plus contextual sealing
// inside one transaction; plaintext is never a SQL value. Only ciphertext and
// persisted head/tail display fragments are stored. After a committed add the
// fetch hook is invoked for this key when it is enabled.
func (s *Service) CreateEndpointKey(ctx context.Context, userID, endpointID int64, secretPlaintext []byte, note *string, enabled *bool) (db.EndpointKey, error) {
	defer clear(secretPlaintext)
	if s == nil || s.repo == nil {
		return db.EndpointKey{}, ErrInvalidRequest
	}
	if userID <= 0 || endpointID <= 0 {
		return db.EndpointKey{}, db.ErrNotFound
	}
	if err := validateSecret(secretPlaintext); err != nil {
		return db.EndpointKey{}, err
	}
	noteStr, err := resolveNote(note)
	if err != nil {
		return db.EndpointKey{}, err
	}
	head, tail := displayFragments(secretPlaintext)

	enabledVal := true
	if enabled != nil {
		enabledVal = *enabled
	}
	now := s.now()

	key, err := s.repo.CreateEndpointKey(ctx, userID, endpointID, secretPlaintext, head, tail, noteStr, enabledVal, now)
	if err != nil {
		return db.EndpointKey{}, mapRepoError(err)
	}
	if enabledVal {
		s.triggerFetch(ctx, userID, endpointID, key.ID)
	}
	return key, nil
}

// ListEndpointKeys returns the keys on endpointID owned by userID (metadata
// and display fragments only; never ciphertext or plaintext). A missing or
// cross-user endpoint id yields ErrNotFound rather than an empty list, so the
// keys route is indistinguishable from the rest of the ownership surface.
func (s *Service) ListEndpointKeys(ctx context.Context, userID, endpointID int64) ([]db.EndpointKey, error) {
	if s == nil || s.repo == nil || userID <= 0 || endpointID <= 0 {
		return nil, db.ErrNotFound
	}
	if _, err := s.repo.GetEndpoint(ctx, userID, endpointID); err != nil {
		return nil, mapRepoError(err)
	}
	keys, err := s.repo.ListEndpointKeys(ctx, userID, endpointID)
	if err != nil {
		return nil, mapRepoError(err)
	}
	return keys, nil
}

// UpdateEndpointKey updates the key keyID on endpointID owned by userID
// (note / enabled only; the secret is never mutable here). endpointID is part
// of the ownership check so a caller cannot address a key through another
// endpoint's path. Disabling a key is immediate; routing-time filtering of
// disabled keys is the binding/forwarding rail's job.
func (s *Service) UpdateEndpointKey(ctx context.Context, userID, endpointID, keyID int64, note *string, enabled *bool) (db.EndpointKey, error) {
	if s == nil || s.repo == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 {
		return db.EndpointKey{}, db.ErrNotFound
	}
	var notePtr *string
	if note != nil {
		noteStr, err := resolveNote(note)
		if err != nil {
			return db.EndpointKey{}, err
		}
		notePtr = &noteStr
	}
	now := s.now()
	key, err := s.repo.UpdateEndpointKey(ctx, userID, endpointID, keyID, notePtr, enabled, now)
	if err != nil {
		return db.EndpointKey{}, mapRepoError(err)
	}
	return key, nil
}

// DeleteEndpointKey deletes the key keyID on endpointID owned by userID.
// endpointID is part of the ownership check so a caller cannot address a key
// through another endpoint's path. Schema cascades remove its fetched_models
// and model_bindings immediately.
func (s *Service) DeleteEndpointKey(ctx context.Context, userID, endpointID, keyID int64) error {
	if s == nil || s.repo == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 {
		return db.ErrNotFound
	}
	if err := s.repo.DeleteEndpointKey(ctx, userID, endpointID, keyID); err != nil {
		return mapRepoError(err)
	}
	return nil
}

// resolveConnector defaults an empty type to openai-compatible and validates it
// against the registry. Unknown types are rejected with no fallback.
func (s *Service) resolveConnector(raw string) (ConnectorType, error) {
	t := ConnectorType(trimSpace(raw))
	if t == "" {
		t = ConnectorOpenAICompatible
	}
	if s.connectors == nil {
		return "", fmt.Errorf("%w: connector registry unavailable", ErrInvalidRequest)
	}
	validated, err := s.connectors.MustValidate(t)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return validated, nil
}

// canonicalURL delegates to the egress boundary, the only base_url
// canonicalizer. Any rejection (unsafe scheme, private/loopback host without
// an allowlist, malformed URL, control characters) is an invalid request.
func (s *Service) canonicalURL(raw string) (string, error) {
	if s.urls == nil {
		return "", fmt.Errorf("%w: base URL validator unavailable", ErrInvalidRequest)
	}
	canonical, err := s.urls.ValidateBaseURL(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return canonical, nil
}

// triggerFetchForEnabledKeys invokes the fetch hook once per enabled key on the
// endpoint. Failures are logged and never propagated: the endpoint update is
// already committed, so a fetch failure must not turn a successful save into a
// half-committed result.
func (s *Service) triggerFetchForEnabledKeys(ctx context.Context, userID, endpointID int64) {
	if s.hook == nil {
		return
	}
	keys, err := s.repo.ListEnabledEndpointKeys(ctx, userID, endpointID)
	if err != nil {
		slog.Warn("endpoint update: list enabled keys for fetch failed",
			"endpoint_id", endpointID)
		return
	}
	for _, k := range keys {
		s.triggerFetch(ctx, userID, endpointID, k.ID)
	}
}

// triggerFetch invokes the hook for one key and logs any failure without
// propagating it.
func (s *Service) triggerFetch(ctx context.Context, userID, endpointID, keyID int64) {
	if s.hook == nil {
		return
	}
	if err := s.hook.FetchModels(ctx, userID, endpointID, keyID); err != nil {
		slog.Warn("endpoint fetch hook failed",
			"endpoint_id", endpointID, "key_id", keyID)
	}
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
	case errors.Is(err, db.ErrEndpointOriginConflict):
		return db.ErrEndpointOriginConflict
	case errors.Is(err, db.ErrInvalidSiteConfig):
		return fmt.Errorf("%w: site config", ErrInvalidRequest)
	default:
		return fmt.Errorf("endpoint: repository error: %w", err)
	}
}

// validateSecret enforces the plaintext bounds before any cryptographic work.
// Empty is invalid (an endpoint key with no secret is meaningless); over-limit
// is payload_too_large.
func validateSecret(plaintext []byte) error {
	if err := ValidateSecret(plaintext); err != nil {
		return err
	}
	// This path keeps its historical payload_too_large distinction for
	// over-limit secrets (the exported policy collapses it into one sentinel).
	if len(plaintext) > MaxSecretBytes {
		return fmt.Errorf("%w: secret too long", ErrPayloadTooLarge)
	}
	return nil
}

// ValidateSecret is the shared plaintext policy for every upstream key entry
// point, including a nested donation submission that creates fresh keys: the
// same length, UTF-8 and control-character rules apply no matter which route
// carried the secret. It never returns any part of the input.
func ValidateSecret(plaintext []byte) error {
	if len(plaintext) == 0 {
		return fmt.Errorf("%w: secret is required", ErrInvalidRequest)
	}
	if len(plaintext) > MaxSecretBytes {
		return fmt.Errorf("%w: secret too long", ErrPayloadTooLarge)
	}
	if !utf8.Valid(plaintext) {
		return fmt.Errorf("%w: secret is invalid", ErrInvalidRequest)
	}
	remaining := plaintext
	for len(remaining) > 0 {
		r, size := utf8.DecodeRune(remaining)
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: secret contains control characters", ErrInvalidRequest)
		}
		remaining = remaining[size:]
	}
	return nil
}

// resolveNote normalizes a note pointer: nil means "unchanged" at the call site
// (the caller decides whether a nil note is allowed) and is returned as an
// empty string for create; non-nil notes are validated. A nil note passed here
// yields "" (used by create, where absence means empty). Callers that need to
// distinguish "unchanged" from "set to empty" pass the pointer through to the
// repo and only call resolveNote when the pointer is non-nil.
func resolveNote(note *string) (string, error) {
	if note == nil {
		return "", nil
	}
	value := *note
	if err := validateNote(value); err != nil {
		return "", err
	}
	return value, nil
}

// validateNote bounds the note to MaxNoteRunes runes and rejects C0 control
// characters and DEL so listings and logs stay bounded and injection-safe.
func validateNote(note string) error {
	if err := validateBoundedText(note, MaxNoteRunes); err != nil {
		return fmt.Errorf("%w: note %v", ErrInvalidRequest, err)
	}
	return nil
}

// validateBoundedText rejects control characters and over-length text. It is
// rune-aware so a multibyte note is bounded by rune count, not bytes.
func validateBoundedText(value string, maxRunes int) error {
	if value == "" {
		return nil
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("contains control characters")
		}
	}
	if count := runeCount(value); count > maxRunes {
		return fmt.Errorf("length %d exceeds %d runes", count, maxRunes)
	}
	return nil
}

// DisplayFragments returns the persisted display fragments of an upstream
// secret using exactly the same suppression rules as endpoint creation. It is
// shared with the donation rail so a nested submission stores fragments that
// are byte-identical to a direct key add for the same secret.
func DisplayFragments(secret []byte) (head, tail string) {
	return displayFragments(secret)
}

// displayFragments returns the first and last DisplayFragLen runes of the
// secret for persisted display. The fragments must never re-cover the entire
// secret: when the secret is short enough that the head alone would reveal
// every rune (rune length <= DisplayFragLen) both fragments are suppressed;// when head+tail would cover it entirely (rune length <= 2*DisplayFragLen) the
// tail is cleared. Otherwise head and tail expose only the first and last few
// runes with the middle hidden.
func displayFragments(secret []byte) (head, tail string) {
	var first [DisplayFragLen]rune
	var last [DisplayFragLen]rune
	count := 0
	for len(secret) > 0 {
		r, size := utf8.DecodeRune(secret)
		if size == 0 {
			break
		}
		if count < DisplayFragLen {
			first[count] = r
		}
		last[count%DisplayFragLen] = r
		count++
		secret = secret[size:]
	}
	if count <= DisplayFragLen {
		return "", ""
	}
	head = string(first[:])
	if count <= 2*DisplayFragLen {
		return head, ""
	}
	orderedTail := make([]rune, DisplayFragLen)
	start := count % DisplayFragLen
	for i := range orderedTail {
		orderedTail[i] = last[(start+i)%DisplayFragLen]
	}
	tail = string(orderedTail)
	clear(orderedTail)
	return head, tail
}

func runeCount(s string) int {
	return len([]rune(s))
}

// trimSpace strips ASCII surrounding whitespace. Connector types and caller
// hints are ASCII protocol identifiers; this avoids importing strings for a
// one-line operation and keeps the package boundary tight.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
