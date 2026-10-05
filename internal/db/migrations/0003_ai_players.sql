-- Preserve existing rows while extending AI participation and economics.
DROP TRIGGER credit_operation_source_guard;
DROP TRIGGER credit_operation_source_update_guard;
DROP TRIGGER generation_two_credit_operations_u128_guard;
DROP TRIGGER generation_two_credit_operations_u128_update_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_update_guard;
DROP TRIGGER welfare_claim_matrix_guard;
DROP TRIGGER welfare_claim_matrix_update_guard;
DROP TRIGGER onboarding_completion_operation_insert;
DROP TRIGGER game_checkin_operation_insert;
DROP TRIGGER game_checkin_operation_update;
DROP TRIGGER game_duel_session_delete_guard;
DROP TRIGGER game_duel_terminal_immutable;
DROP TRIGGER game_duel_seat_payment_insert;
DROP TRIGGER game_duel_seat_payment_update;
DROP TRIGGER game_duel_user_delete_guard;
DROP TRIGGER game_duel_session_frozen;
DROP TRIGGER game_duel_session_accounts;
DROP TRIGGER game_duel_slot_insert;
DROP TRIGGER game_duel_slot_update;
DROP TRIGGER game_duel_seat_deidentify;
DROP TRIGGER game_duel_user_ban_guard;
DROP TRIGGER game_duel_anonymous_immutable;
DROP TRIGGER activity_loan_operation_insert;
DROP TRIGGER onboarding_hold_parent_insert;
DROP TRIGGER onboarding_hold_parent_update;
DROP TRIGGER credit_operations_no_update;
DROP TRIGGER credit_operations_no_delete;
DROP TRIGGER credit_entries_no_delete;
CREATE TABLE game_ai_settings (
 game_key TEXT PRIMARY KEY NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807)
) STRICT;
CREATE TABLE game_ai_policies (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='aip_'),
 game_key TEXT NOT NULL,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description TEXT NOT NULL CHECK(length(description)<=512),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253399708799)
) STRICT;
CREATE TABLE game_ai_policy_versions (
 policy_id TEXT NOT NULL REFERENCES game_ai_policies(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128),
 schema_id TEXT NOT NULL CHECK(length(schema_id) BETWEEN 1 AND 128),
 definition_json TEXT NOT NULL CHECK(json_valid(definition_json) AND length(CAST(definition_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 PRIMARY KEY(policy_id,version)
) STRICT;
CREATE TABLE game_ai_bots (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='bot_'),
 game_key TEXT NOT NULL,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description TEXT NOT NULL CHECK(length(description)<=512),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 policy_id TEXT NOT NULL,
 policy_version INTEGER NOT NULL,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 ticket_milli INTEGER NOT NULL CHECK(ticket_milli BETWEEN 0 AND 9000000000000000),
 reward_milli INTEGER NOT NULL CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 memory_days INTEGER NOT NULL CHECK(memory_days BETWEEN 1 AND 30),
 memory_games INTEGER NOT NULL CHECK(memory_games BETWEEN 1 AND 100),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253399708799),
 FOREIGN KEY(policy_id,policy_version) REFERENCES game_ai_policy_versions(policy_id,version) ON DELETE RESTRICT
) STRICT;
CREATE TABLE game_ai_challenges (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='aic_'),
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 rules_key TEXT NOT NULL CHECK(length(rules_key) BETWEEN 1 AND 128),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799)
) STRICT;
CREATE TABLE game_ai_queue (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='aiq_'),
 game_key TEXT NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708679),
 deadline INTEGER NOT NULL CHECK(deadline=created_at+120),
 state TEXT NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','failed')),
 failure TEXT CHECK(failure IN ('insufficient_credits','closed','expired','account_unavailable','server_restart')),
 resolved_at INTEGER CHECK(resolved_at BETWEEN created_at AND 253399708799),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=32768),
 UNIQUE(user_id,game_key),
 CHECK((state='waiting' AND failure IS NULL AND resolved_at IS NULL) OR (state='failed' AND failure IS NOT NULL AND resolved_at IS NOT NULL))
) STRICT;
CREATE TABLE game_ai_sessions (
 session_id TEXT PRIMARY KEY NOT NULL REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 bot_seat INTEGER NOT NULL CHECK(bot_seat IN (0,1)),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=65536),
 first_clear INTEGER NOT NULL DEFAULT 0 CHECK(first_clear IN (0,1)),
 reward_milli INTEGER NOT NULL DEFAULT 0 CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 CHECK(first_clear=1 OR reward_milli=0)
) STRICT;
CREATE TABLE game_ai_preferences (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 memory_enabled INTEGER NOT NULL CHECK(memory_enabled IN (0,1)),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253399708799),
 PRIMARY KEY(user_id,bot_id)
) STRICT;
CREATE TABLE game_ai_memories (
 session_id TEXT PRIMARY KEY NOT NULL REFERENCES game_ai_sessions(session_id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 feature_version INTEGER NOT NULL CHECK(feature_version>=1),
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253399708799),
 expires_at INTEGER NOT NULL CHECK(expires_at>completed_at AND expires_at<=completed_at+2592000),
 features_json TEXT NOT NULL CHECK(json_valid(features_json) AND length(CAST(features_json AS BLOB))<=4096)
) STRICT;
CREATE TABLE game_ai_clears (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253399708799),
 reward_milli INTEGER NOT NULL CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 operation_id TEXT REFERENCES credit_operations(id) ON DELETE SET NULL,
 PRIMARY KEY(user_id,challenge_id)
) STRICT;
CREATE TABLE game_duel_history (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 game_key TEXT NOT NULL,
 session_id TEXT NOT NULL UNIQUE REFERENCES game_duel_sessions(id) ON DELETE CASCADE
) STRICT;
CREATE TEMP TABLE ai_migration_sequence AS SELECT name,seq FROM sqlite_sequence WHERE name='game_duel_anonymous';
CREATE TEMP TABLE ai_migration_credit_operations AS SELECT id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted FROM credit_operations;
DROP TABLE credit_operations;
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal','ai_ticket','ai_terminal','blackjack_reserve','blackjack_settle','blackjack_release')), source_type TEXT NOT NULL CHECK(source_type IN ('image_task','operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session','duel_queue','duel_session','blackjack_payment')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (donation_credit_user_id IS NOT NULL AND kind IN ('admin_user_adjustment','donor_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal','ai_ticket','ai_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted) SELECT id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted FROM ai_migration_credit_operations;
DROP TABLE ai_migration_credit_operations;
CREATE TEMP TABLE ai_migration_game_duel_sessions AS SELECT id,game_key,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id FROM game_duel_sessions;
DROP TABLE game_duel_sessions;
CREATE TABLE game_duel_sessions (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4) IN ('bid_','lik_') AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,4)='bid_') OR (game_key='likes' AND substr(id,1,4)='lik_')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')))) OR (economy='ai_challenge' AND game_key='bidding' AND mode='ai')),
 content_hash TEXT NOT NULL,
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 0 AND 9000000000000000 AND (economy='ai_challenge' OR ticket_milli>0)),
 platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999),
 welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999),
 thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000),
 state TEXT NOT NULL CHECK(state IN ('active','terminal')),
 phase TEXT NOT NULL CHECK(phase IN ('joker','bid','plan','settlement','terminal')),
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
 reason TEXT CHECK(reason IS NULL OR reason IN ('rounds','target','double-overload','limit','surrender','server_restart','account_unavailable')),
 winner_seat INTEGER CHECK(winner_seat IS NULL OR winner_seat IN (0,1)),
 score0 INTEGER CHECK(score0 IS NULL OR score0 BETWEEN 0 AND 1000000000),
 score1 INTEGER CHECK(score1 IS NULL OR score1 BETWEEN 0 AND 1000000000),
 prize_milli INTEGER CHECK(prize_milli IS NULL OR prize_milli BETWEEN 0 AND ticket_milli),
 platform_milli INTEGER CHECK(platform_milli IS NULL OR platform_milli BETWEEN 0 AND ticket_milli),
 welfare_milli INTEGER CHECK(welfare_milli IS NULL OR welfare_milli BETWEEN 0 AND ticket_milli),
 thursday_milli INTEGER CHECK(thursday_milli IS NULL OR thursday_milli BETWEEN 0 AND ticket_milli),
 terminal_operation_id TEXT UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT,
 CHECK((state='active' AND ((game_key='bidding' AND phase IN ('joker','bid')) OR (game_key='likes' AND phase IN ('plan','settlement'))) AND phase_deadline IS NOT NULL AND (hex(ledger_rows_remaining)='00000000000000000000000000000001' OR (economy='ai_challenge' AND hex(ledger_rows_remaining)='00000000000000000000000000000000')) AND terminal_at IS NULL AND delete_at IS NULL AND outcome IS NULL AND reason IS NULL AND winner_seat IS NULL AND score0 IS NULL AND score1 IS NULL AND prize_milli IS NULL AND platform_milli IS NULL AND welfare_milli IS NULL AND thursday_milli IS NULL AND terminal_operation_id IS NULL) OR (state='terminal' AND phase='terminal' AND phase_deadline IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000' AND terminal_at IS NOT NULL AND delete_at IS NOT NULL AND outcome IS NOT NULL AND reason IS NOT NULL AND score0 IS NOT NULL AND score1 IS NOT NULL AND prize_milli IS NOT NULL AND platform_milli IS NOT NULL AND welfare_milli IS NOT NULL AND thursday_milli IS NOT NULL AND (terminal_operation_id IS NOT NULL OR (economy='ai_challenge' AND ticket_milli=0)))),
 CHECK(state='active' OR (economy='pvp' AND ((outcome='decided' AND winner_seat IS NOT NULL AND reason NOT IN ('server_restart','account_unavailable') AND prize_milli+platform_milli+welfare_milli+thursday_milli=ticket_milli) OR (outcome IN ('draw','system_cancelled') AND winner_seat IS NULL AND prize_milli=0 AND platform_milli=0 AND welfare_milli=0 AND thursday_milli=0))) OR (economy='ai_challenge' AND prize_milli=0 AND welfare_milli=0 AND thursday_milli=0 AND ((outcome IN ('decided','draw') AND platform_milli=ticket_milli AND (outcome='decided')=(winner_seat IS NOT NULL)) OR (outcome='system_cancelled' AND winner_seat IS NULL AND platform_milli=0)))),
 CHECK(economy='pvp' OR (platform_bp=0 AND welfare_bp=0 AND thursday_bp=0)),
 CHECK(state='active' OR (outcome='system_cancelled' AND reason IN ('server_restart','account_unavailable')) OR (outcome<>'system_cancelled' AND reason NOT IN ('server_restart','account_unavailable')))
) STRICT;
INSERT INTO game_duel_sessions(id,game_key,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id) SELECT id,game_key,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id FROM ai_migration_game_duel_sessions;
DROP TABLE ai_migration_game_duel_sessions;
CREATE TEMP TABLE ai_migration_game_duel_seats AS SELECT session_id,seat_no,user_id,general_paid_milli,game_paid_milli,loadout_json,current_plan_json,locked,timeout_count FROM game_duel_seats;
DROP TABLE game_duel_seats;
CREATE TABLE game_duel_seats (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 seat_no INTEGER NOT NULL CHECK(seat_no IN (0,1)),
 participant_kind TEXT NOT NULL DEFAULT 'human' CHECK(participant_kind IN ('human','bot')),
 bot_id TEXT REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
 general_paid_milli INTEGER NOT NULL CHECK(typeof(general_paid_milli)='integer' AND general_paid_milli BETWEEN 0 AND 9000000000000000),
 game_paid_milli INTEGER NOT NULL CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND 9000000000000000),
 loadout_json TEXT CHECK(loadout_json IS NULL OR (typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 current_plan_json TEXT CHECK(current_plan_json IS NULL OR (typeof(current_plan_json)='text' AND length(CAST(current_plan_json AS BLOB))<=16384 AND json_valid(current_plan_json))),
 locked INTEGER NOT NULL CHECK(locked IN (0,1)),
 timeout_count INTEGER NOT NULL CHECK(timeout_count BETWEEN 0 AND 150),
 CHECK((participant_kind='human' AND bot_id IS NULL) OR (participant_kind='bot' AND bot_id IS NOT NULL AND user_id IS NULL AND general_paid_milli=0 AND game_paid_milli=0 AND loadout_json IS NULL)),
 PRIMARY KEY(session_id,seat_no), UNIQUE(session_id,user_id),
 CHECK((locked=0 AND current_plan_json IS NULL) OR (locked=1 AND current_plan_json IS NOT NULL))
) STRICT;
INSERT INTO game_duel_seats(session_id,seat_no,user_id,general_paid_milli,game_paid_milli,loadout_json,current_plan_json,locked,timeout_count) SELECT session_id,seat_no,user_id,general_paid_milli,game_paid_milli,loadout_json,current_plan_json,locked,timeout_count FROM ai_migration_game_duel_seats;
DROP TABLE ai_migration_game_duel_seats;
CREATE TEMP TABLE ai_migration_game_duel_user_slots AS SELECT user_id,game_key,queue_id,session_id FROM game_duel_user_slots;
DROP TABLE game_duel_user_slots;
CREATE TABLE game_duel_user_slots (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 queue_id TEXT UNIQUE REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 ai_queue_id TEXT UNIQUE REFERENCES game_ai_queue(id) ON DELETE RESTRICT,
 session_id TEXT REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 PRIMARY KEY(user_id,game_key),
 CHECK((queue_id IS NOT NULL)+(ai_queue_id IS NOT NULL)+(session_id IS NOT NULL)=1)
) STRICT;
INSERT INTO game_duel_user_slots(user_id,game_key,queue_id,session_id) SELECT user_id,game_key,queue_id,session_id FROM ai_migration_game_duel_user_slots;
DROP TABLE ai_migration_game_duel_user_slots;
CREATE TEMP TABLE ai_migration_game_duel_anonymous AS SELECT export_seq,archive_id,game_key,mode,content_hash,header_json FROM game_duel_anonymous;
DROP TABLE game_duel_anonymous;
CREATE TABLE game_duel_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='dah_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')))) OR (economy='ai_challenge' AND game_key='bidding' AND mode='ai')),
 content_hash TEXT NOT NULL,
 header_json TEXT NOT NULL CHECK(typeof(header_json)='text' AND length(CAST(header_json AS BLOB))<=1048576 AND json_valid(header_json)),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
