CREATE TABLE agent_driver_profiles (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 revision INTEGER NOT NULL CHECK(revision>0),
 name TEXT NOT NULL CHECK(length(CAST(name AS BLOB)) BETWEEN 1 AND 200),
 settings_json TEXT NOT NULL CHECK(json_valid(settings_json) AND json_type(settings_json)='object' AND length(CAST(settings_json AS BLOB))<=65536),
 encrypted_key TEXT NOT NULL DEFAULT '' CHECK(length(encrypted_key)<=131072),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
CREATE TABLE agent_driver_bindings (
 scope TEXT NOT NULL CHECK(scope IN ('default','auto_review','admin_chat','steward_chat')),
 actor_id INTEGER NOT NULL CHECK(actor_id>=0 AND (actor_id=0 OR scope='steward_chat')),
 driver_id INTEGER NOT NULL CHECK(driver_id>=0),
 revision INTEGER NOT NULL CHECK(revision>0),
 PRIMARY KEY(scope,actor_id)
) STRICT;
CREATE TABLE agent_request_drivers (
 request_id INTEGER PRIMARY KEY REFERENCES agent_requests(id) ON DELETE CASCADE,
 driver_id INTEGER NOT NULL CHECK(driver_id>=0),
 provider TEXT NOT NULL CHECK(provider IN ('chat_completions','responses','chatgpt')),
 settings_json TEXT NOT NULL CHECK(json_valid(settings_json) AND length(CAST(settings_json AS BLOB))<=65536)
) STRICT;
CREATE TABLE agent_chatgpt_session (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 host_id TEXT NOT NULL CHECK(length(host_id) BETWEEN 1 AND 256),
 public_key TEXT NOT NULL CHECK(length(public_key)<=256),
 encrypted_private_key TEXT NOT NULL CHECK(length(encrypted_private_key)<=4096),
 encrypted_credentials TEXT NOT NULL DEFAULT '' CHECK(length(encrypted_credentials)<=262144),
 client_id TEXT NOT NULL DEFAULT '' CHECK(length(client_id)<=256),
 subject TEXT NOT NULL DEFAULT '' CHECK(length(subject)<=512),
 account_label TEXT NOT NULL DEFAULT '' CHECK(length(CAST(account_label AS BLOB))<=512),
 expires_at INTEGER NOT NULL DEFAULT 0 CHECK(expires_at>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
