package observability

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/textprivacy"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
	"strconv"
	"time"
	"unicode/utf8"
)

var (
	ErrDiagnosticForbidden = errors.New("observability: diagnostic access forbidden")
	ErrDiagnosticNotFound  = errors.New("observability: diagnostic not found")
)

const imageDiagnosticMIME = "application/vnd.nonbiriapi.image-diagnostic+json"

type DiagnosticFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
	AuthorizeStewardLogRead(context.Context, *sql.Tx, int64) (redactUpstreamIPv4 bool, err error)
}
type DiagnosticReaderConfig struct {
	Database  *sql.DB
	FinalAuth DiagnosticFinalAuthorizer
	Now       func() time.Time
}
type DiagnosticReader struct{ config DiagnosticReaderConfig }
type DiagnosticActor struct {
	UserID int64
	Admin  bool
}
type DiagnosticFilter struct {
	Storage   string
	Kind      string
	UserID    int64
	SubjectID string
	From, To  int64
	Before    string
	Limit     int
}
type DiagnosticItem struct {
	ErrorMetadata
	ID         string              `json:"id"`
	Kind       string              `json:"kind"`
	SubjectID  string              `json:"subject_id"`
	UserID     string              `json:"user_id"`
	AttemptSeq int                 `json:"attempt_seq"`
	Synthetic  bool                `json:"synthetic"`
	Dispatch   *DiagnosticDispatch `json:"dispatch,omitempty"`
}
type DiagnosticDispatch struct {
	Method       string `json:"method"`
	URL          string `json:"url"`
	RequestBody  string `json:"request_body"`
	ContentType  string `json:"content_type"`
	DispatchedAt int64  `json:"dispatched_at"`
}
type DiagnosticPage struct {
	Data       []DiagnosticItem `json:"data"`
	NextBefore *string          `json:"next_before"`
	From       int64            `json:"from"`
	To         int64            `json:"to"`
}
type DiagnosticDetail struct {
	ErrorDiagnostics *diagnostic.Failure `json:"error_diagnostics,omitempty"`
	Item             DiagnosticItem      `json:"item"`
	Body             ErrorBody           `json:"body"`
	Source           *Source             `json:"source"`
}

func NewDiagnosticReader(config DiagnosticReaderConfig) (*DiagnosticReader, error) {
	if config.Database == nil || config.FinalAuth == nil {
		return nil, ErrInvalid
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &DiagnosticReader{config: config}, nil
}
func diagnosticNumber(value string, zero bool) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || (!zero && n == 0) || strconv.FormatInt(n, 10) != value {
		return 0, ErrInvalid
	}
	return n, nil
}
func diagnosticKind(kind string) bool {
	return kind == "all" || kind == "api_request" || kind == "model_discovery" || kind == "image_task" || kind == "image_discovery"
}
func normalizeDiagnosticFilter(f DiagnosticFilter, now int64) (DiagnosticFilter, error) {
	if now < 0 || now >= 253402300799 {
		return f, ErrInvalid
	}
	if f.Kind == "" {
		f.Kind = "all"
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.From == 0 {
		f.From = now - RetentionSeconds + 1
		if f.From < 0 {
			f.From = 0
		}
	}
	if f.To == 0 {
		f.To = now + 1
	}
	if !validStorageFilter(f.Storage) || !diagnosticKind(f.Kind) || f.Limit < 1 || f.Limit > 20 || f.UserID < 0 || f.From < 0 || f.To <= f.From || f.To-f.From > RetentionSeconds || f.To > now+1 {
		return f, ErrInvalid
	}
	if f.From < now-RetentionSeconds {
		f.From = now - RetentionSeconds
	}
	if f.To <= f.From {
		return f, ErrInvalid
	}
	if f.Before != "" {
		if _, err := diagnosticNumber(f.Before, false); err != nil {
			return f, err
		}
	}
	if f.SubjectID != "" && !(db.ValidateOpaqueID(f.SubjectID, "req_") || db.ValidateOpaqueID(f.SubjectID, "img_") || db.ValidateOpaqueID(f.SubjectID, "op_")) {
		return f, ErrInvalid
	}
	return f, nil
}
func (r *DiagnosticReader) begin(ctx context.Context, actor DiagnosticActor) (*sql.Tx, bool, error) {
	if r == nil || ctx == nil || actor.UserID <= 0 {
		return nil, false, ErrDiagnosticForbidden
	}
	if _, ok := authz.StewardCallerFromContext(ctx); ok {
		return nil, false, ErrDiagnosticForbidden
	}
	tx, err := r.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, ErrUnavailable
	}
	redact := false
	if actor.Admin {
		err = r.config.FinalAuth.AuthorizeAdmin(ctx, tx, actor.UserID)
	} else {
		redact, err = r.config.FinalAuth.AuthorizeStewardLogRead(ctx, tx, actor.UserID)
	}
	if err != nil {
		tx.Rollback()
		if errors.Is(err, authz.ErrMFARequired) || errors.Is(err, authz.ErrPermissionDenied) {
			return nil, false, err
		}
		if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden) {
			return nil, false, ErrDiagnosticForbidden
		}
		return nil, false, ErrUnavailable
	}
	return tx, redact, nil
}

