package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// EndpointKey carries the metadata and display fragments of an endpoint key.
// It intentionally has no encrypted-secret field: a value of this type can
// never leak ciphertext through a list/get response path. The ciphertext stays
// in the database row and is read only by the forwarding rail through a
// dedicated accessor, never through this struct.
type EndpointKey struct {
	ID          int64
	EndpointID  int64
	DisplayHead string
	DisplayTail string
	Note        string
	Enabled     bool
	CreatedAt   int64
	UpdatedAt   int64
}

// DefaultEndpointKeyLimit is the fallback per-endpoint key-count cap used when
// the administrator has not yet set the default_endpoint_key_limit site_config
// key. An administrator overrides it at runtime via the site-config endpoint
// (no restart required). It must be > 0 so a fresh install is usable.
const DefaultEndpointKeyLimit = 20

const (
	siteConfigKeyDefaultEndpointKeyLimit = "default_endpoint_key_limit"
	maxStoredEndpointBaseURLBytes        = 4096
	maxEndpointCredentialEnvelopeBytes   = 128 << 10
)

// siteConfigIntLocked reads one non-negative integer site_config key inside a
// transaction (or over *sql.DB). Unset falls back to def; an empty, negative
// or non-integer stored value is ErrInvalidSiteConfig (fail closed: a
// corrupted row never silently allows unbounded creation). It accepts the
// same queryRowContexter as both *sql.DB and *sql.Tx so the cap read shares
// the transaction's snapshot with the count and the insert that follow it,
// eliminating the read-then-write TOCTOU. It is shared by the endpoint-key,
// model and binding cap checks.
func siteConfigIntLocked(ctx context.Context, q queryRowContexter, key string, def int) (int, error) {
	var s sql.NullString
	err := q.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&s)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read site config %s: %w", key, err)
	}
	if !s.Valid {
		return def, nil
	}
	trimmed := strings.TrimSpace(s.String)
	if trimmed == "" {
		return 0, ErrInvalidSiteConfig
	}
	n, perr := strconv.Atoi(trimmed)
	if perr != nil || n < 0 {
		return 0, ErrInvalidSiteConfig
	}
	return n, nil
}

// CreateEndpointKey creates a context-bound key for endpointID owned by
// userID. It consumes and clears secretPlaintext. The cap check, ownership-
// guarded placeholder insert, contextual seal, ciphertext update, and commit
// share one transaction. The placeholder is therefore never visible to
// another connection and every failure rolls it back. The plaintext is never
// supplied to a SQL operation.
func (s *Store) CreateEndpointKey(ctx context.Context, userID, endpointID int64, secretPlaintext []byte, displayHead, displayTail, note string, enabled bool, now int64) (EndpointKey, error) {
	defer clear(secretPlaintext)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EndpointKey{}, fmt.Errorf("begin create endpoint key: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	id, err := s.createEndpointKeyTx(ctx, tx, userID, endpointID, secretPlaintext, displayHead, displayTail, note, enabled, now)
	if err != nil {
		return EndpointKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return EndpointKey{}, ErrEndpointCredentialUnavailable
	}
	committed = true
	return EndpointKey{
		ID:          id,
		EndpointID:  endpointID,
		DisplayHead: displayHead,
		DisplayTail: displayTail,
		Note:        note,
		Enabled:     enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// createEndpointKeyTx performs the whole cap-check → ownership-guarded
// placeholder insert → contextual seal → ciphertext-update sequence INSIDE the
// caller's transaction. It is the shared primitive behind CreateEndpointKey
// and the nested donation creation (which must create personal resources and
// the pending donation in ONE transaction so any failure rolls everything
// back). The caller owns the transaction lifecycle and the final commit; the
// placeholder is never visible to another connection because the enclosing
// transaction is the only writer. On return (success or error) the plaintext
// has been consumed and cleared.
func (s *Store) createEndpointKeyTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, secretPlaintext []byte, displayHead, displayTail, note string, enabled bool, now int64) (int64, error) {
	defer clear(secretPlaintext)
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	cap, err := siteConfigIntLocked(ctx, tx, siteConfigKeyDefaultEndpointKeyLimit, DefaultEndpointKeyLimit)
	if err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.endpoint_id=? AND e.user_id=?`, endpointID, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count endpoint keys: %w", err)
	}
	if count >= cap {
		return 0, newCapError(ErrEndpointKeyCap, ResourceEndpointKey, cap)
	}

	// Ownership and the initial row allocation are one SQL statement. The
	// empty ciphertext is an uncommitted placeholder used only to obtain the
	// database-assigned key id required by the authenticated context.
	res, err := tx.ExecContext(ctx, `
INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, note, enabled, created_at, updated_at)
SELECT ?, '', ?, ?, ?, ?, ?, ?
FROM endpoints
WHERE id=? AND user_id=?`,
		endpointID, displayHead, displayTail, note, enabledInt, now, now, endpointID, userID)
	if err != nil {
		return 0, fmt.Errorf("insert endpoint key placeholder: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("insert endpoint key rows affected: %w", err)
	}
	if affected == 0 {
		return 0, ErrNotFound
	}
	id, err := res.LastInsertId()
	if err != nil || id <= 0 {
		return 0, ErrEndpointCredentialUnavailable
	}

	var baseURL sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT CASE WHEN length(CAST(base_url AS BLOB)) BETWEEN 1 AND ? THEN base_url END
FROM endpoints
WHERE id=? AND user_id=?`, maxStoredEndpointBaseURLBytes, endpointID, userID).Scan(&baseURL); err != nil || !baseURL.Valid {
		return 0, ErrEndpointCredentialUnavailable
	}
	_, canonicalOrigin, err := egress.CanonicalEndpointTarget(baseURL.String)
	baseURL.String = ""
	if err != nil {
		return 0, ErrEndpointCredentialUnavailable
	}
	credentialContext, err := secret.NewEndpointKeyContext(userID, endpointID, id, canonicalOrigin)
	canonicalOrigin = ""
	if err != nil {
		return 0, ErrEndpointCredentialUnavailable
	}
	ciphertext, err := s.secrets.SealForContext(secretPlaintext, credentialContext)
	clear(secretPlaintext)
	if err != nil {
		return 0, ErrEndpointCredentialUnavailable
	}
	res, err = tx.ExecContext(ctx, `
UPDATE endpoint_keys
SET encrypted_secret=?
WHERE id=? AND endpoint_id=?
  AND endpoint_id IN (SELECT id FROM endpoints WHERE id=? AND user_id=?)`,
		ciphertext, id, endpointID, endpointID, userID)
	ciphertext = ""
	if err != nil {
		return 0, ErrEndpointCredentialUnavailable
	}
	affected, err = res.RowsAffected()
	if err != nil || affected != 1 {
		return 0, ErrEndpointCredentialUnavailable
	}
	return id, nil
}

