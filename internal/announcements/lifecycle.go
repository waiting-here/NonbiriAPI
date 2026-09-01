package announcements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RecoverBeforeListener is a bounded startup hook. The composition root calls
// it repeatedly until all returned counts are below batch; no goroutine is
// started by this package.
func (service *Service) RecoverBeforeListener(ctx context.Context, batch int) (RecoveryResult, error) {
	if service == nil || service.repository == nil || batch < 1 || batch > 100 {
		return RecoveryResult{}, ErrInvalidRequest
	}
	expired, err := service.repository.ExpireDue(ctx, batch)
	if err != nil {
		return RecoveryResult{}, err
	}
	deidentified, deleted, err := service.repository.RetainAudits(ctx, batch)
	if err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{Expired: expired, ActorsDeidentified: deidentified, AuditsDeleted: deleted}, nil
}

func (repository *Repository) ExpireDue(ctx context.Context, limit int) (int, error) {
	if repository == nil || limit < 1 || limit > 100 {
		return 0, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return 0, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return 0, err
	}
	committed := false
	defer finishTx(tx, &committed)
	expired, err := expireDueTx(ctx, tx, now, limit)
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return expired, nil
}

func expireDueTx(ctx context.Context, tx *sql.Tx, now int64, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id FROM announcements
WHERE state='published' AND expires_at IS NOT NULL AND expires_at<=?
ORDER BY expires_at,id LIMIT ?`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("announcements: select due expiry: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("announcements: scan due expiry: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("announcements: close due expiry rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("announcements: select due expiry: %w", err)
	}
	expired := 0
	for _, id := range ids {
		changed, err := expireOneTx(ctx, tx, id, now)
		if err != nil {
			return 0, err
		}
		if changed {
			expired++
		}
	}
	return expired, nil
}

func expireOneTx(ctx context.Context, tx *sql.Tx, id string, now int64) (bool, error) {
	row, err := scanAnnouncement(tx.QueryRowContext(ctx, `SELECT `+announcementSelectColumns+`
FROM announcements WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("announcements: read expiry candidate: %w", err)
	}
	if row.state != "published" || !row.expiresAt.Valid || row.expiresAt.Int64 > now {
		return false, nil
	}
	next, err := nextRevision(row.revision)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE announcements SET state='expired',revision=?,updated_at=?
WHERE id=? AND revision=? AND state='published' AND expires_at IS NOT NULL AND expires_at<=?`,
		next, now, id, row.revision, now)
	if err != nil {
		return false, fmt.Errorf("announcements: materialize expiry: %w", err)
	}
	if !exactlyOne(result) {
		return false, nil
	}
	if err := appendAudit(ctx, tx, id, 0, "expire", row.revision, next, "", now); err != nil {
		return false, err
	}
	return true, nil
}

func (repository *Repository) RetainAudits(ctx context.Context, limit int) (int, int, error) {
	if repository == nil || limit < 1 || limit > 100 {
		return 0, 0, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return 0, 0, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return 0, 0, err
	}
	committed := false
	defer finishTx(tx, &committed)
	deidentified, err := deidentifyAuditActorsTx(ctx, tx, now, limit)
	if err != nil {
		return 0, 0, err
	}
	deleted, err := deleteRetainedAuditsTx(ctx, tx, now, limit)
	if err != nil {
		return 0, 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, 0, err
	}
	return deidentified, deleted, nil
}

func deidentifyAuditActorsTx(ctx context.Context, tx *sql.Tx, now int64, limit int) (int, error) {
	deidentify, err := tx.ExecContext(ctx, `
UPDATE announcement_audits SET actor_user_id=NULL
WHERE id IN (
 SELECT id FROM announcement_audits
 WHERE actor_user_id IS NOT NULL AND actor_deidentify_at<=?
 ORDER BY actor_deidentify_at,id LIMIT ?
)`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("announcements: deidentify audit actors: %w", err)
	}
	deidentified64, err := deidentify.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("announcements: count deidentified audit actors: %w", err)
	}
	if deidentified64 < 0 || deidentified64 > int64(limit) {
		return 0, ErrUnavailable
	}
	return int(deidentified64), nil
}

func deleteRetainedAuditsTx(ctx context.Context, tx *sql.Tx, now int64, limit int) (int, error) {
	deletedResult, err := tx.ExecContext(ctx, `
