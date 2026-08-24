package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	sqlite "modernc.org/sqlite"
)

type SettleFishingInput struct {
	UserID  int64
	RoundID string
}

type DueGameSettlement struct {
	SettlementID string
	NextAttempt  time.Time
}

type GameRetryErrorClass string

const (
	GameRetryBusy      GameRetryErrorClass = "busy"
	GameRetryTransient GameRetryErrorClass = "transient"
	GameRetryInternal  GameRetryErrorClass = "internal"
)

// SettleFishingRound performs the owner-scoped terminal transition. A round
// owned by another account is indistinguishable from a missing round.
func (s *GameSettlementService) SettleFishingRound(ctx context.Context, input SettleFishingInput) (FishingRound, error) {
	if s == nil || s.store == nil || ctx == nil || input.UserID <= 0 || !ValidGameSettlementID(input.RoundID) {
		return FishingRound{}, ErrNotFound
	}
	return s.transitionFishing(ctx, gameTransitionSelector{userID: input.UserID, roundID: input.RoundID}, GameCommitted)
}

// SettleDueGame performs the same transition using an internal settlement
// identity. It intentionally bypasses active-account and feature switches so
// a later ban or configuration change cannot confiscate a paid round.
func (s *GameSettlementService) SettleDueGame(ctx context.Context, settlementID string) (FishingRound, error) {
	if s == nil || s.store == nil || ctx == nil || !ValidGameSettlementID(settlementID) {
		return FishingRound{}, ErrNotFound
	}
	return s.transitionFishing(ctx, gameTransitionSelector{settlementID: settlementID}, GameCommitted)
}

// ReleaseGame is an internal terminal capability. No user-facing fishing
// endpoint is allowed to call it; normal failure and timeout remain reserved.
func (s *GameSettlementService) ReleaseGame(ctx context.Context, settlementID string) (FishingRound, error) {
	if s == nil || s.store == nil || ctx == nil || !ValidGameSettlementID(settlementID) {
		return FishingRound{}, ErrNotFound
	}
	return s.transitionFishing(ctx, gameTransitionSelector{settlementID: settlementID}, GameReleased)
}

type gameTransitionSelector struct {
	userID       int64
	roundID      string
	settlementID string
}

type gameTransitionRow struct {
	FishingRound
	userID         int64
	gameType       string
	gameVersion    int
	lastErrorClass string
	finalizedAt    sql.NullInt64
	nextAttemptAt  sql.NullInt64
	attemptCount   int64
	lastAttemptAt  sql.NullInt64
	outcome        fishing.Outcome
}

