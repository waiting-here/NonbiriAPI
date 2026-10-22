package v1

import (
	"context"
	"database/sql"
)

// legacyNativeMaskSQL is deliberately narrower than current configurable
// service support. These are the six protocols accepted by published schema 26.
func legacyNativeMaskSQL(expression string) string {
	return "CASE " + expression + " WHEN 'openai-compatible' THEN 49 WHEN 'openai-responses' THEN 2 WHEN 'openai-decisions' THEN 8192 WHEN 'anthropic-compatible' THEN 65536 WHEN 'google-gemini' THEN 131072 WHEN 'ai-sdk-gateway-v3' THEN 262144 ELSE 0 END"
}

func populateConnectionRelationships(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`INSERT INTO service_connections(id,user_id,name,note,revision,created_at,updated_at)
SELECT id,user_id,'Connection '||id,'',1,created_at,updated_at FROM endpoints ORDER BY id`,
		`INSERT INTO credentials(id,user_id,connection_id,note,enabled,revision,current_version_id,created_at,updated_at)
SELECT k.id,e.user_id,e.id,'',1,1,k.id,k.created_at,k.updated_at
FROM temp.connection_old_keys k JOIN endpoints e ON e.id=k.endpoint_id ORDER BY k.id`,
		`INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,display_head,display_tail,enabled,revision,created_at)
SELECT id,id,1,secret_ref_id,display_head,display_tail,1,1,created_at FROM temp.connection_old_keys ORDER BY id`,
		`INSERT INTO endpoint_key_grant_revisions(id,endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
SELECT k.id,k.id,k.endpoint_id,e.user_id,k.id,k.id,1,e.native_service_mask,'',k.secret_fingerprint,'legacy',k.created_at
FROM temp.connection_old_keys k JOIN endpoints e ON e.id=k.endpoint_id ORDER BY k.id`,
		`INSERT INTO discovery_sources(id,user_id,connection_id,name,enabled,revision,current_revision_id,created_at,updated_at)
SELECT k.id,e.user_id,e.id,'',1,1,k.id,k.created_at,k.updated_at
FROM temp.connection_old_keys k JOIN endpoints e ON e.id=k.endpoint_id ORDER BY k.id`,
		`INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,credential_id,credential_version_id,source_revision,canonical_base_url,list_format,created_at)
SELECT k.id,k.id,e.user_id,k.id,k.id,1,e.base_url,
CASE e.connector_type WHEN 'anthropic-compatible' THEN 'anthropic' WHEN 'google-gemini' THEN 'gemini' WHEN 'ai-sdk-gateway-v3' THEN 'ai-sdk-gateway-v3' ELSE 'openai' END,k.created_at
FROM temp.connection_old_keys k JOIN endpoints e ON e.id=k.endpoint_id ORDER BY k.id`,
		`INSERT INTO endpoint_key_discovery_sources(endpoint_key_id,source_id,grant_revision_id,source_revision_id,credential_version_id)
SELECT id,id,id,id,id FROM temp.connection_old_keys ORDER BY id`,
		`INSERT INTO mainstream_channel_entries(channel_id,ord,name,connector_type,canonical_base_url,native_service_mask,rerank_format,enabled)
SELECT id,0,'',connector_type,canonical_base_url,` + legacyNativeMaskSQL("connector_type") + `,'',1 FROM temp.connection_old_channels ORDER BY id`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func populateConnectionUses(ctx context.Context, tx *sql.Tx) error {
	// An ended member without its original key or retained fingerprint stays
	// represented by the existing donation tombstone. No substitute secret or
	// historical authorization is inferred from a current endpoint.
	if _, err := tx.ExecContext(ctx, `WITH services(bit) AS (VALUES(1),(2),(16),(32),(8192),(65536),(131072),(262144))
INSERT INTO donation_uses(donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at)
SELECT dk.id,services.bit,CASE WHEN dk.ended_at IS NULL AND d.terminal_at IS NULL AND d.status IN ('pending','approved') THEN 1 ELSE 0 END,
 CASE WHEN dk.ended_at IS NULL AND d.terminal_at IS NULL AND d.status IN ('pending','approved') THEN 1 ELSE 0 END,1,dk.created_at,dk.updated_at
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
JOIN temp.connection_old_keys k ON k.id=dk.endpoint_key_id AND k.secret_fingerprint=dk.report_fingerprint
JOIN endpoints e ON e.id=k.endpoint_id AND e.user_id=d.user_id
JOIN endpoint_key_grant_revisions g ON g.id=k.id
CROSS JOIN services
WHERE (g.native_service_mask&services.bit)=services.bit
 AND ((`+legacyNativeMaskSQL("dk.connector_type")+`)&services.bit)=services.bit
ORDER BY dk.id,services.bit`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_use_revisions(id,use_id,use_revision,grant_revision_id,discovery_source_revision_id,source_grant_revision_id,source_discovery_revision_id,credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,origin,review_state,review_reason,reviewer_user_id,reviewer_role,review_requirement_revision,created_at,reviewed_at)
SELECT u.id,u.id,1,k.id,k.id,k.id,k.id,k.id,1,1,dk.display_head,dk.display_tail,dk.report_fingerprint,dk.key_body_review_hmac,'legacy',
 CASE d.status WHEN 'approved' THEN 'approved' WHEN 'pending' THEN CASE WHEN dk.ended_at IS NULL AND d.terminal_at IS NULL THEN 'pending' ELSE 'cancelled' END WHEN 'rejected' THEN 'rejected' ELSE 'cancelled' END,
 d.review_note,d.reviewed_by_user_id,NULLIF(d.reviewed_by_role,''),dk.review_revision,dk.created_at,
 CASE WHEN d.status='pending' AND dk.ended_at IS NULL AND d.terminal_at IS NULL THEN NULL ELSE COALESCE(d.reviewed_at,d.terminal_at,dk.ended_at,dk.updated_at) END
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
JOIN temp.connection_old_keys k ON k.id=dk.endpoint_key_id
ORDER BY u.id`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE donation_uses AS u SET
current_approved_revision_id=CASE WHEN EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.id=u.id AND ur.review_state='approved') THEN u.id END,
current_pending_revision_id=CASE WHEN EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.id=u.id AND ur.review_state='pending') THEN u.id END
WHERE EXISTS(SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id AND m.donation_id=d.id
 WHERE dk.id=u.donation_key_id AND dk.ended_at IS NULL AND d.terminal_at IS NULL AND d.status IN ('pending','approved'))`)
	return err
}

func populateConnectionTargets(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`INSERT INTO model_binding_targets(id,binding_id,endpoint_key_id,upstream_model_id,native_service_mask,selection_origin,selection_revision,ord,revision,created_at,updated_at)
SELECT b.id,b.id,b.endpoint_key_id,b.upstream_model_id,e.native_service_mask,'legacy',NULL,0,1,b.created_at,b.updated_at
FROM temp.connection_old_bindings b JOIN endpoint_keys k ON k.id=b.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id ORDER BY b.id`,
		`INSERT INTO charity_binding_targets(id,binding_id,donation_key_id,upstream_model_id,native_service_mask,selection_origin,selection_revision,ord,revision,created_at,updated_at)
SELECT b.id,b.id,b.donation_key_id,b.upstream_model_id,` + legacyNativeMaskSQL("dk.connector_type") + `,'legacy',NULL,0,1,b.created_at,b.updated_at
FROM temp.connection_old_charity_bindings b JOIN donation_keys dk ON dk.id=b.donation_key_id ORDER BY b.id`,
		// A detached old claim retains its original secret reference. It has no
		// version sidecar unless its exact original key/secret relation survives.
		`INSERT INTO dispatch_claim_targets(claim_id,authorization_kind,owner_user_id,endpoint_key_id_snapshot,endpoint_id_snapshot,credential_id_snapshot,credential_version_id,credential_version,configured_type,canonical_base_url)
SELECT c.id,'legacy',e.user_id,k.id,e.id,k.id,k.id,1,s.connector_type,s.canonical_base_url
FROM dispatch_claims c JOIN temp.connection_old_keys k ON k.id=c.endpoint_key_id AND k.secret_ref_id=c.secret_ref_id
JOIN endpoints e ON e.id=k.endpoint_id JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
WHERE c.state IN ('claimed','dispatched') ORDER BY c.id`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
