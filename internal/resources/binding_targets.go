package resources

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

type BindingTargetSelection struct {
	EndpointKeyID   int64
	UpstreamModelID string
	NativeServices  []contract.NativeService
}

func listBindingsTx(ctx context.Context, tx *sql.Tx, userID, modelID int64) ([]Binding, error) {
	rows, err := tx.QueryContext(ctx, `SELECT b.id,b.ord,t.id,t.revision,t.endpoint_key_id,t.upstream_model_id,t.native_service_mask,
 e.id,e.connector_type,e.base_url,e.note,k.note,k.display_head,k.display_tail,k.credential_id,k.credential_version,k.grant_revision
 FROM model_bindings b JOIN models m ON m.id=b.model_id JOIN model_binding_targets t ON t.binding_id=b.id
 JOIN endpoint_key_current_credentials k ON k.id=t.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
 WHERE m.user_id=? AND m.id=? AND e.user_id=? ORDER BY b.ord,b.id,t.ord,t.id`, userID, modelID, userID)
	if err != nil {
		return nil, err
	}
	items := []Binding{}
	keyIDs := []int64{}
	keySeen := map[int64]bool{}
	byID := map[int64]int{}
	for rows.Next() {
		var bindingID, targetID, revision, keyID, mask, endpointID, credentialID, version, grantRevision int64
		var ord int
		var target BindingTarget
		var endpointNote, keyNote, head, tail string
		if err := rows.Scan(&bindingID, &ord, &targetID, &revision, &keyID, &target.UpstreamModelID, &mask, &endpointID, &target.ConnectorType, &target.BaseURL, &endpointNote, &keyNote, &head, &tail, &credentialID, &version, &grantRevision); err != nil {
			rows.Close()
			return nil, err
		}
		services, err := contract.NativeServicesFromMask(mask)
		if err != nil || services.Empty() {
			rows.Close()
			return nil, ErrUnavailable
		}
		target.ID, target.Revision = strconv.FormatInt(targetID, 10), strconv.FormatInt(revision, 10)
		target.EndpointID, target.EndpointKeyID = strconv.FormatInt(endpointID, 10), strconv.FormatInt(keyID, 10)
		target.CredentialID, target.CredentialVersion, target.GrantRevision = strconv.FormatInt(credentialID, 10), strconv.FormatInt(version, 10), strconv.FormatInt(grantRevision, 10)
		target.ConfigurationType = string(contract.Type(target.ConnectorType).ConfigurationType())
		target.NativeServices = services.Services()
		target.ModelTypes, target.ServiceRoutes = []contract.Operation{}, []BindingServiceRoute{}
		index, exists := byID[bindingID]
		if !exists {
			index = len(items)
			byID[bindingID] = index
			items = append(items, Binding{ID: strconv.FormatInt(bindingID, 10), Ord: ord, EndpointKeyID: target.EndpointKeyID, EndpointBaseURL: target.BaseURL, ConnectorType: target.ConnectorType, EndpointNote: endpointNote, EndpointKeyDisplayHead: head, EndpointKeyDisplayTail: tail, EndpointKeyNote: keyNote, UpstreamModelID: target.UpstreamModelID, Targets: []BindingTarget{}})
		}
		items[index].Targets = append(items[index].Targets, target)
		if !keySeen[keyID] {
			keySeen[keyID] = true
			keyIDs = append(keyIDs, keyID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(keyIDs) == 0 {
		return items, nil
	}
	grants, err := connectionstore.LoadSelfGrantsTx(ctx, tx, userID, keyIDs)
	if err != nil {
		return nil, mapConnectionStoreError(err)
	}
	for i := range items {
		for j := range items[i].Targets {
			target := &items[i].Targets[j]
			keyID, _ := parseDecimalID(target.EndpointKeyID)
			grant, available := grants[keyID]
			selected, _ := contract.ParseNativeServices(target.NativeServices)
			if available {
				effective := selected.Intersect(grant.EffectiveServices)
				target.ModelTypes, target.ServiceRoutes = bindingServiceProjection(grant.ConfiguredType, effective, grant.RerankFormat)
				target.Capabilities.CanBind = !effective.Empty() && selected.IsSubsetOf(grant.EffectiveServices)
			}
			if !target.Capabilities.CanBind {
				reason := "resource_unavailable"
				target.Capabilities.UnavailableReason = &reason
			}
		}
	}
	return items, nil
}

func validTargetSelections(targets []BindingTargetSelection) bool {
	if len(targets) == 0 || len(targets) > maxBindingBatch {
		return false
	}
	seen := map[string]bool{}
	for _, target := range targets {
		services, err := contract.ParseNativeServices(target.NativeServices)
		if target.EndpointKeyID <= 0 || !validateCatalogText(target.UpstreamModelID, 1, 512) || err != nil || services.Empty() {
			return false
		}
		key := strconv.FormatInt(target.EndpointKeyID, 10) + "/" + target.UpstreamModelID
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func verifyBindingTargetTx(ctx context.Context, tx *sql.Tx, userID int64, target BindingTargetSelection) (contract.NativeServiceSet, error) {
	grant, err := connectionstore.LoadSelfGrantTx(ctx, tx, userID, target.EndpointKeyID)
	if err != nil {
		return contract.NativeServiceSet{}, mapConnectionStoreError(err)
	}
	services, err := contract.ParseNativeServices(target.NativeServices)
	if err != nil || services.Empty() || !services.IsSubsetOf(grant.EffectiveServices) || contract.ValidateNativeServices(grant.ConfiguredType, services, grant.RerankFormat) != nil {
		return contract.NativeServiceSet{}, ErrInvalidRequest
	}
	_, eligible, err := bindingSelectionConnectorTx(ctx, tx, userID, target.EndpointKeyID, target.UpstreamModelID)
	if err != nil {
		return contract.NativeServiceSet{}, err
	}
	if !eligible {
		return contract.NativeServiceSet{}, ErrNotFound
	}
	return services, nil
}

func normalizeBindingGroupsTx(ctx context.Context, tx *sql.Tx, userID int64, selections []BindingSelection) ([][]BindingTargetSelection, int, error) {
	groups := make([][]BindingTargetSelection, 0, len(selections))
	total := 0
	for _, selection := range selections {
		targets := selection.Targets
		if targets == nil {
			grant, err := connectionstore.LoadSelfGrantTx(ctx, tx, userID, selection.EndpointKeyID)
			if err != nil {
				return nil, 0, mapConnectionStoreError(err)
			}
			legacy, err := contract.LegacyNativeServices(grant.ConfiguredType)
			if err != nil {
				return nil, 0, ErrUnavailable
			}
			services := legacy.Intersect(grant.EffectiveServices)
			if services.Empty() {
				return nil, 0, ErrInvalidRequest
			}
			targets = []BindingTargetSelection{{EndpointKeyID: selection.EndpointKeyID, UpstreamModelID: selection.UpstreamModelID, NativeServices: services.Services()}}
		}
		groups = append(groups, targets)
		total += len(targets)
	}
	return groups, total, nil
}

func insertBindingTargetTx(ctx context.Context, tx *sql.Tx, userID, modelID, bindingID int64, target BindingTargetSelection, ord int, now int64) error {
	services, err := verifyBindingTargetTx(ctx, tx, userID, target)
	if err != nil {
		return err
	}
	if err := requireTargetModelRouteTx(ctx, tx, userID, modelID, target); err != nil {
		return err
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_binding_targets t JOIN model_bindings b ON b.id=t.binding_id WHERE b.model_id=? AND t.endpoint_key_id=? AND t.upstream_model_id=? AND b.id<>?)`, modelID, target.EndpointKeyID, target.UpstreamModelID, bindingID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO model_binding_targets(binding_id,endpoint_key_id,upstream_model_id,native_service_mask,selection_origin,ord,created_at,updated_at) VALUES(?,?,?,?,'user',?,?,?)`, bindingID, target.EndpointKeyID, target.UpstreamModelID, services.Mask(), ord, now, now)
	return conflictOrError("create binding target", err)
}

func requireTargetModelRouteTx(ctx context.Context, tx *sql.Tx, userID, modelID int64, target BindingTargetSelection) error {
	var kind contract.Type
	var format contract.RerankFormat
	var operations modeltype.Set
	err := tx.QueryRowContext(ctx, `SELECT e.connector_type,g.rerank_format,m.model_types FROM models m
JOIN endpoint_keys k ON k.id=? JOIN endpoints e ON e.id=k.endpoint_id AND e.user_id=m.user_id
JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id
WHERE m.id=? AND m.user_id=?`, target.EndpointKeyID, modelID, userID).Scan(&kind, &format, &operations)
	if err != nil {
		return err
	}
	services, err := contract.ParseNativeServices(target.NativeServices)
	if err != nil {
		return ErrInvalidRequest
	}
	for _, operation := range operations {
		if routes, err := connector.ResolveServiceRoutes(kind, services, format, operation); err == nil && len(routes) > 0 {
			return nil
		}
	}
	return ErrInvalidRequest
}
