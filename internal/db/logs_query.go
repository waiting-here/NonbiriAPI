package db

// Request-log read projections for the redesigned user and administrator log
// screens plus the administrator CSV/JSON export.
//
// Ownership and privacy are enforced inside the SQL, never by filtering a
// global result set afterwards:
//
//   - the user-station query is scoped by `user_id = ?` in the WHERE clause
//     and JOINs only the requester's own endpoint/key notes (the join condition
//     itself re-checks endpoint ownership), so another user's resources can
//     never enter a candidate row;
//   - the administrator query is a separate statement with its own response
//     type that never selects the user-chosen platform model name or any
//     note column: an administrator sees which actual endpoint base URL and
//     upstream model served a request, not how the user named things;
//   - `endpoint_base_url` is the bounded dispatch-time snapshot column written
//     by the forwarding rail (empty until that rail lands); notes are always
//     JOINed current values, so a deleted key/endpoint projects an empty note
//     while the log row itself is retained;
//   - charity consumers' rows reference no donor resource at all (the write
//     path only stores an endpoint_key_id owned by the logging user), so no
//     donor identifier, note, or base URL can leak through either projection.
//
// Every statement is parameterized and pre-validated; results are metadata
// only — no request/response content, credential, or ciphertext is ever
// projected, and ErrorDiag was already reduced through diagnostic.Bound at
// the write sink.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrLogExportTooLarge reports that the export selection exceeded the finite
// row bound; nothing is returned and no truncated result is ever produced.
var ErrLogExportTooLarge = errors.New("log export exceeds its row bound")

// MaxLogExportRows bounds one administrator log export. An export is not
// paginated, so this is the primary fail-closed bound: a selection that would
// produce more rows is refused instead of silently truncated.
const MaxLogExportRows = 10000

// MaxLogModelOptions bounds the candidate-model options endpoint. The list is
// derived from the requester's own retained logs and is small by construction.
const MaxLogModelOptions = 50

// maxLogFilterTextRunes bounds free-text equality filters (platform model,
// upstream model). It matches the stored-text bound of those columns.
const maxLogFilterTextRunes = 512

// AdminRequestLog is the metadata-only administrator log projection. It
// deliberately has no platform-model and no note field: those are user-private
// naming, and this type is never populated with them (the SELECT does not
// request them). EndpointBaseURL is the dispatch-time snapshot column.
type AdminRequestLog struct {
	ID                    int64
	UserID                int64
	RouteKind             string // personal | charity
	EndpointBaseURL       string // bounded snapshot; "" until the dispatch rail writes it
	EndpointKeyID         int64  // 0 when no key was selected or it was deleted later
	UpstreamModelID       string
	StatusCode            int
	DurationMs            int64
	StartedAt             time.Time
	CompletedAt           time.Time
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	PromptTokens          int64
	CompletionTokens      int64
	TotalTokens           int64
	UsageUnknown          bool
	ErrorCode             string
	ErrorSource           string // platform | upstream
	ErrorDiag             string // already diagnostic.Bound at the write sink
	AttemptID             string // opaque correlation id; "" for legacy rows
}

// UserRequestLog is the metadata-only owner-facing log projection. KeyNote and
// EndpointNote are JOINed current values of the user's own resources: they are
// empty once the resource is deleted (notes are never snapshotted).
// EndpointBaseURL is the dispatch-time snapshot column, so it survives resource
// deletion exactly as it was recorded.
type UserRequestLog struct {
	ID                    int64
	RouteKind             string
	Model                 string // the user's own platform model full_name
	EndpointKeyID         int64
	KeyNote               string
	EndpointNote          string
	EndpointBaseURL       string
	UpstreamModelID       string
	StatusCode            int
	DurationMs            int64
	StartedAt             time.Time
	CompletedAt           time.Time
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	PromptTokens          int64
	CompletionTokens      int64
	TotalTokens           int64
	UsageUnknown          bool
	ErrorCode             string
	ErrorSource           string
	ErrorDiag             string
	AttemptID             string
}