INSERT INTO game_duel_anonymous(export_seq,archive_id,game_key,mode,content_hash,header_json) SELECT export_seq,archive_id,game_key,mode,content_hash,header_json FROM ai_migration_game_duel_anonymous;
DROP TABLE ai_migration_game_duel_anonymous;
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM ai_migration_sequence old WHERE NOT EXISTS(SELECT 1 FROM sqlite_sequence s WHERE s.name=old.name);
UPDATE sqlite_sequence SET seq=max(seq,COALESCE((SELECT seq FROM ai_migration_sequence WHERE name=sqlite_sequence.name),seq)) WHERE name='game_duel_anonymous';
DROP TABLE ai_migration_sequence;
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
CREATE TRIGGER onboarding_completion_operation_insert BEFORE INSERT ON game_onboarding_completions
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='game_onboarding_reward' AND o.created_at=NEW.completed_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general'
  AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'onboarding reward operation mismatch'); END;
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
CREATE INDEX idx_duel_sessions_due ON game_duel_sessions(game_key,state,phase_deadline,id);
CREATE INDEX idx_duel_sessions_terminal ON game_duel_sessions(game_key,state,terminal_at,id);
CREATE INDEX idx_duel_sessions_expiry ON game_duel_sessions(game_key,state,delete_at,id);
CREATE INDEX idx_credit_duel_terminal ON credit_operations(substr(source_id,1,4),ledger_seq) WHERE kind='duel_terminal' AND source_type='duel_session';
CREATE INDEX idx_duel_seats_user ON game_duel_seats(user_id,session_id);
CREATE INDEX idx_duel_anonymous_export ON game_duel_anonymous(game_key,mode,export_seq);
CREATE INDEX idx_duel_anonymous_archive ON game_duel_anonymous(game_key,archive_id);
CREATE TRIGGER game_duel_session_delete_guard BEFORE DELETE ON game_duel_sessions WHEN OLD.state<>'terminal' OR hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel session still active'); END;
CREATE TRIGGER game_duel_terminal_immutable BEFORE UPDATE ON game_duel_sessions WHEN OLD.state='terminal' BEGIN SELECT RAISE(ABORT,'duel result immutable'); END;
CREATE TRIGGER game_duel_seat_payment_insert BEFORE INSERT ON game_duel_seats WHEN (NEW.participant_kind='human' AND NEW.general_paid_milli+NEW.game_paid_milli<>(SELECT ticket_milli FROM game_duel_sessions WHERE id=NEW.session_id)) OR (NEW.participant_kind='bot' AND NOT EXISTS(SELECT 1 FROM game_duel_sessions WHERE id=NEW.session_id AND economy='ai_challenge')) BEGIN SELECT RAISE(ABORT,'duel payment mismatch'); END;
CREATE TRIGGER game_duel_seat_payment_update BEFORE UPDATE ON game_duel_seats WHEN NEW.participant_kind<>OLD.participant_kind OR NEW.bot_id IS NOT OLD.bot_id OR NEW.session_id<>OLD.session_id OR NEW.seat_no<>OLD.seat_no OR NEW.general_paid_milli<>OLD.general_paid_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.loadout_json IS NOT OLD.loadout_json OR (OLD.user_id IS NULL AND NEW.user_id IS NOT NULL) OR (OLD.user_id IS NOT NULL AND NEW.user_id IS NOT NULL AND NEW.user_id<>OLD.user_id) BEGIN SELECT RAISE(ABORT,'duel seat immutable'); END;
CREATE TRIGGER game_duel_user_delete_guard BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=OLD.id) OR EXISTS(SELECT 1 FROM game_duel_seats WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'duel user handoff required'); END;
CREATE TRIGGER game_duel_session_frozen BEFORE UPDATE ON game_duel_sessions WHEN NEW.economy<>OLD.economy OR NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR NEW.started_at<>OLD.started_at OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.initial_state_json<>OLD.initial_state_json OR NEW.phase_seq<OLD.phase_seq OR NEW.revision<=OLD.revision BEGIN SELECT RAISE(ABORT,'duel session terms immutable'); END;
CREATE TRIGGER game_duel_session_accounts BEFORE INSERT ON game_duel_sessions WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-session:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-session:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel session account mismatch'); END;
CREATE TRIGGER game_duel_slot_insert BEFORE INSERT ON game_duel_user_slots WHEN (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_slot_update BEFORE UPDATE ON game_duel_user_slots WHEN NEW.user_id<>OLD.user_id OR NEW.game_key<>OLD.game_key OR (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_seat_deidentify BEFORE UPDATE OF user_id ON game_duel_seats WHEN NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND (SELECT state FROM game_duel_sessions WHERE id=NEW.session_id)<>'terminal' BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_user_ban_guard BEFORE UPDATE OF is_banned,banned_until ON users WHEN NEW.is_banned=1 AND (NEW.is_banned<>OLD.is_banned OR NEW.banned_until IS NOT OLD.banned_until) AND EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=NEW.id) BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_anonymous_immutable BEFORE UPDATE ON game_duel_anonymous BEGIN SELECT RAISE(ABORT,'duel archive immutable'); END;
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
CREATE TRIGGER activity_loan_operation_insert BEFORE INSERT ON activity_loans
WHEN NOT EXISTS(SELECT 1 FROM credit_operations o WHERE o.id=NEW.operation_id AND o.kind='activity_loan' AND o.ledger_seq=NEW.ledger_seq AND o.created_at=NEW.created_at)
 OR (SELECT count(*) FROM credit_entries WHERE operation_id=NEW.operation_id)<>4
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='general' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='game' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
BEGIN SELECT RAISE(ABORT,'loan operation mismatch'); END;
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
CREATE TRIGGER credit_entries_no_delete BEFORE DELETE ON credit_entries
WHEN NOT EXISTS(SELECT 1 FROM credit_operations WHERE id=OLD.operation_id AND compacted=1
 AND ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1))
