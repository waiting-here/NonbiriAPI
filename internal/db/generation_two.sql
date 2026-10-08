-- Canonical current schema. Historical transitions live in migrations/.
PRAGMA foreign_keys=ON;
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
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), charity_profile_public INTEGER NOT NULL DEFAULT 0 CHECK(typeof(charity_profile_public)='integer' AND charity_profile_public IN (0,1)), donation_credit_achieved_at INTEGER CHECK(donation_credit_achieved_at IS NULL OR (typeof(donation_credit_achieved_at)='integer' AND donation_credit_achieved_at BETWEEN 0 AND 253402300799)), donation_credit_achieved_seq BLOB CHECK(donation_credit_achieved_seq IS NULL OR (typeof(donation_credit_achieved_seq)='blob' AND length(donation_credit_achieved_seq)=16 AND donation_credit_achieved_seq>X'00000000000000000000000000000000')) CHECK((donation_credit_achieved_at IS NULL)=(donation_credit_achieved_seq IS NULL)), ban_kind TEXT NOT NULL DEFAULT '' CHECK(ban_kind='' OR (ban_kind='protective_inactivity' AND is_admin=0 AND is_banned=1 AND banned_until IS NULL)), discord_gate_policy TEXT NOT NULL DEFAULT 'inherit' CHECK(discord_gate_policy IN ('inherit','require','exempt') AND (is_admin=0 OR discord_gate_policy='inherit')),
 CHECK(endpoint_limit IS NULL OR endpoint_limit BETWEEN 0 AND 10000),
 CHECK(rpm_limit IS NULL OR rpm_limit BETWEEN 1 AND 4096),
 CHECK(concurrency_limit IS NULL OR concurrency_limit BETWEEN 1 AND 100000),
 CHECK(level IS NULL OR level BETWEEN 1 AND 6)
);
CREATE INDEX idx_users_created ON users(created_at);
CREATE UNIQUE INDEX idx_users_one_admin ON users(is_admin) WHERE is_admin=1;
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
 actor_role TEXT NOT NULL CHECK(actor_role IN ('owner','admin','level5','level6','trainee5')),
 resource_type TEXT NOT NULL CHECK(resource_type IN ('endpoint_key','model','charity_model')),
 resource_id INTEGER NOT NULL CHECK(resource_id>0),
 policy TEXT NOT NULL CHECK(policy IN ('force_store_false','flatten_tool_calls','role_policy')),
 old_value INTEGER CHECK(old_value IN (0,1)),
 new_value INTEGER CHECK(new_value IN (0,1)),
 created_at INTEGER NOT NULL
, from_revision INTEGER CHECK(from_revision IS NULL OR (typeof(from_revision)='integer' AND from_revision>=0)), to_revision INTEGER CHECK(to_revision IS NULL OR (typeof(to_revision)='integer' AND to_revision>=1)));
CREATE INDEX idx_policy_audits_resource ON policy_audits(resource_type,resource_id,id);
CREATE INDEX idx_policy_audits_actor ON policy_audits(actor_user_id,id);
CREATE TRIGGER policy_audits_no_delete BEFORE DELETE ON policy_audits
BEGIN SELECT RAISE(ABORT,'policy_audits is append-only'); END;
CREATE TABLE admin_alerts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL CHECK(kind IN ('fetch_failed','forward_error','registration_rejected','maintenance_enabled','donation_failure_disabled','issue_projection_incomplete','report_retry_exhausted','fishing_retry_exhausted','rps_terminal_retrying','worker_checkpoint_failed','invariant_violation','account_deleted')),
 message TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 subject_user_id INTEGER,
 created_at INTEGER NOT NULL,
 resolved INTEGER NOT NULL DEFAULT 0 CHECK(resolved IN (0,1)),
 resolved_at INTEGER
, context_version INTEGER NOT NULL DEFAULT 0 CHECK(context_version IN (0,1)), context_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(context_json) AND json_type(context_json)='object' AND length(CAST(context_json AS BLOB))<=16384), resolution_kind TEXT NOT NULL DEFAULT '' CHECK(resolution_kind IN ('','manual','automatic_blacklist','worker_recovered','legacy')));
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
 updated_at INTEGER NOT NULL, game_checkins INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_checkins)='integer' AND game_checkins BETWEEN 0 AND 9223372036854775807),
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
, game_checkins BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_checkins)='blob' AND length(game_checkins)=16));
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
CREATE TABLE mainstream_channels (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='mch_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 name TEXT NOT NULL CHECK(typeof(name)='text' AND length(name) BETWEEN 1 AND 128),
 category TEXT NOT NULL CHECK(category IN ('subscription','api_platform')),
 connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')),
 canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('active','retired')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 retired_at INTEGER CHECK(retired_at IS NULL OR retired_at BETWEEN 0 AND 253402300799),
 CHECK((state='active' AND retired_at IS NULL) OR (state='retired' AND enabled=0 AND retired_at IS NOT NULL))
);
CREATE INDEX idx_mainstream_channels_admin_cursor ON mainstream_channels(state,updated_at,id);
CREATE INDEX idx_mainstream_channels_options ON mainstream_channels(state,enabled,name,id);
CREATE TRIGGER mainstream_channels_retire_terminal_guard BEFORE UPDATE OF state ON mainstream_channels
WHEN OLD.state='retired' AND NEW.state<>'retired'
BEGIN SELECT RAISE(ABORT,'retired mainstream channel cannot recover'); END;
CREATE TRIGGER mainstream_channels_active_enabled_limit_guard BEFORE INSERT ON mainstream_channels
WHEN NEW.state='active' AND NEW.enabled=1
 AND (SELECT COUNT(*) FROM mainstream_channels WHERE state='active' AND enabled=1)>=100
BEGIN SELECT RAISE(ABORT,'active mainstream channel limit exceeded'); END;
CREATE TRIGGER mainstream_channels_active_enabled_limit_update_guard BEFORE UPDATE OF state,enabled ON mainstream_channels
WHEN NOT (OLD.state='active' AND OLD.enabled=1) AND NEW.state='active' AND NEW.enabled=1
 AND (SELECT COUNT(*) FROM mainstream_channels WHERE state='active' AND enabled=1 AND id<>NEW.id)>=100
BEGIN SELECT RAISE(ABORT,'active mainstream channel limit exceeded'); END;
CREATE TRIGGER generation_two_integer_type_mainstream_channels_insert_guard BEFORE INSERT ON mainstream_channels
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.retired_at IS NOT NULL AND typeof(NEW.retired_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_mainstream_channels_update_guard BEFORE UPDATE ON mainstream_channels
WHEN (NEW.revision IS NOT NULL AND typeof(NEW.revision)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
 OR (NEW.retired_at IS NOT NULL AND typeof(NEW.retired_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
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
 connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')),
 base_url TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807),
 mainstream_channel_id TEXT REFERENCES mainstream_channels(id) ON DELETE RESTRICT
   CHECK(mainstream_channel_id IS NULL OR (length(mainstream_channel_id)=26 AND substr(mainstream_channel_id,1,4)='mch_' AND substr(mainstream_channel_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(mainstream_channel_id,-1,1) IN ('A','Q','g','w'))),
 mainstream_channel_revision INTEGER CHECK(mainstream_channel_revision IS NULL OR (typeof(mainstream_channel_revision)='integer' AND mainstream_channel_revision BETWEEN 1 AND 9223372036854775807)),
 mainstream_channel_name TEXT CHECK(mainstream_channel_name IS NULL OR (typeof(mainstream_channel_name)='text' AND length(mainstream_channel_name) BETWEEN 1 AND 128)),
 mainstream_channel_category TEXT CHECK(mainstream_channel_category IS NULL OR mainstream_channel_category IN ('subscription','api_platform')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(id,user_id),
 CHECK((mainstream_channel_id IS NULL AND mainstream_channel_revision IS NULL AND mainstream_channel_name IS NULL AND mainstream_channel_category IS NULL)
   OR (mainstream_channel_id IS NOT NULL AND mainstream_channel_revision IS NOT NULL AND mainstream_channel_name IS NOT NULL AND mainstream_channel_category IS NOT NULL))
);
CREATE INDEX idx_endpoints_user_cursor ON endpoints(user_id,updated_at,id);
CREATE INDEX idx_endpoints_user_base ON endpoints(user_id,base_url,id);
CREATE TRIGGER endpoints_identity_immutable BEFORE UPDATE OF connector_type,base_url ON endpoints
WHEN OLD.connector_type<>NEW.connector_type OR OLD.base_url<>NEW.base_url
BEGIN SELECT RAISE(ABORT,'endpoint identity is immutable'); END;
CREATE TRIGGER endpoints_channel_snapshot_immutable BEFORE UPDATE OF mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category ON endpoints
WHEN NEW.mainstream_channel_id IS NOT OLD.mainstream_channel_id
 OR NEW.mainstream_channel_revision IS NOT OLD.mainstream_channel_revision
 OR NEW.mainstream_channel_name IS NOT OLD.mainstream_channel_name
 OR NEW.mainstream_channel_category IS NOT OLD.mainstream_channel_category
BEGIN SELECT RAISE(ABORT,'endpoint channel provenance is immutable'); END;
CREATE TABLE endpoint_key_secrets (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 context_id BLOB NOT NULL UNIQUE CHECK(typeof(context_id)='blob' AND length(context_id)=16),
 canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096),
 connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')),
 encrypted_secret TEXT NOT NULL CHECK(typeof(encrypted_secret)='text' AND length(CAST(encrypted_secret AS BLOB)) BETWEEN 1 AND 131072),
 created_at INTEGER NOT NULL,
 orphaned_at INTEGER CHECK(orphaned_at IS NULL OR orphaned_at BETWEEN 0 AND 253402300799)
, key_body_review_hmac BLOB CHECK(key_body_review_hmac IS NULL OR (typeof(key_body_review_hmac)='blob' AND length(key_body_review_hmac)=32)));
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
 updated_at INTEGER NOT NULL, role_policy TEXT NOT NULL DEFAULT '{"default_action":"native","rules":{}}' CHECK(typeof(role_policy)='text' AND length(CAST(role_policy AS BLOB))<=8192 AND json_valid(role_policy) AND json_type(role_policy)='object' AND COALESCE(json_type(role_policy,'$.default_action'),'')='text' AND json_extract(role_policy,'$.default_action') IN ('native','passthrough','system','user','assistant','reject') AND COALESCE(json_type(role_policy,'$.rules'),'')='object'), transport_rule TEXT NOT NULL DEFAULT 'passthrough' CHECK(transport_rule IN ('passthrough','force_non_stream','force_stream')), model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7),
 UNIQUE(user_id,full_name), UNIQUE(id,user_id), CHECK(full_name=provider||'/'||model)
);
CREATE INDEX idx_models_user ON models(user_id);
CREATE INDEX idx_models_user_fullname ON models(user_id,full_name);
CREATE TABLE model_bindings (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER NOT NULL,
 upstream_model_id TEXT NOT NULL,
 ord INTEGER NOT NULL DEFAULT 0 CHECK(ord BETWEEN 0 AND 511),
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
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807), binding_revision INTEGER NOT NULL DEFAULT 0 CHECK(binding_revision BETWEEN 0 AND 9223372036854775807), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, is_mainstream INTEGER NOT NULL DEFAULT 0 CHECK(is_mainstream IN (0,1)), excluded_request_fields TEXT NOT NULL DEFAULT '[]'
 CHECK(json_valid(excluded_request_fields) AND json_type(excluded_request_fields)='array' AND length(excluded_request_fields)<=2200), role_policy TEXT NOT NULL DEFAULT '{"default_action":"native","rules":{}}' CHECK(typeof(role_policy)='text' AND length(CAST(role_policy AS BLOB))<=8192 AND json_valid(role_policy) AND json_type(role_policy)='object' AND COALESCE(json_type(role_policy,'$.default_action'),'')='text' AND json_extract(role_policy,'$.default_action') IN ('native','passthrough','system','user','assistant','reject') AND COALESCE(json_type(role_policy,'$.rules'),'')='object'), transport_rule TEXT NOT NULL DEFAULT 'passthrough' CHECK(transport_rule IN ('passthrough','force_non_stream','force_stream')), model_types INTEGER NOT NULL DEFAULT 1 CHECK(typeof(model_types)='integer' AND model_types BETWEEN 1 AND 7),
 CHECK(full_name='[公益]'||provider||'/'||model), CHECK(discount_end_at IS NULL OR discount_start_at IS NULL OR discount_end_at>=discount_start_at)
);
CREATE TABLE donation_keys (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
 display_head TEXT NOT NULL DEFAULT '', display_tail TEXT NOT NULL DEFAULT '', canonical_base_url TEXT NOT NULL DEFAULT '',
 connector_type TEXT NOT NULL DEFAULT 'openai-compatible' CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')),
 price_limit_mag BLOB CHECK(price_limit_mag IS NULL OR (typeof(price_limit_mag)='blob' AND length(price_limit_mag)=16)), call_limit_mag BLOB CHECK(call_limit_mag IS NULL OR (typeof(call_limit_mag)='blob' AND length(call_limit_mag)=16)), token_limit_mag BLOB CHECK(token_limit_mag IS NULL OR (typeof(token_limit_mag)='blob' AND length(token_limit_mag)=16)),
 price_used_mag BLOB NOT NULL CHECK(typeof(price_used_mag)='blob' AND length(price_used_mag)=16), price_reserved_mag BLOB NOT NULL CHECK(typeof(price_reserved_mag)='blob' AND length(price_reserved_mag)=16), calls_used BLOB NOT NULL CHECK(typeof(calls_used)='blob' AND length(calls_used)=16), calls_reserved BLOB NOT NULL CHECK(typeof(calls_reserved)='blob' AND length(calls_reserved)=16), tokens_used BLOB NOT NULL CHECK(typeof(tokens_used)='blob' AND length(tokens_used)=16), tokens_reserved BLOB NOT NULL CHECK(typeof(tokens_reserved)='blob' AND length(tokens_reserved)=16),
 token_reserve INTEGER NOT NULL DEFAULT 0 CHECK(token_reserve BETWEEN 0 AND 2147483647), enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)), failure_disabled INTEGER NOT NULL DEFAULT 0 CHECK(failure_disabled IN (0,1)), failure_streak BLOB NOT NULL CHECK(typeof(failure_streak)='blob' AND length(failure_streak)=16), streak_generation BLOB NOT NULL CHECK(typeof(streak_generation)='blob' AND length(streak_generation)=16 AND hex(streak_generation)<>'00000000000000000000000000000000'), next_claim_seq BLOB NOT NULL CHECK(typeof(next_claim_seq)='blob' AND length(next_claim_seq)=16), next_fold_seq BLOB NOT NULL CHECK(typeof(next_fold_seq)='blob' AND length(next_fold_seq)=16 AND hex(next_fold_seq)<=hex(next_claim_seq)), safe_note TEXT NOT NULL DEFAULT '', ended_reason TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, ended_at INTEGER,
 authorized_expires_at INTEGER CHECK(authorized_expires_at IS NULL OR (typeof(authorized_expires_at)='integer' AND authorized_expires_at BETWEEN 0 AND 253402300799)),
 expires_at INTEGER CHECK(expires_at IS NULL OR (typeof(expires_at)='integer' AND expires_at BETWEEN 0 AND 253402300799)),
 mainstream_channel_id TEXT REFERENCES mainstream_channels(id) ON DELETE RESTRICT
   CHECK(mainstream_channel_id IS NULL OR (length(mainstream_channel_id)=26 AND substr(mainstream_channel_id,1,4)='mch_' AND substr(mainstream_channel_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(mainstream_channel_id,-1,1) IN ('A','Q','g','w'))),
 mainstream_channel_revision INTEGER CHECK(mainstream_channel_revision IS NULL OR (typeof(mainstream_channel_revision)='integer' AND mainstream_channel_revision BETWEEN 1 AND 9223372036854775807)),
 mainstream_channel_name TEXT CHECK(mainstream_channel_name IS NULL OR (typeof(mainstream_channel_name)='text' AND length(mainstream_channel_name) BETWEEN 1 AND 128)),
 mainstream_channel_category TEXT CHECK(mainstream_channel_category IS NULL OR mainstream_channel_category IN ('subscription','api_platform')),
 source_endpoint_key_id INTEGER NOT NULL CHECK(typeof(source_endpoint_key_id)='integer' AND source_endpoint_key_id>0),
 report_fingerprint BLOB CHECK(report_fingerprint IS NULL OR (typeof(report_fingerprint)='blob' AND length(report_fingerprint)=32)),
 report_match_until INTEGER CHECK(report_match_until IS NULL OR (typeof(report_match_until)='integer' AND report_match_until BETWEEN 0 AND 253402300799)), failure_disable_threshold TEXT NOT NULL DEFAULT '10' CHECK(typeof(failure_disable_threshold)='text' AND instr(failure_disable_threshold,char(0))=0 AND (failure_disable_threshold='0' OR (length(failure_disable_threshold) BETWEEN 1 AND 39 AND failure_disable_threshold NOT GLOB '*[^0-9]*' AND substr(failure_disable_threshold,1,1) BETWEEN '1' AND '9' AND (length(failure_disable_threshold)<39 OR failure_disable_threshold<='340282366920938463463374607431768211455')))), input_token_limit_mag BLOB CHECK(input_token_limit_mag IS NULL OR (typeof(input_token_limit_mag)='blob' AND length(input_token_limit_mag)=16 AND hex(input_token_limit_mag)<='00000000000000007FFFFFFFFFFFFFFF')), output_token_limit_mag BLOB CHECK(output_token_limit_mag IS NULL OR (typeof(output_token_limit_mag)='blob' AND length(output_token_limit_mag)=16 AND hex(output_token_limit_mag)<='00000000000000007FFFFFFFFFFFFFFF')), input_token_reserve INTEGER CHECK(input_token_reserve IS NULL OR (typeof(input_token_reserve)='integer' AND input_token_reserve>=0)), output_token_reserve INTEGER CHECK(output_token_reserve IS NULL OR (typeof(output_token_reserve)='integer' AND output_token_reserve>=0)), input_tokens_used BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(input_tokens_used)='blob' AND length(input_tokens_used)=16), output_tokens_used BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(output_tokens_used)='blob' AND length(output_tokens_used)=16), input_tokens_reserved BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(input_tokens_reserved)='blob' AND length(input_tokens_reserved)=16), output_tokens_reserved BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(output_tokens_reserved)='blob' AND length(output_tokens_reserved)=16), unattributed_total_tokens BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(unattributed_total_tokens)='blob' AND length(unattributed_total_tokens)=16), breakdown_started_at INTEGER NOT NULL DEFAULT 0 CHECK(typeof(breakdown_started_at)='integer' AND breakdown_started_at BETWEEN 0 AND 253402300799), key_body_review_hmac BLOB CHECK(key_body_review_hmac IS NULL OR (typeof(key_body_review_hmac)='blob' AND length(key_body_review_hmac)=32)), review_revision INTEGER CHECK(review_revision IS NULL OR (typeof(review_revision)='integer' AND review_revision>=1)), manual_catalog_revision INTEGER NOT NULL DEFAULT 1 CHECK(typeof(manual_catalog_revision)='integer' AND manual_catalog_revision>=1),
 UNIQUE(id,donation_id), CHECK(typeof(display_head)='text' AND typeof(display_tail)='text' AND typeof(canonical_base_url)='text' AND typeof(safe_note)='text'), CHECK(length(CAST(display_head AS BLOB)) BETWEEN 0 AND 16 AND length(CAST(display_tail AS BLOB)) BETWEEN 0 AND 16 AND display_head NOT GLOB '*[^ -~]*' AND display_tail NOT GLOB '*[^ -~]*'), CHECK(length(CAST(canonical_base_url AS BLOB)) BETWEEN 0 AND 4096), CHECK(length(safe_note) BETWEEN 0 AND 256), CHECK(ended_reason IS NULL OR ended_reason IN ('withdrawn','terminated','expired','member_removed','account_deleted')),
 CHECK(endpoint_key_id IS NOT NULL OR ended_at IS NOT NULL),
 CHECK(endpoint_key_id IS NULL OR endpoint_key_id=source_endpoint_key_id),
 CHECK((mainstream_channel_id IS NULL AND mainstream_channel_revision IS NULL AND mainstream_channel_name IS NULL AND mainstream_channel_category IS NULL)
   OR (mainstream_channel_id IS NOT NULL AND mainstream_channel_revision IS NOT NULL AND mainstream_channel_name IS NOT NULL AND mainstream_channel_category IS NOT NULL)),
 CHECK(authorized_expires_at IS NULL OR (expires_at IS NOT NULL AND expires_at<=authorized_expires_at)),
 CHECK((ended_at IS NULL AND ended_reason IS NULL AND report_fingerprint IS NOT NULL AND report_match_until IS NULL)
   OR (ended_at IS NOT NULL AND ended_reason IS NOT NULL AND enabled=0 AND report_match_until IS NOT NULL AND report_match_until=ended_at+7776000))
);
CREATE INDEX idx_donation_keys_donation ON donation_keys(donation_id);
CREATE INDEX idx_donation_keys_route ON donation_keys(enabled,failure_disabled,ended_at,id);
CREATE UNIQUE INDEX idx_donation_keys_donation_endpoint ON donation_keys(donation_id,endpoint_key_id) WHERE endpoint_key_id IS NOT NULL;
CREATE INDEX idx_donation_keys_source_key ON donation_keys(source_endpoint_key_id,id);
CREATE INDEX idx_donation_keys_report_fingerprint ON donation_keys(report_fingerprint) WHERE report_fingerprint IS NOT NULL;
CREATE INDEX idx_donation_keys_report_match ON donation_keys(report_match_until,id) WHERE report_match_until IS NOT NULL;
CREATE TRIGGER donation_key_initial_expiry_guard BEFORE INSERT ON donation_keys
WHEN NEW.authorized_expires_at IS NOT NEW.expires_at
BEGIN SELECT RAISE(ABORT,'donation key initial expiry must equal donor authorization'); END;
CREATE TRIGGER donation_key_donor_expiry_immutable BEFORE UPDATE OF authorized_expires_at ON donation_keys
WHEN NEW.authorized_expires_at IS NOT OLD.authorized_expires_at
BEGIN SELECT RAISE(ABORT,'donation donor expiry authorization is immutable'); END;
CREATE TRIGGER donation_key_provenance_immutable BEFORE UPDATE OF donation_id,display_head,display_tail,canonical_base_url,connector_type,mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category ON donation_keys
WHEN NEW.donation_id IS NOT OLD.donation_id
 OR NEW.display_head IS NOT OLD.display_head
 OR NEW.display_tail IS NOT OLD.display_tail
 OR NEW.canonical_base_url IS NOT OLD.canonical_base_url
 OR NEW.connector_type IS NOT OLD.connector_type
 OR NEW.mainstream_channel_id IS NOT OLD.mainstream_channel_id
 OR NEW.mainstream_channel_revision IS NOT OLD.mainstream_channel_revision
 OR NEW.mainstream_channel_name IS NOT OLD.mainstream_channel_name
 OR NEW.mainstream_channel_category IS NOT OLD.mainstream_channel_category
BEGIN SELECT RAISE(ABORT,'donation key provenance is immutable'); END;
CREATE TRIGGER donation_key_source_key_immutable BEFORE UPDATE OF source_endpoint_key_id ON donation_keys
WHEN NEW.source_endpoint_key_id IS NOT OLD.source_endpoint_key_id
BEGIN SELECT RAISE(ABORT,'donation key source endpoint key is immutable'); END;
CREATE TRIGGER donation_key_tombstone_immutable BEFORE UPDATE OF endpoint_key_id ON donation_keys
WHEN OLD.endpoint_key_id IS NULL AND NEW.endpoint_key_id IS NOT NULL
BEGIN SELECT RAISE(ABORT,'donation key tombstone cannot recover a physical key'); END;
CREATE TRIGGER donation_key_terminal_immutable BEFORE UPDATE OF ended_at,ended_reason,report_match_until,expires_at ON donation_keys
WHEN (OLD.ended_at IS NULL AND NOT (
       (NEW.ended_at IS NULL AND NEW.ended_reason IS NULL AND NEW.report_match_until IS NULL)
       OR (NEW.ended_at IS NOT NULL AND NEW.ended_reason IS NOT NULL
           AND NEW.report_match_until=NEW.ended_at+7776000 AND NEW.expires_at IS OLD.expires_at)))
 OR (OLD.ended_at IS NOT NULL AND (
       NEW.ended_at IS NOT OLD.ended_at OR NEW.ended_reason IS NOT OLD.ended_reason
       OR NEW.report_match_until IS NOT OLD.report_match_until OR NEW.expires_at IS NOT OLD.expires_at))
BEGIN SELECT RAISE(ABORT,'donation key terminal facts are immutable'); END;
CREATE TRIGGER donation_key_report_fingerprint_insert_guard BEFORE INSERT ON donation_keys
WHEN NEW.ended_at IS NOT NULL AND NEW.report_fingerprint IS NULL
BEGIN SELECT RAISE(ABORT,'donation key terminal fingerprint must be retained on insert'); END;
CREATE TRIGGER donation_key_report_fingerprint_update_guard BEFORE UPDATE OF report_fingerprint ON donation_keys
WHEN NEW.report_fingerprint IS NOT OLD.report_fingerprint AND NOT (
 OLD.report_fingerprint IS NOT NULL AND NEW.report_fingerprint IS NULL
 AND OLD.ended_at IS NOT NULL AND OLD.report_match_until IS NOT NULL
 AND NEW.updated_at>=OLD.report_match_until)
BEGIN SELECT RAISE(ABORT,'donation key report fingerprint mutation is not allowed'); END;
CREATE TABLE charity_model_bindings (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), charity_model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE, donation_key_id INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE, endpoint_key_id INTEGER NOT NULL, upstream_model_id TEXT NOT NULL, ord INTEGER NOT NULL DEFAULT 0 CHECK(ord BETWEEN 0 AND 511), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(charity_model_id,donation_key_id,upstream_model_id), UNIQUE(charity_model_id,ord), FOREIGN KEY(endpoint_key_id) REFERENCES endpoint_keys(id) ON DELETE CASCADE
);
CREATE INDEX idx_charity_bindings_model ON charity_model_bindings(charity_model_id,ord,id);
CREATE TABLE charity_model_stats (model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE, next_slot INTEGER NOT NULL DEFAULT 0 CHECK(next_slot BETWEEN 0 AND 99), sample_count INTEGER NOT NULL DEFAULT 0 CHECK(sample_count BETWEEN 0 AND 100), success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count BETWEEN 0 AND 100)) STRICT;
CREATE TABLE charity_model_outcomes (model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE, slot INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 99), success INTEGER NOT NULL CHECK(success IN (0,1)), created_at INTEGER NOT NULL, PRIMARY KEY(model_id,slot));
CREATE TABLE idempotency_records (
 scope TEXT NOT NULL CHECK(scope IN ('credential_report','control_mutation','openai_chat_completions','charity_chat_completions','model_discovery','maintenance','announcement','activity','game_fishing','game_linklink','game_rps','game_bidding','game_likes','game_gwent','game_catch','game_blackjack','activity_loan','donation','lake_notes','personal_automation')), actor_scope_hash BLOB NOT NULL CHECK(typeof(actor_scope_hash)='blob' AND length(actor_scope_hash)=32), key_hash BLOB NOT NULL CHECK(typeof(key_hash)='blob' AND length(key_hash)=32), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), lookup_fingerprint BLOB CHECK(lookup_fingerprint IS NULL OR (typeof(lookup_fingerprint)='blob' AND length(lookup_fingerprint)=32)), state TEXT NOT NULL CHECK(state IN ('accepted','completed')), http_status INTEGER NOT NULL CHECK(http_status=0 OR http_status BETWEEN 100 AND 599), response_body BLOB NOT NULL CHECK(typeof(response_body)='blob' AND length(response_body)<=65536), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799), PRIMARY KEY(scope,actor_scope_hash,key_hash), CHECK(expires_at>=created_at), CHECK((scope='credential_report' AND lookup_fingerprint IS NOT NULL AND expires_at=created_at+86400) OR (scope<>'credential_report' AND lookup_fingerprint IS NULL)), CHECK((state='accepted' AND http_status=0 AND length(response_body)=0) OR (state='completed' AND http_status BETWEEN 100 AND 599))
);
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);
CREATE TABLE logical_requests (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='req_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 route_kind TEXT NOT NULL CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings','openai_images_generations','charity_images_generations')),
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
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799), rejection_stage TEXT CHECK(rejection_stage IS NULL OR rejection_stage IN ('authorization','flow','preflight')), rejection_reason TEXT CHECK(rejection_reason IS NULL OR rejection_reason IN ('unauthorized','forbidden','charity_suspended','feature_disabled','maintenance','invalid_request','not_found','unbound_model','insufficient_credits','user_rpm','global_rpm','shared_rpm','concurrency','content_too_short','payload_too_large','resource_limit_exceeded','service_unavailable')), request_method TEXT CHECK(request_method IS NULL OR request_method IN ('GET','POST')), request_path TEXT CHECK(request_path IS NULL OR request_path IN ('/v1/models','/v1/chat/completions','/v1/embeddings','/v1/images/generations')),
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
 reserved_tokens INTEGER NOT NULL DEFAULT 0 CHECK(reserved_tokens BETWEEN 0 AND 9223372036854775807),
 donor_reward_actual_milli INTEGER CHECK(donor_reward_actual_milli IS NULL OR donor_reward_actual_milli BETWEEN 0 AND 9000000000000000),
 donor_reward_state TEXT NOT NULL DEFAULT 'not_applicable' CHECK(donor_reward_state IN ('not_applicable','pending','posted','zero','not_due','receiver_deleted')),
 dispatched_at INTEGER CHECK(dispatched_at IS NULL OR dispatched_at BETWEEN 0 AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN 0 AND 253402300799), reserved_input_tokens INTEGER CHECK(reserved_input_tokens IS NULL OR (typeof(reserved_input_tokens)='integer' AND reserved_input_tokens>=0)), reserved_output_tokens INTEGER CHECK(reserved_output_tokens IS NULL OR (typeof(reserved_output_tokens)='integer' AND reserved_output_tokens>=0)), streak_disposition TEXT CHECK(streak_disposition IS NULL OR streak_disposition IN ('success','upstream_failure','neutral')), failure_origin TEXT CHECK(failure_origin IS NULL OR failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','legacy_unknown','recovery_unknown')),
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
 route_kind TEXT NOT NULL DEFAULT 'openai_chat_completions' CHECK(route_kind IN ('openai_chat_completions','charity_chat_completions','model_discovery','openai_embeddings','charity_embeddings','openai_images_generations','charity_images_generations')),
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
 legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), rejection_stage TEXT CHECK(rejection_stage IS NULL OR rejection_stage IN ('authorization','flow','preflight')), rejection_reason TEXT CHECK(rejection_reason IS NULL OR rejection_reason IN ('unauthorized','forbidden','charity_suspended','feature_disabled','maintenance','invalid_request','not_found','unbound_model','insufficient_credits','user_rpm','global_rpm','shared_rpm','concurrency','content_too_short','payload_too_large','resource_limit_exceeded','service_unavailable')), request_method TEXT CHECK(request_method IS NULL OR request_method IN ('GET','POST')), request_path TEXT CHECK(request_path IS NULL OR request_path IN ('/v1/models','/v1/chat/completions','/v1/embeddings','/v1/images/generations')), usage_total_mismatch INTEGER NOT NULL DEFAULT 0
 CHECK(typeof(usage_total_mismatch)='integer' AND usage_total_mismatch IN (0,1)), origin_user_id INTEGER CHECK(origin_user_id IS NULL OR (typeof(origin_user_id)='integer' AND origin_user_id>0)), origin_discord_id TEXT CHECK(origin_discord_id IS NULL OR (typeof(origin_discord_id)='text' AND length(origin_discord_id) BETWEEN 1 AND 20 AND origin_discord_id NOT GLOB '*[^0-9]*' AND substr(origin_discord_id,1,1) BETWEEN '1' AND '9')),
 CHECK((caller_result_class IS NULL AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NULL AND status_code=0 AND error_code='') OR
       (caller_result_class='success' AND caller_status BETWEEN 200 AND 399 AND caller_error_code IS NULL AND completed_at IS NOT NULL) OR
       (caller_result_class='failed' AND caller_status BETWEEN 400 AND 599 AND caller_error_code IN ('internal','invalid_request','unauthorized','forbidden','not_found','conflict','method_not_allowed','rate_limited','payload_too_large','elevated_required','unbound_model','upstream','maintenance','service_unavailable','resource_limit_exceeded','resource_locked','debug_dry_run_intercepted','debug_live_result_captured','debug_live_cancelled','insufficient_credits','feature_disabled','charity_suspended','content_too_short','already_checked_in','checkin_cap_reached') AND completed_at IS NOT NULL) OR
       (caller_result_class='cancelled' AND caller_status IS NULL AND caller_error_code IS NULL AND completed_at IS NOT NULL)),
 CHECK((caller_result_class IS NULL AND completed_at IS NULL) OR (caller_result_class IS NOT NULL AND completed_at IS NOT NULL))
);
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);
CREATE TABLE request_attempts (
 claim_id TEXT NOT NULL PRIMARY KEY CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')), request_log_id INTEGER NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE, attempt_seq INTEGER NOT NULL CHECK(attempt_seq BETWEEN 1 AND 100), endpoint_id_snapshot INTEGER, endpoint_key_id_snapshot INTEGER, connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')), canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096), upstream_model_id TEXT NOT NULL CHECK(typeof(upstream_model_id)='text' AND length(upstream_model_id) BETWEEN 0 AND 512), result_kind TEXT NOT NULL CHECK(result_kind IN ('response','synthetic')), upstream_status INTEGER CHECK(upstream_status IS NULL OR upstream_status BETWEEN 100 AND 599), upstream_code TEXT, diag TEXT, input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_tokens>=0), cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens>=0), cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens>=0), output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens>=0), usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)), started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799), completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253402300799 AND completed_at>=started_at), usage_total_mismatch INTEGER NOT NULL DEFAULT 0
 CHECK(typeof(usage_total_mismatch)='integer' AND usage_total_mismatch IN (0,1)), UNIQUE(request_log_id,attempt_seq), CHECK(upstream_code IS NULL OR (typeof(upstream_code)='text' AND length(CAST(upstream_code AS BLOB)) BETWEEN 1 AND 64 AND upstream_code NOT GLOB '*[^ -~]*')), CHECK(diag IS NULL OR (typeof(diag)='text' AND length(CAST(diag AS BLOB))<=4096))
);
CREATE INDEX idx_request_attempts_log_seq ON request_attempts(request_log_id,attempt_seq);
CREATE INDEX idx_request_attempts_started ON request_attempts(started_at);
CREATE INDEX idx_request_attempts_retention ON request_attempts(completed_at,request_log_id,attempt_seq) WHERE completed_at IS NOT NULL;
CREATE TABLE worker_checkpoints (worker_key TEXT NOT NULL PRIMARY KEY, cursor_text TEXT NOT NULL DEFAULT '', generation INTEGER NOT NULL CHECK(generation BETWEEN 0 AND 9223372036854775807), attempt_count INTEGER NOT NULL CHECK(attempt_count BETWEEN 0 AND 2147483647), next_attempt_at INTEGER NOT NULL CHECK(next_attempt_at BETWEEN 0 AND 253402300799), last_error_class TEXT NOT NULL DEFAULT '' CHECK(last_error_class IN ('','db_busy','internal_retryable','invariant_violation')), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), last_success_at INTEGER CHECK(last_success_at IS NULL OR (typeof(last_success_at)='integer' AND last_success_at BETWEEN 0 AND 253402300799 AND last_success_at<=updated_at)));
CREATE INDEX idx_worker_checkpoints_due ON worker_checkpoints(next_attempt_at,worker_key);
CREATE TABLE accepted_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 kind TEXT NOT NULL CHECK(kind IN ('model_discovery','image_model_discovery','image_upstream_resume','maintenance_enable','report_indexing','report_approved_processing')),
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
CREATE TABLE credit_accounts (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), kind TEXT NOT NULL CHECK(kind IN ('user','pool','platform','external')), user_id INTEGER REFERENCES users(id) ON DELETE CASCADE, code TEXT, balance_sign INTEGER NOT NULL CHECK(balance_sign IN (-1,0,1)), balance_mag BLOB NOT NULL CHECK(typeof(balance_mag)='blob' AND length(balance_mag)=16), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')), CHECK((balance_sign=0 AND hex(balance_mag)='00000000000000000000000000000000') OR (balance_sign<>0 AND hex(balance_mag)<>'00000000000000000000000000000000')), CHECK((kind='user' AND user_id IS NOT NULL AND code IS NULL) OR (kind<>'user' AND user_id IS NULL AND code IS NOT NULL AND length(code) BETWEEN 1 AND 64)), CHECK(kind IN ('user','external') OR balance_sign IN (0,1)), CHECK((kind='user' AND code IS NULL) OR (kind='external' AND code='external') OR (kind='pool' AND length(code)=31 AND substr(code,1,5)='pool:' AND substr(code,6,4)='pol_' AND substr(code,10) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (kind='platform' AND (code IN ('platform','forward_reserve','charity_reserve','game_fishing_reserve','image_activity_reserve') OR (length(code)=44 AND substr(code,1,18)='blackjack-payment:' AND substr(code,19,4)='bjp_' AND substr(code,23) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=38 AND substr(code,1,11)='duel-queue:' AND substr(code,12,5) IN ('bidq_','likq_','gwtq_') AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=39 AND substr(code,1,13)='duel-session:' AND substr(code,14,4) IN ('bid_','lik_','gwt_') AND substr(code,18) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=37 AND substr(code,1,10)='rps-queue:' AND substr(code,11,5)='rpsq_' AND substr(code,16) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=38 AND substr(code,1,12)='rps-session:' AND substr(code,13,4)='rps_' AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')))))
);
CREATE TABLE credit_capacity (id INTEGER PRIMARY KEY CHECK(id=1), last_ledger_seq INTEGER NOT NULL CHECK(last_ledger_seq BETWEEN 0 AND 9223372036854775807), reserved_future_rows BLOB NOT NULL CHECK(typeof(reserved_future_rows)='blob' AND length(reserved_future_rows)=16), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16));
CREATE TABLE credit_operations (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807), kind TEXT NOT NULL CHECK(kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout','forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release','donor_reward','thursday_finalize','fishing_reserve','fishing_settle','fishing_release','linklink_entry','rps_queue_reserve','rps_queue_release','rps_session_start','rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal','ai_ticket','ai_terminal','catch_ticket','catch_refund','catch_reward','blackjack_reserve','blackjack_settle','blackjack_release')), source_type TEXT NOT NULL CHECK(source_type IN ('image_task','operation','logical_request','dispatch_claim','period','fishing_batch','linklink_session','rps_queue','rps_session','duel_queue','duel_session','catch_session','blackjack_payment')), source_id TEXT NOT NULL, source_seq BLOB NOT NULL CHECK(typeof(source_seq)='blob' AND length(source_seq)=16), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, donation_credit_delta_sign INTEGER NOT NULL CHECK(donation_credit_delta_sign IN (-1,0,1)), donation_credit_delta_mag BLOB NOT NULL CHECK(typeof(donation_credit_delta_mag)='blob' AND length(donation_credit_delta_mag)=16), donation_credit_after BLOB CHECK(donation_credit_after IS NULL OR (typeof(donation_credit_after)='blob' AND length(donation_credit_after)=16)), reason TEXT CHECK(reason IS NULL OR (typeof(reason)='text' AND length(reason) BETWEEN 1 AND 1024 AND length(CAST(reason AS BLOB))<=4096)), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), compacted INTEGER NOT NULL DEFAULT 0 CHECK(compacted IN (0,1)), UNIQUE(kind,source_type,source_id,source_seq), CHECK((donation_credit_delta_sign=0 AND hex(donation_credit_delta_mag)='00000000000000000000000000000000') OR (donation_credit_delta_sign<>0 AND hex(donation_credit_delta_mag)<>'00000000000000000000000000000000')), CHECK((donation_credit_user_id IS NULL AND donation_credit_delta_sign=0 AND donation_credit_after IS NULL) OR (donation_credit_user_id IS NOT NULL AND kind IN ('admin_user_adjustment','donor_reward'))), CHECK((donation_credit_delta_sign=0 OR kind IN ('admin_user_adjustment','donor_reward'))), CHECK((reason IS NULL OR kind IN ('admin_user_adjustment','admin_pool_adjustment','anti_abuse_penalty'))), CHECK((source_type='operation' AND length(source_id)=25 AND substr(source_id,1,3)='op_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request' AND length(source_id)=26 AND substr(source_id,1,4)='req_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='dispatch_claim' AND length(source_id)=26 AND substr(source_id,1,4)='clm_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='period' AND length(source_id)=26 AND substr(source_id,1,4)='thu_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='fishing_batch' AND length(source_id)=25 AND substr(source_id,1,3)='fb_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='catch_session' AND length(source_id)=25 AND substr(source_id,1,3)='sc_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='linklink_session' AND length(source_id)=25 AND substr(source_id,1,3)='ll_' AND substr(source_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_queue' AND length(source_id)=27 AND substr(source_id,1,5)='rpsq_' AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_','gwt_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND length(source_id)=26 AND substr(source_id,1,4)='rps_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w'))), CHECK((kind IN ('admin_user_adjustment','admin_pool_adjustment','account_delete_zero','checkin_award','game_onboarding_reward','activity_loan','activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange','anti_abuse_penalty','welfare_claim','thursday_contribution','thursday_payout') AND source_type='operation' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('forward_reserve','forward_settle','forward_release','charity_reserve','charity_settle','charity_release') AND source_type='logical_request' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward' AND source_type='dispatch_claim' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='thursday_finalize' AND source_type='period' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('fishing_reserve','fishing_settle','fishing_release') AND source_type='fishing_batch' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('catch_ticket','catch_refund','catch_reward') AND source_type='catch_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='linklink_entry' AND source_type='linklink_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_queue_reserve','rps_queue_release') AND source_type='rps_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('rps_session_start','rps_terminal') AND source_type='rps_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal','ai_ticket','ai_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND source_type='rps_session' AND hex(source_seq)<>'00000000000000000000000000000000'))
) WITHOUT ROWID;
CREATE INDEX idx_credit_operations_source ON credit_operations(source_type,source_id,source_seq);
CREATE INDEX idx_credit_operations_created ON credit_operations(created_at,ledger_seq);
CREATE TABLE credit_entries (
 operation_id TEXT NOT NULL REFERENCES credit_operations(id) ON DELETE CASCADE CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), line_no INTEGER NOT NULL CHECK(line_no BETWEEN 0 AND 255), account_id INTEGER REFERENCES credit_accounts(id) ON DELETE SET NULL, account_kind_snapshot TEXT NOT NULL CHECK(account_kind_snapshot IN ('user','pool','platform','external')), delta_sign INTEGER NOT NULL CHECK(delta_sign IN (-1,0,1)), delta_mag BLOB NOT NULL CHECK(typeof(delta_mag)='blob' AND length(delta_mag)=16), balance_after_sign INTEGER, balance_after_mag BLOB CHECK((account_kind_snapshot='user' AND balance_after_sign IS NULL AND balance_after_mag IS NULL) OR (account_kind_snapshot<>'user' AND balance_after_sign IS NOT NULL AND balance_after_mag IS NOT NULL)), asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')), PRIMARY KEY(operation_id,line_no), CHECK((delta_sign=0 AND hex(delta_mag)='00000000000000000000000000000000') OR (delta_sign<>0 AND hex(delta_mag)<>'00000000000000000000000000000000')), CHECK((balance_after_sign IS NULL AND balance_after_mag IS NULL) OR (balance_after_sign IN (-1,0,1) AND typeof(balance_after_mag)='blob' AND length(balance_after_mag)=16 AND ((balance_after_sign=0 AND hex(balance_after_mag)='00000000000000000000000000000000') OR (balance_after_sign<>0 AND hex(balance_after_mag)<>'00000000000000000000000000000000')))), CHECK(account_kind_snapshot NOT IN ('pool','platform') OR balance_after_sign IN (0,1))
) WITHOUT ROWID;
CREATE INDEX idx_credit_entries_account ON credit_entries(account_id,operation_id,line_no);
CREATE TABLE shared_pools (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='pol_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), pool_type TEXT NOT NULL CHECK(pool_type IN ('welfare','thursday')), period_id TEXT REFERENCES thursday_periods(id) ON DELETE RESTRICT CHECK(period_id IS NULL OR (length(period_id)=26 AND substr(period_id,1,4)='thu_' AND substr(period_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(period_id,-1,1) IN ('A','Q','g','w'))), account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, state TEXT NOT NULL CHECK(state IN ('open','closed')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), closed_at INTEGER CHECK(closed_at IS NULL OR closed_at BETWEEN 0 AND 253402300799), UNIQUE(pool_type,period_id), CHECK((pool_type='welfare' AND period_id IS NULL) OR pool_type='thursday'), CHECK((state='open' AND closed_at IS NULL) OR (state='closed' AND closed_at IS NOT NULL AND closed_at>=created_at)));
CREATE UNIQUE INDEX idx_shared_pools_welfare_singleton ON shared_pools(pool_type) WHERE pool_type='welfare';
CREATE UNIQUE INDEX idx_shared_pools_unbound_thursday ON shared_pools(pool_type) WHERE pool_type='thursday' AND period_id IS NULL;
CREATE TABLE announcements (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ann_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), state TEXT NOT NULL CHECK(state IN ('draft','published','withdrawn','expired')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), draft_title_zh TEXT NOT NULL DEFAULT '', draft_body_zh TEXT NOT NULL DEFAULT '', draft_title_en TEXT NOT NULL DEFAULT '', draft_body_en TEXT NOT NULL DEFAULT '', published_title_zh TEXT, published_body_zh TEXT, published_title_en TEXT, published_body_en TEXT, severity TEXT NOT NULL CHECK(severity IN ('info','warning','important')), pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0,1)), dismissible INTEGER NOT NULL DEFAULT 1 CHECK(dismissible IN (0,1)), expires_at INTEGER CHECK(expires_at IS NULL OR expires_at BETWEEN 0 AND 253402300799), published_revision INTEGER CHECK(published_revision IS NULL OR published_revision BETWEEN 1 AND 9223372036854775807), published_at INTEGER CHECK(published_at IS NULL OR published_at BETWEEN 0 AND 253402300799), withdrawn_at INTEGER CHECK(withdrawn_at IS NULL OR withdrawn_at BETWEEN 0 AND 253402300799), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), CHECK((state='published' AND published_revision BETWEEN 1 AND revision AND published_at IS NOT NULL AND published_title_zh IS NOT NULL AND published_body_zh IS NOT NULL AND published_title_en IS NOT NULL AND published_body_en IS NOT NULL AND ((length(published_title_zh)>0 AND length(published_body_zh)>0) OR (length(published_title_en)>0 AND length(published_body_en)>0))) OR state<>'published'), CHECK(((published_revision IS NULL) AND published_at IS NULL AND published_title_zh IS NULL AND published_body_zh IS NULL AND published_title_en IS NULL AND published_body_en IS NULL) OR ((published_revision IS NOT NULL) AND published_at IS NOT NULL AND published_title_zh IS NOT NULL AND published_body_zh IS NOT NULL AND published_title_en IS NOT NULL AND published_body_en IS NOT NULL)), CHECK(length(draft_title_zh)<=160 AND length(draft_title_en)<=160 AND length(CAST(draft_body_zh AS BLOB))<=65536 AND length(CAST(draft_body_en AS BLOB))<=65536), CHECK(published_title_zh IS NULL OR length(published_title_zh)<=160), CHECK(published_title_en IS NULL OR length(published_title_en)<=160), CHECK(published_body_zh IS NULL OR length(CAST(published_body_zh AS BLOB))<=65536), CHECK(published_body_en IS NULL OR length(CAST(published_body_en AS BLOB))<=65536)
 );
CREATE INDEX idx_announcements_user ON announcements(state,pinned DESC,published_at DESC,id);
CREATE INDEX idx_announcements_expiry ON announcements(expires_at,id);
CREATE INDEX idx_announcements_revision ON announcements(revision,id);
CREATE TABLE announcement_audits (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), announcement_id_text TEXT NOT NULL CHECK(length(announcement_id_text)=26 AND substr(announcement_id_text,1,4)='ann_' AND substr(announcement_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(announcement_id_text,-1,1) IN ('A','Q','g','w')), actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, action TEXT NOT NULL CHECK(action IN ('create','edit','publish','withdraw','expire','delete')), from_revision INTEGER NOT NULL CHECK(from_revision BETWEEN 0 AND 9223372036854775807), to_revision INTEGER NOT NULL CHECK(to_revision BETWEEN 1 AND 9223372036854775807), reason TEXT NOT NULL DEFAULT '' CHECK(typeof(reason)='text' AND length(reason)<=1024 AND length(CAST(reason AS BLOB))<=4096), created_at INTEGER NOT NULL, actor_deidentify_at INTEGER NOT NULL, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), CHECK(actor_deidentify_at=created_at+7776000));
CREATE INDEX idx_announcement_audits_retention ON announcement_audits(created_at,id);
CREATE INDEX idx_announcement_audits_actor ON announcement_audits(actor_user_id,created_at);
CREATE TABLE welfare_claims (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, site_day TEXT NOT NULL, operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT, threshold_milli INTEGER NOT NULL CHECK(threshold_milli BETWEEN 0 AND 9000000000000000), cap_milli INTEGER NOT NULL CHECK(cap_milli BETWEEN 0 AND 9000000000000000), pool_before_milli INTEGER NOT NULL CHECK(pool_before_milli BETWEEN 0 AND 9000000000000000), award_milli INTEGER NOT NULL CHECK(award_milli BETWEEN 1 AND 9000000000000000), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game')), UNIQUE(user_id,site_day), CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), CHECK(length(site_day)=10 AND site_day GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' AND date(site_day)=site_day), CHECK(award_milli<=cap_milli AND award_milli<=pool_before_milli));
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
CREATE TABLE donations (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','deleted','expired')), revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807), description TEXT NOT NULL DEFAULT '', review_note TEXT NOT NULL DEFAULT '', reviewed_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reviewed_by_role TEXT NOT NULL DEFAULT '' CHECK(reviewed_by_role IN ('','admin','level5','level6','trainee5')), reviewed_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, terminal_at INTEGER, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), discord_public_thanks INTEGER CHECK(discord_public_thanks IN (0,1)), first_approval_origin TEXT NOT NULL DEFAULT 'unknown' CHECK(first_approval_origin IN ('auto','manual','unknown')), CHECK((status IN ('pending','approved') AND terminal_at IS NULL) OR (status IN ('rejected','deleted','expired') AND terminal_at IS NOT NULL)));
CREATE INDEX idx_donations_user ON donations(user_id,created_at,id);
CREATE INDEX idx_donations_status ON donations(status,terminal_at,id);
CREATE TRIGGER donation_terminal_immutable BEFORE UPDATE OF status,terminal_at ON donations
WHEN OLD.status IN ('rejected','deleted','expired')
 AND (NEW.status IS NOT OLD.status OR NEW.terminal_at IS NOT OLD.terminal_at)
BEGIN SELECT RAISE(ABORT,'terminal donation cannot reopen or move its terminal time'); END;
CREATE TABLE donation_key_memberships (endpoint_key_id INTEGER PRIMARY KEY REFERENCES endpoint_keys(id) ON DELETE RESTRICT, donation_key_id INTEGER NOT NULL UNIQUE REFERENCES donation_keys(id) ON DELETE CASCADE, donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE, created_at INTEGER NOT NULL);
CREATE INDEX idx_donation_memberships_donation ON donation_key_memberships(donation_id,endpoint_key_id);
CREATE TABLE donation_reviews (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), donation_id INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE, submission_revision INTEGER NOT NULL DEFAULT 1 CHECK(submission_revision BETWEEN 1 AND 9223372036854775807), reviewer_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reviewer_role TEXT NOT NULL DEFAULT '' CHECK(reviewer_role IN ('','admin','level5','level6','trainee5')), action TEXT NOT NULL CHECK(action IN ('approve','reject','withdraw','terminate','expire','enable','disable','limit_update','note_update','member_removed','failure_streak_reset','failure_policy_update','force_reject')), note TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE INDEX idx_donation_reviews_donation ON donation_reviews(donation_id,id);
CREATE TABLE charity_reservations (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), logical_request_id TEXT NOT NULL UNIQUE REFERENCES logical_requests(id) ON DELETE CASCADE CHECK(length(logical_request_id)=26 AND substr(logical_request_id,1,4)='req_' AND substr(logical_request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(logical_request_id,-1,1) IN ('A','Q','g','w')), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, charity_model_id INTEGER REFERENCES charity_models(id) ON DELETE SET NULL, model_snapshot TEXT NOT NULL DEFAULT '', state TEXT NOT NULL CHECK(state IN ('reserved','dispatched','committed','released')), pricing_mode TEXT NOT NULL CHECK(pricing_mode IN ('per_request','per_token')), discount_percent INTEGER NOT NULL CHECK(discount_percent BETWEEN 0 AND 100), request_user_price_milli INTEGER NOT NULL CHECK(request_user_price_milli BETWEEN 0 AND 9000000000000000), request_donor_reward_milli INTEGER NOT NULL CHECK(request_donor_reward_milli BETWEEN 0 AND 9000000000000000), uncached_user_price_milli INTEGER NOT NULL CHECK(uncached_user_price_milli BETWEEN 0 AND 9000000000000000), cache_write_user_price_milli INTEGER NOT NULL CHECK(cache_write_user_price_milli BETWEEN 0 AND 9000000000000000), cache_read_user_price_milli INTEGER NOT NULL CHECK(cache_read_user_price_milli BETWEEN 0 AND 9000000000000000), output_user_price_milli INTEGER NOT NULL CHECK(output_user_price_milli BETWEEN 0 AND 9000000000000000), uncached_donor_reward_milli INTEGER NOT NULL CHECK(uncached_donor_reward_milli BETWEEN 0 AND 9000000000000000), cache_write_donor_reward_milli INTEGER NOT NULL CHECK(cache_write_donor_reward_milli BETWEEN 0 AND 9000000000000000), cache_read_donor_reward_milli INTEGER NOT NULL CHECK(cache_read_donor_reward_milli BETWEEN 0 AND 9000000000000000), output_donor_reward_milli INTEGER NOT NULL CHECK(output_donor_reward_milli BETWEEN 0 AND 9000000000000000), token_reserve_milli INTEGER NOT NULL CHECK(token_reserve_milli>=0), user_reserved_milli INTEGER NOT NULL CHECK(user_reserved_milli>=0), original_charge_milli INTEGER NOT NULL CHECK(original_charge_milli>=0), user_charge_milli INTEGER NOT NULL CHECK(user_charge_milli>=0), donor_reward_total_mag BLOB NOT NULL CHECK(typeof(donor_reward_total_mag)='blob' AND length(donor_reward_total_mag)=16), usage_uncached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(usage_uncached_input_tokens>=0), cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_input_tokens>=0), cache_read_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_input_tokens>=0), usage_output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(usage_output_tokens>=0), usage_unknown INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)), created_at INTEGER NOT NULL, dispatched_at INTEGER, finalized_at INTEGER, updated_at INTEGER NOT NULL);
CREATE INDEX idx_charity_reservations_state ON charity_reservations(state,created_at,id);
CREATE INDEX idx_charity_reservations_user ON charity_reservations(user_id,created_at,id);
CREATE TABLE donation_usage_reservations (claim_id TEXT NOT NULL PRIMARY KEY CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')), donation_key_id INTEGER REFERENCES donation_keys(id) ON DELETE SET NULL, streak_generation BLOB NOT NULL CHECK(typeof(streak_generation)='blob' AND length(streak_generation)=16), claim_seq BLOB NOT NULL CHECK(typeof(claim_seq)='blob' AND length(claim_seq)=16), price_reserved_milli INTEGER NOT NULL CHECK(price_reserved_milli BETWEEN 0 AND 9000000000000000), price_actual_milli INTEGER CHECK(price_actual_milli IS NULL OR price_actual_milli BETWEEN 0 AND 9000000000000000), reward_actual_milli INTEGER CHECK(reward_actual_milli IS NULL OR reward_actual_milli BETWEEN 0 AND 9000000000000000), calls_reserved INTEGER NOT NULL CHECK(calls_reserved IN (0,1)), calls_actual INTEGER CHECK(calls_actual IS NULL OR calls_actual IN (0,1)), tokens_reserved INTEGER NOT NULL CHECK(tokens_reserved BETWEEN 0 AND 9223372036854775807), tokens_actual INTEGER CHECK(tokens_actual IS NULL OR tokens_actual BETWEEN 0 AND 9223372036854775807), protocol_success INTEGER CHECK(protocol_success IS NULL OR protocol_success IN (0,1)), usage_unknown INTEGER CHECK(usage_unknown IS NULL OR usage_unknown IN (0,1)), state TEXT NOT NULL CHECK(state IN ('reserved','committed','released')), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), finalized_at INTEGER CHECK(finalized_at IS NULL OR finalized_at BETWEEN 0 AND 253402300799), input_tokens_reserved INTEGER CHECK(input_tokens_reserved IS NULL OR (typeof(input_tokens_reserved)='integer' AND input_tokens_reserved>=0)), output_tokens_reserved INTEGER CHECK(output_tokens_reserved IS NULL OR (typeof(output_tokens_reserved)='integer' AND output_tokens_reserved>=0)), input_tokens_actual INTEGER CHECK(input_tokens_actual IS NULL OR (typeof(input_tokens_actual)='integer' AND input_tokens_actual>=0)), output_tokens_actual INTEGER CHECK(output_tokens_actual IS NULL OR (typeof(output_tokens_actual)='integer' AND output_tokens_actual>=0)), streak_disposition TEXT CHECK(streak_disposition IS NULL OR streak_disposition IN ('success','upstream_failure','neutral')), failure_origin TEXT CHECK(failure_origin IS NULL OR failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','legacy_unknown','recovery_unknown')), UNIQUE(donation_key_id,streak_generation,claim_seq), CHECK((state='reserved' AND price_actual_milli IS NULL AND reward_actual_milli IS NULL AND calls_actual IS NULL AND tokens_actual IS NULL AND protocol_success IS NULL AND usage_unknown IS NULL AND finalized_at IS NULL) OR (state='committed' AND price_actual_milli IS NOT NULL AND reward_actual_milli IS NOT NULL AND calls_actual IS NOT NULL AND tokens_actual IS NOT NULL AND protocol_success IS NOT NULL AND usage_unknown IS NOT NULL AND finalized_at IS NOT NULL) OR (state='released' AND finalized_at IS NOT NULL AND ((price_actual_milli IS NULL AND reward_actual_milli IS NULL AND calls_actual IS NULL AND tokens_actual IS NULL AND protocol_success IS NULL AND usage_unknown IS NULL) OR (price_actual_milli IS NOT NULL AND reward_actual_milli IS NOT NULL AND calls_actual IS NOT NULL AND tokens_actual IS NOT NULL AND protocol_success IS NOT NULL AND usage_unknown IS NOT NULL)))));
CREATE INDEX idx_donation_usage_key_state ON donation_usage_reservations(donation_key_id,state,claim_seq);
CREATE TABLE report_cases (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rpc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), fingerprint BLOB NOT NULL CHECK(typeof(fingerprint)='blob' AND length(fingerprint)=32), connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')), canonical_base_url TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending_indexing','pending_review','approved_processing','approved','rejected','expired')), progress_state TEXT NOT NULL CHECK(progress_state IN ('in_progress','complete')), material_version INTEGER NOT NULL CHECK(material_version BETWEEN 1 AND 9223372036854775807), target_version INTEGER NOT NULL CHECK(target_version BETWEEN 1 AND 9223372036854775807), deadline INTEGER NOT NULL, cursor_source TEXT CHECK(cursor_source IS NULL OR cursor_source IN ('endpoint','donation')), cursor_id INTEGER CHECK(cursor_id IS NULL OR (typeof(cursor_id)='integer' AND cursor_id BETWEEN 0 AND 9223372036854775807)), material_count INTEGER NOT NULL CHECK(material_count>=0), target_count INTEGER NOT NULL CHECK(target_count>=0), distinct_owner_count INTEGER NOT NULL CHECK(distinct_owner_count>=0), processed_target_count INTEGER NOT NULL DEFAULT 0 CHECK(processed_target_count>=0), deleted_target_count INTEGER NOT NULL DEFAULT 0 CHECK(deleted_target_count>=0), released_target_count INTEGER NOT NULL DEFAULT 0 CHECK(released_target_count>=0), decision_reason TEXT, decision_actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, decision_at INTEGER, retry_attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_attempt_count>=0), next_retry_at INTEGER, last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('db_busy','internal_retryable','invariant_violation')), created_at INTEGER NOT NULL, terminal_at INTEGER, legal_hold_consumed INTEGER NOT NULL DEFAULT 0 CHECK(legal_hold_consumed IN (0,1)), CHECK(processed_target_count<=target_count AND deleted_target_count<=target_count AND released_target_count<=target_count), CHECK((cursor_source IS NULL AND cursor_id IS NULL) OR (cursor_source IS NOT NULL AND cursor_id IS NOT NULL)), CHECK(cursor_id IS NULL OR cursor_id>0 OR cursor_source='donation'), CHECK(progress_state='in_progress' OR (cursor_source IS NULL AND cursor_id IS NULL)), CHECK((progress_state='in_progress' AND status IN ('pending_indexing','approved_processing')) OR (progress_state='complete' AND status IN ('pending_review','approved','rejected')) OR status='expired'));
CREATE UNIQUE INDEX idx_report_cases_active_fingerprint ON report_cases(fingerprint) WHERE status IN ('pending_indexing','pending_review','approved_processing');
CREATE INDEX idx_report_cases_status_deadline ON report_cases(status,deadline,id);
CREATE INDEX idx_report_cases_retry ON report_cases(next_retry_at,id);
CREATE TABLE report_materials (id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0), case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(case_id)=26 AND substr(case_id,1,4)='rpc_' AND substr(case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(case_id,-1,1) IN ('A','Q','g','w')), material_hash BLOB NOT NULL CHECK(typeof(material_hash)='blob' AND length(material_hash)=32), note_text TEXT NOT NULL DEFAULT '', reporter_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, reporter_discord_id TEXT CHECK(reporter_discord_id IS NULL OR (length(CAST(reporter_discord_id AS BLOB)) BETWEEN 1 AND 64 AND reporter_discord_id NOT GLOB '*[^ -~]*')), source_ip_envelope BLOB NOT NULL CHECK(typeof(source_ip_envelope)='blob' AND length(source_ip_envelope)=45), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), UNIQUE(case_id,material_hash));
CREATE INDEX idx_report_materials_case ON report_materials(case_id,id);
CREATE TABLE report_targets (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rpt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), case_id TEXT NOT NULL REFERENCES report_cases(id) ON DELETE CASCADE CHECK(length(case_id)=26 AND substr(case_id,1,4)='rpc_' AND substr(case_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(case_id,-1,1) IN ('A','Q','g','w')), target_seq INTEGER NOT NULL CHECK(target_seq BETWEEN 0 AND 9223372036854775807), endpoint_key_id INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL, source_endpoint_key_id INTEGER NOT NULL CHECK(typeof(source_endpoint_key_id)='integer' AND source_endpoint_key_id>0), key_ref BLOB NOT NULL CHECK(typeof(key_ref)='blob' AND length(key_ref)=32), owner_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, owner_discord_id TEXT CHECK(owner_discord_id IS NULL OR (length(CAST(owner_discord_id AS BLOB)) BETWEEN 1 AND 64 AND owner_discord_id NOT GLOB '*[^ -~]*')), owner_display_name TEXT CHECK(owner_display_name IS NULL OR (typeof(owner_display_name)='text' AND length(CAST(owner_display_name AS BLOB))<=512)), connector_type TEXT NOT NULL CHECK(connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')), canonical_base_url TEXT NOT NULL CHECK(typeof(canonical_base_url)='text' AND length(CAST(canonical_base_url AS BLOB)) BETWEEN 1 AND 4096), key_display_head TEXT NOT NULL DEFAULT '' CHECK(length(CAST(key_display_head AS BLOB))<=16), key_display_tail TEXT NOT NULL DEFAULT '' CHECK(length(CAST(key_display_tail AS BLOB))<=16), state TEXT NOT NULL CHECK(state IN ('protected','deleted_by_owner','deleted_by_account','deleted_by_approval','released')), discovered_version INTEGER NOT NULL CHECK(discovered_version BETWEEN 1 AND 9223372036854775807), decided_version INTEGER CHECK(decided_version IS NULL OR decided_version BETWEEN 1 AND 9223372036854775807), created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799), UNIQUE(case_id,key_ref), UNIQUE(case_id,target_seq));
CREATE INDEX idx_report_targets_case_state ON report_targets(case_id,state,target_seq,id);
CREATE INDEX idx_report_targets_owner ON report_targets(owner_user_id,id);
CREATE TRIGGER report_target_source_key_immutable BEFORE UPDATE OF source_endpoint_key_id ON report_targets
WHEN NEW.source_endpoint_key_id IS NOT OLD.source_endpoint_key_id
BEGIN SELECT RAISE(ABORT,'report target source endpoint key is immutable'); END;
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
CREATE TABLE game_user_preferences (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, tutorial_rps_seen INTEGER NOT NULL DEFAULT 0 CHECK(tutorial_rps_seen IN (0,1)), game_profile_public INTEGER NOT NULL DEFAULT 0 CHECK(game_profile_public IN (0,1)), updated_at INTEGER NOT NULL, linklink_public_tie_key BLOB CHECK(linklink_public_tie_key IS NULL OR (typeof(linklink_public_tie_key)='blob' AND length(linklink_public_tie_key)=32)));
CREATE TABLE game_fishing_batches (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='fb_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, bait TEXT NOT NULL CHECK(bait IN ('worm','lure','premium')), count INTEGER NOT NULL CHECK(count IN (1,10)), unit_price_milli INTEGER NOT NULL CHECK(unit_price_milli BETWEEN 0 AND 9000000000000000), entry_total_milli INTEGER NOT NULL CHECK(entry_total_milli BETWEEN 0 AND 9000000000000000), payout_total_milli INTEGER NOT NULL CHECK(payout_total_milli BETWEEN 0 AND 9000000000000000), operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), state TEXT NOT NULL CHECK(state IN ('reserved','committed','released')), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 10), next_attempt_at INTEGER, last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('rng_failed','settlement_failed','db_busy','internal_retryable','invariant_violation')), retry_exhausted INTEGER NOT NULL DEFAULT 0 CHECK(retry_exhausted IN (0,1)), created_at INTEGER NOT NULL, settled_at INTEGER, revealed_at INTEGER, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND entry_total_milli), platform_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_bp)='integer' AND platform_bp BETWEEN 0 AND 9999), welfare_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_bp)='integer' AND welfare_bp BETWEEN 0 AND 9999), thursday_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_bp)='integer' AND thursday_bp BETWEEN 0 AND 9999), net_payout_total_milli INTEGER CHECK(net_payout_total_milli IS NULL OR (typeof(net_payout_total_milli)='integer' AND net_payout_total_milli BETWEEN 0 AND 9000000000000000)), platform_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_cut_total_milli)='integer' AND platform_cut_total_milli BETWEEN 0 AND 9000000000000000), welfare_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_cut_total_milli)='integer' AND welfare_cut_total_milli BETWEEN 0 AND 9000000000000000), thursday_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_cut_total_milli)='integer' AND thursday_cut_total_milli BETWEEN 0 AND 9000000000000000), blue_fish_chance_bps INTEGER NOT NULL DEFAULT 1000
 CHECK(typeof(blue_fish_chance_bps)='integer' AND blue_fish_chance_bps BETWEEN 0 AND 10000), config_revision INTEGER
 CHECK(config_revision IS NULL OR (typeof(config_revision)='integer' AND config_revision>0)), UNIQUE(user_id,operation_id), CHECK((state='reserved' AND settled_at IS NULL AND revealed_at IS NULL AND ((retry_exhausted=0 AND next_attempt_at IS NOT NULL) OR (retry_exhausted=1 AND next_attempt_at IS NULL))) OR (state IN ('committed','released') AND settled_at IS NOT NULL AND next_attempt_at IS NULL AND retry_exhausted=0)), CHECK(entry_total_milli=unit_price_milli*count));
CREATE UNIQUE INDEX idx_fishing_one_reserved ON game_fishing_batches(user_id) WHERE state='reserved';
CREATE INDEX idx_fishing_due ON game_fishing_batches(state,next_attempt_at,id);
CREATE INDEX idx_fishing_user ON game_fishing_batches(user_id,created_at,id);
CREATE INDEX idx_fishing_unrevealed ON game_fishing_batches(user_id,settled_at,id) WHERE settled_at IS NOT NULL AND revealed_at IS NULL;
CREATE INDEX idx_fishing_retention ON game_fishing_batches(settled_at,id);
CREATE TABLE game_fishing_outcomes (batch_id TEXT NOT NULL REFERENCES game_fishing_batches(id) ON DELETE CASCADE CHECK(length(batch_id)=25 AND substr(batch_id,1,3)='fb_' AND substr(batch_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id,-1,1) IN ('A','Q','g','w')), ordinal INTEGER NOT NULL CHECK(ordinal>=0), species_key TEXT NOT NULL, tier TEXT NOT NULL CHECK(tier IN ('junk','small','regular','big','giant','legend','treasure')), size_cm INTEGER NOT NULL CHECK(size_cm BETWEEN 0 AND 200), payout_milli INTEGER NOT NULL CHECK(payout_milli>=0), net_payout_milli INTEGER CHECK(net_payout_milli IS NULL OR (typeof(net_payout_milli)='integer' AND net_payout_milli BETWEEN 0 AND 9000000000000000)), platform_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_cut_milli)='integer' AND platform_cut_milli BETWEEN 0 AND 9000000000000000), welfare_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_cut_milli)='integer' AND welfare_cut_milli BETWEEN 0 AND 9000000000000000), thursday_cut_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_cut_milli)='integer' AND thursday_cut_milli BETWEEN 0 AND 9000000000000000), PRIMARY KEY(batch_id,ordinal), CHECK((tier='junk' AND species_key IN ('boot','seaweed','plastic_bag','branch','old_tire','glasses','phone_case','fry')) OR (tier='small' AND species_key IN ('whitebait','gudgeon','horse_mouth','smelt','loach')) OR (tier='regular' AND species_key IN ('crucian','tilapia','yellow_catfish','ayu','stream_carp')) OR (tier='big' AND species_key IN ('common_carp','snakehead','catfish','mandarin_fish','rainbow_trout')) OR (tier='giant' AND species_key IN ('grass_carp','silver_carp','bighead_carp','black_carp','japanese_eel')) OR (tier='legend' AND species_key IN ('yellowcheek','taimen','koi')) OR (tier='treasure' AND species_key IN ('bottle','clover','shell'))), CHECK((tier IN ('junk','treasure') AND size_cm=0) OR (tier='small' AND size_cm BETWEEN 5 AND 25) OR (tier='regular' AND size_cm BETWEEN 15 AND 35) OR (tier='big' AND size_cm BETWEEN 30 AND 80) OR (tier='giant' AND size_cm BETWEEN 60 AND 150) OR (tier='legend' AND size_cm BETWEEN 100 AND 200)));
CREATE INDEX idx_fishing_outcomes_rank ON game_fishing_outcomes(tier,size_cm,batch_id,ordinal);
CREATE TABLE game_fishing_best (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, batch_id TEXT, ordinal INTEGER, species_key TEXT NOT NULL, tier TEXT NOT NULL, size_cm INTEGER NOT NULL CHECK(size_cm BETWEEN 0 AND 200), caught_at INTEGER NOT NULL, public_tie_key BLOB NOT NULL CHECK(typeof(public_tie_key)='blob' AND length(public_tie_key)=32), FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE SET NULL, CHECK((batch_id IS NULL AND ordinal IS NULL) OR (batch_id IS NOT NULL AND ordinal IS NOT NULL)), CHECK(batch_id IS NULL OR (length(batch_id)=25 AND substr(batch_id,1,3)='fb_' AND substr(batch_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id,-1,1) IN ('A','Q','g','w'))));
CREATE INDEX idx_game_fishing_best_rank ON game_fishing_best(size_cm DESC,caught_at ASC,public_tie_key);
CREATE TABLE game_fishing_rank_facts (batch_id_text TEXT NOT NULL PRIMARY KEY CHECK(length(batch_id_text)=25 AND substr(batch_id_text,1,3)='fb_' AND substr(batch_id_text,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(batch_id_text,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, settled_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, payout_total BLOB NOT NULL CHECK(typeof(payout_total)='blob' AND length(payout_total)=16), aggregate_applied INTEGER NOT NULL CHECK(aggregate_applied IN (0,1)), CHECK(expires_at=settled_at+2592000));
CREATE INDEX idx_fishing_rank_facts_due ON game_fishing_rank_facts(expires_at,batch_id_text);
CREATE INDEX idx_fishing_rank_facts_user ON game_fishing_rank_facts(user_id,expires_at,batch_id_text);
CREATE TABLE game_fishing_rank_aggregates (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, batch_count BLOB NOT NULL CHECK(typeof(batch_count)='blob' AND length(batch_count)=16), total_payout BLOB NOT NULL CHECK(typeof(total_payout)='blob' AND length(total_payout)=16), score_achieved_at INTEGER NOT NULL, public_tie_key BLOB NOT NULL CHECK(typeof(public_tie_key)='blob' AND length(public_tie_key)=32), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), updated_at INTEGER NOT NULL);
CREATE INDEX idx_fishing_rank_aggregates_rank ON game_fishing_rank_aggregates(total_payout DESC,score_achieved_at ASC,public_tie_key);
CREATE TABLE game_linklink_sessions (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='ll_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, spec TEXT NOT NULL CHECK(spec IN ('6x8','8x8','10x10')), state TEXT NOT NULL CHECK(state='active'), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), price_milli INTEGER NOT NULL CHECK(price_milli BETWEEN 0 AND 9000000000000000), board_blob BLOB NOT NULL CHECK(typeof(board_blob)='blob' AND length(board_blob)<=16384), removed_bits BLOB NOT NULL CHECK(typeof(removed_bits)='blob' AND length(removed_bits) BETWEEN 1 AND 13 AND ((spec='6x8' AND length(removed_bits)=6) OR (spec='8x8' AND length(removed_bits)=8) OR (spec='10x10' AND length(removed_bits)=13)) AND (spec<>'10x10' OR substr(hex(removed_bits),-2,1)='0')), pairs_removed INTEGER NOT NULL CHECK(pairs_removed>=0), deadline INTEGER NOT NULL, operation_id TEXT NOT NULL CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND price_milli), assists_initial INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_initial)='integer' AND assists_initial BETWEEN 0 AND 5), assists_remaining INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_remaining)='integer' AND assists_remaining BETWEEN 0 AND assists_initial), UNIQUE(user_id));
CREATE INDEX idx_linklink_deadline ON game_linklink_sessions(deadline,id);
CREATE INDEX idx_linklink_user ON game_linklink_sessions(user_id,id);
CREATE TABLE game_linklink_summaries (session_id TEXT NOT NULL PRIMARY KEY CHECK(length(session_id)=25 AND substr(session_id,1,3)='ll_' AND substr(session_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, spec TEXT NOT NULL CHECK(spec IN ('6x8','8x8','10x10')), price_milli INTEGER NOT NULL CHECK(price_milli>=0), terminal_reason TEXT NOT NULL CHECK(terminal_reason IN ('completed','timed_out','abandoned')), started_at INTEGER NOT NULL, deadline INTEGER NOT NULL, terminal_at INTEGER NOT NULL, pairs_removed INTEGER NOT NULL CHECK(pairs_removed>=0), score INTEGER, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND price_milli), assists_initial INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_initial)='integer' AND assists_initial BETWEEN 0 AND 5), assists_remaining INTEGER NOT NULL DEFAULT 0 CHECK(typeof(assists_remaining)='integer' AND assists_remaining BETWEEN 0 AND assists_initial), CHECK((terminal_reason IN ('completed','timed_out') AND score IS NOT NULL) OR (terminal_reason='abandoned' AND score IS NULL)));
CREATE INDEX idx_linklink_summaries_retention ON game_linklink_summaries(terminal_at,session_id);
CREATE INDEX idx_linklink_summaries_user ON game_linklink_summaries(user_id,terminal_at,session_id);
CREATE TABLE game_rps_queue (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5)='rpsq_' AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), reservation_operation_id TEXT NOT NULL CHECK(length(reservation_operation_id)=25 AND substr(reservation_operation_id,1,3)='op_' AND substr(reservation_operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(reservation_operation_id,-1,1) IN ('A','Q','g','w')), reserved BLOB NOT NULL CHECK(typeof(reserved)='blob' AND length(reserved)=16), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), device_token_hash BLOB NOT NULL CHECK(typeof(device_token_hash)='blob' AND length(device_token_hash)=32), source_ip_hash BLOB NOT NULL CHECK(typeof(source_ip_hash)='blob' AND length(source_ip_hash)=32), deadline INTEGER NOT NULL, created_at INTEGER NOT NULL, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), game_paid BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_paid)='blob' AND length(game_paid)=16 AND game_paid<=reserved));
CREATE INDEX idx_rps_queue_user ON game_rps_queue(user_id,id);
CREATE TABLE game_rps_sessions (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='rps_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), rules_version INTEGER NOT NULL CHECK(rules_version>=1), state TEXT NOT NULL CHECK(state IN ('started','terminal_processing')), phase TEXT NOT NULL CHECK(phase IN ('gesture','dealer_raise','followers','paid_pool_gesture','free_pool_gesture','ultimate_gesture','terminal_processing')), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), phase_seq BLOB NOT NULL CHECK(typeof(phase_seq)='blob' AND length(phase_seq)=16), identity_epoch BLOB NOT NULL CHECK(typeof(identity_epoch)='blob' AND length(identity_epoch)=16), cut_seq BLOB NOT NULL CHECK(typeof(cut_seq)='blob' AND length(cut_seq)=16), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), dealer_seat INTEGER CHECK(dealer_seat BETWEEN 0 AND 2), base_milli INTEGER NOT NULL CHECK(base_milli BETWEEN 0 AND 9000000000000000), platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999), welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999), thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999), gesture_seconds INTEGER NOT NULL CHECK(gesture_seconds BETWEEN 5 AND 20), dealer_seconds INTEGER NOT NULL CHECK(dealer_seconds BETWEEN 5 AND 15), follower_seconds INTEGER NOT NULL CHECK(follower_seconds BETWEEN 5 AND 15), player_pool BLOB NOT NULL CHECK(typeof(player_pool)='blob' AND length(player_pool)=16), permanent_multiplier BLOB NOT NULL CHECK(typeof(permanent_multiplier)='blob' AND length(permanent_multiplier)=16), pool_base_multiplier BLOB CHECK(pool_base_multiplier IS NULL OR (typeof(pool_base_multiplier)='blob' AND length(pool_base_multiplier)=16 AND hex(pool_base_multiplier)<>'00000000000000000000000000000000')), current_plan_multiplier BLOB CHECK(current_plan_multiplier IS NULL OR (typeof(current_plan_multiplier)='blob' AND length(current_plan_multiplier)=16 AND hex(current_plan_multiplier)<>'00000000000000000000000000000000')), dealer_raise BLOB CHECK(dealer_raise IS NULL OR (typeof(dealer_raise)='blob' AND length(dealer_raise)=16 AND hex(dealer_raise)<>'00000000000000000000000000000000')), base_round_count BLOB NOT NULL CHECK(typeof(base_round_count)='blob' AND length(base_round_count)=16), paid_tie_count BLOB NOT NULL CHECK(typeof(paid_tie_count)='blob' AND length(paid_tie_count)=16), free_tie_count BLOB NOT NULL CHECK(typeof(free_tie_count)='blob' AND length(free_tie_count)=16), paid_pool_streak BLOB NOT NULL CHECK(typeof(paid_pool_streak)='blob' AND length(paid_pool_streak)=16), free_pool_streak BLOB NOT NULL CHECK(typeof(free_pool_streak)='blob' AND length(free_pool_streak)=16), platform_cut_total BLOB NOT NULL CHECK(typeof(platform_cut_total)='blob' AND length(platform_cut_total)=16), welfare_cut_total BLOB NOT NULL CHECK(typeof(welfare_cut_total)='blob' AND length(welfare_cut_total)=16), thursday_cut_total BLOB NOT NULL CHECK(typeof(thursday_cut_total)='blob' AND length(thursday_cut_total)=16), welfare_carry_total BLOB NOT NULL CHECK(typeof(welfare_carry_total)='blob' AND length(welfare_carry_total)=16), reminder_state TEXT NOT NULL DEFAULT 'none' CHECK(reminder_state IN ('none','active')), phase_deadline INTEGER CHECK(phase_deadline IS NULL OR phase_deadline BETWEEN 0 AND 253402300799), health_epoch INTEGER NOT NULL DEFAULT 0 CHECK(health_epoch>=0), recent_events_blob BLOB NOT NULL DEFAULT X'' CHECK(typeof(recent_events_blob)='blob' AND length(recent_events_blob)<=131072), recent_first_seq BLOB NOT NULL CHECK(typeof(recent_first_seq)='blob' AND length(recent_first_seq)=16), recent_last_seq BLOB NOT NULL CHECK(typeof(recent_last_seq)='blob' AND length(recent_last_seq)=16), recent_event_count INTEGER NOT NULL DEFAULT 0 CHECK(recent_event_count BETWEEN 0 AND 64), terminal_operation_id TEXT CHECK(terminal_operation_id IS NULL OR (length(terminal_operation_id)=25 AND substr(terminal_operation_id,1,3)='op_' AND substr(terminal_operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(terminal_operation_id,-1,1) IN ('A','Q','g','w'))), terminal_retry_attempt_count BLOB NOT NULL CHECK(typeof(terminal_retry_attempt_count)='blob' AND length(terminal_retry_attempt_count)=16), terminal_next_retry_at INTEGER, terminal_last_error_class TEXT CHECK(terminal_last_error_class IS NULL OR terminal_last_error_class IN ('db_busy','internal_retryable','invariant_violation')), started_at INTEGER NOT NULL, terminal_reason TEXT CHECK(terminal_reason IS NULL OR terminal_reason IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')), CHECK((state='started' AND phase IN ('gesture','dealer_raise','followers','paid_pool_gesture','free_pool_gesture','ultimate_gesture') AND phase_deadline IS NOT NULL AND terminal_operation_id IS NULL AND terminal_reason IS NULL AND hex(terminal_retry_attempt_count)='00000000000000000000000000000000' AND terminal_next_retry_at IS NULL AND terminal_last_error_class IS NULL) OR (state='terminal_processing' AND phase='terminal_processing' AND phase_deadline IS NULL AND terminal_operation_id IS NOT NULL AND terminal_reason IS NOT NULL AND (hex(terminal_retry_attempt_count)='00000000000000000000000000000000' AND terminal_next_retry_at IS NULL AND terminal_last_error_class IS NULL OR hex(terminal_retry_attempt_count)<>'00000000000000000000000000000000' AND terminal_next_retry_at IS NOT NULL AND terminal_last_error_class IS NOT NULL))), CHECK((phase IN ('gesture','dealer_raise','followers','ultimate_gesture') AND current_plan_multiplier IS NOT NULL AND pool_base_multiplier IS NULL) OR (phase IN ('paid_pool_gesture','free_pool_gesture') AND current_plan_multiplier IS NULL AND pool_base_multiplier IS NOT NULL) OR (phase='terminal_processing' AND current_plan_multiplier IS NULL AND pool_base_multiplier IS NULL)), CHECK((phase='followers' AND (dealer_raise IS NULL OR hex(dealer_raise)<>'00000000000000000000000000000000')) OR (phase<>'followers' AND dealer_raise IS NULL)), CHECK((reminder_state='none') OR (phase='free_pool_gesture' AND free_pool_streak IN (X'00000000000000000000000000000003',X'00000000000000000000000000000004',X'00000000000000000000000000000005'))));
CREATE INDEX idx_rps_sessions_state ON game_rps_sessions(state,phase_deadline,id);
CREATE INDEX idx_rps_sessions_mode ON game_rps_sessions(mode,id);
CREATE TABLE game_rps_seats (session_id TEXT NOT NULL REFERENCES game_rps_sessions(id) ON DELETE CASCADE CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), seat_no INTEGER NOT NULL CHECK(seat_no BETWEEN 0 AND 2), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, deletion_state TEXT NOT NULL CHECK(deletion_state IN ('active','deletion_pending','deidentified')), display_name_snapshot TEXT, avatar_url_snapshot TEXT, starting_balance BLOB NOT NULL CHECK(typeof(starting_balance)='blob' AND length(starting_balance)=16), current_balance BLOB NOT NULL CHECK(typeof(current_balance)='blob' AND length(current_balance)=16), current_round_input BLOB NOT NULL CHECK(typeof(current_round_input)='blob' AND length(current_round_input)=16), current_all_in INTEGER NOT NULL CHECK(current_all_in IN (0,1)), current_gesture_envelope BLOB CHECK(current_gesture_envelope IS NULL OR (typeof(current_gesture_envelope)='blob' AND length(current_gesture_envelope) IN (33,34,37) AND substr(current_gesture_envelope,1,1)=X'01')), current_gesture_phase_seq BLOB CHECK(current_gesture_phase_seq IS NULL OR (typeof(current_gesture_phase_seq)='blob' AND length(current_gesture_phase_seq)=16)), follower_action TEXT CHECK(follower_action IS NULL OR follower_action IN ('call','surrender')), last_action_phase_seq BLOB CHECK(last_action_phase_seq IS NULL OR (typeof(last_action_phase_seq)='blob' AND length(last_action_phase_seq)=16)), total_input BLOB NOT NULL CHECK(typeof(total_input)='blob' AND length(total_input)=32), total_returned BLOB NOT NULL CHECK(typeof(total_returned)='blob' AND length(total_returned)=32), terminal_return BLOB CHECK(terminal_return IS NULL OR (typeof(terminal_return)='blob' AND length(terminal_return)=16)), wallet_net_sign INTEGER CHECK(wallet_net_sign IS NULL OR wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB, rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), timeout_count BLOB NOT NULL CHECK(typeof(timeout_count)='blob' AND length(timeout_count)=16), snapshot_completed_count BLOB CHECK(snapshot_completed_count IS NULL OR (typeof(snapshot_completed_count)='blob' AND length(snapshot_completed_count)=16)), snapshot_profitable_count BLOB CHECK(snapshot_profitable_count IS NULL OR (typeof(snapshot_profitable_count)='blob' AND length(snapshot_profitable_count)=16)), snapshot_rock_count BLOB CHECK(snapshot_rock_count IS NULL OR (typeof(snapshot_rock_count)='blob' AND length(snapshot_rock_count)=16)), snapshot_scissors_count BLOB CHECK(snapshot_scissors_count IS NULL OR (typeof(snapshot_scissors_count)='blob' AND length(snapshot_scissors_count)=16)), snapshot_paper_count BLOB CHECK(snapshot_paper_count IS NULL OR (typeof(snapshot_paper_count)='blob' AND length(snapshot_paper_count)=16)), stats_applied INTEGER NOT NULL DEFAULT 0 CHECK(stats_applied IN (0,1)), game_buy_in BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_buy_in)='blob' AND length(game_buy_in)=16 AND game_buy_in<=starting_balance), game_remaining BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(game_remaining)='blob' AND length(game_remaining)=16 AND game_remaining<=current_balance), PRIMARY KEY(session_id,seat_no), CHECK((current_gesture_envelope IS NULL AND current_gesture_phase_seq IS NULL) OR (current_gesture_envelope IS NOT NULL AND current_gesture_phase_seq IS NOT NULL)), CHECK((terminal_return IS NULL AND wallet_net_sign IS NULL AND wallet_net_mag IS NULL) OR (terminal_return IS NOT NULL AND wallet_net_sign IS NOT NULL AND wallet_net_mag IS NOT NULL)), CHECK((wallet_net_sign IS NULL AND wallet_net_mag IS NULL) OR (wallet_net_sign IS NOT NULL AND typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7') AND ((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')))), CHECK((snapshot_completed_count IS NULL AND snapshot_profitable_count IS NULL AND snapshot_rock_count IS NULL AND snapshot_scissors_count IS NULL AND snapshot_paper_count IS NULL) OR (snapshot_completed_count IS NOT NULL AND snapshot_profitable_count IS NOT NULL AND snapshot_rock_count IS NOT NULL AND snapshot_scissors_count IS NOT NULL AND snapshot_paper_count IS NOT NULL)), CHECK((deletion_state='active') OR (display_name_snapshot IS NULL AND avatar_url_snapshot IS NULL AND snapshot_completed_count IS NULL AND snapshot_profitable_count IS NULL AND snapshot_rock_count IS NULL AND snapshot_scissors_count IS NULL AND snapshot_paper_count IS NULL)));
CREATE UNIQUE INDEX idx_rps_seats_active_user ON game_rps_seats(user_id) WHERE user_id IS NOT NULL AND deletion_state='active';
CREATE INDEX idx_rps_seats_user ON game_rps_seats(user_id,session_id,seat_no);
CREATE TABLE game_rps_user_slots (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, queue_id TEXT REFERENCES game_rps_queue(id) ON DELETE CASCADE CHECK(queue_id IS NULL OR (length(queue_id)=27 AND substr(queue_id,1,5)='rpsq_' AND substr(queue_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(queue_id,-1,1) IN ('A','Q','g','w'))), session_id TEXT REFERENCES game_rps_sessions(id) ON DELETE CASCADE CHECK(session_id IS NULL OR (length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w'))), created_at INTEGER NOT NULL, CHECK((queue_id IS NOT NULL AND session_id IS NULL) OR (queue_id IS NULL AND session_id IS NOT NULL)));
CREATE UNIQUE INDEX idx_rps_slots_queue ON game_rps_user_slots(queue_id) WHERE queue_id IS NOT NULL;
CREATE INDEX idx_rps_slots_session ON game_rps_user_slots(session_id) WHERE session_id IS NOT NULL;
CREATE TABLE game_online_leases (session_id TEXT NOT NULL CHECK((length(session_id)=25 AND substr(session_id,1,3)='ll_' AND substr(session_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')) OR (length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w'))), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, lease_id TEXT NOT NULL CHECK(length(lease_id)=26 AND substr(lease_id,1,4)='gle_' AND substr(lease_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(lease_id,-1,1) IN ('A','Q','g','w')), health_epoch INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_renewed_at INTEGER NOT NULL, PRIMARY KEY(session_id,user_id,lease_id));
CREATE INDEX idx_game_leases_expiry ON game_online_leases(expires_at,session_id);
CREATE TABLE game_rps_pending_results (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, session_id_text TEXT NOT NULL CHECK(length(session_id_text)=26 AND substr(session_id_text,1,4)='rps_' AND substr(session_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id_text,-1,1) IN ('A','Q','g','w')), mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), terminal_reason TEXT NOT NULL, own_seat_no INTEGER NOT NULL CHECK(own_seat_no BETWEEN 0 AND 2), own_input BLOB NOT NULL CHECK(typeof(own_input)='blob' AND length(own_input)=32), own_returned BLOB NOT NULL CHECK(typeof(own_returned)='blob' AND length(own_returned)=32), own_wallet_net_sign INTEGER NOT NULL CHECK(own_wallet_net_sign IN (-1,0,1)), own_wallet_net_mag BLOB NOT NULL CHECK(typeof(own_wallet_net_mag)='blob' AND length(own_wallet_net_mag)=16 AND substr(hex(own_wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), seat0_result TEXT NOT NULL CHECK(seat0_result IN ('win','loss','tie','deidentified')), seat1_result TEXT NOT NULL CHECK(seat1_result IN ('win','loss','tie','deidentified')), seat2_result TEXT NOT NULL CHECK(seat2_result IN ('win','loss','tie','deidentified')), created_at INTEGER NOT NULL, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), general_buy_in BLOB CHECK(general_buy_in IS NULL OR (typeof(general_buy_in)='blob' AND length(general_buy_in)=16)), game_buy_in BLOB CHECK(game_buy_in IS NULL OR (typeof(game_buy_in)='blob' AND length(game_buy_in)=16)), own_returned_general BLOB CHECK(own_returned_general IS NULL OR (typeof(own_returned_general)='blob' AND length(own_returned_general)=16)), CHECK((own_wallet_net_sign=0 AND hex(own_wallet_net_mag)='00000000000000000000000000000000') OR (own_wallet_net_sign<>0 AND hex(own_wallet_net_mag)<>'00000000000000000000000000000000')));
CREATE TABLE game_rps_summaries (session_id TEXT NOT NULL PRIMARY KEY CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), rules_version INTEGER NOT NULL, base_milli INTEGER NOT NULL, platform_bp INTEGER NOT NULL, welfare_bp INTEGER NOT NULL, thursday_bp INTEGER NOT NULL, started_at INTEGER NOT NULL, terminal_at INTEGER NOT NULL, terminal_reason TEXT NOT NULL, base_round_count BLOB NOT NULL CHECK(typeof(base_round_count)='blob' AND length(base_round_count)=16), paid_tie_count BLOB NOT NULL CHECK(typeof(paid_tie_count)='blob' AND length(paid_tie_count)=16), free_tie_count BLOB NOT NULL CHECK(typeof(free_tie_count)='blob' AND length(free_tie_count)=16), total_timeout_count BLOB NOT NULL CHECK(typeof(total_timeout_count)='blob' AND length(total_timeout_count)=16), total_rock_count BLOB NOT NULL CHECK(typeof(total_rock_count)='blob' AND length(total_rock_count)=16), total_scissors_count BLOB NOT NULL CHECK(typeof(total_scissors_count)='blob' AND length(total_scissors_count)=16), total_paper_count BLOB NOT NULL CHECK(typeof(total_paper_count)='blob' AND length(total_paper_count)=16), platform_total BLOB NOT NULL CHECK(typeof(platform_total)='blob' AND length(platform_total)=16), welfare_total BLOB NOT NULL CHECK(typeof(welfare_total)='blob' AND length(welfare_total)=16), thursday_total BLOB NOT NULL CHECK(typeof(thursday_total)='blob' AND length(thursday_total)=16), delete_at INTEGER NOT NULL, CHECK(terminal_reason IN ('quick_resolved','standard_round_limit','standard_insufficient_balance','deathmatch_balance_exhausted','ultimate_resolved','free_tie_limit')), CHECK(platform_bp BETWEEN 0 AND 9999 AND welfare_bp BETWEEN 0 AND 9999 AND thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000));
CREATE INDEX idx_rps_summaries_retention ON game_rps_summaries(delete_at,session_id);
CREATE TABLE game_rps_summary_seats (session_id TEXT NOT NULL REFERENCES game_rps_summaries(session_id) ON DELETE CASCADE CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')), seat_no INTEGER NOT NULL CHECK(seat_no BETWEEN 0 AND 2), user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, input BLOB NOT NULL CHECK(typeof(input)='blob' AND length(input)=32), returned BLOB NOT NULL CHECK(typeof(returned)='blob' AND length(returned)=32), wallet_net_sign INTEGER NOT NULL CHECK(wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB NOT NULL CHECK(typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), timeout_count BLOB NOT NULL CHECK(typeof(timeout_count)='blob' AND length(timeout_count)=16), rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), general_buy_in BLOB CHECK(general_buy_in IS NULL OR (typeof(general_buy_in)='blob' AND length(general_buy_in)=16)), game_buy_in BLOB CHECK(game_buy_in IS NULL OR (typeof(game_buy_in)='blob' AND length(game_buy_in)=16)), PRIMARY KEY(session_id,seat_no), CHECK((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')));
CREATE TABLE game_rps_fun_stats (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, completed_count BLOB NOT NULL CHECK(typeof(completed_count)='blob' AND length(completed_count)=16), profitable_count BLOB NOT NULL CHECK(typeof(profitable_count)='blob' AND length(profitable_count)=16), rock_count BLOB NOT NULL CHECK(typeof(rock_count)='blob' AND length(rock_count)=16), scissors_count BLOB NOT NULL CHECK(typeof(scissors_count)='blob' AND length(scissors_count)=16), paper_count BLOB NOT NULL CHECK(typeof(paper_count)='blob' AND length(paper_count)=16), updated_at INTEGER NOT NULL);
CREATE TABLE game_rps_rank_facts (session_id_text TEXT NOT NULL CHECK(length(session_id_text)=26 AND substr(session_id_text,1,4)='rps_' AND substr(session_id_text,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id_text,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), terminal_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, wallet_net_sign INTEGER NOT NULL CHECK(wallet_net_sign IN (-1,0,1)), wallet_net_mag BLOB NOT NULL CHECK(typeof(wallet_net_mag)='blob' AND length(wallet_net_mag)=16 AND substr(hex(wallet_net_mag),1,1) IN ('0','1','2','3','4','5','6','7')), profitable INTEGER NOT NULL CHECK(profitable IN (0,1)), aggregate_applied INTEGER NOT NULL CHECK(aggregate_applied IN (0,1)), PRIMARY KEY(session_id_text,user_id), CHECK((wallet_net_sign=0 AND hex(wallet_net_mag)='00000000000000000000000000000000') OR (wallet_net_sign<>0 AND hex(wallet_net_mag)<>'00000000000000000000000000000000')), CHECK(expires_at=terminal_at+2592000));
CREATE INDEX idx_rps_rank_facts_due ON game_rps_rank_facts(expires_at,session_id_text,user_id);
CREATE INDEX idx_rps_rank_facts_mode ON game_rps_rank_facts(mode,user_id,expires_at);
CREATE TABLE game_rps_rank_aggregates (user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, mode TEXT NOT NULL CHECK(mode IN ('quick','standard','deathmatch')), session_count BLOB NOT NULL CHECK(typeof(session_count)='blob' AND length(session_count)=16), profitable_count BLOB NOT NULL CHECK(typeof(profitable_count)='blob' AND length(profitable_count)=16), net_profit_sign INTEGER NOT NULL CHECK(net_profit_sign IN (-1,0,1)), net_profit_mag BLOB NOT NULL CHECK(typeof(net_profit_mag)='blob' AND length(net_profit_mag)=16 AND substr(hex(net_profit_mag),1,1) IN ('0','1','2','3','4','5','6','7')), eligible INTEGER NOT NULL CHECK(eligible IN (0,1)), profit_rate_bp INTEGER NOT NULL CHECK(profit_rate_bp BETWEEN 0 AND 10000), profit_rate_achieved_at INTEGER NOT NULL, net_profit_achieved_at INTEGER NOT NULL, profit_public_tie_key BLOB NOT NULL CHECK(typeof(profit_public_tie_key)='blob' AND length(profit_public_tie_key)=32), net_public_tie_key BLOB NOT NULL CHECK(typeof(net_public_tie_key)='blob' AND length(net_public_tie_key)=32), revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16), updated_at INTEGER NOT NULL, PRIMARY KEY(user_id,mode), CHECK((net_profit_sign=0 AND hex(net_profit_mag)='00000000000000000000000000000000') OR (net_profit_sign<>0 AND hex(net_profit_mag)<>'00000000000000000000000000000000')));
CREATE UNIQUE INDEX idx_rps_rank_profit ON game_rps_rank_aggregates(mode,profit_rate_bp DESC,profit_rate_achieved_at,profit_public_tie_key) WHERE eligible=1;
CREATE INDEX idx_rps_rank_net ON game_rps_rank_aggregates(mode,net_profit_sign DESC,CASE WHEN net_profit_sign=1 THEN net_profit_mag END DESC,CASE WHEN net_profit_sign=-1 THEN net_profit_mag END ASC,net_profit_achieved_at ASC,net_public_tie_key) WHERE eligible=1;
CREATE TABLE site_config (key TEXT NOT NULL PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL DEFAULT 0);
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
 WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.donation_id=NEW.donation_id AND d.endpoint_key_id=NEW.endpoint_key_id AND d.ended_at IS NULL)
 BEGIN SELECT RAISE(ABORT,'donation key membership identity mismatch'); END;
CREATE TRIGGER donation_key_membership_consistency_update_guard BEFORE UPDATE OF endpoint_key_id,donation_key_id,donation_id ON donation_key_memberships
 WHEN NOT EXISTS(SELECT 1 FROM donation_keys d WHERE d.id=NEW.donation_key_id AND d.donation_id=NEW.donation_id AND d.endpoint_key_id=NEW.endpoint_key_id AND d.ended_at IS NULL)
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
         NEW.first_read_at=OLD.first_read_at AND NEW.last_read_at>=OLD.last_read_at AND NEW.read_count=OLD.read_count+1 AND
         NEW.retain_until IS OLD.retain_until AND (NEW.retain_until IS NULL OR
          EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state IN ('released','expired') AND NEW.retain_until=h.retain_until))) OR
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
 OR (NEW.report_match_until IS NOT NULL AND (typeof(NEW.report_match_until)<>'integer' OR NEW.report_match_until NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation key timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donation_keys_time_update_guard BEFORE UPDATE ON donation_keys
WHEN typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.ended_at IS NOT NULL AND (typeof(NEW.ended_at)<>'integer' OR NEW.ended_at NOT BETWEEN 0 AND 253402300799))
 OR (NEW.report_match_until IS NOT NULL AND (typeof(NEW.report_match_until)<>'integer' OR NEW.report_match_until NOT BETWEEN 0 AND 253402300799))
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
WHEN (NEW.reviewed_at IS NOT NULL AND (typeof(NEW.reviewed_at)<>'integer' OR NEW.reviewed_at NOT BETWEEN 0 AND 253402300799))
 OR typeof(NEW.created_at)<>'integer' OR NEW.created_at NOT BETWEEN 0 AND 253402300799
 OR typeof(NEW.updated_at)<>'integer' OR NEW.updated_at NOT BETWEEN 0 AND 253402300799
 OR (NEW.terminal_at IS NOT NULL AND (typeof(NEW.terminal_at)<>'integer' OR NEW.terminal_at NOT BETWEEN 0 AND 253402300799))
BEGIN SELECT RAISE(ABORT,'donation timestamp is outside UTC range'); END;
CREATE TRIGGER generation_two_donations_time_update_guard BEFORE UPDATE ON donations
WHEN (NEW.reviewed_at IS NOT NULL AND (typeof(NEW.reviewed_at)<>'integer' OR NEW.reviewed_at NOT BETWEEN 0 AND 253402300799))
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
 OR (NEW.authorized_expires_at IS NOT NULL AND typeof(NEW.authorized_expires_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.mainstream_channel_revision IS NOT NULL AND typeof(NEW.mainstream_channel_revision)<>'integer')
 OR (NEW.source_endpoint_key_id IS NOT NULL AND typeof(NEW.source_endpoint_key_id)<>'integer')
 OR (NEW.report_match_until IS NOT NULL AND typeof(NEW.report_match_until)<>'integer')
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
 OR (NEW.authorized_expires_at IS NOT NULL AND typeof(NEW.authorized_expires_at)<>'integer')
 OR (NEW.expires_at IS NOT NULL AND typeof(NEW.expires_at)<>'integer')
 OR (NEW.mainstream_channel_revision IS NOT NULL AND typeof(NEW.mainstream_channel_revision)<>'integer')
 OR (NEW.source_endpoint_key_id IS NOT NULL AND typeof(NEW.source_endpoint_key_id)<>'integer')
 OR (NEW.report_match_until IS NOT NULL AND typeof(NEW.report_match_until)<>'integer')
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
 OR (NEW.cursor_id IS NOT NULL AND typeof(NEW.cursor_id)<>'integer')
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
 OR (NEW.cursor_id IS NOT NULL AND typeof(NEW.cursor_id)<>'integer')
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
 OR (NEW.source_endpoint_key_id IS NOT NULL AND typeof(NEW.source_endpoint_key_id)<>'integer')
 OR (NEW.owner_user_id IS NOT NULL AND typeof(NEW.owner_user_id)<>'integer')
 OR (NEW.discovered_version IS NOT NULL AND typeof(NEW.discovered_version)<>'integer')
 OR (NEW.decided_version IS NOT NULL AND typeof(NEW.decided_version)<>'integer')
 OR (NEW.created_at IS NOT NULL AND typeof(NEW.created_at)<>'integer')
 OR (NEW.updated_at IS NOT NULL AND typeof(NEW.updated_at)<>'integer')
BEGIN SELECT RAISE(ABORT,'INTEGER column has non-integer storage'); END;
CREATE TRIGGER generation_two_integer_type_report_targets_update_guard BEFORE UPDATE ON report_targets
WHEN (NEW.target_seq IS NOT NULL AND typeof(NEW.target_seq)<>'integer')
 OR (NEW.endpoint_key_id IS NOT NULL AND typeof(NEW.endpoint_key_id)<>'integer')
 OR (NEW.source_endpoint_key_id IS NOT NULL AND typeof(NEW.source_endpoint_key_id)<>'integer')
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
CREATE TABLE charity_model_routing (
 model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
 strategy TEXT NOT NULL CHECK(strategy IN ('ordered','random','expiry_weighted','cache_balanced'))
);
CREATE TABLE endpoint_key_limits (
 endpoint_key_id INTEGER PRIMARY KEY REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 max_concurrency INTEGER NOT NULL DEFAULT 0 CHECK(typeof(max_concurrency)='integer' AND max_concurrency BETWEEN 0 AND 2147483647),
 max_rpm INTEGER NOT NULL DEFAULT 0 CHECK(typeof(max_rpm)='integer' AND max_rpm BETWEEN 0 AND 2147483647)
);
CREATE INDEX idx_dispatch_claims_key_active ON dispatch_claims(endpoint_key_id,state) WHERE purpose<>'discovery' AND state IN ('claimed','dispatched');
CREATE INDEX idx_dispatch_claims_key_rpm ON dispatch_claims(endpoint_key_id,dispatched_at) WHERE purpose<>'discovery' AND dispatched_at IS NOT NULL;
CREATE TABLE dispatch_response_starts (
 claim_id TEXT NOT NULL PRIMARY KEY REFERENCES dispatch_claims(id) ON DELETE CASCADE CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')),
 started_at INTEGER NOT NULL CHECK(typeof(started_at)='integer' AND started_at BETWEEN 0 AND 253402300799)
);
CREATE TABLE donation_handling (
 donation_id INTEGER PRIMARY KEY REFERENCES donations(id) ON DELETE CASCADE CHECK(donation_id>0),
 state TEXT NOT NULL CHECK(state IN ('legacy','pending','processed','closed')),
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9223372036854775807),
 processed_at INTEGER CHECK(processed_at IS NULL OR (typeof(processed_at)='integer' AND processed_at BETWEEN 0 AND 253402300799)),
 processed_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 processed_by_role TEXT NOT NULL DEFAULT '' CHECK(processed_by_role IN ('','admin','level5','level6','trainee5')),
 closed_at INTEGER CHECK(closed_at IS NULL OR (typeof(closed_at)='integer' AND closed_at BETWEEN 0 AND 253402300799)),
 closed_reason TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799), updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799),
 CHECK((state IN ('legacy','pending') AND processed_at IS NULL AND processed_by_user_id IS NULL AND processed_by_role='' AND closed_at IS NULL AND closed_reason='')
   OR (state='processed' AND processed_at IS NOT NULL AND processed_by_role<>'' AND closed_at IS NULL AND closed_reason='')
   OR (state='closed' AND closed_at IS NOT NULL AND closed_reason<>'' AND processed_at IS NULL AND processed_by_user_id IS NULL AND processed_by_role=''))
);
CREATE INDEX idx_donation_handling_state ON donation_handling(state,donation_id);
CREATE TABLE charity_model_access (
 model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
 allowed_level_mask INTEGER NOT NULL DEFAULT 63 CHECK(typeof(allowed_level_mask)='integer' AND allowed_level_mask BETWEEN 0 AND 63),
 public_description TEXT NOT NULL DEFAULT '' CHECK(typeof(public_description)='text'),
 CHECK(length(public_description)<=1024),
 CHECK(length(cast(public_description AS BLOB))<=4096),
 CHECK(instr(public_description,char(0))=0),
 CHECK(public_description NOT GLOB ('*['||char(1)||'-'||char(8)||char(11)||'-'||char(31)||char(127)||'-'||char(159)||']*'))
);
CREATE TABLE donation_quota_capacity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 rows_used INTEGER NOT NULL CHECK(typeof(rows_used)='integer' AND rows_used BETWEEN 0 AND 5000000),
 rows_held INTEGER NOT NULL CHECK(typeof(rows_held)='integer' AND rows_held BETWEEN 0 AND 5000000),
 CHECK(rows_used+rows_held<=5000000)
);
CREATE TABLE game_rps_presentation (
 session_id TEXT NOT NULL PRIMARY KEY REFERENCES game_rps_sessions(id) ON DELETE CASCADE CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')),
 pool_tie_count BLOB CHECK(pool_tie_count IS NULL OR (typeof(pool_tie_count)='blob' AND length(pool_tie_count)=16)),
 quick_seat0_gesture TEXT CHECK(quick_seat0_gesture IS NULL OR quick_seat0_gesture IN ('rock','scissors','paper')),
 quick_seat1_gesture TEXT CHECK(quick_seat1_gesture IS NULL OR quick_seat1_gesture IN ('rock','scissors','paper')),
 quick_seat2_gesture TEXT CHECK(quick_seat2_gesture IS NULL OR quick_seat2_gesture IN ('rock','scissors','paper')),
 CHECK((quick_seat0_gesture IS NULL AND quick_seat1_gesture IS NULL AND quick_seat2_gesture IS NULL)
   OR (quick_seat0_gesture IS NOT NULL AND quick_seat1_gesture IS NOT NULL AND quick_seat2_gesture IS NOT NULL))
);
CREATE TABLE game_rps_pending_presentation (
 user_id INTEGER PRIMARY KEY REFERENCES game_rps_pending_results(user_id) ON DELETE CASCADE,
 own_buy_in BLOB CHECK(own_buy_in IS NULL OR (typeof(own_buy_in)='blob' AND length(own_buy_in)=16)),
 own_cash_out BLOB CHECK(own_cash_out IS NULL OR (typeof(own_cash_out)='blob' AND length(own_cash_out)=16)),
 quick_seat0_gesture TEXT CHECK(quick_seat0_gesture IS NULL OR quick_seat0_gesture IN ('rock','scissors','paper')),
 quick_seat1_gesture TEXT CHECK(quick_seat1_gesture IS NULL OR quick_seat1_gesture IN ('rock','scissors','paper')),
 quick_seat2_gesture TEXT CHECK(quick_seat2_gesture IS NULL OR quick_seat2_gesture IN ('rock','scissors','paper')),
 CHECK((own_buy_in IS NULL AND own_cash_out IS NULL) OR (own_buy_in IS NOT NULL AND own_cash_out IS NOT NULL)),
 CHECK((quick_seat0_gesture IS NULL AND quick_seat1_gesture IS NULL AND quick_seat2_gesture IS NULL)
   OR (quick_seat0_gesture IS NOT NULL AND quick_seat1_gesture IS NOT NULL AND quick_seat2_gesture IS NOT NULL))
);
CREATE TABLE game_rps_summary_presentation (
 session_id TEXT NOT NULL CHECK(length(session_id)=26 AND substr(session_id,1,4)='rps_' AND substr(session_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(session_id,-1,1) IN ('A','Q','g','w')),
 seat_no INTEGER NOT NULL CHECK(typeof(seat_no)='integer' AND seat_no BETWEEN 0 AND 2),
 own_buy_in BLOB CHECK(own_buy_in IS NULL OR (typeof(own_buy_in)='blob' AND length(own_buy_in)=16)),
 own_cash_out BLOB CHECK(own_cash_out IS NULL OR (typeof(own_cash_out)='blob' AND length(own_cash_out)=16)),
 CHECK((own_buy_in IS NULL AND own_cash_out IS NULL) OR (own_buy_in IS NOT NULL AND own_cash_out IS NOT NULL)),
 PRIMARY KEY(session_id,seat_no),
 FOREIGN KEY(session_id,seat_no) REFERENCES game_rps_summary_seats(session_id,seat_no) ON DELETE CASCADE
);
CREATE TABLE donation_quota_rules (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='qlr_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 donation_key_id INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE CHECK(typeof(donation_key_id)='integer' AND donation_key_id>0),
 current_epoch INTEGER CHECK(current_epoch IS NULL OR (typeof(current_epoch)='integer' AND current_epoch BETWEEN 1 AND 9223372036854775807)),
 display_order INTEGER CHECK(display_order IS NULL OR (typeof(display_order)='integer' AND display_order BETWEEN 0 AND 15)),
 CHECK((current_epoch IS NULL AND display_order IS NULL) OR (current_epoch IS NOT NULL AND display_order IS NOT NULL)),
 FOREIGN KEY(id, current_epoch) REFERENCES donation_quota_epochs(rule_id, epoch)
);
CREATE UNIQUE INDEX idx_donation_quota_rules_key_order ON donation_quota_rules(donation_key_id, display_order) WHERE current_epoch IS NOT NULL;
CREATE TABLE donation_quota_epochs (
 rule_id TEXT NOT NULL REFERENCES donation_quota_rules(id) ON DELETE CASCADE CHECK(length(rule_id)=26 AND substr(rule_id,1,4)='qlr_' AND substr(rule_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(rule_id,-1,1) IN ('A','Q','g','w')),
 epoch INTEGER NOT NULL CHECK(typeof(epoch)='integer' AND epoch BETWEEN 1 AND 9223372036854775807),
 mode TEXT NOT NULL CHECK(mode IN ('reset','sliding')),
 interval TEXT NOT NULL CHECK(interval IN ('1h','5h','day','week','month')),
 alignment TEXT CHECK(alignment IS NULL OR alignment IN ('first_success','calendar','exact_time')),
 anchor_local TEXT CHECK(anchor_local IS NULL OR (typeof(anchor_local)='text' AND length(anchor_local)=19 AND anchor_local GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]')),
 time_zone TEXT NOT NULL CHECK(typeof(time_zone)='text' AND length(CAST(time_zone AS BLOB)) BETWEEN 1 AND 64 AND time_zone NOT GLOB '*[^ -~]*'),
 week_starts_on INTEGER CHECK(week_starts_on IS NULL OR (typeof(week_starts_on)='integer' AND week_starts_on BETWEEN 1 AND 7)),
 metric TEXT NOT NULL CHECK(metric IN ('calls','tokens','credits','input_tokens','output_tokens')),
 limit_mag BLOB NOT NULL CHECK(typeof(limit_mag)='blob' AND length(limit_mag)=16),
 effective_at INTEGER NOT NULL CHECK(typeof(effective_at)='integer' AND effective_at BETWEEN 0 AND 253402300799),
 retired_at INTEGER CHECK(retired_at IS NULL OR (typeof(retired_at)='integer' AND retired_at BETWEEN 0 AND 253402300799)),
 last_observed_at INTEGER CHECK(last_observed_at IS NULL OR (typeof(last_observed_at)='integer' AND last_observed_at BETWEEN 0 AND 253402300799)),
 current_period_start INTEGER CHECK(current_period_start IS NULL OR (typeof(current_period_start)='integer' AND current_period_start BETWEEN -62167219200 AND 253402300799)),
 window_left INTEGER CHECK(window_left IS NULL OR (typeof(window_left)='integer' AND window_left BETWEEN -62167219200 AND 253402300799)),
 window_at INTEGER CHECK(window_at IS NULL OR (typeof(window_at)='integer' AND window_at BETWEEN 0 AND 253402300799)),
 window_used BLOB CHECK(window_used IS NULL OR (typeof(window_used)='blob' AND length(window_used)=16)),
 window_reserved BLOB CHECK(window_reserved IS NULL OR (typeof(window_reserved)='blob' AND length(window_reserved)=16)),
 pending_reserved BLOB NOT NULL CHECK(typeof(pending_reserved)='blob' AND length(pending_reserved)=16),
 PRIMARY KEY(rule_id, epoch),
 FOREIGN KEY(rule_id,epoch,current_period_start) REFERENCES donation_quota_periods(rule_id,epoch,start_at) DEFERRABLE INITIALLY DEFERRED,
 CHECK((alignment IS 'exact_time' AND anchor_local IS NOT NULL) OR (alignment IS NOT 'exact_time' AND anchor_local IS NULL)),
 CHECK((window_left IS NULL AND window_at IS NULL AND window_used IS NULL AND window_reserved IS NULL)
   OR (window_left IS NOT NULL AND window_at IS NOT NULL AND window_used IS NOT NULL AND window_reserved IS NOT NULL AND window_left<window_at)),
 CHECK(retired_at IS NULL OR retired_at>=effective_at),
 CHECK(
  (mode='sliding' AND alignment IS NULL AND week_starts_on IS NULL AND current_period_start IS NULL)
  OR
  (mode='reset' AND alignment IS NOT NULL AND alignment IN ('first_success','calendar','exact_time')
   AND NOT (alignment IN ('calendar','exact_time') AND interval IN ('1h','5h'))
   AND ((alignment='calendar' AND interval='week' AND week_starts_on IS NOT NULL)
     OR ((alignment<>'calendar' OR interval<>'week') AND week_starts_on IS NULL))
   AND window_left IS NULL AND window_at IS NULL AND window_used IS NULL AND window_reserved IS NULL)
 )
);
CREATE INDEX idx_donation_quota_epochs_retired ON donation_quota_epochs(rule_id, retired_at) WHERE retired_at IS NOT NULL;
CREATE TABLE donation_quota_receipts (
 claim_id TEXT NOT NULL REFERENCES dispatch_claims(id) ON DELETE CASCADE CHECK(length(claim_id)=26 AND substr(claim_id,1,4)='clm_' AND substr(claim_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(claim_id,-1,1) IN ('A','Q','g','w')),
 rule_id TEXT NOT NULL CHECK(length(rule_id)=26 AND substr(rule_id,1,4)='qlr_' AND substr(rule_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(rule_id,-1,1) IN ('A','Q','g','w')),
 epoch INTEGER NOT NULL CHECK(typeof(epoch)='integer' AND epoch BETWEEN 1 AND 9223372036854775807),
 state TEXT NOT NULL CHECK(state IN ('reserved','started','settled')),
 reserved_mag BLOB CHECK(reserved_mag IS NULL OR (typeof(reserved_mag)='blob' AND length(reserved_mag)=16)),
 remaining_reserved_mag BLOB CHECK(remaining_reserved_mag IS NULL OR (typeof(remaining_reserved_mag)='blob' AND length(remaining_reserved_mag)=16)),
 actual_mag BLOB CHECK(actual_mag IS NULL OR (typeof(actual_mag)='blob' AND length(actual_mag)=16)),
 success_at INTEGER CHECK(success_at IS NULL OR (typeof(success_at)='integer' AND success_at BETWEEN 0 AND 253402300799)),
 period_start INTEGER CHECK(period_start IS NULL OR (typeof(period_start)='integer' AND period_start BETWEEN -62167219200 AND 253402300799)),
 capacity_state TEXT NOT NULL CHECK(capacity_state IN ('held','attached','released')),
 PRIMARY KEY(claim_id, rule_id, epoch),
 FOREIGN KEY(rule_id, epoch) REFERENCES donation_quota_epochs(rule_id, epoch) ON DELETE RESTRICT,
 FOREIGN KEY(rule_id,epoch,period_start) REFERENCES donation_quota_periods(rule_id,epoch,start_at) DEFERRABLE INITIALLY DEFERRED,
 CHECK(
  (state='reserved' AND reserved_mag IS NOT NULL AND remaining_reserved_mag IS NOT NULL AND actual_mag IS NULL AND success_at IS NULL AND period_start IS NULL)
  OR (state='started' AND reserved_mag IS NOT NULL AND remaining_reserved_mag IS NOT NULL AND success_at IS NOT NULL)
  OR (state='settled' AND reserved_mag IS NULL AND remaining_reserved_mag IS NULL AND actual_mag IS NOT NULL)
 )
);
CREATE INDEX idx_donation_quota_receipts_epoch ON donation_quota_receipts(rule_id, epoch, success_at);
CREATE TABLE donation_quota_periods (
 rule_id TEXT NOT NULL CHECK(length(rule_id)=26 AND substr(rule_id,1,4)='qlr_' AND substr(rule_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(rule_id,-1,1) IN ('A','Q','g','w')),
 epoch INTEGER NOT NULL CHECK(typeof(epoch)='integer' AND epoch BETWEEN 1 AND 9223372036854775807),
 start_at INTEGER NOT NULL CHECK(typeof(start_at)='integer' AND start_at BETWEEN -62167219200 AND 253402300799),
 end_at INTEGER NOT NULL CHECK(typeof(end_at)='integer' AND end_at BETWEEN -62167219200 AND 253402300799),
 used_mag BLOB NOT NULL CHECK(typeof(used_mag)='blob' AND length(used_mag)=16),
 reserved_mag BLOB NOT NULL CHECK(typeof(reserved_mag)='blob' AND length(reserved_mag)=16),
 PRIMARY KEY(rule_id, epoch, start_at),
 FOREIGN KEY(rule_id, epoch) REFERENCES donation_quota_epochs(rule_id, epoch) ON DELETE CASCADE,
 CHECK(end_at>start_at)
);
CREATE INDEX idx_donation_quota_periods_end ON donation_quota_periods(rule_id, epoch, end_at);
CREATE TABLE donation_quota_buckets (
 rule_id TEXT NOT NULL CHECK(length(rule_id)=26 AND substr(rule_id,1,4)='qlr_' AND substr(rule_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(rule_id,-1,1) IN ('A','Q','g','w')),
 epoch INTEGER NOT NULL CHECK(typeof(epoch)='integer' AND epoch BETWEEN 1 AND 9223372036854775807),
 success_at INTEGER NOT NULL CHECK(typeof(success_at)='integer' AND success_at BETWEEN 0 AND 253402300799),
 used_mag BLOB NOT NULL CHECK(typeof(used_mag)='blob' AND length(used_mag)=16),
 reserved_mag BLOB NOT NULL CHECK(typeof(reserved_mag)='blob' AND length(reserved_mag)=16),
 PRIMARY KEY(rule_id, epoch, success_at),
 FOREIGN KEY(rule_id, epoch) REFERENCES donation_quota_epochs(rule_id, epoch) ON DELETE CASCADE
);
CREATE TRIGGER donation_quota_period_mode_insert BEFORE INSERT ON donation_quota_periods
WHEN NOT EXISTS(SELECT 1 FROM donation_quota_epochs WHERE rule_id=NEW.rule_id AND epoch=NEW.epoch AND mode='reset')
BEGIN SELECT RAISE(ABORT,'quota period requires reset epoch'); END;
CREATE TRIGGER donation_quota_period_mode_update BEFORE UPDATE OF rule_id,epoch ON donation_quota_periods
WHEN NOT EXISTS(SELECT 1 FROM donation_quota_epochs WHERE rule_id=NEW.rule_id AND epoch=NEW.epoch AND mode='reset')
BEGIN SELECT RAISE(ABORT,'quota period requires reset epoch'); END;
CREATE TRIGGER donation_quota_bucket_mode_insert BEFORE INSERT ON donation_quota_buckets
WHEN NOT EXISTS(SELECT 1 FROM donation_quota_epochs WHERE rule_id=NEW.rule_id AND epoch=NEW.epoch AND mode='sliding')
BEGIN SELECT RAISE(ABORT,'quota bucket requires sliding epoch'); END;
CREATE TRIGGER donation_quota_bucket_mode_update BEFORE UPDATE OF rule_id,epoch ON donation_quota_buckets
WHEN NOT EXISTS(SELECT 1 FROM donation_quota_epochs WHERE rule_id=NEW.rule_id AND epoch=NEW.epoch AND mode='sliding')
BEGIN SELECT RAISE(ABORT,'quota bucket requires sliding epoch'); END;
CREATE TRIGGER donation_quota_epoch_mode_update BEFORE UPDATE OF mode ON donation_quota_epochs
WHEN (NEW.mode<>'reset' AND EXISTS(SELECT 1 FROM donation_quota_periods WHERE rule_id=OLD.rule_id AND epoch=OLD.epoch))
  OR (NEW.mode<>'sliding' AND EXISTS(SELECT 1 FROM donation_quota_buckets WHERE rule_id=OLD.rule_id AND epoch=OLD.epoch))
BEGIN SELECT RAISE(ABORT,'quota epoch mode conflicts with aggregates'); END;
CREATE INDEX idx_report_cases_created ON report_cases(created_at,id);
CREATE INDEX idx_report_materials_created ON report_materials(case_id,created_at,id);
CREATE INDEX idx_legal_holds_created ON legal_holds(created_at,id);
CREATE INDEX idx_endpoints_base_users ON endpoints(base_url,user_id,id);
CREATE INDEX idx_shared_pools_created ON shared_pools(created_at,id);
CREATE INDEX idx_request_logs_started ON request_logs(started_at,id);
CREATE INDEX idx_models_user_updated ON models(user_id,updated_at,id);
CREATE INDEX idx_model_bindings_key_browse ON model_bindings(endpoint_key_id,upstream_model_id,id);
CREATE INDEX idx_model_catalog_key_source ON model_catalog_entries(endpoint_key_id,source_type,id);
CREATE INDEX idx_mainstream_channels_updated ON mainstream_channels(updated_at,id);
CREATE INDEX idx_donations_owner_page ON donations(user_id,id);
CREATE INDEX idx_charity_bindings_key ON charity_model_bindings(donation_key_id,id);
CREATE INDEX idx_donation_keys_source_page ON donation_keys(
 COALESCE(mainstream_channel_id,''),
 CASE WHEN mainstream_channel_id IS NULL THEN connector_type ELSE '' END,
 CASE WHEN mainstream_channel_id IS NULL THEN canonical_base_url ELSE '' END,
 id
);
CREATE INDEX idx_donation_quota_buckets_cleanup ON donation_quota_buckets(success_at,rule_id,epoch);
CREATE INDEX idx_donation_quota_periods_cleanup ON donation_quota_periods(end_at,rule_id,epoch,start_at);
CREATE INDEX idx_donation_quota_epochs_clock ON donation_quota_epochs(COALESCE(last_observed_at,effective_at),rule_id,epoch) WHERE mode='reset';
CREATE INDEX idx_donation_quota_receipts_settled ON donation_quota_receipts(claim_id,rule_id,epoch) WHERE state='settled';
CREATE INDEX idx_donation_quota_receipts_period ON donation_quota_receipts(rule_id,epoch,period_start) WHERE period_start IS NOT NULL;
CREATE INDEX idx_donation_quota_rules_retired ON donation_quota_rules(id) WHERE current_epoch IS NULL;
CREATE TABLE legal_hold_steward_reads (
 id INTEGER PRIMARY KEY,
 hold_id_text TEXT NOT NULL REFERENCES legal_holds(id) ON DELETE CASCADE,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL CHECK(user_id IS NULL OR (typeof(user_id)='integer' AND user_id>0)),
 first_read_at INTEGER NOT NULL CHECK(typeof(first_read_at)='integer' AND first_read_at BETWEEN 0 AND 253402300799),
 last_read_at INTEGER NOT NULL CHECK(typeof(last_read_at)='integer' AND last_read_at BETWEEN first_read_at AND 253402300799),
 read_count INTEGER NOT NULL CHECK(typeof(read_count)='integer' AND read_count BETWEEN 1 AND 9223372036854775807),
 UNIQUE(hold_id_text,user_id)
);
CREATE INDEX idx_legal_hold_steward_reads_user ON legal_hold_steward_reads(user_id);
CREATE TABLE game_fishing_outcome_lengths (
 batch_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(typeof(ordinal)='integer' AND ordinal BETWEEN 0 AND 9),
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201')),
 PRIMARY KEY(batch_id,ordinal),
 FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE CASCADE
);
CREATE TRIGGER fishing_outcome_length_insert_guard BEFORE INSERT ON game_fishing_outcome_lengths
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o JOIN game_fishing_batches b ON b.id=o.batch_id
 WHERE o.batch_id=NEW.batch_id AND o.ordinal=NEW.ordinal AND o.tier='legend' AND b.state='reserved')
BEGIN SELECT RAISE(ABORT,'fishing presentation requires a reserved legend'); END;
CREATE TRIGGER fishing_outcome_length_update_guard BEFORE UPDATE ON game_fishing_outcome_lengths
BEGIN SELECT RAISE(ABORT,'fishing presentation is immutable'); END;
CREATE TRIGGER fishing_outcome_length_delete_guard BEFORE DELETE ON game_fishing_outcome_lengths
WHEN EXISTS(SELECT 1 FROM game_fishing_outcomes o JOIN game_fishing_batches b ON b.id=o.batch_id
 WHERE o.batch_id=OLD.batch_id AND o.ordinal=OLD.ordinal AND b.state<>'reserved')
BEGIN SELECT RAISE(ABORT,'terminal fishing presentation is immutable'); END;
CREATE TRIGGER fishing_presented_outcome_update_guard BEFORE UPDATE ON game_fishing_outcomes
WHEN EXISTS(SELECT 1 FROM game_fishing_outcome_lengths l WHERE l.batch_id=OLD.batch_id AND l.ordinal=OLD.ordinal)
BEGIN SELECT RAISE(ABORT,'presented fishing outcome is immutable'); END;
CREATE TABLE game_fishing_best_lengths (
 user_id INTEGER PRIMARY KEY REFERENCES game_fishing_best(user_id) ON DELETE CASCADE,
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201'))
);
CREATE INDEX idx_fishing_best_lengths_rank ON game_fishing_best_lengths(length(length_cm) DESC,length_cm DESC,user_id);
CREATE TRIGGER fishing_best_length_insert_guard BEFORE INSERT ON game_fishing_best_lengths
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_best b JOIN game_fishing_outcome_lengths l
 ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
CREATE TRIGGER fishing_best_length_update_guard BEFORE UPDATE ON game_fishing_best_lengths
WHEN NEW.user_id IS NOT OLD.user_id OR NOT EXISTS(SELECT 1 FROM game_fishing_best b
 JOIN game_fishing_outcome_lengths l ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
CREATE TRIGGER fishing_presented_best_update_guard BEFORE UPDATE ON game_fishing_best
WHEN EXISTS(SELECT 1 FROM game_fishing_best_lengths l WHERE l.user_id=OLD.user_id)
 AND NOT (
 (NEW.user_id IS OLD.user_id AND NEW.species_key IS OLD.species_key AND NEW.tier IS OLD.tier
  AND NEW.size_cm IS OLD.size_cm AND NEW.caught_at IS OLD.caught_at AND NEW.public_tie_key IS OLD.public_tie_key
  AND NEW.batch_id IS NULL AND NEW.ordinal IS NULL AND OLD.batch_id IS NOT NULL
  AND NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o WHERE o.batch_id=OLD.batch_id AND o.ordinal=OLD.ordinal))
 OR (NEW.user_id IS OLD.user_id AND NEW.tier='legend' AND EXISTS(
  SELECT 1 FROM game_fishing_best_lengths previous JOIN game_fishing_outcome_lengths fresh
  ON fresh.batch_id=NEW.batch_id AND fresh.ordinal=NEW.ordinal
  WHERE previous.user_id=OLD.user_id AND previous.length_cm=fresh.length_cm)))
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
CREATE TABLE game_fishing_length_facts (
 batch_id_text TEXT PRIMARY KEY NOT NULL REFERENCES game_fishing_rank_facts(batch_id_text) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(typeof(ordinal)='integer' AND ordinal BETWEEN 0 AND 9),
 species_key TEXT NOT NULL,
 tier TEXT NOT NULL CHECK(tier IN ('junk','small','regular','big','giant','legend','treasure')),
 size_cm INTEGER NOT NULL CHECK(typeof(size_cm)='integer' AND size_cm BETWEEN 0 AND 200),
 caught_at INTEGER NOT NULL CHECK(typeof(caught_at)='integer' AND caught_at BETWEEN 0 AND 253402300799),
 blue_fat_fish_length_cm TEXT CHECK(blue_fat_fish_length_cm IS NULL OR
  (tier='legend' AND typeof(blue_fat_fish_length_cm)='text' AND length(blue_fat_fish_length_cm) BETWEEN 3 AND 128
   AND length(CAST(blue_fat_fish_length_cm AS BLOB))=length(blue_fat_fish_length_cm)
   AND substr(blue_fat_fish_length_cm,1,1)<>'0' AND blue_fat_fish_length_cm NOT GLOB '*[^0-9]*'
   AND (length(blue_fat_fish_length_cm)>3 OR blue_fat_fish_length_cm>='201')))
);
CREATE TRIGGER fishing_length_fact_insert_guard BEFORE INSERT ON game_fishing_length_facts
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_rank_facts f
 JOIN game_fishing_batches b ON b.id=f.batch_id_text AND b.user_id=f.user_id
 JOIN game_fishing_outcomes o ON o.batch_id=b.id AND o.ordinal=NEW.ordinal
 LEFT JOIN game_fishing_outcome_lengths l ON l.batch_id=o.batch_id AND l.ordinal=o.ordinal
 WHERE f.batch_id_text=NEW.batch_id_text AND f.aggregate_applied=1 AND b.state IN ('reserved','committed')
 AND NEW.species_key=o.species_key AND NEW.tier=o.tier AND NEW.size_cm=o.size_cm
 AND NEW.caught_at=b.created_at AND NEW.blue_fat_fish_length_cm IS l.length_cm
 AND (SELECT count(*) FROM game_fishing_outcomes all_o WHERE all_o.batch_id=b.id)=b.count
 AND NEW.ordinal=(SELECT all_o.ordinal FROM game_fishing_outcomes all_o
  LEFT JOIN game_fishing_outcome_lengths all_l ON all_l.batch_id=all_o.batch_id AND all_l.ordinal=all_o.ordinal
  WHERE all_o.batch_id=b.id ORDER BY length(COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT))) DESC,
  COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT)) COLLATE BINARY DESC,all_o.ordinal ASC LIMIT 1))
BEGIN SELECT RAISE(ABORT,'fishing length fact does not match batch maximum'); END;
CREATE TRIGGER fishing_length_fact_update_guard BEFORE UPDATE ON game_fishing_length_facts
BEGIN SELECT RAISE(ABORT,'fishing length fact is immutable'); END;
CREATE TABLE charity_model_token_reserves (
    model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
    amount_milli INTEGER NOT NULL CHECK(typeof(amount_milli)='integer' AND amount_milli BETWEEN 1 AND 9000000000000000)
);
CREATE TABLE game_checkins (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(typeof(id)='integer' AND id>0),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE CHECK(typeof(user_id)='integer'),
 site_day TEXT NOT NULL CHECK(typeof(site_day)='text' AND length(site_day)=10 AND site_day GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' AND date(site_day) IS site_day),
 award_milli INTEGER NOT NULL CHECK(typeof(award_milli)='integer' AND award_milli BETWEEN 0 AND 9000000000000000),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 UNIQUE(user_id,site_day)
);
CREATE TABLE game_onboarding_completions (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE CHECK(typeof(user_id)='integer'),
 game_key TEXT NOT NULL,
 task_key TEXT NOT NULL,
 award_milli INTEGER NOT NULL CHECK(typeof(award_milli)='integer'),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT CHECK(typeof(operation_id)='text' AND length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')),
 completed_at INTEGER NOT NULL CHECK(typeof(completed_at)='integer' AND completed_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,game_key,task_key),
 CHECK((game_key='fishing' AND task_key IN ('worm','lure','premium')) OR (game_key='linklink' AND task_key IN ('6x8','8x8','10x10')) OR (game_key='rps' AND task_key IN ('quick','standard','deathmatch')) OR
 (game_key='bidding' AND task_key IN ('complete_tier_1','complete_tier_2','complete_tier_3','first_win')) OR
 (game_key='likes' AND task_key IN ('quick_complete','quick_win','standard_complete','standard_win')) OR
 (game_key='blackjack' AND task_key IN ('complete','first_win','first_bust','first_21','first_natural_21'))),
 CHECK(award_milli=CASE
   WHEN game_key='fishing' THEN 1000000
   WHEN game_key='linklink' THEN CASE task_key WHEN '6x8' THEN 1000000 WHEN '8x8' THEN 2000000 WHEN '10x10' THEN 3000000 END
   WHEN game_key='rps' THEN CASE task_key WHEN 'quick' THEN 1000000 WHEN 'standard' THEN 2000000 WHEN 'deathmatch' THEN 5000000 END
   WHEN game_key='bidding' THEN CASE task_key WHEN 'complete_tier_1' THEN 1000000 WHEN 'complete_tier_2' THEN 2000000 WHEN 'complete_tier_3' THEN 5000000 WHEN 'first_win' THEN 2000000 END
   WHEN game_key='likes' THEN CASE task_key WHEN 'quick_complete' THEN 1000000 WHEN 'quick_win' THEN 2000000 WHEN 'standard_complete' THEN 5000000 WHEN 'standard_win' THEN 10000000 END
   WHEN game_key='blackjack' THEN CASE task_key WHEN 'complete' THEN 1000000 WHEN 'first_win' THEN 2000000 WHEN 'first_bust' THEN 3000000 WHEN 'first_21' THEN 4000000 WHEN 'first_natural_21' THEN 5000000 END
 END)
) WITHOUT ROWID;
CREATE TABLE game_onboarding_holds (
 id TEXT NOT NULL PRIMARY KEY CHECK(typeof(id)='text' AND length(id)=26 AND substr(id,1,4)='goh_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK(typeof(user_id)='integer'),
 game_key TEXT NOT NULL,
 task_key TEXT NOT NULL,
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND ledger_rows_remaining=X'00000000000000000000000000000001'),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 0 AND 253402300799),
 fishing_batch_id TEXT REFERENCES game_fishing_batches(id) ON DELETE RESTRICT,
 linklink_session_id TEXT REFERENCES game_linklink_sessions(id) ON DELETE RESTRICT,
 rps_queue_id TEXT REFERENCES game_rps_queue(id) ON DELETE RESTRICT,
 rps_session_id TEXT,
 seat_no INTEGER CHECK(seat_no IS NULL OR (typeof(seat_no)='integer' AND seat_no BETWEEN 0 AND 2)),
 duel_queue_id TEXT REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 duel_session_id TEXT,
 blackjack_entry_id TEXT REFERENCES game_blackjack_entries(id) ON DELETE RESTRICT,
 FOREIGN KEY(rps_session_id,seat_no) REFERENCES game_rps_seats(session_id,seat_no) ON DELETE RESTRICT,
 FOREIGN KEY(duel_session_id,seat_no) REFERENCES game_duel_seats(session_id,seat_no) ON DELETE RESTRICT,
 CHECK((game_key='fishing' AND task_key IN ('worm','lure','premium')) OR (game_key='linklink' AND task_key IN ('6x8','8x8','10x10')) OR (game_key='rps' AND task_key IN ('quick','standard','deathmatch')) OR
 (game_key='bidding' AND task_key IN ('complete_tier_1','complete_tier_2','complete_tier_3','first_win')) OR
 (game_key='likes' AND task_key IN ('quick_complete','quick_win','standard_complete','standard_win')) OR
 (game_key='blackjack' AND task_key IN ('complete','first_win','first_bust','first_21','first_natural_21'))),
 CHECK(
  (game_key='fishing' AND fishing_batch_id IS NOT NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL) OR
  (game_key='linklink' AND fishing_batch_id IS NULL AND linklink_session_id IS NOT NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL) OR
  (game_key='rps' AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NULL AND
   ((rps_queue_id IS NOT NULL AND rps_session_id IS NULL AND seat_no IS NULL) OR
    (rps_queue_id IS NULL AND rps_session_id IS NOT NULL AND seat_no IS NOT NULL))) OR
  (game_key IN ('bidding','likes') AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND blackjack_entry_id IS NULL AND
   ((duel_queue_id IS NOT NULL AND duel_session_id IS NULL AND seat_no IS NULL) OR
    (duel_queue_id IS NULL AND duel_session_id IS NOT NULL AND seat_no IN (0,1)))) OR
  (game_key='blackjack' AND fishing_batch_id IS NULL AND linklink_session_id IS NULL AND rps_queue_id IS NULL AND rps_session_id IS NULL AND seat_no IS NULL AND duel_queue_id IS NULL AND duel_session_id IS NULL AND blackjack_entry_id IS NOT NULL)
 )
);
CREATE UNIQUE INDEX idx_credit_accounts_user ON credit_accounts(user_id,asset_type) WHERE kind='user';
CREATE UNIQUE INDEX idx_credit_accounts_code ON credit_accounts(code,asset_type) WHERE code IS NOT NULL;
CREATE INDEX idx_rps_queue_match ON game_rps_queue(mode,rules_version,deadline,created_at,id);
CREATE INDEX idx_game_onboarding_hold_user ON game_onboarding_holds(user_id);
CREATE INDEX idx_game_onboarding_hold_created ON game_onboarding_holds(created_at,id);
CREATE INDEX idx_linklink_leaderboard ON game_linklink_summaries(spec,rules_version,terminal_reason,terminal_at,user_id,score DESC);
CREATE TRIGGER credit_entries_no_update BEFORE UPDATE ON credit_entries
WHEN NOT (
 NEW.operation_id IS OLD.operation_id AND NEW.line_no IS OLD.line_no AND NEW.account_kind_snapshot IS OLD.account_kind_snapshot AND
 NEW.delta_sign IS OLD.delta_sign AND NEW.delta_mag IS OLD.delta_mag AND NEW.balance_after_sign IS OLD.balance_after_sign AND
 NEW.balance_after_mag IS OLD.balance_after_mag AND NEW.asset_type IS OLD.asset_type AND
 (NEW.account_id IS OLD.account_id OR (OLD.account_id IS NOT NULL AND NEW.account_id IS NULL AND NOT EXISTS(SELECT 1 FROM credit_accounts a WHERE a.id=OLD.account_id)))
)
BEGIN SELECT RAISE(ABORT,'credit_entries is append-only'); END;
CREATE TRIGGER credit_entries_account_kind_guard BEFORE INSERT ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_entries_account_kind_update_guard BEFORE UPDATE OF account_id,account_kind_snapshot,asset_type ON credit_entries
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM credit_accounts a WHERE a.id=NEW.account_id AND a.kind=NEW.account_kind_snapshot AND a.asset_type=NEW.asset_type)
BEGIN SELECT RAISE(ABORT,'credit entry account kind snapshot mismatch'); END;
CREATE TRIGGER credit_account_identity_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type IS NOT OLD.asset_type OR NEW.kind IS NOT OLD.kind OR NEW.code IS NOT OLD.code OR NEW.user_id IS NOT OLD.user_id
BEGIN SELECT RAISE(ABORT,'account identity is immutable'); END;
CREATE TRIGGER welfare_claim_matrix_guard BEFORE INSERT ON welfare_claims
WHEN NEW.asset_type<>'game' OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id
  WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id
   AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
 OR typeof(NEW.site_day)<>'text'
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
WHEN NEW.asset_type IS NOT OLD.asset_type
 OR typeof(NEW.site_day)<>'text'
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
CREATE TRIGGER fishing_funding_insert BEFORE INSERT ON game_fishing_batches
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.platform_bp<>0 OR NEW.welfare_bp<>0 OR NEW.thursday_bp<>0
 OR NEW.platform_cut_total_milli<>0 OR NEW.welfare_cut_total_milli<>0 OR NEW.thursday_cut_total_milli<>0
 OR (NEW.net_payout_total_milli IS NOT NULL AND NEW.net_payout_total_milli<>NEW.payout_total_milli)))
 OR (NEW.rules_version=2 AND (NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.net_payout_total_milli IS NULL
 OR NEW.payout_total_milli<>NEW.net_payout_total_milli+NEW.platform_cut_total_milli+NEW.welfare_cut_total_milli+NEW.thursday_cut_total_milli))
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is invalid'); END;
CREATE TRIGGER fishing_funding_update BEFORE UPDATE ON game_fishing_batches
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.platform_bp<>0 OR NEW.welfare_bp<>0 OR NEW.thursday_bp<>0
 OR NEW.platform_cut_total_milli<>0 OR NEW.welfare_cut_total_milli<>0 OR NEW.thursday_cut_total_milli<>0
 OR (NEW.net_payout_total_milli IS NOT NULL AND NEW.net_payout_total_milli<>NEW.payout_total_milli)))
 OR (NEW.rules_version=2 AND (NEW.platform_bp+NEW.welfare_bp+NEW.thursday_bp>=10000
 OR NEW.net_payout_total_milli IS NULL
 OR NEW.payout_total_milli<>NEW.net_payout_total_milli+NEW.platform_cut_total_milli+NEW.welfare_cut_total_milli+NEW.thursday_cut_total_milli))
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is invalid'); END;
CREATE TRIGGER fishing_funding_immutable_update BEFORE UPDATE ON game_fishing_batches
WHEN NEW.rules_version IS NOT OLD.rules_version OR NEW.game_paid_milli IS NOT OLD.game_paid_milli OR NEW.platform_bp IS NOT OLD.platform_bp OR NEW.welfare_bp IS NOT OLD.welfare_bp OR NEW.thursday_bp IS NOT OLD.thursday_bp OR NEW.net_payout_total_milli IS NOT OLD.net_payout_total_milli OR NEW.platform_cut_total_milli IS NOT OLD.platform_cut_total_milli OR NEW.welfare_cut_total_milli IS NOT OLD.welfare_cut_total_milli OR NEW.thursday_cut_total_milli IS NOT OLD.thursday_cut_total_milli
BEGIN SELECT RAISE(ABORT,'fishing funding snapshot is immutable'); END;
CREATE TRIGGER fishing_outcome_rake_insert BEFORE INSERT ON game_fishing_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND (
 (b.rules_version=1 AND NEW.platform_cut_milli=0 AND NEW.welfare_cut_milli=0 AND NEW.thursday_cut_milli=0
  AND (NEW.net_payout_milli IS NULL OR NEW.net_payout_milli=NEW.payout_milli)) OR
 (b.rules_version=2 AND NEW.net_payout_milli IS NOT NULL AND
  NEW.payout_milli=NEW.net_payout_milli+NEW.platform_cut_milli+NEW.welfare_cut_milli+NEW.thursday_cut_milli)))
BEGIN SELECT RAISE(ABORT,'fishing outcome rake is invalid'); END;
CREATE TRIGGER fishing_outcome_rake_update BEFORE UPDATE ON game_fishing_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.batch_id AND (
 (b.rules_version=1 AND NEW.platform_cut_milli=0 AND NEW.welfare_cut_milli=0 AND NEW.thursday_cut_milli=0
  AND (NEW.net_payout_milli IS NULL OR NEW.net_payout_milli=NEW.payout_milli)) OR
 (b.rules_version=2 AND NEW.net_payout_milli IS NOT NULL AND
  NEW.payout_milli=NEW.net_payout_milli+NEW.platform_cut_milli+NEW.welfare_cut_milli+NEW.thursday_cut_milli)))
BEGIN SELECT RAISE(ABORT,'fishing outcome rake is invalid'); END;
CREATE TRIGGER game_linklink_sessions_funding_insert BEFORE INSERT ON game_linklink_sessions
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_sessions_funding_update BEFORE UPDATE ON game_linklink_sessions
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_summaries_funding_insert BEFORE INSERT ON game_linklink_summaries
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER game_linklink_summaries_funding_update BEFORE UPDATE ON game_linklink_summaries
WHEN (NEW.rules_version=1 AND (NEW.game_paid_milli<>0 OR NEW.assists_initial<>0 OR NEW.assists_remaining<>0))
 OR (NEW.rules_version=2 AND NEW.assists_initial<>CASE NEW.spec WHEN '6x8' THEN 2 WHEN '8x8' THEN 3 WHEN '10x10' THEN 5 END)
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is invalid'); END;
CREATE TRIGGER linklink_funding_immutable_update BEFORE UPDATE ON game_linklink_sessions
WHEN NEW.rules_version IS NOT OLD.rules_version OR NEW.game_paid_milli IS NOT OLD.game_paid_milli OR NEW.assists_initial IS NOT OLD.assists_initial
BEGIN SELECT RAISE(ABORT,'linklink funding snapshot is immutable'); END;
CREATE TRIGGER rps_queue_funding_insert BEFORE INSERT ON game_rps_queue
WHEN NEW.rules_version=1 AND NEW.game_paid<>X'00000000000000000000000000000000'
BEGIN SELECT RAISE(ABORT,'legacy queue cannot hold game credits'); END;
CREATE TRIGGER rps_queue_funding_update BEFORE UPDATE ON game_rps_queue
WHEN NEW.rules_version=1 AND NEW.game_paid<>X'00000000000000000000000000000000'
BEGIN SELECT RAISE(ABORT,'legacy queue cannot hold game credits'); END;
CREATE TRIGGER rps_seat_funding_insert BEFORE INSERT ON game_rps_seats
WHEN NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND
 (s.rules_version=2 OR (s.rules_version=1 AND NEW.game_buy_in=X'00000000000000000000000000000000' AND NEW.game_remaining=X'00000000000000000000000000000000')))
BEGIN SELECT RAISE(ABORT,'rps seat funding is invalid'); END;
CREATE TRIGGER rps_seat_funding_update BEFORE UPDATE ON game_rps_seats
WHEN NOT EXISTS(SELECT 1 FROM game_rps_sessions s WHERE s.id=NEW.session_id AND
 (s.rules_version=2 OR (s.rules_version=1 AND NEW.game_buy_in=X'00000000000000000000000000000000' AND NEW.game_remaining=X'00000000000000000000000000000000')))
BEGIN SELECT RAISE(ABORT,'rps seat funding is invalid'); END;
CREATE TRIGGER rps_seat_buyin_immutable_update BEFORE UPDATE ON game_rps_seats
WHEN NEW.game_buy_in IS NOT OLD.game_buy_in
BEGIN SELECT RAISE(ABORT,'rps buy-in is immutable'); END;
CREATE TRIGGER rps_summary_funding_insert BEFORE INSERT ON game_rps_summary_seats
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR EXISTS(SELECT 1 FROM game_rps_summaries s WHERE s.session_id=NEW.session_id AND s.rules_version=2 AND NEW.general_buy_in IS NULL)
BEGIN SELECT RAISE(ABORT,'rps summary funding is incomplete'); END;
CREATE TRIGGER rps_summary_funding_update BEFORE UPDATE ON game_rps_summary_seats
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR EXISTS(SELECT 1 FROM game_rps_summaries s WHERE s.session_id=NEW.session_id AND s.rules_version=2 AND NEW.general_buy_in IS NULL)
BEGIN SELECT RAISE(ABORT,'rps summary funding is incomplete'); END;
CREATE TRIGGER rps_pending_funding_insert BEFORE INSERT ON game_rps_pending_results
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR (NEW.rules_version=2 AND (NEW.general_buy_in IS NULL OR NEW.own_returned_general IS NULL))
BEGIN SELECT RAISE(ABORT,'rps pending funding is incomplete'); END;
CREATE TRIGGER rps_pending_funding_update BEFORE UPDATE ON game_rps_pending_results
WHEN (NEW.general_buy_in IS NULL)<>(NEW.game_buy_in IS NULL)
 OR (NEW.rules_version=2 AND (NEW.general_buy_in IS NULL OR NEW.own_returned_general IS NULL))
BEGIN SELECT RAISE(ABORT,'rps pending funding is incomplete'); END;
CREATE TRIGGER onboarding_hold_identity_update BEFORE UPDATE ON game_onboarding_holds
WHEN NEW.id IS NOT OLD.id OR NEW.user_id IS NOT OLD.user_id OR NEW.game_key IS NOT OLD.game_key OR NEW.task_key IS NOT OLD.task_key OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'onboarding hold identity is immutable'); END;
CREATE TRIGGER onboarding_completion_operation_insert BEFORE INSERT ON game_onboarding_completions
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='game_onboarding_reward' AND o.created_at=NEW.completed_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general'
  AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'onboarding reward operation mismatch'); END;
CREATE TRIGGER onboarding_completion_immutable_update BEFORE UPDATE ON game_onboarding_completions
WHEN 1
BEGIN SELECT RAISE(ABORT,'onboarding completion is immutable'); END;
CREATE TRIGGER game_checkin_operation_insert BEFORE INSERT ON game_checkins
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='checkin_award' AND o.created_at=NEW.created_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game'
  AND e.delta_sign=CASE WHEN NEW.award_milli=0 THEN 0 ELSE 1 END AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'game check-in operation mismatch'); END;
CREATE TRIGGER game_checkin_operation_update BEFORE UPDATE ON game_checkins
WHEN NOT EXISTS(
 SELECT 1 FROM credit_operations o JOIN credit_entries e ON e.operation_id=o.id JOIN credit_accounts a ON a.id=e.account_id
 WHERE o.id=NEW.operation_id AND o.kind='checkin_award' AND o.created_at=NEW.created_at
  AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game'
  AND e.delta_sign=CASE WHEN NEW.award_milli=0 THEN 0 ELSE 1 END AND hex(e.delta_mag)=printf('%032X',NEW.award_milli))
BEGIN SELECT RAISE(ABORT,'game check-in operation mismatch'); END;
CREATE TABLE game_duel_catalogs (
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64 AND content_hash NOT GLOB '*[^0-9a-f]*'),
 rules_version INTEGER NOT NULL CHECK(rules_version=1),
 design_version TEXT NOT NULL CHECK(length(design_version) BETWEEN 1 AND 32),
 schema_version INTEGER NOT NULL CHECK(schema_version BETWEEN 1 AND 32),
 catalog_json TEXT NOT NULL CHECK(typeof(catalog_json)='text' AND length(CAST(catalog_json AS BLOB))<=1048576 AND json_valid(catalog_json)),
 PRIMARY KEY(game_key,content_hash)
) STRICT;
CREATE TABLE game_duel_queue (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5) IN ('bidq_','likq_','gwtq_') AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,5)='bidq_') OR (game_key='likes' AND substr(id,1,5)='likq_') OR (game_key='gwent' AND substr(id,1,5)='gwtq_')),
 mode TEXT NOT NULL CHECK((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16 AND hex(revision)<>'00000000000000000000000000000000'),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300679),
 deadline INTEGER NOT NULL CHECK(deadline=created_at+120),
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 content_hash TEXT NOT NULL,
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 1 AND 9000000000000000),
 game_paid_milli INTEGER NOT NULL CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND ticket_milli),
 reservation_operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(game_account_id<>general_account_id),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16 AND hex(ledger_rows_remaining) IN ('00000000000000000000000000000000','00000000000000000000000000000001')),
 device_hash BLOB NOT NULL CHECK(typeof(device_hash)='blob' AND length(device_hash)=32),
 ip_hash BLOB NOT NULL CHECK(typeof(ip_hash)='blob' AND length(ip_hash)=32),
 loadout_json TEXT CHECK((game_key='bidding' AND loadout_json IS NULL) OR (game_key IN ('likes','gwent') AND typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
CREATE INDEX idx_duel_queue_match ON game_duel_queue(game_key,mode,terms_hash,created_at,id);
CREATE INDEX idx_duel_queue_deadline ON game_duel_queue(game_key,deadline,id);
CREATE UNIQUE INDEX idx_duel_queue_user ON game_duel_queue(user_id,game_key);
CREATE TABLE game_duel_sessions (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4) IN ('bid_','lik_','gwt_') AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK((game_key='bidding' AND substr(id,1,4)='bid_') OR (game_key='likes' AND substr(id,1,4)='lik_') OR (game_key='gwent' AND substr(id,1,4)='gwt_')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key IN ('bidding','gwent') AND mode='ai')),
 content_hash TEXT NOT NULL,
 terms_json TEXT NOT NULL CHECK(typeof(terms_json)='text' AND length(CAST(terms_json AS BLOB))<=4096 AND json_valid(terms_json)),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64 AND terms_hash NOT GLOB '*[^0-9a-f]*'),
 ticket_milli INTEGER NOT NULL CHECK(typeof(ticket_milli)='integer' AND ticket_milli BETWEEN 0 AND 9000000000000000 AND (economy='ai_challenge' OR ticket_milli>0)),
 platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999),
 welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999),
 thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000),
 state TEXT NOT NULL CHECK(state IN ('active','terminal')),
 phase TEXT NOT NULL CHECK(phase IN ('joker','bid','plan','settlement','mulligan','turn','choice','terminal')),
 round INTEGER NOT NULL CHECK(round BETWEEN 1 AND 75 AND (game_key<>'bidding' OR round<=13) AND (mode<>'quick' OR round<=25)),
 revision BLOB NOT NULL CHECK(typeof(revision)='blob' AND length(revision)=16 AND hex(revision)<>'00000000000000000000000000000000'),
 phase_seq BLOB NOT NULL CHECK(typeof(phase_seq)='blob' AND length(phase_seq)=16 AND hex(phase_seq)<>'00000000000000000000000000000000'),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253399708799),
 phase_deadline INTEGER CHECK(phase_deadline IS NULL OR phase_deadline BETWEEN started_at AND 253399708799),
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(game_account_id<>general_account_id),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 server_state_json TEXT NOT NULL CHECK(typeof(server_state_json)='text' AND length(CAST(server_state_json AS BLOB))<=1048576 AND json_valid(server_state_json)),
 initial_state_json TEXT NOT NULL CHECK(typeof(initial_state_json)='text' AND length(CAST(initial_state_json AS BLOB))<=1048576 AND json_valid(initial_state_json)),
 terminal_at INTEGER CHECK(terminal_at IS NULL OR terminal_at BETWEEN started_at AND 253399708799),
 delete_at INTEGER CHECK(delete_at IS NULL OR delete_at=terminal_at+2592000),
 outcome TEXT CHECK(outcome IS NULL OR outcome IN ('decided','draw','system_cancelled')),
 reason TEXT CHECK(reason IS NULL OR reason IN ('rounds','target','double-overload','limit','surrender','server_restart','account_unavailable','afk')),
 winner_seat INTEGER CHECK(winner_seat IS NULL OR winner_seat IN (0,1)),
 score0 INTEGER CHECK(score0 IS NULL OR score0 BETWEEN 0 AND 1000000000),
 score1 INTEGER CHECK(score1 IS NULL OR score1 BETWEEN 0 AND 1000000000),
 prize_milli INTEGER CHECK(prize_milli IS NULL OR prize_milli BETWEEN 0 AND ticket_milli),
 platform_milli INTEGER CHECK(platform_milli IS NULL OR platform_milli BETWEEN 0 AND ticket_milli),
 welfare_milli INTEGER CHECK(welfare_milli IS NULL OR welfare_milli BETWEEN 0 AND ticket_milli),
 thursday_milli INTEGER CHECK(thursday_milli IS NULL OR thursday_milli BETWEEN 0 AND ticket_milli),
 terminal_operation_id TEXT UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT,
 CHECK((state='active' AND ((game_key='bidding' AND phase IN ('joker','bid')) OR (game_key='likes' AND phase IN ('plan','settlement')) OR (game_key='gwent' AND phase IN ('mulligan','turn','choice'))) AND phase_deadline IS NOT NULL AND (hex(ledger_rows_remaining)='00000000000000000000000000000001' OR (economy='ai_challenge' AND hex(ledger_rows_remaining)='00000000000000000000000000000000')) AND terminal_at IS NULL AND delete_at IS NULL AND outcome IS NULL AND reason IS NULL AND winner_seat IS NULL AND score0 IS NULL AND score1 IS NULL AND prize_milli IS NULL AND platform_milli IS NULL AND welfare_milli IS NULL AND thursday_milli IS NULL AND terminal_operation_id IS NULL) OR (state='terminal' AND phase='terminal' AND phase_deadline IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000' AND terminal_at IS NOT NULL AND delete_at IS NOT NULL AND outcome IS NOT NULL AND reason IS NOT NULL AND score0 IS NOT NULL AND score1 IS NOT NULL AND prize_milli IS NOT NULL AND platform_milli IS NOT NULL AND welfare_milli IS NOT NULL AND thursday_milli IS NOT NULL AND (terminal_operation_id IS NOT NULL OR (economy='ai_challenge' AND ticket_milli=0)))),
 CHECK(state='active' OR (economy='pvp' AND ((outcome='decided' AND winner_seat IS NOT NULL AND reason NOT IN ('server_restart','account_unavailable') AND prize_milli+platform_milli+welfare_milli+thursday_milli=ticket_milli) OR (outcome IN ('draw','system_cancelled') AND winner_seat IS NULL AND prize_milli=0 AND platform_milli=0 AND welfare_milli=0 AND thursday_milli=0))) OR (economy='ai_challenge' AND prize_milli=0 AND welfare_milli=0 AND thursday_milli=0 AND ((outcome IN ('decided','draw') AND platform_milli=ticket_milli AND (outcome='decided')=(winner_seat IS NOT NULL)) OR (outcome='system_cancelled' AND winner_seat IS NULL AND platform_milli=0)))),
 CHECK(economy='pvp' OR (platform_bp=0 AND welfare_bp=0 AND thursday_bp=0)),
 CHECK(state='active' OR (outcome='system_cancelled' AND reason IN ('server_restart','account_unavailable')) OR (outcome<>'system_cancelled' AND reason NOT IN ('server_restart','account_unavailable')))
) STRICT;
CREATE INDEX idx_duel_sessions_due ON game_duel_sessions(game_key,state,phase_deadline,id);
CREATE INDEX idx_duel_sessions_terminal ON game_duel_sessions(game_key,state,terminal_at,id);
CREATE INDEX idx_duel_sessions_expiry ON game_duel_sessions(game_key,state,delete_at,id);
CREATE INDEX idx_credit_duel_terminal ON credit_operations(substr(source_id,1,4),ledger_seq) WHERE kind='duel_terminal' AND source_type='duel_session';
CREATE TABLE game_duel_seats (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 seat_no INTEGER NOT NULL CHECK(seat_no IN (0,1)),
 participant_kind TEXT NOT NULL DEFAULT 'human' CHECK(participant_kind IN ('human','bot')),
 bot_id TEXT REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
 general_paid_milli INTEGER NOT NULL CHECK(typeof(general_paid_milli)='integer' AND general_paid_milli BETWEEN 0 AND 9000000000000000),
 game_paid_milli INTEGER NOT NULL CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND 9000000000000000),
 loadout_json TEXT CHECK(loadout_json IS NULL OR (typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB))<=4096 AND json_valid(loadout_json))),
 current_plan_json TEXT CHECK(current_plan_json IS NULL OR (typeof(current_plan_json)='text' AND length(CAST(current_plan_json AS BLOB))<=16384 AND json_valid(current_plan_json))),
 locked INTEGER NOT NULL CHECK(locked IN (0,1)),
 timeout_count INTEGER NOT NULL CHECK(timeout_count BETWEEN 0 AND 150),
 CHECK((participant_kind='human' AND bot_id IS NULL) OR (participant_kind='bot' AND bot_id IS NOT NULL AND user_id IS NULL AND general_paid_milli=0 AND game_paid_milli=0 AND loadout_json IS NULL)),
 PRIMARY KEY(session_id,seat_no), UNIQUE(session_id,user_id),
 CHECK((locked=0 AND current_plan_json IS NULL) OR (locked=1 AND current_plan_json IS NOT NULL))
) STRICT;
CREATE INDEX idx_duel_seats_user ON game_duel_seats(user_id,session_id);
CREATE TABLE game_duel_user_slots (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 queue_id TEXT UNIQUE REFERENCES game_duel_queue(id) ON DELETE RESTRICT,
 ai_queue_id TEXT UNIQUE REFERENCES game_ai_queue(id) ON DELETE RESTRICT,
 session_id TEXT REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 PRIMARY KEY(user_id,game_key),
 CHECK((queue_id IS NOT NULL)+(ai_queue_id IS NOT NULL)+(session_id IS NOT NULL)=1)
) STRICT;
CREATE TABLE game_duel_rounds (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE RESTRICT,
 round_no INTEGER NOT NULL CHECK(round_no BETWEEN 1 AND 75),
 record_json TEXT NOT NULL CHECK(typeof(record_json)='text' AND length(CAST(record_json AS BLOB))<=1048576 AND json_valid(record_json)),
 PRIMARY KEY(session_id,round_no)
) STRICT;
CREATE TABLE game_duel_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='dah_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 economy TEXT NOT NULL DEFAULT 'pvp' CHECK(economy IN ('pvp','ai_challenge')),
 mode TEXT NOT NULL CHECK((economy='pvp' AND ((game_key='bidding' AND mode IN ('tier1','tier2','tier3')) OR (game_key='likes' AND mode IN ('quick','standard')) OR (game_key='gwent' AND mode='standard'))) OR (economy='ai_challenge' AND game_key IN ('bidding','gwent') AND mode='ai')),
 content_hash TEXT NOT NULL,
 header_json TEXT NOT NULL CHECK(typeof(header_json)='text' AND length(CAST(header_json AS BLOB))<=1048576 AND json_valid(header_json)),
 FOREIGN KEY(game_key,content_hash) REFERENCES game_duel_catalogs(game_key,content_hash) ON DELETE RESTRICT
) STRICT;
CREATE INDEX idx_duel_anonymous_export ON game_duel_anonymous(game_key,mode,export_seq);
CREATE INDEX idx_duel_anonymous_archive ON game_duel_anonymous(game_key,archive_id);
CREATE TABLE game_duel_anonymous_rounds (
 archive_id TEXT NOT NULL REFERENCES game_duel_anonymous(archive_id) ON DELETE CASCADE,
 round_no INTEGER NOT NULL CHECK(round_no BETWEEN 1 AND 75),
 record_json TEXT NOT NULL CHECK(typeof(record_json)='text' AND length(CAST(record_json AS BLOB))<=1048576 AND json_valid(record_json)),
 PRIMARY KEY(archive_id,round_no)
) STRICT;
CREATE TRIGGER game_duel_queue_delete_guard BEFORE DELETE ON game_duel_queue WHEN hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel queue still reserves capacity'); END;
CREATE TRIGGER game_duel_session_delete_guard BEFORE DELETE ON game_duel_sessions WHEN OLD.state<>'terminal' OR hex(OLD.ledger_rows_remaining)<>'00000000000000000000000000000000' BEGIN SELECT RAISE(ABORT,'duel session still active'); END;
CREATE TRIGGER game_duel_terminal_immutable BEFORE UPDATE ON game_duel_sessions WHEN OLD.state='terminal' BEGIN SELECT RAISE(ABORT,'duel result immutable'); END;
CREATE TRIGGER game_duel_round_immutable BEFORE UPDATE ON game_duel_rounds BEGIN SELECT RAISE(ABORT,'duel round immutable'); END;
CREATE TRIGGER game_duel_catalog_immutable BEFORE UPDATE ON game_duel_catalogs BEGIN SELECT RAISE(ABORT,'duel catalog immutable'); END;
CREATE TRIGGER game_duel_seat_payment_insert BEFORE INSERT ON game_duel_seats WHEN (NEW.participant_kind='human' AND NEW.general_paid_milli+NEW.game_paid_milli<>(SELECT ticket_milli FROM game_duel_sessions WHERE id=NEW.session_id)) OR (NEW.participant_kind='bot' AND NOT EXISTS(SELECT 1 FROM game_duel_sessions WHERE id=NEW.session_id AND economy='ai_challenge')) BEGIN SELECT RAISE(ABORT,'duel payment mismatch'); END;
CREATE TRIGGER game_duel_seat_payment_update BEFORE UPDATE ON game_duel_seats WHEN NEW.participant_kind<>OLD.participant_kind OR NEW.bot_id IS NOT OLD.bot_id OR NEW.session_id<>OLD.session_id OR NEW.seat_no<>OLD.seat_no OR NEW.general_paid_milli<>OLD.general_paid_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.loadout_json IS NOT OLD.loadout_json OR (OLD.user_id IS NULL AND NEW.user_id IS NOT NULL) OR (OLD.user_id IS NOT NULL AND NEW.user_id IS NOT NULL AND NEW.user_id<>OLD.user_id) BEGIN SELECT RAISE(ABORT,'duel seat immutable'); END;
CREATE TRIGGER game_duel_user_delete_guard BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=OLD.id) OR EXISTS(SELECT 1 FROM game_duel_seats WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'duel user handoff required'); END;
CREATE TRIGGER game_duel_queue_frozen BEFORE UPDATE ON game_duel_queue WHEN NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.user_id<>OLD.user_id OR NEW.revision<>OLD.revision OR NEW.created_at<>OLD.created_at OR NEW.deadline<>OLD.deadline OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.reservation_operation_id<>OLD.reservation_operation_id OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.device_hash<>OLD.device_hash OR NEW.ip_hash<>OLD.ip_hash OR NEW.loadout_json IS NOT OLD.loadout_json OR NEW.ledger_rows_remaining>OLD.ledger_rows_remaining BEGIN SELECT RAISE(ABORT,'duel queue terms immutable'); END;
CREATE TRIGGER game_duel_session_frozen BEFORE UPDATE ON game_duel_sessions WHEN NEW.economy<>OLD.economy OR NEW.id<>OLD.id OR NEW.game_key<>OLD.game_key OR NEW.mode<>OLD.mode OR NEW.terms_json<>OLD.terms_json OR NEW.terms_hash<>OLD.terms_hash OR NEW.content_hash<>OLD.content_hash OR NEW.ticket_milli<>OLD.ticket_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR NEW.started_at<>OLD.started_at OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.initial_state_json<>OLD.initial_state_json OR NEW.phase_seq<OLD.phase_seq OR NEW.revision<=OLD.revision BEGIN SELECT RAISE(ABORT,'duel session terms immutable'); END;
CREATE TRIGGER game_duel_queue_accounts BEFORE INSERT ON game_duel_queue WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-queue:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-queue:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel queue account mismatch'); END;
CREATE TRIGGER game_duel_session_accounts BEFORE INSERT ON game_duel_sessions WHEN NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='duel-session:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='duel-session:'||NEW.id) BEGIN SELECT RAISE(ABORT,'duel session account mismatch'); END;
CREATE TRIGGER game_duel_slot_insert BEFORE INSERT ON game_duel_user_slots WHEN (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_slot_update BEFORE UPDATE ON game_duel_user_slots WHEN NEW.user_id<>OLD.user_id OR NEW.game_key<>OLD.game_key OR (NEW.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue WHERE id=NEW.queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.ai_queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_ai_queue WHERE id=NEW.ai_queue_id AND user_id=NEW.user_id AND game_key=NEW.game_key)) OR (NEW.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=NEW.session_id AND g.game_key=NEW.game_key AND g.state='active' AND p.user_id=NEW.user_id)) BEGIN SELECT RAISE(ABORT,'duel slot owner mismatch'); END;
CREATE TRIGGER game_duel_seat_deidentify BEFORE UPDATE OF user_id ON game_duel_seats WHEN NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND (SELECT state FROM game_duel_sessions WHERE id=NEW.session_id)<>'terminal' BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_user_ban_guard BEFORE UPDATE OF is_banned,banned_until ON users WHEN NEW.is_banned=1 AND (NEW.is_banned<>OLD.is_banned OR NEW.banned_until IS NOT OLD.banned_until) AND EXISTS(SELECT 1 FROM game_duel_user_slots WHERE user_id=NEW.id) BEGIN SELECT RAISE(ABORT,'duel cancellation required'); END;
CREATE TRIGGER game_duel_anonymous_immutable BEFORE UPDATE ON game_duel_anonymous BEGIN SELECT RAISE(ABORT,'duel archive immutable'); END;
CREATE TRIGGER game_duel_anonymous_round_immutable BEFORE UPDATE ON game_duel_anonymous_rounds BEGIN SELECT RAISE(ABORT,'duel archive round immutable'); END;
CREATE TABLE game_blackjack_clock (
 id INTEGER PRIMARY KEY CHECK(id=1),
 observed_at INTEGER NOT NULL CHECK(observed_at BETWEEN 0 AND 253399708739)
) STRICT;
CREATE TABLE game_blackjack_sessions (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='bjt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 started_at INTEGER NOT NULL UNIQUE CHECK(started_at BETWEEN 0 AND 253399708739 AND started_at%30=0),
 phase TEXT NOT NULL CHECK(phase IN ('seating','decision','result','cancelled')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 last_batch INTEGER NOT NULL CHECK(last_batch BETWEEN started_at AND started_at+45),
 state_json TEXT CHECK(state_json IS NULL OR (json_valid(state_json) AND length(CAST(state_json AS BLOB))<=32768)),
 view_json TEXT CHECK(view_json IS NULL OR (json_valid(view_json) AND length(CAST(view_json AS BLOB))<=24576)),
 terminal_at INTEGER CHECK(terminal_at BETWEEN started_at AND 253399708739),
 reason TEXT CHECK(reason IN ('completed','server_restart','closed','maintenance')),
 CHECK((phase='seating' AND state_json IS NULL AND view_json IS NULL AND terminal_at IS NULL AND reason IS NULL)
 OR (phase='decision' AND state_json IS NOT NULL AND view_json IS NOT NULL AND terminal_at IS NULL AND reason IS NULL)
 OR (phase IN ('result','cancelled') AND state_json IS NULL AND view_json IS NOT NULL AND terminal_at IS NOT NULL AND reason IS NOT NULL)),
 CHECK(phase<>'result' OR reason='completed'),
 CHECK(phase<>'cancelled' OR reason<>'completed')
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_single_table ON game_blackjack_sessions((1)) WHERE phase IN ('seating','decision');
CREATE INDEX idx_blackjack_sessions_recent ON game_blackjack_sessions(started_at DESC,id);
CREATE INDEX idx_blackjack_sessions_retention ON game_blackjack_sessions(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE TABLE game_blackjack_entries (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='bjq_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
 state TEXT NOT NULL CHECK(state IN ('waiting','seated','playing','settled','released')),
 stake_milli INTEGER NOT NULL CHECK(stake_milli BETWEEN 1 AND 140625000000000),
 platform_bp INTEGER NOT NULL CHECK(platform_bp BETWEEN 0 AND 9999),
 welfare_bp INTEGER NOT NULL CHECK(welfare_bp BETWEEN 0 AND 9999),
 thursday_bp INTEGER NOT NULL CHECK(thursday_bp BETWEEN 0 AND 9999 AND platform_bp+welfare_bp+thursday_bp<10000),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708739),
 resolved_at INTEGER CHECK(resolved_at BETWEEN created_at AND 253399708739),
 session_id TEXT REFERENCES game_blackjack_sessions(id) ON DELETE RESTRICT,
 seat_no INTEGER CHECK(seat_no BETWEEN 0 AND 8),
 pending_json TEXT CHECK(pending_json IS NULL OR (json_valid(pending_json) AND length(CAST(pending_json AS BLOB))<=512)),
 pending_batch INTEGER CHECK(pending_batch BETWEEN 0 AND 253399708739),
 stopped INTEGER NOT NULL DEFAULT 0 CHECK(stopped IN (0,1)),
 emote TEXT CHECK(emote IN ('hello','nice','wow','good_luck','thanks','gg')),
 emote_at INTEGER CHECK(emote_at BETWEEN 0 AND 253399708739),
 CHECK((emote IS NULL)=(emote_at IS NULL)),
 CHECK((pending_json IS NULL)=(pending_batch IS NULL)),
 CHECK(pending_json IS NULL OR state='playing'),
 CHECK(user_id IS NOT NULL OR state IN ('playing','settled','released')),
 CHECK((state='waiting' AND session_id IS NULL AND seat_no IS NULL AND resolved_at IS NULL)
 OR (state IN ('seated','playing') AND session_id IS NOT NULL AND seat_no IS NOT NULL AND resolved_at IS NULL)
 OR (state='settled' AND session_id IS NOT NULL AND seat_no IS NOT NULL AND resolved_at IS NOT NULL)
 OR (state='released' AND resolved_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_entry_user ON game_blackjack_entries(user_id) WHERE state IN ('waiting','seated','playing');
CREATE UNIQUE INDEX idx_blackjack_entry_seat ON game_blackjack_entries(session_id,seat_no) WHERE state IN ('seated','playing','settled');
CREATE INDEX idx_blackjack_entry_fifo ON game_blackjack_entries(ordinal) WHERE state='waiting';
CREATE INDEX idx_blackjack_entry_session ON game_blackjack_entries(session_id,seat_no,ordinal);
CREATE INDEX idx_blackjack_entry_history ON game_blackjack_entries(user_id,resolved_at DESC,ordinal DESC);
CREATE INDEX idx_blackjack_entry_retention ON game_blackjack_entries(resolved_at,ordinal) WHERE resolved_at IS NOT NULL;
CREATE TABLE game_blackjack_payments (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND substr(id,1,4)='bjp_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 entry_id TEXT NOT NULL REFERENCES game_blackjack_entries(id) ON DELETE RESTRICT,
 kind TEXT NOT NULL CHECK(kind IN ('base','split','double')),
 hand_no INTEGER NOT NULL CHECK(hand_no IN (0,1)),
 amount_milli INTEGER NOT NULL CHECK(amount_milli BETWEEN 1 AND 140625000000000),
 game_paid_milli INTEGER NOT NULL CHECK(game_paid_milli BETWEEN 0 AND amount_milli),
 general_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT,
 game_account_id INTEGER NOT NULL UNIQUE REFERENCES credit_accounts(id) ON DELETE RESTRICT CHECK(general_account_id<>game_account_id),
 reserve_operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 terminal_operation_id TEXT UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 state TEXT NOT NULL CHECK(state IN ('reserved','settled','released')),
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708739),
 CHECK((state='reserved' AND terminal_operation_id IS NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000001') OR (state<>'reserved' AND terminal_operation_id IS NOT NULL AND hex(ledger_rows_remaining)='00000000000000000000000000000000'))
) STRICT;
CREATE UNIQUE INDEX idx_blackjack_base_payment ON game_blackjack_payments(entry_id) WHERE kind='base';
CREATE INDEX idx_blackjack_payment_entry ON game_blackjack_payments(entry_id,ordinal);
CREATE INDEX idx_blackjack_payment_reserved ON game_blackjack_payments(id) WHERE state='reserved';
CREATE INDEX idx_credit_blackjack_history ON credit_operations(ledger_seq) WHERE kind='blackjack_settle' AND source_type='blackjack_payment';
CREATE TABLE game_blackjack_events (
 session_id TEXT NOT NULL REFERENCES game_blackjack_sessions(id) ON DELETE RESTRICT,
 seq INTEGER NOT NULL CHECK(seq BETWEEN 1 AND 128),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253399708739),
 kind TEXT NOT NULL CHECK(kind IN ('deal','actions','timeout','result','cancelled')),
 public_json TEXT NOT NULL CHECK(json_valid(public_json) AND length(CAST(public_json AS BLOB))<=24576),
 PRIMARY KEY(session_id,seq)
) STRICT;
CREATE TABLE game_blackjack_anonymous (
 export_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 archive_id TEXT NOT NULL UNIQUE CHECK(length(archive_id)=26 AND substr(archive_id,1,4)='bja_' AND substr(archive_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(archive_id,-1,1) IN ('A','Q','g','w')),
 public_json TEXT NOT NULL CHECK(json_valid(public_json) AND length(CAST(public_json AS BLOB))<=32768)
) STRICT;
CREATE TRIGGER blackjack_session_frozen BEFORE UPDATE ON game_blackjack_sessions WHEN NEW.id<>OLD.id OR NEW.started_at<>OLD.started_at OR NEW.revision<=OLD.revision OR NEW.last_batch<OLD.last_batch OR OLD.phase IN ('result','cancelled') BEGIN SELECT RAISE(ABORT,'blackjack session immutable'); END;
CREATE TRIGGER blackjack_entry_frozen BEFORE UPDATE ON game_blackjack_entries WHEN NEW.ordinal<>OLD.ordinal OR NEW.id<>OLD.id OR NEW.created_at<>OLD.created_at OR NEW.stake_milli<>OLD.stake_milli OR NEW.platform_bp<>OLD.platform_bp OR NEW.welfare_bp<>OLD.welfare_bp OR NEW.thursday_bp<>OLD.thursday_bp OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL) OR ((OLD.session_id IS NOT NULL AND NEW.session_id IS NOT OLD.session_id) OR (OLD.seat_no IS NOT NULL AND NEW.seat_no IS NOT OLD.seat_no)) AND NOT (OLD.state='seated' AND NEW.state='released' AND NEW.session_id IS NULL AND NEW.seat_no IS NULL) OR NEW.stopped<OLD.stopped BEGIN SELECT RAISE(ABORT,'blackjack entry immutable'); END;
CREATE TRIGGER blackjack_entry_transition BEFORE UPDATE OF state ON game_blackjack_entries WHEN NEW.state<>OLD.state AND NOT ((OLD.state='waiting' AND NEW.state IN ('seated','released')) OR (OLD.state='seated' AND NEW.state IN ('playing','released')) OR (OLD.state='playing' AND NEW.state IN ('settled','released'))) BEGIN SELECT RAISE(ABORT,'blackjack entry transition'); END;
CREATE TRIGGER blackjack_payment_insert BEFORE INSERT ON game_blackjack_payments WHEN NOT EXISTS(SELECT 1 FROM game_blackjack_entries WHERE id=NEW.entry_id AND stake_milli=NEW.amount_milli AND state IN ('waiting','seated','playing')) OR (SELECT COUNT(*) FROM game_blackjack_payments WHERE entry_id=NEW.entry_id)>=4 OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.general_account_id AND kind='platform' AND asset_type='general' AND code='blackjack-payment:'||NEW.id) OR NOT EXISTS(SELECT 1 FROM credit_accounts WHERE id=NEW.game_account_id AND kind='platform' AND asset_type='game' AND code='blackjack-payment:'||NEW.id) BEGIN SELECT RAISE(ABORT,'blackjack payment mismatch'); END;
CREATE TRIGGER blackjack_payment_frozen BEFORE UPDATE ON game_blackjack_payments WHEN NEW.ordinal<>OLD.ordinal OR NEW.id<>OLD.id OR NEW.entry_id<>OLD.entry_id OR NEW.kind<>OLD.kind OR NEW.hand_no<>OLD.hand_no OR NEW.amount_milli<>OLD.amount_milli OR NEW.game_paid_milli<>OLD.game_paid_milli OR NEW.general_account_id<>OLD.general_account_id OR NEW.game_account_id<>OLD.game_account_id OR NEW.reserve_operation_id<>OLD.reserve_operation_id OR NEW.created_at<>OLD.created_at OR OLD.state<>'reserved' BEGIN SELECT RAISE(ABORT,'blackjack payment immutable'); END;
CREATE TRIGGER blackjack_payment_delete BEFORE DELETE ON game_blackjack_payments WHEN OLD.state='reserved' BEGIN SELECT RAISE(ABORT,'blackjack payment still reserved'); END;
CREATE TRIGGER blackjack_entry_delete BEFORE DELETE ON game_blackjack_entries WHEN OLD.state IN ('waiting','seated','playing') BEGIN SELECT RAISE(ABORT,'blackjack entry still active'); END;
CREATE TRIGGER blackjack_session_delete BEFORE DELETE ON game_blackjack_sessions WHEN OLD.phase='decision' OR (OLD.phase='seating' AND EXISTS(SELECT 1 FROM game_blackjack_entries WHERE session_id=OLD.id)) BEGIN SELECT RAISE(ABORT,'blackjack table still active'); END;
CREATE TRIGGER blackjack_user_delete BEFORE DELETE ON users WHEN EXISTS(SELECT 1 FROM game_blackjack_entries WHERE user_id=OLD.id) BEGIN SELECT RAISE(ABORT,'blackjack user handoff required'); END;
CREATE TRIGGER blackjack_event_immutable BEFORE UPDATE ON game_blackjack_events BEGIN SELECT RAISE(ABORT,'blackjack event immutable'); END;
CREATE TRIGGER blackjack_anonymous_immutable BEFORE UPDATE ON game_blackjack_anonymous BEGIN SELECT RAISE(ABORT,'blackjack archive immutable'); END;
CREATE TABLE game_random_proofs (
 resource_id TEXT PRIMARY KEY NOT NULL,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','gwent','blackjack')),
 fishing_id TEXT UNIQUE REFERENCES game_fishing_batches(id) ON DELETE CASCADE,
 linklink_id TEXT UNIQUE REFERENCES game_linklink_sessions(id) ON DELETE CASCADE,
 linklink_summary_id TEXT UNIQUE REFERENCES game_linklink_summaries(session_id) ON DELETE CASCADE,
 rps_id TEXT UNIQUE REFERENCES game_rps_sessions(id) ON DELETE CASCADE,
 rps_summary_id TEXT UNIQUE REFERENCES game_rps_summaries(session_id) ON DELETE CASCADE,
 duel_id TEXT UNIQUE REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 blackjack_id TEXT UNIQUE REFERENCES game_blackjack_sessions(id) ON DELETE CASCADE,
 private_json TEXT NOT NULL CHECK(typeof(private_json)='text' AND length(CAST(private_json AS BLOB)) BETWEEN 1 AND 2097152 AND json_valid(private_json)),
 CHECK(
  (game_key='fishing' AND fishing_id IS resource_id AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='linklink' AND ((linklink_id IS resource_id AND linklink_summary_id IS NULL) OR (linklink_summary_id IS resource_id AND linklink_id IS NULL)) AND fishing_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='rps' AND ((rps_id IS resource_id AND rps_summary_id IS NULL) OR (rps_summary_id IS resource_id AND rps_id IS NULL)) AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key IN ('bidding','likes','gwent') AND duel_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='blackjack' AND blackjack_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL)
 ),
 CHECK(json_extract(private_json,'$.resource_id') IS resource_id AND json_extract(private_json,'$.game') IS game_key AND json_extract(private_json,'$.algorithm') IS 'hmac-sha256-reject64-v1'),
 CHECK(json_type(private_json,'$.seed') IS 'text' AND length(json_extract(private_json,'$.seed'))=64 AND json_extract(private_json,'$.seed') NOT GLOB '*[^0-9a-f]*'),
 CHECK(json_type(private_json,'$.commitment') IS 'text' AND length(json_extract(private_json,'$.commitment'))=64 AND json_extract(private_json,'$.commitment') NOT GLOB '*[^0-9a-f]*')
) STRICT;
CREATE TRIGGER game_random_proof_identity_guard BEFORE UPDATE ON game_random_proofs
WHEN NEW.resource_id IS NOT OLD.resource_id OR NEW.game_key IS NOT OLD.game_key
 OR NEW.fishing_id IS NOT OLD.fishing_id OR NEW.duel_id IS NOT OLD.duel_id OR NEW.blackjack_id IS NOT OLD.blackjack_id
 OR ((NEW.linklink_id IS NOT OLD.linklink_id OR NEW.linklink_summary_id IS NOT OLD.linklink_summary_id) AND NOT (OLD.linklink_id IS OLD.resource_id AND OLD.linklink_summary_id IS NULL AND NEW.linklink_id IS NULL AND NEW.linklink_summary_id IS OLD.resource_id))
 OR ((NEW.rps_id IS NOT OLD.rps_id OR NEW.rps_summary_id IS NOT OLD.rps_summary_id) AND NOT (OLD.rps_id IS OLD.resource_id AND OLD.rps_summary_id IS NULL AND NEW.rps_id IS NULL AND NEW.rps_summary_id IS OLD.resource_id))
 OR json_extract(NEW.private_json,'$.seed') IS NOT json_extract(OLD.private_json,'$.seed')
 OR json_extract(NEW.private_json,'$.commitment') IS NOT json_extract(OLD.private_json,'$.commitment')
 OR json_extract(NEW.private_json,'$.rules') IS NOT json_extract(OLD.private_json,'$.rules')
BEGIN SELECT RAISE(ABORT,'game random commitment is immutable'); END;
CREATE INDEX idx_users_charity_rank ON users(donation_credit_mag DESC,donation_credit_achieved_at,donation_credit_achieved_seq);
CREATE INDEX idx_credit_donation_sequence ON credit_operations(donation_credit_user_id,ledger_seq DESC) WHERE donation_credit_delta_sign<>0;
CREATE TABLE game_statistics_epoch (
 id INTEGER PRIMARY KEY CHECK(id=1),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 rules_version INTEGER NOT NULL CHECK(rules_version=1)
) STRICT;
CREATE TRIGGER game_statistics_epoch_immutable BEFORE UPDATE ON game_statistics_epoch BEGIN SELECT RAISE(ABORT,'statistics epoch is immutable'); END;
CREATE TRIGGER game_statistics_epoch_no_delete BEFORE DELETE ON game_statistics_epoch BEGIN SELECT RAISE(ABORT,'statistics epoch is required'); END;
CREATE TABLE game_rank_counters (
 id INTEGER PRIMARY KEY CHECK(id=1),
 next_event_seq BLOB NOT NULL CHECK(length(next_event_seq)=16 AND next_event_seq>X'00000000000000000000000000000000')
) STRICT;
CREATE TABLE game_rank_events (
 seq BLOB NOT NULL PRIMARY KEY CHECK(length(seq)=16 AND seq>X'00000000000000000000000000000000'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','gwent','steadycatch','blackjack')),
 source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128 AND source_id NOT GLOB '*[^A-Za-z0-9_:-]*'),
 settled_at INTEGER NOT NULL CHECK(settled_at BETWEEN 0 AND 253399708799),
 loss_sign INTEGER CHECK(loss_sign IS NULL OR loss_sign IN (-1,0,1)),
 loss_mag BLOB CHECK(loss_mag IS NULL OR (length(loss_mag)=16 AND loss_mag<X'80000000000000000000000000000000')),
 positive_profit BLOB NOT NULL CHECK(length(positive_profit)=16),
 charity_expires_at INTEGER CHECK(charity_expires_at IS NULL OR charity_expires_at=settled_at+604800),
 profit_7d_expires_at INTEGER CHECK(profit_7d_expires_at IS NULL OR profit_7d_expires_at=settled_at+604800),
 profit_30d_expires_at INTEGER CHECK(profit_30d_expires_at IS NULL OR profit_30d_expires_at=settled_at+2592000),
 UNIQUE(user_id,game_key,source_id),
 CHECK((loss_sign IS NULL AND loss_mag IS NULL AND charity_expires_at IS NULL) OR
  (loss_sign IS NOT NULL AND loss_mag IS NOT NULL AND charity_expires_at IS NOT NULL AND ((loss_sign=0 AND loss_mag=X'00000000000000000000000000000000') OR (loss_sign<>0 AND loss_mag>X'00000000000000000000000000000000')))),
 CHECK(game_key IN ('bidding','blackjack') OR (positive_profit=X'00000000000000000000000000000000' AND profit_7d_expires_at IS NULL AND profit_30d_expires_at IS NULL))
) STRICT;
CREATE INDEX idx_rank_events_charity_expiry ON game_rank_events(charity_expires_at,user_id,seq) WHERE charity_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit7_expiry ON game_rank_events(profit_7d_expires_at,user_id,game_key,seq) WHERE profit_7d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_profit30_expiry ON game_rank_events(profit_30d_expires_at,user_id,game_key,seq) WHERE profit_30d_expires_at IS NOT NULL;
CREATE INDEX idx_rank_events_user ON game_rank_events(user_id,settled_at,seq);
CREATE TABLE game_rank_totals (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_charity','bidding','blackjack','game_net_profit','fishing_net_profit','blackjack_net_profit','bidding_net_profit')),
 window TEXT NOT NULL CHECK(window IN ('7d','30d','history')),
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16 AND amount_mag<X'80000000000000000000000000000000'),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_phase INTEGER NOT NULL CHECK(achieved_phase IN (0,1)),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16),
 PRIMARY KEY(user_id,board,window),
 CHECK(board NOT IN ('game_charity','game_net_profit','fishing_net_profit','blackjack_net_profit','bidding_net_profit') OR window='7d'),
 CHECK(board IN ('game_charity','game_net_profit','fishing_net_profit','blackjack_net_profit','bidding_net_profit') OR amount_sign>=0),
 CHECK((amount_sign=0 AND amount_mag=X'00000000000000000000000000000000') OR (amount_sign<>0 AND amount_mag>X'00000000000000000000000000000000'))
) STRICT;
CREATE INDEX idx_rank_totals_board ON game_rank_totals(board,window,amount_sign,amount_mag DESC,achieved_at,achieved_phase,achieved_seq);
CREATE TABLE game_rank_expiry_work (
 id INTEGER PRIMARY KEY CHECK(id=1),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_charity','bidding','blackjack')),
 window TEXT NOT NULL CHECK(window IN ('7d','30d')),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799),
 delta_sign INTEGER NOT NULL CHECK(delta_sign IN (-1,0,1)),
 delta_mag BLOB NOT NULL CHECK(length(delta_mag)=32),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16), net_game_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_game_delta_sign IN(-1,0,1)), net_game_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_game_delta_mag)=32 AND (net_game_delta_sign=0)=(net_game_delta_mag=zeroblob(32))), net_fishing_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_fishing_delta_sign IN(-1,0,1)), net_fishing_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_fishing_delta_mag)=32 AND (net_fishing_delta_sign=0)=(net_fishing_delta_mag=zeroblob(32))), net_blackjack_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_blackjack_delta_sign IN(-1,0,1)), net_blackjack_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_blackjack_delta_mag)=32 AND (net_blackjack_delta_sign=0)=(net_blackjack_delta_mag=zeroblob(32))), net_fishing_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_fishing_last_seq)=16), net_blackjack_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_blackjack_last_seq)=16), net_bidding_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_bidding_delta_sign IN(-1,0,1)), net_bidding_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK(length(net_bidding_delta_mag)=32 AND (net_bidding_delta_sign=0)=(net_bidding_delta_mag=zeroblob(32))), net_bidding_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_bidding_last_seq)=16),
 CHECK(board<>'game_charity' OR window='7d'),
 CHECK((delta_sign=0)=(delta_mag=zeroblob(32)))
) STRICT;
CREATE TABLE activity_loans (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=27 AND substr(id,1,5)='loan_' AND substr(id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 quote_nonce TEXT NOT NULL UNIQUE CHECK(length(quote_nonce)=26 AND substr(quote_nonce,1,4)='lqn_' AND substr(quote_nonce,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(quote_nonce,-1,1) IN ('A','Q','g','w')),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id) ON DELETE RESTRICT,
 ledger_seq INTEGER NOT NULL UNIQUE CHECK(ledger_seq>0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 config_revision INTEGER NOT NULL CHECK(config_revision>0),
 principal INTEGER NOT NULL CHECK(principal BETWEEN 1 AND 9000000000000),
 coefficient_a INTEGER NOT NULL CHECK(coefficient_a BETWEEN 1 AND 999),
 coefficient_b INTEGER NOT NULL CHECK(coefficient_b BETWEEN 1001 AND 9000000000000000),
 nominal_milli INTEGER NOT NULL CHECK(nominal_milli BETWEEN 1 AND 9000000000000000 AND nominal_milli=principal*1000),
 disbursed_milli INTEGER NOT NULL CHECK(disbursed_milli BETWEEN 1 AND 9000000000000000 AND disbursed_milli=principal*coefficient_a),
 fee_milli INTEGER NOT NULL CHECK(fee_milli BETWEEN 1 AND 9000000000000000 AND fee_milli=nominal_milli-disbursed_milli),
 repayment_milli INTEGER NOT NULL CHECK(repayment_milli BETWEEN 1 AND 9000000000000000 AND coefficient_b<=9000000000000000/principal AND repayment_milli=principal*coefficient_b),
 interest_milli INTEGER NOT NULL CHECK(interest_milli BETWEEN 1 AND 9000000000000000 AND interest_milli=repayment_milli-nominal_milli),
 general_before_sign INTEGER NOT NULL CHECK(general_before_sign IN (0,1)),
 general_before_mag BLOB NOT NULL CHECK(length(general_before_mag)=16 AND general_before_mag<X'80000000000000000000000000000000'),
 general_after_sign INTEGER NOT NULL CHECK(general_after_sign IN (-1,0,1)),
 general_after_mag BLOB NOT NULL CHECK(length(general_after_mag)=16 AND general_after_mag<X'80000000000000000000000000000000'),
 game_before_sign INTEGER NOT NULL CHECK(game_before_sign IN (-1,0,1)),
 game_before_mag BLOB NOT NULL CHECK(length(game_before_mag)=16 AND game_before_mag<X'80000000000000000000000000000000'),
 game_after_sign INTEGER NOT NULL CHECK(game_after_sign IN (-1,0,1)),
 game_after_mag BLOB NOT NULL CHECK(length(game_after_mag)=16 AND game_after_mag<X'80000000000000000000000000000000'),
 CHECK((general_before_sign=0)=(general_before_mag=X'00000000000000000000000000000000')),
 CHECK((general_after_sign=0)=(general_after_mag=X'00000000000000000000000000000000')),
 CHECK((game_before_sign=0)=(game_before_mag=X'00000000000000000000000000000000')),
 CHECK((game_after_sign=0)=(game_after_mag=X'00000000000000000000000000000000'))
) STRICT;
CREATE INDEX idx_activity_loans_user ON activity_loans(user_id,created_at DESC,ledger_seq DESC);
CREATE TRIGGER activity_loan_immutable BEFORE UPDATE ON activity_loans BEGIN SELECT RAISE(ABORT,'loan receipt is immutable'); END;
CREATE TRIGGER activity_loan_operation_insert BEFORE INSERT ON activity_loans
WHEN NOT EXISTS(SELECT 1 FROM credit_operations o WHERE o.id=NEW.operation_id AND o.kind='activity_loan' AND o.ledger_seq=NEW.ledger_seq AND o.created_at=NEW.created_at)
 OR (SELECT count(*) FROM credit_entries WHERE operation_id=NEW.operation_id)<>4
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='general' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='general' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.repayment_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='user' AND a.user_id=NEW.user_id AND e.asset_type='game' AND e.delta_sign=1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
 OR NOT EXISTS(SELECT 1 FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=NEW.operation_id AND a.kind='external' AND a.code='external' AND e.asset_type='game' AND e.delta_sign=-1 AND hex(e.delta_mag)=printf('%032X',NEW.disbursed_milli))
BEGIN SELECT RAISE(ABORT,'loan operation mismatch'); END;
CREATE TABLE abuse_windows (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content')),
 ban_done INTEGER NOT NULL CHECK(ban_done IN (0,1)),
 suspend_done INTEGER NOT NULL CHECK(suspend_done IN (0,1)),
 next_event_seq INTEGER NOT NULL CHECK(next_event_seq>0),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,violation_kind)
) STRICT;
CREATE TABLE abuse_window_events (
 user_id INTEGER NOT NULL,
 violation_kind TEXT NOT NULL,
 seq INTEGER NOT NULL CHECK(seq>0),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576), expires_at INTEGER CHECK(expires_at>occurred_at AND expires_at<=253402300799),
 PRIMARY KEY(user_id,violation_kind,seq), UNIQUE(request_id,violation_kind),
 FOREIGN KEY(user_id,violation_kind) REFERENCES abuse_windows(user_id,violation_kind) ON DELETE CASCADE,
 CHECK((violation_kind='rpm' AND content_chars IS NULL) OR (violation_kind='short_content' AND content_chars IS NOT NULL))
) STRICT;
CREATE INDEX idx_abuse_events_window ON abuse_window_events(user_id,violation_kind,occurred_at,seq);
CREATE INDEX idx_abuse_events_expiry ON abuse_window_events(violation_kind,occurred_at,user_id,seq);
CREATE TABLE abuse_cases (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='abc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('deduction','ban','charity_suspend')),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content')),
 started_at INTEGER NOT NULL CHECK(started_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN started_at AND 253402300799),
 ended_at INTEGER CHECK(ended_at IS NULL OR ended_at BETWEEN started_at AND 253402300799),
 state TEXT NOT NULL CHECK(state IN ('active','ended')),
 result TEXT NOT NULL CHECK(result IN ('applied','extended','adjusted','released','expired')),
 CHECK((state='active' AND ended_at IS NULL AND kind<>'deduction') OR (state='ended' AND ended_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX idx_abuse_cases_active ON abuse_cases(user_id,kind) WHERE state='active';
CREATE INDEX idx_abuse_cases_user ON abuse_cases(user_id,started_at DESC,id);
CREATE INDEX idx_abuse_cases_retention ON abuse_cases(state,ended_at,id);
CREATE INDEX idx_abuse_cases_due ON abuse_cases(ends_at,id) WHERE state='active' AND ends_at IS NOT NULL;
CREATE TABLE abuse_actions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT CHECK(seq>0),
 case_id TEXT NOT NULL REFERENCES abuse_cases(id) ON DELETE CASCADE,
 action TEXT NOT NULL CHECK(action IN ('trigger','extend','adjust','release','expire')),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT CHECK(request_id IS NULL OR (length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w'))),
 operation_id TEXT REFERENCES credit_operations(id) ON DELETE RESTRICT,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 previous_ends_at INTEGER CHECK(previous_ends_at IS NULL OR previous_ends_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('charity_rpm','charity_short_content','manual_adjustment','manual_release','expired')),
 rules_json TEXT NOT NULL CHECK(length(CAST(rules_json AS BLOB))<=4096 AND json_valid(rules_json) AND json_type(rules_json)='object'),
 statistics_json TEXT NOT NULL CHECK(length(CAST(statistics_json AS BLOB))<=4096 AND json_valid(statistics_json) AND json_type(statistics_json)='object'),
 evidence_count INTEGER NOT NULL CHECK(evidence_count BETWEEN 0 AND 4096),
 evidence_bytes INTEGER NOT NULL CHECK(evidence_bytes BETWEEN 0 AND 1048576)
) STRICT;
CREATE INDEX idx_abuse_actions_case ON abuse_actions(case_id,seq DESC);
CREATE INDEX idx_abuse_actions_actor ON abuse_actions(actor_user_id) WHERE actor_user_id IS NOT NULL;
CREATE TRIGGER abuse_action_immutable BEFORE UPDATE ON abuse_actions
WHEN NOT (NEW.seq IS OLD.seq AND NEW.case_id IS OLD.case_id AND NEW.action IS OLD.action AND NEW.occurred_at IS OLD.occurred_at
 AND NEW.request_id IS OLD.request_id AND NEW.operation_id IS OLD.operation_id AND NEW.previous_ends_at IS OLD.previous_ends_at AND NEW.ends_at IS OLD.ends_at
 AND NEW.reason_code IS OLD.reason_code AND NEW.rules_json IS OLD.rules_json AND NEW.statistics_json IS OLD.statistics_json
 AND NEW.evidence_count IS OLD.evidence_count AND NEW.evidence_bytes IS OLD.evidence_bytes
 AND (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))))
BEGIN SELECT RAISE(ABORT,'penalty action is immutable'); END;
CREATE TABLE abuse_evidence (
 action_seq INTEGER NOT NULL REFERENCES abuse_actions(seq) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 4095),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 request_id TEXT NOT NULL CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 violation_kind TEXT NOT NULL CHECK(violation_kind IN ('rpm','short_content')),
 content_chars INTEGER CHECK(content_chars IS NULL OR content_chars BETWEEN 0 AND 1048576),
 PRIMARY KEY(action_seq,ordinal), UNIQUE(action_seq,request_id)
) STRICT;
CREATE TRIGGER abuse_evidence_immutable BEFORE UPDATE ON abuse_evidence BEGIN SELECT RAISE(ABORT,'penalty evidence is immutable'); END;
CREATE TRIGGER abuse_evidence_bound BEFORE INSERT ON abuse_evidence
WHEN NOT EXISTS(SELECT 1 FROM abuse_actions WHERE seq=NEW.action_seq AND NEW.ordinal<evidence_count)
BEGIN SELECT RAISE(ABORT,'penalty evidence exceeds declared count'); END;
CREATE UNIQUE INDEX idx_game_onboarding_hold_fishing ON game_onboarding_holds(fishing_batch_id,task_key) WHERE fishing_batch_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_linklink ON game_onboarding_holds(linklink_session_id,task_key) WHERE linklink_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_queue ON game_onboarding_holds(rps_queue_id,task_key) WHERE rps_queue_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_seat ON game_onboarding_holds(rps_session_id,seat_no,task_key) WHERE rps_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_duel_queue ON game_onboarding_holds(duel_queue_id,task_key) WHERE duel_queue_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_duel_seat ON game_onboarding_holds(duel_session_id,seat_no,task_key) WHERE duel_session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_game_onboarding_hold_blackjack ON game_onboarding_holds(blackjack_entry_id,task_key) WHERE blackjack_entry_id IS NOT NULL;
CREATE TRIGGER onboarding_hold_parent_insert BEFORE INSERT ON game_onboarding_holds WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=NEW.duel_queue_id AND q.user_id=NEW.user_id AND q.game_key=NEW.game_key AND
  ((q.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(q.mode,5,1))) OR
   (q.game_key='likes' AND NEW.task_key IN (q.mode||'_complete',q.mode||'_win'))))) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_seats p JOIN game_duel_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.duel_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND s.game_key=NEW.game_key AND
  ((s.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(s.mode,5,1))) OR
   (s.game_key='likes' AND NEW.task_key IN (s.mode||'_complete',s.mode||'_win'))))) OR
 (NEW.game_key='blackjack' AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.id=NEW.blackjack_entry_id AND e.user_id=NEW.user_id AND e.state IN ('waiting','seated','playing')))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER onboarding_hold_parent_update BEFORE UPDATE ON game_onboarding_holds WHEN NOT (
 (NEW.game_key='fishing' AND EXISTS(SELECT 1 FROM game_fishing_batches b WHERE b.id=NEW.fishing_batch_id AND b.user_id=NEW.user_id AND b.bait=NEW.task_key AND b.rules_version=2 AND b.state='reserved')) OR
 (NEW.game_key='linklink' AND EXISTS(SELECT 1 FROM game_linklink_sessions s WHERE s.id=NEW.linklink_session_id AND s.user_id=NEW.user_id AND s.spec=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_queue q WHERE q.id=NEW.rps_queue_id AND q.user_id=NEW.user_id AND q.mode=NEW.task_key AND q.rules_version=2)) OR
 (NEW.game_key='rps' AND EXISTS(SELECT 1 FROM game_rps_seats p JOIN game_rps_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.rps_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND p.deletion_state='active' AND s.mode=NEW.task_key AND s.rules_version=2)) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=NEW.duel_queue_id AND q.user_id=NEW.user_id AND q.game_key=NEW.game_key AND
  ((q.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(q.mode,5,1))) OR
   (q.game_key='likes' AND NEW.task_key IN (q.mode||'_complete',q.mode||'_win'))))) OR
 (NEW.game_key IN ('bidding','likes') AND EXISTS(SELECT 1 FROM game_duel_seats p JOIN game_duel_sessions s ON s.id=p.session_id WHERE p.session_id=NEW.duel_session_id AND p.seat_no=NEW.seat_no AND p.user_id=NEW.user_id AND s.game_key=NEW.game_key AND
  ((s.game_key='bidding' AND (NEW.task_key='first_win' OR NEW.task_key='complete_tier_'||substr(s.mode,5,1))) OR
   (s.game_key='likes' AND NEW.task_key IN (s.mode||'_complete',s.mode||'_win'))))) OR
 (NEW.game_key='blackjack' AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.id=NEW.blackjack_entry_id AND e.user_id=NEW.user_id AND e.state IN ('waiting','seated','playing')))
)
BEGIN SELECT RAISE(ABORT,'onboarding hold parent mismatch'); END;
CREATE TRIGGER logical_requests_rejection_insert BEFORE INSERT ON logical_requests
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.state<>'terminal' OR NEW.accounting_state<>'none' OR NEW.account_reserved_milli<>0 OR NEW.ledger_rows_remaining<>X'00000000000000000000000000000000')) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER logical_requests_rejection_update BEFORE UPDATE ON logical_requests
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.state<>'terminal' OR NEW.accounting_state<>'none' OR NEW.account_reserved_milli<>0 OR NEW.ledger_rows_remaining<>X'00000000000000000000000000000000')) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER request_logs_rejection_insert BEFORE INSERT ON request_logs
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.attempt_count<>0 OR NEW.uncached_input_tokens<>0 OR NEW.cache_write_input_tokens<>0 OR NEW.cache_read_input_tokens<>0 OR NEW.output_tokens<>0 OR NEW.usage_unknown<>0
 OR NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND r.rejection_stage IS NEW.rejection_stage AND r.rejection_reason IS NEW.rejection_reason AND r.request_method IS NEW.request_method AND r.request_path IS NEW.request_path))) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE TRIGGER request_logs_rejection_update BEFORE UPDATE ON request_logs
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/images/generations' AND NEW.route_kind IN ('openai_images_generations','charity_images_generations')))
 OR NEW.caller_result_class IS NOT 'failed' OR NEW.attempt_count<>0 OR NEW.uncached_input_tokens<>0 OR NEW.cache_write_input_tokens<>0 OR NEW.cache_read_input_tokens<>0 OR NEW.output_tokens<>0 OR NEW.usage_unknown<>0
 OR NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND r.rejection_stage IS NEW.rejection_stage AND r.rejection_reason IS NEW.rejection_reason AND r.request_method IS NEW.request_method AND r.request_path IS NEW.request_path))) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
CREATE INDEX idx_request_logs_phase ON request_logs(user_id,rejection_stage,started_at,id);
CREATE TRIGGER rejected_request_no_dispatch BEFORE INSERT ON dispatch_claims
WHEN EXISTS(SELECT 1 FROM logical_requests WHERE id=NEW.logical_request_id AND rejection_stage IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'rejected request cannot dispatch'); END;
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
CREATE TABLE request_source_facts (
 source_id INTEGER PRIMARY KEY AUTOINCREMENT,
 request_log_id INTEGER NOT NULL UNIQUE REFERENCES request_logs(id) ON DELETE CASCADE,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 kind TEXT NOT NULL CHECK(kind IN ('self','charity','unclassified','discovery')),
 effective_ip TEXT NOT NULL CHECK(length(effective_ip)<=45),
 ip_quality TEXT NOT NULL CHECK(ip_quality IN ('direct_peer','trusted_forwarded','peer_fallback')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))<=8192),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_request_sources_user_time ON request_source_facts(user_id,occurred_at,request_log_id);
CREATE TABLE request_error_bodies (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 request_log_id INTEGER REFERENCES request_logs(id) ON DELETE CASCADE,
 task_id TEXT REFERENCES image_activity_tasks(id) ON DELETE CASCADE,
 operation_id TEXT REFERENCES accepted_operations(id) ON DELETE CASCADE,
 attempt_seq INTEGER NOT NULL CHECK(attempt_seq>=1),
 event_seq INTEGER NOT NULL CHECK(event_seq>=1),
 http_status INTEGER CHECK(http_status BETWEEN 100 AND 599),
 content_type TEXT NOT NULL CHECK(length(CAST(content_type AS BLOB))<=256),
 body BLOB CHECK(body IS NULL OR length(body)<=1048576),
 bytes_saved INTEGER NOT NULL CHECK(bytes_saved BETWEEN 0 AND 1048576),
 truncated INTEGER NOT NULL CHECK(truncated IN (0,1)),
 save_state TEXT NOT NULL CHECK(save_state IN ('saved','capacity_exhausted','unavailable')),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN created_at AND 253402300799),
 CHECK((request_log_id IS NOT NULL)+(task_id IS NOT NULL)+(operation_id IS NOT NULL)=1),
 CHECK((save_state='saved' AND body IS NOT NULL AND bytes_saved=length(body))
    OR (save_state<>'saved' AND body IS NULL AND bytes_saved=0))
) STRICT;
CREATE UNIQUE INDEX idx_request_errors_request ON request_error_bodies(request_log_id,attempt_seq,event_seq) WHERE request_log_id IS NOT NULL;
CREATE UNIQUE INDEX idx_request_errors_task ON request_error_bodies(task_id,attempt_seq,event_seq) WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX idx_request_errors_operation ON request_error_bodies(operation_id,attempt_seq,event_seq) WHERE operation_id IS NOT NULL;
CREATE INDEX idx_request_errors_expiry ON request_error_bodies(expires_at,id);
CREATE TABLE observability_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 raw_body_bytes INTEGER NOT NULL DEFAULT 0 CHECK(raw_body_bytes>=0),
 raw_capacity_omissions INTEGER NOT NULL DEFAULT 0 CHECK(raw_capacity_omissions>=0),
 raw_unavailable INTEGER NOT NULL DEFAULT 0 CHECK(raw_unavailable>=0),
 access_dropped INTEGER NOT NULL DEFAULT 0 CHECK(access_dropped>=0),
 access_rows INTEGER NOT NULL DEFAULT 0 CHECK(access_rows BETWEEN 0 AND 1000000),
 capture_started_at INTEGER NOT NULL CHECK(capture_started_at BETWEEN 0 AND 253402300799),
 last_access_gap_at INTEGER CHECK(last_access_gap_at BETWEEN capture_started_at AND 253402300799)
) STRICT;
CREATE TRIGGER request_error_delete_count AFTER DELETE ON request_error_bodies
BEGIN UPDATE observability_state SET raw_body_bytes=raw_body_bytes-OLD.bytes_saved WHERE id=1; END;
CREATE TABLE audit_access_events (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='aev_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 caller_key_user_id INTEGER REFERENCES caller_keys(user_id) ON DELETE SET NULL,
 caller_key_generation INTEGER CHECK(caller_key_generation>=0),
 path_kind TEXT NOT NULL CHECK(path_kind IN ('models','billing_subscription','billing_usage','v1_billing_subscription','v1_billing_usage','sub2api_billing','chat_head')),
 method TEXT NOT NULL CHECK((path_kind='chat_head' AND method='HEAD') OR (path_kind<>'chat_head' AND method='GET')),
 http_status INTEGER NOT NULL CHECK(http_status BETWEEN 100 AND 599),
 response_kind TEXT NOT NULL CHECK(response_kind IN ('api_json','spa_fallback','other')),
 effective_ip TEXT NOT NULL CHECK(length(effective_ip)<=45),
 ip_quality TEXT NOT NULL CHECK(ip_quality IN ('direct_peer','trusted_forwarded','peer_fallback')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))<=8192),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 CHECK((caller_key_user_id IS NULL)=(caller_key_generation IS NULL)),
 CHECK(caller_key_user_id IS NULL OR caller_key_user_id=user_id)
) STRICT;
CREATE INDEX idx_audit_access_user_time ON audit_access_events(user_id,occurred_at,id);
CREATE INDEX idx_audit_access_key_time ON audit_access_events(caller_key_user_id,caller_key_generation,occurred_at,id);
CREATE INDEX idx_audit_access_path_time ON audit_access_events(path_kind,occurred_at,id);
CREATE TRIGGER audit_access_delete_count AFTER DELETE ON audit_access_events
BEGIN UPDATE observability_state SET access_rows=access_rows-1 WHERE id=1; END;
CREATE TRIGGER audit_access_key_delete BEFORE DELETE ON caller_keys
BEGIN UPDATE audit_access_events SET caller_key_user_id=NULL,caller_key_generation=NULL WHERE caller_key_user_id=OLD.user_id; END;
CREATE TRIGGER audit_access_key_rotate AFTER UPDATE OF generation,key_hash ON caller_keys
WHEN NEW.generation<>OLD.generation OR NEW.key_hash IS NULL
BEGIN UPDATE audit_access_events SET caller_key_user_id=NULL,caller_key_generation=NULL WHERE caller_key_user_id=OLD.user_id; END;
CREATE TABLE anonymous_access_minutes (
 minute_at INTEGER NOT NULL CHECK(minute_at BETWEEN 0 AND 253402300799 AND minute_at%60=0),
 path_kind TEXT NOT NULL CHECK(path_kind IN ('models','billing_subscription','billing_usage','v1_billing_subscription','v1_billing_usage','sub2api_billing','chat_head')),
 method TEXT NOT NULL CHECK((path_kind='chat_head' AND method='HEAD') OR (path_kind<>'chat_head' AND method='GET')),
 status_class INTEGER NOT NULL CHECK(status_class BETWEEN 1 AND 5),
 count INTEGER NOT NULL CHECK(count>=0),
 PRIMARY KEY(minute_at,path_kind,method,status_class)
) STRICT, WITHOUT ROWID;
CREATE TABLE charity_request_outcomes (
 request_log_id INTEGER PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253402300799),
 dispatched INTEGER NOT NULL CHECK(dispatched IN (0,1)),
 result TEXT NOT NULL CHECK(result IN ('success','failure','cancelled'))
) STRICT;
CREATE INDEX idx_charity_outcomes_model_time ON charity_request_outcomes(model_id,completed_at,request_log_id);
CREATE TABLE risk_audit_minutes (
 epoch TEXT NOT NULL CHECK(length(epoch) BETWEEN 1 AND 64),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 minute INTEGER NOT NULL CHECK(minute>=0 AND minute%60=0),
 call_kind TEXT NOT NULL CHECK(call_kind IN ('total','self','charity','unclassified')),
 rpm_committed INTEGER NOT NULL DEFAULT 0 CHECK(rpm_committed>=0),
 rpm_released INTEGER NOT NULL DEFAULT 0 CHECK(rpm_released>=0),
 rpm_pending INTEGER NOT NULL DEFAULT 0 CHECK(rpm_pending>=0),
 rpm_denied INTEGER NOT NULL DEFAULT 0 CHECK(rpm_denied>=0),
 concurrency_denied INTEGER NOT NULL DEFAULT 0 CHECK(concurrency_denied>=0),
 occupancy_millis INTEGER NOT NULL DEFAULT 0 CHECK(occupancy_millis>=0),
 peak INTEGER NOT NULL DEFAULT 0 CHECK(peak>=0),
 rpm_limit INTEGER,
 concurrency_limit INTEGER,
 coverage INTEGER NOT NULL DEFAULT 0 CHECK(coverage>=0),
 config_revision INTEGER NOT NULL CHECK(config_revision>=1),
 updated_at INTEGER NOT NULL CHECK(updated_at>=minute),
 PRIMARY KEY(epoch,user_id,minute,call_kind),
 CHECK(rpm_limit IS NULL OR rpm_limit>0),
 CHECK(concurrency_limit IS NULL OR concurrency_limit>0)
) STRICT;
CREATE INDEX idx_risk_minutes_user_time ON risk_audit_minutes(user_id,minute,call_kind);
CREATE INDEX idx_risk_minutes_retention ON risk_audit_minutes(minute,user_id);
CREATE TABLE risk_audit_gaps (
 epoch TEXT NOT NULL CHECK(length(epoch) BETWEEN 1 AND 64),
 minute INTEGER NOT NULL CHECK(minute>=0 AND minute%60=0),
 reason TEXT NOT NULL CHECK(reason IN ('restart','queue_overflow','capacity','persistence','configuration','clock')),
 lost_count INTEGER NOT NULL DEFAULT 0 CHECK(lost_count>=0),
 PRIMARY KEY(epoch,minute,reason)
) STRICT;
CREATE INDEX idx_risk_gaps_time ON risk_audit_gaps(minute);
CREATE TABLE risk_audit_config (
 id INTEGER NOT NULL PRIMARY KEY CHECK(id=1),
 threshold_percent INTEGER NOT NULL CHECK(threshold_percent BETWEEN 1 AND 100),
 consecutive_minutes INTEGER NOT NULL CHECK(consecutive_minutes BETWEEN 1 AND 60),
 shared_ip_hours INTEGER NOT NULL CHECK(shared_ip_hours BETWEEN 1 AND 720),
 shared_ip_users INTEGER NOT NULL CHECK(shared_ip_users BETWEEN 2 AND 1000),
 revision INTEGER NOT NULL CHECK(revision>=1),
 updated_at INTEGER NOT NULL CHECK(updated_at>=0)
, user_ip_window_hours INTEGER NOT NULL DEFAULT 24 CHECK(user_ip_window_hours BETWEEN 1 AND 720), user_ip_min_ips INTEGER NOT NULL DEFAULT 3 CHECK(user_ip_min_ips BETWEEN 2 AND 1000)) STRICT;
CREATE TABLE risk_client_rules (
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 status TEXT NOT NULL CHECK(status IN ('suspected','confirmed')),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>=1),
 conditions_json TEXT NOT NULL CHECK(json_valid(conditions_json) AND length(CAST(conditions_json AS BLOB)) BETWEEN 2 AND 16384),
 evidence_note TEXT NOT NULL DEFAULT '' CHECK(length(evidence_note)<=4096),
 evidence_url TEXT NOT NULL DEFAULT '' CHECK(length(evidence_url)<=2048),
 created_by_role TEXT NOT NULL CHECK(created_by_role IN ('admin','level6')),
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_by_role TEXT NOT NULL CHECK(updated_by_role IN ('admin','level6')),
 updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at)
) STRICT;
CREATE INDEX idx_risk_rules_updated ON risk_client_rules(updated_at,id);
CREATE TABLE economy_audit_checkpoint (
 id INTEGER PRIMARY KEY CHECK(id=1),
 last_ledger_seq INTEGER NOT NULL DEFAULT 0 CHECK(typeof(last_ledger_seq)='integer' AND last_ledger_seq BETWEEN 0 AND 9223372036854775807),
 first_ledger_seq INTEGER CHECK(first_ledger_seq IS NULL OR (typeof(first_ledger_seq)='integer' AND first_ledger_seq BETWEEN 1 AND last_ledger_seq)),
 first_occurred_at INTEGER CHECK(first_occurred_at IS NULL OR (typeof(first_occurred_at)='integer' AND first_occurred_at BETWEEN 0 AND 253402300799)),
 offset_minutes INTEGER CHECK(offset_minutes IS NULL OR (typeof(offset_minutes)='integer' AND offset_minutes BETWEEN -720 AND 840 AND offset_minutes%30=0)),
 unclassified_operations INTEGER NOT NULL DEFAULT 0 CHECK(typeof(unclassified_operations)='integer' AND unclassified_operations BETWEEN 0 AND last_ledger_seq),
 opening_known INTEGER NOT NULL CHECK(opening_known IN (0,1)),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799),
 CHECK((last_ledger_seq=0 AND first_ledger_seq IS NULL AND first_occurred_at IS NULL) OR
       (last_ledger_seq>0 AND first_ledger_seq IS NOT NULL AND first_occurred_at IS NOT NULL AND offset_minutes IS NOT NULL))
) STRICT;
CREATE TABLE economy_audit_buckets (
 asset_type TEXT NOT NULL CHECK(asset_type IN ('general','game','sketch_paper','sketch_brush')),
 kind TEXT NOT NULL CHECK(typeof(kind)='text' AND length(kind) BETWEEN 1 AND 64 AND kind NOT GLOB '*[^a-z0-9_]*'),
 source_type TEXT NOT NULL CHECK(typeof(source_type)='text' AND length(source_type) BETWEEN 1 AND 32 AND source_type NOT GLOB '*[^a-z0-9_]*'),
 channel TEXT NOT NULL CHECK(channel IN ('admin','account','checkin','welfare','thursday','api','charity','donation','fishing','linklink','rps','bidding','likes','gwent','steadycatch','blackjack','onboarding','loan','picture_book','inactivity','penalty','fat_fish','lake_notes','unclassified')),
 bucket TEXT NOT NULL CHECK(bucket IN ('hour','day')),
 bucket_start INTEGER NOT NULL CHECK(typeof(bucket_start)='integer' AND bucket_start BETWEEN -86400 AND 253402300799),
 offset_minutes INTEGER NOT NULL CHECK(typeof(offset_minutes)='integer' AND offset_minutes BETWEEN -720 AND 840 AND offset_minutes%30=0),
 issued TEXT NOT NULL CHECK(typeof(issued)='text' AND length(issued) BETWEEN 1 AND 64 AND issued NOT GLOB '*[^0-9]*' AND (issued='0' OR substr(issued,1,1)<>'0')),
 reclaimed TEXT NOT NULL CHECK(typeof(reclaimed)='text' AND length(reclaimed) BETWEEN 1 AND 64 AND reclaimed NOT GLOB '*[^0-9]*' AND (reclaimed='0' OR substr(reclaimed,1,1)<>'0')),
 user_income TEXT NOT NULL CHECK(typeof(user_income)='text' AND length(user_income) BETWEEN 1 AND 64 AND user_income NOT GLOB '*[^0-9]*' AND (user_income='0' OR substr(user_income,1,1)<>'0')),
 user_expense TEXT NOT NULL CHECK(typeof(user_expense)='text' AND length(user_expense) BETWEEN 1 AND 64 AND user_expense NOT GLOB '*[^0-9]*' AND (user_expense='0' OR substr(user_expense,1,1)<>'0')),
 internal_transfer TEXT NOT NULL CHECK(typeof(internal_transfer)='text' AND length(internal_transfer) BETWEEN 1 AND 64 AND internal_transfer NOT GLOB '*[^0-9]*' AND (internal_transfer='0' OR substr(internal_transfer,1,1)<>'0')),
 operation_count INTEGER NOT NULL CHECK(typeof(operation_count)='integer' AND operation_count BETWEEN 1 AND 9223372036854775807),
 first_ledger_seq INTEGER NOT NULL CHECK(typeof(first_ledger_seq)='integer' AND first_ledger_seq BETWEEN 1 AND 9223372036854775807),
 last_ledger_seq INTEGER NOT NULL CHECK(typeof(last_ledger_seq)='integer' AND last_ledger_seq BETWEEN first_ledger_seq AND 9223372036854775807),
 PRIMARY KEY(asset_type,kind,source_type,channel,bucket,bucket_start,offset_minutes),
 CHECK((bucket_start+offset_minutes*60)%CASE bucket WHEN 'hour' THEN 3600 ELSE 86400 END=0)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_economy_audit_buckets_time ON economy_audit_buckets(asset_type,bucket,bucket_start,kind,source_type,channel);
CREATE TRIGGER legal_hold_steward_read_insert_guard BEFORE INSERT ON legal_hold_steward_reads
WHEN NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.user_id AND u.is_admin=0 AND u.is_banned=0 AND u.level=6)
 OR NEW.read_count<>1 OR NEW.first_read_at<>NEW.last_read_at
 OR NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.object_kind IN ('donation','request_log') AND h.state='active' AND NEW.first_read_at>=h.created_at AND NEW.last_read_at<h.expires_at)
BEGIN SELECT RAISE(ABORT,'steward held read is inconsistent'); END;
CREATE TRIGGER legal_hold_steward_read_update_guard BEFORE UPDATE ON legal_hold_steward_reads
WHEN NEW.id IS NOT OLD.id OR NEW.hold_id_text IS NOT OLD.hold_id_text OR NEW.first_read_at IS NOT OLD.first_read_at
 OR NOT (
  (NEW.user_id IS OLD.user_id AND NEW.last_read_at>=OLD.last_read_at AND NEW.read_count=OLD.read_count+1
   AND EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.user_id AND u.is_admin=0 AND u.is_banned=0 AND u.level=6)
   AND EXISTS(SELECT 1 FROM legal_holds h WHERE h.id=NEW.hold_id_text AND h.state='active' AND NEW.last_read_at<h.expires_at))
  OR (NEW.user_id IS NULL AND OLD.user_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users u WHERE u.id=OLD.user_id)
   AND NEW.last_read_at=OLD.last_read_at AND NEW.read_count=OLD.read_count)
 )
BEGIN SELECT RAISE(ABORT,'steward held read is inconsistent'); END;
CREATE TRIGGER credit_account_asset_insert BEFORE INSERT ON credit_accounts
WHEN (NEW.asset_type='game' AND (NEW.kind='pool' OR NEW.code IN ('forward_reserve','charity_reserve')))
 OR (NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT
  (NEW.kind='user' OR (NEW.kind='external' AND NEW.code='external') OR (NEW.kind='platform' AND NEW.code='image_activity_reserve')))
 OR (NEW.code='image_activity_reserve' AND NEW.asset_type NOT IN ('sketch_paper','sketch_brush'))
BEGIN SELECT RAISE(ABORT,'account code does not support this asset'); END;
CREATE TRIGGER activity_account_integer_insert BEFORE INSERT ON credit_accounts
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),32,1))-1)*1)%1000=0)
BEGIN SELECT RAISE(ABORT,'activity balance is not an integer'); END;
CREATE TRIGGER activity_entry_integer_insert BEFORE INSERT ON credit_entries
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND (NOT (((instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),32,1))-1)*1)%1000=0) OR (NEW.balance_after_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),32,1))-1)*1)%1000=0)))
BEGIN SELECT RAISE(ABORT,'activity entry is not an integer'); END;
CREATE TRIGGER activity_account_integer_update BEFORE UPDATE ON credit_accounts
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_mag),32,1))-1)*1)%1000=0)
BEGIN SELECT RAISE(ABORT,'activity balance is not an integer'); END;
CREATE TRIGGER activity_entry_integer_update BEFORE UPDATE ON credit_entries
WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND (NOT (((instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.delta_mag),32,1))-1)*1)%1000=0) OR (NEW.balance_after_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.balance_after_mag),32,1))-1)*1)%1000=0)))
BEGIN SELECT RAISE(ABORT,'activity entry is not an integer'); END;
CREATE TRIGGER donation_thanks_immutable BEFORE UPDATE OF discord_public_thanks ON donations
WHEN NEW.user_id IS NOT NULL AND OLD.discord_public_thanks IS NOT NEW.discord_public_thanks
BEGIN SELECT RAISE(ABORT,'donation public thanks choice is immutable'); END;
CREATE TABLE limited_activity_configs (
 activity_key TEXT NOT NULL PRIMARY KEY CHECK(length(activity_key) BETWEEN 1 AND 64 AND activity_key NOT GLOB '*[^a-z0-9-]*'),
 visible INTEGER NOT NULL CHECK(visible IN (0,1)),
 starts_at INTEGER CHECK(starts_at IS NULL OR starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 module_config TEXT NOT NULL CHECK(typeof(module_config)='text' AND length(CAST(module_config AS BLOB)) BETWEEN 2 AND 65536 AND json_valid(module_config)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((starts_at IS NULL AND ends_at IS NULL) OR (starts_at IS NOT NULL AND ends_at IS NOT NULL AND starts_at<ends_at))
) STRICT;
CREATE TABLE limited_activity_revisions (
 activity_key TEXT NOT NULL REFERENCES limited_activity_configs(activity_key),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 visible INTEGER NOT NULL CHECK(visible IN (0,1)),
 starts_at INTEGER CHECK(starts_at IS NULL OR starts_at BETWEEN 0 AND 253402300799),
 ends_at INTEGER CHECK(ends_at IS NULL OR ends_at BETWEEN 0 AND 253402300799),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 module_config TEXT NOT NULL CHECK(typeof(module_config)='text' AND length(CAST(module_config AS BLOB)) BETWEEN 2 AND 65536 AND json_valid(module_config)),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(activity_key,revision),
 CHECK((starts_at IS NULL AND ends_at IS NULL) OR (starts_at IS NOT NULL AND ends_at IS NOT NULL AND starts_at<ends_at))
) STRICT;
CREATE TRIGGER limited_activity_revisions_immutable BEFORE UPDATE ON limited_activity_revisions
WHEN NEW.activity_key<>OLD.activity_key OR NEW.revision<>OLD.revision OR NEW.visible<>OLD.visible
 OR NEW.starts_at IS NOT OLD.starts_at OR NEW.ends_at IS NOT OLD.ends_at OR NEW.paused<>OLD.paused
 OR NEW.module_config<>OLD.module_config OR NEW.created_at<>OLD.created_at
 OR (NEW.actor_user_id IS NOT OLD.actor_user_id AND NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL))
BEGIN SELECT RAISE(ABORT,'limited activity revisions are immutable'); END;
CREATE TRIGGER limited_activity_revisions_no_delete BEFORE DELETE ON limited_activity_revisions
BEGIN SELECT RAISE(ABORT,'limited activity revisions are immutable'); END;
CREATE TABLE activity_exchange_state (
 activity_key TEXT NOT NULL REFERENCES limited_activity_configs(activity_key),
 asset_type TEXT NOT NULL CHECK(asset_type IN ('sketch_paper','sketch_brush')),
 total_exchanged_mag BLOB NOT NULL CHECK(typeof(total_exchanged_mag)='blob' AND length(total_exchanged_mag)=16),
 cap_mag BLOB CHECK(cap_mag IS NULL OR (typeof(cap_mag)='blob' AND length(cap_mag)=16)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 PRIMARY KEY(activity_key,asset_type),
 CHECK((asset_type='sketch_paper' AND cap_mag IS NULL) OR (asset_type='sketch_brush' AND cap_mag IS NOT NULL))
) STRICT;
CREATE TRIGGER activity_exchange_state_monotone BEFORE UPDATE ON activity_exchange_state
WHEN NEW.activity_key<>OLD.activity_key OR NEW.asset_type<>OLD.asset_type OR NEW.total_exchanged_mag<OLD.total_exchanged_mag OR NEW.revision<=OLD.revision
BEGIN SELECT RAISE(ABORT,'invalid activity exchange state transition'); END;
CREATE TRIGGER activity_exchange_state_no_delete BEFORE DELETE ON activity_exchange_state
BEGIN SELECT RAISE(ABORT,'activity exchange totals are permanent'); END;
CREATE TABLE activity_exchange_receipts (
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES credit_operations(id),
 activity_key TEXT NOT NULL,
 config_revision INTEGER NOT NULL,
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 asset_type TEXT NOT NULL CHECK(asset_type IN ('sketch_paper','sketch_brush')),
 quantity_mag BLOB NOT NULL CHECK(typeof(quantity_mag)='blob' AND length(quantity_mag)=16 AND quantity_mag>X'00000000000000000000000000000000'),
 unit_price_milli INTEGER NOT NULL CHECK(unit_price_milli BETWEEN 1 AND 9000000000000000),
 cost_mag BLOB NOT NULL CHECK(typeof(cost_mag)='blob' AND length(cost_mag)=16 AND cost_mag>X'00000000000000000000000000000000' AND cost_mag<=X'7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF'),
 ledger_seq INTEGER NOT NULL CHECK(ledger_seq BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 FOREIGN KEY(activity_key,config_revision) REFERENCES limited_activity_revisions(activity_key,revision)
) STRICT;
CREATE INDEX idx_activity_exchange_receipts_user_time ON activity_exchange_receipts(user_id,created_at,operation_id);
CREATE TRIGGER activity_exchange_receipts_immutable BEFORE UPDATE ON activity_exchange_receipts
WHEN NEW.operation_id<>OLD.operation_id OR NEW.activity_key<>OLD.activity_key OR NEW.config_revision<>OLD.config_revision
 OR NEW.asset_type<>OLD.asset_type OR NEW.quantity_mag<>OLD.quantity_mag OR NEW.unit_price_milli<>OLD.unit_price_milli
 OR NEW.cost_mag<>OLD.cost_mag OR NEW.ledger_seq<>OLD.ledger_seq OR NEW.created_at<>OLD.created_at
 OR (NEW.user_id IS NOT OLD.user_id AND NOT (OLD.user_id IS NOT NULL AND NEW.user_id IS NULL))
BEGIN SELECT RAISE(ABORT,'activity exchange receipts are immutable'); END;
CREATE TRIGGER activity_exchange_receipts_no_delete BEFORE DELETE ON activity_exchange_receipts
BEGIN SELECT RAISE(ABORT,'activity exchange receipts are permanent'); END;
CREATE TABLE inactivity_policy (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 config_json TEXT NOT NULL CHECK(json_valid(config_json) AND length(CAST(config_json AS BLOB))<=4096),
 decay_grace_until INTEGER NOT NULL CHECK(decay_grace_until BETWEEN 0 AND 253402300799),
 protection_grace_until INTEGER NOT NULL CHECK(protection_grace_until BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL
) STRICT;
CREATE TABLE user_activity_state (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 observation_started_at INTEGER NOT NULL CHECK(observation_started_at BETWEEN 0 AND 253402300799),
 last_active_at INTEGER CHECK(last_active_at IS NULL OR last_active_at BETWEEN 0 AND 253402300799),
 activity_seq INTEGER NOT NULL DEFAULT 0 CHECK(activity_seq>=0),
 activity_epoch INTEGER NOT NULL DEFAULT 0 CHECK(activity_epoch>=0),
 schedule_revision INTEGER NOT NULL DEFAULT 0 CHECK(schedule_revision>=0),
 next_due_at INTEGER CHECK(next_due_at IS NULL OR next_due_at BETWEEN 0 AND 253402300799),
 last_decay_at INTEGER CHECK(last_decay_at IS NULL OR last_decay_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_activity_schedule ON user_activity_state(schedule_revision,user_id);
CREATE INDEX idx_activity_due ON user_activity_state(next_due_at,user_id) WHERE next_due_at IS NOT NULL;
CREATE TABLE inactivity_runs (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 policy_revision INTEGER NOT NULL CHECK(policy_revision>=1),
 activity_epoch INTEGER NOT NULL CHECK(activity_epoch>=0),
 due_slot INTEGER NOT NULL CHECK(due_slot BETWEEN 0 AND 253402300799),
 action TEXT NOT NULL CHECK(action IN ('decay','protection')),
 general_milli TEXT NOT NULL CHECK(length(general_milli) BETWEEN 1 AND 39 AND general_milli NOT GLOB '*[^0-9]*' AND (general_milli='0' OR substr(general_milli,1,1)<>'0') AND (length(general_milli)<39 OR general_milli<='170141183460469231731687303715884105727')),
 game_milli TEXT NOT NULL CHECK(length(game_milli) BETWEEN 1 AND 39 AND game_milli NOT GLOB '*[^0-9]*' AND (game_milli='0' OR substr(game_milli,1,1)<>'0') AND (length(game_milli)<39 OR game_milli<='170141183460469231731687303715884105727')),
 ledger_operation_id TEXT REFERENCES credit_operations(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 deidentify_at INTEGER NOT NULL CHECK(deidentify_at BETWEEN 0 AND 253402300799 AND deidentify_at=created_at+7776000),
 retain_until INTEGER NOT NULL CHECK(retain_until BETWEEN 0 AND 253402300799 AND retain_until=created_at+34560000),
 UNIQUE(user_id,policy_revision,activity_epoch,due_slot)
) STRICT;
CREATE INDEX idx_inactivity_runs_user ON inactivity_runs(user_id,created_at,id);
CREATE INDEX idx_inactivity_runs_retention ON inactivity_runs(retain_until,id);
CREATE INDEX idx_inactivity_runs_identity ON inactivity_runs(deidentify_at,id) WHERE user_id IS NOT NULL;
CREATE TRIGGER inactivity_runs_immutable BEFORE UPDATE ON inactivity_runs
WHEN NEW.id<>OLD.id OR NEW.policy_revision<>OLD.policy_revision OR NEW.activity_epoch<>OLD.activity_epoch
 OR NEW.due_slot<>OLD.due_slot OR NEW.action<>OLD.action OR NEW.general_milli<>OLD.general_milli OR NEW.game_milli<>OLD.game_milli
 OR NEW.created_at<>OLD.created_at OR NEW.deidentify_at<>OLD.deidentify_at OR NEW.retain_until<>OLD.retain_until
 OR (NEW.user_id IS NOT OLD.user_id AND NOT (OLD.user_id IS NOT NULL AND NEW.user_id IS NULL))
 OR (NEW.ledger_operation_id IS NOT OLD.ledger_operation_id AND NOT (OLD.ledger_operation_id IS NOT NULL AND NEW.ledger_operation_id IS NULL))
BEGIN SELECT RAISE(ABORT,'inactivity execution receipts are immutable'); END;
CREATE TABLE inactivity_audits (
 id TEXT PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='op_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 action TEXT NOT NULL CHECK(action IN ('configure','preview')),
 policy_revision INTEGER NOT NULL CHECK(policy_revision>=1),
 details_json TEXT NOT NULL CHECK(json_valid(details_json) AND length(CAST(details_json AS BLOB))<=4096),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 deidentify_at INTEGER NOT NULL CHECK(deidentify_at BETWEEN 0 AND 253402300799 AND deidentify_at=created_at+7776000),
 retain_until INTEGER NOT NULL CHECK(retain_until BETWEEN 0 AND 253402300799 AND retain_until=created_at+34560000)
) STRICT;
CREATE INDEX idx_inactivity_audits_retention ON inactivity_audits(retain_until,id);
CREATE INDEX idx_inactivity_audits_identity ON inactivity_audits(deidentify_at,id) WHERE actor_user_id IS NOT NULL;
CREATE TRIGGER fishing_batch_probability_immutable BEFORE UPDATE OF blue_fish_chance_bps,config_revision ON game_fishing_batches
WHEN NEW.blue_fish_chance_bps IS NOT OLD.blue_fish_chance_bps OR NEW.config_revision IS NOT OLD.config_revision
BEGIN SELECT RAISE(ABORT,'fishing batch configuration is immutable'); END;
CREATE TABLE game_rank_net_rebuild (
 id INTEGER PRIMARY KEY CHECK(id=1),
 phase INTEGER NOT NULL CHECK(phase IN (0,1,2)),
 through_seq BLOB CHECK(through_seq IS NULL OR (length(through_seq)=16 AND through_seq>zeroblob(16))),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16),
 CHECK(phase=0 OR through_seq IS NOT NULL)
) STRICT;
CREATE TABLE game_rank_net_rebuild_totals (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_net_profit','fishing_net_profit','blackjack_net_profit','bidding_net_profit')),
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=32),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16),
 PRIMARY KEY(user_id,board),
 CHECK((amount_sign=0)=(amount_mag=zeroblob(32)))
) STRICT;
CREATE TRIGGER image_model_revisions_whole_insert BEFORE INSERT ON image_model_revisions
WHEN (NEW.paper_price_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),32,1))-1)*1)%1000=0)) OR (NEW.brush_price_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),32,1))-1)*1)%1000=0))
BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE TRIGGER image_activity_tasks_whole_insert BEFORE INSERT ON image_activity_tasks
WHEN (NEW.paper_charge_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),32,1))-1)*1)%1000=0)) OR (NEW.brush_charge_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),32,1))-1)*1)%1000=0))
BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE TRIGGER image_model_revisions_whole_update BEFORE UPDATE ON image_model_revisions
WHEN (NEW.paper_price_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_price_mag),32,1))-1)*1)%1000=0)) OR (NEW.brush_price_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_price_mag),32,1))-1)*1)%1000=0))
BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE TRIGGER image_activity_tasks_whole_update BEFORE UPDATE ON image_activity_tasks
WHEN (NEW.paper_charge_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.paper_charge_mag),32,1))-1)*1)%1000=0)) OR (NEW.brush_charge_mag IS NOT NULL AND NOT (((instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.brush_charge_mag),32,1))-1)*1)%1000=0))
BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE TRIGGER donation_token_breakdown_insert AFTER INSERT ON donation_keys WHEN NEW.breakdown_started_at=0
BEGIN UPDATE donation_keys SET breakdown_started_at=NEW.created_at WHERE id=NEW.id; END;
CREATE TRIGGER donation_token_configuration_insert BEFORE INSERT ON donation_keys
WHEN (NEW.input_token_reserve IS NULL)<>(NEW.output_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NOT NULL AND (NEW.input_token_reserve>9223372036854775807-NEW.output_token_reserve OR (NEW.input_token_reserve=0 AND NEW.output_token_reserve=0)))
 OR ((NEW.input_token_limit_mag IS NOT NULL OR NEW.output_token_limit_mag IS NOT NULL) AND NEW.input_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NULL AND EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch WHERE r.donation_key_id=NEW.id AND e.metric IN ('input_tokens','output_tokens')))
BEGIN SELECT RAISE(ABORT,'invalid token reservation configuration'); END;
CREATE TRIGGER dispatch_claims_token_vector_insert BEFORE INSERT ON dispatch_claims
WHEN (NEW.reserved_input_tokens IS NULL)<>(NEW.reserved_output_tokens IS NULL)
 OR (NEW.reserved_input_tokens IS NOT NULL AND (NEW.reserved_input_tokens>9223372036854775807-NEW.reserved_output_tokens OR NEW.reserved_input_tokens+NEW.reserved_output_tokens<>NEW.reserved_tokens))
BEGIN SELECT RAISE(ABORT,'invalid token reservation vector'); END;
CREATE TRIGGER donation_usage_reservations_token_vector_insert BEFORE INSERT ON donation_usage_reservations
WHEN (NEW.input_tokens_reserved IS NULL)<>(NEW.output_tokens_reserved IS NULL)
 OR (NEW.input_tokens_reserved IS NOT NULL AND (NEW.input_tokens_reserved>9223372036854775807-NEW.output_tokens_reserved OR NEW.input_tokens_reserved+NEW.output_tokens_reserved<>NEW.tokens_reserved))
BEGIN SELECT RAISE(ABORT,'invalid token reservation vector'); END;
CREATE TRIGGER donation_token_actual_insert BEFORE INSERT ON donation_usage_reservations
WHEN (NEW.input_tokens_actual IS NULL)<>(NEW.output_tokens_actual IS NULL)
 OR (NEW.input_tokens_actual IS NOT NULL AND (NEW.tokens_actual IS NULL OR NEW.input_tokens_actual>9223372036854775807-NEW.output_tokens_actual OR NEW.input_tokens_actual+NEW.output_tokens_actual<>NEW.tokens_actual))
BEGIN SELECT RAISE(ABORT,'invalid actual token vector'); END;
CREATE TRIGGER donation_token_configuration_update BEFORE UPDATE ON donation_keys
WHEN (NEW.input_token_reserve IS NULL)<>(NEW.output_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NOT NULL AND (NEW.input_token_reserve>9223372036854775807-NEW.output_token_reserve OR (NEW.input_token_reserve=0 AND NEW.output_token_reserve=0)))
 OR ((NEW.input_token_limit_mag IS NOT NULL OR NEW.output_token_limit_mag IS NOT NULL) AND NEW.input_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NULL AND EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch WHERE r.donation_key_id=NEW.id AND e.metric IN ('input_tokens','output_tokens')))
BEGIN SELECT RAISE(ABORT,'invalid token reservation configuration'); END;
CREATE TRIGGER dispatch_claims_token_vector_update BEFORE UPDATE ON dispatch_claims
WHEN (NEW.reserved_input_tokens IS NULL)<>(NEW.reserved_output_tokens IS NULL)
 OR (NEW.reserved_input_tokens IS NOT NULL AND (NEW.reserved_input_tokens>9223372036854775807-NEW.reserved_output_tokens OR NEW.reserved_input_tokens+NEW.reserved_output_tokens<>NEW.reserved_tokens))
BEGIN SELECT RAISE(ABORT,'invalid token reservation vector'); END;
CREATE TRIGGER donation_usage_reservations_token_vector_update BEFORE UPDATE ON donation_usage_reservations
WHEN (NEW.input_tokens_reserved IS NULL)<>(NEW.output_tokens_reserved IS NULL)
 OR (NEW.input_tokens_reserved IS NOT NULL AND (NEW.input_tokens_reserved>9223372036854775807-NEW.output_tokens_reserved OR NEW.input_tokens_reserved+NEW.output_tokens_reserved<>NEW.tokens_reserved))
BEGIN SELECT RAISE(ABORT,'invalid token reservation vector'); END;
CREATE TRIGGER donation_token_actual_update BEFORE UPDATE ON donation_usage_reservations
WHEN (NEW.input_tokens_actual IS NULL)<>(NEW.output_tokens_actual IS NULL)
 OR (NEW.input_tokens_actual IS NOT NULL AND (NEW.tokens_actual IS NULL OR NEW.input_tokens_actual>9223372036854775807-NEW.output_tokens_actual OR NEW.input_tokens_actual+NEW.output_tokens_actual<>NEW.tokens_actual))
BEGIN SELECT RAISE(ABORT,'invalid actual token vector'); END;
CREATE TABLE discord_blacklist (
 discord_id TEXT NOT NULL PRIMARY KEY CHECK(length(discord_id) BETWEEN 1 AND 20 AND discord_id NOT GLOB '*[^0-9]*' AND substr(discord_id,1,1)<>'0' AND (length(discord_id)<20 OR discord_id<='18446744073709551615')),
 reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE admin_account_deletions (
 alert_id INTEGER NOT NULL PRIMARY KEY REFERENCES admin_alerts(id) ON DELETE CASCADE,
 snapshot_json TEXT NOT NULL CHECK(length(snapshot_json) BETWEEN 2 AND 8192 AND json_valid(snapshot_json) AND json_type(snapshot_json)='object')
, snapshot_version INTEGER NOT NULL DEFAULT 1 CHECK(snapshot_version IN (1,2)), former_user_id INTEGER CHECK(former_user_id>0), discord_id TEXT CHECK(length(CAST(discord_id AS BLOB)) BETWEEN 1 AND 128), registered_at INTEGER CHECK(registered_at BETWEEN 0 AND 253402300799), deleted_at INTEGER CHECK(deleted_at BETWEEN 0 AND 253402300799), effective_level INTEGER CHECK(effective_level BETWEEN 1 AND 6), source TEXT NOT NULL DEFAULT 'unknown' CHECK(source IN ('unknown','self','admin','system')), actor_user_id INTEGER CHECK(actor_user_id>0), ban_active INTEGER CHECK(ban_active IN (0,1)), pause_active INTEGER CHECK(pause_active IN (0,1)), blacklist_action TEXT NOT NULL DEFAULT 'unknown' CHECK(blacklist_action IN ('unknown','none','added','appended'))) STRICT;
CREATE INDEX idx_admin_alerts_kind_resolved ON admin_alerts(kind,resolved,id DESC);
CREATE INDEX idx_admin_alerts_kind ON admin_alerts(kind,id DESC);
CREATE TRIGGER discord_blacklist_registration_guard BEFORE INSERT ON users
WHEN NEW.discord_id IS NOT NULL AND EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=NEW.discord_id)
BEGIN SELECT RAISE(ABORT,'discord identity is blocked'); END;
CREATE TRIGGER discord_blacklist_user_guard BEFORE UPDATE OF discord_id,is_banned,banned_until ON users
WHEN EXISTS(SELECT 1 FROM discord_blacklist WHERE discord_id=NEW.discord_id) AND (NEW.is_banned<>1 OR NEW.banned_until IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'blacklisted account must remain permanently banned'); END;
CREATE TRIGGER discord_blacklist_insert_guard BEFORE INSERT ON discord_blacklist
WHEN EXISTS(SELECT 1 FROM users WHERE discord_id=NEW.discord_id AND (is_admin=1 OR is_banned<>1 OR banned_until IS NOT NULL))
BEGIN SELECT RAISE(ABORT,'existing account must be permanently banned first'); END;
CREATE TRIGGER discord_blacklist_identity_guard BEFORE UPDATE OF discord_id ON discord_blacklist
WHEN NEW.discord_id<>OLD.discord_id
BEGIN SELECT RAISE(ABORT,'blacklist identity is immutable'); END;
CREATE TABLE image_discovery_dispatches (
 operation_id TEXT PRIMARY KEY REFERENCES image_model_refreshes(operation_id) ON DELETE CASCADE,
 method TEXT NOT NULL CHECK(method='GET'),
 url TEXT NOT NULL CHECK(length(CAST(url AS BLOB)) BETWEEN 1 AND 8192),
 request_body TEXT NOT NULL DEFAULT '' CHECK(request_body=''),
 request_content_type TEXT NOT NULL DEFAULT '' CHECK(request_content_type=''),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE risk_client_scans (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='scn_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 admin INTEGER NOT NULL CHECK(admin IN (0,1)),
 request_token TEXT NOT NULL CHECK(length(request_token) BETWEEN 16 AND 64 AND request_token NOT GLOB '*[^A-Za-z0-9_-]*'),
 query_json TEXT NOT NULL CHECK(json_valid(query_json) AND length(CAST(query_json AS BLOB))<=4096),
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json) AND json_type(rules_json)='array' AND length(CAST(rules_json AS BLOB))<=16777216),
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','cancelled','limited','failed')),
 reason TEXT NOT NULL DEFAULT '' CHECK(reason IN ('','result_limit','permission_changed','scan_failed','candidate_limit','minute_limit','source_changed','window_limit')),
 from_at INTEGER NOT NULL CHECK(from_at BETWEEN 0 AND 253402300799),
 to_at INTEGER NOT NULL CHECK(to_at>from_at AND to_at-from_at<=2592000),
 call_kind TEXT NOT NULL CHECK(call_kind IN ('total','self','charity','unclassified')),
 model TEXT NOT NULL CHECK(length(CAST(model AS BLOB))<=512),
 upper_log_id INTEGER NOT NULL CHECK(upper_log_id>=0),
 after_at INTEGER NOT NULL CHECK(after_at>=from_at AND after_at<to_at),
 after_log_id INTEGER NOT NULL DEFAULT 0 CHECK(after_log_id BETWEEN 0 AND upper_log_id),
 candidates INTEGER NOT NULL CHECK(candidates>=0),
 scanned INTEGER NOT NULL DEFAULT 0 CHECK(scanned>=0),
 matched INTEGER NOT NULL DEFAULT 0 CHECK(matched BETWEEN 0 AND 100000),
 failures INTEGER NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 3),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402292399),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400), kind TEXT NOT NULL DEFAULT 'client_hits' CHECK(kind IN ('client_hits','users','shared_ips','user_ips')), filter_revision INTEGER NOT NULL DEFAULT 1 CHECK(filter_revision>0), checkpoint_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(checkpoint_json) AND json_type(checkpoint_json)='object' AND length(CAST(checkpoint_json AS BLOB))<=16384), coverage_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(coverage_json) AND json_type(coverage_json)='object' AND length(CAST(coverage_json AS BLOB))<=4096), changed INTEGER NOT NULL DEFAULT 0 CHECK(changed IN (0,1)), upper_source_id INTEGER NOT NULL DEFAULT 0 CHECK(upper_source_id>=0), after_source_id INTEGER NOT NULL DEFAULT 0 CHECK(after_source_id BETWEEN 0 AND upper_source_id),
 UNIQUE(user_id,request_token)
) STRICT;
CREATE INDEX idx_risk_scans_queue ON risk_client_scans(state,created_at,id);
CREATE INDEX idx_risk_scans_expiry ON risk_client_scans(expires_at,id);
CREATE INDEX idx_risk_scans_owner ON risk_client_scans(user_id,created_at DESC,id);
CREATE TABLE risk_client_scan_matches (
 scan_id TEXT NOT NULL REFERENCES risk_client_scans(id) ON DELETE CASCADE,
 request_log_id INTEGER NOT NULL REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 100000),
 PRIMARY KEY(scan_id,request_log_id),
 UNIQUE(scan_id,ordinal)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_matches_log ON risk_client_scan_matches(request_log_id,scan_id);
CREATE TABLE charity_routing_settings (
 model_id INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 affinity_ttl_seconds INTEGER NOT NULL DEFAULT 300 CHECK(affinity_ttl_seconds BETWEEN 1 AND 86400)
) STRICT;
CREATE TABLE charity_routing_capacity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 affinities INTEGER NOT NULL CHECK(affinities BETWEEN 0 AND 200000),
 buckets INTEGER NOT NULL CHECK(buckets BETWEEN 0 AND 1000000),
 revision INTEGER NOT NULL CHECK(revision>0)
) STRICT;
CREATE TABLE charity_key_affinities (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_id INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 attempt_id TEXT NOT NULL CHECK(length(attempt_id)=26 AND substr(attempt_id,1,4)='clm_' AND substr(attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(attempt_id,-1,1) IN ('A','Q','g','w')),
 routing_revision INTEGER NOT NULL CHECK(routing_revision>0),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at>dispatched_at AND expires_at<=dispatched_at+86400),
 PRIMARY KEY(user_id,model_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_charity_affinities_expiry ON charity_key_affinities(expires_at,user_id,model_id);
CREATE INDEX idx_charity_affinities_key ON charity_key_affinities(endpoint_key_id,physical_id);
CREATE TRIGGER charity_affinity_capacity BEFORE INSERT ON charity_key_affinities
WHEN NOT EXISTS(SELECT 1 FROM charity_key_affinities WHERE user_id=NEW.user_id AND model_id=NEW.model_id)
 AND ((SELECT affinities FROM charity_routing_capacity WHERE id=1)>=200000
 OR (SELECT count(*) FROM charity_key_affinities WHERE user_id=NEW.user_id)>=1000)
BEGIN SELECT RAISE(ABORT,'charity association capacity exhausted'); END;
CREATE TRIGGER charity_affinity_count_insert AFTER INSERT ON charity_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities+1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_affinity_count_delete AFTER DELETE ON charity_key_affinities
BEGIN UPDATE charity_routing_capacity SET affinities=affinities-1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_affinity_identity BEFORE UPDATE ON charity_key_affinities
WHEN NEW.user_id<>OLD.user_id OR NEW.model_id<>OLD.model_id
BEGIN SELECT RAISE(ABORT,'charity association identity is immutable'); END;
CREATE TABLE charity_dispatch_buckets (
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 dispatched_at INTEGER NOT NULL CHECK(dispatched_at BETWEEN 0 AND 253402300499),
 dispatch_count INTEGER NOT NULL CHECK(dispatch_count BETWEEN 1 AND 9223372036854775807),
 PRIMARY KEY(physical_id,dispatched_at)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_charity_dispatch_buckets_expiry ON charity_dispatch_buckets(dispatched_at,physical_id);
CREATE TRIGGER charity_bucket_capacity BEFORE INSERT ON charity_dispatch_buckets
WHEN NOT EXISTS(SELECT 1 FROM charity_dispatch_buckets WHERE physical_id=NEW.physical_id AND dispatched_at=NEW.dispatched_at)
 AND (SELECT buckets FROM charity_routing_capacity WHERE id=1)>=1000000
BEGIN SELECT RAISE(ABORT,'charity dispatch capacity exhausted'); END;
CREATE TRIGGER charity_bucket_count_insert AFTER INSERT ON charity_dispatch_buckets
BEGIN UPDATE charity_routing_capacity SET buckets=buckets+1,revision=revision+1 WHERE id=1; END;
CREATE TRIGGER charity_bucket_count_delete AFTER DELETE ON charity_dispatch_buckets
BEGIN UPDATE charity_routing_capacity SET buckets=buckets-1,revision=revision+1 WHERE id=1; END;
CREATE TABLE charity_dispatch_receipts (
 attempt_id TEXT PRIMARY KEY CHECK(length(attempt_id)=26 AND substr(attempt_id,1,4)='clm_' AND substr(attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(attempt_id,-1,1) IN ('A','Q','g','w')),
 physical_id BLOB NOT NULL CHECK(length(physical_id)=32),
 endpoint_key_id INTEGER NOT NULL CHECK(endpoint_key_id>0),
 user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 model_id INTEGER REFERENCES charity_models(id) ON DELETE SET NULL,
 routing_revision INTEGER NOT NULL CHECK(routing_revision>0),
 state TEXT NOT NULL CHECK(state IN ('reserved','dispatched')),
 reserved_at INTEGER NOT NULL CHECK(reserved_at BETWEEN 0 AND 253402300499),
 dispatched_at INTEGER CHECK(dispatched_at BETWEEN reserved_at AND 253402300499),
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN reserved_at AND 253402300799),
 previous_attempt_id TEXT CHECK(previous_attempt_id IS NULL OR (length(previous_attempt_id)=26 AND substr(previous_attempt_id,1,4)='clm_' AND substr(previous_attempt_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(previous_attempt_id,-1,1) IN ('A','Q','g','w'))),
 previous_endpoint_key_id INTEGER CHECK(previous_endpoint_key_id>0),
 previous_physical_id BLOB CHECK(previous_physical_id IS NULL OR length(previous_physical_id)=32),
 previous_routing_revision INTEGER CHECK(previous_routing_revision>0),
 previous_dispatched_at INTEGER CHECK(previous_dispatched_at BETWEEN 0 AND 253402214399),
 previous_expires_at INTEGER CHECK(previous_expires_at>previous_dispatched_at AND previous_expires_at<=previous_dispatched_at+86400),
 CHECK((state='reserved' AND dispatched_at IS NULL) OR (state='dispatched' AND dispatched_at IS NOT NULL AND expires_at>=dispatched_at+300)),
 CHECK((previous_attempt_id IS NULL AND previous_endpoint_key_id IS NULL AND previous_physical_id IS NULL AND previous_routing_revision IS NULL AND previous_dispatched_at IS NULL AND previous_expires_at IS NULL)
 OR (state='dispatched' AND previous_attempt_id IS NOT NULL AND previous_attempt_id<>attempt_id AND previous_endpoint_key_id IS NOT NULL AND previous_physical_id IS NOT NULL AND previous_routing_revision IS NOT NULL AND previous_dispatched_at IS NOT NULL AND previous_expires_at IS NOT NULL AND previous_dispatched_at<=dispatched_at))
) STRICT;
CREATE INDEX idx_charity_dispatch_receipts_expiry ON charity_dispatch_receipts(expires_at,attempt_id);
CREATE INDEX idx_charity_dispatch_receipts_pending ON charity_dispatch_receipts(physical_id,state,reserved_at);
CREATE UNIQUE INDEX idx_charity_dispatch_receipts_previous ON charity_dispatch_receipts(previous_attempt_id) WHERE previous_attempt_id IS NOT NULL;
CREATE TRIGGER charity_dispatch_receipt_identity BEFORE UPDATE ON charity_dispatch_receipts
WHEN NEW.attempt_id<>OLD.attempt_id OR NEW.physical_id<>OLD.physical_id OR NEW.endpoint_key_id<>OLD.endpoint_key_id
 OR NEW.routing_revision<>OLD.routing_revision OR NEW.reserved_at<>OLD.reserved_at
 OR (OLD.state='dispatched' AND (NEW.state<>OLD.state OR NEW.dispatched_at<>OLD.dispatched_at))
 OR (NEW.user_id IS NOT OLD.user_id AND NEW.user_id IS NOT NULL)
 OR (NEW.model_id IS NOT OLD.model_id AND NEW.model_id IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'dispatch receipt is immutable'); END;
CREATE TABLE request_adaptations (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','charity_model','binding')),
 endpoint_id INTEGER UNIQUE REFERENCES endpoints(id) ON DELETE CASCADE,
 model_id INTEGER UNIQUE REFERENCES charity_models(id) ON DELETE CASCADE,
 binding_id INTEGER UNIQUE REFERENCES charity_model_bindings(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 secret_context BLOB NOT NULL CHECK(length(secret_context)=16),
 secret_ciphertext TEXT NOT NULL CHECK(length(CAST(secret_ciphertext AS BLOB)) BETWEEN 1 AND 131072),
 structure_json TEXT NOT NULL CHECK(json_valid(structure_json) AND json_type(structure_json)='object' AND length(CAST(structure_json AS BLOB))<=65536),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799),
 CHECK((scope='endpoint' AND endpoint_id IS NOT NULL AND model_id IS NULL AND binding_id IS NULL)
 OR (scope='charity_model' AND endpoint_id IS NULL AND model_id IS NOT NULL AND binding_id IS NULL)
 OR (scope='binding' AND endpoint_id IS NULL AND model_id IS NULL AND binding_id IS NOT NULL))
) STRICT;
CREATE TRIGGER request_adaptation_identity BEFORE UPDATE ON request_adaptations
WHEN NEW.id<>OLD.id OR NEW.scope<>OLD.scope OR NEW.endpoint_id IS NOT OLD.endpoint_id
 OR NEW.model_id IS NOT OLD.model_id OR NEW.binding_id IS NOT OLD.binding_id
 OR NEW.secret_context<>OLD.secret_context OR NEW.revision<OLD.revision
 OR (NEW.revision=OLD.revision AND (NEW.secret_ciphertext<>OLD.secret_ciphertext OR NEW.structure_json<>OLD.structure_json OR NEW.updated_at<>OLD.updated_at))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation revision'); END;
CREATE TABLE request_adaptation_audits (
 id INTEGER PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('endpoint','charity_model','binding')),
 resource_id INTEGER NOT NULL CHECK(resource_id>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 actor_role TEXT NOT NULL CHECK(actor_role IN ('owner','admin')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 changed_partitions TEXT NOT NULL CHECK(json_valid(changed_partitions) AND json_type(changed_partitions)='array' AND json_array_length(changed_partitions)<=5 AND length(CAST(changed_partitions AS BLOB))<=256),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 UNIQUE(scope,resource_id,revision),
 CHECK((scope='endpoint')=(actor_role='owner'))
) STRICT;
CREATE INDEX idx_request_adaptation_audits_expiry ON request_adaptation_audits(created_at,id);
CREATE INDEX idx_request_adaptation_audits_actor ON request_adaptation_audits(actor_user_id,id);
CREATE TRIGGER request_adaptation_audit_partitions BEFORE INSERT ON request_adaptation_audits
WHEN EXISTS(SELECT 1 FROM json_each(NEW.changed_partitions) WHERE type<>'text' OR value NOT IN ('forward_headers','fixed_headers','body_defaults','body_forced','native_extension_paths'))
 OR json_array_length(NEW.changed_partitions)<>(SELECT count(DISTINCT value) FROM json_each(NEW.changed_partitions))
BEGIN SELECT RAISE(ABORT,'invalid request adaptation audit partitions'); END;
CREATE TRIGGER request_adaptation_audit_immutable BEFORE UPDATE ON request_adaptation_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND NEW.id=OLD.id AND NEW.scope=OLD.scope AND NEW.resource_id=OLD.resource_id
 AND NEW.actor_role=OLD.actor_role AND NEW.revision=OLD.revision
 AND NEW.changed_partitions=OLD.changed_partitions AND NEW.created_at=OLD.created_at)
BEGIN SELECT RAISE(ABORT,'request adaptation audit is immutable'); END;
CREATE TABLE client_rule_auto_bans (
 rule_id TEXT PRIMARY KEY REFERENCES risk_client_rules(id) ON DELETE RESTRICT,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 duration_seconds INTEGER CHECK(duration_seconds BETWEEN 1 AND 315360000),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER client_rule_auto_ban_capacity BEFORE INSERT ON client_rule_auto_bans
WHEN NOT EXISTS(SELECT 1 FROM client_rule_auto_bans WHERE rule_id=NEW.rule_id)
 AND (SELECT count(*) FROM client_rule_auto_bans)>=100
BEGIN SELECT RAISE(ABORT,'client rule action capacity exhausted'); END;
CREATE TABLE client_rule_ban_receipts (
 request_id TEXT PRIMARY KEY CHECK(length(request_id)=26 AND substr(request_id,1,4)='req_' AND substr(request_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(request_id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 rules_json TEXT NOT NULL CHECK(json_valid(rules_json) AND json_type(rules_json)='array' AND json_array_length(rules_json) BETWEEN 1 AND 100 AND length(CAST(rules_json AS BLOB))<=16384),
 banned_until INTEGER CHECK(banned_until BETWEEN created_at AND 253402300799),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253394524799),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+7776000)
) STRICT;
CREATE INDEX idx_client_rule_ban_receipts_expiry ON client_rule_ban_receipts(expires_at,request_id);
CREATE TABLE discord_blacklist_origins (
 discord_id TEXT PRIMARY KEY REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 first_actor_kind TEXT NOT NULL CHECK(first_actor_kind IN ('admin','steward6','automatic','unknown')),
 first_actor_user_id INTEGER CHECK(first_actor_user_id>0)
) STRICT;
CREATE INDEX idx_blacklist_origins_actor ON discord_blacklist_origins(first_actor_kind,first_actor_user_id,discord_id);
CREATE TRIGGER blacklist_origin_immutable BEFORE UPDATE ON discord_blacklist_origins
BEGIN SELECT RAISE(ABORT,'blacklist origin is immutable'); END;
CREATE TRIGGER blacklist_reason_immutable BEFORE UPDATE OF reason,created_at ON discord_blacklist
WHEN NEW.reason<>OLD.reason OR NEW.created_at<>OLD.created_at
BEGIN SELECT RAISE(ABORT,'blacklist first event is immutable'); END;
CREATE TABLE discord_blacklist_events (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL REFERENCES discord_blacklist(discord_id) ON DELETE CASCADE,
 operation_key TEXT NOT NULL UNIQUE CHECK(length(CAST(operation_key AS BLOB)) BETWEEN 1 AND 128),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('admin','steward6','automatic','unknown')),
 actor_user_id INTEGER CHECK(actor_user_id>0),
 reason_codes_json TEXT NOT NULL CHECK(json_valid(reason_codes_json) AND json_type(reason_codes_json)='array' AND json_array_length(reason_codes_json)<=8 AND length(CAST(reason_codes_json AS BLOB))<=1024),
 safe_note TEXT NOT NULL CHECK(length(safe_note)<=2000 AND instr(safe_note,char(0))=0 AND instr(safe_note,char(13))=0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_blacklist_events_identity ON discord_blacklist_events(discord_id,created_at,id);
CREATE TRIGGER blacklist_event_immutable BEFORE UPDATE ON discord_blacklist_events
BEGIN SELECT RAISE(ABORT,'blacklist event is immutable'); END;
CREATE TABLE user_continuity_identities (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32)
) STRICT;
CREATE INDEX idx_user_continuity_key ON user_continuity_identities(identity_key,user_id);
CREATE TABLE identity_continuity_facts (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 kind TEXT NOT NULL CHECK(kind IN ('checkin_general','checkin_game','welfare','game_onboarding','abuse_state')),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128),
 window_key TEXT NOT NULL CHECK(length(CAST(window_key AS BLOB)) BETWEEN 1 AND 128),
 fact_json TEXT NOT NULL CHECK(json_valid(fact_json) AND json_type(fact_json)='object' AND length(CAST(fact_json AS BLOB))<=16384),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER CHECK(expires_at>occurred_at AND expires_at<=253402300799),
 PRIMARY KEY(identity_key,kind,scope,window_key),
 CHECK((kind='game_onboarding')=(expires_at IS NULL))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_identity_facts_expiry ON identity_continuity_facts(expires_at,identity_key,kind,scope,window_key) WHERE expires_at IS NOT NULL;
CREATE TABLE identity_window_events (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 kind TEXT NOT NULL CHECK(kind IN ('user_rpm','report_frequency','game_start','abuse_rpm','abuse_short_content')),
 scope TEXT NOT NULL CHECK(length(CAST(scope AS BLOB)) BETWEEN 1 AND 128),
 event_key TEXT NOT NULL CHECK(length(CAST(event_key AS BLOB)) BETWEEN 1 AND 128),
 occurred_at_ms INTEGER NOT NULL CHECK(occurred_at_ms BETWEEN 0 AND 253402300799000),
 expires_at_ms INTEGER NOT NULL CHECK(expires_at_ms>occurred_at_ms AND expires_at_ms<=253402300799000),
 count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 9223372036854775807),
 value INTEGER CHECK(value>=0),
 PRIMARY KEY(identity_key,kind,scope,event_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_identity_windows_expiry ON identity_window_events(expires_at_ms,identity_key,kind,scope,event_key);
CREATE INDEX idx_identity_windows_current ON identity_window_events(identity_key,kind,scope,occurred_at_ms);
CREATE UNIQUE INDEX idx_deleted_accounts_former_user ON admin_account_deletions(former_user_id) WHERE former_user_id IS NOT NULL;
CREATE INDEX idx_deleted_accounts_discord ON admin_account_deletions(discord_id,deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_time ON admin_account_deletions(deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_level ON admin_account_deletions(effective_level,deleted_at DESC,former_user_id);
CREATE INDEX idx_deleted_accounts_ban ON admin_account_deletions(ban_active,deleted_at DESC,former_user_id);
CREATE TRIGGER deleted_account_projection_permanent BEFORE DELETE ON admin_account_deletions
BEGIN SELECT RAISE(ABORT,'deleted account projection has no automatic expiry'); END;
CREATE TABLE self_deletion_duel_aborts (
 id INTEGER PRIMARY KEY,
 discord_id TEXT NOT NULL CHECK(length(CAST(discord_id AS BLOB)) BETWEEN 1 AND 128),
 game_key TEXT NOT NULL CHECK(game_key IN ('bidding','likes','gwent')),
 match_id TEXT NOT NULL CHECK(length(CAST(match_id AS BLOB)) BETWEEN 1 AND 128),
 former_user_id INTEGER NOT NULL CHECK(former_user_id>0),
 reason TEXT NOT NULL CHECK(reason='self_deletion_cancelled_match'),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253394524799),
 expires_at INTEGER NOT NULL CHECK(expires_at=occurred_at+7776000),
 UNIQUE(game_key,match_id,former_user_id)
) STRICT;
CREATE INDEX idx_self_deletion_duel_aborts_discord ON self_deletion_duel_aborts(discord_id,occurred_at,id);
CREATE INDEX idx_self_deletion_duel_aborts_expiry ON self_deletion_duel_aborts(expires_at,id);
CREATE TABLE image_capability_profiles (
 control_id TEXT PRIMARY KEY REFERENCES image_upstream_control(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 profile_json TEXT NOT NULL CHECK(json_valid(profile_json) AND json_type(profile_json)='object' AND length(CAST(profile_json AS BLOB))<=262144),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE image_capability_snapshots (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ics_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 control_id TEXT NOT NULL REFERENCES image_upstream_control(id) ON DELETE CASCADE,
 upstream_revision INTEGER NOT NULL REFERENCES image_upstream_revisions(revision),
 profile_revision INTEGER NOT NULL CHECK(profile_revision>=0),
 candidate_hash BLOB NOT NULL CHECK(length(candidate_hash)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400)
) STRICT;
CREATE INDEX idx_image_capability_snapshots_latest ON image_capability_snapshots(control_id,created_at DESC,id);
CREATE INDEX idx_image_capability_snapshots_expiry ON image_capability_snapshots(expires_at,id);
CREATE TRIGGER image_capability_snapshot_capacity BEFORE INSERT ON image_capability_snapshots
WHEN (SELECT count(*) FROM image_capability_snapshots WHERE control_id=NEW.control_id)>=2
BEGIN SELECT RAISE(ABORT,'image capability snapshot capacity exhausted'); END;
CREATE TABLE image_capability_candidates (
 snapshot_id TEXT NOT NULL REFERENCES image_capability_snapshots(id) ON DELETE CASCADE,
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND json_type(source_json)='object' AND length(CAST(source_json AS BLOB))<=262144),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND json_type(metadata_json)='object' AND length(CAST(metadata_json AS BLOB))<=32768),
 candidate_hash BLOB NOT NULL CHECK(length(candidate_hash)=32),
 PRIMARY KEY(snapshot_id,model_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE image_model_capability_revisions (
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 schema_version INTEGER NOT NULL CHECK(schema_version IN (1,2)),
 readiness TEXT NOT NULL CHECK(readiness IN ('legacy','pending','ready')),
 source_json TEXT NOT NULL CHECK(json_valid(source_json) AND json_type(source_json)='object' AND length(CAST(source_json AS BLOB))<=262144),
 manual_json TEXT NOT NULL CHECK(json_valid(manual_json) AND json_type(manual_json)='object' AND length(CAST(manual_json AS BLOB))<=262144),
 effective_json TEXT NOT NULL CHECK(json_valid(effective_json) AND json_type(effective_json)='object' AND length(CAST(effective_json AS BLOB))<=262144),
 candidate_hash BLOB CHECK(candidate_hash IS NULL OR length(candidate_hash)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(model_id,revision),
 CHECK((schema_version=1)=(readiness='legacy'))
) STRICT;
CREATE TRIGGER image_model_capability_immutable BEFORE UPDATE ON image_model_capability_revisions
BEGIN SELECT RAISE(ABORT,'image capability revision is immutable'); END;
CREATE TABLE image_model_pricing_revisions (
 model_id TEXT NOT NULL REFERENCES image_activity_models(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0),
 default_paper_mag BLOB NOT NULL CHECK(length(default_paper_mag)=16 AND default_paper_mag<X'80000000000000000000000000000000'),
 default_brush_mag BLOB NOT NULL CHECK(length(default_brush_mag)=16 AND default_brush_mag<X'80000000000000000000000000000000'),
 fallback TEXT NOT NULL CHECK(fallback IN ('default','unavailable')),
 tiers_json TEXT NOT NULL CHECK(json_valid(tiers_json) AND json_type(tiers_json)='array' AND json_array_length(tiers_json)<=64 AND length(CAST(tiers_json AS BLOB))<=16384),
 sizes_json TEXT NOT NULL CHECK(json_valid(sizes_json) AND json_type(sizes_json)='array' AND json_array_length(sizes_json)<=2048 AND length(CAST(sizes_json AS BLOB))<=262144),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(model_id,revision),
 CHECK(default_paper_mag<>zeroblob(16) OR default_brush_mag<>zeroblob(16))
) STRICT;
CREATE TRIGGER image_model_pricing_immutable BEFORE UPDATE ON image_model_pricing_revisions
BEGIN SELECT RAISE(ABORT,'image pricing revision is immutable'); END;
CREATE TABLE image_model_revision_policies (
 model_id TEXT NOT NULL,
 model_revision INTEGER NOT NULL,
 capability_revision INTEGER NOT NULL,
 pricing_revision INTEGER NOT NULL,
 PRIMARY KEY(model_id,model_revision),
 FOREIGN KEY(model_id,model_revision) REFERENCES image_model_revisions(model_id,revision) ON DELETE CASCADE,
 FOREIGN KEY(model_id,capability_revision) REFERENCES image_model_capability_revisions(model_id,revision),
 FOREIGN KEY(model_id,pricing_revision) REFERENCES image_model_pricing_revisions(model_id,revision)
) STRICT;
CREATE TRIGGER image_model_policy_immutable BEFORE UPDATE ON image_model_revision_policies
BEGIN SELECT RAISE(ABORT,'image revision policy is immutable'); END;
CREATE TABLE image_task_price_receipts (
 task_id TEXT PRIMARY KEY REFERENCES image_activity_tasks(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 model_revision INTEGER NOT NULL CHECK(model_revision>0),
 pricing_revision INTEGER NOT NULL CHECK(pricing_revision>0),
 n INTEGER NOT NULL CHECK(n BETWEEN 1 AND 16),
 unit_paper_mag BLOB NOT NULL CHECK(length(unit_paper_mag)=16 AND unit_paper_mag<X'80000000000000000000000000000000'),
 unit_brush_mag BLOB NOT NULL CHECK(length(unit_brush_mag)=16 AND unit_brush_mag<X'80000000000000000000000000000000'),
 total_paper_mag BLOB NOT NULL CHECK(length(total_paper_mag)=16 AND total_paper_mag<X'80000000000000000000000000000000'),
 total_brush_mag BLOB NOT NULL CHECK(length(total_brush_mag)=16 AND total_brush_mag<X'80000000000000000000000000000000'),
 basis TEXT NOT NULL CHECK(basis IN ('legacy','default','tier','size','auto')),
 price_key TEXT NOT NULL CHECK(length(CAST(price_key AS BLOB))<=128),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER image_task_price_receipt_immutable BEFORE UPDATE ON image_task_price_receipts
BEGIN SELECT RAISE(ABORT,'image accepted price is immutable'); END;
CREATE TRIGGER image_model_pricing_revisions_whole_insert BEFORE INSERT ON image_model_pricing_revisions WHEN NOT (((instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.default_paper_mag),32,1))-1)*1)%1000=0) OR NOT (((instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.default_brush_mag),32,1))-1)*1)%1000=0) BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE TRIGGER image_task_price_receipts_whole_insert BEFORE INSERT ON image_task_price_receipts WHEN NOT (((instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.unit_paper_mag),32,1))-1)*1)%1000=0) OR NOT (((instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.unit_brush_mag),32,1))-1)*1)%1000=0) OR NOT (((instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.total_paper_mag),32,1))-1)*1)%1000=0) OR NOT (((instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),1,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),2,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),3,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),4,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),5,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),6,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),7,1))-1)*376+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),8,1))-1)*336+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),9,1))-1)*896+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),10,1))-1)*56+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),11,1))-1)*816+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),12,1))-1)*176+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),13,1))-1)*136+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),14,1))-1)*696+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),15,1))-1)*856+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),16,1))-1)*616+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),17,1))-1)*976+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),18,1))-1)*936+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),19,1))-1)*496+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),20,1))-1)*656+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),21,1))-1)*416+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),22,1))-1)*776+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),23,1))-1)*736+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),24,1))-1)*296+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),25,1))-1)*456+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),26,1))-1)*216+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),27,1))-1)*576+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),28,1))-1)*536+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),29,1))-1)*96+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),30,1))-1)*256+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),31,1))-1)*16+(instr('0123456789ABCDEF',substr(hex(NEW.total_brush_mag),32,1))-1)*1)%1000=0) BEGIN SELECT RAISE(ABORT,'image price is not an integer'); END;
CREATE INDEX idx_audit_access_time_page ON audit_access_events(occurred_at DESC,id DESC);
CREATE INDEX idx_risk_scans_unfinished ON risk_client_scans(user_id,state) WHERE state IN ('queued','running');
CREATE TRIGGER risk_scan_capacity BEFORE INSERT ON risk_client_scans
WHEN (SELECT count(*) FROM risk_client_scans)>=200
 OR (NEW.state IN ('queued','running') AND ((SELECT count(*) FROM risk_client_scans WHERE state IN ('queued','running'))>=20
 OR (SELECT count(*) FROM risk_client_scans WHERE user_id=NEW.user_id AND state IN ('queued','running'))>=2))
BEGIN SELECT RAISE(ABORT,'audit scan capacity exhausted'); END;
CREATE TRIGGER risk_scan_capacity_update BEFORE UPDATE OF state,user_id ON risk_client_scans
WHEN NEW.state IN ('queued','running') AND (OLD.state NOT IN ('queued','running') OR NEW.user_id<>OLD.user_id)
 AND ((SELECT count(*) FROM risk_client_scans WHERE state IN ('queued','running') AND id<>OLD.id)>=20
 OR (SELECT count(*) FROM risk_client_scans WHERE user_id=NEW.user_id AND state IN ('queued','running') AND id<>OLD.id)>=2)
BEGIN SELECT RAISE(ABORT,'audit scan capacity exhausted'); END;
CREATE TABLE risk_scan_results (
 scan_id TEXT NOT NULL REFERENCES risk_client_scans(id) ON DELETE CASCADE,
 row_no INTEGER NOT NULL CHECK(row_no BETWEEN 1 AND 100000),
 user_id INTEGER CHECK(user_id IS NULL OR user_id>0),
 request_log_id INTEGER REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 published INTEGER NOT NULL DEFAULT 0 CHECK(published IN (0,1)),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=16384),
 PRIMARY KEY(scan_id,row_no),
 CHECK(user_id IS NOT NULL OR request_log_id IS NOT NULL)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_results_user ON risk_scan_results(user_id,scan_id,row_no);
CREATE INDEX idx_risk_scan_results_source ON risk_scan_results(request_log_id,scan_id,row_no);
CREATE TABLE risk_scan_result_users (
 scan_id TEXT NOT NULL,
 row_no INTEGER NOT NULL,
 user_id INTEGER NOT NULL CHECK(user_id>0),
 PRIMARY KEY(scan_id,row_no,user_id),
 FOREIGN KEY(scan_id,row_no) REFERENCES risk_scan_results(scan_id,row_no) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_result_users_source ON risk_scan_result_users(user_id,scan_id,row_no);
CREATE TABLE risk_scan_result_sources (
 scan_id TEXT NOT NULL,
 row_no INTEGER NOT NULL,
 request_log_id INTEGER NOT NULL REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 PRIMARY KEY(scan_id,row_no,request_log_id),
 FOREIGN KEY(scan_id,row_no) REFERENCES risk_scan_results(scan_id,row_no) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_result_sources_source ON risk_scan_result_sources(request_log_id,scan_id,row_no);
CREATE TRIGGER risk_scan_authority_changed AFTER UPDATE OF is_admin,level,auto_level,is_banned,banned_until ON users
BEGIN
 UPDATE risk_client_scans SET state='cancelled',reason='permission_changed',
 checkpoint_json=json_remove(checkpoint_json,'$.pending_user','$.after_user','$.pending_ip','$.after_ip','$.source_at','$.source_id','$.ip_summary')
 WHERE user_id=NEW.id
 AND ((admin=1 AND NEW.is_admin<>1) OR (admin=0 AND (NEW.is_admin<>0 OR COALESCE(NEW.level,NEW.auto_level)<>6))
 OR (NEW.is_banned=1 AND (NEW.banned_until IS NULL OR NEW.banned_until>unixepoch())));
END;
CREATE TABLE game_bidding_net_rebuild (
 id INTEGER PRIMARY KEY CHECK(id=1),
 state TEXT NOT NULL CHECK(state IN ('pending','scanning','publishing','completed')),
 watermark BLOB CHECK(watermark IS NULL OR length(watermark)=16),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16),
 history_coverage_start INTEGER CHECK(history_coverage_start BETWEEN 0 AND 253402300799),
 missing_events INTEGER NOT NULL CHECK(missing_events>=0),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TABLE game_bidding_net_rebuild_events (
 source_seq BLOB NOT NULL PRIMARY KEY REFERENCES game_rank_events(seq) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 loss_sign INTEGER NOT NULL CHECK(loss_sign IN (-1,0,1)),
 loss_mag BLOB NOT NULL CHECK(length(loss_mag)=16 AND (loss_sign=0)=(loss_mag=zeroblob(16))),
 settled_at INTEGER NOT NULL CHECK(settled_at BETWEEN 0 AND 253401695999)
) STRICT;
CREATE INDEX idx_bidding_rebuild_events_user ON game_bidding_net_rebuild_events(user_id,source_seq);
CREATE TABLE game_bidding_net_rebuild_totals (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=32 AND (amount_sign=0)=(amount_mag=zeroblob(32))),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_phase INTEGER NOT NULL CHECK(achieved_phase IN (0,1)),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16)
) STRICT;
CREATE TABLE fatfish_levels (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffl_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=4096),
 draft_json TEXT NOT NULL CHECK(json_valid(draft_json) AND json_type(draft_json)='object' AND length(CAST(draft_json AS BLOB))<=262144),
 revision INTEGER NOT NULL CHECK(revision>0),
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE TABLE fatfish_level_versions (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffv_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 level_id TEXT NOT NULL REFERENCES fatfish_levels(id),
 content_hash BLOB NOT NULL CHECK(length(content_hash)=32),
 engine_version INTEGER NOT NULL CHECK(engine_version IN (1,2,3)),
 scoring_version INTEGER NOT NULL CHECK(scoring_version=1),
 content_json TEXT NOT NULL CHECK(json_valid(content_json) AND json_type(content_json)='object' AND length(CAST(content_json AS BLOB))<=262144),
 duration_seconds INTEGER NOT NULL CHECK(duration_seconds BETWEEN 10 AND 600),
 maximum_stars INTEGER NOT NULL CHECK(maximum_stars BETWEEN 1 AND 3),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799), version_number INTEGER NOT NULL DEFAULT 1 CHECK(version_number>=1),
 UNIQUE(level_id,content_hash,engine_version,scoring_version)
) STRICT;
CREATE TABLE fatfish_periods (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffp_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=8192),
 state TEXT NOT NULL CHECK(state IN ('draft','open','closed')),
 visible INTEGER NOT NULL DEFAULT 0 CHECK(visible IN (0,1)),
 paused INTEGER NOT NULL DEFAULT 0 CHECK(paused IN (0,1)),
 past_public INTEGER NOT NULL DEFAULT 0 CHECK(past_public IN (0,1)),
 starts_at INTEGER NOT NULL CHECK(starts_at BETWEEN 0 AND 253402300798),
 ends_at INTEGER NOT NULL CHECK(ends_at>starts_at AND ends_at<=253402300799),
 revision INTEGER NOT NULL CHECK(revision>0),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE UNIQUE INDEX idx_fatfish_accepting_period ON fatfish_periods(state) WHERE state='open';
CREATE INDEX idx_fatfish_period_times ON fatfish_periods(state,starts_at,ends_at,id);
CREATE TABLE fatfish_nodes (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffn_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 period_id TEXT NOT NULL REFERENCES fatfish_periods(id),
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 128),
 description TEXT NOT NULL CHECK(length(CAST(description AS BLOB))<=4096),
 map_x INTEGER NOT NULL CHECK(map_x BETWEEN -1000000 AND 1000000),
 map_y INTEGER NOT NULL CHECK(map_y BETWEEN -1000000 AND 1000000),
 ord INTEGER NOT NULL CHECK(ord BETWEEN 0 AND 127),
 current_revision INTEGER NOT NULL CHECK(current_revision>0),
 UNIQUE(period_id,id),
 FOREIGN KEY(id,current_revision) REFERENCES fatfish_node_revisions(node_id,revision) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX idx_fatfish_nodes_period ON fatfish_nodes(period_id,ord,id);
CREATE TRIGGER fatfish_node_capacity BEFORE INSERT ON fatfish_nodes
WHEN (SELECT count(*) FROM fatfish_nodes WHERE period_id=NEW.period_id)>=128
BEGIN SELECT RAISE(ABORT,'period node capacity exhausted'); END;
CREATE TRIGGER fatfish_node_identity BEFORE UPDATE OF id,period_id ON fatfish_nodes
WHEN NEW.id<>OLD.id OR NEW.period_id<>OLD.period_id
BEGIN SELECT RAISE(ABORT,'period node identity is immutable'); END;
CREATE TABLE fatfish_node_revisions (
 node_id TEXT NOT NULL REFERENCES fatfish_nodes(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 condition_json TEXT NOT NULL CHECK(json_valid(condition_json) AND json_type(condition_json)='object' AND length(CAST(condition_json AS BLOB))<=32768),
 hidden_until_eligible INTEGER NOT NULL CHECK(hidden_until_eligible IN (0,1)),
 unlock_cost_mag BLOB NOT NULL CHECK(length(unlock_cost_mag)=16),
 ticket_price_mag BLOB NOT NULL CHECK(length(ticket_price_mag)=16),
 first_clear_reward_mag BLOB NOT NULL CHECK(length(first_clear_reward_mag)=16),
 star1_reward_mag BLOB NOT NULL CHECK(length(star1_reward_mag)=16),
 star2_reward_mag BLOB NOT NULL CHECK(length(star2_reward_mag)=16),
 star3_reward_mag BLOB NOT NULL CHECK(length(star3_reward_mag)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(node_id,revision)
) STRICT;
CREATE TRIGGER fatfish_node_revision_immutable BEFORE UPDATE ON fatfish_node_revisions
BEGIN SELECT RAISE(ABORT,'node configuration revision is immutable'); END;
CREATE TABLE fatfish_capacity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 active_challenges INTEGER NOT NULL CHECK(active_challenges BETWEEN 0 AND 10000),
 summary_rows INTEGER NOT NULL CHECK(summary_rows BETWEEN 0 AND 1000000)
) STRICT;
CREATE TABLE fatfish_challenges (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='ffc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 playtest INTEGER NOT NULL CHECK(playtest IN (0,1)),
 period_id TEXT,
 node_id TEXT,
 node_revision INTEGER,
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 tab_capability_hash BLOB NOT NULL CHECK(length(tab_capability_hash)=32),
 seed BLOB NOT NULL CHECK(length(seed)=32),
 seed_commit BLOB NOT NULL CHECK(length(seed_commit)=32),
 state TEXT NOT NULL CHECK(state IN ('prepared','active','verifying','settled_pass','settled_fail','abandoned','expired','cancelled_refunded')),
 prepared_at_ms INTEGER NOT NULL CHECK(prepared_at_ms BETWEEN 0 AND 253402298300000),
 prepare_until_ms INTEGER NOT NULL CHECK(prepare_until_ms=prepared_at_ms+60000),
 start_at_ms INTEGER,
 end_at_ms INTEGER,
 submit_until_ms INTEGER,
 ticket_price_mag BLOB NOT NULL CHECK(length(ticket_price_mag)=16),
 ticket_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 input_digest BLOB CHECK(input_digest IS NULL OR length(input_digest)=32),
 received_at_ms INTEGER,
 verified_result_json TEXT CHECK(verified_result_json IS NULL OR (json_valid(verified_result_json) AND json_type(verified_result_json)='object' AND length(CAST(verified_result_json AS BLOB))<=16384)),
 terminal_at_ms INTEGER CHECK(terminal_at_ms BETWEEN prepared_at_ms AND 253402300799000),
 terminal_reason TEXT NOT NULL DEFAULT '' CHECK(length(CAST(terminal_reason AS BLOB))<=128),
 refund_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 FOREIGN KEY(period_id,node_id) REFERENCES fatfish_nodes(period_id,id),
 FOREIGN KEY(node_id,node_revision) REFERENCES fatfish_node_revisions(node_id,revision),
 CHECK((playtest=1 AND period_id IS NULL AND node_id IS NULL AND node_revision IS NULL AND ticket_price_mag=zeroblob(16) AND ticket_operation_id IS NULL AND refund_operation_id IS NULL)
 OR (playtest=0 AND period_id IS NOT NULL AND node_id IS NOT NULL AND node_revision IS NOT NULL)),
 CHECK((start_at_ms IS NULL AND end_at_ms IS NULL AND submit_until_ms IS NULL)
 OR (start_at_ms IS NOT NULL AND end_at_ms IS NOT NULL AND submit_until_ms IS NOT NULL
 AND start_at_ms>=prepared_at_ms+3000 AND start_at_ms<=prepare_until_ms+3000
 AND end_at_ms BETWEEN start_at_ms+10000 AND start_at_ms+600000
 AND submit_until_ms=end_at_ms+1800000)),
 CHECK((input_digest IS NULL)=(received_at_ms IS NULL)),
 CHECK(input_digest IS NULL OR start_at_ms IS NOT NULL),
 CHECK(verified_result_json IS NULL OR input_digest IS NOT NULL),
 CHECK(received_at_ms IS NULL OR (received_at_ms>=start_at_ms-3000 AND received_at_ms<=submit_until_ms)),
 CHECK(state<>'prepared' OR (start_at_ms IS NULL AND input_digest IS NULL AND terminal_at_ms IS NULL)),
 CHECK(state NOT IN ('active','verifying','settled_pass','settled_fail') OR start_at_ms IS NOT NULL),
 CHECK(state<>'active' OR (input_digest IS NULL AND terminal_at_ms IS NULL)),
 CHECK(state<>'verifying' OR (input_digest IS NOT NULL AND terminal_at_ms IS NULL)),
 CHECK((state IN ('prepared','active','verifying'))=(terminal_at_ms IS NULL)),
 CHECK(state NOT IN ('settled_pass','settled_fail') OR (input_digest IS NOT NULL AND verified_result_json IS NOT NULL)),
 CHECK(start_at_ms IS NOT NULL OR ticket_operation_id IS NULL),
 CHECK(start_at_ms IS NULL OR (ticket_price_mag=zeroblob(16))=(ticket_operation_id IS NULL)),
 CHECK(refund_operation_id IS NULL OR state='cancelled_refunded'),
 CHECK(state<>'cancelled_refunded' OR (start_at_ms IS NOT NULL AND (ticket_price_mag=zeroblob(16))=(refund_operation_id IS NULL)))
) STRICT;
CREATE UNIQUE INDEX idx_fatfish_user_active ON fatfish_challenges(user_id) WHERE state IN ('prepared','active','verifying');
CREATE INDEX idx_fatfish_challenge_deadline ON fatfish_challenges(state,prepare_until_ms,submit_until_ms,id);
CREATE INDEX idx_fatfish_challenge_retention ON fatfish_challenges(terminal_at_ms,id) WHERE terminal_at_ms IS NOT NULL;
CREATE INDEX idx_fatfish_challenge_history ON fatfish_challenges(user_id,prepared_at_ms DESC,id);
CREATE INDEX idx_fatfish_challenge_node ON fatfish_challenges(node_id,state,id);
CREATE TRIGGER fatfish_challenge_capacity BEFORE INSERT ON fatfish_challenges
WHEN (SELECT summary_rows FROM fatfish_capacity WHERE id=1)>=1000000
 OR (NEW.state IN ('prepared','active','verifying') AND (SELECT active_challenges FROM fatfish_capacity WHERE id=1)>=10000)
BEGIN SELECT RAISE(ABORT,'game challenge capacity exhausted'); END;
CREATE TRIGGER fatfish_challenge_count_insert AFTER INSERT ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET summary_rows=summary_rows+1,active_challenges=active_challenges+(NEW.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_count_delete AFTER DELETE ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET summary_rows=summary_rows-1,active_challenges=active_challenges-(OLD.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_count_update AFTER UPDATE OF state ON fatfish_challenges
BEGIN UPDATE fatfish_capacity SET active_challenges=active_challenges+(NEW.state IN ('prepared','active','verifying'))-(OLD.state IN ('prepared','active','verifying')) WHERE id=1; END;
CREATE TRIGGER fatfish_challenge_transition BEFORE UPDATE ON fatfish_challenges
WHEN NEW.id<>OLD.id OR NEW.user_id<>OLD.user_id OR NEW.playtest<>OLD.playtest
 OR NEW.period_id IS NOT OLD.period_id OR NEW.node_id IS NOT OLD.node_id OR NEW.node_revision IS NOT OLD.node_revision
 OR NEW.version_id<>OLD.version_id OR NEW.tab_capability_hash<>OLD.tab_capability_hash OR NEW.seed<>OLD.seed OR NEW.seed_commit<>OLD.seed_commit
 OR NEW.prepared_at_ms<>OLD.prepared_at_ms OR NEW.prepare_until_ms<>OLD.prepare_until_ms OR NEW.ticket_price_mag<>OLD.ticket_price_mag
 OR (OLD.start_at_ms IS NOT NULL AND (NEW.start_at_ms IS NOT OLD.start_at_ms OR NEW.end_at_ms IS NOT OLD.end_at_ms OR NEW.submit_until_ms IS NOT OLD.submit_until_ms OR NEW.ticket_operation_id IS NOT OLD.ticket_operation_id))
 OR (OLD.input_digest IS NOT NULL AND (NEW.input_digest IS NOT OLD.input_digest OR NEW.received_at_ms IS NOT OLD.received_at_ms))
 OR (OLD.verified_result_json IS NOT NULL AND NEW.verified_result_json IS NOT OLD.verified_result_json)
 OR NEW.revision<=OLD.revision
 OR NOT ((OLD.state='prepared' AND NEW.state IN ('prepared','active','abandoned','expired'))
 OR (OLD.state='active' AND NEW.state IN ('active','verifying','abandoned','expired','cancelled_refunded'))
 OR (OLD.state='verifying' AND NEW.state IN ('verifying','settled_pass','settled_fail','abandoned','cancelled_refunded')))
BEGIN SELECT RAISE(ABORT,'invalid game challenge transition'); END;
CREATE TABLE fatfish_progress (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT NOT NULL,
 node_id TEXT NOT NULL,
 unlock_operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 unlocked_at INTEGER NOT NULL CHECK(unlocked_at BETWEEN 0 AND 253402300799),
 passed INTEGER NOT NULL DEFAULT 0 CHECK(passed IN (0,1)),
 best_stars INTEGER NOT NULL DEFAULT 0 CHECK(best_stars BETWEEN 0 AND 3),
 best_score_units INTEGER NOT NULL DEFAULT 0 CHECK(best_score_units BETWEEN 0 AND 100000000),
 best_version_id TEXT REFERENCES fatfish_level_versions(id),
 best_at_ms INTEGER CHECK(best_at_ms BETWEEN 0 AND 253402300799000),
 best_challenge_id TEXT CHECK(length(best_challenge_id)=26 AND substr(best_challenge_id,1,4)='ffc_'),
 best_confirmed_at_ms INTEGER CHECK(best_confirmed_at_ms BETWEEN 0 AND 253402300799000),
 PRIMARY KEY(user_id,period_id,node_id),
 FOREIGN KEY(period_id,node_id) REFERENCES fatfish_nodes(period_id,id),
 CHECK((passed=0 AND best_stars=0 AND best_score_units=0 AND best_version_id IS NULL AND best_at_ms IS NULL AND best_challenge_id IS NULL AND best_confirmed_at_ms IS NULL)
 OR (passed=1 AND best_stars BETWEEN 1 AND 3 AND best_score_units>0 AND best_version_id IS NOT NULL AND best_at_ms IS NOT NULL AND best_challenge_id IS NOT NULL AND best_confirmed_at_ms>=best_at_ms))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fatfish_progress_board ON fatfish_progress(period_id,node_id,best_score_units DESC,best_at_ms,user_id);
CREATE TRIGGER fatfish_progress_monotone BEFORE UPDATE ON fatfish_progress
WHEN NEW.user_id<>OLD.user_id OR NEW.period_id<>OLD.period_id OR NEW.node_id<>OLD.node_id
 OR NEW.unlocked_at<>OLD.unlocked_at OR NEW.unlock_operation_id IS NOT OLD.unlock_operation_id
 OR NEW.passed<OLD.passed OR NEW.best_stars<OLD.best_stars OR NEW.best_score_units<OLD.best_score_units
 OR (OLD.passed=1 AND NEW.best_score_units=OLD.best_score_units AND (NEW.best_version_id IS NOT OLD.best_version_id
 OR NEW.best_at_ms IS NOT OLD.best_at_ms OR NEW.best_challenge_id IS NOT OLD.best_challenge_id OR NEW.best_confirmed_at_ms IS NOT OLD.best_confirmed_at_ms))
BEGIN SELECT RAISE(ABORT,'game progress cannot regress'); END;
CREATE TABLE fatfish_period_progress (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT NOT NULL REFERENCES fatfish_periods(id),
 total_score_units INTEGER NOT NULL CHECK(total_score_units BETWEEN 0 AND 12800000000),
 achieved_at_ms INTEGER NOT NULL CHECK(achieved_at_ms BETWEEN 0 AND 253402300799000),
 public_tie_key BLOB NOT NULL CHECK(length(public_tie_key)=16),
 PRIMARY KEY(user_id,period_id),
 UNIQUE(period_id,public_tie_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fatfish_period_leaderboard ON fatfish_period_progress(period_id,total_score_units DESC,achieved_at_ms,public_tie_key);
CREATE TRIGGER fatfish_period_progress_monotone BEFORE UPDATE ON fatfish_period_progress
WHEN NEW.user_id<>OLD.user_id OR NEW.period_id<>OLD.period_id OR NEW.public_tie_key<>OLD.public_tie_key
 OR NEW.total_score_units<OLD.total_score_units OR NEW.achieved_at_ms<OLD.achieved_at_ms
 OR (NEW.total_score_units=OLD.total_score_units AND NEW.achieved_at_ms<>OLD.achieved_at_ms)
BEGIN SELECT RAISE(ABORT,'period score cannot regress'); END;
CREATE TABLE fatfish_reward_claims (
 identity_key BLOB NOT NULL CHECK(length(identity_key)=32),
 period_id TEXT NOT NULL CHECK(length(period_id)=26),
 node_id TEXT NOT NULL CHECK(length(node_id)=26),
 tier TEXT NOT NULL CHECK(tier IN ('first_clear','star1','star2','star3')),
 operation_id TEXT UNIQUE REFERENCES credit_operations(id),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(identity_key,period_id,node_id,tier),
 CHECK((amount_mag=zeroblob(16))=(operation_id IS NULL))
) STRICT, WITHOUT ROWID;
CREATE TRIGGER fatfish_reward_claim_immutable BEFORE UPDATE ON fatfish_reward_claims
BEGIN SELECT RAISE(ABORT,'game reward claim is immutable'); END;
CREATE TABLE fatfish_financial_receipts (
 receipt_key TEXT PRIMARY KEY CHECK(length(CAST(receipt_key AS BLOB)) BETWEEN 1 AND 128),
 kind TEXT NOT NULL CHECK(kind IN ('unlock','ticket','refund')),
 operation_id TEXT NOT NULL UNIQUE REFERENCES credit_operations(id),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=16 AND amount_mag<>zeroblob(16)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE TRIGGER fatfish_financial_receipt_immutable BEFORE UPDATE ON fatfish_financial_receipts
BEGIN SELECT RAISE(ABORT,'game payment receipt is immutable'); END;
CREATE TABLE fatfish_playtests (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='fpt_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 version_id TEXT NOT NULL REFERENCES fatfish_level_versions(id),
 challenge_id TEXT UNIQUE REFERENCES fatfish_challenges(id) ON DELETE SET NULL,
 actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 passed INTEGER NOT NULL CHECK(passed IN (0,1)),
 stars INTEGER NOT NULL CHECK(stars BETWEEN 0 AND 3),
 score_units INTEGER NOT NULL CHECK(score_units BETWEEN 0 AND 100000000),
 duration_ms INTEGER NOT NULL CHECK(duration_ms BETWEEN 0 AND 5000),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 CHECK((passed=1)=(stars>=1)),
 CHECK((passed=1)=(score_units>0))
) STRICT;
CREATE INDEX idx_fatfish_playtests_passed ON fatfish_playtests(version_id,passed,created_at DESC);
CREATE INDEX idx_request_logs_usage_mismatch ON request_logs(started_at DESC,id DESC) WHERE usage_total_mismatch=1;
CREATE TABLE game_likes_loadouts (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 slot INTEGER NOT NULL CHECK(typeof(slot)='integer' AND slot BETWEEN 1 AND 10),
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9223372036854775807),
 mode TEXT NOT NULL CHECK(mode IN ('quick','standard')),
 loadout_json TEXT NOT NULL CHECK(typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB)) BETWEEN 1 AND 4096
  AND json_valid(loadout_json) AND COALESCE(json_type(loadout_json),'')='object'
  AND COALESCE(json_type(loadout_json,'$.role'),'')='text'
  AND COALESCE(json_type(loadout_json,'$.harness'),'') IN ('null','text')
  AND COALESCE(json_type(loadout_json,'$.skills'),'')='array'
  AND json_array_length(loadout_json,'$.skills') BETWEEN 1 AND 6),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799), name TEXT NOT NULL DEFAULT '' CHECK(typeof(name)='text' AND length(name)<=20 AND instr(name,char(0))=0 AND instr(name,char(10))=0 AND instr(name,char(13))=0),
 PRIMARY KEY(user_id,slot)
);
CREATE TRIGGER game_likes_loadouts_insert_guard BEFORE INSERT ON game_likes_loadouts
WHEN NEW.revision<>1 OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'invalid custom preset owner or revision'); END;
CREATE TRIGGER game_likes_loadouts_update_guard BEFORE UPDATE ON game_likes_loadouts
WHEN NEW.user_id<>OLD.user_id OR NEW.slot<>OLD.slot OR OLD.revision=9223372036854775807
 OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
 OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'invalid custom preset update'); END;
CREATE TABLE fatfish_deleted_levels (
 level_id TEXT PRIMARY KEY REFERENCES fatfish_levels(id) ON DELETE CASCADE,
 deleted_at INTEGER NOT NULL CHECK(deleted_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE INDEX idx_request_logs_origin_user ON request_logs(origin_user_id,started_at,id);
CREATE INDEX idx_request_logs_origin_discord ON request_logs(origin_discord_id,started_at,id);
CREATE TRIGGER endpoint_key_review_identity BEFORE UPDATE OF key_body_review_hmac ON endpoint_key_secrets
WHEN OLD.key_body_review_hmac IS NOT NULL AND NEW.key_body_review_hmac IS NOT OLD.key_body_review_hmac
BEGIN SELECT RAISE(ABORT,'credential review identity is immutable'); END;
CREATE TABLE auth_denial_grants (
 token_hash BLOB PRIMARY KEY CHECK(length(token_hash)=32),
 discord_id TEXT NOT NULL CHECK(length(discord_id) BETWEEN 1 AND 20 AND discord_id NOT GLOB '*[^0-9]*' AND substr(discord_id,1,1) BETWEEN '1' AND '9'),
 cursor_domain BLOB NOT NULL CHECK(length(cursor_domain)=32),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300499),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+300)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_auth_denial_grants_expiry ON auth_denial_grants(expires_at,token_hash);
CREATE TABLE donation_review_material (
 id INTEGER PRIMARY KEY CHECK(id=1),
 version INTEGER NOT NULL CHECK(version=1),
 envelope BLOB NOT NULL CHECK(length(envelope) BETWEEN 29 AND 4096),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT;
CREATE TABLE donation_key_review_requirements (
 hmac BLOB PRIMARY KEY CHECK(length(hmac)=32),
 required INTEGER NOT NULL CHECK(required IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>=1),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799)
) STRICT, WITHOUT ROWID;
CREATE TRIGGER donation_review_requirement_capacity BEFORE INSERT ON donation_key_review_requirements
WHEN NOT EXISTS(SELECT 1 FROM donation_key_review_requirements WHERE hmac=NEW.hmac)
 AND (SELECT count(*) FROM donation_key_review_requirements)>=100000
BEGIN SELECT RAISE(ABORT,'review requirement capacity exhausted'); END;
CREATE TRIGGER donation_review_requirement_identity BEFORE UPDATE ON donation_key_review_requirements
WHEN NEW.hmac IS NOT OLD.hmac OR NEW.created_at<>OLD.created_at OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
BEGIN SELECT RAISE(ABORT,'review requirement identity or revision is invalid'); END;
CREATE TABLE donation_key_manual_models (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 donation_key_id INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE,
 normalized_model_id TEXT NOT NULL CHECK(length(normalized_model_id) BETWEEN 1 AND 512 AND instr(normalized_model_id,char(0))=0),
 display_name TEXT NOT NULL CHECK(length(display_name) BETWEEN 1 AND 512 AND instr(display_name,char(0))=0),
 revision INTEGER NOT NULL CHECK(revision>=1),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 UNIQUE(donation_key_id,normalized_model_id)
) STRICT;
CREATE INDEX idx_donation_manual_models_member ON donation_key_manual_models(donation_key_id,id);
CREATE TABLE resource_operation_status (
 actor_scope_hash BLOB NOT NULL CHECK(length(actor_scope_hash)=32),
 key_hash BLOB NOT NULL CHECK(length(key_hash)=32),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 stage TEXT NOT NULL CHECK(stage IN ('endpoint','key','catalog_refresh','catalog_manual','model','binding_batch')),
 status TEXT NOT NULL CHECK(status IN ('recorded','in_progress')),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400),
 PRIMARY KEY(actor_scope_hash,key_hash)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_resource_operation_status_expiry ON resource_operation_status(expires_at,actor_scope_hash,key_hash);
CREATE TABLE automatic_reason_metadata (
 owner_kind TEXT NOT NULL CHECK(owner_kind IN ('user_ban','blacklist_event','client_rule_ban','admin_restriction')),
 owner_id TEXT NOT NULL CHECK(length(CAST(owner_id AS BLOB)) BETWEEN 1 AND 128),
 kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 schema_version INTEGER NOT NULL CHECK(schema_version=1),
 params_json TEXT NOT NULL CHECK(json_valid(params_json) AND json_type(params_json)='object' AND length(CAST(params_json AS BLOB))<=4096),
 manual_text TEXT NOT NULL DEFAULT '' CHECK(length(CAST(manual_text AS BLOB))<=4096),
 PRIMARY KEY(owner_kind,owner_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE automatic_reason_rule_labels (
 owner_kind TEXT NOT NULL,
 owner_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 99),
 rule_id TEXT NOT NULL CHECK(length(rule_id) BETWEEN 1 AND 64),
 rule_revision INTEGER NOT NULL CHECK(rule_revision>=1),
 name_snapshot TEXT NOT NULL CHECK(length(name_snapshot) BETWEEN 1 AND 120),
 PRIMARY KEY(owner_kind,owner_id,ordinal),
 FOREIGN KEY(owner_kind,owner_id) REFERENCES automatic_reason_metadata(owner_kind,owner_id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
CREATE TABLE instance_cleanup_receipts (
 instance_identity BLOB NOT NULL CHECK(length(instance_identity)=32),
 operation_key_hash BLOB NOT NULL CHECK(length(operation_key_hash)=32),
 source_commit TEXT NOT NULL CHECK(length(source_commit)=40 AND source_commit NOT GLOB '*[^0-9a-f]*'),
 source_schema_hash BLOB NOT NULL CHECK(length(source_schema_hash)=32),
 source_tree TEXT NOT NULL CHECK(length(source_tree)=40 AND source_tree NOT GLOB '*[^0-9a-f]*'),
 eligible_ids_hash BLOB NOT NULL CHECK(length(eligible_ids_hash)=32),
 checkpoint_json TEXT NOT NULL CHECK(json_valid(checkpoint_json) AND json_type(checkpoint_json)='object' AND length(CAST(checkpoint_json AS BLOB))<=16384),
 retained_counts_json TEXT NOT NULL CHECK(json_valid(retained_counts_json) AND json_type(retained_counts_json)='object' AND length(CAST(retained_counts_json AS BLOB))<=16384),
 completed INTEGER NOT NULL CHECK(completed IN (0,1)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 completed_at INTEGER CHECK(completed_at BETWEEN created_at AND 253402300799),
 PRIMARY KEY(instance_identity,operation_key_hash),
 CHECK((completed=0 AND completed_at IS NULL) OR (completed=1 AND completed_at IS NOT NULL))
) STRICT, WITHOUT ROWID;
CREATE TABLE fatfish_graph_layouts (
 period_id TEXT PRIMARY KEY REFERENCES fatfish_periods(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>=1),
 layout_json TEXT NOT NULL CHECK(json_valid(layout_json) AND json_type(layout_json)='object' AND length(CAST(layout_json AS BLOB))<=32768),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT, WITHOUT ROWID;
CREATE TABLE lake_notes_periods (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lnp_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 revision INTEGER NOT NULL CHECK(revision>=1),
 status TEXT NOT NULL CHECK(status IN ('draft','published','cancelled')),
 starts_at INTEGER NOT NULL CHECK(starts_at BETWEEN 0 AND 253402300798),
 ends_at INTEGER NOT NULL CHECK(ends_at BETWEEN starts_at+1 AND 253402300799),
 entry_fee_milli INTEGER CHECK(entry_fee_milli BETWEEN 0 AND 9000000000000000),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 CHECK(status<>'published' OR entry_fee_milli IS NOT NULL)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_lake_notes_periods_time ON lake_notes_periods(status,starts_at,ends_at,id);
CREATE TRIGGER lake_notes_period_overlap_insert BEFORE INSERT ON lake_notes_periods
WHEN NEW.status='published' AND EXISTS(SELECT 1 FROM lake_notes_periods WHERE status='published' AND starts_at<NEW.ends_at AND ends_at>NEW.starts_at)
BEGIN SELECT RAISE(ABORT,'published periods overlap'); END;
CREATE TRIGGER lake_notes_period_overlap_update BEFORE UPDATE ON lake_notes_periods
WHEN NEW.status='published' AND EXISTS(SELECT 1 FROM lake_notes_periods WHERE id<>OLD.id AND status='published' AND starts_at<NEW.ends_at AND ends_at>NEW.starts_at)
BEGIN SELECT RAISE(ABORT,'published periods overlap'); END;
CREATE TABLE lake_notes_exchange_settings (
 period_id TEXT NOT NULL REFERENCES lake_notes_periods(id) ON DELETE CASCADE,
 direction TEXT NOT NULL CHECK(direction IN ('general_to_coin','coin_to_general','game_to_coin','coin_to_game')),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 source_lot BLOB NOT NULL CHECK(length(source_lot)=16 AND source_lot<>zeroblob(16)),
 target_lot BLOB NOT NULL CHECK(length(target_lot)=16 AND target_lot<>zeroblob(16)),
 PRIMARY KEY(period_id,direction)
) STRICT, WITHOUT ROWID;
CREATE TABLE lake_notes_profiles (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>=1),
 rules_id TEXT NOT NULL CHECK(length(CAST(rules_id AS BLOB)) BETWEEN 1 AND 128),
 coin_mag BLOB NOT NULL CHECK(length(coin_mag)=16),
 profile BLOB NOT NULL CHECK(length(profile) BETWEEN 1 AND 65536),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
, storage_version INTEGER NOT NULL DEFAULT 1 CHECK(storage_version>=1)) STRICT;
CREATE TABLE lake_notes_casts (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lnc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source_period_id TEXT REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 rules_id TEXT NOT NULL CHECK(length(CAST(rules_id AS BLOB)) BETWEEN 1 AND 128),
 phase TEXT NOT NULL CHECK(phase IN ('waiting','playing','success','failed')),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 generation INTEGER NOT NULL CHECK(generation>=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 last_tick INTEGER NOT NULL CHECK(last_tick BETWEEN 0 AND 9007199254740991),
 snapshot BLOB NOT NULL CHECK(length(snapshot) BETWEEN 1 AND 65536),
 reward_plan BLOB NOT NULL CHECK(length(reward_plan) BETWEEN 1 AND 4096),
 held INTEGER NOT NULL CHECK(held IN (0,1)),
 active_elapsed_ns INTEGER NOT NULL CHECK(active_elapsed_ns>=0),
 active_started_at_ns INTEGER CHECK(active_started_at_ns>=0),
 lease_until_ns INTEGER CHECK(lease_until_ns>=active_started_at_ns),
 last_ack_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(last_ack_json) AND json_type(last_ack_json)='object' AND length(CAST(last_ack_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at BETWEEN created_at AND 253402300799), storage_version INTEGER NOT NULL DEFAULT 1 CHECK(storage_version>=1),
 CHECK((paused=1 AND held=0 AND active_started_at_ns IS NULL AND lease_until_ns IS NULL) OR (paused=0 AND active_started_at_ns IS NOT NULL AND lease_until_ns IS NOT NULL)),
 CHECK((phase IN ('waiting','playing') AND terminal_at IS NULL) OR (phase IN ('success','failed') AND terminal_at IS NOT NULL AND paused=1))
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX idx_lake_notes_active_cast_user ON lake_notes_casts(user_id) WHERE phase IN ('waiting','playing');
CREATE INDEX idx_lake_notes_cast_retention ON lake_notes_casts(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE TABLE lake_notes_entitlements (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT NOT NULL REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 fee_milli INTEGER NOT NULL CHECK(fee_milli BETWEEN 0 AND 9000000000000000),
 period_revision INTEGER NOT NULL CHECK(period_revision>=1),
 operation_key_hash BLOB NOT NULL CHECK(length(operation_key_hash)=32),
 ledger_operation_id TEXT REFERENCES credit_operations(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,period_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE lake_notes_exchange_receipts (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lne_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 period_id TEXT REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 period_revision INTEGER CHECK(period_revision>=1),
 config_revision INTEGER CHECK(config_revision>=1),
 direction TEXT NOT NULL CHECK(direction IN ('general_to_coin','coin_to_general','game_to_coin','coin_to_game')),
 quantity_mag BLOB NOT NULL CHECK(length(quantity_mag)=16 AND quantity_mag<>zeroblob(16)),
 source_lot BLOB NOT NULL CHECK(length(source_lot)=16 AND source_lot<>zeroblob(16)),
 target_lot BLOB NOT NULL CHECK(length(target_lot)=16 AND target_lot<>zeroblob(16)),
 source_amount_mag BLOB NOT NULL CHECK(length(source_amount_mag)=16),
 target_amount_mag BLOB NOT NULL CHECK(length(target_amount_mag)=16),
 operation_key_hash BLOB NOT NULL CHECK(length(operation_key_hash)=32),
 ledger_operation_id TEXT NOT NULL REFERENCES credit_operations(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 CHECK((period_id IS NOT NULL AND period_revision IS NOT NULL AND config_revision IS NULL) OR (period_id IS NULL AND period_revision IS NULL AND config_revision IS NOT NULL)),
 UNIQUE(user_id,operation_key_hash)
) STRICT, WITHOUT ROWID;
CREATE TABLE personal_automation_batches (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='pab_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 actor_scope_hash BLOB NOT NULL CHECK(length(actor_scope_hash)=32),
 root_key_hash BLOB NOT NULL CHECK(length(root_key_hash)=32),
 request_hash BLOB NOT NULL CHECK(length(request_hash)=32),
 kind TEXT NOT NULL CHECK(kind IN ('key_import','binding_append')),
 target_id INTEGER NOT NULL CHECK(target_id>0),
 item_count INTEGER NOT NULL CHECK(item_count BETWEEN 1 AND 100),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402214399),
 expires_at INTEGER NOT NULL CHECK(expires_at=created_at+86400),
 UNIQUE(user_id,root_key_hash),
 UNIQUE(id,user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_personal_automation_batches_expiry ON personal_automation_batches(expires_at,id);
CREATE INDEX idx_personal_automation_batches_owner ON personal_automation_batches(user_id,expires_at,id);
CREATE TRIGGER personal_automation_batch_capacity BEFORE INSERT ON personal_automation_batches
WHEN NOT EXISTS(SELECT 1 FROM personal_automation_batches WHERE user_id=NEW.user_id AND root_key_hash=NEW.root_key_hash)
 AND ((SELECT count(*) FROM personal_automation_batches WHERE expires_at>NEW.created_at)>=10000
 OR (SELECT count(*) FROM personal_automation_batches WHERE user_id=NEW.user_id AND expires_at>NEW.created_at)>=1000)
BEGIN SELECT RAISE(ABORT,'automation batch capacity exhausted'); END;
CREATE TRIGGER personal_automation_batch_owner BEFORE INSERT ON personal_automation_batches
WHEN NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'automation batch owner is unavailable'); END;
CREATE TRIGGER personal_automation_batch_immutable BEFORE UPDATE ON personal_automation_batches
BEGIN SELECT RAISE(ABORT,'automation batch identity is immutable'); END;
CREATE TABLE personal_automation_steps (
 batch_id TEXT NOT NULL,
 user_id INTEGER NOT NULL,
 item_index INTEGER NOT NULL CHECK(item_index BETWEEN 0 AND 99),
 status TEXT NOT NULL CHECK(status IN ('success','failed')),
 result_json TEXT NOT NULL CHECK(json_valid(result_json) AND json_type(result_json)='object' AND length(CAST(result_json AS BLOB))<=4096),
 discovery_operation_id TEXT,
 discovery_revision INTEGER CHECK(discovery_revision>=1),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(batch_id,item_index),
 FOREIGN KEY(batch_id,user_id) REFERENCES personal_automation_batches(id,user_id) ON DELETE CASCADE,
 CHECK((discovery_operation_id IS NULL AND discovery_revision IS NULL) OR (discovery_operation_id IS NOT NULL AND discovery_revision IS NOT NULL))
) STRICT, WITHOUT ROWID;
CREATE TRIGGER personal_automation_step_insert_guard BEFORE INSERT ON personal_automation_steps
WHEN NOT EXISTS(SELECT 1 FROM personal_automation_batches WHERE id=NEW.batch_id AND user_id=NEW.user_id AND NEW.item_index<item_count AND NEW.created_at>=created_at AND NEW.created_at<expires_at)
 OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
 OR (SELECT COALESCE(sum(length(CAST(result_json AS BLOB))),0) FROM personal_automation_steps WHERE batch_id=NEW.batch_id)+length(CAST(NEW.result_json AS BLOB))>65536
BEGIN SELECT RAISE(ABORT,'automation step is inconsistent'); END;
CREATE TRIGGER personal_automation_step_immutable BEFORE UPDATE ON personal_automation_steps
BEGIN SELECT RAISE(ABORT,'automation step result is immutable'); END;
CREATE TRIGGER models_role_policy_insert_guard BEFORE INSERT ON models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;
CREATE TRIGGER models_role_policy_update_guard BEFORE UPDATE OF role_policy ON models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;
CREATE TRIGGER charity_models_role_policy_insert_guard BEFORE INSERT ON charity_models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;
CREATE TRIGGER charity_models_role_policy_update_guard BEFORE UPDATE OF role_policy ON charity_models
WHEN (SELECT count(*) FROM json_each(NEW.role_policy))<>2
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) WHERE key NOT IN ('default_action','rules'))
 OR (SELECT count(*) FROM json_each(NEW.role_policy,'$.rules'))>32
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') WHERE type<>'text'
  OR value NOT IN ('native','passthrough','system','user','assistant','reject')
  OR length(key) NOT BETWEEN 1 AND 64 OR length(CAST(key AS BLOB))>256
  OR key IN ('system','assistant','user','tool','function') OR key<>trim(key)
  OR instr(key,char(0))>0 OR key GLOB '*['||char(1)||'-'||char(31)||char(127)||']*')
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy) GROUP BY key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM json_each(NEW.role_policy,'$.rules') GROUP BY key HAVING count(*)>1)
BEGIN SELECT RAISE(ABORT,'invalid model role policy'); END;
CREATE TRIGGER policy_audits_no_update BEFORE UPDATE ON policy_audits
WHEN NOT (OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id)
 AND OLD.id=NEW.id AND OLD.actor_role=NEW.actor_role AND OLD.resource_type=NEW.resource_type
 AND OLD.resource_id=NEW.resource_id AND OLD.policy=NEW.policy
 AND OLD.old_value IS NEW.old_value AND OLD.new_value IS NEW.new_value
 AND OLD.from_revision IS NEW.from_revision AND OLD.to_revision IS NEW.to_revision
 AND OLD.created_at=NEW.created_at)
BEGIN SELECT RAISE(ABORT,'policy_audits is append-only'); END;
CREATE TRIGGER policy_audits_value_insert_guard BEFORE INSERT ON policy_audits
WHEN NOT COALESCE((NEW.policy='role_policy' AND NEW.resource_type IN ('model','charity_model')
 AND NEW.old_value IS NULL AND NEW.new_value IS NULL
 AND NEW.from_revision IS NOT NULL AND NEW.to_revision IS NOT NULL
 AND NEW.from_revision<9223372036854775807 AND NEW.to_revision=NEW.from_revision+1)
 OR (NEW.policy<>'role_policy' AND NEW.old_value IS NOT NULL AND NEW.new_value IS NOT NULL
 AND NEW.from_revision IS NULL AND NEW.to_revision IS NULL),0)
BEGIN SELECT RAISE(ABORT,'invalid policy audit values'); END;
CREATE UNIQUE INDEX idx_fatfish_version_number ON fatfish_level_versions(level_id,version_number);
CREATE TRIGGER fatfish_content_immutable BEFORE UPDATE ON fatfish_level_versions
BEGIN SELECT RAISE(ABORT,'published game content is immutable'); END;
CREATE TRIGGER donation_usage_reservations_outcome_insert_guard BEFORE INSERT ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
CREATE TRIGGER donation_usage_reservations_outcome_update_guard BEFORE UPDATE ON donation_usage_reservations
WHEN NOT COALESCE((NEW.state IN ('reserved') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND ((NEW.failure_origin='none' AND NEW.protocol_success=1) OR (NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown') AND NEW.protocol_success=0)))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout') AND COALESCE(NEW.protocol_success,0)=0)
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown') AND COALESCE(NEW.protocol_success,0)=0))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
CREATE TRIGGER dispatch_claims_outcome_insert_guard BEFORE INSERT ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown')))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
CREATE TRIGGER dispatch_claims_outcome_update_guard BEFORE UPDATE ON dispatch_claims
WHEN NOT COALESCE((NEW.state IN ('claimed','dispatched') AND NEW.streak_disposition IS NULL AND NEW.failure_origin IS NULL)
 OR (NEW.state IN ('committed','released') AND (
  (NEW.state='committed' AND NEW.streak_disposition='success' AND NEW.failure_origin IN ('none','upstream_response','upstream_protocol','network','timeout','client_cancel','downstream','platform','recovery_unknown'))
  OR (NEW.streak_disposition='upstream_failure' AND NEW.failure_origin IN ('upstream_response','upstream_protocol','network','timeout'))
  OR (NEW.streak_disposition='neutral' AND NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown')))),0)
BEGIN SELECT RAISE(ABORT,'invalid terminal outcome'); END;
CREATE TRIGGER request_log_origin_capture AFTER INSERT ON request_logs
WHEN NEW.user_id IS NOT NULL AND NEW.origin_user_id IS NULL
BEGIN
 UPDATE request_logs SET origin_user_id=NEW.user_id,
 origin_discord_id=(SELECT discord_id FROM users WHERE id=NEW.user_id
  AND length(discord_id) BETWEEN 1 AND 20 AND discord_id NOT GLOB '*[^0-9]*'
  AND substr(discord_id,1,1) BETWEEN '1' AND '9') WHERE id=NEW.id;
END;
CREATE TRIGGER request_log_origin_immutable BEFORE UPDATE OF origin_user_id,origin_discord_id ON request_logs
WHEN (OLD.origin_user_id IS NOT NULL AND NEW.origin_user_id IS NOT OLD.origin_user_id)
 OR (OLD.origin_discord_id IS NOT NULL AND NEW.origin_discord_id IS NOT OLD.origin_discord_id)
BEGIN SELECT RAISE(ABORT,'request origin is immutable'); END;
CREATE INDEX idx_request_sources_scan ON request_source_facts(occurred_at,source_id) WHERE kind IN ('self','charity','unclassified');
CREATE INDEX idx_request_sources_ip_time ON request_source_facts(effective_ip,occurred_at,source_id);
CREATE TRIGGER risk_scan_user_retired BEFORE DELETE ON users
BEGIN
 UPDATE risk_client_scans SET changed=1,
 state=CASE WHEN state IN ('queued','running') THEN 'failed' ELSE state END,
 reason=CASE WHEN state IN ('queued','running') THEN 'source_changed' ELSE reason END,
 checkpoint_json=json_remove(checkpoint_json,'$.pending_user','$.after_user','$.source_at','$.source_id')
 WHERE kind='users' AND (id IN (SELECT scan_id FROM risk_scan_results WHERE user_id=OLD.id
 UNION SELECT scan_id FROM risk_scan_result_users WHERE user_id=OLD.id)
 OR json_extract(checkpoint_json,'$.pending_user')=OLD.id OR json_extract(checkpoint_json,'$.after_user')=OLD.id);
 DELETE FROM risk_scan_results WHERE scan_id IN (SELECT id FROM risk_client_scans WHERE kind='users')
 AND (user_id=OLD.id OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_users WHERE user_id=OLD.id));
END;
CREATE TABLE risk_scan_window_sources (
 scan_id TEXT NOT NULL REFERENCES risk_client_scans(id) ON DELETE CASCADE,
 request_log_id INTEGER NOT NULL REFERENCES request_source_facts(request_log_id) ON DELETE CASCADE,
 PRIMARY KEY(scan_id,request_log_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_risk_scan_window_source ON risk_scan_window_sources(request_log_id,scan_id);
CREATE TRIGGER risk_scan_window_finished AFTER UPDATE OF state ON risk_client_scans
WHEN NEW.state IN ('completed','failed','cancelled','limited')
BEGIN
 DELETE FROM risk_scan_window_sources WHERE scan_id=NEW.id;
 UPDATE risk_client_scans SET checkpoint_json=json_remove(checkpoint_json,
 '$.pending_discord','$.after_discord','$.user_ip_summary','$.pending_user','$.after_user',
 '$.pending_ip','$.after_ip','$.source_at','$.source_id','$.ip_summary') WHERE id=NEW.id;
END;
CREATE TRIGGER risk_scan_result_source_deleted BEFORE DELETE ON request_source_facts
BEGIN
 UPDATE risk_client_scans SET changed=1,
 state=CASE WHEN state IN ('queued','running') THEN 'failed' ELSE state END,
 reason=CASE WHEN state IN ('queued','running') THEN 'source_changed' ELSE reason END,
 checkpoint_json=json_remove(checkpoint_json,'$.pending_user','$.after_user','$.pending_ip','$.after_ip',
 '$.source_at','$.source_id','$.ip_summary','$.pending_discord','$.after_discord','$.user_ip_summary')
 WHERE id IN (SELECT scan_id FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 UNION SELECT scan_id FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id
 UNION SELECT scan_id FROM risk_scan_window_sources WHERE request_log_id=OLD.request_log_id)
 OR (kind IN ('client_hits','users','shared_ips','user_ips') AND (
 json_extract(checkpoint_json,'$.pending_user')=(SELECT origin_user_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.after_user')=(SELECT origin_user_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.pending_discord')=(SELECT origin_discord_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.after_discord')=(SELECT origin_discord_id FROM request_logs WHERE id=OLD.request_log_id)
 OR json_extract(checkpoint_json,'$.source_id')=OLD.source_id))
 OR (OLD.effective_ip<>'' AND (json_extract(checkpoint_json,'$.pending_ip')=OLD.effective_ip OR json_extract(checkpoint_json,'$.after_ip')=OLD.effective_ip));
 DELETE FROM risk_scan_results WHERE request_log_id=OLD.request_log_id
 OR (scan_id,row_no) IN (SELECT scan_id,row_no FROM risk_scan_result_sources WHERE request_log_id=OLD.request_log_id);
END;
CREATE TRIGGER automatic_user_ban_reason_deleted AFTER DELETE ON users
BEGIN DELETE FROM automatic_reason_metadata WHERE owner_kind='user_ban' AND owner_id=CAST(OLD.id AS TEXT); END;
CREATE TABLE gateway_model_capabilities (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 base_url TEXT NOT NULL,
 model TEXT NOT NULL,
 policy_json TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9223372036854775807),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799),
 UNIQUE(base_url,model)
);
CREATE TABLE gateway_model_capabilities_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 initialized_at INTEGER NOT NULL CHECK(typeof(initialized_at)='integer' AND initialized_at BETWEEN 0 AND 253402300799)
);
CREATE INDEX idx_idempotency_recovery ON idempotency_records(state,expires_at,scope,actor_scope_hash,key_hash);
CREATE TABLE credit_compaction (
 id INTEGER PRIMARY KEY CHECK(id=1),
 through_seq INTEGER NOT NULL DEFAULT 0 CHECK(through_seq>=0),
 details_before INTEGER NOT NULL DEFAULT 0 CHECK(details_before BETWEEN 0 AND 253402300799),
 sweep_at INTEGER NOT NULL DEFAULT 0 CHECK(sweep_at BETWEEN 0 AND 253402300799),
 sweep_after_seq INTEGER NOT NULL DEFAULT 0 CHECK(sweep_after_seq>=0)
) STRICT;
CREATE TABLE credit_opening_balances (
 account_id INTEGER PRIMARY KEY REFERENCES credit_accounts(id) ON DELETE CASCADE,
 balance_sign INTEGER NOT NULL CHECK(balance_sign IN (-1,0,1)),
 balance_mag BLOB NOT NULL CHECK(length(balance_mag)=16 AND hex(balance_mag)<'80000000000000000000000000000000'),
 CHECK((balance_sign=0 AND balance_mag=zeroblob(16)) OR (balance_sign<>0 AND balance_mag<>zeroblob(16)))
) STRICT;
CREATE TRIGGER credit_operations_no_update BEFORE UPDATE ON credit_operations
WHEN NOT (
 NEW.id IS OLD.id AND NEW.ledger_seq IS OLD.ledger_seq AND NEW.kind IS OLD.kind AND
 NEW.source_type IS OLD.source_type AND NEW.source_id IS OLD.source_id AND NEW.source_seq IS OLD.source_seq AND
 NEW.created_at IS OLD.created_at AND (
  (NEW.compacted IS OLD.compacted AND
   NEW.donation_credit_delta_sign IS OLD.donation_credit_delta_sign AND NEW.donation_credit_delta_mag IS OLD.donation_credit_delta_mag AND
   NEW.donation_credit_after IS OLD.donation_credit_after AND NEW.reason IS OLD.reason AND
   (NEW.actor_user_id IS OLD.actor_user_id OR (NEW.actor_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.actor_user_id))) AND
   (NEW.donation_credit_user_id IS OLD.donation_credit_user_id OR (NEW.donation_credit_user_id IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=OLD.donation_credit_user_id))))
  OR (OLD.compacted=0 AND NEW.compacted=1 AND OLD.ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1) AND
   NEW.actor_user_id IS NULL AND NEW.donation_credit_user_id IS NULL AND NEW.donation_credit_delta_sign=0 AND
   NEW.donation_credit_delta_mag=zeroblob(16) AND NEW.donation_credit_after IS NULL AND NEW.reason IS NULL)
 ))
BEGIN SELECT RAISE(ABORT,'credit_operations is immutable outside compaction'); END;
CREATE TRIGGER credit_operations_no_delete BEFORE DELETE ON credit_operations
WHEN OLD.compacted<>1 OR OLD.ledger_seq>(SELECT through_seq FROM credit_compaction WHERE id=1)
BEGIN SELECT RAISE(ABORT,'credit operation is not compacted'); END;
CREATE TRIGGER credit_entries_no_delete BEFORE DELETE ON credit_entries
WHEN NOT EXISTS(SELECT 1 FROM credit_operations WHERE id=OLD.operation_id AND compacted=1
 AND ledger_seq<=(SELECT through_seq FROM credit_compaction WHERE id=1))
BEGIN SELECT RAISE(ABORT,'credit entry is not compacted'); END;
CREATE INDEX idx_inactivity_run_operation ON inactivity_runs(ledger_operation_id) WHERE ledger_operation_id IS NOT NULL;
CREATE INDEX idx_abuse_action_operation ON abuse_actions(operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX idx_lake_entitlement_operation ON lake_notes_entitlements(ledger_operation_id) WHERE ledger_operation_id IS NOT NULL;
CREATE INDEX idx_lake_exchange_operation ON lake_notes_exchange_receipts(ledger_operation_id);
CREATE INDEX idx_linklink_operation ON game_linklink_sessions(operation_id);
CREATE INDEX idx_rps_queue_operation ON game_rps_queue(reservation_operation_id);
CREATE INDEX idx_rps_terminal_operation ON game_rps_sessions(terminal_operation_id) WHERE terminal_operation_id IS NOT NULL;
CREATE INDEX idx_image_reserve_operation ON image_activity_tasks(reserve_operation_id) WHERE reserve_operation_id IS NOT NULL;
CREATE INDEX idx_image_terminal_operation ON image_activity_tasks(terminal_operation_id) WHERE terminal_operation_id IS NOT NULL;
CREATE INDEX idx_credit_entries_history ON credit_entries(account_id,operation_id,line_no,delta_sign) WHERE account_kind_snapshot='user' AND delta_sign<>0;
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id);
CREATE INDEX idx_charity_reservations_retention ON charity_reservations(finalized_at,logical_request_id) WHERE state IN ('committed','released');
CREATE INDEX idx_donation_usage_retention ON donation_usage_reservations(finalized_at,claim_id) WHERE state IN ('committed','released');
CREATE TABLE schema_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 version INTEGER NOT NULL CHECK(version>=1)
) STRICT;
INSERT INTO credit_compaction(id,through_seq,details_before,sweep_at,sweep_after_seq) VALUES(1,0,0,0,0);
INSERT INTO game_blackjack_clock(id,observed_at) VALUES(1,0);
INSERT INTO schema_state(id,version) VALUES(1,7);

CREATE TABLE game_ai_settings (
 game_key TEXT PRIMARY KEY NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9223372036854775807)
) STRICT;
CREATE TABLE game_ai_policies (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='aip_'),
 game_key TEXT NOT NULL,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description TEXT NOT NULL CHECK(length(description)<=512),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253399708799)
) STRICT;
CREATE INDEX idx_ai_policies_game ON game_ai_policies(game_key,id);
CREATE TABLE game_ai_policy_versions (
 policy_id TEXT NOT NULL REFERENCES game_ai_policies(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL CHECK(version BETWEEN 1 AND 1000000),
 source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128),
 schema_id TEXT NOT NULL CHECK(length(schema_id) BETWEEN 1 AND 128),
 definition_json TEXT NOT NULL CHECK(json_valid(definition_json) AND length(CAST(definition_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 PRIMARY KEY(policy_id,version)
) STRICT;
CREATE TRIGGER game_ai_policy_version_immutable BEFORE UPDATE ON game_ai_policy_versions BEGIN SELECT RAISE(ABORT,'AI policy version immutable'); END;
CREATE TABLE game_ai_bots (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='bot_'),
 game_key TEXT NOT NULL,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description TEXT NOT NULL CHECK(length(description)<=512),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9223372036854775807),
 policy_id TEXT NOT NULL,
 policy_version INTEGER NOT NULL,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 ticket_milli INTEGER NOT NULL CHECK(ticket_milli BETWEEN 0 AND 9000000000000000),
 reward_milli INTEGER NOT NULL CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 memory_days INTEGER NOT NULL CHECK(memory_days BETWEEN 1 AND 30),
 memory_games INTEGER NOT NULL CHECK(memory_games BETWEEN 1 AND 100),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253399708799),
 FOREIGN KEY(policy_id,policy_version) REFERENCES game_ai_policy_versions(policy_id,version) ON DELETE RESTRICT
) STRICT;
CREATE INDEX idx_ai_bots_game ON game_ai_bots(game_key,enabled,id);
CREATE TABLE game_ai_challenges (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=26 AND substr(id,1,4)='aic_'),
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 rules_key TEXT NOT NULL CHECK(length(rules_key) BETWEEN 1 AND 128),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708799)
) STRICT;
CREATE TRIGGER game_ai_challenge_immutable BEFORE UPDATE ON game_ai_challenges BEGIN SELECT RAISE(ABORT,'AI challenge immutable'); END;
CREATE TABLE game_ai_queue (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE CHECK(length(id)=26 AND ((game_key='bidding' AND substr(id,1,4)='aiq_') OR (game_key='gwent' AND substr(id,1,4)='gaq_'))),
 game_key TEXT NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253399708679),
 deadline INTEGER NOT NULL CHECK(deadline=created_at+120),
 state TEXT NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','failed')),
 failure TEXT CHECK(failure IN ('insufficient_credits','closed','expired','account_unavailable','server_restart')),
 resolved_at INTEGER CHECK(resolved_at BETWEEN created_at AND 253399708799),
 terms_hash TEXT NOT NULL CHECK(length(terms_hash)=64),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=32768),
 UNIQUE(user_id,game_key),
 CHECK((state='waiting' AND failure IS NULL AND resolved_at IS NULL) OR (state='failed' AND failure IS NOT NULL AND resolved_at IS NOT NULL))
) STRICT;
CREATE INDEX idx_ai_queue_game ON game_ai_queue(game_key,ordinal);
CREATE TABLE game_ai_sessions (
 session_id TEXT PRIMARY KEY NOT NULL REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 bot_seat INTEGER NOT NULL CHECK(bot_seat IN (0,1)),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=65536),
 first_clear INTEGER NOT NULL DEFAULT 0 CHECK(first_clear IN (0,1)),
 reward_milli INTEGER NOT NULL DEFAULT 0 CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 CHECK(first_clear=1 OR reward_milli=0)
) STRICT;
CREATE INDEX idx_ai_sessions_bot ON game_ai_sessions(bot_id,session_id);
CREATE TABLE game_ai_preferences (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 memory_enabled INTEGER NOT NULL CHECK(memory_enabled IN (0,1)),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253399708799),
 PRIMARY KEY(user_id,bot_id)
) STRICT;
CREATE TABLE game_ai_memories (
 session_id TEXT PRIMARY KEY NOT NULL REFERENCES game_ai_sessions(session_id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES game_ai_bots(id) ON DELETE RESTRICT,
 feature_version INTEGER NOT NULL CHECK(feature_version>=1),
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253399708799),
 expires_at INTEGER NOT NULL CHECK(expires_at>completed_at AND expires_at<=completed_at+2592000),
 features_json TEXT NOT NULL CHECK(json_valid(features_json) AND length(CAST(features_json AS BLOB))<=4096)
) STRICT;
CREATE INDEX idx_ai_memory_pair ON game_ai_memories(user_id,bot_id,feature_version,completed_at DESC,session_id);
CREATE INDEX idx_ai_memory_expiry ON game_ai_memories(expires_at,session_id);
CREATE TABLE game_ai_clears (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 challenge_id TEXT NOT NULL REFERENCES game_ai_challenges(id) ON DELETE RESTRICT,
 completed_at INTEGER NOT NULL CHECK(completed_at BETWEEN 0 AND 253399708799),
 reward_milli INTEGER NOT NULL CHECK(reward_milli BETWEEN 0 AND 9000000000000000),
 operation_id TEXT REFERENCES credit_operations(id) ON DELETE SET NULL,
 PRIMARY KEY(user_id,challenge_id)
) STRICT;
CREATE TABLE game_duel_history (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 game_key TEXT NOT NULL,
 session_id TEXT NOT NULL UNIQUE REFERENCES game_duel_sessions(id) ON DELETE CASCADE
) STRICT;
CREATE INDEX idx_duel_history_game ON game_duel_history(game_key,sequence);
CREATE TRIGGER game_duel_history_terminal AFTER UPDATE OF state ON game_duel_sessions
WHEN NEW.state='terminal' AND OLD.state='active' BEGIN
 INSERT INTO game_duel_history(game_key,session_id) VALUES(NEW.game_key,NEW.id);
END;

INSERT INTO game_ai_settings(game_key) VALUES('bidding'),('gwent');

CREATE TABLE admin_endpoint_tags (
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 4096),
 tag TEXT NOT NULL CHECK(tag IN ('abusive_third_party','community_charity')),
 PRIMARY KEY(base_url,tag)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_admin_endpoint_tags_tag ON admin_endpoint_tags(tag,base_url);

ALTER TABLE request_error_bodies ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '' CHECK(failure_reason IN ('','read_failure','storage_failure'));
CREATE INDEX idx_request_errors_missing ON request_error_bodies(id) WHERE save_state<>'saved';

ALTER TABLE dispatch_response_starts ADD COLUMN http_status INTEGER CHECK(http_status IS NULL OR (typeof(http_status)='integer' AND http_status=200));

CREATE TABLE game_duel_ratings (
 game_key TEXT NOT NULL CHECK(game_key='gwent'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 rating INTEGER NOT NULL,
 played INTEGER NOT NULL CHECK(played>=1),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(game_key,user_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE game_duel_results (
 session_id TEXT NOT NULL REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 game_key TEXT NOT NULL CHECK(game_key='gwent'),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 result INTEGER NOT NULL CHECK(result IN (0,1,2)),
 rating_before INTEGER NOT NULL,
 rating_after INTEGER NOT NULL,
 settled_at INTEGER NOT NULL,
 PRIMARY KEY(session_id,user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_duel_results_window ON game_duel_results(game_key,settled_at,user_id);
CREATE INDEX idx_duel_results_user ON game_duel_results(user_id,game_key,settled_at,session_id);


CREATE TABLE game_catch_sessions (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=25 AND substr(id,1,3)='sc_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('playing','paused','completed','failed','abandoned','cancelled')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 seed INTEGER NOT NULL CHECK(seed BETWEEN 0 AND 4294967295),
 engine_json TEXT NOT NULL CHECK(json_valid(engine_json) AND length(engine_json)<=32768),
 tick INTEGER NOT NULL CHECK(tick BETWEEN 0 AND 5400),
 anchor_tick INTEGER NOT NULL CHECK(anchor_tick BETWEEN 0 AND tick),
 anchor_ms INTEGER NOT NULL CHECK(anchor_ms BETWEEN 0 AND 253402300799000),
 price_milli INTEGER NOT NULL CHECK(price_milli BETWEEN 0 AND 9000000000000000),
 general_paid_milli INTEGER NOT NULL CHECK(general_paid_milli BETWEEN 0 AND price_milli),
 game_paid_milli INTEGER NOT NULL CHECK(game_paid_milli BETWEEN 0 AND price_milli),
 first_reward_milli INTEGER NOT NULL CHECK(first_reward_milli BETWEEN 0 AND 9000000000000000),
 first_clear INTEGER NOT NULL DEFAULT 0 CHECK(first_clear IN (0,1)),
 reward_milli INTEGER NOT NULL DEFAULT 0 CHECK(reward_milli BETWEEN 0 AND first_reward_milli),
 score INTEGER NOT NULL DEFAULT 0 CHECK(score>=0),
 entry_operation_id TEXT,
 terminal_operation_id TEXT,
 ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16 AND hex(ledger_rows_remaining) IN ('00000000000000000000000000000000','00000000000000000000000000000001')),
 last_request_hash BLOB CHECK(last_request_hash IS NULL OR (typeof(last_request_hash)='blob' AND length(last_request_hash)=32)),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 expires_at INTEGER NOT NULL CHECK(expires_at>created_at),
 updated_at INTEGER NOT NULL CHECK(updated_at>=created_at),
 terminal_at INTEGER,
 CHECK(general_paid_milli+game_paid_milli=price_milli),
 CHECK((status IN ('playing','paused') AND terminal_at IS NULL AND first_clear=0 AND reward_milli=0) OR
       (status NOT IN ('playing','paused') AND terminal_at>=created_at AND hex(ledger_rows_remaining)='00000000000000000000000000000000')),
 CHECK(first_clear=0 OR status='completed'),
 CHECK(reward_milli=0 OR first_clear=1)
) STRICT;
CREATE UNIQUE INDEX idx_catch_active_user ON game_catch_sessions(user_id) WHERE status IN ('playing','paused');
CREATE INDEX idx_catch_user_history ON game_catch_sessions(user_id,created_at DESC,id);
CREATE INDEX idx_catch_expiry ON game_catch_sessions(expires_at,id) WHERE status IN ('playing','paused');
CREATE INDEX idx_catch_retention ON game_catch_sessions(terminal_at,id) WHERE terminal_at IS NOT NULL;
CREATE INDEX idx_catch_score ON game_catch_sessions(terminal_at,user_id,score DESC) WHERE status IN ('completed','failed');
CREATE TABLE game_catch_inputs (
 session_id TEXT NOT NULL REFERENCES game_catch_sessions(id) ON DELETE CASCADE,
 until_tick INTEGER NOT NULL CHECK(until_tick BETWEEN 1 AND 5400),
 controls_json TEXT NOT NULL CHECK(json_valid(controls_json) AND length(controls_json)<=65536),
 PRIMARY KEY(session_id,until_tick)
);
