package logapi

import (
	"context"
	"database/sql"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func (repository *Repository) GetUser(ctx context.Context, userID int64, requestID string, filter AttemptFilter) (UserLogDetail, error) {
	if repository == nil || ctx == nil || userID <= 0 || !db.ValidateOpaqueID(requestID, "req_") {
		return nil, ErrInvalid
	}
	filter, err := normalizeAttemptFilter(filter)
	if err != nil {
		return nil, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return nil, err
	}
	var model string
	record, err := scanCommon(repository.db.QueryRowContext(ctx,
		`SELECT `+commonListColumns+`,l.model FROM request_logs l WHERE l.logical_request_id=? AND l.user_id=?`,
		requestID, userID), &model)
	if err != nil {
		return nil, translateSQLError(err)
	}
	if !requestLogOrdinarilyVisible(record.completedAt, now) {
		return nil, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil || !utf8.ValidString(model) || len(model) > 512 {
		return nil, ErrInvariant
	}
	switch RouteKind(record.routeKind) {
	case RouteOpenAIChat, RouteDiscovery:
		row := UserSelfLogRow{
			ID: record.id, RouteKind: RouteKind(record.routeKind),
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
			Model: model, AttemptCount: strconv.FormatInt(record.attemptCount, 10),
		}
		attempts, err := repository.listUserAttempts(ctx, userID, record.rowID, requestID, filter)
		if err != nil {
			return nil, err
		}
		return UserSelfLogDetail{Request: row, Attempts: attempts}, nil
	case RouteCharityChat:
		// Charity detail deliberately returns before constructing or querying an
		// attempt projection. Its response shape and size are independent of the
		// number of physical candidates/retries.
		if !record.callerResultClass.Valid {
			return nil, ErrConflict
		}
		row := UserCharityLogRow{
			ID: record.id, RouteKind: RouteCharityChat,
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model,
		}
		return UserCharityLogDetail{
			Request: row, CallerSafeResult: CallerSafeResult{Class: ResultClass(record.callerResultClass.String)},
		}, nil
	default:
		return nil, ErrInvariant
	}
}

func (repository *Repository) GetAdmin(ctx context.Context, requestID string, filter AttemptFilter) (AdminLogDetail, error) {
	if repository == nil || ctx == nil || !db.ValidateOpaqueID(requestID, "req_") {
		return AdminLogDetail{}, ErrInvalid
	}
	filter, err := normalizeAttemptFilter(filter)
	if err != nil {
		return AdminLogDetail{}, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return AdminLogDetail{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminLogDetail{}, translateSQLError(err)
	}
	defer tx.Rollback()
	var userID sql.NullInt64
	record, err := scanCommon(tx.QueryRowContext(ctx,
		`SELECT `+commonListColumns+`,l.user_id FROM request_logs l WHERE l.logical_request_id=?`, requestID), &userID)
	if err != nil {
		return AdminLogDetail{}, translateSQLError(err)
	}
	ordinary := requestLogOrdinarilyVisible(record.completedAt, now)
	held := false
	if repository.heldRead != nil {
		held, err = repository.heldRead.AuthorizeHeldRequestLogRead(ctx, tx, record.rowID, now)
		if err != nil {
			return AdminLogDetail{}, err
		}
	}
	if !ordinary && !held {
		if err := tx.Commit(); err != nil {
			return AdminLogDetail{}, translateSQLError(err)
		}
		return AdminLogDetail{}, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil {
		return AdminLogDetail{}, err
	}
	row := AdminLogRow{
		ID: record.id, RouteKind: RouteKind(record.routeKind),
		CallerResultClass: resultClassPointer(record.callerResultClass),
		CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
		StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
		UserID: nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10),
	}
	attempts, err := repository.listAdminAttemptsTx(ctx, tx, record.rowID, requestID, filter)
	if err != nil {
		return AdminLogDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminLogDetail{}, translateSQLError(err)
	}
	return AdminLogDetail{Request: row, Attempts: attempts}, nil
}

func (repository *Repository) GetSteward(
	ctx context.Context,
	stewardUserID int64,
	requestID string,
	filter AttemptFilter,
	authorizer StewardAuthorizer,
) (StewardLogDetail, error) {
	if repository == nil || ctx == nil || stewardUserID <= 0 || authorizer == nil ||
		!db.ValidateOpaqueID(requestID, "req_") {
		return StewardLogDetail{}, ErrInvalid
	}
	filter, err := normalizeAttemptFilter(filter)
	if err != nil {
		return StewardLogDetail{}, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return StewardLogDetail{}, err
	}
	tx, err := repository.beginStewardRead(ctx, stewardUserID, authorizer)
	if err != nil {
		return StewardLogDetail{}, err
	}
	defer tx.Rollback()
	// Independent SELECT excludes user identity and logical model before scan.
	record, err := scanCommon(tx.QueryRowContext(ctx,
		`SELECT `+commonListColumns+` FROM request_logs l WHERE l.logical_request_id=?`, requestID))
	if err != nil {
		return StewardLogDetail{}, translateSQLError(err)
	}
	if !requestLogOrdinarilyVisible(record.completedAt, now) {
		return StewardLogDetail{}, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil {
		return StewardLogDetail{}, err
	}
	row := StewardLogRow{
		ID: record.id, RouteKind: RouteKind(record.routeKind),
		CallerResultClass: resultClassPointer(record.callerResultClass),
		CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
		StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
		AttemptCount: strconv.FormatInt(record.attemptCount, 10),
	}
	attempts, err := repository.listStewardAttempts(ctx, tx, stewardUserID, record.rowID, requestID, filter)
	if err != nil {
		return StewardLogDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return StewardLogDetail{}, translateSQLError(err)
	}
	return StewardLogDetail{Request: row, Attempts: attempts}, nil
}

type attemptRecord struct {
	sequence      int64
	resultKind    string
	endpointKeyID sql.NullInt64
	baseURL       string
	connectorType string
	upstreamModel string
	statusCode    sql.NullInt64
	upstreamCode  sql.NullString
	diag          sql.NullString
	uncached      int64
	cacheWrite    int64
	cacheRead     int64
	output        int64
	usageUnknown  int
	startedAt     int64
	completedAt   int64
}

const attemptColumns = `a.attempt_seq,a.result_kind,a.endpoint_key_id_snapshot,a.canonical_base_url,
a.connector_type,a.upstream_model_id,a.upstream_status,a.upstream_code,a.diag,
a.input_tokens,a.cache_write_input_tokens,a.cache_read_input_tokens,a.output_tokens,
a.usage_unknown,a.started_at,a.completed_at`

func scanAttempt(scanner rowScanner, extra ...any) (attemptRecord, error) {
	var record attemptRecord
	targets := []any{
		&record.sequence, &record.resultKind, &record.endpointKeyID, &record.baseURL,
		&record.connectorType, &record.upstreamModel, &record.statusCode, &record.upstreamCode, &record.diag,
		&record.uncached, &record.cacheWrite, &record.cacheRead, &record.output,
		&record.usageUnknown, &record.startedAt, &record.completedAt,
	}
	targets = append(targets, extra...)
	if err := scanner.Scan(targets...); err != nil {
		return attemptRecord{}, err
	}
	if record.sequence < 1 || record.sequence > 100 ||
		(record.resultKind != string(ResultResponse) && record.resultKind != string(ResultSynthetic)) ||
		!validBaseURL(record.baseURL) || !validConnector(record.connectorType) ||
		!utf8.ValidString(record.upstreamModel) || len([]rune(record.upstreamModel)) > 512 ||
		record.uncached < 0 || record.cacheWrite < 0 || record.cacheRead < 0 || record.output < 0 ||
		(record.usageUnknown != 0 && record.usageUnknown != 1) || record.startedAt < 0 ||
		record.completedAt < record.startedAt || record.completedAt > maxUnixSecond {
		return attemptRecord{}, ErrInvariant
	}
	if record.endpointKeyID.Valid && record.endpointKeyID.Int64 <= 0 {
		return attemptRecord{}, ErrInvariant
	}
	if record.statusCode.Valid && (record.statusCode.Int64 < 100 || record.statusCode.Int64 > 599) {
		return attemptRecord{}, ErrInvariant
	}
	if record.resultKind == string(ResultResponse) && !record.statusCode.Valid {
		return attemptRecord{}, ErrInvariant
	}
	if record.upstreamCode.Valid && !validUpstreamCode(record.upstreamCode.String) {
		return attemptRecord{}, ErrInvariant
	}
	if record.diag.Valid && !validDiagnostic(record.diag.String) {
		return attemptRecord{}, ErrInvariant
	}
	return record, nil
}

func attemptKeyID(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	result := strconv.FormatInt(value.Int64, 10)
	return &result
}

func (repository *Repository) listUserAttempts(ctx context.Context, userID, requestLogID int64, requestID string, filter AttemptFilter) (Page[UserSelfLogAttempt], error) {
	owner := attemptOwner("user", userID, requestID)
	cursor, err := repository.decodeAttemptCursor(filter.Cursor, "logapi-user-attempt-v1", owner)
	if err != nil {
		return Page[UserSelfLogAttempt]{}, err
	}
	query := `SELECT ` + attemptColumns + `,
CASE WHEN e.id IS NULL THEN '' ELSE e.note END,
CASE WHEN k.id IS NULL THEN '' ELSE k.note END
FROM request_attempts a
LEFT JOIN endpoints e ON e.id=a.endpoint_id_snapshot AND e.user_id=?
LEFT JOIN endpoint_keys k ON k.id=a.endpoint_key_id_snapshot AND k.endpoint_id=e.id
WHERE a.request_log_id=? AND a.attempt_seq>? ORDER BY a.attempt_seq ASC LIMIT ?`
	rows, err := repository.db.QueryContext(ctx, query, userID, requestLogID, cursor, filter.Limit+1)
	if err != nil {
		return Page[UserSelfLogAttempt]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[UserSelfLogAttempt]{Data: make([]UserSelfLogAttempt, 0, filter.Limit)}
	var last int64
	more := false
	for rows.Next() {
		var endpointNote, keyNote string
		record, scanErr := scanAttempt(rows, &endpointNote, &keyNote)
		if scanErr != nil {
			return Page[UserSelfLogAttempt]{}, translateSQLError(scanErr)
		}
		if !utf8.ValidString(endpointNote) || !utf8.ValidString(keyNote) || len(endpointNote) > 4096 || len(keyNote) > 4096 {
			return Page[UserSelfLogAttempt]{}, ErrInvariant
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0)
		if usageErr != nil {
			return Page[UserSelfLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, UserSelfLogAttempt{
			AttemptSeq: strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			EndpointNote: endpointNote, KeyNote: keyNote, ConnectorType: record.connectorType,
			UpstreamModelID: record.upstreamModel, StatusCode: intPointer(record.statusCode),
			UpstreamCode: textPointer(record.upstreamCode), Diag: textPointer(record.diag), Usage: usage,
			StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[UserSelfLogAttempt]{}, translateSQLError(err)
	}
	if more {
		next, err := repository.encodeAttemptCursor("logapi-user-attempt-v1", owner, last)
		if err != nil {
			return Page[UserSelfLogAttempt]{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (repository *Repository) listAdminAttempts(ctx context.Context, requestLogID int64, requestID string, filter AttemptFilter) (Page[AdminLogAttempt], error) {
	return repository.listAdminAttemptsTx(ctx, repository.db, requestLogID, requestID, filter)
}

type adminAttemptQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (repository *Repository) listAdminAttemptsTx(
	ctx context.Context,
	queryer adminAttemptQueryer,
	requestLogID int64,
	requestID string,
	filter AttemptFilter,
) (Page[AdminLogAttempt], error) {
	if queryer == nil {
		return Page[AdminLogAttempt]{}, ErrInvalid
	}
	owner := attemptOwner("admin", 0, requestID)
	cursor, err := repository.decodeAttemptCursor(filter.Cursor, "logapi-admin-attempt-v1", owner)
	if err != nil {
		return Page[AdminLogAttempt]{}, err
	}
	query := `SELECT ` + attemptColumns + ` FROM request_attempts a
WHERE a.request_log_id=? AND a.attempt_seq>? ORDER BY a.attempt_seq ASC LIMIT ?`
	rows, err := queryer.QueryContext(ctx, query, requestLogID, cursor, filter.Limit+1)
	if err != nil {
		return Page[AdminLogAttempt]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[AdminLogAttempt]{Data: make([]AdminLogAttempt, 0, filter.Limit)}
	var last int64
	more := false
	for rows.Next() {
		record, scanErr := scanAttempt(rows)
		if scanErr != nil {
			return Page[AdminLogAttempt]{}, translateSQLError(scanErr)
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0)
		if usageErr != nil {
			return Page[AdminLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, AdminLogAttempt{
			AttemptSeq: strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			ConnectorType: record.connectorType, UpstreamModelID: record.upstreamModel,
			StatusCode: intPointer(record.statusCode), UpstreamCode: textPointer(record.upstreamCode),
			Diag: textPointer(record.diag), Usage: usage, StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[AdminLogAttempt]{}, translateSQLError(err)
	}
	if more {
		next, err := repository.encodeAttemptCursor("logapi-admin-attempt-v1", owner, last)
		if err != nil {
			return Page[AdminLogAttempt]{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}

func requestLogOrdinarilyVisible(completedAt sql.NullInt64, now int64) bool {
	if !completedAt.Valid {
		return true
	}
	if completedAt.Int64 < 0 || completedAt.Int64 > maxUnixSecond {
		return false
	}
	if completedAt.Int64 > maxUnixSecond-requestLogRetentionSeconds {
		return true
	}
	return now < completedAt.Int64+requestLogRetentionSeconds
}

func (repository *Repository) listStewardAttempts(
	ctx context.Context,
	tx *sql.Tx,
	stewardUserID, requestLogID int64,
	requestID string,
	filter AttemptFilter,
) (Page[StewardLogAttempt], error) {
	owner := attemptOwner("steward", stewardUserID, requestID)
	cursor, err := repository.decodeAttemptCursor(filter.Cursor, "logapi-steward-attempt-v1", owner)
	if err != nil {
		return Page[StewardLogAttempt]{}, err
	}
	// Independent query and projection: no Admin/user/note/logical model column.
	query := `SELECT ` + attemptColumns + ` FROM request_attempts a
WHERE a.request_log_id=? AND a.attempt_seq>? ORDER BY a.attempt_seq ASC LIMIT ?`
	rows, err := tx.QueryContext(ctx, query, requestLogID, cursor, filter.Limit+1)
	if err != nil {
		return Page[StewardLogAttempt]{}, translateSQLError(err)
	}
	defer rows.Close()
	page := Page[StewardLogAttempt]{Data: make([]StewardLogAttempt, 0, filter.Limit)}
	var last int64
	more := false
	for rows.Next() {
		record, scanErr := scanAttempt(rows)
		if scanErr != nil {
			return Page[StewardLogAttempt]{}, translateSQLError(scanErr)
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0)
		if usageErr != nil {
			return Page[StewardLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, StewardLogAttempt{
			AttemptSeq: strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			ConnectorType: record.connectorType, UpstreamModelID: record.upstreamModel,
			StatusCode: intPointer(record.statusCode), UpstreamCode: textPointer(record.upstreamCode),
			Diag: textPointer(record.diag), Usage: usage, StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[StewardLogAttempt]{}, translateSQLError(err)
	}
	if more {
		next, err := repository.encodeAttemptCursor("logapi-steward-attempt-v1", owner, last)
		if err != nil {
			return Page[StewardLogAttempt]{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}
