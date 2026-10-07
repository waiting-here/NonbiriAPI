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

INSERT INTO site_config(key,value,updated_at) VALUES('global_rpm_per_user','60',0);

-- Extend card-game storage while preserving existing rows.
DROP TRIGGER activity_account_integer_insert;
DROP TRIGGER activity_account_integer_update;
DROP TRIGGER activity_loan_operation_insert;
DROP TRIGGER blackjack_payment_insert;
DROP TRIGGER credit_account_asset_insert;
DROP TRIGGER credit_account_code_guard;
DROP TRIGGER credit_account_code_update_guard;
DROP TRIGGER credit_account_identity_update;
DROP TRIGGER credit_entries_account_kind_guard;
DROP TRIGGER credit_entries_account_kind_update_guard;
DROP TRIGGER credit_entries_no_delete;
DROP TRIGGER credit_entries_no_update;
DROP TRIGGER credit_operation_source_guard;
DROP TRIGGER credit_operation_source_update_guard;
DROP TRIGGER credit_operations_no_delete;
DROP TRIGGER credit_operations_no_update;
DROP TRIGGER game_checkin_operation_insert;
DROP TRIGGER game_checkin_operation_update;
DROP TRIGGER game_duel_anonymous_immutable;
DROP TRIGGER game_duel_catalog_immutable;
DROP TRIGGER game_duel_history_terminal;
DROP TRIGGER game_duel_queue_accounts;
DROP TRIGGER game_duel_queue_delete_guard;
DROP TRIGGER game_duel_queue_frozen;
DROP TRIGGER game_duel_seat_deidentify;
DROP TRIGGER game_duel_seat_payment_insert;
DROP TRIGGER game_duel_session_accounts;
DROP TRIGGER game_duel_session_delete_guard;
DROP TRIGGER game_duel_session_frozen;
DROP TRIGGER game_duel_slot_insert;
DROP TRIGGER game_duel_slot_update;
DROP TRIGGER game_duel_terminal_immutable;
DROP TRIGGER game_duel_user_ban_guard;
DROP TRIGGER game_duel_user_delete_guard;
DROP TRIGGER game_random_proof_identity_guard;
DROP TRIGGER generation_two_credit_accounts_sm128_guard;
DROP TRIGGER generation_two_credit_accounts_sm128_update_guard;
DROP TRIGGER generation_two_credit_accounts_time_guard;
DROP TRIGGER generation_two_credit_accounts_time_update_guard;
DROP TRIGGER generation_two_credit_operations_u128_guard;
DROP TRIGGER generation_two_credit_operations_u128_update_guard;
DROP TRIGGER generation_two_integer_type_credit_accounts_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_accounts_update_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_update_guard;
DROP TRIGGER generation_two_integer_type_idempotency_records_insert_guard;
DROP TRIGGER generation_two_integer_type_idempotency_records_update_guard;
DROP TRIGGER idempotency_expiry_guard;
DROP TRIGGER idempotency_expiry_update_guard;
DROP TRIGGER idempotency_identity_update_guard;
DROP TRIGGER onboarding_completion_operation_insert;
DROP TRIGGER onboarding_hold_parent_insert;
DROP TRIGGER onboarding_hold_parent_update;
DROP TRIGGER rps_queue_account_guard;
DROP TRIGGER rps_queue_account_update_guard;
DROP TRIGGER rps_session_account_guard;
DROP TRIGGER rps_session_account_update_guard;
DROP TRIGGER shared_pool_account_guard;
DROP TRIGGER shared_pool_account_update_guard;
DROP TRIGGER welfare_claim_matrix_guard;
DROP TRIGGER welfare_claim_matrix_update_guard;
CREATE TEMP TABLE card_migration_sequence AS SELECT name,seq FROM sqlite_sequence WHERE name IN ('credit_accounts','credit_operations','economy_audit_buckets','game_duel_anonymous','game_duel_catalogs','game_duel_queue','game_duel_sessions','game_duel_user_slots','game_random_proofs','game_rank_events','idempotency_records','lake_notes_casts','lake_notes_exchange_receipts','self_deletion_duel_aborts');
CREATE TEMP TABLE card_migration_credit_accounts AS SELECT * FROM credit_accounts;
DROP TABLE credit_accounts;
CREATE TABLE credit_accounts (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), kind TEXT NOT NULL CHECK(kind IN ('user','pool','platform','external')), user_id INTEGER REFERENCES users(id) ON DELETE CASCADE, code TEXT, balance_sign INTEGER NOT NULL CHECK(balance_sign IN (-1,0,1)), balance_mag BLOB NOT NULL CHECK(typeof(balance_mag)='blob' AND length(balance_mag)=16), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')), CHECK((balance_sign=0 AND hex(balance_mag)='00000000000000000000000000000000') OR (balance_sign<>0 AND hex(balance_mag)<>'00000000000000000000000000000000')), CHECK((kind='user' AND user_id IS NOT NULL AND code IS NULL) OR (kind<>'user' AND user_id IS NULL AND code IS NOT NULL AND length(code) BETWEEN 1 AND 64)), CHECK(kind IN ('user','external') OR balance_sign IN (0,1)), CHECK((kind='user' AND code IS NULL) OR (kind='external' AND code='external') OR (kind='pool' AND length(code)=31 AND substr(code,1,5)='pool:' AND substr(code,6,4)='pol_' AND substr(code,10) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (kind='platform' AND (code IN ('platform','forward_reserve','charity_reserve','game_fishing_reserve','image_activity_reserve') OR (length(code)=44 AND substr(code,1,18)='blackjack-payment:' AND substr(code,19,4)='bjp_' AND substr(code,23) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=38 AND substr(code,1,11)='duel-queue:' AND substr(code,12,5) IN ('bidq_','likq_','gwtq_') AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=39 AND substr(code,1,13)='duel-session:' AND substr(code,14,4) IN ('bid_','lik_','gwt_') AND substr(code,18) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=37 AND substr(code,1,10)='rps-queue:' AND substr(code,11,5)='rpsq_' AND substr(code,16) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=38 AND substr(code,1,12)='rps-session:' AND substr(code,13,4)='rps_' AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')))))
);
INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at,asset_type) SELECT id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at,asset_type FROM card_migration_credit_accounts;
DROP TABLE card_migration_credit_accounts;
CREATE UNIQUE INDEX idx_credit_accounts_user ON credit_accounts(user_id,asset_type) WHERE kind='user';
CREATE UNIQUE INDEX idx_credit_accounts_code ON credit_accounts(code,asset_type) WHERE code IS NOT NULL;
CREATE TEMP TABLE card_migration_credit_operations AS SELECT * FROM credit_operations;
DROP TABLE credit_operations;
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal','ai_ticket','ai_terminal','catch_ticket','catch_refund','catch_reward','blackjack_reserve','blackjack_settle','blackjack_release')), source_type TEXT NOT NULL CHECK(source_type IN ('image_task','operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session','duel_queue','duel_session','catch_session','blackjack_payment')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (donation_credit_user_id IS NOT NULL AND kind IN ('admin_user_adjustment','donor_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='catch_session' AND length(source_id)=25 AND substr(source_id,1,3)='sc_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_','gwt_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('catch_ticket','catch_refund','catch_reward') AND source_type='catch_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal','ai_ticket','ai_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted) SELECT id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted FROM card_migration_credit_operations;
DROP TABLE card_migration_credit_operations;
CREATE INDEX idx_credit_operations_source ON credit_operations(source_type,source_id,source_seq);
CREATE INDEX idx_credit_operations_created ON credit_operations(created_at,ledger_seq);
CREATE INDEX idx_credit_duel_terminal ON credit_operations(substr(source_id,1,4),ledger_seq) WHERE kind='duel_terminal' AND source_type='duel_session';
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
CREATE TEMP TABLE card_migration_economy_audit_buckets AS SELECT * FROM economy_audit_buckets;
DROP TABLE economy_audit_buckets;
CREATE TABLE economy_audit_buckets (
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'),
 source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 32 AND source_type NOT GLOB '*[^a-z0-9_]*'),
 channel TEXT NOT NULL CHECK(channel IN ('admin','account','checkin','welfare','thursday','api','charity','donation','fishing','linklink','rps','bidding','likes','gwent','steadycatch','blackjack','onboarding','loan','picture_book','inactivity','penalty','fat_fish','lake_notes','unclassified')),
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
INSERT INTO economy_audit_buckets(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq) SELECT asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq FROM card_migration_economy_audit_buckets;
DROP TABLE card_migration_economy_audit_buckets;
CREATE INDEX idx_economy_audit_buckets_time ON economy_audit_buckets(asset_type,bucket,bucket_start,kind,source_type,channel);
CREATE TEMP TABLE card_migration_game_duel_anonymous AS SELECT * FROM game_duel_anonymous;
DROP TABLE game_duel_anonymous;
CREATE TABLE game_duel_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='dah_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key='bidding' AND mode='ai')),
 content_hash TEXT NOT NULL,
 header_json TEXT NOT NULL CHECK(typeof(header_json)='text' AND length(CAST(header_json AS BLOB))<=1048576 AND json_valid(header_json)),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
