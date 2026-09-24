package observability

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

var (
	ErrInvalid     = errors.New("observability: invalid input")
	ErrUnavailable = errors.New("observability: unavailable")
)

const RetentionSeconds int64 = 30 * 24 * 60 * 60

type Repository struct {
	db  *sql.DB
	now func() time.Time
}

func NewRepository(database *sql.DB) (*Repository, error) {
	if database == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, now: time.Now}, nil
}

func (r *Repository) RecordSourceTx(ctx context.Context, tx *sql.Tx, requestID string, userID int64, kind string, at int64) error {
	source, ok := SourceFromContext(ctx)
	if !ok {
		return nil
	}
	if tx == nil || !db.ValidateOpaqueID(requestID, "req_") || userID <= 0 || at < 0 || (kind != "self" && kind != "charity" && kind != "unclassified" && kind != "discovery") {
		return ErrInvalid
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return ErrInvalid
	}
	if _, err = ParseSource(encoded); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at)
		SELECT id,user_id,?,?,?,?,? FROM request_logs WHERE logical_request_id=? AND user_id=?
		ON CONFLICT(request_log_id) DO UPDATE SET kind=excluded.kind WHERE request_source_facts.kind='unclassified'`, kind, source.EffectiveIP, source.IPQuality, string(encoded), at, requestID, userID)
	return err
}

type DiagnosticRef struct {
	RequestID, TaskID, OperationID string
	AttemptSeq                     int
	EventSeq                       int
}

func (ref DiagnosticRef) valid() bool {
	n := 0
	if ref.RequestID != "" {
		if !db.ValidateOpaqueID(ref.RequestID, "req_") {
			return false
		}
		n++
	}
	if ref.TaskID != "" {
		if !db.ValidateOpaqueID(ref.TaskID, "img_") {
			return false
		}
		n++
	}
	if ref.OperationID != "" {
		if !db.ValidateOpaqueID(ref.OperationID, "op_") {
			return false
		}
		n++
	}
	return n == 1 && ref.AttemptSeq > 0 && ref.EventSeq >= 0
}

// ErrorScope binds a private sink to one existing subject. It does not add raw
// diagnostics to the public connector result or ordinary request context facts.
func (r *Repository) ErrorScope(ctx context.Context, ref DiagnosticRef) context.Context {
	if r == nil || !ref.valid() {
		return ctx
	}
	var sequence atomic.Int64
	sequence.Store(int64(ref.EventSeq))
	return upstreamerror.WithCapture(ctx, func(ctx context.Context, event upstreamerror.Event) {
		seq := sequence.Add(1)
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := r.recordError(writeCtx, ref, seq, event); err != nil && writeCtx.Err() == nil {
			_ = r.recordError(writeCtx, ref, seq, event.WithoutBody())
		}
	})
}

func (r *Repository) DiscoveryScope(ctx context.Context, requestID string, attempt int) context.Context {
	return r.ErrorScope(ctx, DiagnosticRef{RequestID: requestID, AttemptSeq: attempt})
}

func (r *Repository) recordError(ctx context.Context, ref DiagnosticRef, seq int64, event upstreamerror.Event) error {
	if !ref.valid() || seq < 1 || len(event.Bytes()) > upstreamerror.MaxRawBodyBytes {
		return ErrInvalid
	}
	at := r.now().Unix()
	if at < 0 || at > 253402300799-RetentionSeconds {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The first statement acquires the write reservation before reading counters.
	if _, err = tx.ExecContext(ctx, `UPDATE observability_state SET raw_body_bytes=raw_body_bytes WHERE id=1`); err != nil {
		return err
	}
	var logID any
	var taskID any
	var operationID any
	if ref.RequestID != "" {
		var id int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM request_logs WHERE logical_request_id=?`, ref.RequestID).Scan(&id); err != nil {
			return err
		}
		logID = id
	} else if ref.TaskID != "" {
		var live bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM image_activity_tasks WHERE id=? AND user_id IS NOT NULL)`, ref.TaskID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return nil
		}
		taskID = ref.TaskID
	} else {
		operationID = ref.OperationID
	}
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM request_error_bodies WHERE request_log_id IS ? AND task_id IS ? AND operation_id IS ? AND attempt_seq=? AND event_seq=?`, logID, taskID, operationID, ref.AttemptSeq, seq).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	var rawBudget string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='request_error_body_budget_mib'`).Scan(&rawBudget); err != nil {
		return err
	}
	budget, err := strconv.ParseInt(rawBudget, 10, 64)
	if err != nil || budget < 1 || budget > 65536 {
		return ErrUnavailable
	}
	budget *= 1 << 20
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT raw_body_bytes FROM observability_state WHERE id=1`).Scan(&used); err != nil {
		return err
	}
	state := "saved"
	var body any = event.Bytes()
	size := int64(len(event.Bytes()))
	if body == nil || size == 0 {
		body = []byte{}
	}
	if event.Unavailable() {
		state = "unavailable"
		body = nil
		size = 0
	} else if size > budget-used {
		state = "capacity_exhausted"
		body = nil
		size = 0
	}
	var status any
	if event.Status() != 0 {
		status = event.Status()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO request_error_bodies(request_log_id,task_id,operation_id,attempt_seq,event_seq,http_status,content_type,body,bytes_saved,truncated,save_state,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, logID, taskID, operationID, ref.AttemptSeq, seq, status, event.ContentType(), body, size, event.Truncated(), state, at, at+RetentionSeconds); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE observability_state SET raw_body_bytes=raw_body_bytes+?,raw_capacity_omissions=raw_capacity_omissions+?,raw_unavailable=raw_unavailable+? WHERE id=1`, size, boolInt(state == "capacity_exhausted"), boolInt(state == "unavailable")); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// FinalizeRequestTx replaces provisional raw expiry metadata with the ordinary
// log-root deadline. Actual deletion and legal holds stay with that root.
func FinalizeRequestTx(ctx context.Context, tx *sql.Tx, requestID string, completedAt int64) error {
	if tx == nil || !db.ValidateOpaqueID(requestID, "req_") || completedAt < 0 || completedAt > 253402300799-RetentionSeconds {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `UPDATE request_error_bodies SET expires_at=? WHERE request_log_id=(SELECT id FROM request_logs WHERE logical_request_id=?)`, completedAt+RetentionSeconds, requestID)
	return err
}

// ReconcileCounters is a startup maintenance operation, never a request hook.
func (r *Repository) ReconcileCounters(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE observability_state SET raw_body_bytes=(SELECT COALESCE(SUM(bytes_saved),0) FROM request_error_bodies),access_rows=(SELECT count(*) FROM audit_access_events) WHERE id=1`)
	return err
}

