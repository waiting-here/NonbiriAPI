package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

// MarkResponseStarted persists the billable-response checkpoint before delivery.
// The optional status records a received HTTP 200 stream for restart recovery.
func (s *Service) MarkResponseStarted(ctx context.Context, handle Handle, status ...int) error {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) || handle.purpose == PurposeDiscovery || len(status) > 1 || len(status) == 1 && status[0] != 0 && status[0] != 200 {
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
	if err := recordResponseStartTx(ctx, tx, record.claimID, at, status...); err != nil {
		return err
	}
	return tx.Commit()
}

func recordResponseStartTx(ctx context.Context, tx *sql.Tx, claimID string, at int64, status ...int) error {
	var received any
	if len(status) == 1 && status[0] == 200 {
		received = 200
	}
	var existing int64
	var existingStatus sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT started_at,http_status FROM dispatch_response_starts WHERE claim_id=?`, claimID).Scan(&existing, &existingStatus)
	if err == nil {
		if received != nil && !existingStatus.Valid {
			_, err = tx.ExecContext(ctx, `UPDATE dispatch_response_starts SET http_status=200 WHERE claim_id=?`, claimID)
		}
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	at, err = donationquota.Start(ctx, tx, claimID, at)
	if err != nil {
		return fmt.Errorf("claim: assign recurring success: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch_response_starts(claim_id,started_at,http_status)
VALUES(?,?,?) ON CONFLICT(claim_id) DO NOTHING`, claimID, at, received)
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
