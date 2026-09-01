// Package db owns the canonical Generation 2 SQLite schema.
//
// This string is the only DDL source used by fresh bootstrap and manifest
// validation. Generation 2 is a destructive fresh database: no migration,
// compatibility view, or legacy table is intentionally present here.
package db

// no runtime DDL transformation is permitted

// generationTwoSchema is deliberately non-idempotent. Keep all scalar
// constraints in this source so that the startup manifest is an exact lock,
// rather than a best-effort list of tables.
const generationTwoSchema = `
PRAGMA foreign_keys=ON;

-- ===== identity and inherited alpha.3 facts ================================
CREATE TABLE users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 discord_id TEXT UNIQUE,
 username TEXT NOT NULL DEFAULT '',
 avatar TEXT NOT NULL DEFAULT '',
 guild_nick TEXT NOT NULL DEFAULT '',
 guild_avatar_url TEXT NOT NULL DEFAULT '',
 is_admin INTEGER NOT NULL DEFAULT 0 CHECK(is_admin IN (0,1)),
 is_banned INTEGER NOT NULL DEFAULT 0 CHECK(is_banned IN (0,1)),
 banned_reason TEXT NOT NULL DEFAULT '',
 banned_until INTEGER,
 auto_banned INTEGER NOT NULL DEFAULT 0 CHECK(auto_banned IN (0,1)),
 charity_suspended_until INTEGER,
 endpoint_limit INTEGER,
 rpm_limit INTEGER,
 concurrency_limit INTEGER,
 game_profile_public INTEGER NOT NULL DEFAULT 0 CHECK(game_profile_public IN (0,1)),
 donation_credit_mag BLOB NOT NULL CHECK(typeof(donation_credit_mag)='blob' AND length(donation_credit_mag)=16),
 level INTEGER,
 auto_level INTEGER NOT NULL DEFAULT 1 CHECK(auto_level BETWEEN 1 AND 4),
 total_requests BLOB NOT NULL CHECK(typeof(total_requests)='blob' AND length(total_requests)=16),
 total_uncached_input_tokens BLOB NOT NULL CHECK(typeof(total_uncached_input_tokens)='blob' AND length(total_uncached_input_tokens)=16),
 total_cache_write_input_tokens BLOB NOT NULL CHECK(typeof(total_cache_write_input_tokens)='blob' AND length(total_cache_write_input_tokens)=16),
 total_cache_read_input_tokens BLOB NOT NULL CHECK(typeof(total_cache_read_input_tokens)='blob' AND length(total_cache_read_input_tokens)=16),
 total_output_tokens BLOB NOT NULL CHECK(typeof(total_output_tokens)='blob' AND length(total_output_tokens)=16),
 total_unknown_usage_requests BLOB NOT NULL CHECK(typeof(total_unknown_usage_requests)='blob' AND length(total_unknown_usage_requests)=16),
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16),
 lang TEXT NOT NULL DEFAULT '' CHECK(lang IN ('','zh','en')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK(endpoint_limit IS NULL OR endpoint_limit BETWEEN 0 AND 10000),
 CHECK(rpm_limit IS NULL OR rpm_limit BETWEEN 1 AND 4096),
 CHECK(concurrency_limit IS NULL OR concurrency_limit BETWEEN 1 AND 100000),
 CHECK(level IS NULL OR level BETWEEN 1 AND 5)
);
CREATE INDEX idx_users_created ON users(created_at);
CREATE UNIQUE INDEX idx_users_one_admin ON users(is_admin) WHERE is_admin=1;
-- A short-lived marker lets the trusted account-deletion trigger distinguish
-- its coordinated maintenance-audit de-identification from a direct UPDATE.
-- It is inserted and removed in the same BEFORE DELETE trigger invocation.
CREATE TABLE user_deletion_markers (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE sessions (
 token_hash TEXT NOT NULL PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 oauth_state TEXT NOT NULL DEFAULT '',
 last_seen_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 absolute_expires_at INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 cred_gen TEXT NOT NULL DEFAULT '',
 CHECK(last_seen_at BETWEEN 0 AND 253402300799),
 CHECK(expires_at BETWEEN 0 AND 253402300799),
 CHECK(absolute_expires_at BETWEEN 0 AND 253402300799),
 CHECK(created_at BETWEEN 0 AND 253402300799),
 CHECK(last_seen_at<=expires_at AND expires_at<=absolute_expires_at)
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_absolute ON sessions(absolute_expires_at);

CREATE TABLE policy_audits (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 actor_role TEXT NOT NULL CHECK(actor_role IN ('owner','admin','level5')),
 resource_type TEXT NOT NULL CHECK(resource_type IN ('endpoint_key','model','charity_model')),
 resource_id INTEGER NOT NULL CHECK(resource_id>0),
 policy TEXT NOT NULL CHECK(policy IN ('force_store_false','flatten_tool_calls')),
 old_value INTEGER NOT NULL CHECK(old_value IN (0,1)),
 new_value INTEGER NOT NULL CHECK(new_value IN (0,1)),
 created_at INTEGER NOT NULL
);
CREATE INDEX idx_policy_audits_resource ON policy_audits(resource_type,resource_id,id);
CREATE INDEX idx_policy_audits_actor ON policy_audits(actor_user_id,id);
CREATE TRIGGER policy_audits_no_update BEFORE UPDATE ON policy_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND OLD.actor_role=NEW.actor_role AND OLD.resource_type=NEW.resource_type
 AND OLD.resource_id=NEW.resource_id AND OLD.policy=NEW.policy
 AND OLD.old_value=NEW.old_value AND OLD.new_value=NEW.new_value
 AND OLD.created_at=NEW.created_at)
BEGIN SELECT RAISE(ABORT,'policy_audits is append-only'); END;
CREATE TRIGGER policy_audits_no_delete BEFORE DELETE ON policy_audits
BEGIN SELECT RAISE(ABORT,'policy_audits is append-only'); END;

 CREATE TABLE admin_alerts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL CHECK(kind IN ('fetch_failed','forward_error','registration_rejected','maintenance_enabled','donation_failure_disabled','issue_projection_incomplete','report_retry_exhausted','fishing_retry_exhausted','rps_terminal_retrying','worker_checkpoint_failed','invariant_violation')),
 message TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 subject_user_id INTEGER,
 created_at INTEGER NOT NULL,
 resolved INTEGER NOT NULL DEFAULT 0 CHECK(resolved IN (0,1)),
 resolved_at INTEGER
);
CREATE INDEX idx_admin_alerts_created ON admin_alerts(created_at);
CREATE INDEX idx_admin_alerts_subject_user ON admin_alerts(subject_user_id);
CREATE INDEX idx_admin_alerts_unresolved ON admin_alerts(resolved,created_at);

CREATE TABLE user_activity_daily (
 day INTEGER NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 product_active INTEGER NOT NULL DEFAULT 0 CHECK(product_active IN (0,1)),
 api_requests INTEGER NOT NULL DEFAULT 0 CHECK(api_requests BETWEEN 0 AND 9223372036854775807),
 uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(uncached_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens BETWEEN 0 AND 9223372036854775807),
 output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens BETWEEN 0 AND 9223372036854775807),
 checkins INTEGER NOT NULL DEFAULT 0 CHECK(checkins BETWEEN 0 AND 9223372036854775807),
 console_writes INTEGER NOT NULL DEFAULT 0 CHECK(console_writes BETWEEN 0 AND 9223372036854775807),
 game_active INTEGER NOT NULL DEFAULT 0 CHECK(game_active IN (0,1)),
 game_rounds INTEGER NOT NULL DEFAULT 0 CHECK(game_rounds BETWEEN 0 AND 9223372036854775807),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(day,user_id)
);
CREATE INDEX idx_user_activity_user ON user_activity_daily(user_id);

CREATE TABLE site_activity_daily (
 day INTEGER PRIMARY KEY,
 product_active INTEGER NOT NULL DEFAULT 0 CHECK(product_active IN (0,1)),
 api_requests BLOB NOT NULL CHECK(typeof(api_requests)='blob' AND length(api_requests)=16),
 uncached_input_tokens BLOB NOT NULL CHECK(typeof(uncached_input_tokens)='blob' AND length(uncached_input_tokens)=16),
 cache_write_input_tokens BLOB NOT NULL CHECK(typeof(cache_write_input_tokens)='blob' AND length(cache_write_input_tokens)=16),
 cache_read_input_tokens BLOB NOT NULL CHECK(typeof(cache_read_input_tokens)='blob' AND length(cache_read_input_tokens)=16),
 output_tokens BLOB NOT NULL CHECK(typeof(output_tokens)='blob' AND length(output_tokens)=16),
 checkins BLOB NOT NULL CHECK(typeof(checkins)='blob' AND length(checkins)=16),
 console_writes BLOB NOT NULL CHECK(typeof(console_writes)='blob' AND length(console_writes)=16),
 game_active INTEGER NOT NULL DEFAULT 0 CHECK(game_active IN (0,1)),
 game_rounds BLOB NOT NULL CHECK(typeof(game_rounds)='blob' AND length(game_rounds)=16),
 distinct_product_users BLOB NOT NULL CHECK(typeof(distinct_product_users)='blob' AND length(distinct_product_users)=16),
 updated_at INTEGER NOT NULL
);

CREATE TABLE site_usage_totals (
 id INTEGER PRIMARY KEY CHECK(id=1),
 total_requests BLOB NOT NULL CHECK(typeof(total_requests)='blob' AND length(total_requests)=16),
 total_uncached_input_tokens BLOB NOT NULL CHECK(typeof(total_uncached_input_tokens)='blob' AND length(total_uncached_input_tokens)=16),
 total_cache_write_input_tokens BLOB NOT NULL CHECK(typeof(total_cache_write_input_tokens)='blob' AND length(total_cache_write_input_tokens)=16),
 total_cache_read_input_tokens BLOB NOT NULL CHECK(typeof(total_cache_read_input_tokens)='blob' AND length(total_cache_read_input_tokens)=16),
 total_output_tokens BLOB NOT NULL CHECK(typeof(total_output_tokens)='blob' AND length(total_output_tokens)=16),
 total_unknown_usage_requests BLOB NOT NULL CHECK(typeof(total_unknown_usage_requests)='blob' AND length(total_unknown_usage_requests)=16),
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16),
 updated_at INTEGER NOT NULL
);
CREATE TABLE config_revisions (
 domain TEXT NOT NULL PRIMARY KEY CHECK(domain IN ('site','activities','games')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 updated_at INTEGER NOT NULL
);

-- ===== caller keys, endpoint identity, and model catalog ====================
CREATE TABLE caller_keys (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 generation INTEGER NOT NULL CHECK(generation BETWEEN 0 AND 9223372036854775807),
 key_hash BLOB,
 display_head TEXT NOT NULL DEFAULT '',
 display_tail TEXT NOT NULL DEFAULT '',
 key_created_at INTEGER,
 updated_at INTEGER NOT NULL,
 CHECK(key_hash IS NULL OR (typeof(key_hash)='blob' AND length(key_hash)=32)),
 CHECK((key_hash IS NULL AND display_head='' AND display_tail='' AND key_created_at IS NULL)
   OR (key_hash IS NOT NULL AND length(display_head)<=16 AND length(display_tail)<=16 AND key_created_at IS NOT NULL))
);
CREATE UNIQUE INDEX idx_caller_keys_hash ON caller_keys(key_hash) WHERE key_hash IS NOT NULL;

CREATE TABLE checkins (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 site_day TEXT NOT NULL CHECK(site_day GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
 award_milli INTEGER NOT NULL CHECK(award_milli BETWEEN 0 AND 9000000000000000),
 operation_id TEXT NOT NULL UNIQUE,
 created_at INTEGER NOT NULL,
 UNIQUE(user_id,site_day),
 CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w'))
);
CREATE INDEX idx_checkins_user_day ON checkins(user_id,site_day);
CREATE INDEX idx_checkins_operation ON checkins(operation_id);

CREATE TABLE endpoints (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible')),
 base_url TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(id,user_id)
);
CREATE INDEX idx_endpoints_user_cursor ON endpoints(user_id,updated_at,id);
CREATE INDEX idx_endpoints_user_base ON endpoints(user_id,base_url,id);
CREATE TRIGGER endpoints_identity_immutable BEFORE UPDATE OF connector_type,base_url ON endpoints
WHEN OLD.connector_type<>NEW.connector_type OR OLD.base_url<>NEW.base_url
BEGIN SELECT RAISE(ABORT,'endpoint identity is immutable'); END;

CREATE TABLE endpoint_key_secrets (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 context_id BLOB NOT NULL UNIQUE CHECK(typeof(context_id)='blob' AND length(context_id)=16),
 canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096),
 connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible')),
 encrypted_secret TEXT NOT NULL CHECK(typeof(encrypted_secret)='text' AND length(CAST(encrypted_secret AS BLOB)) BETWEEN 1 AND 131072),
 created_at INTEGER NOT NULL,
 orphaned_at INTEGER CHECK(orphaned_at IS NULL OR orphaned_at BETWEEN 0 AND 253402300799)
);
CREATE INDEX idx_endpoint_key_secrets_orphaned ON endpoint_key_secrets(orphaned_at,id) WHERE orphaned_at IS NOT NULL;

CREATE TABLE endpoint_keys (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 endpoint_id INTEGER NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
 secret_ref_id INTEGER NOT NULL UNIQUE REFERENCES endpoint_key_secrets(id) ON DELETE RESTRICT,
 secret_fingerprint BLOB NOT NULL CHECK(typeof(secret_fingerprint)='blob' AND length(secret_fingerprint)=32),
 display_head TEXT NOT NULL DEFAULT '',
 display_tail TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 force_store_false INTEGER NOT NULL DEFAULT 0 CHECK(force_store_false IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(id,endpoint_id),
 CHECK(typeof(display_head)='text' AND typeof(display_tail)='text' AND typeof(note)='text'
   AND length(CAST(display_head AS BLOB)) BETWEEN 0 AND 16
   AND length(CAST(display_tail AS BLOB)) BETWEEN 0 AND 16
   AND length(note) BETWEEN 0 AND 1024
   AND display_head NOT GLOB '*[^ -~]*' AND display_tail NOT GLOB '*[^ -~]*')
);
CREATE INDEX idx_endpoint_keys_owner_cursor ON endpoint_keys(endpoint_id,updated_at,id);
CREATE INDEX idx_endpoint_keys_fingerprint ON endpoint_keys(secret_fingerprint);

CREATE TABLE endpoint_key_suspensions (
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 reason_type TEXT NOT NULL CHECK(reason_type='report_case'),
 report_case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(report_case_id)=26 AND substr(report_case_id,1,4)='rpc_' AND substr(report_case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(report_case_id,-1,1) IN ('A','Q','g','w')),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(endpoint_key_id,reason_type,report_case_id)
);
CREATE INDEX idx_endpoint_key_suspensions_reason ON endpoint_key_suspensions(endpoint_key_id,reason_type);

CREATE TABLE model_discovery_evidence (
 endpoint_key_id INTEGER PRIMARY KEY REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('unknown','checking','succeeded','failed')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 operation_hash BLOB CHECK(operation_hash IS NULL OR (typeof(operation_hash)='blob' AND length(operation_hash)=32)),
 safe_class TEXT NOT NULL DEFAULT 'none' CHECK(safe_class IN ('auth','rate_limit','timeout','protocol','transport','interrupted','none')),
 safe_diag TEXT NOT NULL DEFAULT '' CHECK(typeof(safe_diag)='text' AND length(CAST(safe_diag AS BLOB))<=4096),
 started_at INTEGER,
 completed_at INTEGER,
 fetched_count INTEGER NOT NULL DEFAULT 0 CHECK(fetched_count BETWEEN 0 AND 10000),
 CHECK((state='unknown' AND operation_hash IS NULL AND safe_class='none' AND safe_diag='' AND started_at IS NULL AND completed_at IS NULL AND fetched_count=0)
   OR (state='checking' AND operation_hash IS NOT NULL AND safe_class='none' AND started_at IS NOT NULL AND completed_at IS NULL)
   OR (state='succeeded' AND operation_hash IS NULL AND safe_class='none' AND completed_at IS NOT NULL)
   OR (state='failed' AND operation_hash IS NULL AND safe_class IN ('auth','rate_limit','timeout','protocol','transport','interrupted') AND completed_at IS NOT NULL)),
 CHECK(started_at IS NULL OR started_at BETWEEN 0 AND 253402300799),
 CHECK(completed_at IS NULL OR completed_at BETWEEN 0 AND 253402300799),
 CHECK(started_at IS NULL OR completed_at IS NULL OR completed_at>=started_at)
);
CREATE INDEX idx_model_discovery_due ON model_discovery_evidence(state,started_at,endpoint_key_id);

CREATE TABLE model_catalog_entries (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 source_type TEXT NOT NULL CHECK(source_type IN ('automatic','manual')),
 source_identity TEXT NOT NULL CHECK((source_type='manual' AND source_identity=normalized_model_id) OR (source_type='automatic'
   AND typeof(source_identity)='text' AND instr(source_identity,':')>1
   AND instr(substr(source_identity,instr(source_identity,':')+1),':')=0
   AND substr(source_identity,1,instr(source_identity,':')-1) NOT GLOB '*[^0-9]*'
   AND substr(source_identity,1,1)<>'0'
   AND length(substr(source_identity,instr(source_identity,':')+1)) BETWEEN 1 AND 4
   AND substr(source_identity,instr(source_identity,':')+1) NOT GLOB '*[^0-9]*'
   AND (substr(source_identity,instr(source_identity,':')+1)='0' OR substr(source_identity,instr(source_identity,':')+1,1)<>'0')
   AND length(substr(source_identity,1,instr(source_identity,':')-1)) BETWEEN 1 AND 19
   AND CAST(substr(source_identity,1,instr(source_identity,':')-1) AS INTEGER) BETWEEN 1 AND 9223372036854775807
   AND CAST(CAST(substr(source_identity,1,instr(source_identity,':')-1) AS INTEGER) AS TEXT)=substr(source_identity,1,instr(source_identity,':')-1)
   AND substr(source_identity,1,instr(source_identity,':')-1)=CAST(source_revision AS TEXT)
   AND CAST(substr(source_identity,1,instr(source_identity,':')-1) AS INTEGER)=source_revision
   AND CAST(substr(source_identity,instr(source_identity,':')+1) AS INTEGER) BETWEEN 0 AND 9999
   AND CAST(CAST(substr(source_identity,instr(source_identity,':')+1) AS INTEGER) AS TEXT)=substr(source_identity,instr(source_identity,':')+1))),
 normalized_model_id TEXT NOT NULL CHECK(typeof(normalized_model_id)='text' AND length(normalized_model_id) BETWEEN 1 AND 512),
 provider TEXT NOT NULL DEFAULT '' CHECK(typeof(provider)='text' AND length(provider) BETWEEN 0 AND 128),
 source_revision INTEGER NOT NULL CHECK(source_revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(endpoint_key_id,source_type,source_identity),
 UNIQUE(id,endpoint_key_id)
);
CREATE INDEX idx_model_catalog_pair_model ON model_catalog_entries(endpoint_key_id,normalized_model_id,source_type);

CREATE TABLE model_pair_catalog (
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 normalized_model_id TEXT NOT NULL CHECK(typeof(normalized_model_id)='text' AND length(normalized_model_id) BETWEEN 1 AND 512),
 automatic_supports INTEGER NOT NULL DEFAULT 0 CHECK(automatic_supports BETWEEN 0 AND 9223372036854775807),
 manual_supports INTEGER NOT NULL DEFAULT 0 CHECK(manual_supports BETWEEN 0 AND 9223372036854775807),
 automatic_revision INTEGER NOT NULL DEFAULT 0 CHECK(automatic_revision BETWEEN 0 AND 9223372036854775807),
 pair_revision INTEGER NOT NULL DEFAULT 1 CHECK(pair_revision BETWEEN 1 AND 9223372036854775807),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(endpoint_key_id,normalized_model_id)
);
CREATE INDEX idx_model_pair_catalog_model ON model_pair_catalog(normalized_model_id,endpoint_key_id);

CREATE TABLE models (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 model TEXT NOT NULL,
 full_name TEXT NOT NULL,
 route_strategy TEXT NOT NULL DEFAULT 'ordered' CHECK(route_strategy IN ('ordered','random')),
 silent_retry INTEGER NOT NULL DEFAULT 0 CHECK(silent_retry IN (0,1)),
 flatten_tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(flatten_tool_calls IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 binding_revision INTEGER NOT NULL DEFAULT 0 CHECK(binding_revision BETWEEN 0 AND 9223372036854775807),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(user_id,full_name), UNIQUE(id,user_id), CHECK(full_name=provider||'/'||model)
);
CREATE INDEX idx_models_user ON models(user_id);
CREATE INDEX idx_models_user_fullname ON models(user_id,full_name);

CREATE TABLE model_bindings (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER NOT NULL,
 upstream_model_id TEXT NOT NULL,
 ord INTEGER NOT NULL DEFAULT 0 CHECK(ord BETWEEN 0 AND 255),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(model_id,endpoint_key_id,upstream_model_id), UNIQUE(model_id,ord),
 FOREIGN KEY(endpoint_key_id,upstream_model_id) REFERENCES model_pair_catalog(endpoint_key_id,normalized_model_id) ON DELETE CASCADE
);
CREATE INDEX idx_model_bindings_model ON model_bindings(model_id,ord,id);

CREATE TABLE charity_models (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 provider TEXT NOT NULL, model TEXT NOT NULL, full_name TEXT NOT NULL UNIQUE,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)), pricing_mode TEXT NOT NULL CHECK(pricing_mode IN ('per_request','per_token')),
 request_user_price INTEGER NOT NULL DEFAULT 0 CHECK(request_user_price BETWEEN 0 AND 9000000000000000), request_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(request_donor_reward BETWEEN 0 AND 9000000000000000),
 uncached_user_price INTEGER NOT NULL DEFAULT 0 CHECK(uncached_user_price BETWEEN 0 AND 9000000000000000), cache_write_user_price INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_user_price BETWEEN 0 AND 9000000000000000), cache_read_user_price INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_user_price BETWEEN 0 AND 9000000000000000), output_user_price INTEGER NOT NULL DEFAULT 0 CHECK(output_user_price BETWEEN 0 AND 9000000000000000),
 uncached_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(uncached_donor_reward BETWEEN 0 AND 9000000000000000), cache_write_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_donor_reward BETWEEN 0 AND 9000000000000000), cache_read_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_donor_reward BETWEEN 0 AND 9000000000000000), output_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(output_donor_reward BETWEEN 0 AND 9000000000000000),
 discount_percent INTEGER NOT NULL DEFAULT 100 CHECK(discount_percent BETWEEN 0 AND 100), discount_start_at INTEGER, discount_end_at INTEGER, discount_enabled INTEGER NOT NULL DEFAULT 0 CHECK(discount_enabled IN (0,1)), flatten_tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(flatten_tool_calls IN (0,1)), created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807), binding_revision INTEGER NOT NULL DEFAULT 0 CHECK(binding_revision BETWEEN 0 AND 9223372036854775807), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 CHECK(full_name='[公益]'||provider||'/'||model), CHECK(discount_end_at IS NULL OR discount_start_at IS NULL OR discount_end_at>=discount_start_at)
);

CREATE TABLE donation_keys (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
 display_head TEXT NOT NULL DEFAULT '', display_tail TEXT NOT NULL DEFAULT '', canonical_base_url TEXT NOT NULL DEFAULT '',
 connector_type TEXT NOT NULL DEFAULT 'openai-compatible' CHECK(connector_type IN ('openai-compatible','anthropic-compatible')),
 price_limit_mag BLOB CHECK(price_limit_mag IS NULL OR (typeof(price_limit_mag)='blob' AND length(price_limit_mag)=16)), call_limit_mag BLOB CHECK(call_limit_mag IS NULL OR (typeof(call_limit_mag)='blob' AND length(call_limit_mag)=16)), token_limit_mag BLOB CHECK(token_limit_mag IS NULL OR (typeof(token_limit_mag)='blob' AND length(token_limit_mag)=16)),
 price_used_mag BLOB NOT NULL CHECK(typeof(price_used_mag)='blob' AND length(price_used_mag)=16), price_reserved_mag BLOB NOT NULL CHECK(typeof(price_reserved_mag)='blob' AND length(price_reserved_mag)=16), calls_used BLOB NOT NULL CHECK(typeof(calls_used)='blob' AND length(calls_used)=16), calls_reserved BLOB NOT NULL CHECK(typeof(calls_reserved)='blob' AND length(calls_reserved)=16), tokens_used BLOB NOT NULL CHECK(typeof(tokens_used)='blob' AND length(tokens_used)=16), tokens_reserved BLOB NOT NULL CHECK(typeof(tokens_reserved)='blob' AND length(tokens_reserved)=16),
 token_reserve INTEGER NOT NULL DEFAULT 0 CHECK(token_reserve BETWEEN 0 AND 2147483647), enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)), failure_disabled INTEGER NOT NULL DEFAULT 0 CHECK(failure_disabled IN (0,1)), failure_streak BLOB NOT NULL CHECK(typeof(failure_streak)='blob' AND length(failure_streak)=16), streak_generation BLOB NOT NULL CHECK(typeof(streak_generation)='blob' AND length(streak_generation)=16 AND hex(streak_generation)<>'00000000000000000000000000000000'), next_claim_seq BLOB NOT NULL CHECK(typeof(next_claim_seq)='blob' AND length(next_claim_seq)=16), next_fold_seq BLOB NOT NULL CHECK(typeof(next_fold_seq)='blob' AND length(next_fold_seq)=16 AND hex(next_fold_seq)<=hex(next_claim_seq)), safe_note TEXT NOT NULL DEFAULT '', ended_reason TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, ended_at INTEGER,
 UNIQUE(id,donation_id), CHECK(typeof(display_head)='text' AND typeof(display_tail)='text' AND typeof(canonical_base_url)='text' AND typeof(safe_note)='text'), CHECK(length(CAST(display_head AS BLOB)) BETWEEN 0 AND 16 AND length(CAST(display_tail AS BLOB)) BETWEEN 0 AND 16 AND display_head NOT GLOB '*[^ -~]*' AND display_tail NOT GLOB '*[^ -~]*'), CHECK(length(CAST(canonical_base_url AS BLOB)) BETWEEN 0 AND 4096), CHECK(length(safe_note) BETWEEN 0 AND 256), CHECK(ended_reason IS NULL OR ended_reason IN ('withdrawn','terminated','expired','member_removed','account_deleted'))
);
 CREATE INDEX idx_donation_keys_donation ON donation_keys(donation_id);
 CREATE INDEX idx_donation_keys_route ON donation_keys(enabled,failure_disabled,ended_at,id);
 CREATE UNIQUE INDEX idx_donation_keys_donation_endpoint ON donation_keys(donation_id,endpoint_key_id) WHERE endpoint_key_id IS NOT NULL;

CREATE TABLE charity_model_bindings (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), charity_model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE, donation_key_id INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE, endpoint_key_id INTEGER NOT NULL, upstream_model_id TEXT NOT NULL, ord INTEGER NOT NULL DEFAULT 0 CHECK(ord BETWEEN 0 AND 255), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(charity_model_id,donation_key_id,upstream_model_id), UNIQUE(charity_model_id,ord), FOREIGN KEY(endpoint_key_id,upstream_model_id) REFERENCES model_pair_catalog(endpoint_key_id,normalized_model_id) ON DELETE CASCADE
);
CREATE INDEX idx_charity_bindings_model ON charity_model_bindings(charity_model_id,ord,id);
CREATE TABLE charity_model_stats (model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE, next_slot INTEGER NOT NULL DEFAULT 0 CHECK(next_slot BETWEEN 0 AND 99), sample_count INTEGER NOT NULL DEFAULT 0 CHECK(sample_count BETWEEN 0 AND 100), success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count BETWEEN 0 AND 100)) STRICT;
CREATE TABLE charity_model_outcomes (model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE, slot INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 99), success INTEGER NOT NULL CHECK(success IN (0,1)), created_at INTEGER NOT NULL, PRIMARY KEY(model_id,slot));

-- ===== common request/idempotency and dispatch facts =======================
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL CHECK(scope IN ('credential_report','control_mutation','openai_chat_completions','charity_chat_completions','model_discovery','maintenance','announcement','activity','game_fishing','game_linklink','game_rps','donation')), actor_scope_hash BLOB NOT NULL CHECK(typeof(actor_scope_hash)='blob' AND length(actor_scope_hash)=32), key_hash BLOB NOT NULL CHECK(typeof(key_hash)='blob' AND length(key_hash)=32), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), lookup_fingerprint BLOB CHECK(lookup_fingerprint IS NULL OR (typeof(lookup_fingerprint)='blob' AND length(lookup_fingerprint)=32)), state TEXT NOT NULL CHECK(state IN ('accepted','completed')), http_status INTEGER NOT NULL CHECK(http_status=0 OR http_status BETWEEN 100 AND 599), response_body BLOB NOT NULL CHECK(typeof(response_body)='blob' AND length(response_body)<=65536), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), PRIMARY KEY(scope,actor_scope_hash,key_hash), CHECK(expires_at>=created_at), CHECK((scope='credential_report' AND lookup_fingerprint IS NOT NULL AND expires_at=created_at+86400) OR (scope<>'credential_report' AND lookup_fingerprint IS NULL)), CHECK((state='accepted' AND http_status=0 AND length(response_body)=0) OR (state='completed' AND http_status BETWEEN 100 AND 599))
);
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);

 CREATE TABLE logical_requests (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='req_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 route_kind TEXT NOT NULL CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery')),
 model_snapshot TEXT NOT NULL DEFAULT '' CHECK(typeof(model_snapshot)='text' AND length(CAST(model_snapshot AS BLOB))<=512),
 state TEXT NOT NULL CHECK(state IN ('accepted','running','terminal')),
 attempt_limit INTEGER NOT NULL CHECK(attempt_limit BETWEEN 1 AND 100),
 caller_result_class TEXT CHECK(caller_result_class IS NULL OR caller_result_class IN ('success','failed','cancelled')),
 caller_status INTEGER CHECK(caller_status IS NULL OR caller_status BETWEEN 100 AND 599),
 caller_error_code TEXT CHECK(caller_error_code IS NULL OR (typeof(caller_error_code)='text' AND length(CAST(caller_error_code AS BLOB)) BETWEEN 1 AND 64 AND caller_error_code NOT GLOB '*[^a-z0-9_]*')),
 accounting_state TEXT NOT NULL CHECK(accounting_state IN ('none','reserved','committed','released')),
 account_reserved_milli INTEGER NOT NULL DEFAULT 0 CHECK(account_reserved_milli BETWEEN 0 AND 9000000000000000),
 settlement_destination TEXT NOT NULL CHECK(settlement_destination IN ('user','external')),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799),
 CHECK((state<>'terminal' AND caller_result_class IS NULL AND caller_status IS NULL AND caller_error_code IS NULL AND terminal_at IS NULL) OR
       (state='terminal' AND caller_result_class IS NOT NULL AND terminal_at IS NOT NULL AND terminal_at>=created_at AND
        ((caller_result_class='success' AND caller_status BETWEEN 200 AND 399 AND caller_error_code IS NULL) OR
         (caller_result_class='failed' AND caller_status BETWEEN 400 AND 599 AND caller_error_code IN ('internal','invalid_request','unauthorized','forbidden','not_found','conflict','method_not_allowed','rate_limited','payload_too_large','elevated_required','unbound_model','upstream','maintenance','service_unavailable','resource_limit_exceeded','resource_locked','debug_dry_run_intercepted','debug_live_result_captured','debug_live_cancelled','insufficient_credits','feature_disabled','charity_suspended','content_too_short','already_checked_in','checkin_cap_reached')) OR
         (caller_result_class='cancelled' AND caller_status IS NULL AND caller_error_code IS NULL)))),
 CHECK((route_kind='model_discovery' AND attempt_limit=1) OR route_kind<>'model_discovery'),
 CHECK((accounting_state='reserved' AND account_reserved_milli>=0) OR (accounting_state<>'reserved' AND account_reserved_milli=0)),
 CHECK((settlement_destination='user') OR (settlement_destination='external' AND user_id IS NULL))
);
CREATE INDEX idx_logical_requests_user ON logical_requests(user_id,created_at,id);
CREATE INDEX idx_logical_requests_state ON logical_requests(state,created_at,id);

CREATE TABLE dispatch_claims (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='clm_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 logical_request_id TEXT NOT NULL REFERENCES logical_requests(id) ON DELETE CASCADE CHECK(length(logical_request_id)=26 AND substr(logical_request_id,1,4)='req_' AND substr(logical_request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(logical_request_id,-1,1) IN ('A','Q','g','w')),
 attempt_seq INTEGER NOT NULL CHECK(attempt_seq BETWEEN 1 AND 100),
 purpose TEXT NOT NULL CHECK(purpose IN ('self','charity','debug_live','discovery')),
 endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
 secret_ref_id INTEGER REFERENCES endpoint_key_secrets(id) ON DELETE RESTRICT,
 donation_key_id INTEGER REFERENCES donation_keys(id) ON DELETE SET NULL,
 streak_generation INTEGER CHECK(streak_generation IS NULL OR streak_generation BETWEEN 0 AND 9223372036854775807),
 claim_now INTEGER NOT NULL CHECK(claim_now BETWEEN 0 AND 253402300799),
 state TEXT NOT NULL CHECK(state IN ('claimed','dispatched','committed','released')),
 frozen_price_milli INTEGER NOT NULL DEFAULT 0 CHECK(frozen_price_milli BETWEEN 0 AND 9000000000000000),
 frozen_reward_milli INTEGER NOT NULL DEFAULT 0 CHECK(frozen_reward_milli BETWEEN 0 AND 9000000000000000),
 receiver_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 reserved_price_milli INTEGER NOT NULL DEFAULT 0 CHECK(reserved_price_milli BETWEEN 0 AND 9000000000000000),
 reserved_calls INTEGER NOT NULL DEFAULT 0 CHECK(reserved_calls BETWEEN 0 AND 1),
 reserved_tokens INTEGER NOT NULL DEFAULT 0 CHECK(reserved_tokens BETWEEN 0 AND 2147483647),
 donor_reward_actual_milli INTEGER CHECK(donor_reward_actual_milli IS NULL OR donor_reward_actual_milli BETWEEN 0 AND 9000000000000000),
 donor_reward_state TEXT NOT NULL DEFAULT 'not_applicable' CHECK(donor_reward_state IN ('not_applicable','pending','posted','zero','not_due','receiver_deleted')),
 dispatched_at INTEGER CHECK(dispatched_at IS NULL OR dispatched_at BETWEEN 0 AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799),
 UNIQUE(logical_request_id,attempt_seq),
 CHECK((state IN ('claimed','dispatched') AND secret_ref_id IS NOT NULL) OR (state IN ('committed','released') AND secret_ref_id IS NULL AND terminal_at IS NOT NULL)),
 CHECK((state='claimed' AND dispatched_at IS NULL AND terminal_at IS NULL) OR
       (state='dispatched' AND dispatched_at IS NOT NULL AND terminal_at IS NULL) OR
       (state='committed' AND dispatched_at IS NOT NULL AND terminal_at IS NOT NULL) OR
       (state='released' AND terminal_at IS NOT NULL)),
 CHECK(terminal_at IS NULL OR terminal_at>=claim_now),
 CHECK(terminal_at IS NULL OR dispatched_at IS NULL OR terminal_at>=dispatched_at),
 CHECK((purpose='charity' AND donor_reward_state<>'not_applicable') OR (purpose<>'charity' AND donor_reward_state='not_applicable')),
 CHECK((purpose='discovery' AND frozen_price_milli=0 AND frozen_reward_milli=0 AND reserved_price_milli=0 AND reserved_calls=0 AND reserved_tokens=0) OR purpose<>'discovery'),
 CHECK((purpose<>'charity' AND donor_reward_actual_milli IS NULL AND donor_reward_state='not_applicable') OR
       (purpose='charity' AND state IN ('claimed','dispatched') AND donor_reward_state='pending' AND donor_reward_actual_milli IS NULL) OR
       (purpose='charity' AND state='committed' AND donor_reward_state='posted' AND donor_reward_actual_milli>0 AND receiver_user_id IS NOT NULL) OR
       (purpose='charity' AND state='committed' AND donor_reward_state='zero' AND donor_reward_actual_milli=0) OR
       (purpose='charity' AND state='committed' AND donor_reward_state='receiver_deleted' AND donor_reward_actual_milli>0 AND receiver_user_id IS NULL) OR
       (purpose='charity' AND state='released' AND donor_reward_actual_milli IS NULL AND donor_reward_state='not_due'))
);
CREATE INDEX idx_dispatch_claims_request ON dispatch_claims(logical_request_id,attempt_seq);
CREATE INDEX idx_dispatch_claims_secret ON dispatch_claims(secret_ref_id,state);
CREATE INDEX idx_dispatch_claims_due ON dispatch_claims(state,claim_now,id);

CREATE TABLE request_logs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 logical_request_id TEXT NOT NULL UNIQUE CHECK(length(logical_request_id)=26 AND substr(logical_request_id,1,4)='req_' AND substr(logical_request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(logical_request_id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 model TEXT NOT NULL DEFAULT '' CHECK(typeof(model)='text' AND length(CAST(model AS BLOB))<=512),
 endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
 upstream_model_id TEXT NOT NULL DEFAULT '' CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id)<=512),
 route_kind TEXT NOT NULL DEFAULT 'openai_chat_completions' CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery')),
 endpoint_base_url TEXT NOT NULL DEFAULT '' CHECK(typeof(endpoint_base_url)='text' AND length(CAST(endpoint_base_url AS BLOB))<=4096),
 caller_result_class TEXT CHECK(caller_result_class IS NULL OR caller_result_class IN ('success','failed','cancelled')),
 caller_status INTEGER CHECK(caller_status IS NULL OR caller_status BETWEEN 100 AND 599),
 caller_error_code TEXT CHECK(caller_error_code IS NULL OR (typeof(caller_error_code)='text' AND length(CAST(caller_error_code AS BLOB)) BETWEEN 1 AND 64 AND caller_error_code NOT GLOB '*[^a-z0-9_]*')),
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 100),
 status_code INTEGER NOT NULL DEFAULT 0 CHECK(status_code=0 OR status_code BETWEEN 100 AND 599),
 duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms BETWEEN 0 AND 86400000),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 completed_at INTEGER CHECK(completed_at IS NULL OR (completed_at BETWEEN 0 AND 253402300799 AND completed_at>=started_at)),
 uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(uncached_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens BETWEEN 0 AND 9223372036854775807),
 cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens BETWEEN 0 AND 9223372036854775807),
 output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens BETWEEN 0 AND 9223372036854775807),
 usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)),
 error_source TEXT NOT NULL DEFAULT 'platform' CHECK(error_source IN ('platform','upstream')),
 error_code TEXT NOT NULL DEFAULT '' CHECK(typeof(error_code)='text' AND length(CAST(error_code AS BLOB))<=64 AND error_code NOT GLOB '*[^a-z0-9_]*'),
 error_diag TEXT NOT NULL DEFAULT '' CHECK(typeof(error_diag)='text' AND length(CAST(error_diag AS BLOB))<=4096),
 legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)),
 CHECK((caller_result_class IS NULL AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NULL AND status_code=0 AND error_code='') OR
       (caller_result_class='success' AND caller_status BETWEEN 200 AND 399 AND caller_error_code IS NULL AND completed_at IS NOT NULL) OR
       (caller_result_class='failed' AND caller_status BETWEEN 400 AND 599 AND caller_error_code IN ('internal','invalid_request','unauthorized','forbidden','not_found','conflict','method_not_allowed','rate_limited','payload_too_large','elevated_required','unbound_model','upstream','maintenance','service_unavailable','resource_limit_exceeded','resource_locked','debug_dry_run_intercepted','debug_live_result_captured','debug_live_cancelled','insufficient_credits','feature_disabled','charity_suspended','content_too_short','already_checked_in','checkin_cap_reached') AND completed_at IS NOT NULL) OR
       (caller_result_class='cancelled' AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NOT NULL)),
 CHECK((caller_result_class IS NULL AND completed_at IS NULL) OR (caller_result_class IS NOT NULL AND completed_at IS NOT NULL))
);
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id) WHERE completed_at IS NOT NULL;
CREATE TABLE request_attempts (
 claim_id TEXT NOT NULL PRIMARY KEY CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')), request_log_id INTEGER NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE, attempt_seq INTEGER NOT NULL CHECK(attempt_seq BETWEEN 1 AND 100), endpoint_id_snapshot INTEGER, endpoint_key_id_snapshot INTEGER, connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible')), canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096), upstream_model_id TEXT NOT NULL CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id) BETWEEN 0 AND 512), result_kind TEXT NOT NULL CHECK(result_kind IN ('response','synthetic')), upstream_status INTEGER CHECK(upstream_status IS NULL OR upstream_status BETWEEN 100 AND 599), upstream_code TEXT, diag TEXT, input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_tokens>=0), cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens>=0), cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens>=0), output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens>=0), usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)), started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799), completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253402300799 AND completed_at>=started_at), UNIQUE(request_log_id,attempt_seq), CHECK(upstream_code IS NULL OR (typeof(upstream_code)='text' AND length(CAST(upstream_code AS BLOB)) BETWEEN 1 AND 64 AND upstream_code NOT GLOB '*[^ -~]*')), CHECK(diag IS NULL OR (typeof(diag)='text' AND length(CAST(diag AS BLOB))<=4096))
);
CREATE INDEX idx_request_attempts_log_seq ON request_attempts(request_log_id,attempt_seq);
CREATE INDEX idx_request_attempts_started ON request_attempts(started_at);
CREATE INDEX idx_request_attempts_retention ON request_attempts(completed_at,request_log_id,attempt_seq) WHERE completed_at IS NOT NULL;
 CREATE TABLE worker_checkpoints (worker_key TEXT NOT NULL PRIMARY KEY, cursor_text TEXT NOT NULL DEFAULT '', generation INTEGER NOT NULL CHECK(generation BETWEEN 0 AND 9223372036854775807), attempt_count INTEGER NOT NULL CHECK(attempt_count BETWEEN 0 AND 2147483647), next_attempt_at INTEGER NOT NULL CHECK(next_attempt_at BETWEEN 0 AND 253402300799), last_error_class TEXT NOT NULL DEFAULT '' CHECK(last_error_class IN ('','db_busy','internal_retryable','invariant_violation')), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799));
CREATE INDEX idx_worker_checkpoints_due ON worker_checkpoints(next_attempt_at,worker_key);
CREATE TABLE accepted_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 kind TEXT NOT NULL CHECK(kind IN ('model_discovery','maintenance_enable','report_indexing','report_approved_processing')),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 actor_role TEXT NOT NULL DEFAULT '' CHECK(typeof(actor_role)='text' AND length(CAST(actor_role AS BLOB))<=32 AND actor_role NOT GLOB '*[^ -~]*'),
 payload_hash BLOB NOT NULL CHECK(typeof(payload_hash)='blob' AND length(payload_hash)=32),
 state TEXT NOT NULL CHECK(state IN ('accepted','running','completed','failed_retryable','failed_blocked')),
 checkpoint TEXT NOT NULL DEFAULT '' CHECK(typeof(checkpoint)='text' AND length(CAST(checkpoint AS BLOB))<=4096),
 last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('db_busy','internal_retryable','invariant_violation')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799),
 CHECK((state IN ('accepted','running') AND last_error_class IS NULL AND terminal_at IS NULL) OR
       (state='completed' AND last_error_class IS NULL AND terminal_at IS NOT NULL AND terminal_at>=created_at) OR
       (state='failed_retryable' AND last_error_class IN ('db_busy','internal_retryable') AND terminal_at IS NULL) OR
       (state='failed_blocked' AND last_error_class='invariant_violation' AND terminal_at IS NOT NULL AND terminal_at>=created_at))
);
CREATE INDEX idx_accepted_operations_state ON accepted_operations(state,created_at,id);

-- ===== central wide ledger ==================================================
CREATE TABLE credit_accounts (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), kind TEXT NOT NULL CHECK(kind IN ('user','pool','platform','external')), user_id INTEGER REFERENCES users(id) ON DELETE CASCADE, code TEXT, balance_sign INTEGER NOT NULL CHECK(balance_sign IN (-1,0,1)), balance_mag BLOB NOT NULL CHECK(typeof(balance_mag)='blob' AND length(balance_mag)=16), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, CHECK((balance_sign=0 AND hex(balance_mag)='00000000000000000000000000000000') OR (balance_sign<>0 AND hex(balance_mag)<>'00000000000000000000000000000000')), CHECK((kind='user' AND user_id IS NOT NULL AND code IS NULL) OR (kind<>'user' AND user_id IS NULL AND code IS NOT NULL AND length(code) BETWEEN 1 AND 64)), CHECK(kind IN ('user','external') OR balance_sign IN (0,1)), CHECK((kind='user' AND code IS NULL) OR (kind='external' AND code='external') OR (kind='pool' AND length(code)=31 AND substr(code,1,5)='pool:' AND substr(code,6,4)='pol_' AND substr(code,10) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (kind='platform' AND (code IN ('platform','forward_reserve','charity_reserve','game_fishing_reserve') OR (length(code)=37 AND substr(code,1,10)='rps-queue:' AND substr(code,11,5)='rpsq_' AND substr(code,16) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=38 AND substr(code,1,12)='rps-session:' AND substr(code,13,4)='rps_' AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')))))
);
CREATE UNIQUE INDEX idx_credit_accounts_user ON credit_accounts(user_id) WHERE kind='user';
CREATE UNIQUE INDEX idx_credit_accounts_code ON credit_accounts(code) WHERE code IS NOT NULL;
CREATE TABLE credit_capacity (id INTEGER PRIMARY KEY CHECK(id=1), last_ledger_seq INTEGER NOT NULL CHECK(last_ledger_seq BETWEEN 0 AND 9223372036854775807), reserved_future_rows BLOB NOT NULL CHECK(typeof(reserved_future_rows)='blob' AND length(reserved_future_rows)=16), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16));
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal')), source_type TEXT NOT NULL CHECK(source_type IN ('operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (donation_credit_user_id IS NOT NULL AND kind IN ('admin_user_adjustment','donor_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
CREATE INDEX idx_credit_operations_source ON credit_operations(source_type,source_id,source_seq);
CREATE INDEX idx_credit_operations_created ON credit_operations(created_at,ledger_seq);
CREATE TABLE credit_entries (
 operation_id TEXT NOT NULL REFERENCES credit_operations(id) ON DELETE CASCADE CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), line_no INTEGER NOT NULL CHECK(line_no BETWEEN 0 AND 255), account_id INTEGER REFERENCES credit_accounts(id) ON DELETE SET NULL, account_kind_snapshot TEXT NOT NULL CHECK(account_kind_snapshot IN ('user','pool','platform','external')), delta_sign INTEGER NOT NULL CHECK(delta_sign IN (-1,0,1)), delta_mag BLOB NOT NULL CHECK(typeof(delta_mag)='blob' AND length(delta_mag)=16), balance_after_sign INTEGER, balance_after_mag BLOB CHECK((account_kind_snapshot='user' AND balance_after_sign IS NULL AND balance_after_mag IS NULL) OR (account_kind_snapshot<>'user' AND balance_after_sign IS NOT NULL AND balance_after_mag IS NOT NULL)), PRIMARY KEY(operation_id,line_no), CHECK((delta_sign=0 AND hex(delta_mag)='00000000000000000000000000000000') OR (delta_sign<>0 AND hex(delta_mag)<>'00000000000000000000000000000000')), CHECK((balance_after_sign IS NULL AND balance_after_mag IS NULL) OR (balance_after_sign IN (-1,0,1) AND typeof(balance_after_mag)='blob' AND length(balance_after_mag)=16 AND ((balance_after_sign=0 AND hex(balance_after_mag)='00000000000000000000000000000000') OR (balance_after_sign<>0 AND hex(balance_after_mag)<>'00000000000000000000000000000000')))), CHECK(account_kind_snapshot NOT IN ('pool','platform') OR balance_after_sign IN (0,1))
) WITHOUT ROWID;
CREATE INDEX idx_credit_entries_account ON credit_entries(account_id,operation_id,line_no);
 CREATE TABLE shared_pools (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='pol_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), pool_type TEXT NOT NULL CHECK(pool_type IN ('welfare','thursday')), period_id TEXT REFERENCES thursday_periods(id) ON DELETE RESTRICT CHECK(period_id IS NULL OR (length(period_id)=26 AND substr(period_id,1,4)='thu_' AND substr(period_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(period_id,-1,1) IN ('A','Q','g','w'))), account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, state TEXT NOT NULL CHECK(state IN ('open','closed')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), closed_at INTEGER CHECK(closed_at IS NULL OR closed_at BETWEEN 0 AND 253402300799), UNIQUE(pool_type,period_id), CHECK((pool_type='welfare' AND period_id IS NULL) OR pool_type='thursday'), CHECK((state='open' AND closed_at IS NULL) OR (state='closed' AND closed_at IS NOT NULL AND closed_at>=created_at)));
CREATE UNIQUE INDEX idx_shared_pools_welfare_singleton ON shared_pools(pool_type) WHERE pool_type='welfare';
CREATE UNIQUE INDEX idx_shared_pools_unbound_thursday ON shared_pools(pool_type) WHERE pool_type='thursday' AND period_id IS NULL;

-- ===== announcement and activity facts =====================================
 CREATE TABLE announcements (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ann_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), state TEXT NOT NULL CHECK(state IN ('draft','published','withdrawn','expired')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), draft_title_zh TEXT NOT NULL DEFAULT '', draft_body_zh TEXT NOT NULL DEFAULT '', draft_title_en TEXT NOT NULL DEFAULT '', draft_body_en TEXT NOT NULL DEFAULT '', published_title_zh TEXT, published_body_zh TEXT, published_title_en TEXT, published_body_en TEXT, severity TEXT NOT NULL CHECK(severity IN ('info','warning','important')), pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0,1)), dismissible INTEGER NOT NULL DEFAULT 1 CHECK(dismissible IN (0,1)), expires_at INTEGER CHECK(expires_at IS NULL OR expires_at BETWEEN 0 AND 253402300799), published_revision INTEGER CHECK(published_revision IS NULL OR published_revision BETWEEN 1 AND 9223372036854775807), published_at INTEGER CHECK(published_at IS NULL OR published_at BETWEEN 0 AND 253402300799), withdrawn_at INTEGER CHECK(withdrawn_at IS NULL OR withdrawn_at BETWEEN 0 AND 253402300799), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), CHECK((state='published' AND published_revision BETWEEN 1 AND revision AND published_at IS NOT NULL AND published_title_zh IS NOT NULL AND published_body_zh IS NOT NULL AND published_title_en IS NOT NULL AND published_body_en IS NOT NULL AND ((length(published_title_zh)>0 AND length(published_body_zh)>0) OR (length(published_title_en)>0 AND length(published_body_en)>0))) OR state<>'published'), CHECK(((published_revision IS NULL) AND published_at IS NULL AND published_title_zh IS NULL AND published_body_zh IS NULL AND published_title_en IS NULL AND published_body_en IS NULL) OR ((published_revision IS NOT NULL) AND published_at IS NOT NULL AND published_title_zh IS NOT NULL AND published_body_zh IS NOT NULL AND published_title_en IS NOT NULL AND published_body_en IS NOT NULL)), CHECK(length(draft_title_zh)<=160 AND length(draft_title_en)<=160 AND length(CAST(draft_body_zh AS BLOB))<=65536 AND length(CAST(draft_body_en AS BLOB))<=65536), CHECK(published_title_zh IS NULL OR length(published_title_zh)<=160), CHECK(published_title_en IS NULL OR length(published_title_en)<=160), CHECK(published_body_zh IS NULL OR length(CAST(published_body_zh AS BLOB))<=65536), CHECK(published_body_en IS NULL OR length(CAST(published_body_en AS BLOB))<=65536)
 );
CREATE INDEX idx_announcements_user ON announcements(state,pinned DESC,published_at DESC,id);
CREATE INDEX idx_announcements_expiry ON announcements(expires_at,id);
CREATE INDEX idx_announcements_revision ON announcements(revision,id);
 CREATE TABLE announcement_audits (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), announcement_id_text TEXT NOT NULL CHECK(length(announcement_id_text)=26 AND substr(announcement_id_text,1,4)='ann_' AND substr(announcement_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(announcement_id_text,-1,1) IN ('A','Q','g','w')), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, action TEXT NOT NULL CHECK(action IN ('create','edit','publish','withdraw','expire','delete')), from_revision INTEGER NOT NULL CHECK(from_revision BETWEEN 0 AND 9223372036854775807), to_revision INTEGER NOT NULL CHECK(to_revision BETWEEN 1 AND 9223372036854775807), reason TEXT NOT NULL DEFAULT '' CHECK(typeof(reason)='text' AND length(reason)<=1024 AND length(CAST(reason AS BLOB))<=4096), created_at INTEGER NOT NULL, actor_deidentify_at INTEGER NOT NULL, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), CHECK(actor_deidentify_at=created_at+7776000));
CREATE INDEX idx_announcement_audits_retention ON announcement_audits(created_at,id);
CREATE INDEX idx_announcement_audits_actor ON announcement_audits(actor_user_id,created_at);
 CREATE TABLE welfare_claims (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, site_day TEXT NOT NULL, operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT, threshold_milli INTEGER NOT NULL CHECK(threshold_milli BETWEEN 0 AND 9000000000000000), cap_milli INTEGER NOT NULL CHECK(cap_milli BETWEEN 0 AND 9000000000000000), pool_before_milli INTEGER NOT NULL CHECK(pool_before_milli BETWEEN 0 AND 9000000000000000), award_milli INTEGER NOT NULL CHECK(award_milli BETWEEN 1 AND 9000000000000000), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), UNIQUE(user_id,site_day), CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), CHECK(length(site_day)=10 AND site_day GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' AND date(site_day)=site_day), CHECK(award_milli<=cap_milli AND award_milli<=pool_before_milli));
CREATE INDEX idx_welfare_claims_user_day ON welfare_claims(user_id,site_day);
CREATE INDEX idx_welfare_claims_retention ON welfare_claims(created_at,id);
CREATE TABLE thursday_periods (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='thu_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), period_key TEXT NOT NULL UNIQUE, state TEXT NOT NULL CHECK(state IN ('configured','open','settling','settled','configuration_error')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), opens_at INTEGER NOT NULL, closes_at INTEGER NOT NULL, literature TEXT NOT NULL DEFAULT '', entry_milli INTEGER NOT NULL CHECK(entry_milli BETWEEN 1 AND 9000000000000000), per_user_limit INTEGER NOT NULL CHECK(per_user_limit BETWEEN 1 AND 1000), platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999), welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999), next_pool_bp INTEGER NOT NULL CHECK(next_pool_bp BETWEEN 0 AND 9999), current_pool_id TEXT NOT NULL REFERENCES shared_pools(id) ON DELETE RESTRICT, next_pool_id TEXT NOT NULL REFERENCES shared_pools(id) ON DELETE RESTRICT, settlement_cursor TEXT, ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), frozen_pool_mag BLOB NOT NULL CHECK(typeof(frozen_pool_mag)='blob' AND length(frozen_pool_mag)=16), frozen_contribution_count BLOB NOT NULL CHECK(typeof(frozen_contribution_count)='blob' AND length(frozen_contribution_count)=16), eligible_contribution_count BLOB NOT NULL CHECK(typeof(eligible_contribution_count)='blob' AND length(eligible_contribution_count)=16), platform_cut_mag BLOB NOT NULL CHECK(typeof(platform_cut_mag)='blob' AND length(platform_cut_mag)=16), welfare_cut_mag BLOB NOT NULL CHECK(typeof(welfare_cut_mag)='blob' AND length(welfare_cut_mag)=16), next_cut_mag BLOB NOT NULL CHECK(typeof(next_cut_mag)='blob' AND length(next_cut_mag)=16), payout_total_mag BLOB NOT NULL CHECK(typeof(payout_total_mag)='blob' AND length(payout_total_mag)=16), rollover_mag BLOB NOT NULL CHECK(typeof(rollover_mag)='blob' AND length(rollover_mag)=16), created_at INTEGER NOT NULL, started_settlement_at INTEGER, terminal_at INTEGER, CHECK(closes_at=opens_at+86400 AND next_pool_id<>current_pool_id), CHECK(settlement_cursor IS NULL OR (length(settlement_cursor)=26 AND substr(settlement_cursor,1,4)='thp_' AND substr(settlement_cursor,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(settlement_cursor,-1,1) IN ('A','Q','g','w')))
);
CREATE INDEX idx_thursday_periods_due ON thursday_periods(state,closes_at,id);
CREATE INDEX idx_thursday_periods_cursor ON thursday_periods(settlement_cursor,id);
CREATE TABLE thursday_participants (period_id TEXT NOT NULL REFERENCES thursday_periods(id) ON DELETE CASCADE CHECK(length(period_id)=26 AND substr(period_id,1,4)='thu_' AND substr(period_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(period_id,-1,1) IN ('A','Q','g','w')), participant_ref TEXT NOT NULL CHECK(length(participant_ref)=26 AND substr(participant_ref,1,4)='thp_' AND substr(participant_ref,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(participant_ref,-1,1) IN ('A','Q','g','w')), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, contribution_count BLOB NOT NULL CHECK(typeof(contribution_count)='blob' AND length(contribution_count)=16), contributed_mag BLOB NOT NULL CHECK(typeof(contributed_mag)='blob' AND length(contributed_mag)=16), eligible_at_freeze INTEGER NOT NULL CHECK(eligible_at_freeze IN (0,1)), payout_mag BLOB NOT NULL CHECK(typeof(payout_mag)='blob' AND length(payout_mag)=16), unpaid_reason TEXT CHECK(unpaid_reason IS NULL OR unpaid_reason IN ('account_banned','account_deleted')), settled INTEGER NOT NULL DEFAULT 0 CHECK(settled IN (0,1)), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(period_id,participant_ref));
CREATE UNIQUE INDEX idx_thursday_active_participant ON thursday_participants(period_id,user_id) WHERE user_id IS NOT NULL AND settled=0;
CREATE INDEX idx_thursday_participants_user ON thursday_participants(user_id,period_id);

CREATE TRIGGER welfare_claim_matrix_guard BEFORE INSERT ON welfare_claims
WHEN typeof(NEW.site_day)<>'text'
 OR length(NEW.site_day)<>10
 OR NEW.site_day NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.site_day)<>NEW.site_day
 OR NEW.threshold_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.cap_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.pool_before_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.award_milli NOT BETWEEN 1 AND 9000000000000000
 OR NEW.award_milli>NEW.cap_milli
 OR NEW.award_milli>NEW.pool_before_milli
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NOT EXISTS(SELECT 1 FROM credit_operations o
               WHERE o.id=NEW.operation_id AND o.kind='welfare_claim'
                 AND o.source_type='operation' AND o.source_id=NEW.operation_id
                 AND hex(o.source_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'welfare claim matrix is invalid'); END;
CREATE TRIGGER welfare_claim_matrix_update_guard BEFORE UPDATE ON welfare_claims
WHEN typeof(NEW.site_day)<>'text'
 OR length(NEW.site_day)<>10
 OR NEW.site_day NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.site_day)<>NEW.site_day
 OR NEW.threshold_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.cap_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.pool_before_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.award_milli NOT BETWEEN 1 AND 9000000000000000
 OR NEW.award_milli>NEW.cap_milli
 OR NEW.award_milli>NEW.pool_before_milli
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NOT EXISTS(SELECT 1 FROM credit_operations o
               WHERE o.id=NEW.operation_id AND o.kind='welfare_claim'
                 AND o.source_type='operation' AND o.source_id=NEW.operation_id
                 AND hex(o.source_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'welfare claim matrix is invalid'); END;

CREATE TRIGGER thursday_period_matrix_guard BEFORE INSERT ON thursday_periods
WHEN typeof(NEW.period_key)<>'text'
 OR length(NEW.period_key)<>10
 OR NEW.period_key NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.period_key)<>NEW.period_key
 OR strftime('%w',NEW.period_key)<>'4'
 OR typeof(NEW.literature)<>'text'
 OR length(NEW.literature)>1024
 OR length(CAST(NEW.literature AS BLOB))>4096
 OR NEW.revision<1
 OR NEW.opens_at NOT BETWEEN 0 AND 253402300799
 OR NEW.closes_at NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.started_settlement_at IS NOT NULL AND NEW.started_settlement_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.terminal_at IS NOT NULL AND NEW.terminal_at NOT BETWEEN 0 AND 253402300799)
 OR NEW.closes_at<>NEW.opens_at+86400
 OR NEW.platform_bp+NEW.welfare_bp+NEW.next_pool_bp>=10000
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.frozen_pool_mag)<>'blob' OR length(NEW.frozen_pool_mag)<>16 OR substr(hex(NEW.frozen_pool_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.frozen_contribution_count)<>'blob' OR length(NEW.frozen_contribution_count)<>16 OR substr(hex(NEW.frozen_contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.eligible_contribution_count)<>'blob' OR length(NEW.eligible_contribution_count)<>16 OR substr(hex(NEW.eligible_contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_cut_mag)<>'blob' OR length(NEW.platform_cut_mag)<>16 OR substr(hex(NEW.platform_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_cut_mag)<>'blob' OR length(NEW.welfare_cut_mag)<>16 OR substr(hex(NEW.welfare_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_cut_mag)<>'blob' OR length(NEW.next_cut_mag)<>16 OR substr(hex(NEW.next_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.payout_total_mag)<>'blob' OR length(NEW.payout_total_mag)<>16 OR substr(hex(NEW.payout_total_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rollover_mag)<>'blob' OR length(NEW.rollover_mag)<>16 OR substr(hex(NEW.rollover_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.state IN ('configured','open') AND (NEW.started_settlement_at IS NOT NULL OR NEW.terminal_at IS NOT NULL))
 OR (NEW.state='settling' AND (NEW.started_settlement_at IS NULL OR NEW.terminal_at IS NOT NULL))
 OR (NEW.state='settled' AND (NEW.started_settlement_at IS NULL OR NEW.terminal_at IS NULL OR NEW.terminal_at<NEW.started_settlement_at))
 OR (NEW.state IN ('configured','open','settling') AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001')
 OR (NEW.state='settled' AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
 OR (NEW.settlement_cursor IS NOT NULL AND (typeof(NEW.settlement_cursor)<>'text' OR length(NEW.settlement_cursor)<>26 OR substr(NEW.settlement_cursor,1,4)<>'thp_' OR substr(NEW.settlement_cursor,5) GLOB '*[^A-Za-z0-9_-]*' OR substr(NEW.settlement_cursor,-1,1) NOT IN ('A','Q','g','w')))
 OR NOT EXISTS(SELECT 1 FROM shared_pools p WHERE p.id=NEW.current_pool_id AND p.pool_type='thursday' AND (p.period_id IS NULL OR p.period_id=NEW.id))
 OR NOT EXISTS(SELECT 1 FROM shared_pools p WHERE p.id=NEW.next_pool_id AND p.pool_type='thursday' AND (p.period_id IS NULL OR p.period_id=NEW.id))
BEGIN SELECT RAISE(ABORT,'thursday period matrix is invalid'); END;
CREATE TRIGGER thursday_period_matrix_update_guard BEFORE UPDATE ON thursday_periods
WHEN typeof(NEW.period_key)<>'text'
 OR length(NEW.period_key)<>10
 OR NEW.period_key NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
 OR date(NEW.period_key)<>NEW.period_key
 OR strftime('%w',NEW.period_key)<>'4'
 OR typeof(NEW.literature)<>'text'
 OR length(NEW.literature)>1024
 OR length(CAST(NEW.literature AS BLOB))>4096
 OR NEW.revision<1
 OR NEW.opens_at NOT BETWEEN 0 AND 253402300799
 OR NEW.closes_at NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.started_settlement_at IS NOT NULL AND NEW.started_settlement_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.terminal_at IS NOT NULL AND NEW.terminal_at NOT BETWEEN 0 AND 253402300799)
 OR NEW.closes_at<>NEW.opens_at+86400
 OR NEW.platform_bp+NEW.welfare_bp+NEW.next_pool_bp>=10000
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.frozen_pool_mag)<>'blob' OR length(NEW.frozen_pool_mag)<>16 OR substr(hex(NEW.frozen_pool_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.frozen_contribution_count)<>'blob' OR length(NEW.frozen_contribution_count)<>16 OR substr(hex(NEW.frozen_contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.eligible_contribution_count)<>'blob' OR length(NEW.eligible_contribution_count)<>16 OR substr(hex(NEW.eligible_contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_cut_mag)<>'blob' OR length(NEW.platform_cut_mag)<>16 OR substr(hex(NEW.platform_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_cut_mag)<>'blob' OR length(NEW.welfare_cut_mag)<>16 OR substr(hex(NEW.welfare_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_cut_mag)<>'blob' OR length(NEW.next_cut_mag)<>16 OR substr(hex(NEW.next_cut_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.payout_total_mag)<>'blob' OR length(NEW.payout_total_mag)<>16 OR substr(hex(NEW.payout_total_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rollover_mag)<>'blob' OR length(NEW.rollover_mag)<>16 OR substr(hex(NEW.rollover_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.state IN ('configured','open') AND (NEW.started_settlement_at IS NOT NULL OR NEW.terminal_at IS NOT NULL))
 OR (NEW.state='settling' AND (NEW.started_settlement_at IS NULL OR NEW.terminal_at IS NOT NULL))
 OR (NEW.state='settled' AND (NEW.started_settlement_at IS NULL OR NEW.terminal_at IS NULL OR NEW.terminal_at<NEW.started_settlement_at))
 OR (NEW.state IN ('configured','open','settling') AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001')
 OR (NEW.state='settled' AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
 OR (NEW.settlement_cursor IS NOT NULL AND (typeof(NEW.settlement_cursor)<>'text' OR length(NEW.settlement_cursor)<>26 OR substr(NEW.settlement_cursor,1,4)<>'thp_' OR substr(NEW.settlement_cursor,5) GLOB '*[^A-Za-z0-9_-]*' OR substr(NEW.settlement_cursor,-1,1) NOT IN ('A','Q','g','w')))
 OR NOT EXISTS(SELECT 1 FROM shared_pools p WHERE p.id=NEW.current_pool_id AND p.pool_type='thursday' AND (p.period_id IS NULL OR p.period_id=NEW.id))
 OR NOT EXISTS(SELECT 1 FROM shared_pools p WHERE p.id=NEW.next_pool_id AND p.pool_type='thursday' AND (p.period_id IS NULL OR p.period_id=NEW.id))
BEGIN SELECT RAISE(ABORT,'thursday period matrix is invalid'); END;
CREATE TRIGGER thursday_participant_matrix_guard BEFORE INSERT ON thursday_participants
WHEN typeof(NEW.contribution_count)<>'blob' OR length(NEW.contribution_count)<>16 OR substr(hex(NEW.contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.contributed_mag)<>'blob' OR length(NEW.contributed_mag)<>16 OR substr(hex(NEW.contributed_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.payout_mag)<>'blob' OR length(NEW.payout_mag)<>16 OR substr(hex(NEW.payout_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799 OR NEW.updated_at<NEW.created_at
 OR (NEW.settled=0 AND (NEW.user_id IS NULL OR hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001' OR hex(NEW.payout_mag)<>'00000000000000000000000000000000' OR NEW.unpaid_reason IS NOT NULL))
 OR (NEW.settled=1 AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
 OR (NEW.settled=1 AND NEW.user_id IS NULL AND CASE
      WHEN NEW.unpaid_reason='account_deleted' AND hex(NEW.payout_mag)='00000000000000000000000000000000' THEN 0
      WHEN NEW.eligible_at_freeze=0 AND NEW.unpaid_reason='account_banned' AND hex(NEW.payout_mag)='00000000000000000000000000000000' THEN 0
      WHEN NEW.eligible_at_freeze=1 AND NEW.unpaid_reason IS NULL THEN 0
      ELSE 1
    END=1)
 OR (NEW.settled=1 AND NEW.user_id IS NOT NULL AND NEW.eligible_at_freeze=0 AND (NEW.unpaid_reason IS NULL OR NEW.unpaid_reason<>'account_banned' OR hex(NEW.payout_mag)<>'00000000000000000000000000000000'))
 OR (NEW.settled=1 AND NEW.user_id IS NOT NULL AND NEW.eligible_at_freeze=1 AND NEW.unpaid_reason IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'thursday participant matrix is invalid'); END;
CREATE TRIGGER thursday_participant_matrix_update_guard BEFORE UPDATE ON thursday_participants
WHEN typeof(NEW.contribution_count)<>'blob' OR length(NEW.contribution_count)<>16 OR substr(hex(NEW.contribution_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.contributed_mag)<>'blob' OR length(NEW.contributed_mag)<>16 OR substr(hex(NEW.contributed_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.payout_mag)<>'blob' OR length(NEW.payout_mag)<>16 OR substr(hex(NEW.payout_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799 OR NEW.updated_at<NEW.created_at
 OR (NEW.settled=0 AND (NEW.user_id IS NULL OR hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001' OR hex(NEW.payout_mag)<>'00000000000000000000000000000000' OR NEW.unpaid_reason IS NOT NULL))
 OR (NEW.settled=1 AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
 OR (NEW.settled=1 AND NEW.user_id IS NULL AND CASE
      WHEN NEW.unpaid_reason='account_deleted' AND hex(NEW.payout_mag)='00000000000000000000000000000000' THEN 0
      WHEN NEW.eligible_at_freeze=0 AND NEW.unpaid_reason='account_banned' AND hex(NEW.payout_mag)='00000000000000000000000000000000' THEN 0
      WHEN NEW.eligible_at_freeze=1 AND NEW.unpaid_reason IS NULL THEN 0
      ELSE 1
    END=1)
 OR (NEW.settled=1 AND NEW.user_id IS NOT NULL AND NEW.eligible_at_freeze=0 AND (NEW.unpaid_reason IS NULL OR NEW.unpaid_reason<>'account_banned' OR hex(NEW.payout_mag)<>'00000000000000000000000000000000'))
 OR (NEW.settled=1 AND NEW.user_id IS NOT NULL AND NEW.eligible_at_freeze=1 AND NEW.unpaid_reason IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'thursday participant matrix is invalid'); END;

-- ===== donations, charity reservations, and report/security facts ==========
 CREATE TABLE donations (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','deleted','expired')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), description TEXT NOT NULL DEFAULT '', review_note TEXT NOT NULL DEFAULT '', reviewed_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reviewed_by_role TEXT NOT NULL DEFAULT '' CHECK(reviewed_by_role IN ('','admin','level5')), expires_at INTEGER, reviewed_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, terminal_at INTEGER, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)));
CREATE INDEX idx_donations_user ON donations(user_id,created_at,id);
CREATE INDEX idx_donations_status ON donations(status,expires_at,id);
CREATE TABLE donation_key_memberships (endpoint_key_id INTEGER PRIMARY KEY REFERENCES endpoint_keys(id) ON DELETE RESTRICT, donation_key_id INTEGER NOT NULL UNIQUE REFERENCES donation_keys(id) ON DELETE CASCADE, donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE, created_at INTEGER NOT NULL);
CREATE INDEX idx_donation_memberships_donation ON donation_key_memberships(donation_id,endpoint_key_id);
 CREATE TABLE donation_reviews (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE, submission_revision INTEGER NOT NULL DEFAULT 1 CHECK(submission_revision BETWEEN 1 AND 9223372036854775807), reviewer_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reviewer_role TEXT NOT NULL DEFAULT '' CHECK(reviewer_role IN ('','admin','level5')), action TEXT NOT NULL CHECK(action IN ('approve','reject','withdraw','terminate','expire','enable','disable','limit_update','note_update','member_removed')), note TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE INDEX idx_donation_reviews_donation ON donation_reviews(donation_id,id);
 CREATE TABLE charity_reservations (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), logical_request_id TEXT NOT NULL UNIQUE REFERENCES logical_requests(id) ON DELETE CASCADE CHECK(length(logical_request_id)=26 AND substr(logical_request_id,1,4)='req_' AND substr(logical_request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(logical_request_id,-1,1) IN ('A','Q','g','w')), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, charity_model_id INTEGER REFERENCES charity_models(id) ON DELETE SET NULL, model_snapshot TEXT NOT NULL DEFAULT '', state TEXT NOT NULL CHECK(state IN ('reserved','dispatched','committed','released')), pricing_mode TEXT NOT NULL CHECK(pricing_mode IN ('per_request','per_token')), discount_percent INTEGER NOT NULL CHECK(discount_percent BETWEEN 0 AND 100), request_user_price_milli INTEGER NOT NULL CHECK(request_user_price_milli BETWEEN 0 AND 9000000000000000), request_donor_reward_milli INTEGER NOT NULL CHECK(request_donor_reward_milli BETWEEN 0 AND 9000000000000000), uncached_user_price_milli INTEGER NOT NULL CHECK(uncached_user_price_milli BETWEEN 0 AND 9000000000000000), cache_write_user_price_milli INTEGER NOT NULL CHECK(cache_write_user_price_milli BETWEEN 0 AND 9000000000000000), cache_read_user_price_milli INTEGER NOT NULL CHECK(cache_read_user_price_milli BETWEEN 0 AND 9000000000000000), output_user_price_milli INTEGER NOT NULL CHECK(output_user_price_milli BETWEEN 0 AND 9000000000000000), uncached_donor_reward_milli INTEGER NOT NULL CHECK(uncached_donor_reward_milli BETWEEN 0 AND 9000000000000000), cache_write_donor_reward_milli INTEGER NOT NULL CHECK(cache_write_donor_reward_milli BETWEEN 0 AND 9000000000000000), cache_read_donor_reward_milli INTEGER NOT NULL CHECK(cache_read_donor_reward_milli BETWEEN 0 AND 9000000000000000), output_donor_reward_milli INTEGER NOT NULL CHECK(output_donor_reward_milli BETWEEN 0 AND 9000000000000000), token_reserve_milli INTEGER NOT NULL CHECK(token_reserve_milli>=0), user_reserved_milli INTEGER NOT NULL CHECK(user_reserved_milli>=0), original_charge_milli INTEGER NOT NULL CHECK(original_charge_milli>=0), user_charge_milli INTEGER NOT NULL CHECK(user_charge_milli>=0), donor_reward_total_mag BLOB NOT NULL CHECK(typeof(donor_reward_total_mag)='blob' AND length(donor_reward_total_mag)=16), usage_uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(usage_uncached_input_tokens>=0), cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens>=0), cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens>=0), usage_output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(usage_output_tokens>=0), usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)), created_at INTEGER NOT NULL, dispatched_at INTEGER, finalized_at INTEGER, updated_at INTEGER NOT NULL);
CREATE INDEX idx_charity_reservations_state ON charity_reservations(state,created_at,id);
CREATE INDEX idx_charity_reservations_user ON charity_reservations(user_id,created_at,id);
CREATE TABLE donation_usage_reservations (claim_id TEXT NOT NULL PRIMARY KEY CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')), donation_key_id INTEGER REFERENCES donation_keys(id) ON DELETE SET NULL, streak_generation BLOB NOT NULL CHECK(typeof(streak_generation)='blob' AND length(streak_generation)=16), claim_seq BLOB NOT NULL CHECK(typeof(claim_seq)='blob' AND length(claim_seq)=16), price_reserved_milli INTEGER NOT NULL CHECK(price_reserved_milli BETWEEN 0 AND 9000000000000000), price_actual_milli INTEGER CHECK(price_actual_milli IS NULL OR price_actual_milli BETWEEN 0 AND 9000000000000000), reward_actual_milli INTEGER CHECK(reward_actual_milli IS NULL OR reward_actual_milli BETWEEN 0 AND 9000000000000000), calls_reserved INTEGER NOT NULL CHECK(calls_reserved IN (0,1)), calls_actual INTEGER CHECK(calls_actual IS NULL OR calls_actual IN (0,1)), tokens_reserved INTEGER NOT NULL CHECK(tokens_reserved BETWEEN 0 AND 2147483647), tokens_actual INTEGER CHECK(tokens_actual IS NULL OR tokens_actual BETWEEN 0 AND 2147483647), protocol_success INTEGER CHECK(protocol_success IS NULL OR protocol_success IN (0,1)), usage_unknown INTEGER CHECK(usage_unknown IS NULL OR usage_unknown IN (0,1)), state TEXT NOT NULL CHECK(state IN ('reserved','committed','released')), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), finalized_at INTEGER CHECK(finalized_at IS NULL OR finalized_at BETWEEN 0 AND 253402300799), UNIQUE(donation_key_id,streak_generation,claim_seq), CHECK((state='reserved' AND price_actual_milli IS NULL AND reward_actual_milli IS NULL AND calls_actual IS NULL AND tokens_actual IS NULL AND protocol_success IS NULL AND usage_unknown IS NULL AND finalized_at IS NULL) OR (state='committed' AND price_actual_milli IS NOT NULL AND reward_actual_milli IS NOT NULL AND calls_actual IS NOT NULL AND tokens_actual IS NOT NULL AND protocol_success IS NOT NULL AND usage_unknown IS NOT NULL AND finalized_at IS NOT NULL) OR (state='released' AND finalized_at IS NOT NULL AND ((price_actual_milli IS NULL AND reward_actual_milli IS NULL AND calls_actual IS NULL AND tokens_actual IS NULL AND protocol_success IS NULL AND usage_unknown IS NULL) OR (price_actual_milli IS NOT NULL AND reward_actual_milli IS NOT NULL AND calls_actual IS NOT NULL AND tokens_actual IS NOT NULL AND protocol_success IS NOT NULL AND usage_unknown IS NOT NULL)))));
CREATE INDEX idx_donation_usage_key_state ON donation_usage_reservations(donation_key_id,state,claim_seq);

CREATE TABLE report_cases (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rpc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), fingerprint BLOB NOT NULL CHECK(typeof(fingerprint)='blob' AND length(fingerprint)=32), connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible')), canonical_base_url TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending_indexing','pending_review','approved_processing','approved','rejected','expired')), progress_state TEXT NOT NULL CHECK(progress_state IN ('in_progress','complete')), material_version INTEGER NOT NULL CHECK(material_version BETWEEN 1 AND 9223372036854775807), target_version INTEGER NOT NULL CHECK(target_version BETWEEN 1 AND 9223372036854775807), deadline INTEGER NOT NULL, cursor_text TEXT, material_count INTEGER NOT NULL CHECK(material_count>=0), target_count INTEGER NOT NULL CHECK(target_count>=0), distinct_owner_count INTEGER NOT NULL CHECK(distinct_owner_count>=0), processed_target_count INTEGER NOT NULL DEFAULT 0 CHECK(processed_target_count>=0), deleted_target_count INTEGER NOT NULL DEFAULT 0 CHECK(deleted_target_count>=0), released_target_count INTEGER NOT NULL DEFAULT 0 CHECK(released_target_count>=0), decision_reason TEXT, decision_actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, decision_at INTEGER, retry_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_attempt_count>=0), next_retry_at INTEGER, last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('db_busy','internal_retryable','invariant_violation')), created_at INTEGER NOT NULL, terminal_at INTEGER, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), CHECK(processed_target_count<=target_count AND deleted_target_count<=target_count AND released_target_count<=target_count), CHECK((progress_state='in_progress' AND status IN ('pending_indexing','approved_processing')) OR (progress_state='complete' AND status IN ('pending_review','approved','rejected')) OR status='expired'));
CREATE UNIQUE INDEX idx_report_cases_active_fingerprint ON report_cases(fingerprint) WHERE status IN ('pending_indexing','pending_review','approved_processing');
CREATE INDEX idx_report_cases_status_deadline ON report_cases(status,deadline,id);
CREATE INDEX idx_report_cases_retry ON report_cases(next_retry_at,id);
 CREATE TABLE report_materials (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(case_id)=26 AND substr(case_id,1,4)='rpc_' AND substr(case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(case_id,-1,1) IN ('A','Q','g','w')), material_hash BLOB NOT NULL CHECK(typeof(material_hash)='blob' AND length(material_hash)=32), note_text TEXT NOT NULL DEFAULT '', reporter_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reporter_discord_id TEXT CHECK(reporter_discord_id IS NULL OR (length(CAST(reporter_discord_id AS BLOB)) BETWEEN 1 AND 64 AND reporter_discord_id NOT GLOB '*[^ -~]*')), source_ip_envelope BLOB NOT NULL CHECK(typeof(source_ip_envelope)='blob' AND length(source_ip_envelope)=45), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), UNIQUE(case_id,material_hash));
CREATE INDEX idx_report_materials_case ON report_materials(case_id,id);
CREATE TABLE report_targets (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rpt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(case_id)=26 AND substr(case_id,1,4)='rpc_' AND substr(case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(case_id,-1,1) IN ('A','Q','g','w')), target_seq INTEGER NOT NULL CHECK(target_seq BETWEEN 0 AND 9223372036854775807), endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL, key_ref BLOB NOT NULL CHECK(typeof(key_ref)='blob' AND length(key_ref)=32), owner_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, owner_discord_id TEXT CHECK(owner_discord_id IS NULL OR (length(CAST(owner_discord_id AS BLOB)) BETWEEN 1 AND 64 AND owner_discord_id NOT GLOB '*[^ -~]*')), owner_display_name TEXT CHECK(owner_display_name IS NULL OR (typeof(owner_display_name)='text' AND length(CAST(owner_display_name AS BLOB))<=512)), connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible')), canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096), key_display_head TEXT NOT NULL DEFAULT '' CHECK(length(CAST(key_display_head AS BLOB))<=16), key_display_tail TEXT NOT NULL DEFAULT '' CHECK(length(CAST(key_display_tail AS BLOB))<=16), state TEXT NOT NULL CHECK(state IN ('protected','deleted_by_owner','deleted_by_account','deleted_by_approval','released')), discovered_version INTEGER NOT NULL CHECK(discovered_version BETWEEN 1 AND 9223372036854775807), decided_version INTEGER CHECK(decided_version IS NULL OR decided_version BETWEEN 1 AND 9223372036854775807), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), UNIQUE(case_id,key_ref), UNIQUE(case_id,target_seq));
CREATE INDEX idx_report_targets_case_state ON report_targets(case_id,state,target_seq,id);
CREATE INDEX idx_report_targets_owner ON report_targets(owner_user_id,id);
 CREATE TABLE report_decisions (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(case_id)=26 AND substr(case_id,1,4)='rpc_' AND substr(case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(case_id,-1,1) IN ('A','Q','g','w')), material_version INTEGER NOT NULL CHECK(material_version BETWEEN 1 AND 9223372036854775807), target_version INTEGER NOT NULL CHECK(target_version BETWEEN 1 AND 9223372036854775807), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, action TEXT NOT NULL CHECK(action IN ('approve','reject','expire','resume_processing')), reason TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE INDEX idx_report_decisions_case ON report_decisions(case_id,id);
CREATE TABLE report_rate_buckets (scope TEXT NOT NULL CHECK(scope IN ('ip','account','fingerprint','global')), scope_hash BLOB NOT NULL CHECK(typeof(scope_hash)='blob' AND length(scope_hash)=32), window_start INTEGER NOT NULL CHECK(window_start BETWEEN 0 AND 253402300799), count INTEGER NOT NULL CHECK(count BETWEEN 0 AND 4096), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), PRIMARY KEY(scope,scope_hash,window_start), CHECK((scope IN ('ip','account','fingerprint') AND expires_at=window_start+1200) OR (scope='global' AND expires_at=window_start+120)));
CREATE INDEX idx_report_rate_buckets_expiry ON report_rate_buckets(expires_at);
CREATE TABLE user_issues (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='iss_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, source TEXT NOT NULL CHECK(source IN ('model_discovery','routing_projection','resource_validator')), resource_kind TEXT NOT NULL CHECK(resource_kind IN ('endpoint','endpoint_key','model')), resource_ref TEXT NOT NULL CHECK(resource_ref GLOB '[1-9]*' AND resource_ref NOT GLOB '*[^0-9]*'), root_cause TEXT NOT NULL CHECK(root_cause IN ('discovery_failed','no_routable_binding','credential_invalid','configuration_invalid')), generation INTEGER NOT NULL CHECK(generation>=0), state TEXT NOT NULL CHECK(state IN ('current','closed')), summary_code TEXT NOT NULL CHECK(length(CAST(summary_code AS BLOB)) BETWEEN 1 AND 128), safe_detail TEXT NOT NULL DEFAULT '', deep_link_kind TEXT CHECK(deep_link_kind IS NULL OR deep_link_kind IN ('endpoint','endpoint_key','model')), deep_link_ref TEXT CHECK(deep_link_ref IS NULL OR (deep_link_ref GLOB '[1-9]*' AND deep_link_ref NOT GLOB '*[^0-9]*')), first_seen_at INTEGER NOT NULL CHECK(first_seen_at BETWEEN 0 AND 253402300799), last_seen_at INTEGER NOT NULL CHECK(last_seen_at BETWEEN 0 AND 253402300799), count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 9223372036854775807), closed_at INTEGER CHECK(closed_at IS NULL OR closed_at BETWEEN 0 AND 253402300799), retain_until INTEGER CHECK(retain_until IS NULL OR retain_until BETWEEN 0 AND 253402300799), CHECK((source='model_discovery' AND resource_kind='endpoint_key' AND root_cause='discovery_failed') OR (source='routing_projection' AND resource_kind='model' AND root_cause='no_routable_binding') OR (source='resource_validator' AND resource_kind IN ('endpoint','endpoint_key') AND root_cause IN ('credential_invalid','configuration_invalid'))), CHECK((deep_link_kind IS NULL AND deep_link_ref IS NULL) OR (deep_link_kind IS NOT NULL AND deep_link_ref IS NOT NULL)), CHECK((state='current' AND closed_at IS NULL AND retain_until IS NULL) OR (state='closed' AND closed_at IS NOT NULL AND retain_until IS NOT NULL)));
CREATE UNIQUE INDEX idx_user_issues_current ON user_issues(user_id,source,resource_kind,resource_ref,root_cause) WHERE state='current';
CREATE INDEX idx_user_issues_user_state ON user_issues(user_id,state,last_seen_at,id);
CREATE INDEX idx_user_issues_retention ON user_issues(retain_until,id);
CREATE TABLE user_issue_projection_state (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, projection_incomplete INTEGER NOT NULL DEFAULT 0 CHECK(projection_incomplete IN (0,1)), rebuild_generation INTEGER NOT NULL DEFAULT 0 CHECK(rebuild_generation>=0), rebuild_cursor TEXT, updated_at INTEGER NOT NULL, CHECK(projection_incomplete=1 OR rebuild_cursor IS NULL));
CREATE TABLE maintenance_state (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), revision INTEGER NOT NULL CHECK(revision>=1), changed_at INTEGER NOT NULL CHECK(changed_at BETWEEN 0 AND 253402300799), current_event_id TEXT CHECK(current_event_id IS NULL OR (length(current_event_id)=25 AND substr(current_event_id,1,3)='op_' AND substr(current_event_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(current_event_id,-1,1) IN ('A','Q','g','w'))));
CREATE TABLE maintenance_events (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, actor_discord_id TEXT CHECK(actor_discord_id IS NULL OR (length(CAST(actor_discord_id AS BLOB)) BETWEEN 1 AND 64 AND actor_discord_id NOT GLOB '*[^ -~]*')), actor_role TEXT CHECK(actor_role IS NULL OR actor_role IN ('admin','steward')), action TEXT NOT NULL CHECK(action IN ('enable','disable')), reason TEXT NOT NULL DEFAULT '' CHECK(length(CAST(reason AS BLOB))<=4096), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), resolved_at INTEGER CHECK(resolved_at IS NULL OR resolved_at BETWEEN 0 AND 253402300799), deidentify_at INTEGER CHECK(deidentify_at IS NULL OR deidentify_at BETWEEN 0 AND 253402300799), retain_until INTEGER CHECK(retain_until IS NULL OR retain_until BETWEEN 0 AND 253402300799), legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), CHECK((actor_user_id IS NULL AND actor_discord_id IS NULL AND actor_role IS NULL) OR (actor_user_id IS NOT NULL AND actor_discord_id IS NULL AND actor_role IS NOT NULL AND actor_role='admin') OR (actor_user_id IS NOT NULL AND actor_discord_id IS NOT NULL AND actor_role IS NOT NULL AND actor_role='steward')), CHECK((action='enable' AND ((resolved_at IS NULL AND deidentify_at IS NULL AND retain_until IS NULL) OR (resolved_at IS NOT NULL AND deidentify_at=resolved_at+7776000 AND retain_until=resolved_at+34560000))) OR (action='disable' AND resolved_at IS NOT NULL)), CHECK((resolved_at IS NULL AND deidentify_at IS NULL AND retain_until IS NULL) OR (resolved_at IS NOT NULL AND deidentify_at=resolved_at+7776000 AND retain_until=resolved_at+34560000)));
CREATE INDEX idx_maintenance_events_retention ON maintenance_events(retain_until,id);
CREATE INDEX idx_maintenance_events_open ON maintenance_events(action,resolved_at,id);
CREATE TABLE legal_holds (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lgh_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), object_kind TEXT NOT NULL CHECK(object_kind IN ('maintenance_event','report_case','announcement_audit','donation','request_log')), object_ref TEXT NOT NULL CHECK(typeof(object_ref)='text'), state TEXT NOT NULL CHECK(state IN ('active','released','expired')), revision INTEGER NOT NULL CHECK(revision>=1), basis TEXT NOT NULL DEFAULT '' CHECK(typeof(basis)='text' AND length(basis) BETWEEN 1 AND 1024 AND length(CAST(basis AS BLOB))<=4096), created_by_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), ended_by_user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT, ended_at INTEGER CHECK(ended_at IS NULL OR ended_at BETWEEN 0 AND 253402300799), end_reason TEXT CHECK(end_reason IS NULL OR (typeof(end_reason)='text' AND length(end_reason) BETWEEN 1 AND 1024 AND length(CAST(end_reason AS BLOB))<=4096)), retain_until INTEGER CHECK(retain_until IS NULL OR retain_until BETWEEN 0 AND 253402300799), UNIQUE(object_kind,object_ref), CHECK(expires_at>created_at AND expires_at<=created_at+31536000), CHECK((object_kind IN ('maintenance_event','report_case') AND ((object_kind='maintenance_event' AND length(object_ref)=25 AND substr(object_ref,1,3)='op_' AND substr(object_ref,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(object_ref,-1,1) IN ('A','Q','g','w')) OR (object_kind='report_case' AND length(object_ref)=26 AND substr(object_ref,1,4)='rpc_' AND substr(object_ref,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(object_ref,-1,1) IN ('A','Q','g','w')))) OR (object_kind IN ('announcement_audit','donation','request_log') AND object_ref GLOB '[1-9]*' AND object_ref NOT GLOB '*[^0-9]*')), CHECK((state='active' AND ended_by_user_id IS NULL AND ended_at IS NULL AND end_reason IS NULL AND retain_until IS NULL) OR (state='released' AND ended_by_user_id IS NOT NULL AND ended_at IS NOT NULL AND ended_at<expires_at AND end_reason IS NOT NULL AND retain_until=ended_at+34560000) OR (state='expired' AND ended_by_user_id IS NULL AND ended_at=expires_at AND end_reason='expired' AND retain_until=ended_at+34560000)));
CREATE INDEX idx_legal_holds_object ON legal_holds(object_kind,object_ref,state);
CREATE INDEX idx_legal_holds_expiry ON legal_holds(state,expires_at,id);
CREATE INDEX idx_legal_holds_retention ON legal_holds(retain_until,id);
 CREATE TABLE legal_hold_audits (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), hold_id_text TEXT NOT NULL CHECK(length(hold_id_text)=26 AND substr(hold_id_text,1,4)='lgh_' AND substr(hold_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(hold_id_text,-1,1) IN ('A','Q','g','w')), actor_user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT, action TEXT NOT NULL CHECK(action IN ('create','release','expire')), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), retain_until INTEGER CHECK(retain_until IS NULL OR retain_until BETWEEN 0 AND 253402300799), UNIQUE(hold_id_text,action));
CREATE INDEX idx_legal_hold_audits_retention ON legal_hold_audits(retain_until,id);
CREATE TABLE legal_hold_read_audits (hold_id_text TEXT NOT NULL CHECK(length(hold_id_text)=26 AND substr(hold_id_text,1,4)='lgh_' AND substr(hold_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(hold_id_text,-1,1) IN ('A','Q','g','w')), admin_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT, read_kind TEXT NOT NULL CHECK(read_kind IN ('metadata','object')), first_read_at INTEGER NOT NULL CHECK(first_read_at BETWEEN 0 AND 253402300799), last_read_at INTEGER NOT NULL CHECK(last_read_at BETWEEN 0 AND 253402300799 AND last_read_at>=first_read_at), read_count INTEGER NOT NULL CHECK(read_count>=1), retain_until INTEGER CHECK(retain_until IS NULL OR retain_until BETWEEN 0 AND 253402300799), PRIMARY KEY(hold_id_text,admin_user_id,read_kind));
CREATE INDEX idx_legal_hold_reads_retention ON legal_hold_read_audits(retain_until,hold_id_text);

-- ===== games ================================================================
CREATE TABLE game_user_preferences (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, tutorial_rps_seen INTEGER NOT NULL DEFAULT 0 CHECK(tutorial_rps_seen IN (0,1)), game_profile_public INTEGER NOT NULL DEFAULT 0 CHECK(game_profile_public IN (0,1)), updated_at INTEGER NOT NULL);
CREATE TABLE game_fishing_batches (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='fb_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, bait TEXT NOT NULL CHECK(bait IN ('worm','lure','premium')), count INTEGER NOT NULL CHECK(count IN (1,10)), unit_price_milli INTEGER NOT NULL CHECK(unit_price_milli BETWEEN 0 AND 9000000000000000), entry_total_milli INTEGER NOT NULL CHECK(entry_total_milli BETWEEN 0 AND 9000000000000000), payout_total_milli INTEGER NOT NULL CHECK(payout_total_milli BETWEEN 0 AND 9000000000000000), operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), state TEXT NOT NULL CHECK(state IN ('reserved','committed','released')), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 10), next_attempt_at INTEGER, last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('rng_failed','settlement_failed','db_busy','internal_retryable','invariant_violation')), retry_exhausted INTEGER NOT NULL DEFAULT 0 CHECK(retry_exhausted IN (0,1)), created_at INTEGER NOT NULL, settled_at INTEGER, revealed_at INTEGER, UNIQUE(user_id,operation_id), CHECK((state='reserved' AND settled_at IS NULL AND revealed_at IS NULL AND ((retry_exhausted=0 AND next_attempt_at IS NOT NULL) OR (retry_exhausted=1 AND next_attempt_at IS NULL))) OR (state IN ('committed','released') AND settled_at IS NOT NULL AND next_attempt_at IS NULL AND retry_exhausted=0)), CHECK(entry_total_milli=unit_price_milli*count));
CREATE UNIQUE INDEX idx_fishing_one_reserved ON game_fishing_batches(user_id) WHERE state='reserved';
CREATE INDEX idx_fishing_due ON game_fishing_batches(state,next_attempt_at,id);
CREATE INDEX idx_fishing_user ON game_fishing_batches(user_id,created_at,id);
CREATE INDEX idx_fishing_unrevealed ON game_fishing_batches(user_id,settled_at,id) WHERE settled_at IS NOT NULL AND revealed_at IS NULL;
CREATE INDEX idx_fishing_retention ON game_fishing_batches(settled_at,id);
CREATE TABLE game_fishing_outcomes (batch_id TEXT NOT NULL REFERENCES game_fishing_batches(id) ON DELETE CASCADE CHECK(length(batch_id)=25 AND substr(batch_id,1,3)='fb_' AND substr(batch_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id,-1,1) IN ('A','Q','g','w')), ordinal INTEGER NOT NULL CHECK(ordinal>=0), species_key TEXT NOT NULL, tier TEXT NOT NULL CHECK(tier IN ('junk','small','regular','big','giant','legend','treasure')), size_cm INTEGER NOT NULL CHECK(size_cm BETWEEN 0 AND 200), payout_milli INTEGER NOT NULL CHECK(payout_milli>=0), PRIMARY KEY(batch_id,ordinal), CHECK((tier='junk' AND species_key IN ('boot','seaweed','plastic_bag','branch','old_tire','glasses','phone_case','fry')) OR (tier='small' AND species_key IN ('whitebait','gudgeon','horse_mouth','smelt','loach')) OR (tier='regular' AND species_key IN ('crucian','tilapia','yellow_catfish','ayu','stream_carp')) OR (tier='big' AND species_key IN ('common_carp','snakehead','catfish','mandarin_fish','rainbow_trout')) OR (tier='giant' AND species_key IN ('grass_carp','silver_carp','bighead_carp','black_carp','japanese_eel')) OR (tier='legend' AND species_key IN ('yellowcheek','taimen','koi')) OR (tier='treasure' AND species_key IN ('bottle','clover','shell'))), CHECK((tier IN ('junk','treasure') AND size_cm=0) OR (tier='small' AND size_cm BETWEEN 5 AND 25) OR (tier='regular' AND size_cm BETWEEN 15 AND 35) OR (tier='big' AND size_cm BETWEEN 30 AND 80) OR (tier='giant' AND size_cm BETWEEN 60 AND 150) OR (tier='legend' AND size_cm BETWEEN 100 AND 200)));
CREATE INDEX idx_fishing_outcomes_rank ON game_fishing_outcomes(tier,size_cm,batch_id,ordinal);
CREATE TABLE game_fishing_best (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, batch_id TEXT, ordinal INTEGER, species_key TEXT NOT NULL, tier TEXT NOT NULL, size_cm INTEGER NOT NULL CHECK(size_cm BETWEEN 0 AND 200), caught_at INTEGER NOT NULL, public_tie_key BLOB NOT NULL CHECK(typeof(public_tie_key)='blob' AND length(public_tie_key)=32), FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE SET NULL, CHECK((batch_id IS NULL AND ordinal IS NULL) OR (batch_id IS NOT NULL AND ordinal IS NOT NULL)), CHECK(batch_id IS NULL OR (length(batch_id)=25 AND substr(batch_id,1,3)='fb_' AND substr(batch_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id,-1,1) IN ('A','Q','g','w'))));
CREATE INDEX idx_game_fishing_best_rank ON game_fishing_best(size_cm DESC,caught_at ASC,public_tie_key);
CREATE TABLE game_fishing_rank_facts (batch_id_text TEXT NOT NULL PRIMARY KEY CHECK(length(batch_id_text)=25 AND substr(batch_id_text,1,3)='fb_' AND substr(batch_id_text,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id_text,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, settled_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, payout_total BLOB NOT NULL CHECK(typeof(payout_total)='blob' AND length(payout_total)=16), aggregate_applied INTEGER NOT NULL CHECK(aggregate_applied IN (0,1)), CHECK(expires_at=settled_at+2592000));
CREATE INDEX idx_fishing_rank_facts_due ON game_fishing_rank_facts(expires_at,batch_id_text);
CREATE INDEX idx_fishing_rank_facts_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text);
CREATE TABLE game_fishing_rank_aggregates (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, batch_count BLOB NOT NULL CHECK(typeof(batch_count)='blob' AND length(batch_count)=16), total_payout BLOB NOT NULL CHECK(typeof(total_payout)='blob' AND length(total_payout)=16), score_achieved_at INTEGER NOT NULL, public_tie_key BLOB NOT NULL CHECK(typeof(public_tie_key)='blob' AND length(public_tie_key)=32), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), updated_at INTEGER NOT NULL);
CREATE INDEX idx_fishing_rank_aggregates_rank ON game_fishing_rank_aggregates(total_payout DESC,score_achieved_at ASC,public_tie_key);
CREATE TABLE game_linklink_sessions (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='ll_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, spec TEXT NOT NULL CHECK(spec IN ('6x8','8x8','10x10')), state TEXT NOT NULL CHECK(state='active'), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), price_milli INTEGER NOT NULL CHECK(price_milli BETWEEN 0 AND 9000000000000000), board_blob BLOB NOT NULL CHECK(typeof(board_blob)='blob' AND length(board_blob)<=16384), removed_bits BLOB NOT NULL CHECK(typeof(removed_bits)='blob' AND length(removed_bits) BETWEEN 1 AND 13 AND ((spec='6x8' AND length(removed_bits)=6) OR (spec='8x8' AND length(removed_bits)=8) OR (spec='10x10' AND length(removed_bits)=13)) AND (spec<>'10x10' OR substr(hex(removed_bits),-2,1)='0')), pairs_removed INTEGER NOT NULL CHECK(pairs_removed>=0), deadline INTEGER NOT NULL, operation_id TEXT NOT NULL CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE(user_id));
CREATE INDEX idx_linklink_deadline ON game_linklink_sessions(deadline,id);
CREATE INDEX idx_linklink_user ON game_linklink_sessions(user_id,id);
CREATE TABLE game_linklink_summaries (session_id TEXT NOT NULL PRIMARY KEY CHECK(length(session_id)=25 AND substr(session_id,1,3)='ll_' AND substr(session_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, spec TEXT NOT NULL CHECK(spec IN ('6x8','8x8','10x10')), price_milli INTEGER NOT NULL CHECK(price_milli>=0), terminal_reason TEXT NOT NULL CHECK(terminal_reason IN ('completed','timed_out','abandoned')), started_at INTEGER NOT NULL, deadline INTEGER NOT NULL, terminal_at INTEGER NOT NULL, pairs_removed INTEGER NOT NULL CHECK(pairs_removed>=0), score INTEGER, CHECK((terminal_reason IN ('completed','timed_out') AND score IS NOT NULL) OR (terminal_reason='abandoned' AND score IS NULL)));
CREATE INDEX idx_linklink_summaries_retention ON game_linklink_summaries(terminal_at,session_id);
CREATE INDEX idx_linklink_summaries_user ON game_linklink_summaries(user_id,terminal_at,session_id);
CREATE TABLE game_rps_queue (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5)='rpsq_' AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), reservation_operation_id TEXT NOT NULL CHECK(length(reservation_operation_id)=25 AND substr(reservation_operation_id,1,3)='op_' AND substr(reservation_operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(reservation_operation_id,-1,1) IN ('A','Q','g','w')), reserved BLOB NOT NULL CHECK(typeof(reserved)='blob' AND length(reserved)=16), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), device_token_hash BLOB NOT NULL CHECK(typeof(device_token_hash)='blob' AND length(device_token_hash)=32), source_ip_hash BLOB NOT NULL CHECK(typeof(source_ip_hash)='blob' AND length(source_ip_hash)=32), deadline INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX idx_rps_queue_match ON game_rps_queue(mode,deadline,created_at,id);
CREATE INDEX idx_rps_queue_user ON game_rps_queue(user_id,id);
CREATE TABLE game_rps_sessions (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rps_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), rules_version INTEGER NOT NULL CHECK(rules_version>=1), state TEXT NOT NULL CHECK(state IN ('started','terminal_processing')), phase TEXT NOT NULL CHECK(phase IN ('gesture','dealer_raise','followers','paid_pool_gesture','free_pool_gesture','ultimate_gesture','terminal_processing')), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), phase_seq BLOB NOT NULL CHECK(typeof(phase_seq)='blob' AND length(phase_seq)=16), identity_epoch BLOB NOT NULL CHECK(typeof(identity_epoch)='blob' AND length(identity_epoch)=16), cut_seq BLOB NOT NULL CHECK(typeof(cut_seq)='blob' AND length(cut_seq)=16), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), dealer_seat INTEGER CHECK(dealer_seat BETWEEN 0 AND 2), base_milli INTEGER NOT NULL CHECK(base_milli BETWEEN 0 AND 9000000000000000), platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999), welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999), thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999), gesture_seconds INTEGER NOT NULL CHECK(gesture_seconds BETWEEN 5 AND 20), dealer_seconds INTEGER NOT NULL CHECK(dealer_seconds BETWEEN 5 AND 15), follower_seconds INTEGER NOT NULL CHECK(follower_seconds BETWEEN 5 AND 15), player_pool BLOB NOT NULL CHECK(typeof(player_pool)='blob' AND length(player_pool)=16), permanent_multiplier BLOB NOT NULL CHECK(typeof(permanent_multiplier)='blob' AND length(permanent_multiplier)=16), pool_base_multiplier BLOB CHECK(pool_base_multiplier IS NULL OR (typeof(pool_base_multiplier)='blob' AND length(pool_base_multiplier)=16 AND hex(pool_base_multiplier)<>'00000000000000000000000000000000')), current_plan_multiplier BLOB CHECK(current_plan_multiplier IS NULL OR (typeof(current_plan_multiplier)='blob' AND length(current_plan_multiplier)=16 AND hex(current_plan_multiplier)<>'00000000000000000000000000000000')), dealer_raise BLOB CHECK(dealer_raise IS NULL OR (typeof(dealer_raise)='blob' AND length(dealer_raise)=16 AND hex(dealer_raise)<>'00000000000000000000000000000000')), base_round_count BLOB NOT NULL CHECK(typeof(base_round_count)='blob' AND length(base_round_count)=16), paid_tie_count BLOB NOT NULL CHECK(typeof(paid_tie_count)='blob' AND length(paid_tie_count)=16), free_tie_count BLOB NOT NULL CHECK(typeof(free_tie_count)='blob' AND length(free_tie_count)=16), paid_pool_streak BLOB NOT NULL CHECK(typeof(paid_pool_streak)='blob' AND length(paid_pool_streak)=16), free_pool_streak BLOB NOT NULL CHECK(typeof(free_pool_streak)='blob' AND length(free_pool_streak)=16), platform_cut_total BLOB NOT NULL CHECK(typeof(platform_cut_total)='blob' AND length(platform_cut_total)=16), welfare_cut_total BLOB NOT NULL CHECK(typeof(welfare_cut_total)='blob' AND length(welfare_cut_total)=16), thursday_cut_total BLOB NOT NULL CHECK(typeof(thursday_cut_total)='blob' AND length(thursday_cut_total)=16), welfare_carry_total BLOB NOT NULL CHECK(typeof(welfare_carry_total)='blob' AND length(welfare_carry_total)=16), reminder_state TEXT NOT NULL DEFAULT 'none' CHECK(reminder_state IN ('none','active')), phase_deadline INTEGER CHECK(phase_deadline IS NULL OR phase_deadline BETWEEN 0 AND 253402300799), health_epoch INTEGER NOT NULL DEFAULT 0 CHECK(health_epoch>=0), recent_events_blob BLOB NOT NULL DEFAULT X'' CHECK(typeof(recent_events_blob)='blob' AND length(recent_events_blob)<=131072), recent_first_seq BLOB NOT NULL CHECK(typeof(recent_first_seq)='blob' AND length(recent_first_seq)=16), recent_last_seq BLOB NOT NULL CHECK(typeof(recent_last_seq)='blob' AND length(recent_last_seq)=16), recent_event_count INTEGER NOT NULL DEFAULT 0 CHECK(recent_event_count BETWEEN 0 AND 64), terminal_operation_id TEXT CHECK(terminal_operation_id IS NULL OR (length(terminal_operation_id)=25 AND substr(terminal_operation_id,1,3)='op_' AND substr(terminal_operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(terminal_operation_id,-1,1) IN ('A','Q','g','w'))), terminal_retry_attempt_count BLOB NOT NULL CHECK(typeof(terminal_retry_attempt_count)='blob' AND length(terminal_retry_attempt_count)=16), terminal_next_retry_at INTEGER, terminal_last_error_class TEXT CHECK(terminal_last_error_class IS NULL OR terminal_last_error_class IN ('db_busy','internal_retryable','invariant_violation')), started_at INTEGER NOT NULL, terminal_reason TEXT CHECK(terminal_reason IS NULL OR terminal_reason IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')), CHECK((state='started' AND phase IN ('gesture','dealer_raise','followers','paid_pool_gesture','free_pool_gesture','ultimate_gesture') AND phase_deadline IS NOT NULL AND terminal_operation_id IS NULL AND terminal_reason IS NULL AND hex(terminal_retry_attempt_count)='00000000000000000000000000000000' AND terminal_next_retry_at IS NULL AND terminal_last_error_class IS NULL) OR (state='terminal_processing' AND phase='terminal_processing' AND phase_deadline IS NULL AND terminal_operation_id IS NOT NULL AND terminal_reason IS NOT NULL AND (hex(terminal_retry_attempt_count)='00000000000000000000000000000000' AND terminal_next_retry_at IS NULL AND terminal_last_error_class IS NULL OR hex(terminal_retry_attempt_count)<>'00000000000000000000000000000000' AND terminal_next_retry_at IS NOT NULL AND terminal_last_error_class IS NOT NULL))), CHECK((phase IN ('gesture','dealer_raise','followers','ultimate_gesture') AND current_plan_multiplier IS NOT NULL AND pool_base_multiplier IS NULL) OR (phase IN ('paid_pool_gesture','free_pool_gesture') AND current_plan_multiplier IS NULL AND pool_base_multiplier IS NOT NULL) OR (phase='terminal_processing' AND current_plan_multiplier IS NULL AND pool_base_multiplier IS NULL)), CHECK((phase='followers' AND (dealer_raise IS NULL OR hex(dealer_raise)<>'00000000000000000000000000000000')) OR (phase<>'followers' AND dealer_raise IS NULL)), CHECK((reminder_state='none') OR (phase='free_pool_gesture' AND free_pool_streak IN (X'00000000000000000000000000000003',X'00000000000000000000000000000004',X'00000000000000000000000000000005'))));
CREATE INDEX idx_rps_sessions_state ON game_rps_sessions(state,phase_deadline,id);
CREATE INDEX idx_rps_sessions_mode ON game_rps_sessions(mode,id);
CREATE TABLE game_rps_seats (session_id TEXT NOT NULL REFERENCES game_rps_sessions(id) ON DELETE CASCADE CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), seat_no INTEGER NOT NULL CHECK(seat_no BETWEEN 0 AND 2), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, deletion_state TEXT NOT NULL CHECK(deletion_state IN ('active','deletion_pending','deidentified')), display_name_snapshot TEXT, avatar_url_snapshot TEXT, starting_balance BLOB NOT NULL CHECK(typeof(starting_balance)='blob' AND length(starting_balance)=16), current_balance BLOB NOT NULL CHECK(typeof(current_balance)='blob' AND length(current_balance)=16), current_round_input BLOB NOT NULL CHECK(typeof(current_round_input)='blob' AND length(current_round_input)=16), current_all_in INTEGER NOT NULL CHECK(current_all_in IN (0,1)), current_gesture_envelope BLOB CHECK(current_gesture_envelope IS NULL OR (typeof(current_gesture_envelope)='blob' AND length(current_gesture_envelope) IN (33,34,37) AND substr(current_gesture_envelope,1,1)=X'01')), current_gesture_phase_seq BLOB CHECK(current_gesture_phase_seq IS NULL OR (typeof(current_gesture_phase_seq)='blob' AND length(current_gesture_phase_seq)=16)), follower_action TEXT CHECK(follower_action IS NULL OR follower_action IN ('call','surrender')), last_action_phase_seq BLOB CHECK(last_action_phase_seq IS NULL OR (typeof(last_action_phase_seq)='blob' AND length(last_action_phase_seq)=16)), total_input BLOB NOT NULL CHECK(typeof(total_input)='blob' AND length(total_input)=32), total_returned BLOB NOT NULL CHECK(typeof(total_returned)='blob' AND length(total_returned)=32), terminal_return BLOB CHECK(terminal_return IS NULL OR (typeof(terminal_return)='blob' AND length(terminal_return)=16)), wallet_net_sign INTEGER CHECK(wallet_net_sign IS NULL OR wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB, rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), timeout_count BLOB NOT NULL CHECK(typeof(timeout_count)='blob' AND length(timeout_count)=16), snapshot_completed_count BLOB CHECK(snapshot_completed_count IS NULL OR (typeof(snapshot_completed_count)='blob' AND length(snapshot_completed_count)=16)), snapshot_profitable_count BLOB CHECK(snapshot_profitable_count IS NULL OR (typeof(snapshot_profitable_count)='blob' AND length(snapshot_profitable_count)=16)), snapshot_rock_count BLOB CHECK(snapshot_rock_count IS NULL OR (typeof(snapshot_rock_count)='blob' AND length(snapshot_rock_count)=16)), snapshot_scissors_count BLOB CHECK(snapshot_scissors_count IS NULL OR (typeof(snapshot_scissors_count)='blob' AND length(snapshot_scissors_count)=16)), snapshot_paper_count BLOB CHECK(snapshot_paper_count IS NULL OR (typeof(snapshot_paper_count)='blob' AND length(snapshot_paper_count)=16)), stats_applied INTEGER NOT NULL DEFAULT 0 CHECK(stats_applied IN (0,1)), PRIMARY KEY(session_id,seat_no), CHECK((current_gesture_envelope IS NULL AND current_gesture_phase_seq IS NULL) OR (current_gesture_envelope IS NOT NULL AND current_gesture_phase_seq IS NOT NULL)), CHECK((terminal_return IS NULL AND wallet_net_sign IS NULL AND wallet_net_mag IS NULL) OR (terminal_return IS NOT NULL AND wallet_net_sign IS NOT NULL AND wallet_net_mag IS NOT NULL)), CHECK((wallet_net_sign IS NULL AND wallet_net_mag IS NULL) OR (wallet_net_sign IS NOT NULL AND typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7') AND ((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')))), CHECK((snapshot_completed_count IS NULL AND snapshot_profitable_count IS NULL AND snapshot_rock_count IS NULL AND snapshot_scissors_count IS NULL AND snapshot_paper_count IS NULL) OR (snapshot_completed_count IS NOT NULL AND snapshot_profitable_count IS NOT NULL AND snapshot_rock_count IS NOT NULL AND snapshot_scissors_count IS NOT NULL AND snapshot_paper_count IS NOT NULL)), CHECK((deletion_state='active') OR (display_name_snapshot IS NULL AND avatar_url_snapshot IS NULL AND snapshot_completed_count IS NULL AND snapshot_profitable_count IS NULL AND snapshot_rock_count IS NULL AND snapshot_scissors_count IS NULL AND snapshot_paper_count IS NULL)));
CREATE UNIQUE INDEX idx_rps_seats_active_user ON game_rps_seats(user_id) WHERE user_id IS NOT NULL AND deletion_state='active';
CREATE INDEX idx_rps_seats_user ON game_rps_seats(user_id,session_id,seat_no);
CREATE TABLE game_rps_user_slots (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, queue_id TEXT REFERENCES game_rps_queue(id) ON DELETE CASCADE CHECK(queue_id IS NULL OR (length(queue_id)=27 AND substr(queue_id,1,5)='rpsq_' AND substr(queue_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(queue_id,-1,1) IN ('A','Q','g','w'))), session_id TEXT REFERENCES game_rps_sessions(id) ON DELETE CASCADE CHECK(session_id IS NULL OR (length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w'))), created_at INTEGER NOT NULL, CHECK((queue_id IS NOT NULL AND session_id IS NULL) OR (queue_id IS NULL AND session_id IS NOT NULL)));
CREATE UNIQUE INDEX idx_rps_slots_queue ON game_rps_user_slots(queue_id) WHERE queue_id IS NOT NULL;
CREATE INDEX idx_rps_slots_session ON game_rps_user_slots(session_id) WHERE session_id IS NOT NULL;
CREATE TABLE game_online_leases (session_id TEXT NOT NULL CHECK((length(session_id)=25 AND substr(session_id,1,3)='ll_' AND substr(session_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')) OR (length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w'))), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, lease_id TEXT NOT NULL CHECK(length(lease_id)=26 AND substr(lease_id,1,4)='gle_' AND substr(lease_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(lease_id,-1,1) IN ('A','Q','g','w')), health_epoch INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_renewed_at INTEGER NOT NULL, PRIMARY KEY(session_id,user_id,lease_id));
CREATE INDEX idx_game_leases_expiry ON game_online_leases(expires_at,session_id);
CREATE TABLE game_rps_pending_results (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, session_id_text TEXT NOT NULL CHECK(length(session_id_text)=26 AND substr(session_id_text,1,4)='rps_' AND substr(session_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id_text,-1,1) IN ('A','Q','g','w')), mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), terminal_reason TEXT NOT NULL, own_seat_no INTEGER NOT NULL CHECK(own_seat_no BETWEEN 0 AND 2), own_input BLOB NOT NULL CHECK(typeof(own_input)='blob' AND length(own_input)=32), own_returned BLOB NOT NULL CHECK(typeof(own_returned)='blob' AND length(own_returned)=32), own_wallet_net_sign INTEGER NOT NULL CHECK(own_wallet_net_sign IN (-1,0,1)), own_wallet_net_mag BLOB NOT NULL CHECK(typeof(own_wallet_net_mag)='blob' AND length(own_wallet_net_mag)=16 AND substr(hex(own_wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), seat0_result TEXT NOT NULL CHECK(seat0_result IN ('win','loss','tie','deidentified')), seat1_result TEXT NOT NULL CHECK(seat1_result IN ('win','loss','tie','deidentified')), seat2_result TEXT NOT NULL CHECK(seat2_result IN ('win','loss','tie','deidentified')), created_at INTEGER NOT NULL, CHECK((own_wallet_net_sign=0 AND hex(own_wallet_net_mag)='00000000000000000000000000000000') OR (own_wallet_net_sign<>0 AND hex(own_wallet_net_mag)<>'00000000000000000000000000000000')));
CREATE TABLE game_rps_summaries (session_id TEXT NOT NULL PRIMARY KEY CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), rules_version INTEGER NOT NULL, base_milli INTEGER NOT NULL, platform_bp INTEGER NOT NULL, welfare_bp INTEGER NOT NULL, thursday_bp INTEGER NOT NULL, started_at INTEGER NOT NULL, terminal_at INTEGER NOT NULL, terminal_reason TEXT NOT NULL, base_round_count BLOB NOT NULL CHECK(typeof(base_round_count)='blob' AND length(base_round_count)=16), paid_tie_count BLOB NOT NULL CHECK(typeof(paid_tie_count)='blob' AND length(paid_tie_count)=16), free_tie_count BLOB NOT NULL CHECK(typeof(free_tie_count)='blob' AND length(free_tie_count)=16), total_timeout_count BLOB NOT NULL CHECK(typeof(total_timeout_count)='blob' AND length(total_timeout_count)=16), total_rock_count BLOB NOT NULL CHECK(typeof(total_rock_count)='blob' AND length(total_rock_count)=16), total_scissors_count BLOB NOT NULL CHECK(typeof(total_scissors_count)='blob' AND length(total_scissors_count)=16), total_paper_count BLOB NOT NULL CHECK(typeof(total_paper_count)='blob' AND length(total_paper_count)=16), platform_total BLOB NOT NULL CHECK(typeof(platform_total)='blob' AND length(platform_total)=16), welfare_total BLOB NOT NULL CHECK(typeof(welfare_total)='blob' AND length(welfare_total)=16), thursday_total BLOB NOT NULL CHECK(typeof(thursday_total)='blob' AND length(thursday_total)=16), delete_at INTEGER NOT NULL, CHECK(terminal_reason IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')), CHECK(platform_bp BETWEEN 0 AND 9999 AND welfare_bp BETWEEN 0 AND 9999 AND thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000));
CREATE INDEX idx_rps_summaries_retention ON game_rps_summaries(delete_at,session_id);
CREATE TABLE game_rps_summary_seats (session_id TEXT NOT NULL REFERENCES game_rps_summaries(session_id) ON DELETE CASCADE CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), seat_no INTEGER NOT NULL CHECK(seat_no BETWEEN 0 AND 2), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, input BLOB NOT NULL CHECK(typeof(input)='blob' AND length(input)=32), returned BLOB NOT NULL CHECK(typeof(returned)='blob' AND length(returned)=32), wallet_net_sign INTEGER NOT NULL CHECK(wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB NOT NULL CHECK(typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), timeout_count BLOB NOT NULL CHECK(typeof(timeout_count)='blob' AND length(timeout_count)=16), rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), PRIMARY KEY(session_id,seat_no), CHECK((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')));
CREATE TABLE game_rps_fun_stats (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, completed_count BLOB NOT NULL CHECK(typeof(completed_count)='blob' AND length(completed_count)=16), profitable_count BLOB NOT NULL CHECK(typeof(profitable_count)='blob' AND length(profitable_count)=16), rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), updated_at INTEGER NOT NULL);
CREATE TABLE game_rps_rank_facts (session_id_text TEXT NOT NULL CHECK(length(session_id_text)=26 AND substr(session_id_text,1,4)='rps_' AND substr(session_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id_text,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), terminal_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, wallet_net_sign INTEGER NOT NULL CHECK(wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB NOT NULL CHECK(typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), profitable INTEGER NOT NULL CHECK(profitable IN (0,1)), aggregate_applied INTEGER NOT NULL CHECK(aggregate_applied IN (0,1)), PRIMARY KEY(session_id_text,user_id), CHECK((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')), CHECK(expires_at=terminal_at+2592000));
CREATE INDEX idx_rps_rank_facts_due ON game_rps_rank_facts(expires_at,session_id_text,user_id);
CREATE INDEX idx_rps_rank_facts_mode ON game_rps_rank_facts(mode,user_id,expires_at);
CREATE TABLE game_rps_rank_aggregates (user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), session_count BLOB NOT NULL CHECK(typeof(session_count)='blob' AND length(session_count)=16), profitable_count BLOB NOT NULL CHECK(typeof(profitable_count)='blob' AND length(profitable_count)=16), net_profit_sign INTEGER NOT NULL CHECK(net_profit_sign IN (-1,0,1)), net_profit_mag BLOB NOT NULL CHECK(typeof(net_profit_mag)='blob' AND length(net_profit_mag)=16 AND substr(hex(net_profit_mag),1,1) IN ('0','1','2','3','4','5','6','7')), eligible INTEGER NOT NULL CHECK(eligible IN (0,1)), profit_rate_bp INTEGER NOT NULL CHECK(profit_rate_bp BETWEEN 0 AND 10000), profit_rate_achieved_at INTEGER NOT NULL, net_profit_achieved_at INTEGER NOT NULL, profit_public_tie_key BLOB NOT NULL CHECK(typeof(profit_public_tie_key)='blob' AND length(profit_public_tie_key)=32), net_public_tie_key BLOB NOT NULL CHECK(typeof(net_public_tie_key)='blob' AND length(net_public_tie_key)=32), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), updated_at INTEGER NOT NULL, PRIMARY KEY(user_id,mode), CHECK((net_profit_sign=0 AND hex(net_profit_mag)='00000000000000000000000000000000') OR (net_profit_sign<>0 AND hex(net_profit_mag)<>'00000000000000000000000000000000')));
CREATE UNIQUE INDEX idx_rps_rank_profit ON game_rps_rank_aggregates(mode,profit_rate_bp DESC,profit_rate_achieved_at,profit_public_tie_key) WHERE eligible=1;
CREATE INDEX idx_rps_rank_net ON game_rps_rank_aggregates(mode,net_profit_sign DESC,CASE WHEN net_profit_sign=1 THEN net_profit_mag END DESC,CASE WHEN net_profit_sign=-1 THEN net_profit_mag END ASC,net_profit_achieved_at ASC,net_public_tie_key) WHERE eligible=1;

-- ===== site configuration ===================================================
CREATE TABLE site_config (key TEXT NOT NULL PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL DEFAULT 0);

-- ===== invariant guards =====================================================
CREATE TRIGGER idempotency_expiry_guard BEFORE INSERT ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR NEW.expires_at<>NEW.created_at+86400
BEGIN SELECT RAISE(ABORT,'idempotency records expire exactly 24 hours after creation'); END;
CREATE TRIGGER idempotency_expiry_update_guard BEFORE UPDATE OF created_at,expires_at ON idempotency_records
WHEN typeof(NEW.created_at)<>'integer' OR typeof(NEW.expires_at)<>'integer' OR
     NEW.created_at NOT BETWEEN 0 AND 253402300799-86400 OR NEW.expires_at<>NEW.created_at+86400
BEGIN SELECT RAISE(ABORT,'idempotency records expire exactly 24 hours after creation'); END;
CREATE TRIGGER idempotency_identity_update_guard BEFORE UPDATE ON idempotency_records
WHEN NEW.scope IS NOT OLD.scope OR NEW.actor_scope_hash IS NOT OLD.actor_scope_hash OR
     NEW.key_hash IS NOT OLD.key_hash OR NEW.request_hash IS NOT OLD.request_hash OR
     NEW.lookup_fingerprint IS NOT OLD.lookup_fingerprint OR NEW.created_at IS NOT OLD.created_at OR
     NEW.expires_at IS NOT OLD.expires_at OR
     (OLD.state='accepted' AND NOT (
        (NEW.state='accepted' AND NEW.http_status IS OLD.http_status AND NEW.response_body IS OLD.response_body) OR
        (NEW.state='completed' AND NEW.http_status BETWEEN 100 AND 599 AND typeof(NEW.response_body)='blob' AND length(NEW.response_body)<=65536)
     )) OR
     (OLD.state='completed' AND (NEW.state IS NOT OLD.state OR NEW.http_status IS NOT OLD.http_status OR NEW.response_body IS NOT OLD.response_body))
BEGIN SELECT RAISE(ABORT,'idempotency identity or terminal state is immutable'); END;
CREATE TRIGGER dispatch_claim_release_guard BEFORE INSERT ON dispatch_claims
WHEN NEW.state='released' AND NEW.dispatched_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'released claim cannot be dispatched'); END;
CREATE TRIGGER dispatch_claim_release_update_guard BEFORE UPDATE OF state,dispatched_at ON dispatch_claims
WHEN NEW.state='released' AND NEW.dispatched_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'released claim cannot be dispatched'); END;
CREATE TRIGGER logical_request_terminal_state_guard BEFORE UPDATE OF state ON logical_requests
WHEN OLD.state='terminal' AND NEW.state<>'terminal'
BEGIN SELECT RAISE(ABORT,'terminal logical request cannot be reopened'); END;
CREATE TRIGGER logical_request_acceptance_identity_update_guard BEFORE UPDATE OF route_kind,model_snapshot,attempt_limit,created_at ON logical_requests
WHEN NEW.route_kind IS NOT OLD.route_kind OR NEW.model_snapshot IS NOT OLD.model_snapshot OR
     NEW.attempt_limit IS NOT OLD.attempt_limit OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'logical request acceptance facts are immutable'); END;
CREATE TRIGGER dispatch_claim_terminal_state_guard BEFORE UPDATE OF state ON dispatch_claims
WHEN OLD.state IN ('committed','released') AND NEW.state IS NOT OLD.state
BEGIN SELECT RAISE(ABORT,'terminal dispatch claim cannot be reopened'); END;
CREATE TRIGGER accepted_operation_terminal_state_guard BEFORE UPDATE OF state ON accepted_operations
WHEN OLD.state IN ('completed','failed_blocked') AND NEW.state IS NOT OLD.state
BEGIN SELECT RAISE(ABORT,'terminal accepted operation cannot be reopened'); END;
CREATE TRIGGER credit_operations_no_update BEFORE UPDATE ON credit_operations
WHEN NOT (
 NEW.id IS OLD.id AND NEW.ledger_seq IS OLD.ledger_seq AND NEW.kind IS OLD.kind AND
 NEW.source_type IS OLD.source_type AND NEW.source_id IS OLD.source_id AND NEW.source_seq IS OLD.source_seq AND
 NEW.donation_credit_delta_sign IS OLD.donation_credit_delta_sign AND NEW.donation_credit_delta_mag IS OLD.donation_credit_delta_mag AND
 NEW.donation_credit_after IS OLD.donation_credit_after AND NEW.reason IS OLD.reason AND NEW.created_at IS OLD.created_at AND
 (NEW.actor_user_id IS OLD.actor_user_id OR (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.actor_user_id))) AND
 (NEW.donation_credit_user_id IS OLD.donation_credit_user_id OR (OLD.donation_credit_user_id IS NOT NULL AND NEW.donation_credit_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.donation_credit_user_id)))
)
BEGIN SELECT RAISE(ABORT,'credit_operations is append-only'); END;
CREATE TRIGGER credit_operations_no_delete BEFORE DELETE ON credit_operations
BEGIN SELECT RAISE(ABORT,'credit_operations is append-only'); END;
CREATE TRIGGER credit_entries_no_update BEFORE UPDATE ON credit_entries
WHEN NOT (
 NEW.operation_id IS OLD.operation_id AND NEW.line_no IS OLD.line_no AND NEW.account_kind_snapshot IS OLD.account_kind_snapshot AND
 NEW.delta_sign IS OLD.delta_sign AND NEW.delta_mag IS OLD.delta_mag AND NEW.balance_after_sign IS OLD.balance_after_sign AND
 NEW.balance_after_mag IS OLD.balance_after_mag AND
 (NEW.account_id IS OLD.account_id OR (OLD.account_id IS NOT NULL AND NEW.account_id IS NULL AND NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=OLD.account_id)))
)
BEGIN SELECT RAISE(ABORT,'credit_entries is append-only'); END;
CREATE TRIGGER credit_entries_no_delete BEFORE DELETE ON credit_entries
BEGIN SELECT RAISE(ABORT,'credit_entries is append-only'); END;
CREATE TRIGGER credit_entries_account_kind_guard BEFORE INSERT ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_entries_account_kind_update_guard BEFORE UPDATE OF account_id,account_kind_snapshot ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_account_code_guard BEFORE INSERT ON credit_accounts
WHEN NEW.code IS NOT NULL AND NOT (typeof(NEW.code)='text' AND length(CAST(NEW.code AS BLOB)) BETWEEN 1 AND 64 AND NEW.code NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit account code is not canonical text'); END;
CREATE TRIGGER credit_account_code_update_guard BEFORE UPDATE OF code ON credit_accounts
WHEN NEW.code IS NOT NULL AND NOT (typeof(NEW.code)='text' AND length(CAST(NEW.code AS BLOB)) BETWEEN 1 AND 64 AND NEW.code NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit account code is not canonical text'); END;
CREATE TRIGGER credit_operation_source_guard BEFORE INSERT ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER credit_operation_source_update_guard BEFORE UPDATE OF source_type,source_id ON credit_operations
WHEN NOT (typeof(NEW.source_id)='text' AND length(CAST(NEW.source_id AS BLOB)) BETWEEN 25 AND 64 AND NEW.source_id NOT GLOB '*[^ -~]*')
BEGIN SELECT RAISE(ABORT,'credit operation source is not canonical text'); END;
CREATE TRIGGER report_case_scalar_guard BEFORE INSERT ON report_cases
WHEN typeof(NEW.canonical_base_url)<>'text'
 OR length(CAST(NEW.canonical_base_url AS BLOB)) NOT BETWEEN 1 AND 4096
 OR (NEW.cursor_text IS NOT NULL AND (typeof(NEW.cursor_text)<>'text' OR length(CAST(NEW.cursor_text AS BLOB))>512))
 OR (NEW.decision_reason IS NOT NULL AND (typeof(NEW.decision_reason)<>'text' OR length(NEW.decision_reason)>2048))
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.terminal_at IS NOT NULL AND NEW.terminal_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.next_retry_at IS NOT NULL AND NEW.next_retry_at NOT BETWEEN 0 AND 253402300799)
 OR typeof(NEW.material_version)<>'integer' OR NEW.material_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.target_version)<>'integer' OR NEW.target_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.material_count)<>'integer' OR NEW.material_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.target_count)<>'integer' OR NEW.target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.distinct_owner_count)<>'integer' OR NEW.distinct_owner_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.processed_target_count)<>'integer' OR NEW.processed_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.deleted_target_count)<>'integer' OR NEW.deleted_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.released_target_count)<>'integer' OR NEW.released_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.retry_attempt_count)<>'integer' OR NEW.retry_attempt_count NOT BETWEEN 0 AND 9223372036854775807
 OR NOT ((NEW.last_error_class IS NULL AND NEW.next_retry_at IS NULL)
      OR (NEW.progress_state='in_progress' AND NEW.last_error_class IS NOT NULL AND NEW.next_retry_at IS NOT NULL))
 OR (NEW.status IN ('pending_indexing','pending_review','approved_processing') AND NEW.terminal_at IS NOT NULL)
 OR (NEW.status IN ('approved','rejected','expired') AND NEW.terminal_at IS NULL)
 OR (NEW.status='pending_review' AND NEW.decision_at IS NOT NULL)
 OR (NEW.status IN ('approved','rejected') AND NEW.decision_at IS NULL)
BEGIN SELECT RAISE(ABORT,'report case scalar/state invariant'); END;
CREATE TRIGGER report_case_scalar_update_guard BEFORE UPDATE ON report_cases
WHEN typeof(NEW.canonical_base_url)<>'text'
 OR length(CAST(NEW.canonical_base_url AS BLOB)) NOT BETWEEN 1 AND 4096
 OR (NEW.cursor_text IS NOT NULL AND (typeof(NEW.cursor_text)<>'text' OR length(CAST(NEW.cursor_text AS BLOB))>512))
 OR (NEW.decision_reason IS NOT NULL AND (typeof(NEW.decision_reason)<>'text' OR length(NEW.decision_reason)>2048))
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.terminal_at IS NOT NULL AND NEW.terminal_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.next_retry_at IS NOT NULL AND NEW.next_retry_at NOT BETWEEN 0 AND 253402300799)
 OR typeof(NEW.material_version)<>'integer' OR NEW.material_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.target_version)<>'integer' OR NEW.target_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.material_count)<>'integer' OR NEW.material_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.target_count)<>'integer' OR NEW.target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.distinct_owner_count)<>'integer' OR NEW.distinct_owner_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.processed_target_count)<>'integer' OR NEW.processed_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.deleted_target_count)<>'integer' OR NEW.deleted_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.released_target_count)<>'integer' OR NEW.released_target_count NOT BETWEEN 0 AND 9223372036854775807
 OR typeof(NEW.retry_attempt_count)<>'integer' OR NEW.retry_attempt_count NOT BETWEEN 0 AND 9223372036854775807
 OR NOT ((NEW.last_error_class IS NULL AND NEW.next_retry_at IS NULL)
      OR (NEW.progress_state='in_progress' AND NEW.last_error_class IS NOT NULL AND NEW.next_retry_at IS NOT NULL))
 OR (NEW.status IN ('pending_indexing','pending_review','approved_processing') AND NEW.terminal_at IS NOT NULL)
 OR (NEW.status IN ('approved','rejected','expired') AND NEW.terminal_at IS NULL)
 OR (NEW.status='pending_review' AND NEW.decision_at IS NOT NULL)
 OR (NEW.status IN ('approved','rejected') AND NEW.decision_at IS NULL)
BEGIN SELECT RAISE(ABORT,'report case scalar/state invariant'); END;
CREATE TRIGGER report_material_scalar_guard BEFORE INSERT ON report_materials
WHEN typeof(NEW.note_text)<>'text' OR length(NEW.note_text)>2048
BEGIN SELECT RAISE(ABORT,'report material text is invalid'); END;
CREATE TRIGGER report_material_scalar_update_guard BEFORE UPDATE OF note_text ON report_materials
WHEN typeof(NEW.note_text)<>'text' OR length(NEW.note_text)>2048
BEGIN SELECT RAISE(ABORT,'report material text is invalid'); END;
CREATE TRIGGER report_target_scalar_guard BEFORE INSERT ON report_targets
WHEN typeof(NEW.target_seq)<>'integer' OR NEW.target_seq NOT BETWEEN 0 AND 9223372036854775807
 OR (NEW.owner_display_name IS NOT NULL AND (typeof(NEW.owner_display_name)<>'text' OR length(NEW.owner_display_name)>128))
 OR length(CAST(NEW.key_display_head AS BLOB))>16
 OR length(CAST(NEW.key_display_tail AS BLOB))>16
 OR NEW.key_display_head GLOB '*[^ -~]*'
 OR NEW.key_display_tail GLOB '*[^ -~]*'
BEGIN SELECT RAISE(ABORT,'report target scalar is invalid'); END;
CREATE TRIGGER report_target_scalar_update_guard BEFORE UPDATE ON report_targets
WHEN typeof(NEW.target_seq)<>'integer' OR NEW.target_seq NOT BETWEEN 0 AND 9223372036854775807
 OR (NEW.owner_display_name IS NOT NULL AND (typeof(NEW.owner_display_name)<>'text' OR length(NEW.owner_display_name)>128))
 OR length(CAST(NEW.key_display_head AS BLOB))>16
 OR length(CAST(NEW.key_display_tail AS BLOB))>16
 OR NEW.key_display_head GLOB '*[^ -~]*'
 OR NEW.key_display_tail GLOB '*[^ -~]*'
BEGIN SELECT RAISE(ABORT,'report target scalar is invalid'); END;
CREATE TRIGGER report_decisions_no_update BEFORE UPDATE ON report_decisions
WHEN NOT (
 NEW.id IS OLD.id AND NEW.case_id IS OLD.case_id AND NEW.material_version IS OLD.material_version AND
 NEW.target_version IS OLD.target_version AND NEW.action IS OLD.action AND NEW.reason IS OLD.reason AND
 NEW.created_at IS OLD.created_at AND
 (NEW.actor_user_id IS OLD.actor_user_id OR (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL))
)
BEGIN SELECT RAISE(ABORT,'report decisions are append-only'); END;
CREATE TRIGGER donation_reviews_no_update BEFORE UPDATE ON donation_reviews
WHEN NOT (
 NEW.id IS OLD.id AND NEW.donation_id IS OLD.donation_id AND NEW.submission_revision IS OLD.submission_revision AND
 NEW.reviewer_role IS OLD.reviewer_role AND NEW.action IS OLD.action AND NEW.note IS OLD.note AND NEW.created_at IS OLD.created_at AND
 (NEW.reviewer_user_id IS OLD.reviewer_user_id OR (OLD.reviewer_user_id IS NOT NULL AND NEW.reviewer_user_id IS NULL))
)
BEGIN SELECT RAISE(ABORT,'donation reviews are append-only'); END;
CREATE TRIGGER donations_text_guard BEFORE INSERT ON donations
WHEN typeof(NEW.description)<>'text' OR length(NEW.description)>1024 OR length(CAST(NEW.description AS BLOB))>4096
 OR typeof(NEW.review_note)<>'text' OR length(NEW.review_note)>1024 OR length(CAST(NEW.review_note AS BLOB))>4096
BEGIN SELECT RAISE(ABORT,'donation text is too long or invalid'); END;
CREATE TRIGGER donations_text_update_guard BEFORE UPDATE OF description,review_note ON donations
WHEN typeof(NEW.description)<>'text' OR length(NEW.description)>1024 OR length(CAST(NEW.description AS BLOB))>4096
 OR typeof(NEW.review_note)<>'text' OR length(NEW.review_note)>1024 OR length(CAST(NEW.review_note AS BLOB))>4096
BEGIN SELECT RAISE(ABORT,'donation text is too long or invalid'); END;
CREATE TRIGGER donation_reviews_text_guard BEFORE INSERT ON donation_reviews
WHEN typeof(NEW.note)<>'text' OR length(NEW.note)>1024 OR length(CAST(NEW.note AS BLOB))>4096
BEGIN SELECT RAISE(ABORT,'donation review text is too long or invalid'); END;
CREATE TRIGGER donation_reviews_text_update_guard BEFORE UPDATE OF note ON donation_reviews
WHEN typeof(NEW.note)<>'text' OR length(NEW.note)>1024 OR length(CAST(NEW.note AS BLOB))>4096
BEGIN SELECT RAISE(ABORT,'donation review text is too long or invalid'); END;
CREATE TRIGGER report_decisions_text_guard BEFORE INSERT ON report_decisions
WHEN typeof(NEW.reason)<>'text' OR length(NEW.reason)>2048 OR length(CAST(NEW.reason AS BLOB))>8192
BEGIN SELECT RAISE(ABORT,'report decision text is too long or invalid'); END;
CREATE TRIGGER report_decisions_text_update_guard BEFORE UPDATE OF reason ON report_decisions
WHEN typeof(NEW.reason)<>'text' OR length(NEW.reason)>2048 OR length(CAST(NEW.reason AS BLOB))>8192
BEGIN SELECT RAISE(ABORT,'report decision text is too long or invalid'); END;
CREATE TRIGGER maintenance_events_no_update BEFORE UPDATE ON maintenance_events
WHEN NOT (
  NEW.id IS OLD.id AND NEW.action IS OLD.action AND NEW.reason IS OLD.reason AND NEW.created_at IS OLD.created_at AND
  (NEW.legal_hold_consumed IS OLD.legal_hold_consumed OR
   (OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND EXISTS(
    SELECT 1 FROM legal_holds h WHERE h.object_kind='maintenance_event' AND h.object_ref=OLD.id AND h.state='active'))) AND
  ((NEW.actor_user_id IS OLD.actor_user_id AND NEW.actor_discord_id IS OLD.actor_discord_id AND NEW.actor_role IS OLD.actor_role) OR
   (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL AND NEW.actor_discord_id IS NULL AND NEW.actor_role IS NULL AND
    (NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.actor_user_id) OR
     EXISTS(SELECT 1 FROM user_deletion_markers m WHERE m.user_id=OLD.actor_user_id) OR
     (OLD.resolved_at IS NOT NULL AND OLD.deidentify_at IS NOT NULL AND unixepoch()>=OLD.deidentify_at)))) AND
  ((NEW.resolved_at IS OLD.resolved_at AND NEW.deidentify_at IS OLD.deidentify_at AND NEW.retain_until IS OLD.retain_until) OR
   (OLD.action='enable' AND OLD.resolved_at IS NULL AND OLD.deidentify_at IS NULL AND OLD.retain_until IS NULL AND
    NEW.resolved_at IS NOT NULL AND NEW.deidentify_at=NEW.resolved_at+7776000 AND NEW.retain_until=NEW.resolved_at+34560000))
)
BEGIN SELECT RAISE(ABORT,'maintenance event is append-only'); END;
CREATE TRIGGER announcement_scalar_guard BEFORE INSERT ON announcements
WHEN typeof(NEW.draft_title_zh)<>'text' OR typeof(NEW.draft_title_en)<>'text'
 OR typeof(NEW.draft_body_zh)<>'text' OR typeof(NEW.draft_body_en)<>'text'
 OR (NEW.published_title_zh IS NOT NULL AND typeof(NEW.published_title_zh)<>'text')
 OR (NEW.published_title_en IS NOT NULL AND typeof(NEW.published_title_en)<>'text')
 OR (NEW.published_body_zh IS NOT NULL AND typeof(NEW.published_body_zh)<>'text')
 OR (NEW.published_body_en IS NOT NULL AND typeof(NEW.published_body_en)<>'text')
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'announcement scalar is invalid'); END;
CREATE TRIGGER announcement_scalar_update_guard BEFORE UPDATE ON announcements
WHEN typeof(NEW.draft_title_zh)<>'text' OR typeof(NEW.draft_title_en)<>'text'
 OR typeof(NEW.draft_body_zh)<>'text' OR typeof(NEW.draft_body_en)<>'text'
 OR (NEW.published_title_zh IS NOT NULL AND typeof(NEW.published_title_zh)<>'text')
 OR (NEW.published_title_en IS NOT NULL AND typeof(NEW.published_title_en)<>'text')
 OR (NEW.published_body_zh IS NOT NULL AND typeof(NEW.published_body_zh)<>'text')
 OR (NEW.published_body_en IS NOT NULL AND typeof(NEW.published_body_en)<>'text')
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'announcement scalar is invalid'); END;
CREATE TRIGGER announcement_audits_scalar_guard BEFORE INSERT ON announcement_audits
WHEN NEW.from_revision<0 OR NEW.to_revision<1
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NEW.actor_deidentify_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'announcement audit scalar is invalid'); END;
CREATE TRIGGER announcement_audits_revision_guard BEFORE INSERT ON announcement_audits
WHEN (NEW.action='create' AND (NEW.from_revision<>0 OR NEW.to_revision<>1))
  OR (NEW.action<>'create' AND (NEW.from_revision<1 OR NEW.to_revision<=NEW.from_revision))
BEGIN SELECT RAISE(ABORT,'announcement audit revision transition is invalid'); END;
 CREATE TRIGGER announcement_audits_no_update BEFORE UPDATE ON announcement_audits
 WHEN NOT (
   NEW.id=OLD.id AND NEW.announcement_id_text IS OLD.announcement_id_text AND NEW.action IS OLD.action AND
   NEW.from_revision=OLD.from_revision AND NEW.to_revision=OLD.to_revision AND NEW.reason IS OLD.reason AND
   NEW.created_at=OLD.created_at AND NEW.actor_deidentify_at=OLD.actor_deidentify_at AND
   (NEW.actor_user_id IS OLD.actor_user_id OR
    (NEW.actor_user_id IS NULL AND OLD.actor_user_id IS NOT NULL AND
     (NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.actor_user_id) OR unixepoch()>=NEW.actor_deidentify_at))) AND
   (NEW.legal_hold_consumed IS OLD.legal_hold_consumed OR
    (OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND EXISTS(
      SELECT 1 FROM legal_holds h WHERE h.object_kind='announcement_audit' AND h.object_ref=CAST(OLD.id AS TEXT) AND h.state='active'))) AND
   (NEW.actor_user_id IS NOT OLD.actor_user_id OR NEW.legal_hold_consumed IS NOT OLD.legal_hold_consumed)
 )
 BEGIN SELECT RAISE(ABORT,'announcement audit update is not an allowed retention mutation'); END;
CREATE TRIGGER charity_reservation_scalar_guard BEFORE INSERT ON charity_reservations
WHEN NEW.token_reserve_milli NOT BETWEEN 0 AND 2147483647
 OR NEW.user_reserved_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.original_charge_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.user_charge_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.dispatched_at IS NOT NULL AND NEW.dispatched_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.finalized_at IS NOT NULL AND NEW.finalized_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.updated_at NOT BETWEEN 0 AND 253402300799)
 OR NOT ((NEW.state='reserved' AND NEW.dispatched_at IS NULL AND NEW.finalized_at IS NULL)
      OR (NEW.state='dispatched' AND NEW.dispatched_at IS NOT NULL AND NEW.finalized_at IS NULL)
      OR (NEW.state IN ('committed','released') AND NEW.finalized_at IS NOT NULL))
BEGIN SELECT RAISE(ABORT,'charity reservation scalar/state invariant'); END;
CREATE TRIGGER charity_reservation_scalar_update_guard BEFORE UPDATE ON charity_reservations
WHEN NEW.token_reserve_milli NOT BETWEEN 0 AND 2147483647
 OR NEW.user_reserved_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.original_charge_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.user_charge_milli NOT BETWEEN 0 AND 9000000000000000
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.dispatched_at IS NOT NULL AND NEW.dispatched_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.finalized_at IS NOT NULL AND NEW.finalized_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.updated_at NOT BETWEEN 0 AND 253402300799)
 OR NOT ((NEW.state='reserved' AND NEW.dispatched_at IS NULL AND NEW.finalized_at IS NULL)
      OR (NEW.state='dispatched' AND NEW.dispatched_at IS NOT NULL AND NEW.finalized_at IS NULL)
      OR (NEW.state IN ('committed','released') AND NEW.finalized_at IS NOT NULL))
BEGIN SELECT RAISE(ABORT,'charity reservation scalar/state invariant'); END;
CREATE TRIGGER charity_binding_endpoint_guard BEFORE INSERT ON charity_model_bindings
WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.endpoint_key_id=NEW.endpoint_key_id)
BEGIN SELECT RAISE(ABORT,'charity binding endpoint mismatch'); END;
 CREATE TRIGGER charity_binding_endpoint_update_guard BEFORE UPDATE OF donation_key_id,endpoint_key_id ON charity_model_bindings
 WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.endpoint_key_id=NEW.endpoint_key_id)
 BEGIN SELECT RAISE(ABORT,'charity binding endpoint mismatch'); END;
 CREATE TRIGGER donation_key_membership_consistency_guard BEFORE INSERT ON donation_key_memberships
 WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.donation_id=NEW.donation_id AND d.endpoint_key_id=NEW.endpoint_key_id)
 BEGIN SELECT RAISE(ABORT,'donation key membership identity mismatch'); END;
 CREATE TRIGGER donation_key_membership_consistency_update_guard BEFORE UPDATE OF endpoint_key_id,donation_key_id,donation_id ON donation_key_memberships
 WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.donation_id=NEW.donation_id AND d.endpoint_key_id=NEW.endpoint_key_id)
 BEGIN SELECT RAISE(ABORT,'donation key membership identity mismatch'); END;
CREATE TRIGGER rps_queue_account_guard BEFORE INSERT ON game_rps_queue
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-queue:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps queue account mismatch'); END;
CREATE TRIGGER rps_queue_account_update_guard BEFORE UPDATE OF id,account_id ON game_rps_queue
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-queue:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps queue account mismatch'); END;
CREATE TRIGGER rps_session_account_guard BEFORE INSERT ON game_rps_sessions
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-session:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps session account mismatch'); END;
CREATE TRIGGER rps_session_account_update_guard BEFORE UPDATE OF id,account_id ON game_rps_sessions
WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='platform' AND a.code='rps-session:'||NEW.id)
BEGIN SELECT RAISE(ABORT,'rps session account mismatch'); END;
CREATE TRIGGER thursday_period_pool_oid_guard BEFORE INSERT ON thursday_periods
WHEN NOT ((length(NEW.current_pool_id)=26 AND substr(NEW.current_pool_id,1,4)='pol_' AND substr(NEW.current_pool_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(NEW.current_pool_id,-1,1) IN ('A','Q','g','w'))
 AND (length(NEW.next_pool_id)=26 AND substr(NEW.next_pool_id,1,4)='pol_' AND substr(NEW.next_pool_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(NEW.next_pool_id,-1,1) IN ('A','Q','g','w')))
BEGIN SELECT RAISE(ABORT,'thursday pool reference is not canonical'); END;
 CREATE TRIGGER thursday_period_pool_oid_update_guard BEFORE UPDATE OF current_pool_id,next_pool_id ON thursday_periods
 WHEN NOT ((length(NEW.current_pool_id)=26 AND substr(NEW.current_pool_id,1,4)='pol_' AND substr(NEW.current_pool_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(NEW.current_pool_id,-1,1) IN ('A','Q','g','w'))
  AND (length(NEW.next_pool_id)=26 AND substr(NEW.next_pool_id,1,4)='pol_' AND substr(NEW.next_pool_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(NEW.next_pool_id,-1,1) IN ('A','Q','g','w')))
 BEGIN SELECT RAISE(ABORT,'thursday pool reference is not canonical'); END;
 CREATE TRIGGER shared_pool_account_guard BEFORE INSERT ON shared_pools
 WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='pool' AND a.code='pool:'||NEW.id)
 BEGIN SELECT RAISE(ABORT,'shared pool account identity mismatch'); END;
 CREATE TRIGGER shared_pool_account_update_guard BEFORE UPDATE OF id,account_id ON shared_pools
 WHEN NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind='pool' AND a.code='pool:'||NEW.id)
 BEGIN SELECT RAISE(ABORT,'shared pool account identity mismatch'); END;
 CREATE TRIGGER logical_request_destination_insert_guard BEFORE INSERT ON logical_requests
 WHEN NEW.settlement_destination='external'
 BEGIN SELECT RAISE(ABORT,'external settlement requires an in-flight user handoff'); END;
 CREATE TRIGGER logical_request_destination_update_guard BEFORE UPDATE OF user_id,settlement_destination ON logical_requests
 WHEN (OLD.settlement_destination='external' AND NEW.settlement_destination<>'external')
  OR (NEW.settlement_destination='external' AND NEW.user_id IS NOT NULL)
  OR (OLD.settlement_destination='user' AND NEW.settlement_destination='external' AND
      NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.logical_request_id=NEW.id AND c.dispatched_at IS NOT NULL))
 BEGIN SELECT RAISE(ABORT,'logical request settlement handoff is invalid'); END;
 CREATE TRIGGER dispatch_claim_attempt_limit_guard BEFORE INSERT ON dispatch_claims
 WHEN NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND NEW.attempt_seq<=r.attempt_limit)
 BEGIN SELECT RAISE(ABORT,'dispatch attempt exceeds request limit'); END;
 CREATE TRIGGER dispatch_claim_attempt_limit_update_guard BEFORE UPDATE OF logical_request_id,attempt_seq ON dispatch_claims
 WHEN NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND NEW.attempt_seq<=r.attempt_limit)
 BEGIN SELECT RAISE(ABORT,'dispatch attempt exceeds request limit'); END;
 CREATE TRIGGER request_log_logical_snapshot_guard BEFORE INSERT ON request_logs
 WHEN NOT EXISTS(
   SELECT 1 FROM logical_requests r
   WHERE r.id=NEW.logical_request_id AND NEW.user_id IS r.user_id AND NEW.route_kind IS r.route_kind
     AND NEW.caller_result_class IS r.caller_result_class AND NEW.caller_status IS r.caller_status
     AND NEW.caller_error_code IS r.caller_error_code
     AND NEW.status_code IS COALESCE(r.caller_status,0)
     AND NEW.error_code IS COALESCE(r.caller_error_code,'')
     AND NEW.completed_at IS r.terminal_at)
 BEGIN SELECT RAISE(ABORT,'request log caller snapshot mismatch'); END;
 CREATE TRIGGER request_log_logical_snapshot_update_guard BEFORE UPDATE OF logical_request_id,user_id,route_kind,caller_result_class,caller_status,caller_error_code,status_code,error_code,completed_at ON request_logs
 WHEN NOT EXISTS(
   SELECT 1 FROM logical_requests r
   WHERE r.id=NEW.logical_request_id AND NEW.user_id IS r.user_id AND NEW.route_kind IS r.route_kind
     AND NEW.caller_result_class IS r.caller_result_class AND NEW.caller_status IS r.caller_status
     AND NEW.caller_error_code IS r.caller_error_code
     AND NEW.status_code IS COALESCE(r.caller_status,0)
     AND NEW.error_code IS COALESCE(r.caller_error_code,'')
     AND NEW.completed_at IS r.terminal_at)
 BEGIN SELECT RAISE(ABORT,'request log caller snapshot mismatch'); END;
 CREATE TRIGGER logical_request_log_snapshot_sync AFTER UPDATE OF state,user_id,route_kind,caller_result_class,caller_status,caller_error_code,terminal_at ON logical_requests
 WHEN EXISTS(SELECT 1 FROM request_logs l WHERE l.logical_request_id=NEW.id)
 BEGIN
   UPDATE request_logs
   SET user_id=NEW.user_id,
       route_kind=NEW.route_kind,
       caller_result_class=NEW.caller_result_class,
       caller_status=NEW.caller_status,
       caller_error_code=NEW.caller_error_code,
       status_code=COALESCE(NEW.caller_status,0),
       error_code=COALESCE(NEW.caller_error_code,''),
       completed_at=NEW.terminal_at
   WHERE logical_request_id=NEW.id;
 END;
CREATE TRIGGER fishing_outcome_ordinal_guard BEFORE INSERT ON game_fishing_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND NEW.ordinal BETWEEN 0 AND b.count-1)
BEGIN SELECT RAISE(ABORT,'fishing outcome ordinal is outside batch'); END;
 CREATE TRIGGER fishing_outcome_ordinal_update_guard BEFORE UPDATE OF batch_id,ordinal ON game_fishing_outcomes
 WHEN OLD.batch_id IS NOT NEW.batch_id
  OR NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=OLD.batch_id AND b.state='reserved')
  OR NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND NEW.ordinal BETWEEN 0 AND b.count-1 AND b.state='reserved')
 BEGIN SELECT RAISE(ABORT,'fishing outcome ordinal or parent is invalid'); END;
CREATE TRIGGER fishing_best_snapshot_guard BEFORE INSERT ON game_fishing_best
WHEN NEW.batch_id IS NULL OR NOT EXISTS(
 SELECT 1 FROM game_fishing_outcomes o WHERE o.batch_id=NEW.batch_id AND o.ordinal=NEW.ordinal
 AND o.species_key=NEW.species_key AND o.tier=NEW.tier AND o.size_cm=NEW.size_cm)
BEGIN SELECT RAISE(ABORT,'fishing best snapshot mismatch'); END;
CREATE TRIGGER fishing_best_snapshot_update_guard BEFORE UPDATE OF batch_id,ordinal,species_key,tier,size_cm ON game_fishing_best
WHEN NOT (
 (NEW.batch_id IS NULL AND NEW.ordinal IS NULL AND OLD.batch_id IS NOT NULL AND OLD.ordinal IS NOT NULL
  AND NEW.species_key IS OLD.species_key AND NEW.tier IS OLD.tier AND NEW.size_cm IS OLD.size_cm
  AND NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o WHERE o.batch_id=OLD.batch_id AND o.ordinal=OLD.ordinal))
 OR (NEW.batch_id IS NOT NULL AND NEW.ordinal IS NOT NULL AND EXISTS(
  SELECT 1 FROM game_fishing_outcomes o WHERE o.batch_id=NEW.batch_id AND o.ordinal=NEW.ordinal
   AND o.species_key=NEW.species_key AND o.tier=NEW.tier AND o.size_cm=NEW.size_cm))
)
BEGIN SELECT RAISE(ABORT,'fishing best snapshot mismatch'); END;
CREATE TRIGGER fishing_batch_terminal_guard BEFORE UPDATE OF state,payout_total_milli ON game_fishing_batches
WHEN NEW.state IN ('committed','released') AND (
 (NEW.state='committed' AND (SELECT COUNT(*) FROM game_fishing_outcomes WHERE batch_id=NEW.id)<>NEW.count)
 OR (NEW.state='committed' AND (SELECT COALESCE(SUM(payout_milli),0) FROM game_fishing_outcomes WHERE batch_id=NEW.id)<>NEW.payout_total_milli)
 OR (NEW.state='released' AND (SELECT COUNT(*) FROM game_fishing_outcomes WHERE batch_id=NEW.id)<>0)
 OR NEW.settled_at IS NULL)
BEGIN SELECT RAISE(ABORT,'fishing terminal facts are incomplete'); END;
CREATE TRIGGER fishing_batch_terminal_insert_guard BEFORE INSERT ON game_fishing_batches
WHEN NEW.state<>'reserved'
 OR (NEW.state='reserved' AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001')
 OR (NEW.state IN ('committed','released') AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
 OR (NEW.state='committed' AND (SELECT COUNT(*) FROM game_fishing_outcomes WHERE batch_id=NEW.id)<>NEW.count)
BEGIN SELECT RAISE(ABORT,'fishing terminal headroom or facts are incomplete'); END;
CREATE TRIGGER fishing_batch_terminal_immutable_guard BEFORE UPDATE OF state,payout_total_milli,settled_at ON game_fishing_batches
WHEN OLD.state IN ('committed','released') AND (NEW.state IS NOT OLD.state OR NEW.payout_total_milli IS NOT OLD.payout_total_milli OR NEW.settled_at IS NOT OLD.settled_at)
BEGIN SELECT RAISE(ABORT,'fishing terminal facts are immutable'); END;
CREATE TRIGGER fishing_outcome_parent_state_guard BEFORE INSERT ON game_fishing_outcomes
WHEN EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND b.state<>'reserved')
BEGIN SELECT RAISE(ABORT,'fishing outcome parent is terminal'); END;
 CREATE TRIGGER fishing_outcome_parent_state_update_guard BEFORE UPDATE ON game_fishing_outcomes
 WHEN EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=OLD.batch_id AND b.state<>'reserved')
  OR EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND b.state<>'reserved')
 BEGIN SELECT RAISE(ABORT,'fishing outcome parent is terminal'); END;
CREATE TRIGGER fishing_outcome_parent_state_delete_guard BEFORE DELETE ON game_fishing_outcomes
WHEN EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=OLD.batch_id AND b.state<>'reserved')
BEGIN SELECT RAISE(ABORT,'fishing outcome parent is terminal'); END;
CREATE TRIGGER fishing_batch_headroom_update_guard BEFORE UPDATE OF state,ledger_rows_remaining ON game_fishing_batches
WHEN (NEW.state='reserved' AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001')
 OR (NEW.state IN ('committed','released') AND hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'fishing terminal headroom is invalid'); END;
CREATE TRIGGER fishing_outcome_payout_guard BEFORE INSERT ON game_fishing_outcomes
WHEN NEW.payout_milli NOT BETWEEN 0 AND 9000000000000000
BEGIN SELECT RAISE(ABORT,'fishing outcome payout is outside money range'); END;
CREATE TRIGGER fishing_outcome_payout_update_guard BEFORE UPDATE OF payout_milli ON game_fishing_outcomes
WHEN NEW.payout_milli NOT BETWEEN 0 AND 9000000000000000
BEGIN SELECT RAISE(ABORT,'fishing outcome payout is outside money range'); END;
CREATE TRIGGER linklink_scalar_guard BEFORE INSERT ON game_linklink_sessions
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR NEW.pairs_removed NOT BETWEEN 0 AND CASE NEW.spec WHEN '6x8' THEN 24 WHEN '8x8' THEN 32 WHEN '10x10' THEN 50 ELSE -1 END
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'LinkLink scalar is invalid'); END;
CREATE TRIGGER linklink_scalar_update_guard BEFORE UPDATE ON game_linklink_sessions
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR NEW.pairs_removed NOT BETWEEN 0 AND CASE NEW.spec WHEN '6x8' THEN 24 WHEN '8x8' THEN 32 WHEN '10x10' THEN 50 ELSE -1 END
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'LinkLink scalar is invalid'); END;
CREATE TRIGGER fishing_batch_time_guard BEFORE INSERT ON game_fishing_batches
WHEN NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.next_attempt_at IS NOT NULL AND NEW.next_attempt_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.settled_at IS NOT NULL AND NEW.settled_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.revealed_at IS NOT NULL AND NEW.revealed_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'fishing timestamp is outside UTC range'); END;
CREATE TRIGGER fishing_batch_time_update_guard BEFORE UPDATE OF created_at,next_attempt_at,settled_at,revealed_at ON game_fishing_batches
WHEN NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.next_attempt_at IS NOT NULL AND NEW.next_attempt_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.settled_at IS NOT NULL AND NEW.settled_at NOT BETWEEN 0 AND 253402300799)
 OR (NEW.revealed_at IS NOT NULL AND NEW.revealed_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'fishing timestamp is outside UTC range'); END;
CREATE TRIGGER rps_queue_scalar_guard BEFORE INSERT ON game_rps_queue
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR hex(NEW.reserved)='00000000000000000000000000000000'
 OR hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001'
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'rps queue scalar is invalid'); END;
CREATE TRIGGER rps_queue_scalar_update_guard BEFORE UPDATE OF revision,reserved,ledger_rows_remaining,deadline,created_at ON game_rps_queue
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR hex(NEW.reserved)='00000000000000000000000000000000'
 OR hex(NEW.ledger_rows_remaining)<>'00000000000000000000000000000001'
 OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'rps queue scalar is invalid'); END;
CREATE TRIGGER rps_session_scalar_guard BEFORE INSERT ON game_rps_sessions
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR hex(NEW.phase_seq)='00000000000000000000000000000000'
 OR hex(NEW.identity_epoch)='00000000000000000000000000000000'
 OR NEW.base_milli NOT BETWEEN 1 AND 9000000000000000
 OR (NEW.mode='standard' AND NEW.base_milli>1800000000000000)
 OR NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.phase_deadline IS NOT NULL AND NEW.phase_deadline NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'rps session scalar is invalid'); END;
CREATE TRIGGER rps_session_scalar_update_guard BEFORE UPDATE OF revision,phase_seq,identity_epoch,started_at,phase_deadline,base_milli,platform_bp,welfare_bp,thursday_bp ON game_rps_sessions
WHEN hex(NEW.revision)='00000000000000000000000000000000'
 OR hex(NEW.phase_seq)='00000000000000000000000000000000'
 OR hex(NEW.identity_epoch)='00000000000000000000000000000000'
 OR NEW.base_milli NOT BETWEEN 1 AND 9000000000000000
 OR (NEW.mode='standard' AND NEW.base_milli>1800000000000000)
 OR NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.phase_deadline IS NOT NULL AND NEW.phase_deadline NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'rps session scalar is invalid'); END;
CREATE TRIGGER rps_seat_phase_seq_guard BEFORE INSERT ON game_rps_seats
WHEN (NEW.current_gesture_phase_seq IS NOT NULL AND hex(NEW.current_gesture_phase_seq)='00000000000000000000000000000000')
  OR (NEW.last_action_phase_seq IS NOT NULL AND hex(NEW.last_action_phase_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'rps seat phase sequence is invalid'); END;
CREATE TRIGGER rps_seat_phase_seq_update_guard BEFORE UPDATE OF current_gesture_phase_seq,last_action_phase_seq ON game_rps_seats
WHEN (NEW.current_gesture_phase_seq IS NOT NULL AND hex(NEW.current_gesture_phase_seq)='00000000000000000000000000000000')
  OR (NEW.last_action_phase_seq IS NOT NULL AND hex(NEW.last_action_phase_seq)='00000000000000000000000000000000')
BEGIN SELECT RAISE(ABORT,'rps seat phase sequence is invalid'); END;
CREATE TRIGGER rps_seat_follower_action_guard BEFORE INSERT ON game_rps_seats
WHEN NEW.follower_action IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM game_rps_sessions s
 WHERE s.id=NEW.session_id AND s.phase='followers' AND s.dealer_seat IS NOT NEW.seat_no
   AND NEW.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_phase_seq<s.phase_seq
   AND NEW.last_action_phase_seq IS s.phase_seq)
BEGIN SELECT RAISE(ABORT,'rps follower action is invalid for this seat'); END;
CREATE TRIGGER rps_seat_follower_action_update_guard BEFORE UPDATE OF current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq,session_id,seat_no ON game_rps_seats
WHEN OLD.follower_action IS NOT NULL
 AND (NEW.follower_action IS NOT OLD.follower_action OR NEW.last_action_phase_seq IS NOT OLD.last_action_phase_seq)
 AND NOT (
   NEW.current_gesture_envelope IS NULL AND NEW.current_gesture_phase_seq IS NULL AND
   NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL AND
   EXISTS(SELECT 1 FROM game_rps_sessions s
          WHERE s.id=NEW.session_id
            AND s.phase IN ('gesture','paid_pool_gesture','free_pool_gesture','ultimate_gesture','terminal_processing')
            AND s.phase_seq>OLD.current_gesture_phase_seq)
 )
BEGIN SELECT RAISE(ABORT,'rps follower action is invalid for this seat'); END;
CREATE TRIGGER rps_seat_action_phase_matrix_guard BEFORE INSERT ON game_rps_seats
WHEN NOT EXISTS(
 SELECT 1 FROM game_rps_sessions s
 WHERE s.id=NEW.session_id AND (
   (s.phase IN ('gesture','paid_pool_gesture','free_pool_gesture','ultimate_gesture') AND
    ((NEW.current_gesture_envelope IS NULL AND NEW.current_gesture_phase_seq IS NULL AND
      NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
     (NEW.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_phase_seq IS s.phase_seq AND
      NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS s.phase_seq))) OR
   (s.phase='dealer_raise' AND NEW.current_gesture_envelope IS NOT NULL AND
    NEW.current_gesture_phase_seq<s.phase_seq AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
   (s.phase='followers' AND NEW.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_phase_seq<s.phase_seq AND
    ((s.dealer_seat IS NEW.seat_no AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
     (s.dealer_seat IS NOT NEW.seat_no AND
      ((NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
       (NEW.follower_action IS NOT NULL AND NEW.last_action_phase_seq IS s.phase_seq))))) OR
   (s.phase='terminal_processing' AND NEW.current_gesture_envelope IS NULL AND
    NEW.current_gesture_phase_seq IS NULL AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL)
 ))
BEGIN SELECT RAISE(ABORT,'rps seat action phase matrix is invalid'); END;
CREATE TRIGGER rps_seat_action_phase_matrix_update_guard BEFORE UPDATE ON game_rps_seats
WHEN NOT EXISTS(
 SELECT 1 FROM game_rps_sessions s
 WHERE s.id=NEW.session_id AND (
   (s.phase IN ('gesture','paid_pool_gesture','free_pool_gesture','ultimate_gesture') AND
    ((NEW.current_gesture_envelope IS NULL AND NEW.current_gesture_phase_seq IS NULL AND
      NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
     (NEW.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_phase_seq IS s.phase_seq AND
      NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS s.phase_seq))) OR
   (s.phase='dealer_raise' AND NEW.current_gesture_envelope IS NOT NULL AND
    NEW.current_gesture_phase_seq<s.phase_seq AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
   (s.phase='followers' AND NEW.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_phase_seq<s.phase_seq AND
    ((s.dealer_seat IS NEW.seat_no AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
     (s.dealer_seat IS NOT NEW.seat_no AND
      ((NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL) OR
       (NEW.follower_action IS NOT NULL AND NEW.last_action_phase_seq IS s.phase_seq))))) OR
   (s.phase='terminal_processing' AND NEW.current_gesture_envelope IS NULL AND
    NEW.current_gesture_phase_seq IS NULL AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL)
 ))
BEGIN SELECT RAISE(ABORT,'rps seat action phase matrix is invalid'); END;
CREATE TRIGGER rps_gesture_envelope_guard BEFORE INSERT ON game_rps_seats
WHEN NEW.current_gesture_envelope IS NOT NULL AND
     (typeof(NEW.current_gesture_envelope)<>'blob' OR length(NEW.current_gesture_envelope) NOT IN (33,34,37) OR
      substr(NEW.current_gesture_envelope,1,1) IS NOT X'01')
BEGIN SELECT RAISE(ABORT,'rps gesture envelope is invalid'); END;
CREATE TRIGGER rps_gesture_envelope_update_guard BEFORE UPDATE OF current_gesture_envelope,current_gesture_phase_seq ON game_rps_seats
WHEN (NEW.current_gesture_envelope IS NOT NULL AND
      (typeof(NEW.current_gesture_envelope)<>'blob' OR length(NEW.current_gesture_envelope) NOT IN (33,34,37) OR substr(NEW.current_gesture_envelope,1,1) IS NOT X'01'))
  OR (OLD.current_gesture_envelope IS NULL AND NEW.current_gesture_envelope IS NOT NULL AND
      NOT EXISTS(SELECT 1 FROM game_rps_sessions s
                 WHERE s.id=NEW.session_id
                   AND s.phase IN ('gesture','paid_pool_gesture','free_pool_gesture','ultimate_gesture')
                   AND NEW.current_gesture_phase_seq IS s.phase_seq
                   AND NEW.last_action_phase_seq IS s.phase_seq
                   AND NEW.follower_action IS NULL))
  OR (OLD.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_envelope IS NOT NULL AND
      (NEW.current_gesture_envelope IS NOT OLD.current_gesture_envelope OR
       NEW.current_gesture_phase_seq IS NOT OLD.current_gesture_phase_seq))
  OR (OLD.current_gesture_envelope IS NOT NULL AND NEW.current_gesture_envelope IS NULL AND NOT (
      NEW.current_gesture_phase_seq IS NULL AND NEW.follower_action IS NULL AND NEW.last_action_phase_seq IS NULL AND
      EXISTS(SELECT 1 FROM game_rps_sessions s
             WHERE s.id=NEW.session_id
               AND s.phase IN ('gesture','paid_pool_gesture','free_pool_gesture','ultimate_gesture','terminal_processing')
               AND s.phase_seq>OLD.current_gesture_phase_seq)
  ))
BEGIN SELECT RAISE(ABORT,'rps gesture envelope is invalid'); END;
CREATE TRIGGER rps_seat_terminal_matrix_guard BEFORE INSERT ON game_rps_seats
WHEN (NEW.terminal_return IS NOT NULL OR NEW.wallet_net_sign IS NOT NULL OR NEW.wallet_net_mag IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND s.state='terminal_processing')
BEGIN SELECT RAISE(ABORT,'rps terminal seat data requires terminal processing'); END;
CREATE TRIGGER rps_seat_terminal_matrix_update_guard BEFORE UPDATE OF session_id,terminal_return,wallet_net_sign,wallet_net_mag ON game_rps_seats
WHEN (NEW.terminal_return IS NOT NULL OR NEW.wallet_net_sign IS NOT NULL OR NEW.wallet_net_mag IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND s.state='terminal_processing')
BEGIN SELECT RAISE(ABORT,'rps terminal seat data requires terminal processing'); END;
CREATE TRIGGER rps_terminal_fact_immutable BEFORE UPDATE OF terminal_return,wallet_net_sign,wallet_net_mag ON game_rps_seats
WHEN OLD.terminal_return IS NOT NULL AND
     (NEW.terminal_return IS NOT OLD.terminal_return OR NEW.wallet_net_sign IS NOT OLD.wallet_net_sign OR NEW.wallet_net_mag IS NOT OLD.wallet_net_mag)
BEGIN SELECT RAISE(ABORT,'rps terminal seat facts are immutable'); END;
CREATE TRIGGER rps_pending_result_reason_guard BEFORE INSERT ON game_rps_pending_results
WHEN NEW.terminal_reason NOT IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')
BEGIN SELECT RAISE(ABORT,'rps pending result reason is invalid'); END;
CREATE TRIGGER rps_pending_result_reason_update_guard BEFORE UPDATE OF terminal_reason ON game_rps_pending_results
WHEN NEW.terminal_reason NOT IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')
BEGIN SELECT RAISE(ABORT,'rps pending result reason is invalid'); END;
CREATE TRIGGER rps_pending_result_outcome_guard BEFORE INSERT ON game_rps_pending_results
WHEN (NEW.own_seat_no=0 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat0_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat0_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat0_result<>'tie'))) OR
     (NEW.own_seat_no=1 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat1_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat1_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat1_result<>'tie'))) OR
     (NEW.own_seat_no=2 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat2_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat2_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat2_result<>'tie')))
BEGIN SELECT RAISE(ABORT,'rps pending result outcome is inconsistent'); END;
CREATE TRIGGER rps_pending_result_outcome_update_guard BEFORE UPDATE OF own_seat_no,own_wallet_net_sign,own_wallet_net_mag,seat0_result,seat1_result,seat2_result ON game_rps_pending_results
WHEN (NEW.own_seat_no=0 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat0_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat0_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat0_result<>'tie'))) OR
     (NEW.own_seat_no=1 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat1_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat1_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat1_result<>'tie'))) OR
     (NEW.own_seat_no=2 AND ((NEW.own_wallet_net_sign=1 AND NEW.seat2_result<>'win') OR
                             (NEW.own_wallet_net_sign=-1 AND NEW.seat2_result<>'loss') OR
                             (NEW.own_wallet_net_sign=0 AND NEW.seat2_result<>'tie')))
BEGIN SELECT RAISE(ABORT,'rps pending result outcome is inconsistent'); END;
CREATE TRIGGER rps_summary_retention_guard BEFORE INSERT ON game_rps_summaries
WHEN NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
 OR NEW.terminal_at<NEW.started_at
 OR NEW.delete_at<>NEW.terminal_at+2592000
BEGIN SELECT RAISE(ABORT,'rps summary retention is invalid'); END;
CREATE TRIGGER rps_summary_retention_update_guard BEFORE UPDATE OF started_at,terminal_at,delete_at ON game_rps_summaries
WHEN NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
 OR NEW.terminal_at<NEW.started_at
 OR NEW.delete_at<>NEW.terminal_at+2592000
BEGIN SELECT RAISE(ABORT,'rps summary retention is invalid'); END;
CREATE TRIGGER game_lease_time_guard BEFORE INSERT ON game_online_leases
WHEN NEW.expires_at NOT BETWEEN 0 AND 253402300799
 OR NEW.last_renewed_at NOT BETWEEN 0 AND 253402300799
 OR NEW.expires_at<NEW.last_renewed_at
BEGIN SELECT RAISE(ABORT,'game lease time is invalid'); END;
CREATE TRIGGER game_lease_time_update_guard BEFORE UPDATE OF expires_at,last_renewed_at ON game_online_leases
WHEN NEW.expires_at NOT BETWEEN 0 AND 253402300799
 OR NEW.last_renewed_at NOT BETWEEN 0 AND 253402300799
 OR NEW.expires_at<NEW.last_renewed_at
BEGIN SELECT RAISE(ABORT,'game lease time is invalid'); END;
CREATE TRIGGER users_admin_immutable BEFORE UPDATE OF is_admin ON users WHEN OLD.is_admin<>NEW.is_admin BEGIN SELECT RAISE(ABORT,'is_admin is immutable'); END;
CREATE TRIGGER users_admin_delete_guard BEFORE DELETE ON users WHEN OLD.is_admin=1 BEGIN SELECT RAISE(ABORT,'environment admin cannot be deleted'); END;
CREATE TRIGGER users_request_handoff_delete_guard BEFORE DELETE ON users
WHEN EXISTS(
 SELECT 1 FROM logical_requests r
 WHERE r.user_id=OLD.id AND r.settlement_destination='user'
   AND (r.accounting_state='reserved' OR EXISTS(
    SELECT 1 FROM dispatch_claims c
    WHERE c.logical_request_id=r.id AND c.state IN ('claimed','dispatched')
   ))
)
BEGIN SELECT RAISE(ABORT,'user request settlement is not handed off'); END;
CREATE TRIGGER users_fishing_reservation_delete_guard BEFORE DELETE ON users
WHEN EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.user_id=OLD.id AND b.state='reserved')
BEGIN SELECT RAISE(ABORT,'user fishing reservation is not released'); END;
CREATE TRIGGER users_rps_queue_delete_guard BEFORE DELETE ON users
WHEN EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.user_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'user rps queue reservation is not released'); END;
CREATE TRIGGER users_rps_session_delete_guard BEFORE DELETE ON users
WHEN EXISTS(SELECT 1 FROM game_rps_seats s WHERE s.user_id=OLD.id AND s.deletion_state='active')
BEGIN SELECT RAISE(ABORT,'user rps session is not handed off'); END;
CREATE TRIGGER users_maintenance_actor_deidentify BEFORE DELETE ON users
BEGIN
 INSERT INTO user_deletion_markers(user_id) VALUES(OLD.id);
 UPDATE maintenance_events SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL WHERE actor_user_id=OLD.id;
 DELETE FROM user_deletion_markers WHERE user_id=OLD.id;
END;
CREATE TRIGGER endpoint_key_secrets_identity_immutable BEFORE UPDATE ON endpoint_key_secrets WHEN OLD.context_id<>NEW.context_id OR OLD.canonical_base_url<>NEW.canonical_base_url OR OLD.connector_type<>NEW.connector_type OR OLD.encrypted_secret<>NEW.encrypted_secret OR OLD.created_at<>NEW.created_at BEGIN SELECT RAISE(ABORT,'endpoint key secret identity is immutable'); END;
CREATE TRIGGER endpoint_key_secrets_orphan_marker_guard BEFORE UPDATE OF orphaned_at ON endpoint_key_secrets
WHEN (OLD.orphaned_at IS NOT NULL AND NEW.orphaned_at IS NOT OLD.orphaned_at)
 OR (OLD.orphaned_at IS NULL AND NEW.orphaned_at IS NOT NULL AND
     (EXISTS(SELECT 1 FROM endpoint_keys WHERE secret_ref_id=OLD.id)
      OR EXISTS(SELECT 1 FROM dispatch_claims WHERE secret_ref_id=OLD.id AND state IN ('claimed','dispatched'))))
BEGIN SELECT RAISE(ABORT,'endpoint key secret orphan marker is invalid'); END;
CREATE TRIGGER endpoint_key_secrets_orphan_delete_guard BEFORE DELETE ON endpoint_key_secrets
WHEN OLD.orphaned_at IS NULL
 OR EXISTS(SELECT 1 FROM endpoint_keys WHERE secret_ref_id=OLD.id)
 OR EXISTS(SELECT 1 FROM dispatch_claims WHERE secret_ref_id=OLD.id AND state IN ('claimed','dispatched'))
BEGIN SELECT RAISE(ABORT,'endpoint key secret is still referenced'); END;
CREATE TRIGGER caller_keys_generation_guard BEFORE UPDATE ON caller_keys
WHEN (NEW.generation<OLD.generation OR NEW.generation>OLD.generation+1)
 OR (NEW.generation=OLD.generation+1 AND NEW.key_hash IS OLD.key_hash)
 OR (NEW.generation=OLD.generation AND (NEW.key_hash IS NOT OLD.key_hash OR NEW.display_head IS NOT OLD.display_head OR NEW.display_tail IS NOT OLD.display_tail OR NEW.key_created_at IS NOT OLD.key_created_at))
BEGIN SELECT RAISE(ABORT,'caller key generation replacement is not atomic'); END;
 CREATE TRIGGER announcements_publish_guard BEFORE UPDATE ON announcements
 WHEN (NEW.state='published' AND
       (OLD.state<>'published' OR NEW.published_revision IS NOT OLD.published_revision) AND
       NOT (NEW.published_revision=NEW.revision AND NEW.published_at IS NOT NULL AND
            NEW.published_title_zh IS NEW.draft_title_zh AND NEW.published_body_zh IS NEW.draft_body_zh AND
            NEW.published_title_en IS NEW.draft_title_en AND NEW.published_body_en IS NEW.draft_body_en))
  OR (NOT (NEW.state='published' AND
           (OLD.state<>'published' OR NEW.published_revision IS NOT OLD.published_revision)) AND
      (NEW.published_revision IS NOT OLD.published_revision OR NEW.published_at IS NOT OLD.published_at OR
       NEW.published_title_zh IS NOT OLD.published_title_zh OR NEW.published_body_zh IS NOT OLD.published_body_zh OR
       NEW.published_title_en IS NOT OLD.published_title_en OR NEW.published_body_en IS NOT OLD.published_body_en))
 BEGIN SELECT RAISE(ABORT,'announcement publication is incomplete'); END;
 CREATE TRIGGER announcements_publish_insert_guard BEFORE INSERT ON announcements
 WHEN (NEW.state='published' AND NOT (NEW.published_revision=NEW.revision AND NEW.published_at IS NOT NULL AND
     NEW.published_title_zh IS NEW.draft_title_zh AND NEW.published_body_zh IS NEW.draft_body_zh AND
     NEW.published_title_en IS NEW.draft_title_en AND NEW.published_body_en IS NEW.draft_body_en))
  OR (NEW.state<>'published' AND (NEW.published_revision IS NOT NULL OR NEW.published_at IS NOT NULL OR
      NEW.published_title_zh IS NOT NULL OR NEW.published_body_zh IS NOT NULL OR
      NEW.published_title_en IS NOT NULL OR NEW.published_body_en IS NOT NULL))
 BEGIN SELECT RAISE(ABORT,'announcement publication is incomplete'); END;
CREATE TRIGGER legal_hold_insert_guard BEFORE INSERT ON legal_holds
WHEN NEW.state='active' AND NOT (
 (NEW.object_kind='maintenance_event' AND EXISTS(SELECT 1 FROM maintenance_events WHERE id=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='report_case' AND EXISTS(SELECT 1 FROM report_cases WHERE id=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='announcement_audit' AND EXISTS(SELECT 1 FROM announcement_audits WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='donation' AND EXISTS(SELECT 1 FROM donations WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)) OR
 (NEW.object_kind='request_log' AND EXISTS(SELECT 1 FROM request_logs WHERE CAST(id AS TEXT)=NEW.object_ref AND legal_hold_consumed=0)))
BEGIN SELECT RAISE(ABORT,'legal hold object does not exist'); END;
CREATE TRIGGER legal_hold_insert_state_guard BEFORE INSERT ON legal_holds
WHEN NEW.state<>'active'
BEGIN SELECT RAISE(ABORT,'legal hold must be created active'); END;
CREATE TRIGGER legal_hold_update_guard BEFORE UPDATE ON legal_holds
WHEN NEW.id IS NOT OLD.id OR NEW.object_kind IS NOT OLD.object_kind OR NEW.object_ref IS NOT OLD.object_ref OR
     NEW.basis IS NOT OLD.basis OR NEW.created_by_user_id IS NOT OLD.created_by_user_id OR
     NEW.created_at IS NOT OLD.created_at OR NEW.expires_at IS NOT OLD.expires_at OR
     (OLD.state='active' AND NOT (
        (NEW.state='active' AND NEW.revision=OLD.revision AND NEW.ended_by_user_id IS OLD.ended_by_user_id AND
         NEW.ended_at IS OLD.ended_at AND NEW.end_reason IS OLD.end_reason AND NEW.retain_until IS OLD.retain_until) OR
        (NEW.state='released' AND NEW.revision=OLD.revision+1 AND NEW.ended_by_user_id IS NOT NULL AND
         NEW.ended_at IS NOT NULL AND NEW.ended_at<NEW.expires_at AND NEW.end_reason IS NOT NULL AND
         NEW.retain_until=NEW.ended_at+34560000) OR
        (NEW.state='expired' AND NEW.revision=OLD.revision+1 AND NEW.ended_by_user_id IS NULL AND
         NEW.ended_at=NEW.expires_at AND NEW.end_reason='expired' AND NEW.retain_until=NEW.ended_at+34560000)
     )) OR
     (OLD.state<>'active' AND (NEW.state IS NOT OLD.state OR NEW.revision IS NOT OLD.revision OR
         NEW.ended_by_user_id IS NOT OLD.ended_by_user_id OR NEW.ended_at IS NOT OLD.ended_at OR
         NEW.end_reason IS NOT OLD.end_reason OR NEW.retain_until IS NOT OLD.retain_until))
BEGIN SELECT RAISE(ABORT,'legal hold transition is immutable or invalid'); END;
CREATE TRIGGER legal_hold_marker_insert_maintenance BEFORE INSERT ON maintenance_events
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER legal_hold_marker_insert_announcement BEFORE INSERT ON announcement_audits
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER legal_hold_marker_insert_report BEFORE INSERT ON report_cases
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER legal_hold_marker_insert_donation BEFORE INSERT ON donations
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER legal_hold_marker_insert_log BEFORE INSERT ON request_logs
WHEN NEW.legal_hold_consumed<>0
BEGIN SELECT RAISE(ABORT,'legal hold marker must start clear'); END;
CREATE TRIGGER legal_hold_admin_actor_guard BEFORE INSERT ON legal_holds
WHEN NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.created_by_user_id AND is_admin=1)
BEGIN SELECT RAISE(ABORT,'legal hold actor is not the active admin'); END;
CREATE TRIGGER legal_hold_release_actor_guard BEFORE UPDATE OF ended_by_user_id ON legal_holds
WHEN NEW.ended_by_user_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.ended_by_user_id AND is_admin=1)
BEGIN SELECT RAISE(ABORT,'legal hold end actor is not the active admin'); END;
CREATE TRIGGER legal_hold_audit_guard BEFORE INSERT ON legal_hold_audits
WHEN NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text)
 OR (NEW.action IN ('create','release') AND NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.actor_user_id AND u.is_admin=1))
 OR (NEW.action='expire' AND (NEW.actor_user_id IS NOT NULL OR NEW.reason<>'expired'))
 OR (NEW.action='create' AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND (h.state<>'active' OR NEW.retain_until IS NOT NULL)))
 OR (NEW.action IN ('release','expire') AND NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state IN ('released','expired') AND NEW.retain_until=h.retain_until))
BEGIN SELECT RAISE(ABORT,'legal hold audit is inconsistent'); END;
CREATE TRIGGER legal_hold_read_audit_guard BEFORE INSERT ON legal_hold_read_audits
WHEN NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text)
 OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.admin_user_id AND u.is_admin=1)
 OR (NEW.retain_until IS NOT NULL AND NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state IN ('released','expired') AND NEW.retain_until=h.retain_until))
BEGIN SELECT RAISE(ABORT,'legal hold read audit is inconsistent'); END;
CREATE TRIGGER legal_hold_read_audit_update_guard BEFORE UPDATE ON legal_hold_read_audits
WHEN NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text)
 OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.admin_user_id AND u.is_admin=1)
 OR typeof(NEW.first_read_at)<>'integer' OR NEW.first_read_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.last_read_at)<>'integer' OR NEW.last_read_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.read_count)<>'integer' OR NEW.read_count<1
 OR (NOT (
       (NEW.hold_id_text IS OLD.hold_id_text AND NEW.admin_user_id IS OLD.admin_user_id AND NEW.read_kind IS OLD.read_kind AND
        OLD.retain_until IS NULL AND NEW.first_read_at=OLD.first_read_at AND NEW.last_read_at>=OLD.last_read_at AND NEW.read_count=OLD.read_count+1 AND
        NEW.retain_until IS OLD.retain_until) OR
       (NEW.hold_id_text IS OLD.hold_id_text AND NEW.admin_user_id IS OLD.admin_user_id AND NEW.read_kind IS OLD.read_kind AND
        NEW.first_read_at=OLD.first_read_at AND NEW.last_read_at=OLD.last_read_at AND NEW.read_count=OLD.read_count AND
        OLD.retain_until IS NULL AND NEW.retain_until IS NOT NULL AND
        EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state IN ('released','expired') AND
               NEW.retain_until=h.retain_until AND NEW.retain_until=h.ended_at+34560000))
     ))
BEGIN SELECT RAISE(ABORT,'legal hold read audit is inconsistent'); END;
 CREATE TRIGGER legal_hold_audits_no_update BEFORE UPDATE ON legal_hold_audits
 WHEN NOT (
   NEW.id=OLD.id AND NEW.hold_id_text IS OLD.hold_id_text AND NEW.actor_user_id IS OLD.actor_user_id AND
   NEW.action IS OLD.action AND NEW.reason IS OLD.reason AND NEW.created_at=OLD.created_at AND
   OLD.retain_until IS NULL AND NEW.retain_until=(SELECT h.retain_until FROM legal_holds h WHERE h.id=OLD.hold_id_text)
   AND NEW.retain_until IS NOT NULL AND NEW.retain_until=(SELECT h.ended_at+34560000 FROM legal_holds h WHERE h.id=OLD.hold_id_text AND h.state IN ('released','expired'))
 )
 BEGIN SELECT RAISE(ABORT,'legal hold audit update is not an allowed retention mutation'); END;
CREATE TRIGGER legal_hold_active_delete_guard BEFORE DELETE ON legal_holds
WHEN OLD.state='active'
BEGIN SELECT RAISE(ABORT,'active legal hold cannot be deleted'); END;
CREATE TRIGGER legal_hold_mark_maintenance BEFORE UPDATE OF legal_hold_consumed ON maintenance_events
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='maintenance_event' AND object_ref=OLD.id AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_mark_announcement BEFORE UPDATE OF legal_hold_consumed ON announcement_audits
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='announcement_audit' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_mark_report BEFORE UPDATE OF legal_hold_consumed ON report_cases
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='report_case' AND object_ref=OLD.id AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_mark_donation BEFORE UPDATE OF legal_hold_consumed ON donations
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='donation' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_mark_log BEFORE UPDATE OF legal_hold_consumed ON request_logs
WHEN OLD.legal_hold_consumed=0 AND NEW.legal_hold_consumed=1 AND NOT EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='request_log' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'active legal hold is required'); END;
CREATE TRIGGER legal_hold_delete_maintenance BEFORE DELETE ON maintenance_events
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='maintenance_event' AND object_ref=OLD.id AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_announcement BEFORE DELETE ON announcement_audits
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='announcement_audit' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_report BEFORE DELETE ON report_cases
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='report_case' AND object_ref=OLD.id AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_donation BEFORE DELETE ON donations
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='donation' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_log BEFORE DELETE ON request_logs
WHEN EXISTS(SELECT 1 FROM legal_holds WHERE object_kind='request_log' AND object_ref=CAST(OLD.id AS TEXT) AND state='active')
BEGIN SELECT RAISE(ABORT,'legal-held object cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_report_material BEFORE DELETE ON report_materials
WHEN EXISTS(SELECT 1 FROM report_cases c WHERE c.id=OLD.case_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='report_case' AND h.object_ref=c.id AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_report_target BEFORE DELETE ON report_targets
WHEN EXISTS(SELECT 1 FROM report_cases c WHERE c.id=OLD.case_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='report_case' AND h.object_ref=c.id AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_report_decision BEFORE DELETE ON report_decisions
WHEN EXISTS(SELECT 1 FROM report_cases c WHERE c.id=OLD.case_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='report_case' AND h.object_ref=c.id AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_donation_review BEFORE DELETE ON donation_reviews
WHEN EXISTS(SELECT 1 FROM donations d WHERE d.id=OLD.donation_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='donation' AND h.object_ref=CAST(d.id AS TEXT) AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_donation_key BEFORE DELETE ON donation_keys
WHEN EXISTS(SELECT 1 FROM donations d WHERE d.id=OLD.donation_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='donation' AND h.object_ref=CAST(d.id AS TEXT) AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_delete_request_attempt BEFORE DELETE ON request_attempts
WHEN EXISTS(SELECT 1 FROM request_logs l WHERE l.id=OLD.request_log_id AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='request_log' AND h.object_ref=CAST(l.id AS TEXT) AND h.state='active'))
BEGIN SELECT RAISE(ABORT,'legal-held child cannot be deleted'); END;
CREATE TRIGGER legal_hold_consumed_guard BEFORE UPDATE OF legal_hold_consumed ON maintenance_events WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;
CREATE TRIGGER legal_hold_consumed_guard_announcement BEFORE UPDATE OF legal_hold_consumed ON announcement_audits WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;
CREATE TRIGGER legal_hold_consumed_guard_report BEFORE UPDATE OF legal_hold_consumed ON report_cases WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;
CREATE TRIGGER legal_hold_consumed_guard_donation BEFORE UPDATE OF legal_hold_consumed ON donations WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;
CREATE TRIGGER legal_hold_consumed_guard_log BEFORE UPDATE OF legal_hold_consumed ON request_logs WHEN OLD.legal_hold_consumed=1 AND NEW.legal_hold_consumed<>1 BEGIN SELECT RAISE(ABORT,'legal hold marker cannot be cleared'); END;

-- Every persisted timestamp is a signed Unix second in the frozen UTC range.
-- These narrow table guards cover columns whose state matrices intentionally
-- allow NULL; INTEGER affinity alone is not a sufficient boundary because
-- SQLite accepts out-of-range text/numeric values in an INTEGER column.
CREATE TRIGGER generation_two_users_time_guard BEFORE INSERT ON users
WHEN (NEW.banned_until IS NOT NULL AND (typeof(NEW.banned_until)<>'integer' OR NEW.banned_until NOT BETWEEN 0 AND 253402300799))
 OR (NEW.charity_suspended_until IS NOT NULL AND (typeof(NEW.charity_suspended_until)<>'integer' OR NEW.charity_suspended_until NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'users timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_users_time_update_guard BEFORE UPDATE ON users
WHEN (NEW.banned_until IS NOT NULL AND (typeof(NEW.banned_until)<>'integer' OR NEW.banned_until NOT BETWEEN 0 AND 253402300799))
 OR (NEW.charity_suspended_until IS NOT NULL AND (typeof(NEW.charity_suspended_until)<>'integer' OR NEW.charity_suspended_until NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'users timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_sessions_time_guard BEFORE INSERT ON sessions
WHEN typeof(NEW.last_seen_at)<>'integer' OR NEW.last_seen_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.absolute_expires_at)<>'integer' OR NEW.absolute_expires_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'session timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_sessions_time_update_guard BEFORE UPDATE ON sessions
WHEN typeof(NEW.last_seen_at)<>'integer' OR NEW.last_seen_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.absolute_expires_at)<>'integer' OR NEW.absolute_expires_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'session timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_policy_audits_time_guard BEFORE INSERT ON policy_audits
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'policy audit timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_policy_audits_time_update_guard BEFORE UPDATE ON policy_audits
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'policy audit timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_admin_alerts_time_guard BEFORE INSERT ON admin_alerts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.resolved_at IS NOT NULL AND (typeof(NEW.resolved_at)<>'integer' OR NEW.resolved_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'admin alert timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_admin_alerts_time_update_guard BEFORE UPDATE ON admin_alerts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.resolved_at IS NOT NULL AND (typeof(NEW.resolved_at)<>'integer' OR NEW.resolved_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'admin alert timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_user_activity_time_guard BEFORE INSERT ON user_activity_daily
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'user activity timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_user_activity_time_update_guard BEFORE UPDATE ON user_activity_daily
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'user activity timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_activity_time_guard BEFORE INSERT ON site_activity_daily
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site activity timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_activity_time_update_guard BEFORE UPDATE ON site_activity_daily
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site activity timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_usage_time_guard BEFORE INSERT ON site_usage_totals
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site usage timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_usage_time_update_guard BEFORE UPDATE ON site_usage_totals
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site usage timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_config_revision_time_guard BEFORE INSERT ON config_revisions
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'config revision timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_config_revision_time_update_guard BEFORE UPDATE ON config_revisions
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'config revision timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_caller_keys_time_guard BEFORE INSERT ON caller_keys
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.key_created_at IS NOT NULL AND (typeof(NEW.key_created_at)<>'integer' OR NEW.key_created_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'caller key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_caller_keys_time_update_guard BEFORE UPDATE ON caller_keys
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.key_created_at IS NOT NULL AND (typeof(NEW.key_created_at)<>'integer' OR NEW.key_created_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'caller key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_checkins_time_guard BEFORE INSERT ON checkins
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'check-in timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_checkins_time_update_guard BEFORE UPDATE ON checkins
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'check-in timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoints_time_guard BEFORE INSERT ON endpoints
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoints_time_update_guard BEFORE UPDATE ON endpoints
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_secrets_time_guard BEFORE INSERT ON endpoint_key_secrets
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.orphaned_at IS NOT NULL AND (typeof(NEW.orphaned_at)<>'integer' OR NEW.orphaned_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'endpoint secret timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_secrets_time_update_guard BEFORE UPDATE ON endpoint_key_secrets
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.orphaned_at IS NOT NULL AND (typeof(NEW.orphaned_at)<>'integer' OR NEW.orphaned_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'endpoint secret timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_keys_time_guard BEFORE INSERT ON endpoint_keys
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_keys_time_update_guard BEFORE UPDATE ON endpoint_keys
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_suspensions_time_guard BEFORE INSERT ON endpoint_key_suspensions
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint suspension timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_endpoint_suspensions_time_update_guard BEFORE UPDATE ON endpoint_key_suspensions
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'endpoint suspension timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_discovery_time_guard BEFORE INSERT ON model_discovery_evidence
WHEN (NEW.started_at IS NOT NULL AND (typeof(NEW.started_at)<>'integer' OR NEW.started_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.completed_at IS NOT NULL AND (typeof(NEW.completed_at)<>'integer' OR NEW.completed_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'discovery timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_discovery_time_update_guard BEFORE UPDATE ON model_discovery_evidence
WHEN (NEW.started_at IS NOT NULL AND (typeof(NEW.started_at)<>'integer' OR NEW.started_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.completed_at IS NOT NULL AND (typeof(NEW.completed_at)<>'integer' OR NEW.completed_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'discovery timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_catalog_time_guard BEFORE INSERT ON model_catalog_entries
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model catalog timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_catalog_time_update_guard BEFORE UPDATE ON model_catalog_entries
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model catalog timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_pair_catalog_time_guard BEFORE INSERT ON model_pair_catalog
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model pair timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_pair_catalog_time_update_guard BEFORE UPDATE ON model_pair_catalog
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model pair timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_models_time_guard BEFORE INSERT ON models
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_models_time_update_guard BEFORE UPDATE ON models
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_model_bindings_time_guard BEFORE INSERT ON model_bindings
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model binding timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_model_bindings_time_update_guard BEFORE UPDATE ON model_bindings
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'model binding timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_models_time_guard BEFORE INSERT ON charity_models
WHEN (NEW.discount_start_at IS NOT NULL AND (typeof(NEW.discount_start_at)<>'integer' OR NEW.discount_start_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.discount_end_at IS NOT NULL AND (typeof(NEW.discount_end_at)<>'integer' OR NEW.discount_end_at NOT BETWEEN 0 AND 253402300799))
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity model timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_models_time_update_guard BEFORE UPDATE ON charity_models
WHEN (NEW.discount_start_at IS NOT NULL AND (typeof(NEW.discount_start_at)<>'integer' OR NEW.discount_start_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.discount_end_at IS NOT NULL AND (typeof(NEW.discount_end_at)<>'integer' OR NEW.discount_end_at NOT BETWEEN 0 AND 253402300799))
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity model timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_bindings_time_guard BEFORE INSERT ON charity_model_bindings
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity binding timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_bindings_time_update_guard BEFORE UPDATE ON charity_model_bindings
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity binding timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_outcomes_time_guard BEFORE INSERT ON charity_model_outcomes
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity outcome timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_charity_outcomes_time_update_guard BEFORE UPDATE ON charity_model_outcomes
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'charity outcome timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_keys_time_guard BEFORE INSERT ON donation_keys
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.ended_at IS NOT NULL AND (typeof(NEW.ended_at)<>'integer' OR NEW.ended_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_keys_time_update_guard BEFORE UPDATE ON donation_keys
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.ended_at IS NOT NULL AND (typeof(NEW.ended_at)<>'integer' OR NEW.ended_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation key timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_credit_accounts_time_guard BEFORE INSERT ON credit_accounts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'credit account timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_credit_accounts_time_update_guard BEFORE UPDATE ON credit_accounts
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'credit account timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donations_time_guard BEFORE INSERT ON donations
WHEN (NEW.expires_at IS NOT NULL AND (typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.reviewed_at IS NOT NULL AND (typeof(NEW.reviewed_at)<>'integer' OR NEW.reviewed_at NOT BETWEEN 0 AND 253402300799))
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.terminal_at IS NOT NULL AND (typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donations_time_update_guard BEFORE UPDATE ON donations
WHEN (NEW.expires_at IS NOT NULL AND (typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.reviewed_at IS NOT NULL AND (typeof(NEW.reviewed_at)<>'integer' OR NEW.reviewed_at NOT BETWEEN 0 AND 253402300799))
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.terminal_at IS NOT NULL AND (typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_memberships_time_guard BEFORE INSERT ON donation_key_memberships
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'donation membership timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_memberships_time_update_guard BEFORE UPDATE ON donation_key_memberships
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'donation membership timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_reviews_time_guard BEFORE INSERT ON donation_reviews
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'donation review timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_reviews_time_update_guard BEFORE UPDATE ON donation_reviews
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'donation review timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_report_case_decision_time_guard BEFORE INSERT ON report_cases
WHEN NEW.decision_at IS NOT NULL AND (typeof(NEW.decision_at)<>'integer' OR NEW.decision_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'report decision timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_report_case_decision_time_update_guard BEFORE UPDATE ON report_cases
WHEN NEW.decision_at IS NOT NULL AND (typeof(NEW.decision_at)<>'integer' OR NEW.decision_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'report decision timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_revision_guard BEFORE INSERT ON donations
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'donation revision is invalid'); END;
CREATE TRIGGER generation_two_donation_revision_update_guard BEFORE UPDATE OF revision ON donations
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'donation revision is invalid'); END;
CREATE TRIGGER generation_two_report_decisions_time_guard BEFORE INSERT ON report_decisions
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'report decision timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_report_decisions_time_update_guard BEFORE UPDATE ON report_decisions
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'report decision timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_issue_projection_time_guard BEFORE INSERT ON user_issue_projection_state
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'issue projection timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_issue_projection_time_update_guard BEFORE UPDATE ON user_issue_projection_state
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'issue projection timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_fishing_best_time_guard BEFORE INSERT ON game_fishing_best
WHEN typeof(NEW.caught_at)<>'integer' OR NEW.caught_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing best timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_fishing_best_time_update_guard BEFORE UPDATE ON game_fishing_best
WHEN typeof(NEW.caught_at)<>'integer' OR NEW.caught_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing best timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_fishing_rank_fact_time_guard BEFORE INSERT ON game_fishing_rank_facts
WHEN typeof(NEW.settled_at)<>'integer' OR NEW.settled_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing rank timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_fishing_rank_fact_time_update_guard BEFORE UPDATE ON game_fishing_rank_facts
WHEN typeof(NEW.settled_at)<>'integer' OR NEW.settled_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing rank timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_fishing_rank_aggregate_time_guard BEFORE INSERT ON game_fishing_rank_aggregates
WHEN typeof(NEW.score_achieved_at)<>'integer' OR NEW.score_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing rank aggregate timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_fishing_rank_aggregate_time_update_guard BEFORE UPDATE ON game_fishing_rank_aggregates
WHEN typeof(NEW.score_achieved_at)<>'integer' OR NEW.score_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'fishing rank aggregate timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_linklink_summary_time_guard BEFORE INSERT ON game_linklink_summaries
WHEN typeof(NEW.started_at)<>'integer' OR NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.deadline)<>'integer' OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'LinkLink summary timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_linklink_summary_time_update_guard BEFORE UPDATE ON game_linklink_summaries
WHEN typeof(NEW.started_at)<>'integer' OR NEW.started_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.deadline)<>'integer' OR NEW.deadline NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'LinkLink summary timestamp is outside UTC range'); END;

CREATE TRIGGER generation_two_rps_session_retry_time_guard BEFORE INSERT ON game_rps_sessions
WHEN NEW.terminal_next_retry_at IS NOT NULL AND (typeof(NEW.terminal_next_retry_at)<>'integer' OR NEW.terminal_next_retry_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'RPS retry timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_session_retry_time_update_guard BEFORE UPDATE ON game_rps_sessions
WHEN NEW.terminal_next_retry_at IS NOT NULL AND (typeof(NEW.terminal_next_retry_at)<>'integer' OR NEW.terminal_next_retry_at NOT BETWEEN 0 AND 253402300799)
BEGIN SELECT RAISE(ABORT,'RPS retry timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_seat_time_guard BEFORE INSERT ON game_rps_user_slots
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS slot timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_seat_time_update_guard BEFORE UPDATE ON game_rps_user_slots
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS slot timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_pending_time_guard BEFORE INSERT ON game_rps_pending_results
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS pending result timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_pending_time_update_guard BEFORE UPDATE ON game_rps_pending_results
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS pending result timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_fun_stats_time_guard BEFORE INSERT ON game_rps_fun_stats
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS fun stats timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_fun_stats_time_update_guard BEFORE UPDATE ON game_rps_fun_stats
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS fun stats timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_rank_fact_time_guard BEFORE INSERT ON game_rps_rank_facts
WHEN typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS rank timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_rank_fact_time_update_guard BEFORE UPDATE ON game_rps_rank_facts
WHEN typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.expires_at)<>'integer' OR NEW.expires_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS rank timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_rank_aggregate_time_guard BEFORE INSERT ON game_rps_rank_aggregates
WHEN typeof(NEW.profit_rate_achieved_at)<>'integer' OR NEW.profit_rate_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.net_profit_achieved_at)<>'integer' OR NEW.net_profit_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_rps_rank_aggregate_time_update_guard BEFORE UPDATE ON game_rps_rank_aggregates
WHEN typeof(NEW.profit_rate_achieved_at)<>'integer' OR NEW.profit_rate_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.net_profit_achieved_at)<>'integer' OR NEW.net_profit_achieved_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_config_time_guard BEFORE INSERT ON site_config
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site config timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_site_config_time_update_guard BEFORE UPDATE ON site_config
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'site config timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_game_preferences_time_guard BEFORE INSERT ON game_user_preferences
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'game preference timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_game_preferences_time_update_guard BEFORE UPDATE ON game_user_preferences
WHEN typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
BEGIN SELECT RAISE(ABORT,'game preference timestamp is outside UTC range'); END;

-- Keep every generation/epoch/revision INTEGER in SQLite's signed 64-bit domain.
-- The column declarations retain their semantic lower bounds; these guards make
-- the upper bound explicit even when a direct SQL writer bypasses application code.
CREATE TRIGGER generation_two_user_issue_generation_guard BEFORE INSERT ON user_issues
WHEN typeof(NEW.generation)<>'integer' OR NEW.generation NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'user issue generation is outside integer range'); END;
CREATE TRIGGER generation_two_user_issue_generation_update_guard BEFORE UPDATE OF generation ON user_issues
WHEN typeof(NEW.generation)<>'integer' OR NEW.generation NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'user issue generation is outside integer range'); END;
CREATE TRIGGER generation_two_issue_projection_generation_guard BEFORE INSERT ON user_issue_projection_state
WHEN typeof(NEW.rebuild_generation)<>'integer' OR NEW.rebuild_generation NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'issue projection generation is outside integer range'); END;
CREATE TRIGGER generation_two_issue_projection_generation_update_guard BEFORE UPDATE OF rebuild_generation ON user_issue_projection_state
WHEN typeof(NEW.rebuild_generation)<>'integer' OR NEW.rebuild_generation NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'issue projection generation is outside integer range'); END;
CREATE TRIGGER generation_two_maintenance_revision_guard BEFORE INSERT ON maintenance_state
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'maintenance revision is outside integer range'); END;
CREATE TRIGGER generation_two_maintenance_revision_update_guard BEFORE UPDATE OF revision ON maintenance_state
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'maintenance revision is outside integer range'); END;
CREATE TRIGGER generation_two_legal_hold_revision_guard BEFORE INSERT ON legal_holds
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'legal hold revision is outside integer range'); END;
CREATE TRIGGER generation_two_legal_hold_revision_update_guard BEFORE UPDATE OF revision ON legal_holds
WHEN typeof(NEW.revision)<>'integer' OR NEW.revision NOT BETWEEN 1 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'legal hold revision is outside integer range'); END;
CREATE TRIGGER generation_two_rps_session_scalar_guard BEFORE INSERT ON game_rps_sessions
WHEN typeof(NEW.rules_version)<>'integer' OR NEW.rules_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.health_epoch)<>'integer' OR NEW.health_epoch NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'RPS session scalar is outside integer range'); END;
CREATE TRIGGER generation_two_rps_session_scalar_update_guard BEFORE UPDATE OF rules_version,health_epoch ON game_rps_sessions
WHEN typeof(NEW.rules_version)<>'integer' OR NEW.rules_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.health_epoch)<>'integer' OR NEW.health_epoch NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'RPS session scalar is outside integer range'); END;
CREATE TRIGGER generation_two_rps_lease_epoch_guard BEFORE INSERT ON game_online_leases
WHEN typeof(NEW.health_epoch)<>'integer' OR NEW.health_epoch NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'RPS lease health epoch is outside integer range'); END;
CREATE TRIGGER generation_two_rps_lease_epoch_update_guard BEFORE UPDATE OF health_epoch ON game_online_leases
WHEN typeof(NEW.health_epoch)<>'integer' OR NEW.health_epoch NOT BETWEEN 0 AND 9223372036854775807
BEGIN SELECT RAISE(ABORT,'RPS lease health epoch is outside integer range'); END;
CREATE TRIGGER generation_two_rps_summary_scalar_guard BEFORE INSERT ON game_rps_summaries
WHEN typeof(NEW.rules_version)<>'integer' OR NEW.rules_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.base_milli)<>'integer' OR NEW.base_milli NOT BETWEEN 1 AND 9000000000000000
BEGIN SELECT RAISE(ABORT,'RPS summary scalar is outside integer range'); END;
CREATE TRIGGER generation_two_rps_summary_scalar_update_guard BEFORE UPDATE OF rules_version,base_milli ON game_rps_summaries
WHEN typeof(NEW.rules_version)<>'integer' OR NEW.rules_version NOT BETWEEN 1 AND 9223372036854775807
 OR typeof(NEW.base_milli)<>'integer' OR NEW.base_milli NOT BETWEEN 1 AND 9000000000000000
BEGIN SELECT RAISE(ABORT,'RPS summary scalar is outside integer range'); END;
CREATE TRIGGER rps_rank_fact_profit_guard BEFORE INSERT ON game_rps_rank_facts
WHEN (NEW.profitable<>CASE WHEN NEW.wallet_net_sign=1 THEN 1 ELSE 0 END)
BEGIN SELECT RAISE(ABORT,'RPS rank profitability is inconsistent'); END;
CREATE TRIGGER rps_rank_fact_profit_update_guard BEFORE UPDATE OF profitable,wallet_net_sign,wallet_net_mag ON game_rps_rank_facts
WHEN (NEW.profitable<>CASE WHEN NEW.wallet_net_sign=1 THEN 1 ELSE 0 END)
BEGIN SELECT RAISE(ABORT,'RPS rank profitability is inconsistent'); END;
CREATE TRIGGER rps_rank_aggregate_matrix_guard BEFORE INSERT ON game_rps_rank_aggregates
WHEN hex(NEW.session_count)='00000000000000000000000000000000'
 OR hex(NEW.profitable_count)>hex(NEW.session_count)
 OR (NEW.eligible=1 AND hex(NEW.session_count)<'0000000000000000000000000000000A')
 OR (NEW.eligible=0 AND hex(NEW.session_count)>='0000000000000000000000000000000A')
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate matrix is inconsistent'); END;
CREATE TRIGGER rps_rank_aggregate_matrix_update_guard BEFORE UPDATE OF session_count,profitable_count,eligible ON game_rps_rank_aggregates
WHEN hex(NEW.session_count)='00000000000000000000000000000000'
 OR hex(NEW.profitable_count)>hex(NEW.session_count)
 OR (NEW.eligible=1 AND hex(NEW.session_count)<'0000000000000000000000000000000A')
 OR (NEW.eligible=0 AND hex(NEW.session_count)>='0000000000000000000000000000000A')
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate matrix is inconsistent'); END;

-- U128 values are fixed-width big-endian unsigned integers.  Their complete
-- 128-bit range is allowed; SM128 high-bit and sign/zero rules are declared
-- inline on the corresponding signed-magnitude columns.
CREATE TRIGGER generation_two_users_u128_guard BEFORE INSERT ON users
WHEN typeof(NEW.donation_credit_mag)<>'blob' OR length(NEW.donation_credit_mag)<>16 OR substr(hex(NEW.donation_credit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_requests)<>'blob' OR length(NEW.total_requests)<>16 OR substr(hex(NEW.total_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_uncached_input_tokens)<>'blob' OR length(NEW.total_uncached_input_tokens)<>16 OR substr(hex(NEW.total_uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_write_input_tokens)<>'blob' OR length(NEW.total_cache_write_input_tokens)<>16 OR substr(hex(NEW.total_cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_read_input_tokens)<>'blob' OR length(NEW.total_cache_read_input_tokens)<>16 OR substr(hex(NEW.total_cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_output_tokens)<>'blob' OR length(NEW.total_output_tokens)<>16 OR substr(hex(NEW.total_output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_unknown_usage_requests)<>'blob' OR length(NEW.total_unknown_usage_requests)<>16 OR substr(hex(NEW.total_unknown_usage_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'user U128 value is invalid'); END;
CREATE TRIGGER generation_two_users_u128_update_guard BEFORE UPDATE ON users
WHEN typeof(NEW.donation_credit_mag)<>'blob' OR length(NEW.donation_credit_mag)<>16 OR substr(hex(NEW.donation_credit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_requests)<>'blob' OR length(NEW.total_requests)<>16 OR substr(hex(NEW.total_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_uncached_input_tokens)<>'blob' OR length(NEW.total_uncached_input_tokens)<>16 OR substr(hex(NEW.total_uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_write_input_tokens)<>'blob' OR length(NEW.total_cache_write_input_tokens)<>16 OR substr(hex(NEW.total_cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_read_input_tokens)<>'blob' OR length(NEW.total_cache_read_input_tokens)<>16 OR substr(hex(NEW.total_cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_output_tokens)<>'blob' OR length(NEW.total_output_tokens)<>16 OR substr(hex(NEW.total_output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_unknown_usage_requests)<>'blob' OR length(NEW.total_unknown_usage_requests)<>16 OR substr(hex(NEW.total_unknown_usage_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'user U128 value is invalid'); END;
CREATE TRIGGER generation_two_site_activity_u128_guard BEFORE INSERT ON site_activity_daily
WHEN typeof(NEW.api_requests)<>'blob' OR length(NEW.api_requests)<>16 OR substr(hex(NEW.api_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.uncached_input_tokens)<>'blob' OR length(NEW.uncached_input_tokens)<>16 OR substr(hex(NEW.uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cache_write_input_tokens)<>'blob' OR length(NEW.cache_write_input_tokens)<>16 OR substr(hex(NEW.cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cache_read_input_tokens)<>'blob' OR length(NEW.cache_read_input_tokens)<>16 OR substr(hex(NEW.cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.output_tokens)<>'blob' OR length(NEW.output_tokens)<>16 OR substr(hex(NEW.output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.checkins)<>'blob' OR length(NEW.checkins)<>16 OR substr(hex(NEW.checkins),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.console_writes)<>'blob' OR length(NEW.console_writes)<>16 OR substr(hex(NEW.console_writes),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.game_rounds)<>'blob' OR length(NEW.game_rounds)<>16 OR substr(hex(NEW.game_rounds),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.distinct_product_users)<>'blob' OR length(NEW.distinct_product_users)<>16 OR substr(hex(NEW.distinct_product_users),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'site activity U128 value is invalid'); END;
CREATE TRIGGER generation_two_site_activity_u128_update_guard BEFORE UPDATE ON site_activity_daily
WHEN typeof(NEW.api_requests)<>'blob' OR length(NEW.api_requests)<>16 OR substr(hex(NEW.api_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.uncached_input_tokens)<>'blob' OR length(NEW.uncached_input_tokens)<>16 OR substr(hex(NEW.uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cache_write_input_tokens)<>'blob' OR length(NEW.cache_write_input_tokens)<>16 OR substr(hex(NEW.cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cache_read_input_tokens)<>'blob' OR length(NEW.cache_read_input_tokens)<>16 OR substr(hex(NEW.cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.output_tokens)<>'blob' OR length(NEW.output_tokens)<>16 OR substr(hex(NEW.output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.checkins)<>'blob' OR length(NEW.checkins)<>16 OR substr(hex(NEW.checkins),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.console_writes)<>'blob' OR length(NEW.console_writes)<>16 OR substr(hex(NEW.console_writes),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.game_rounds)<>'blob' OR length(NEW.game_rounds)<>16 OR substr(hex(NEW.game_rounds),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.distinct_product_users)<>'blob' OR length(NEW.distinct_product_users)<>16 OR substr(hex(NEW.distinct_product_users),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'site activity U128 value is invalid'); END;
CREATE TRIGGER generation_two_site_usage_u128_guard BEFORE INSERT ON site_usage_totals
WHEN typeof(NEW.total_requests)<>'blob' OR length(NEW.total_requests)<>16 OR substr(hex(NEW.total_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_uncached_input_tokens)<>'blob' OR length(NEW.total_uncached_input_tokens)<>16 OR substr(hex(NEW.total_uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_write_input_tokens)<>'blob' OR length(NEW.total_cache_write_input_tokens)<>16 OR substr(hex(NEW.total_cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_read_input_tokens)<>'blob' OR length(NEW.total_cache_read_input_tokens)<>16 OR substr(hex(NEW.total_cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_output_tokens)<>'blob' OR length(NEW.total_output_tokens)<>16 OR substr(hex(NEW.total_output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_unknown_usage_requests)<>'blob' OR length(NEW.total_unknown_usage_requests)<>16 OR substr(hex(NEW.total_unknown_usage_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'site usage U128 value is invalid'); END;
CREATE TRIGGER generation_two_site_usage_u128_update_guard BEFORE UPDATE ON site_usage_totals
WHEN typeof(NEW.total_requests)<>'blob' OR length(NEW.total_requests)<>16 OR substr(hex(NEW.total_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_uncached_input_tokens)<>'blob' OR length(NEW.total_uncached_input_tokens)<>16 OR substr(hex(NEW.total_uncached_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_write_input_tokens)<>'blob' OR length(NEW.total_cache_write_input_tokens)<>16 OR substr(hex(NEW.total_cache_write_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_cache_read_input_tokens)<>'blob' OR length(NEW.total_cache_read_input_tokens)<>16 OR substr(hex(NEW.total_cache_read_input_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_output_tokens)<>'blob' OR length(NEW.total_output_tokens)<>16 OR substr(hex(NEW.total_output_tokens),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_unknown_usage_requests)<>'blob' OR length(NEW.total_unknown_usage_requests)<>16 OR substr(hex(NEW.total_unknown_usage_requests),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'site usage U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_capacity_u128_guard BEFORE INSERT ON credit_capacity
WHEN typeof(NEW.reserved_future_rows)<>'blob' OR length(NEW.reserved_future_rows)<>16 OR substr(hex(NEW.reserved_future_rows),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'credit capacity U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_capacity_u128_update_guard BEFORE UPDATE ON credit_capacity
WHEN typeof(NEW.reserved_future_rows)<>'blob' OR length(NEW.reserved_future_rows)<>16 OR substr(hex(NEW.reserved_future_rows),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'credit capacity U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_accounts_sm128_guard BEFORE INSERT ON credit_accounts
WHEN typeof(NEW.balance_mag)<>'blob' OR length(NEW.balance_mag)<>16 OR substr(hex(NEW.balance_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'credit account SM128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_accounts_sm128_update_guard BEFORE UPDATE OF balance_mag ON credit_accounts
WHEN typeof(NEW.balance_mag)<>'blob' OR length(NEW.balance_mag)<>16 OR substr(hex(NEW.balance_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'credit account SM128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_operations_u128_guard BEFORE INSERT ON credit_operations
WHEN typeof(NEW.source_seq)<>'blob' OR length(NEW.source_seq)<>16 OR substr(hex(NEW.source_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.donation_credit_delta_mag)<>'blob' OR length(NEW.donation_credit_delta_mag)<>16 OR substr(hex(NEW.donation_credit_delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.donation_credit_after IS NOT NULL AND (typeof(NEW.donation_credit_after)<>'blob' OR length(NEW.donation_credit_after)<>16 OR substr(hex(NEW.donation_credit_after),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'credit operation U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_operations_u128_update_guard BEFORE UPDATE ON credit_operations
WHEN typeof(NEW.source_seq)<>'blob' OR length(NEW.source_seq)<>16 OR substr(hex(NEW.source_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.donation_credit_delta_mag)<>'blob' OR length(NEW.donation_credit_delta_mag)<>16 OR substr(hex(NEW.donation_credit_delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.donation_credit_after IS NOT NULL AND (typeof(NEW.donation_credit_after)<>'blob' OR length(NEW.donation_credit_after)<>16 OR substr(hex(NEW.donation_credit_after),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'credit operation U128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_entries_sm128_guard BEFORE INSERT ON credit_entries
WHEN typeof(NEW.delta_mag)<>'blob' OR length(NEW.delta_mag)<>16 OR substr(hex(NEW.delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.balance_after_mag IS NOT NULL AND (typeof(NEW.balance_after_mag)<>'blob' OR length(NEW.balance_after_mag)<>16 OR substr(hex(NEW.balance_after_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')))
BEGIN SELECT RAISE(ABORT,'credit entry SM128 value is invalid'); END;
CREATE TRIGGER generation_two_credit_entries_sm128_update_guard BEFORE UPDATE OF delta_mag,balance_after_mag ON credit_entries
WHEN typeof(NEW.delta_mag)<>'blob' OR length(NEW.delta_mag)<>16 OR substr(hex(NEW.delta_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR (NEW.balance_after_mag IS NOT NULL AND (typeof(NEW.balance_after_mag)<>'blob' OR length(NEW.balance_after_mag)<>16 OR substr(hex(NEW.balance_after_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')))
BEGIN SELECT RAISE(ABORT,'credit entry SM128 value is invalid'); END;
CREATE TRIGGER generation_two_charity_reservation_u128_guard BEFORE INSERT ON charity_reservations
WHEN typeof(NEW.donor_reward_total_mag)<>'blob' OR length(NEW.donor_reward_total_mag)<>16 OR substr(hex(NEW.donor_reward_total_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'charity reservation U128 value is invalid'); END;
CREATE TRIGGER generation_two_charity_reservation_u128_update_guard BEFORE UPDATE ON charity_reservations
WHEN typeof(NEW.donor_reward_total_mag)<>'blob' OR length(NEW.donor_reward_total_mag)<>16 OR substr(hex(NEW.donor_reward_total_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'charity reservation U128 value is invalid'); END;
CREATE TRIGGER generation_two_donation_usage_u128_guard BEFORE INSERT ON donation_usage_reservations
WHEN typeof(NEW.streak_generation)<>'blob' OR length(NEW.streak_generation)<>16 OR substr(hex(NEW.streak_generation),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.claim_seq)<>'blob' OR length(NEW.claim_seq)<>16 OR substr(hex(NEW.claim_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'donation usage U128 value is invalid'); END;
CREATE TRIGGER generation_two_donation_usage_u128_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN typeof(NEW.streak_generation)<>'blob' OR length(NEW.streak_generation)<>16 OR substr(hex(NEW.streak_generation),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.claim_seq)<>'blob' OR length(NEW.claim_seq)<>16 OR substr(hex(NEW.claim_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'donation usage U128 value is invalid'); END;
CREATE TRIGGER generation_two_donation_keys_u128_guard BEFORE INSERT ON donation_keys
WHEN (NEW.price_limit_mag IS NOT NULL AND (typeof(NEW.price_limit_mag)<>'blob' OR length(NEW.price_limit_mag)<>16 OR substr(hex(NEW.price_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.call_limit_mag IS NOT NULL AND (typeof(NEW.call_limit_mag)<>'blob' OR length(NEW.call_limit_mag)<>16 OR substr(hex(NEW.call_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.token_limit_mag IS NOT NULL AND (typeof(NEW.token_limit_mag)<>'blob' OR length(NEW.token_limit_mag)<>16 OR substr(hex(NEW.token_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR typeof(NEW.price_used_mag)<>'blob' OR length(NEW.price_used_mag)<>16 OR substr(hex(NEW.price_used_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.price_reserved_mag)<>'blob' OR length(NEW.price_reserved_mag)<>16 OR substr(hex(NEW.price_reserved_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.calls_used)<>'blob' OR length(NEW.calls_used)<>16 OR substr(hex(NEW.calls_used),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.calls_reserved)<>'blob' OR length(NEW.calls_reserved)<>16 OR substr(hex(NEW.calls_reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.tokens_used)<>'blob' OR length(NEW.tokens_used)<>16 OR substr(hex(NEW.tokens_used),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.tokens_reserved)<>'blob' OR length(NEW.tokens_reserved)<>16 OR substr(hex(NEW.tokens_reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.failure_streak)<>'blob' OR length(NEW.failure_streak)<>16 OR substr(hex(NEW.failure_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.streak_generation)<>'blob' OR length(NEW.streak_generation)<>16 OR substr(hex(NEW.streak_generation),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_claim_seq)<>'blob' OR length(NEW.next_claim_seq)<>16 OR substr(hex(NEW.next_claim_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_fold_seq)<>'blob' OR length(NEW.next_fold_seq)<>16 OR substr(hex(NEW.next_fold_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'donation key U128 value is invalid'); END;
CREATE TRIGGER generation_two_donation_keys_u128_update_guard BEFORE UPDATE ON donation_keys
WHEN (NEW.price_limit_mag IS NOT NULL AND (typeof(NEW.price_limit_mag)<>'blob' OR length(NEW.price_limit_mag)<>16 OR substr(hex(NEW.price_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.call_limit_mag IS NOT NULL AND (typeof(NEW.call_limit_mag)<>'blob' OR length(NEW.call_limit_mag)<>16 OR substr(hex(NEW.call_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.token_limit_mag IS NOT NULL AND (typeof(NEW.token_limit_mag)<>'blob' OR length(NEW.token_limit_mag)<>16 OR substr(hex(NEW.token_limit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR typeof(NEW.price_used_mag)<>'blob' OR length(NEW.price_used_mag)<>16 OR substr(hex(NEW.price_used_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.price_reserved_mag)<>'blob' OR length(NEW.price_reserved_mag)<>16 OR substr(hex(NEW.price_reserved_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.calls_used)<>'blob' OR length(NEW.calls_used)<>16 OR substr(hex(NEW.calls_used),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.calls_reserved)<>'blob' OR length(NEW.calls_reserved)<>16 OR substr(hex(NEW.calls_reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.tokens_used)<>'blob' OR length(NEW.tokens_used)<>16 OR substr(hex(NEW.tokens_used),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.tokens_reserved)<>'blob' OR length(NEW.tokens_reserved)<>16 OR substr(hex(NEW.tokens_reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.failure_streak)<>'blob' OR length(NEW.failure_streak)<>16 OR substr(hex(NEW.failure_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.streak_generation)<>'blob' OR length(NEW.streak_generation)<>16 OR substr(hex(NEW.streak_generation),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_claim_seq)<>'blob' OR length(NEW.next_claim_seq)<>16 OR substr(hex(NEW.next_claim_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.next_fold_seq)<>'blob' OR length(NEW.next_fold_seq)<>16 OR substr(hex(NEW.next_fold_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'donation key U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_batch_u128_guard BEFORE INSERT ON game_fishing_batches
WHEN typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing batch U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_batch_u128_update_guard BEFORE UPDATE ON game_fishing_batches
WHEN typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing batch U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_rank_fact_u128_guard BEFORE INSERT ON game_fishing_rank_facts
WHEN typeof(NEW.payout_total)<>'blob' OR length(NEW.payout_total)<>16 OR substr(hex(NEW.payout_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing rank fact U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_rank_fact_u128_update_guard BEFORE UPDATE ON game_fishing_rank_facts
WHEN typeof(NEW.payout_total)<>'blob' OR length(NEW.payout_total)<>16 OR substr(hex(NEW.payout_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing rank fact U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_rank_aggregate_u128_guard BEFORE INSERT ON game_fishing_rank_aggregates
WHEN typeof(NEW.batch_count)<>'blob' OR length(NEW.batch_count)<>16 OR substr(hex(NEW.batch_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_payout)<>'blob' OR length(NEW.total_payout)<>16 OR substr(hex(NEW.total_payout),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing rank aggregate U128 value is invalid'); END;
CREATE TRIGGER generation_two_fishing_rank_aggregate_u128_update_guard BEFORE UPDATE ON game_fishing_rank_aggregates
WHEN typeof(NEW.batch_count)<>'blob' OR length(NEW.batch_count)<>16 OR substr(hex(NEW.batch_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_payout)<>'blob' OR length(NEW.total_payout)<>16 OR substr(hex(NEW.total_payout),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'fishing rank aggregate U128 value is invalid'); END;
CREATE TRIGGER generation_two_linklink_u128_guard BEFORE INSERT ON game_linklink_sessions
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'LinkLink U128 value is invalid'); END;
CREATE TRIGGER generation_two_linklink_u128_update_guard BEFORE UPDATE ON game_linklink_sessions
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'LinkLink U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_queue_u128_guard BEFORE INSERT ON game_rps_queue
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.reserved)<>'blob' OR length(NEW.reserved)<>16 OR substr(hex(NEW.reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS queue U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_queue_u128_update_guard BEFORE UPDATE ON game_rps_queue
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.reserved)<>'blob' OR length(NEW.reserved)<>16 OR substr(hex(NEW.reserved),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS queue U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_session_u128_guard BEFORE INSERT ON game_rps_sessions
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.phase_seq)<>'blob' OR length(NEW.phase_seq)<>16 OR substr(hex(NEW.phase_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.identity_epoch)<>'blob' OR length(NEW.identity_epoch)<>16 OR substr(hex(NEW.identity_epoch),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cut_seq)<>'blob' OR length(NEW.cut_seq)<>16 OR substr(hex(NEW.cut_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.player_pool)<>'blob' OR length(NEW.player_pool)<>16 OR substr(hex(NEW.player_pool),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.permanent_multiplier)<>'blob' OR length(NEW.permanent_multiplier)<>16 OR substr(hex(NEW.permanent_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.pool_base_multiplier IS NOT NULL AND (typeof(NEW.pool_base_multiplier)<>'blob' OR length(NEW.pool_base_multiplier)<>16 OR substr(hex(NEW.pool_base_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.current_plan_multiplier IS NOT NULL AND (typeof(NEW.current_plan_multiplier)<>'blob' OR length(NEW.current_plan_multiplier)<>16 OR substr(hex(NEW.current_plan_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.dealer_raise IS NOT NULL AND (typeof(NEW.dealer_raise)<>'blob' OR length(NEW.dealer_raise)<>16 OR substr(hex(NEW.dealer_raise),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR typeof(NEW.base_round_count)<>'blob' OR length(NEW.base_round_count)<>16 OR substr(hex(NEW.base_round_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_tie_count)<>'blob' OR length(NEW.paid_tie_count)<>16 OR substr(hex(NEW.paid_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_tie_count)<>'blob' OR length(NEW.free_tie_count)<>16 OR substr(hex(NEW.free_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_pool_streak)<>'blob' OR length(NEW.paid_pool_streak)<>16 OR substr(hex(NEW.paid_pool_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_pool_streak)<>'blob' OR length(NEW.free_pool_streak)<>16 OR substr(hex(NEW.free_pool_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_cut_total)<>'blob' OR length(NEW.platform_cut_total)<>16 OR substr(hex(NEW.platform_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_cut_total)<>'blob' OR length(NEW.welfare_cut_total)<>16 OR substr(hex(NEW.welfare_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.thursday_cut_total)<>'blob' OR length(NEW.thursday_cut_total)<>16 OR substr(hex(NEW.thursday_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_carry_total)<>'blob' OR length(NEW.welfare_carry_total)<>16 OR substr(hex(NEW.welfare_carry_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.recent_first_seq)<>'blob' OR length(NEW.recent_first_seq)<>16 OR substr(hex(NEW.recent_first_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.recent_last_seq)<>'blob' OR length(NEW.recent_last_seq)<>16 OR substr(hex(NEW.recent_last_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.terminal_retry_attempt_count)<>'blob' OR length(NEW.terminal_retry_attempt_count)<>16 OR substr(hex(NEW.terminal_retry_attempt_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS session U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_session_u128_update_guard BEFORE UPDATE ON game_rps_sessions
WHEN typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.phase_seq)<>'blob' OR length(NEW.phase_seq)<>16 OR substr(hex(NEW.phase_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.identity_epoch)<>'blob' OR length(NEW.identity_epoch)<>16 OR substr(hex(NEW.identity_epoch),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.cut_seq)<>'blob' OR length(NEW.cut_seq)<>16 OR substr(hex(NEW.cut_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.ledger_rows_remaining)<>'blob' OR length(NEW.ledger_rows_remaining)<>16 OR substr(hex(NEW.ledger_rows_remaining),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.player_pool)<>'blob' OR length(NEW.player_pool)<>16 OR substr(hex(NEW.player_pool),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.permanent_multiplier)<>'blob' OR length(NEW.permanent_multiplier)<>16 OR substr(hex(NEW.permanent_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.pool_base_multiplier IS NOT NULL AND (typeof(NEW.pool_base_multiplier)<>'blob' OR length(NEW.pool_base_multiplier)<>16 OR substr(hex(NEW.pool_base_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.current_plan_multiplier IS NOT NULL AND (typeof(NEW.current_plan_multiplier)<>'blob' OR length(NEW.current_plan_multiplier)<>16 OR substr(hex(NEW.current_plan_multiplier),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.dealer_raise IS NOT NULL AND (typeof(NEW.dealer_raise)<>'blob' OR length(NEW.dealer_raise)<>16 OR substr(hex(NEW.dealer_raise),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR typeof(NEW.base_round_count)<>'blob' OR length(NEW.base_round_count)<>16 OR substr(hex(NEW.base_round_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_tie_count)<>'blob' OR length(NEW.paid_tie_count)<>16 OR substr(hex(NEW.paid_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_tie_count)<>'blob' OR length(NEW.free_tie_count)<>16 OR substr(hex(NEW.free_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_pool_streak)<>'blob' OR length(NEW.paid_pool_streak)<>16 OR substr(hex(NEW.paid_pool_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_pool_streak)<>'blob' OR length(NEW.free_pool_streak)<>16 OR substr(hex(NEW.free_pool_streak),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_cut_total)<>'blob' OR length(NEW.platform_cut_total)<>16 OR substr(hex(NEW.platform_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_cut_total)<>'blob' OR length(NEW.welfare_cut_total)<>16 OR substr(hex(NEW.welfare_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.thursday_cut_total)<>'blob' OR length(NEW.thursday_cut_total)<>16 OR substr(hex(NEW.thursday_cut_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_carry_total)<>'blob' OR length(NEW.welfare_carry_total)<>16 OR substr(hex(NEW.welfare_carry_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.recent_first_seq)<>'blob' OR length(NEW.recent_first_seq)<>16 OR substr(hex(NEW.recent_first_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.recent_last_seq)<>'blob' OR length(NEW.recent_last_seq)<>16 OR substr(hex(NEW.recent_last_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.terminal_retry_attempt_count)<>'blob' OR length(NEW.terminal_retry_attempt_count)<>16 OR substr(hex(NEW.terminal_retry_attempt_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS session U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_seats_u128_guard BEFORE INSERT ON game_rps_seats
WHEN typeof(NEW.starting_balance)<>'blob' OR length(NEW.starting_balance)<>16 OR substr(hex(NEW.starting_balance),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.current_balance)<>'blob' OR length(NEW.current_balance)<>16 OR substr(hex(NEW.current_balance),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.current_round_input)<>'blob' OR length(NEW.current_round_input)<>16 OR substr(hex(NEW.current_round_input),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.current_gesture_phase_seq IS NOT NULL AND (typeof(NEW.current_gesture_phase_seq)<>'blob' OR length(NEW.current_gesture_phase_seq)<>16 OR substr(hex(NEW.current_gesture_phase_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.terminal_return IS NOT NULL AND (typeof(NEW.terminal_return)<>'blob' OR length(NEW.terminal_return)<>16 OR substr(hex(NEW.terminal_return),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.wallet_net_mag IS NOT NULL AND (typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')))
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.timeout_count)<>'blob' OR length(NEW.timeout_count)<>16 OR substr(hex(NEW.timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.snapshot_completed_count IS NOT NULL AND (typeof(NEW.snapshot_completed_count)<>'blob' OR length(NEW.snapshot_completed_count)<>16 OR substr(hex(NEW.snapshot_completed_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_profitable_count IS NOT NULL AND (typeof(NEW.snapshot_profitable_count)<>'blob' OR length(NEW.snapshot_profitable_count)<>16 OR substr(hex(NEW.snapshot_profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_rock_count IS NOT NULL AND (typeof(NEW.snapshot_rock_count)<>'blob' OR length(NEW.snapshot_rock_count)<>16 OR substr(hex(NEW.snapshot_rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_scissors_count IS NOT NULL AND (typeof(NEW.snapshot_scissors_count)<>'blob' OR length(NEW.snapshot_scissors_count)<>16 OR substr(hex(NEW.snapshot_scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_paper_count IS NOT NULL AND (typeof(NEW.snapshot_paper_count)<>'blob' OR length(NEW.snapshot_paper_count)<>16 OR substr(hex(NEW.snapshot_paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'RPS seat U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_seats_u128_update_guard BEFORE UPDATE ON game_rps_seats
WHEN typeof(NEW.starting_balance)<>'blob' OR length(NEW.starting_balance)<>16 OR substr(hex(NEW.starting_balance),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.current_balance)<>'blob' OR length(NEW.current_balance)<>16 OR substr(hex(NEW.current_balance),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.current_round_input)<>'blob' OR length(NEW.current_round_input)<>16 OR substr(hex(NEW.current_round_input),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.current_gesture_phase_seq IS NOT NULL AND (typeof(NEW.current_gesture_phase_seq)<>'blob' OR length(NEW.current_gesture_phase_seq)<>16 OR substr(hex(NEW.current_gesture_phase_seq),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.terminal_return IS NOT NULL AND (typeof(NEW.terminal_return)<>'blob' OR length(NEW.terminal_return)<>16 OR substr(hex(NEW.terminal_return),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.wallet_net_mag IS NOT NULL AND (typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')))
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.timeout_count)<>'blob' OR length(NEW.timeout_count)<>16 OR substr(hex(NEW.timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR (NEW.snapshot_completed_count IS NOT NULL AND (typeof(NEW.snapshot_completed_count)<>'blob' OR length(NEW.snapshot_completed_count)<>16 OR substr(hex(NEW.snapshot_completed_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_profitable_count IS NOT NULL AND (typeof(NEW.snapshot_profitable_count)<>'blob' OR length(NEW.snapshot_profitable_count)<>16 OR substr(hex(NEW.snapshot_profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_rock_count IS NOT NULL AND (typeof(NEW.snapshot_rock_count)<>'blob' OR length(NEW.snapshot_rock_count)<>16 OR substr(hex(NEW.snapshot_rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_scissors_count IS NOT NULL AND (typeof(NEW.snapshot_scissors_count)<>'blob' OR length(NEW.snapshot_scissors_count)<>16 OR substr(hex(NEW.snapshot_scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
 OR (NEW.snapshot_paper_count IS NOT NULL AND (typeof(NEW.snapshot_paper_count)<>'blob' OR length(NEW.snapshot_paper_count)<>16 OR substr(hex(NEW.snapshot_paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')))
BEGIN SELECT RAISE(ABORT,'RPS seat U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_pending_u128_guard BEFORE INSERT ON game_rps_pending_results
WHEN typeof(NEW.own_wallet_net_mag)<>'blob' OR length(NEW.own_wallet_net_mag)<>16 OR substr(hex(NEW.own_wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'RPS pending U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_pending_u128_update_guard BEFORE UPDATE ON game_rps_pending_results
WHEN typeof(NEW.own_wallet_net_mag)<>'blob' OR length(NEW.own_wallet_net_mag)<>16 OR substr(hex(NEW.own_wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'RPS pending U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_summary_u128_guard BEFORE INSERT ON game_rps_summaries
WHEN typeof(NEW.base_round_count)<>'blob' OR length(NEW.base_round_count)<>16 OR substr(hex(NEW.base_round_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_tie_count)<>'blob' OR length(NEW.paid_tie_count)<>16 OR substr(hex(NEW.paid_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_tie_count)<>'blob' OR length(NEW.free_tie_count)<>16 OR substr(hex(NEW.free_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_timeout_count)<>'blob' OR length(NEW.total_timeout_count)<>16 OR substr(hex(NEW.total_timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_rock_count)<>'blob' OR length(NEW.total_rock_count)<>16 OR substr(hex(NEW.total_rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_scissors_count)<>'blob' OR length(NEW.total_scissors_count)<>16 OR substr(hex(NEW.total_scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_paper_count)<>'blob' OR length(NEW.total_paper_count)<>16 OR substr(hex(NEW.total_paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_total)<>'blob' OR length(NEW.platform_total)<>16 OR substr(hex(NEW.platform_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_total)<>'blob' OR length(NEW.welfare_total)<>16 OR substr(hex(NEW.welfare_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.thursday_total)<>'blob' OR length(NEW.thursday_total)<>16 OR substr(hex(NEW.thursday_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS summary U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_summary_u128_update_guard BEFORE UPDATE ON game_rps_summaries
WHEN typeof(NEW.base_round_count)<>'blob' OR length(NEW.base_round_count)<>16 OR substr(hex(NEW.base_round_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paid_tie_count)<>'blob' OR length(NEW.paid_tie_count)<>16 OR substr(hex(NEW.paid_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.free_tie_count)<>'blob' OR length(NEW.free_tie_count)<>16 OR substr(hex(NEW.free_tie_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_timeout_count)<>'blob' OR length(NEW.total_timeout_count)<>16 OR substr(hex(NEW.total_timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_rock_count)<>'blob' OR length(NEW.total_rock_count)<>16 OR substr(hex(NEW.total_rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_scissors_count)<>'blob' OR length(NEW.total_scissors_count)<>16 OR substr(hex(NEW.total_scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.total_paper_count)<>'blob' OR length(NEW.total_paper_count)<>16 OR substr(hex(NEW.total_paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.platform_total)<>'blob' OR length(NEW.platform_total)<>16 OR substr(hex(NEW.platform_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.welfare_total)<>'blob' OR length(NEW.welfare_total)<>16 OR substr(hex(NEW.welfare_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.thursday_total)<>'blob' OR length(NEW.thursday_total)<>16 OR substr(hex(NEW.thursday_total),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS summary U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_summary_seats_u128_guard BEFORE INSERT ON game_rps_summary_seats
WHEN typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR typeof(NEW.timeout_count)<>'blob' OR length(NEW.timeout_count)<>16 OR substr(hex(NEW.timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS summary seat U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_summary_seats_u128_update_guard BEFORE UPDATE ON game_rps_summary_seats
WHEN typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR typeof(NEW.timeout_count)<>'blob' OR length(NEW.timeout_count)<>16 OR substr(hex(NEW.timeout_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS summary seat U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_fun_stats_u128_guard BEFORE INSERT ON game_rps_fun_stats
WHEN typeof(NEW.completed_count)<>'blob' OR length(NEW.completed_count)<>16 OR substr(hex(NEW.completed_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.profitable_count)<>'blob' OR length(NEW.profitable_count)<>16 OR substr(hex(NEW.profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS fun stats U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_fun_stats_u128_update_guard BEFORE UPDATE ON game_rps_fun_stats
WHEN typeof(NEW.completed_count)<>'blob' OR length(NEW.completed_count)<>16 OR substr(hex(NEW.completed_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.profitable_count)<>'blob' OR length(NEW.profitable_count)<>16 OR substr(hex(NEW.profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.rock_count)<>'blob' OR length(NEW.rock_count)<>16 OR substr(hex(NEW.rock_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.scissors_count)<>'blob' OR length(NEW.scissors_count)<>16 OR substr(hex(NEW.scissors_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.paper_count)<>'blob' OR length(NEW.paper_count)<>16 OR substr(hex(NEW.paper_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS fun stats U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_rank_fact_u128_guard BEFORE INSERT ON game_rps_rank_facts
WHEN typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'RPS rank fact U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_rank_fact_u128_update_guard BEFORE UPDATE ON game_rps_rank_facts
WHEN typeof(NEW.wallet_net_mag)<>'blob' OR length(NEW.wallet_net_mag)<>16 OR substr(hex(NEW.wallet_net_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
BEGIN SELECT RAISE(ABORT,'RPS rank fact U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_rank_aggregate_u128_guard BEFORE INSERT ON game_rps_rank_aggregates
WHEN typeof(NEW.session_count)<>'blob' OR length(NEW.session_count)<>16 OR substr(hex(NEW.session_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.profitable_count)<>'blob' OR length(NEW.profitable_count)<>16 OR substr(hex(NEW.profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.net_profit_mag)<>'blob' OR length(NEW.net_profit_mag)<>16 OR substr(hex(NEW.net_profit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate U128 value is invalid'); END;
CREATE TRIGGER generation_two_rps_rank_aggregate_u128_update_guard BEFORE UPDATE ON game_rps_rank_aggregates
WHEN typeof(NEW.session_count)<>'blob' OR length(NEW.session_count)<>16 OR substr(hex(NEW.session_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.profitable_count)<>'blob' OR length(NEW.profitable_count)<>16 OR substr(hex(NEW.profitable_count),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
 OR typeof(NEW.net_profit_mag)<>'blob' OR length(NEW.net_profit_mag)<>16 OR substr(hex(NEW.net_profit_mag),1,1) NOT IN ('0','1','2','3','4','5','6','7')
 OR typeof(NEW.revision)<>'blob' OR length(NEW.revision)<>16 OR substr(hex(NEW.revision),1,1) NOT IN ('0','1','2','3','4','5','6','7','8','9','A','B','C','D','E','F')
BEGIN SELECT RAISE(ABORT,'RPS rank aggregate U128 value is invalid'); END;
-- SQLite has INTEGER affinity, so every INTEGER column also receives an explicit type guard.
CREATE TRIGGER generation_two_integer_type_accepted_operations_insert_guard BEFORE INSERT ON accepted_operations
WHEN (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_accepted_operations_update_guard BEFORE UPDATE ON accepted_operations
WHEN (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_admin_alerts_insert_guard BEFORE INSERT ON admin_alerts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.subject_user_id IS NOT NULL AND typeof(NEW.subject_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.resolved IS NOT NULL AND typeof(NEW.resolved)<>'integer')
 OR (NEW.resolved_at IS NOT NULL AND typeof(NEW.resolved_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_admin_alerts_update_guard BEFORE UPDATE ON admin_alerts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.subject_user_id IS NOT NULL AND typeof(NEW.subject_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.resolved IS NOT NULL AND typeof(NEW.resolved)<>'integer')
 OR (NEW.resolved_at IS NOT NULL AND typeof(NEW.resolved_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_announcement_audits_insert_guard BEFORE INSERT ON announcement_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.from_revision IS NOT NULL AND typeof(NEW.from_revision)<>'integer')
 OR (NEW.to_revision IS NOT NULL AND typeof(NEW.to_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.actor_deidentify_at IS NOT NULL AND typeof(NEW.actor_deidentify_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_announcement_audits_update_guard BEFORE UPDATE ON announcement_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.from_revision IS NOT NULL AND typeof(NEW.from_revision)<>'integer')
 OR (NEW.to_revision IS NOT NULL AND typeof(NEW.to_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.actor_deidentify_at IS NOT NULL AND typeof(NEW.actor_deidentify_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_announcements_insert_guard BEFORE INSERT ON announcements
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.pinned IS NOT NULL AND typeof(NEW.pinned)<>'integer')
 OR (NEW.dismissible IS NOT NULL AND typeof(NEW.dismissible)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.published_revision IS NOT NULL AND typeof(NEW.published_revision)<>'integer')
 OR (NEW.published_at IS NOT NULL AND typeof(NEW.published_at)<>'integer')
 OR (NEW.withdrawn_at IS NOT NULL AND typeof(NEW.withdrawn_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_announcements_update_guard BEFORE UPDATE ON announcements
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.pinned IS NOT NULL AND typeof(NEW.pinned)<>'integer')
 OR (NEW.dismissible IS NOT NULL AND typeof(NEW.dismissible)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.published_revision IS NOT NULL AND typeof(NEW.published_revision)<>'integer')
 OR (NEW.published_at IS NOT NULL AND typeof(NEW.published_at)<>'integer')
 OR (NEW.withdrawn_at IS NOT NULL AND typeof(NEW.withdrawn_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_caller_keys_insert_guard BEFORE INSERT ON caller_keys
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.key_created_at IS NOT NULL AND typeof(NEW.key_created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_caller_keys_update_guard BEFORE UPDATE ON caller_keys
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.key_created_at IS NOT NULL AND typeof(NEW.key_created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_bindings_insert_guard BEFORE INSERT ON charity_model_bindings
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.charity_model_id IS NOT NULL AND typeof(NEW.charity_model_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.ord IS NOT NULL AND typeof(NEW.ord)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_bindings_update_guard BEFORE UPDATE ON charity_model_bindings
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.charity_model_id IS NOT NULL AND typeof(NEW.charity_model_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.ord IS NOT NULL AND typeof(NEW.ord)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_outcomes_insert_guard BEFORE INSERT ON charity_model_outcomes
WHEN (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.slot IS NOT NULL AND typeof(NEW.slot)<>'integer')
 OR (NEW.success IS NOT NULL AND typeof(NEW.success)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_outcomes_update_guard BEFORE UPDATE ON charity_model_outcomes
WHEN (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.slot IS NOT NULL AND typeof(NEW.slot)<>'integer')
 OR (NEW.success IS NOT NULL AND typeof(NEW.success)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_stats_insert_guard BEFORE INSERT ON charity_model_stats
WHEN (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.next_slot IS NOT NULL AND typeof(NEW.next_slot)<>'integer')
 OR (NEW.sample_count IS NOT NULL AND typeof(NEW.sample_count)<>'integer')
 OR (NEW.success_count IS NOT NULL AND typeof(NEW.success_count)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_model_stats_update_guard BEFORE UPDATE ON charity_model_stats
WHEN (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.next_slot IS NOT NULL AND typeof(NEW.next_slot)<>'integer')
 OR (NEW.sample_count IS NOT NULL AND typeof(NEW.sample_count)<>'integer')
 OR (NEW.success_count IS NOT NULL AND typeof(NEW.success_count)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_models_insert_guard BEFORE INSERT ON charity_models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.request_user_price IS NOT NULL AND typeof(NEW.request_user_price)<>'integer')
 OR (NEW.request_donor_reward IS NOT NULL AND typeof(NEW.request_donor_reward)<>'integer')
 OR (NEW.uncached_user_price IS NOT NULL AND typeof(NEW.uncached_user_price)<>'integer')
 OR (NEW.cache_write_user_price IS NOT NULL AND typeof(NEW.cache_write_user_price)<>'integer')
 OR (NEW.cache_read_user_price IS NOT NULL AND typeof(NEW.cache_read_user_price)<>'integer')
 OR (NEW.output_user_price IS NOT NULL AND typeof(NEW.output_user_price)<>'integer')
 OR (NEW.uncached_donor_reward IS NOT NULL AND typeof(NEW.uncached_donor_reward)<>'integer')
 OR (NEW.cache_write_donor_reward IS NOT NULL AND typeof(NEW.cache_write_donor_reward)<>'integer')
 OR (NEW.cache_read_donor_reward IS NOT NULL AND typeof(NEW.cache_read_donor_reward)<>'integer')
 OR (NEW.output_donor_reward IS NOT NULL AND typeof(NEW.output_donor_reward)<>'integer')
 OR (NEW.discount_percent IS NOT NULL AND typeof(NEW.discount_percent)<>'integer')
 OR (NEW.discount_start_at IS NOT NULL AND typeof(NEW.discount_start_at)<>'integer')
 OR (NEW.discount_end_at IS NOT NULL AND typeof(NEW.discount_end_at)<>'integer')
 OR (NEW.discount_enabled IS NOT NULL AND typeof(NEW.discount_enabled)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.created_by_user_id IS NOT NULL AND typeof(NEW.created_by_user_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_models_update_guard BEFORE UPDATE ON charity_models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.request_user_price IS NOT NULL AND typeof(NEW.request_user_price)<>'integer')
 OR (NEW.request_donor_reward IS NOT NULL AND typeof(NEW.request_donor_reward)<>'integer')
 OR (NEW.uncached_user_price IS NOT NULL AND typeof(NEW.uncached_user_price)<>'integer')
 OR (NEW.cache_write_user_price IS NOT NULL AND typeof(NEW.cache_write_user_price)<>'integer')
 OR (NEW.cache_read_user_price IS NOT NULL AND typeof(NEW.cache_read_user_price)<>'integer')
 OR (NEW.output_user_price IS NOT NULL AND typeof(NEW.output_user_price)<>'integer')
 OR (NEW.uncached_donor_reward IS NOT NULL AND typeof(NEW.uncached_donor_reward)<>'integer')
 OR (NEW.cache_write_donor_reward IS NOT NULL AND typeof(NEW.cache_write_donor_reward)<>'integer')
 OR (NEW.cache_read_donor_reward IS NOT NULL AND typeof(NEW.cache_read_donor_reward)<>'integer')
 OR (NEW.output_donor_reward IS NOT NULL AND typeof(NEW.output_donor_reward)<>'integer')
 OR (NEW.discount_percent IS NOT NULL AND typeof(NEW.discount_percent)<>'integer')
 OR (NEW.discount_start_at IS NOT NULL AND typeof(NEW.discount_start_at)<>'integer')
 OR (NEW.discount_end_at IS NOT NULL AND typeof(NEW.discount_end_at)<>'integer')
 OR (NEW.discount_enabled IS NOT NULL AND typeof(NEW.discount_enabled)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.created_by_user_id IS NOT NULL AND typeof(NEW.created_by_user_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_reservations_insert_guard BEFORE INSERT ON charity_reservations
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.charity_model_id IS NOT NULL AND typeof(NEW.charity_model_id)<>'integer')
 OR (NEW.discount_percent IS NOT NULL AND typeof(NEW.discount_percent)<>'integer')
 OR (NEW.request_user_price_milli IS NOT NULL AND typeof(NEW.request_user_price_milli)<>'integer')
 OR (NEW.request_donor_reward_milli IS NOT NULL AND typeof(NEW.request_donor_reward_milli)<>'integer')
 OR (NEW.uncached_user_price_milli IS NOT NULL AND typeof(NEW.uncached_user_price_milli)<>'integer')
 OR (NEW.cache_write_user_price_milli IS NOT NULL AND typeof(NEW.cache_write_user_price_milli)<>'integer')
 OR (NEW.cache_read_user_price_milli IS NOT NULL AND typeof(NEW.cache_read_user_price_milli)<>'integer')
 OR (NEW.output_user_price_milli IS NOT NULL AND typeof(NEW.output_user_price_milli)<>'integer')
 OR (NEW.uncached_donor_reward_milli IS NOT NULL AND typeof(NEW.uncached_donor_reward_milli)<>'integer')
 OR (NEW.cache_write_donor_reward_milli IS NOT NULL AND typeof(NEW.cache_write_donor_reward_milli)<>'integer')
 OR (NEW.cache_read_donor_reward_milli IS NOT NULL AND typeof(NEW.cache_read_donor_reward_milli)<>'integer')
 OR (NEW.output_donor_reward_milli IS NOT NULL AND typeof(NEW.output_donor_reward_milli)<>'integer')
 OR (NEW.token_reserve_milli IS NOT NULL AND typeof(NEW.token_reserve_milli)<>'integer')
 OR (NEW.user_reserved_milli IS NOT NULL AND typeof(NEW.user_reserved_milli)<>'integer')
 OR (NEW.original_charge_milli IS NOT NULL AND typeof(NEW.original_charge_milli)<>'integer')
 OR (NEW.user_charge_milli IS NOT NULL AND typeof(NEW.user_charge_milli)<>'integer')
 OR (NEW.usage_uncached_input_tokens IS NOT NULL AND typeof(NEW.usage_uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.usage_output_tokens IS NOT NULL AND typeof(NEW.usage_output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.dispatched_at IS NOT NULL AND typeof(NEW.dispatched_at)<>'integer')
 OR (NEW.finalized_at IS NOT NULL AND typeof(NEW.finalized_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_charity_reservations_update_guard BEFORE UPDATE ON charity_reservations
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.charity_model_id IS NOT NULL AND typeof(NEW.charity_model_id)<>'integer')
 OR (NEW.discount_percent IS NOT NULL AND typeof(NEW.discount_percent)<>'integer')
 OR (NEW.request_user_price_milli IS NOT NULL AND typeof(NEW.request_user_price_milli)<>'integer')
 OR (NEW.request_donor_reward_milli IS NOT NULL AND typeof(NEW.request_donor_reward_milli)<>'integer')
 OR (NEW.uncached_user_price_milli IS NOT NULL AND typeof(NEW.uncached_user_price_milli)<>'integer')
 OR (NEW.cache_write_user_price_milli IS NOT NULL AND typeof(NEW.cache_write_user_price_milli)<>'integer')
 OR (NEW.cache_read_user_price_milli IS NOT NULL AND typeof(NEW.cache_read_user_price_milli)<>'integer')
 OR (NEW.output_user_price_milli IS NOT NULL AND typeof(NEW.output_user_price_milli)<>'integer')
 OR (NEW.uncached_donor_reward_milli IS NOT NULL AND typeof(NEW.uncached_donor_reward_milli)<>'integer')
 OR (NEW.cache_write_donor_reward_milli IS NOT NULL AND typeof(NEW.cache_write_donor_reward_milli)<>'integer')
 OR (NEW.cache_read_donor_reward_milli IS NOT NULL AND typeof(NEW.cache_read_donor_reward_milli)<>'integer')
 OR (NEW.output_donor_reward_milli IS NOT NULL AND typeof(NEW.output_donor_reward_milli)<>'integer')
 OR (NEW.token_reserve_milli IS NOT NULL AND typeof(NEW.token_reserve_milli)<>'integer')
 OR (NEW.user_reserved_milli IS NOT NULL AND typeof(NEW.user_reserved_milli)<>'integer')
 OR (NEW.original_charge_milli IS NOT NULL AND typeof(NEW.original_charge_milli)<>'integer')
 OR (NEW.user_charge_milli IS NOT NULL AND typeof(NEW.user_charge_milli)<>'integer')
 OR (NEW.usage_uncached_input_tokens IS NOT NULL AND typeof(NEW.usage_uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.usage_output_tokens IS NOT NULL AND typeof(NEW.usage_output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.dispatched_at IS NOT NULL AND typeof(NEW.dispatched_at)<>'integer')
 OR (NEW.finalized_at IS NOT NULL AND typeof(NEW.finalized_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_checkins_insert_guard BEFORE INSERT ON checkins
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.award_milli IS NOT NULL AND typeof(NEW.award_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_checkins_update_guard BEFORE UPDATE ON checkins
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.award_milli IS NOT NULL AND typeof(NEW.award_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_config_revisions_insert_guard BEFORE INSERT ON config_revisions
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_config_revisions_update_guard BEFORE UPDATE ON config_revisions
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_accounts_insert_guard BEFORE INSERT ON credit_accounts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.balance_sign IS NOT NULL AND typeof(NEW.balance_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_accounts_update_guard BEFORE UPDATE ON credit_accounts
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.balance_sign IS NOT NULL AND typeof(NEW.balance_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_capacity_insert_guard BEFORE INSERT ON credit_capacity
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.last_ledger_seq IS NOT NULL AND typeof(NEW.last_ledger_seq)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_capacity_update_guard BEFORE UPDATE ON credit_capacity
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.last_ledger_seq IS NOT NULL AND typeof(NEW.last_ledger_seq)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_entries_insert_guard BEFORE INSERT ON credit_entries
WHEN (NEW.line_no IS NOT NULL AND typeof(NEW.line_no)<>'integer')
 OR (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.delta_sign IS NOT NULL AND typeof(NEW.delta_sign)<>'integer')
 OR (NEW.balance_after_sign IS NOT NULL AND typeof(NEW.balance_after_sign)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_entries_update_guard BEFORE UPDATE ON credit_entries
WHEN (NEW.line_no IS NOT NULL AND typeof(NEW.line_no)<>'integer')
 OR (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.delta_sign IS NOT NULL AND typeof(NEW.delta_sign)<>'integer')
 OR (NEW.balance_after_sign IS NOT NULL AND typeof(NEW.balance_after_sign)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_operations_insert_guard BEFORE INSERT ON credit_operations
WHEN (NEW.ledger_seq IS NOT NULL AND typeof(NEW.ledger_seq)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.donation_credit_user_id IS NOT NULL AND typeof(NEW.donation_credit_user_id)<>'integer')
 OR (NEW.donation_credit_delta_sign IS NOT NULL AND typeof(NEW.donation_credit_delta_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_credit_operations_update_guard BEFORE UPDATE ON credit_operations
WHEN (NEW.ledger_seq IS NOT NULL AND typeof(NEW.ledger_seq)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.donation_credit_user_id IS NOT NULL AND typeof(NEW.donation_credit_user_id)<>'integer')
 OR (NEW.donation_credit_delta_sign IS NOT NULL AND typeof(NEW.donation_credit_delta_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_dispatch_claims_insert_guard BEFORE INSERT ON dispatch_claims
WHEN (NEW.attempt_seq IS NOT NULL AND typeof(NEW.attempt_seq)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.secret_ref_id IS NOT NULL AND typeof(NEW.secret_ref_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.streak_generation IS NOT NULL AND typeof(NEW.streak_generation)<>'integer')
 OR (NEW.claim_now IS NOT NULL AND typeof(NEW.claim_now)<>'integer')
 OR (NEW.frozen_price_milli IS NOT NULL AND typeof(NEW.frozen_price_milli)<>'integer')
 OR (NEW.frozen_reward_milli IS NOT NULL AND typeof(NEW.frozen_reward_milli)<>'integer')
 OR (NEW.receiver_user_id IS NOT NULL AND typeof(NEW.receiver_user_id)<>'integer')
 OR (NEW.reserved_price_milli IS NOT NULL AND typeof(NEW.reserved_price_milli)<>'integer')
 OR (NEW.reserved_calls IS NOT NULL AND typeof(NEW.reserved_calls)<>'integer')
 OR (NEW.reserved_tokens IS NOT NULL AND typeof(NEW.reserved_tokens)<>'integer')
 OR (NEW.donor_reward_actual_milli IS NOT NULL AND typeof(NEW.donor_reward_actual_milli)<>'integer')
 OR (NEW.dispatched_at IS NOT NULL AND typeof(NEW.dispatched_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_dispatch_claims_update_guard BEFORE UPDATE ON dispatch_claims
WHEN (NEW.attempt_seq IS NOT NULL AND typeof(NEW.attempt_seq)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.secret_ref_id IS NOT NULL AND typeof(NEW.secret_ref_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.streak_generation IS NOT NULL AND typeof(NEW.streak_generation)<>'integer')
 OR (NEW.claim_now IS NOT NULL AND typeof(NEW.claim_now)<>'integer')
 OR (NEW.frozen_price_milli IS NOT NULL AND typeof(NEW.frozen_price_milli)<>'integer')
 OR (NEW.frozen_reward_milli IS NOT NULL AND typeof(NEW.frozen_reward_milli)<>'integer')
 OR (NEW.receiver_user_id IS NOT NULL AND typeof(NEW.receiver_user_id)<>'integer')
 OR (NEW.reserved_price_milli IS NOT NULL AND typeof(NEW.reserved_price_milli)<>'integer')
 OR (NEW.reserved_calls IS NOT NULL AND typeof(NEW.reserved_calls)<>'integer')
 OR (NEW.reserved_tokens IS NOT NULL AND typeof(NEW.reserved_tokens)<>'integer')
 OR (NEW.donor_reward_actual_milli IS NOT NULL AND typeof(NEW.donor_reward_actual_milli)<>'integer')
 OR (NEW.dispatched_at IS NOT NULL AND typeof(NEW.dispatched_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_key_memberships_insert_guard BEFORE INSERT ON donation_key_memberships
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_key_memberships_update_guard BEFORE UPDATE ON donation_key_memberships
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_keys_insert_guard BEFORE INSERT ON donation_keys
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.token_reserve IS NOT NULL AND typeof(NEW.token_reserve)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.failure_disabled IS NOT NULL AND typeof(NEW.failure_disabled)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.ended_at IS NOT NULL AND typeof(NEW.ended_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_keys_update_guard BEFORE UPDATE ON donation_keys
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.token_reserve IS NOT NULL AND typeof(NEW.token_reserve)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.failure_disabled IS NOT NULL AND typeof(NEW.failure_disabled)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.ended_at IS NOT NULL AND typeof(NEW.ended_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_reviews_insert_guard BEFORE INSERT ON donation_reviews
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.submission_revision IS NOT NULL AND typeof(NEW.submission_revision)<>'integer')
 OR (NEW.reviewer_user_id IS NOT NULL AND typeof(NEW.reviewer_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_reviews_update_guard BEFORE UPDATE ON donation_reviews
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.donation_id IS NOT NULL AND typeof(NEW.donation_id)<>'integer')
 OR (NEW.submission_revision IS NOT NULL AND typeof(NEW.submission_revision)<>'integer')
 OR (NEW.reviewer_user_id IS NOT NULL AND typeof(NEW.reviewer_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_usage_reservations_insert_guard BEFORE INSERT ON donation_usage_reservations
WHEN (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.price_reserved_milli IS NOT NULL AND typeof(NEW.price_reserved_milli)<>'integer')
 OR (NEW.price_actual_milli IS NOT NULL AND typeof(NEW.price_actual_milli)<>'integer')
 OR (NEW.reward_actual_milli IS NOT NULL AND typeof(NEW.reward_actual_milli)<>'integer')
 OR (NEW.calls_reserved IS NOT NULL AND typeof(NEW.calls_reserved)<>'integer')
 OR (NEW.calls_actual IS NOT NULL AND typeof(NEW.calls_actual)<>'integer')
 OR (NEW.tokens_reserved IS NOT NULL AND typeof(NEW.tokens_reserved)<>'integer')
 OR (NEW.tokens_actual IS NOT NULL AND typeof(NEW.tokens_actual)<>'integer')
 OR (NEW.protocol_success IS NOT NULL AND typeof(NEW.protocol_success)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.finalized_at IS NOT NULL AND typeof(NEW.finalized_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donation_usage_reservations_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN (NEW.donation_key_id IS NOT NULL AND typeof(NEW.donation_key_id)<>'integer')
 OR (NEW.price_reserved_milli IS NOT NULL AND typeof(NEW.price_reserved_milli)<>'integer')
 OR (NEW.price_actual_milli IS NOT NULL AND typeof(NEW.price_actual_milli)<>'integer')
 OR (NEW.reward_actual_milli IS NOT NULL AND typeof(NEW.reward_actual_milli)<>'integer')
 OR (NEW.calls_reserved IS NOT NULL AND typeof(NEW.calls_reserved)<>'integer')
 OR (NEW.calls_actual IS NOT NULL AND typeof(NEW.calls_actual)<>'integer')
 OR (NEW.tokens_reserved IS NOT NULL AND typeof(NEW.tokens_reserved)<>'integer')
 OR (NEW.tokens_actual IS NOT NULL AND typeof(NEW.tokens_actual)<>'integer')
 OR (NEW.protocol_success IS NOT NULL AND typeof(NEW.protocol_success)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.finalized_at IS NOT NULL AND typeof(NEW.finalized_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donations_insert_guard BEFORE INSERT ON donations
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.reviewed_by_user_id IS NOT NULL AND typeof(NEW.reviewed_by_user_id)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.reviewed_at IS NOT NULL AND typeof(NEW.reviewed_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_donations_update_guard BEFORE UPDATE ON donations
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.reviewed_by_user_id IS NOT NULL AND typeof(NEW.reviewed_by_user_id)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.reviewed_at IS NOT NULL AND typeof(NEW.reviewed_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_key_secrets_insert_guard BEFORE INSERT ON endpoint_key_secrets
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.orphaned_at IS NOT NULL AND typeof(NEW.orphaned_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_key_secrets_update_guard BEFORE UPDATE ON endpoint_key_secrets
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.orphaned_at IS NOT NULL AND typeof(NEW.orphaned_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_key_suspensions_insert_guard BEFORE INSERT ON endpoint_key_suspensions
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_key_suspensions_update_guard BEFORE UPDATE ON endpoint_key_suspensions
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_keys_insert_guard BEFORE INSERT ON endpoint_keys
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.endpoint_id IS NOT NULL AND typeof(NEW.endpoint_id)<>'integer')
 OR (NEW.secret_ref_id IS NOT NULL AND typeof(NEW.secret_ref_id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.force_store_false IS NOT NULL AND typeof(NEW.force_store_false)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoint_keys_update_guard BEFORE UPDATE ON endpoint_keys
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.endpoint_id IS NOT NULL AND typeof(NEW.endpoint_id)<>'integer')
 OR (NEW.secret_ref_id IS NOT NULL AND typeof(NEW.secret_ref_id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.force_store_false IS NOT NULL AND typeof(NEW.force_store_false)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoints_insert_guard BEFORE INSERT ON endpoints
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_endpoints_update_guard BEFORE UPDATE ON endpoints
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_batches_insert_guard BEFORE INSERT ON game_fishing_batches
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.unit_price_milli IS NOT NULL AND typeof(NEW.unit_price_milli)<>'integer')
 OR (NEW.entry_total_milli IS NOT NULL AND typeof(NEW.entry_total_milli)<>'integer')
 OR (NEW.payout_total_milli IS NOT NULL AND typeof(NEW.payout_total_milli)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.next_attempt_at IS NOT NULL AND typeof(NEW.next_attempt_at)<>'integer')
 OR (NEW.retry_exhausted IS NOT NULL AND typeof(NEW.retry_exhausted)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.settled_at IS NOT NULL AND typeof(NEW.settled_at)<>'integer')
 OR (NEW.revealed_at IS NOT NULL AND typeof(NEW.revealed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_batches_update_guard BEFORE UPDATE ON game_fishing_batches
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.unit_price_milli IS NOT NULL AND typeof(NEW.unit_price_milli)<>'integer')
 OR (NEW.entry_total_milli IS NOT NULL AND typeof(NEW.entry_total_milli)<>'integer')
 OR (NEW.payout_total_milli IS NOT NULL AND typeof(NEW.payout_total_milli)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.next_attempt_at IS NOT NULL AND typeof(NEW.next_attempt_at)<>'integer')
 OR (NEW.retry_exhausted IS NOT NULL AND typeof(NEW.retry_exhausted)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.settled_at IS NOT NULL AND typeof(NEW.settled_at)<>'integer')
 OR (NEW.revealed_at IS NOT NULL AND typeof(NEW.revealed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_best_insert_guard BEFORE INSERT ON game_fishing_best
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.ordinal IS NOT NULL AND typeof(NEW.ordinal)<>'integer')
 OR (NEW.size_cm IS NOT NULL AND typeof(NEW.size_cm)<>'integer')
 OR (NEW.caught_at IS NOT NULL AND typeof(NEW.caught_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_best_update_guard BEFORE UPDATE ON game_fishing_best
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.ordinal IS NOT NULL AND typeof(NEW.ordinal)<>'integer')
 OR (NEW.size_cm IS NOT NULL AND typeof(NEW.size_cm)<>'integer')
 OR (NEW.caught_at IS NOT NULL AND typeof(NEW.caught_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_outcomes_insert_guard BEFORE INSERT ON game_fishing_outcomes
WHEN (NEW.ordinal IS NOT NULL AND typeof(NEW.ordinal)<>'integer')
 OR (NEW.size_cm IS NOT NULL AND typeof(NEW.size_cm)<>'integer')
 OR (NEW.payout_milli IS NOT NULL AND typeof(NEW.payout_milli)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_outcomes_update_guard BEFORE UPDATE ON game_fishing_outcomes
WHEN (NEW.ordinal IS NOT NULL AND typeof(NEW.ordinal)<>'integer')
 OR (NEW.size_cm IS NOT NULL AND typeof(NEW.size_cm)<>'integer')
 OR (NEW.payout_milli IS NOT NULL AND typeof(NEW.payout_milli)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_rank_aggregates_insert_guard BEFORE INSERT ON game_fishing_rank_aggregates
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.score_achieved_at IS NOT NULL AND typeof(NEW.score_achieved_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_rank_aggregates_update_guard BEFORE UPDATE ON game_fishing_rank_aggregates
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.score_achieved_at IS NOT NULL AND typeof(NEW.score_achieved_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_rank_facts_insert_guard BEFORE INSERT ON game_fishing_rank_facts
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.settled_at IS NOT NULL AND typeof(NEW.settled_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.aggregate_applied IS NOT NULL AND typeof(NEW.aggregate_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_fishing_rank_facts_update_guard BEFORE UPDATE ON game_fishing_rank_facts
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.settled_at IS NOT NULL AND typeof(NEW.settled_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.aggregate_applied IS NOT NULL AND typeof(NEW.aggregate_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_linklink_sessions_insert_guard BEFORE INSERT ON game_linklink_sessions
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.price_milli IS NOT NULL AND typeof(NEW.price_milli)<>'integer')
 OR (NEW.pairs_removed IS NOT NULL AND typeof(NEW.pairs_removed)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_linklink_sessions_update_guard BEFORE UPDATE ON game_linklink_sessions
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.price_milli IS NOT NULL AND typeof(NEW.price_milli)<>'integer')
 OR (NEW.pairs_removed IS NOT NULL AND typeof(NEW.pairs_removed)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_linklink_summaries_insert_guard BEFORE INSERT ON game_linklink_summaries
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.price_milli IS NOT NULL AND typeof(NEW.price_milli)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.pairs_removed IS NOT NULL AND typeof(NEW.pairs_removed)<>'integer')
 OR (NEW.score IS NOT NULL AND typeof(NEW.score)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_linklink_summaries_update_guard BEFORE UPDATE ON game_linklink_summaries
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.price_milli IS NOT NULL AND typeof(NEW.price_milli)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.pairs_removed IS NOT NULL AND typeof(NEW.pairs_removed)<>'integer')
 OR (NEW.score IS NOT NULL AND typeof(NEW.score)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_online_leases_insert_guard BEFORE INSERT ON game_online_leases
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.health_epoch IS NOT NULL AND typeof(NEW.health_epoch)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.last_renewed_at IS NOT NULL AND typeof(NEW.last_renewed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_online_leases_update_guard BEFORE UPDATE ON game_online_leases
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.health_epoch IS NOT NULL AND typeof(NEW.health_epoch)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.last_renewed_at IS NOT NULL AND typeof(NEW.last_renewed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_fun_stats_insert_guard BEFORE INSERT ON game_rps_fun_stats
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_fun_stats_update_guard BEFORE UPDATE ON game_rps_fun_stats
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_pending_results_insert_guard BEFORE INSERT ON game_rps_pending_results
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.own_seat_no IS NOT NULL AND typeof(NEW.own_seat_no)<>'integer')
 OR (NEW.own_wallet_net_sign IS NOT NULL AND typeof(NEW.own_wallet_net_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_pending_results_update_guard BEFORE UPDATE ON game_rps_pending_results
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.own_seat_no IS NOT NULL AND typeof(NEW.own_seat_no)<>'integer')
 OR (NEW.own_wallet_net_sign IS NOT NULL AND typeof(NEW.own_wallet_net_sign)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_queue_insert_guard BEFORE INSERT ON game_rps_queue
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_queue_update_guard BEFORE UPDATE ON game_rps_queue
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_rank_aggregates_insert_guard BEFORE INSERT ON game_rps_rank_aggregates
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.net_profit_sign IS NOT NULL AND typeof(NEW.net_profit_sign)<>'integer')
 OR (NEW.eligible IS NOT NULL AND typeof(NEW.eligible)<>'integer')
 OR (NEW.profit_rate_bp IS NOT NULL AND typeof(NEW.profit_rate_bp)<>'integer')
 OR (NEW.profit_rate_achieved_at IS NOT NULL AND typeof(NEW.profit_rate_achieved_at)<>'integer')
 OR (NEW.net_profit_achieved_at IS NOT NULL AND typeof(NEW.net_profit_achieved_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_rank_aggregates_update_guard BEFORE UPDATE ON game_rps_rank_aggregates
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.net_profit_sign IS NOT NULL AND typeof(NEW.net_profit_sign)<>'integer')
 OR (NEW.eligible IS NOT NULL AND typeof(NEW.eligible)<>'integer')
 OR (NEW.profit_rate_bp IS NOT NULL AND typeof(NEW.profit_rate_bp)<>'integer')
 OR (NEW.profit_rate_achieved_at IS NOT NULL AND typeof(NEW.profit_rate_achieved_at)<>'integer')
 OR (NEW.net_profit_achieved_at IS NOT NULL AND typeof(NEW.net_profit_achieved_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_rank_facts_insert_guard BEFORE INSERT ON game_rps_rank_facts
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
 OR (NEW.profitable IS NOT NULL AND typeof(NEW.profitable)<>'integer')
 OR (NEW.aggregate_applied IS NOT NULL AND typeof(NEW.aggregate_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_rank_facts_update_guard BEFORE UPDATE ON game_rps_rank_facts
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
 OR (NEW.profitable IS NOT NULL AND typeof(NEW.profitable)<>'integer')
 OR (NEW.aggregate_applied IS NOT NULL AND typeof(NEW.aggregate_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_seats_insert_guard BEFORE INSERT ON game_rps_seats
WHEN (NEW.seat_no IS NOT NULL AND typeof(NEW.seat_no)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.current_all_in IS NOT NULL AND typeof(NEW.current_all_in)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
 OR (NEW.stats_applied IS NOT NULL AND typeof(NEW.stats_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_seats_update_guard BEFORE UPDATE ON game_rps_seats
WHEN (NEW.seat_no IS NOT NULL AND typeof(NEW.seat_no)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.current_all_in IS NOT NULL AND typeof(NEW.current_all_in)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
 OR (NEW.stats_applied IS NOT NULL AND typeof(NEW.stats_applied)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_sessions_insert_guard BEFORE INSERT ON game_rps_sessions
WHEN (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.rules_version IS NOT NULL AND typeof(NEW.rules_version)<>'integer')
 OR (NEW.dealer_seat IS NOT NULL AND typeof(NEW.dealer_seat)<>'integer')
 OR (NEW.base_milli IS NOT NULL AND typeof(NEW.base_milli)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.thursday_bp IS NOT NULL AND typeof(NEW.thursday_bp)<>'integer')
 OR (NEW.gesture_seconds IS NOT NULL AND typeof(NEW.gesture_seconds)<>'integer')
 OR (NEW.dealer_seconds IS NOT NULL AND typeof(NEW.dealer_seconds)<>'integer')
 OR (NEW.follower_seconds IS NOT NULL AND typeof(NEW.follower_seconds)<>'integer')
 OR (NEW.phase_deadline IS NOT NULL AND typeof(NEW.phase_deadline)<>'integer')
 OR (NEW.health_epoch IS NOT NULL AND typeof(NEW.health_epoch)<>'integer')
 OR (NEW.recent_event_count IS NOT NULL AND typeof(NEW.recent_event_count)<>'integer')
 OR (NEW.terminal_next_retry_at IS NOT NULL AND typeof(NEW.terminal_next_retry_at)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_sessions_update_guard BEFORE UPDATE ON game_rps_sessions
WHEN (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.rules_version IS NOT NULL AND typeof(NEW.rules_version)<>'integer')
 OR (NEW.dealer_seat IS NOT NULL AND typeof(NEW.dealer_seat)<>'integer')
 OR (NEW.base_milli IS NOT NULL AND typeof(NEW.base_milli)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.thursday_bp IS NOT NULL AND typeof(NEW.thursday_bp)<>'integer')
 OR (NEW.gesture_seconds IS NOT NULL AND typeof(NEW.gesture_seconds)<>'integer')
 OR (NEW.dealer_seconds IS NOT NULL AND typeof(NEW.dealer_seconds)<>'integer')
 OR (NEW.follower_seconds IS NOT NULL AND typeof(NEW.follower_seconds)<>'integer')
 OR (NEW.phase_deadline IS NOT NULL AND typeof(NEW.phase_deadline)<>'integer')
 OR (NEW.health_epoch IS NOT NULL AND typeof(NEW.health_epoch)<>'integer')
 OR (NEW.recent_event_count IS NOT NULL AND typeof(NEW.recent_event_count)<>'integer')
 OR (NEW.terminal_next_retry_at IS NOT NULL AND typeof(NEW.terminal_next_retry_at)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_summaries_insert_guard BEFORE INSERT ON game_rps_summaries
WHEN (NEW.rules_version IS NOT NULL AND typeof(NEW.rules_version)<>'integer')
 OR (NEW.base_milli IS NOT NULL AND typeof(NEW.base_milli)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.thursday_bp IS NOT NULL AND typeof(NEW.thursday_bp)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.delete_at IS NOT NULL AND typeof(NEW.delete_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_summaries_update_guard BEFORE UPDATE ON game_rps_summaries
WHEN (NEW.rules_version IS NOT NULL AND typeof(NEW.rules_version)<>'integer')
 OR (NEW.base_milli IS NOT NULL AND typeof(NEW.base_milli)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.thursday_bp IS NOT NULL AND typeof(NEW.thursday_bp)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.delete_at IS NOT NULL AND typeof(NEW.delete_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_summary_seats_insert_guard BEFORE INSERT ON game_rps_summary_seats
WHEN (NEW.seat_no IS NOT NULL AND typeof(NEW.seat_no)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_summary_seats_update_guard BEFORE UPDATE ON game_rps_summary_seats
WHEN (NEW.seat_no IS NOT NULL AND typeof(NEW.seat_no)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.wallet_net_sign IS NOT NULL AND typeof(NEW.wallet_net_sign)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_user_slots_insert_guard BEFORE INSERT ON game_rps_user_slots
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_rps_user_slots_update_guard BEFORE UPDATE ON game_rps_user_slots
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_user_preferences_insert_guard BEFORE INSERT ON game_user_preferences
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.tutorial_rps_seen IS NOT NULL AND typeof(NEW.tutorial_rps_seen)<>'integer')
 OR (NEW.game_profile_public IS NOT NULL AND typeof(NEW.game_profile_public)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_game_user_preferences_update_guard BEFORE UPDATE ON game_user_preferences
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.tutorial_rps_seen IS NOT NULL AND typeof(NEW.tutorial_rps_seen)<>'integer')
 OR (NEW.game_profile_public IS NOT NULL AND typeof(NEW.game_profile_public)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_idempotency_records_insert_guard BEFORE INSERT ON idempotency_records
WHEN (NEW.http_status IS NOT NULL AND typeof(NEW.http_status)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_idempotency_records_update_guard BEFORE UPDATE ON idempotency_records
WHEN (NEW.http_status IS NOT NULL AND typeof(NEW.http_status)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_hold_audits_insert_guard BEFORE INSERT ON legal_hold_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_hold_audits_update_guard BEFORE UPDATE ON legal_hold_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_hold_read_audits_insert_guard BEFORE INSERT ON legal_hold_read_audits
WHEN (NEW.admin_user_id IS NOT NULL AND typeof(NEW.admin_user_id)<>'integer')
 OR (NEW.first_read_at IS NOT NULL AND typeof(NEW.first_read_at)<>'integer')
 OR (NEW.last_read_at IS NOT NULL AND typeof(NEW.last_read_at)<>'integer')
 OR (NEW.read_count IS NOT NULL AND typeof(NEW.read_count)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_hold_read_audits_update_guard BEFORE UPDATE ON legal_hold_read_audits
WHEN (NEW.admin_user_id IS NOT NULL AND typeof(NEW.admin_user_id)<>'integer')
 OR (NEW.first_read_at IS NOT NULL AND typeof(NEW.first_read_at)<>'integer')
 OR (NEW.last_read_at IS NOT NULL AND typeof(NEW.last_read_at)<>'integer')
 OR (NEW.read_count IS NOT NULL AND typeof(NEW.read_count)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_holds_insert_guard BEFORE INSERT ON legal_holds
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_by_user_id IS NOT NULL AND typeof(NEW.created_by_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.ended_by_user_id IS NOT NULL AND typeof(NEW.ended_by_user_id)<>'integer')
 OR (NEW.ended_at IS NOT NULL AND typeof(NEW.ended_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_legal_holds_update_guard BEFORE UPDATE ON legal_holds
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_by_user_id IS NOT NULL AND typeof(NEW.created_by_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.ended_by_user_id IS NOT NULL AND typeof(NEW.ended_by_user_id)<>'integer')
 OR (NEW.ended_at IS NOT NULL AND typeof(NEW.ended_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_logical_requests_insert_guard BEFORE INSERT ON logical_requests
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.attempt_limit IS NOT NULL AND typeof(NEW.attempt_limit)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.account_reserved_milli IS NOT NULL AND typeof(NEW.account_reserved_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_logical_requests_update_guard BEFORE UPDATE ON logical_requests
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.attempt_limit IS NOT NULL AND typeof(NEW.attempt_limit)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.account_reserved_milli IS NOT NULL AND typeof(NEW.account_reserved_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_maintenance_events_insert_guard BEFORE INSERT ON maintenance_events
WHEN (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.resolved_at IS NOT NULL AND typeof(NEW.resolved_at)<>'integer')
 OR (NEW.deidentify_at IS NOT NULL AND typeof(NEW.deidentify_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_maintenance_events_update_guard BEFORE UPDATE ON maintenance_events
WHEN (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.resolved_at IS NOT NULL AND typeof(NEW.resolved_at)<>'integer')
 OR (NEW.deidentify_at IS NOT NULL AND typeof(NEW.deidentify_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_maintenance_state_insert_guard BEFORE INSERT ON maintenance_state
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.changed_at IS NOT NULL AND typeof(NEW.changed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_maintenance_state_update_guard BEFORE UPDATE ON maintenance_state
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.enabled IS NOT NULL AND typeof(NEW.enabled)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.changed_at IS NOT NULL AND typeof(NEW.changed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_bindings_insert_guard BEFORE INSERT ON model_bindings
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.ord IS NOT NULL AND typeof(NEW.ord)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_bindings_update_guard BEFORE UPDATE ON model_bindings
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.model_id IS NOT NULL AND typeof(NEW.model_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.ord IS NOT NULL AND typeof(NEW.ord)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_catalog_entries_insert_guard BEFORE INSERT ON model_catalog_entries
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.source_revision IS NOT NULL AND typeof(NEW.source_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_catalog_entries_update_guard BEFORE UPDATE ON model_catalog_entries
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.source_revision IS NOT NULL AND typeof(NEW.source_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_discovery_evidence_insert_guard BEFORE INSERT ON model_discovery_evidence
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.fetched_count IS NOT NULL AND typeof(NEW.fetched_count)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_discovery_evidence_update_guard BEFORE UPDATE ON model_discovery_evidence
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.fetched_count IS NOT NULL AND typeof(NEW.fetched_count)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_pair_catalog_insert_guard BEFORE INSERT ON model_pair_catalog
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.automatic_supports IS NOT NULL AND typeof(NEW.automatic_supports)<>'integer')
 OR (NEW.manual_supports IS NOT NULL AND typeof(NEW.manual_supports)<>'integer')
 OR (NEW.automatic_revision IS NOT NULL AND typeof(NEW.automatic_revision)<>'integer')
 OR (NEW.pair_revision IS NOT NULL AND typeof(NEW.pair_revision)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_model_pair_catalog_update_guard BEFORE UPDATE ON model_pair_catalog
WHEN (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.automatic_supports IS NOT NULL AND typeof(NEW.automatic_supports)<>'integer')
 OR (NEW.manual_supports IS NOT NULL AND typeof(NEW.manual_supports)<>'integer')
 OR (NEW.automatic_revision IS NOT NULL AND typeof(NEW.automatic_revision)<>'integer')
 OR (NEW.pair_revision IS NOT NULL AND typeof(NEW.pair_revision)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_models_insert_guard BEFORE INSERT ON models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.silent_retry IS NOT NULL AND typeof(NEW.silent_retry)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_models_update_guard BEFORE UPDATE ON models
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.silent_retry IS NOT NULL AND typeof(NEW.silent_retry)<>'integer')
 OR (NEW.flatten_tool_calls IS NOT NULL AND typeof(NEW.flatten_tool_calls)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.binding_revision IS NOT NULL AND typeof(NEW.binding_revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_policy_audits_insert_guard BEFORE INSERT ON policy_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.resource_id IS NOT NULL AND typeof(NEW.resource_id)<>'integer')
 OR (NEW.old_value IS NOT NULL AND typeof(NEW.old_value)<>'integer')
 OR (NEW.new_value IS NOT NULL AND typeof(NEW.new_value)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_policy_audits_update_guard BEFORE UPDATE ON policy_audits
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.resource_id IS NOT NULL AND typeof(NEW.resource_id)<>'integer')
 OR (NEW.old_value IS NOT NULL AND typeof(NEW.old_value)<>'integer')
 OR (NEW.new_value IS NOT NULL AND typeof(NEW.new_value)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_cases_insert_guard BEFORE INSERT ON report_cases
WHEN (NEW.material_version IS NOT NULL AND typeof(NEW.material_version)<>'integer')
 OR (NEW.target_version IS NOT NULL AND typeof(NEW.target_version)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.material_count IS NOT NULL AND typeof(NEW.material_count)<>'integer')
 OR (NEW.target_count IS NOT NULL AND typeof(NEW.target_count)<>'integer')
 OR (NEW.distinct_owner_count IS NOT NULL AND typeof(NEW.distinct_owner_count)<>'integer')
 OR (NEW.processed_target_count IS NOT NULL AND typeof(NEW.processed_target_count)<>'integer')
 OR (NEW.deleted_target_count IS NOT NULL AND typeof(NEW.deleted_target_count)<>'integer')
 OR (NEW.released_target_count IS NOT NULL AND typeof(NEW.released_target_count)<>'integer')
 OR (NEW.decision_actor_user_id IS NOT NULL AND typeof(NEW.decision_actor_user_id)<>'integer')
 OR (NEW.decision_at IS NOT NULL AND typeof(NEW.decision_at)<>'integer')
 OR (NEW.retry_attempt_count IS NOT NULL AND typeof(NEW.retry_attempt_count)<>'integer')
 OR (NEW.next_retry_at IS NOT NULL AND typeof(NEW.next_retry_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_cases_update_guard BEFORE UPDATE ON report_cases
WHEN (NEW.material_version IS NOT NULL AND typeof(NEW.material_version)<>'integer')
 OR (NEW.target_version IS NOT NULL AND typeof(NEW.target_version)<>'integer')
 OR (NEW.deadline IS NOT NULL AND typeof(NEW.deadline)<>'integer')
 OR (NEW.material_count IS NOT NULL AND typeof(NEW.material_count)<>'integer')
 OR (NEW.target_count IS NOT NULL AND typeof(NEW.target_count)<>'integer')
 OR (NEW.distinct_owner_count IS NOT NULL AND typeof(NEW.distinct_owner_count)<>'integer')
 OR (NEW.processed_target_count IS NOT NULL AND typeof(NEW.processed_target_count)<>'integer')
 OR (NEW.deleted_target_count IS NOT NULL AND typeof(NEW.deleted_target_count)<>'integer')
 OR (NEW.released_target_count IS NOT NULL AND typeof(NEW.released_target_count)<>'integer')
 OR (NEW.decision_actor_user_id IS NOT NULL AND typeof(NEW.decision_actor_user_id)<>'integer')
 OR (NEW.decision_at IS NOT NULL AND typeof(NEW.decision_at)<>'integer')
 OR (NEW.retry_attempt_count IS NOT NULL AND typeof(NEW.retry_attempt_count)<>'integer')
 OR (NEW.next_retry_at IS NOT NULL AND typeof(NEW.next_retry_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_decisions_insert_guard BEFORE INSERT ON report_decisions
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.material_version IS NOT NULL AND typeof(NEW.material_version)<>'integer')
 OR (NEW.target_version IS NOT NULL AND typeof(NEW.target_version)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_decisions_update_guard BEFORE UPDATE ON report_decisions
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.material_version IS NOT NULL AND typeof(NEW.material_version)<>'integer')
 OR (NEW.target_version IS NOT NULL AND typeof(NEW.target_version)<>'integer')
 OR (NEW.actor_user_id IS NOT NULL AND typeof(NEW.actor_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_materials_insert_guard BEFORE INSERT ON report_materials
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.reporter_user_id IS NOT NULL AND typeof(NEW.reporter_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_materials_update_guard BEFORE UPDATE ON report_materials
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.reporter_user_id IS NOT NULL AND typeof(NEW.reporter_user_id)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_rate_buckets_insert_guard BEFORE INSERT ON report_rate_buckets
WHEN (NEW.window_start IS NOT NULL AND typeof(NEW.window_start)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_rate_buckets_update_guard BEFORE UPDATE ON report_rate_buckets
WHEN (NEW.window_start IS NOT NULL AND typeof(NEW.window_start)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_targets_insert_guard BEFORE INSERT ON report_targets
WHEN (NEW.target_seq IS NOT NULL AND typeof(NEW.target_seq)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.owner_user_id IS NOT NULL AND typeof(NEW.owner_user_id)<>'integer')
 OR (NEW.discovered_version IS NOT NULL AND typeof(NEW.discovered_version)<>'integer')
 OR (NEW.decided_version IS NOT NULL AND typeof(NEW.decided_version)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_targets_update_guard BEFORE UPDATE ON report_targets
WHEN (NEW.target_seq IS NOT NULL AND typeof(NEW.target_seq)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.owner_user_id IS NOT NULL AND typeof(NEW.owner_user_id)<>'integer')
 OR (NEW.discovered_version IS NOT NULL AND typeof(NEW.discovered_version)<>'integer')
 OR (NEW.decided_version IS NOT NULL AND typeof(NEW.decided_version)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_attempts_insert_guard BEFORE INSERT ON request_attempts
WHEN (NEW.request_log_id IS NOT NULL AND typeof(NEW.request_log_id)<>'integer')
 OR (NEW.attempt_seq IS NOT NULL AND typeof(NEW.attempt_seq)<>'integer')
 OR (NEW.endpoint_id_snapshot IS NOT NULL AND typeof(NEW.endpoint_id_snapshot)<>'integer')
 OR (NEW.endpoint_key_id_snapshot IS NOT NULL AND typeof(NEW.endpoint_key_id_snapshot)<>'integer')
 OR (NEW.upstream_status IS NOT NULL AND typeof(NEW.upstream_status)<>'integer')
 OR (NEW.input_tokens IS NOT NULL AND typeof(NEW.input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_attempts_update_guard BEFORE UPDATE ON request_attempts
WHEN (NEW.request_log_id IS NOT NULL AND typeof(NEW.request_log_id)<>'integer')
 OR (NEW.attempt_seq IS NOT NULL AND typeof(NEW.attempt_seq)<>'integer')
 OR (NEW.endpoint_id_snapshot IS NOT NULL AND typeof(NEW.endpoint_id_snapshot)<>'integer')
 OR (NEW.endpoint_key_id_snapshot IS NOT NULL AND typeof(NEW.endpoint_key_id_snapshot)<>'integer')
 OR (NEW.upstream_status IS NOT NULL AND typeof(NEW.upstream_status)<>'integer')
 OR (NEW.input_tokens IS NOT NULL AND typeof(NEW.input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_logs_insert_guard BEFORE INSERT ON request_logs
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.status_code IS NOT NULL AND typeof(NEW.status_code)<>'integer')
 OR (NEW.duration_ms IS NOT NULL AND typeof(NEW.duration_ms)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_request_logs_update_guard BEFORE UPDATE ON request_logs
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.caller_status IS NOT NULL AND typeof(NEW.caller_status)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.status_code IS NOT NULL AND typeof(NEW.status_code)<>'integer')
 OR (NEW.duration_ms IS NOT NULL AND typeof(NEW.duration_ms)<>'integer')
 OR (NEW.started_at IS NOT NULL AND typeof(NEW.started_at)<>'integer')
 OR (NEW.completed_at IS NOT NULL AND typeof(NEW.completed_at)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.usage_unknown IS NOT NULL AND typeof(NEW.usage_unknown)<>'integer')
 OR (NEW.legal_hold_consumed IS NOT NULL AND typeof(NEW.legal_hold_consumed)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_sessions_insert_guard BEFORE INSERT ON sessions
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.last_seen_at IS NOT NULL AND typeof(NEW.last_seen_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.absolute_expires_at IS NOT NULL AND typeof(NEW.absolute_expires_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_sessions_update_guard BEFORE UPDATE ON sessions
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.last_seen_at IS NOT NULL AND typeof(NEW.last_seen_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.absolute_expires_at IS NOT NULL AND typeof(NEW.absolute_expires_at)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_shared_pools_insert_guard BEFORE INSERT ON shared_pools
WHEN (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.closed_at IS NOT NULL AND typeof(NEW.closed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_shared_pools_update_guard BEFORE UPDATE ON shared_pools
WHEN (NEW.account_id IS NOT NULL AND typeof(NEW.account_id)<>'integer')
 OR (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.closed_at IS NOT NULL AND typeof(NEW.closed_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_activity_daily_insert_guard BEFORE INSERT ON site_activity_daily
WHEN (NEW.day IS NOT NULL AND typeof(NEW.day)<>'integer')
 OR (NEW.product_active IS NOT NULL AND typeof(NEW.product_active)<>'integer')
 OR (NEW.game_active IS NOT NULL AND typeof(NEW.game_active)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_activity_daily_update_guard BEFORE UPDATE ON site_activity_daily
WHEN (NEW.day IS NOT NULL AND typeof(NEW.day)<>'integer')
 OR (NEW.product_active IS NOT NULL AND typeof(NEW.product_active)<>'integer')
 OR (NEW.game_active IS NOT NULL AND typeof(NEW.game_active)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_config_insert_guard BEFORE INSERT ON site_config
WHEN (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_config_update_guard BEFORE UPDATE ON site_config
WHEN (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_usage_totals_insert_guard BEFORE INSERT ON site_usage_totals
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_site_usage_totals_update_guard BEFORE UPDATE ON site_usage_totals
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_thursday_participants_insert_guard BEFORE INSERT ON thursday_participants
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.eligible_at_freeze IS NOT NULL AND typeof(NEW.eligible_at_freeze)<>'integer')
 OR (NEW.settled IS NOT NULL AND typeof(NEW.settled)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_thursday_participants_update_guard BEFORE UPDATE ON thursday_participants
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.eligible_at_freeze IS NOT NULL AND typeof(NEW.eligible_at_freeze)<>'integer')
 OR (NEW.settled IS NOT NULL AND typeof(NEW.settled)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_thursday_periods_insert_guard BEFORE INSERT ON thursday_periods
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.opens_at IS NOT NULL AND typeof(NEW.opens_at)<>'integer')
 OR (NEW.closes_at IS NOT NULL AND typeof(NEW.closes_at)<>'integer')
 OR (NEW.entry_milli IS NOT NULL AND typeof(NEW.entry_milli)<>'integer')
 OR (NEW.per_user_limit IS NOT NULL AND typeof(NEW.per_user_limit)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.next_pool_bp IS NOT NULL AND typeof(NEW.next_pool_bp)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.started_settlement_at IS NOT NULL AND typeof(NEW.started_settlement_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_thursday_periods_update_guard BEFORE UPDATE ON thursday_periods
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.opens_at IS NOT NULL AND typeof(NEW.opens_at)<>'integer')
 OR (NEW.closes_at IS NOT NULL AND typeof(NEW.closes_at)<>'integer')
 OR (NEW.entry_milli IS NOT NULL AND typeof(NEW.entry_milli)<>'integer')
 OR (NEW.per_user_limit IS NOT NULL AND typeof(NEW.per_user_limit)<>'integer')
 OR (NEW.platform_bp IS NOT NULL AND typeof(NEW.platform_bp)<>'integer')
 OR (NEW.welfare_bp IS NOT NULL AND typeof(NEW.welfare_bp)<>'integer')
 OR (NEW.next_pool_bp IS NOT NULL AND typeof(NEW.next_pool_bp)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.started_settlement_at IS NOT NULL AND typeof(NEW.started_settlement_at)<>'integer')
 OR (NEW.terminal_at IS NOT NULL AND typeof(NEW.terminal_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_activity_daily_insert_guard BEFORE INSERT ON user_activity_daily
WHEN (NEW.day IS NOT NULL AND typeof(NEW.day)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.product_active IS NOT NULL AND typeof(NEW.product_active)<>'integer')
 OR (NEW.api_requests IS NOT NULL AND typeof(NEW.api_requests)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.checkins IS NOT NULL AND typeof(NEW.checkins)<>'integer')
 OR (NEW.console_writes IS NOT NULL AND typeof(NEW.console_writes)<>'integer')
 OR (NEW.game_active IS NOT NULL AND typeof(NEW.game_active)<>'integer')
 OR (NEW.game_rounds IS NOT NULL AND typeof(NEW.game_rounds)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_activity_daily_update_guard BEFORE UPDATE ON user_activity_daily
WHEN (NEW.day IS NOT NULL AND typeof(NEW.day)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.product_active IS NOT NULL AND typeof(NEW.product_active)<>'integer')
 OR (NEW.api_requests IS NOT NULL AND typeof(NEW.api_requests)<>'integer')
 OR (NEW.uncached_input_tokens IS NOT NULL AND typeof(NEW.uncached_input_tokens)<>'integer')
 OR (NEW.cache_write_input_tokens IS NOT NULL AND typeof(NEW.cache_write_input_tokens)<>'integer')
 OR (NEW.cache_read_input_tokens IS NOT NULL AND typeof(NEW.cache_read_input_tokens)<>'integer')
 OR (NEW.output_tokens IS NOT NULL AND typeof(NEW.output_tokens)<>'integer')
 OR (NEW.checkins IS NOT NULL AND typeof(NEW.checkins)<>'integer')
 OR (NEW.console_writes IS NOT NULL AND typeof(NEW.console_writes)<>'integer')
 OR (NEW.game_active IS NOT NULL AND typeof(NEW.game_active)<>'integer')
 OR (NEW.game_rounds IS NOT NULL AND typeof(NEW.game_rounds)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_issue_projection_state_insert_guard BEFORE INSERT ON user_issue_projection_state
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.projection_incomplete IS NOT NULL AND typeof(NEW.projection_incomplete)<>'integer')
 OR (NEW.rebuild_generation IS NOT NULL AND typeof(NEW.rebuild_generation)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_issue_projection_state_update_guard BEFORE UPDATE ON user_issue_projection_state
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.projection_incomplete IS NOT NULL AND typeof(NEW.projection_incomplete)<>'integer')
 OR (NEW.rebuild_generation IS NOT NULL AND typeof(NEW.rebuild_generation)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_issues_insert_guard BEFORE INSERT ON user_issues
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.first_seen_at IS NOT NULL AND typeof(NEW.first_seen_at)<>'integer')
 OR (NEW.last_seen_at IS NOT NULL AND typeof(NEW.last_seen_at)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.closed_at IS NOT NULL AND typeof(NEW.closed_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_user_issues_update_guard BEFORE UPDATE ON user_issues
WHEN (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.first_seen_at IS NOT NULL AND typeof(NEW.first_seen_at)<>'integer')
 OR (NEW.last_seen_at IS NOT NULL AND typeof(NEW.last_seen_at)<>'integer')
 OR (NEW.count IS NOT NULL AND typeof(NEW.count)<>'integer')
 OR (NEW.closed_at IS NOT NULL AND typeof(NEW.closed_at)<>'integer')
 OR (NEW.retain_until IS NOT NULL AND typeof(NEW.retain_until)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_users_insert_guard BEFORE INSERT ON users
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.is_admin IS NOT NULL AND typeof(NEW.is_admin)<>'integer')
 OR (NEW.is_banned IS NOT NULL AND typeof(NEW.is_banned)<>'integer')
 OR (NEW.banned_until IS NOT NULL AND typeof(NEW.banned_until)<>'integer')
 OR (NEW.auto_banned IS NOT NULL AND typeof(NEW.auto_banned)<>'integer')
 OR (NEW.charity_suspended_until IS NOT NULL AND typeof(NEW.charity_suspended_until)<>'integer')
 OR (NEW.endpoint_limit IS NOT NULL AND typeof(NEW.endpoint_limit)<>'integer')
 OR (NEW.rpm_limit IS NOT NULL AND typeof(NEW.rpm_limit)<>'integer')
 OR (NEW.concurrency_limit IS NOT NULL AND typeof(NEW.concurrency_limit)<>'integer')
 OR (NEW.game_profile_public IS NOT NULL AND typeof(NEW.game_profile_public)<>'integer')
 OR (NEW.level IS NOT NULL AND typeof(NEW.level)<>'integer')
 OR (NEW.auto_level IS NOT NULL AND typeof(NEW.auto_level)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_users_update_guard BEFORE UPDATE ON users
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.is_admin IS NOT NULL AND typeof(NEW.is_admin)<>'integer')
 OR (NEW.is_banned IS NOT NULL AND typeof(NEW.is_banned)<>'integer')
 OR (NEW.banned_until IS NOT NULL AND typeof(NEW.banned_until)<>'integer')
 OR (NEW.auto_banned IS NOT NULL AND typeof(NEW.auto_banned)<>'integer')
 OR (NEW.charity_suspended_until IS NOT NULL AND typeof(NEW.charity_suspended_until)<>'integer')
 OR (NEW.endpoint_limit IS NOT NULL AND typeof(NEW.endpoint_limit)<>'integer')
 OR (NEW.rpm_limit IS NOT NULL AND typeof(NEW.rpm_limit)<>'integer')
 OR (NEW.concurrency_limit IS NOT NULL AND typeof(NEW.concurrency_limit)<>'integer')
 OR (NEW.game_profile_public IS NOT NULL AND typeof(NEW.game_profile_public)<>'integer')
 OR (NEW.level IS NOT NULL AND typeof(NEW.level)<>'integer')
 OR (NEW.auto_level IS NOT NULL AND typeof(NEW.auto_level)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_welfare_claims_insert_guard BEFORE INSERT ON welfare_claims
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.threshold_milli IS NOT NULL AND typeof(NEW.threshold_milli)<>'integer')
 OR (NEW.cap_milli IS NOT NULL AND typeof(NEW.cap_milli)<>'integer')
 OR (NEW.pool_before_milli IS NOT NULL AND typeof(NEW.pool_before_milli)<>'integer')
 OR (NEW.award_milli IS NOT NULL AND typeof(NEW.award_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_welfare_claims_update_guard BEFORE UPDATE ON welfare_claims
WHEN (NEW.id IS NOT NULL AND typeof(NEW.id)<>'integer')
 OR (NEW.user_id IS NOT NULL AND typeof(NEW.user_id)<>'integer')
 OR (NEW.threshold_milli IS NOT NULL AND typeof(NEW.threshold_milli)<>'integer')
 OR (NEW.cap_milli IS NOT NULL AND typeof(NEW.cap_milli)<>'integer')
 OR (NEW.pool_before_milli IS NOT NULL AND typeof(NEW.pool_before_milli)<>'integer')
 OR (NEW.award_milli IS NOT NULL AND typeof(NEW.award_milli)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_worker_checkpoints_insert_guard BEFORE INSERT ON worker_checkpoints
WHEN (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.next_attempt_at IS NOT NULL AND typeof(NEW.next_attempt_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_worker_checkpoints_update_guard BEFORE UPDATE ON worker_checkpoints
WHEN (NEW.generation IS NOT NULL AND typeof(NEW.generation)<>'integer')
 OR (NEW.attempt_count IS NOT NULL AND typeof(NEW.attempt_count)<>'integer')
 OR (NEW.next_attempt_at IS NOT NULL AND typeof(NEW.next_attempt_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
`
