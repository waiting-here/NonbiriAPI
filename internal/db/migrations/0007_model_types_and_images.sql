-- Existing models retain both previously available operations. New models default to chat.
ALTER TABLE models ADD COLUMN model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7);
UPDATE models SET model_types=3;
ALTER TABLE charity_models ADD COLUMN model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7);
UPDATE charity_models SET model_types=3;

DROP TRIGGER dispatch_claim_attempt_limit_guard;
DROP TRIGGER dispatch_claim_attempt_limit_update_guard;
DROP TRIGGER generation_two_integer_type_logical_requests_insert_guard;
DROP TRIGGER generation_two_integer_type_logical_requests_update_guard;
DROP TRIGGER generation_two_integer_type_request_logs_insert_guard;
DROP TRIGGER generation_two_integer_type_request_logs_update_guard;
DROP TRIGGER legal_hold_consumed_guard_log;
DROP TRIGGER legal_hold_delete_log;
DROP TRIGGER legal_hold_delete_request_attempt;
DROP TRIGGER legal_hold_insert_guard;
DROP TRIGGER legal_hold_mark_log;
DROP TRIGGER legal_hold_marker_insert_log;
DROP TRIGGER logical_request_acceptance_identity_update_guard;
DROP TRIGGER logical_request_destination_insert_guard;
DROP TRIGGER logical_request_destination_update_guard;
DROP TRIGGER logical_request_log_snapshot_sync;
DROP TRIGGER logical_request_terminal_state_guard;
DROP TRIGGER logical_requests_rejection_insert;
DROP TRIGGER logical_requests_rejection_update;
DROP TRIGGER rejected_request_no_dispatch;
DROP TRIGGER request_log_logical_snapshot_guard;
DROP TRIGGER request_log_logical_snapshot_update_guard;
DROP TRIGGER request_log_origin_capture;
DROP TRIGGER request_log_origin_immutable;
DROP TRIGGER request_logs_rejection_insert;
DROP TRIGGER request_logs_rejection_update;
DROP TRIGGER risk_scan_result_source_deleted;
DROP TRIGGER users_request_handoff_delete_guard;
CREATE TEMP TABLE image_migration_sequence AS SELECT name,seq FROM sqlite_sequence WHERE name IN ('logical_requests','request_logs');
CREATE TEMP TABLE image_migration_logical_requests AS SELECT * FROM logical_requests;
DROP TABLE logical_requests;
CREATE TABLE logical_requests (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='req_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 route_kind TEXT NOT NULL CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings','openai_images_generations','charity_images_generations')),
 model_snapshot TEXT NOT NULL DEFAULT '' CHECK(typeof(model_snapshot)='text' AND length(CAST(model_snapshot AS BLOB))<=512),
 state TEXT NOT NULL CHECK(state IN ('accepted','running','terminal')),
 attempt_limit INTEGER NOT NULL CHECK(attempt_limit BETWEEN 1 AND 100),
 caller_result_class TEXT CHECK(caller_result_class IS NULL OR caller_result_class IN ('success','failed','cancelled')),
 caller_status INTEGER CHECK(caller_status IS NULL OR caller_status BETWEEN 100 AND 599),
 caller_error_code TEXT CHECK(caller_error_code IS NULL OR (typeof(caller_error_code)='text' AND length(CAST(caller_error_code AS BLOB)) BETWEEN 1 AND 64 AND caller_error_code NOT GLOB '*[^a-z0-9_]*')),
 accounting_state TEXT NOT NULL CHECK(accounting_state IN ('none','reserved','committed','released')),
 account_reserved_milli INTEGER NOT NULL DEFAULT 0 CHECK(account_reserved_milli BETWEEN 0 AND 9000000000000000),
 settlement_destination TEXT NOT NULL CHECK(settlement_destination IN ('user','external')),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799), rejection_stage TEXT CHECK(rejection_stage IS NULL OR rejection_stage IN ('authorization','flow','preflight')), rejection_reason TEXT CHECK(rejection_reason IS NULL OR rejection_reason IN ('unauthorized','forbidden','charity_suspended','feature_disabled','maintenance','invalid_request','not_found','unbound_model','insufficient_credits','user_rpm','global_rpm','shared_rpm','concurrency','content_too_short','payload_too_large','resource_limit_exceeded','service_unavailable')), request_method TEXT CHECK(request_method IS NULL OR request_method IN ('GET','POST')), request_path TEXT CHECK(request_path IS NULL OR request_path IN ('/v1/models','/v1/chat/completions','/v1/embeddings','/v1/images/generations')),
 CHECK((state<>'terminal' AND caller_result_class IS NULL AND caller_status IS NULL AND caller_error_code IS NULL AND terminal_at IS NULL) OR
       (state='terminal' AND caller_result_class IS NOT NULL AND terminal_at IS NOT NULL AND terminal_at>=created_at AND
        ((caller_result_class='success' AND caller_status BETWEEN 200 AND 399 AND caller_error_code IS NULL) OR
         (caller_result_class='failed' AND caller_status BETWEEN 400 AND 599 AND caller_error_code IN ('internal','invalid_request','unauthorized','forbidden','not_found','conflict','method_not_allowed','rate_limited','payload_too_large','elevated_required','unbound_model','upstream','maintenance','service_unavailable','resource_limit_exceeded','resource_locked','debug_dry_run_intercepted','debug_live_result_captured','debug_live_cancelled','insufficient_credits','feature_disabled','charity_suspended','content_too_short','already_checked_in','checkin_cap_reached')) OR
         (caller_result_class='cancelled' AND caller_status IS NULL AND caller_error_code IS NULL)))),
 CHECK((route_kind='model_discovery' AND attempt_limit=1) OR route_kind<>'model_discovery'),
 CHECK((accounting_state='reserved' AND account_reserved_milli>=0) OR (accounting_state<>'reserved' AND account_reserved_milli=0)),
 CHECK((settlement_destination='user') OR (settlement_destination='external' AND user_id IS NULL))
);
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at,terminal_at,rejection_stage,rejection_reason,request_method,request_path) SELECT id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at,terminal_at,rejection_stage,rejection_reason,request_method,request_path FROM image_migration_logical_requests;
DROP TABLE image_migration_logical_requests;
CREATE INDEX idx_logical_requests_state ON logical_requests(state,created_at,id);
CREATE INDEX idx_logical_requests_user ON logical_requests(user_id,created_at,id);
CREATE TEMP TABLE image_migration_request_logs AS SELECT * FROM request_logs;
DROP TABLE request_logs;
CREATE TABLE request_logs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 logical_request_id TEXT NOT NULL UNIQUE CHECK(length(logical_request_id)=26 AND substr(logical_request_id,1,4)='req_' AND substr(logical_request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(logical_request_id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 model TEXT NOT NULL DEFAULT '' CHECK(typeof(model)='text' AND length(CAST(model AS BLOB))<=512),
 endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
 upstream_model_id TEXT NOT NULL DEFAULT '' CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id)<=512),
 route_kind TEXT NOT NULL DEFAULT 'openai_chat_completions' CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings','openai_images_generations','charity_images_generations')),
 endpoint_base_url TEXT NOT NULL DEFAULT '' CHECK(typeof(endpoint_base_url)='text' AND length(CAST(endpoint_base_url AS BLOB))<=4096),
 caller_result_class TEXT CHECK(caller_result_class IS NULL OR caller_result_class IN ('success','failed','cancelled')),
 caller_status INTEGER CHECK(caller_status IS NULL OR caller_status BETWEEN 100 AND 599),
 caller_error_code TEXT CHECK(caller_error_code IS NULL OR (typeof(caller_error_code)='text' AND length(CAST(caller_error_code AS BLOB)) BETWEEN 1 AND 64 AND caller_error_code NOT GLOB '*[^a-z0-9_]*')),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 100),
 status_code INTEGER NOT NULL DEFAULT 0 CHECK(status_code=0 OR status_code BETWEEN 100 AND 599),
 duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms BETWEEN 0 AND 86400000),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 completed_at INTEGER CHECK(completed_at IS NULL OR (completed_at BETWEEN 0 AND 253402300799 AND completed_at>=started_at)),
 uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(uncached_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens BETWEEN 0 AND 9223372036854775807),
 output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens BETWEEN 0 AND 9223372036854775807),
 usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)),
 error_source TEXT NOT NULL DEFAULT 'platform' CHECK(error_source IN ('platform','upstream')),
 error_code TEXT NOT NULL DEFAULT '' CHECK(typeof(error_code)='text' AND length(CAST(error_code AS BLOB))<=64 AND error_code NOT GLOB '*[^a-z0-9_]*'),
 error_diag TEXT NOT NULL DEFAULT '' CHECK(typeof(error_diag)='text' AND length(CAST(error_diag AS BLOB))<=4096),
 legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), rejection_stage TEXT CHECK(rejection_stage IS NULL OR rejection_stage IN ('authorization','flow','preflight')), rejection_reason TEXT CHECK(rejection_reason IS NULL OR rejection_reason IN ('unauthorized','forbidden','charity_suspended','feature_disabled','maintenance','invalid_request','not_found','unbound_model','insufficient_credits','user_rpm','global_rpm','shared_rpm','concurrency','content_too_short','payload_too_large','resource_limit_exceeded','service_unavailable')), request_method TEXT CHECK(request_method IS NULL OR request_method IN ('GET','POST')), request_path TEXT CHECK(request_path IS NULL OR request_path IN ('/v1/models','/v1/chat/completions','/v1/embeddings','/v1/images/generations')), usage_total_mismatch INTEGER NOT NULL DEFAULT 0
 CHECK(typeof(usage_total_mismatch)='integer' AND usage_total_mismatch IN (0,1)), origin_user_id INTEGER CHECK(origin_user_id IS NULL OR (typeof(origin_user_id)='integer' AND origin_user_id>0)), origin_discord_id TEXT CHECK(origin_discord_id IS NULL OR (typeof(origin_discord_id)='text' AND length(origin_discord_id) BETWEEN 1 AND 20 AND origin_discord_id NOT GLOB '*[^0-9]*' AND substr(origin_discord_id,1,1) BETWEEN '1' AND '9')),
 CHECK((caller_result_class IS NULL AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NULL AND status_code=0 AND error_code='') OR
       (caller_result_class='success' AND caller_status BETWEEN 200 AND 399 AND caller_error_code IS NULL AND completed_at IS NOT NULL) OR
       (caller_result_class='failed' AND caller_status BETWEEN 400 AND 599 AND caller_error_code IN ('internal','invalid_request','unauthorized','forbidden','not_found','conflict','method_not_allowed','rate_limited','payload_too_large','elevated_required','unbound_model','upstream','maintenance','service_unavailable','resource_limit_exceeded','resource_locked','debug_dry_run_intercepted','debug_live_result_captured','debug_live_cancelled','insufficient_credits','feature_disabled','charity_suspended','content_too_short','already_checked_in','checkin_cap_reached') AND completed_at IS NOT NULL) OR
       (caller_result_class='cancelled' AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NOT NULL)),
 CHECK((caller_result_class IS NULL AND completed_at IS NULL) OR (caller_result_class IS NOT NULL AND completed_at IS NOT NULL))
);
INSERT INTO request_logs(id,logical_request_id,user_id,model,endpoint_key_id,upstream_model_id,route_kind,endpoint_base_url,caller_result_class,caller_status,caller_error_code,attempt_count,status_code,duration_ms,started_at,completed_at,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,error_source,error_code,error_diag,legal_hold_consumed,rejection_stage,rejection_reason,request_method,request_path,usage_total_mismatch,origin_user_id,origin_discord_id) SELECT id,logical_request_id,user_id,model,endpoint_key_id,upstream_model_id,route_kind,endpoint_base_url,caller_result_class,caller_status,caller_error_code,attempt_count,status_code,duration_ms,started_at,completed_at,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,error_source,error_code,error_diag,legal_hold_consumed,rejection_stage,rejection_reason,request_method,request_path,usage_total_mismatch,origin_user_id,origin_discord_id FROM image_migration_request_logs;
DROP TABLE image_migration_request_logs;
CREATE INDEX idx_request_logs_origin_discord ON request_logs(origin_discord_id,started_at,id);
CREATE INDEX idx_request_logs_origin_user ON request_logs(origin_user_id,started_at,id);
CREATE INDEX idx_request_logs_phase ON request_logs(user_id,rejection_stage,started_at,id);
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id);
CREATE INDEX idx_request_logs_started ON request_logs(started_at,id);
CREATE INDEX idx_request_logs_usage_mismatch ON request_logs(started_at DESC,id DESC) WHERE usage_total_mismatch=1;
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);
DELETE FROM sqlite_sequence WHERE name IN ('logical_requests','request_logs');
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM image_migration_sequence;
DROP TABLE image_migration_sequence;
CREATE TRIGGER dispatch_claim_attempt_limit_guard BEFORE INSERT ON dispatch_claims
 WHEN NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND NEW.attempt_seq<=r.attempt_limit)
 BEGIN SELECT RAISE(ABORT,'dispatch attempt exceeds request limit'); END;