INSERT INTO game_duel_anonymous(export_seq,archive_id,game_key,economy,mode,content_hash,header_json) SELECT export_seq,archive_id,game_key,economy,mode,content_hash,header_json FROM card_migration_game_duel_anonymous;
DROP TABLE card_migration_game_duel_anonymous;
CREATE INDEX idx_duel_anonymous_export ON game_duel_anonymous(game_key,mode,export_seq);
CREATE INDEX idx_duel_anonymous_archive ON game_duel_anonymous(game_key,archive_id);
CREATE TEMP TABLE card_migration_game_duel_catalogs AS SELECT * FROM game_duel_catalogs;
DROP TABLE game_duel_catalogs;
CREATE TABLE game_duel_catalogs (
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64 AND content_hash NOT GLOB '*[^0-9a-f]*'),
 rules_version INTEGER NOT NULL CHECK(rules_version=1),
 design_version TEXT NOT NULL CHECK(length(design_version) BETWEEN 1 AND 32),
 schema_version INTEGER NOT NULL CHECK(schema_version BETWEEN 1 AND 32),
 catalog_json TEXT NOT NULL CHECK(typeof(catalog_json)='text' AND length(CAST(catalog_json AS BLOB))<=1048576 AND json_valid(catalog_json)),
 PRIMARY KEY(game_key,content_hash)
) STRICT;
INSERT INTO game_duel_catalogs(game_key,content_hash,rules_version,design_version,schema_version,catalog_json) SELECT game_key,content_hash,rules_version,design_version,schema_version,catalog_json FROM card_migration_game_duel_catalogs;
DROP TABLE card_migration_game_duel_catalogs;
CREATE TEMP TABLE card_migration_game_duel_queue AS SELECT * FROM game_duel_queue;
DROP TABLE game_duel_queue;
CREATE TABLE game_duel_queue (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,5)='bidq_') OR (game_key='likes' AND substr(id,1,5)='likq_') OR (game_key='gwent' AND substr(id,1,5)='gwtq_')),
 mode TEXT NOT NULL CHECK((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16 AND hex(revision)<>'00000000000000000000000000000000'),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300679),
 deadline INTEGER NOT NULL CHECK(deadline=created_at+120),
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 content_hash TEXT NOT NULL,
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 1 AND 9000000000000000),
 game_paid_milli INTEGER NOT NULL CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND ticket_milli),
 reservation_operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(game_account_id<>general_account_id),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16 AND hex(ledger_rows_remaining) IN ('00000000000000000000000000000000','00000000000000000000000000000001')),
 device_hash BLOB NOT NULL CHECK(typeof(device_hash)='blob' AND length(device_hash)=32),
 ip_hash BLOB NOT NULL CHECK(typeof(ip_hash)='blob' AND length(ip_hash)=32),
 loadout_json TEXT CHECK((game_key='bidding' AND loadout_json IS NULL) OR (game_key IN ('likes','gwent') AND typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
INSERT INTO game_duel_queue(id,game_key,mode,user_id,revision,created_at,deadline,terms_json,terms_hash,content_hash,ticket_milli,game_paid_milli,reservation_operation_id,general_account_id,game_account_id,ledger_rows_remaining,device_hash,ip_hash,loadout_json) SELECT id,game_key,mode,user_id,revision,created_at,deadline,terms_json,terms_hash,content_hash,ticket_milli,game_paid_milli,reservation_operation_id,general_account_id,game_account_id,ledger_rows_remaining,device_hash,ip_hash,loadout_json FROM card_migration_game_duel_queue;
DROP TABLE card_migration_game_duel_queue;
CREATE INDEX idx_duel_queue_match ON game_duel_queue(game_key,mode,terms_hash,created_at,id);
CREATE INDEX idx_duel_queue_deadline ON game_duel_queue(game_key,deadline,id);
CREATE UNIQUE INDEX idx_duel_queue_user ON game_duel_queue(user_id,game_key);
CREATE TEMP TABLE card_migration_game_duel_sessions AS SELECT * FROM game_duel_sessions;
DROP TABLE game_duel_sessions;
CREATE TABLE game_duel_sessions (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4) IN ('bid_','lik_','gwt_') AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,4)='bid_') OR (game_key='likes' AND substr(id,1,4)='lik_') OR (game_key='gwent' AND substr(id,1,4)='gwt_')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key='bidding' AND mode='ai')),
 content_hash TEXT NOT NULL,
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 0 AND 9000000000000000 AND (economy='ai_challenge' OR ticket_milli>0)),
 platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999),
 welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999),
 thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000),
 state TEXT NOT NULL CHECK(state IN ('active','terminal')),
 phase TEXT NOT NULL CHECK(phase IN ('joker','bid','plan','settlement','mulligan','turn','choice','terminal')),
 round INTEGER NOT NULL CHECK(round BETWEEN 1 AND 75 AND (game_key<>'bidding' OR round<=13) AND (mode<>'quick' OR round<=25)),
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16 AND hex(revision)<>'00000000000000000000000000000000'),
 phase_seq BLOB NOT NULL CHECK(typeof(phase_seq)='blob' AND length(phase_seq)=16 AND hex(phase_seq)<>'00000000000000000000000000000000'),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253399708799),
 phase_deadline INTEGER CHECK(phase_deadline IS NULL OR phase_deadline BETWEEN started_at AND 253399708799),
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(game_account_id<>general_account_id),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 server_state_json TEXT NOT NULL CHECK(typeof(server_state_json)='text' AND length(CAST(server_state_json AS BLOB))<=1048576 AND json_valid(server_state_json)),
 initial_state_json TEXT NOT NULL CHECK(typeof(initial_state_json)='text' AND length(CAST(initial_state_json AS BLOB))<=1048576 AND json_valid(initial_state_json)),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN started_at AND 253399708799),
 delete_at INTEGER CHECK(delete_at IS NULL OR delete_at=terminal_at+2592000),
 outcome TEXT CHECK(outcome IS NULL OR outcome IN ('decided','draw','system_cancelled')),
 reason TEXT CHECK(reason IS NULL OR reason IN ('rounds','target','double-overload','limit','surrender','server_restart','account_unavailable','afk')),
 winner_seat INTEGER CHECK(winner_seat IS NULL OR winner_seat IN (0,1)),
 score0 INTEGER CHECK(score0 IS NULL OR score0 BETWEEN 0 AND 1000000000),
 score1 INTEGER CHECK(score1 IS NULL OR score1 BETWEEN 0 AND 1000000000),
 prize_milli INTEGER CHECK(prize_milli IS NULL OR prize_milli BETWEEN 0 AND ticket_milli),
 platform_milli INTEGER CHECK(platform_milli IS NULL OR platform_milli BETWEEN 0 AND ticket_milli),
 welfare_milli INTEGER CHECK(welfare_milli IS NULL OR welfare_milli BETWEEN 0 AND ticket_milli),
 thursday_milli INTEGER CHECK(thursday_milli IS NULL OR thursday_milli BETWEEN 0 AND ticket_milli),
 terminal_operation_id TEXT UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT,
 CHECK((state='active' AND ((game_key='bidding' AND phase IN ('joker','bid')) OR (game_key='likes' AND phase IN ('plan','settlement')) OR (game_key='gwent' AND phase IN ('mulligan','turn','choice'))) AND phase_deadline IS NOT NULL AND (hex(ledger_rows_remaining)='00000000000000000000000000000001' OR (economy='ai_challenge' AND hex(ledger_rows_remaining)='00000000000000000000000000000000')) AND terminal_at IS NULL AND delete_at IS NULL AND outcome IS NULL AND reason IS NULL AND winner_seat IS NULL AND score0 IS NULL AND score1 IS NULL AND prize_milli IS NULL AND platform_milli IS NULL AND welfare_milli IS NULL AND thursday_milli IS NULL AND terminal_operation_id IS NULL) OR (state='terminal' AND phase='terminal' AND phase_deadline IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000' AND terminal_at IS NOT NULL AND delete_at IS NOT NULL AND outcome IS NOT NULL AND reason IS NOT NULL AND score0 IS NOT NULL AND score1 IS NOT NULL AND prize_milli IS NOT NULL AND platform_milli IS NOT NULL AND welfare_milli IS NOT NULL AND thursday_milli IS NOT NULL AND (terminal_operation_id IS NOT NULL OR (economy='ai_challenge' AND ticket_milli=0)))),
 CHECK(state='active' OR (economy='pvp' AND ((outcome='decided' AND winner_seat IS NOT NULL AND reason NOT IN ('server_restart','account_unavailable') AND prize_milli+platform_milli+welfare_milli+thursday_milli=ticket_milli) OR (outcome IN ('draw','system_cancelled') AND winner_seat IS NULL AND prize_milli=0 AND platform_milli=0 AND welfare_milli=0 AND thursday_milli=0))) OR (economy='ai_challenge' AND prize_milli=0 AND welfare_milli=0 AND thursday_milli=0 AND ((outcome IN ('decided','draw') AND platform_milli=ticket_milli AND (outcome='decided')=(winner_seat IS NOT NULL)) OR (outcome='system_cancelled' AND winner_seat IS NULL AND platform_milli=0)))),
 CHECK(economy='pvp' OR (platform_bp=0 AND welfare_bp=0 AND thursday_bp=0)),
 CHECK(state='active' OR (outcome='system_cancelled' AND reason IN ('server_restart','account_unavailable')) OR (outcome<>'system_cancelled' AND reason NOT IN ('server_restart','account_unavailable')))
) STRICT;
INSERT INTO game_duel_sessions(id,game_key,economy,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id) SELECT id,game_key,economy,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id FROM card_migration_game_duel_sessions;
DROP TABLE card_migration_game_duel_sessions;
CREATE INDEX idx_duel_sessions_due ON game_duel_sessions(game_key,state,phase_deadline,id);
CREATE INDEX idx_duel_sessions_terminal ON game_duel_sessions(game_key,state,terminal_at,id);
CREATE INDEX idx_duel_sessions_expiry ON game_duel_sessions(game_key,state,delete_at,id);
CREATE TEMP TABLE card_migration_game_duel_user_slots AS SELECT * FROM game_duel_user_slots;
DROP TABLE game_duel_user_slots;
CREATE TABLE game_duel_user_slots (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 queue_id TEXT UNIQUE REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 ai_queue_id TEXT UNIQUE REFERENCES game_ai_queue(id) ON DELETE RESTRICT,
 session_id TEXT REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 PRIMARY KEY(user_id,game_key),
 CHECK((queue_id IS NOT NULL)+(ai_queue_id IS NOT NULL)+(session_id IS NOT NULL)=1)
) STRICT;
INSERT INTO game_duel_user_slots(user_id,game_key,queue_id,ai_queue_id,session_id) SELECT user_id,game_key,queue_id,ai_queue_id,session_id FROM card_migration_game_duel_user_slots;
DROP TABLE card_migration_game_duel_user_slots;
CREATE TEMP TABLE card_migration_game_random_proofs AS SELECT * FROM game_random_proofs;
DROP TABLE game_random_proofs;
CREATE TABLE game_random_proofs (
 resource_id TEXT PRIMARY KEY NOT NULL,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','gwent','blackjack')),
 fishing_id TEXT UNIQUE REFERENCES game_fishing_batches(id) ON DELETE CASCADE,
 linklink_id TEXT UNIQUE REFERENCES game_linklink_sessions(id) ON DELETE CASCADE,
 linklink_summary_id TEXT UNIQUE REFERENCES game_linklink_summaries(session_id) ON DELETE CASCADE,
 rps_id TEXT UNIQUE REFERENCES game_rps_sessions(id) ON DELETE CASCADE,
 rps_summary_id TEXT UNIQUE REFERENCES game_rps_summaries(session_id) ON DELETE CASCADE,
 duel_id TEXT UNIQUE REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 blackjack_id TEXT UNIQUE REFERENCES game_blackjack_sessions(id) ON DELETE CASCADE,
 private_json TEXT NOT NULL CHECK(typeof(private_json)='text' AND length(CAST(private_json AS BLOB)) BETWEEN 1 AND 2097152 AND json_valid(private_json)),
 CHECK(
  (game_key='fishing' AND fishing_id IS resource_id AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='linklink' AND ((linklink_id IS resource_id AND linklink_summary_id IS NULL) OR (linklink_summary_id IS resource_id AND linklink_id IS NULL)) AND fishing_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='rps' AND ((rps_id IS resource_id AND rps_summary_id IS NULL) OR (rps_summary_id IS resource_id AND rps_id IS NULL)) AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key IN ('bidding','likes','gwent') AND duel_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='blackjack' AND blackjack_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL)
 ),
 CHECK(json_extract(private_json,'$.resource_id') IS resource_id AND json_extract(private_json,'$.game') IS game_key AND json_extract(private_json,'$.algorithm') IS 'hmac-sha256-reject64-v1'),
 CHECK(json_type(private_json,'$.seed') IS 'text' AND length(json_extract(private_json,'$.seed'))=64 AND json_extract(private_json,'$.seed') NOT GLOB '*[^0-9a-f]*'),
 CHECK(json_type(private_json,'$.commitment') IS 'text' AND length(json_extract(private_json,'$.commitment'))=64 AND json_extract(private_json,'$.commitment') NOT GLOB '*[^0-9a-f]*')
) STRICT;
INSERT INTO game_random_proofs(resource_id,game_key,fishing_id,linklink_id,linklink_summary_id,rps_id,rps_summary_id,duel_id,blackjack_id,private_json) SELECT resource_id,game_key,fishing_id,linklink_id,linklink_summary_id,rps_id,rps_summary_id,duel_id,blackjack_id,private_json FROM card_migration_game_random_proofs;
DROP TABLE card_migration_game_random_proofs;
CREATE TEMP TABLE card_migration_game_rank_events AS SELECT * FROM game_rank_events;
DROP TABLE game_rank_events;
CREATE TABLE game_rank_events (
 seq BLOB NOT NULL PRIMARY KEY CHECK(length(seq)=16 AND seq>X'00000000000000000000000000000000'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','gwent','steadycatch','blackjack')),
 source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128 AND source_id NOT GLOB '*[^A-Za-z0-9_:-]*'),
 settled_at INTEGER NOT NULL CHECK(settled_at BETWEEN 0 AND 253399708799),
 loss_sign INTEGER CHECK(loss_sign IS NULL OR loss_sign IN (-1,0,1)),
 loss_mag BLOB CHECK(loss_mag IS NULL OR (length(loss_mag)=16 AND loss_mag<X'80000000000000000000000000000000')),
 positive_profit BLOB NOT NULL CHECK(length(positive_profit)=16),
 charity_expires_at INTEGER CHECK(charity_expires_at IS NULL OR charity_expires_at=settled_at+604800),
 profit_7d_expires_at INTEGER CHECK(profit_7d_expires_at IS NULL OR profit_7d_expires_at=settled_at+604800),
 profit_30d_expires_at INTEGER CHECK(profit_30d_expires_at IS NULL OR profit_30d_expires_at=settled_at+2592000),
 UNIQUE(user_id,game_key,source_id),
 CHECK((loss_sign IS NULL AND loss_mag IS NULL AND charity_expires_at IS NULL) OR
  (loss_sign IS NOT NULL AND loss_mag IS NOT NULL AND charity_expires_at IS NOT NULL AND ((loss_sign=0 AND loss_mag=X'00000000000000000000000000000000') OR (loss_sign<>0 AND loss_mag>X'00000000000000000000000000000000')))),
 CHECK(game_key IN ('bidding','blackjack') OR (positive_profit=X'00000000000000000000000000000000' AND profit_7d_expires_at IS NULL AND profit_30d_expires_at IS NULL))
) STRICT;
INSERT INTO game_rank_events(seq,user_id,game_key,source_id,settled_at,loss_sign,loss_mag,positive_profit,charity_expires_at,profit_7d_expires_at,profit_30d_expires_at) SELECT seq,user_id,game_key,source_id,settled_at,loss_sign,loss_mag,positive_profit,charity_expires_at,profit_7d_expires_at,profit_30d_expires_at FROM card_migration_game_rank_events;
DROP TABLE card_migration_game_rank_events;
CREATE INDEX idx_rank_events_charity_expiry ON game_rank_events(charity_expires_at,user_id,seq) WHERE charity_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit7_expiry ON game_rank_events(profit_7d_expires_at,user_id,game_key,seq) WHERE profit_7d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit30_expiry ON game_rank_events(profit_30d_expires_at,user_id,game_key,seq) WHERE profit_30d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_user ON game_rank_events(user_id,settled_at,seq);
CREATE TEMP TABLE card_migration_idempotency_records AS SELECT * FROM idempotency_records;
DROP TABLE idempotency_records;
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL CHECK(scope IN ('credential_report','control_mutation','openai_chat_completions','charity_chat_completions','model_discovery','maintenance','announcement','activity','game_fishing','game_linklink','game_rps','game_bidding','game_likes','game_gwent','game_catch','game_blackjack','activity_loan','donation','lake_notes','personal_automation')), actor_scope_hash BLOB NOT NULL CHECK(typeof(actor_scope_hash)='blob' AND length(actor_scope_hash)=32), key_hash BLOB NOT NULL CHECK(typeof(key_hash)='blob' AND length(key_hash)=32), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), lookup_fingerprint BLOB CHECK(lookup_fingerprint IS NULL OR (typeof(lookup_fingerprint)='blob' AND length(lookup_fingerprint)=32)), state TEXT NOT NULL CHECK(state IN ('accepted','completed')), http_status INTEGER NOT NULL CHECK(http_status=0 OR http_status BETWEEN 100 AND 599), response_body BLOB NOT NULL CHECK(typeof(response_body)='blob' AND length(response_body)<=65536), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), PRIMARY KEY(scope,actor_scope_hash,key_hash), CHECK(expires_at>=created_at), CHECK((scope='credential_report' AND lookup_fingerprint IS NOT NULL AND expires_at=created_at+86400) OR (scope<>'credential_report' AND lookup_fingerprint IS NULL)), CHECK((state='accepted' AND http_status=0 AND length(response_body)=0) OR (state='completed' AND http_status BETWEEN 100 AND 599))
);
INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,response_body,created_at,expires_at) SELECT scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,response_body,created_at,expires_at FROM card_migration_idempotency_records;
DROP TABLE card_migration_idempotency_records;
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);
CREATE INDEX idx_idempotency_recovery ON idempotency_records(state,expires_at,scope,actor_scope_hash,key_hash);
CREATE TEMP TABLE card_migration_lake_notes_casts AS SELECT * FROM lake_notes_casts;
DROP TABLE lake_notes_casts;
CREATE TABLE lake_notes_casts (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lnc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source_period_id TEXT REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 rules_id TEXT NOT NULL CHECK(length(CAST(rules_id AS BLOB)) BETWEEN 1 AND 128),
 phase TEXT NOT NULL CHECK(phase IN ('waiting','playing','success','failed')),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 generation INTEGER NOT NULL CHECK(generation>=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 last_tick INTEGER NOT NULL CHECK(last_tick BETWEEN 0 AND 9007199254740991),
 snapshot BLOB NOT NULL CHECK(length(snapshot) BETWEEN 1 AND 65536),
 reward_plan BLOB NOT NULL CHECK(length(reward_plan) BETWEEN 1 AND 4096),
 held INTEGER NOT NULL CHECK(held IN (0,1)),
 active_elapsed_ns INTEGER NOT NULL CHECK(active_elapsed_ns>=0),
 active_started_at_ns INTEGER CHECK(active_started_at_ns>=0),
 lease_until_ns INTEGER CHECK(lease_until_ns>=active_started_at_ns),
 last_ack_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(last_ack_json) AND json_type(last_ack_json)='object' AND length(CAST(last_ack_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at BETWEEN created_at AND 253402300799), storage_version INTEGER NOT NULL DEFAULT 1 CHECK(storage_version>=1),
 CHECK((paused=1 AND held=0 AND active_started_at_ns IS NULL AND lease_until_ns IS NULL) OR (paused=0 AND active_started_at_ns IS NOT NULL AND lease_until_ns IS NOT NULL)),
 CHECK((phase IN ('waiting','playing') AND terminal_at IS NULL) OR (phase IN ('success','failed') AND terminal_at IS NOT NULL AND paused=1))
) STRICT, WITHOUT ROWID;
INSERT INTO lake_notes_casts(id,user_id,source_period_id,rules_id,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,active_started_at_ns,lease_until_ns,last_ack_json,created_at,updated_at,terminal_at,storage_version) SELECT id,user_id,source_period_id,rules_id,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,active_started_at_ns,lease_until_ns,last_ack_json,created_at,updated_at,terminal_at,storage_version FROM card_migration_lake_notes_casts;
DROP TABLE card_migration_lake_notes_casts;
CREATE UNIQUE INDEX idx_lake_notes_active_cast_user ON lake_notes_casts(user_id) WHERE phase IN ('waiting','playing');
CREATE INDEX idx_lake_notes_cast_retention ON lake_notes_casts(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE TEMP TABLE card_migration_lake_notes_exchange_receipts AS SELECT * FROM lake_notes_exchange_receipts;
DROP TABLE lake_notes_exchange_receipts;
CREATE TABLE lake_notes_exchange_receipts (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lne_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 period_revision INTEGER CHECK(period_revision>=1),
 config_revision INTEGER CHECK(config_revision>=1),
 direction TEXT NOT NULL CHECK(direction IN ('general_to_coin','coin_to_general','game_to_coin','coin_to_game')),
 quantity_mag BLOB NOT NULL CHECK(length(quantity_mag)=16 AND quantity_mag<>zeroblob(16)),
 source_lot BLOB NOT NULL CHECK(length(source_lot)=16 AND source_lot<>zeroblob(16)),
 target_lot BLOB NOT NULL CHECK(length(target_lot)=16 AND target_lot<>zeroblob(16)),
 source_amount_mag BLOB NOT NULL CHECK(length(source_amount_mag)=16),
 target_amount_mag BLOB NOT NULL CHECK(length(target_amount_mag)=16),
 operation_key_hash BLOB NOT NULL CHECK(length(operation_key_hash)=32),
 ledger_operation_id TEXT NOT NULL REFERENCES credit_operations(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 CHECK((period_id IS NOT NULL AND period_revision IS NOT NULL AND config_revision IS NULL) OR (period_id IS NULL AND period_revision IS NULL AND config_revision IS NOT NULL)),
 UNIQUE(user_id,operation_key_hash)
) STRICT, WITHOUT ROWID;
INSERT INTO lake_notes_exchange_receipts(id,user_id,period_id,period_revision,direction,quantity_mag,source_lot,target_lot,source_amount_mag,target_amount_mag,operation_key_hash,ledger_operation_id,created_at) SELECT id,user_id,period_id,period_revision,direction,quantity_mag,source_lot,target_lot,source_amount_mag,target_amount_mag,operation_key_hash,ledger_operation_id,created_at FROM card_migration_lake_notes_exchange_receipts;
DROP TABLE card_migration_lake_notes_exchange_receipts;
CREATE INDEX idx_lake_exchange_operation ON lake_notes_exchange_receipts(ledger_operation_id);
CREATE TEMP TABLE card_migration_self_deletion_duel_aborts AS SELECT * FROM self_deletion_duel_aborts;
DROP TABLE self_deletion_duel_aborts;
CREATE TABLE self_deletion_duel_aborts (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL CHECK(length(CAST(discord_id AS BLOB)) BETWEEN 1 AND 128),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 match_id TEXT NOT NULL CHECK(length(CAST(match_id AS BLOB)) BETWEEN 1 AND 128),
 former_user_id INTEGER NOT NULL CHECK(former_user_id>0),
 reason TEXT NOT NULL CHECK(reason='self_deletion_cancelled_match'),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253394524799),
 expires_at INTEGER NOT NULL CHECK(expires_at=occurred_at+7776000),
 UNIQUE(game_key,match_id,former_user_id)
) STRICT;
INSERT INTO self_deletion_duel_aborts(id,discord_id,game_key,match_id,former_user_id,reason,occurred_at,expires_at) SELECT id,discord_id,game_key,match_id,former_user_id,reason,occurred_at,expires_at FROM card_migration_self_deletion_duel_aborts;
DROP TABLE card_migration_self_deletion_duel_aborts;
CREATE INDEX idx_self_deletion_duel_aborts_discord ON self_deletion_duel_aborts(discord_id,occurred_at,id);
CREATE INDEX idx_self_deletion_duel_aborts_expiry ON self_deletion_duel_aborts(expires_at,id);
CREATE TABLE game_duel_ratings (
 game_key TEXT NOT NULL CHECK(game_key='gwent'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 rating INTEGER NOT NULL,
 played INTEGER NOT NULL CHECK(played>=1),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(game_key,user_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE game_duel_results (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 game_key TEXT NOT NULL CHECK(game_key='gwent'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 result INTEGER NOT NULL CHECK(result IN (0,1,2)),
 rating_before INTEGER NOT NULL,
 rating_after INTEGER NOT NULL,
 settled_at INTEGER NOT NULL,
 PRIMARY KEY(session_id,user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_duel_results_window ON game_duel_results(game_key,settled_at,user_id);
CREATE INDEX idx_duel_results_user ON game_duel_results(user_id,game_key,settled_at,session_id);
CREATE TABLE game_catch_sessions (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=25 AND substr(id,1,3)='sc_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('playing','paused','completed','failed','abandoned','cancelled')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 seed INTEGER NOT NULL CHECK(seed BETWEEN 0 AND 4294967295),
 engine_json TEXT NOT NULL CHECK(json_valid(engine_json) AND length(engine_json)<=32768),
 tick INTEGER NOT NULL CHECK(tick BETWEEN 0 AND 5400),
 anchor_tick INTEGER NOT NULL CHECK(anchor_tick BETWEEN 0 AND tick),
 anchor_ms INTEGER NOT NULL CHECK(anchor_ms BETWEEN 0 AND 253402300799000),
 price_milli INTEGER NOT NULL CHECK(price_milli BETWEEN 0 AND 9000000000000000),
 general_paid_milli INTEGER NOT NULL CHECK(general_paid_milli BETWEEN 0 AND price_milli),
 game_paid_milli INTEGER NOT NULL CHECK(game_paid_milli BETWEEN 0 AND price_milli),
 first_reward_milli INTEGER NOT NULL CHECK(first_reward_milli BETWEEN 0 AND 9000000000000000),
 first_clear INTEGER NOT NULL DEFAULT 0 CHECK(first_clear IN (0,1)),
 reward_milli INTEGER NOT NULL DEFAULT 0 CHECK(reward_milli BETWEEN 0 AND first_reward_milli),
 score INTEGER NOT NULL DEFAULT 0 CHECK(score>=0),
 entry_operation_id TEXT,
 terminal_operation_id TEXT,
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16 AND hex(ledger_rows_remaining) IN ('00000000000000000000000000000000','00000000000000000000000000000001')),
 last_request_hash BLOB CHECK(last_request_hash IS NULL OR (typeof(last_request_hash)='blob' AND length(last_request_hash)=32)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER NOT NULL CHECK(expires_at>created_at),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at),
 terminal_at INTEGER,
 CHECK(general_paid_milli+game_paid_milli=price_milli),
 CHECK((status IN ('playing','paused') AND terminal_at IS NULL AND first_clear=0 AND reward_milli=0) OR
       (status NOT IN ('playing','paused') AND terminal_at>=created_at AND hex(ledger_rows_remaining)='00000000000000000000000000000000')),
 CHECK(first_clear=0 OR status='completed'),
 CHECK(reward_milli=0 OR first_clear=1)
) STRICT;
CREATE UNIQUE INDEX idx_catch_active_user ON game_catch_sessions(user_id) WHERE status IN ('playing','paused');
CREATE INDEX idx_catch_user_history ON game_catch_sessions(user_id,created_at DESC,id);
CREATE INDEX idx_catch_expiry ON game_catch_sessions(expires_at,id) WHERE status IN ('playing','paused');
CREATE INDEX idx_catch_retention ON game_catch_sessions(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE INDEX idx_catch_score ON game_catch_sessions(terminal_at,user_id,score DESC) WHERE status IN ('completed','failed');
CREATE TABLE game_catch_inputs (
 session_id TEXT NOT NULL REFERENCES game_catch_sessions(id) ON DELETE CASCADE,
 until_tick INTEGER NOT NULL CHECK(until_tick BETWEEN 1 AND 5400),
 controls_json TEXT NOT NULL CHECK(json_valid(controls_json) AND length(controls_json)<=65536),
 PRIMARY KEY(session_id,until_tick)
);
DELETE FROM sqlite_sequence WHERE name IN ('credit_accounts','credit_operations','economy_audit_buckets','game_duel_anonymous','game_duel_catalogs','game_duel_queue','game_duel_sessions','game_duel_user_slots','game_random_proofs','game_rank_events','idempotency_records','lake_notes_casts','lake_notes_exchange_receipts','self_deletion_duel_aborts');
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM card_migration_sequence;
DROP TABLE card_migration_sequence;
CREATE TRIGGER activity_account_integer_insert BEFORE INSERT ON credit_accounts
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),32,1))-1)*1)%1000=0)
BEGIN SELECT RAISE(ABORT,'activity balance is not an integer'); END;
CREATE TRIGGER activity_account_integer_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),32,1))-1)*1)%1000=0)
BEGIN SELECT RAISE(ABORT,'activity balance is not an integer'); END;
CREATE TRIGGER activity_loan_operation_insert BEFORE INSERT ON activity_loans
WHEN NOT EXISTS(SELECT 1 FROM credit_operations o WHERE o.id=NEW.operation_id AND o.kind='activity_loan' AND o.ledger_seq=NEW.ledger_seq AND o.created_at=NEW.created_at)
 OR (SELECT count(*) FROM credit_entries WHERE operation_id=NEW.operation_id)<>4
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='general' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='game' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
BEGIN SELECT RAISE(ABORT,'loan operation mismatch'); END;
CREATE TRIGGER blackjack_payment_insert BEFORE INSERT ON game_blackjack_payments WHEN NOT EXISTS(SELECT 1 FROM game_blackjack_entries WHERE id=NEW.entry_id AND stake_milli=NEW.amount_milli AND state IN ('waiting','seated','playing')) OR (SELECT COUNT(*) FROM game_blackjack_payments WHERE entry_id=NEW.entry_id)>=4 OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='blackjack-payment:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='blackjack-payment:'||NEW.id) BEGIN SELECT RAISE(ABORT,'blackjack payment mismatch'); END;
CREATE TRIGGER credit_account_asset_insert BEFORE INSERT ON credit_accounts
WHEN (NEW.asset_type='game' AND (NEW.kind='pool' OR NEW.code IN ('forward_reserve','charity_reserve')))
 OR (NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT
  (NEW.kind='user' OR (NEW.kind='external' AND NEW.code='external') OR (NEW.kind='platform' AND NEW.code='image_activity_reserve')))
 OR (NEW.code='image_activity_reserve' AND NEW.asset_type NOT IN ('sketch_paper','sketch_brush'))
