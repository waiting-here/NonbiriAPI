package db

const blackjackTablesSchema = `
CREATE TABLE game_blackjack_clock (
 id INTEGER PRIMARY KEY CHECK(id=1),
 observed_at INTEGER NOT NULL CHECK(observed_at BETWEEN 0 AND 253399708739)
) STRICT;
INSERT INTO game_blackjack_clock(id,observed_at) VALUES(1,0);
CREATE TABLE game_blackjack_sessions (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='bjt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 started_at INTEGER NOT NULL UNIQUE CHECK(started_at BETWEEN 0 AND 253399708739 AND started_at%60=0),
 phase TEXT NOT NULL CHECK(phase IN ('seating','decision','result','cancelled')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 last_batch INTEGER NOT NULL CHECK(last_batch BETWEEN started_at AND started_at+45),
 state_json TEXT CHECK(state_json IS NULL OR (json_valid(state_json) AND length(CAST(state_json AS BLOB))<=32768)),
 view_json TEXT CHECK(view_json IS NULL OR (json_valid(view_json) AND length(CAST(view_json AS BLOB))<=24576)),
 terminal_at INTEGER CHECK(terminal_at BETWEEN started_at AND 253399708739),
 reason TEXT CHECK(reason IN ('completed','server_restart','closed','maintenance')),
 CHECK((phase='seating' AND state_json IS NULL AND view_json IS NULL AND terminal_at IS NULL AND reason IS NULL)
 OR (phase='decision' AND state_json IS NOT NULL AND view_json IS NOT NULL AND terminal_at IS NULL AND reason IS NULL)
 OR (phase IN ('result','cancelled') AND state_json IS NULL AND view_json IS NOT NULL AND terminal_at IS NOT NULL AND reason IS NOT NULL)),
 CHECK(phase<>'result' OR reason='completed'),
 CHECK(phase<>'cancelled' OR reason<>'completed')
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_single_table ON game_blackjack_sessions((1)) WHERE phase IN ('seating','decision');
CREATE INDEX idx_blackjack_sessions_recent ON game_blackjack_sessions(started_at DESC,id);
CREATE INDEX idx_blackjack_sessions_retention ON game_blackjack_sessions(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE TABLE game_blackjack_entries (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='bjq_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
 state TEXT NOT NULL CHECK(state IN ('waiting','seated','playing','settled','released')),
 stake_milli INTEGER NOT NULL CHECK(stake_milli BETWEEN 1 AND 140625000000000),
 platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999),
 welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999),
 thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708739),
 resolved_at INTEGER CHECK(resolved_at BETWEEN created_at AND 253399708739),
 session_id TEXT REFERENCES game_blackjack_sessions(id) ON DELETE RESTRICT,
 seat_no INTEGER CHECK(seat_no BETWEEN 0 AND 8),
 pending_json TEXT CHECK(pending_json IS NULL OR (json_valid(pending_json) AND length(CAST(pending_json AS BLOB))<=512)),
 pending_batch INTEGER CHECK(pending_batch BETWEEN 0 AND 253399708739),
 stopped INTEGER NOT NULL DEFAULT 0 CHECK(stopped IN (0,1)),
 emote TEXT CHECK(emote IN ('hello','nice','wow','good_luck','thanks','gg')),
 emote_at INTEGER CHECK(emote_at BETWEEN 0 AND 253399708739),
 CHECK((emote IS NULL)=(emote_at IS NULL)),
 CHECK((pending_json IS NULL)=(pending_batch IS NULL)),
 CHECK(pending_json IS NULL OR state='playing'),
 CHECK(user_id IS NOT NULL OR state IN ('playing','settled','released')),
 CHECK((state='waiting' AND session_id IS NULL AND seat_no IS NULL AND resolved_at IS NULL)
 OR (state IN ('seated','playing') AND session_id IS NOT NULL AND seat_no IS NOT NULL AND resolved_at IS NULL)
 OR (state='settled' AND session_id IS NOT NULL AND seat_no IS NOT NULL AND resolved_at IS NOT NULL)
 OR (state='released' AND resolved_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_entry_user ON game_blackjack_entries(user_id) WHERE state IN ('waiting','seated','playing');
CREATE UNIQUE INDEX idx_blackjack_entry_seat ON game_blackjack_entries(session_id,seat_no) WHERE state IN ('seated','playing','settled');
CREATE INDEX idx_blackjack_entry_fifo ON game_blackjack_entries(ordinal) WHERE state='waiting';
CREATE INDEX idx_blackjack_entry_session ON game_blackjack_entries(session_id,seat_no,ordinal);
CREATE INDEX idx_blackjack_entry_history ON game_blackjack_entries(user_id,resolved_at DESC,ordinal DESC);
CREATE INDEX idx_blackjack_entry_retention ON game_blackjack_entries(resolved_at,ordinal) WHERE resolved_at IS NOT NULL;
CREATE TABLE game_blackjack_payments (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='bjp_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 entry_id TEXT NOT NULL REFERENCES game_blackjack_entries(id) ON DELETE RESTRICT,
 kind TEXT NOT NULL CHECK(kind IN ('base','split','double')),
 hand_no INTEGER NOT NULL CHECK(hand_no IN (0,1)),
 amount_milli INTEGER NOT NULL CHECK(amount_milli BETWEEN 1 AND 140625000000000),
 game_paid_milli INTEGER NOT NULL CHECK(game_paid_milli BETWEEN 0 AND amount_milli),
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(general_account_id<>game_account_id),
 reserve_operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 terminal_operation_id TEXT UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 state TEXT NOT NULL CHECK(state IN ('reserved','settled','released')),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708739),
 CHECK((state='reserved' AND terminal_operation_id IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000001') OR (state<>'reserved' AND terminal_operation_id IS NOT NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000'))
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_base_payment ON game_blackjack_payments(entry_id) WHERE kind='base';
CREATE INDEX idx_blackjack_payment_entry ON game_blackjack_payments(entry_id,ordinal);
CREATE INDEX idx_blackjack_payment_reserved ON game_blackjack_payments(id) WHERE state='reserved';
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE TABLE game_blackjack_events (
 session_id TEXT NOT NULL REFERENCES game_blackjack_sessions(id) ON DELETE RESTRICT,
 seq INTEGER NOT NULL CHECK(seq BETWEEN 1 AND 128),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253399708739),
 kind TEXT NOT NULL CHECK(kind IN ('deal','actions','timeout','result','cancelled')),
 public_json TEXT NOT NULL CHECK(json_valid(public_json) AND length(CAST(public_json AS BLOB))<=24576),
 PRIMARY KEY(session_id,seq)
) STRICT;
CREATE TABLE game_blackjack_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='bja_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 public_json TEXT NOT NULL CHECK(json_valid(public_json) AND length(CAST(public_json AS BLOB))<=32768)
) STRICT;
CREATE TRIGGER blackjack_session_frozen BEFORE UPDATE ON game_blackjack_sessions WHEN NEW.id<>OLD.id OR NEW.started_at<>OLD.started_at OR NEW.revision<=OLD.revision OR NEW.last_batch<OLD.last_batch OR OLD.phase IN ('result','cancelled') BEGIN SELECT RAISE(ABORT,'blackjack session immutable'); END;
CREATE TRIGGER blackjack_entry_frozen BEFORE UPDATE ON game_blackjack_entries WHEN NEW.ordinal<>OLD.ordinal OR NEW.id<>OLD.id OR NEW.created_at<>OLD.created_at OR NEW.stake_milli<>OLD.stake_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL) OR ((OLD.session_id IS NOT NULL AND NEW.session_id IS NOT OLD.session_id) OR (OLD.seat_no IS NOT NULL AND NEW.seat_no IS NOT OLD.seat_no)) AND NOT (OLD.state='seated' AND NEW.state='released' AND NEW.session_id IS NULL AND NEW.seat_no IS NULL) OR NEW.stopped<OLD.stopped BEGIN SELECT RAISE(ABORT,'blackjack entry immutable'); END;
CREATE TRIGGER blackjack_entry_transition BEFORE UPDATE OF state ON game_blackjack_entries WHEN NEW.state<>OLD.state AND NOT ((OLD.state='waiting' AND NEW.state IN ('seated','released')) OR (OLD.state='seated' AND NEW.state IN ('playing','released')) OR (OLD.state='playing' AND NEW.state IN ('settled','released'))) BEGIN SELECT RAISE(ABORT,'blackjack entry transition'); END;
CREATE TRIGGER blackjack_payment_insert BEFORE INSERT ON game_blackjack_payments WHEN NOT EXISTS(SELECT 1 FROM game_blackjack_entries WHERE id=NEW.entry_id AND stake_milli=NEW.amount_milli AND state IN ('waiting','seated','playing')) OR (SELECT COUNT(*) FROM game_blackjack_payments WHERE entry_id=NEW.entry_id)>=4 OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='blackjack-payment:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='blackjack-payment:'||NEW.id) BEGIN SELECT RAISE(ABORT,'blackjack payment mismatch'); END;
CREATE TRIGGER blackjack_payment_frozen BEFORE UPDATE ON game_blackjack_payments WHEN NEW.ordinal<>OLD.ordinal OR NEW.id<>OLD.id OR NEW.entry_id<>OLD.entry_id OR NEW.kind<>OLD.kind OR NEW.hand_no<>OLD.hand_no OR NEW.amount_milli<>OLD.amount_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.reserve_operation_id<>OLD.reserve_operation_id OR NEW.created_at<>OLD.created_at OR OLD.state<>'reserved' BEGIN SELECT RAISE(ABORT,'blackjack payment immutable'); END;
CREATE TRIGGER blackjack_payment_delete BEFORE DELETE ON game_blackjack_payments WHEN OLD.state='reserved' BEGIN SELECT RAISE(ABORT,'blackjack payment still reserved'); END;
CREATE TRIGGER blackjack_entry_delete BEFORE DELETE ON game_blackjack_entries WHEN OLD.state IN ('waiting','seated','playing') BEGIN SELECT RAISE(ABORT,'blackjack entry still active'); END;
CREATE TRIGGER blackjack_session_delete BEFORE DELETE ON game_blackjack_sessions WHEN OLD.phase='decision' OR (OLD.phase='seating' AND EXISTS(SELECT 1 FROM game_blackjack_entries WHERE session_id=OLD.id)) BEGIN SELECT RAISE(ABORT,'blackjack table still active'); END;
CREATE TRIGGER blackjack_user_delete BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_blackjack_entries WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'blackjack user handoff required'); END;
CREATE TRIGGER blackjack_event_immutable BEFORE UPDATE ON game_blackjack_events BEGIN SELECT RAISE(ABORT,'blackjack event immutable'); END;
CREATE TRIGGER blackjack_anonymous_immutable BEFORE UPDATE ON game_blackjack_anonymous BEGIN SELECT RAISE(ABORT,'blackjack archive immutable'); END;
`
