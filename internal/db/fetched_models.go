package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"nonbiriapi/internal/diagnostic"
)

// FetchedModel is one row of the upstream model cache for an (Endpoint, Key)
// combo. UpstreamModelID and Provider are untrusted upstream identifiers:
// they are bounded and validated by the fetch rail before they reach this
// repository, and are rendered as plain text by every sink. Status is a
// bounded state marker ('ok'); the row set is emptied on fetch failure, so a
// failed combo never shows stale success rows.
type FetchedModel struct {
	ID              int64
	EndpointKeyID   int64
	UpstreamModelID string
	Provider        string
	FetchedAt       int64
	Status          string
}

// FetchState is the ownership-scoped projection the model fetch rail reads
// before dialing an upstream: connector type, canonical base URL, and the
// enabled flags of both the endpoint and the key. It never carries the
// encrypted secret (the ciphertext has its own dedicated accessor).
type FetchState struct {
	ConnectorType   string
	BaseURL         string
	EndpointEnabled bool
	KeyEnabled      bool
}

// GetEndpointKeyFetchState returns the fetch projection for keyID on
// endpointID owned by userID. The SQL constrains all three ids in one join,
// so a missing key, a wrong endpoint, and a cross-user path are
// indistinguishable (ErrNotFound). This is the read gate the fetch rail and
// the manual-refresh handler use before any upstream work.
func (s *Store) GetEndpointKeyFetchState(ctx context.Context, userID, endpointID, keyID int64) (FetchState, error) {
	var st FetchState
	var endpointEnabled, keyEnabled int
	row := s.db.QueryRowContext(ctx, `
SELECT e.connector_type, e.base_url, e.enabled, ek.enabled
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`, keyID, endpointID, userID)
	err := row.Scan(&st.ConnectorType, &st.BaseURL, &endpointEnabled, &keyEnabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FetchState{}, ErrNotFound
		}
		return FetchState{}, fmt.Errorf("read endpoint key fetch state: %w", err)
	}
	st.EndpointEnabled = endpointEnabled != 0
	st.KeyEnabled = keyEnabled != 0
	return st, nil
}

// GetEndpointKeyCiphertext returns the sealed AES envelope of keyID on
// endpointID owned by userID. It is the only path that reads encrypted_secret
// (forwarding and fetching); every other endpoint-key query selects metadata
// and display fragments only. The caller owns the returned envelope and must
// never place it in metadata, responses, logs, or issue messages; the fetch
// rail clears the decrypted plaintext promptly.
func (s *Store) GetEndpointKeyCiphertext(ctx context.Context, userID, endpointID, keyID int64) (string, error) {
	var ciphertext string
	row := s.db.QueryRowContext(ctx, `
SELECT ek.encrypted_secret
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`, keyID, endpointID, userID)
	err := row.Scan(&ciphertext)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read endpoint key ciphertext: %w", err)
	}
	return ciphertext, nil
}

// ListFetchedModels returns the cached upstream models for keyID on
// endpointID owned by userID, ordered by upstream_model_id. A missing or
// cross-user combo yields ErrNotFound (indistinguishable); an owned combo
// with no cache rows yields an empty slice. The rows are untrusted upstream
// metadata only — never secret material.
func (s *Store) ListFetchedModels(ctx context.Context, userID, endpointID, keyID int64) ([]FetchedModel, error) {
	if _, err := s.GetEndpointKeyFetchState(ctx, userID, endpointID, keyID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT fm.id, fm.endpoint_key_id, fm.upstream_model_id, fm.provider, fm.fetched_at, fm.status
FROM fetched_models fm
JOIN endpoint_keys ek ON fm.endpoint_key_id = ek.id
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?
ORDER BY fm.upstream_model_id`, keyID, endpointID, userID)
	if err != nil {
		return nil, fmt.Errorf("list fetched models: %w", err)
	}
	defer rows.Close()
	var out []FetchedModel
	for rows.Next() {
		var fm FetchedModel
		if err := rows.Scan(&fm.ID, &fm.EndpointKeyID, &fm.UpstreamModelID, &fm.Provider, &fm.FetchedAt, &fm.Status); err != nil {
			return nil, fmt.Errorf("scan fetched model: %w", err)
		}
		out = append(out, fm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fetched models: %w", err)
	}
	return out, nil
}

// ReplaceFetchedModels atomically replaces the cache of keyID on endpointID
// owned by userID with models, in one transaction: the old rows are removed,
// the new rows are inserted only while the key still exists (a key deleted
// concurrently makes the whole replacement a no-op instead of an FK error),
// and the endpoint's model_fetch_failed flag is cleared (a successful fetch
// resets the flag). A missing or cross-user combo yields ErrNotFound and
// writes nothing.
func (s *Store) ReplaceFetchedModels(ctx context.Context, userID, endpointID, keyID int64, models []FetchedModel, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace fetched models: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := clearComboCacheLocked(ctx, tx, userID, endpointID, keyID); err != nil {
		return err
	}
	// The key may have been deleted while this fetch was in flight. The
	// ownership-scoped EXISTS keeps the insert a no-op instead of an FK
	// failure, linearizing the late fetch against the delete: a vanished or
	// cross-user combo is indistinguishable ErrNotFound and writes nothing.
	var exists int
	err = tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`, keyID, endpointID, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check endpoint key for fetched models: %w", err)
	}
	if exists == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit noop replace fetched models: %w", err)
		}
		committed = true
		return ErrNotFound
	}

	status := "ok"
	for _, m := range models {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status)
