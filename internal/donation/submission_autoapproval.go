package donation

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

// autoApproveSubmittedUsesTx receives only the use IDs staged by the current
// explicit submission. Rules never call it, and older pending revisions are
// never selected just because their address gained a label.
func (s *Service) autoApproveSubmittedUsesTx(ctx context.Context, tx *sql.Tx, donationID int64, ids []int64, now int64) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var qualified bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_keys k WHERE k.donation_id=? AND k.ended_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>?))
 AND NOT EXISTS(SELECT 1 FROM donation_keys k WHERE k.donation_id=? AND k.ended_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>?)
 AND (k.mainstream_channel_id IS NULL AND NOT EXISTS(SELECT 1 FROM effective_endpoint_tags t WHERE t.base_url=k.canonical_base_url AND t.tag='mainstream')
	OR EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.endpoint_key_id)
	OR EXISTS(SELECT 1 FROM donation_key_review_requirements req WHERE req.hmac=k.key_body_review_hmac AND req.required=1)
	OR EXISTS(SELECT 1 FROM donation_uses u JOIN donation_use_revisions ur ON ur.id IN (u.current_pending_revision_id,u.current_approved_revision_id)
	 JOIN donation_key_review_requirements req ON req.hmac=ur.key_body_review_hmac AND req.required=1 WHERE u.donation_key_id=k.id)))`, donationID, now, donationID, now).Scan(&qualified)
	if err != nil || !qualified {
		return false, err
	}
	args := []any{donationID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.revision,p.id,oldg.credential_version_id,COALESCE(req.required,0)
 FROM donation_uses u JOIN donation_keys k ON k.id=u.donation_key_id
 JOIN donation_use_revisions p ON p.id=u.current_pending_revision_id AND p.use_id=u.id AND p.review_state='pending'
 LEFT JOIN donation_key_review_requirements req ON req.hmac=p.key_body_review_hmac
 LEFT JOIN donation_use_revisions old ON old.id=u.current_approved_revision_id
 LEFT JOIN endpoint_key_grant_revisions oldg ON oldg.id=old.grant_revision_id
 WHERE k.donation_id=? AND u.id IN (`+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return false, err
	}
	type staged struct {
		use, revision, pending int64
		previous               sql.NullInt64
	}
	items := []staged{}
	blocked := false
	for rows.Next() {
		var item staged
		var required bool
		if err := rows.Scan(&item.use, &item.revision, &item.pending, &item.previous, &required); err != nil {
			rows.Close()
			return false, err
		}
		blocked = blocked || required
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(items) != len(ids) {
		return false, ErrConflict
	}
	if blocked {
		return false, nil
	}
	for _, item := range items {
		if err := s.reviewMaterial.ApproveUseRevision(ctx, tx, item.pending, nil, time.Unix(now, 0)); err != nil {
			return false, mapReviewError(err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE donation_use_revisions SET review_state='approved',reviewed_at=? WHERE id=? AND review_state='pending'`, now, item.pending)
		if err != nil {
			return false, err
		}
		if err := requireOne(result); err != nil {
			return false, err
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET current_approved_revision_id=?,current_pending_revision_id=NULL,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_pending_revision_id=?`, item.pending, now, item.use, item.revision, item.pending)
		if err != nil {
			return false, err
		}
		if err := requireOne(result); err != nil {
			return false, err
		}
		if item.previous.Valid {
			if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, item.previous.Int64, now); err != nil {
				return false, err
			}
		}
	}
	// Keep budgets, pause controls, bindings and old immutable review rows. A
	// receipt describes this new automatic decision at the submission revision.
	if _, err := tx.ExecContext(ctx, `UPDATE donations SET status='approved',first_approval_origin=CASE WHEN first_approval_origin='unknown' THEN 'auto' ELSE first_approval_origin END,
 review_note='',reviewed_by_user_id=NULL,reviewed_by_role='',reviewed_at=?,updated_at=? WHERE id=? AND status IN ('pending','approved')`, now, now, donationID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
 SELECT id,revision,NULL,'','approve','',? FROM donations WHERE id=?`, now, donationID); err != nil {
		return false, err
	}
	return true, nil
}
