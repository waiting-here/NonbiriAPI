// Package antiabuse owns the process-local violation windows and the
// server-authoritative consequences for RPM and charity misuse. No event in
// this package is persisted: only the resulting ban, suspension, or ledger
// entry is durable.
package antiabuse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

var (
	ErrWindowCapacity = errors.New("anti-abuse: bounded violation window is full")
	ErrInvalidUser    = errors.New("anti-abuse: user id is invalid")
)

const (
	rpmBanReason     = "RPM 超限自动封禁 (24h)"
	charityBanReason = "公益请求防滥用自动封禁"
	penaltyReason    = "公益请求过短防滥用扣分"
)

// Service is safe for concurrent RPM callbacks and charity preflight calls.
// Each user has an independent lock so a database action for one identity
// cannot serialize unrelated users, while the bounded map lock protects the
// total-user ceiling.
type Service struct {
	store  *db.Store
	now    func() time.Time
	logger *slog.Logger

	mu  sync.Mutex
	rpm map[int64]*userWindow
	// charity contains only users that have produced a short charity request
	// in the active window. It is never exported or included in user data.
	charity   map[int64]*userWindow
	maxUsers  int
	maxEvents int
	seq       atomic.Uint64
}

type ServiceConfig struct {
	Store            *db.Store
	Now              func() time.Time
	Logger           *slog.Logger
	MaxUsers         int
	MaxEventsPerUser int
}

type windowEvent struct {
	at   string // operation id; opaque and bounded
	time time.Time
}

type userWindow struct {
	mu               sync.Mutex
	events           []windowEvent
	seen             map[string]struct{}
	thresholdBanDone bool
	suspendDone      bool
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("anti-abuse: store is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxUsers <= 0 {
		cfg.MaxUsers = MaxWindowUsers
	}
	if cfg.MaxEventsPerUser <= 0 {
		cfg.MaxEventsPerUser = MaxEventsPerUser
	}
	if cfg.MaxUsers > MaxWindowUsers || cfg.MaxEventsPerUser > MaxEventsPerUser {
		return nil, errors.New("anti-abuse: window bounds exceed platform ceiling")
	}
	return &Service{
		store: cfg.Store, now: cfg.Now, logger: cfg.Logger,
		rpm: make(map[int64]*userWindow), charity: make(map[int64]*userWindow),
		maxUsers: cfg.MaxUsers, maxEvents: cfg.MaxEventsPerUser,
	}, nil
}

