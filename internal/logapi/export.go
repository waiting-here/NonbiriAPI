package logapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
)

const maxExportBytes = 16 * 1024 * 1024

type AdminLogExport struct {
	Data []AdminLogRow `json:"data"`
}

// ExportAdmin returns the fixed Admin row projection in ascending
// (started_at,id) order. Cursor/limit are rejected; exceeding either export
// bound fails the whole operation without truncating a seemingly complete file.
func (repository *Repository) ExportAdmin(ctx context.Context, filter ListFilter) ([]AdminLogRow, error) {
	if repository == nil || ctx == nil {
		return nil, ErrInvalid
	}
	return repository.exportManagement(ctx, repository.db, filter)
}

// ExportSteward authorizes and reads the complete bounded file in one snapshot.
// It shares the management projection without sharing administrator authority.
func (repository *Repository) ExportSteward(ctx context.Context, userID int64, filter ListFilter, authorizer StewardAuthorizer) ([]AdminLogRow, error) {
	if repository == nil || ctx == nil || userID <= 0 || authorizer == nil {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, logPageTimeout)
	defer cancel()
	tx, err := repository.beginStewardRead(ctx, userID, authorizer)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := repository.exportManagement(ctx, tx, filter)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, translateSQLError(err)
	}
	return rows, nil
}

func (repository *Repository) exportManagement(ctx context.Context, reader logReadQueryer, filter ListFilter) ([]AdminLogRow, error) {
	if filter.Cursor != "" || filter.Limit != 0 || filter.Model != nil || filter.Page != nil {
		return nil, ErrInvalid
	}
	normalized, err := normalizeListFilter(ListFilter{
		Phase:  filter.Phase,
		UserID: filter.UserID, EndpointBaseURL: filter.EndpointBaseURL,
		UpstreamModel: filter.UpstreamModel, ErrorCode: filter.ErrorCode,
		Status: filter.Status, From: filter.From, To: filter.To, Limit: maximumLimit,
	}, "admin")
	if err != nil {
		return nil, err
	}
	now, err := repository.decisionNow()
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + commonListColumns + `,` + callerIdentityColumns + `,l.user_id FROM request_logs l` + callerIdentityJoin + `
WHERE (l.completed_at IS NULL OR l.completed_at>?)`
	args := make([]any, 0, 16)
	args = append(args, now-requestLogRetentionSeconds)
	query += phaseFilter(normalized.Phase)
	if normalized.UserID != nil {
		query += ` AND l.user_id=?`
		args = append(args, *normalized.UserID)
	}
	if normalized.EndpointBaseURL != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts ea WHERE ea.request_log_id=l.id AND ea.canonical_base_url=?)`
		args = append(args, *normalized.EndpointBaseURL)
	}
	if normalized.UpstreamModel != nil {
		query += ` AND EXISTS(SELECT 1 FROM request_attempts em WHERE em.request_log_id=l.id AND em.upstream_model_id=?)`
		args = append(args, *normalized.UpstreamModel)
	}
	if normalized.ErrorCode != nil {
		query += ` AND l.caller_error_code=?`
		args = append(args, *normalized.ErrorCode)
	}
	if normalized.Status != nil {
		query += ` AND l.caller_status=?`
		args = append(args, *normalized.Status)
	}
	if normalized.From != nil {
		query += ` AND l.started_at>=?`
		args = append(args, *normalized.From)
	}
	if normalized.To != nil {
		query += ` AND l.started_at<?`
		args = append(args, *normalized.To)
	}
	query += ` ORDER BY l.started_at ASC,l.id ASC LIMIT ?`
	args = append(args, maxExportRows+1)
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, translateSQLError(err)
	}
	defer rows.Close()
	result := make([]AdminLogRow, 0, 256)
	for rows.Next() {
		var userID sql.NullInt64
		record, identity, scanErr := scanManagementCommon(rows, &userID)
		if scanErr != nil {
			return nil, translateSQLError(scanErr)
		}
		if len(result) >= maxExportRows {
			return nil, ErrCapacity
		}
		usage, usageErr := usageFromRecord(record)
		if usageErr != nil {
			return nil, usageErr
		}
		result = append(result, AdminLogRow{
			RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind),
			CallerResultClass: resultClassPointer(record.callerResultClass),
			CallerStatus:      intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode),
			StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage,
			UserID: nullableDecimal(userID), AttemptCount: strconv.FormatInt(record.attemptCount, 10), CallerIdentity: identity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, translateSQLError(err)
	}
	return result, nil
}

// MarshalAdminJSON applies the 16 MiB all-or-error boundary.
func MarshalAdminJSON(rows []AdminLogRow) ([]byte, error) {
	if len(rows) > maxExportRows {
		return nil, ErrCapacity
	}
	encoded, err := json.Marshal(AdminLogExport{Data: rows})
	if err != nil {
		return nil, ErrInvariant
	}
	// The trailing newline is part of the response body and therefore part of
	// the frozen 16 MiB all-or-error boundary.
	if len(encoded)+1 > maxExportBytes {
		return nil, ErrCapacity
	}
	return append(encoded, '\n'), nil
}

func MarshalAdminCSV(rows []AdminLogRow) ([]byte, error) {
	if len(rows) > maxExportRows {
		return nil, ErrCapacity
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := []string{
		"id", "route_kind", "caller_result_class", "caller_status", "caller_error_code",
		"started_at", "completed_at", "user_id", "attempt_count",
		"uncached_input_tokens", "cache_write_input_tokens", "cache_read_input_tokens",
		"output_tokens", "total_tokens", "usage_unknown", "charge",
		"caller_discord_nickname", "caller_discord_id",
		"phase", "rejection_stage", "rejection_reason", "request_method", "request_path",
	}
	if err := writer.Write(header); err != nil {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		var nickname, discordID *string
		if row.CallerIdentity != nil {
			nickname, discordID = row.CallerIdentity.DiscordNickname, row.CallerIdentity.DiscordID
		}
		record := []string{
			csvSafe(row.ID), csvSafe(string(row.RouteKind)), csvResultClass(row.CallerResultClass),
			csvInt(row.CallerStatus), csvString(row.CallerErrorCode), strconv.FormatInt(row.StartedAt, 10),
			csvInt64(row.CompletedAt), csvString(row.UserID), row.AttemptCount,
			row.Usage.UncachedInputTokens, row.Usage.CacheWriteInputTokens, row.Usage.CacheReadInputTokens,
			row.Usage.OutputTokens, row.Usage.TotalTokens, strconv.FormatBool(row.Usage.UsageUnknown), row.Usage.Charge,
			csvString(nickname), csvString(discordID),
			csvSafe(row.Phase), csvString(row.RejectionStage), csvString(row.RejectionReason), csvString(row.RequestMethod), csvString(row.RequestPath),
		}
		if err := writer.Write(record); err != nil {
			return nil, ErrUnavailable
		}
		writer.Flush()
		if writer.Error() != nil {
			return nil, ErrUnavailable
		}
		if buffer.Len() > maxExportBytes {
			return nil, ErrCapacity
		}
	}
	writer.Flush()
	if writer.Error() != nil {
		return nil, ErrUnavailable
	}
	return buffer.Bytes(), nil
}

func csvSafe(value string) string {
	if value == "" {
		return value
	}
	if strings.ContainsAny(value[:1], "=+-@\t\r\n") {
		return "'" + value
	}
	return value
}

func csvString(value *string) string {
	if value == nil {
		return ""
	}
	return csvSafe(*value)
}

func csvResultClass(value *ResultClass) string {
	if value == nil {
		return ""
	}
	return csvSafe(string(*value))
}

func csvInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func csvInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}
