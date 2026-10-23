package db

// DonationDiscoveryAuthorizationSQL changes catalog access without granting inference.
const DonationDiscoveryAuthorizationSQL = `DROP TRIGGER discovery_run_version_guard;
CREATE TRIGGER discovery_run_version_guard BEFORE INSERT ON discovery_runs
WHEN NEW.authorization_kind<>'legacy' AND NOT EXISTS(
 SELECT 1 FROM discovery_source_authorizations a
 WHERE a.source_revision_id=NEW.source_revision_id AND a.source_revision=NEW.source_revision_snapshot
 AND a.credential_version_id=NEW.credential_version_id AND a.available=1 AND a.relationship_valid=1
 AND ((NEW.authorization_kind='self' AND EXISTS(
  SELECT 1 FROM discovery_sources ds WHERE ds.id=a.source_id AND ds.current_revision_id=a.source_revision_id))
 OR (NEW.authorization_kind='donation_use' AND EXISTS(
  SELECT 1 FROM donation_uses u JOIN donation_use_revisions ur ON ur.use_id=u.id
   AND (ur.id=u.current_pending_revision_id OR ur.id=u.current_approved_revision_id)
  JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
  JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id
  JOIN endpoint_grant_authorizations g ON g.grant_revision_id=ur.grant_revision_id
  WHERE u.id=NEW.authorization_use_id AND ur.id=NEW.authorization_use_revision_id
  AND u.enabled=1 AND ur.review_state IN ('pending','approved') AND dk.enabled=1 AND dk.failure_disabled=0
  AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>NEW.started_at)
  AND d.status IN ('pending','approved') AND d.terminal_at IS NULL
  AND g.relationship_valid=1 AND g.available=1 AND g.owner_user_id=d.user_id AND g.endpoint_key_id=dk.endpoint_key_id
  AND g.owner_user_id=a.owner_user_id AND g.credential_id=a.credential_id AND g.credential_version_id=a.credential_version_id
  AND (g.effective_native_service_mask&u.native_service_bit)=u.native_service_bit))))
BEGIN SELECT RAISE(ABORT,'discovery run authorization mismatch'); END;
DROP VIEW charity_model_catalog;
CREATE VIEW charity_model_catalog AS
 WITH automatic_entries AS (
  SELECT DISTINCT u.donation_key_id,c.id,c.normalized_model_id,r.result_generation
  FROM donation_uses u
  JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id AND ur.review_state='approved'
  JOIN donation_keys dk ON dk.id=u.donation_key_id
  JOIN (SELECT g.id,g.owner_user_id,g.credential_id,g.credential_version_id,e.connector_type AS configured_type,e.base_url AS canonical_base_url
 FROM endpoint_key_grant_revisions g JOIN endpoints e ON e.id=g.endpoint_id) a ON a.id=ur.grant_revision_id
  JOIN discovery_source_states st ON st.source_revision_id=COALESCE(ur.discovery_source_revision_id,
 (SELECT sr.id FROM endpoint_key_discovery_sources ks JOIN discovery_source_revisions sr ON sr.source_id=ks.source_id
 WHERE ks.endpoint_key_id=dk.endpoint_key_id AND sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id
 ORDER BY sr.source_revision DESC LIMIT 1),
 (SELECT sr.id FROM discovery_source_revisions sr JOIN discovery_sources ds ON ds.id=sr.source_id
 WHERE sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id
 AND sr.canonical_base_url=a.canonical_base_url AND sr.list_format=CASE a.configured_type
 WHEN 'openai-compatible' THEN 'openai' WHEN 'openai-responses' THEN 'openai' WHEN 'openai-chat-completions' THEN 'openai' WHEN 'openai-decisions' THEN 'openai' WHEN 'anthropic-compatible' THEN 'anthropic' WHEN 'google-gemini' THEN 'gemini'
 WHEN 'ai-sdk-gateway-v3' THEN 'ai-sdk-gateway-v3' WHEN 'ai-sdk-gateway-v4' THEN 'ai-sdk-gateway-v4' END
 ORDER BY sr.id LIMIT 1),0)
  JOIN discovery_runs r ON r.id=st.last_successful_run_id AND r.source_revision_id=st.source_revision_id AND r.credential_version_id=a.credential_version_id AND r.state='succeeded'
  JOIN model_catalog_entries c ON c.discovery_run_id=r.id AND c.source_type='automatic'
 ), supports AS (
  SELECT donation_key_id,normalized_model_id,count(*) AS automatic_supports,0 AS manual_supports,max(result_generation) AS automatic_revision
  FROM automatic_entries GROUP BY donation_key_id,normalized_model_id
  UNION ALL
  SELECT donation_key_id,normalized_model_id,0,1,0 FROM donation_key_manual_models
 )
 SELECT donation_key_id,normalized_model_id,sum(automatic_supports) AS automatic_supports,sum(manual_supports) AS manual_supports,max(automatic_revision) AS automatic_revision
 FROM supports GROUP BY donation_key_id,normalized_model_id;
`