BEGIN SELECT RAISE(ABORT,'account code does not support this asset'); END;
CREATE TRIGGER credit_account_code_guard BEFORE INSERT ON credit_accounts
WHEN NEW.code IS NOT NULL AND NOT (typeof(NEW.code)='text' AND length(CAST(NEW.code AS BLOB)) BETWEEN 1 AND 64 AND NEW.code NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit account code is not canonical text'); END;
CREATE TRIGGER credit_account_code_update_guard BEFORE UPDATE OF code ON credit_accounts
WHEN NEW.code IS NOT NULL AND NOT (typeof(NEW.code)='text' AND length(CAST(NEW.code AS BLOB)) BETWEEN 1 AND 64 AND NEW.code NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit account code is not canonical text'); END;
CREATE TRIGGER credit_account_identity_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type IS NOT OLD.asset_type OR NEW.kind IS NOT OLD.kind OR NEW.code IS NOT OLD.code OR NEW.user_id IS NOT OLD.user_id
BEGIN SELECT RAISE(ABORT,'account identity is immutable'); END;
CREATE TRIGGER credit_entries_account_kind_guard BEFORE INSERT ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_entries_account_kind_update_guard BEFORE UPDATE OF account_id,account_kind_snapshot,asset_type ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_entries_no_delete BEFORE DELETE ON credit_entries
WHEN NOT EXISTS(SELECT 1 FROM credit_operations WHERE id=OLD.operation_id AND compacted=1
 AND ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1))
BEGIN SELECT RAISE(ABORT,'credit entry is not compacted'); END;
CREATE TRIGGER credit_entries_no_update BEFORE UPDATE ON credit_entries
WHEN NOT (
 NEW.operation_id IS OLD.operation_id AND NEW.line_no IS OLD.line_no AND NEW.account_kind_snapshot IS OLD.account_kind_snapshot AND
 NEW.delta_sign IS OLD.delta_sign AND NEW.delta_mag IS OLD.delta_mag AND NEW.balance_after_sign IS OLD.balance_after_sign AND
 NEW.balance_after_mag IS OLD.balance_after_mag AND NEW.asset_type IS OLD.asset_type AND
 (NEW.account_id IS OLD.account_id OR (OLD.account_id IS NOT NULL AND NEW.account_id IS NULL AND NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=OLD.account_id)))
)
BEGIN SELECT RAISE(ABORT,'credit_entries is append-only'); END;
CREATE TRIGGER credit_operation_source_guard BEFORE INSERT ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER credit_operation_source_update_guard BEFORE UPDATE OF source_type,source_id ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER credit_operations_no_delete BEFORE DELETE ON credit_operations
WHEN OLD.compacted<>1 OR OLD.ledger_seq>(SELECT through_seq FROM credit_compaction WHERE id=1)
BEGIN SELECT RAISE(ABORT,'credit operation is not compacted'); END;
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
CREATE TRIGGER game_checkin_operation_insert BEFORE INSERT ON game_checkins
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='checkin_award' AND o.created_at=NEW.created_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game'
  AND e.delta_sign=CASE WHEN NEW.award_milli=0 THEN 0 ELSE 1 END AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'game check-in operation mismatch'); END;
