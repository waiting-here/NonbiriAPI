package resources

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func (r *Repository) startDiscovery(ctx context.Context, actorID, endpointID, keyID int64, mutation ControlMutation, wait bool, managed *managedDiscoveryRequest) (MutationResult[DiscoveryAccepted], error) {
	role, stage := "user", "catalog_refresh"
	var target ManagedDiscoveryTarget
	var authorize func(context.Context, *sql.Tx) error
	identity := context.WithoutCancel(ctx)
	preflight := func(ctx context.Context, tx *sql.Tx) error {
		if managed != nil {
			var err error
			target, err = r.managedDiscovery.AuthorizeManagedDiscovery(ctx, tx, managed.role, actorID, managed.donationID, managed.keyID, managed.selection, true)
			if err != nil {
				return err
			}
			authorize = func(work context.Context, tx *sql.Tx) error {
				current, err := r.managedDiscovery.AuthorizeManagedDiscovery(discoveryIdentityContext{Context: work, identity: identity}, tx, managed.role, actorID, managed.donationID, managed.keyID, ManagedDiscoverySelection{UseID: target.UseID}, true)
				if err != nil {
					return err
				}
				current.UseRevision = target.UseRevision
				if current != target {
					return ErrNotFound
				}
				return nil
			}
			return nil
		}
		if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, actorID); err != nil {
			return err
		}
		snapshot, hasSnapshot := ctx.Value(automationDiscoverySnapshotKey{}).(AutomationDiscoverySnapshot)
		if hasSnapshot {
			if err := r.VerifyAutomationDiscoverySnapshotInTransaction(ctx, tx, actorID, snapshot); err != nil {
				return err
			}
		}
		authorize = func(work context.Context, tx *sql.Tx) error {
			bound := discoveryIdentityContext{Context: work, identity: identity}
			if err := r.finalAuth.AuthorizeUserMutation(bound, tx, actorID); err != nil {
				return err
			}
			if hasSnapshot {
				return r.VerifyAutomationDiscoverySnapshotInTransaction(bound, tx, actorID, snapshot)
			}
			return nil
		}
		return nil
	}
	if managed != nil {
		role, stage = managed.role, ""
	}
	resolve := func(ctx context.Context, tx *sql.Tx) (discoverySelection, error) {
		ref := connectionstore.SourceAuthorizationRef{OwnerUserID: actorID, Purpose: connectionstore.SourceSelf}
		selectedEndpoint, selectedKey := endpointID, keyID
		if managed != nil {
			if target.SourceRevisionID == 0 {
				use, err := connectionstore.LoadDiscoveryUseTx(ctx, tx, target.UseID, target.ApprovedRevisionID)
				if err != nil {
					return discoverySelection{}, mapConnectionStoreError(err)
				}
				now, err := r.nowUnix()
				if err != nil {
					return discoverySelection{}, err
				}
				if _, err = connectionstore.EnsureDiscoverySourceTx(ctx, tx, use, now); err != nil {
					return discoverySelection{}, mapConnectionStoreError(err)
				}
				target, err = r.managedDiscovery.AuthorizeManagedDiscovery(ctx, tx, managed.role, actorID, managed.donationID, managed.keyID, managed.selection, true)
				if err != nil {
					return discoverySelection{}, err
				}
			}
			ref.OwnerUserID, ref.SourceID, ref.Purpose, ref.DonationUseID = target.OwnerUserID, target.SourceID, connectionstore.SourceCharity, target.UseID
			ref.UseRevisionID = target.ApprovedRevisionID
			selectedEndpoint, selectedKey = target.EndpointID, target.EndpointKeyID
		} else {
			if _, err := discoveryOwnerTx(ctx, tx, actorID, endpointID, keyID); err != nil {
				return discoverySelection{}, err
			}
			err := tx.QueryRowContext(ctx, `SELECT source_id FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?`, keyID).Scan(&ref.SourceID)
			if errors.Is(err, sql.ErrNoRows) {
				return discoverySelection{}, ErrInvalidRequest
			}
			if err != nil {
				return discoverySelection{}, err
			}
		}
		locked, err := endpointKeyLockedTx(ctx, tx, selectedKey)
		if err != nil {
			return discoverySelection{}, err
		}
		if locked {
			return discoverySelection{}, ErrResourceLocked
		}
		source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, ref)
		if err != nil {
			return discoverySelection{}, mapConnectionStoreError(err)
		}
		return discoverySelection{source: source, ref: ref, endpointID: selectedEndpoint, keyID: selectedKey, authorize: authorize}, nil
	}
	project := func(_ context.Context, _ *sql.Tx, selected discoverySelection, operationID string, evidence DiscoveryEvidence) (DiscoveryAccepted, ResourceOperationResult, error) {
		return DiscoveryAccepted{OperationID: operationID, Evidence: evidence}, ResourceOperationResult{EndpointID: strconv.FormatInt(selected.endpointID, 10), EndpointKeyID: strconv.FormatInt(selected.keyID, 10), OperationID: operationID}, nil
	}
	return beginSourceDiscovery(ctx, r, mutation, discoveryStart[DiscoveryAccepted]{actorID: actorID, role: role, stage: stage, wait: wait, preflight: preflight, resolve: resolve, project: project})
}

func (r *Repository) RefreshDiscoverySource(ctx context.Context, userID, sourceID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[DiscoverySourceAccepted], error) {
	if r == nil || userID <= 0 || sourceID <= 0 || expectedRevision <= 0 || !validResourceMutation(mutation, "POST", routeSourceRefresh, sourceID) {
		return MutationResult[DiscoverySourceAccepted]{}, ErrInvalidRequest
	}
	identity := context.WithoutCancel(ctx)
	authorize := func(work context.Context, tx *sql.Tx) error {
		return r.finalAuth.AuthorizeUserMutation(discoveryIdentityContext{Context: work, identity: identity}, tx, userID)
	}
	resolve := func(ctx context.Context, tx *sql.Tx) (discoverySelection, error) {
		ref := connectionstore.SourceAuthorizationRef{OwnerUserID: userID, SourceID: sourceID, Purpose: connectionstore.SourceSelf}
		source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, ref)
		if err != nil {
			return discoverySelection{}, mapConnectionStoreError(err)
		}
		if source.ResourceRevision != expectedRevision {
			return discoverySelection{}, ErrConflict
		}
		locked, err := credentialLockedTx(ctx, tx, userID, source.CredentialID)
		if err != nil {
			return discoverySelection{}, err
		}
		if locked {
			return discoverySelection{}, ErrResourceLocked
		}
		return discoverySelection{source: source, ref: ref, authorize: authorize}, nil
	}
	project := func(ctx context.Context, tx *sql.Tx, selected discoverySelection, operationID string, _ DiscoveryEvidence) (DiscoverySourceAccepted, ResourceOperationResult, error) {
		source, err := getDiscoverySourceTx(ctx, tx, userID, sourceID)
		return DiscoverySourceAccepted{OperationID: operationID, DiscoverySource: source}, ResourceOperationResult{DiscoverySourceID: source.ID, DiscoverySourceRevision: source.DiscoverySourceRevision, OperationID: operationID}, err
	}
	return beginSourceDiscovery(ctx, r, mutation, discoveryStart[DiscoverySourceAccepted]{actorID: userID, role: "user", stage: "source_refresh", preflight: authorize, resolve: resolve, project: project})
}