// EndpointKeyCap returns the effective per-endpoint key-count cap: the
// default_endpoint_key_limit site_config value or DefaultEndpointKeyLimit when
// unset. It is exported for handlers that surface the cap and for tests.
func (s *Store) EndpointKeyCap(ctx context.Context) (int, error) {
	return siteConfigIntLocked(ctx, s.db, siteConfigKeyDefaultEndpointKeyLimit, DefaultEndpointKeyLimit)
}

// CountEndpointKeys returns the number of keys on endpointID owned by userID
// (ownership-scoped via the endpoints join, so a cross-user endpoint id counts
// 0). It is exported for tests that assert the cap boundary.
func (s *Store) CountEndpointKeys(ctx context.Context, userID, endpointID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.endpoint_id=? AND e.user_id=?`, endpointID, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count endpoint keys: %w", err)
	}
	return count, nil
}

// ListEndpointKeys returns every key on endpointID owned by userID, ordered by
// id. The query joins through endpoints on user_id so a cross-user caller can
// never list another user's keys. It selects only metadata and display
// fragments, never encrypted_secret, so the ciphertext never enters the list
// response path.
func (s *Store) ListEndpointKeys(ctx context.Context, userID, endpointID int64) ([]EndpointKey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT ek.id, ek.endpoint_id, ek.display_head, ek.display_tail, ek.note, ek.enabled, ek.created_at, ek.updated_at
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.endpoint_id=? AND e.user_id=?
ORDER BY ek.id`, endpointID, userID)
	if err != nil {
		return nil, fmt.Errorf("list endpoint keys: %w", err)
	}
	defer rows.Close()
	return scanEndpointKeys(rows)
}

