CREATE TABLE management_operations (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL UNIQUE CHECK(length(CAST(event_id AS BLOB))<=128),
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 bucket INTEGER NOT NULL,
 group_key TEXT NOT NULL CHECK(length(CAST(group_key AS BLOB))<=64),
 source TEXT NOT NULL CHECK(source IN ('web','caller_key','ai_manual','ai_auto','mainstream_rule')) CHECK(length(CAST(source AS BLOB))<=32),
 executor_kind TEXT NOT NULL CHECK(length(CAST(executor_kind AS BLOB))<=160),
 actor_id TEXT NOT NULL DEFAULT '' CHECK(length(CAST(actor_id AS BLOB))<=192),
 actor_name TEXT NOT NULL DEFAULT '' CHECK(length(CAST(actor_name AS BLOB))<=256),
 actor_role TEXT NOT NULL DEFAULT '' CHECK(length(CAST(actor_role AS BLOB))<=64),
 authorizer_id TEXT NOT NULL DEFAULT '' CHECK(length(CAST(authorizer_id AS BLOB))<=192),
 authorizer_name TEXT NOT NULL DEFAULT '' CHECK(length(CAST(authorizer_name AS BLOB))<=256),
 authorizer_role TEXT NOT NULL DEFAULT '' CHECK(length(CAST(authorizer_role AS BLOB))<=64),
 authorization_ref TEXT NOT NULL DEFAULT '' CHECK(length(CAST(authorization_ref AS BLOB))<=192),
 domain TEXT NOT NULL CHECK(length(CAST(domain AS BLOB))<=160),
 action TEXT NOT NULL CHECK(length(CAST(action AS BLOB))<=160),
 correlation_kind TEXT NOT NULL DEFAULT '' CHECK(length(CAST(correlation_kind AS BLOB))<=160),
 correlation_id TEXT NOT NULL DEFAULT '' CHECK(length(CAST(correlation_id AS BLOB))<=192),
 outcome TEXT NOT NULL CHECK(outcome IN ('succeeded','failed','rejected','no_change','partial','accepted','unknown')) CHECK(length(CAST(outcome AS BLOB))<=16),
 code TEXT NOT NULL DEFAULT '' CHECK(length(CAST(code AS BLOB))<=128),
 summary TEXT NOT NULL DEFAULT '' CHECK(length(CAST(summary AS BLOB))<=1024),
 targets INTEGER NOT NULL DEFAULT 0 CHECK(targets>=0),
 succeeded INTEGER NOT NULL DEFAULT 0 CHECK(succeeded>=0),
 skipped INTEGER NOT NULL DEFAULT 0 CHECK(skipped>=0),
 failed INTEGER NOT NULL DEFAULT 0 CHECK(failed>=0),
 cleaning INTEGER NOT NULL DEFAULT 0 CHECK(cleaning IN (0,1))
) STRICT;
CREATE INDEX management_operations_time ON management_operations(created_at DESC,id DESC);
CREATE INDEX management_operations_source ON management_operations(source,created_at DESC,id DESC);
CREATE INDEX management_operations_actor ON management_operations(actor_id,created_at DESC,id DESC);
CREATE INDEX management_operations_authorizer ON management_operations(authorizer_id,created_at DESC,id DESC);
CREATE INDEX management_operations_action ON management_operations(domain,action,created_at DESC,id DESC);
CREATE INDEX management_operations_outcome ON management_operations(outcome,created_at DESC,id DESC);
CREATE INDEX management_operations_group ON management_operations(group_key,id);
CREATE INDEX management_operations_correlation ON management_operations(correlation_kind,correlation_id,id);
CREATE INDEX management_operations_actor_name ON management_operations(actor_name,created_at DESC,id DESC);
CREATE TABLE management_operation_items (
 operation_id INTEGER NOT NULL REFERENCES management_operations(id),
 ordinal INTEGER NOT NULL CHECK(ordinal>0),
 action TEXT NOT NULL DEFAULT '' CHECK(length(CAST(action AS BLOB))<=160),
 target_kind TEXT NOT NULL DEFAULT '' CHECK(length(CAST(target_kind AS BLOB))<=96),
 target_id TEXT NOT NULL DEFAULT '' CHECK(length(CAST(target_id AS BLOB))<=192),
 target_name TEXT NOT NULL DEFAULT '' CHECK(length(CAST(target_name AS BLOB))<=256),
 outcome TEXT NOT NULL CHECK(outcome IN ('succeeded','failed','rejected','no_change','partial','accepted','unknown')) CHECK(length(CAST(outcome AS BLOB))<=16),
 code TEXT NOT NULL DEFAULT '' CHECK(length(CAST(code AS BLOB))<=128),
 changes TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(changes) AND json_type(changes)='array') CHECK(length(CAST(changes AS BLOB))<=16384),
 PRIMARY KEY(operation_id,ordinal)
) STRICT, WITHOUT ROWID;
CREATE INDEX management_operation_items_target ON management_operation_items(target_kind,target_id,operation_id);
CREATE TABLE management_operation_rollups (
 group_key TEXT PRIMARY KEY CHECK(length(CAST(group_key AS BLOB))<=64),
 bucket INTEGER NOT NULL,
 source TEXT NOT NULL CHECK(length(CAST(source AS BLOB))<=32),
 domain TEXT NOT NULL CHECK(length(CAST(domain AS BLOB))<=160),
 action TEXT NOT NULL CHECK(length(CAST(action AS BLOB))<=160),
 actor_id TEXT NOT NULL CHECK(length(CAST(actor_id AS BLOB))<=192),
 actor_name TEXT NOT NULL CHECK(length(CAST(actor_name AS BLOB))<=256),
 actor_role TEXT NOT NULL CHECK(length(CAST(actor_role AS BLOB))<=64),
 authorizer_id TEXT NOT NULL CHECK(length(CAST(authorizer_id AS BLOB))<=192),
 authorizer_name TEXT NOT NULL CHECK(length(CAST(authorizer_name AS BLOB))<=256),
 authorizer_role TEXT NOT NULL CHECK(length(CAST(authorizer_role AS BLOB))<=64),
 outcome TEXT NOT NULL CHECK(length(CAST(outcome AS BLOB))<=16),
 operations INTEGER NOT NULL CHECK(operations>=0),
 targets INTEGER NOT NULL CHECK(targets>=0),
 succeeded INTEGER NOT NULL CHECK(succeeded>=0),
 skipped INTEGER NOT NULL CHECK(skipped>=0),
 failed INTEGER NOT NULL CHECK(failed>=0),
 min_id INTEGER NOT NULL,
 max_id INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX management_operation_rollups_time ON management_operation_rollups(bucket DESC,group_key);
CREATE INDEX management_operation_rollups_source ON management_operation_rollups(source,bucket DESC,group_key);
CREATE INDEX management_operation_rollups_actor ON management_operation_rollups(actor_id,bucket DESC,group_key);
CREATE INDEX management_operation_rollups_authorizer ON management_operation_rollups(authorizer_id,bucket DESC,group_key);
CREATE INDEX management_operation_rollups_action ON management_operation_rollups(domain,action,bucket DESC,group_key);
CREATE TABLE management_operation_cleanup_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 request_id TEXT NOT NULL UNIQUE CHECK(length(CAST(request_id AS BLOB))<=128),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 actor_id TEXT NOT NULL CHECK(length(CAST(actor_id AS BLOB))<=192),
 actor_name TEXT NOT NULL CHECK(length(CAST(actor_name AS BLOB))<=256),
 actor_role TEXT NOT NULL CHECK(length(CAST(actor_role AS BLOB))<=64),
 filter_json TEXT NOT NULL CHECK(json_valid(filter_json)) CHECK(length(CAST(filter_json AS BLOB))<=8192),
 cutoff_max_id INTEGER NOT NULL CHECK(cutoff_max_id>=0),
 state TEXT NOT NULL CHECK(state IN ('pending','running','completed','failed','cancelled')) CHECK(length(CAST(state AS BLOB))<=16),
 current_operation_id INTEGER NOT NULL DEFAULT 0,
 after_operation_id INTEGER NOT NULL DEFAULT 0 CHECK(after_operation_id>=0),
 deleted_operations INTEGER NOT NULL DEFAULT 0,
 deleted_items INTEGER NOT NULL DEFAULT 0,
 skipped_active INTEGER NOT NULL DEFAULT 0 CHECK(skipped_active>=0),
 error_code TEXT NOT NULL DEFAULT '' CHECK(length(CAST(error_code AS BLOB))<=128),
 start_operation_id INTEGER NOT NULL DEFAULT 0,
 finish_operation_id INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX management_operation_cleanup_pending ON management_operation_cleanup_jobs(state,id);
