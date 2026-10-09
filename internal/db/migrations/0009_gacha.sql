CREATE TEMP TABLE gacha_previous_audit_buckets AS SELECT * FROM economy_audit_buckets;
DROP TABLE economy_audit_buckets;
CREATE TABLE economy_audit_buckets (
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'),
 source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 32 AND source_type NOT GLOB '*[^a-z0-9_]*'),
 channel TEXT NOT NULL CHECK(channel IN ('admin','account','checkin','welfare','thursday','api','charity','donation','fishing','linklink','rps','bidding','likes','gwent','steadycatch','blackjack','onboarding','loan','picture_book','inactivity','penalty','fat_fish','lake_notes','gacha','unclassified')),
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
INSERT INTO economy_audit_buckets(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq) SELECT asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes,issued,reclaimed,user_income,user_expense,internal_transfer,operation_count,first_ledger_seq,last_ledger_seq FROM gacha_previous_audit_buckets;
DROP TABLE gacha_previous_audit_buckets;
CREATE INDEX idx_economy_audit_buckets_time ON economy_audit_buckets(asset_type,bucket,bucket_start,kind,source_type,channel);
DROP TRIGGER credit_operation_source_guard;
DROP TRIGGER credit_operation_source_update_guard;
DROP TRIGGER credit_operations_no_delete;
DROP TRIGGER credit_operations_no_update;
DROP TRIGGER generation_two_credit_operations_u128_guard;
DROP TRIGGER generation_two_credit_operations_u128_update_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_insert_guard;
DROP TRIGGER generation_two_integer_type_credit_operations_update_guard;
CREATE TEMP TABLE gacha_previous_credit_operations AS SELECT * FROM credit_operations;
DROP TABLE credit_operations;
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','gacha_ticket_purchase','gacha_theme_reward','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal','ai_ticket','ai_terminal','catch_ticket','catch_refund','catch_reward','blackjack_reserve','blackjack_settle','blackjack_release')), source_type TEXT NOT NULL CHECK(source_type IN ('image_task','operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session','duel_queue','duel_session','catch_session','blackjack_payment')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (donation_credit_user_id IS NOT NULL AND kind IN ('admin_user_adjustment','donor_reward','gacha_theme_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward','gacha_theme_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='catch_session' AND length(source_id)=25 AND substr(source_id,1,3)='sc_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_','gwt_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','gacha_ticket_purchase','gacha_theme_reward','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('catch_ticket','catch_refund','catch_reward') AND source_type='catch_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal','ai_ticket','ai_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted) SELECT id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,reason,created_at,compacted FROM gacha_previous_credit_operations;
DROP TABLE gacha_previous_credit_operations;
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
CREATE INDEX idx_credit_duel_terminal ON credit_operations(substr(source_id,1,4),ledger_seq) WHERE kind='duel_terminal' AND source_type='duel_session';
CREATE INDEX idx_credit_operations_created ON credit_operations(created_at,ledger_seq);
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
CREATE INDEX idx_credit_operations_source ON credit_operations(source_type,source_id,source_seq);
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