CREATE TRIGGER game_checkin_operation_update BEFORE UPDATE ON game_checkins
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='checkin_award' AND o.created_at=NEW.created_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game'
  AND e.delta_sign=CASE WHEN NEW.award_milli=0 THEN 0 ELSE 1 END AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'game check-in operation mismatch'); END;
CREATE TRIGGER game_duel_anonymous_immutable BEFORE UPDATE ON game_duel_anonymous BEGIN SELECT RAISE(ABORT,'duel archive immutable'); END;
CREATE TRIGGER game_duel_catalog_immutable BEFORE UPDATE ON game_duel_catalogs BEGIN SELECT RAISE(ABORT,'duel catalog immutable'); END;
CREATE TRIGGER game_duel_history_terminal AFTER UPDATE OF state ON game_duel_sessions
WHEN NEW.state='terminal' AND OLD.state='active' BEGIN
 INSERT INTO game_duel_history(game_key,session_id) VALUES(NEW.game_key,NEW.id);
END;
CREATE TRIGGER game_duel_queue_accounts BEFORE INSERT ON game_duel_queue WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-queue:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-queue:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel queue account mismatch'); END;
CREATE TRIGGER game_duel_queue_delete_guard BEFORE DELETE ON game_duel_queue WHEN hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel queue still reserves capacity'); END;
CREATE TRIGGER game_duel_queue_frozen BEFORE UPDATE ON game_duel_queue WHEN NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.user_id<>OLD.user_id OR NEW.revision<>OLD.revision OR NEW.created_at<>OLD.created_at OR NEW.deadline<>OLD.deadline OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.reservation_operation_id<>OLD.reservation_operation_id OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.device_hash<>OLD.device_hash OR NEW.ip_hash<>OLD.ip_hash OR NEW.loadout_json IS NOT OLD.loadout_json OR NEW.ledger_rows_remaining>OLD.ledger_rows_remaining BEGIN SELECT RAISE(ABORT,'duel queue terms immutable'); END;
CREATE TRIGGER game_duel_seat_deidentify BEFORE UPDATE OF user_id ON game_duel_seats WHEN NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND (SELECT state FROM game_duel_sessions WHERE id=NEW.session_id)<>'terminal' BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_seat_payment_insert BEFORE INSERT ON game_duel_seats WHEN (NEW.participant_kind='human' AND NEW.general_paid_milli+NEW.game_paid_milli<>(SELECT ticket_milli FROM game_duel_sessions WHERE id=NEW.session_id)) OR (NEW.participant_kind='bot' AND NOT EXISTS(SELECT 1 FROM game_duel_sessions WHERE id=NEW.session_id AND economy='ai_challenge')) BEGIN SELECT RAISE(ABORT,'duel payment mismatch'); END;
CREATE TRIGGER game_duel_session_accounts BEFORE INSERT ON game_duel_sessions WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-session:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-session:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel session account mismatch'); END;
CREATE TRIGGER game_duel_session_delete_guard BEFORE DELETE ON game_duel_sessions WHEN OLD.state<>'terminal' OR hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel session still active'); END;
CREATE TRIGGER game_duel_session_frozen BEFORE UPDATE ON game_duel_sessions WHEN NEW.economy<>OLD.economy OR NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR NEW.started_at<>OLD.started_at OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.initial_state_json<>OLD.initial_state_json OR NEW.phase_seq<OLD.phase_seq OR NEW.revision<=OLD.revision BEGIN SELECT RAISE(ABORT,'duel session terms immutable'); END;
CREATE TRIGGER game_duel_slot_insert BEFORE INSERT ON game_duel_user_slots WHEN (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_slot_update BEFORE UPDATE ON game_duel_user_slots WHEN NEW.user_id<>OLD.user_id OR NEW.game_key<>OLD.game_key OR (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_terminal_immutable BEFORE UPDATE ON game_duel_sessions WHEN OLD.state='terminal' BEGIN SELECT RAISE(ABORT,'duel result immutable'); END;
CREATE TRIGGER game_duel_user_ban_guard BEFORE UPDATE OF is_banned,banned_until ON users WHEN NEW.is_banned=1 AND (NEW.is_banned<>OLD.is_banned OR NEW.banned_until IS NOT OLD.banned_until) AND EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=NEW.id) BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_user_delete_guard BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=OLD.id) OR EXISTS(SELECT 1 FROM game_duel_seats WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'duel user handoff required'); END;
CREATE TRIGGER game_random_proof_identity_guard BEFORE UPDATE ON game_random_proofs
WHEN NEW.resource_id IS NOT OLD.resource_id OR NEW.game_key IS NOT OLD.game_key
 OR NEW.fishing_id IS NOT OLD.fishing_id OR NEW.duel_id IS NOT OLD.duel_id OR NEW.blackjack_id IS NOT OLD.blackjack_id
 OR ((NEW.linklink_id IS NOT OLD.linklink_id OR NEW.linklink_summary_id IS NOT OLD.linklink_summary_id) AND NOT (OLD.linklink_id IS OLD.resource_id AND OLD.linklink_summary_id IS NULL AND NEW.linklink_id IS NULL AND NEW.linklink_summary_id IS OLD.resource_id))
 OR ((NEW.rps_id IS NOT OLD.rps_id OR NEW.rps_summary_id IS NOT OLD.rps_summary_id) AND NOT (OLD.rps_id IS OLD.resource_id AND OLD.rps_summary_id IS NULL AND NEW.rps_id IS NULL AND NEW.rps_summary_id IS OLD.resource_id))
 OR json_extract(NEW.private_json,'$.seed') IS NOT json_extract(OLD.private_json,'$.seed')
 OR json_extract(NEW.private_json,'$.commitment') IS NOT json_extract(OLD.private_json,'$.commitment')
 OR json_extract(NEW.private_json,'$.rules') IS NOT json_extract(OLD.private_json,'$.rules')
