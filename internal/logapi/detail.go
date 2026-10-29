package logapi

import (
	"context"
	"database/sql"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
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
	var reader logReadQueryer = repository.readDB
	if filter.Page != nil || repository.readAuth != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, logPageTimeout)
		defer cancel()
		tx, err := repository.beginPageRead(ctx, "user", userID, now)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		reader = tx
	}
	var model string
	record, err := scanCommon(reader.QueryRowContext(ctx,
		`SELECT `+commonListColumns+`,l.model FROM request_logs l WHERE l.logical_request_id=? AND l.user_id=?`,
		requestID, userID), &model)
	if err != nil {
		return nil, translateReadError(ctx, err)
	}
	if !requestLogOrdinarilyVisible(record.completedAt, now) {
		return nil, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil || !utf8.ValidString(model) || len(model) > 512 {
		return nil, ErrInvariant
	}
	sources, err := readLogPrivacySources(ctx, reader, []int64{record.rowID}, 0)
	if err != nil {
		return nil, err
	}
	redactor := requestDNSRedactor(sources[record.rowID])
	delivery, err := readDelivery(ctx, reader, record.rowID)
	if err != nil {
		return nil, err
	}
	if delivery != nil {
		redactFailure(delivery.Failure, redactor.Redact)
	}
	switch kind := RouteKind(record.routeKind); {
	case kind.IsSelf() || kind == RouteDiscovery:
		row := UserSelfLogRow{
			RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind),
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
			Model: model, AttemptCount: strconv.FormatInt(record.attemptCount, 10),
		}
		attempts, err := repository.listUserAttempts(ctx, reader, userID, record.rowID, requestID, filter)
		if err != nil {
			return nil, err
		}
		row = redactUserLogRow(row, redactor).(UserSelfLogRow)
		redactUserLogAttempts(attempts.Data, sources[record.rowID])
		metadata := attempts.Pagination
		attempts.Pagination = nil
		return UserSelfLogDetail{Delivery: delivery, Request: row, Attempts: attempts, AttemptPagination: metadata}, nil
	case kind.IsCharity():
		// Endpoint metadata is used only to redact existing display text. Charity
		// detail never constructs an attempt DTO or exposes candidate counts.
		if !record.callerResultClass.Valid {
			return nil, ErrConflict
		}
		row := UserCharityLogRow{
			RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind),
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model,
		}
		row = redactUserLogRow(row, redactor).(UserCharityLogRow)
		return UserCharityLogDetail{
			Delivery: delivery,
			Request:  row, CallerSafeResult: CallerSafeResult{Class: ResultClass(record.callerResultClass.String)},
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
	if filter.Page != nil || repository.readAuth != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, logPageTimeout)
		defer cancel()
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminLogDetail{}, translateReadError(ctx, err)
	}
	defer tx.Rollback()
	if filter.Page != nil || repository.readAuth != nil {
		if err := repository.authorizeRead(ctx, tx, "admin", 0, now); err != nil {
			return AdminLogDetail{}, err
		}
	}
	var userID sql.NullInt64
	record, identity, err := scanManagementCommon(tx.QueryRowContext(ctx,
		`SELECT `+commonListColumns+`,`+callerIdentityColumns+`,l.user_id FROM request_logs l`+callerIdentityJoin+`WHERE l.logical_request_id=?`, requestID), &userID)
	if err != nil {
		return AdminLogDetail{}, translateReadError(ctx, err)
	}
	ordinary := requestLogOrdinarilyVisible(record.completedAt, now)
	held := false
	if repository.heldRead != nil {
		held, err = repository.heldRead.AuthorizeHeldRequestLogRead(ctx, tx, record.rowID, now)
		if err != nil {
			return AdminLogDetail{}, translateReadError(ctx, err)
		}
	}
	if !ordinary && !held {
		if err := tx.Commit(); err != nil {
			return AdminLogDetail{}, translateReadError(ctx, err)
		}
		return AdminLogDetail{}, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil {
		return AdminLogDetail{}, err
	}
	row := AdminLogRow{
		OriginIdentity:  record.origin,
		RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind),
		CallerResultClass: resultClassPointer(record.callerResultClass),
		CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
		StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
		UsageTotalMismatch: record.usageTotalMismatch == 1,
		UserID:             nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10), CallerIdentity: identity, CharityModel: record.charityModel, CharityModelID: record.charityModelID,
	}
	attempts, err := repository.listAdminAttemptsTx(ctx, tx, record.rowID, requestID, filter)
	if err != nil {
		return AdminLogDetail{}, err
	}
	delivery, err := readDelivery(ctx, tx, record.rowID)
	if err != nil {
		return AdminLogDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminLogDetail{}, translateReadError(ctx, err)
	}
	metadata := attempts.Pagination
	attempts.Pagination = nil
	return AdminLogDetail{Delivery: delivery, Request: row, Attempts: attempts, AttemptPagination: metadata}, nil
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
	if filter.Page != nil || repository.readAuth != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, logPageTimeout)
		defer cancel()
	}
	tx, redact, err := repository.beginStewardRead(ctx, stewardUserID, authorizer)
	if err != nil {
		return StewardLogDetail{}, err
	}
	defer tx.Rollback()
	var userID sql.NullInt64
	record, identity, err := scanManagementCommon(tx.QueryRowContext(ctx,
		`SELECT `+commonListColumns+`,`+callerIdentityColumns+`,l.user_id FROM request_logs l`+callerIdentityJoin+`WHERE l.logical_request_id=?`, requestID), &userID)
	if err != nil {
		return StewardLogDetail{}, translateReadError(ctx, err)
	}
	visible := requestLogOrdinarilyVisible(record.completedAt, now)
	if hook, ok := repository.heldRead.(StewardHeldReadAuthorizer); !visible && ok {
		held, err := hook.AuthorizeStewardHeldRequestLogRead(ctx, tx, stewardUserID, record.rowID, now)
		if err != nil {
			return StewardLogDetail{}, translateReadError(ctx, err)
		}
		visible = visible || held
	}
	if !visible {
		if err := tx.Commit(); err != nil {
			return StewardLogDetail{}, translateReadError(ctx, err)
		}
		return StewardLogDetail{}, ErrNotFound
	}
	usage, err := usageFromRecord(record)
	if err != nil {
		return StewardLogDetail{}, err
	}
	row := StewardLogRow{
		OriginIdentity:  record.origin,
		RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind),
		CallerResultClass: resultClassPointer(record.callerResultClass),
		CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
		StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
		UsageTotalMismatch: record.usageTotalMismatch == 1,
		UserID:             nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10), CallerIdentity: identity, CharityModel: record.charityModel, CharityModelID: record.charityModelID,
	}
	attempts, err := repository.listStewardAttempts(ctx, tx, stewardUserID, record.rowID, requestID, filter)
	if err != nil {
		return StewardLogDetail{}, err
	}
	delivery, err := readDelivery(ctx, tx, record.rowID)
	if err != nil {
		return StewardLogDetail{}, err
	}
	if redact {
		sources, err := readLogPrivacySources(ctx, tx, []int64{record.rowID}, 0)
		if err != nil {
			return StewardLogDetail{}, err
		}
		if delivery != nil {
			redactFailure(delivery.Failure, func(value string) string { return *redactUpstreamText(&value) })
			redactFailure(delivery.Failure, requestDNSRedactor(sources[record.rowID]).Redact)
		}
		for i := range attempts.Data {
			redactFailure(attempts.Data[i].ErrorDiagnostics, func(value string) string { return *redactUpstreamText(&value) })
			attempts.Data[i].Diag = redactUpstreamText(attempts.Data[i].Diag)
			attempts.Data[i].UpstreamCode = redactUpstreamText(attempts.Data[i].UpstreamCode)
			redactor := attemptDNSRedactor(sources[record.rowID], attempts.Data[i].AttemptSeq, attempts.Data[i].EndpointBaseURL)
			attempts.Data[i].Diag = redactDNSPointer(attempts.Data[i].Diag, redactor)
			redactFailure(attempts.Data[i].ErrorDiagnostics, redactor.Redact)
		}
	}
	if err := tx.Commit(); err != nil {
		return StewardLogDetail{}, translateReadError(ctx, err)
	}
	metadata := attempts.Pagination
	attempts.Pagination = nil
	return StewardLogDetail{Delivery: delivery, Request: row, Attempts: attempts, AttemptPagination: metadata}, nil
}