// RPMDenied records one denial only when reason is RPMUserLimit. The caller
// supplies isAdmin from a fresh server-side user lookup; administrator rows
// are always excluded. Other ratelimit reasons are intentionally ignored.
func (s *Service) RPMDenied(ctx context.Context, userID int64, isAdmin bool, reason ratelimit.RPMReason) {
	if s == nil || userID <= 0 || isAdmin || reason != ratelimit.RPMUserLimit {
		return
	}
	cfg := readConfig(s.store)
	if cfg.RPMBanThreshold == 0 {
		return
	}
	now := s.now()
	window, ok := s.getWindow(s.rpm, userID, time.Duration(cfg.RPMBanWindowSeconds)*time.Second)
	if !ok {
		s.logger.Warn("rpm anti-abuse window capacity reached; refusing to grow state", "user_id", userID)
		return
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	s.pruneLocked(window, now, time.Duration(cfg.RPMBanWindowSeconds)*time.Second)
	if accepted, fresh := s.addEventLocked(window, now, nextOperationID(&s.seq, "rpm", userID)); !accepted || !fresh {
		return
	}
	if len(window.events) < cfg.RPMBanThreshold {
		window.thresholdBanDone = false
		return
	}
	if window.thresholdBanDone {
		return
	}
	window.thresholdBanDone = true
	if err := s.store.BanUserWithOptions(userID, db.UserBan{
		Reason: rpmBanReason, DurationSeconds: cfg.RPMBanDurationSeconds, Auto: true,
	}); err != nil && !errors.Is(err, db.ErrNotFound) {
		s.logger.Error("rpm automatic ban failed", "user_id", userID, "error", err)
	}
}

// Preflight is injected at the charity routing boundary before route
// reservation or any upstream dispatch. The switch and suspension are checked
// here as well as in the routing transaction, so an ineligible request cannot create
// a penalty or an in-flight reservation.
func (s *Service) Preflight(ctx context.Context, userID int64, request *openai.ChatRequest) error {
	if s == nil || ctx == nil || userID <= 0 || request == nil {
		return forward.ErrAntiAbuseUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	charityEnabled, err := s.store.GetSiteConfigValue("charity_enabled")
	if err != nil {
		return forward.ErrAntiAbuseUnavailable
	}
	if charityEnabled != "1" {
		return forward.ErrCharityDisabled
	}
	user, err := s.store.GetUserByID(userID)
	if err != nil || user == nil || user.IsAdmin {
		return forward.ErrAntiAbuseUnavailable
	}
	now := s.now()
	if user.CharitySuspensionEffectiveAt(now) {
		return forward.ErrCharitySuspended
	}
	cfg := readConfig(s.store)
	if cfg.CharityMinChars == 0 {
		return nil
	}
	actual, err := request.CharityTextRuneCount()
	if err != nil {
		return openai.ErrInvalidRequest
	}
	if actual >= cfg.CharityMinChars {
		return nil
	}
	if err := s.recordViolation(ctx, userID, actual, cfg, now); err != nil {
		if errors.Is(err, ErrWindowCapacity) {
			return forward.ErrAntiAbuseUnavailable
		}
		return forward.ErrAntiAbuseUnavailable
	}
	return &forward.ContentTooShortError{Actual: actual, Minimum: cfg.CharityMinChars}
}

func (s *Service) recordViolation(ctx context.Context, userID int64, actual int, cfg Config, now time.Time) error {
	window, ok := s.getWindow(s.charity, userID, maxDuration(time.Duration(cfg.CharityViolationWindowSeconds)*time.Second, time.Duration(cfg.CharitySuspendWindowSeconds)*time.Second))
	if !ok {
		return ErrWindowCapacity
	}
	operationID := nextOperationID(&s.seq, "charity", userID)
	window.mu.Lock()
	defer window.mu.Unlock()
	violationDuration := time.Duration(cfg.CharityViolationWindowSeconds) * time.Second
	suspendDuration := time.Duration(cfg.CharitySuspendWindowSeconds) * time.Second
	windowDuration := violationDuration
	if suspendDuration > windowDuration {
		windowDuration = suspendDuration
	}
	s.pruneLocked(window, now, windowDuration)
	accepted, fresh := s.addEventLocked(window, now, operationID)
	if !accepted {
		return ErrWindowCapacity
	}
	if !fresh {
		return nil
	}
	violationCount := countSince(window.events, now, violationDuration)
	suspendCount := countSince(window.events, now, suspendDuration)
	if cfg.CharityViolationBanThreshold == 0 || violationCount < cfg.CharityViolationBanThreshold {
		window.thresholdBanDone = false
	}
	if cfg.CharitySuspendThreshold == 0 || suspendCount < cfg.CharitySuspendThreshold {
		window.suspendDone = false
	}
	windowBan := cfg.CharityViolationBanThreshold > 0 && violationCount >= cfg.CharityViolationBanThreshold && !window.thresholdBanDone
	suspend := cfg.CharitySuspendThreshold > 0 && suspendCount >= cfg.CharitySuspendThreshold && !window.suspendDone
	if windowBan {
		window.thresholdBanDone = true
	}
	if suspend {
		window.suspendDone = true
	}

	// The operation id is unique per accepted violation and is persisted only
	// in the penalty ledger's idempotency key. The in-memory seen set handles a
	// retried preflight before the DB call and the ledger handles process-local
	// duplicate calls with the same operation id in tests or wrappers.
	if cfg.CharityViolationDeductMilli > 0 {
		_, err := s.store.ApplyCreditOperation(ctx, db.CreditOperation{
			Kind: db.LedgerAntiAbusePenalty, UserID: userID,
			OperationID: operationID, CreditsDelta: -cfg.CharityViolationDeductMilli,
			Reason: penaltyReason, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("anti-abuse penalty: %w", err)
		}
	}
	if cfg.CharityViolationBanSeconds > 0 {
		if err := s.store.BanUserWithOptions(userID, db.UserBan{
			Reason: charityBanReason, DurationSeconds: cfg.CharityViolationBanSeconds, Auto: true,
		}); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse single ban: %w", err)
		}
	}
	if windowBan && cfg.CharityViolationWindowBanSeconds > 0 {
		if err := s.store.BanUserWithOptions(userID, db.UserBan{
			Reason: charityBanReason, DurationSeconds: cfg.CharityViolationWindowBanSeconds, Auto: true,
		}); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse window ban: %w", err)
		}
	}
	if suspend && cfg.CharitySuspendDurationSeconds > 0 {
		if err := s.store.SuspendCharityUntil(userID, now.Unix(), cfg.CharitySuspendDurationSeconds); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse charity suspension: %w", err)
		}
	}
	_ = actual // retained in the caller's bounded error only; never persisted
	return nil
}

