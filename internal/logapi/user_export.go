package logapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
)

// ExportUser repeats the account check in the owner-only read snapshot and
// retains the same distinct private/charity projections as the personal list.
func (r *Repository) ExportUser(ctx context.Context, user int64, filter ListFilter) ([]UserLogRow, error) {
	if r == nil || ctx == nil || user <= 0 || filter.Cursor != "" || filter.Limit != 0 || filter.Page != nil {
		return nil, ErrInvalid
	}
	filter, err := normalizeListFilter(filter, "user")
	if err != nil {
		return nil, err
	}
	now, err := r.decisionNow()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, logPageTimeout)
	defer cancel()
	tx, err := r.beginPageRead(ctx, "user", user, now)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `SELECT ` + commonListColumns + `,l.model FROM request_logs l WHERE l.user_id=? AND (l.completed_at IS NULL OR l.completed_at>?)` + phaseFilter(filter.Phase)
	args := []any{user, now - requestLogRetentionSeconds}
	if filter.Model != nil {
		query += ` AND l.model=?`
		args = append(args, *filter.Model)
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
	query += ` ORDER BY l.started_at,l.id LIMIT ?`
	args = append(args, maxExportRows+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, translateSQLError(err)
	}
	defer rows.Close()
	result := []UserLogRow{}
	for rows.Next() {
		var model string
		record, err := scanCommon(rows, &model)
		if err != nil {
			return nil, translateSQLError(err)
		}
		if len(result) >= maxExportRows {
			return nil, ErrCapacity
		}
		usage, err := usageFromRecord(record)
		if err != nil || !utf8Bound(model, 512) {
			return nil, ErrInvariant
		}
		switch RouteKind(record.routeKind) {
		case RouteOpenAIChat, RouteOpenAIEmbeddings, RouteDiscovery:
			result = append(result, UserSelfLogRow{RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind), CallerResultClass: resultClassPointer(record.callerResultClass), CallerStatus: intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode), StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model, AttemptCount: strconv.FormatInt(record.attemptCount, 10)})
		case RouteCharityChat, RouteCharityEmbeddings:
			result = append(result, UserCharityLogRow{RejectionFields: rejectionFields(record), ID: record.id, RouteKind: RouteKind(record.routeKind), CallerResultClass: resultClassPointer(record.callerResultClass), CallerStatus: intPointer(record.callerStatus), CallerErrorCode: textPointer(record.callerErrorCode), StartedAt: record.startedAt, CompletedAt: int64Pointer(record.completedAt), Usage: usage, Model: model})
		default:
			return nil, ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		return nil, translateSQLError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, translateSQLError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, translateSQLError(err)
	}
	return result, nil
}

func MarshalUserJSON(rows []UserLogRow) ([]byte, error) {
	if len(rows) > maxExportRows {
		return nil, ErrCapacity
	}
	body, err := json.Marshal(struct {
		Data []UserLogRow `json:"data"`
	}{rows})
	if err != nil {
		return nil, ErrInvariant
	}
	if len(body)+1 > maxExportBytes {
		return nil, ErrCapacity
	}
	return append(body, '\n'), nil
}
func MarshalUserCSV(rows []UserLogRow) ([]byte, error) {
	if len(rows) > maxExportRows {
		return nil, ErrCapacity
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"id", "route_kind", "model", "caller_result_class", "caller_status", "caller_error_code", "started_at", "completed_at", "attempt_count", "uncached_input_tokens", "cache_write_input_tokens", "cache_read_input_tokens", "output_tokens", "total_tokens", "usage_unknown", "charge", "phase", "rejection_stage", "rejection_reason", "request_method", "request_path"}); err != nil {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		var v UserSelfLogRow
		switch item := row.(type) {
		case UserSelfLogRow:
			v = item
		case UserCharityLogRow:
			v = UserSelfLogRow{RejectionFields: item.RejectionFields, ID: item.ID, RouteKind: item.RouteKind, Model: item.Model, CallerResultClass: item.CallerResultClass, CallerStatus: item.CallerStatus, CallerErrorCode: item.CallerErrorCode, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt, Usage: item.Usage}
		default:
			return nil, ErrInvariant
		}
		if err := w.Write([]string{csvSafe(v.ID), csvSafe(string(v.RouteKind)), csvSafe(v.Model), csvResultClass(v.CallerResultClass), csvInt(v.CallerStatus), csvString(v.CallerErrorCode), strconv.FormatInt(v.StartedAt, 10), csvInt64(v.CompletedAt), v.AttemptCount, v.Usage.UncachedInputTokens, v.Usage.CacheWriteInputTokens, v.Usage.CacheReadInputTokens, v.Usage.OutputTokens, v.Usage.TotalTokens, strconv.FormatBool(v.Usage.UsageUnknown), v.Usage.Charge, csvSafe(v.Phase), csvString(v.RejectionStage), csvString(v.RejectionReason), csvString(v.RequestMethod), csvString(v.RequestPath)}); err != nil {
			return nil, ErrUnavailable
		}
		w.Flush()
		if w.Error() != nil {
			return nil, ErrUnavailable
		}
		if b.Len() > maxExportBytes {
			return nil, ErrCapacity
		}
	}
	w.Flush()
	if w.Error() != nil {
		return nil, ErrUnavailable
	}
	return b.Bytes(), nil
}
func (api *HTTPAPI) userExportJSON(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.userExport(w, r, p, false)
}
func (api *HTTPAPI) userExportCSV(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.userExport(w, r, p, true)
}
func (api *HTTPAPI) userExport(w http.ResponseWriter, r *http.Request, p UserPrincipal, csv bool) {
	if !requireNoBody(w, r) {
		return
	}
	filter, err := parseListFilter(r.URL.RawQuery, "user", true)
	if err != nil {
		writeLogError(w, err)
		return
	}
	rows, err := api.repository.ExportUser(r.Context(), p.UserID, filter)
	if err != nil {
		writeLogError(w, err)
		return
	}
	var body []byte
	if csv {
		body, err = MarshalUserCSV(rows)
	} else {
		body, err = MarshalUserJSON(rows)
	}
	if err != nil {
		writeLogError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if csv {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="nonbiri-logs.csv"`)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="nonbiri-logs.json"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
