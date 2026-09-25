package riskaudit

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Erase child references first so ordinary retention shares a real row budget
// rather than hiding an unbounded cascade behind a one-row parent deletion.
func cleanupScansTx(ctx context.Context, tx *sql.Tx, now int64, limit int) (CleanupResult, error) {
	var result CleanupResult
	if limit < 1 {
		return result, nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM risk_client_scans WHERE expires_at<=? OR reason='permission_changed' ORDER BY expires_at,id LIMIT 1`, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM risk_client_scan_matches WHERE scan_id=? AND request_log_id IN (SELECT request_log_id FROM risk_client_scan_matches WHERE scan_id=? LIMIT ?)`, id, id, limit)
	if err != nil {
		return result, err
	}
	n, err := deleted.RowsAffected()
	if err != nil {
		return result, err
	}
	result.Deleted, result.Processed = int(n), int(n)
	if n < int64(limit) {
		if _, err = tx.ExecContext(ctx, `DELETE FROM risk_client_scans WHERE id=?`, id); err != nil {
			return result, err
		}
		result.Deleted++
		result.Processed++
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM risk_client_scans WHERE expires_at<=? OR reason='permission_changed')`, now).Scan(&result.More)
	return result, err
}

func (r *Repository) CleanupScans(ctx context.Context) (CleanupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, err
	}
	defer tx.Rollback()
	result, err := cleanupScansTx(ctx, tx, r.now().Unix(), 100)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