func (s *Service) getWindow(store map[int64]*userWindow, userID int64, duration time.Duration) (*userWindow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window := store[userID]; window != nil {
		return window, true
	}
	if len(store) >= s.maxUsers {
		s.cleanupMapLocked(store, s.now(), duration)
		if len(store) >= s.maxUsers {
			return nil, false
		}
	}
	window := &userWindow{events: make([]windowEvent, 0, min(s.maxEvents, 16)), seen: make(map[string]struct{}, min(s.maxEvents, 16))}
	store[userID] = window
	return window, true
}

// ForgetUser drops all process-local history for an account before its
// deletion transaction starts. The windows are not exported or persisted, but
// removing them here prevents a deleted user id from occupying a bounded slot.
func (s *Service) ForgetUser(userID int64) {
	if s == nil || userID <= 0 {
		return
	}
	s.mu.Lock()
	delete(s.rpm, userID)
	delete(s.charity, userID)
	s.mu.Unlock()
}

// Cleanup removes expired empty user windows from both bounded maps. It is
// safe to call from a maintenance sweep; admission also performs a targeted
// cleanup when a map reaches its total-user ceiling.
func (s *Service) Cleanup() {
	if s == nil {
		return
	}
	cfg := readConfig(s.store)
	rpmDuration := time.Duration(cfg.RPMBanWindowSeconds) * time.Second
	charityDuration := maxDuration(time.Duration(cfg.CharityViolationWindowSeconds)*time.Second, time.Duration(cfg.CharitySuspendWindowSeconds)*time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.cleanupMapLocked(s.rpm, now, rpmDuration)
	s.cleanupMapLocked(s.charity, now, charityDuration)
}

func (s *Service) cleanupMapLocked(store map[int64]*userWindow, now time.Time, duration time.Duration) {
	for userID, window := range store {
		window.mu.Lock()
		s.pruneLocked(window, now, duration)
		empty := len(window.events) == 0
		window.mu.Unlock()
		if empty {
			delete(store, userID)
		}
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func (s *Service) pruneLocked(window *userWindow, now time.Time, duration time.Duration) {
	if duration <= 0 {
		window.events = window.events[:0]
		window.seen = make(map[string]struct{}, min(s.maxEvents, 16))
		window.thresholdBanDone = false
		window.suspendDone = false
		return
	}
	cutoff := now.Add(-duration)
	kept := window.events[:0]
	for _, event := range window.events {
		if event.time.After(cutoff) {
			kept = append(kept, event)
		}
	}
	window.events = kept
	if len(window.events) == 0 {
		window.thresholdBanDone = false
		window.suspendDone = false
		window.seen = make(map[string]struct{}, min(s.maxEvents, 16))
	}
}

func countSince(events []windowEvent, now time.Time, duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	cutoff := now.Add(-duration)
	count := 0
	for _, event := range events {
		if event.time.After(cutoff) {
			count++
		}
	}
	return count
}

func (s *Service) addEventLocked(window *userWindow, now time.Time, operationID string) (accepted, fresh bool) {
	if _, exists := window.seen[operationID]; exists {
		return true, false
	}
	if len(window.events) >= s.maxEvents {
		return false, false
	}
	window.events = append(window.events, windowEvent{at: operationID, time: now})
	window.seen[operationID] = struct{}{}
	return true, true
}

func nextOperationID(seq *atomic.Uint64, kind string, userID int64) string {
	return fmt.Sprintf("sys.anti-abuse.%s.%d.%d", kind, userID, seq.Add(1))
}
