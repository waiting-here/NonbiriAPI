-- Convert the exact rc.7 schema without changing retained business facts.
CREATE TEMP TABLE previous_sequences AS SELECT name,seq FROM sqlite_sequence WHERE name='abuse_actions';
DROP TRIGGER credit_operation_source_guard;
DROP TRIGGER credit_operation_source_update_guard;
DROP TRIGGER generation_two_credit_operations_u128_guard;
DROP TRIGGER generation_two_credit_operations_u128_update_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_update_guard;
DROP TRIGGER credit_operations_no_update;
DROP TRIGGER credit_operations_no_delete;
DROP TRIGGER credit_operation_donation_identity_guard;
CREATE TEMP TABLE previous_credit_operations AS SELECT * FROM credit_operations;
DROP TABLE credit_operations;
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'), source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 64 AND source_type NOT GLOB '*[^a-z0-9_]*'), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
INSERT INTO credit_operations SELECT * FROM previous_credit_operations;
DROP TABLE previous_credit_operations;
CREATE INDEX idx_credit_operations_source ON credit_operations(source_type,source_id,source_seq);
CREATE INDEX idx_credit_operations_created ON credit_operations(created_at,ledger_seq);
CREATE TRIGGER credit_operation_source_guard BEFORE INSERT ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER credit_operation_source_update_guard BEFORE UPDATE OF source_type,source_id ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER generation_two_credit_operations_u128_guard BEFORE INSERT ON credit_operations
WHEN typeof(NEW.source_seq)<>'blob' OR length(NEW.source_seq)<>16 OR substr(hex(NEW.source_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.donation_credit_delta_mag)<>'blob' OR length(NEW.donation_credit_delta_mag)<>16 OR substr(hex(NEW.donation_credit_delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.donation_credit_after IS NOT NULL AND (typeof(NEW.donation_credit_after)<>'blob' OR length(NEW.donation_credit_after)<>16 OR substr(hex(NEW.donation_credit_after),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'credit operation U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_operations_u128_update_guard BEFORE UPDATE ON credit_operations
WHEN typeof(NEW.source_seq)<>'blob' OR length(NEW.source_seq)<>16 OR substr(hex(NEW.source_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.donation_credit_delta_mag)<>'blob' OR length(NEW.donation_credit_delta_mag)<>16 OR substr(hex(NEW.donation_credit_delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.donation_credit_after IS NOT NULL AND (typeof(NEW.donation_credit_after)<>'blob' OR length(NEW.donation_credit_after)<>16 OR substr(hex(NEW.donation_credit_after),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'credit operation U128 value is invalid'); END;
CREATE TRIGGER generation_two_integer_type_credit_operations_insert_guard BEFORE INSERT ON credit_operations
WHEN (NEW.ledger_seq IS NOT NULL AND typeof(NEW.ledger_seq)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.donation_credit_user_id IS NOT NULL AND typeof(NEW.donation_credit_user_id)<>'integer')
 OR (NEW.donation_credit_delta_sign IS NOT NULL AND typeof(NEW.donation_credit_delta_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_operations_update_guard BEFORE UPDATE ON credit_operations
WHEN (NEW.ledger_seq IS NOT NULL AND typeof(NEW.ledger_seq)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.donation_credit_user_id IS NOT NULL AND typeof(NEW.donation_credit_user_id)<>'integer')
 OR (NEW.donation_credit_delta_sign IS NOT NULL AND typeof(NEW.donation_credit_delta_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE INDEX idx_credit_duel_terminal ON credit_operations(substr(source_id,1,4),ledger_seq) WHERE kind='duel_terminal' AND source_type='duel_session';
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
CREATE TRIGGER credit_operations_no_update BEFORE UPDATE ON credit_operations
WHEN NOT (
 NEW.id IS OLD.id AND NEW.ledger_seq IS OLD.ledger_seq AND NEW.kind IS OLD.kind AND
 NEW.source_type IS OLD.source_type AND NEW.source_id IS OLD.source_id AND NEW.source_seq IS OLD.source_seq AND
 NEW.created_at IS OLD.created_at AND (
  (NEW.compacted IS OLD.compacted AND
   NEW.donation_credit_delta_sign IS OLD.donation_credit_delta_sign AND NEW.donation_credit_delta_mag IS OLD.donation_credit_delta_mag AND
   NEW.donation_credit_after IS OLD.donation_credit_after AND NEW.reason IS OLD.reason AND
   (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))) AND
   (NEW.donation_credit_user_id IS OLD.donation_credit_user_id OR (NEW.donation_credit_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.donation_credit_user_id))))
  OR (OLD.compacted=0 AND NEW.compacted=1 AND OLD.ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1) AND
   NEW.actor_user_id IS NULL AND NEW.donation_credit_user_id IS NULL AND NEW.donation_credit_delta_sign=0 AND
   NEW.donation_credit_delta_mag=zeroblob(16) AND NEW.donation_credit_after IS NULL AND NEW.reason IS NULL)
 ))
BEGIN SELECT RAISE(ABORT,'credit_operations is immutable outside compaction'); END;
CREATE TRIGGER credit_operations_no_delete BEFORE DELETE ON credit_operations
WHEN OLD.compacted<>1 OR OLD.ledger_seq>(SELECT through_seq FROM credit_compaction WHERE id=1)
BEGIN SELECT RAISE(ABORT,'credit operation is not compacted'); END;
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
CREATE TRIGGER credit_operation_donation_identity_guard BEFORE INSERT ON credit_operations
WHEN NEW.donation_credit_user_id IS NULL AND (NEW.donation_credit_delta_sign<>0 OR NEW.donation_credit_after IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'new donation credit requires an account'); END;
CREATE TEMP TABLE previous_economy_audit_buckets AS SELECT * FROM economy_audit_buckets;
DROP TABLE economy_audit_buckets;
CREATE TABLE economy_audit_buckets (
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'),
 source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 32 AND source_type NOT GLOB '*[^a-z0-9_]*'),
 channel TEXT NOT NULL CHECK(length(channel) BETWEEN 1 AND 64 AND channel NOT GLOB '*[^a-z0-9_]*'),
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
INSERT INTO economy_audit_buckets SELECT * FROM previous_economy_audit_buckets;
DROP TABLE previous_economy_audit_buckets;
CREATE INDEX idx_economy_audit_buckets_time ON economy_audit_buckets(asset_type,bucket,bucket_start,kind,source_type,channel);
CREATE TEMP TABLE previous_abuse_windows AS SELECT * FROM abuse_windows;
DROP TABLE abuse_windows;
CREATE TABLE abuse_windows (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content','redemption_code')),
 ban_done INTEGER NOT NULL CHECK(ban_done IN (0,1)),
 suspend_done INTEGER NOT NULL CHECK(suspend_done IN (0,1)),
 next_event_seq INTEGER NOT NULL CHECK(next_event_seq>0),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,violation_kind)
) STRICT;
INSERT INTO abuse_windows SELECT * FROM previous_abuse_windows;
DROP TABLE previous_abuse_windows;
CREATE TEMP TABLE previous_abuse_window_events AS SELECT * FROM abuse_window_events;
DROP TABLE abuse_window_events;
CREATE TABLE abuse_window_events (
 user_id INTEGER NOT NULL,
 violation_kind TEXT NOT NULL,
 seq INTEGER NOT NULL CHECK(seq>0),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576), expires_at INTEGER CHECK(expires_at>occurred_at AND expires_at<=253402300799),
 PRIMARY KEY(user_id,violation_kind,seq), UNIQUE(request_id,violation_kind),
 FOREIGN KEY(user_id,violation_kind) REFERENCES abuse_windows(user_id,violation_kind) ON DELETE CASCADE,
 CHECK((violation_kind IN ('rpm','redemption_code') AND content_chars IS NULL) OR (violation_kind='short_content' AND content_chars IS NOT NULL))
) STRICT;
INSERT INTO abuse_window_events SELECT * FROM previous_abuse_window_events;
DROP TABLE previous_abuse_window_events;
CREATE INDEX idx_abuse_events_window ON abuse_window_events(user_id,violation_kind,occurred_at,seq);
CREATE INDEX idx_abuse_events_expiry ON abuse_window_events(violation_kind,occurred_at,user_id,seq);
CREATE TEMP TABLE previous_abuse_cases AS SELECT * FROM abuse_cases;
DROP TABLE abuse_cases;
CREATE TABLE abuse_cases (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='abc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('deduction','ban','charity_suspend')),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content','redemption_code')),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN started_at AND 253402300799),
 ended_at INTEGER CHECK(ended_at IS NULL OR ended_at BETWEEN started_at AND 253402300799),
 state TEXT NOT NULL CHECK(state IN ('active','ended')),
 result TEXT NOT NULL CHECK(result IN ('applied','extended','adjusted','released','expired')),
 CHECK((state='active' AND ended_at IS NULL AND kind<>'deduction') OR (state='ended' AND ended_at IS NOT NULL))
) STRICT;
INSERT INTO abuse_cases SELECT * FROM previous_abuse_cases;
DROP TABLE previous_abuse_cases;
CREATE UNIQUE INDEX idx_abuse_cases_active ON abuse_cases(user_id,kind) WHERE state='active';
CREATE INDEX idx_abuse_cases_user ON abuse_cases(user_id,started_at DESC,id);
CREATE INDEX idx_abuse_cases_retention ON abuse_cases(state,ended_at,id);
CREATE INDEX idx_abuse_cases_due ON abuse_cases(ends_at,id) WHERE state='active' AND ends_at IS NOT NULL;
DROP TRIGGER abuse_action_immutable;
CREATE TEMP TABLE previous_abuse_actions AS SELECT * FROM abuse_actions;
DROP TABLE abuse_actions;
CREATE TABLE abuse_actions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT CHECK(seq>0),
 case_id TEXT NOT NULL REFERENCES abuse_cases(id) ON DELETE CASCADE,
 action TEXT NOT NULL CHECK(action IN ('trigger','extend','adjust','release','expire')),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT CHECK(request_id IS NULL OR (length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w'))),
 operation_id TEXT REFERENCES credit_operations(id) ON DELETE RESTRICT,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 previous_ends_at INTEGER CHECK(previous_ends_at IS NULL OR previous_ends_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content','redemption_code','manual_adjustment','manual_release','expired')),
 rules_json TEXT NOT NULL CHECK(length(CAST(rules_json AS BLOB))<=4096 AND json_valid(rules_json) AND json_type(rules_json)='object'),
 statistics_json TEXT NOT NULL CHECK(length(CAST(statistics_json AS BLOB))<=4096 AND json_valid(statistics_json) AND json_type(statistics_json)='object'),
 evidence_count INTEGER NOT NULL CHECK(evidence_count BETWEEN 0 AND 4096),
 evidence_bytes INTEGER NOT NULL CHECK(evidence_bytes BETWEEN 0 AND 1048576)
) STRICT;
INSERT INTO abuse_actions SELECT * FROM previous_abuse_actions;
DROP TABLE previous_abuse_actions;
CREATE INDEX idx_abuse_actions_case ON abuse_actions(case_id,seq DESC);
CREATE INDEX idx_abuse_actions_actor ON abuse_actions(actor_user_id) WHERE actor_user_id IS NOT NULL;
CREATE TRIGGER abuse_action_immutable BEFORE UPDATE ON abuse_actions
WHEN NOT (NEW.seq IS OLD.seq AND NEW.case_id IS OLD.case_id AND NEW.action IS OLD.action AND NEW.occurred_at IS OLD.occurred_at
 AND NEW.request_id IS OLD.request_id AND NEW.operation_id IS OLD.operation_id AND NEW.previous_ends_at IS OLD.previous_ends_at AND NEW.ends_at IS OLD.ends_at
 AND NEW.reason_code IS OLD.reason_code AND NEW.rules_json IS OLD.rules_json AND NEW.statistics_json IS OLD.statistics_json
 AND NEW.evidence_count IS OLD.evidence_count AND NEW.evidence_bytes IS OLD.evidence_bytes
 AND (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))))
BEGIN SELECT RAISE(ABORT,'penalty action is immutable'); END;
CREATE INDEX idx_abuse_action_operation ON abuse_actions(operation_id) WHERE operation_id IS NOT NULL;
DROP TRIGGER abuse_evidence_immutable;
DROP TRIGGER abuse_evidence_bound;
CREATE TEMP TABLE previous_abuse_evidence AS SELECT * FROM abuse_evidence;
DROP TABLE abuse_evidence;
CREATE TABLE abuse_evidence (
 action_seq INTEGER NOT NULL REFERENCES abuse_actions(seq) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 4095),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content','redemption_code')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576),
 PRIMARY KEY(action_seq,ordinal), UNIQUE(action_seq,request_id)
) STRICT;
INSERT INTO abuse_evidence SELECT * FROM previous_abuse_evidence;
DROP TABLE previous_abuse_evidence;
CREATE TRIGGER abuse_evidence_immutable BEFORE UPDATE ON abuse_evidence BEGIN SELECT RAISE(ABORT,'penalty evidence is immutable'); END;
CREATE TRIGGER abuse_evidence_bound BEFORE INSERT ON abuse_evidence
WHEN NOT EXISTS(SELECT 1 FROM abuse_actions WHERE seq=NEW.action_seq AND NEW.ordinal<evidence_count)
BEGIN SELECT RAISE(ABORT,'penalty evidence exceeds declared count'); END;
CREATE TEMP TABLE previous_identity_window_events AS SELECT * FROM identity_window_events;
DROP TABLE identity_window_events;
CREATE TABLE identity_window_events (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 kind TEXT NOT NULL CHECK(kind IN ('user_rpm','report_frequency','game_start','abuse_rpm','abuse_short_content','abuse_redemption_code')),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128),
 event_key TEXT NOT NULL CHECK(length(CAST(event_key AS BLOB)) BETWEEN 1 AND 128),
 occurred_at_ms INTEGER NOT NULL CHECK(occurred_at_ms BETWEEN 0 AND 253402300799000),
 expires_at_ms INTEGER NOT NULL CHECK(expires_at_ms>occurred_at_ms AND expires_at_ms<=253402300799000),
 count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 9223372036854775807),
 value INTEGER CHECK(value>=0),
 PRIMARY KEY(identity_key,kind,scope,event_key)
) STRICT, WITHOUT ROWID;
INSERT INTO identity_window_events SELECT * FROM previous_identity_window_events;
DROP TABLE previous_identity_window_events;
CREATE INDEX idx_identity_windows_expiry ON identity_window_events(expires_at_ms,identity_key,kind,scope,event_key);
CREATE INDEX idx_identity_windows_current ON identity_window_events(identity_key,kind,scope,occurred_at_ms);
DROP TRIGGER idempotency_expiry_guard;
DROP TRIGGER idempotency_expiry_update_guard;
DROP TRIGGER idempotency_identity_update_guard;
DROP TRIGGER generation_two_integer_type_idempotency_records_insert_guard;
DROP TRIGGER generation_two_integer_type_idempotency_records_update_guard;
CREATE TEMP TABLE previous_idempotency_records AS SELECT * FROM idempotency_records;
DROP TABLE idempotency_records;
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL CHECK(scope IN ('credential_report','control_mutation','openai_chat_completions','charity_chat_completions','model_discovery','maintenance','announcement','activity','game_fishing','game_linklink','game_rps','game_bidding','game_likes','game_gwent','game_catch','game_blackjack','activity_loan','donation','lake_notes','personal_automation','reward_redemption')), actor_scope_hash BLOB NOT NULL CHECK(typeof(actor_scope_hash)='blob' AND length(actor_scope_hash)=32), key_hash BLOB NOT NULL CHECK(typeof(key_hash)='blob' AND length(key_hash)=32), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), lookup_fingerprint BLOB CHECK(lookup_fingerprint IS NULL OR (typeof(lookup_fingerprint)='blob' AND length(lookup_fingerprint)=32)), state TEXT NOT NULL CHECK(state IN ('accepted','completed')), http_status INTEGER NOT NULL CHECK(http_status=0 OR http_status BETWEEN 100 AND 599), response_body BLOB NOT NULL CHECK(typeof(response_body)='blob' AND length(response_body)<=65536), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), PRIMARY KEY(scope,actor_scope_hash,key_hash), CHECK(expires_at>=created_at), CHECK((scope='credential_report' AND lookup_fingerprint IS NOT NULL AND expires_at=created_at+86400) OR (scope<>'credential_report' AND lookup_fingerprint IS NULL)), CHECK((state='accepted' AND http_status=0 AND length(response_body)=0) OR (state='completed' AND http_status BETWEEN 100 AND 599))
);
INSERT INTO idempotency_records SELECT * FROM previous_idempotency_records;
DROP TABLE previous_idempotency_records;
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);
CREATE TRIGGER idempotency_expiry_guard BEFORE INSERT ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR
     (NEW.scope='reward_redemption' AND NEW.expires_at-NEW.created_at NOT BETWEEN 86400 AND 316224000) OR
     (NEW.scope<>'reward_redemption' AND NEW.expires_at<>NEW.created_at+86400)
