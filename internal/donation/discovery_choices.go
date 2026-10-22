package donation

import (
	"context"
	"database/sql"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

type ApprovedDiscoveryUse struct {
	Use      connectionstore.UseAuthorization
	SourceID int64
}

type discoveryChoice struct {
	ApprovedDiscoveryUse
	healthy bool
}

// SelectApprovedDiscoveryUseTx resolves an unqualified discovery request by its
// exact source revision and credential version. Caller permissions and the
// donation's shared admission gates remain the responsibility of its caller.
func SelectApprovedDiscoveryUseTx(ctx context.Context, tx *sql.Tx, keyID, now int64) (ApprovedDiscoveryUse, error) {
	choices, err := readDiscoveryChoicesTx(ctx, tx, []int64{keyID}, now)
	if err != nil {
		return ApprovedDiscoveryUse{}, err
	}
	return chooseDiscoveryUse(choices[keyID], resources.ManagedDiscoverySelection{}, true)
}

// Both the bounded selection page and dispatch authorization use these exact
// approved sources. Each batch uses two reads, never one read per member/use.
func readDiscoveryChoicesTx(ctx context.Context, tx *sql.Tx, keys []int64, now int64) (map[int64][]discoveryChoice, error) {
	out := make(map[int64][]discoveryChoice)
	if len(keys) == 0 {
		return out, nil
	}
	if len(keys) > 512 {
		return nil, ErrInvalidRequest
	}
	approved, err := connectionstore.LoadApprovedUsesTx(ctx, tx, keys)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]connectionstore.UseAuthorization)
	for _, uses := range approved {
		for _, use := range uses {
			byID[use.UseID] = use
		}
	}
	args := []any{now, now}
	for _, key := range keys {
		args = append(args, key)
	}
	rows, err := tx.QueryContext(ctx, `SELECT du.donation_key_id,du.id,ur.id,sa.source_id,sa.source_revision_id,
sa.owner_user_id,sa.credential_id,sa.credential_version_id,sa.credential_version,sa.relationship_valid,sa.available,
COALESCE(h.failure_disabled,0)=0 AND COALESCE(h.retry_after_until,0)<=? AND COALESCE((`+smartbackoff.ServiceUntilSQL("h")+`),0)<=?
FROM charity_use_availability av JOIN donation_uses du ON du.id=av.use_id
JOIN donation_use_revisions ur ON ur.id=du.current_approved_revision_id AND ur.use_id=du.id
JOIN discovery_source_authorizations sa ON sa.source_revision_id=ur.discovery_source_revision_id
LEFT JOIN donation_service_health h ON h.use_id=du.id AND h.endpoint_key_id=(SELECT endpoint_key_id FROM donation_keys WHERE id=du.donation_key_id)
AND h.native_service_bit=av.native_service_bit AND h.credential_version_id=av.credential_version_id
WHERE du.discovery_enabled=1 AND du.donation_key_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+`)
ORDER BY du.donation_key_id,du.native_service_bit`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, useID, approvedID, sourceID, sourceRevisionID, owner, credential, versionID, version int64
		var relation, available int
		var healthy bool
		if err := rows.Scan(&key, &useID, &approvedID, &sourceID, &sourceRevisionID, &owner, &credential, &versionID, &version, &relation, &available, &healthy); err != nil {
			return nil, err
		}
		use, ok := byID[useID]
		if !ok || relation != 1 || available < 0 || available > 1 || sourceID <= 0 || !use.DiscoveryEnabled ||
			use.ApprovedRevisionID != approvedID || use.DiscoverySourceRevisionID != sourceRevisionID ||
			use.Grant.OwnerUserID != owner || use.Grant.CredentialID != credential || use.Grant.CredentialVersionID != versionID || use.Grant.CredentialVersion != version {
			return nil, ErrInvariant
		}
		if available == 1 {
			out[key] = append(out[key], discoveryChoice{ApprovedDiscoveryUse: ApprovedDiscoveryUse{Use: use, SourceID: sourceID}, healthy: healthy})
		}
	}
	return out, rows.Err()
}

func chooseDiscoveryUse(choices []discoveryChoice, selection resources.ManagedDiscoverySelection, requireHealthy bool) (ApprovedDiscoveryUse, error) {
	var selected ApprovedDiscoveryUse
	for _, choice := range choices {
		use := choice.Use
		if selection.UseID > 0 && use.UseID != selection.UseID {
			continue
		}
		if selection.ExpectedRevision > 0 && use.UseRevision != selection.ExpectedRevision {
			return ApprovedDiscoveryUse{}, resources.ErrConflict
		}
		if requireHealthy && !choice.healthy {
			continue
		}
		if selected.Use.UseID > 0 {
			if selected.Use.DiscoverySourceRevisionID != use.DiscoverySourceRevisionID || selected.Use.Grant.CredentialVersionID != use.Grant.CredentialVersionID {
				return ApprovedDiscoveryUse{}, resources.ErrConflict
			}
			continue
		}
		selected = choice.ApprovedDiscoveryUse
	}
	if selected.Use.UseID == 0 {
		return selected, resources.ErrResourceLocked
	}
	return selected, nil
}
