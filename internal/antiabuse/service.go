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
	ErrUserRetiring   = errors.New("anti-abuse: user deletion is in progress")
)

const (
	rpmBanReason      = "RPM 超限自动封禁 (24h)"
	charityBanReason  = "公益请求防滥用自动封禁"
	penaltyReason     = "公益请求过短防滥用扣分"
	maxUserLookupPins = db.MaxUserConcurrencyLimit
)

// Service is safe for concurrent RPM callbacks and charity preflight calls.
// Each user has an independent lock so a database action for one identity
// cannot serialize unrelated users, while the bounded map lock protects the
// total-user ceiling.
type Service struct {
	store  *db.Store
	now    func() time.Time
	logger *slog.Logger
	// lookupUser is fixed at construction to the authoritative repository
	// read. Keeping the function as a field permits deterministic gap tests.
	lookupUser func(userID int64) (*db.User, error)

	mu  sync.Mutex
	rpm map[int64]*userWindow
	// charity contains only users that have produced a short charity request
	// in the active window. It is never exported or included in user data.
	charity map[int64]*userWindow
	// retiring marks account deletions between begin and commit/abort. A
	// committed marker remains until all pre-begin lookups and pinned windows
	// drain, preventing a stale request from recreating state after DB delete.
	// lookups pins missing-window DB existence checks across the period where
	// mu must be released. Both maps are guarded by mu and bounded by maxUsers.
	retiring            map[int64]*userDeletion
	lookups             map[int64]int
	maxUsers            int
	maxEvents           int
	seq                 atomic.Uint64
	beginUserRetirement func(userID int64) (commit func() bool, abort func() bool, err error)
}

type ServiceConfig struct {
	Store               *db.Store
	Now                 func() time.Time
	Logger              *slog.Logger
	MaxUsers            int
	MaxEventsPerUser    int
	BeginUserRetirement func(userID int64) (commit func() bool, abort func() bool, err error)
}

type windowEvent struct {
	at   string // operation id; opaque and bounded
	time time.Time
}

type userWindow struct {
	userID int64
	// pins is guarded only by Service.mu. getWindow increments it before
	// returning a map-owned pointer; cleanup may prune/delete only at zero.
	// This closes the gap between releasing Service.mu and acquiring the
	// per-window locks without introducing a reverse window -> Service order.
	pins int
	// effectsMu serializes one user's state decision plus its ordered durable
	// consequences without keeping mu across DB, retirement, or logging calls.
	// Every event path acquires effectsMu before mu; cleanup acquires only mu.
	// No code may acquire effectsMu while holding mu.
	effectsMu sync.Mutex
	mu        sync.Mutex
	events    []windowEvent
	// seen mirrors only active events; pruneLocked removes each expired id so
	// sustained rolling traffic cannot grow it beyond the event bound.
	seen             map[string]struct{}
	thresholdBanDone bool
	suspendDone      bool
}

