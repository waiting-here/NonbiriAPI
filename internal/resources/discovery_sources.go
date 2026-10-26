package resources

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type CreateDiscoverySourceInput struct {
	Name, BaseURL, ListFormat string
	CredentialID              int64
	Enabled                   bool
}

type PatchDiscoverySourceInput struct {
	Name, BaseURL, ListFormat *string
	CredentialID              *int64
	Enabled                   *bool
	ExpectedRevision          int64
}

const discoverySourceSelect = `SELECT ds.id,ds.connection_id,ds.name,sr.canonical_base_url,sr.list_format,sr.credential_id,v.version,ds.revision,sr.source_revision,ds.enabled,
 COALESCE(run.state,'unknown'),run.completed_at,COALESCE(run.safe_class,'none'),COALESCE(run.safe_diagnostic,''),ds.created_at,ds.updated_at,
 EXISTS(SELECT 1 FROM credentials cr JOIN endpoint_key_secrets sec ON sec.id=v.secret_ref_id WHERE cr.id=v.credential_id AND cr.deleted_at IS NULL AND cr.enabled=1 AND v.enabled=1 AND sec.orphaned_at IS NULL),
 (SELECT endpoint_key_id FROM discovery_source_keys WHERE source_id=ds.id)
 FROM discovery_sources ds JOIN discovery_source_revisions sr ON sr.id=ds.current_revision_id AND sr.source_id=ds.id
 JOIN credential_versions v ON v.id=sr.credential_version_id AND v.credential_id=sr.credential_id
 LEFT JOIN discovery_source_states st ON st.source_revision_id=sr.id LEFT JOIN discovery_runs run ON run.id=st.current_run_id
 WHERE ds.user_id=?`

func scanDiscoverySource(scanner pageScanner) (DiscoverySource, error) {
	var item DiscoverySource
	var id, credentialID, version, revision, sourceRevision int64
	var connectionID, checkedAt, endpointKeyID sql.NullInt64
	var enabled int
	if err := scanner.Scan(&id, &connectionID, &item.Name, &item.BaseURL, &item.ListFormat, &credentialID, &version, &revision, &sourceRevision, &enabled, &item.State, &checkedAt, &item.SafeClass, &item.SafeDiagnostic, &item.CreatedAt, &item.UpdatedAt, &item.CredentialAvailable, &endpointKeyID); err != nil {
		return item, err
	}
	if id <= 0 || credentialID <= 0 || version <= 0 || revision <= 0 || sourceRevision <= 0 || enabled < 0 || enabled > 1 || !validDiscoveryListFormat(item.ListFormat) || !optionalChoice(item.State, "unknown", "checking", "succeeded", "failed") {
		return DiscoverySource{}, ErrUnavailable
	}
	var err error
	item.ConnectionID, err = optionalDecimal(connectionID)
	if err != nil {
		return DiscoverySource{}, err
	}
	item.EndpointKeyID, err = optionalDecimal(endpointKeyID)
	if err != nil {
		return DiscoverySource{}, err
	}
	item.ID, item.CredentialID, item.CredentialVersion, item.Revision, item.DiscoverySourceRevision = strconv.FormatInt(id, 10), strconv.FormatInt(credentialID, 10), strconv.FormatInt(version, 10), strconv.FormatInt(revision, 10), strconv.FormatInt(sourceRevision, 10)
	kind, err := discoveryConnectorType(item.ListFormat)
	if err != nil {
		return DiscoverySource{}, ErrUnavailable
	}
	item.ModelsURL, err = connector.DiscoveryModelsURL(kind, item.BaseURL)
	if err != nil {
		return DiscoverySource{}, ErrUnavailable
	}
	item.Enabled = enabled == 1
	if checkedAt.Valid {
		value := checkedAt.Int64
		item.CheckedAt = &value
	}
	return item, nil
}