const adminLogSelectColumns = `
	rl.id, rl.user_id, rl.route_kind, rl.endpoint_base_url, rl.endpoint_key_id,
	rl.upstream_model_id, rl.status_code, rl.duration_ms, rl.started_at, rl.completed_at,
	rl.uncached_input_tokens, rl.cache_write_input_tokens, rl.cache_read_input_tokens, rl.output_tokens,
	rl.prompt_tokens, rl.completion_tokens, rl.total_tokens,
	rl.usage_unknown, rl.error_code, rl.error_source, rl.error_diag, rl.attempt_id`

// stewardLogSelectColumns mirrors adminLogSelectColumns except that charity
// rows (route_kind='charity') carry the DONOR's endpoint/key/upstream model as
// the dispatch target. A level-5 steward co-manages site-wide LOG/activity,
// not donor resources, so the donor's upstream identity is blanked for charity
// rows (endpoint_key_id → NULL, endpoint_base_url → ”, upstream_model_id →
// ”). The steward still sees the consumer's user id, route kind, status,
// tokens and bounded diagnostic — the minimal de-privacy projection for
// full-site log co-management (frozen §G, clarification §1.8). Personal rows
// are unaffected: their endpoint belongs to the logging user.
const stewardLogSelectColumns = `
	rl.id, rl.user_id, rl.route_kind,
	CASE WHEN rl.route_kind='charity' THEN '' ELSE rl.endpoint_base_url END,
	CASE WHEN rl.route_kind='charity' THEN NULL ELSE rl.endpoint_key_id END,
	CASE WHEN rl.route_kind='charity' THEN '' ELSE rl.upstream_model_id END,
	rl.status_code, rl.duration_ms, rl.started_at, rl.completed_at,
	rl.uncached_input_tokens, rl.cache_write_input_tokens, rl.cache_read_input_tokens, rl.output_tokens,
	rl.prompt_tokens, rl.completion_tokens, rl.total_tokens,
	rl.usage_unknown, rl.error_code, rl.error_source, rl.error_diag, rl.attempt_id`

func scanAdminRequestLog(scanner interface{ Scan(...any) error }) (AdminRequestLog, error) {
	var log AdminRequestLog
	var endpointKeyID sql.NullInt64
	var usageUnknown int
	var attemptID sql.NullString
	var startedAt, completedAt int64
	if err := scanner.Scan(
		&log.ID, &log.UserID, &log.RouteKind, &log.EndpointBaseURL, &endpointKeyID,
		&log.UpstreamModelID, &log.StatusCode, &log.DurationMs, &startedAt, &completedAt,
		&log.UncachedInputTokens, &log.CacheWriteInputTokens, &log.CacheReadInputTokens, &log.OutputTokens,
		&log.PromptTokens, &log.CompletionTokens, &log.TotalTokens,
		&usageUnknown, &log.ErrorCode, &log.ErrorSource, &log.ErrorDiag, &attemptID,
	); err != nil {
		return AdminRequestLog{}, err
	}
	if endpointKeyID.Valid {
		log.EndpointKeyID = endpointKeyID.Int64
	}
	if attemptID.Valid {
		log.AttemptID = attemptID.String
	}
	log.UsageUnknown = usageUnknown != 0
	log.StartedAt = time.Unix(startedAt, 0).UTC()
	log.CompletedAt = time.Unix(completedAt, 0).UTC()
	return log, nil
}

func scanUserRequestLog(scanner interface{ Scan(...any) error }) (UserRequestLog, error) {
	var log UserRequestLog
	var endpointKeyID sql.NullInt64
	var usageUnknown int
	var attemptID sql.NullString
	var startedAt, completedAt int64
	if err := scanner.Scan(
		&log.ID, &log.RouteKind, &log.Model, &endpointKeyID, &log.KeyNote, &log.EndpointNote,
		&log.EndpointBaseURL, &log.UpstreamModelID, &log.StatusCode, &log.DurationMs,
		&startedAt, &completedAt,
		&log.UncachedInputTokens, &log.CacheWriteInputTokens, &log.CacheReadInputTokens, &log.OutputTokens,
		&log.PromptTokens, &log.CompletionTokens, &log.TotalTokens,
		&usageUnknown, &log.ErrorCode, &log.ErrorSource, &log.ErrorDiag, &attemptID,
	); err != nil {
		return UserRequestLog{}, err
	}
	if endpointKeyID.Valid {
		log.EndpointKeyID = endpointKeyID.Int64
	}
	if attemptID.Valid {
		log.AttemptID = attemptID.String
	}
	log.UsageUnknown = usageUnknown != 0
	log.StartedAt = time.Unix(startedAt, 0).UTC()
	log.CompletedAt = time.Unix(completedAt, 0).UTC()
	return log, nil
}

