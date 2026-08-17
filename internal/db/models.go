package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"
)

// Model is a user-defined platform model. The external/routing name is the
// full "provider/model" string; provider and model are opaque free strings
// that allow '/' and are never split into path segments or interpreted. The
// repository always stores and matches the full name as a single opaque key
// (the schema CHECK keeps full_name == provider || '/' || model, and the
// unique (user_id, full_name) index makes field-pair collisions collide).
// BindingCount is projected by a SQL aggregate, never client-supplied.
type Model struct {
	ID            int64
	UserID        int64
	Provider      string
	Model         string
	FullName      string
	RouteStrategy string
	SilentRetry   bool
	BindingCount  int
	CreatedAt     int64
	UpdatedAt     int64
}

// modelListSQL projects models with their live binding counts. The LEFT JOIN
// keeps zero-binding drafts visible with count 0; GROUP BY m.id is safe
// because id is the primary key (SQLite's bare-column rule).
const modelListSQL = `
SELECT m.id, m.user_id, m.provider, m.model, m.full_name, m.route_strategy, m.silent_retry, m.created_at, m.updated_at, COUNT(b.id)
FROM models m
LEFT JOIN model_bindings b ON b.model_id = m.id
WHERE m.user_id = ?
GROUP BY m.id
ORDER BY m.id`

const modelGetSQL = `
SELECT m.id, m.user_id, m.provider, m.model, m.full_name, m.route_strategy, m.silent_retry, m.created_at, m.updated_at, COUNT(b.id)
FROM models m
LEFT JOIN model_bindings b ON b.model_id = m.id
WHERE m.id = ? AND m.user_id = ?
GROUP BY m.id`

