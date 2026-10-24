package db

// StewardPermissionsSQL adds user-owned authority with account deletion cascade.
const StewardPermissionsSQL = `CREATE TABLE steward_permissions (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 permissions TEXT NOT NULL CHECK(length(CAST(permissions AS BLOB))<=8192 AND json_valid(permissions) AND json_type(permissions)='object'),
 limits TEXT NOT NULL CHECK(length(CAST(limits AS BLOB))<=2048 AND json_valid(limits) AND json_type(limits)='object'),
 donation_scope TEXT NOT NULL DEFAULT 'standard' CHECK(donation_scope IN ('none','standard','enhanced','full')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
);
CREATE TABLE steward_permission_audits (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 from_revision INTEGER NOT NULL CHECK(from_revision>=0),
 to_revision INTEGER NOT NULL CHECK(to_revision=from_revision+1),
 previous_policy TEXT NOT NULL CHECK(length(CAST(previous_policy AS BLOB))<=12288 AND json_valid(previous_policy)),
 next_policy TEXT NOT NULL CHECK(length(CAST(next_policy AS BLOB))<=12288 AND json_valid(next_policy)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
);
CREATE INDEX idx_steward_permission_audits_user ON steward_permission_audits(user_id,id);
CREATE INDEX idx_steward_permission_audits_actor ON steward_permission_audits(actor_user_id,id);
CREATE INDEX idx_steward_permission_audits_retention ON steward_permission_audits(created_at,id);
`

// StewardAuditRolesSQL preserves historical actors while admitting actual level 5 audit labels.
const StewardAuditRolesSQL = `DROP TRIGGER legal_hold_steward_read_insert_guard;
DROP TRIGGER legal_hold_steward_read_update_guard;
CREATE TRIGGER legal_hold_steward_read_insert_guard BEFORE INSERT ON legal_hold_steward_reads
WHEN NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.user_id AND u.is_admin=0 AND u.is_banned=0 AND u.level IN (5,6))
 OR NEW.read_count<>1 OR NEW.first_read_at<>NEW.last_read_at
 OR NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.object_kind IN ('donation','request_log') AND h.state='active' AND NEW.first_read_at>=h.created_at AND NEW.last_read_at<h.expires_at)
BEGIN SELECT RAISE(ABORT,'steward held read is inconsistent'); END;
CREATE TRIGGER legal_hold_steward_read_update_guard BEFORE UPDATE ON legal_hold_steward_reads
WHEN NEW.id IS NOT OLD.id OR NEW.hold_id_text IS NOT OLD.hold_id_text OR NEW.first_read_at IS NOT OLD.first_read_at
 OR NOT (
  (NEW.user_id IS OLD.user_id AND NEW.last_read_at>=OLD.last_read_at AND NEW.read_count=OLD.read_count+1
   AND EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.user_id AND u.is_admin=0 AND u.is_banned=0 AND u.level IN (5,6))
   AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state='active' AND NEW.last_read_at<h.expires_at))
  OR (NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.user_id)
   AND NEW.last_read_at=OLD.last_read_at AND NEW.read_count=OLD.read_count)
 )
BEGIN SELECT RAISE(ABORT,'steward held read is inconsistent'); END;
CREATE TEMP TABLE saved_risk_client_rules AS SELECT * FROM risk_client_rules;
CREATE TEMP TABLE saved_client_rule_auto_bans AS SELECT * FROM client_rule_auto_bans;
CREATE TEMP TABLE saved_discord_blacklist_origins AS SELECT * FROM discord_blacklist_origins;
CREATE TEMP TABLE saved_discord_blacklist_events AS SELECT * FROM discord_blacklist_events;
DROP TABLE client_rule_auto_bans;
DROP TABLE risk_client_rules;
DROP TABLE discord_blacklist_origins;
DROP TABLE discord_blacklist_events;
CREATE TABLE risk_client_rules (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 status TEXT NOT NULL CHECK(status IN ('suspected','confirmed')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>=1),
 conditions_json TEXT NOT NULL CHECK(json_valid(conditions_json) AND length(CAST(conditions_json AS BLOB)) BETWEEN 2 AND 16384),
 evidence_note TEXT NOT NULL DEFAULT '' CHECK(length(evidence_note)<=4096),
 evidence_url TEXT NOT NULL DEFAULT '' CHECK(length(evidence_url)<=2048),
 created_by_role TEXT NOT NULL CHECK(created_by_role IN ('admin','level5','level6')),
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_by_role TEXT NOT NULL CHECK(updated_by_role IN ('admin','level5','level6')),
 updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at)
) STRICT;
CREATE INDEX idx_risk_rules_updated ON risk_client_rules(updated_at,id);
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
CREATE TABLE discord_blacklist_origins (
 discord_id TEXT PRIMARY KEY REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 first_actor_kind TEXT NOT NULL CHECK(first_actor_kind IN ('admin','steward5','steward6','automatic','unknown')),
 first_actor_user_id INTEGER CHECK(first_actor_user_id>0)
) STRICT;
CREATE INDEX idx_blacklist_origins_actor ON discord_blacklist_origins(first_actor_kind,first_actor_user_id,discord_id);
CREATE TRIGGER blacklist_origin_immutable BEFORE UPDATE ON discord_blacklist_origins
BEGIN SELECT RAISE(ABORT,'blacklist origin is immutable'); END;
CREATE TABLE discord_blacklist_events (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 operation_key TEXT NOT NULL UNIQUE CHECK(length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('admin','steward5','steward6','automatic','unknown')),
 actor_user_id INTEGER CHECK(actor_user_id>0),
 reason_codes_json TEXT NOT NULL CHECK(json_valid(reason_codes_json) AND json_type(reason_codes_json)='array' AND json_array_length(reason_codes_json)<=8 AND length(CAST(reason_codes_json AS BLOB))<=1024),
 safe_note TEXT NOT NULL CHECK(length(safe_note)<=2000 AND instr(safe_note,char(0))=0 AND instr(safe_note,char(13))=0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_blacklist_events_identity ON discord_blacklist_events(discord_id,created_at,id);
CREATE TRIGGER blacklist_event_immutable BEFORE UPDATE ON discord_blacklist_events
BEGIN SELECT RAISE(ABORT,'blacklist event is immutable'); END;
INSERT INTO risk_client_rules SELECT * FROM saved_risk_client_rules;
DROP TABLE saved_risk_client_rules;
INSERT INTO client_rule_auto_bans SELECT * FROM saved_client_rule_auto_bans;
DROP TABLE saved_client_rule_auto_bans;
INSERT INTO discord_blacklist_origins SELECT * FROM saved_discord_blacklist_origins;
DROP TABLE saved_discord_blacklist_origins;
INSERT INTO discord_blacklist_events SELECT * FROM saved_discord_blacklist_events;
DROP TABLE saved_discord_blacklist_events;
`

// EndpointTagsSQL widens the existing tag registry without changing stored labels.
const EndpointTagsSQL = `CREATE TEMP TABLE saved_endpoint_tags AS SELECT base_url,tag FROM admin_endpoint_tags;
DROP TABLE admin_endpoint_tags;
CREATE TABLE admin_endpoint_tags (
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 4096),
 tag TEXT NOT NULL CHECK(tag IN ('abusive_third_party','community_charity','noncommercial_gateway','user_gateway','steward_gateway','invalid_entry')),
 PRIMARY KEY(base_url,tag)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_admin_endpoint_tags_tag ON admin_endpoint_tags(tag,base_url);
INSERT INTO admin_endpoint_tags SELECT base_url,tag FROM saved_endpoint_tags;
DROP TABLE saved_endpoint_tags;
`