// AdminLogQuery is the bounded, parameterized administrator log filter. Every
// filter is optional and matched exactly (frozen semantics):
//
//   - UserID: exact user_id (administrator drill-down);
//   - EndpointBaseURL: exact equality against the stored dispatch snapshot
//     (row click-through drills down by the full URL, never a substring);
//   - UpstreamModel: exact equality against the stored upstream model id;
//   - ErrorCode: exact equality against the stable stored code; an unknown
//     code simply matches nothing (the stored set is closed);
//   - Status: exact status code in [100,599]; 0 disables the filter;
//   - FromUnix/ToUnix: started_at >= FromUnix and started_at < ToUnix;
//     0 disables each side; FromUnix > ToUnix is invalid.
//
// Results are ordered id DESC and offset-paginated with Page/PageSize
// (LIMIT page_size+1 OFFSET (page-1)*page_size), so a client never infers
// pagination from a raw page size.
type AdminLogQuery struct {
	UserID          int64
	EndpointBaseURL string
	UpstreamModel   string
	ErrorCode       string
	Status          int
	FromUnix        int64
	ToUnix          int64
	Page            int
	PageSize        int
}

func (q AdminLogQuery) validate() error {
	if q.UserID < 0 {
		return fmt.Errorf("admin log query: user id is invalid")
	}
	if !validOptionalStoredText(q.EndpointBaseURL, maxBaseURLFilterBytes) {
		return fmt.Errorf("admin log query: endpoint base url filter is invalid")
	}
	if !validOptionalStoredText(q.UpstreamModel, maxLogFilterTextRunes) {
		return fmt.Errorf("admin log query: upstream model filter is invalid")
	}
	if !validErrorCodeFilter(q.ErrorCode) {
		return fmt.Errorf("admin log query: error code filter is invalid")
	}
	if q.Status != 0 && (q.Status < 100 || q.Status > maxLogStatus) {
		return fmt.Errorf("admin log query: status is out of range")
	}
	if q.FromUnix < 0 || q.ToUnix < 0 || (q.FromUnix > 0 && q.ToUnix > 0 && q.FromUnix > q.ToUnix) {
		return fmt.Errorf("admin log query: time range is invalid")
	}
	return nil
}

// maxBaseURLFilterBytes mirrors the egress canonical base_url byte bound; the
// snapshot column never stores anything longer, so a longer filter cannot match.
const maxBaseURLFilterBytes = 4096

func validErrorCodeFilter(s string) bool {
	if s == "" {
		return true
	}
	return validOptionalStoredText(s, maxLogErrorCodeLen)
}

// clampLogPage normalizes the 1-based page and clamps the page size into
// [1, MaxLogPageLimit] (frozen contract: default 20, maximum 100).
func clampLogPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxLogPageLimit {
		pageSize = MaxLogPageLimit
	}
	return page, pageSize
}