// Each subject is joined through its actual foreign key. An operation must
// identify an image refresh, and anonymized task owners never reappear.
const diagnosticJoins = `
 FROM request_error_bodies e
 LEFT JOIN request_logs l ON l.id=e.request_log_id
 LEFT JOIN image_activity_tasks t ON t.id=e.task_id
 LEFT JOIN accepted_operations o ON o.id=e.operation_id
 LEFT JOIN image_model_refreshes f ON f.operation_id=o.id
 LEFT JOIN image_discovery_dispatches d ON d.operation_id=f.operation_id
`
const diagnosticLive = `
 e.expires_at>? AND (
 ((l.route_kind='model_discovery' OR (l.id IS NOT NULL AND e.save_state<>'saved')) AND l.user_id IS NOT NULL AND EXISTS(SELECT 1 FROM users u WHERE u.id=l.user_id) AND (l.completed_at IS NULL OR l.completed_at>?))
 OR (t.user_id IS NOT NULL AND t.finance_state<>'deleted' AND EXISTS(SELECT 1 FROM users u WHERE u.id=t.user_id) AND (t.completed_at IS NULL OR t.completed_at>?))
 OR (o.kind='image_model_discovery' AND o.actor_user_id IS NOT NULL AND f.operation_id IS NOT NULL AND EXISTS(SELECT 1 FROM users u WHERE u.id=o.actor_user_id) AND (f.completed_at IS NULL OR f.completed_at>?))
 )`
const diagnosticKindExpr = "CASE WHEN l.route_kind='model_discovery' THEN 'model_discovery' WHEN l.id IS NOT NULL THEN 'api_request' WHEN t.id IS NOT NULL THEN 'image_task' ELSE 'image_discovery' END"
const diagnosticSubjectExpr = "COALESCE(l.logical_request_id,t.id,o.id)"
const diagnosticUserExpr = "COALESCE(l.user_id,t.user_id,o.actor_user_id)"
const diagnosticColumns = "e.id," + diagnosticKindExpr + "," + diagnosticSubjectExpr + "," + diagnosticUserExpr + ",e.attempt_seq,e.event_seq,e.http_status,e.content_type,e.bytes_saved,e.truncated,e.save_state,e.failure_reason,e.created_at,e.expires_at,d.method,d.url,d.request_body,d.request_content_type,d.dispatched_at"

type diagnosticScanner interface{ Scan(...any) error }

