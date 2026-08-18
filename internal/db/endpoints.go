package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Endpoint is a user's configured upstream endpoint. It is the trust root for
// model fetching and forwarding. The struct mirrors the endpoints row minus
// nothing sensitive: base_url is a canonicalized public URL the user supplied.
// ModelFetchFailed reports that the last upstream model fetch for one of this
// endpoint's keys failed; the flag is bounded state only (never a diagnostic
// or any upstream content).
type Endpoint struct {
	ID                 int64
	UserID             int64
	ConnectorType      string
	BaseURL            string
	Note               string
	Enabled            bool
	ModelFetchFailed   bool
	ModelFetchFailedAt int64
	CreatedAt          int64
	UpdatedAt          int64
}

// DefaultEndpointLimit is the fallback global endpoint-count cap used when the
// administrator has not yet set the default_endpoint_limit site_config key. It
// is an implementation-time default (the requirements leave the exact default
// to implementation time); an administrator overrides it at runtime via the
// site-config endpoint. It must be > 0 so a fresh install is usable.
const DefaultEndpointLimit = 50

const siteConfigKeyDefaultEndpointLimit = "default_endpoint_limit"

// queryRowContexter is the shared interface used by cap calculation so the
// same code runs both inside a write transaction (atomic cap check + insert)
// and for a standalone read.
type queryRowContexter interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CreateEndpoint inserts a new endpoint for userID after an atomic cap check.
// The cap is min(global default, per-user override); when the current endpoint
// count has already reached it, ErrEndpointCap is returned and no row is
// written. connectorType and baseURL must already be validated and
// canonicalized by the caller (the service layer uses the connector registry
// and the egress canonical validator); the repository only persists them.
// now is the caller-supplied unix timestamp so callers control the clock.
func (s *Store) CreateEndpoint(ctx context.Context, userID int64, connectorType, baseURL, note string, enabled bool, now int64) (Endpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin create endpoint: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	cap, err := endpointCapLocked(ctx, tx, userID)
	if err != nil {
		return Endpoint{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, userID).Scan(&count); err != nil {
		return Endpoint{}, fmt.Errorf("count endpoints: %w", err)
	}
	if count >= cap {
		return Endpoint{}, ErrEndpointCap
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO endpoints (user_id, connector_type, base_url, note, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		userID, connectorType, baseURL, note, enabledInt, now, now)
	if err != nil {
		return Endpoint{}, fmt.Errorf("insert endpoint: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Endpoint{}, fmt.Errorf("endpoint last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Endpoint{}, fmt.Errorf("commit create endpoint: %w", err)
	}
	committed = true

	return Endpoint{
		ID:            id,
		UserID:        userID,
		ConnectorType: connectorType,
		BaseURL:       baseURL,
		Note:          note,
		Enabled:       enabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// ListEndpoints returns every endpoint owned by userID, ordered by id. The
// query filters by user_id in SQL so a cross-user caller can never enumerate
// another user's endpoints.
func (s *Store) ListEndpoints(ctx context.Context, userID int64) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	defer rows.Close()
	return scanEndpoints(rows)
}

// GetEndpoint returns the endpoint with id owned by userID. A missing or
// cross-user id yields ErrNotFound; the two are indistinguishable.
func (s *Store) GetEndpoint(ctx context.Context, userID, id int64) (Endpoint, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	ep, err := scanEndpointRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Endpoint{}, ErrNotFound
		}
		return Endpoint{}, fmt.Errorf("get endpoint: %w", err)
	}
	return ep, nil
}

// UpdateEndpoint atomically updates the endpoint with id owned by userID. A
// nil argument leaves that field unchanged. A missing or cross-user id yields
// ErrNotFound. now updates updated_at. The caller must canonicalize baseURL
// (when non-nil) via the egress validator before calling.
func (s *Store) UpdateEndpoint(ctx context.Context, userID, id int64, baseURL *string, note *string, enabled *bool, now int64) (Endpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin update endpoint: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if baseURL != nil {
		sets = append(sets, "base_url=?")
		args = append(args, *baseURL)
	}
	if note != nil {
		sets = append(sets, "note=?")
		args = append(args, *note)
	}
	if enabled != nil {
		enabledInt := 0
		if *enabled {
			enabledInt = 1
		}
		sets = append(sets, "enabled=?")
		args = append(args, enabledInt)
	}
	if len(sets) == 0 {
		// No fields to change: still verify ownership and return the row so the
		// handler can echo the canonical endpoint without a separate round-trip.
		row := tx.QueryRowContext(ctx,
			`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE id=? AND user_id=?`, id, userID)
		ep, err := scanEndpointRow(row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Endpoint{}, ErrNotFound
			}
			return Endpoint{}, fmt.Errorf("read endpoint for update: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Endpoint{}, fmt.Errorf("commit noop update: %w", err)
		}
		committed = true
		return ep, nil
	}

	sets = append(sets, "updated_at=?")
	args = append(args, now)
	args = append(args, id, userID)
	// #nosec G202 -- sets is restricted to constant base_url/note/enabled/time
	// fragments selected above; all values and ownership keys are bound arguments.
	query := "UPDATE endpoints SET " + strings.Join(sets, ", ") + " WHERE id=? AND user_id=?"
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return Endpoint{}, fmt.Errorf("update endpoint: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Endpoint{}, fmt.Errorf("update endpoint rows affected: %w", err)
	}
	if affected == 0 {
		return Endpoint{}, ErrNotFound
	}

	row := tx.QueryRowContext(ctx,
		`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	ep, err := scanEndpointRow(row)
	if err != nil {
		return Endpoint{}, fmt.Errorf("read updated endpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Endpoint{}, fmt.Errorf("commit update endpoint: %w", err)
	}
	committed = true
	return ep, nil
}

// DeleteEndpoint deletes the endpoint with id owned by userID. Cascading
// foreign keys remove its endpoint_keys and (through them) their
// fetched_models and model_bindings, so routing candidates are invalidated
// immediately. A missing or cross-user id yields ErrNotFound.
func (s *Store) DeleteEndpoint(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete endpoint: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete endpoint rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// EndpointCap returns the effective endpoint-count cap for userID:
// min(global default, per-user override), where the global default is the
// default_endpoint_limit site_config value or DefaultEndpointLimit when unset.
// It is exported for handlers that surface the cap to the user and for tests.
func (s *Store) EndpointCap(ctx context.Context, userID int64) (int, error) {
	return endpointCapLocked(ctx, s.db, userID)
}

// CountEndpoints returns the number of endpoints owned by userID. It is
// exported for tests that assert the cap boundary.
func (s *Store) CountEndpoints(ctx context.Context, userID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count endpoints: %w", err)
	}
	return count, nil
}

// endpointCapLocked computes the effective cap. It accepts the same
// queryRowContexter interface as both *sql.DB and *sql.Tx so the cap check
// inside CreateEndpoint's transaction reads a consistent snapshot with the
// count that follows it.
func endpointCapLocked(ctx context.Context, q queryRowContexter, userID int64) (int, error) {
	var globalStr sql.NullString
	err := q.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, siteConfigKeyDefaultEndpointLimit).Scan(&globalStr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read default endpoint limit: %w", err)
	}
	global := DefaultEndpointLimit
	if globalStr.Valid {
		trimmed := strings.TrimSpace(globalStr.String)
		if trimmed == "" {
			return 0, ErrInvalidSiteConfig
		}
		n, perr := strconv.Atoi(trimmed)
		if perr != nil || n < 0 {
			return 0, ErrInvalidSiteConfig
		}
		global = n
	}

	var userLimit sql.NullInt64
	err = q.QueryRowContext(ctx, `SELECT endpoint_limit FROM users WHERE id=?`, userID).Scan(&userLimit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("read user endpoint limit: %w", err)
	}
	if !userLimit.Valid || userLimit.Int64 < 0 {
		return global, nil
	}
	user := int(userLimit.Int64)
	if user < global {
		return user, nil
	}
	return global, nil
}

func scanEndpointRow(row *sql.Row) (Endpoint, error) {
	var ep Endpoint
	var enabledInt, fetchFailedInt int
	err := row.Scan(&ep.ID, &ep.UserID, &ep.ConnectorType, &ep.BaseURL, &ep.Note, &enabledInt, &fetchFailedInt, &ep.ModelFetchFailedAt, &ep.CreatedAt, &ep.UpdatedAt)
	if err != nil {
		return Endpoint{}, err
	}
	ep.Enabled = enabledInt != 0
	ep.ModelFetchFailed = fetchFailedInt != 0
	return ep, nil
}

func scanEndpoints(rows *sql.Rows) ([]Endpoint, error) {
	var out []Endpoint
	for rows.Next() {
		var ep Endpoint
		var enabledInt, fetchFailedInt int
		if err := rows.Scan(&ep.ID, &ep.UserID, &ep.ConnectorType, &ep.BaseURL, &ep.Note, &enabledInt, &fetchFailedInt, &ep.ModelFetchFailedAt, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan endpoint: %w", err)
		}
		ep.Enabled = enabledInt != 0
		ep.ModelFetchFailed = fetchFailedInt != 0
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoints: %w", err)
	}
	return out, nil
}
