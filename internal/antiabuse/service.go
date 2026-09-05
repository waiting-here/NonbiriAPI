package antiabuse

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

type Retirement interface {
	Commit() bool
	Abort() bool
}
type RejectionRecorder interface {
	RecordCharityRejectionTx(context.Context, *sql.Tx, int64, string, string, int, int, int64) error
}
type ServiceConfig struct {
	Database            *sql.DB
	Rejections          RejectionRecorder
	BeginUserRetirement func(context.Context, int64) (Retirement, error)
	OnBan               func(int64)
	Now                 func() time.Time
}

// Service serializes bounded process-local windows with their durable effects.
// Lock order is flow admission, window gate, then SQLite. Failed transactions
// never consume an event; ordinary account deletion drains admitted calls first.
type Service struct {
	config  ServiceConfig
	gate    chan struct{}
	windows map[windowKey]violationWindow
	events  int
	closed  bool
}
type windowKey struct {
	userID  int64
	charity bool
}
type violationWindow struct {
	events               []int64
	banDone, suspendDone bool
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Database == nil || config.Rejections == nil || config.BeginUserRetirement == nil {
		return nil, charityrouting.ErrUnavailable
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{config: config, gate: make(chan struct{}, 1), windows: make(map[windowKey]violationWindow)}, nil
}

// RecordShort rechecks the current account and policy in the effects transaction.
// A nil rejection means the administrator relaxed the minimum in the meantime.
func (s *Service) RecordShort(ctx context.Context, userID int64, model string, actual int) (*charityrouting.ContentTooShortError, error) {
	if actual < 0 || actual > MaxCharityContentRuneCount {
		return nil, charityrouting.ErrInvalidRequest
	}
	return s.record(ctx, userID, true, model, actual)
}

func (s *Service) RPMDenied(ctx context.Context, userID int64, reason ratelimit.RPMReason) {
	if reason != ratelimit.RPMUserLimit {
		return
	}
	if _, err := s.record(ctx, userID, false, "", 0); err != nil && !errors.Is(err, charityrouting.ErrUnauthorized) && !errors.Is(err, context.Canceled) {
		slog.Error("automatic rate-limit policy could not be applied", "error", err)
	}
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
		if actual >= cfg.CharityMinChars {
			return nil, nil
		}
	} else if cfg.RPMBanThreshold == 0 {
		return nil, nil
	}

	s.cleanup(now, cfg)
	key := windowKey{userID: userID, charity: charity}
	old, exists := s.windows[key]
	// Per-user capacity reaches every supported threshold; the separate global
	// event ceiling keeps many busy accounts from multiplying that allocation.
	if !exists && len(s.windows) >= MaxWindowUsers || len(old.events) >= MaxViolationThreshold || s.events >= MaxWindowUsers*MaxEventsPerUser {
		return nil, charityrouting.ErrResourceLimit
	}
	window := violationWindow{events: append(append([]int64(nil), old.events...), now), banDone: old.banDone, suspendDone: old.suspendDone}
	banSeconds, suspendSeconds := int64(0), int64(0)
	if charity {
		banSeconds = cfg.CharityViolationBanSeconds
		count := countEvents(window.events, now, cfg.CharityViolationWindowSeconds)
		if cfg.CharityViolationBanThreshold == 0 || count < cfg.CharityViolationBanThreshold {
			window.banDone = false
		}
		if cfg.CharityViolationBanThreshold > 0 && count >= cfg.CharityViolationBanThreshold && !window.banDone && cfg.CharityViolationWindowBanSeconds > 0 {
			banSeconds = max(banSeconds, cfg.CharityViolationWindowBanSeconds)
			window.banDone = true
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
	if charity {
		requestID, err := db.GenerateOpaqueID("req_")
		if err != nil {
			return nil, err
		}
		if err := s.config.Rejections.RecordCharityRejectionTx(ctx, tx, userID, requestID, model, actual, cfg.CharityMinChars, now); err != nil {
			return nil, err
		}
		if cfg.CharityViolationDeductMilli > 0 {
			wallet, err := ledger.UserAccount(ctx, tx, userID)
			if err != nil {
				return nil, err
			}
			external, err := ledger.CodedAccount(ctx, tx, "external")
			if err != nil {
				return nil, err
			}
			plan, err := ledger.NewAntiAbusePenalty(ledger.Meta{OperationID: "op_" + requestID[4:], ActorUserID: userID, CreatedAt: now}, wallet.ID, external.ID, ledger.AmountFromMilli(cfg.CharityViolationDeductMilli), "Short charity request")
			if err != nil {
				return nil, err
			}
			if _, err := ledger.Apply(ctx, tx, plan); err != nil {
				return nil, err
			}
		}
		rejection = &charityrouting.ContentTooShortError{Actual: actual, Minimum: cfg.CharityMinChars, RequestID: requestID}
	}
	if banSeconds > 0 || suspendSeconds > 0 {
		if err := applyRestrictions(ctx, tx, userID, now, revision, banSeconds, suspendSeconds); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.events += len(window.events) - len(old.events)
	s.windows[key] = window
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
banned_reason=CASE WHEN ?>0 THEN 'Automatic abuse prevention' ELSE banned_reason END,
banned_until=CASE WHEN ?>0 THEN MAX(COALESCE(banned_until,0),?) ELSE banned_until END,
charity_suspended_until=CASE WHEN ?>0 THEN MAX(COALESCE(charity_suspended_until,0),?) ELSE charity_suspended_until END,
revision=?,updated_at=MAX(updated_at,?) WHERE id=? AND is_admin=0`, banSeconds, banSeconds, banSeconds, banSeconds, now+banSeconds, suspendSeconds, now+suspendSeconds, db.EncodeU128(next), now, userID); err != nil {
		return err
	}
	if banSeconds == 0 {
		return nil
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
func (s *Service) cleanup(now int64, cfg Config) {
	for key, window := range s.windows {
		duration := cfg.RPMBanWindowSeconds
		if key.charity {
			duration = max(cfg.CharityViolationWindowSeconds, cfg.CharitySuspendWindowSeconds)
		}
		kept := window.events[:0]
		for _, at := range window.events {
			if at > now-duration {
				kept = append(kept, at)
			}
		}
		s.events -= len(window.events) - len(kept)
		clear(window.events[len(kept):])
		window.events = kept
		if len(kept) == 0 {
			delete(s.windows, key)
		} else {
			s.windows[key] = window
		}
	}
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
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	s.closed = true
	clear(s.windows)
	s.events = 0
	return nil
}
