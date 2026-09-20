package db

// These additions preserve the existing database generation and monetary
// encodings. Personal facts cascade with their owner; financial operations
// keep the existing immutable ledger lifecycle.
const progressionColumnsSchema = `
ALTER TABLE users ADD COLUMN charity_profile_public INTEGER NOT NULL DEFAULT 0 CHECK(typeof(charity_profile_public)='integer' AND charity_profile_public IN (0,1));
ALTER TABLE users ADD COLUMN donation_credit_achieved_at INTEGER CHECK(donation_credit_achieved_at IS NULL OR (typeof(donation_credit_achieved_at)='integer' AND donation_credit_achieved_at BETWEEN 0 AND 253402300799));
ALTER TABLE users ADD COLUMN donation_credit_achieved_seq BLOB CHECK(donation_credit_achieved_seq IS NULL OR (typeof(donation_credit_achieved_seq)='blob' AND length(donation_credit_achieved_seq)=16 AND donation_credit_achieved_seq>X'00000000000000000000000000000000')) CHECK((donation_credit_achieved_at IS NULL)=(donation_credit_achieved_seq IS NULL));
CREATE INDEX idx_users_charity_rank ON users(donation_credit_mag DESC,donation_credit_achieved_at,donation_credit_achieved_seq);
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
`