CREATE TRIGGER dispatch_claim_attempt_limit_update_guard BEFORE UPDATE OF logical_request_id,attempt_seq ON dispatch_claims
 WHEN NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND NEW.attempt_seq<=r.attempt_limit)
 BEGIN SELECT RAISE(ABORT,'dispatch attempt exceeds request limit'); END;
CREATE TRIGGER generation_two_integer_type_logical_requests_insert_guard BEFORE INSERT ON logical_requests
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.attempt_limit IS NOT NULL AND typeof(NEW.attempt_limit)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.account_reserved_milli IS NOT NULL AND typeof(NEW.account_reserved_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_logical_requests_update_guard BEFORE UPDATE ON logical_requests
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.attempt_limit IS NOT NULL AND typeof(NEW.attempt_limit)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.account_reserved_milli IS NOT NULL AND typeof(NEW.account_reserved_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_logs_insert_guard BEFORE INSERT ON request_logs
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.status_code IS NOT NULL AND typeof(NEW.status_code)<>'integer')
 OR (NEW.duration_ms IS NOT NULL AND typeof(NEW.duration_ms)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_logs_update_guard BEFORE UPDATE ON request_logs
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.status_code IS NOT NULL AND typeof(NEW.status_code)<>'integer')
 OR (NEW.duration_ms IS NOT NULL AND typeof(NEW.duration_ms)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER legal_hold_consumed_guard_log BEFORE UPDATE OF legal_hold_consumed ON request_logs WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;
CREATE TRIGGER legal_hold_delete_log BEFORE DELETE ON request_logs
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='request_log' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_request_attempt BEFORE DELETE ON request_attempts
WHEN EXISTS(SELECT 1 FROM request_logs l WHERE l.id=OLD.request_log_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='request_log' AND h.object_ref=CAST(l.id AS TEXT) AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_insert_guard BEFORE INSERT ON legal_holds
WHEN NEW.state='active' AND NOT (
 (NEW.object_kind='maintenance_event' AND EXISTS(SELECT 1 FROM maintenance_events WHERE id=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='report_case' AND EXISTS(SELECT 1 FROM report_cases WHERE id=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='announcement_audit' AND EXISTS(SELECT 1 FROM announcement_audits WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='donation' AND EXISTS(SELECT 1 FROM donations WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='request_log' AND EXISTS(SELECT 1 FROM request_logs WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)))
BEGIN SELECT RAISE(ABORT,'legal hold object does not exist'); END;
CREATE TRIGGER legal_hold_mark_log BEFORE UPDATE OF legal_hold_consumed ON request_logs
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='request_log' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_marker_insert_log BEFORE INSERT ON request_logs
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER logical_request_acceptance_identity_update_guard BEFORE UPDATE OF route_kind,model_snapshot,attempt_limit,created_at ON logical_requests
WHEN NEW.route_kind IS NOT OLD.route_kind OR NEW.model_snapshot IS NOT OLD.model_snapshot OR
     NEW.attempt_limit IS NOT OLD.attempt_limit OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'logical request acceptance facts are immutable'); END;
CREATE TRIGGER logical_request_destination_insert_guard BEFORE INSERT ON logical_requests
 WHEN NEW.settlement_destination='external'
 BEGIN SELECT RAISE(ABORT,'external settlement requires an in-flight user handoff'); END;
CREATE TRIGGER logical_request_destination_update_guard BEFORE UPDATE OF user_id,settlement_destination ON logical_requests
 WHEN (OLD.settlement_destination='external' AND NEW.settlement_destination<>'external')
  OR (NEW.settlement_destination='external' AND NEW.user_id IS NOT NULL)
  OR (OLD.settlement_destination='user' AND NEW.settlement_destination='external' AND
      NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.logical_request_id=NEW.id AND c.dispatched_at IS NOT NULL))
 BEGIN SELECT RAISE(ABORT,'logical request settlement handoff is invalid'); END;
CREATE TRIGGER logical_request_log_snapshot_sync AFTER UPDATE OF state,user_id,route_kind,caller_result_class,caller_status,caller_error_code,terminal_at ON logical_requests
 WHEN EXISTS(SELECT 1 FROM request_logs l WHERE l.logical_request_id=NEW.id)
 BEGIN
   UPDATE request_logs
   SET user_id=NEW.user_id,
       route_kind=NEW.route_kind,
       caller_result_class=NEW.caller_result_class,
       caller_status=NEW.caller_status,
       caller_error_code=NEW.caller_error_code,
       status_code=COALESCE(NEW.caller_status,0),
       error_code=COALESCE(NEW.caller_error_code,''),
       completed_at=NEW.terminal_at
   WHERE logical_request_id=NEW.id;
 END;
CREATE TRIGGER logical_request_terminal_state_guard BEFORE UPDATE OF state ON logical_requests
WHEN OLD.state='terminal' AND NEW.state<>'terminal'
BEGIN SELECT RAISE(ABORT,'terminal logical request cannot be reopened'); END;
CREATE TRIGGER logical_requests_rejection_insert BEFORE INSERT ON logical_requests
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.state<>'terminal' OR NEW.accounting_state<>'none' OR NEW.account_reserved_milli<>0 OR NEW.ledger_rows_remaining<>X'00000000000000000000000000000000')) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER logical_requests_rejection_update BEFORE UPDATE ON logical_requests
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.state<>'terminal' OR NEW.accounting_state<>'none' OR NEW.account_reserved_milli<>0 OR NEW.ledger_rows_remaining<>X'00000000000000000000000000000000')) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER rejected_request_no_dispatch BEFORE INSERT ON dispatch_claims
WHEN EXISTS(SELECT 1 FROM logical_requests WHERE id=NEW.logical_request_id AND rejection_stage IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'rejected request cannot dispatch'); END;
CREATE TRIGGER request_log_logical_snapshot_guard BEFORE INSERT ON request_logs
 WHEN NOT EXISTS(
   SELECT 1 FROM logical_requests r
   WHERE r.id=NEW.logical_request_id AND NEW.user_id IS r.user_id AND NEW.route_kind IS r.route_kind
     AND NEW.caller_result_class IS r.caller_result_class AND NEW.caller_status IS r.caller_status
     AND NEW.caller_error_code IS r.caller_error_code
     AND NEW.status_code IS COALESCE(r.caller_status,0)
     AND NEW.error_code IS COALESCE(r.caller_error_code,'')
     AND NEW.completed_at IS r.terminal_at)
 BEGIN SELECT RAISE(ABORT,'request log caller snapshot mismatch'); END;
CREATE TRIGGER request_log_logical_snapshot_update_guard BEFORE UPDATE OF logical_request_id,user_id,route_kind,caller_result_class,caller_status,caller_error_code,status_code,error_code,completed_at ON request_logs
 WHEN NOT EXISTS(
   SELECT 1 FROM logical_requests r
   WHERE r.id=NEW.logical_request_id AND NEW.user_id IS r.user_id AND NEW.route_kind IS r.route_kind
     AND NEW.caller_result_class IS r.caller_result_class AND NEW.caller_status IS r.caller_status
     AND NEW.caller_error_code IS r.caller_error_code
     AND NEW.status_code IS COALESCE(r.caller_status,0)
     AND NEW.error_code IS COALESCE(r.caller_error_code,'')
     AND NEW.completed_at IS r.terminal_at)
 BEGIN SELECT RAISE(ABORT,'request log caller snapshot mismatch'); END;
CREATE TRIGGER request_log_origin_capture AFTER INSERT ON request_logs
WHEN NEW.user_id IS NOT NULL AND NEW.origin_user_id IS NULL
BEGIN
 UPDATE request_logs SET origin_user_id=NEW.user_id,
 origin_discord_id=(SELECT discord_id FROM users WHERE id=NEW.user_id
  AND length(discord_id) BETWEEN 1 AND 20 AND discord_id NOT GLOB '*[^0-9]*'
  AND substr(discord_id,1,1) BETWEEN '1' AND '9') WHERE id=NEW.id;
END;
CREATE TRIGGER request_log_origin_immutable BEFORE UPDATE OF origin_user_id,origin_discord_id ON request_logs
WHEN (OLD.origin_user_id IS NOT NULL AND NEW.origin_user_id IS NOT OLD.origin_user_id)
 OR (OLD.origin_discord_id IS NOT NULL AND NEW.origin_discord_id IS NOT OLD.origin_discord_id)
BEGIN SELECT RAISE(ABORT,'request origin is immutable'); END;
CREATE TRIGGER request_logs_rejection_insert BEFORE INSERT ON request_logs
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.attempt_count<>0 OR NEW.uncached_input_tokens<>0 OR NEW.cache_write_input_tokens<>0 OR NEW.cache_read_input_tokens<>0 OR NEW.output_tokens<>0 OR NEW.usage_unknown<>0
 OR NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND r.rejection_stage IS NEW.rejection_stage AND r.rejection_reason IS NEW.rejection_reason AND r.request_method IS NEW.request_method AND r.request_path IS NEW.request_path))) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER request_logs_rejection_update BEFORE UPDATE ON request_logs
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.attempt_count<>0 OR NEW.uncached_input_tokens<>0 OR NEW.cache_write_input_tokens<>0 OR NEW.cache_read_input_tokens<>0 OR NEW.output_tokens<>0 OR NEW.usage_unknown<>0
 OR NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND r.rejection_stage IS NEW.rejection_stage AND r.rejection_reason IS NEW.rejection_reason AND r.request_method IS NEW.request_method AND r.request_path IS NEW.request_path))) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER risk_scan_result_source_deleted BEFORE DELETE ON request_source_facts
