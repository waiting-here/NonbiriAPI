package db

// The extension is ordinary DDL shared by fresh bootstrap and the exact
// prior-schema upgrade. Defaults preserve existing general-credit facts.
const dualAssetSchema = dualAssetColumnsSchema + dualAssetTablesSchema + dualAssetIndexesSchema + dualAssetGuardsSchema

const dualAssetColumnsSchema = `
ALTER TABLE credit_accounts ADD COLUMN asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game'));
ALTER TABLE credit_entries ADD COLUMN asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game'));
ALTER TABLE welfare_claims ADD COLUMN asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game'));
ALTER TABLE user_activity_daily ADD COLUMN game_checkins INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_checkins)='integer' AND game_checkins BETWEEN 0 AND 9223372036854775807);
ALTER TABLE site_activity_daily ADD COLUMN game_checkins BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_checkins)='blob' AND length(game_checkins)=16);
ALTER TABLE game_fishing_batches ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2);
ALTER TABLE game_linklink_sessions ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2);
ALTER TABLE game_linklink_summaries ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2);
ALTER TABLE game_rps_queue ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2);
ALTER TABLE game_rps_pending_results ADD COLUMN rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2);
ALTER TABLE game_fishing_batches ADD COLUMN game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND entry_total_milli);
ALTER TABLE game_fishing_batches ADD COLUMN platform_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_bp)='integer' AND platform_bp BETWEEN 0 AND 9999);
ALTER TABLE game_fishing_batches ADD COLUMN welfare_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_bp)='integer' AND welfare_bp BETWEEN 0 AND 9999);
ALTER TABLE game_fishing_batches ADD COLUMN thursday_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_bp)='integer' AND thursday_bp BETWEEN 0 AND 9999);
ALTER TABLE game_fishing_batches ADD COLUMN net_payout_total_milli INTEGER CHECK(net_payout_total_milli IS NULL OR (typeof(net_payout_total_milli)='integer' AND net_payout_total_milli BETWEEN 0 AND 9000000000000000));
ALTER TABLE game_fishing_batches ADD COLUMN platform_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_cut_total_milli)='integer' AND platform_cut_total_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_fishing_batches ADD COLUMN welfare_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_cut_total_milli)='integer' AND welfare_cut_total_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_fishing_batches ADD COLUMN thursday_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_cut_total_milli)='integer' AND thursday_cut_total_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_fishing_outcomes ADD COLUMN net_payout_milli INTEGER CHECK(net_payout_milli IS NULL OR (typeof(net_payout_milli)='integer' AND net_payout_milli BETWEEN 0 AND 9000000000000000));
ALTER TABLE game_fishing_outcomes ADD COLUMN platform_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_cut_milli)='integer' AND platform_cut_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_fishing_outcomes ADD COLUMN welfare_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_cut_milli)='integer' AND welfare_cut_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_fishing_outcomes ADD COLUMN thursday_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_cut_milli)='integer' AND thursday_cut_milli BETWEEN 0 AND 9000000000000000);
ALTER TABLE game_linklink_sessions ADD COLUMN game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND price_milli);
ALTER TABLE game_linklink_sessions ADD COLUMN assists_initial INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_initial)='integer' AND assists_initial BETWEEN 0 AND 5);
ALTER TABLE game_linklink_sessions ADD COLUMN assists_remaining INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_remaining)='integer' AND assists_remaining BETWEEN 0 AND assists_initial);
ALTER TABLE game_linklink_summaries ADD COLUMN game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND price_milli);
ALTER TABLE game_linklink_summaries ADD COLUMN assists_initial INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_initial)='integer' AND assists_initial BETWEEN 0 AND 5);
ALTER TABLE game_linklink_summaries ADD COLUMN assists_remaining INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_remaining)='integer' AND assists_remaining BETWEEN 0 AND assists_initial);
ALTER TABLE game_rps_queue ADD COLUMN game_paid BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_paid)='blob' AND length(game_paid)=16 AND game_paid<=reserved);
ALTER TABLE game_rps_seats ADD COLUMN game_buy_in BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_buy_in)='blob' AND length(game_buy_in)=16 AND game_buy_in<=starting_balance);
ALTER TABLE game_rps_seats ADD COLUMN game_remaining BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_remaining)='blob' AND length(game_remaining)=16 AND game_remaining<=current_balance);
ALTER TABLE game_rps_summary_seats ADD COLUMN general_buy_in BLOB CHECK(general_buy_in IS NULL OR (typeof(general_buy_in)='blob' AND length(general_buy_in)=16));
ALTER TABLE game_rps_summary_seats ADD COLUMN game_buy_in BLOB CHECK(game_buy_in IS NULL OR (typeof(game_buy_in)='blob' AND length(game_buy_in)=16));
ALTER TABLE game_rps_pending_results ADD COLUMN general_buy_in BLOB CHECK(general_buy_in IS NULL OR (typeof(general_buy_in)='blob' AND length(general_buy_in)=16));
ALTER TABLE game_rps_pending_results ADD COLUMN game_buy_in BLOB CHECK(game_buy_in IS NULL OR (typeof(game_buy_in)='blob' AND length(game_buy_in)=16));
ALTER TABLE game_rps_pending_results ADD COLUMN own_returned_general BLOB CHECK(own_returned_general IS NULL OR (typeof(own_returned_general)='blob' AND length(own_returned_general)=16));
ALTER TABLE game_user_preferences ADD COLUMN linklink_public_tie_key BLOB CHECK(linklink_public_tie_key IS NULL OR (typeof(linklink_public_tie_key)='blob' AND length(linklink_public_tie_key)=32));
`

