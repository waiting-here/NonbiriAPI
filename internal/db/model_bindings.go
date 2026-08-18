package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ModelBinding binds a platform model to an upstream (EndpointKey,
// upstream_model_id). ord orders the "ordered" route strategy; the unique
// (model_id, endpoint_key_id, upstream_model_id) triple makes a duplicate
// binding a conflict. The endpoint_key_id can never be changed after creation
// (updates only touch ord / upstream_model_id), so a binding cannot be
// re-pointed at another key or user through the update path.
type ModelBinding struct {
	ID              int64
	ModelID         int64
	EndpointKeyID   int64
	UpstreamModelID string
	Ord             int64
	CreatedAt       int64
}

const bindingSelectSQL = `
SELECT b.id, b.model_id, b.endpoint_key_id, b.upstream_model_id, b.ord, b.created_at
FROM model_bindings b
JOIN models m ON b.model_id = m.id
WHERE b.model_id=? AND m.user_id=?`

// ListBindings returns the bindings of modelID owned by userID, ordered by
// (ord, id) as the "ordered" route strategy consumes them. The join filters by
// user_id in SQL; the service resolves the not-found contract by checking the
// model first, so a cross-user model never yields a list.
func (s *Store) ListBindings(ctx context.Context, userID, modelID int64) ([]ModelBinding, error) {
	rows, err := s.db.QueryContext(ctx, bindingSelectSQL+` ORDER BY b.ord, b.id`, modelID, userID)
	if err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	defer rows.Close()
	return scanBindings(rows)
}

// DefaultBindingLimit is the fallback per-platform-model binding-count cap
// used when the administrator has not yet set the default_binding_limit
// site_config key. An administrator overrides it at runtime via the
// site-config endpoint (no restart required). It must be > 0 so a fresh
// install is usable.
const DefaultBindingLimit = 50

const siteConfigKeyDefaultBindingLimit = "default_binding_limit"

// CreateBinding inserts a binding from modelID (owned by userID) to an
// enabled endpoint key of an enabled endpoint owned by the same user, and only
// for an upstream_model_id present in that key's fetched cache. The per-model
// binding-count cap and the current count are read inside the same
// transaction as the insert, so a concurrent add cannot slip between the count
// and the write (no read-then-write TOCTOU); when the count has reached the
// cap a *CapError wrapping ErrBindingCap is returned and no row is written.
// The count is ownership-scoped via the models join, so a cross-user or missing
// model id counts 0 and falls through to the ownership-guarded INSERT...SELECT
// -> ErrNotFound, never leaking the real owner's binding count. All candidate
// checks are one atomic INSERT...SELECT: a cross-user or missing model, a
// missing or disabled key, a disabled endpoint, and an uncached upstream id
// all produce zero rows and map to ErrNotFound (indistinguishable, no
// existence leak). A duplicate (model_id, endpoint_key_id, upstream_model_id)
// triple is a ErrConflict. ord must already be validated by the service. now
// is caller-supplied.
func (s *Store) CreateBinding(ctx context.Context, userID, modelID, endpointKeyID int64, upstreamModelID string, ord, now int64) (ModelBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelBinding{}, fmt.Errorf("begin create binding: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	cap, err := siteConfigIntLocked(ctx, tx, siteConfigKeyDefaultBindingLimit, DefaultBindingLimit)
	if err != nil {
		return ModelBinding{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM model_bindings b
JOIN models m ON b.model_id = m.id
WHERE b.model_id=? AND m.user_id=?`, modelID, userID).Scan(&count); err != nil {
		return ModelBinding{}, fmt.Errorf("count bindings: %w", err)
	}
	if count >= cap {
		return ModelBinding{}, newCapError(ErrBindingCap, ResourceBinding, cap)
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at)
SELECT m.id, ek.id, ?, ?, ?
FROM models m
JOIN endpoint_keys ek ON ek.id = ? AND ek.enabled = 1
JOIN endpoints e ON ek.endpoint_id = e.id AND e.user_id = ? AND e.enabled = 1
JOIN fetched_models fm ON fm.endpoint_key_id = ek.id AND fm.upstream_model_id = ?
WHERE m.id = ? AND m.user_id = ?`,
		upstreamModelID, ord, now,
		endpointKeyID, userID, upstreamModelID,
		modelID, userID)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM model_bindings WHERE model_id=? AND endpoint_key_id=? AND upstream_model_id=?`,
				modelID, endpointKeyID, upstreamModelID); derr != nil {
				return ModelBinding{}, derr
			}
		}
		return ModelBinding{}, fmt.Errorf("insert binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ModelBinding{}, fmt.Errorf("insert binding rows affected: %w", err)
	}
	if affected == 0 {
		return ModelBinding{}, ErrNotFound
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ModelBinding{}, fmt.Errorf("binding last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelBinding{}, fmt.Errorf("commit create binding: %w", err)
	}
	committed = true
	return ModelBinding{
		ID: id, ModelID: modelID, EndpointKeyID: endpointKeyID,
		UpstreamModelID: upstreamModelID, Ord: ord, CreatedAt: now,
	}, nil
}

// BindingCap returns the effective per-platform-model binding-count cap: the
// default_binding_limit site_config value or DefaultBindingLimit when unset.
// It is exported for handlers that surface the cap and for tests.
func (s *Store) BindingCap(ctx context.Context) (int, error) {
	return siteConfigIntLocked(ctx, s.db, siteConfigKeyDefaultBindingLimit, DefaultBindingLimit)
}

// CountBindings returns the number of bindings on modelID owned by userID
// (ownership-scoped via the models join, so a cross-user model id counts 0).
// It is exported for tests that assert the cap boundary.
func (s *Store) CountBindings(ctx context.Context, userID, modelID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM model_bindings b
JOIN models m ON b.model_id = m.id
WHERE b.model_id=? AND m.user_id=?`, modelID, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count bindings: %w", err)
	}
	return count, nil
}

// UpdateBinding updates the binding bindingID on modelID owned by userID.
// Only ord and/or upstream_model_id are mutable; endpoint_key_id never changes
// through this path, so a binding cannot be re-pointed at another key or user.
// Ownership and candidate validity are enforced in the same transaction: the
// model must belong to the user, the binding's endpoint key must still be
// enabled on an enabled endpoint of that user, and the resulting (key,
// upstream_model_id) must exist in the key's fetched cache. A missing or
// cross-user model/binding, a now-disabled key or endpoint, and an uncached
// upstream id all yield ErrNotFound; a duplicate triple after the update
// yields ErrConflict. A nil argument leaves that field unchanged.
func (s *Store) UpdateBinding(ctx context.Context, userID, modelID, bindingID int64, ord *int64, upstreamModelID *string, now int64) (ModelBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelBinding{}, fmt.Errorf("begin update binding: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRowContext(ctx, bindingSelectSQL+` AND b.id=?`, modelID, userID, bindingID)
	current, err := scanBindingRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModelBinding{}, ErrNotFound
		}
		return ModelBinding{}, fmt.Errorf("read binding for update: %w", err)
	}

	newOrd := current.Ord
	if ord != nil {
		newOrd = *ord
	}
	newUpstream := current.UpstreamModelID
	if upstreamModelID != nil {
		newUpstream = *upstreamModelID
	}
	if newOrd == current.Ord && newUpstream == current.UpstreamModelID {
		if err := tx.Commit(); err != nil {
			return ModelBinding{}, fmt.Errorf("commit noop binding update: %w", err)
		}
		committed = true
		return current, nil
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 6)
	if ord != nil {
		sets = append(sets, "ord=?")
		args = append(args, newOrd)
	}
	if upstreamModelID != nil {
		sets = append(sets, "upstream_model_id=?")
		args = append(args, newUpstream)
	}
	args = append(args, bindingID, modelID, modelID, userID, userID, newUpstream)
	// #nosec G202 -- sets contains only the fixed ord/upstream_model_id fragments
	// selected above; every model, owner, binding, and value remains parameterized.
	query := `UPDATE model_bindings SET ` + joinSets(sets) + `
WHERE id=? AND model_id=?
  AND model_id IN (SELECT id FROM models WHERE id=? AND user_id=?)
  AND EXISTS (
    SELECT 1 FROM endpoint_keys ek
    JOIN endpoints e ON ek.endpoint_id = e.id AND e.user_id = ?
    JOIN fetched_models fm ON fm.endpoint_key_id = ek.id AND fm.upstream_model_id = ?
    WHERE ek.id = model_bindings.endpoint_key_id AND ek.enabled = 1 AND e.enabled = 1
  )`
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM model_bindings WHERE model_id=? AND endpoint_key_id=? AND upstream_model_id=? AND id<>?`,
				modelID, current.EndpointKeyID, newUpstream, bindingID); derr != nil {
				return ModelBinding{}, derr
			}
		}
		return ModelBinding{}, fmt.Errorf("update binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ModelBinding{}, fmt.Errorf("update binding rows affected: %w", err)
	}
	if affected == 0 {
		return ModelBinding{}, ErrNotFound
	}

	updated, err := scanBindingRow(tx.QueryRowContext(ctx, bindingSelectSQL+` AND b.id=?`, modelID, userID, bindingID))
	if err != nil {
		return ModelBinding{}, fmt.Errorf("read updated binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelBinding{}, fmt.Errorf("commit update binding: %w", err)
	}
	committed = true
	return updated, nil
}

// DeleteBinding deletes the binding bindingID on modelID owned by userID. The
// model ownership is part of the SQL condition, so a cross-user model or a
// binding id on another model's path both yield ErrNotFound.
func (s *Store) DeleteBinding(ctx context.Context, userID, modelID, bindingID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM model_bindings WHERE id=? AND model_id=? AND model_id IN (SELECT id FROM models WHERE id=? AND user_id=?)`,
		bindingID, modelID, modelID, userID)
	if err != nil {
		return fmt.Errorf("delete binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete binding rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanBindingRow(row *sql.Row) (ModelBinding, error) {
	var b ModelBinding
	err := row.Scan(&b.ID, &b.ModelID, &b.EndpointKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt)
	if err != nil {
		return ModelBinding{}, err
	}
	return b, nil
}

func scanBindings(rows *sql.Rows) ([]ModelBinding, error) {
	var out []ModelBinding
	for rows.Next() {
		var b ModelBinding
		if err := rows.Scan(&b.ID, &b.ModelID, &b.EndpointKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan binding: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bindings: %w", err)
	}
	return out, nil
}