BEGIN SELECT RAISE(ABORT,'idempotency expiry is outside its scope retention period'); END;
CREATE TRIGGER idempotency_expiry_update_guard BEFORE UPDATE OF created_at,expires_at ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR
     (NEW.scope='reward_redemption' AND NEW.expires_at-NEW.created_at NOT BETWEEN 86400 AND 316224000) OR
     (NEW.scope<>'reward_redemption' AND NEW.expires_at<>NEW.created_at+86400)
BEGIN SELECT RAISE(ABORT,'idempotency expiry is outside its scope retention period'); END;
CREATE TRIGGER idempotency_identity_update_guard BEFORE UPDATE ON idempotency_records
WHEN NEW.scope IS NOT OLD.scope OR NEW.actor_scope_hash IS NOT OLD.actor_scope_hash OR
     NEW.key_hash IS NOT OLD.key_hash OR NEW.request_hash IS NOT OLD.request_hash OR
     NEW.lookup_fingerprint IS NOT OLD.lookup_fingerprint OR NEW.created_at IS NOT OLD.created_at OR
     NEW.expires_at IS NOT OLD.expires_at OR
     (OLD.state='accepted' AND NOT (
        (NEW.state='accepted' AND NEW.http_status IS OLD.http_status AND NEW.response_body IS OLD.response_body) OR
        (NEW.state='completed' AND NEW.http_status BETWEEN 100 AND 599 AND typeof(NEW.response_body)='blob' AND length(NEW.response_body)<=65536)
     )) OR
     (OLD.state='completed' AND (NEW.state IS NOT OLD.state OR NEW.http_status IS NOT OLD.http_status OR NEW.response_body IS NOT OLD.response_body))