BEGIN SELECT RAISE(ABORT,'credit entry is not compacted'); END;
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
CREATE INDEX idx_ai_policies_game ON game_ai_policies(game_key,id);
CREATE TRIGGER game_ai_policy_version_immutable BEFORE UPDATE ON game_ai_policy_versions BEGIN SELECT RAISE(ABORT,'AI policy version immutable'); END;
CREATE INDEX idx_ai_bots_game ON game_ai_bots(game_key,enabled,id);
CREATE TRIGGER game_ai_challenge_immutable BEFORE UPDATE ON game_ai_challenges BEGIN SELECT RAISE(ABORT,'AI challenge immutable'); END;
CREATE INDEX idx_ai_queue_game ON game_ai_queue(game_key,ordinal);
CREATE INDEX idx_ai_sessions_bot ON game_ai_sessions(bot_id,session_id);
CREATE INDEX idx_ai_memory_pair ON game_ai_memories(user_id,bot_id,feature_version,completed_at DESC,session_id);
CREATE INDEX idx_ai_memory_expiry ON game_ai_memories(expires_at,session_id);
CREATE INDEX idx_duel_history_game ON game_duel_history(game_key,sequence);
CREATE TRIGGER game_duel_history_terminal AFTER UPDATE OF state ON game_duel_sessions
WHEN NEW.state='terminal' AND OLD.state='active' BEGIN
 INSERT INTO game_duel_history(game_key,session_id) VALUES(NEW.game_key,NEW.id);
END;
INSERT INTO game_ai_settings(game_key) VALUES('bidding');
INSERT INTO game_duel_history(game_key,session_id) SELECT g.game_key,g.id FROM game_duel_sessions g JOIN credit_operations o ON o.id=g.terminal_operation_id WHERE g.state='terminal' ORDER BY o.ledger_seq;
