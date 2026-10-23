package donation

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Pending and approved are independent pointers: one use can count in both.
// ReviewStatus is a display summary and never grants or ends authorization.
type UseSummary struct {
	Total        string `json:"total"`
	Pending      string `json:"pending"`
	Approved     string `json:"approved"`
	Rejected     string `json:"rejected"`
	Withdrawn    string `json:"withdrawn"`
	ReviewStatus string `json:"review_status"`
}

const useSummaryFromSQL = ` FROM donation_keys summary_key
JOIN donation_uses summary_use ON summary_use.donation_key_id=summary_key.id
LEFT JOIN donation_use_revisions summary_latest ON summary_latest.id=(
 SELECT latest.id FROM donation_use_revisions latest WHERE latest.use_id=summary_use.id
 ORDER BY latest.use_revision DESC LIMIT 1)`

const useReviewStatusSQL = `CASE
 WHEN COUNT(*)=0 THEN 'none'
 WHEN COUNT(summary_use.current_pending_revision_id)>0 AND COUNT(summary_use.current_approved_revision_id)>0 THEN 'approved_pending'
 WHEN COUNT(summary_use.current_pending_revision_id)>0 THEN 'pending'
 WHEN COUNT(summary_use.current_approved_revision_id)=COUNT(*) THEN 'approved'
 WHEN COUNT(summary_use.current_approved_revision_id)>0 THEN 'partially_approved'
 WHEN SUM(summary_latest.review_state='rejected')=COUNT(*) THEN 'rejected'
 WHEN SUM(summary_latest.review_state='cancelled')=COUNT(*) THEN 'withdrawn'
 ELSE 'mixed' END`

func validReviewStatusFilter(status string) bool {
	switch status {
	case "", "pending", "approved_pending", "approved", "partially_approved", "rejected", "withdrawn", "mixed", "deleted", "expired":
		return true
	default:
		return false
	}
}

// Terminal lifecycle state takes precedence over the use review summary.
func displayedDonationStatusSQL() string {
	return `CASE WHEN d.status NOT IN ('pending','approved') OR d.terminal_at IS NOT NULL THEN d.status
 WHEN NOT ` + logicallyActiveDonationSQL + ` THEN 'expired'
 ELSE COALESCE(NULLIF((SELECT ` + useReviewStatusSQL + useSummaryFromSQL + ` WHERE summary_key.donation_id=d.id),'none'),d.status) END`
}

func readUseSummariesTx(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]UseSummary, error) {
	out := make(map[int64]UseSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, ErrInvalidRequest
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
		out[id] = UseSummary{Total: "0", Pending: "0", Approved: "0", Rejected: "0", Withdrawn: "0", ReviewStatus: "none"}
	}
	rows, err := tx.QueryContext(ctx, `SELECT summary_key.donation_id,COUNT(*),
COUNT(summary_use.current_pending_revision_id),COUNT(summary_use.current_approved_revision_id),
COALESCE(SUM(summary_latest.review_state='rejected'),0),COALESCE(SUM(summary_latest.review_state='cancelled'),0),`+
		useReviewStatusSQL+useSummaryFromSQL+` WHERE summary_key.donation_id IN (`+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+`) GROUP BY summary_key.donation_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, total, pending, approved, rejected, withdrawn int64
		var status string
		if err := rows.Scan(&id, &total, &pending, &approved, &rejected, &withdrawn, &status); err != nil {
			return nil, err
		}
		out[id] = UseSummary{Total: strconv.FormatInt(total, 10), Pending: strconv.FormatInt(pending, 10), Approved: strconv.FormatInt(approved, 10), Rejected: strconv.FormatInt(rejected, 10), Withdrawn: strconv.FormatInt(withdrawn, 10), ReviewStatus: status}
	}
	return out, rows.Err()
}
