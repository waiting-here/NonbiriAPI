package db

const fatFishSchema = `
CREATE TABLE fatfish_levels (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffl_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=4096),
 draft_json TEXT NOT NULL CHECK(json_valid(draft_json) AND json_type(draft_json)='object' AND length(CAST(draft_json AS BLOB))<=262144),
 revision INTEGER NOT NULL CHECK(revision>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE TABLE fatfish_level_versions (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffv_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 level_id TEXT NOT NULL REFERENCES fatfish_levels(id),
 content_hash BLOB NOT NULL CHECK(length(content_hash)=32),
 engine_version INTEGER NOT NULL CHECK(engine_version=1),
 scoring_version INTEGER NOT NULL CHECK(scoring_version=1),
 content_json TEXT NOT NULL CHECK(json_valid(content_json) AND json_type(content_json)='object' AND length(CAST(content_json AS BLOB))<=262144),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 10 AND 600),
 maximum_stars INTEGER NOT NULL CHECK(maximum_stars BETWEEN 1 AND 3),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 UNIQUE(level_id,content_hash,engine_version,scoring_version)
) STRICT;
CREATE TRIGGER fatfish_content_immutable BEFORE UPDATE ON fatfish_level_versions
BEGIN SELECT RAISE(ABORT,'published game content is immutable'); END;
CREATE TABLE fatfish_periods (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffp_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=8192),
 state TEXT NOT NULL CHECK(state IN ('draft','open','closed')),
 visible INTEGER NOT NULL DEFAULT 0 CHECK(visible IN (0,1)),
 paused INTEGER NOT NULL DEFAULT 0 CHECK(paused IN (0,1)),
 past_public INTEGER NOT NULL DEFAULT 0 CHECK(past_public IN (0,1)),
 starts_at INTEGER NOT NULL CHECK(starts_at BETWEEN 0 AND 253402300798),
 ends_at INTEGER NOT NULL CHECK(ends_at>starts_at AND ends_at<=253402300799),
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE UNIQUE INDEX idx_fatfish_accepting_period ON fatfish_periods(state) WHERE state='open';
CREATE INDEX idx_fatfish_period_times ON fatfish_periods(state,starts_at,ends_at,id);
CREATE TABLE fatfish_nodes (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffn_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 period_id TEXT NOT NULL REFERENCES fatfish_periods(id),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=4096),
 map_x INTEGER NOT NULL CHECK(map_x BETWEEN -1000000 AND 1000000),
 map_y INTEGER NOT NULL CHECK(map_y BETWEEN -1000000 AND 1000000),
 ord INTEGER NOT NULL CHECK(ord BETWEEN 0 AND 127),
 current_revision INTEGER NOT NULL CHECK(current_revision>0),
 UNIQUE(period_id,id),
 FOREIGN KEY(id,current_revision) REFERENCES fatfish_node_revisions(node_id,revision) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX idx_fatfish_nodes_period ON fatfish_nodes(period_id,ord,id);
CREATE TRIGGER fatfish_node_capacity BEFORE INSERT ON fatfish_nodes
WHEN (SELECT count(*) FROM fatfish_nodes WHERE period_id=NEW.period_id)>=128
BEGIN SELECT RAISE(ABORT,'period node capacity exhausted'); END;
CREATE TRIGGER fatfish_node_identity BEFORE UPDATE OF id,period_id ON fatfish_nodes
WHEN NEW.id<>OLD.id OR NEW.period_id<>OLD.period_id
BEGIN SELECT RAISE(ABORT,'period node identity is immutable'); END;
CREATE TABLE fatfish_node_revisions (
 node_id TEXT NOT NULL REFERENCES fatfish_nodes(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 condition_json TEXT NOT NULL CHECK(json_valid(condition_json) AND json_type(condition_json)='object' AND length(CAST(condition_json AS BLOB))<=32768),
 hidden_until_eligible INTEGER NOT NULL CHECK(hidden_until_eligible IN (0,1)),
 unlock_cost_mag BLOB NOT NULL CHECK(length(unlock_cost_mag)=16),
 ticket_price_mag BLOB NOT NULL CHECK(length(ticket_price_mag)=16),
 first_clear_reward_mag BLOB NOT NULL CHECK(length(first_clear_reward_mag)=16),
 star1_reward_mag BLOB NOT NULL CHECK(length(star1_reward_mag)=16),
 star2_reward_mag BLOB NOT NULL CHECK(length(star2_reward_mag)=16),
 star3_reward_mag BLOB NOT NULL CHECK(length(star3_reward_mag)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(node_id,revision)
) STRICT;
CREATE TRIGGER fatfish_node_revision_immutable BEFORE UPDATE ON fatfish_node_revisions
BEGIN SELECT RAISE(ABORT,'node configuration revision is immutable'); END;
CREATE TABLE fatfish_capacity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 active_challenges INTEGER NOT NULL CHECK(active_challenges BETWEEN 0 AND 10000),
 summary_rows INTEGER NOT NULL CHECK(summary_rows BETWEEN 0 AND 1000000)
) STRICT;
CREATE TABLE fatfish_challenges (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 playtest INTEGER NOT NULL CHECK(playtest IN (0,1)),
 period_id TEXT,
 node_id TEXT,
 node_revision INTEGER,
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 tab_capability_hash BLOB NOT NULL CHECK(length(tab_capability_hash)=32),
 seed BLOB NOT NULL CHECK(length(seed)=32),
 seed_commit BLOB NOT NULL CHECK(length(seed_commit)=32),
 state TEXT NOT NULL CHECK(state IN ('prepared','active','verifying','settled_pass','settled_fail','abandoned','expired','cancelled_refunded')),
 prepared_at_ms INTEGER NOT NULL CHECK(prepared_at_ms BETWEEN 0 AND 253402298300000),
 prepare_until_ms INTEGER NOT NULL CHECK(prepare_until_ms=prepared_at_ms+60000),
 start_at_ms INTEGER,
 end_at_ms INTEGER,
 submit_until_ms INTEGER,
 ticket_price_mag BLOB NOT NULL CHECK(length(ticket_price_mag)=16),
 ticket_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 input_digest BLOB CHECK(input_digest IS NULL OR length(input_digest)=32),
 received_at_ms INTEGER,
 verified_result_json TEXT CHECK(verified_result_json IS NULL OR (json_valid(verified_result_json) AND json_type(verified_result_json)='object' AND length(CAST(verified_result_json AS BLOB))<=16384)),
 terminal_at_ms INTEGER CHECK(terminal_at_ms BETWEEN prepared_at_ms AND 253402300799000),
 terminal_reason TEXT NOT NULL DEFAULT '' CHECK(length(CAST(terminal_reason AS BLOB))<=128),
 refund_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 FOREIGN KEY(period_id,node_id) REFERENCES fatfish_nodes(period_id,id),
 FOREIGN KEY(node_id,node_revision) REFERENCES fatfish_node_revisions(node_id,revision),
 CHECK((playtest=1 AND period_id IS NULL AND node_id IS NULL AND node_revision IS NULL AND ticket_price_mag=zeroblob(16) AND ticket_operation_id IS NULL AND refund_operation_id IS NULL)
 OR (playtest=0 AND period_id IS NOT NULL AND node_id IS NOT NULL AND node_revision IS NOT NULL)),
 CHECK((start_at_ms IS NULL AND end_at_ms IS NULL AND submit_until_ms IS NULL)
 OR (start_at_ms IS NOT NULL AND end_at_ms IS NOT NULL AND submit_until_ms IS NOT NULL
 AND start_at_ms>=prepared_at_ms+3000 AND start_at_ms<=prepare_until_ms+3000
 AND end_at_ms BETWEEN start_at_ms+10000 AND start_at_ms+600000
 AND submit_until_ms=end_at_ms+1800000)),
 CHECK((input_digest IS NULL)=(received_at_ms IS NULL)),
 CHECK(input_digest IS NULL OR start_at_ms IS NOT NULL),
 CHECK(verified_result_json IS NULL OR input_digest IS NOT NULL),
 CHECK(received_at_ms IS NULL OR (received_at_ms>=start_at_ms-3000 AND received_at_ms<=submit_until_ms)),
 CHECK(state<>'prepared' OR (start_at_ms IS NULL AND input_digest IS NULL AND terminal_at_ms IS NULL)),
 CHECK(state NOT IN ('active','verifying','settled_pass','settled_fail') OR start_at_ms IS NOT NULL),
 CHECK(state<>'active' OR (input_digest IS NULL AND terminal_at_ms IS NULL)),
 CHECK(state<>'verifying' OR (input_digest IS NOT NULL AND terminal_at_ms IS NULL)),
 CHECK((state IN ('prepared','active','verifying'))=(terminal_at_ms IS NULL)),
 CHECK(state NOT IN ('settled_pass','settled_fail') OR (input_digest IS NOT NULL AND verified_result_json IS NOT NULL)),
 CHECK(start_at_ms IS NOT NULL OR ticket_operation_id IS NULL),
 CHECK(start_at_ms IS NULL OR (ticket_price_mag=zeroblob(16))=(ticket_operation_id IS NULL)),
 CHECK(refund_operation_id IS NULL OR state='cancelled_refunded'),
 CHECK(state<>'cancelled_refunded' OR (start_at_ms IS NOT NULL AND (ticket_price_mag=zeroblob(16))=(refund_operation_id IS NULL)))
) STRICT;
CREATE UNIQUE INDEX idx_fatfish_user_active ON fatfish_challenges(user_id) WHERE state IN ('prepared','active','verifying');
CREATE INDEX idx_fatfish_challenge_deadline ON fatfish_challenges(state,prepare_until_ms,submit_until_ms,id);
CREATE INDEX idx_fatfish_challenge_retention ON fatfish_challenges(terminal_at_ms,id) WHERE terminal_at_ms IS NOT NULL;
CREATE INDEX idx_fatfish_challenge_history ON fatfish_challenges(user_id,prepared_at_ms DESC,id);
CREATE INDEX idx_fatfish_challenge_node ON fatfish_challenges(node_id,state,id);
CREATE TRIGGER fatfish_challenge_capacity BEFORE INSERT ON fatfish_challenges
WHEN (SELECT summary_rows FROM fatfish_capacity WHERE id=1)>=1000000
 OR (NEW.state IN ('prepared','active','verifying') AND (SELECT active_challenges FROM fatfish_capacity WHERE id=1)>=10000)
BEGIN SELECT RAISE(ABORT,'game challenge capacity exhausted'); END;
CREATE TRIGGER fatfish_challenge_count_insert AFTER INSERT ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET summary_rows=summary_rows+1,active_challenges=active_challenges+(NEW.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_count_delete AFTER DELETE ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET summary_rows=summary_rows-1,active_challenges=active_challenges-(OLD.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_count_update AFTER UPDATE OF state ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET active_challenges=active_challenges+(NEW.state IN ('prepared','active','verifying'))-(OLD.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_transition BEFORE UPDATE ON fatfish_challenges
WHEN NEW.id<>OLD.id OR NEW.user_id<>OLD.user_id OR NEW.playtest<>OLD.playtest
 OR NEW.period_id IS NOT OLD.period_id OR NEW.node_id IS NOT OLD.node_id OR NEW.node_revision IS NOT OLD.node_revision
 OR NEW.version_id<>OLD.version_id OR NEW.tab_capability_hash<>OLD.tab_capability_hash OR NEW.seed<>OLD.seed OR NEW.seed_commit<>OLD.seed_commit
 OR NEW.prepared_at_ms<>OLD.prepared_at_ms OR NEW.prepare_until_ms<>OLD.prepare_until_ms OR NEW.ticket_price_mag<>OLD.ticket_price_mag
 OR (OLD.start_at_ms IS NOT NULL AND (NEW.start_at_ms IS NOT OLD.start_at_ms OR NEW.end_at_ms IS NOT OLD.end_at_ms OR NEW.submit_until_ms IS NOT OLD.submit_until_ms OR NEW.ticket_operation_id IS NOT OLD.ticket_operation_id))
 OR (OLD.input_digest IS NOT NULL AND (NEW.input_digest IS NOT OLD.input_digest OR NEW.received_at_ms IS NOT OLD.received_at_ms))
 OR (OLD.verified_result_json IS NOT NULL AND NEW.verified_result_json IS NOT OLD.verified_result_json)
 OR NEW.revision<=OLD.revision
 OR NOT ((OLD.state='prepared' AND NEW.state IN ('prepared','active','abandoned','expired'))
 OR (OLD.state='active' AND NEW.state IN ('active','verifying','abandoned','expired','cancelled_refunded'))
 OR (OLD.state='verifying' AND NEW.state IN ('verifying','settled_pass','settled_fail','abandoned','cancelled_refunded')))
BEGIN SELECT RAISE(ABORT,'invalid game challenge transition'); END;
CREATE TABLE fatfish_progress (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT NOT NULL,
 node_id TEXT NOT NULL,
 unlock_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 unlocked_at INTEGER NOT NULL CHECK(unlocked_at BETWEEN 0 AND 253402300799),
 passed INTEGER NOT NULL DEFAULT 0 CHECK(passed IN (0,1)),
 best_stars INTEGER NOT NULL DEFAULT 0 CHECK(best_stars BETWEEN 0 AND 3),
 best_score_units INTEGER NOT NULL DEFAULT 0 CHECK(best_score_units BETWEEN 0 AND 100000000),
 best_version_id TEXT REFERENCES fatfish_level_versions(id),
 best_at_ms INTEGER CHECK(best_at_ms BETWEEN 0 AND 253402300799000),
 best_challenge_id TEXT CHECK(length(best_challenge_id)=26 AND substr(best_challenge_id,1,4)='ffc_'),
 best_confirmed_at_ms INTEGER CHECK(best_confirmed_at_ms BETWEEN 0 AND 253402300799000),
 PRIMARY KEY(user_id,period_id,node_id),
 FOREIGN KEY(period_id,node_id) REFERENCES fatfish_nodes(period_id,id),
 CHECK((passed=0 AND best_stars=0 AND best_score_units=0 AND best_version_id IS NULL AND best_at_ms IS NULL AND best_challenge_id IS NULL AND best_confirmed_at_ms IS NULL)
 OR (passed=1 AND best_stars BETWEEN 1 AND 3 AND best_score_units>0 AND best_version_id IS NOT NULL AND best_at_ms IS NOT NULL AND best_challenge_id IS NOT NULL AND best_confirmed_at_ms>=best_at_ms))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fatfish_progress_board ON fatfish_progress(period_id,node_id,best_score_units DESC,best_at_ms,user_id);
CREATE TRIGGER fatfish_progress_monotone BEFORE UPDATE ON fatfish_progress
WHEN NEW.user_id<>OLD.user_id OR NEW.period_id<>OLD.period_id OR NEW.node_id<>OLD.node_id
 OR NEW.unlocked_at<>OLD.unlocked_at OR NEW.unlock_operation_id IS NOT OLD.unlock_operation_id
 OR NEW.passed<OLD.passed OR NEW.best_stars<OLD.best_stars OR NEW.best_score_units<OLD.best_score_units
 OR (OLD.passed=1 AND NEW.best_score_units=OLD.best_score_units AND (NEW.best_version_id IS NOT OLD.best_version_id
 OR NEW.best_at_ms IS NOT OLD.best_at_ms OR NEW.best_challenge_id IS NOT OLD.best_challenge_id OR NEW.best_confirmed_at_ms IS NOT OLD.best_confirmed_at_ms))
BEGIN SELECT RAISE(ABORT,'game progress cannot regress'); END;
CREATE TABLE fatfish_period_progress (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT NOT NULL REFERENCES fatfish_periods(id),
 total_score_units INTEGER NOT NULL CHECK(total_score_units BETWEEN 0 AND 12800000000),
 achieved_at_ms INTEGER NOT NULL CHECK(achieved_at_ms BETWEEN 0 AND 253402300799000),
 public_tie_key BLOB NOT NULL CHECK(length(public_tie_key)=16),
 PRIMARY KEY(user_id,period_id),
 UNIQUE(period_id,public_tie_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fatfish_period_leaderboard ON fatfish_period_progress(period_id,total_score_units DESC,achieved_at_ms,public_tie_key);
CREATE TRIGGER fatfish_period_progress_monotone BEFORE UPDATE ON fatfish_period_progress
WHEN NEW.user_id<>OLD.user_id OR NEW.period_id<>OLD.period_id OR NEW.public_tie_key<>OLD.public_tie_key
 OR NEW.total_score_units<OLD.total_score_units OR NEW.achieved_at_ms<OLD.achieved_at_ms
 OR (NEW.total_score_units=OLD.total_score_units AND NEW.achieved_at_ms<>OLD.achieved_at_ms)
BEGIN SELECT RAISE(ABORT,'period score cannot regress'); END;
CREATE TABLE fatfish_reward_claims (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 period_id TEXT NOT NULL CHECK(length(period_id)=26),
 node_id TEXT NOT NULL CHECK(length(node_id)=26),
 tier TEXT NOT NULL CHECK(tier IN ('first_clear','star1','star2','star3')),
 operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(identity_key,period_id,node_id,tier),
 CHECK((amount_mag=zeroblob(16))=(operation_id IS NULL))
) STRICT, WITHOUT ROWID;
CREATE TRIGGER fatfish_reward_claim_immutable BEFORE UPDATE ON fatfish_reward_claims
BEGIN SELECT RAISE(ABORT,'game reward claim is immutable'); END;
CREATE TABLE fatfish_financial_receipts (
 receipt_key TEXT PRIMARY KEY CHECK(length(CAST(receipt_key AS BLOB)) BETWEEN 1 AND 128),
 kind TEXT NOT NULL CHECK(kind IN ('unlock','ticket','refund')),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16 AND amount_mag<>zeroblob(16)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER fatfish_financial_receipt_immutable BEFORE UPDATE ON fatfish_financial_receipts
BEGIN SELECT RAISE(ABORT,'game payment receipt is immutable'); END;
CREATE TABLE fatfish_playtests (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='fpt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 challenge_id TEXT UNIQUE REFERENCES fatfish_challenges(id) ON DELETE SET NULL,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 passed INTEGER NOT NULL CHECK(passed IN (0,1)),
 stars INTEGER NOT NULL CHECK(stars BETWEEN 0 AND 3),
 score_units INTEGER NOT NULL CHECK(score_units BETWEEN 0 AND 100000000),
 duration_ms INTEGER NOT NULL CHECK(duration_ms BETWEEN 0 AND 5000),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 CHECK((passed=1)=(stars>=1)),
 CHECK((passed=1)=(score_units>0))
) STRICT;
CREATE INDEX idx_fatfish_playtests_passed ON fatfish_playtests(version_id,passed,created_at DESC);
`
