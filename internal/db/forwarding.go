package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// ErrForwardProjectionLimit reports that a caller-owned model or binding
// projection crossed the finite limit supplied by the forwarding service.
// The repository never returns a partial candidate set: partial routing would
// make selection depend on an accidental SQL limit.
var ErrForwardProjectionLimit = errors.New("forward projection exceeds its limit")

// CallerModel is the non-sensitive platform-model projection exposed by the
// OpenAI-compatible GET /v1/models endpoint. It contains no binding, endpoint,
// key, fetched-model, or ciphertext data.
type CallerModel struct {
	FullName  string
	Provider  string
	CreatedAt int64
}

// ForwardRoute is one caller-owned platform model together with its complete
// currently usable candidate set. FullName is an opaque routing key and is
// never split or interpreted.
type ForwardRoute struct {
	ModelID       int64
	UserID        int64
	FullName      string
	RouteStrategy string
	SilentRetry   bool
	Candidates    []ForwardCandidate
}

// ForwardCandidate is non-sensitive selector metadata. The candidate query
// admits a row only while the model, binding, endpoint, key, and fetched-cache
// row all belong to the same user and remain usable.
type ForwardCandidate struct {
	BindingID       int64
	ModelID         int64
	EndpointID      int64
	EndpointKeyID   int64
	UpstreamModelID string
	Ord             int64
}

// ForwardTarget is the final dispatch projection. Its sealed credential is
// private and can only be transferred once through TakeEncryptedSecret; it
// must never enter a response, diagnostic, hook, or log record.
type ForwardTarget struct {
	BindingID       int64
	EndpointID      int64
	EndpointKeyID   int64
	ConnectorType   string
	BaseURL         string
	UpstreamModelID string
	encryptedSecret string
}

// TakeEncryptedSecret transfers the sealed envelope to the single-attempt
// runner and removes it from this projection. The returned immutable string
// must be dropped immediately after Vault.Open.
func (t *ForwardTarget) TakeEncryptedSecret() string {
	if t == nil {
		return ""
	}
	ciphertext := t.encryptedSecret
	t.encryptedSecret = ""
	return ciphertext
}

// DiscardEncryptedSecret drops the projection's sealed envelope when dispatch
// is refused before decryption.
func (t *ForwardTarget) DiscardEncryptedSecret() {
	if t != nil {
		t.encryptedSecret = ""
	}
}

func (ForwardTarget) String() string   { return "[redacted forward target]" }
func (ForwardTarget) GoString() string { return "[redacted forward target]" }
func (ForwardTarget) LogValue() slog.Value {
	return slog.StringValue("[redacted forward target]")
}

// ListCallerModels returns at most limit platform models owned by userID in
// stable id order. The SQL is ownership-scoped and deliberately does not join
// upstream model caches or endpoint keys. If more than limit rows exist, no
// partial list is returned.
func (s *Store) ListCallerModels(ctx context.Context, userID int64, limit int) ([]CallerModel, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		return nil, ErrForwardProjectionLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT full_name, provider, created_at
FROM models
WHERE user_id=?
ORDER BY id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list caller models: %w", err)
	}
	defer rows.Close()

	models := make([]CallerModel, 0, min(limit, 32))
	for rows.Next() {
		if len(models) == limit {
			return nil, ErrForwardProjectionLimit
		}
		var model CallerModel
		if err := rows.Scan(&model.FullName, &model.Provider, &model.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan caller model: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate caller models: %w", err)
	}
	return models, nil
}