BEGIN
 UPDATE risk_client_scans SET changed=1,
 state=CASE WHEN state IN ('queued','running') THEN 'failed' ELSE state END,
 reason=CASE WHEN state IN ('queued','running') THEN 'source_changed' ELSE reason END,
 checkpoint_json=json_remove(checkpoint_json,'$.pending_user','$.after_user','$.pending_ip','$.after_ip',
 '$.source_at','$.source_id','$.ip_summary','$.pending_discord','$.after_discord','$.user_ip_summary')
 WHERE id IN (SELECT scan_id FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 UNION SELECT scan_id FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id
 UNION SELECT scan_id FROM risk_scan_window_sources WHERE request_log_id=OLD.request_log_id)
 OR (kind IN ('client_hits','users','shared_ips','user_ips') AND (
 json_extract(checkpoint_json,'$.pending_user')=(SELECT origin_user_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.after_user')=(SELECT origin_user_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.pending_discord')=(SELECT origin_discord_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.after_discord')=(SELECT origin_discord_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.source_id')=OLD.source_id))
 OR (OLD.effective_ip<>'' AND (json_extract(checkpoint_json,'$.pending_ip')=OLD.effective_ip OR json_extract(checkpoint_json,'$.after_ip')=OLD.effective_ip));
 DELETE FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id);
END;
CREATE TRIGGER users_request_handoff_delete_guard BEFORE DELETE ON users
WHEN EXISTS(
 SELECT 1 FROM logical_requests r
 WHERE r.user_id=OLD.id AND r.settlement_destination='user'
   AND (r.accounting_state='reserved' OR EXISTS(
    SELECT 1 FROM dispatch_claims c
    WHERE c.logical_request_id=r.id AND c.state IN ('claimed','dispatched')
   ))
)
BEGIN SELECT RAISE(ABORT,'user request settlement is not handed off'); END;