BEGIN SELECT RAISE(ABORT,'game random commitment is immutable'); END;
CREATE TRIGGER generation_two_credit_accounts_sm128_guard BEFORE INSERT ON credit_accounts
WHEN typeof(NEW.balance_mag)<>'blob' OR length(NEW.balance_mag)<>16 OR substr(hex(NEW.balance_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'credit account SM128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_accounts_sm128_update_guard BEFORE UPDATE OF balance_mag ON credit_accounts
WHEN typeof(NEW.balance_mag)<>'blob' OR length(NEW.balance_mag)<>16 OR substr(hex(NEW.balance_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'credit account SM128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_accounts_time_guard BEFORE INSERT ON credit_accounts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'credit account timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_credit_accounts_time_update_guard BEFORE UPDATE ON credit_accounts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'credit account timestamp is outside UTC range'); END;
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
CREATE TRIGGER generation_two_integer_type_credit_accounts_insert_guard BEFORE INSERT ON credit_accounts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.balance_sign IS NOT NULL AND typeof(NEW.balance_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_accounts_update_guard BEFORE UPDATE ON credit_accounts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.balance_sign IS NOT NULL AND typeof(NEW.balance_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
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
CREATE TRIGGER idempotency_expiry_guard BEFORE INSERT ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR NEW.expires_at<>NEW.created_at+86400
BEGIN SELECT RAISE(ABORT,'idempotency records expire exactly 24 hours after creation'); END;
CREATE TRIGGER idempotency_expiry_update_guard BEFORE UPDATE OF created_at,expires_at ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR NEW.expires_at<>NEW.created_at+86400
BEGIN SELECT RAISE(ABORT,'idempotency records expire exactly 24 hours after creation'); END;
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
CREATE TRIGGER onboarding_completion_operation_insert BEFORE INSERT ON game_onboarding_completions
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='game_onboarding_reward' AND o.created_at=NEW.completed_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general'
  AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'onboarding reward operation mismatch'); END;
CREATE TRIGGER onboarding_hold_parent_insert BEFORE INSERT ON game_onboarding_holds WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=NEW.duel_queue_id AND q.user_id=NEW.user_id AND q.game_key=NEW.game_key AND
  ((q.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(q.mode,5,1))) OR
   (q.game_key='likes' AND NEW.task_key IN (q.mode||'_complete',q.mode||'_win'))))) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_seats p JOIN game_duel_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.duel_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND s.game_key=NEW.game_key AND
  ((s.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(s.mode,5,1))) OR
   (s.game_key='likes' AND NEW.task_key IN (s.mode||'_complete',s.mode||'_win'))))) OR
 (NEW.game_key='blackjack' AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.id=NEW.blackjack_entry_id AND e.user_id=NEW.user_id AND e.state IN ('waiting','seated','playing')))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER onboarding_hold_parent_update BEFORE UPDATE ON game_onboarding_holds WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=NEW.duel_queue_id AND q.user_id=NEW.user_id AND q.game_key=NEW.game_key AND
  ((q.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(q.mode,5,1))) OR
   (q.game_key='likes' AND NEW.task_key IN (q.mode||'_complete',q.mode||'_win'))))) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_seats p JOIN game_duel_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.duel_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND s.game_key=NEW.game_key AND
  ((s.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(s.mode,5,1))) OR
   (s.game_key='likes' AND NEW.task_key IN (s.mode||'_complete',s.mode||'_win'))))) OR
 (NEW.game_key='blackjack' AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.id=NEW.blackjack_entry_id AND e.user_id=NEW.user_id AND e.state IN ('waiting','seated','playing')))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER rps_queue_account_guard BEFORE INSERT ON game_rps_queue
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-queue:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps queue account mismatch'); END;
CREATE TRIGGER rps_queue_account_update_guard BEFORE UPDATE OF id,account_id ON game_rps_queue
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-queue:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps queue account mismatch'); END;
CREATE TRIGGER rps_session_account_guard BEFORE INSERT ON game_rps_sessions
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-session:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps session account mismatch'); END;
CREATE TRIGGER rps_session_account_update_guard BEFORE UPDATE OF id,account_id ON game_rps_sessions
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-session:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps session account mismatch'); END;
CREATE TRIGGER shared_pool_account_guard BEFORE INSERT ON shared_pools
 WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='pool' AND a.code='pool:'||NEW.id)
 BEGIN SELECT RAISE(ABORT,'shared pool account identity mismatch'); END;