// CreateModel inserts a platform model for userID. provider/model must already
// be validated by the service; the repository builds the routing key as the
// opaque concatenation provider || '/' || model (construction, never
// interpretation). silentRetry persists the explicit retry switch (false =
// fail fast, the default). A second model with the same (user_id, full_name) —
// including a different provider/model split that yields the same external
// name — returns ErrConflict and writes nothing. now is caller-supplied.
func (s *Store) CreateModel(ctx context.Context, userID int64, provider, model, routeStrategy string, silentRetry bool, now int64) (Model, error) {
	fullName := provider + "/" + model
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, fmt.Errorf("begin create model: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	retryInt := 0
	if silentRetry {
		retryInt = 1
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO models (user_id, provider, model, full_name, route_strategy, silent_retry, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		userID, provider, model, fullName, routeStrategy, retryInt, now, now)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM models WHERE user_id=? AND full_name=?`, userID, fullName); derr != nil {
				return Model{}, derr
			}
		}
		return Model{}, fmt.Errorf("insert model: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Model{}, fmt.Errorf("model last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Model{}, fmt.Errorf("commit create model: %w", err)
	}
	committed = true
	return Model{
		ID: id, UserID: userID, Provider: provider, Model: model,
		FullName: fullName, RouteStrategy: routeStrategy, SilentRetry: silentRetry,
		BindingCount: 0, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ListModels returns every platform model owned by userID with its live
// binding count, ordered by id. The query filters by user_id in SQL so a
// cross-user caller can never enumerate another user's models.
func (s *Store) ListModels(ctx context.Context, userID int64) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, modelListSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	return scanModels(rows)
}

// GetModel returns the model with id owned by userID. A missing or cross-user
// id yields ErrNotFound; the two are indistinguishable.
func (s *Store) GetModel(ctx context.Context, userID, id int64) (Model, error) {
	row := s.db.QueryRowContext(ctx, modelGetSQL, id, userID)
	m, err := scanModelRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Model{}, ErrNotFound
		}
		return Model{}, fmt.Errorf("get model: %w", err)
	}
	return m, nil
}

// UpdateModel atomically updates the model with id owned by userID. A nil
// argument leaves that field unchanged. When provider and/or model change, the
// full name is recomputed inside the same transaction from the merged values;
// a collision with another of the user's models returns ErrConflict. A nil
// silentRetry leaves the retry switch unchanged. A missing or cross-user id
// yields ErrNotFound. now updates updated_at.
func (s *Store) UpdateModel(ctx context.Context, userID, id int64, provider, model, routeStrategy *string, silentRetry *bool, now int64) (Model, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, fmt.Errorf("begin update model: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Read the current row inside the transaction so the full name can be
	// recomputed from merged values atomically with the write.
	row := tx.QueryRowContext(ctx, modelGetSQL, id, userID)
	current, err := scanModelRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Model{}, ErrNotFound
		}
		return Model{}, fmt.Errorf("read model for update: %w", err)
	}

	newProvider := current.Provider
	if provider != nil {
		newProvider = *provider
	}
	newModel := current.Model
	if model != nil {
		newModel = *model
	}
	newStrategy := current.RouteStrategy
	if routeStrategy != nil {
		newStrategy = *routeStrategy
	}
	newRetry := current.SilentRetry
	if silentRetry != nil {
		newRetry = *silentRetry
	}
	newFullName := newProvider + "/" + newModel
	if newProvider == current.Provider && newModel == current.Model && newStrategy == current.RouteStrategy && newRetry == current.SilentRetry {
		// No-op update: the ownership check already passed, echo the row.
		if err := tx.Commit(); err != nil {
			return Model{}, fmt.Errorf("commit noop model update: %w", err)
		}
		committed = true
		return current, nil
	}

	retryInt := 0
	if newRetry {
		retryInt = 1
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE models SET provider=?, model=?, full_name=?, route_strategy=?, silent_retry=?, updated_at=? WHERE id=? AND user_id=?`,
		newProvider, newModel, newFullName, newStrategy, retryInt, now, id, userID)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM models WHERE user_id=? AND full_name=? AND id<>?`, userID, newFullName, id); derr != nil {
				return Model{}, derr
			}
		}
		return Model{}, fmt.Errorf("update model: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Model{}, fmt.Errorf("update model rows affected: %w", err)
	}
	if affected == 0 {
		return Model{}, ErrNotFound
	}

	updated, err := scanModelRow(tx.QueryRowContext(ctx, modelGetSQL, id, userID))
	if err != nil {
		return Model{}, fmt.Errorf("read updated model: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Model{}, fmt.Errorf("commit update model: %w", err)
	}
	committed = true
	return updated, nil
}

// DeleteModel deletes the model with id owned by userID. Cascading foreign
// keys remove its model_bindings immediately. A missing or cross-user id
// yields ErrNotFound.
func (s *Store) DeleteModel(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM models WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete model rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// isConstraintError reports whether err is a SQLite constraint failure. The
// primary result code cannot distinguish UNIQUE from CHECK/FK/PK violations,
// so callers pair this with a diagnostic query (see classifyConflict).
func isConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr)
}

// classifyConflict runs after a failed write inside the same open transaction
// (which still holds the write lock, so the diagnostic reads a consistent
// snapshot). When the diagnostic finds the conflicting row, ErrConflict is
// returned; otherwise nil lets the caller wrap the original write error. This
// keeps uniqueness failures mapped to conflict while any other constraint
// failure stays an internal error.
func classifyConflict(ctx context.Context, q queryRowContexter, diagSQL string, diagArgs ...any) error {
	var n int
	if err := q.QueryRowContext(ctx, diagSQL, diagArgs...).Scan(&n); err != nil {
		return fmt.Errorf("conflict diagnostic: %w", err)
	}
	if n > 0 {
		return ErrConflict
	}
	return nil
}

func scanModelRow(row *sql.Row) (Model, error) {
	var m Model
	var count int
	var silentRetry int
	err := row.Scan(&m.ID, &m.UserID, &m.Provider, &m.Model, &m.FullName, &m.RouteStrategy,
		&silentRetry, &m.CreatedAt, &m.UpdatedAt, &count)
	if err != nil {
		return Model{}, err
	}
	m.SilentRetry = silentRetry != 0
	m.BindingCount = count
	return m, nil
}

func scanModels(rows *sql.Rows) ([]Model, error) {
	var out []Model
	for rows.Next() {
		var m Model
		var count int
		var silentRetry int
		if err := rows.Scan(&m.ID, &m.UserID, &m.Provider, &m.Model, &m.FullName, &m.RouteStrategy,
			&silentRetry, &m.CreatedAt, &m.UpdatedAt, &count); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		m.SilentRetry = silentRetry != 0
		m.BindingCount = count
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate models: %w", err)
	}
	return out, nil
}
