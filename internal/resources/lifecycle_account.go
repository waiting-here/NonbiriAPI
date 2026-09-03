package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

const lifecycleCollectionLimit = 10_000

// LifecycleResourceExport is the closed resource slice consumed by the
// account lifecycle coordinator. The types below deliberately omit secret
// references, fingerprints, discovery diagnostics, and every revision.
type LifecycleResourceExport struct {
	Endpoints    []LifecycleEndpoint
	CatalogPairs []LifecycleCatalogPair
	Models       []LifecycleModel
	CallerKey    *LifecycleCallerKey
}

type LifecycleEndpoint struct {
	ID            string
	ConnectorType string
	BaseURL       string
	Origin        EndpointOrigin
	Note          string
	Enabled       bool
	CreatedAt     int64
	UpdatedAt     int64
	Keys          []LifecycleEndpointKey
}

type LifecycleEndpointKey struct {
	ID              string
	DisplayHead     string
	DisplayTail     string
	Note            string
	Enabled         bool
	ForceStoreFalse bool
	SuspensionState string
	CreatedAt       int64
	UpdatedAt       int64
}

type LifecycleDiscoveryEvidence struct {
	State      string
	Result     *string
	SafeClass  string
	ObservedAt *int64
	Count      *string
}

type LifecycleCatalogEntry struct {
	ID              string
	SourceType      string
	UpstreamModelID string
	Provider        string
	CreatedAt       int64
	UpdatedAt       int64
}

type LifecycleCatalogPair struct {
	EndpointID       string
	EndpointKeyID    string
	Evidence         LifecycleDiscoveryEvidence
	AutomaticEntries []LifecycleCatalogEntry
	ManualEntries    []LifecycleCatalogEntry
}

type LifecycleModel struct {
	ID               string
	Provider         string
	Model            string
	FullName         string
	RouteStrategy    string
	SilentRetry      bool
	FlattenToolCalls bool
	CreatedAt        int64
	UpdatedAt        int64
	Bindings         []LifecycleBinding
}

type LifecycleBinding struct {
	ID                     string
	EndpointKeyID          string
	EndpointBaseURL        string
	ConnectorType          string
	EndpointNote           string
	EndpointKeyDisplayHead string
	EndpointKeyDisplayTail string
	EndpointKeyNote        string
	UpstreamModelID        string
	Ord                    int
}

type LifecycleCallerKey struct {
	Display    string
	Generation string
	CreatedAt  int64
	UpdatedAt  int64
}