func scanDiagnostic(row diagnosticScanner, body *[]byte) (DiagnosticItem, error) {
	var out DiagnosticItem
	var id, user int64
	var status sql.NullInt64
	var method, dispatchURL, requestBody, requestContentType sql.NullString
	var dispatchedAt sql.NullInt64
	values := []any{&id, &out.Kind, &out.SubjectID, &user, &out.AttemptSeq, &out.EventSeq, &status, &out.ContentType, &out.BytesSaved, &out.Truncated, &out.SaveState, &out.FailureReason, &out.CreatedAt, &out.ExpiresAt, &method, &dispatchURL, &requestBody, &requestContentType, &dispatchedAt}
	if body != nil {
		values = append(values, body)
	}
	if err := row.Scan(values...); err != nil {
		return out, err
	}
	out.ID = strconv.FormatInt(id, 10)
	out.UserID = strconv.FormatInt(user, 10)
	if status.Valid {
		v := int(status.Int64)
		out.HTTPStatus = &v
	}
	out.Synthetic = out.ContentType == imageDiagnosticMIME
	if method.Valid && dispatchURL.Valid && dispatchedAt.Valid {
		out.Dispatch = &DiagnosticDispatch{Method: method.String, URL: dispatchURL.String, RequestBody: requestBody.String, ContentType: requestContentType.String, DispatchedAt: dispatchedAt.Int64}
	}
	return out, nil
}
func (r *DiagnosticReader) List(ctx context.Context, actor DiagnosticActor, filter DiagnosticFilter) (DiagnosticPage, error) {
	out := DiagnosticPage{Data: []DiagnosticItem{}}
	now := r.config.Now().Unix()
	f, err := normalizeDiagnosticFilter(filter, now)
	if err != nil {
		return out, err
	}
	out.From, out.To = f.From, f.To
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, _, err := r.begin(ctx, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	query := "SELECT " + diagnosticColumns + diagnosticJoins + " WHERE " + diagnosticLive + " AND e.created_at>=? AND e.created_at<?"
	args := []any{now, now - RetentionSeconds, now - RetentionSeconds, now - RetentionSeconds, f.From, f.To}
	if f.Storage == "" {
		query += " AND (l.id IS NULL OR l.route_kind='model_discovery')"
	} else {
		query += " AND e.save_state<>'saved'"
		switch f.Storage {
		case "capacity_exhausted":
			query += " AND e.save_state='capacity_exhausted'"
		case "read_failure", "storage_failure", "unspecified":
			reason := f.Storage
			if reason == "unspecified" {
				reason = ""
			}
			query += " AND e.save_state='unavailable' AND e.failure_reason=?"
			args = append(args, reason)
		}
	}
	if f.Kind != "all" {
		query += " AND " + diagnosticKindExpr + "=?"
		args = append(args, f.Kind)
	}
	if f.UserID > 0 {
		query += " AND " + diagnosticUserExpr + "=?"
		args = append(args, f.UserID)
	}
	if f.SubjectID != "" {
		query += " AND " + diagnosticSubjectExpr + "=?"
		args = append(args, f.SubjectID)
	}
	if f.Before != "" {
		id, _ := diagnosticNumber(f.Before, false)
		query += " AND e.id<?"
		args = append(args, id)
	}
	query += " ORDER BY e.id DESC LIMIT ?"
	args = append(args, f.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, ErrUnavailable
	}
	for rows.Next() {
		item, e := scanDiagnostic(rows, nil)
		if e != nil {
			rows.Close()
			return out, ErrUnavailable
		}
		out.Data = append(out.Data, item)
	}
	if err = rows.Close(); err != nil {
		return out, ErrUnavailable
	}
	if err = rows.Err(); err != nil {
		return out, ErrUnavailable
	}
	if len(out.Data) > f.Limit {
		out.Data = out.Data[:f.Limit]
		next := out.Data[len(out.Data)-1].ID
		out.NextBefore = &next
	}
	if err = tx.Commit(); err != nil {
		return out, ErrUnavailable
	}
	return out, nil
}
func (r *DiagnosticReader) Detail(ctx context.Context, actor DiagnosticActor, id string) (DiagnosticDetail, error) {
	var out DiagnosticDetail
	rowID, err := diagnosticNumber(id, false)
	if err != nil {
		return out, err
	}
	now := r.config.Now().Unix()
	if now < 0 || now >= 253402300799 {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, redact, err := r.begin(ctx, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var raw []byte
	defer func() { clear(raw) }()
	out.Item, err = scanDiagnostic(tx.QueryRowContext(ctx, "SELECT "+diagnosticColumns+",e.body"+diagnosticJoins+" WHERE e.id=? AND "+diagnosticLive, rowID, now, now-RetentionSeconds, now-RetentionSeconds, now-RetentionSeconds), &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDiagnosticNotFound
	}
	if err != nil {
		return out, ErrUnavailable
	}
	if len(raw) > upstreamerror.MaxRawBodyBytes || int64(len(raw)) != out.Item.BytesSaved {
		return out, ErrUnavailable
	}
	var failure string
	if e := tx.QueryRowContext(ctx, "SELECT details FROM request_error_diagnostics WHERE error_id=?", rowID).Scan(&failure); e == nil {
		out.ErrorDiagnostics = diagnostic.Decode(failure)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	out.Body.ErrorMetadata = out.Item.ErrorMetadata
	out.Body.Encoding = "utf-8"
	if utf8.Valid(raw) {
		out.Body.Body = string(raw)
	} else {
		out.Body.Encoding = "base64"
		out.Body.Body = base64.StdEncoding.EncodeToString(raw)
	}
	if redact {
		dns, known, e := diagnosticDNSRedactor(ctx, tx, out.Item)
		if e != nil {
			return DiagnosticDetail{}, ErrUnavailable
		}
		diagnostic.Redact(out.ErrorDiagnostics, func(value string) string {
			return dns.Redact(textprivacy.IPv4(value))
		})
		// Classification and cause text are authored locally. Only correlation
		// values and the raw payload need concealment without a proven source.
		if !known && out.ErrorDiagnostics != nil {
			for i := range out.ErrorDiagnostics.Correlation {
				out.ErrorDiagnostics.Correlation[i].Value = "[redacted]"
			}
		}
		out.Body.IPv4Redacted = true
		if len(raw) > 0 {
			projected := "[redacted]"
			if known {
				projected = dns.Redact(textprivacy.IPv4(string(raw)))
			}
			if out.Body.Encoding == "base64" {
				out.Body.Body = base64.StdEncoding.EncodeToString([]byte(projected))
			} else {
				out.Body.Body = projected
			}
		}
	}
	var source string
	if out.Item.Kind == "model_discovery" || out.Item.Kind == "api_request" {
		err = tx.QueryRowContext(ctx, "SELECT s.source_json FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE l.logical_request_id=? AND s.user_id=l.user_id AND s.occurred_at>?", out.Item.SubjectID, now-RetentionSeconds).Scan(&source)
	} else if out.Item.Kind == "image_task" {
		err = tx.QueryRowContext(ctx, "SELECT s.source_json FROM image_task_sources s JOIN image_activity_tasks t ON t.id=s.task_id WHERE t.id=? AND s.user_id=t.user_id AND s.occurred_at>?", out.Item.SubjectID, now-RetentionSeconds).Scan(&source)
	} else {
		err = sql.ErrNoRows
	}
	if err == nil {
		parsed, e := ParseSource([]byte(source))
		if e != nil {
			return out, ErrUnavailable
		}
		out.Source = &parsed
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return out, ErrUnavailable
	}
	return out, nil
}

func validStorageFilter(value string) bool {
	switch value {
	case "", "missing", "capacity_exhausted", "read_failure", "storage_failure", "unspecified":
		return true
	}
	return false
}

// Resolve the already authorized event's immutable source. An unfinished
// request may use only the matching dispatched claim; later attempts and
// current image configuration cannot replace the event's source.
func diagnosticDNSRedactor(ctx context.Context, tx *sql.Tx, item DiagnosticItem) (*textprivacy.DNSRedactor, bool, error) {
	var query string
	args := []any{item.SubjectID}
	switch item.Kind {
	case "api_request", "model_discovery":
		query = `SELECT a.canonical_base_url FROM request_logs l
 JOIN request_attempts a ON a.request_log_id=l.id
 WHERE l.logical_request_id=? AND a.attempt_seq=?
 UNION ALL
 SELECT COALESCE(t.canonical_base_url,s.canonical_base_url) FROM request_logs l
 JOIN dispatch_claims c ON c.logical_request_id=l.logical_request_id AND c.state='dispatched'
 LEFT JOIN dispatch_claim_targets t ON t.claim_id=c.id
 LEFT JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
 WHERE l.logical_request_id=? AND c.attempt_seq=?
 AND NOT EXISTS(SELECT 1 FROM request_attempts a WHERE a.request_log_id=l.id AND a.attempt_seq=c.attempt_seq)
 LIMIT 1`
		args = []any{item.SubjectID, item.AttemptSeq, item.SubjectID, item.AttemptSeq}
	case "image_task":
		query = `SELECT COALESCE(d.source_base_url,r.base_url) FROM request_error_bodies e
 LEFT JOIN request_error_diagnostics d ON d.error_id=e.id
 JOIN image_activity_tasks t ON t.id=e.task_id
 LEFT JOIN image_upstream_revisions r ON r.revision=t.upstream_revision WHERE e.id=? AND t.id=?`
		args = []any{item.ID, item.SubjectID}
	case "image_discovery":
		query = `SELECT COALESCE(d.source_base_url,r.base_url) FROM request_error_bodies e
 LEFT JOIN request_error_diagnostics d ON d.error_id=e.id
 JOIN image_model_refreshes f ON f.operation_id=e.operation_id
 LEFT JOIN image_upstream_revisions r ON r.revision=f.upstream_revision WHERE e.id=? AND f.operation_id=?`
		args = []any{item.ID, item.SubjectID}
	default:
		return nil, false, nil
	}
	var base sql.NullString
	err := tx.QueryRowContext(ctx, query, args...).Scan(&base)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil || !base.Valid || base.String == "" {
		return nil, false, err
	}
	var mainstream bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM mainstream_channels m JOIN mainstream_channel_entries e ON e.channel_id=m.id
 WHERE m.state='active' AND m.enabled=1 AND e.enabled=1 AND e.canonical_base_url=?)`, base.String).Scan(&mainstream)
	if err != nil || mainstream {
		return nil, true, err
	}
	return textprivacy.NewDNSRedactor(base.String), true, nil
}
