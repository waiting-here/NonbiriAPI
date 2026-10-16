package v1

const accountMFASchema = `INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_1','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_2','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_3','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_4','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_5','0',0);
INSERT INTO site_config(key,value,updated_at) VALUES('mfa_required_level_6','0',0);
CREATE TABLE account_mfa_credentials (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 context_id BLOB NOT NULL UNIQUE CHECK(typeof(context_id)='blob' AND length(context_id)=16),
 encrypted_secret TEXT NOT NULL CHECK(length(encrypted_secret) BETWEEN 1 AND 256),
 last_step INTEGER NOT NULL CHECK(last_step>=0)
) STRICT;

CREATE TABLE account_mfa_pending (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 session_hash TEXT NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
 context_id BLOB NOT NULL UNIQUE CHECK(typeof(context_id)='blob' AND length(context_id)=16),
 encrypted_secret TEXT NOT NULL CHECK(length(encrypted_secret) BETWEEN 1 AND 256),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_account_mfa_pending_expiry ON account_mfa_pending(expires_at);

CREATE TABLE account_mfa_attempts (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 window_start INTEGER NOT NULL DEFAULT 0 CHECK(window_start BETWEEN 0 AND 253402300799),
 failures INTEGER NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 5),
 blocked_until INTEGER NOT NULL DEFAULT 0 CHECK(blocked_until BETWEEN 0 AND 253402300799)
) STRICT;

CREATE TABLE account_mfa_proofs (
 session_hash TEXT PRIMARY KEY REFERENCES sessions(token_hash) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES account_mfa_credentials(user_id) ON DELETE CASCADE,
 session_generation TEXT NOT NULL,
 verified_until INTEGER NOT NULL CHECK(verified_until BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_account_mfa_proofs_user ON account_mfa_proofs(user_id);

CREATE TRIGGER account_mfa_security_revoke
AFTER UPDATE OF is_admin,is_banned ON users
WHEN NEW.is_admin<>0 OR NEW.is_banned<>0
BEGIN
 DELETE FROM account_mfa_proofs WHERE user_id=NEW.id;
 DELETE FROM account_mfa_pending WHERE user_id=NEW.id;
END;
`