VALUES (?,?,?,?,?)`,
			keyID, m.UpstreamModelID, m.Provider, now, status); err != nil {
			return fmt.Errorf("insert fetched model: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE endpoints SET model_fetch_failed=0, model_fetch_failed_at=0, updated_at=?
WHERE id=? AND user_id=?`, now, endpointID, userID); err != nil {
		return fmt.Errorf("clear endpoint fetch flag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace fetched models: %w", err)
	}
	committed = true
	return nil
}

// FailFetch atomically records a failed upstream model fetch for keyID on
// endpointID owned by userID: the combo's cache is cleared, the endpoint's
// model_fetch_failed flag is set with its timestamp, and a user-issue row is
// inserted with an atomic user-existence guard (a deleted user makes the
// issue insert a no-op, linearizing the late failure against account
// deletion). kind/message/ref are bounded, sanitized, secret-free text
// prepared by the caller. A missing or cross-user combo yields ErrNotFound
// and writes nothing.
func (s *Store) FailFetch(ctx context.Context, userID, endpointID, keyID int64, kind, message, ref string, now int64) error {
	// Keep the repository a final bounded sink as well as trusting the fetcher
	// boundary. Future rails must not be able to insert an unbounded or
	// line-forging issue by calling this shared method directly.
	kind = diagnostic.BoundTo(kind, 128)
	message = diagnostic.BoundTo(message, 1024)
	ref = diagnostic.BoundTo(ref, 256)
	if kind == "" {
		kind = "model_fetch_failed"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail fetch: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := clearComboCacheLocked(ctx, tx, userID, endpointID, keyID); err != nil {
		return err
	}
	// A late failure for a deleted key must be a complete no-op. Checking only
	// the endpoint below would otherwise flag a still-existing endpoint and
	// create an issue for a combo that no longer exists.
	var comboExists int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id=e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`, keyID, endpointID, userID).Scan(&comboExists); err != nil {
		return fmt.Errorf("check endpoint key for fail fetch: %w", err)
	}
	if comboExists == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit noop fail fetch: %w", err)
		}
		committed = true
		return ErrNotFound
	}
	res, err := tx.ExecContext(ctx, `
UPDATE endpoints SET model_fetch_failed=1, model_fetch_failed_at=?, updated_at=?
WHERE id=? AND user_id=?`, now, now, endpointID, userID)
	if err != nil {
		return fmt.Errorf("set endpoint fetch flag: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("endpoint fetch flag rows affected: %w", err)
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit fail fetch noop: %w", err)
		}
		committed = true
		return ErrNotFound
	}

	// INSERT ... SELECT ... WHERE EXISTS: the issue is written only while the
	// user still exists, so a late failure after account deletion no-ops.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_issues (user_id, kind, message, ref, created_at)
SELECT ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM users WHERE id=?)`,
		userID, kind, message, ref, now, userID); err != nil {
		return fmt.Errorf("insert fetch issue: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fail fetch: %w", err)
	}
	committed = true
	return nil
}

// clearComboCacheLocked removes every fetched_models row for keyID on
// endpointID owned by userID. The ownership is enforced in SQL (the key must
// belong to the endpoint and the endpoint to the user), so a cross-user or
// stale combo clears nothing.
func clearComboCacheLocked(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) error {
	if _, err := tx.ExecContext(ctx, `
DELETE FROM fetched_models
WHERE endpoint_key_id=?
  AND endpoint_key_id IN (
    SELECT ek.id FROM endpoint_keys ek
    JOIN endpoints e ON ek.endpoint_id = e.id
    WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?)`,
		keyID, keyID, endpointID, userID); err != nil {
		return fmt.Errorf("clear fetched models: %w", err)
	}
	return nil
}