type attemptRecord struct {
	errorDiagnostics   string
	sequence           int64
	resultKind         string
	endpointKeyID      sql.NullInt64
	baseURL            string
	connectorType      string
	upstreamModel      string
	statusCode         sql.NullInt64
	upstreamCode       sql.NullString
	diag               sql.NullString
	uncached           int64
	cacheWrite         int64
	cacheRead          int64
	output             int64
	usageUnknown       int
	usageTotalMismatch int
	usageDetails       string
	startedAt          int64
	completedAt        int64
}

const attemptColumns = `a.attempt_seq,a.result_kind,a.endpoint_key_id_snapshot,a.canonical_base_url,
a.connector_type,a.upstream_model_id,a.upstream_status,a.upstream_code,a.diag,
a.input_tokens,a.cache_write_input_tokens,a.cache_read_input_tokens,a.output_tokens,
a.usage_unknown,a.usage_total_mismatch,a.started_at,a.completed_at,a.usage_details,
COALESCE((SELECT d.details FROM request_attempt_diagnostics d WHERE d.claim_id=a.claim_id),'')`

// Claim identity is the only link to the donation used by this attempt. Deleted
// claims or resources leave both navigation IDs absent; snapshots cannot restore them.
const managementAttemptTargetColumns = `d.id,CASE WHEN d.id IS NOT NULL THEN dk.id END`
const managementAttemptTargetJoin = ` JOIN request_logs l ON l.id=a.request_log_id
LEFT JOIN dispatch_claims dc ON dc.id=a.claim_id AND dc.logical_request_id=l.logical_request_id AND dc.attempt_seq=a.attempt_seq AND dc.purpose='charity' AND l.route_kind IN ` + charityRouteKindsSQL + `
LEFT JOIN donation_keys dk ON dk.id=dc.donation_key_id
LEFT JOIN donations d ON d.id=dk.donation_id `