type userDeletion struct {
	committed bool
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
		lookupUser: cfg.Store.GetUserByID,
		rpm:        make(map[int64]*userWindow), charity: make(map[int64]*userWindow),
		retiring: make(map[int64]*userDeletion), lookups: make(map[int64]int),
		maxUsers: cfg.MaxUsers, maxEvents: cfg.MaxEventsPerUser,
		beginUserRetirement: cfg.BeginUserRetirement,
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
	window, err := s.getWindow(s.rpm, userID, time.Duration(cfg.RPMBanWindowSeconds)*time.Second)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserRetiring), errors.Is(err, ErrInvalidUser):
			return
		case errors.Is(err, ErrWindowCapacity):
			s.logger.Warn("rpm anti-abuse window capacity reached; refusing to grow state", "user_id", userID)
		default:
			s.logger.Error("rpm anti-abuse account revalidation failed", "user_id", userID, "error", err)
		}
		return
	}
	defer s.unpinWindow(window)
	// Serialize same-user decisions and their ordered consequence, but release
	// the state mutex before the retirement boundary/DB mutation. This lock
	// order prevents window -> retirement -> service-map cycles with account
	// deletion and maintenance cleanup.
	window.effectsMu.Lock()
	defer window.effectsMu.Unlock()
	shouldBan := func() bool {
		window.mu.Lock()
		defer window.mu.Unlock()
		s.pruneLocked(window, now, time.Duration(cfg.RPMBanWindowSeconds)*time.Second)
		if accepted, fresh := s.addEventLocked(window, now, nextOperationID(&s.seq, "rpm", userID)); !accepted || !fresh {
			return false
		}
		if len(window.events) < cfg.RPMBanThreshold {
			window.thresholdBanDone = false
			return false
		}
		if window.thresholdBanDone {
			return false
		}
		window.thresholdBanDone = true
		return true
	}()
	if !shouldBan {
		return
	}
	if err := s.banUser(userID, db.UserBan{
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
	window, err := s.getWindow(s.charity, userID, maxDuration(time.Duration(cfg.CharityViolationWindowSeconds)*time.Second, time.Duration(cfg.CharitySuspendWindowSeconds)*time.Second))
	if err != nil {
		return err
	}
	defer s.unpinWindow(window)
	operationID := nextOperationID(&s.seq, "charity", userID)
	// effectsMu preserves the existing per-user ordering/failure semantics,
	// while mu protects only the bounded in-memory state calculation. Durable
	// effects below run with mu released.
	window.effectsMu.Lock()
	defer window.effectsMu.Unlock()
	type violationDecision struct {
		fresh, windowBan, suspend bool
	}
	decision, err := func() (violationDecision, error) {
		window.mu.Lock()
		defer window.mu.Unlock()
		violationDuration := time.Duration(cfg.CharityViolationWindowSeconds) * time.Second
		suspendDuration := time.Duration(cfg.CharitySuspendWindowSeconds) * time.Second
		windowDuration := maxDuration(violationDuration, suspendDuration)
		s.pruneLocked(window, now, windowDuration)
		accepted, fresh := s.addEventLocked(window, now, operationID)
		if !accepted {
			return violationDecision{}, ErrWindowCapacity
		}
		if !fresh {
			return violationDecision{}, nil
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
		return violationDecision{fresh: true, windowBan: windowBan, suspend: suspend}, nil
	}()
	if err != nil {
		return err
	}
	if !decision.fresh {
		return nil
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
		if err := s.banUser(userID, db.UserBan{
			Reason: charityBanReason, DurationSeconds: cfg.CharityViolationBanSeconds, Auto: true,
		}); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse single ban: %w", err)
		}
	}
	if decision.windowBan && cfg.CharityViolationWindowBanSeconds > 0 {
		if err := s.banUser(userID, db.UserBan{
			Reason: charityBanReason, DurationSeconds: cfg.CharityViolationWindowBanSeconds, Auto: true,
		}); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse window ban: %w", err)
		}
	}
	if decision.suspend && cfg.CharitySuspendDurationSeconds > 0 {
		if err := s.store.SuspendCharityUntil(userID, now.Unix(), cfg.CharitySuspendDurationSeconds); err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("anti-abuse charity suspension: %w", err)
		}
	}
	_ = actual // retained in the caller's bounded error only; never persisted
	return nil
}

// banUser holds the shared admission write barrier across the authoritative
// DB mutation. A failed mutation aborts the barrier; success retires the
// exact active counter before admitting another live-account resolver.
func (s *Service) banUser(userID int64, ban db.UserBan) error {
	commit := func() bool { return true }
	abort := func() bool { return false }
	if s.beginUserRetirement != nil {
		var err error
		commit, abort, err = s.beginUserRetirement(userID)
		if err != nil {
			return err
		}
		if commit == nil || abort == nil {
			if abort != nil {
				abort()
			}
			return errors.New("anti-abuse: invalid user retirement boundary")
		}
	}
	defer abort()
	if err := s.store.BanUserWithOptions(userID, ban); err != nil {
		return err
	}
	commit()
	return nil
}

