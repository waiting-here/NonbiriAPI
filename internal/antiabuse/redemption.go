package antiabuse

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

// RedemptionAttempt contains only a classified outcome. GraceCampaign is a
// stable campaign identifier, never a code or a digest that exposes its text.
type RedemptionAttempt struct {
	Replay        bool
	Violation     bool
	GraceCampaign string
	Complete      func(context.Context, *sql.Tx, int64) error
}

// RedemptionMutation authenticates and classifies a submission in this
// transaction. It may run twice when a ban needs the retirement barrier, so it
// must not publish memory state or perform effects outside SQLite.
type RedemptionMutation func(context.Context, *sql.Tx, int64, int64) (RedemptionAttempt, error)

// Redeem shares the durable window and ban rail with other abuse rules. Normal
// submissions never retire the account or cancel its other in-flight work.
func (s *Service) Redeem(parent context.Context, user int64, mutate RedemptionMutation) error {
	if s == nil || parent == nil || user <= 0 || mutate == nil {
		return charityrouting.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var retirement Retirement
	defer func() {
		if retirement != nil {
			retirement.Abort()
		}
	}()
	for {
		select {
		case s.gate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		needsRetirement, banned, err := s.redemptionTx(ctx, user, mutate, retirement != nil)
		<-s.gate
		if err != nil {
			return err
		}
		if needsRetirement {
			retirement, err = s.config.BeginUserRetirement(ctx, user)
			if err != nil {
				return err
			}
			if retirement == nil {
				return charityrouting.ErrUnavailable
			}
			continue
		}
		if banned {
			retirement.Commit()
			if s.config.OnBan != nil {
				s.config.OnBan(user)
			}
		}
		return nil
	}
}

func (s *Service) redemptionTx(ctx context.Context, user int64, mutate RedemptionMutation, retired bool) (bool, bool, error) {
	if s.closed {
		return false, false, charityrouting.ErrUnavailable
	}
	now := s.config.Now().Unix()
	if now < 0 || now > 253402300799 {
		return false, false, charityrouting.ErrInvariant
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	reader := &transactionConfig{ctx: ctx, tx: tx}
	cfg := readConfig(reader)
	if reader.err != nil {
		return false, false, reader.err
	}
	attempt, err := mutate(ctx, tx, now, max(idempotency.ReplayWindowSeconds, cfg.RedemptionBanWindowSeconds))
	if err != nil {
		return false, false, err
	}
	if attempt.Replay {
		return false, false, tx.Commit()
	}
	if attempt.Complete == nil {
		return false, false, charityrouting.ErrInvariant
	}
	violation := attempt.Violation
	if cfg.RedemptionBanThreshold > 0 && attempt.GraceCampaign != "" {
		grace, err := s.redemptionGraceTx(ctx, tx, user, attempt.GraceCampaign, now, cfg.RedemptionBanWindowSeconds)
		if err != nil {
			return false, false, err
		}
		violation = !grace
	}
	if !violation || cfg.RedemptionBanThreshold == 0 {
		var windows map[windowKey]violationWindow
		var count int
		if cfg.RedemptionBanThreshold == 0 {
			key := windowKey{userID: user, violation: violationRedemption}
			windows, count, err = s.cleanupCopy(ctx, tx, now, cfg, key)
			if err != nil {
				return false, false, err
			}
			window, exists := windows[key]
			imported, err := s.mergeContinuityWindow(ctx, tx, key, &window, now, cfg)
			if err != nil {
				return false, false, err
			}
			count += imported
			if imported > 0 && (!exists && len(windows) >= MaxWindowUsers || count > MaxWindowUsers*MaxEventsPerUser) {
				return false, false, charityrouting.ErrResourceLimit
			}
			if len(window.events) > 0 {
				window.banDone = false
				if err := saveWindowFlags(ctx, tx, key, window, now); err != nil {
					return false, false, err
				}
				windows[key] = window
			}
		}
		if err := attempt.Complete(ctx, tx, 0); err != nil {
			return false, false, err
		}
		if err := tx.Commit(); err != nil {
			return false, false, err
		}
		if windows != nil {
			s.windows, s.events = windows, count
		}
		return false, false, nil
	}
	key := windowKey{userID: user, violation: violationRedemption}
	windows, count, err := s.cleanupCopy(ctx, tx, now, cfg, key)
	if err != nil {
		return false, false, err
	}
	window, exists := windows[key]
	imported, err := s.mergeContinuityWindow(ctx, tx, key, &window, now, cfg)
	if err != nil {
		return false, false, err
	}
	count += imported
	if !exists && len(windows) >= MaxWindowUsers || len(window.events) >= MaxViolationThreshold || count >= MaxWindowUsers*MaxEventsPerUser {
		return false, false, charityrouting.ErrResourceLimit
	}
	if cfg.RedemptionBanWindowSeconds > 253402300799-now {
		return false, false, charityrouting.ErrInvariant
	}
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		return false, false, err
	}
	window.events = append(append([]int64(nil), window.events...), now)
	window.facts = append(append([]windowEvent(nil), window.facts...), windowEvent{At: now, Expires: now + cfg.RedemptionBanWindowSeconds, RequestID: id})
	activeCount := countEvents(window.events, now, cfg.RedemptionBanWindowSeconds)
	if activeCount < cfg.RedemptionBanThreshold {
		window.banDone = false
	}
	banned := activeCount >= cfg.RedemptionBanThreshold && !window.banDone
	if banned && !retired {
		// Roll back the provisional submission before taking the outer barrier.
		return true, false, nil
	}
	var bannedUntil int64
	var finalize func(bool)
	committed := false
	if banned {
		var revision []byte
		if err := tx.QueryRowContext(ctx, "SELECT revision FROM users WHERE id=? AND is_admin=0", user).Scan(&revision); err != nil {
			return false, false, err
		}
		if s.config.CancelUserDuelsTx != nil {
			finalize, err = s.config.CancelUserDuelsTx(ctx, tx, user, "account_unavailable", now)
			if err != nil {
				return false, false, err
			}
			if finalize == nil {
				return false, false, charityrouting.ErrInvariant
			}
			defer func() { finalize(committed) }()
		}
		if err := applyRestrictions(ctx, tx, user, now, revision, cfg.RedemptionBanDurationSeconds, 0); err != nil {
			return false, false, err
		}
		if err := expireUserTx(ctx, tx, user, now); err != nil {
			return false, false, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT banned_until FROM users WHERE id=?", user).Scan(&bannedUntil); err != nil {
			return false, false, err
		}
		facts := matchingFacts(window, now, cfg.RedemptionBanWindowSeconds)
		stats := evidenceStatistics{Rule: "redemption_window", WindowStart: now - cfg.RedemptionBanWindowSeconds, WindowEnd: now, Threshold: cfg.RedemptionBanThreshold, Count: len(facts)}
		if err := recordCase(ctx, tx, user, now, "ban", "redemption_code", id, "", bannedUntil, cfg, stats, facts); err != nil {
			return false, false, err
		}
		window.banDone = true
	}
	if err := persistWindow(ctx, tx, key, &window, now); err != nil {
		return false, false, err
	}
	if err := attempt.Complete(ctx, tx, bannedUntil); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	committed = true
	windows[key] = window
	s.windows, s.events = windows, count+1
	return false, banned, nil
}

func (s *Service) redemptionGraceTx(ctx context.Context, tx *sql.Tx, user int64, campaign string, now, duration int64) (bool, error) {
	if !db.ValidateOpaqueID(campaign, "rw_") || s.config.Continuity == nil || duration > 253402300799-now {
		return false, charityrouting.ErrInvariant
	}
	identity, err := s.config.Continuity.UserKeyTx(ctx, tx, user)
	if err != nil {
		return false, err
	}
	var at, expires int64
	err = tx.QueryRowContext(ctx, `SELECT occurred_at,expires_at FROM identity_continuity_facts WHERE identity_key=? AND kind='abuse_state' AND scope='redemption_grace' AND window_key=?`, identity[:], campaign).Scan(&at, &expires)
	if err == nil && at > now-duration && expires > now {
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_continuity_facts WHERE identity_key=? AND kind='abuse_state' AND scope='redemption_grace' AND expires_at>?`, identity[:], now).Scan(&count); err != nil {
		return false, err
	}
	if count >= MaxViolationThreshold && (errors.Is(err, sql.ErrNoRows) || expires <= now) {
		return false, charityrouting.ErrResourceLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_continuity_facts(identity_key,kind,scope,window_key,fact_json,occurred_at,expires_at)
 VALUES(?,'abuse_state','redemption_grace',?,'{}',?,?) ON CONFLICT(identity_key,kind,scope,window_key) DO UPDATE SET occurred_at=excluded.occurred_at,expires_at=excluded.expires_at`, identity[:], campaign, now, now+duration)
	return err == nil, err
}
