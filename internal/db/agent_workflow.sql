CREATE TABLE ai_agent_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 mode TEXT NOT NULL DEFAULT 'off' CHECK(mode IN ('off','preview','auto')),
 config_json TEXT NOT NULL CHECK(json_valid(config_json)),
 search_secret BLOB,
 owner_id INTEGER NOT NULL,
 owner_label TEXT NOT NULL,
 updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE ai_agent_memories (
 kind TEXT PRIMARY KEY CHECK(kind IN ('auto_review','chat')),
 content TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 updated_at INTEGER NOT NULL,
 updated_by TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE TABLE ai_agent_memory_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL,
 before_content TEXT NOT NULL,
 after_content TEXT NOT NULL,
 revision INTEGER NOT NULL,
 actor_id INTEGER NOT NULL,
 actor_label TEXT NOT NULL,
 created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE ai_agent_conversations (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 actor_id INTEGER NOT NULL UNIQUE,
 actor_role TEXT NOT NULL,
 actor_label TEXT NOT NULL,
 generation INTEGER NOT NULL DEFAULT 1,
 summary TEXT NOT NULL DEFAULT '',
 summary_through_id INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE ai_agent_messages (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 conversation_id INTEGER NOT NULL REFERENCES ai_agent_conversations(id),
 generation INTEGER NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('system','user','assistant','tool')),
 content TEXT NOT NULL,
 tool_calls TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tool_calls)),
 tool_call_id TEXT NOT NULL DEFAULT '',
 wire_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(wire_json)),
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX ai_agent_messages_active ON ai_agent_messages(conversation_id,active,id);

CREATE TABLE ai_agent_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL CHECK(kind IN ('chat','compact','auto_review')),
 actor_id INTEGER NOT NULL,
 actor_role TEXT NOT NULL,
 actor_label TEXT NOT NULL,
 session_hash TEXT NOT NULL DEFAULT '',
 session_generation TEXT NOT NULL DEFAULT '',
 client_key TEXT NOT NULL DEFAULT '',
 client_digest TEXT NOT NULL DEFAULT '',
 conversation_id INTEGER REFERENCES ai_agent_conversations(id),
 generation INTEGER NOT NULL DEFAULT 0,
 donation_id INTEGER REFERENCES donations(id) ON DELETE CASCADE,
 revision_signature TEXT NOT NULL DEFAULT '',
 mode TEXT NOT NULL DEFAULT 'preview',
 state TEXT NOT NULL CHECK(state IN ('pending','running','waiting_confirmation','completed','failed','cancelled','interrupted')),
 request_id INTEGER NOT NULL DEFAULT 0,
 request_sequence INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 intelligence TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(intelligence)),
 review_result TEXT,
 execution TEXT,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
) STRICT;
CREATE INDEX ai_agent_jobs_state ON ai_agent_jobs(state,id);
CREATE INDEX ai_agent_jobs_review ON ai_agent_jobs(kind,id);
CREATE INDEX ai_agent_jobs_conversation ON ai_agent_jobs(conversation_id,id);
CREATE TABLE ai_agent_model_results (
 request_id INTEGER PRIMARY KEY,
 job_id INTEGER NOT NULL REFERENCES ai_agent_jobs(id) ON DELETE CASCADE,
 kind TEXT NOT NULL,
 message TEXT NOT NULL CHECK(json_valid(message)),
 error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX ai_agent_model_results_job ON ai_agent_model_results(job_id,request_id);
CREATE UNIQUE INDEX ai_agent_jobs_client_key ON ai_agent_jobs(actor_id,client_key) WHERE client_key<>'';
CREATE UNIQUE INDEX ai_agent_jobs_active_conversation ON ai_agent_jobs(conversation_id)
 WHERE conversation_id IS NOT NULL AND state IN ('pending','running','waiting_confirmation');

CREATE TABLE ai_agent_review_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 donation_id INTEGER NOT NULL UNIQUE REFERENCES donations(id) ON DELETE CASCADE,
 sequence INTEGER NOT NULL DEFAULT 1,
 mode TEXT NOT NULL CHECK(mode IN ('preview','auto')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE ai_agent_plans (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 job_id INTEGER NOT NULL REFERENCES ai_agent_jobs(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('pending','approved','rejected','failed','superseded')),
 operations TEXT NOT NULL CHECK(json_valid(operations)),
 preview TEXT NOT NULL CHECK(json_valid(preview)),
 tool_call_ids TEXT NOT NULL CHECK(json_valid(tool_call_ids)),
 result TEXT,
 created_at INTEGER NOT NULL,
 decided_at INTEGER
) STRICT;
CREATE INDEX ai_agent_plans_job ON ai_agent_plans(job_id,id);