func getDiscoverySourceTx(ctx context.Context, tx *sql.Tx, userID, sourceID int64) (DiscoverySource, error) {
	item, err := scanDiscoverySource(tx.QueryRowContext(ctx, discoverySourceSelect+` AND ds.id=?`, userID, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return DiscoverySource{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) GetDiscoverySource(ctx context.Context, userID, sourceID int64) (DiscoverySource, error) {
	if sourceID <= 0 {
		return DiscoverySource{}, ErrNotFound
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (DiscoverySource, error) {
		return getDiscoverySourceTx(ctx, tx, userID, sourceID)
	})
}

func (r *Repository) listConnectionSources(ctx context.Context, userID, connectionID int64, search, enabled string, window connectionListWindow) (Page[DiscoverySource], error) {
	if connectionID <= 0 || !validateFreeText(search, 0, 128) || !optionalChoice(enabled, "true", "false") {
		return Page[DiscoverySource]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[DiscoverySource], error) {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return Page[DiscoverySource]{}, err
		}
		query := discoverySourceSelect + ` AND ds.connection_id=?`
		args := []any{userID, connectionID}
		if search = strings.TrimSpace(search); search != "" {
			query += ` AND (instr(lower(ds.name),lower(?))>0 OR instr(lower(sr.canonical_base_url),lower(?))>0)`
			args = append(args, search, search)
		}
		if enabled != "" {
			query += ` AND ds.enabled=?`
			args = append(args, enabled == "true")
		}
		return readConnectionList(ctx, tx, r, window, "connection-sources", query, "ds.id", args, scanDiscoverySource, func(item DiscoverySource) string { return item.ID })
	})
}

func (r *Repository) CreateDiscoverySource(ctx context.Context, userID, connectionID int64, mutation ControlMutation, input CreateDiscoverySourceInput) (MutationResult[DiscoverySource], error) {
	if r == nil || userID <= 0 || connectionID <= 0 || !validResourceMutation(mutation, http.MethodPost, routeConnectionSources, connectionID) || !validateFreeText(input.Name, 0, 128) || !validDiscoveryListFormat(input.ListFormat) || input.CredentialID <= 0 {
		return MutationResult[DiscoverySource]{}, ErrInvalidRequest
	}
	baseURL, err := r.baseURLs.ValidateBaseURL(input.BaseURL)
	if err != nil {
		return MutationResult[DiscoverySource]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusCreated, "discovery_source", func(ctx context.Context, tx *sql.Tx, now int64) (DiscoverySource, ResourceOperationResult, error) {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		limit, err := effectiveEndpointLimitTx(ctx, tx, userID)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		count, err := countTx(ctx, tx, `SELECT count(*) FROM discovery_sources ds WHERE ds.user_id=? AND NOT EXISTS(SELECT 1 FROM endpoint_key_discovery_sources a WHERE a.source_id=ds.id)`, userID)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if count >= limit {
			return DiscoverySource{}, ResourceOperationResult{}, ErrResourceLimit
		}
		ref, err := currentCredentialReferenceTx(ctx, tx, userID, input.CredentialID)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if err := requireCredentialConnectionTx(ctx, tx, userID, input.CredentialID, connectionID); err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		id, _, err := createDiscoverySourceTx(ctx, tx, userID, connectionID, ref.credentialID, ref.versionID, input.Name, baseURL, input.ListFormat, input.Enabled, now)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		item, err := getDiscoverySourceTx(ctx, tx, userID, id)
		return item, ResourceOperationResult{ConnectionID: strconv.FormatInt(connectionID, 10), DiscoverySourceID: item.ID, DiscoverySourceRevision: item.DiscoverySourceRevision}, err
	})
}

func (r *Repository) PatchDiscoverySource(ctx context.Context, userID, sourceID int64, mutation ControlMutation, input PatchDiscoverySourceInput) (MutationResult[DiscoverySource], error) {
	if r == nil || userID <= 0 || sourceID <= 0 || !validResourceMutation(mutation, http.MethodPatch, routeDiscoverySource, sourceID) || input.ExpectedRevision <= 0 ||
		(input.Name == nil && input.BaseURL == nil && input.ListFormat == nil && input.CredentialID == nil && input.Enabled == nil) || (input.Name != nil && !validateFreeText(*input.Name, 0, 128)) || (input.ListFormat != nil && !validDiscoveryListFormat(*input.ListFormat)) || (input.CredentialID != nil && *input.CredentialID <= 0) {
		return MutationResult[DiscoverySource]{}, ErrInvalidRequest
	}
	if input.BaseURL != nil {
		baseURL, err := r.baseURLs.ValidateBaseURL(*input.BaseURL)
		if err != nil {
			return MutationResult[DiscoverySource]{}, ErrInvalidRequest
		}
		input.BaseURL = &baseURL
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusOK, "", func(ctx context.Context, tx *sql.Tx, now int64) (DiscoverySource, ResourceOperationResult, error) {
		item, err := getDiscoverySourceTx(ctx, tx, userID, sourceID)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if item.Revision != strconv.FormatInt(input.ExpectedRevision, 10) || input.ExpectedRevision == math.MaxInt64 {
			return DiscoverySource{}, ResourceOperationResult{}, ErrConflict
		}
		var currentRevisionID, oldVersionID int64
		if err := tx.QueryRowContext(ctx, `SELECT ds.current_revision_id,sr.credential_version_id FROM discovery_sources ds JOIN discovery_source_revisions sr ON sr.id=ds.current_revision_id WHERE ds.id=? AND ds.user_id=?`, sourceID, userID).Scan(&currentRevisionID, &oldVersionID); err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if input.Name != nil {
			item.Name = *input.Name
		}
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
		}
		if input.BaseURL != nil || input.ListFormat != nil || input.CredentialID != nil {
			if input.BaseURL != nil {
				item.BaseURL = *input.BaseURL
			}
			if input.ListFormat != nil {
				item.ListFormat = *input.ListFormat
			}
			credentialID, _ := parseDecimalID(item.CredentialID)
			versionID := oldVersionID
			if input.CredentialID != nil {
				if item.ConnectionID == nil {
					return DiscoverySource{}, ResourceOperationResult{}, ErrNotFound
				}
				connectionID, _ := parseDecimalID(*item.ConnectionID)
				if err := requireCredentialConnectionTx(ctx, tx, userID, *input.CredentialID, connectionID); err != nil {
					return DiscoverySource{}, ResourceOperationResult{}, err
				}
				ref, err := currentCredentialReferenceTx(ctx, tx, userID, *input.CredentialID)
				if err != nil {
					return DiscoverySource{}, ResourceOperationResult{}, err
				}
				credentialID, versionID = ref.credentialID, ref.versionID
			}
			revision, _ := parseDecimalID(item.DiscoverySourceRevision)
			if revision == math.MaxInt64 {
				return DiscoverySource{}, ResourceOperationResult{}, ErrConflict
			}
			currentRevisionID, err = nextConnectionIdentityTx(ctx, tx, "discovery_source_revisions")
			if err != nil {
				return DiscoverySource{}, ResourceOperationResult{}, err
			}
			if err := insertSourceRevisionTx(ctx, tx, currentRevisionID, sourceID, userID, credentialID, versionID, revision+1, item.BaseURL, item.ListFormat, now); err != nil {
				return DiscoverySource{}, ResourceOperationResult{}, err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE discovery_sources SET name=?,enabled=?,revision=revision+1,current_revision_id=?,updated_at=? WHERE id=? AND user_id=? AND revision=?`, item.Name, boolInt(item.Enabled), currentRevisionID, now, sourceID, userID, input.ExpectedRevision)
		if err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return DiscoverySource{}, ResourceOperationResult{}, ErrConflict
		}
		if err := r.reconcileSourceKeysTx(ctx, tx, userID, sourceID, now); err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, err
		}
		if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, oldVersionID, now); err != nil {
			return DiscoverySource{}, ResourceOperationResult{}, mapConnectionStoreError(err)
		}
		item, err = getDiscoverySourceTx(ctx, tx, userID, sourceID)
		return item, ResourceOperationResult{}, err
	})
}

func (r *Repository) reconcileSourceKeysTx(ctx context.Context, tx *sql.Tx, userID, sourceID, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT a.endpoint_key_id FROM endpoint_key_discovery_sources a JOIN endpoints e ON e.id=(SELECT endpoint_id FROM endpoint_keys WHERE id=a.endpoint_key_id) WHERE a.source_id=? AND e.user_id=? ORDER BY a.endpoint_key_id`, sourceID, userID)
	if err != nil {
		return err
	}
	keys := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, keyID := range keys {
		if err := setKeySourceTx(ctx, tx, userID, keyID, &sourceID); errors.Is(err, ErrConflict) {
			if err := setKeySourceTx(ctx, tx, userID, keyID, nil); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := r.rebuildKeyCatalogProjectionTx(ctx, tx, userID, keyID, now); err != nil {
			return err
		}
		if err := r.projection.ReconcileModelDiscovery(ctx, tx, userID, keyID); err != nil {
			return err
		}
	}
	return nil
}
