package db

// Governance tables share the ordinary request, account, and ledger roots.
const governanceTablesSchema = `
CREATE TABLE image_activity_tasks (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='img_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','dispatched','succeeded','failed','cancelled','unknown')),
 ledger_rows_remaining BLOB NOT NULL CHECK(ledger_rows_remaining IN (X'00000000000000000000000000000000',X'00000000000000000000000000000001')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,created_at,id);

CREATE TABLE request_source_facts (
 request_log_id INTEGER PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 kind TEXT NOT NULL CHECK(kind IN ('self','charity','unclassified','discovery')),
 effective_ip TEXT NOT NULL CHECK(length(effective_ip)<=45),
 ip_quality TEXT NOT NULL CHECK(ip_quality IN ('direct_peer','trusted_forwarded','peer_fallback')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))<=8192),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_request_sources_user_time ON request_source_facts(user_id,occurred_at,request_log_id);
CREATE INDEX idx_request_sources_ip_time ON request_source_facts(effective_ip,occurred_at,request_log_id);

CREATE TABLE request_error_bodies (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 request_log_id INTEGER REFERENCES request_logs(id) ON DELETE CASCADE,
 task_id TEXT REFERENCES image_activity_tasks(id) ON DELETE CASCADE,
 operation_id TEXT REFERENCES accepted_operations(id) ON DELETE CASCADE,
 attempt_seq INTEGER NOT NULL CHECK(attempt_seq>=1),
 event_seq INTEGER NOT NULL CHECK(event_seq>=1),
 http_status INTEGER CHECK(http_status BETWEEN 100 AND 599),
 content_type TEXT NOT NULL CHECK(length(CAST(content_type AS BLOB))<=256),
 body BLOB CHECK(body IS NULL OR length(body)<=1048576),
 bytes_saved INTEGER NOT NULL CHECK(bytes_saved BETWEEN 0 AND 1048576),
 truncated INTEGER NOT NULL CHECK(truncated IN (0,1)),
 save_state TEXT NOT NULL CHECK(save_state IN ('saved','capacity_exhausted','unavailable')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN created_at AND 253402300799),
 CHECK((request_log_id IS NOT NULL)+(task_id IS NOT NULL)+(operation_id IS NOT NULL)=1),
 CHECK((save_state='saved' AND body IS NOT NULL AND bytes_saved=length(body))
    OR (save_state<>'saved' AND body IS NULL AND bytes_saved=0))
) STRICT;
CREATE UNIQUE INDEX idx_request_errors_request ON request_error_bodies(request_log_id,attempt_seq,event_seq) WHERE request_log_id IS NOT NULL;
CREATE UNIQUE INDEX idx_request_errors_task ON request_error_bodies(task_id,attempt_seq,event_seq) WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX idx_request_errors_operation ON request_error_bodies(operation_id,attempt_seq,event_seq) WHERE operation_id IS NOT NULL;
CREATE INDEX idx_request_errors_expiry ON request_error_bodies(expires_at,id);

CREATE TABLE observability_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 raw_body_bytes INTEGER NOT NULL DEFAULT 0 CHECK(raw_body_bytes>=0),
 raw_capacity_omissions INTEGER NOT NULL DEFAULT 0 CHECK(raw_capacity_omissions>=0),
 raw_unavailable INTEGER NOT NULL DEFAULT 0 CHECK(raw_unavailable>=0),
 access_dropped INTEGER NOT NULL DEFAULT 0 CHECK(access_dropped>=0),
 access_rows INTEGER NOT NULL DEFAULT 0 CHECK(access_rows BETWEEN 0 AND 1000000),
 capture_started_at INTEGER NOT NULL CHECK(capture_started_at BETWEEN 0 AND 253402300799),
 last_access_gap_at INTEGER CHECK(last_access_gap_at BETWEEN capture_started_at AND 253402300799)
) STRICT;
CREATE TRIGGER request_error_delete_count AFTER DELETE ON request_error_bodies
BEGIN UPDATE observability_state SET raw_body_bytes=raw_body_bytes-OLD.bytes_saved WHERE id=1; END;

CREATE TABLE audit_access_events (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='aev_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 caller_key_user_id INTEGER REFERENCES caller_keys(user_id) ON DELETE SET NULL,
 caller_key_generation INTEGER CHECK(caller_key_generation>=0),
 path_kind TEXT NOT NULL CHECK(path_kind IN ('models','billing_subscription','billing_usage','v1_billing_subscription','v1_billing_usage','sub2api_billing','chat_head')),
 method TEXT NOT NULL CHECK((path_kind='chat_head' AND method='HEAD') OR (path_kind<>'chat_head' AND method='GET')),
 http_status INTEGER NOT NULL CHECK(http_status BETWEEN 100 AND 599),
 response_kind TEXT NOT NULL CHECK(response_kind IN ('api_json','spa_fallback','other')),
 effective_ip TEXT NOT NULL CHECK(length(effective_ip)<=45),
 ip_quality TEXT NOT NULL CHECK(ip_quality IN ('direct_peer','trusted_forwarded','peer_fallback')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))<=8192),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 CHECK((caller_key_user_id IS NULL)=(caller_key_generation IS NULL)),
 CHECK(caller_key_user_id IS NULL OR caller_key_user_id=user_id)
) STRICT;
CREATE INDEX idx_audit_access_user_time ON audit_access_events(user_id,occurred_at,id);
CREATE INDEX idx_audit_access_key_time ON audit_access_events(caller_key_user_id,caller_key_generation,occurred_at,id);
CREATE INDEX idx_audit_access_path_time ON audit_access_events(path_kind,occurred_at,id);
CREATE TRIGGER audit_access_delete_count AFTER DELETE ON audit_access_events
BEGIN UPDATE observability_state SET access_rows=access_rows-1 WHERE id=1; END;
CREATE TRIGGER audit_access_key_delete BEFORE DELETE ON caller_keys
BEGIN UPDATE audit_access_events SET caller_key_user_id=NULL,caller_key_generation=NULL WHERE caller_key_user_id=OLD.user_id; END;
CREATE TRIGGER audit_access_key_rotate AFTER UPDATE OF generation,key_hash ON caller_keys
WHEN NEW.generation<>OLD.generation OR NEW.key_hash IS NULL
BEGIN UPDATE audit_access_events SET caller_key_user_id=NULL,caller_key_generation=NULL WHERE caller_key_user_id=OLD.user_id; END;

CREATE TABLE anonymous_access_minutes (
 minute_at INTEGER NOT NULL CHECK(minute_at BETWEEN 0 AND 253402300799 AND minute_at%60=0),
 path_kind TEXT NOT NULL CHECK(path_kind IN ('models','billing_subscription','billing_usage','v1_billing_subscription','v1_billing_usage','sub2api_billing','chat_head')),
 method TEXT NOT NULL CHECK((path_kind='chat_head' AND method='HEAD') OR (path_kind<>'chat_head' AND method='GET')),
 status_class INTEGER NOT NULL CHECK(status_class BETWEEN 1 AND 5),
 count INTEGER NOT NULL CHECK(count>=0),
 PRIMARY KEY(minute_at,path_kind,method,status_class)
) STRICT, WITHOUT ROWID;

CREATE TABLE charity_request_outcomes (
 request_log_id INTEGER PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253402300799),
 dispatched INTEGER NOT NULL CHECK(dispatched IN (0,1)),
 result TEXT NOT NULL CHECK(result IN ('success','failure','cancelled'))
) STRICT;
CREATE INDEX idx_charity_outcomes_model_time ON charity_request_outcomes(model_id,completed_at,request_log_id);
`

