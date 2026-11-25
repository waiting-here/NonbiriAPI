package logapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

var ErrSearchExpired = errors.New("logapi: search expired")

type logSearchScope struct {
	role    string
	actorID int64
	steward StewardAuthorizer
}

func (s logSearchScope) valid() bool {
	switch s.role {
	case "user":
		return s.actorID > 0
	case "admin":
		return s.actorID >= 0
	case "steward":
		return s.actorID > 0 && s.steward != nil
	default:
		return false
	}
}

func (s logSearchScope) visibleLogs(now int64) (string, []any) {
	const retained = "(l.completed_at IS NULL OR l.completed_at>?)"
	if s.role == "user" {
		return "WHERE l.user_id=? AND " + retained, []any{s.actorID, now - requestLogRetentionSeconds}
	}
	return "WHERE " + retained, []any{now - requestLogRetentionSeconds}
}

// Count, ID discovery and synchronous cursor reads use the same normalized
// predicates. Historical charity names are resolved in the selection snapshot.
func listPredicates(ctx context.Context, reader logReadQueryer, scope logSearchScope, now int64, filter ListFilter, cursor listCursor) (string, []any, error) {
	query, args := scope.visibleLogs(now)
	if useOperationRange(filter) {
		query += " AND " + logOperationSQL + "=?"
		args = append(args, filter.Operation)
	}
	query += phaseFilter(filter.Phase) + outcomeFilter(filter.Outcome)
	query, args = diagnosisFilter(query, args, filter)
	if filter.Model != nil {
		query += ` AND l.model=?`
		args = append(args, *filter.Model)
	}
	if filter.UserID != nil {
		query += ` AND l.origin_user_id=?`
		args = append(args, *filter.UserID)
	}
	if filter.DiscordID != nil {
		query += ` AND l.origin_discord_id=?`
		args = append(args, *filter.DiscordID)
	}
	if filter.EndpointKeyID != nil {
		query += endpointKeyPredicate
		args = append(args, *filter.EndpointKeyID)
	}
	if filter.EndpointBaseURL != nil {
		query += endpointURLPredicate
		args = append(args, *filter.EndpointBaseURL)
	}
	if filter.UpstreamModel != nil {
		query += upstreamModelPredicate
		args = append(args, *filter.UpstreamModel)
	}
	if filter.ErrorCode != nil {
		query += ` AND l.caller_error_code=?`
		args = append(args, *filter.ErrorCode)
	}
	if filter.Status != nil {
		query += ` AND l.caller_status=?`
		args = append(args, *filter.Status)
	}
	if filter.UsageTotalMismatch != nil {
		query += ` AND l.usage_total_mismatch=?`
		args = append(args, *filter.UsageTotalMismatch)
	}
	if filter.From != nil {
		query += ` AND l.started_at>=?`
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		query += ` AND l.started_at<?`
		args = append(args, *filter.To)
	}
	modelPredicate, modelArgs, err := charityModelFilter(ctx, reader, filter)
	if err != nil {
		return "", nil, err
	}
	query += modelPredicate
	args = append(args, modelArgs...)
	if cursor.rowID != 0 {
		query += ` AND (l.started_at<? OR (l.started_at=? AND l.id<?))`
		args = append(args, cursor.startedAt, cursor.startedAt, cursor.rowID)
	}
	return query, args, nil
}

func (r *Repository) beginSearchRead(ctx context.Context, scope logSearchScope, now int64) (*sql.Tx, error) {
	if scope.role == "steward" {
		tx, _, err := r.beginStewardReadOn(ctx, r.readDB, scope.actorID, scope.steward)
		return tx, err
	}
	return r.beginPageRead(ctx, scope.role, scope.actorID, now)
}

func (r *Repository) authorizeSearch(ctx context.Context, scope logSearchScope) error {
	if ctx == nil || r == nil || !scope.valid() {
		return ErrInvalid
	}
	now, err := r.decisionNow()
	if err != nil {
		return err
	}
	ctx = logReadContext(ctx, scope.role)
	tx, err := r.beginSearchRead(ctx, scope, now)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return translateReadError(ctx, tx.Commit())
}

// Stream the ordered membership and its retention boundaries. The worker owns
// its budget. No page count, joins for display, or DTOs enter this read.
func (r *Repository) collectSearchIDs(ctx context.Context, scope logSearchScope, filter ListFilter, add func(id, expiresAt int64) error) (err error) {
	if ctx == nil || r == nil || !scope.valid() || add == nil || filter.Cursor != "" {
		return ErrInvalid
	}
	filter, err = normalizeListFilter(filter, scope.role)
	if err != nil {
		return err
	}
	ctx = logListContext(ctx, scope.role, filter)
	now, err := r.decisionNow()
	if err != nil {
		return err
	}
	tx, err := r.beginSearchRead(ctx, scope, now)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	where, args, err := listPredicates(ctx, tx, scope, now, filter, listCursor{})
	if err != nil {
		return err
	}
	finish := observeLogRead(ctx, "ids")
	defer func() { finish(err) }()
	rows, err := tx.QueryContext(ctx, "SELECT l.id,l.completed_at FROM request_logs l "+where+logListOrder, args...)
	if err != nil {
		return translateReadError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var completed sql.NullInt64
		if err := rows.Scan(&id, &completed); err != nil {
			return translateReadError(ctx, err)
		}
		var expiresAt int64
		if completed.Valid {
			if completed.Int64 < 0 || completed.Int64 > maxUnixSecond {
				return ErrInvariant
			}
			expiresAt = completed.Int64 + requestLogRetentionSeconds
		}
		if err := add(id, expiresAt); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return translateReadError(ctx, err)
	}
	if err := rows.Close(); err != nil {
		return translateReadError(ctx, err)
	}
	finish(nil)
	return translateReadError(ctx, tx.Commit())
}

func (r *Repository) readSearchPage(ctx context.Context, scope logSearchScope, ids []int64, metadata pagination.Metadata, decisionNow int64) (any, error) {
	if ctx == nil || r == nil || !scope.valid() || len(ids) > maximumLimit || decisionNow < 0 || decisionNow > maxUnixSecond {
		return nil, ErrInvalid
	}
	now, err := r.decisionNow()
	if err != nil {
		return nil, err
	}
	ctx = logReadContext(ctx, scope.role)
	tx, err := r.beginSearchRead(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The read pool may have been busy since the manager determined the window.
	now, err = r.decisionNow()
	if err != nil {
		return nil, err
	}
	decisionNow = max(decisionNow, now)
	var page any
	switch scope.role {
	case "user":
		page, err = projectLogPage(ctx, tx, scope, ids, &metadata, decisionNow, userProjection)
	case "steward":
		page, err = projectLogPage(ctx, tx, scope, ids, &metadata, decisionNow, managementProjection[StewardLogRow]())
	case "admin":
		page, err = projectLogPage(ctx, tx, scope, ids, &metadata, decisionNow, managementProjection[AdminLogRow]())
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, translateReadError(ctx, err)
	}
	return page, nil
}
