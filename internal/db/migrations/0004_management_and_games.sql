CREATE TABLE admin_endpoint_tags (
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 4096),
 tag TEXT NOT NULL CHECK(tag IN ('abusive_third_party','community_charity')),
 PRIMARY KEY(base_url,tag)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_admin_endpoint_tags_tag ON admin_endpoint_tags(tag,base_url);

ALTER TABLE request_error_bodies ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '' CHECK(failure_reason IN ('','read_failure','storage_failure'));
CREATE INDEX idx_request_errors_missing ON request_error_bodies(id) WHERE save_state<>'saved';

DROP TRIGGER donation_usage_reservations_outcome_insert_guard;
CREATE TRIGGER donation_usage_reservations_outcome_insert_guard BEFORE INSERT ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER donation_usage_reservations_outcome_update_guard;
CREATE TRIGGER donation_usage_reservations_outcome_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_insert_guard;
CREATE TRIGGER dispatch_claims_outcome_insert_guard BEFORE INSERT ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown')))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
DROP TRIGGER dispatch_claims_outcome_update_guard;
CREATE TRIGGER dispatch_claims_outcome_update_guard BEFORE UPDATE ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown')))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;

ALTER TABLE dispatch_response_starts ADD COLUMN http_status INTEGER CHECK(http_status IS NULL OR (typeof(http_status)='integer' AND http_status=200));