func (s *Service) getWindow(store map[int64]*userWindow, userID int64, duration time.Duration) (*userWindow, error) {
	s.mu.Lock()
	if _, retiring := s.retiring[userID]; retiring {
		s.mu.Unlock()
		return nil, ErrUserRetiring
	}
	if window := store[userID]; window != nil {
		window.pins++
		s.mu.Unlock()
		return window, nil
	}
	if _, exists := s.lookups[userID]; !exists && len(s.lookups) >= s.maxUsers {
		s.mu.Unlock()
		return nil, ErrWindowCapacity
	}
	if s.lookups[userID] >= maxUserLookupPins {
		s.mu.Unlock()
		return nil, ErrWindowCapacity
	}
	s.lookups[userID]++
	s.mu.Unlock()

	// Missing windows require a fresh authoritative existence read. This is
	// intentionally outside Service.mu; the lookup pin plus the second marker
	// check below orders a concurrent BeginUserDeletion around the DB read.
	user, lookupErr := s.lookupUser(userID)

	s.mu.Lock()
	s.lookups[userID]--
	if s.lookups[userID] == 0 {
		delete(s.lookups, userID)
	}
	if _, retiring := s.retiring[userID]; retiring {
		s.collectCommittedDeletionLocked(userID)
		s.mu.Unlock()
		return nil, ErrUserRetiring
	}
	if lookupErr != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("anti-abuse user lookup: %w", lookupErr)
	}
	if user == nil {
		s.mu.Unlock()
		return nil, ErrInvalidUser
	}
	// Another lookup may have installed the same user's window while the DB
	// read was in flight. Pin that exact pointer instead of replacing it.
	if window := store[userID]; window != nil {
		window.pins++
		s.mu.Unlock()
		return window, nil
	}
	if len(store) >= s.maxUsers {
		s.cleanupMapLocked(store, s.now(), duration)
		if len(store) >= s.maxUsers {
			s.mu.Unlock()
			return nil, ErrWindowCapacity
		}
	}
	window := &userWindow{userID: userID, pins: 1, events: make([]windowEvent, 0, min(s.maxEvents, 16)), seen: make(map[string]struct{}, min(s.maxEvents, 16))}
	store[userID] = window
	s.mu.Unlock()
	return window, nil
}

func (s *Service) unpinWindow(window *userWindow) {
	if s == nil || window == nil {
		return
	}
	s.mu.Lock()
	if window.pins <= 0 {
		s.mu.Unlock()
		panic("anti-abuse: window pin accounting underflow")
	}
	window.pins--
	// Commit never waits for a pinned event (which may itself be waiting on the
	// flow-control deletion stripe). The last unpin performs final collection.
	s.collectCommittedDeletionLocked(window.userID)
	s.mu.Unlock()
}

// BeginUserDeletion marks an account deletion before its DB transaction. New
// window acquisition fails closed while the boundary is active. Commit drops
// both map-owned windows after the authoritative delete succeeds; pinned
// callers may finish against their retained pointers and are then collected.
// Abort preserves the exact map pointers/events and reopens acquisition. Both
// terminal functions are idempotent.
func (s *Service) BeginUserDeletion(userID int64) (commit func() bool, abort func() bool, err error) {
	if s == nil || userID <= 0 {
		return nil, nil, ErrInvalidUser
	}
	s.mu.Lock()
	if _, exists := s.retiring[userID]; exists {
		s.mu.Unlock()
		return nil, nil, ErrUserRetiring
	}
	if len(s.retiring) >= s.maxUsers {
		s.mu.Unlock()
		return nil, nil, ErrWindowCapacity
	}
	marker := &userDeletion{}
	s.retiring[userID] = marker
	s.mu.Unlock()

	var done atomic.Bool
	finish := func(remove bool) bool {
		if !done.CompareAndSwap(false, true) {
			return false
		}
		s.mu.Lock()
		if remove {
			if current := s.retiring[userID]; current == marker {
				current.committed = true
				s.collectCommittedDeletionLocked(userID)
			}
		} else if current := s.retiring[userID]; current == marker {
			delete(s.retiring, userID)
		}
		s.mu.Unlock()
		return true
	}
	return func() bool { return finish(true) }, func() bool { return finish(false) }, nil
}

func (s *Service) collectCommittedDeletionLocked(userID int64) {
	marker := s.retiring[userID]
	if marker == nil || !marker.committed || s.lookups[userID] != 0 {
		return
	}
	if window := s.rpm[userID]; window != nil && window.pins != 0 {
		return
	}
	if window := s.charity[userID]; window != nil && window.pins != 0 {
		return
	}
	delete(s.rpm, userID)
	delete(s.charity, userID)
	delete(s.retiring, userID)
}

// ForgetUser is the non-transactional convenience for a caller that has
// already made authoritative account state permanently reject new work. A
// delete that can still fail must use BeginUserDeletion so Abort can preserve
// pinned windows and their threshold history.
func (s *Service) ForgetUser(userID int64) {
	commit, abort, err := s.BeginUserDeletion(userID)
	if err != nil {
		return
	}
	defer abort()
	commit()
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
		if s.retiring[userID] != nil {
			continue
		}
		if window.pins != 0 {
			continue
		}
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
		clear(window.events)
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
			continue
		}
		delete(window.seen, event.at)
	}
	clear(window.events[len(kept):])
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
