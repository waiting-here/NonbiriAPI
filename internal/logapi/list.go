package logapi

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const commonListColumns = `l.id,l.logical_request_id,l.route_kind,l.caller_result_class,
l.caller_status,l.caller_error_code,l.started_at,l.completed_at,
l.uncached_input_tokens,l.cache_write_input_tokens,l.cache_read_input_tokens,l.output_tokens,
l.usage_unknown,l.usage_total_mismatch,l.attempt_count,
COALESCE((SELECT ce.delta_mag
 FROM credit_operations co JOIN credit_entries ce ON ce.operation_id=co.id
 WHERE co.source_type='logical_request' AND co.source_id=l.logical_request_id
   AND co.kind IN ('forward_settle','charity_settle')
   AND ce.account_kind_snapshot='platform' AND ce.delta_sign=1
 ORDER BY co.ledger_seq DESC,ce.line_no ASC LIMIT 1),zeroblob(16)),l.rejection_stage,l.rejection_reason,l.request_method,l.request_path,l.requested_streaming,l.automatic_actions,l.usage_details,CASE WHEN l.rejection_stage IS NOT NULL OR
 (l.attempt_count=0 AND l.caller_result_class='failed' AND l.error_source='platform' AND l.caller_error_code IN ('unbound_model','rate_limited','service_unavailable','internal')) THEN l.error_diag END`

// Each condition can match a different attempt. Independent sets also prevent
// repeated attempts from duplicating logical requests or their count.
const (
	endpointKeyPredicate   = ` AND l.id IN (SELECT request_log_id FROM request_attempts WHERE endpoint_key_id_snapshot=?)`
	endpointURLPredicate   = ` AND l.id IN (SELECT request_log_id FROM request_attempts WHERE canonical_base_url=?)`
	upstreamModelPredicate = ` AND l.id IN (SELECT request_log_id FROM request_attempts WHERE upstream_model_id=?)`
)

type logProjection[T any] struct {
	columns string
	join    string
	scan    func(rowScanner) (int64, T, error)
	redact  func(context.Context, logReadQueryer, []int64, []T) error
}

var userProjection = logProjection[UserLogRow]{columns: commonListColumns + ",l.model", scan: scanUserLogRow, redact: redactUserLogRows}

func managementProjection[T AdminLogRow | StewardLogRow]() logProjection[T] {
	return logProjection[T]{columns: commonListColumns + "," + callerIdentityColumns + ",l.user_id", join: callerIdentityJoin, scan: scanManagementLogRow[T]}
}

func (r *Repository) ListUser(ctx context.Context, userID int64, filter ListFilter) (Page[UserLogRow], error) {
	return listLogs(ctx, r, logSearchScope{role: "user", actorID: userID}, filter, userProjection)
}

func (r *Repository) ListAdmin(ctx context.Context, filter ListFilter) (Page[AdminLogRow], error) {
	return listLogs(ctx, r, logSearchScope{role: "admin"}, filter, managementProjection[AdminLogRow]())
}

func (r *Repository) ListSteward(ctx context.Context, userID int64, filter ListFilter, authorizer StewardAuthorizer) (Page[StewardLogRow], error) {
	return listLogs(ctx, r, logSearchScope{role: "steward", actorID: userID, steward: authorizer}, filter, managementProjection[StewardLogRow]())
}

func listLogs[T any](ctx context.Context, r *Repository, scope logSearchScope, filter ListFilter, projection logProjection[T]) (Page[T], error) {
	if ctx == nil || r == nil || !scope.valid() {
		return Page[T]{}, ErrInvalid
	}
	filter, err := normalizeListFilter(filter, scope.role)
	if err != nil {
		return Page[T]{}, err
	}
	ctx = logListContext(ctx, scope.role, filter)
	if filter.Page != nil || r.readAuth != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, logPageTimeout)
		defer cancel()
	}
	owner := filterOwner(scope.role, scope.actorID, filter)
	tokenScope := "logapi-" + scope.role + "-list-v1"
	now, err := r.decisionNow()
	if err != nil {
		return Page[T]{}, err
	}
	var tx *sql.Tx
	if filter.Page == nil && r.readAuth == nil && scope.role != "steward" {
		// Legacy embedded repositories rely on the caller for cursor authorization.
		tx, err = r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		err = translateReadError(ctx, err)
	} else {
		tx, err = r.beginSearchRead(ctx, scope, now)
	}
	if err != nil {
		return Page[T]{}, err
	}
	defer tx.Rollback()
	cursor, err := r.decodeListCursor(filter.Cursor, tokenScope, owner)
	if err != nil {
		return Page[T]{}, err
	}
	where, args, err := listPredicates(ctx, tx, scope, now, filter, cursor)
	if err != nil {
		return Page[T]{}, err
	}
	query, args, metadata, err := logIDPageQuery(ctx, tx, where, args, filter.Page, filter.Limit)
	if err != nil {
		return Page[T]{}, err
	}
	finishRows := observeLogRead(ctx, "rows")
	ids, last, more, err := listPageIDs(ctx, tx, query, args, filter.Limit)
	if err != nil {
		finishRows(err)
		return Page[T]{}, err
	}
	page, err := projectLogPage(ctx, tx, scope, ids, metadata, now, projection)
	finishRows(err)
	if err != nil {
		return Page[T]{}, err
	}
	if more {
		next, err := r.encodeListCursor(tokenScope, owner, last)
		if err != nil {
			return Page[T]{}, err
		}
		page.NextCursor = &next
	}
	if err := tx.Commit(); err != nil {
		return Page[T]{}, translateReadError(ctx, err)
	}
	return page, nil
}

