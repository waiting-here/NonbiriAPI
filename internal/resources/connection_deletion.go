package resources

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func (r *Repository) DeleteDiscoverySource(ctx context.Context, userID, sourceID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || sourceID <= 0 || expectedRevision <= 0 || !validResourceMutation(mutation, http.MethodDelete, routeDiscoverySource, sourceID) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusNoContent, "", func(ctx context.Context, tx *sql.Tx, now int64) (struct{}, ResourceOperationResult, error) {
		item, err := getDiscoverySourceTx(ctx, tx, userID, sourceID)
		if err != nil {
			return struct{}{}, ResourceOperationResult{}, err
		}
		if item.Revision != strconv.FormatInt(expectedRevision, 10) {
			return struct{}{}, ResourceOperationResult{}, ErrConflict
		}
		return struct{}{}, ResourceOperationResult{}, r.deleteSourceInTransaction(ctx, tx, userID, sourceID, now)
	})
}

func (r *Repository) deleteSourceInTransaction(ctx context.Context, tx *sql.Tx, userID, sourceID, now int64) error {
	keys, err := resourceIDsTx(ctx, tx, `SELECT ks.endpoint_key_id FROM endpoint_key_discovery_sources ks JOIN discovery_sources s ON s.id=ks.source_id WHERE s.id=? AND s.user_id=?`, sourceID, userID)
	if err != nil {
		return err
	}
	if err := connectionstore.DeleteDiscoverySourceTx(ctx, tx, userID, sourceID, now); err != nil {
		return mapConnectionStoreError(err)
	}
	for _, id := range keys {
		if err := r.rebuildKeyCatalogProjectionTx(ctx, tx, userID, id, now); err != nil {
			return err
		}
		if err := r.projection.ReconcileModelDiscovery(ctx, tx, userID, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) DeleteConnection(ctx context.Context, userID, connectionID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || connectionID <= 0 || expectedRevision <= 0 || !validResourceMutation(mutation, http.MethodDelete, routeConnection, connectionID) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusNoContent, "", func(ctx context.Context, tx *sql.Tx, now int64) (struct{}, ResourceOperationResult, error) {
		err := r.deleteConnectionInTransaction(ctx, tx, userID, connectionID, expectedRevision, now)
		return struct{}{}, ResourceOperationResult{}, err
	})
}

func (r *Repository) deleteConnectionInTransaction(ctx context.Context, tx *sql.Tx, userID, connectionID, expectedRevision, now int64) error {
	item, err := getConnectionTx(ctx, tx, userID, connectionID)
	if err != nil {
		return err
	}
	if item.Revision != strconv.FormatInt(expectedRevision, 10) {
		return ErrConflict
	}
	credentials, err := resourceIDsTx(ctx, tx, `SELECT cr.id FROM credentials cr JOIN service_connections sc ON sc.id=? AND sc.user_id=cr.user_id WHERE cr.user_id=? AND cr.deleted_at IS NULL AND `+connectionCredentialMembership, connectionID, userID)
	if err != nil {
		return err
	}
	endpoints, err := resourceIDsTx(ctx, tx, "SELECT id FROM endpoints WHERE connection_id=? AND user_id=? ORDER BY id", connectionID, userID)
	if err != nil {
		return err
	}
	for _, id := range endpoints {
		endpoint, err := getEndpointTx(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		revision, _ := parseDecimalID(endpoint.Revision)
		if err := r.deleteEndpointInTransaction(ctx, tx, userID, id, revision, now); err != nil {
			return err
		}
	}
	sources, err := resourceIDsTx(ctx, tx, "SELECT id FROM discovery_sources WHERE connection_id=? AND user_id=? ORDER BY id", connectionID, userID)
	if err != nil {
		return err
	}
	for _, id := range sources {
		if err := r.deleteSourceInTransaction(ctx, tx, userID, id, now); err != nil {
			return err
		}
	}
	for _, id := range credentials {
		credential, err := getCredentialTx(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if credential.ConnectionID != nil && *credential.ConnectionID != strconv.FormatInt(connectionID, 10) {
			continue
		}
		var referenced bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_key_grant_revisions g WHERE g.credential_id=? AND
 (EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.current_grant_revision_id=g.id) OR EXISTS(SELECT 1 FROM donation_use_revisions ur JOIN donation_uses u ON ur.use_id=u.id WHERE ur.grant_revision_id=g.id AND ur.id IN(u.current_approved_revision_id,u.current_pending_revision_id))))
 OR EXISTS(SELECT 1 FROM discovery_sources s JOIN discovery_source_revisions sr ON sr.id=s.current_revision_id WHERE sr.credential_id=?)`, id, id).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			if _, err := tx.ExecContext(ctx, "UPDATE credentials SET connection_id=NULL,revision=revision+1,updated_at=max(updated_at,?) WHERE id=? AND connection_id=?", now, id, connectionID); err != nil {
				return err
			}
		} else {
			revision, _ := parseDecimalID(credential.Revision)
			if err := r.deleteCredentialInTransaction(ctx, tx, userID, id, revision, now); err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM service_connections WHERE id=? AND user_id=? AND revision=?", connectionID, userID, expectedRevision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	return nil
}
