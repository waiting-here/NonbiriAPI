package observability

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

// Facts are already redacted and compressed before the caller's transaction.
// Missing contexts represent historical/recovery work, never an empty capture.
func RecordRequestHeadersTx(ctx context.Context, tx *sql.Tx, requestID string) error {
	return recordHeadersTx(ctx, tx, requestID, 0, headerfacts.Request(ctx))
}
func RecordResponseHeadersTx(ctx context.Context, tx *sql.Tx, requestID string, attempt int) error {
	return recordHeadersTx(ctx, tx, requestID, attempt, headerfacts.Response(ctx, requestID, attempt))
}
func recordHeadersTx(ctx context.Context, tx *sql.Tx, requestID string, attempt int, fact headerfacts.Fact) error {
	if fact.State() == "" {
		return nil
	}
	if tx == nil || !db.ValidateOpaqueID(requestID, "req_") || attempt < 0 || attempt > 100 {
		return ErrInvalid
	}
	var status any
	if fact.Status() != 0 {
		status = fact.Status()
	}
	// Insert from the live root so a late callback cannot recreate deleted facts.
	// The counters and budget decision use the same serialized write transaction.
	// Numbered parameters bind the blob once and keep this INSERT eligible
	// for the existing connection-local statement cache.
	_, err := tx.ExecContext(ctx, `INSERT INTO request_header_facts(request_log_id,attempt_seq,captured_at,http_status,save_state,encoded,bytes_saved)
 SELECT l.id,?4,?5,?6,CASE WHEN ?1='saved' AND ?3>CAST(c.value AS INTEGER)*1048576-s.header_bytes THEN 'capacity_exhausted' ELSE ?1 END,
 CASE WHEN ?1='saved' AND ?3<=CAST(c.value AS INTEGER)*1048576-s.header_bytes THEN ?2 ELSE NULL END,
 CASE WHEN ?1='saved' AND ?3<=CAST(c.value AS INTEGER)*1048576-s.header_bytes THEN ?3 ELSE 0 END
 FROM request_logs l,observability_state s,site_config c
 WHERE l.logical_request_id=?7 AND s.id=1 AND c.key='request_header_budget_mib'
 ON CONFLICT(request_log_id,attempt_seq) DO NOTHING`, fact.State(), fact, fact.BytesSaved(), attempt, fact.CapturedAt(), status, requestID)
	return err
}

// StoredHeaders is an opaque authorized read result; callers decode only after
// closing their transaction. Its formatting cannot leak encoded header data.
type StoredHeaders struct {
	state   string
	attempt int
	at      *int64
	status  *int
	data    []byte
}

func (StoredHeaders) String() string   { return "[private stored header facts]" }
func (StoredHeaders) GoString() string { return "[private stored header facts]" }
func (value StoredHeaders) Decode(redactIPv4 bool) (any, error) {
	if value.attempt > 0 {
		return headerfacts.DecodeCollection(value.state, value.data, redactIPv4)
	}
	return headerfacts.Decode(value.state, value.at, value.status, value.data, redactIPv4)
}

// HeadersTx requires final root authorization in this transaction, including
// retention/hold visibility. Header reads never authorize ordinary user logs.
func HeadersTx(ctx context.Context, tx *sql.Tx, logID int64, attempt int) (StoredHeaders, error) {
	value := StoredHeaders{state: "not_recorded", attempt: attempt}
	if tx == nil || logID <= 0 || attempt < 0 || attempt > 100 {
		return value, ErrInvalid
	}
	if attempt > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_claims c JOIN request_logs l ON l.logical_request_id=c.logical_request_id WHERE l.id=? AND c.attempt_seq=?) OR EXISTS(SELECT 1 FROM request_attempts WHERE request_log_id=? AND attempt_seq=?)`, logID, attempt, logID, attempt).Scan(&exists); err != nil {
			return value, err
		}
		if !exists {
			return value, sql.ErrNoRows
		}
	}
	var at int64
	var status sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT save_state,captured_at,http_status,encoded FROM request_header_facts WHERE request_log_id=? AND attempt_seq=?`, logID, attempt).Scan(&value.state, &at, &status, &value.data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	value.at = &at
	if status.Valid {
		v := int(status.Int64)
		value.status = &v
	}
	return value, nil
}
