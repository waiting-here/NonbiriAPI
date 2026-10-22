package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

// RefreshDonationDiscoveryAndWait uses an owner's explicitly shared, approved
// discovery authorization with the same caller context as other automation.
func (r *Repository) RefreshDonationDiscoveryAndWait(ctx context.Context, userID, donationID, donationKeyID int64, selection ManagedDiscoverySelection) (int64, error) {
	if r == nil || userID <= 0 || donationID <= 0 || donationKeyID <= 0 || selection.UseID <= 0 || selection.ExpectedRevision <= 0 {
		return 0, ErrInvalidRequest
	}
	identity := context.WithoutCancel(ctx)
	var pinned connectionstore.SourceAuthorization
	var endpointID, keyID int64
	load := func(ctx context.Context, tx *sql.Tx, requireRevision bool) (connectionstore.SourceAuthorization, error) {
		if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
			return connectionstore.SourceAuthorization{}, err
		}
		var sourceID, revision, currentEndpoint, currentKey int64
		err := tx.QueryRowContext(ctx, `SELECT sr.source_id,u.revision,dk.endpoint_id,dk.endpoint_key_id
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
 JOIN donation_key_memberships dm ON dm.donation_key_id=dk.id AND dm.donation_id=d.id AND dm.endpoint_key_id=dk.endpoint_key_id
 JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id
 JOIN discovery_source_revisions sr ON sr.id=ur.discovery_source_revision_id
 WHERE u.id=? AND dk.id=? AND d.id=? AND d.user_id=? AND d.status='approved' AND d.terminal_at IS NULL AND dk.ended_at IS NULL`, selection.UseID, donationKeyID, donationID, userID).Scan(&sourceID, &revision, &currentEndpoint, &currentKey)
		if errors.Is(err, sql.ErrNoRows) {
			return connectionstore.SourceAuthorization{}, ErrNotFound
		}
		if err != nil {
			return connectionstore.SourceAuthorization{}, err
		}
		if requireRevision && revision != selection.ExpectedRevision {
			return connectionstore.SourceAuthorization{}, ErrConflict
		}
		source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: userID, SourceID: sourceID, Purpose: connectionstore.SourceCharity, DonationUseID: selection.UseID})
		if err != nil {
			return source, mapConnectionStoreError(err)
		}
		if !requireRevision && (currentEndpoint != endpointID || currentKey != keyID || !sameSourceAuthorization(source, pinned)) {
			return source, ErrConflict
		}
		if requireRevision {
			endpointID, keyID = currentEndpoint, currentKey
		}
		return source, nil
	}
	preflight := func(ctx context.Context, tx *sql.Tx) error {
		var err error
		pinned, err = load(ctx, tx, true)
		return err
	}
	authorize := func(work context.Context, tx *sql.Tx) error {
		_, err := load(discoveryIdentityContext{Context: work, identity: identity}, tx, false)
		return err
	}
	resolve := func(context.Context, *sql.Tx) (discoverySelection, error) {
		return discoverySelection{source: pinned, ref: connectionstore.SourceAuthorizationRef{OwnerUserID: userID, SourceID: pinned.SourceID, Purpose: connectionstore.SourceCharity, DonationUseID: selection.UseID}, endpointID: endpointID, keyID: keyID, authorize: authorize}, nil
	}
	project := func(_ context.Context, _ *sql.Tx, _ discoverySelection, operationID string, evidence DiscoveryEvidence) (DiscoveryAccepted, ResourceOperationResult, error) {
		return DiscoveryAccepted{OperationID: operationID, Evidence: evidence}, ResourceOperationResult{}, nil
	}
	key, err := r.operationID()
	if err != nil {
		return 0, ErrUnavailable
	}
	canonical, err := json.Marshal(struct {
		UseID            string `json:"use_id"`
		ExpectedRevision string `json:"expected_revision"`
	}{strconv.FormatInt(selection.UseID, 10), strconv.FormatInt(selection.ExpectedRevision, 10)})
	if err != nil {
		return 0, err
	}
	mutation := ControlMutation{IdempotencyKey: key, Method: http.MethodPost, Route: "/api/automation/donations/{id}/keys/{keyId}/models/refresh", PathIDs: []string{strconv.FormatInt(donationID, 10), strconv.FormatInt(donationKeyID, 10)}, CanonicalBody: canonical}
	out, err := beginSourceDiscovery(ctx, r, mutation, discoveryStart[DiscoveryAccepted]{actorID: userID, role: "user", wait: true, preflight: preflight, resolve: resolve, project: project})
	if errors.Is(err, errDiscoveryWorkerUnavailable) {
		return 0, ErrUnavailable
	}
	if err != nil {
		return 0, err
	}
	if out.Value.Evidence.State != "succeeded" {
		return 0, ErrUnavailable
	}
	return parseDecimalID(out.Value.Evidence.Revision)
}

func sameSourceAuthorization(a, b connectionstore.SourceAuthorization) bool {
	// Renaming or adding a pending use does not change a pinned approved source.
	a.ResourceRevision, b.ResourceRevision = 0, 0
	return a == b
}