// ResolveForwardRoute resolves fullName only inside userID's ownership scope
// and returns the complete usable binding set in (ord,id) order. Candidates
// are filtered in SQL, never by reading globally-addressed ids and checking
// ownership in memory. Disabled endpoints/keys, missing cache rows, non-ok
// cache rows, deleted resources, and resources belonging to another user do
// not enter the result. A model may legitimately return zero candidates.
func (s *Store) ResolveForwardRoute(ctx context.Context, userID int64, fullName string, limit int) (ForwardRoute, error) {
	if userID <= 0 || fullName == "" {
		return ForwardRoute{}, ErrNotFound
	}
	if limit <= 0 {
		return ForwardRoute{}, ErrForwardProjectionLimit
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ForwardRoute{}, fmt.Errorf("begin forward route projection: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var route ForwardRoute
	var silentRetry int
	err = tx.QueryRowContext(ctx, `
SELECT id, user_id, full_name, route_strategy, silent_retry
FROM models
WHERE user_id=? AND full_name=?`, userID, fullName).
		Scan(&route.ModelID, &route.UserID, &route.FullName, &route.RouteStrategy, &silentRetry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ForwardRoute{}, ErrNotFound
		}
		return ForwardRoute{}, fmt.Errorf("resolve caller model: %w", err)
	}
	route.SilentRetry = silentRetry != 0

	rows, err := tx.QueryContext(ctx, `
SELECT b.id, b.model_id, e.id, ek.id, b.upstream_model_id, b.ord
FROM model_bindings b
JOIN models m
  ON m.id=b.model_id
 AND m.id=?
 AND m.user_id=?
 AND m.full_name=?
JOIN endpoint_keys ek
  ON ek.id=b.endpoint_key_id
 AND ek.enabled=1
JOIN endpoints e
  ON e.id=ek.endpoint_id
 AND e.user_id=m.user_id
 AND e.user_id=?
 AND e.enabled=1
JOIN fetched_models fm
  ON fm.endpoint_key_id=ek.id
 AND fm.upstream_model_id=b.upstream_model_id
 AND fm.status='ok'
ORDER BY b.ord, b.id
LIMIT ?`, route.ModelID, userID, fullName, userID, limit+1)
	if err != nil {
		return ForwardRoute{}, fmt.Errorf("query forward candidates: %w", err)
	}
	candidates := make([]ForwardCandidate, 0, min(limit, 16))
	for rows.Next() {
		if len(candidates) == limit {
			_ = rows.Close()
			return ForwardRoute{}, ErrForwardProjectionLimit
		}
		var candidate ForwardCandidate
		if err := rows.Scan(
			&candidate.BindingID,
			&candidate.ModelID,
			&candidate.EndpointID,
			&candidate.EndpointKeyID,
			&candidate.UpstreamModelID,
			&candidate.Ord,
		); err != nil {
			_ = rows.Close()
			return ForwardRoute{}, fmt.Errorf("scan forward candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ForwardRoute{}, fmt.Errorf("iterate forward candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ForwardRoute{}, fmt.Errorf("close forward candidates: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ForwardRoute{}, fmt.Errorf("commit forward route projection: %w", err)
	}
	committed = true
	route.Candidates = candidates
	return route, nil
}

// GetForwardTarget revalidates a selected binding immediately before dispatch
// and returns its connector/base URL/upstream model plus sealed credential.
// The opaque full name and binding id are both part of the ownership-scoped
// SQL predicate. A rename, delete, disable, cache invalidation, cross-user id,
// or binding mutation therefore fails closed as ErrNotFound.
func (s *Store) GetForwardTarget(ctx context.Context, userID int64, fullName string, bindingID int64) (ForwardTarget, error) {
	if userID <= 0 || fullName == "" || bindingID <= 0 {
		return ForwardTarget{}, ErrNotFound
	}
	var target ForwardTarget
	err := s.db.QueryRowContext(ctx, `
SELECT b.id, e.id, ek.id, e.connector_type, e.base_url, b.upstream_model_id, ek.encrypted_secret
FROM model_bindings b
JOIN models m
  ON m.id=b.model_id
 AND m.user_id=?
 AND m.full_name=?
JOIN endpoint_keys ek
  ON ek.id=b.endpoint_key_id
 AND ek.enabled=1
JOIN endpoints e
  ON e.id=ek.endpoint_id
 AND e.user_id=m.user_id
 AND e.user_id=?
 AND e.enabled=1
JOIN fetched_models fm
  ON fm.endpoint_key_id=ek.id
 AND fm.upstream_model_id=b.upstream_model_id
 AND fm.status='ok'
WHERE b.id=?`, userID, fullName, userID, bindingID).
		Scan(
			&target.BindingID,
			&target.EndpointID,
			&target.EndpointKeyID,
			&target.ConnectorType,
			&target.BaseURL,
			&target.UpstreamModelID,
			&target.encryptedSecret,
		)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ForwardTarget{}, ErrNotFound
		}
		return ForwardTarget{}, fmt.Errorf("read forward target: %w", err)
	}
	return target, nil
}
