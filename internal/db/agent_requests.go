package db

// AgentRequestsSQL adds administrator-owned transport settings and history.
// These assets never cascade from user accounts or request detail cleanup.
const AgentRequestsSQL = `CREATE TABLE agent_request_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 settings_json TEXT NOT NULL CHECK(json_valid(settings_json) AND json_type(settings_json)='object' AND length(CAST(settings_json AS BLOB))<=65536),
 encrypted_key TEXT NOT NULL CHECK(length(encrypted_key)<=131072),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
CREATE TABLE agent_requests (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 client_request_id TEXT NOT NULL UNIQUE CHECK(length(client_request_id) BETWEEN 1 AND 128),
 source TEXT NOT NULL CHECK(source IN ('auto_review','admin_chat','steward_chat')),
 kind TEXT NOT NULL CHECK(kind IN ('model','compaction')),
 actor_id INTEGER NOT NULL CHECK(actor_id>=0),
 actor_role TEXT NOT NULL CHECK(actor_role IN ('admin','level5','level6')),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 input_json TEXT NOT NULL CHECK(json_valid(input_json) AND length(CAST(input_json AS BLOB))<=67108864),
 state TEXT NOT NULL CHECK(state IN ('pending','running','succeeded','failed','cancelled','interrupted')),
 base_url TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 request_body BLOB NOT NULL DEFAULT X'' CHECK(length(request_body)<=67108864),
 response_body BLOB NOT NULL DEFAULT X'' CHECK(length(response_body)<=67108864),
 response_complete INTEGER NOT NULL DEFAULT 0 CHECK(response_complete IN (0,1)),
 content_redacted INTEGER NOT NULL DEFAULT 0 CHECK(content_redacted IN (0,1)),
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 started_at INTEGER CHECK(started_at IS NULL OR started_at>=created_at),
 completed_at INTEGER CHECK(completed_at IS NULL OR completed_at>=created_at),
 consumed_at INTEGER CHECK(consumed_at IS NULL OR (consumed_at>=created_at AND state NOT IN ('pending','running'))),
 status_code INTEGER NOT NULL DEFAULT 0 CHECK(status_code=0 OR status_code BETWEEN 100 AND 599),
 error_code TEXT NOT NULL DEFAULT '' CHECK(length(error_code)<=64),
 usage_json TEXT NOT NULL DEFAULT '{"unknown":true}' CHECK(json_valid(usage_json)),
 CHECK((state IN ('pending','running') AND completed_at IS NULL) OR (state IN ('succeeded','failed','cancelled','interrupted') AND completed_at IS NOT NULL))
) STRICT;
CREATE TABLE agent_request_response_chunks (
 request_id INTEGER NOT NULL REFERENCES agent_requests(id) ON DELETE CASCADE,
 seq INTEGER NOT NULL CHECK(seq>=0),
 body BLOB NOT NULL CHECK(length(body)<=524288),
 redacted INTEGER NOT NULL CHECK(redacted IN (0,1)),
 PRIMARY KEY(request_id,seq)
) STRICT;
CREATE INDEX idx_agent_requests_unconsumed ON agent_requests(id) WHERE consumed_at IS NULL AND state NOT IN ('pending','running');
CREATE INDEX idx_agent_requests_cleanup ON agent_requests(id) WHERE consumed_at IS NOT NULL;
CREATE INDEX idx_agent_requests_pending ON agent_requests(state,id);
CREATE INDEX idx_agent_requests_source ON agent_requests(source,id);
CREATE INDEX idx_agent_requests_actor ON agent_requests(actor_id,id);
CREATE TABLE agent_request_usage (
 request_id INTEGER PRIMARY KEY,
 client_request_id TEXT NOT NULL UNIQUE,
 source TEXT NOT NULL CHECK(source IN ('auto_review','admin_chat','steward_chat')),
 kind TEXT NOT NULL CHECK(kind IN ('model','compaction')),
 actor_id INTEGER NOT NULL CHECK(actor_id>=0),
 actor_role TEXT NOT NULL CHECK(actor_role IN ('admin','level5','level6')),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 recorded_at INTEGER NOT NULL CHECK(recorded_at>=0),
 input_tokens INTEGER NOT NULL CHECK(input_tokens>=0),
 cached_input_tokens INTEGER NOT NULL CHECK(cached_input_tokens>=0),
 cache_write_input_tokens INTEGER NOT NULL CHECK(cache_write_input_tokens>=0),
 output_tokens INTEGER NOT NULL CHECK(output_tokens>=0),
 reasoning_tokens INTEGER NOT NULL CHECK(reasoning_tokens>=0),
 unknown INTEGER NOT NULL CHECK(unknown IN (0,1))
) STRICT;
CREATE INDEX idx_agent_request_usage_time ON agent_request_usage(recorded_at,source,kind);
CREATE TABLE agent_request_usage_totals (
 source TEXT NOT NULL,
 kind TEXT NOT NULL,
 actor_id INTEGER NOT NULL CHECK(actor_id>=0),
 actor_role TEXT NOT NULL CHECK(actor_role IN ('admin','level5','level6')),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 requests INTEGER NOT NULL CHECK(requests>=0),
 input_tokens INTEGER NOT NULL CHECK(input_tokens>=0),
 cached_input_tokens INTEGER NOT NULL CHECK(cached_input_tokens>=0),
 cache_write_input_tokens INTEGER NOT NULL CHECK(cache_write_input_tokens>=0),
 output_tokens INTEGER NOT NULL CHECK(output_tokens>=0),
 reasoning_tokens INTEGER NOT NULL CHECK(reasoning_tokens>=0),
 unknown_count INTEGER NOT NULL CHECK(unknown_count>=0),
 PRIMARY KEY(source,kind,actor_id)
) STRICT;
CREATE TABLE agent_request_usage_hours (
 hour INTEGER NOT NULL CHECK(hour>=0),
 source TEXT NOT NULL,
 kind TEXT NOT NULL,
 actor_id INTEGER NOT NULL CHECK(actor_id>=0),
 actor_role TEXT NOT NULL CHECK(actor_role IN ('admin','level5','level6')),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 requests INTEGER NOT NULL CHECK(requests>=0),
 input_tokens INTEGER NOT NULL CHECK(input_tokens>=0),
 cached_input_tokens INTEGER NOT NULL CHECK(cached_input_tokens>=0),
 cache_write_input_tokens INTEGER NOT NULL CHECK(cache_write_input_tokens>=0),
 output_tokens INTEGER NOT NULL CHECK(output_tokens>=0),
 reasoning_tokens INTEGER NOT NULL CHECK(reasoning_tokens>=0),
 unknown_count INTEGER NOT NULL CHECK(unknown_count>=0),
 PRIMARY KEY(hour,source,kind,actor_id)
) STRICT;
`