const dualAssetTablesSchema = `
CREATE TABLE game_checkins (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(typeof(id)='integer' AND id>0),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE CHECK(typeof(user_id)='integer'),
 site_day TEXT NOT NULL CHECK(typeof(site_day)='text' AND length(site_day)=10 AND site_day GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' AND date(site_day) IS site_day),
 award_milli INTEGER NOT NULL CHECK(typeof(award_milli)='integer' AND award_milli BETWEEN 0 AND 9000000000000000),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 UNIQUE(user_id,site_day)
);
CREATE TABLE game_onboarding_completions (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE CHECK(typeof(user_id)='integer'),
 game_key TEXT NOT NULL,
 task_key TEXT NOT NULL,
 award_milli INTEGER NOT NULL CHECK(typeof(award_milli)='integer'),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')),
 completed_at INTEGER NOT NULL CHECK(typeof(completed_at)='integer' AND completed_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,game_key,task_key),
 CHECK((game_key='fishing' AND task_key IN ('worm','lure','premium')) OR (game_key='linklink' AND task_key IN ('6x8','8x8','10x10')) OR (game_key='rps' AND task_key IN ('quick','standard','deathmatch'))),
 CHECK(award_milli=CASE
   WHEN game_key='fishing' THEN 1000000
   WHEN game_key='linklink' THEN CASE task_key WHEN '6x8' THEN 1000000 WHEN '8x8' THEN 2000000 WHEN '10x10' THEN 3000000 END
   WHEN game_key='rps' THEN CASE task_key WHEN 'quick' THEN 1000000 WHEN 'standard' THEN 2000000 WHEN 'deathmatch' THEN 5000000 END
 END)
) WITHOUT ROWID;
CREATE TABLE game_onboarding_holds (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=26 AND substr(id,1,4)='goh_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK(typeof(user_id)='integer'),
 game_key TEXT NOT NULL,
 task_key TEXT NOT NULL,
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND ledger_rows_remaining=X'00000000000000000000000000000001'),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 fishing_batch_id TEXT REFERENCES game_fishing_batches(id) ON DELETE RESTRICT,
 linklink_session_id TEXT REFERENCES game_linklink_sessions(id) ON DELETE RESTRICT,
 rps_queue_id TEXT REFERENCES game_rps_queue(id) ON DELETE RESTRICT,
 rps_session_id TEXT,
 seat_no INTEGER CHECK(seat_no IS NULL OR (typeof(seat_no)='integer' AND seat_no BETWEEN 0 AND 2)),
 FOREIGN KEY(rps_session_id,seat_no) REFERENCES game_rps_seats(session_id,seat_no) ON DELETE RESTRICT,
 CHECK((game_key='fishing' AND task_key IN ('worm','lure','premium')) OR (game_key='linklink' AND task_key IN ('6x8','8x8','10x10')) OR (game_key='rps' AND task_key IN ('quick','standard','deathmatch'))),
 CHECK(
  (game_key='fishing' AND fishing_batch_id IS NOT NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL) OR
  (game_key='linklink' AND fishing_batch_id IS NULL AND linklink_session_id IS NOT NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL) OR
  (game_key='rps' AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND
   ((rps_queue_id IS NOT NULL AND rps_session_id IS NULL AND seat_no IS NULL) OR
    (rps_queue_id IS NULL AND rps_session_id IS NOT NULL AND seat_no IS NOT NULL)))
 )
);
`

