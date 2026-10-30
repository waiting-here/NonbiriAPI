package diagnostic

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// OperationError is an administrator-only, sanitized account or catalogue
// failure. Cause is retained for classification but never serialized.
type OperationError struct {
	ID           string   `json:"id"`
	Operation    string   `json:"operation"`
	SourceName   string   `json:"source_name"`
	SiteStatus   int      `json:"site_status"`
	UpstreamCode string   `json:"upstream_code,omitempty"`
	Details      *Failure `json:"details"`
	Saved        bool     `json:"saved"`
	Cause        error    `json:"-"`
}

func (e *OperationError) Error() string { return "administrator operation failed: " + e.Details.Code }
func (e *OperationError) Unwrap() error { return e.Cause }

func OperationFailure(ctx context.Context, operation, source string, cause error) *OperationError {
	var previous *OperationError
	if errors.As(cause, &previous) {
		return previous
	}
	RecordError(ctx, "", cause)
	f := Finish(ctx, false, false, false, 0, "", "")
	var id [16]byte
	_, _ = rand.Read(id[:])
	status := http.StatusBadGateway
	switch f.Category {
	case "timeout":
		status = http.StatusGatewayTimeout
	case "canceled", "unavailable", "connection":
		status = http.StatusServiceUnavailable
	case "configuration":
		status = http.StatusBadRequest
	case "platform":
		status = http.StatusInternalServerError
	}
	if f.Code == "upstream_rate_limited" {
		status = http.StatusServiceUnavailable
	}
	return &OperationError{ID: hex.EncodeToString(id[:]), Operation: operation, SourceName: BoundTo(source, 200), SiteStatus: status, Details: f, Cause: cause}
}

// SaveOperation persists only the already-sanitized projection. A diagnostic
// write failure never replaces the original result. The bounded cleanup runs
// on writes and on the runtime's hourly maintenance tick.
func SaveOperation(ctx context.Context, database *sql.DB, value *OperationError) {
	if value == nil || database == nil || value.Saved {
		return
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	raw := Encode(value.Details)
	if raw == "" {
		return
	}
	_, err := database.ExecContext(bounded, "INSERT INTO agent_operation_diagnostics(id,operation,source_name,site_status,upstream_code,details,created_at) VALUES(?,?,?,?,?,?,?)", value.ID, value.Operation, value.SourceName, value.SiteStatus, value.UpstreamCode, raw, value.Details.StartedAt)
	value.Saved = err == nil
	if err == nil {
		_ = RetainOperations(bounded, database, time.Now())
	}
}
func RetainOperations(ctx context.Context, database *sql.DB, now time.Time) error {
	_, err := database.ExecContext(ctx, `DELETE FROM agent_operation_diagnostics WHERE id IN (
 SELECT id FROM agent_operation_diagnostics WHERE created_at<? ORDER BY created_at,id LIMIT 1000
 ) OR id IN (SELECT id FROM agent_operation_diagnostics ORDER BY created_at DESC,id DESC LIMIT 1000 OFFSET 1000)`, now.Add(-30*24*time.Hour).UnixMilli())
	return err
}
func ReadOperation(ctx context.Context, database *sql.DB, id string) (OperationError, error) {
	var value OperationError
	var raw string
	if len(id) != 32 {
		return value, sql.ErrNoRows
	}
	if _, err := hex.DecodeString(id); err != nil {
		return value, sql.ErrNoRows
	}
	err := database.QueryRowContext(ctx, "SELECT id,operation,source_name,site_status,upstream_code,details FROM agent_operation_diagnostics WHERE id=? AND created_at>=?", id, time.Now().Add(-30*24*time.Hour).UnixMilli()).Scan(&value.ID, &value.Operation, &value.SourceName, &value.SiteStatus, &value.UpstreamCode, &raw)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal([]byte(raw), &value.Details)
	value.Saved = true
	return value, err
}
