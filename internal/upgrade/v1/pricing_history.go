package v1

const pricingHistorySchema = `CREATE TABLE charity_price_templates (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 description TEXT NOT NULL CHECK(length(description)<=500),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 pricing TEXT NOT NULL CHECK(json_valid(pricing) AND json_type(pricing)='object' AND length(CAST(pricing AS BLOB))<=8192),
 token_reserve_credits TEXT CHECK(token_reserve_credits IS NULL OR length(token_reserve_credits) BETWEEN 1 AND 32),
 discount TEXT NOT NULL CHECK(json_valid(discount) AND json_type(discount)='object' AND length(CAST(discount AS BLOB))<=512),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE TRIGGER charity_price_template_capacity BEFORE INSERT ON charity_price_templates
WHEN (SELECT COUNT(*) FROM charity_price_templates)>=256
BEGIN SELECT RAISE(ABORT,'price template capacity reached'); END;
CREATE TABLE charity_price_template_audits (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 template_id INTEGER NOT NULL CHECK(template_id>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 action TEXT NOT NULL CHECK(action IN ('create','replace','delete')),
 from_revision INTEGER NOT NULL CHECK(from_revision>=0),
 to_revision INTEGER NOT NULL CHECK(to_revision>from_revision),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_price_template_audits_expiry ON charity_price_template_audits(created_at,id);
CREATE INDEX idx_price_template_audits_actor ON charity_price_template_audits(actor_user_id,id);
CREATE TRIGGER price_template_audit_immutable BEFORE UPDATE ON charity_price_template_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND NEW.id=OLD.id AND NEW.template_id=OLD.template_id AND NEW.action=OLD.action
 AND NEW.from_revision=OLD.from_revision AND NEW.to_revision=OLD.to_revision AND NEW.created_at=OLD.created_at)
BEGIN SELECT RAISE(ABORT,'price template audit is immutable'); END;
CREATE TABLE donation_owner_preferences (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 donation_id INTEGER NOT NULL UNIQUE REFERENCES donations(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 owner_hidden_at INTEGER CHECK(owner_hidden_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_donation_owner_preferences_user ON donation_owner_preferences(user_id,donation_id);
`