func scanAttempt(scanner rowScanner, extra ...any) (attemptRecord, error) {
	var record attemptRecord
	targets := []any{
		&record.sequence, &record.resultKind, &record.endpointKeyID, &record.baseURL,
		&record.connectorType, &record.upstreamModel, &record.statusCode, &record.upstreamCode, &record.diag,
		&record.uncached, &record.cacheWrite, &record.cacheRead, &record.output,
		&record.usageUnknown, &record.usageTotalMismatch, &record.startedAt, &record.completedAt, &record.usageDetails,
		&record.errorDiagnostics,
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
		(record.usageUnknown != 0 && record.usageUnknown != 1) ||
		(record.usageTotalMismatch != 0 && record.usageTotalMismatch != 1) || record.startedAt < 0 ||
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

func (repository *Repository) listUserAttempts(ctx context.Context, reader logReadQueryer, userID, requestLogID int64, requestID string, filter AttemptFilter) (Page[UserSelfLogAttempt], error) {
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
WHERE a.request_log_id=? AND a.attempt_seq>?`
	query, args, metadata, err := logPageQuery(ctx, reader, query, `SELECT COUNT(*) FROM (`+query+`)`, ` ORDER BY a.attempt_seq ASC`, []any{userID, requestLogID, cursor}, filter.Page, filter.Limit)
	if err != nil {
		return Page[UserSelfLogAttempt]{}, err
	}
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[UserSelfLogAttempt]{}, translateReadError(ctx, err)
	}
	defer rows.Close()
	capacity := filter.Limit
	if capacity < 1 || capacity > maximumLimit {
		return Page[UserSelfLogAttempt]{}, ErrInvalid
	}
	page := Page[UserSelfLogAttempt]{Data: make([]UserSelfLogAttempt, 0, capacity), Pagination: metadata}
	var last int64
	more := false
	for rows.Next() {
		var endpointNote, keyNote string
		record, scanErr := scanAttempt(rows, &endpointNote, &keyNote)
		if scanErr != nil {
			return Page[UserSelfLogAttempt]{}, translateReadError(ctx, scanErr)
		}
		if !utf8.ValidString(endpointNote) || !utf8.ValidString(keyNote) || len(endpointNote) > 4096 || len(keyNote) > 4096 {
			return Page[UserSelfLogAttempt]{}, ErrInvariant
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0, record.usageDetails)
		if usageErr != nil {
			return Page[UserSelfLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, UserSelfLogAttempt{
			ErrorDiagnostics: diagnostic.Decode(record.errorDiagnostics),
			AttemptSeq:       strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			EndpointNote: endpointNote, KeyNote: keyNote, ConnectorType: record.connectorType,
			UpstreamModelID: record.upstreamModel, StatusCode: intPointer(record.statusCode),
			UpstreamCode: textPointer(record.upstreamCode), Diag: textPointer(record.diag), Usage: usage,
			StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[UserSelfLogAttempt]{}, translateReadError(ctx, err)
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
	return repository.listAdminAttemptsTx(ctx, repository.readDB, requestLogID, requestID, filter)
}

type adminAttemptQueryer = logReadQueryer

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
	query := `SELECT ` + attemptColumns + `,` + managementAttemptTargetColumns + ` FROM request_attempts a` + managementAttemptTargetJoin + `
WHERE a.request_log_id=? AND a.attempt_seq>?`
	query, args, metadata, err := logPageQuery(ctx, queryer, query, `SELECT COUNT(*) FROM (`+query+`)`, ` ORDER BY a.attempt_seq ASC`, []any{requestLogID, cursor}, filter.Page, filter.Limit)
	if err != nil {
		return Page[AdminLogAttempt]{}, err
	}
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[AdminLogAttempt]{}, translateReadError(ctx, err)
	}
	defer rows.Close()
	capacity := filter.Limit
	if capacity < 1 || capacity > maximumLimit {
		return Page[AdminLogAttempt]{}, ErrInvalid
	}
	page := Page[AdminLogAttempt]{Data: make([]AdminLogAttempt, 0, capacity), Pagination: metadata}
	var last int64
	more := false
	for rows.Next() {
		var donationID, donationKeyID sql.NullInt64
		record, scanErr := scanAttempt(rows, &donationID, &donationKeyID)
		if scanErr != nil {
			return Page[AdminLogAttempt]{}, translateReadError(ctx, scanErr)
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0, record.usageDetails)
		if usageErr != nil {
			return Page[AdminLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, AdminLogAttempt{
			ErrorDiagnostics: diagnostic.Decode(record.errorDiagnostics),
			DonationID:       nullableDecimal(donationID), DonationKeyID: nullableDecimal(donationKeyID),
			AttemptSeq: strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			ConnectorType: record.connectorType, UpstreamModelID: record.upstreamModel,
			StatusCode: intPointer(record.statusCode), UpstreamCode: textPointer(record.upstreamCode),
			Diag: textPointer(record.diag), Usage: usage, UsageTotalMismatch: record.usageTotalMismatch == 1,
			StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[AdminLogAttempt]{}, translateReadError(ctx, err)
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
	query := `SELECT ` + attemptColumns + `,` + managementAttemptTargetColumns + ` FROM request_attempts a` + managementAttemptTargetJoin + `
WHERE a.request_log_id=? AND a.attempt_seq>?`
	query, args, metadata, err := logPageQuery(ctx, tx, query, `SELECT COUNT(*) FROM (`+query+`)`, ` ORDER BY a.attempt_seq ASC`, []any{requestLogID, cursor}, filter.Page, filter.Limit)
	if err != nil {
		return Page[StewardLogAttempt]{}, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[StewardLogAttempt]{}, translateReadError(ctx, err)
	}
	defer rows.Close()
	capacity := filter.Limit
	if capacity < 1 || capacity > maximumLimit {
		return Page[StewardLogAttempt]{}, ErrInvalid
	}
	page := Page[StewardLogAttempt]{Data: make([]StewardLogAttempt, 0, capacity), Pagination: metadata}
	var last int64
	more := false
	for rows.Next() {
		var donationID, donationKeyID sql.NullInt64
		record, scanErr := scanAttempt(rows, &donationID, &donationKeyID)
		if scanErr != nil {
			return Page[StewardLogAttempt]{}, translateReadError(ctx, scanErr)
		}
		if len(page.Data) >= filter.Limit {
			more = true
			continue
		}
		usage, usageErr := zeroUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageUnknown != 0, record.usageDetails)
		if usageErr != nil {
			return Page[StewardLogAttempt]{}, usageErr
		}
		page.Data = append(page.Data, StewardLogAttempt{
			ErrorDiagnostics: diagnostic.Decode(record.errorDiagnostics),
			DonationID:       nullableDecimal(donationID), DonationKeyID: nullableDecimal(donationKeyID),
			AttemptSeq: strconv.FormatInt(record.sequence, 10), ResultKind: ResultKind(record.resultKind),
			EndpointKeyID: attemptKeyID(record.endpointKeyID), EndpointBaseURL: record.baseURL,
			ConnectorType: record.connectorType, UpstreamModelID: record.upstreamModel,
			StatusCode: intPointer(record.statusCode), UpstreamCode: textPointer(record.upstreamCode),
			Diag: textPointer(record.diag), Usage: usage, UsageTotalMismatch: record.usageTotalMismatch == 1,
			StartedAt: record.startedAt, CompletedAt: record.completedAt,
		})
		last = record.sequence
	}
	if err := rows.Err(); err != nil {
		return Page[StewardLogAttempt]{}, translateReadError(ctx, err)
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