BEGIN SELECT RAISE(ABORT,'idempotency identity or terminal state is immutable'); END;
CREATE TRIGGER generation_two_integer_type_idempotency_records_insert_guard BEFORE INSERT ON idempotency_records
WHEN (NEW.http_status IS NOT NULL AND typeof(NEW.http_status)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_idempotency_records_update_guard BEFORE UPDATE ON idempotency_records
WHEN (NEW.http_status IS NOT NULL AND typeof(NEW.http_status)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE INDEX idx_idempotency_recovery ON idempotency_records(state,expires_at,scope,actor_scope_hash,key_hash);
CREATE INDEX idx_reward_redemption_attempts ON idempotency_records(actor_scope_hash,created_at) WHERE scope='reward_redemption';
UPDATE sqlite_sequence SET seq=(SELECT p.seq FROM previous_sequences p WHERE p.name=sqlite_sequence.name) WHERE EXISTS(SELECT 1 FROM previous_sequences p WHERE p.name=sqlite_sequence.name AND p.seq>sqlite_sequence.seq);
INSERT INTO sqlite_sequence(name,seq) SELECT p.name,p.seq FROM previous_sequences p WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence s WHERE s.name=p.name);
DROP TABLE previous_sequences;
CREATE INDEX idx_gacha_config_events_pool ON gacha_config_events(target,seq,created_at) WHERE substr(target,1,3)='gp_';
INSERT INTO site_config(key,value,updated_at) VALUES('redemption_ban_threshold','5',0),('redemption_ban_window_seconds','86400',0),('redemption_ban_duration_seconds','86400',0) ON CONFLICT(key) DO NOTHING;
UPDATE schema_state SET version=17 WHERE id=1 AND version=16;
