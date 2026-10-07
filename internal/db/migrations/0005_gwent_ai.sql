DROP TRIGGER game_duel_session_delete_guard;
DROP TRIGGER game_duel_terminal_immutable;
DROP TRIGGER game_duel_seat_payment_insert;
DROP TRIGGER game_duel_session_frozen;
DROP TRIGGER game_duel_session_accounts;
DROP TRIGGER game_duel_slot_insert;
DROP TRIGGER game_duel_slot_update;
DROP TRIGGER game_duel_seat_deidentify;
DROP TRIGGER game_duel_anonymous_immutable;
DROP TRIGGER game_duel_anonymous_round_immutable;
DROP TRIGGER onboarding_hold_parent_insert;
DROP TRIGGER onboarding_hold_parent_update;
DROP TRIGGER game_duel_history_terminal;
CREATE TEMP TABLE gwent_ai_sequence AS SELECT name,seq FROM sqlite_sequence WHERE name IN ('game_duel_sessions','game_duel_anonymous','game_ai_queue');
CREATE TEMP TABLE gwent_ai_game_duel_sessions AS SELECT * FROM game_duel_sessions;
DROP TABLE game_duel_sessions;
CREATE TABLE game_duel_sessions (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4) IN ('bid_','lik_','gwt_') AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,4)='bid_') OR (game_key='likes' AND substr(id,1,4)='lik_') OR (game_key='gwent' AND substr(id,1,4)='gwt_')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key IN ('bidding','gwent') AND mode='ai')),
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
INSERT INTO game_duel_sessions SELECT * FROM gwent_ai_game_duel_sessions;
DROP TABLE gwent_ai_game_duel_sessions;
CREATE INDEX idx_duel_sessions_due ON game_duel_sessions(game_key,state,phase_deadline,id);
CREATE INDEX idx_duel_sessions_terminal ON game_duel_sessions(game_key,state,terminal_at,id);
CREATE INDEX idx_duel_sessions_expiry ON game_duel_sessions(game_key,state,delete_at,id);
CREATE TEMP TABLE gwent_ai_game_duel_anonymous AS SELECT * FROM game_duel_anonymous;
DROP TABLE game_duel_anonymous;
CREATE TABLE game_duel_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='dah_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key IN ('bidding','gwent') AND mode='ai')),
 content_hash TEXT NOT NULL,
 header_json TEXT NOT NULL CHECK(typeof(header_json)='text' AND length(CAST(header_json AS BLOB))<=1048576 AND json_valid(header_json)),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
INSERT INTO game_duel_anonymous SELECT * FROM gwent_ai_game_duel_anonymous;
DROP TABLE gwent_ai_game_duel_anonymous;
CREATE INDEX idx_duel_anonymous_export ON game_duel_anonymous(game_key,mode,export_seq);
CREATE INDEX idx_duel_anonymous_archive ON game_duel_anonymous(game_key,archive_id);
CREATE TEMP TABLE gwent_ai_game_ai_queue AS SELECT * FROM game_ai_queue;
DROP TABLE game_ai_queue;
CREATE TABLE game_ai_queue (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND ((game_key='bidding' AND substr(id,1,4)='aiq_') OR (game_key='gwent' AND substr(id,1,4)='gaq_'))),
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
INSERT INTO game_ai_queue SELECT * FROM gwent_ai_game_ai_queue;
DROP TABLE gwent_ai_game_ai_queue;
CREATE INDEX idx_ai_queue_game ON game_ai_queue(game_key,ordinal);
DELETE FROM sqlite_sequence WHERE name IN ('game_duel_sessions','game_duel_anonymous','game_ai_queue');
INSERT INTO sqlite_sequence SELECT * FROM gwent_ai_sequence;
DROP TABLE gwent_ai_sequence;
CREATE TRIGGER game_duel_session_delete_guard BEFORE DELETE ON game_duel_sessions WHEN OLD.state<>'terminal' OR hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel session still active'); END;
CREATE TRIGGER game_duel_terminal_immutable BEFORE UPDATE ON game_duel_sessions WHEN OLD.state='terminal' BEGIN SELECT RAISE(ABORT,'duel result immutable'); END;
CREATE TRIGGER game_duel_seat_payment_insert BEFORE INSERT ON game_duel_seats WHEN (NEW.participant_kind='human' AND NEW.general_paid_milli+NEW.game_paid_milli<>(SELECT ticket_milli FROM game_duel_sessions WHERE id=NEW.session_id)) OR (NEW.participant_kind='bot' AND NOT EXISTS(SELECT 1 FROM game_duel_sessions WHERE id=NEW.session_id AND economy='ai_challenge')) BEGIN SELECT RAISE(ABORT,'duel payment mismatch'); END;
CREATE TRIGGER game_duel_session_frozen BEFORE UPDATE ON game_duel_sessions WHEN NEW.economy<>OLD.economy OR NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR NEW.started_at<>OLD.started_at OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.initial_state_json<>OLD.initial_state_json OR NEW.phase_seq<OLD.phase_seq OR NEW.revision<=OLD.revision BEGIN SELECT RAISE(ABORT,'duel session terms immutable'); END;
CREATE TRIGGER game_duel_session_accounts BEFORE INSERT ON game_duel_sessions WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-session:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-session:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel session account mismatch'); END;
CREATE TRIGGER game_duel_slot_insert BEFORE INSERT ON game_duel_user_slots WHEN (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_slot_update BEFORE UPDATE ON game_duel_user_slots WHEN NEW.user_id<>OLD.user_id OR NEW.game_key<>OLD.game_key OR (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_seat_deidentify BEFORE UPDATE OF user_id ON game_duel_seats WHEN NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND (SELECT state FROM game_duel_sessions WHERE id=NEW.session_id)<>'terminal' BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_anonymous_immutable BEFORE UPDATE ON game_duel_anonymous BEGIN SELECT RAISE(ABORT,'duel archive immutable'); END;
CREATE TRIGGER game_duel_anonymous_round_immutable BEFORE UPDATE ON game_duel_anonymous_rounds BEGIN SELECT RAISE(ABORT,'duel archive round immutable'); END;
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
CREATE TRIGGER game_duel_history_terminal AFTER UPDATE OF state ON game_duel_sessions
WHEN NEW.state='terminal' AND OLD.state='active' BEGIN
 INSERT INTO game_duel_history(game_key,session_id) VALUES(NEW.game_key,NEW.id);
END;
INSERT INTO game_ai_settings(game_key) VALUES('gwent');