// QueryAdminRequestLogs returns at most one page of metadata-only log rows
// matching the frozen administrator filter, newest first. The projection never
// selects the user-chosen platform model name or any note column.
func (s *Store) QueryAdminRequestLogs(ctx context.Context, query AdminLogQuery) ([]AdminRequestLog, bool, error) {
	if err := query.validate(); err != nil {
		return nil, false, err
	}
	page, pageSize := clampLogPage(query.Page, query.PageSize)

	var clauses []string
	var args []any
	if query.UserID > 0 {
		clauses = append(clauses, "rl.user_id = ?")
		args = append(args, query.UserID)
	}
	if query.EndpointBaseURL != "" {
		clauses = append(clauses, "rl.endpoint_base_url = ?")
		args = append(args, query.EndpointBaseURL)
	}
	if query.UpstreamModel != "" {
		clauses = append(clauses, "rl.upstream_model_id = ?")
		args = append(args, query.UpstreamModel)
	}
	if query.ErrorCode != "" {
		clauses = append(clauses, "rl.error_code = ?")
		args = append(args, query.ErrorCode)
	}
	if query.Status != 0 {
		clauses = append(clauses, "rl.status_code = ?")
		args = append(args, query.Status)
	}
	if query.FromUnix > 0 {
		clauses = append(clauses, "rl.started_at >= ?")
		args = append(args, query.FromUnix)
	}
	if query.ToUnix > 0 {
		clauses = append(clauses, "rl.started_at < ?")
		args = append(args, query.ToUnix)
	}

	sqlText := `SELECT ` + adminLogSelectColumns + ` FROM request_logs rl`
	if len(clauses) > 0 {
		// #nosec G202 -- clauses is assembled exclusively from the fixed predicates
		// above and every filter value is passed separately in args.
		sqlText += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	sqlText += ` ORDER BY rl.id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query admin request logs: %w", err)
	}
	defer rows.Close()

	logs := make([]AdminRequestLog, 0, min(pageSize, 32))
	for rows.Next() {
		log, err := scanAdminRequestLog(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan admin request log: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate admin request logs: %w", err)
	}
	hasMore := len(logs) > pageSize
	if hasMore {
		logs = logs[:pageSize]
	}
	return logs, hasMore, nil
}

// QueryStewardRequestLogs is the level-5 full-site log projection for the
// user-station co-management rail (frozen §G, clarification §1.8). It accepts
// the same bounded filter as the administrator query and reuses the same
// AdminRequestLog shape, but applies the steward de-privacy projection
// (stewardLogSelectColumns): charity rows never reveal the donor's endpoint
// key id, base URL or upstream model id. The steward therefore sees the same
// metadata the administrator sees for personal rows, and for charity rows sees
// only the consumer's identity, route kind, status, tokens and bounded
// diagnostic — never the donated resource that served the call.
func (s *Store) QueryStewardRequestLogs(ctx context.Context, query AdminLogQuery) ([]AdminRequestLog, bool, error) {
	if err := query.validate(); err != nil {
		return nil, false, err
	}
	page, pageSize := clampLogPage(query.Page, query.PageSize)

	var clauses []string
	var args []any
	if query.UserID > 0 {
		clauses = append(clauses, "rl.user_id = ?")
		args = append(args, query.UserID)
	}
	if query.EndpointBaseURL != "" {
		clauses = append(clauses, "rl.endpoint_base_url = ?")
		args = append(args, query.EndpointBaseURL)
	}
	if query.UpstreamModel != "" {
		clauses = append(clauses, "rl.upstream_model_id = ?")
		args = append(args, query.UpstreamModel)
	}
	if query.ErrorCode != "" {
		clauses = append(clauses, "rl.error_code = ?")
		args = append(args, query.ErrorCode)
	}
	if query.Status != 0 {
		clauses = append(clauses, "rl.status_code = ?")
		args = append(args, query.Status)
	}
	if query.FromUnix > 0 {
		clauses = append(clauses, "rl.started_at >= ?")
		args = append(args, query.FromUnix)
	}
	if query.ToUnix > 0 {
		clauses = append(clauses, "rl.started_at < ?")
		args = append(args, query.ToUnix)
	}

	sqlText := `SELECT ` + stewardLogSelectColumns + ` FROM request_logs rl`
	if len(clauses) > 0 {
		// #nosec G202 -- clauses is assembled exclusively from the fixed predicates
		// above and every filter value is passed separately in args.
		sqlText += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	sqlText += ` ORDER BY rl.id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query steward request logs: %w", err)
	}
	defer rows.Close()

	logs := make([]AdminRequestLog, 0, min(pageSize, 32))
	for rows.Next() {
		log, err := scanAdminRequestLog(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan steward request log: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate steward request logs: %w", err)
	}
	hasMore := len(logs) > pageSize
	if hasMore {
		logs = logs[:pageSize]
	}
	return logs, hasMore, nil
}

// UserLogQuery is the bounded, parameterized owner-facing log filter. Frozen
// semantics: Model is exact equality against the user's own platform model
// full_name (candidates come from the bounded options endpoint, which the
// client may fuzzy-search locally); ErrorCode/Status/FromUnix/ToUnix follow
// the same rules as AdminLogQuery.
type UserLogQuery struct {
	Model     string
	ErrorCode string
	Status    int
	FromUnix  int64
	ToUnix    int64
	Page      int
	PageSize  int
}

func (q UserLogQuery) validate() error {
	if !validOptionalStoredText(q.Model, maxLogFilterTextRunes) {
		return fmt.Errorf("user log query: model filter is invalid")
	}
	if !validErrorCodeFilter(q.ErrorCode) {
		return fmt.Errorf("user log query: error code filter is invalid")
	}
	if q.Status != 0 && (q.Status < 100 || q.Status > maxLogStatus) {
		return fmt.Errorf("user log query: status is out of range")
	}
	if q.FromUnix < 0 || q.ToUnix < 0 || (q.FromUnix > 0 && q.ToUnix > 0 && q.FromUnix > q.ToUnix) {
		return fmt.Errorf("user log query: time range is invalid")
	}
	return nil
}

// QueryUserRequestLogs returns at most one page of the given user's own
// metadata-only log rows, newest first. Ownership is part of the SQL: the
// user_id predicate gates every row and the note JOIN re-checks endpoint
// ownership, so no other user's data can ever enter the result.
func (s *Store) QueryUserRequestLogs(ctx context.Context, userID int64, query UserLogQuery) ([]UserRequestLog, bool, error) {
	if userID <= 0 {
		return nil, false, ErrNotFound
	}
	if err := query.validate(); err != nil {
		return nil, false, err
	}
	page, pageSize := clampLogPage(query.Page, query.PageSize)

	// The user_id predicate is first in both the SQL and the args; the
	// optional filter clauses follow in the same order they are appended.
	var clauses []string
	args := []any{userID}
	if query.Model != "" {
		clauses = append(clauses, "rl.model = ?")
		args = append(args, query.Model)
	}
	if query.ErrorCode != "" {
		clauses = append(clauses, "rl.error_code = ?")
		args = append(args, query.ErrorCode)
	}
	if query.Status != 0 {
		clauses = append(clauses, "rl.status_code = ?")
		args = append(args, query.Status)
	}
	if query.FromUnix > 0 {
		clauses = append(clauses, "rl.started_at >= ?")
		args = append(args, query.FromUnix)
	}
	if query.ToUnix > 0 {
		clauses = append(clauses, "rl.started_at < ?")
		args = append(args, query.ToUnix)
	}

	// The note JOIN is ownership-scoped: ek/e rows are only reachable when the
	// endpoint belongs to the logging user, and a deleted key/endpoint simply
	// yields empty notes (LEFT JOIN + COALESCE). Notes are current values by
	// design; endpoint_base_url is the durable dispatch snapshot column.
	//
	// Charity rows (route_kind='charity') carry the DONOR's endpoint/key as the
	// dispatch target; the consumer must never learn donor resources from their
	// own log projection (frozen §7). The CASE expressions blank the donor's
	// endpoint key id, base URL, and upstream model id for charity rows; the
	// admin station reads the authoritative reservation for that correlation.
	sqlText := `SELECT rl.id, rl.route_kind, rl.model,
		CASE WHEN rl.route_kind='charity' THEN 0 ELSE rl.endpoint_key_id END,
		COALESCE(ek.note, ''), COALESCE(e.note, ''),
		CASE WHEN rl.route_kind='charity' THEN '' ELSE rl.endpoint_base_url END,
		CASE WHEN rl.route_kind='charity' THEN '' ELSE rl.upstream_model_id END,
		rl.status_code, rl.duration_ms,
		rl.started_at, rl.completed_at,
		rl.uncached_input_tokens, rl.cache_write_input_tokens, rl.cache_read_input_tokens, rl.output_tokens,
		rl.prompt_tokens, rl.completion_tokens, rl.total_tokens,
		rl.usage_unknown, rl.error_code, rl.error_source, rl.error_diag, rl.attempt_id
	FROM request_logs rl
	LEFT JOIN endpoint_keys ek ON ek.id = rl.endpoint_key_id
	LEFT JOIN endpoints e ON e.id = ek.endpoint_id AND e.user_id = rl.user_id
	WHERE rl.user_id = ?`
	if len(clauses) > 0 {
		// #nosec G202 -- clauses is assembled exclusively from the fixed predicates
		// above and every filter value is passed separately in args.
		sqlText += ` AND ` + strings.Join(clauses, " AND ")
	}
	sqlText += ` ORDER BY rl.id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query user request logs: %w", err)
	}
	defer rows.Close()

	logs := make([]UserRequestLog, 0, min(pageSize, 32))
	for rows.Next() {
		log, err := scanUserRequestLog(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan user request log: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate user request logs: %w", err)
	}
	hasMore := len(logs) > pageSize
	if hasMore {
		logs = logs[:pageSize]
	}
	return logs, hasMore, nil
}

// ListUserLogModelOptions returns the bounded candidate list for the user's
// model filter dropdown: the distinct non-empty platform model names appearing
// in the requester's own retained logs (30-day retention applies implicitly),
// ordered ascending, capped at MaxLogModelOptions. Fuzzy matching happens on
// the client over this bounded server-authoritative list; the server never
// performs substring search over the log table.
func (s *Store) ListUserLogModelOptions(ctx context.Context, userID int64) ([]string, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT model FROM request_logs
WHERE user_id = ? AND model <> ''
ORDER BY model ASC
LIMIT ?`, userID, MaxLogModelOptions)
	if err != nil {
		return nil, fmt.Errorf("list user log model options: %w", err)
	}
	defer rows.Close()

	models := make([]string, 0, MaxLogModelOptions)
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, fmt.Errorf("scan user log model option: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user log model options: %w", err)
	}
	return models, nil
}