func (s *GameSettlementService) transitionFishing(ctx context.Context, selector gameTransitionSelector, target GameSettlementState) (FishingRound, error) {
	if target != GameCommitted && target != GameReleased {
		return FishingRound{}, ErrGameInternal
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return FishingRound{}, fmt.Errorf("game transition begin: %w", err)
	}
	defer tx.Rollback()
	row, err := readGameTransitionRow(ctx, tx, selector)
	if err != nil {
		return FishingRound{}, err
	}
	if row.gameType != game.FishingID || row.gameVersion != game.FishingVersion ||
		row.EventSequence < 1 || !validFishingBait(row.Bait) || row.AutoSettleAt.Before(row.CreatedAt) {
		return FishingRound{}, ErrGameInternal
	}
	if row.State == GameCommitted || row.State == GameReleased {
		if row.EventSequence < 2 || row.SettledAt == nil || !row.finalizedAt.Valid ||
			row.SettledAt.Unix() != row.finalizedAt.Int64 || row.nextAttemptAt.Valid ||
			row.attemptCount != 0 || row.lastAttemptAt.Valid || row.lastErrorClass != "" {
			return FishingRound{}, ErrGameInternal
		}
		result := row.FishingRound
		result.Replayed = true
		if row.State == GameCommitted {
			outcome := row.outcome
			result.Outcome = &outcome
		} else {
			// A release refunds the reserved entry and deliberately reveals no
			// server outcome or hypothetical payout.
			result.PayoutMilli = 0
			result.Outcome = nil
		}
		return result, nil
	}
	if row.State != GameReserved || row.SettledAt != nil || row.finalizedAt.Valid || !row.nextAttemptAt.Valid || row.EventSequence != 1 {
		return FishingRound{}, ErrGameInternal
	}

	finalizedUnix := s.now().Unix()
	if finalizedUnix < row.CreatedAt.Unix() {
		finalizedUnix = row.CreatedAt.Unix()
	}
	finalized := time.Unix(finalizedUnix, 0)
	operation := CreditOperation{
		UserID: row.userID, GameSettlementID: row.SettlementID,
		CreatedAt: finalized,
	}
	if target == GameCommitted {
		operation.Kind = LedgerGameSettlement
		operation.OperationID = gameLedgerOperationID(row.SettlementID, "settle")
		operation.CreditsDelta = row.PayoutMilli
	} else {
		operation.Kind = LedgerGameRelease
		operation.OperationID = gameLedgerOperationID(row.SettlementID, "release")
		operation.CreditsDelta = row.EntryMilli
	}
	if err := operation.validate(); err != nil {
		return FishingRound{}, ErrGameInternal
	}
	ledger, err := s.store.applyCreditOperationTx(ctx, tx, operation)
	if err != nil {
		return FishingRound{}, err
	}
	if !ledger.Applied {
		return FishingRound{}, ErrGameLedgerCollision
	}
	update, err := tx.ExecContext(ctx, `
UPDATE game_settlements
SET state=?,next_attempt_at=NULL,attempt_count=0,last_attempt_at=NULL,last_error_class='',finalized_at=?,updated_at=?
WHERE id=? AND state='reserved' AND finalized_at IS NULL AND next_attempt_at IS NOT NULL`,
		string(target), finalizedUnix, finalizedUnix, row.SettlementID)
	if err != nil {
		return FishingRound{}, fmt.Errorf("game transition settlement: %w", err)
	}
	if affected, rowsErr := update.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return FishingRound{}, fmt.Errorf("game transition settlement: %w", rowsErr)
		}
		return FishingRound{}, ErrGameInternal
	}
	update, err = tx.ExecContext(ctx, `
UPDATE game_rounds SET settled_at=?,event_seq=event_seq+1,updated_at=?
WHERE id=? AND settlement_id=? AND settled_at IS NULL AND event_seq=1`,
		finalizedUnix, finalizedUnix, row.RoundID, row.SettlementID)
	if err != nil {
		return FishingRound{}, fmt.Errorf("game transition round: %w", err)
	}
	if affected, rowsErr := update.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return FishingRound{}, fmt.Errorf("game transition round: %w", rowsErr)
		}
		return FishingRound{}, ErrGameInternal
	}
	if target == GameCommitted && isFishTier(row.outcome.Tier) {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(user_id) DO UPDATE SET
 round_id=excluded.round_id,species_key=excluded.species_key,tier=excluded.tier,
 size_cm=excluded.size_cm,caught_at=excluded.caught_at
	WHERE excluded.size_cm > game_fishing_best.size_cm`, row.userID, row.RoundID,
			row.outcome.Key, string(row.outcome.Tier), row.outcome.SizeCentimetre, row.CreatedAt.Unix()); err != nil {
			return FishingRound{}, fmt.Errorf("game transition best: %w", err)
		}
	}
	if err := s.commitTx(tx); err != nil {
		return FishingRound{}, fmt.Errorf("game transition commit: %w", err)
	}
	result := row.FishingRound
	result.State = target
	result.BalanceMilli = ledger.CreditsAfter
	result.SettledAt = &finalized
	result.EventSequence++
	if target == GameCommitted {
		outcome := row.outcome
		result.Outcome = &outcome
	} else {
		result.PayoutMilli = 0
		result.Outcome = nil
	}
	return result, nil
}

func readGameTransitionRow(ctx context.Context, tx *sql.Tx, selector gameTransitionSelector) (gameTransitionRow, error) {
	query := `
SELECT r.id,r.settlement_id,r.user_id,r.game_type,r.game_version,r.event_seq,r.created_at,r.settled_at,
       s.state,s.entry_milli,s.payout_milli,s.auto_settle_at,s.next_attempt_at,s.attempt_count,s.last_attempt_at,
       s.last_error_class,s.finalized_at,u.credits,o.bait,o.species_key,o.tier,o.size_cm
FROM game_rounds r
JOIN game_settlements s ON s.id=r.settlement_id
JOIN game_fishing_outcomes o ON o.round_id=r.id
JOIN users u ON u.id=r.user_id
WHERE `
	var args []any
	if selector.userID > 0 {
		query += `r.user_id=? AND r.id=?`
		args = []any{selector.userID, selector.roundID}
	} else {
		query += `s.id=?`
		args = []any{selector.settlementID}
	}
	var row gameTransitionRow
	var state, bait, tier string
	var createdAt, autoSettleAt int64
	var settledAt sql.NullInt64
	err := tx.QueryRowContext(ctx, query, args...).Scan(
		&row.RoundID, &row.SettlementID, &row.userID, &row.gameType, &row.gameVersion,
		&row.EventSequence, &createdAt, &settledAt, &state, &row.EntryMilli, &row.PayoutMilli,
		&autoSettleAt, &row.nextAttemptAt, &row.attemptCount, &row.lastAttemptAt, &row.lastErrorClass,
		&row.finalizedAt, &row.BalanceMilli, &bait, &row.outcome.Key, &tier, &row.outcome.SizeCentimetre)
	if errors.Is(err, sql.ErrNoRows) {
		return gameTransitionRow{}, ErrNotFound
	}
	if err != nil {
		return gameTransitionRow{}, fmt.Errorf("game transition read: %w", err)
	}
	if !validGameState(state) {
		return gameTransitionRow{}, ErrGameInternal
	}
	row.State = GameSettlementState(state)
	row.CreatedAt = time.Unix(createdAt, 0)
	row.AutoSettleAt = time.Unix(autoSettleAt, 0)
	if settledAt.Valid {
		settled := time.Unix(settledAt.Int64, 0)
		row.SettledAt = &settled
	}
	row.outcome.Bait = fishing.Bait(bait)
	row.Bait = row.outcome.Bait
	row.outcome.Tier = fishing.Tier(tier)
	return row, nil
}

func isFishTier(tier fishing.Tier) bool {
	switch tier {
	case fishing.TierSmall, fishing.TierRegular, fishing.TierBig, fishing.TierGiant, fishing.TierLegend:
		return true
	default:
		return false
	}
}

// ListDueGameSettlements returns a stable, bounded batch. It does not mutate
// retry metadata and does not expose outcome or user identity.
func (s *GameSettlementService) ListDueGameSettlements(ctx context.Context, now time.Time, limit int) ([]DueGameSettlement, error) {
	if s == nil || s.store == nil || ctx == nil || now.IsZero() || limit < 1 || limit > GameRecoveryBatchMax {
		return nil, ErrGameInvalid
	}
	rows, err := s.store.db.QueryContext(ctx, `
SELECT id,next_attempt_at FROM game_settlements
WHERE state='reserved' AND next_attempt_at<=?
ORDER BY next_attempt_at,id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("game due list: %w", err)
	}
	defer rows.Close()
	out := make([]DueGameSettlement, 0, limit)
	for rows.Next() {
		var item DueGameSettlement
		var next int64
		if err := rows.Scan(&item.SettlementID, &next); err != nil {
			return nil, fmt.Errorf("game due list: %w", err)
		}
		item.NextAttempt = time.Unix(next, 0)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("game due list: %w", err)
	}
	return out, nil
}