// ListEnabledEndpointKeys returns the enabled keys on endpointID owned by
// userID. It is the candidate set the fetch hook iterates after an endpoint
// save/edit ("fetch once per enabled key"). Like ListEndpointKeys it never
// selects encrypted_secret.
func (s *Store) ListEnabledEndpointKeys(ctx context.Context, userID, endpointID int64) ([]EndpointKey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT ek.id, ek.endpoint_id, ek.display_head, ek.display_tail, ek.note, ek.enabled, ek.created_at, ek.updated_at
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.endpoint_id=? AND e.user_id=? AND ek.enabled=1
ORDER BY ek.id`, endpointID, userID)
	if err != nil {
		return nil, fmt.Errorf("list enabled endpoint keys: %w", err)
	}
	defer rows.Close()
	return scanEndpointKeys(rows)
}

// UpdateEndpointKey atomically updates the key keyID on endpointID owned by
// userID. A nil argument leaves that field unchanged. Ownership is enforced in
// SQL: the key must belong to endpointID AND endpointID must belong to userID,
// so a cross-user id and a same-user wrong-endpoint path both yield ErrNotFound
// with no read-then-write race. now updates updated_at. The encrypted secret is
// never mutable through this method (key rotation is delete + add).
func (s *Store) UpdateEndpointKey(ctx context.Context, userID, endpointID, keyID int64, note *string, enabled *bool, now int64) (EndpointKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EndpointKey{}, fmt.Errorf("begin update endpoint key: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	sets := make([]string, 0, 3)
	args := make([]any, 0, 5)
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
		row := tx.QueryRowContext(ctx, endpointKeySelectSQL, keyID, endpointID, userID)
		k, err := scanEndpointKeyRow(row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return EndpointKey{}, ErrNotFound
			}
			return EndpointKey{}, fmt.Errorf("read endpoint key for update: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return EndpointKey{}, fmt.Errorf("commit noop update: %w", err)
		}
		committed = true
		return k, nil
	}

	sets = append(sets, "updated_at=?")
	args = append(args, now)
	args = append(args, keyID, endpointID, userID)
	// #nosec G202 -- sets is restricted to fixed note/enabled/time fragments;
	// all values and the complete ownership predicate remain parameterized.
	query := "UPDATE endpoint_keys SET " + joinSets(sets) + " WHERE id=? AND endpoint_id=? AND endpoint_id IN (SELECT id FROM endpoints WHERE user_id=?)"
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return EndpointKey{}, fmt.Errorf("update endpoint key: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return EndpointKey{}, fmt.Errorf("update endpoint key rows affected: %w", err)
	}
	if affected == 0 {
		return EndpointKey{}, ErrNotFound
	}

	row := tx.QueryRowContext(ctx, endpointKeySelectSQL, keyID, endpointID, userID)
	k, err := scanEndpointKeyRow(row)
	if err != nil {
		return EndpointKey{}, fmt.Errorf("read updated endpoint key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EndpointKey{}, fmt.Errorf("commit update endpoint key: %w", err)
	}
	committed = true
	return k, nil
}

// DeleteEndpointKey deletes the key keyID on endpointID owned by userID.
// Cascading foreign keys remove its fetched_models and model_bindings, so
// routing candidates are invalidated immediately. Ownership is enforced in SQL:
// the key must belong to endpointID AND endpointID must belong to userID, so a
// cross-user id and a same-user wrong-endpoint path both yield ErrNotFound with
// no read-then-write race.
// The deletion is refused with ErrResourceInActiveDonation while a pending or
// approved+enabled donation references this key; guard, delete and the claims
// table's RESTRICT constraint share one transaction.
func (s *Store) DeleteEndpointKey(ctx context.Context, userID, endpointID, keyID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete endpoint key: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := refuseActiveDonationRefsTx(ctx, tx,
		`SELECT id FROM endpoint_keys WHERE id=? AND endpoint_id=?`, keyID, endpointID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM endpoint_keys WHERE id=? AND endpoint_id=? AND endpoint_id IN (SELECT id FROM endpoints WHERE user_id=?)`, keyID, endpointID, userID)
	if err != nil {
		return classifyResourceRefusal(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete endpoint key rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete endpoint key: commit: %w", err)
	}
	committed = true
	return nil
}

// endpointKeySelectSQL selects metadata + display fragments for one key on
// endpointID owned by userID. It never selects encrypted_secret. The
// ek.endpoint_id=? clause makes the path's endpoint id part of the ownership
// check so a key cannot be addressed through another endpoint's path.
const endpointKeySelectSQL = `
SELECT ek.id, ek.endpoint_id, ek.display_head, ek.display_tail, ek.note, ek.enabled, ek.created_at, ek.updated_at
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`

func scanEndpointKeyRow(row *sql.Row) (EndpointKey, error) {
	var k EndpointKey
	var enabledInt int
	err := row.Scan(&k.ID, &k.EndpointID, &k.DisplayHead, &k.DisplayTail, &k.Note, &enabledInt, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return EndpointKey{}, err
	}
	k.Enabled = enabledInt != 0
	return k, nil
}

func scanEndpointKeys(rows *sql.Rows) ([]EndpointKey, error) {
	var out []EndpointKey
	for rows.Next() {
		var k EndpointKey
		var enabledInt int
		if err := rows.Scan(&k.ID, &k.EndpointID, &k.DisplayHead, &k.DisplayTail, &k.Note, &enabledInt, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan endpoint key: %w", err)
		}
		k.Enabled = enabledInt != 0
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoint keys: %w", err)
	}
	return out, nil
}

// joinSets joins set clauses with ", ". It is a tiny local helper to keep the
// update builder readable without importing strings here.
func joinSets(sets []string) string {
	if len(sets) == 0 {
		return ""
	}
	out := sets[0]
	for _, s := range sets[1:] {
		out += ", " + s
	}
	return out
}