type ErrorMetadata struct {
	EventSeq    int64  `json:"event_seq"`
	HTTPStatus  *int   `json:"http_status"`
	ContentType string `json:"content_type"`
	BytesSaved  int64  `json:"bytes_saved"`
	Truncated   bool   `json:"truncated"`
	SaveState   string `json:"save_state"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
}
type ErrorPage struct {
	Data      []ErrorMetadata `json:"data"`
	NextAfter *int64          `json:"next_after"`
}
type ErrorBody struct {
	ErrorMetadata
	Encoding string `json:"encoding"`
	Body     string `json:"body"`
}

// ListErrorsTx and ErrorBodyTx require the caller to authorize the log root in
// this same transaction, including any expired held-object authorization.
func ListErrorsTx(ctx context.Context, tx *sql.Tx, logID int64, attempt int, after int64) (ErrorPage, error) {
	page := ErrorPage{Data: make([]ErrorMetadata, 0)}
	if tx == nil || logID <= 0 || attempt < 1 || after < 0 {
		return page, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_seq,http_status,content_type,bytes_saved,truncated,save_state,created_at,expires_at FROM request_error_bodies WHERE request_log_id=? AND attempt_seq=? AND event_seq>? ORDER BY event_seq LIMIT 21`, logID, attempt, after)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ErrorMetadata
		var status sql.NullInt64
		if err = rows.Scan(&item.EventSeq, &status, &item.ContentType, &item.BytesSaved, &item.Truncated, &item.SaveState, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return page, err
		}
		if status.Valid {
			v := int(status.Int64)
			item.HTTPStatus = &v
		}
		if len(page.Data) == 20 {
			last := page.Data[19].EventSeq
			page.NextAfter = &last
			break
		}
		page.Data = append(page.Data, item)
	}
	return page, rows.Err()
}

func ErrorBodyTx(ctx context.Context, tx *sql.Tx, logID int64, attempt int, event int64) (ErrorBody, error) {
	var item ErrorBody
	var status sql.NullInt64
	var body []byte
	if tx == nil || logID <= 0 || attempt < 1 || event < 1 {
		return item, ErrInvalid
	}
	err := tx.QueryRowContext(ctx, `SELECT event_seq,http_status,content_type,bytes_saved,truncated,save_state,created_at,expires_at,body FROM request_error_bodies WHERE request_log_id=? AND attempt_seq=? AND event_seq=?`, logID, attempt, event).Scan(&item.EventSeq, &status, &item.ContentType, &item.BytesSaved, &item.Truncated, &item.SaveState, &item.CreatedAt, &item.ExpiresAt, &body)
	defer clear(body)
	if err != nil {
		return item, err
	}
	if status.Valid {
		v := int(status.Int64)
		item.HTTPStatus = &v
	}
	if len(body) > upstreamerror.MaxRawBodyBytes {
		return ErrorBody{}, ErrUnavailable
	}
	item.Encoding = "utf-8"
	if utf8.Valid(body) {
		item.Body = string(body)
	} else {
		item.Encoding = "base64"
		item.Body = base64.StdEncoding.EncodeToString(body)
	}
	return item, nil
}

func SourceTx(ctx context.Context, tx *sql.Tx, logID int64) (*Source, error) {
	var encoded string
	err := tx.QueryRowContext(ctx, `SELECT source_json FROM request_source_facts WHERE request_log_id=?`, logID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	source, err := ParseSource([]byte(encoded))
	if err != nil {
		return nil, err
	}
	return &source, nil
}
