package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// DiscoveryUseAuthorization is catalog access, never inference authorization.
type DiscoveryUseAuthorization struct {
	DonationKeyID, UseID, UseRevision, RevisionID int64                  `json:"-"`
	NativeService                                 contract.NativeService `json:"-"`
	DiscoverySourceRevisionID                     int64                  `json:"-"`
	Grant                                         GrantAuthorization     `json:"-"`
}

// DiscoverySourceRevisionSQL selects the pinned catalog, the key's configured
// catalog for this exact version, or this key's existing standard catalog.
// A different key or credential version is never substituted.
const DiscoverySourceRevisionSQL = `COALESCE(ur.discovery_source_revision_id,
 (SELECT sr.id FROM endpoint_key_discovery_sources ks JOIN discovery_source_revisions sr ON sr.source_id=ks.source_id
 WHERE ks.endpoint_key_id=dk.endpoint_key_id AND sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id
 ORDER BY sr.source_revision DESC LIMIT 1),
 (SELECT sr.id FROM discovery_source_revisions sr JOIN discovery_sources ds ON ds.id=sr.source_id
 WHERE sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id
 AND EXISTS(SELECT 1 FROM discovery_source_keys scope WHERE scope.source_id=ds.id AND scope.endpoint_key_id=dk.endpoint_key_id)
 AND sr.canonical_base_url=a.canonical_base_url AND sr.list_format=CASE a.configured_type
 WHEN 'openai-compatible' THEN 'openai' WHEN 'openai-responses' THEN 'openai' WHEN 'openai-chat-completions' THEN 'openai' WHEN 'openai-decisions' THEN 'openai' WHEN 'anthropic-compatible' THEN 'anthropic' WHEN 'google-gemini' THEN 'gemini'
 WHEN 'ai-sdk-gateway-v3' THEN 'ai-sdk-gateway-v3' WHEN 'ai-sdk-gateway-v4' THEN 'ai-sdk-gateway-v4' END
 ORDER BY sr.id LIMIT 1),0)`

// LoadDiscoveryUsesTx prefers a pending revision for review. approvedOnly is
// reserved for workflows that will bind models to an approved inference use.
func LoadDiscoveryUsesTx(ctx context.Context, tx *sql.Tx, ids []int64, approvedOnly bool) (map[int64][]DiscoveryUseAuthorization, error) {
	if tx == nil {
		return nil, ErrInvariant
	}
	placeholders, args, err := boundedIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]DiscoveryUseAuthorization)
	if len(args) == 0 {
		return out, nil
	}
	query := discoveryUsesQuery(placeholders, approvedOnly)
	queryArgs := append(append([]any{}, args...), args...)
	rows, err := tx.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var use DiscoveryUseAuthorization
		var row grantRow
		var bit int64
		dest := []any{&use.DonationKeyID, &use.UseID, &use.UseRevision, &use.RevisionID, &bit, &use.DiscoverySourceRevisionID}
		if err := rows.Scan(append(dest, row.destinations()...)...); err != nil {
			return nil, err
		}
		services, err := contract.NativeServicesFromMask(bit)
		if err != nil || len(services.Services()) != 1 {
			return nil, ErrInvariant
		}
		use.NativeService = services.Services()[0]
		use.Grant, err = row.authorization()
		if errors.Is(err, ErrUnavailable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !use.Grant.EffectiveServices.Has(use.NativeService) {
			continue
		}
		out[use.DonationKeyID] = append(out[use.DonationKeyID], use)
	}
	return out, rows.Err()
}

func discoveryUsesQuery(placeholders string, approvedOnly bool) string {
	pointer := "COALESCE(u.current_pending_revision_id,u.current_approved_revision_id)"
	if approvedOnly {
		pointer = "u.current_approved_revision_id"
	}
	return `SELECT dk.id,u.id,u.revision,ur.id,u.native_service_bit,` + DiscoverySourceRevisionSQL + "," + grantReadColumns + `
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
 JOIN donation_use_revisions ur ON ur.id=` + pointer + ` AND ur.use_id=u.id
 JOIN (SELECT * FROM endpoint_grant_authorizations WHERE grant_revision_id IN (
  SELECT r.grant_revision_id FROM donation_uses selected JOIN donation_use_revisions r
  ON r.id=` + strings.ReplaceAll(pointer, "u.", "selected.") + ` AND r.use_id=selected.id
  WHERE selected.donation_key_id IN (` + placeholders + `)
 )) a ON a.grant_revision_id=ur.grant_revision_id
 WHERE dk.id IN (` + placeholders + `) AND u.enabled=1 AND ur.review_state IN ('pending','approved')
 AND dk.ended_at IS NULL AND d.status IN ('pending','approved') AND d.terminal_at IS NULL
 AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id)
 AND a.owner_user_id=d.user_id AND a.endpoint_key_id=dk.endpoint_key_id
 ORDER BY dk.id,u.native_service_bit`
}

