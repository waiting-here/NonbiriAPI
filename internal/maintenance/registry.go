package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const MaxContinuationSnapshotBytes = 64 * 1024

var (
	ErrRegistryFrozen      = errors.New("maintenance continuation registry is frozen")
	ErrRegistryNotFrozen   = errors.New("maintenance continuation registry is not frozen")
	ErrUnknownContinuation = errors.New("unknown maintenance continuation kind")
	ErrContinuationDenied  = errors.New("maintenance continuation is not authorized")
)

// ContinuationKind is registered during startup. It names a closed domain
// flow, not an HTTP route or a client-selected arbitrary operation.
type ContinuationKind string

func validContinuationKind(kind ContinuationKind) bool {
	text := string(kind)
	if text == "" || len(text) > 64 || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || r == 0x7f || unicode.IsSpace(r) || (r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// ContinuationAuthority keeps background recovery and an authenticated game
// continuation in different authority domains. A system continuation is a
// previously accepted worker action; it never carries a session principal.
type ContinuationAuthority uint8

const (
	ContinuationSystem ContinuationAuthority = iota + 1
	ContinuationSession
)

func (authority ContinuationAuthority) valid() bool {
	return authority == ContinuationSystem || authority == ContinuationSession
}

// ContinuationRequest contains only stable lookup facts. It deliberately has
// no arbitrary payload with which a client could create new intent.
type ContinuationRequest struct {
	Kind           ContinuationKind
	Authority      ContinuationAuthority
	AcceptedRef    string
	ActorUserID    int64
	SessionBinding string
	ResourceRef    string
	Action         string
}

func (request ContinuationRequest) valid() bool {
	if !validContinuationKind(request.Kind) || !request.Authority.valid() ||
		request.AcceptedRef == "" || len(request.AcceptedRef) > 128 ||
		len(request.SessionBinding) > 256 ||
		request.ResourceRef == "" || len(request.ResourceRef) > 128 ||
		request.Action == "" || len(request.Action) > 64 {
		return false
	}
	switch request.Authority {
	case ContinuationSystem:
		if request.ActorUserID != 0 || request.SessionBinding != "" {
			return false
		}
	case ContinuationSession:
		if request.ActorUserID <= 0 || request.SessionBinding == "" {
			return false
		}
	}
	for _, value := range []string{request.AcceptedRef, request.SessionBinding, request.ResourceRef, request.Action} {
		if !utf8.ValidString(value) || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || r == 0x7f }) >= 0 {
			return false
		}
	}
	return true
}

// ContinuationSnapshot is the complete authority projection returned by a
// domain hook. Payload is bounded canonical JSON consumed only by that domain.
type ContinuationSnapshot struct {
	Revision      string
	IdentityEpoch *string
	ExpiresAt     *int64
	Payload       json.RawMessage
}

func (snapshot ContinuationSnapshot) valid(authority ContinuationAuthority) bool {
	if !canonicalDecimal(snapshot.Revision) ||
		len(snapshot.Payload) == 0 || len(snapshot.Payload) > MaxContinuationSnapshotBytes || !json.Valid(snapshot.Payload) {
		return false
	}
	if authority == ContinuationSession && snapshot.ExpiresAt == nil {
		return false
	}
	if snapshot.ExpiresAt != nil && (*snapshot.ExpiresAt < 0 || *snapshot.ExpiresAt > maxUnixSecond) {
		return false
	}
	if snapshot.IdentityEpoch != nil && !canonicalDecimal(*snapshot.IdentityEpoch) {
		return false
	}
	return true
}

func canonicalDecimal(value string) bool {
	if value == "" || len(value) > 128 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

type AuthorityPredicate func(context.Context, *sql.Tx, ContinuationRequest) (bool, error)
type SnapshotHook func(context.Context, *sql.Tx, ContinuationRequest) (ContinuationSnapshot, error)

type ContinuationRegistration struct {
	Authority AuthorityPredicate
	Snapshot  SnapshotHook
}

// Registry is mutable only during startup. Freeze must happen before the
// listener starts, after every domain has registered its accepted-flow hooks.
type Registry struct {
	mu      sync.RWMutex
	frozen  bool
	entries map[ContinuationKind]ContinuationRegistration
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[ContinuationKind]ContinuationRegistration)}
}

func (registry *Registry) Register(kind ContinuationKind, registration ContinuationRegistration) error {
	if registry == nil || !validContinuationKind(kind) || registration.Authority == nil || registration.Snapshot == nil {
		return errors.New("invalid maintenance continuation registration")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.frozen {
		return ErrRegistryFrozen
	}
	if _, exists := registry.entries[kind]; exists {
		return fmt.Errorf("duplicate maintenance continuation kind %q", kind)
	}
	registry.entries[kind] = registration
	return nil
}

func (registry *Registry) Freeze() error {
	if registry == nil {
		return errors.New("maintenance continuation registry is required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.frozen = true
	return nil
}

func (registry *Registry) Frozen() bool {
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.frozen
}

// Authorize re-evaluates both the persisted accepted-flow predicate and its
// complete snapshot in the caller's transaction. It never accepts or mutates
// a new operation.
func (registry *Registry) Authorize(ctx context.Context, tx *sql.Tx, request ContinuationRequest) (ContinuationSnapshot, error) {
	if registry == nil || ctx == nil || tx == nil || !request.valid() {
		return ContinuationSnapshot{}, ErrContinuationDenied
	}
	registry.mu.RLock()
	if !registry.frozen {
		registry.mu.RUnlock()
		return ContinuationSnapshot{}, ErrRegistryNotFrozen
	}
	registration, exists := registry.entries[request.Kind]
	registry.mu.RUnlock()
	if !exists {
		return ContinuationSnapshot{}, ErrUnknownContinuation
	}

	allowed, err := registration.Authority(ctx, tx, request)
	if err != nil {
		return ContinuationSnapshot{}, fmt.Errorf("authorize maintenance continuation: %w", err)
	}
	if !allowed {
		return ContinuationSnapshot{}, ErrContinuationDenied
	}
	snapshot, err := registration.Snapshot(ctx, tx, request)
	if err != nil {
		return ContinuationSnapshot{}, fmt.Errorf("snapshot maintenance continuation: %w", err)
	}
	if !snapshot.valid(request.Authority) {
		return ContinuationSnapshot{}, errors.New("maintenance continuation snapshot is invalid")
	}
	snapshot.Payload = append(json.RawMessage(nil), snapshot.Payload...)
	if snapshot.IdentityEpoch != nil {
		epoch := *snapshot.IdentityEpoch
		snapshot.IdentityEpoch = &epoch
	}
	if snapshot.ExpiresAt != nil {
		expiresAt := *snapshot.ExpiresAt
		snapshot.ExpiresAt = &expiresAt
	}
	return snapshot, nil
}
