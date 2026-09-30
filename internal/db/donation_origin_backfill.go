package db

import (
	"context"
	"database/sql"
)

// This runs once while upgrading the recognized legacy schema. Reviewer roles
// survive account deletion; a missing reviewer alone cannot prove automation.
func backfillDonationApprovalOrigins(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `WITH first_approval AS (
 SELECT donation_id,MIN(submission_revision) AS revision FROM donation_reviews
 WHERE action='approve' GROUP BY donation_id
), evidence AS (
 SELECT r.* FROM donation_reviews r JOIN first_approval f
 ON f.donation_id=r.donation_id AND f.revision=r.submission_revision
 WHERE r.action='approve' GROUP BY r.donation_id HAVING COUNT(*)=1
)
UPDATE donations AS d SET first_approval_origin=(
 SELECT CASE
  WHEN e.reviewer_role IN ('admin','level5','level6','trainee5') THEN 'manual'
  WHEN e.submission_revision=1 AND e.reviewer_user_id IS NULL
   AND e.reviewer_role='' AND e.note='' AND e.created_at=d.created_at
   AND (SELECT COUNT(*)>0 AND COUNT(mainstream_channel_id)=COUNT(*)
         AND COUNT(DISTINCT mainstream_channel_id)=1
        FROM donation_keys WHERE donation_id=d.id) THEN 'auto'
  ELSE 'unknown' END FROM evidence e WHERE e.donation_id=d.id
) WHERE d.id IN (SELECT donation_id FROM evidence)`)
	return err
}
