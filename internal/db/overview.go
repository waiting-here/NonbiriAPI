package db

// Admin overview projections: read-only metadata listings across all normal
// users for the administrator station. Neither projection ever selects,
// decrypts, or projects a secret or ciphertext column
// (endpoint_keys.encrypted_secret, caller_keys.key_hash); callers must gate
// both behind the admin-session boundary.

import (
	"context"
	"errors"
	"fmt"
)

// ErrOverviewLimit reports that an admin overview crossed the finite result
// bound; the repository never returns a partial global projection.
var ErrOverviewLimit = errors.New("admin overview exceeds its limit")

const MaxOverviewRows = 10000

// EndpointOverview is one endpoint's admin-station overview projection: its
// metadata across all users plus the live key count. The struct has no
// secret-bearing field by construction.
type EndpointOverview struct {
	ID            int64
	UserID        int64
	ConnectorType string
	BaseURL       string
	Note          string
	Enabled       bool
	KeyCount      int
	CreatedAt     int64
	UpdatedAt     int64
}

// ListEndpointsOverview returns every endpoint across all users with its live
// key count, ordered by id. The LEFT JOIN keeps zero-key endpoints visible
// with count 0; GROUP BY e.id is safe because id is the primary key (SQLite's
// bare-column rule). Read-only metadata projection.
func (s *Store) ListEndpointsOverview(ctx context.Context) ([]EndpointOverview, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id, e.user_id, e.connector_type, e.base_url, e.note, e.enabled, e.created_at, e.updated_at, COUNT(k.id)
FROM endpoints e
LEFT JOIN endpoint_keys k ON k.endpoint_id = e.id
GROUP BY e.id
ORDER BY e.id
LIMIT ?`, MaxOverviewRows+1)
	if err != nil {
		return nil, fmt.Errorf("list endpoints overview: %w", err)
	}
	defer rows.Close()

	out := make([]EndpointOverview, 0, 32)
	for rows.Next() {
		if len(out) == MaxOverviewRows {
			return nil, ErrOverviewLimit
		}
		var ep EndpointOverview
		var enabled int
		if err := rows.Scan(&ep.ID, &ep.UserID, &ep.ConnectorType, &ep.BaseURL, &ep.Note, &enabled, &ep.CreatedAt, &ep.UpdatedAt, &ep.KeyCount); err != nil {
			return nil, fmt.Errorf("scan endpoint overview: %w", err)
		}
		ep.Enabled = enabled != 0
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoints overview: %w", err)
	}
	return out, nil
}

// ModelOverview is one platform model's admin-station overview projection:
// its metadata across all users plus the live binding count.
type ModelOverview struct {
	ID            int64
	UserID        int64
	Provider      string
	Model         string
	FullName      string
	RouteStrategy string
	SilentRetry   bool
	BindingCount  int
	CreatedAt     int64
}

// ListModelsOverview returns every platform model across all users with its
// live binding count, ordered by id. The LEFT JOIN keeps zero-binding drafts
// visible with count 0. Read-only metadata projection.
func (s *Store) ListModelsOverview(ctx context.Context) ([]ModelOverview, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT m.id, m.user_id, m.provider, m.model, m.full_name, m.route_strategy, m.silent_retry, m.created_at, COUNT(b.id)
FROM models m
LEFT JOIN model_bindings b ON b.model_id = m.id
GROUP BY m.id
ORDER BY m.id
LIMIT ?`, MaxOverviewRows+1)
	if err != nil {
		return nil, fmt.Errorf("list models overview: %w", err)
	}
	defer rows.Close()

	out := make([]ModelOverview, 0, 32)
	for rows.Next() {
		if len(out) == MaxOverviewRows {
			return nil, ErrOverviewLimit
		}
		var m ModelOverview
		var silentRetry int
		if err := rows.Scan(&m.ID, &m.UserID, &m.Provider, &m.Model, &m.FullName, &m.RouteStrategy, &silentRetry, &m.CreatedAt, &m.BindingCount); err != nil {
			return nil, fmt.Errorf("scan model overview: %w", err)
		}
		m.SilentRetry = silentRetry != 0
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate models overview: %w", err)
	}
	return out, nil
}