const progressionTablesSchema = `
CREATE TABLE game_statistics_epoch (
 id INTEGER PRIMARY KEY CHECK(id=1),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 rules_version INTEGER NOT NULL CHECK(rules_version=1)
) STRICT;
CREATE TRIGGER game_statistics_epoch_immutable BEFORE UPDATE ON game_statistics_epoch BEGIN SELECT RAISE(ABORT,'statistics epoch is immutable'); END;
CREATE TRIGGER game_statistics_epoch_no_delete BEFORE DELETE ON game_statistics_epoch BEGIN SELECT RAISE(ABORT,'statistics epoch is required'); END;
CREATE TABLE game_rank_counters (
 id INTEGER PRIMARY KEY CHECK(id=1),
 next_event_seq BLOB NOT NULL CHECK(length(next_event_seq)=16 AND next_event_seq>X'00000000000000000000000000000000')
) STRICT;
CREATE TABLE game_rank_events (
 seq BLOB NOT NULL PRIMARY KEY CHECK(length(seq)=16 AND seq>X'00000000000000000000000000000000'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','blackjack')),
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
CREATE INDEX idx_rank_events_charity_expiry ON game_rank_events(charity_expires_at,user_id,seq) WHERE charity_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit7_expiry ON game_rank_events(profit_7d_expires_at,user_id,game_key,seq) WHERE profit_7d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit30_expiry ON game_rank_events(profit_30d_expires_at,user_id,game_key,seq) WHERE profit_30d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_user ON game_rank_events(user_id,settled_at,seq);
CREATE TABLE game_rank_totals (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_charity','bidding','blackjack')),
 window TEXT NOT NULL CHECK(window IN ('7d','30d','history')),
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16 AND amount_mag<X'80000000000000000000000000000000'),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_phase INTEGER NOT NULL CHECK(achieved_phase IN (0,1)),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16),
 PRIMARY KEY(user_id,board,window),
 CHECK(board<>'game_charity' OR window='7d'),
 CHECK(board='game_charity' OR amount_sign>=0),
 CHECK((amount_sign=0 AND amount_mag=X'00000000000000000000000000000000') OR (amount_sign<>0 AND amount_mag>X'00000000000000000000000000000000'))
) STRICT;
CREATE INDEX idx_rank_totals_board ON game_rank_totals(board,window,amount_sign,amount_mag DESC,achieved_at,achieved_phase,achieved_seq);
CREATE TABLE game_rank_expiry_work (
 id INTEGER PRIMARY KEY CHECK(id=1),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_charity','bidding','blackjack')),
 window TEXT NOT NULL CHECK(window IN ('7d','30d')),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799),
 delta_sign INTEGER NOT NULL CHECK(delta_sign IN (-1,0,1)),
 delta_mag BLOB NOT NULL CHECK(length(delta_mag)=32),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16),
 CHECK(board<>'game_charity' OR window='7d'),
 CHECK((delta_sign=0)=(delta_mag=zeroblob(32)))
) STRICT;
CREATE TABLE activity_loans (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5)='loan_' AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 quote_nonce TEXT NOT NULL UNIQUE CHECK(length(quote_nonce)=26 AND substr(quote_nonce,1,4)='lqn_' AND substr(quote_nonce,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(quote_nonce,-1,1) IN ('A','Q','g','w')),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT,
 ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq>0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 config_revision INTEGER NOT NULL CHECK(config_revision>0),
 principal INTEGER NOT NULL CHECK(principal BETWEEN 1 AND 9000000000000),
 coefficient_a INTEGER NOT NULL CHECK(coefficient_a BETWEEN 1 AND 999),
 coefficient_b INTEGER NOT NULL CHECK(coefficient_b BETWEEN 1001 AND 9000000000000000),
 nominal_milli INTEGER NOT NULL CHECK(nominal_milli BETWEEN 1 AND 9000000000000000 AND nominal_milli=principal*1000),
 disbursed_milli INTEGER NOT NULL CHECK(disbursed_milli BETWEEN 1 AND 9000000000000000 AND disbursed_milli=principal*coefficient_a),
 fee_milli INTEGER NOT NULL CHECK(fee_milli BETWEEN 1 AND 9000000000000000 AND fee_milli=nominal_milli-disbursed_milli),
 repayment_milli INTEGER NOT NULL CHECK(repayment_milli BETWEEN 1 AND 9000000000000000 AND coefficient_b<=9000000000000000/principal AND repayment_milli=principal*coefficient_b),
 interest_milli INTEGER NOT NULL CHECK(interest_milli BETWEEN 1 AND 9000000000000000 AND interest_milli=repayment_milli-nominal_milli),
 general_before_sign INTEGER NOT NULL CHECK(general_before_sign IN (0,1)),
 general_before_mag BLOB NOT NULL CHECK(length(general_before_mag)=16 AND general_before_mag<X'80000000000000000000000000000000'),
 general_after_sign INTEGER NOT NULL CHECK(general_after_sign IN (-1,0,1)),
 general_after_mag BLOB NOT NULL CHECK(length(general_after_mag)=16 AND general_after_mag<X'80000000000000000000000000000000'),
 game_before_sign INTEGER NOT NULL CHECK(game_before_sign IN (-1,0,1)),
 game_before_mag BLOB NOT NULL CHECK(length(game_before_mag)=16 AND game_before_mag<X'80000000000000000000000000000000'),
 game_after_sign INTEGER NOT NULL CHECK(game_after_sign IN (-1,0,1)),
 game_after_mag BLOB NOT NULL CHECK(length(game_after_mag)=16 AND game_after_mag<X'80000000000000000000000000000000'),
 CHECK((general_before_sign=0)=(general_before_mag=X'00000000000000000000000000000000')),
 CHECK((general_after_sign=0)=(general_after_mag=X'00000000000000000000000000000000')),
 CHECK((game_before_sign=0)=(game_before_mag=X'00000000000000000000000000000000')),
 CHECK((game_after_sign=0)=(game_after_mag=X'00000000000000000000000000000000'))
) STRICT;
CREATE INDEX idx_activity_loans_user ON activity_loans(user_id,created_at DESC,ledger_seq DESC);
CREATE TRIGGER activity_loan_immutable BEFORE UPDATE ON activity_loans BEGIN SELECT RAISE(ABORT,'loan receipt is immutable'); END;
CREATE TRIGGER activity_loan_operation_insert BEFORE INSERT ON activity_loans
WHEN NOT EXISTS(SELECT 1 FROM credit_operations o WHERE o.id=NEW.operation_id AND o.kind='activity_loan' AND o.ledger_seq=NEW.ledger_seq AND o.created_at=NEW.created_at)
 OR (SELECT count(*) FROM credit_entries WHERE operation_id=NEW.operation_id)<>4
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='general' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='game' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
BEGIN SELECT RAISE(ABORT,'loan operation mismatch'); END;
`

