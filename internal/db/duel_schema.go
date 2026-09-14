package db

// The duel extension is activated together with its complete game modules.
// Currency balances and operation counters retain their wide ledger types.
const duelTablesSchema = `
CREATE TABLE game_duel_catalogs (
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64 AND content_hash NOT GLOB '*[^0-9a-f]*'),
 rules_version INTEGER NOT NULL CHECK(rules_version=1),
 design_version TEXT NOT NULL CHECK(length(design_version) BETWEEN 1 AND 32),
 schema_version INTEGER NOT NULL CHECK(schema_version BETWEEN 1 AND 32),
 catalog_json TEXT NOT NULL CHECK(typeof(catalog_json)='text' AND length(CAST(catalog_json AS BLOB))<=1048576 AND json_valid(catalog_json)),
 PRIMARY KEY(game_key,content_hash)
) STRICT;
CREATE TABLE game_duel_queue (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5) IN ('bidq_','likq_') AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,5)='bidq_') OR (game_key='likes' AND substr(id,1,5)='likq_')),
 mode TEXT NOT NULL CHECK((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard'))),
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
 loadout_json TEXT CHECK((game_key='bidding' AND loadout_json IS NULL) OR (game_key='likes' AND typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
CREATE INDEX idx_duel_queue_match ON game_duel_queue(game_key,mode,terms_hash,created_at,id);
CREATE INDEX idx_duel_queue_deadline ON game_duel_queue(game_key,deadline,id);
CREATE UNIQUE INDEX idx_duel_queue_user ON game_duel_queue(user_id,game_key);
CREATE TABLE game_duel_sessions (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4) IN ('bid_','lik_') AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,4)='bid_') OR (game_key='likes' AND substr(id,1,4)='lik_')),
 mode TEXT NOT NULL CHECK((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard'))),
 content_hash TEXT NOT NULL,
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 1 AND 9000000000000000),
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
 CHECK((state='active' AND ((game_key='bidding' AND phase IN ('joker','bid')) OR (game_key='likes' AND phase IN ('plan','settlement'))) AND phase_deadline IS NOT NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000001' AND terminal_at IS NULL AND delete_at IS NULL AND outcome IS NULL AND reason IS NULL AND winner_seat IS NULL AND score0 IS NULL AND score1 IS NULL AND prize_milli IS NULL AND platform_milli IS NULL AND welfare_milli IS NULL AND thursday_milli IS NULL AND terminal_operation_id IS NULL) OR (state='terminal' AND phase='terminal' AND phase_deadline IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000' AND terminal_at IS NOT NULL AND delete_at IS NOT NULL AND outcome IS NOT NULL AND reason IS NOT NULL AND score0 IS NOT NULL AND score1 IS NOT NULL AND prize_milli IS NOT NULL AND platform_milli IS NOT NULL AND welfare_milli IS NOT NULL AND thursday_milli IS NOT NULL AND terminal_operation_id IS NOT NULL)),
 CHECK(state='active' OR (outcome='decided' AND winner_seat IS NOT NULL AND reason NOT IN ('server_restart','account_unavailable') AND prize_milli+platform_milli+welfare_milli+thursday_milli=ticket_milli) OR (outcome IN ('draw','system_cancelled') AND winner_seat IS NULL AND prize_milli=0 AND platform_milli=0 AND welfare_milli=0 AND thursday_milli=0)),
 CHECK(state='active' OR (outcome='system_cancelled' AND reason IN ('server_restart','account_unavailable')) OR (outcome<>'system_cancelled' AND reason NOT IN ('server_restart','account_unavailable')))
) STRICT;
CREATE INDEX idx_duel_sessions_due ON game_duel_sessions(game_key,state,phase_deadline,id);
CREATE INDEX idx_duel_sessions_terminal ON game_duel_sessions(game_key,state,terminal_at,id);
CREATE INDEX idx_duel_sessions_expiry ON game_duel_sessions(game_key,state,delete_at,id);
CREATE TABLE game_duel_seats (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 seat_no INTEGER NOT NULL CHECK(seat_no IN (0,1)),
 user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
 general_paid_milli INTEGER NOT NULL CHECK(typeof(general_paid_milli)='integer' AND general_paid_milli BETWEEN 0 AND 9000000000000000),
 game_paid_milli INTEGER NOT NULL CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND 9000000000000000),
 loadout_json TEXT CHECK(loadout_json IS NULL OR (typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 current_plan_json TEXT CHECK(current_plan_json IS NULL OR (typeof(current_plan_json)='text' AND length(CAST(current_plan_json AS BLOB))<=16384 AND json_valid(current_plan_json))),
 locked INTEGER NOT NULL CHECK(locked IN (0,1)),
 timeout_count INTEGER NOT NULL CHECK(timeout_count BETWEEN 0 AND 150),
 PRIMARY KEY(session_id,seat_no), UNIQUE(session_id,user_id),
 CHECK((locked=0 AND current_plan_json IS NULL) OR (locked=1 AND current_plan_json IS NOT NULL))
) STRICT;
CREATE INDEX idx_duel_seats_user ON game_duel_seats(user_id,session_id);
CREATE TABLE game_duel_user_slots (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 queue_id TEXT UNIQUE REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 session_id TEXT REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 PRIMARY KEY(user_id,game_key),
 CHECK((queue_id IS NULL)<>(session_id IS NULL))
) STRICT;
CREATE TABLE game_duel_rounds (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 round_no INTEGER NOT NULL CHECK(round_no BETWEEN 1 AND 75),
 record_json TEXT NOT NULL CHECK(typeof(record_json)='text' AND length(CAST(record_json AS BLOB))<=1048576 AND json_valid(record_json)),
 PRIMARY KEY(session_id,round_no)
) STRICT;
CREATE TABLE game_duel_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='dah_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 mode TEXT NOT NULL CHECK((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard'))),
 content_hash TEXT NOT NULL,
 header_json TEXT NOT NULL CHECK(typeof(header_json)='text' AND length(CAST(header_json AS BLOB))<=1048576 AND json_valid(header_json)),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
CREATE INDEX idx_duel_anonymous_export ON game_duel_anonymous(game_key,mode,export_seq);
CREATE TABLE game_duel_anonymous_rounds (
 archive_id TEXT NOT NULL REFERENCES game_duel_anonymous(archive_id) ON DELETE CASCADE,
 round_no INTEGER NOT NULL CHECK(round_no BETWEEN 1 AND 75),
 record_json TEXT NOT NULL CHECK(typeof(record_json)='text' AND length(CAST(record_json AS BLOB))<=1048576 AND json_valid(record_json)),
 PRIMARY KEY(archive_id,round_no)
) STRICT;
CREATE TRIGGER game_duel_queue_delete_guard BEFORE DELETE ON game_duel_queue WHEN hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel queue still reserves capacity'); END;
CREATE TRIGGER game_duel_session_delete_guard BEFORE DELETE ON game_duel_sessions WHEN OLD.state<>'terminal' OR hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel session still active'); END;
CREATE TRIGGER game_duel_terminal_immutable BEFORE UPDATE ON game_duel_sessions WHEN OLD.state='terminal' BEGIN SELECT RAISE(ABORT,'duel result immutable'); END;
CREATE TRIGGER game_duel_round_immutable BEFORE UPDATE ON game_duel_rounds BEGIN SELECT RAISE(ABORT,'duel round immutable'); END;
CREATE TRIGGER game_duel_catalog_immutable BEFORE UPDATE ON game_duel_catalogs BEGIN SELECT RAISE(ABORT,'duel catalog immutable'); END;
CREATE TRIGGER game_duel_seat_payment_insert BEFORE INSERT ON game_duel_seats WHEN NEW.general_paid_milli+NEW.game_paid_milli<>(SELECT ticket_milli FROM game_duel_sessions WHERE id=NEW.session_id) BEGIN SELECT RAISE(ABORT,'duel payment mismatch'); END;
CREATE TRIGGER game_duel_seat_payment_update BEFORE UPDATE ON game_duel_seats WHEN NEW.session_id<>OLD.session_id OR NEW.seat_no<>OLD.seat_no OR NEW.general_paid_milli<>OLD.general_paid_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.loadout_json IS NOT OLD.loadout_json OR (OLD.user_id IS NULL AND NEW.user_id IS NOT NULL) OR (OLD.user_id IS NOT NULL AND NEW.user_id IS NOT NULL AND NEW.user_id<>OLD.user_id) BEGIN SELECT RAISE(ABORT,'duel seat immutable'); END;
CREATE TRIGGER game_duel_user_delete_guard BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=OLD.id) OR EXISTS(SELECT 1 FROM game_duel_seats WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'duel user handoff required'); END;
CREATE TRIGGER game_duel_queue_frozen BEFORE UPDATE ON game_duel_queue WHEN NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.user_id<>OLD.user_id OR NEW.revision<>OLD.revision OR NEW.created_at<>OLD.created_at OR NEW.deadline<>OLD.deadline OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.reservation_operation_id<>OLD.reservation_operation_id OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.device_hash<>OLD.device_hash OR NEW.ip_hash<>OLD.ip_hash OR NEW.loadout_json IS NOT OLD.loadout_json OR NEW.ledger_rows_remaining>OLD.ledger_rows_remaining BEGIN SELECT RAISE(ABORT,'duel queue terms immutable'); END;
CREATE TRIGGER game_duel_session_frozen BEFORE UPDATE ON game_duel_sessions WHEN NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR NEW.started_at<>OLD.started_at OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.initial_state_json<>OLD.initial_state_json OR NEW.phase_seq<OLD.phase_seq OR NEW.revision<=OLD.revision BEGIN SELECT RAISE(ABORT,'duel session terms immutable'); END;
CREATE TRIGGER game_duel_queue_accounts BEFORE INSERT ON game_duel_queue WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-queue:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-queue:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel queue account mismatch'); END;
CREATE TRIGGER game_duel_session_accounts BEFORE INSERT ON game_duel_sessions WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-session:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-session:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel session account mismatch'); END;
CREATE TRIGGER game_duel_slot_insert BEFORE INSERT ON game_duel_user_slots WHEN (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_slot_update BEFORE UPDATE ON game_duel_user_slots WHEN NEW.user_id<>OLD.user_id OR NEW.game_key<>OLD.game_key OR (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_seat_deidentify BEFORE UPDATE OF user_id ON game_duel_seats WHEN NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND (SELECT state FROM game_duel_sessions WHERE id=NEW.session_id)<>'terminal' BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_user_ban_guard BEFORE UPDATE OF is_banned,banned_until ON users WHEN NEW.is_banned=1 AND (NEW.is_banned<>OLD.is_banned OR NEW.banned_until IS NOT OLD.banned_until) AND EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=NEW.id) BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_anonymous_immutable BEFORE UPDATE ON game_duel_anonymous BEGIN SELECT RAISE(ABORT,'duel archive immutable'); END;
CREATE TRIGGER game_duel_anonymous_round_immutable BEFORE UPDATE ON game_duel_anonymous_rounds BEGIN SELECT RAISE(ABORT,'duel archive round immutable'); END;
`
