// Package usage consumes the forwarding stage's attempt/usage metadata hooks
// and persists metadata-only request logs plus user-level accumulators in one
// atomic SQLite transaction. Accounting boundary: committed response bytes —
// a request counts only when at least one body byte was written to the
// client. Committed attempts without a valid usage triple count as
// usage_unknown with no fabricated token values; pre-commit attempts are
// never persisted.
//
// Each Forward invocation generates one random opaque AttemptID shared by its
// Attempt and Usage hook records. This service correlates the pair (the
// Attempt record carries status/duration/timestamps, the Usage record the
// token values) and hands the combined metadata to the repository, which
// enforces the correlation id as a unique key: duplicated hook delivery can
// never double-accumulate, and a user deleted before a late hook arrives
// makes the write an atomic no-op.
package usage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/forward"
)

const (
	// DefaultMaxPendingAttempts bounds the in-memory attempt cache.
	DefaultMaxPendingAttempts = 4096
	// DefaultPendingTTL bounds how long an attempt record may wait for its
	// committed usage record. The pair arrives synchronously within one
	// request; the TTL only reaps abandoned pre-commit attempts.
	DefaultPendingTTL = 10 * time.Minute
)

// Config wires the usage service. Logger defaults to slog.Default().
type Config struct {
	Store              *db.Store
	Logger             *slog.Logger
	Now                func() time.Time
	MaxPendingAttempts int
	PendingTTL         time.Duration
}

// Service correlates each committed UsageRecord with its AttemptRecord via
// the random opaque AttemptID and persists the combined metadata in one
// transaction. Hook handlers are safe for concurrent use.
type Service struct {
	store  *db.Store
	logger *slog.Logger
	now    func() time.Time
	max    int
	ttl    time.Duration

	mu      sync.Mutex
	pending map[string]attemptEntry
	order   []string
}

type attemptEntry struct {
	record    forward.AttemptRecord
	arrivedAt time.Time
}

// NewService validates the wiring and builds the hook consumer.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("usage: store is required")
	}
	service := &Service{
		store:   cfg.Store,
		logger:  cfg.Logger,
		now:     cfg.Now,
		max:     cfg.MaxPendingAttempts,
		ttl:     cfg.PendingTTL,
		pending: make(map[string]attemptEntry),
	}
	if service.logger == nil {
		service.logger = slog.Default()
	}
	if service.now == nil {
		service.now = time.Now
	}
	if service.max <= 0 {
		service.max = DefaultMaxPendingAttempts
	}
	if service.ttl <= 0 {
		service.ttl = DefaultPendingTTL
	}
	return service, nil
}

// HandleAttempt caches the metadata of one attempt until its committed Usage
// hook arrives. Pre-commit attempts are never persisted (accounting boundary
// = committed bytes) and expire through the bounded cache. A record without a
// correlation id is a broken contract and is dropped with an error log.
func (s *Service) HandleAttempt(record forward.AttemptRecord) {
	if s == nil || s.store == nil {
		return
	}
	if record.UserID <= 0 || record.AttemptID == "" || len(record.AttemptID) > db.MaxAttemptIDLen {
		s.logger.Error("usage: attempt record without a valid correlation id was dropped")
		return
	}
	now := s.now().UTC()
	s.mu.Lock()
	s.evictExpiredLocked(now)
	if _, exists := s.pending[record.AttemptID]; !exists {
		if len(s.pending) >= s.max {
			// Capacity bound: drop the oldest unpaired attempt. If its usage
			// hook arrives later, the orphan fallback in HandleUsage still
			// accounts the committed request with zeroed metadata.
			oldest := s.order[0]
			delete(s.pending, oldest)
			s.order = s.order[1:]
		}
		s.order = append(s.order, record.AttemptID)
	}
	s.pending[record.AttemptID] = attemptEntry{record: record, arrivedAt: now}
	s.mu.Unlock()
}

// HandleUsage persists one committed attempt's accounting in a single
// transaction. The record is correlated with its cached AttemptRecord by the
// random opaque AttemptID; a missing cache entry (eviction, out-of-order
// delivery) falls back to zeroed metadata so a committed request is never
// silently uncounted, and the repository's unique attempt id makes any
// duplicate delivery a no-op.
func (s *Service) HandleUsage(record forward.UsageRecord) {
	if s == nil || s.store == nil {
		return
	}
	if record.UserID <= 0 || record.AttemptID == "" || len(record.AttemptID) > db.MaxAttemptIDLen {
		s.logger.Error("usage: usage record without a valid correlation id was dropped; a committed request was not accounted")
		return
	}
	now := s.now().UTC()
	s.mu.Lock()
	entry, ok := s.pending[record.AttemptID]
	if ok {
		delete(s.pending, record.AttemptID)
		s.removeOrderLocked(record.AttemptID)
	}
	s.mu.Unlock()

	input := db.RequestLogInput{
		AttemptID:        record.AttemptID,
		UserID:           record.UserID,
		EndpointKeyID:    record.EndpointKeyID,
		UpstreamModelID:  record.UpstreamModelID,
		PromptTokens:     record.Usage.PromptTokens,
		CompletionTokens: record.Usage.CompletionTokens,
		TotalTokens:      record.Usage.TotalTokens,
		// A committed attempt without a valid usage triple is a truncated /
		// no-usage request: count it, never fabricate token values.
		UsageUnknown: !record.Usage.Present,
	}
	if ok {
		input.Model = entry.record.FullName
		input.StatusCode = entry.record.ClientStatus
		input.DurationMs = entry.record.Duration.Milliseconds()
		input.StartedAt = entry.record.StartedAt
		input.CompletedAt = entry.record.StartedAt.Add(entry.record.Duration)
		input.ErrorCode = entry.record.StableErrorCode
		input.ErrorDiag = entry.record.SafeDiagnostic
	} else {
		s.logger.Error("usage: usage record arrived without its attempt record; zeroed metadata was persisted",
			"user_id", record.UserID)
		input.StartedAt = now
		input.CompletedAt = now
	}
	// The hooks fire after the response was committed, so the request context
	// is no longer usable; the store's own busy_timeout bounds contention.
	if err := s.store.RecordRequest(context.Background(), input); err != nil {
		s.logger.Error("usage: request accounting failed; the committed request was not accounted",
			"user_id", record.UserID, "error", err)
	}
}

func (s *Service) evictExpiredLocked(now time.Time) {
	for len(s.order) > 0 {
		id := s.order[0]
		entry, ok := s.pending[id]
		if !ok {
			// Already removed by a usage hook; drop the stale queue slot.
			s.order = s.order[1:]
			continue
		}
		if now.Sub(entry.arrivedAt) <= s.ttl {
			break
		}
		delete(s.pending, id)
		s.order = s.order[1:]
	}
}

func (s *Service) removeOrderLocked(id string) {
	for i, candidate := range s.order {
		if candidate == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}
