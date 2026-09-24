package db

import "strings"

const imageActivitySchema = `
CREATE TABLE image_upstream_control (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='iup_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 identity_hash BLOB NOT NULL UNIQUE CHECK(length(identity_hash)=32),
 rpm_limit INTEGER NOT NULL CHECK(rpm_limit BETWEEN 1 AND 10000),
 concurrency_limit INTEGER NOT NULL CHECK(concurrency_limit BETWEEN 1 AND 32),
 http_times BLOB NOT NULL DEFAULT X'' CHECK(length(http_times)<=80000 AND length(http_times)%8=0),
 protection_paused INTEGER NOT NULL CHECK(protection_paused IN (0,1)),
 protection_reason TEXT NOT NULL CHECK(protection_reason IN ('','receipt_unknown','execution_timeout','recovery_uncertain')),
 protection_revision INTEGER NOT NULL CHECK(protection_revision>=1),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((protection_paused=0 AND protection_reason='') OR (protection_paused=1 AND protection_reason<>''))
) STRICT;
CREATE TABLE image_upstream_revisions (
 revision INTEGER PRIMARY KEY CHECK(revision>=1),
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id),
 base_url TEXT NOT NULL CHECK(length(CAST(base_url AS BLOB)) BETWEEN 1 AND 4096),
 secret_context BLOB NOT NULL CHECK(length(secret_context)=16),
 secret_ciphertext TEXT NOT NULL CHECK(length(CAST(secret_ciphertext AS BLOB)) BETWEEN 1 AND 131072),
 adapter_json TEXT NOT NULL CHECK(json_valid(adapter_json) AND length(CAST(adapter_json AS BLOB))<=262144),
 image_origins_json TEXT NOT NULL CHECK(json_valid(image_origins_json) AND json_type(image_origins_json)='array' AND json_array_length(image_origins_json)<=8),
 per_user_limit INTEGER NOT NULL CHECK(per_user_limit BETWEEN 1 AND 100),
 global_limit INTEGER NOT NULL CHECK(global_limit BETWEEN 1 AND 10000),
 queue_timeout_seconds INTEGER NOT NULL CHECK(queue_timeout_seconds BETWEEN 60 AND 86400),
 execution_timeout_seconds INTEGER NOT NULL CHECK(execution_timeout_seconds BETWEEN 60 AND 86400),
 memory_budget_mib INTEGER NOT NULL CHECK(memory_budget_mib BETWEEN 512 AND 4096),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE image_activity_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 upstream_revision INTEGER REFERENCES image_upstream_revisions(revision),
 task_rows INTEGER NOT NULL CHECK(task_rows BETWEEN 0 AND 1000000),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE image_activity_models (
 id TEXT PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5)='imdl_' AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id),
 upstream_model_id TEXT NOT NULL CHECK(length(CAST(upstream_model_id AS BLOB)) BETWEEN 1 AND 2048),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND length(CAST(metadata_json AS BLOB))<=32768),
 current_revision INTEGER CHECK(current_revision>=1),
 discovered_at INTEGER NOT NULL CHECK(discovered_at BETWEEN 0 AND 253402300799),
 UNIQUE(control_id,upstream_model_id),
 FOREIGN KEY(id,current_revision) REFERENCES image_model_revisions(model_id,revision) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE TABLE image_model_revisions (
 model_id TEXT NOT NULL REFERENCES image_activity_models(id),
 revision INTEGER NOT NULL CHECK(revision>=1),
 display_name TEXT NOT NULL CHECK(length(display_name) BETWEEN 1 AND 128 AND length(CAST(display_name AS BLOB))<=512),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=4096),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 parameters_json TEXT NOT NULL CHECK(json_valid(parameters_json) AND length(CAST(parameters_json AS BLOB))<=65536),
 combinations_json TEXT NOT NULL CHECK(json_valid(combinations_json) AND length(CAST(combinations_json AS BLOB))<=65536),
 mapping_json TEXT NOT NULL CHECK(json_valid(mapping_json) AND length(CAST(mapping_json AS BLOB))<=65536),
 paper_price_mag BLOB NOT NULL CHECK(length(paper_price_mag)=16 AND paper_price_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF'),
 brush_price_mag BLOB NOT NULL CHECK(length(brush_price_mag)=16 AND brush_price_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF'),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(model_id,revision),
 CHECK(paper_price_mag<>X'00000000000000000000000000000000' OR brush_price_mag<>X'00000000000000000000000000000000')
) STRICT;
CREATE TABLE image_activity_tasks (
 accepted_seq INTEGER PRIMARY KEY AUTOINCREMENT CHECK(accepted_seq>0),
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='img_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 model_id TEXT,
 model_revision INTEGER,
 upstream_revision INTEGER REFERENCES image_upstream_revisions(revision),
 control_id TEXT REFERENCES image_upstream_control(id),
 n INTEGER CHECK(n BETWEEN 1 AND 16),
 paper_charge_mag BLOB CHECK(paper_charge_mag IS NULL OR (length(paper_charge_mag)=16 AND paper_charge_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF')),
 brush_charge_mag BLOB CHECK(brush_charge_mag IS NULL OR (length(brush_charge_mag)=16 AND brush_charge_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF')),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','running','succeeded','failed','cancelled','unknown_refunded')),
 finance_state TEXT NOT NULL CHECK(finance_state IN ('reserved','settled','refunded','deleted')),
 slot_state TEXT NOT NULL CHECK(slot_state IN ('none','held','uncertain','ignored')),
 ledger_rows_remaining BLOB NOT NULL CHECK(ledger_rows_remaining IN (X'00000000000000000000000000000000',X'00000000000000000000000000000001')),
 reserve_operation_id TEXT REFERENCES credit_operations(id),
 terminal_operation_id TEXT REFERENCES credit_operations(id),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 queue_deadline INTEGER NOT NULL CHECK(queue_deadline BETWEEN created_at AND 253402300799),
 execution_timeout_seconds INTEGER NOT NULL CHECK(execution_timeout_seconds BETWEEN 60 AND 86400),
 dispatched_at INTEGER CHECK(dispatched_at BETWEEN created_at AND 253402300799),
 execution_deadline INTEGER CHECK(execution_deadline BETWEEN dispatched_at AND 253402300799),
 completed_at INTEGER CHECK(completed_at BETWEEN created_at AND 253402300799),
 upstream_task_id TEXT CHECK(length(CAST(upstream_task_id AS BLOB)) BETWEEN 1 AND 2048),
 next_poll_at INTEGER CHECK(next_poll_at BETWEEN 0 AND 253402300799),
 poll_count INTEGER NOT NULL DEFAULT 0 CHECK(poll_count BETWEEN 0 AND 2147483647),
 http_seq INTEGER NOT NULL DEFAULT 0 CHECK(http_seq BETWEEN 0 AND 2147483647),
 cleanup_deadline INTEGER CHECK(cleanup_deadline BETWEEN 0 AND 253402300799),
 actual_images INTEGER NOT NULL DEFAULT 0 CHECK(actual_images BETWEEN 0 AND 16),
 result_expires_at INTEGER CHECK(result_expires_at BETWEEN 0 AND 253402300799),
 error_code TEXT CHECK(error_code IN ('upstream_failed','invalid_result','response_too_large','execution_timeout','result_unknown','queue_timeout','cancelled_by_user','activity_paused','maintenance','model_unavailable','account_restricted','service_restarted')),
 FOREIGN KEY(model_id,model_revision) REFERENCES image_model_revisions(model_id,revision),
 CHECK((model_id IS NULL)=(model_revision IS NULL)),
 CHECK((dispatched_at IS NULL)=(execution_deadline IS NULL)),
 CHECK((finance_state='reserved' AND ledger_rows_remaining=X'00000000000000000000000000000001') OR (finance_state<>'reserved' AND ledger_rows_remaining=X'00000000000000000000000000000000')),
 CHECK(user_id IS NOT NULL OR (finance_state='deleted' AND model_id IS NULL AND model_revision IS NULL AND n IS NULL AND paper_charge_mag IS NULL AND brush_charge_mag IS NULL AND reserve_operation_id IS NULL AND terminal_operation_id IS NULL AND actual_images=0 AND result_expires_at IS NULL)),
 CHECK(user_id IS NULL OR (model_id IS NOT NULL AND n IS NOT NULL AND paper_charge_mag IS NOT NULL AND brush_charge_mag IS NOT NULL)),
 CHECK(state<>'queued' OR (user_id IS NOT NULL AND finance_state='reserved' AND slot_state='none' AND dispatched_at IS NULL)),
 CHECK(state NOT IN ('dispatching','running') OR dispatched_at IS NOT NULL),
 CHECK(state<>'running' OR upstream_task_id IS NOT NULL),
 CHECK(state NOT IN ('succeeded','failed','cancelled','unknown_refunded') OR (completed_at IS NOT NULL AND finance_state<>'reserved'))
) STRICT;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,created_at,id);
CREATE INDEX idx_image_activity_tasks_fifo ON image_activity_tasks(state,accepted_seq);
CREATE INDEX idx_image_activity_tasks_unfinished ON image_activity_tasks(user_id,finance_state,accepted_seq);
CREATE INDEX idx_image_activity_tasks_poll ON image_activity_tasks(next_poll_at,accepted_seq) WHERE next_poll_at IS NOT NULL;
CREATE INDEX idx_image_activity_tasks_deadlines ON image_activity_tasks(state,execution_deadline,queue_deadline,accepted_seq);
CREATE INDEX idx_image_activity_tasks_retention ON image_activity_tasks(completed_at,accepted_seq) WHERE completed_at IS NOT NULL;
CREATE INDEX idx_image_activity_tasks_slots ON image_activity_tasks(control_id,slot_state,accepted_seq);
CREATE TRIGGER image_tasks_capacity_before_insert BEFORE INSERT ON image_activity_tasks
WHEN (SELECT task_rows FROM image_activity_state WHERE id=1)>=1000000
BEGIN SELECT RAISE(ABORT,'image task capacity exhausted'); END;
CREATE TRIGGER image_tasks_count_insert AFTER INSERT ON image_activity_tasks
BEGIN UPDATE image_activity_state SET task_rows=task_rows+1 WHERE id=1; END;
CREATE TRIGGER image_tasks_count_delete AFTER DELETE ON image_activity_tasks
BEGIN UPDATE image_activity_state SET task_rows=task_rows-1 WHERE id=1; END;
CREATE TABLE image_model_refreshes (
 operation_id TEXT PRIMARY KEY REFERENCES accepted_operations(id) ON DELETE CASCADE,
 upstream_revision INTEGER NOT NULL REFERENCES image_upstream_revisions(revision),
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 deadline INTEGER NOT NULL CHECK(deadline BETWEEN created_at AND 253402300799),
 completed_at INTEGER CHECK(completed_at BETWEEN created_at AND 253402300799),
 model_count INTEGER NOT NULL DEFAULT 0 CHECK(model_count BETWEEN 0 AND 1000),
 http_seq INTEGER NOT NULL DEFAULT 0 CHECK(http_seq BETWEEN 0 AND 2147483647),
 error_code TEXT CHECK(error_code IN ('upstream_failed','invalid_result','response_too_large','execution_timeout','service_restarted'))
) STRICT;
CREATE INDEX idx_image_model_refreshes_due ON image_model_refreshes(state,deadline,operation_id);
CREATE TABLE image_upstream_resume_audits (
 operation_id TEXT PRIMARY KEY REFERENCES accepted_operations(id) ON DELETE CASCADE,
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 previous_revision INTEGER NOT NULL CHECK(previous_revision>=1),
 resulting_revision INTEGER NOT NULL CHECK(resulting_revision>previous_revision),
 reason TEXT NOT NULL CHECK(length(CAST(reason AS BLOB)) BETWEEN 1 AND 1024),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE image_task_sources (
 task_id TEXT PRIMARY KEY REFERENCES image_activity_tasks(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 effective_ip TEXT NOT NULL CHECK(length(effective_ip)<=45),
 ip_quality TEXT NOT NULL CHECK(ip_quality IN ('direct_peer','trusted_forwarded','peer_fallback')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))<=8192),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_image_sources_user_time ON image_task_sources(user_id,occurred_at,task_id);

CREATE TRIGGER image_upstream_revisions_immutable BEFORE UPDATE ON image_upstream_revisions
WHEN NEW.revision IS NOT OLD.revision OR NEW.control_id IS NOT OLD.control_id OR NEW.base_url IS NOT OLD.base_url OR NEW.secret_context IS NOT OLD.secret_context OR NEW.secret_ciphertext IS NOT OLD.secret_ciphertext OR NEW.adapter_json IS NOT OLD.adapter_json OR NEW.image_origins_json IS NOT OLD.image_origins_json OR NEW.per_user_limit IS NOT OLD.per_user_limit OR NEW.global_limit IS NOT OLD.global_limit OR NEW.queue_timeout_seconds IS NOT OLD.queue_timeout_seconds OR NEW.execution_timeout_seconds IS NOT OLD.execution_timeout_seconds OR NEW.memory_budget_mib IS NOT OLD.memory_budget_mib OR NEW.created_at IS NOT OLD.created_at
 OR (NEW.actor_user_id IS NOT OLD.actor_user_id AND NEW.actor_user_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'image configuration revision is immutable'); END;

CREATE TRIGGER image_model_revisions_immutable BEFORE UPDATE ON image_model_revisions
WHEN NEW.model_id IS NOT OLD.model_id OR NEW.revision IS NOT OLD.revision OR NEW.display_name IS NOT OLD.display_name OR NEW.description IS NOT OLD.description OR NEW.enabled IS NOT OLD.enabled OR NEW.parameters_json IS NOT OLD.parameters_json OR NEW.combinations_json IS NOT OLD.combinations_json OR NEW.mapping_json IS NOT OLD.mapping_json OR NEW.paper_price_mag IS NOT OLD.paper_price_mag OR NEW.brush_price_mag IS NOT OLD.brush_price_mag OR NEW.created_at IS NOT OLD.created_at
 OR (NEW.actor_user_id IS NOT OLD.actor_user_id AND NEW.actor_user_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'image configuration revision is immutable'); END;

`

func imageActivityGuardsSchema() string {
	var b strings.Builder
	for _, event := range []string{"INSERT", "UPDATE"} {
		for _, table := range []struct{ name, paper, brush string }{
			{"image_model_revisions", "paper_price_mag", "brush_price_mag"},
			{"image_activity_tasks", "paper_charge_mag", "brush_charge_mag"},
		} {
			b.WriteString("CREATE TRIGGER " + table.name + "_whole_" + strings.ToLower(event) + " BEFORE " + event + " ON " + table.name + "\n")
			b.WriteString("WHEN (NEW." + table.paper + " IS NOT NULL AND NOT " + governanceWholeAsset("NEW."+table.paper) + ") OR (NEW." + table.brush + " IS NOT NULL AND NOT " + governanceWholeAsset("NEW."+table.brush) + ")\n")
			b.WriteString("BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;\n")
		}
	}
	return b.String()
}
