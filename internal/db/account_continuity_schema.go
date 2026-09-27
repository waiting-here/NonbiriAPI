package db

const accountContinuitySchema = `
ALTER TABLE abuse_window_events ADD COLUMN expires_at INTEGER CHECK(expires_at>occurred_at AND expires_at<=253402300799);
CREATE TABLE client_rule_auto_bans (
 rule_id TEXT PRIMARY KEY REFERENCES risk_client_rules(id) ON DELETE RESTRICT,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 duration_seconds INTEGER CHECK(duration_seconds BETWEEN 1 AND 315360000),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER client_rule_auto_ban_capacity BEFORE INSERT ON client_rule_auto_bans
WHEN NOT EXISTS(SELECT 1 FROM client_rule_auto_bans WHERE rule_id=NEW.rule_id)
 AND (SELECT count(*) FROM client_rule_auto_bans)>=100
BEGIN SELECT RAISE(ABORT,'client rule action capacity exhausted'); END;
CREATE TABLE client_rule_ban_receipts (
 request_id TEXT PRIMARY KEY CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json) AND json_type(rules_json)='array' AND json_array_length(rules_json) BETWEEN 1 AND 100 AND length(CAST(rules_json AS BLOB))<=16384),
 banned_until INTEGER CHECK(banned_until BETWEEN created_at AND 253402300799),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253394524799),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+7776000)
) STRICT;
CREATE INDEX idx_client_rule_ban_receipts_expiry ON client_rule_ban_receipts(expires_at,request_id);
CREATE TABLE discord_blacklist_origins (
 discord_id TEXT PRIMARY KEY REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 first_actor_kind TEXT NOT NULL CHECK(first_actor_kind IN ('admin','steward6','automatic','unknown')),
 first_actor_user_id INTEGER CHECK(first_actor_user_id>0)
) STRICT;
CREATE INDEX idx_blacklist_origins_actor ON discord_blacklist_origins(first_actor_kind,first_actor_user_id,discord_id);
CREATE TRIGGER blacklist_origin_immutable BEFORE UPDATE ON discord_blacklist_origins
BEGIN SELECT RAISE(ABORT,'blacklist origin is immutable'); END;
CREATE TRIGGER blacklist_reason_immutable BEFORE UPDATE OF reason,created_at ON discord_blacklist
WHEN NEW.reason<>OLD.reason OR NEW.created_at<>OLD.created_at
BEGIN SELECT RAISE(ABORT,'blacklist first event is immutable'); END;
CREATE TABLE discord_blacklist_events (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 operation_key TEXT NOT NULL UNIQUE CHECK(length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('admin','steward6','automatic','unknown')),
 actor_user_id INTEGER CHECK(actor_user_id>0),
 reason_codes_json TEXT NOT NULL CHECK(json_valid(reason_codes_json) AND json_type(reason_codes_json)='array' AND json_array_length(reason_codes_json)<=8 AND length(CAST(reason_codes_json AS BLOB))<=1024),
 safe_note TEXT NOT NULL CHECK(length(safe_note)<=2000 AND instr(safe_note,char(0))=0 AND instr(safe_note,char(13))=0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_blacklist_events_identity ON discord_blacklist_events(discord_id,created_at,id);
CREATE TRIGGER blacklist_event_immutable BEFORE UPDATE ON discord_blacklist_events
BEGIN SELECT RAISE(ABORT,'blacklist event is immutable'); END;
CREATE TABLE user_continuity_identities (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32)
) STRICT;
CREATE INDEX idx_user_continuity_key ON user_continuity_identities(identity_key,user_id);
CREATE TABLE identity_continuity_facts (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 kind TEXT NOT NULL CHECK(kind IN ('checkin_general','checkin_game','welfare','game_onboarding','abuse_state')),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128),
 window_key TEXT NOT NULL CHECK(length(CAST(window_key AS BLOB)) BETWEEN 1 AND 128),
 fact_json TEXT NOT NULL CHECK(json_valid(fact_json) AND json_type(fact_json)='object' AND length(CAST(fact_json AS BLOB))<=16384),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER CHECK(expires_at>occurred_at AND expires_at<=253402300799),
 PRIMARY KEY(identity_key,kind,scope,window_key),
 CHECK((kind='game_onboarding')=(expires_at IS NULL))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_identity_facts_expiry ON identity_continuity_facts(expires_at,identity_key,kind,scope,window_key) WHERE expires_at IS NOT NULL;
CREATE TABLE identity_window_events (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 kind TEXT NOT NULL CHECK(kind IN ('user_rpm','report_frequency','game_start','abuse_rpm','abuse_short_content')),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128),
 event_key TEXT NOT NULL CHECK(length(CAST(event_key AS BLOB)) BETWEEN 1 AND 128),
 occurred_at_ms INTEGER NOT NULL CHECK(occurred_at_ms BETWEEN 0 AND 253402300799000),
 expires_at_ms INTEGER NOT NULL CHECK(expires_at_ms>occurred_at_ms AND expires_at_ms<=253402300799000),
 count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 9223372036854775807),
 value INTEGER CHECK(value>=0),
 PRIMARY KEY(identity_key,kind,scope,event_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_identity_windows_expiry ON identity_window_events(expires_at_ms,identity_key,kind,scope,event_key);
CREATE INDEX idx_identity_windows_current ON identity_window_events(identity_key,kind,scope,occurred_at_ms);
ALTER TABLE admin_account_deletions ADD COLUMN snapshot_version INTEGER NOT NULL DEFAULT 1 CHECK(snapshot_version IN (1,2));
ALTER TABLE admin_account_deletions ADD COLUMN former_user_id INTEGER CHECK(former_user_id>0);
ALTER TABLE admin_account_deletions ADD COLUMN discord_id TEXT CHECK(length(CAST(discord_id AS BLOB)) BETWEEN 1 AND 128);
ALTER TABLE admin_account_deletions ADD COLUMN registered_at INTEGER CHECK(registered_at BETWEEN 0 AND 253402300799);
ALTER TABLE admin_account_deletions ADD COLUMN deleted_at INTEGER CHECK(deleted_at BETWEEN 0 AND 253402300799);
ALTER TABLE admin_account_deletions ADD COLUMN effective_level INTEGER CHECK(effective_level BETWEEN 1 AND 6);
ALTER TABLE admin_account_deletions ADD COLUMN source TEXT NOT NULL DEFAULT 'unknown' CHECK(source IN ('unknown','self','admin','system'));
ALTER TABLE admin_account_deletions ADD COLUMN actor_user_id INTEGER CHECK(actor_user_id>0);
ALTER TABLE admin_account_deletions ADD COLUMN ban_active INTEGER CHECK(ban_active IN (0,1));
ALTER TABLE admin_account_deletions ADD COLUMN pause_active INTEGER CHECK(pause_active IN (0,1));
ALTER TABLE admin_account_deletions ADD COLUMN blacklist_action TEXT NOT NULL DEFAULT 'unknown' CHECK(blacklist_action IN ('unknown','none','added','appended'));
CREATE UNIQUE INDEX idx_deleted_accounts_former_user ON admin_account_deletions(former_user_id) WHERE former_user_id IS NOT NULL;
CREATE INDEX idx_deleted_accounts_discord ON admin_account_deletions(discord_id,deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_time ON admin_account_deletions(deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_level ON admin_account_deletions(effective_level,deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_ban ON admin_account_deletions(ban_active,deleted_at DESC,former_user_id);
CREATE TRIGGER deleted_account_projection_permanent BEFORE DELETE ON admin_account_deletions
BEGIN SELECT RAISE(ABORT,'deleted account projection has no automatic expiry'); END;
CREATE TABLE self_deletion_duel_aborts (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL CHECK(length(CAST(discord_id AS BLOB)) BETWEEN 1 AND 128),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes')),
 match_id TEXT NOT NULL CHECK(length(CAST(match_id AS BLOB)) BETWEEN 1 AND 128),
 former_user_id INTEGER NOT NULL CHECK(former_user_id>0),
 reason TEXT NOT NULL CHECK(reason='self_deletion_cancelled_match'),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253394524799),
 expires_at INTEGER NOT NULL CHECK(expires_at=occurred_at+7776000),
 UNIQUE(game_key,match_id,former_user_id)
) STRICT;
CREATE INDEX idx_self_deletion_duel_aborts_discord ON self_deletion_duel_aborts(discord_id,occurred_at,id);
CREATE INDEX idx_self_deletion_duel_aborts_expiry ON self_deletion_duel_aborts(expires_at,id);
`
