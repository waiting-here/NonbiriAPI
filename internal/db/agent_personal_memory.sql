CREATE TABLE ai_agent_personal_memories (
 actor_id INTEGER PRIMARY KEY CHECK(actor_id>0),
 actor_role TEXT NOT NULL CHECK(actor_role IN ('admin','level5','level6')),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 content TEXT NOT NULL DEFAULT '' CHECK(length(CAST(content AS BLOB))<=4194304),
 revision INTEGER NOT NULL CHECK(revision>0),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0),
 updated_by TEXT NOT NULL CHECK(length(CAST(updated_by AS BLOB))<=512)
) STRICT;
CREATE INDEX ai_agent_personal_memories_updated ON ai_agent_personal_memories(updated_at,actor_id);
CREATE TABLE ai_agent_personal_memory_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 owner_id INTEGER NOT NULL CHECK(owner_id>0),
 before_content TEXT NOT NULL CHECK(length(CAST(before_content AS BLOB))<=4194304),
 after_content TEXT NOT NULL CHECK(length(CAST(after_content AS BLOB))<=4194304),
 revision INTEGER NOT NULL CHECK(revision>0),
 actor_id INTEGER NOT NULL CHECK(actor_id>0),
 actor_label TEXT NOT NULL CHECK(length(CAST(actor_label AS BLOB))<=512),
 created_at INTEGER NOT NULL CHECK(created_at>=0)
) STRICT;
CREATE INDEX ai_agent_personal_memory_history_owner ON ai_agent_personal_memory_history(owner_id,id);
