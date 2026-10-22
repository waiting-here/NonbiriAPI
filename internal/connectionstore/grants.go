package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const grantReadColumns = "COALESCE(a.owner_user_id,0),COALESCE(a.endpoint_id,0),COALESCE(a.endpoint_key_id,0),COALESCE(a.credential_id,0),COALESCE(a.credential_version_id,0),COALESCE(a.credential_version,0),COALESCE(a.grant_revision_id,0),COALESCE(a.grant_revision,0),COALESCE(a.secret_ref_id,0),COALESCE(a.configured_type,''),COALESCE(a.canonical_base_url,''),COALESCE(a.endpoint_native_service_mask,-1),COALESCE(a.granted_native_service_mask,-1),COALESCE(a.effective_native_service_mask,-1),COALESCE(a.rerank_format,''),COALESCE(a.endpoint_rerank_format,''),COALESCE(a.relationship_valid,0),COALESCE(a.available,0)"

type grantRow struct {
	grant                                    GrantAuthorization
	endpointMask, grantedMask, effectiveMask int64
	endpointFormat                           connectorcontract.RerankFormat
	relationshipValid, available             int
}

func (r *grantRow) destinations() []any {
	return []any{&r.grant.OwnerUserID, &r.grant.EndpointID, &r.grant.EndpointKeyID, &r.grant.CredentialID, &r.grant.CredentialVersionID, &r.grant.CredentialVersion, &r.grant.GrantRevisionID, &r.grant.GrantRevision, &r.grant.SecretRefID, &r.grant.ConfiguredType, &r.grant.CanonicalBaseURL, &r.endpointMask, &r.grantedMask, &r.effectiveMask, &r.grant.RerankFormat, &r.endpointFormat, &r.relationshipValid, &r.available}
}

func (r *grantRow) facts() (GrantAuthorization, error) {
	g := r.grant
	if r.relationshipValid != 1 || !validBool(r.available) || g.OwnerUserID <= 0 || g.EndpointID <= 0 || g.EndpointKeyID <= 0 || g.CredentialID <= 0 || g.CredentialVersionID <= 0 || g.CredentialVersion <= 0 || g.GrantRevisionID <= 0 || g.GrantRevision <= 0 || g.CanonicalBaseURL == "" {
		return GrantAuthorization{}, ErrInvariant
	}
	var err error
	g.EndpointServices, err = connectorcontract.NativeServicesFromMask(r.endpointMask)
	if err != nil {
		return GrantAuthorization{}, ErrInvariant
	}
	g.GrantedServices, err = connectorcontract.NativeServicesFromMask(r.grantedMask)
	if err != nil {
		return GrantAuthorization{}, ErrInvariant
	}
	g.EffectiveServices, err = connectorcontract.NativeServicesFromMask(r.effectiveMask)
	if err != nil || connectorcontract.ValidateNativeServices(g.ConfiguredType, g.EndpointServices, r.endpointFormat) != nil || connectorcontract.ValidateNativeServices(g.ConfiguredType, g.GrantedServices, g.RerankFormat) != nil {
		return GrantAuthorization{}, ErrInvariant
	}
	expected := g.EndpointServices.Intersect(g.GrantedServices)
	if g.ConfiguredType.OpenAIServiceFamily() && r.endpointFormat != g.RerankFormat {
		rerank, _ := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{connectorcontract.NativeServiceRerank})
		expected, err = connectorcontract.NativeServicesFromMask(expected.Mask() &^ rerank.Mask())
		if err != nil {
			return GrantAuthorization{}, ErrInvariant
		}
	}
	if expected.Mask() != g.EffectiveServices.Mask() {
		return GrantAuthorization{}, ErrInvariant
	}
	return g, nil
}

func (r *grantRow) authorization() (GrantAuthorization, error) {
	g, err := r.facts()
	if err != nil {
		return GrantAuthorization{}, err
	}
	if r.available == 0 || g.EffectiveServices.Empty() {
		return GrantAuthorization{}, ErrUnavailable
	}
	if g.SecretRefID <= 0 {
		return GrantAuthorization{}, ErrInvariant
	}
	return g, nil
}

func validBool(value int) bool { return value == 0 || value == 1 }

// LoadSelfGrantTx resolves the current key grant, not credential.current.
func LoadSelfGrantTx(ctx context.Context, tx *sql.Tx, ownerUserID, endpointKeyID int64) (GrantAuthorization, error) {
	grants, err := loadSelfGrantsTx(ctx, tx, ownerUserID, []int64{endpointKeyID}, true)
	if err != nil {
		return GrantAuthorization{}, err
	}
	grant, ok := grants[endpointKeyID]
	if !ok {
		return GrantAuthorization{}, ErrNotFound
	}
	return grant, nil
}

// LoadSelfGrantsTx omits absent or unavailable keys. Broken relationships fail.
func LoadSelfGrantsTx(ctx context.Context, tx *sql.Tx, ownerUserID int64, endpointKeyIDs []int64) (map[int64]GrantAuthorization, error) {
	return loadSelfGrantsTx(ctx, tx, ownerUserID, endpointKeyIDs, false)
}

func loadSelfGrantsTx(ctx context.Context, tx *sql.Tx, ownerUserID int64, ids []int64, strict bool) (map[int64]GrantAuthorization, error) {
	if tx == nil || ownerUserID <= 0 {
		return nil, ErrInvariant
	}
	placeholders, args, err := boundedIDs(ids)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]GrantAuthorization)
	if len(args) == 0 {
		return result, nil
	}
	args = append([]any{ownerUserID}, args...)
	rows, err := tx.QueryContext(ctx, "SELECT k.id,"+grantReadColumns+" FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=k.current_grant_revision_id WHERE e.user_id=? AND k.id IN ("+placeholders+") ORDER BY k.id", args...)
	if err != nil {
		return nil, fmt.Errorf("connectionstore: read self grants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var keyID int64
		var row grantRow
		if err := rows.Scan(append([]any{&keyID}, row.destinations()...)...); err != nil {
			return nil, err
		}
		grant, err := row.authorization()
		if errors.Is(err, ErrUnavailable) && !strict {
			continue
		}
		if err != nil {
			return nil, err
		}
		if grant.EndpointKeyID != keyID || grant.OwnerUserID != ownerUserID {
			return nil, ErrInvariant
		}
		result[keyID] = grant
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func loadGrantTx(ctx context.Context, tx *sql.Tx, grantID int64) (GrantAuthorization, error) {
	var row grantRow
	err := tx.QueryRowContext(ctx, "SELECT "+grantReadColumns+" FROM endpoint_grant_authorizations a WHERE a.grant_revision_id=?", grantID).Scan(row.destinations()...)
	if errors.Is(err, sql.ErrNoRows) {
		return GrantAuthorization{}, ErrInvariant
	}
	if err != nil {
		return GrantAuthorization{}, fmt.Errorf("connectionstore: read grant: %w", err)
	}
	return row.authorization()
}

func boundedIDs(ids []int64) (string, []any, error) {
	if len(ids) > 512 {
		return "", nil, ErrInvariant
	}
	seen := make(map[int64]struct{}, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return "", nil, ErrInvariant
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	args := make([]any, len(unique))
	for i, id := range unique {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), args, nil
}