// ExportAdminRequestLogs returns every log row matching the frozen
// administrator filter, ordered id ASC (stable oldest-first export order),
// without pagination. The selection is bounded by MaxLogExportRows: a larger
// selection fails closed with ErrLogExportTooLarge instead of being silently
// truncated. Page/PageSize are ignored by design.
func (s *Store) ExportAdminRequestLogs(ctx context.Context, query AdminLogQuery) ([]AdminRequestLog, error) {
	if err := query.validate(); err != nil {
		return nil, err
	}
	query.Page = 1
	query.PageSize = MaxLogExportRows

	var clauses []string
	var args []any
	if query.UserID > 0 {
		clauses = append(clauses, "rl.user_id = ?")
		args = append(args, query.UserID)
	}
	if query.EndpointBaseURL != "" {
		clauses = append(clauses, "rl.endpoint_base_url = ?")
		args = append(args, query.EndpointBaseURL)
	}
	if query.UpstreamModel != "" {
		clauses = append(clauses, "rl.upstream_model_id = ?")
		args = append(args, query.UpstreamModel)
	}
	if query.ErrorCode != "" {
		clauses = append(clauses, "rl.error_code = ?")
		args = append(args, query.ErrorCode)
	}
	if query.Status != 0 {
		clauses = append(clauses, "rl.status_code = ?")
		args = append(args, query.Status)
	}
	if query.FromUnix > 0 {
		clauses = append(clauses, "rl.started_at >= ?")
		args = append(args, query.FromUnix)
	}
	if query.ToUnix > 0 {
		clauses = append(clauses, "rl.started_at < ?")
		args = append(args, query.ToUnix)
	}

	sqlText := `SELECT ` + adminLogSelectColumns + ` FROM request_logs rl`
	if len(clauses) > 0 {
		// #nosec G202 -- clauses is assembled exclusively from the fixed predicates
		// above and every filter value is passed separately in args.
		sqlText += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	// One extra row detects an over-bound selection without buffering it all.
	sqlText += ` ORDER BY rl.id ASC LIMIT ?`
	args = append(args, MaxLogExportRows+1)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("export admin request logs: %w", err)
	}
	defer rows.Close()

	logs := make([]AdminRequestLog, 0, MaxLogExportRows)
	for rows.Next() {
		log, err := scanAdminRequestLog(rows)
		if err != nil {
			return nil, fmt.Errorf("scan admin request log export: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin request log export: %w", err)
	}
	if len(logs) > MaxLogExportRows {
		return nil, ErrLogExportTooLarge
	}
	return logs, nil
}
