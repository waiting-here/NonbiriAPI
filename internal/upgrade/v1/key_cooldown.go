package v1

const keyCooldownSchema = `CREATE TABLE donation_key_cooldowns (
 donation_key_id INTEGER PRIMARY KEY REFERENCES donation_keys(id) ON DELETE CASCADE,
 rate_limited_until INTEGER NOT NULL CHECK(rate_limited_until BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE dispatch_retry_after_observations (
 claim_id TEXT PRIMARY KEY REFERENCES dispatch_claims(id) ON DELETE CASCADE,
 http_status INTEGER NOT NULL CHECK(http_status BETWEEN 100 AND 599),
 observed_at INTEGER NOT NULL CHECK(observed_at BETWEEN 0 AND 253402300799),
 rate_limited_until INTEGER NOT NULL CHECK(rate_limited_until BETWEEN 0 AND 253402300799)
) STRICT;
DROP TRIGGER donation_usage_reservations_outcome_insert_guard;
CREATE TRIGGER donation_usage_reservations_outcome_insert_guard BEFORE INSERT ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status<>200))) AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER donation_usage_reservations_outcome_update_guard;
CREATE TRIGGER donation_usage_reservations_outcome_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND (EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status<>200) OR (OLD.streak_disposition='neutral' AND OLD.failure_origin=NEW.failure_origin)))) AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_insert_guard;
CREATE TRIGGER dispatch_claims_outcome_insert_guard BEFORE INSERT ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.id AND r.http_status<>200)))))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_update_guard;
CREATE TRIGGER dispatch_claims_outcome_update_guard BEFORE UPDATE ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND (EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.id AND r.http_status<>200) OR (OLD.streak_disposition='neutral' AND OLD.failure_origin=NEW.failure_origin))))))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
`