func LoadDiscoveryUseTx(ctx context.Context, tx *sql.Tx, useID, revisionID int64) (DiscoveryUseAuthorization, error) {
	var keyID int64
	var approved sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT donation_key_id,current_approved_revision_id FROM donation_uses WHERE id=?", useID).Scan(&keyID, &approved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DiscoveryUseAuthorization{}, ErrNotFound
		}
		return DiscoveryUseAuthorization{}, err
	}
	uses, err := LoadDiscoveryUsesTx(ctx, tx, []int64{keyID}, revisionID > 0 && approved.Valid && approved.Int64 == revisionID)
	if err != nil {
		return DiscoveryUseAuthorization{}, err
	}
	for _, use := range uses[keyID] {
		if use.UseID == useID && (revisionID == 0 || use.RevisionID == revisionID) {
			return use, nil
		}
	}
	return DiscoveryUseAuthorization{}, ErrUnavailable
}

func DefaultDiscoveryFormat(kind contract.Type) string {
	switch kind.ConfigurationType() {
	case contract.TypeOpenAICompatible:
		return "openai"
	case contract.TypeAnthropicCompatible:
		return "anthropic"
	case contract.TypeGoogleGemini:
		return "gemini"
	case contract.TypeAISDKGatewayV3:
		return "ai-sdk-gateway-v3"
	case contract.TypeAISDKGatewayV4:
		return "ai-sdk-gateway-v4"
	default:
		return ""
	}
}

// EnsureDiscoverySourceTx creates only a standard catalog for the donated
// version when no configured catalog exists. It does not change self grants.
func EnsureDiscoverySourceTx(ctx context.Context, tx *sql.Tx, use DiscoveryUseAuthorization, now int64) (int64, error) {
	if use.DiscoverySourceRevisionID > 0 {
		return use.DiscoverySourceRevisionID, nil
	}
	g := use.Grant
	format := DefaultDiscoveryFormat(g.ConfiguredType)
	if format == "" {
		return 0, ErrUnavailable
	}
	var revisionID int64
	if err := tx.QueryRowContext(ctx, "SELECT max(COALESCE((SELECT seq FROM sqlite_sequence WHERE name='discovery_source_revisions'),0),COALESCE((SELECT max(id) FROM discovery_source_revisions),0))").Scan(&revisionID); err != nil {
		return 0, err
	}
	if revisionID < 0 || revisionID == math.MaxInt64 {
		return 0, ErrUnavailable
	}
	revisionID++
	result, err := tx.ExecContext(ctx, `INSERT INTO discovery_sources(user_id,connection_id,name,enabled,revision,current_revision_id,created_at,updated_at)
 SELECT ?,connection_id,'',1,1,?,?,? FROM endpoints WHERE id=? AND user_id=?`, g.OwnerUserID, revisionID, now, now, g.EndpointID, g.OwnerUserID)
	if err != nil {
		return 0, err
	}
	if err := requireOne(result); err != nil {
		return 0, err
	}
	sourceID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO discovery_source_keys(source_id,endpoint_key_id) VALUES(?,?)`, sourceID, g.EndpointKeyID); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(?,?,?,1,?,?,?,?,?)`, revisionID, sourceID, g.OwnerUserID, g.CredentialID, g.CredentialVersionID, g.CanonicalBaseURL, format, now)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(?,1,?)`, revisionID, now)
	return revisionID, err
}

// ConfiguredDiscoverySourceRevisionTx captures a configured catalog only when it
// belongs to the exact submitted credential version.
func ConfiguredDiscoverySourceRevisionTx(ctx context.Context, tx *sql.Tx, grant GrantAuthorization) (*int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT ks.source_revision_id FROM endpoint_key_discovery_sources ks
 JOIN discovery_source_authorizations a ON a.source_revision_id=ks.source_revision_id
 WHERE ks.endpoint_key_id=? AND ks.credential_version_id=? AND a.owner_user_id=? AND a.credential_id=? AND a.relationship_valid=1`,
		grant.EndpointKeyID, grant.CredentialVersionID, grant.OwnerUserID, grant.CredentialID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// ClaimDiscoverySourceKeyTx makes a catalog's endpoint/key scope permanent,
// including catalogs selected directly when submitting a donation use.
func ClaimDiscoverySourceKeyTx(ctx context.Context, tx *sql.Tx, ownerID, keyID, sourceID int64) error {
	var matches bool
	err := tx.QueryRowContext(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM discovery_source_keys scope WHERE scope.source_id=ds.id AND scope.endpoint_key_id<>k.id)
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_discovery_sources other WHERE other.source_id=ds.id AND other.endpoint_key_id<>k.id)
 AND NOT EXISTS(SELECT 1 FROM donation_use_revisions ur JOIN discovery_source_revisions sr ON sr.id=ur.discovery_source_revision_id
 JOIN donation_uses u ON u.id=ur.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id
 WHERE sr.source_id=ds.id AND dk.endpoint_key_id IS NOT NULL AND dk.endpoint_key_id<>k.id)
 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
 JOIN discovery_sources ds ON ds.id=? AND ds.user_id=e.user_id AND ds.connection_id IS e.connection_id
 WHERE k.id=? AND e.user_id=?`, sourceID, keyID, ownerID).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovery_source_keys(source_id,endpoint_key_id) VALUES(?,?) ON CONFLICT(source_id) DO NOTHING`, sourceID, keyID)
	return err
}
