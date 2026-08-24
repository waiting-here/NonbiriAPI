package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// refuseActiveDonationRefsTx fails with ErrResourceInActiveDonation when any
// physical endpoint key produced by the given key-id query is referenced by a
// donation_keys row whose donation is pending or approved+enabled — i.e. a
// donation that currently holds (or is about to hold) claims on it. It must
// run inside the same transaction as the guarded deletion; the single SQLite
// writer serializes the pair, so no read-then-delete race exists.
func refuseActiveDonationRefsTx(ctx context.Context, tx *sql.Tx, keyIDQuery string, args ...any) error {
	query := `
SELECT COUNT(*) FROM donation_keys dk
JOIN donations d ON dk.donation_id = d.id
WHERE dk.endpoint_key_id IN (` + keyIDQuery + `)
  AND (d.status='pending' OR (d.status='approved' AND d.enabled=1))`
	var n int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return fmt.Errorf("active-donation guard: %w", err)
	}
	if n > 0 {
		return ErrResourceInActiveDonation
	}
	return nil
}

// classifyResourceRefusal maps the claims table's RESTRICT constraint (the
// backstop behind the in-use guard) to the stable sentinel; every other
// constraint failure stays wrapped for diagnostics.
func classifyResourceRefusal(err error) error {
	if isConstraintError(err) {
		return ErrResourceInActiveDonation
	}
	return fmt.Errorf("delete endpoint resource: %w", err)
}

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

// EndpointChangeMask reports which persisted or upstream-significant values
// actually changed in one atomic endpoint update. Overlapping bits are
// intentional: a path or origin change also carries EndpointChangeBaseURL.
type EndpointChangeMask uint8

const (
	EndpointChangeBaseURL EndpointChangeMask = 1 << iota
	EndpointChangeUpstreamPath
	EndpointChangeOrigin
	EndpointChangeNote
	EndpointChangeEnabled
)

// Has reports whether every bit in change is present.
func (m EndpointChangeMask) Has(change EndpointChangeMask) bool {
	return m&change == change
}

// ErrEndpointOriginConflict prevents a credential-bearing endpoint from being
// moved to a different canonical origin. It contains no URL or key material.
var ErrEndpointOriginConflict = errors.New("db: endpoint origin change conflicts with existing keys")

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
// The cap is the per-user override when non-NULL, otherwise the site default;
// when the current endpoint
// count has already reached it, a *CapError wrapping ErrEndpointCap is returned
// and no row is written. connectorType and baseURL must already be validated
// and canonicalized by the caller (the service layer uses the connector
// registry and the egress canonical validator); the repository only persists
// them. now is the caller-supplied unix timestamp so callers control the clock.
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
		return Endpoint{}, newCapError(ErrEndpointCap, ResourceEndpoint, cap)
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

