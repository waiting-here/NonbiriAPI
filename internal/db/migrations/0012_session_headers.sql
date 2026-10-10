ALTER TABLE charity_models ADD COLUMN session_headers TEXT NOT NULL DEFAULT '[]' CHECK(length(session_headers)<=4096 AND json_valid(session_headers) AND json_type(session_headers)='array');
CREATE TABLE charity_header_sessions (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 value TEXT NOT NULL CHECK(length(value) IN (32,36)),
 format TEXT NOT NULL CHECK(format IN ('uuid_v4','hex_32')),
 idle_seconds INTEGER NOT NULL CHECK(idle_seconds BETWEEN 1 AND 86400),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402387199),
 PRIMARY KEY(user_id,model_id,name)
) STRICT;
CREATE INDEX idx_charity_header_expiry ON charity_header_sessions(expires_at);
CREATE TABLE charity_header_uses (
 logical_request_id TEXT NOT NULL REFERENCES logical_requests(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 value TEXT NOT NULL CHECK(length(value) IN (32,36)),
 idle_seconds INTEGER NOT NULL CHECK(idle_seconds BETWEEN 1 AND 86400),
 sent INTEGER NOT NULL DEFAULT 0 CHECK(sent IN (0,1)),
 PRIMARY KEY(logical_request_id,name)
) STRICT;
CREATE INDEX idx_charity_header_active ON charity_header_uses(user_id,model_id,name,value);
