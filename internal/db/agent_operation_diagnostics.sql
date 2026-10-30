CREATE TABLE agent_operation_diagnostics (
 id TEXT PRIMARY KEY CHECK(length(id)=32 AND id NOT GLOB '*[^0-9a-f]*'),
 operation TEXT NOT NULL CHECK(operation IN ('device_code','device_poll','token_exchange','token_refresh','model_catalogue','codex_quota','codex_local_usage')),
 source_name TEXT NOT NULL CHECK(length(CAST(source_name AS BLOB))<=200),
 site_status INTEGER NOT NULL CHECK(site_status IN (400,409,500,502,503,504)),
 upstream_code TEXT NOT NULL DEFAULT '' CHECK(length(CAST(upstream_code AS BLOB))<=64),
 details TEXT NOT NULL CHECK(length(CAST(details AS BLOB))<=16384 AND json_valid(details) AND json_type(details)='object'),
 created_at INTEGER NOT NULL CHECK(created_at>=0)
) STRICT;
CREATE INDEX idx_agent_operation_diagnostics_time ON agent_operation_diagnostics(created_at,id);