const riskAuditSchema = `
CREATE TABLE risk_audit_minutes (
 epoch TEXT NOT NULL CHECK(length(epoch) BETWEEN 1 AND 64),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 minute INTEGER NOT NULL CHECK(minute>=0 AND minute%60=0),
 call_kind TEXT NOT NULL CHECK(call_kind IN ('total','self','charity','unclassified')),
 rpm_committed INTEGER NOT NULL DEFAULT 0 CHECK(rpm_committed>=0),
 rpm_released INTEGER NOT NULL DEFAULT 0 CHECK(rpm_released>=0),
 rpm_pending INTEGER NOT NULL DEFAULT 0 CHECK(rpm_pending>=0),
 rpm_denied INTEGER NOT NULL DEFAULT 0 CHECK(rpm_denied>=0),
 concurrency_denied INTEGER NOT NULL DEFAULT 0 CHECK(concurrency_denied>=0),
 occupancy_millis INTEGER NOT NULL DEFAULT 0 CHECK(occupancy_millis>=0),
 peak INTEGER NOT NULL DEFAULT 0 CHECK(peak>=0),
 rpm_limit INTEGER,
 concurrency_limit INTEGER,
 coverage INTEGER NOT NULL DEFAULT 0 CHECK(coverage>=0),
 config_revision INTEGER NOT NULL CHECK(config_revision>=1),
 updated_at INTEGER NOT NULL CHECK(updated_at>=minute),
 PRIMARY KEY(epoch,user_id,minute,call_kind),
 CHECK(rpm_limit IS NULL OR rpm_limit>0),
 CHECK(concurrency_limit IS NULL OR concurrency_limit>0)
) STRICT;
CREATE INDEX idx_risk_minutes_user_time ON risk_audit_minutes(user_id,minute,call_kind);
CREATE INDEX idx_risk_minutes_retention ON risk_audit_minutes(minute,user_id);
CREATE TABLE risk_audit_gaps (
 epoch TEXT NOT NULL CHECK(length(epoch) BETWEEN 1 AND 64),
 minute INTEGER NOT NULL CHECK(minute>=0 AND minute%60=0),
 reason TEXT NOT NULL CHECK(reason IN ('restart','queue_overflow','capacity','persistence','configuration','clock')),
 lost_count INTEGER NOT NULL DEFAULT 0 CHECK(lost_count>=0),
 PRIMARY KEY(epoch,minute,reason)
) STRICT;
CREATE INDEX idx_risk_gaps_time ON risk_audit_gaps(minute);
CREATE TABLE risk_audit_config (
 id INTEGER NOT NULL PRIMARY KEY CHECK(id=1),
 threshold_percent INTEGER NOT NULL CHECK(threshold_percent BETWEEN 1 AND 100),
 consecutive_minutes INTEGER NOT NULL CHECK(consecutive_minutes BETWEEN 1 AND 60),
 shared_ip_hours INTEGER NOT NULL CHECK(shared_ip_hours BETWEEN 1 AND 720),
 shared_ip_users INTEGER NOT NULL CHECK(shared_ip_users BETWEEN 2 AND 1000),
 revision INTEGER NOT NULL CHECK(revision>=1),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
CREATE TABLE risk_client_rules (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 status TEXT NOT NULL CHECK(status IN ('suspected','confirmed')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>=1),
 conditions_json TEXT NOT NULL CHECK(json_valid(conditions_json) AND length(CAST(conditions_json AS BLOB)) BETWEEN 2 AND 16384),
 evidence_note TEXT NOT NULL DEFAULT '' CHECK(length(evidence_note)<=4096),
 evidence_url TEXT NOT NULL DEFAULT '' CHECK(length(evidence_url)<=2048),
 created_by_role TEXT NOT NULL CHECK(created_by_role IN ('admin','level6')),
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_by_role TEXT NOT NULL CHECK(updated_by_role IN ('admin','level6')),
 updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at)
) STRICT;
CREATE INDEX idx_risk_rules_updated ON risk_client_rules(updated_at,id);
`

