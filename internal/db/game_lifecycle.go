package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	// GameRoundRetention is measured from the authoritative terminal
	// settled_at timestamp. A reserved round is never selected by retention.
	GameRoundRetention = 30 * 24 * time.Hour
	// GameRetentionBatchSize and GameRetentionMaxBatches are the maintenance
	// bounds are fixed by the game retention contract. The sweep is resumable
	// on the next run.
	GameRetentionBatchSize  = 200
	GameRetentionMaxBatches = 100
)

// FishingState is the bounded, owner-scoped state projection used by the
// page-reload/recovery API. At most one pending and one unrevealed result are
// returned; callers use HasMoreUnrevealed and ACK to page through results.
type FishingState struct {
	Pending           *FishingRound
	Unrevealed        *FishingRound
	HasMoreUnrevealed bool
}

// GetFishingState returns the user's pending round and earliest committed,
// unacknowledged result. The user row is checked first so a deleted account
// cannot be projected by a late request.
func (s *Store) GetFishingState(ctx context.Context, userID int64) (FishingState, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 {
		return FishingState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FishingState{}, fmt.Errorf("game state begin: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&exists); err != nil {
		return FishingState{}, fmt.Errorf("game state user: %w", err)
	}
	if exists == 0 {
		return FishingState{}, ErrNotFound
	}

	state := FishingState{}
	pending, err := queryFishingStateRound(ctx, tx, userID, true, 0)
	if err != nil {
		return FishingState{}, err
	}
	state.Pending = pending

	result, err := queryFishingStateRound(ctx, tx, userID, false, 0)
	if err != nil {
		return FishingState{}, err
	}
	state.Unrevealed = result
	if result != nil {
		var more int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM game_rounds r
JOIN game_settlements s ON s.id=r.settlement_id
WHERE r.user_id=? AND r.game_type=? AND r.game_version=?
  AND s.state='committed' AND r.settled_at IS NOT NULL AND r.revealed_at IS NULL
	AND (r.settled_at > ? OR (r.settled_at = ? AND
	     (r.created_at > ? OR (r.created_at = ? AND r.id > ?))))`,
			userID, game.FishingID, game.FishingVersion,
			result.SettledAt.Unix(), result.SettledAt.Unix(), result.CreatedAt.Unix(),
			result.CreatedAt.Unix(), result.RoundID).Scan(&more); err != nil {
			return FishingState{}, fmt.Errorf("game state remaining: %w", err)
		}
		state.HasMoreUnrevealed = more > 0
	}
	if err := tx.Commit(); err != nil {
		return FishingState{}, fmt.Errorf("game state commit: %w", err)
	}
	return state, nil
}

// queryFishingStateRound selects one row for either the unique pending state
// or the earliest unrevealed committed result. The pending projection clears
// payout/outcome even though the outcome is persisted for server recovery.
func queryFishingStateRound(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64, pending bool, _ int) (*FishingRound, error) {
	where := `r.settled_at IS NULL AND s.state='reserved'`
	order := `r.created_at ASC,r.id ASC`
	if !pending {
		where = `r.settled_at IS NOT NULL AND r.revealed_at IS NULL AND s.state='committed'`
		order = `r.settled_at ASC,r.created_at ASC,r.id ASC`
	}
	row := query.QueryRowContext(ctx, `
SELECT r.id,r.settlement_id,r.event_seq,r.created_at,r.settled_at,r.revealed_at,
       s.state,s.entry_milli,s.payout_milli,s.auto_settle_at,u.credits,
       o.bait,o.species_key,o.tier,o.size_cm
FROM game_rounds r
JOIN game_settlements s ON s.id=r.settlement_id
JOIN users u ON u.id=r.user_id
JOIN game_fishing_outcomes o ON o.round_id=r.id
WHERE r.user_id=? AND r.game_type=? AND r.game_version=? AND `+where+`
ORDER BY `+order+` LIMIT 1`, userID, game.FishingID, game.FishingVersion)
	result, err := scanFishingStateRound(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if result.State == GameReserved {
		result.PayoutMilli = 0
		result.Outcome = nil
	}
	return &result, nil
}

func scanFishingStateRound(row interface{ Scan(...any) error }) (FishingRound, error) {
	var result FishingRound
	var state, bait, species, tier string
	var createdAt, autoSettleAt int64
	var settledAt, revealedAt sql.NullInt64
	var size int
	if err := row.Scan(&result.RoundID, &result.SettlementID, &result.EventSequence,
		&createdAt, &settledAt, &revealedAt, &state, &result.EntryMilli,
		&result.PayoutMilli, &autoSettleAt, &result.BalanceMilli, &bait,
		&species, &tier, &size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FishingRound{}, err
		}
		return FishingRound{}, fmt.Errorf("game state scan: %w", err)
	}
	if !validGameState(state) || !validFishingBait(fishing.Bait(bait)) {
		return FishingRound{}, ErrGameInternal
	}
	result.State = GameSettlementState(state)
	result.Bait = fishing.Bait(bait)
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	result.AutoSettleAt = time.Unix(autoSettleAt, 0).UTC()
	if result.AutoSettleAt.Before(result.CreatedAt) {
		return FishingRound{}, ErrGameInternal
	}
	if settledAt.Valid {
		value := time.Unix(settledAt.Int64, 0).UTC()
		result.SettledAt = &value
	}
	if revealedAt.Valid && result.SettledAt == nil {
		return FishingRound{}, ErrGameInternal
	}
	if result.State == GameCommitted {
		outcome := fishing.Outcome{Bait: result.Bait, Key: species, Tier: fishing.Tier(tier), SizeCentimetre: size}
		result.Outcome = &outcome
	}
	return result, nil
}

// AckFishingRound marks one committed outcome as revealed. It is deliberately
// a non-economic conditional update: concurrent/manual retries either perform
// the one event_seq increment or observe the already acknowledged row.
func (s *Store) AckFishingRound(ctx context.Context, userID int64, roundID string) error {
	return s.ackFishingRoundAt(ctx, userID, roundID, time.Now().UTC())
}

// AckFishingRoundAt is the deterministic clock seam used by lifecycle tests.
func (s *Store) AckFishingRoundAt(ctx context.Context, userID int64, roundID string, at time.Time) error {
	return s.ackFishingRoundAt(ctx, userID, roundID, at)
}

func (s *Store) ackFishingRoundAt(ctx context.Context, userID int64, roundID string, at time.Time) error {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || !ValidGameSettlementID(roundID) || at.IsZero() {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("game ack begin: %w", err)
	}
	defer tx.Rollback()
	var state string
	var settledAt, revealedAt sql.NullInt64
	var eventSeq int64
	err = tx.QueryRowContext(ctx, `
SELECT s.state,r.event_seq,r.settled_at,r.revealed_at
FROM game_rounds r JOIN game_settlements s ON s.id=r.settlement_id
WHERE r.id=? AND r.user_id=? AND r.game_type=? AND r.game_version=?`,
		roundID, userID, game.FishingID, game.FishingVersion).Scan(&state, &eventSeq, &settledAt, &revealedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("game ack read: %w", err)
	}
	if state != string(GameCommitted) || !settledAt.Valid || eventSeq < 2 {
		return ErrConflict
	}
	if revealedAt.Valid {
		return nil
	}
	revealedUnix := at.Unix()
	if revealedUnix < settledAt.Int64 {
		revealedUnix = settledAt.Int64
	}
	result, err := tx.ExecContext(ctx, `
UPDATE game_rounds SET revealed_at=?,event_seq=event_seq+1,updated_at=?
		WHERE id=? AND user_id=? AND settled_at IS NOT NULL AND revealed_at IS NULL
	  AND event_seq=2`, revealedUnix, revealedUnix, roundID, userID)
	if err != nil {
		return fmt.Errorf("game ack update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("game ack affected: %w", err)
	}
	if affected == 0 {
		// SQLite serializes writers, but a defensive reread keeps a concurrent
		// duplicate ACK idempotent if the row was acknowledged between reads.
		var replay sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT revealed_at FROM game_rounds WHERE id=? AND user_id=?`, roundID, userID).Scan(&replay); err == nil && replay.Valid {
			return nil
		}
		return ErrGameInternal
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("game ack commit: %w", err)
	}
	return nil
}

