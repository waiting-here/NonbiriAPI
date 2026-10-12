DROP TRIGGER credit_operation_source_guard;
DROP TRIGGER credit_operation_source_update_guard;
DROP TRIGGER generation_two_credit_operations_u128_guard;
DROP TRIGGER generation_two_credit_operations_u128_update_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_update_guard;
DROP TRIGGER credit_operations_no_update;
DROP TRIGGER credit_operations_no_delete;
DROP TRIGGER credit_operation_donation_identity_guard;
CREATE TEMP TABLE reward_previous_credit_operations AS SELECT * FROM credit_operations;
DROP TABLE credit_operations;
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','gacha_ticket_purchase','gacha_theme_reward','lake_collection_reward','reward_grant','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal','ai_ticket','ai_terminal','catch_ticket','catch_refund','catch_reward','blackjack_reserve','blackjack_settle','blackjack_release')), source_type TEXT NOT NULL CHECK(source_type IN ('image_task','operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session','duel_queue','duel_session','catch_session','blackjack_payment')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (kind IN ('admin_user_adjustment','donor_reward','gacha_theme_reward','lake_collection_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward','gacha_theme_reward','lake_collection_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty','reward_grant'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='catch_session' AND length(source_id)=25 AND substr(source_id,1,3)='sc_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_','gwt_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','gacha_ticket_purchase','gacha_theme_reward','lake_collection_reward','reward_grant','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('catch_ticket','catch_refund','catch_reward') AND source_type='catch_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal','ai_ticket','ai_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
INSERT INTO credit_operations SELECT * FROM reward_previous_credit_operations;
DROP TABLE reward_previous_credit_operations;
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
CREATE TEMP TABLE reward_previous_gacha_owned AS SELECT * FROM gacha_owned;
DROP TABLE gacha_owned;
CREATE TABLE gacha_owned (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 card_id TEXT NOT NULL CHECK(length(card_id) BETWEEN 1 AND 80),
 obtained_at INTEGER NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('draw','shop','reward')),
 PRIMARY KEY(user_id,card_id)
) STRICT, WITHOUT ROWID;
INSERT INTO gacha_owned SELECT * FROM reward_previous_gacha_owned;
DROP TABLE reward_previous_gacha_owned;
CREATE TEMP TABLE reward_previous_gacha_operations AS SELECT * FROM gacha_operations;
DROP TABLE gacha_operations;
CREATE TABLE gacha_operations (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_'),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 key_hash BLOB CHECK(key_hash IS NULL OR length(key_hash)=32),
 request_hash BLOB CHECK(request_hash IS NULL OR length(request_hash)=32),
 action TEXT NOT NULL CHECK(action IN ('enter','check-in','limited-check-in','draw','buy','redeem','claim')),
 delta TEXT NOT NULL CHECK(json_valid(delta) AND length(delta)<=4096),
 balance_after TEXT NOT NULL CHECK(json_valid(balance_after) AND length(balance_after)<=4096),
 credit_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
INSERT INTO gacha_operations SELECT * FROM reward_previous_gacha_operations;
DROP TABLE reward_previous_gacha_operations;
CREATE UNIQUE INDEX idx_gacha_operation_key ON gacha_operations(user_id,key_hash) WHERE user_id IS NOT NULL;
CREATE INDEX idx_gacha_operation_user ON gacha_operations(user_id,created_at,id);
CREATE TEMP TABLE saved_reward_profiles AS SELECT user_id,permanent,limited,n_shards,r_shards,sr_shards,ssr_shards,newcomer,checked_day,month,monthly_permanent,monthly_limited,entered_at,updated_at FROM gacha_profiles;
DROP TABLE gacha_profiles;
CREATE TABLE gacha_profiles (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 permanent INTEGER NOT NULL DEFAULT 0 CHECK(permanent BETWEEN 0 AND 9007199254740991),
 limited INTEGER NOT NULL DEFAULT 0 CHECK(limited BETWEEN 0 AND 9007199254740991),
 n_shards INTEGER NOT NULL DEFAULT 0 CHECK(n_shards BETWEEN 0 AND 9007199254740991),
 r_shards INTEGER NOT NULL DEFAULT 0 CHECK(r_shards BETWEEN 0 AND 9007199254740991),
 sr_shards INTEGER NOT NULL DEFAULT 0 CHECK(sr_shards BETWEEN 0 AND 9007199254740991),
 ssr_shards INTEGER NOT NULL DEFAULT 0 CHECK(ssr_shards BETWEEN 0 AND 9007199254740991),
 newcomer INTEGER NOT NULL DEFAULT 1 CHECK(newcomer IN (0,1)),
 checked_day TEXT NOT NULL DEFAULT '' CHECK(length(checked_day) IN (0,10)),
 month TEXT NOT NULL CHECK(length(month)=7),
 monthly_permanent INTEGER NOT NULL DEFAULT 0 CHECK(monthly_permanent BETWEEN 0 AND 9007199254740991),
 monthly_limited INTEGER NOT NULL DEFAULT 0 CHECK(monthly_limited BETWEEN 0 AND 9007199254740991),
 entered_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 limited_checked_day TEXT NOT NULL DEFAULT '' CHECK(length(limited_checked_day) IN (0,10)),
 welcome_granted INTEGER NOT NULL DEFAULT 1 CHECK(welcome_granted IN (0,1))
) STRICT;
INSERT INTO gacha_profiles(user_id,permanent,limited,n_shards,r_shards,sr_shards,ssr_shards,newcomer,checked_day,month,monthly_permanent,monthly_limited,entered_at,updated_at) SELECT user_id,permanent,limited,n_shards,r_shards,sr_shards,ssr_shards,newcomer,checked_day,month,monthly_permanent,monthly_limited,entered_at,updated_at FROM saved_reward_profiles;
DROP TABLE saved_reward_profiles;

CREATE TABLE reward_campaigns (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='rw_'),
 kind TEXT NOT NULL CHECK(kind IN ('broadcast','code')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 80),
 rewards TEXT NOT NULL CHECK(json_valid(rewards) AND length(rewards)<=16384),
 conditions TEXT NOT NULL CHECK(json_valid(conditions) AND length(conditions)<=16384),
 code TEXT UNIQUE CHECK(code IS NULL OR (length(code) BETWEEN 4 AND 64 AND code NOT GLOB '*[^A-Z0-9_-]*')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 quota INTEGER CHECK(quota IS NULL OR quota BETWEEN 1 AND 9007199254740991),
 starts_at INTEGER CHECK(starts_at IS NULL OR starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 1 AND 253402300799),
 claimed INTEGER NOT NULL DEFAULT 0 CHECK(claimed BETWEEN 0 AND 9007199254740991),
 matched INTEGER NOT NULL DEFAULT 0 CHECK(matched BETWEEN 0 AND 9007199254740991),
 skipped INTEGER NOT NULL DEFAULT 0 CHECK(skipped BETWEEN 0 AND 9007199254740991),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 key_hash BLOB NOT NULL UNIQUE CHECK(length(key_hash)=32),
 request_hash BLOB NOT NULL CHECK(length(request_hash)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((kind='code' AND code IS NOT NULL) OR (kind='broadcast' AND code IS NULL AND quota IS NULL AND starts_at IS NULL AND ends_at IS NULL)),
 CHECK(starts_at IS NULL OR ends_at IS NULL OR starts_at<ends_at)
) STRICT;
CREATE INDEX idx_reward_campaigns_created ON reward_campaigns(created_at,id);
CREATE TABLE reward_claims (
 id INTEGER PRIMARY KEY,
 campaign_id TEXT NOT NULL REFERENCES reward_campaigns(id),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','success','failed','skipped')),
 receipt TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(receipt) AND length(receipt)<=32768),
 failure TEXT NOT NULL DEFAULT '' CHECK(failure IN ('','capacity','delivery')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 completed_at INTEGER CHECK(completed_at IS NULL OR completed_at BETWEEN 0 AND 253402300799),
 UNIQUE(campaign_id,user_id)
) STRICT;
CREATE INDEX idx_reward_claims_work ON reward_claims(status,id);
CREATE INDEX idx_reward_claims_user ON reward_claims(user_id,id);