const economyAuditSchema = `
CREATE TABLE economy_audit_checkpoint (
 id INTEGER PRIMARY KEY CHECK(id=1),
 last_ledger_seq INTEGER NOT NULL DEFAULT 0 CHECK(typeof(last_ledger_seq)='integer' AND last_ledger_seq BETWEEN 0 AND 9223372036854775807),
 first_ledger_seq INTEGER CHECK(first_ledger_seq IS NULL OR (typeof(first_ledger_seq)='integer' AND first_ledger_seq BETWEEN 1 AND last_ledger_seq)),
 first_occurred_at INTEGER CHECK(first_occurred_at IS NULL OR (typeof(first_occurred_at)='integer' AND first_occurred_at BETWEEN 0 AND 253402300799)),
 offset_minutes INTEGER CHECK(offset_minutes IS NULL OR (typeof(offset_minutes)='integer' AND offset_minutes BETWEEN -720 AND 840 AND offset_minutes%30=0)),
 unclassified_operations INTEGER NOT NULL DEFAULT 0 CHECK(typeof(unclassified_operations)='integer' AND unclassified_operations BETWEEN 0 AND last_ledger_seq),
 opening_known INTEGER NOT NULL CHECK(opening_known IN (0,1)),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799),
 CHECK((last_ledger_seq=0 AND first_ledger_seq IS NULL AND first_occurred_at IS NULL) OR
       (last_ledger_seq>0 AND first_ledger_seq IS NOT NULL AND first_occurred_at IS NOT NULL AND offset_minutes IS NOT NULL))
) STRICT;
CREATE TABLE economy_audit_buckets (
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'),
 source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 32 AND source_type NOT GLOB '*[^a-z0-9_]*'),
 channel TEXT NOT NULL CHECK(channel IN ('admin','account','checkin','welfare','thursday','api','charity','donation','fishing','linklink','rps','bidding','likes','blackjack','onboarding','loan','picture_book','inactivity','penalty','unclassified')),
 bucket TEXT NOT NULL CHECK(bucket IN ('hour','day')),
 bucket_start INTEGER NOT NULL CHECK(typeof(bucket_start)='integer' AND bucket_start BETWEEN -86400 AND 253402300799),
 offset_minutes INTEGER NOT NULL CHECK(typeof(offset_minutes)='integer' AND offset_minutes BETWEEN -720 AND 840 AND offset_minutes%30=0),
 issued TEXT NOT NULL CHECK(typeof(issued)='text' AND length(issued) BETWEEN 1 AND 64 AND issued NOT GLOB '*[^0-9]*' AND (issued='0' OR substr(issued,1,1)<>'0')),
 reclaimed TEXT NOT NULL CHECK(typeof(reclaimed)='text' AND length(reclaimed) BETWEEN 1 AND 64 AND reclaimed NOT GLOB '*[^0-9]*' AND (reclaimed='0' OR substr(reclaimed,1,1)<>'0')),
 user_income TEXT NOT NULL CHECK(typeof(user_income)='text' AND length(user_income) BETWEEN 1 AND 64 AND user_income NOT GLOB '*[^0-9]*' AND (user_income='0' OR substr(user_income,1,1)<>'0')),
 user_expense TEXT NOT NULL CHECK(typeof(user_expense)='text' AND length(user_expense) BETWEEN 1 AND 64 AND user_expense NOT GLOB '*[^0-9]*' AND (user_expense='0' OR substr(user_expense,1,1)<>'0')),
 internal_transfer TEXT NOT NULL CHECK(typeof(internal_transfer)='text' AND length(internal_transfer) BETWEEN 1 AND 64 AND internal_transfer NOT GLOB '*[^0-9]*' AND (internal_transfer='0' OR substr(internal_transfer,1,1)<>'0')),
 operation_count INTEGER NOT NULL CHECK(typeof(operation_count)='integer' AND operation_count BETWEEN 1 AND 9223372036854775807),
 first_ledger_seq INTEGER NOT NULL CHECK(typeof(first_ledger_seq)='integer' AND first_ledger_seq BETWEEN 1 AND 9223372036854775807),
 last_ledger_seq INTEGER NOT NULL CHECK(typeof(last_ledger_seq)='integer' AND last_ledger_seq BETWEEN first_ledger_seq AND 9223372036854775807),
 PRIMARY KEY(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes),
 CHECK((bucket_start+offset_minutes*60)%CASE bucket WHEN 'hour' THEN 3600 ELSE 86400 END=0)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_economy_audit_buckets_time ON economy_audit_buckets(asset_type,bucket,bucket_start,kind,source_type,channel);
`