CREATE TRIGGER shared_pool_account_update_guard BEFORE UPDATE OF id,account_id ON shared_pools
 WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='pool' AND a.code='pool:'||NEW.id)
 BEGIN SELECT RAISE(ABORT,'shared pool account identity mismatch'); END;
CREATE TRIGGER welfare_claim_matrix_guard BEFORE INSERT ON welfare_claims
WHEN NEW.asset_type<>'game' OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id
  WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id
   AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
 OR typeof(NEW.site_day)<>'text'
 OR length(NEW.site_day)<>10
 OR NEW.site_day NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.site_day)<>NEW.site_day
 OR NEW.threshold_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.cap_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.pool_before_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.award_milli NOT BETWEEN 1 AND 9000000000000000
 OR NEW.award_milli>NEW.cap_milli
 OR NEW.award_milli>NEW.pool_before_milli
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NOT EXISTS(SELECT 1 FROM credit_operations o
               WHERE o.id=NEW.operation_id AND o.kind='welfare_claim'
                 AND o.source_type='operation' AND o.source_id=NEW.operation_id
                 AND hex(o.source_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'welfare claim matrix is invalid'); END;
CREATE TRIGGER welfare_claim_matrix_update_guard BEFORE UPDATE ON welfare_claims
WHEN NEW.asset_type IS NOT OLD.asset_type
 OR typeof(NEW.site_day)<>'text'
 OR length(NEW.site_day)<>10
 OR NEW.site_day NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.site_day)<>NEW.site_day
 OR NEW.threshold_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.cap_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.pool_before_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.award_milli NOT BETWEEN 1 AND 9000000000000000
 OR NEW.award_milli>NEW.cap_milli
 OR NEW.award_milli>NEW.pool_before_milli
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NOT EXISTS(SELECT 1 FROM credit_operations o
               WHERE o.id=NEW.operation_id AND o.kind='welfare_claim'
                 AND o.source_type='operation' AND o.source_id=NEW.operation_id
                 AND hex(o.source_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'welfare claim matrix is invalid'); END;
INSERT INTO site_config(key,value,updated_at) VALUES('game_steadycatch_enabled','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_steadycatch_price_milli','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_steadycatch_first_reward_milli','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_enabled','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_standard_enabled','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_standard_ticket_milli','5000000',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_lakenotes_enabled','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_lakenotes_exchanges','{"coins_to_game":{"enabled":false,"source_amount":"","target_amount":""},"coins_to_general":{"enabled":false,"source_amount":"","target_amount":""},"game_to_coins":{"enabled":false,"source_amount":"","target_amount":""},"general_to_coins":{"enabled":false,"source_amount":"","target_amount":""}}',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_standard_rake_platform_bp','100',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_standard_rake_welfare_bp','100',0);
INSERT INTO site_config(key,value,updated_at) VALUES('game_gwent_standard_rake_thursday_bp','100',0);
UPDATE site_config SET value='1' WHERE key='game_lakenotes_enabled' AND EXISTS(SELECT 1 FROM limited_activity_configs WHERE activity_key='lake-notes' AND visible=1 AND paused=0);
UPDATE site_config SET value='1' WHERE key='games_enabled' AND EXISTS(SELECT 1 FROM site_config WHERE key='game_lakenotes_enabled' AND value='1');
