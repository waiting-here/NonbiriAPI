package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const lifecycleCollectionLimit = 10_000

var ErrResourceLimit = errors.New("issues: resource limit exceeded")

// LifecycleIssue is the closed issue projection consumed by account export.
// Resource references, root-cause internals, generations, and retention
// bookkeeping are intentionally absent.
type LifecycleIssue struct {
	ID           string
	State        string
	Source       string
	ResourceKind string
	SummaryCode  string
	SafeDetail   string
	DeepLink     *LifecycleIssueDeepLink
	FirstSeenAt  int64
	LastSeenAt   int64
	Count        string
	ClosedAt     *int64
}

type LifecycleIssueDeepLink struct {
	RouteID    string
	ResourceID *string
}

// ExportLifecycleIssues returns current issues plus only closed rows whose
// ordinary 90-day window remains open at decisionNow. Active legal holds do
// not widen this projection.
func (adapter *SourceAdapter) ExportLifecycleIssues(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) ([]LifecycleIssue, error) {
	empty := []LifecycleIssue{}
	if adapter == nil || adapter.repository == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > lifecycleCollectionLimit {
		return empty, ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return empty, fmt.Errorf("issues: read lifecycle export owner: %w", err)
	}
	if exists != 1 {
		return empty, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+issueSelectColumns+`
FROM user_issues i
WHERE i.user_id=?
  AND (i.state='current' OR (i.state='closed' AND i.retain_until>?))
  AND (i.state<>'current' OR i.resource_kind<>'endpoint_key' OR NOT EXISTS(
    SELECT 1 FROM endpoint_key_suspensions s
    WHERE CAST(s.endpoint_key_id AS TEXT)=i.resource_ref AND s.reason_type='report_case'
  ))
ORDER BY i.last_seen_at DESC,i.id ASC
LIMIT ?`, userID, decisionNow, limit+1)
	if err != nil {
		return empty, fmt.Errorf("issues: list lifecycle export projection: %w", err)
	}
	defer rows.Close()
	items := make([]LifecycleIssue, 0, limit+1)
	for rows.Next() {
		raw, err := scanIssue(rows)
		if err != nil {
			return empty, fmt.Errorf("issues: scan lifecycle export projection: %w", err)
		}
		issue, err := issueDTO(raw)
		if err != nil {
			return empty, err
		}
		item := LifecycleIssue{
			ID: issue.ID, State: issue.State, Source: string(issue.Source),
			ResourceKind: string(issue.ResourceKind), SummaryCode: issue.SummaryCode,
			SafeDetail: issue.SafeDetail, FirstSeenAt: issue.FirstSeenAt,
			LastSeenAt: issue.LastSeenAt, Count: issue.Count, ClosedAt: issue.ClosedAt,
		}
		if issue.DeepLink != nil {
			item.DeepLink = &LifecycleIssueDeepLink{
				RouteID: issue.DeepLink.RouteID, ResourceID: issue.DeepLink.ResourceID,
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return empty, fmt.Errorf("issues: iterate lifecycle export projection: %w", err)
	}
	if len(items) > limit {
		return empty, ErrResourceLimit
	}
	return items, nil
}