const dualAssetIndexesSchema = `
DROP INDEX idx_credit_accounts_user;
DROP INDEX idx_credit_accounts_code;
CREATE UNIQUE INDEX idx_credit_accounts_user ON credit_accounts(user_id,asset_type) WHERE kind='user';
CREATE UNIQUE INDEX idx_credit_accounts_code ON credit_accounts(code,asset_type) WHERE code IS NOT NULL;
DROP INDEX idx_rps_queue_match;
CREATE INDEX idx_rps_queue_match ON game_rps_queue(mode,rules_version,deadline,created_at,id);
CREATE UNIQUE INDEX idx_game_onboarding_hold_fishing ON game_onboarding_holds(fishing_batch_id) WHERE fishing_batch_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_linklink ON game_onboarding_holds(linklink_session_id) WHERE linklink_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_queue ON game_onboarding_holds(rps_queue_id) WHERE rps_queue_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_seat ON game_onboarding_holds(rps_session_id,seat_no) WHERE rps_session_id IS NOT NULL;
CREATE INDEX idx_game_onboarding_hold_user ON game_onboarding_holds(user_id);
CREATE INDEX idx_game_onboarding_hold_created ON game_onboarding_holds(created_at,id);
CREATE INDEX idx_linklink_leaderboard ON game_linklink_summaries(spec,rules_version,terminal_reason,terminal_at,user_id,score DESC);
`

