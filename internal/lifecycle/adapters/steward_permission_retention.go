package adapters

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type StewardPermissionRetention struct{ database *sql.DB }

func NewStewardPermissionRetention(database *sql.DB) *StewardPermissionRetention {
	return &StewardPermissionRetention{database: database}
}

func (a *StewardPermissionRetention) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if a == nil || a.database == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	if err := validateAdminObjectWorker(ctx, now, limit, deadline); err != nil {
		return lifecycle.WorkResult{}, err
	}
	const retention = 90 * 24 * 60 * 60
	if now < retention {
		return lifecycle.WorkResult{}, nil
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := a.database.BeginTx(work, nil)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(work, `DELETE FROM steward_permission_audits WHERE id IN (SELECT id FROM steward_permission_audits WHERE created_at<=? ORDER BY created_at,id LIMIT ?)`, now-retention, limit)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	var more bool
	if err := tx.QueryRowContext(work, `SELECT EXISTS(SELECT 1 FROM steward_permission_audits WHERE created_at<=?)`, now-retention).Scan(&more); err != nil {
		return lifecycle.WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: int(count), More: more}, nil
}
