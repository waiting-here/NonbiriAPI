package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// CreateEndpointKey inserts a new key for endpointID owned by userID. The
// ownership check and the insert are a single INSERT...SELECT statement so a
// cross-user or missing endpoint yields ErrNotFound with no read-then-write
// race. encryptedSecret is the already-sealed AES-256-GCM envelope produced by
// secret.Codec; the repository never receives plaintext. displayHead/displayTail
// are the persisted first/last fragments so listings never decrypt. now is the
// caller-supplied unix timestamp.
func (s *Store) CreateEndpointKey(ctx context.Context, userID, endpointID int64, encryptedSecret, displayHead, displayTail, note string, enabled bool, now int64) (EndpointKey, error) {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	// The SELECT guards ownership atomically: the INSERT produces zero rows
	// when the endpoint does not exist or belongs to another user, which we
	// report as ErrNotFound (indistinguishable from a missing endpoint).
	res, err := s.db.ExecContext(ctx, `
INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, note, enabled, created_at, updated_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
FROM endpoints
WHERE id=? AND user_id=?`,
		endpointID, encryptedSecret, displayHead, displayTail, note, enabledInt, now, now, endpointID, userID)
	if err != nil {
		return EndpointKey{}, fmt.Errorf("insert endpoint key: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return EndpointKey{}, fmt.Errorf("insert endpoint key rows affected: %w", err)
	}
	if affected == 0 {
		return EndpointKey{}, ErrNotFound
	}
	id, err := res.LastInsertId()
	if err != nil {
		return EndpointKey{}, fmt.Errorf("endpoint key last insert id: %w", err)
	}
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
func (s *Store) DeleteEndpointKey(ctx context.Context, userID, endpointID, keyID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM endpoint_keys WHERE id=? AND endpoint_id=? AND endpoint_id IN (SELECT id FROM endpoints WHERE user_id=?)`, keyID, endpointID, userID)
	if err != nil {
		return fmt.Errorf("delete endpoint key: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete endpoint key rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
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