DELETE FROM announcement_audits WHERE id IN (
 SELECT a.id FROM announcement_audits a
 WHERE a.created_at<=?
   AND NOT EXISTS(
    SELECT 1 FROM legal_holds h
    WHERE h.object_kind='announcement_audit' AND h.object_ref=CAST(a.id AS TEXT) AND h.state='active'
   )
 ORDER BY a.created_at,a.id LIMIT ?
)`, now-announcementAuditTTL, limit)
	if err != nil {
		return 0, fmt.Errorf("announcements: retain audit facts: %w", err)
	}
	deleted64, err := deletedResult.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("announcements: count retained audit facts: %w", err)
	}
	if deleted64 < 0 || deleted64 > int64(limit) {
		return 0, ErrUnavailable
	}
	return int(deleted64), nil
}

type LifecycleAuditRetentionResult struct {
	ActorsDeidentified int
	AuditsDeleted      int
	Processed          int
	More               bool
}

// RetainLifecycleAudits performs one total-budgeted lifecycle step. Actor
// identity is always removed before an audit becomes eligible for deletion;
// a following bounded pass handles deletion without counting one row twice.
func (repository *Repository) RetainLifecycleAudits(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleAuditRetentionResult, error) {
	if repository == nil || repository.db == nil || ctx == nil || ctx.Err() != nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 || budgetDeadline.IsZero() {
		return LifecycleAuditRetentionResult{}, ErrInvalidRequest
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	tx, err := beginTx(workerCtx, repository.db)
	if err != nil {
		return LifecycleAuditRetentionResult{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	deidentified, err := deidentifyAuditActorsTx(workerCtx, tx, decisionNow, limit)
	if err != nil {
		return LifecycleAuditRetentionResult{}, err
	}
	result := LifecycleAuditRetentionResult{ActorsDeidentified: deidentified, Processed: deidentified}
	if deidentified > 0 {
		result.More = true
	} else {
		deleted, err := deleteRetainedAuditsTx(workerCtx, tx, decisionNow, limit)
		if err != nil {
			return LifecycleAuditRetentionResult{}, err
		}
		result.AuditsDeleted = deleted
		result.Processed = deleted
		result.More = deleted == limit
	}
	if err := workerCtx.Err(); err != nil {
		return LifecycleAuditRetentionResult{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return LifecycleAuditRetentionResult{}, err
	}
	return result, nil
}

type LifecycleHeldAuditState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

func (repository *Repository) InspectLifecycleHeldAudit(
	ctx context.Context,
	tx *sql.Tx,
	auditID int64,
	decisionNow int64,
) (LifecycleHeldAuditState, error) {
	if repository == nil || ctx == nil || tx == nil || auditID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return LifecycleHeldAuditState{}, ErrInvalidRequest
	}
	var createdAt int64
	var marker int
	err := tx.QueryRowContext(ctx, `SELECT created_at,legal_hold_consumed
FROM announcement_audits WHERE id=?`, auditID).Scan(&createdAt, &marker)
	if errors.Is(err, sql.ErrNoRows) {
		return LifecycleHeldAuditState{}, nil
	}
	if err != nil {
		return LifecycleHeldAuditState{}, fmt.Errorf("announcements: inspect lifecycle held audit: %w", err)
	}
	if createdAt < 0 || createdAt > maxUnixSecond-announcementAuditTTL || (marker != 0 && marker != 1) {
		return LifecycleHeldAuditState{}, ErrUnavailable
	}
	return LifecycleHeldAuditState{
		Exists: true, OrdinaryDeadline: createdAt + announcementAuditTTL,
		LegalHoldConsumed: marker == 1,
	}, nil
}

func (repository *Repository) ConsumeLifecycleHeldAuditMarker(
	ctx context.Context,
	tx *sql.Tx,
	auditID int64,
) error {
	if repository == nil || ctx == nil || tx == nil || auditID <= 0 {
		return ErrInvalidRequest
	}
	result, err := tx.ExecContext(ctx, `UPDATE announcement_audits SET legal_hold_consumed=1
WHERE id=? AND legal_hold_consumed=0`, auditID)
	if err != nil {
		return fmt.Errorf("announcements: consume lifecycle held audit marker: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("announcements: count lifecycle held audit marker: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ReadLifecycleHeldAudit confirms only the content-free audit row. It never
// joins or projects the announcement body or draft.
func (repository *Repository) ReadLifecycleHeldAudit(
	ctx context.Context,
	tx *sql.Tx,
	auditID int64,
	decisionNow int64,
) (bool, error) {
	state, err := repository.InspectLifecycleHeldAudit(ctx, tx, auditID, decisionNow)
	return state.Exists, err
}
