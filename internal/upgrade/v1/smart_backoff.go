package v1

const smartBackoffSchema = `CREATE TABLE donation_key_backoffs (
 donation_key_id INTEGER PRIMARY KEY REFERENCES donation_keys(id) ON DELETE CASCADE,
 profile TEXT NOT NULL CHECK(profile IN ('none','five_hour_week','five_hour_week_month','day','day_month')),
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json) AND json_type(rules_json)='array' AND json_array_length(rules_json)<=8 AND length(CAST(rules_json AS BLOB))<=16384),
 generation INTEGER NOT NULL CHECK(generation>=1),
 episode INTEGER NOT NULL CHECK(episode>=0),
 period TEXT CHECK(period IN ('unknown','five_hour','day','week','month')),
 source TEXT CHECK(source IN ('builtin','custom_rule')),
 stage INTEGER NOT NULL CHECK(stage BETWEEN 0 AND 2),
 retry_at INTEGER CHECK(retry_at BETWEEN 0 AND 253402300799),
 trial_claim_id TEXT REFERENCES dispatch_claims(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((retry_at IS NULL AND period IS NULL AND source IS NULL AND trial_claim_id IS NULL) OR (retry_at IS NOT NULL AND period IS NOT NULL AND source IS NOT NULL))
) STRICT;
CREATE TABLE dispatch_smart_backoff_facts (
 claim_id TEXT PRIMARY KEY REFERENCES dispatch_claims(id) ON DELETE CASCADE,
 donation_key_id INTEGER REFERENCES donation_keys(id) ON DELETE SET NULL,
 generation INTEGER NOT NULL CHECK(generation>=1),
 episode INTEGER NOT NULL CHECK(episode>=0),
 profile TEXT NOT NULL CHECK(profile IN ('five_hour_week','five_hour_week_month','day','day_month')),
 is_trial INTEGER NOT NULL CHECK(is_trial IN (0,1)),
 accepted_at INTEGER CHECK(accepted_at BETWEEN 0 AND 253402300799),
 period TEXT CHECK(period IN ('unknown','five_hour','day','week','month')),
 source TEXT CHECK(source IN ('builtin','custom_rule')),
 reset_at INTEGER CHECK(reset_at BETWEEN 0 AND 253402300799),
 CHECK((period IS NULL AND source IS NULL AND reset_at IS NULL) OR (period IS NOT NULL AND source IS NOT NULL AND reset_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX idx_donation_backoffs_trial ON donation_key_backoffs(trial_claim_id) WHERE trial_claim_id IS NOT NULL;
CREATE INDEX idx_dispatch_backoff_key ON dispatch_smart_backoff_facts(donation_key_id);
DROP TRIGGER donation_usage_reservations_outcome_insert_guard;
CREATE TRIGGER donation_usage_reservations_outcome_insert_guard BEFORE INSERT ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND (EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status<>200) OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.claim_id AND sb.period IS NOT NULL AND sb.accepted_at IS NULL)))) AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER donation_usage_reservations_outcome_update_guard;
CREATE TRIGGER donation_usage_reservations_outcome_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND ((EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status<>200) OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.claim_id AND sb.period IS NOT NULL AND sb.accepted_at IS NULL)) OR (OLD.streak_disposition='neutral' AND OLD.failure_origin=NEW.failure_origin)))) AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_insert_guard;
CREATE TRIGGER dispatch_claims_outcome_insert_guard BEFORE INSERT ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND (EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.id AND r.http_status<>200) OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.id AND sb.period IS NOT NULL AND sb.accepted_at IS NULL))))))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_update_guard;
CREATE TRIGGER dispatch_claims_outcome_update_guard BEFORE UPDATE ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND (NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND ((EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.id AND r.http_status<>200) OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.id AND sb.period IS NOT NULL AND sb.accepted_at IS NULL)) OR (OLD.streak_disposition='neutral' AND OLD.failure_origin=NEW.failure_origin))))))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
`