const abuseStorageSchema = `
CREATE TABLE abuse_windows (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content')),
 ban_done INTEGER NOT NULL CHECK(ban_done IN (0,1)),
 suspend_done INTEGER NOT NULL CHECK(suspend_done IN (0,1)),
 next_event_seq INTEGER NOT NULL CHECK(next_event_seq>0),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,violation_kind)
) STRICT;
CREATE TABLE abuse_window_events (
 user_id INTEGER NOT NULL,
 violation_kind TEXT NOT NULL,
 seq INTEGER NOT NULL CHECK(seq>0),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576),
 PRIMARY KEY(user_id,violation_kind,seq), UNIQUE(request_id,violation_kind),
 FOREIGN KEY(user_id,violation_kind) REFERENCES abuse_windows(user_id,violation_kind) ON DELETE CASCADE,
 CHECK((violation_kind='rpm' AND content_chars IS NULL) OR (violation_kind='short_content' AND content_chars IS NOT NULL))
) STRICT;
CREATE INDEX idx_abuse_events_window ON abuse_window_events(user_id,violation_kind,occurred_at,seq);
CREATE INDEX idx_abuse_events_expiry ON abuse_window_events(violation_kind,occurred_at,user_id,seq);
CREATE TABLE abuse_cases (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='abc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('deduction','ban','charity_suspend')),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content')),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN started_at AND 253402300799),
 ended_at INTEGER CHECK(ended_at IS NULL OR ended_at BETWEEN started_at AND 253402300799),
 state TEXT NOT NULL CHECK(state IN ('active','ended')),
 result TEXT NOT NULL CHECK(result IN ('applied','extended','adjusted','released','expired')),
 CHECK((state='active' AND ended_at IS NULL AND kind<>'deduction') OR (state='ended' AND ended_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX idx_abuse_cases_active ON abuse_cases(user_id,kind) WHERE state='active';
CREATE INDEX idx_abuse_cases_user ON abuse_cases(user_id,started_at DESC,id);
CREATE INDEX idx_abuse_cases_retention ON abuse_cases(state,ended_at,id);
CREATE INDEX idx_abuse_cases_due ON abuse_cases(ends_at,id) WHERE state='active' AND ends_at IS NOT NULL;
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
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content','manual_adjustment','manual_release','expired')),
 rules_json TEXT NOT NULL CHECK(length(CAST(rules_json AS BLOB))<=4096 AND json_valid(rules_json) AND json_type(rules_json)='object'),
 statistics_json TEXT NOT NULL CHECK(length(CAST(statistics_json AS BLOB))<=4096 AND json_valid(statistics_json) AND json_type(statistics_json)='object'),
 evidence_count INTEGER NOT NULL CHECK(evidence_count BETWEEN 0 AND 4096),
 evidence_bytes INTEGER NOT NULL CHECK(evidence_bytes BETWEEN 0 AND 1048576)
) STRICT;
CREATE INDEX idx_abuse_actions_case ON abuse_actions(case_id,seq DESC);
CREATE INDEX idx_abuse_actions_actor ON abuse_actions(actor_user_id) WHERE actor_user_id IS NOT NULL;
CREATE TRIGGER abuse_action_immutable BEFORE UPDATE ON abuse_actions
WHEN NOT (NEW.seq IS OLD.seq AND NEW.case_id IS OLD.case_id AND NEW.action IS OLD.action AND NEW.occurred_at IS OLD.occurred_at
 AND NEW.request_id IS OLD.request_id AND NEW.operation_id IS OLD.operation_id AND NEW.previous_ends_at IS OLD.previous_ends_at AND NEW.ends_at IS OLD.ends_at
 AND NEW.reason_code IS OLD.reason_code AND NEW.rules_json IS OLD.rules_json AND NEW.statistics_json IS OLD.statistics_json
 AND NEW.evidence_count IS OLD.evidence_count AND NEW.evidence_bytes IS OLD.evidence_bytes
 AND (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))))
BEGIN SELECT RAISE(ABORT,'penalty action is immutable'); END;
CREATE TABLE abuse_evidence (
 action_seq INTEGER NOT NULL REFERENCES abuse_actions(seq) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 4095),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576),
 PRIMARY KEY(action_seq,ordinal), UNIQUE(action_seq,request_id)
) STRICT;
CREATE TRIGGER abuse_evidence_immutable BEFORE UPDATE ON abuse_evidence BEGIN SELECT RAISE(ABORT,'penalty evidence is immutable'); END;
CREATE TRIGGER abuse_evidence_bound BEFORE INSERT ON abuse_evidence
WHEN NOT EXISTS(SELECT 1 FROM abuse_actions WHERE seq=NEW.action_seq AND NEW.ordinal<evidence_count)
BEGIN SELECT RAISE(ABORT,'penalty evidence exceeds declared count'); END;
`
