package claim

import (
	"context"
	"database/sql"
	"fmt"
)

// MarkResponseStarted records that a charity connector has validated its
// first successful response payload. It precedes delivery so recovery never
// has to infer successful output from a dispatch alone.
func (s *Service) MarkResponseStarted(ctx context.Context, handle Handle) error {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) || handle.purpose != PurposeCharity {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("claim: begin response checkpoint: %w", err)
	}
	defer tx.Rollback()
	record, err := loadClaimTx(ctx, tx, handle.claimID)
	if err != nil {
		return err
	}
	if err := verifyHandle(record, handle); err != nil {
		return err
	}
	if record.state != StateDispatched {
		return ErrNotDispatched
	}
	at, err := s.nowUnix()
	if err != nil {
		return err
	}
	if at < record.dispatchedAt.Int64 {
		at = record.dispatchedAt.Int64
	}
	if err := recordResponseStartTx(ctx, tx, record.claimID, at); err != nil {
		return err
	}
	return tx.Commit()
}

func recordResponseStartTx(ctx context.Context, tx *sql.Tx, claimID string, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO dispatch_response_starts(claim_id,started_at)
VALUES(?,?) ON CONFLICT(claim_id) DO NOTHING`, claimID, at)
	if err != nil {
		return fmt.Errorf("claim: record successful response: %w", err)
	}
	return nil
}

func responseStartedTx(ctx context.Context, tx *sql.Tx, claimID string) (bool, error) {
	var started bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_response_starts WHERE claim_id=?)`, claimID).Scan(&started)
	return started, err
}