// UpdateEndpoint atomically reads and updates the endpoint with id owned by
// userID and returns a mask of actual changes. A nil argument leaves that field
// unchanged. The transaction also checks key existence before any canonical
// origin change: when at least one key exists, the entire update fails with
// ErrEndpointOriginConflict. A missing or cross-user id yields ErrNotFound.
// The caller must validate and canonicalize baseURL through egress first.
func (s *Store) UpdateEndpoint(ctx context.Context, userID, id int64, baseURL *string, note *string, enabled *bool, now int64) (Endpoint, EndpointChangeMask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Endpoint{}, 0, fmt.Errorf("begin update endpoint: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRowContext(ctx,
		`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	current, err := scanEndpointRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Endpoint{}, 0, ErrNotFound
		}
		return Endpoint{}, 0, fmt.Errorf("read endpoint for update: %w", err)
	}

	var changes EndpointChangeMask
	if baseURL != nil && *baseURL != current.BaseURL {
		currentTarget, currentOrigin, parseErr := egress.CanonicalEndpointTarget(current.BaseURL)
		if parseErr != nil {
			return Endpoint{}, 0, fmt.Errorf("parse stored endpoint target: %w", parseErr)
		}
		nextTarget, nextOrigin, parseErr := egress.CanonicalEndpointTarget(*baseURL)
		if parseErr != nil {
			return Endpoint{}, 0, fmt.Errorf("parse updated endpoint target: %w", parseErr)
		}

		changes |= EndpointChangeBaseURL
		switch {
		case currentOrigin != nextOrigin:
			var hasKeys int
			if err := tx.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM endpoint_keys WHERE endpoint_id=?)`, id).Scan(&hasKeys); err != nil {
				return Endpoint{}, 0, fmt.Errorf("check endpoint keys for origin update: %w", err)
			}
			if hasKeys != 0 {
				return Endpoint{}, 0, ErrEndpointOriginConflict
			}
			changes |= EndpointChangeOrigin
		case currentTarget != nextTarget:
			changes |= EndpointChangeUpstreamPath
		}
	}
	if note != nil && *note != current.Note {
		changes |= EndpointChangeNote
	}
	if enabled != nil && *enabled != current.Enabled {
		changes |= EndpointChangeEnabled
	}

	if changes == 0 {
		if err := tx.Commit(); err != nil {
			return Endpoint{}, 0, fmt.Errorf("commit unchanged endpoint update: %w", err)
		}
		committed = true
		return current, 0, nil
	}

	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if changes.Has(EndpointChangeBaseURL) {
		sets = append(sets, "base_url=?")
		args = append(args, *baseURL)
	}
	if changes.Has(EndpointChangeNote) {
		sets = append(sets, "note=?")
		args = append(args, *note)
	}
	if changes.Has(EndpointChangeEnabled) {
		enabledInt := 0
		if *enabled {
			enabledInt = 1
		}
		sets = append(sets, "enabled=?")
		args = append(args, enabledInt)
	}
	sets = append(sets, "updated_at=?")
	args = append(args, now, id, userID)

	// #nosec G202 -- sets contains only constant base_url/note/enabled/time
	// fragments selected above; values and ownership keys are bound arguments.
	query := "UPDATE endpoints SET " + strings.Join(sets, ", ") + " WHERE id=? AND user_id=?"
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return Endpoint{}, 0, fmt.Errorf("update endpoint: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Endpoint{}, 0, fmt.Errorf("update endpoint rows affected: %w", err)
	}
	if affected != 1 {
		return Endpoint{}, 0, ErrNotFound
	}

	row = tx.QueryRowContext(ctx,
		`SELECT id, user_id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	updated, err := scanEndpointRow(row)
	if err != nil {
		return Endpoint{}, 0, fmt.Errorf("read updated endpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Endpoint{}, 0, fmt.Errorf("commit update endpoint: %w", err)
	}
	committed = true
	return updated, changes, nil
}

// DeleteEndpoint deletes the endpoint with id owned by userID. Cascading
// foreign keys remove its endpoint_keys and (through them) their
// fetched_models and model_bindings, so routing candidates are invalidated
// immediately. A missing or cross-user id yields ErrNotFound.
//
// The deletion is refused with ErrResourceInActiveDonation when any key of
// this endpoint is referenced by a pending or approved+enabled donation: the
// guard and the delete share one transaction, and the claims table's RESTRICT
// foreign key backstops any hypothetical interleaving, so an in-use physical
// key can never vanish under an active donation.
func (s *Store) DeleteEndpoint(ctx context.Context, userID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete endpoint: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := refuseActiveDonationRefsTx(ctx, tx,
		`SELECT id FROM endpoint_keys WHERE endpoint_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM endpoints WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return classifyResourceRefusal(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete endpoint rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete endpoint: commit: %w", err)
	}
	committed = true
	return nil
}

// EndpointCap returns the effective endpoint-count cap for userID: a non-NULL
// per-user override applies directly, otherwise the global default is the
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
	var userLimit sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT endpoint_limit FROM users WHERE id=?`, userID).Scan(&userLimit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("read user endpoint limit: %w", err)
	}
	// A non-NULL override is self-contained: the site default is neither a cap
	// nor a prerequisite, so even a missing/corrupt default cannot block it.
	if userLimit.Valid {
		if userLimit.Int64 < 0 || userLimit.Int64 > MaxUserEndpointLimit {
			return 0, ErrInvalidSiteConfig
		}
		return int(userLimit.Int64), nil
	}

	var globalStr sql.NullString
	err = q.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, siteConfigKeyDefaultEndpointLimit).Scan(&globalStr)
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
		if perr != nil || strconv.Itoa(n) != trimmed || trimmed != globalStr.String || n < 0 || n > MaxUserEndpointLimit {
			return 0, ErrInvalidSiteConfig
		}
		global = n
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
