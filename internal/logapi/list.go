package logapi

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

const commonListColumns = `l.id,l.logical_request_id,l.route_kind,l.caller_result_class,
l.caller_status,l.caller_error_code,l.started_at,l.completed_at,
l.uncached_input_tokens,l.cache_write_input_tokens,l.cache_read_input_tokens,l.output_tokens,
l.usage_unknown,l.attempt_count,
COALESCE((SELECT ce.delta_mag
 FROM credit_operations co JOIN credit_entries ce ON ce.operation_id=co.id
 WHERE co.source_type='logical_request' AND co.source_id=l.logical_request_id
   AND co.kind IN ('forward_settle','charity_settle')
   AND ce.account_kind_snapshot='platform' AND ce.delta_sign=1
 ORDER BY co.ledger_seq DESC,ce.line_no ASC LIMIT 1),zeroblob(16))`

func (repository *Repository) ListUser(ctx context.Context, userID int64, filter ListFilter) (Page[UserLogRow], error) {
	if repository == nil || ctx == nil || userID <= 0 {
		return Page[UserLogRow]{}, ErrInvalid
	}
	filter, err := normalizeListFilter(filter, "user")
	if err != nil {
		return Page[UserLogRow]{}, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return Page[UserLogRow]{}, err
	}
	owner := filterOwner("user", userID, filter)
	cursor, err := repository.decodeListCursor(filter.Cursor, "logapi-user-list-v1", owner)
	if err != nil {
		return Page[UserLogRow]{}, err
	}
	query := `SELECT ` + commonListColumns + `,l.model FROM request_logs l
WHERE l.user_id=? AND (l.completed_at IS NULL OR l.completed_at>?)`
	args := []any{userID, now - requestLogRetentionSeconds}
	if filter.Model != nil {
		query += ` AND l.model=?`
		args = append(args, *filter.Model)
	}
	if filter.ErrorCode != nil {
		query += ` AND l.caller_error_code=?`
		args = append(args, *filter.ErrorCode)
	}
	if filter.Status != nil {
		// Logical caller status only; attempt status never participates.
		query += ` AND l.caller_status=?`
		args = append(args, *filter.Status)
	}
	if filter.From != nil {
		query += ` AND l.started_at>=?`
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		query += ` AND l.started_at<?`
		args = append(args, *filter.To)
	}
	if cursor.rowID != 0 {
		query += ` AND (l.started_at<? OR (l.started_at=? AND l.id<?))`
		args = append(args, cursor.startedAt, cursor.startedAt, cursor.rowID)
	}
	query += ` ORDER BY l.started_at DESC,l.id DESC LIMIT ?`
	args = append(args, filter.Limit+1)

	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[UserLogRow]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[UserLogRow]{Data: make([]UserLogRow, 0, filter.Limit)}
	positions := make([]listCursor, 0, filter.Limit+1)
	for rows.Next() {
		var model string
		record, scanErr := scanCommon(rows, &model)
		if scanErr != nil {
			return Page[UserLogRow]{}, translateSQLError(scanErr)
		}
		usage, usageErr := usageFromRecord(record)
		if usageErr != nil || !utf8Bound(model, 512) {
			return Page[UserLogRow]{}, ErrInvariant
		}
		if len(page.Data) < filter.Limit {
			switch RouteKind(record.routeKind) {
			case RouteOpenAIChat, RouteDiscovery:
				page.Data = append(page.Data, UserSelfLogRow{
					ID: record.id, RouteKind: RouteKind(record.routeKind),
					CallerResultClass: resultClassPointer(record.callerResultClass),
					CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
					StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
					Model: model, AttemptCount: strconv.FormatInt(record.attemptCount, 10),
				})
			case RouteCharityChat:
				page.Data = append(page.Data, UserCharityLogRow{
					ID: record.id, RouteKind: RouteCharityChat,
					CallerResultClass: resultClassPointer(record.callerResultClass),
					CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
					StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model,
				})
			default:
				return Page[UserLogRow]{}, ErrInvariant
			}
		}
		positions = append(positions, listCursor{startedAt: record.startedAt, rowID: record.rowID})
	}
	if err := rows.Err(); err != nil {
		return Page[UserLogRow]{}, translateSQLError(err)
	}
	if len(positions) > filter.Limit {
		next, err := repository.encodeListCursor("logapi-user-list-v1", owner, positions[filter.Limit-1])
		if err != nil {
			return Page[UserLogRow]{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (repository *Repository) ListAdmin(ctx context.Context, filter ListFilter) (Page[AdminLogRow], error) {
	if repository == nil || ctx == nil {
		return Page[AdminLogRow]{}, ErrInvalid
	}
	filter, err := normalizeListFilter(filter, "admin")
	if err != nil {
		return Page[AdminLogRow]{}, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return Page[AdminLogRow]{}, err
	}
	owner := filterOwner("admin", 0, filter)
	cursor, err := repository.decodeListCursor(filter.Cursor, "logapi-admin-list-v1", owner)
	if err != nil {
		return Page[AdminLogRow]{}, err
	}
	query := `SELECT ` + commonListColumns + `,l.user_id FROM request_logs l
WHERE (l.completed_at IS NULL OR l.completed_at>?)`
	args := make([]any, 0, 16)
	args = append(args, now-requestLogRetentionSeconds)
	if filter.UserID != nil {
		query += ` AND l.user_id=?`
		args = append(args, *filter.UserID)
	}
	if filter.EndpointBaseURL != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts fa WHERE fa.request_log_id=l.id AND fa.canonical_base_url=?)`
		args = append(args, *filter.EndpointBaseURL)
	}
	if filter.UpstreamModel != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts fm WHERE fm.request_log_id=l.id AND fm.upstream_model_id=?)`
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
	if filter.From != nil {
		query += ` AND l.started_at>=?`
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		query += ` AND l.started_at<?`
		args = append(args, *filter.To)
	}
	if cursor.rowID != 0 {
		query += ` AND (l.started_at<? OR (l.started_at=? AND l.id<?))`
		args = append(args, cursor.startedAt, cursor.startedAt, cursor.rowID)
	}
	query += ` ORDER BY l.started_at DESC,l.id DESC LIMIT ?`
	args = append(args, filter.Limit+1)

	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[AdminLogRow]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[AdminLogRow]{Data: make([]AdminLogRow, 0, filter.Limit)}
	positions := make([]listCursor, 0, filter.Limit+1)
	for rows.Next() {
		var userID sql.NullInt64
		record, scanErr := scanCommon(rows, &userID)
		if scanErr != nil {
			return Page[AdminLogRow]{}, translateSQLError(scanErr)
		}
		usage, usageErr := usageFromRecord(record)
		if usageErr != nil {
			return Page[AdminLogRow]{}, usageErr
		}
		if len(page.Data) < filter.Limit {
			page.Data = append(page.Data, AdminLogRow{
				ID: record.id, RouteKind: RouteKind(record.routeKind),
				CallerResultClass: resultClassPointer(record.callerResultClass),
				CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
				StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
				UserID: nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10),
			})
		}
		positions = append(positions, listCursor{startedAt: record.startedAt, rowID: record.rowID})
	}
	if err := rows.Err(); err != nil {
		return Page[AdminLogRow]{}, translateSQLError(err)
	}
	if len(positions) > filter.Limit {
		next, err := repository.encodeListCursor("logapi-admin-list-v1", owner, positions[filter.Limit-1])
		if err != nil {
			return Page[AdminLogRow]{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (repository *Repository) ListSteward(
	ctx context.Context,
	stewardUserID int64,
	filter ListFilter,
	authorizer StewardAuthorizer,
) (Page[StewardLogRow], error) {
	if repository == nil || ctx == nil || stewardUserID <= 0 || authorizer == nil {
		return Page[StewardLogRow]{}, ErrInvalid
	}
	filter, err := normalizeListFilter(filter, "steward")
	if err != nil {
		return Page[StewardLogRow]{}, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return Page[StewardLogRow]{}, err
	}
	owner := filterOwner("steward", stewardUserID, filter)
	cursor, err := repository.decodeListCursor(filter.Cursor, "logapi-steward-list-v1", owner)
	if err != nil {
		return Page[StewardLogRow]{}, err
	}
	tx, err := repository.beginStewardRead(ctx, stewardUserID, authorizer)
	if err != nil {
		return Page[StewardLogRow]{}, err
	}
	defer tx.Rollback()
	// This query is deliberately independent from Admin list SQL. Its SELECT
	// omits identity/model/note columns before filtering or scanning.
	query := `SELECT ` + commonListColumns + ` FROM request_logs l
WHERE (l.completed_at IS NULL OR l.completed_at>?)`
	args := make([]any, 0, 16)
	args = append(args, now-requestLogRetentionSeconds)
	if filter.EndpointBaseURL != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts sa WHERE sa.request_log_id=l.id AND sa.canonical_base_url=?)`
		args = append(args, *filter.EndpointBaseURL)
	}
	if filter.UpstreamModel != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts sm WHERE sm.request_log_id=l.id AND sm.upstream_model_id=?)`
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
	if filter.From != nil {
		query += ` AND l.started_at>=?`
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		query += ` AND l.started_at<?`
		args = append(args, *filter.To)
	}
	if cursor.rowID != 0 {
		query += ` AND (l.started_at<? OR (l.started_at=? AND l.id<?))`
		args = append(args, cursor.startedAt, cursor.startedAt, cursor.rowID)
	}
	query += ` ORDER BY l.started_at DESC,l.id DESC LIMIT ?`
	args = append(args, filter.Limit+1)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[StewardLogRow]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[StewardLogRow]{Data: make([]StewardLogRow, 0, filter.Limit)}
	positions := make([]listCursor, 0, filter.Limit+1)
	for rows.Next() {
		record, scanErr := scanCommon(rows)
		if scanErr != nil {
			return Page[StewardLogRow]{}, translateSQLError(scanErr)
		}
		usage, usageErr := usageFromRecord(record)
		if usageErr != nil {
			return Page[StewardLogRow]{}, usageErr
		}
		if len(page.Data) < filter.Limit {
			// Construct directly into the independent Steward type. No Admin DTO
			// exists on this path, including transiently.
			page.Data = append(page.Data, StewardLogRow{
				ID: record.id, RouteKind: RouteKind(record.routeKind),
				CallerResultClass: resultClassPointer(record.callerResultClass),
				CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
				StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
				AttemptCount: strconv.FormatInt(record.attemptCount, 10),
			})
		}
		positions = append(positions, listCursor{startedAt: record.startedAt, rowID: record.rowID})
	}
	if err := rows.Err(); err != nil {
		return Page[StewardLogRow]{}, translateSQLError(err)
	}
	if err := rows.Close(); err != nil {
		return Page[StewardLogRow]{}, translateSQLError(err)
	}
	if len(positions) > filter.Limit {
		next, err := repository.encodeListCursor("logapi-steward-list-v1", owner, positions[filter.Limit-1])
		if err != nil {
			return Page[StewardLogRow]{}, err
		}
		page.NextCursor = &next
	}
	if err := tx.Commit(); err != nil {
		return Page[StewardLogRow]{}, translateSQLError(err)
	}
	return page, nil
}

func utf8Bound(value string, max int) bool {
	return len(value) <= max && strings.ToValidUTF8(value, "") == value
}

func nullableDecimal(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	result := strconv.FormatInt(value.Int64, 10)
	return &result
}
