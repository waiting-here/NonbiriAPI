package db

// RerankPathSQL adds same-origin routing configuration without changing grants.
const RerankPathSQL = `ALTER TABLE endpoints ADD COLUMN rerank_path TEXT NOT NULL DEFAULT '' CHECK(length(CAST(rerank_path AS BLOB))<=2048);
ALTER TABLE donation_use_revisions ADD COLUMN rerank_path TEXT NOT NULL DEFAULT '' CHECK(length(CAST(rerank_path AS BLOB))<=2048);
ALTER TABLE donation_uses ADD COLUMN rerank_path_override TEXT CHECK(rerank_path_override IS NULL OR (native_service_bit=16384 AND length(CAST(rerank_path_override AS BLOB))<=2048));
ALTER TABLE dispatch_claim_targets ADD COLUMN rerank_path TEXT NOT NULL DEFAULT '' CHECK(length(CAST(rerank_path AS BLOB))<=2048);
DROP VIEW endpoint_grant_authorizations;
CREATE VIEW endpoint_grant_authorizations AS
 SELECT g.owner_user_id,g.endpoint_id,g.endpoint_key_id,g.credential_id,g.credential_version_id,
 v.version AS credential_version,g.id AS grant_revision_id,g.grant_revision,v.secret_ref_id,
 e.connector_type AS configured_type,e.base_url AS canonical_base_url,e.rerank_path,
 e.native_service_mask AS endpoint_native_service_mask,g.native_service_mask AS granted_native_service_mask,
 (e.native_service_mask&g.native_service_mask&CASE WHEN e.connector_type IN ('openai-compatible','openai-chat-completions','openai-responses','openai-decisions') AND e.rerank_format<>g.rerank_format THEN 507903 ELSE 524287 END) AS effective_native_service_mask,g.rerank_format,e.rerank_format AS endpoint_rerank_format,
 CASE WHEN k.id IS NOT NULL AND k.endpoint_id=g.endpoint_id AND e.id=g.endpoint_id AND e.user_id=g.owner_user_id AND c.id=g.credential_id AND c.user_id=g.owner_user_id AND v.id=g.credential_version_id AND v.credential_id=g.credential_id THEN 1 ELSE 0 END AS relationship_valid,
 CASE WHEN k.id IS NOT NULL AND k.endpoint_id=g.endpoint_id AND e.id=g.endpoint_id AND e.user_id=g.owner_user_id AND c.id=g.credential_id AND c.user_id=g.owner_user_id AND v.id=g.credential_version_id AND v.credential_id=g.credential_id AND e.enabled=1 AND k.enabled=1 AND c.enabled=1 AND c.deleted_at IS NULL
   AND v.enabled=1 AND s.id=v.secret_ref_id AND s.orphaned_at IS NULL AND (e.native_service_mask&g.native_service_mask&CASE WHEN e.connector_type IN ('openai-compatible','openai-chat-completions','openai-responses','openai-decisions') AND e.rerank_format<>g.rerank_format THEN 507903 ELSE 524287 END)<>0 THEN 1 ELSE 0 END AS available
 FROM endpoint_key_grant_revisions g
 LEFT JOIN endpoint_keys k ON k.id=g.endpoint_key_id
 LEFT JOIN endpoints e ON e.id=g.endpoint_id
 LEFT JOIN credentials c ON c.id=g.credential_id
 LEFT JOIN credential_versions v ON v.id=g.credential_version_id
 LEFT JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id;
DROP TRIGGER donation_use_pointer_revision_guard;
CREATE TRIGGER donation_use_pointer_revision_guard BEFORE UPDATE ON donation_uses
WHEN (NEW.current_approved_revision_id IS NOT OLD.current_approved_revision_id OR NEW.current_pending_revision_id IS NOT OLD.current_pending_revision_id OR NEW.enabled<>OLD.enabled OR NEW.discovery_enabled<>OLD.discovery_enabled OR NEW.rerank_path_override IS NOT OLD.rerank_path_override) AND (OLD.revision=9223372036854775807 OR NEW.revision<>OLD.revision+1)
BEGIN SELECT RAISE(ABORT,'donation use changes require a revision'); END;
DROP TRIGGER donation_use_revision_identity_guard;
CREATE TRIGGER donation_use_revision_identity_guard BEFORE UPDATE ON donation_use_revisions
WHEN NEW.rerank_path IS NOT OLD.rerank_path OR NEW.id IS NOT OLD.id OR NEW.use_id IS NOT OLD.use_id OR NEW.use_revision IS NOT OLD.use_revision OR NEW.source_grant_revision_id IS NOT OLD.source_grant_revision_id OR NEW.source_discovery_revision_id IS NOT OLD.source_discovery_revision_id OR NEW.credential_id_snapshot IS NOT OLD.credential_id_snapshot OR NEW.credential_version IS NOT OLD.credential_version OR NEW.grant_revision IS NOT OLD.grant_revision OR NEW.display_head IS NOT OLD.display_head OR NEW.display_tail IS NOT OLD.display_tail OR NEW.key_body_review_hmac IS NOT OLD.key_body_review_hmac OR NEW.origin IS NOT OLD.origin OR NEW.created_at IS NOT OLD.created_at OR (NEW.grant_revision_id IS NOT OLD.grant_revision_id AND NOT (NEW.grant_revision_id IS NULL AND NOT EXISTS(SELECT 1 FROM endpoint_key_grant_revisions WHERE id=OLD.grant_revision_id))) OR (NEW.discovery_source_revision_id IS NOT OLD.discovery_source_revision_id AND NOT (NEW.discovery_source_revision_id IS NULL AND NOT EXISTS(SELECT 1 FROM discovery_source_revisions WHERE id=OLD.discovery_source_revision_id)))
BEGIN SELECT RAISE(ABORT,'use revision identity is immutable'); END;

DROP TRIGGER management_capture_donation_uses_update;
CREATE TRIGGER management_capture_donation_uses_update BEFORE UPDATE ON donation_uses
WHEN EXISTS(SELECT 1 FROM management_capture_context)
BEGIN
 INSERT INTO management_capture_totals(event_id,kind,updated) SELECT event_id,'donation_uses',1 FROM management_capture_context WHERE id=1 AND (OLD.current_approved_revision_id IS NOT NEW.current_approved_revision_id OR OLD.current_pending_revision_id IS NOT NEW.current_pending_revision_id OR OLD.discovery_enabled IS NOT NEW.discovery_enabled OR OLD.donation_key_id IS NOT NEW.donation_key_id OR OLD.enabled IS NOT NEW.enabled OR OLD.native_service_bit IS NOT NEW.native_service_bit OR OLD.rerank_path_override IS NOT NEW.rerank_path_override)
 ON CONFLICT(event_id,kind) DO UPDATE SET updated=updated+1;
 UPDATE management_capture_totals SET omitted=omitted+1 WHERE event_id=(SELECT event_id FROM management_capture_context) AND kind='donation_uses' AND (OLD.current_approved_revision_id IS NOT NEW.current_approved_revision_id OR OLD.current_pending_revision_id IS NOT NEW.current_pending_revision_id OR OLD.discovery_enabled IS NOT NEW.discovery_enabled OR OLD.donation_key_id IS NOT NEW.donation_key_id OR OLD.enabled IS NOT NEW.enabled OR OLD.native_service_bit IS NOT NEW.native_service_bit OR OLD.rerank_path_override IS NOT NEW.rerank_path_override) AND (SELECT COUNT(*) FROM management_capture_rows WHERE event_id=management_capture_totals.event_id)>=256 AND NOT EXISTS(SELECT 1 FROM management_capture_rows WHERE event_id=management_capture_totals.event_id AND kind='donation_uses' AND target_id=CAST(NEW.id AS TEXT));
 INSERT INTO management_capture_rows(event_id,kind,target_id,before_json,after_json,facts_json)
 SELECT event_id,'donation_uses',CAST(NEW.id AS TEXT),json_object('$name',substr(COALESCE(CAST('Donation use '||OLD.id AS TEXT),''),1,512),'donation_key_id',substr(CAST(OLD.donation_key_id AS TEXT),1,512),'native_service_bit',substr(CAST(OLD.native_service_bit AS TEXT),1,512),'enabled',substr(CAST(OLD.enabled AS TEXT),1,512),'discovery_enabled',substr(CAST(OLD.discovery_enabled AS TEXT),1,512),'current_approved_revision_id',substr(CAST(OLD.current_approved_revision_id AS TEXT),1,512),'current_pending_revision_id',substr(CAST(OLD.current_pending_revision_id AS TEXT),1,512)),json_object('$name',substr(COALESCE(CAST('Donation use '||NEW.id AS TEXT),''),1,512),'donation_key_id',substr(CAST(NEW.donation_key_id AS TEXT),1,512),'native_service_bit',substr(CAST(NEW.native_service_bit AS TEXT),1,512),'enabled',substr(CAST(NEW.enabled AS TEXT),1,512),'discovery_enabled',substr(CAST(NEW.discovery_enabled AS TEXT),1,512),'current_approved_revision_id',substr(CAST(NEW.current_approved_revision_id AS TEXT),1,512),'current_pending_revision_id',substr(CAST(NEW.current_pending_revision_id AS TEXT),1,512)),CASE WHEN OLD.rerank_path_override IS NOT NEW.rerank_path_override THEN json_object('rerank_path_override','updated') ELSE '{}' END FROM management_capture_context WHERE id=1
 AND ((SELECT COUNT(*) FROM management_capture_rows WHERE event_id=management_capture_context.event_id)<256 OR EXISTS(SELECT 1 FROM management_capture_rows WHERE event_id=management_capture_context.event_id AND kind='donation_uses' AND target_id=CAST(NEW.id AS TEXT)))
 ON CONFLICT(event_id,kind,target_id) DO UPDATE SET after_json=excluded.after_json,facts_json=json_patch(facts_json,excluded.facts_json);
END;
`