// CleanupFishingRetention uses the frozen 30-day window and maintenance
// bounds. The returned stoppedEarly flag means another eligible batch remains.
func (s *Store) CleanupFishingRetention(ctx context.Context, now time.Time) (deleted int64, stoppedEarly bool, err error) {
	if now.IsZero() {
		return 0, false, fmt.Errorf("game retention: now is invalid")
	}
	cutoff := now.Unix() - int64(GameRoundRetention/time.Second)
	return s.CleanupFishingRetentionBefore(ctx, cutoff, GameRetentionBatchSize, GameRetentionMaxBatches)
}

// CleanupFishingRetentionBefore is an explicit-bound seam for maintenance and
// tests. Rows exactly at cutoff remain retained; only settled_at < cutoff is
// eligible, and reserved rows are never selected.
func (s *Store) CleanupFishingRetentionBefore(ctx context.Context, cutoffUnix int64, batchSize, maxBatches int) (deleted int64, stoppedEarly bool, err error) {
	if s == nil || s.db == nil || ctx == nil || batchSize < 1 || maxBatches < 1 {
		return 0, false, fmt.Errorf("game retention: invalid bounds")
	}
	for batch := 0; batch < maxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return deleted, true, err
		}
		n, err := s.cleanupFishingRetentionBatch(ctx, cutoffUnix, batchSize)
		if err != nil {
			return deleted, false, err
		}
		deleted += n
		if n < int64(batchSize) {
			return deleted, false, nil
		}
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM game_rounds r JOIN game_settlements s ON s.id=r.settlement_id
WHERE r.game_type=? AND r.game_version=? AND r.settled_at IS NOT NULL AND r.settled_at < ?
  AND s.state IN ('committed','released')`, game.FishingID, game.FishingVersion, cutoffUnix).Scan(&remaining); err != nil {
		return deleted, true, fmt.Errorf("game retention remaining: %w", err)
	}
	return deleted, remaining > 0, nil
}

func (s *Store) cleanupFishingRetentionBatch(ctx context.Context, cutoffUnix int64, limit int) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("game retention begin: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT s.id
FROM game_rounds r JOIN game_settlements s ON s.id=r.settlement_id
WHERE r.game_type=? AND r.game_version=? AND r.settled_at IS NOT NULL AND r.settled_at < ?
  AND s.state IN ('committed','released')
ORDER BY r.settled_at ASC,r.id ASC LIMIT ?`, game.FishingID, game.FishingVersion, cutoffUnix, limit)
	if err != nil {
		return 0, fmt.Errorf("game retention select: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("game retention scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("game retention rows: %w", err)
	}
	rows.Close()
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("game retention empty commit: %w", err)
		}
		return 0, nil
	}
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `DELETE FROM game_settlements WHERE id=? AND state IN ('committed','released')`, id)
		if err != nil {
			return 0, fmt.Errorf("game retention delete: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return 0, fmt.Errorf("game retention affected: %w", err)
		} else if affected != 1 {
			return 0, ErrGameInternal
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("game retention commit: %w", err)
	}
	return int64(len(ids)), nil
}
