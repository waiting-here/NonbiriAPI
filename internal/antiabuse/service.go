package antiabuse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type Retirement interface {
	Commit() bool
	Abort() bool
}
type RejectionRecorder interface {
	RecordCharityRejectionTx(context.Context, *sql.Tx, int64, string, string, int, int, int64) error
	RecordRejectionTx(context.Context, *sql.Tx, int64, requestattempt.Fact, int64) error
}
type ServiceConfig struct {
	Database            *sql.DB
	Rejections          RejectionRecorder
	BeginUserRetirement func(context.Context, int64) (Retirement, error)
	OnBan               func(int64)
	CancelUserDuelsTx   func(context.Context, *sql.Tx, int64, string, int64) (func(bool), error)
	Now                 func() time.Time
}

// Service serializes bounded durable windows with their transactional effects.
// Lock order is flow admission, window gate, then SQLite. Failed transactions
// never consume an event; ordinary account deletion drains admitted calls first.
type Service struct {
	config  ServiceConfig
	gate    chan struct{}
	windows map[windowKey]violationWindow
	events  int
	closed  bool
	cancel  context.CancelFunc
	done    <-chan struct{}
}
type windowKey struct {
	userID  int64
	charity bool
}
type violationWindow struct {
	events               []int64
	facts                []windowEvent
	next                 int64
	banDone, suspendDone bool
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Database == nil || config.Rejections == nil || config.BeginUserRetirement == nil {
		return nil, charityrouting.ErrUnavailable
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	s := &Service{config: config, gate: make(chan struct{}, 1), windows: make(map[windowKey]violationWindow)}
	if err := s.restore(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	go func() { defer close(done); s.runCleanup(ctx) }()
	return s, nil
}

// RecordShort rechecks the current account and policy in the effects transaction.
// A nil rejection means the administrator relaxed the minimum in the meantime.
func (s *Service) RecordShort(ctx context.Context, userID int64, model string, actual int) (*charityrouting.ContentTooShortError, error) {
	if actual < 0 || actual > MaxCharityContentRuneCount {
		return nil, charityrouting.ErrInvalidRequest
	}
	return s.record(ctx, userID, true, model, actual)
}

// RPMDenied records an already classified charity request. The ingress observer
// must establish its resource scope; downstream or shared-key 429s are not events.
func (s *Service) RPMDenied(ctx context.Context, userID int64, reason ratelimit.RPMReason) error {
	if reason != ratelimit.RPMUserLimit {
		return nil
	}
	_, err := s.record(ctx, userID, false, "", 0)
	return err
}

func (s *Service) record(parent context.Context, userID int64, charity bool, model string, actual int) (*charityrouting.ContentTooShortError, error) {
	if s == nil || parent == nil || userID <= 0 {
		return nil, charityrouting.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	retirement, err := s.config.BeginUserRetirement(ctx, userID)
	if err != nil {
		return nil, err
	}
	if retirement == nil {
		return nil, charityrouting.ErrUnavailable
	}
	defer retirement.Abort()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	locked := true
	defer func() {
		if locked {
			<-s.gate
		}
	}()
	if s.closed {
		return nil, charityrouting.ErrUnavailable
	}
	now := s.config.Now().Unix()
	if now < 0 || now > 253402300799 {
		return nil, charityrouting.ErrInvariant
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	reader := &transactionConfig{ctx: ctx, tx: tx}
	cfg := readConfig(reader)
	if reader.err != nil {
		return nil, reader.err
	}
	requestID, err := requestattempt.Identity(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Internal reentry of a committed attempt never repeats its window or
	// financial effects, including after that attempt revoked the CallerKey.
	var priorOwner sql.NullInt64
	var priorCode, priorDiag string
	err = tx.QueryRowContext(ctx, `SELECT user_id,error_code,error_diag FROM request_logs WHERE logical_request_id=?`, requestID).Scan(&priorOwner, &priorCode, &priorDiag)
	if err == nil {
		if !priorOwner.Valid || priorOwner.Int64 != userID {
			return nil, charityrouting.ErrInvariant
		}
		requestattempt.Handled(ctx)
		if priorCode == "content_too_short" {
			var measured, minimum int
			if _, err := fmt.Sscanf(priorDiag, "content has %d characters; minimum is %d", &measured, &minimum); err != nil {
				return nil, err
			}
			return &charityrouting.ContentTooShortError{Actual: measured, Minimum: minimum, RequestID: requestID}, nil
		}
		return nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var isAdmin, banned int
	var until, suspended sql.NullInt64
	var revision []byte
	err = tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until,charity_suspended_until,revision FROM users WHERE id=?`, userID).Scan(&isAdmin, &banned, &until, &suspended, &revision)
	if errors.Is(err, sql.ErrNoRows) || isAdmin != 0 || banned != 0 && (!until.Valid || until.Int64 > now) {
		return nil, charityrouting.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if charity {
		var enabled string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_enabled'`).Scan(&enabled); err != nil {
			return nil, err
		}
		if enabled != "1" {
			return nil, charityrouting.ErrFeatureDisabled
		}
		if suspended.Valid && suspended.Int64 > now {
			return nil, charityrouting.ErrCharitySuspended
		}
	}
	key := windowKey{userID: userID, charity: charity}
	windows, eventCount, err := s.cleanupCopy(ctx, tx, now, cfg, key)
	if err != nil {
		return nil, err
	}
	if charity && actual >= cfg.CharityMinChars || !charity && cfg.RPMBanThreshold == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		s.windows, s.events = windows, eventCount
		return nil, nil
	}
	old, exists := windows[key]
	// Per-user capacity reaches every supported threshold; the separate global
	// event ceiling keeps many busy accounts from multiplying that allocation.
	if !exists && len(windows) >= MaxWindowUsers || len(old.events) >= MaxViolationThreshold || eventCount >= MaxWindowUsers*MaxEventsPerUser {
		stage := "flow"
		if charity {
			stage = "preflight"
		}
		fact := requestattempt.Snapshot(ctx, requestID, model, stage, "resource_limit_exceeded", 422, "resource_limit_exceeded")
		if err := s.config.Rejections.RecordRejectionTx(ctx, tx, userID, fact, now); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		s.windows, s.events = windows, eventCount
		requestattempt.Handled(ctx)
		return nil, charityrouting.ErrResourceLimit
	}
	window := old
	window.events = append(append([]int64(nil), window.events...), now)
	event := windowEvent{At: now, RequestID: requestID}
	if charity {
		event.Chars = new(actual)
	}
	window.facts = append(append([]windowEvent(nil), window.facts...), event)
	banSeconds, suspendSeconds := int64(0), int64(0)
	windowBan := false
	if charity {
		banSeconds = cfg.CharityViolationBanSeconds
		count := countEvents(window.events, now, cfg.CharityViolationWindowSeconds)
		if cfg.CharityViolationBanThreshold == 0 || count < cfg.CharityViolationBanThreshold {
			window.banDone = false
		}
		if cfg.CharityViolationBanThreshold > 0 && count >= cfg.CharityViolationBanThreshold && !window.banDone && cfg.CharityViolationWindowBanSeconds > 0 {
			banSeconds = max(banSeconds, cfg.CharityViolationWindowBanSeconds)
			window.banDone = true
			windowBan = true
		}
		count = countEvents(window.events, now, cfg.CharitySuspendWindowSeconds)
		if cfg.CharitySuspendThreshold == 0 || count < cfg.CharitySuspendThreshold {
			window.suspendDone = false
		}
		if cfg.CharitySuspendThreshold > 0 && count >= cfg.CharitySuspendThreshold && !window.suspendDone && cfg.CharitySuspendDurationSeconds > 0 {
			suspendSeconds = cfg.CharitySuspendDurationSeconds
			window.suspendDone = true
		}
	} else {
		count := countEvents(window.events, now, cfg.RPMBanWindowSeconds)
		if count < cfg.RPMBanThreshold {
			window.banDone = false
		}
		if count >= cfg.RPMBanThreshold && !window.banDone {
			banSeconds = cfg.RPMBanDurationSeconds
			window.banDone = true
		}
	}
	var rejection *charityrouting.ContentTooShortError
	operationID := ""
	if charity {
		if err := s.config.Rejections.RecordCharityRejectionTx(ctx, tx, userID, requestID, model, actual, cfg.CharityMinChars, now); err != nil {
			return nil, err
		}
		if cfg.CharityViolationDeductMilli > 0 {
			operationID = "op_" + requestID[4:]
			wallet, err := ledger.UserAccount(ctx, tx, userID)
			if err != nil {
				return nil, err
			}
			external, err := ledger.CodedAccount(ctx, tx, "external")
			if err != nil {
				return nil, err
			}
			plan, err := ledger.NewAntiAbusePenalty(ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: now}, wallet.ID, external.ID, ledger.AmountFromMilli(cfg.CharityViolationDeductMilli), "Short charity request")
			if err != nil {
				return nil, err
			}
			if _, err := ledger.Apply(ctx, tx, plan); err != nil {
				return nil, err
			}
		}
		rejection = &charityrouting.ContentTooShortError{Actual: actual, Minimum: cfg.CharityMinChars, RequestID: requestID}
	} else {
		fact := requestattempt.Snapshot(ctx, requestID, model, "flow", "user_rpm", 429, "rate_limited")
		if err := s.config.Rejections.RecordRejectionTx(ctx, tx, userID, fact, now); err != nil {
			return nil, err
		}
	}
	var finalizeDuels func(bool)
	duelsCommitted := false
	if banSeconds > 0 && s.config.CancelUserDuelsTx != nil {
		finalizeDuels, err = s.config.CancelUserDuelsTx(ctx, tx, userID, "account_unavailable", now)
		if err != nil {
			return nil, err
		}
		if finalizeDuels == nil {
			return nil, charityrouting.ErrInvariant
		}
		defer func() { finalizeDuels(duelsCommitted) }()
	}
	if banSeconds > 0 || suspendSeconds > 0 {
		if err := applyRestrictions(ctx, tx, userID, now, revision, banSeconds, suspendSeconds); err != nil {
			return nil, err
		}
	}
	if err := expireUserTx(ctx, tx, userID, now); err != nil {
		return nil, err
	}
	reason := "charity_rpm"
	if charity {
		reason = "charity_short_content"
	}
	if operationID != "" {
		stats := evidenceStatistics{Rule: "short_content_deduction", WindowStart: now, WindowEnd: now, Count: 1, Actual: new(actual), Minimum: new(cfg.CharityMinChars)}
		if err := recordCase(ctx, tx, userID, now, "deduction", reason, requestID, operationID, now, cfg, stats, []windowEvent{event}); err != nil {
			return nil, err
		}
	}
	if banSeconds > 0 {
		stats := evidenceStatistics{Rule: "rpm_window", WindowStart: now - cfg.RPMBanWindowSeconds, WindowEnd: now, Threshold: cfg.RPMBanThreshold}
		facts := matchingFacts(window, now, cfg.RPMBanWindowSeconds)
		if charity {
			stats = evidenceStatistics{Rule: "short_content_direct", WindowStart: now, WindowEnd: now, Actual: new(actual), Minimum: new(cfg.CharityMinChars), DirectSeconds: cfg.CharityViolationBanSeconds}
			facts = []windowEvent{event}
			if windowBan {
				stats.Rule = "short_content_window"
				stats.WindowStart = now - cfg.CharityViolationWindowSeconds
				stats.Threshold = cfg.CharityViolationBanThreshold
				facts = matchingFacts(window, now, cfg.CharityViolationWindowSeconds)
			}
		}
		stats.Count = len(facts)
		var ends int64
		if err := tx.QueryRowContext(ctx, `SELECT banned_until FROM users WHERE id=?`, userID).Scan(&ends); err != nil {
			return nil, err
		}
		if err := recordCase(ctx, tx, userID, now, "ban", reason, requestID, "", ends, cfg, stats, facts); err != nil {
			return nil, err
		}
	}
	if suspendSeconds > 0 {
		facts := matchingFacts(window, now, cfg.CharitySuspendWindowSeconds)
		stats := evidenceStatistics{Rule: "short_content_suspend_window", WindowStart: now - cfg.CharitySuspendWindowSeconds, WindowEnd: now, Threshold: cfg.CharitySuspendThreshold, Count: len(facts), Actual: new(actual), Minimum: new(cfg.CharityMinChars)}
		var ends int64
		if err := tx.QueryRowContext(ctx, `SELECT charity_suspended_until FROM users WHERE id=?`, userID).Scan(&ends); err != nil {
			return nil, err
		}
		if err := recordCase(ctx, tx, userID, now, "charity_suspend", reason, requestID, "", ends, cfg, stats, facts); err != nil {
			return nil, err
		}
	}
	if err := persistWindow(ctx, tx, key, &window, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	duelsCommitted = true
	windows[key] = window
	s.windows, s.events = windows, eventCount+1
	requestattempt.Handled(ctx)
	<-s.gate
	locked = false
	if banSeconds > 0 {
		retirement.Commit()
		if s.config.OnBan != nil {
			s.config.OnBan(userID)
		}
	}
	return rejection, nil
}

func applyRestrictions(ctx context.Context, tx *sql.Tx, userID, now int64, revision []byte, banSeconds, suspendSeconds int64) error {
	if banSeconds > 253402300799-now || suspendSeconds > 253402300799-now {
		return charityrouting.ErrInvariant
	}
	current, err := db.DecodeU128(revision)
	if err != nil {
		return err
	}
	next, err := db.U128FromBig(new(big.Int).Add(current.Big(), big.NewInt(1)))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET
is_banned=CASE WHEN ?>0 THEN 1 ELSE is_banned END,
auto_banned=CASE WHEN ?>0 THEN 1 ELSE auto_banned END,
ban_kind=CASE WHEN ?>0 THEN '' ELSE ban_kind END,
banned_reason=CASE WHEN ?>0 THEN 'Automatic abuse prevention' ELSE banned_reason END,
banned_until=CASE WHEN ?>0 THEN MAX(COALESCE(banned_until,0),?) ELSE banned_until END,
charity_suspended_until=CASE WHEN ?>0 THEN MAX(COALESCE(charity_suspended_until,0),?) ELSE charity_suspended_until END,
revision=?,updated_at=MAX(updated_at,?) WHERE id=? AND is_admin=0`, banSeconds, banSeconds, banSeconds, banSeconds, banSeconds, now+banSeconds, suspendSeconds, now+suspendSeconds, db.EncodeU128(next), now, userID); err != nil {
		return err
	}
	if banSeconds == 0 {
		return nil
	}
	if err := useractivity.RescheduleTx(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE caller_keys SET generation=CASE WHEN key_hash IS NULL THEN generation ELSE generation+1 END,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=? WHERE user_id=? AND (key_hash IS NULL OR generation<?)`, now, userID, int64(math.MaxInt64))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return charityrouting.ErrInvariant
	}
	return nil
}

type transactionConfig struct {
	ctx context.Context
	tx  *sql.Tx
	err error
}

func (r *transactionConfig) GetSiteConfigValue(key string) (string, error) {
	var value string
	err := r.tx.QueryRowContext(r.ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if err != nil {
		r.err = err
	}
	return value, err
}
func countEvents(events []int64, now, duration int64) int {
	count := 0
	for _, at := range events {
		if at > now-duration {
			count++
		}
	}
	return count
}

// ForgetUser runs after the caller lifecycle gate and deletion transaction drain.
func (s *Service) ForgetUser(userID int64) {
	if s == nil {
		return
	}
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	for _, charity := range []bool{false, true} {
		key := windowKey{userID: userID, charity: charity}
		s.events -= len(s.windows[key].events)
		delete(s.windows, key)
	}
}
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	s.closed = true
	clear(s.windows)
	s.events = 0
	return nil
}
