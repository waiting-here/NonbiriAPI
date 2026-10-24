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
	Use      connectionstore.DiscoveryUseAuthorization
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
	choices, err := readDiscoveryChoicesModeTx(ctx, tx, []int64{keyID}, now, true)
	if err != nil {
		return ApprovedDiscoveryUse{}, err
	}
	return chooseDiscoveryUse(choices[keyID], resources.ManagedDiscoverySelection{}, true)
}

// Selection and dispatch use the same bounded catalog, grant, and health reads.
// No query is issued per member or per use.
func readDiscoveryChoicesTx(ctx context.Context, tx *sql.Tx, keys []int64, now int64) (map[int64][]discoveryChoice, error) {
	return readDiscoveryChoicesModeTx(ctx, tx, keys, now, false)
}

func readDiscoveryChoicesModeTx(ctx context.Context, tx *sql.Tx, keys []int64, now int64, approvedOnly bool) (map[int64][]discoveryChoice, error) {
	out := make(map[int64][]discoveryChoice)
	if len(keys) == 0 {
		return out, nil
	}
	if len(keys) > 512 {
		return nil, ErrInvalidRequest
	}
	approved, err := connectionstore.LoadDiscoveryUsesTx(ctx, tx, keys, approvedOnly)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]connectionstore.DiscoveryUseAuthorization)
	for _, uses := range approved {
		for _, use := range uses {
			byID[use.UseID] = use
		}
	}
	sourceIDs := make([]any, 0, len(byID))
	useIDs := make([]any, 0, len(byID))
	for _, use := range byID {
		useIDs = append(useIDs, use.UseID)
		if use.DiscoverySourceRevisionID > 0 {
			sourceIDs = append(sourceIDs, use.DiscoverySourceRevisionID)
		}
	}
	if len(useIDs) == 0 {
		return out, nil
	}
	health := make(map[int64]bool)
	args := append([]any{now, now}, useIDs...)
	rows, err := tx.QueryContext(ctx, `SELECT h.use_id,h.credential_version_id,
 h.failure_disabled=0 AND COALESCE(h.retry_after_until,0)<=? AND COALESCE((`+smartbackoff.ServiceUntilSQL("h")+`),0)<=?
 FROM donation_service_health h WHERE h.use_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(useIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var useID, versionID int64
		var healthy bool
		if err := rows.Scan(&useID, &versionID, &healthy); err != nil {
			rows.Close()
			return nil, err
		}
		if use, ok := byID[useID]; ok && use.Grant.CredentialVersionID == versionID {
			health[useID] = healthy
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	type catalog struct {
		id, owner, credential, versionID, version int64
		available                                 bool
	}
	sources := make(map[int64]catalog)
	if len(sourceIDs) > 0 {
		rows, err = tx.QueryContext(ctx, `SELECT source_revision_id,source_id,owner_user_id,credential_id,credential_version_id,credential_version,relationship_valid,available
 FROM discovery_source_authorizations WHERE source_revision_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(sourceIDs)), ",")+`)`, sourceIDs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var revision int64
			var source catalog
			var relation int
			if err := rows.Scan(&revision, &source.id, &source.owner, &source.credential, &source.versionID, &source.version, &relation, &source.available); err != nil {
				return nil, err
			}
			if relation != 1 {
				return nil, ErrInvariant
			}
			sources[revision] = source
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for key, uses := range approved {
		for _, use := range uses {
			healthy, exists := health[use.UseID]
			if !exists {
				healthy = true
			}
			if use.DiscoverySourceRevisionID == 0 {
				if connectionstore.DefaultDiscoveryFormat(use.Grant.ConfiguredType) != "" {
					out[key] = append(out[key], discoveryChoice{ApprovedDiscoveryUse: ApprovedDiscoveryUse{Use: use}, healthy: healthy})
				}
				continue
			}
			source, ok := sources[use.DiscoverySourceRevisionID]
			if !ok || source.owner != use.Grant.OwnerUserID || source.credential != use.Grant.CredentialID || source.versionID != use.Grant.CredentialVersionID || source.version != use.Grant.CredentialVersion {
				return nil, ErrInvariant
			}
			if source.available {
				out[key] = append(out[key], discoveryChoice{ApprovedDiscoveryUse: ApprovedDiscoveryUse{Use: use, SourceID: source.id}, healthy: healthy})
			}
		}
	}
	return out, nil
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
