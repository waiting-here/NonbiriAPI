// Export repository: ownership-scoped, bounded projections for the self-service
// account export rail. Every projection returns metadata only — no endpoint-key
// ciphertext, no OAuth token, no caller-key material, and no request/response
// content is ever selected. Collections are finite: a projection that crosses
// its limit fails closed (ErrExportLimit) instead of returning a partial set,
// so an export package can never grow without bound or silently drop rows.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrExportLimit reports that an export collection exceeded the finite bound.
// The export rail maps it to a stable payload_too_large response; nothing is
// ever returned partially.
var ErrExportLimit = errors.New("export collection exceeds its limit")

// ExportCollectionLimit caps every collection inside one export package. It
// is generous relative to every product cap (endpoints are capped by
// DefaultEndpointLimit; models/bindings have no count cap today), while still
// keeping the assembled package and the response bounded.
const ExportCollectionLimit = 10000

// LogSummaryWindow is the retention-visible window for the export's log
// summary: aggregates over the most recent window of request metadata.
const LogSummaryWindow = 30 * 24 * time.Hour

// ExportLogSummary is the aggregated metadata summary backing the export's
// log section. It never contains individual log rows or any upstream text.
type ExportLogSummary struct {
	TotalLogs        int64
	LogsLast30Days   int64
	ErrorLogs        int64
	UsageUnknownLogs int64
	AvgDurationMs    int64
}

// ListExportEndpoints returns up to limit endpoints owned by userID in id
// order. It projects the same public metadata as the endpoint list endpoint;
// no key material is involved.
func (s *Store) ListExportEndpoints(ctx context.Context, userID int64, limit int) ([]Endpoint, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at
FROM endpoints WHERE user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export endpoints: %w", err)
	}
	defer rows.Close()
	return scanExportEndpoints(rows, limit)
}

// ListExportEndpointKeys returns up to limit endpoint keys owned by userID in
// id order, joined through endpoints so ownership is enforced in SQL. Only
// metadata and display fragments are selected; encrypted_secret never enters
// the export path.
func (s *Store) ListExportEndpointKeys(ctx context.Context, userID int64, limit int) ([]EndpointKey, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT ek.id, ek.endpoint_id, ek.display_head, ek.display_tail, ek.note, ek.enabled, ek.created_at, ek.updated_at
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE e.user_id=?
ORDER BY ek.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export endpoint keys: %w", err)
	}
	defer rows.Close()
	return scanExportEndpointKeys(rows, limit)
}

// ListExportModels returns up to limit platform models owned by userID in id
// order, with their live binding counts. It is the bounded variant of the
// model list projection used by the export rail.
func (s *Store) ListExportModels(ctx context.Context, userID int64, limit int) ([]Model, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, modelListSQL+` LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export models: %w", err)
	}
	defer rows.Close()
	return scanExportModels(rows, limit)
}

// ListExportBindings returns up to limit model bindings owned by userID in
// (ord, id) order, joined through models so ownership is enforced in SQL.
func (s *Store) ListExportBindings(ctx context.Context, userID int64, limit int) ([]ModelBinding, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT b.id, b.model_id, b.endpoint_key_id, b.upstream_model_id, b.ord, b.created_at
FROM model_bindings b
JOIN models m ON b.model_id = m.id
WHERE m.user_id=?
ORDER BY b.ord, b.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export bindings: %w", err)
	}
	defer rows.Close()
	return scanExportBindings(rows, limit)
}

// ExportLogSummaryForUser aggregates the user's request-log metadata into one
// bounded summary row. A user with no logs yields an all-zero summary, never
// an error; a missing user yields ErrNotFound.
func (s *Store) ExportLogSummaryForUser(ctx context.Context, userID int64) (ExportLogSummary, error) {
	if userID <= 0 {
		return ExportLogSummary{}, ErrNotFound
	}
	var summary ExportLogSummary
	var avg float64
	cutoff := time.Now().Add(-LogSummaryWindow).Unix()
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN started_at >= ? THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(usage_unknown), 0),
       COALESCE(AVG(duration_ms), 0)
FROM request_logs WHERE user_id=?`, cutoff, userID).
		Scan(&summary.TotalLogs, &summary.LogsLast30Days, &summary.ErrorLogs, &summary.UsageUnknownLogs, &avg)
	if errors.Is(err, sql.ErrNoRows) {
		return ExportLogSummary{}, ErrNotFound
	}
	if err != nil {
		return ExportLogSummary{}, fmt.Errorf("export log summary: %w", err)
	}
	summary.AvgDurationMs = int64(avg)
	return summary, nil
}

func scanExportEndpoints(rows *sql.Rows, limit int) ([]Endpoint, error) {
	out := make([]Endpoint, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var ep Endpoint
		var enabledInt, fetchFailedInt int
		if err := rows.Scan(&ep.ID, &ep.UserID, &ep.ConnectorType, &ep.BaseURL, &ep.Note, &enabledInt, &fetchFailedInt, &ep.ModelFetchFailedAt, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan export endpoint: %w", err)
		}
		ep.Enabled = enabledInt != 0
		ep.ModelFetchFailed = fetchFailedInt != 0
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export endpoints: %w", err)
	}
	return out, nil
}

func scanExportEndpointKeys(rows *sql.Rows, limit int) ([]EndpointKey, error) {
	out := make([]EndpointKey, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var k EndpointKey
		var enabledInt int
		if err := rows.Scan(&k.ID, &k.EndpointID, &k.DisplayHead, &k.DisplayTail, &k.Note, &enabledInt, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan export endpoint key: %w", err)
		}
		k.Enabled = enabledInt != 0
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export endpoint keys: %w", err)
	}
	return out, nil
}

func scanExportModels(rows *sql.Rows, limit int) ([]Model, error) {
	out := make([]Model, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var m Model
		var count int
		var silentRetry int
		if err := rows.Scan(&m.ID, &m.UserID, &m.Provider, &m.Model, &m.FullName, &m.RouteStrategy,
			&silentRetry, &m.CreatedAt, &m.UpdatedAt, &count); err != nil {
			return nil, fmt.Errorf("scan export model: %w", err)
		}
		m.SilentRetry = silentRetry != 0
		m.BindingCount = count
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export models: %w", err)
	}
	return out, nil
}

func scanExportBindings(rows *sql.Rows, limit int) ([]ModelBinding, error) {
	out := make([]ModelBinding, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			return nil, ErrExportLimit
		}
		var b ModelBinding
		if err := rows.Scan(&b.ID, &b.ModelID, &b.EndpointKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan export binding: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export bindings: %w", err)
	}
	return out, nil
}