// RecordGameRetryFailure atomically advances only a still-reserved row. A
// terminal row is an idempotent no-op. Raw errors are classified by the caller
// and never persisted.
func (s *GameSettlementService) RecordGameRetryFailure(ctx context.Context, settlementID string, attemptedAt time.Time, class GameRetryErrorClass) error {
	if s == nil || s.store == nil || ctx == nil || !ValidGameSettlementID(settlementID) || attemptedAt.IsZero() || !validRetryClass(class) {
		return ErrGameInvalid
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("game retry begin: %w", err)
	}
	defer tx.Rollback()
	var state string
	var attempts int64
	err = tx.QueryRowContext(ctx, `SELECT state,attempt_count FROM game_settlements WHERE id=?`, settlementID).Scan(&state, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("game retry read: %w", err)
	}
	if state != string(GameReserved) {
		if state == string(GameCommitted) || state == string(GameReleased) {
			return nil
		}
		return ErrGameInternal
	}
	nextAttempts := attempts
	if nextAttempts < maxGameAttemptCount {
		nextAttempts++
	}
	delay := gameRetryDelay(nextAttempts)
	attemptUnix := attemptedAt.Unix()
	nextUnix := attemptUnix
	if delaySeconds := int64(delay / time.Second); attemptUnix <= math.MaxInt64-delaySeconds {
		nextUnix += delaySeconds
	} else {
		nextUnix = math.MaxInt64
	}
	update, err := tx.ExecContext(ctx, `
UPDATE game_settlements
SET attempt_count=?,last_attempt_at=?,last_error_class=?,next_attempt_at=?,updated_at=?
WHERE id=? AND state='reserved' AND attempt_count=?`, nextAttempts, attemptUnix,
		string(class), nextUnix, attemptUnix, settlementID, attempts)
	if err != nil {
		return fmt.Errorf("game retry update: %w", err)
	}
	if affected, rowsErr := update.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return fmt.Errorf("game retry update: %w", rowsErr)
		}
		return ErrGameInternal
	}
	if err := s.commitTx(tx); err != nil {
		return fmt.Errorf("game retry commit: %w", err)
	}
	return nil
}

func validRetryClass(class GameRetryErrorClass) bool {
	return class == GameRetryBusy || class == GameRetryTransient || class == GameRetryInternal
}

func gameRetryDelay(attemptCount int64) time.Duration {
	if attemptCount <= 1 {
		return GameRetryInitialDelay
	}
	// The eighth failure would exceed one hour (30*2^7 seconds), so every
	// later count is already at the frozen cap.
	if attemptCount >= 8 {
		return GameRetryMaximumDelay
	}
	return GameRetryInitialDelay * time.Duration(int64(1)<<(attemptCount-1))
}

// ClassifyGameRetryError collapses infrastructure errors to the only values
// allowed in persistent retry metadata.
func ClassifyGameRetryError(err error) GameRetryErrorClass {
	if err == nil {
		return GameRetryInternal
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		primary := sqliteErr.Code() & 0xff
		if primary == 5 || primary == 6 { // SQLITE_BUSY / SQLITE_LOCKED
			return GameRetryBusy
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sql.ErrConnDone) {
		return GameRetryTransient
	}
	return GameRetryInternal
}