// ExportLifecycleResources reads every resource collection from the supplied
// transaction. Each JSON array is independently probed at limit+1 and the
// method fails instead of returning a truncated document.
func (r *Repository) ExportLifecycleResources(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (LifecycleResourceExport, error) {
	empty := LifecycleResourceExport{
		Endpoints: []LifecycleEndpoint{}, CatalogPairs: []LifecycleCatalogPair{}, Models: []LifecycleModel{},
	}
	if r == nil || r.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > lifecycleCollectionLimit {
		return empty, ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return empty, fmt.Errorf("resources: read lifecycle export owner: %w", err)
	}
	if exists != 1 {
		return empty, ErrNotFound
	}

	endpointRows, err := tx.QueryContext(ctx, `
SELECT e.id,e.connector_type,e.base_url,e.note,e.enabled,e.revision,
       (SELECT count(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id),
       e.created_at,e.updated_at,e.mainstream_channel_id,e.mainstream_channel_revision,
       e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoints e
WHERE e.user_id=?
ORDER BY e.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return empty, fmt.Errorf("resources: list lifecycle endpoints: %w", err)
	}
	endpoints := make([]Endpoint, 0, limit+1)
	for endpointRows.Next() {
		endpoint, scanErr := scanEndpoint(endpointRows)
		if scanErr != nil {
			_ = endpointRows.Close()
			return empty, fmt.Errorf("resources: scan lifecycle endpoint: %w", scanErr)
		}
		endpoints = append(endpoints, endpoint)
	}
	if err := endpointRows.Err(); err != nil {
		_ = endpointRows.Close()
		return empty, fmt.Errorf("resources: iterate lifecycle endpoints: %w", err)
	}
	if err := endpointRows.Close(); err != nil {
		return empty, fmt.Errorf("resources: close lifecycle endpoints: %w", err)
	}
	if len(endpoints) > limit {
		return empty, ErrResourceLimit
	}

	for _, endpoint := range endpoints {
		endpointID, err := parseDecimalID(endpoint.ID)
		if err != nil {
			return empty, ErrUnavailable
		}
		keys, err := lifecycleEndpointKeysTx(ctx, tx, userID, endpointID, limit)
		if err != nil {
			return empty, err
		}
		item := LifecycleEndpoint{
			ID: endpoint.ID, ConnectorType: endpoint.ConnectorType, BaseURL: endpoint.BaseURL,
			Origin: endpoint.Origin, Note: endpoint.Note, Enabled: endpoint.Enabled, CreatedAt: endpoint.CreatedAt,
			UpdatedAt: endpoint.UpdatedAt, Keys: make([]LifecycleEndpointKey, 0, len(keys)),
		}
		for _, key := range keys {
			item.Keys = append(item.Keys, LifecycleEndpointKey{
				ID: key.ID, DisplayHead: key.DisplayHead, DisplayTail: key.DisplayTail, Note: key.Note,
				Enabled: key.Enabled, ForceStoreFalse: key.ForceStoreFalse,
				SuspensionState: key.SuspensionState, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt,
			})
			if len(empty.CatalogPairs) == limit {
				return empty, ErrResourceLimit
			}
			keyID, err := parseDecimalID(key.ID)
			if err != nil {
				return empty, ErrUnavailable
			}
			pair, err := lifecycleCatalogPairTx(ctx, tx, userID, endpointID, keyID, limit)
			if err != nil {
				return empty, err
			}
			empty.CatalogPairs = append(empty.CatalogPairs, pair)
		}
		empty.Endpoints = append(empty.Endpoints, item)
	}

	models, err := lifecycleModelsTx(ctx, tx, userID, limit)
	if err != nil {
		return empty, err
	}
	for _, model := range models {
		modelID, err := parseDecimalID(model.ID)
		if err != nil {
			return empty, ErrUnavailable
		}
		bindings, err := lifecycleBindingsTx(ctx, tx, userID, modelID, limit)
		if err != nil {
			return empty, err
		}
		item := LifecycleModel{
			ID: model.ID, Provider: model.Provider, Model: model.Model, FullName: model.FullName,
			RouteStrategy: model.RouteStrategy, SilentRetry: model.SilentRetry,
			FlattenToolCalls: model.FlattenToolCalls, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
			Bindings: make([]LifecycleBinding, 0, len(bindings)),
		}
		for _, binding := range bindings {
			item.Bindings = append(item.Bindings, LifecycleBinding{
				ID: binding.ID, EndpointKeyID: binding.EndpointKeyID,
				EndpointBaseURL: binding.EndpointBaseURL, ConnectorType: binding.ConnectorType,
				EndpointNote:           binding.EndpointNote,
				EndpointKeyDisplayHead: binding.EndpointKeyDisplayHead,
				EndpointKeyDisplayTail: binding.EndpointKeyDisplayTail,
				EndpointKeyNote:        binding.EndpointKeyNote, UpstreamModelID: binding.UpstreamModelID,
				Ord: binding.Ord,
			})
		}
		empty.Models = append(empty.Models, item)
	}
	callerKey, err := lifecycleCallerKeyTx(ctx, tx, userID)
	if err != nil {
		return empty, err
	}
	empty.CallerKey = callerKey
	return empty, nil
}

func lifecycleEndpointKeysTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, limit int) ([]EndpointKey, error) {
	rows, err := tx.QueryContext(ctx, endpointKeySelect+`
WHERE e.user_id=? AND e.id=?
ORDER BY k.id
LIMIT ?`, userID, endpointID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("resources: list lifecycle endpoint keys: %w", err)
	}
	defer rows.Close()
	items := make([]EndpointKey, 0, limit+1)
	for rows.Next() {
		item, err := scanEndpointKey(rows)
		if err != nil {
			return nil, fmt.Errorf("resources: scan lifecycle endpoint key: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate lifecycle endpoint keys: %w", err)
	}
	if len(items) > limit {
		return nil, ErrResourceLimit
	}
	return items, nil
}

func lifecycleCatalogPairTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, endpointID, keyID int64,
	limit int,
) (LifecycleCatalogPair, error) {
	owner, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	evidence, err := owner.evidence()
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	endpointText, err := decimalID(endpointID)
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	keyText, err := decimalID(keyID)
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	pair := LifecycleCatalogPair{
		EndpointID: endpointText, EndpointKeyID: keyText,
		Evidence: LifecycleDiscoveryEvidence{
			State: evidence.State, Result: evidence.Result, SafeClass: evidence.SafeClass,
			ObservedAt: evidence.ObservedAt, Count: evidence.Count,
		},
		AutomaticEntries: []LifecycleCatalogEntry{}, ManualEntries: []LifecycleCatalogEntry{},
	}
	pair.AutomaticEntries, err = lifecycleCatalogEntriesTx(ctx, tx, keyID, "automatic", limit)
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	pair.ManualEntries, err = lifecycleCatalogEntriesTx(ctx, tx, keyID, "manual", limit)
	if err != nil {
		return LifecycleCatalogPair{}, err
	}
	return pair, nil
}

func lifecycleCatalogEntriesTx(ctx context.Context, tx *sql.Tx, keyID int64, source string, limit int) ([]LifecycleCatalogEntry, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT c.id,c.source_type,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision,c.created_at,c.updated_at
FROM model_catalog_entries c
JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE c.endpoint_key_id=? AND c.source_type=?
ORDER BY c.id
LIMIT ?`, keyID, source, limit+1)
	if err != nil {
		return nil, fmt.Errorf("resources: list lifecycle catalog entries: %w", err)
	}
	defer rows.Close()
	items := make([]LifecycleCatalogEntry, 0, limit+1)
	for rows.Next() {
		entry, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("resources: scan lifecycle catalog entry: %w", err)
		}
		items = append(items, LifecycleCatalogEntry{
			ID: entry.ID, SourceType: entry.SourceType, UpstreamModelID: entry.UpstreamModelID,
			Provider: entry.Provider, CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate lifecycle catalog entries: %w", err)
	}
	if len(items) > limit {
		return nil, ErrResourceLimit
	}
	return items, nil
}

func lifecycleModelsTx(ctx context.Context, tx *sql.Tx, userID int64, limit int) ([]Model, error) {
	rows, err := tx.QueryContext(ctx, modelSelect+`
WHERE m.user_id=?
ORDER BY m.id
LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("resources: list lifecycle models: %w", err)
	}
	defer rows.Close()
	items := make([]Model, 0, limit+1)
	for rows.Next() {
		item, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("resources: scan lifecycle model: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate lifecycle models: %w", err)
	}
	if len(items) > limit {
		return nil, ErrResourceLimit
	}
	return items, nil
}

func lifecycleBindingsTx(ctx context.Context, tx *sql.Tx, userID, modelID int64, limit int) ([]Binding, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT b.id,b.endpoint_key_id,e.base_url,e.connector_type,e.note,k.display_head,k.display_tail,k.note,b.upstream_model_id,b.ord
FROM model_bindings b
JOIN models m ON m.id=b.model_id
JOIN endpoint_keys k ON k.id=b.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
WHERE m.user_id=? AND m.id=? AND e.user_id=?
ORDER BY b.ord,b.id
LIMIT ?`, userID, modelID, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("resources: list lifecycle bindings: %w", err)
	}
	defer rows.Close()
	items := make([]Binding, 0, limit+1)
	for rows.Next() {
		var item Binding
		var id, keyID int64
		if err := rows.Scan(&id, &keyID, &item.EndpointBaseURL, &item.ConnectorType, &item.EndpointNote,
			&item.EndpointKeyDisplayHead, &item.EndpointKeyDisplayTail, &item.EndpointKeyNote,
			&item.UpstreamModelID, &item.Ord); err != nil {
			return nil, fmt.Errorf("resources: scan lifecycle binding: %w", err)
		}
		if item.Ord < 0 || item.Ord > 255 {
			return nil, ErrUnavailable
		}
		var err error
		item.ID, err = decimalID(id)
		if err != nil {
			return nil, err
		}
		item.EndpointKeyID, err = decimalID(keyID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate lifecycle bindings: %w", err)
	}
	if len(items) > limit {
		return nil, ErrResourceLimit
	}
	return items, nil
}

func lifecycleCallerKeyTx(ctx context.Context, tx *sql.Tx, userID int64) (*LifecycleCallerKey, error) {
	var generation int64
	var hash []byte
	var head, tail string
	var created sql.NullInt64
	var updated int64
	err := tx.QueryRowContext(ctx, `
SELECT generation,key_hash,display_head,display_tail,key_created_at,updated_at
FROM caller_keys WHERE user_id=?`, userID).Scan(&generation, &hash, &head, &tail, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("resources: read lifecycle caller key: %w", err)
	}
	if generation < 0 {
		return nil, ErrUnavailable
	}
	if hash == nil {
		if head != "" || tail != "" || created.Valid {
			return nil, ErrUnavailable
		}
		return nil, nil
	}
	if len(hash) != sha256.Size || !created.Valid || len(head) != 4 || len(tail) != 4 {
		return nil, ErrUnavailable
	}
	return &LifecycleCallerKey{
		Display: callerKeyPrefix + head + "…" + tail, Generation: strconv.FormatInt(generation, 10),
		CreatedAt: created.Int64, UpdatedAt: updated,
	}, nil
}

// PrepareLifecycleAccountDeletion removes owner-private resources and revokes
// CallerKey authority in the caller-owned account deletion transaction.
func (r *Repository) PrepareLifecycleAccountDeletion(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) error {
	if r == nil || r.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond || isNilInterface(r.secrets) || isNilInterface(r.keyDeletion) {
		return ErrInvalidRequest
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("resources: read lifecycle deletion owner: %w", err)
	}
	if exists != 1 {
		return ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `
SELECT k.id,k.secret_ref_id
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE e.user_id=?
ORDER BY k.id`, userID)
	if err != nil {
		return fmt.Errorf("resources: list lifecycle deletion keys: %w", err)
	}
	keyIDs := make([]int64, 0)
	secretRefs := make([]int64, 0)
	for rows.Next() {
		var keyID, secretRef int64
		if err := rows.Scan(&keyID, &secretRef); err != nil {
			_ = rows.Close()
			return fmt.Errorf("resources: scan lifecycle deletion key: %w", err)
		}
		if keyID <= 0 || secretRef <= 0 {
			_ = rows.Close()
			return ErrUnavailable
		}
		keyIDs = append(keyIDs, keyID)
		secretRefs = append(secretRefs, secretRef)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("resources: iterate lifecycle deletion keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("resources: close lifecycle deletion keys: %w", err)
	}
	if len(keyIDs) != 0 {
		if err := r.keyDeletion.PrepareEndpointKeyDeletion(ctx, tx, userID, keyIDs, decisionNow); err != nil {
			return fmt.Errorf("resources: prepare lifecycle endpoint key deletion: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("resources: delete lifecycle models: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoints WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("resources: delete lifecycle endpoints: %w", err)
	}
	for _, secretRef := range secretRefs {
		if err := r.secrets.MarkEndpointSecretOrphaned(ctx, tx, secretRef, decisionNow); err != nil {
			return fmt.Errorf("resources: orphan lifecycle endpoint secret: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM caller_keys WHERE user_id=?`, userID)
	if err != nil {
		return fmt.Errorf("resources: revoke lifecycle caller key: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resources: observe lifecycle caller key revocation: %w", err)
	}
	if deleted != 1 {
		return ErrUnavailable
	}
	return nil
}
