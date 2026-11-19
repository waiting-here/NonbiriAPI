package db

// Pending header waits are indexed independently so completing one response
// can narrow its scope without clearing a concurrent service-wide wait.
const GoogleModelCooldownSQL = `
ALTER TABLE dispatch_retry_after_observations ADD COLUMN pending_health_id INTEGER REFERENCES donation_service_health(id) ON DELETE SET NULL;
CREATE INDEX idx_retry_pending_health ON dispatch_retry_after_observations(pending_health_id,rate_limited_until) WHERE pending_health_id IS NOT NULL;
CREATE TABLE donation_service_model_cooldowns (
 health_id INTEGER NOT NULL REFERENCES donation_service_health(id) ON DELETE CASCADE,
 upstream_model_id TEXT NOT NULL CHECK(length(upstream_model_id) BETWEEN 1 AND 512 AND instr(upstream_model_id,char(0))=0),
 reason TEXT NOT NULL CHECK(reason IN ('google_daily_requests','google_minute_requests','google_minute_input_tokens','google_daily_input_tokens','google_model_quota')),
 retry_at INTEGER NOT NULL CHECK(retry_at BETWEEN 1 AND 253402300799),
 PRIMARY KEY(health_id,upstream_model_id)
) STRICT, WITHOUT ROWID;
` + cleanupGoogleModelAfterDelete + cleanupGoogleModelAfterUpdate

const cleanupGoogleModelScope = `
BEGIN
 DELETE FROM donation_service_model_cooldowns WHERE upstream_model_id=OLD.upstream_model_id AND health_id IN (
 SELECT h.id FROM donation_uses u JOIN donation_service_health h ON h.use_id=u.id
 WHERE u.donation_key_id=OLD.donation_key_id AND (OLD.native_service_mask & h.native_service_bit)<>0
 AND NOT EXISTS(SELECT 1 FROM charity_binding_targets t WHERE t.donation_key_id=u.donation_key_id
 AND t.upstream_model_id=OLD.upstream_model_id AND (t.native_service_mask & h.native_service_bit)<>0));
END;
`

const cleanupGoogleModelAfterDelete = `CREATE TRIGGER google_model_target_delete AFTER DELETE ON charity_binding_targets` + cleanupGoogleModelScope
const cleanupGoogleModelAfterUpdate = `CREATE TRIGGER google_model_target_update AFTER UPDATE OF native_service_mask,upstream_model_id,donation_key_id ON charity_binding_targets
WHEN (OLD.native_service_mask & NEW.native_service_mask)<>OLD.native_service_mask OR OLD.upstream_model_id<>NEW.upstream_model_id OR OLD.donation_key_id<>NEW.donation_key_id` + cleanupGoogleModelScope