CREATE TABLE gacha_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 rules TEXT NOT NULL CHECK(json_valid(rules) AND length(rules)<=2048),
 shop TEXT NOT NULL CHECK(json_valid(shop) AND length(shop)<=4096)
) STRICT;
CREATE TABLE gacha_pools (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='gp_'),
 config TEXT NOT NULL CHECK(json_valid(config) AND length(config)<=32768),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE gacha_presets (
 id TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 config TEXT NOT NULL CHECK(json_valid(config) AND length(config)<=32768),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991)
) STRICT;
CREATE TABLE gacha_config_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 target TEXT NOT NULL CHECK(length(target) BETWEEN 1 AND 80),
 revision INTEGER NOT NULL,
 config TEXT NOT NULL CHECK(json_valid(config) AND length(config)<=32768),
 created_at INTEGER NOT NULL
) STRICT;
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
 updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE gacha_owned (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 card_id TEXT NOT NULL CHECK(length(card_id) BETWEEN 1 AND 80),
 obtained_at INTEGER NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('draw','shop')),
 PRIMARY KEY(user_id,card_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE gacha_pity (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL CHECK(group_id='permanent' OR length(group_id)=64),
 sr INTEGER NOT NULL CHECK(sr BETWEEN 0 AND 100),
 ssr INTEGER NOT NULL CHECK(ssr BETWEEN 0 AND 1000),
 sr_limit INTEGER NOT NULL CHECK(sr_limit BETWEEN 1 AND 100),
 ssr_limit INTEGER NOT NULL CHECK(ssr_limit BETWEEN 1 AND 1000),
 sr_featured INTEGER NOT NULL CHECK(sr_featured IN (0,1)),
 ssr_featured INTEGER NOT NULL CHECK(ssr_featured IN (0,1)),
 PRIMARY KEY(user_id,group_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE gacha_claims (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('theme','card')),
 target TEXT NOT NULL CHECK(length(target) BETWEEN 1 AND 80),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,kind,target)
) STRICT, WITHOUT ROWID;
CREATE TABLE gacha_operations (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_'),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 key_hash BLOB CHECK(key_hash IS NULL OR length(key_hash)=32),
 request_hash BLOB CHECK(request_hash IS NULL OR length(request_hash)=32),
 action TEXT NOT NULL CHECK(action IN ('enter','check-in','draw','buy','redeem','claim')),
 delta TEXT NOT NULL CHECK(json_valid(delta) AND length(delta)<=4096),
 balance_after TEXT NOT NULL CHECK(json_valid(balance_after) AND length(balance_after)<=4096),
 credit_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE UNIQUE INDEX idx_gacha_operation_key ON gacha_operations(user_id,key_hash) WHERE user_id IS NOT NULL;
CREATE INDEX idx_gacha_operation_user ON gacha_operations(user_id,created_at,id);
CREATE TABLE gacha_draws (
 operation_id TEXT PRIMARY KEY REFERENCES gacha_operations(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 receipt TEXT NOT NULL CHECK(json_valid(receipt) AND length(receipt)<=16384),
 rules TEXT NOT NULL CHECK(json_valid(rules) AND length(rules)<=2048),
 roster TEXT NOT NULL CHECK(json_valid(roster) AND length(roster)<=65536),
 catalog_version INTEGER NOT NULL,
 created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_gacha_draw_retention ON gacha_draws(created_at);
CREATE INDEX idx_gacha_draw_user ON gacha_draws(user_id,created_at DESC,operation_id DESC);
INSERT INTO gacha_settings(id,revision,rules,shop) VALUES(1,1,'{"weights":[5500,3500,900,100],"sr_pity":10,"ssr_pity":60,"sr_featured":5000,"ssr_featured":5000,"sr_guarantee":true,"ssr_guarantee":true}','{"permanent_price":"10000","limited_price":"10000","n_ticket":20,"r_ticket":10,"sr_ticket":5,"ssr_tickets":1,"permanent_cap":20,"limited_cap":10,"sr_card":10,"ssr_card":3,"chibi_n":30,"chibi_r":15,"revision":1}');
INSERT INTO gacha_presets(id,name,config,revision) VALUES('summer','夏夜花火','{"id":"","name":"夏夜花火","cover":"i","kind":"limited","group":"","rules":{"weights":[5500,3500,900,100],"sr_pity":10,"ssr_pity":60,"sr_featured":5000,"ssr_featured":5000,"sr_guarantee":true,"ssr_guarantee":true},"featured":["h-chatgpt","h-claude","h-glm","h-deepseek","h-gemini","i-chatgpt","i-claude","i-glm","i-deepseek","i-gemini"],"starts_at":null,"ends_at":null,"paused":false,"published":false,"revision":0}',1);
INSERT INTO gacha_presets(id,name,config,revision) VALUES('theatre','童话剧场','{"id":"","name":"童话剧场","cover":"j","kind":"limited","group":"","rules":{"weights":[5500,3500,900,100],"sr_pity":10,"ssr_pity":60,"sr_featured":5000,"ssr_featured":5000,"sr_guarantee":true,"ssr_guarantee":true},"featured":["h-chatgpt","h-claude","h-glm","h-deepseek","h-gemini","j-chatgpt","j-claude","j-glm","j-deepseek","j-gemini"],"starts_at":null,"ends_at":null,"paused":false,"published":false,"revision":0}',1);
