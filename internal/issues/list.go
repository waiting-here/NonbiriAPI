package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

func (service *Service) List(ctx context.Context, userID int64, query ListQuery) (Page, error) {
	empty := Page{Data: []Issue{}}
	if service == nil || service.repository == nil || userID <= 0 || !validLimit(query.Limit) ||
		(query.State != "current" && query.State != "closed") {
		return empty, ErrInvalidRequest
	}
	repository := service.repository
	now, err := repository.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var userExists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&userExists); err != nil {
		return empty, fmt.Errorf("issues: read list owner: %w", err)
	}
	if userExists != 1 {
		return empty, ErrUnauthorized
	}
	var incomplete int
	err = tx.QueryRowContext(ctx, `SELECT projection_incomplete FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, fmt.Errorf("issues: read projection state: %w", err)
	}
	if incomplete != 0 && incomplete != 1 {
		return empty, ErrUnavailable
	}
	empty.ProjectionIncomplete = incomplete == 1
	limit := normalizedLimit(query.Limit)
	args := []any{userID, query.State, now}
	cursorPredicate := ""
	if query.Cursor != "" {
		payload, err := repository.cursors.decode(query.Cursor, "issues:"+query.State, issueCursorOwner(userID, query.State), now)
		if err != nil {
			return empty, ErrInvalidRequest
		}
		cursorPredicate = " AND (last_seen_at<? OR (last_seen_at=? AND id>?))"
		args = append(args, payload.LastSeen, payload.LastSeen, payload.ID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT `+issueSelectColumns+`
FROM user_issues i
WHERE i.user_id=? AND i.state=?
	  AND (i.state<>'closed' OR i.retain_until>?)
	  AND (i.state<>'current' OR i.resource_kind<>'endpoint_key' OR NOT EXISTS(
    SELECT 1 FROM endpoint_key_suspensions s
    WHERE CAST(s.endpoint_key_id AS TEXT)=i.resource_ref AND s.reason_type='report_case'
  ))`+cursorPredicate+`
ORDER BY i.last_seen_at DESC,i.id ASC LIMIT ?`, args...)
	if err != nil {
		return empty, fmt.Errorf("issues: list projection: %w", err)
	}
	defer rows.Close()
	rawRows := make([]rawIssue, 0, limit+1)
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return empty, fmt.Errorf("issues: scan projection: %w", err)
		}
		rawRows = append(rawRows, issue)
	}
	if err := rows.Err(); err != nil {
		return empty, fmt.Errorf("issues: list projection: %w", err)
	}
	hasMore := len(rawRows) > limit
	if hasMore {
		rawRows = rawRows[:limit]
	}
	out := Page{Data: make([]Issue, 0, len(rawRows)), ProjectionIncomplete: incomplete == 1}
	for _, raw := range rawRows {
		value, err := issueDTO(raw)
		if err != nil {
			return empty, err
		}
		out.Data = append(out.Data, value)
	}
	if hasMore && len(rawRows) > 0 {
		last := rawRows[len(rawRows)-1]
		cursor, err := repository.cursors.encode(issueCursorPayload{
			Version: issueCursorVersion, Scope: "issues:" + query.State, Owner: issueCursorOwner(userID, query.State),
			Expiry: now + cursorLifetimeSeconds, LastSeen: last.lastSeen, ID: last.id,
		})
		if err != nil {
			return empty, err
		}
		out.NextCursor = &cursor
	}
	if err := tx.Commit(); err != nil {
		return empty, fmt.Errorf("issues: commit list read: %w", err)
	}
	return out, nil
}

func issueDTO(raw rawIssue) (Issue, error) {
	value := Issue{
		ID: raw.id, State: raw.state, Source: raw.source, ResourceKind: raw.resourceKind,
		SummaryCode: raw.summaryCode, SafeDetail: raw.safeDetail, FirstSeenAt: raw.firstSeen,
		LastSeenAt: raw.lastSeen, Count: strconv.FormatInt(raw.count, 10), ClosedAt: nullableInt64(raw.closedAt),
	}
	if raw.deepLinkKind.Valid {
		resourceID := raw.deepLinkRef.String
		link := &DeepLink{ResourceID: &resourceID}
		switch ResourceKind(raw.deepLinkKind.String) {
		case ResourceEndpoint, ResourceEndpointKey:
			link.RouteID = "endpoint-detail"
		case ResourceModel:
			link.RouteID = "models"
		default:
			return Issue{}, ErrUnavailable
		}
		value.DeepLink = link
	}
	return value, nil
}