func listPageIDs(ctx context.Context, tx *sql.Tx, query string, args []any, limit int) (ids []int64, last listCursor, more bool, err error) {
	finish := observeLogRead(ctx, "ids")
	defer func() { finish(err) }()
	if limit < 1 || limit > maximumLimit {
		return nil, last, false, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, last, false, translateReadError(ctx, err)
	}
	defer rows.Close()
	ids = make([]int64, 0, limit)
	for rows.Next() {
		var pos listCursor
		if err := rows.Scan(&pos.rowID, &pos.startedAt); err != nil {
			return nil, last, false, translateReadError(ctx, err)
		}
		if len(ids) == limit {
			more = true
		} else {
			ids = append(ids, pos.rowID)
			last = pos
		}
	}
	return ids, last, more, translateReadError(ctx, rows.Err())
}

// The supplied IDs carry the snapshot order. Project only this window, in that
// order, rechecking visibility before reading the caller and settlement facts.
func projectLogPage[T any](ctx context.Context, tx *sql.Tx, scope logSearchScope, ids []int64, metadata *pagination.Metadata, now int64, projection logProjection[T]) (page Page[T], err error) {
	finish := observeLogRead(ctx, "projection")
	defer func() { finish(err) }()
	page = Page[T]{Data: make([]T, 0, len(ids)), Pagination: metadata}
	if len(ids) == 0 {
		return page, nil
	}
	args := make([]any, 0, 2*len(ids)+2)
	for index, id := range ids {
		args = append(args, index, id)
	}
	where, scopeArgs := scope.visibleLogs(now)
	query := "WITH wanted(pos,id) AS (VALUES " + strings.TrimSuffix(strings.Repeat("(?,?),", len(ids)), ",") + ") SELECT " + projection.columns +
		" FROM wanted CROSS JOIN request_logs l ON l.id=wanted.id" + projection.join + " " + where + " ORDER BY wanted.pos"
	rows, err := tx.QueryContext(ctx, query, append(args, scopeArgs...)...)
	if err != nil {
		return Page[T]{}, translateReadError(ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		id, data, err := projection.scan(rows)
		if err != nil {
			return Page[T]{}, translateReadError(ctx, err)
		}
		if len(page.Data) >= len(ids) || ids[len(page.Data)] != id {
			return Page[T]{}, ErrSearchExpired
		}
		page.Data = append(page.Data, data)
	}
	if err := rows.Err(); err != nil {
		return Page[T]{}, translateReadError(ctx, err)
	}
	if err := rows.Close(); err != nil {
		return Page[T]{}, translateReadError(ctx, err)
	}
	if len(page.Data) != len(ids) {
		return Page[T]{}, ErrSearchExpired
	}
	if projection.redact != nil {
		if err := projection.redact(ctx, tx, ids, page.Data); err != nil {
			return Page[T]{}, err
		}
	}
	return page, nil
}

func scanUserLogRow(scanner rowScanner) (int64, UserLogRow, error) {
	var model string
	record, err := scanCommon(scanner, &model)
	if err != nil {
		return 0, nil, err
	}
	usage, err := usageFromRecord(record)
	if err != nil || !utf8Bound(model, 512) {
		return 0, nil, ErrInvariant
	}
	switch kind := RouteKind(record.routeKind); {
	case kind.IsSelf() || kind == RouteDiscovery:
		return record.rowID, UserSelfLogRow{
			RejectionFields: rejectionFields(record),
			ID:              record.id, RouteKind: kind,
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
			Model: model, AttemptCount: strconv.FormatInt(record.attemptCount, 10),
		}, nil
	case kind.IsCharity():
		return record.rowID, UserCharityLogRow{
			RejectionFields: rejectionFields(record),
			ID:              record.id, RouteKind: kind,
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model,
		}, nil
	default:
		return 0, nil, ErrInvariant
	}
}

func scanManagementLogRow[T AdminLogRow | StewardLogRow](scanner rowScanner) (int64, T, error) {
	var userID sql.NullInt64
	record, identity, err := scanManagementCommon(scanner, &userID)
	if err != nil {
		return 0, T{}, err
	}
	usage, err := usageFromRecord(record)
	if err != nil {
		return 0, T{}, err
	}
	return record.rowID, T{
		OriginIdentity:  record.origin,
		RejectionFields: rejectionFields(record),
		ID:              record.id, RouteKind: RouteKind(record.routeKind),
		CallerResultClass: resultClassPointer(record.callerResultClass),
		CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
		StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
		UsageTotalMismatch: record.usageTotalMismatch == 1,
		UserID:             nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10), CallerIdentity: identity, CharityModel: record.charityModel, CharityModelID: record.charityModelID,
	}, nil
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