const dualAssetGuardsSchema = `
DROP TRIGGER credit_entries_no_update;
CREATE TRIGGER credit_entries_no_update BEFORE UPDATE ON credit_entries
WHEN NOT (
 NEW.operation_id IS OLD.operation_id AND NEW.line_no IS OLD.line_no AND NEW.account_kind_snapshot IS OLD.account_kind_snapshot AND
 NEW.delta_sign IS OLD.delta_sign AND NEW.delta_mag IS OLD.delta_mag AND NEW.balance_after_sign IS OLD.balance_after_sign AND
 NEW.balance_after_mag IS OLD.balance_after_mag AND NEW.asset_type IS OLD.asset_type AND
 (NEW.account_id IS OLD.account_id OR (OLD.account_id IS NOT NULL AND NEW.account_id IS NULL AND NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=OLD.account_id)))
)
BEGIN SELECT RAISE(ABORT,'credit_entries is append-only'); END;
DROP TRIGGER credit_entries_account_kind_guard;
CREATE TRIGGER credit_entries_account_kind_guard BEFORE INSERT ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
DROP TRIGGER credit_entries_account_kind_update_guard;
CREATE TRIGGER credit_entries_account_kind_update_guard BEFORE UPDATE OF account_id,account_kind_snapshot,asset_type ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_account_asset_insert BEFORE INSERT ON credit_accounts
WHEN NEW.asset_type='game' AND (NEW.kind='pool' OR NEW.code IN ('forward_reserve','charity_reserve'))
BEGIN SELECT RAISE(ABORT,'account code does not support this asset'); END;
CREATE TRIGGER credit_account_asset_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type='game' AND (NEW.kind='pool' OR NEW.code IN ('forward_reserve','charity_reserve'))
BEGIN SELECT RAISE(ABORT,'account code does not support this asset'); END;
CREATE TRIGGER credit_account_identity_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type IS NOT OLD.asset_type OR NEW.kind IS NOT OLD.kind OR NEW.code IS NOT OLD.code OR NEW.user_id IS NOT OLD.user_id
BEGIN SELECT RAISE(ABORT,'account identity is immutable'); END;
DROP TRIGGER welfare_claim_matrix_guard;
CREATE TRIGGER welfare_claim_matrix_guard BEFORE INSERT ON welfare_claims
WHEN NEW.asset_type='game' AND NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id
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
DROP TRIGGER welfare_claim_matrix_update_guard;
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
CREATE TRIGGER fishing_funding_insert BEFORE INSERT ON game_fishing_batches
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.platform_bp<>0 OR NEW.welfare_bp<>0 OR NEW.thursday_bp<>0
 OR NEW.platform_cut_total_milli<>0 OR NEW.welfare_cut_total_milli<>0 OR NEW.thursday_cut_total_milli<>0
 OR (NEW.net_payout_total_milli IS NOT NULL AND NEW.net_payout_total_milli<>NEW.payout_total_milli)))
 OR (NEW.rules_version=2 AND (NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.net_payout_total_milli IS NULL
 OR NEW.payout_total_milli<>NEW.net_payout_total_milli+NEW.platform_cut_total_milli+NEW.welfare_cut_total_milli+NEW.thursday_cut_total_milli))
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is invalid'); END;
CREATE TRIGGER fishing_funding_update BEFORE UPDATE ON game_fishing_batches
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.platform_bp<>0 OR NEW.welfare_bp<>0 OR NEW.thursday_bp<>0
 OR NEW.platform_cut_total_milli<>0 OR NEW.welfare_cut_total_milli<>0 OR NEW.thursday_cut_total_milli<>0
 OR (NEW.net_payout_total_milli IS NOT NULL AND NEW.net_payout_total_milli<>NEW.payout_total_milli)))
 OR (NEW.rules_version=2 AND (NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.net_payout_total_milli IS NULL
 OR NEW.payout_total_milli<>NEW.net_payout_total_milli+NEW.platform_cut_total_milli+NEW.welfare_cut_total_milli+NEW.thursday_cut_total_milli))
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is invalid'); END;
CREATE TRIGGER fishing_funding_immutable_update BEFORE UPDATE ON game_fishing_batches
WHEN NEW.rules_version IS NOT OLD.rules_version OR NEW.game_paid_milli IS NOT OLD.game_paid_milli OR NEW.platform_bp IS NOT OLD.platform_bp OR NEW.welfare_bp IS NOT OLD.welfare_bp OR NEW.thursday_bp IS NOT OLD.thursday_bp OR NEW.net_payout_total_milli IS NOT OLD.net_payout_total_milli OR NEW.platform_cut_total_milli IS NOT OLD.platform_cut_total_milli OR NEW.welfare_cut_total_milli IS NOT OLD.welfare_cut_total_milli OR NEW.thursday_cut_total_milli IS NOT OLD.thursday_cut_total_milli
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is immutable'); END;
CREATE TRIGGER fishing_outcome_rake_insert BEFORE INSERT ON game_fishing_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND (
 (b.rules_version=1 AND NEW.platform_cut_milli=0 AND NEW.welfare_cut_milli=0 AND NEW.thursday_cut_milli=0
  AND (NEW.net_payout_milli IS NULL OR NEW.net_payout_milli=NEW.payout_milli)) OR
 (b.rules_version=2 AND NEW.net_payout_milli IS NOT NULL AND
  NEW.payout_milli=NEW.net_payout_milli+NEW.platform_cut_milli+NEW.welfare_cut_milli+NEW.thursday_cut_milli)))
BEGIN SELECT RAISE(ABORT,'fishing outcome rake is invalid'); END;
CREATE TRIGGER fishing_outcome_rake_update BEFORE UPDATE ON game_fishing_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND (
 (b.rules_version=1 AND NEW.platform_cut_milli=0 AND NEW.welfare_cut_milli=0 AND NEW.thursday_cut_milli=0
  AND (NEW.net_payout_milli IS NULL OR NEW.net_payout_milli=NEW.payout_milli)) OR
 (b.rules_version=2 AND NEW.net_payout_milli IS NOT NULL AND
  NEW.payout_milli=NEW.net_payout_milli+NEW.platform_cut_milli+NEW.welfare_cut_milli+NEW.thursday_cut_milli)))
BEGIN SELECT RAISE(ABORT,'fishing outcome rake is invalid'); END;
CREATE TRIGGER game_linklink_sessions_funding_insert BEFORE INSERT ON game_linklink_sessions
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_sessions_funding_update BEFORE UPDATE ON game_linklink_sessions
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_summaries_funding_insert BEFORE INSERT ON game_linklink_summaries
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_summaries_funding_update BEFORE UPDATE ON game_linklink_summaries
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER linklink_funding_immutable_update BEFORE UPDATE ON game_linklink_sessions
WHEN NEW.rules_version IS NOT OLD.rules_version OR NEW.game_paid_milli IS NOT OLD.game_paid_milli OR NEW.assists_initial IS NOT OLD.assists_initial
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is immutable'); END;
CREATE TRIGGER rps_queue_funding_insert BEFORE INSERT ON game_rps_queue
WHEN NEW.rules_version=1 AND NEW.game_paid<>X'00000000000000000000000000000000'
BEGIN SELECT RAISE(ABORT,'legacy queue cannot hold game credits'); END;
CREATE TRIGGER rps_queue_funding_update BEFORE UPDATE ON game_rps_queue
WHEN NEW.rules_version=1 AND NEW.game_paid<>X'00000000000000000000000000000000'
BEGIN SELECT RAISE(ABORT,'legacy queue cannot hold game credits'); END;
CREATE TRIGGER rps_seat_funding_insert BEFORE INSERT ON game_rps_seats
WHEN NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND
 (s.rules_version=2 OR (s.rules_version=1 AND NEW.game_buy_in=X'00000000000000000000000000000000' AND NEW.game_remaining=X'00000000000000000000000000000000')))
BEGIN SELECT RAISE(ABORT,'rps seat funding is invalid'); END;
CREATE TRIGGER rps_seat_funding_update BEFORE UPDATE ON game_rps_seats
WHEN NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND
 (s.rules_version=2 OR (s.rules_version=1 AND NEW.game_buy_in=X'00000000000000000000000000000000' AND NEW.game_remaining=X'00000000000000000000000000000000')))
BEGIN SELECT RAISE(ABORT,'rps seat funding is invalid'); END;
CREATE TRIGGER rps_seat_buyin_immutable_update BEFORE UPDATE ON game_rps_seats
WHEN NEW.game_buy_in IS NOT OLD.game_buy_in
BEGIN SELECT RAISE(ABORT,'rps buy-in is immutable'); END;
CREATE TRIGGER rps_summary_funding_insert BEFORE INSERT ON game_rps_summary_seats
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR EXISTS(SELECT 1 FROM game_rps_summaries s WHERE s.session_id=NEW.session_id AND s.rules_version=2 AND NEW.general_buy_in IS NULL)
BEGIN SELECT RAISE(ABORT,'rps summary funding is incomplete'); END;
CREATE TRIGGER rps_summary_funding_update BEFORE UPDATE ON game_rps_summary_seats
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR EXISTS(SELECT 1 FROM game_rps_summaries s WHERE s.session_id=NEW.session_id AND s.rules_version=2 AND NEW.general_buy_in IS NULL)
BEGIN SELECT RAISE(ABORT,'rps summary funding is incomplete'); END;
CREATE TRIGGER rps_pending_funding_insert BEFORE INSERT ON game_rps_pending_results
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR (NEW.rules_version=2 AND (NEW.general_buy_in IS NULL OR NEW.own_returned_general IS NULL))
BEGIN SELECT RAISE(ABORT,'rps pending funding is incomplete'); END;
CREATE TRIGGER rps_pending_funding_update BEFORE UPDATE ON game_rps_pending_results
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR (NEW.rules_version=2 AND (NEW.general_buy_in IS NULL OR NEW.own_returned_general IS NULL))
BEGIN SELECT RAISE(ABORT,'rps pending funding is incomplete'); END;
CREATE TRIGGER onboarding_hold_parent_insert BEFORE INSERT ON game_onboarding_holds
WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER onboarding_hold_parent_update BEFORE UPDATE ON game_onboarding_holds
WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER onboarding_hold_identity_update BEFORE UPDATE ON game_onboarding_holds
WHEN NEW.id IS NOT OLD.id OR NEW.user_id IS NOT OLD.user_id OR NEW.game_key IS NOT OLD.game_key OR NEW.task_key IS NOT OLD.task_key OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'onboarding hold identity is immutable'); END;
CREATE TRIGGER onboarding_completion_operation_insert BEFORE INSERT ON game_onboarding_completions
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='game_onboarding_reward' AND o.created_at=NEW.completed_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general'
  AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'onboarding reward operation mismatch'); END;
CREATE TRIGGER onboarding_completion_immutable_update BEFORE UPDATE ON game_onboarding_completions
WHEN 1
BEGIN SELECT RAISE(ABORT,'onboarding completion is immutable'); END;
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
`
