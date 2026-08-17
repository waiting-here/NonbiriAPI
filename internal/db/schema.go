// Package db owns the SQLite persistence layer: a single-writer connection and
// an idempotent, one-shot schema bootstrap (no versioned migration framework).
//
// The schema below is the Phase 0 contract freeze point for the data model.
// Table and column names are contract drafts; the entity relationships and
// invariants (ownership isolation, uniqueness, cascade deletes, instant
// invalidation, usage accumulators, bounded diagnostics, atomic late-callback
// suppression hooks) are the frozen contract. Per-entity invariants are called
// out in SQL comments next to the columns they constrain.
//
// Time fields are INTEGER unix seconds. Booleans are INTEGER 0/1.
package db

// schema is applied idempotently on every Open via CREATE ... IF NOT EXISTS.
// Alpha allows breaking changes + one-shot manual migration between dev
// versions; a versioned migration mechanism is deferred past v1.0.0.
const schema = `
-- ===== users ================================================================
-- Regular users (Discord OAuth) plus a single administrator row configured via
-- environment (discord_id NULL for the admin). is_banned drives instant
-- invalidation of a banned user's caller key at request time.
CREATE TABLE IF NOT EXISTS users (
	id                           INTEGER PRIMARY KEY AUTOINCREMENT,
	discord_id                   TEXT UNIQUE,                       -- NULL for the administrator (env-configured, no Discord identity)
	username                     TEXT NOT NULL DEFAULT '',
	avatar                       TEXT NOT NULL DEFAULT '',
	is_admin                     INTEGER NOT NULL DEFAULT 0,
	is_banned                    INTEGER NOT NULL DEFAULT 0,         -- instant invalidation: a banned user's caller key fails request-time auth
	banned_reason                TEXT NOT NULL DEFAULT '',
	endpoint_limit               INTEGER,                           -- nullable admin per-user endpoint-count cap override; NULL = global default; clearing to NULL restores the default
	rpm_limit                    INTEGER,                           -- nullable user self-tuned per-minute RPM within the admin cap; NULL = admin global default
	total_requests               INTEGER NOT NULL DEFAULT 0,
	total_prompt_tokens          INTEGER NOT NULL DEFAULT 0,
	total_completion_tokens      INTEGER NOT NULL DEFAULT 0,
	total_unknown_usage_requests INTEGER NOT NULL DEFAULT 0,        -- truncated/no-usage request count; no token values are fabricated
	lang                         TEXT NOT NULL DEFAULT '',          -- UI preference 'zh'|'en'
	created_at                   INTEGER NOT NULL,
	updated_at                   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_created ON users(created_at);

-- ===== sessions =============================================================
-- Opaque session tokens are looked up by hash. Idle TTL renews on activity,
-- capped by an immutable absolute lifetime. OAuth state is carried on the row;
-- the in-flight handshake is finalized by the Phase 2 auth track.
CREATE TABLE IF NOT EXISTS sessions (
	token_hash          TEXT PRIMARY KEY,                          -- lookup key (hash of the opaque token)
	user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	oauth_state         TEXT NOT NULL DEFAULT '',
	last_seen_at        INTEGER NOT NULL,                          -- idle-TTL tracking
	expires_at          INTEGER NOT NULL,                          -- idle expiry, renewed on activity (sliding)
	absolute_expires_at INTEGER NOT NULL,                          -- absolute lifetime cap (created_at + absolute TTL), immutable
	created_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_absolute ON sessions(absolute_expires_at);

-- ===== caller_keys ==========================================================
-- Exactly one caller key per user (user_id is the primary key). Regeneration
-- replaces key_hash/display_* in place, so the previous key immediately stops
-- matching lookups. The plaintext is shown once at generation time and is
-- never persisted in a recoverable form (no encrypted copy): only an
-- irretrievable SHA-256 hash plus the head/tail display fragments are stored.
CREATE TABLE IF NOT EXISTS caller_keys (
	user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	key_hash      TEXT NOT NULL UNIQUE,                             -- SHA-256 of plaintext, for lookup
	display_head  TEXT NOT NULL DEFAULT '',                         -- first 4 chars of the secret portion (renders "nbk_xxxx...")
	display_tail  TEXT NOT NULL DEFAULT '',                         -- last 4 chars of the key
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);

-- ===== endpoints ============================================================
-- A user's configured upstream endpoint. base_url stores the normalized
-- canonical value; the canonicalization function is a Phase 1 egress invariant.
-- Multiple endpoints MAY share the same base_url (no unique constraint on
-- (user_id, base_url)); each keeps independent note/enabled state.
CREATE TABLE IF NOT EXISTS endpoints (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	connector_type TEXT NOT NULL DEFAULT 'openai-compatible',      -- validated against the authoritative connector registry; unknown types are rejected with no silent protocol fallback
	base_url       TEXT NOT NULL,                                  -- normalized canonical value
	note           TEXT NOT NULL DEFAULT '',
	enabled        INTEGER NOT NULL DEFAULT 1,
	model_fetch_failed    INTEGER NOT NULL DEFAULT 0,              -- fetch flag: set when a model fetch for any of this endpoint's keys failed; cleared by the next successful fetch
	model_fetch_failed_at INTEGER NOT NULL DEFAULT 0,              -- unix seconds of the last failed model fetch; 0 = never failed
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_endpoints_user ON endpoints(user_id);
-- Alpha one-shot manual migration (dev databases created before this rail):
--   ALTER TABLE endpoints ADD COLUMN model_fetch_failed    INTEGER NOT NULL DEFAULT 0;
--   ALTER TABLE endpoints ADD COLUMN model_fetch_failed_at INTEGER NOT NULL DEFAULT 0;

-- ===== endpoint_keys ========================================================
-- One endpoint may hold multiple keys, each independently noted/enabled. The
-- upstream secret is persisted only as a versioned AES-256-GCM envelope;
-- plaintext is never a SQL value. display_head/display_tail carry the first
-- and last few runes of the secret so list views never have to decrypt: the
-- ciphertext stays in the row and is read only by the forwarding rail.
CREATE TABLE IF NOT EXISTS endpoint_keys (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	endpoint_id       INTEGER NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
	encrypted_secret TEXT NOT NULL,                                -- nbsec:v1:aes-256-gcm:<nonce>:<ciphertext-and-tag> (raw base64url parts); plaintext is never a SQL value
	display_head     TEXT NOT NULL DEFAULT '',                     -- first 4 runes of the upstream secret; persisted so listings never decrypt (renders "xxxx…yyyy")
	display_tail     TEXT NOT NULL DEFAULT '',                     -- last 4 runes of the upstream secret; empty when the secret is too short to reveal a tail without re-covering it
	note             TEXT NOT NULL DEFAULT '',
	enabled          INTEGER NOT NULL DEFAULT 1,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_endpoint_keys_endpoint ON endpoint_keys(endpoint_id);

-- ===== fetched_models =======================================================
-- Upstream model cache keyed by (Endpoint, Key) combo: different keys on the
-- same endpoint may legitimately return different model lists. Dropping an
-- endpoint_key cascades to its cache rows (immediate invalidation).
CREATE TABLE IF NOT EXISTS fetched_models (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	endpoint_key_id   INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
	upstream_model_id TEXT NOT NULL,                               -- untrusted upstream identifier; bounded/validated at fetch time
	provider          TEXT NOT NULL DEFAULT '',
	fetched_at        INTEGER NOT NULL,
	status            TEXT NOT NULL DEFAULT 'ok',
	UNIQUE(endpoint_key_id, upstream_model_id)
);
CREATE INDEX IF NOT EXISTS idx_fetched_models_key ON fetched_models(endpoint_key_id);

-- ===== models ===============================================================
-- A user-defined platform model. The external/routing name is the full
-- "provider/model" string; uniqueness is enforced on that full name (by way of
-- the derived full_name column) so two different provider/model splits that
-- produce the same external name collide and are rejected, eliminating field
-- pair collisions. provider and model are free strings that allow '/'.
CREATE TABLE IF NOT EXISTS models (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider       TEXT NOT NULL,                                  -- free string, allows '/'
	model          TEXT NOT NULL,                                  -- free string, allows '/'; length/control-char validation in handler
	full_name      TEXT NOT NULL,                                  -- routing key = provider || '/' || model
	route_strategy TEXT NOT NULL DEFAULT 'ordered',
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL,
	UNIQUE(user_id, full_name),
	CHECK(route_strategy IN ('ordered','random')),
	CHECK(full_name = provider || '/' || model)
);
CREATE INDEX IF NOT EXISTS idx_models_user ON models(user_id);
CREATE INDEX IF NOT EXISTS idx_models_user_fullname ON models(user_id, full_name);

-- ===== model_bindings =======================================================
-- Binds a platform model to an upstream (EndpointKey, upstream_model_id). ord
-- orders the "ordered" strategy. Deleting an endpoint_key cascades to its
-- bindings so they no longer participate in routing. A platform model may keep
-- zero bindings (a draft); calling it yields a 503 with a user-issue hint.
CREATE TABLE IF NOT EXISTS model_bindings (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	model_id          INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
	endpoint_key_id   INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
	upstream_model_id TEXT NOT NULL,
	ord               INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER NOT NULL,
	UNIQUE(model_id, endpoint_key_id, upstream_model_id)
);
CREATE INDEX IF NOT EXISTS idx_model_bindings_model ON model_bindings(model_id, ord);

-- ===== request_logs =========================================================
-- Metadata only: no request/response content is persisted (privacy policy).
-- Usage accumulators are written in the same transaction as the log row; the
-- usage_unknown flag counts a request without inventing token values.
CREATE TABLE IF NOT EXISTS request_logs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	model             TEXT NOT NULL DEFAULT '',                    -- platform model full_name
	endpoint_key_id   INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,  -- nullable (routing may fail before key selection); key deletion nulls the ref but keeps the log
	upstream_model_id TEXT NOT NULL DEFAULT '',
	status_code       INTEGER NOT NULL DEFAULT 0,
	duration_ms       INTEGER NOT NULL DEFAULT 0,
	started_at        INTEGER NOT NULL,
	completed_at      INTEGER NOT NULL DEFAULT 0,
	prompt_tokens     INTEGER NOT NULL DEFAULT 0,
	completion_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens      INTEGER NOT NULL DEFAULT 0,
	usage_unknown     INTEGER NOT NULL DEFAULT 0,                   -- truncated/no-usage flag
	error_code        TEXT NOT NULL DEFAULT '',                    -- stable error code
	error_diag        TEXT NOT NULL DEFAULT ''                     -- bounded (<=4096) sanitized upstream diagnostic
);
CREATE INDEX IF NOT EXISTS idx_request_logs_user_started ON request_logs(user_id, started_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_started ON request_logs(started_at);  -- retention cleanup (30 days)

-- ===== user_issues ==========================================================
-- A per-user issue center (e.g. fetch failures, upstream errors). ref links
-- the issue to an endpoint id or model full_name.
CREATE TABLE IF NOT EXISTS user_issues (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL,
	message     TEXT NOT NULL DEFAULT '',                           -- bounded, sanitized
	ref         TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	resolved    INTEGER NOT NULL DEFAULT 0,
	resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_user_issues_user_created ON user_issues(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_user_issues_unresolved ON user_issues(resolved, created_at);

-- ===== admin_alerts =========================================================
-- Administrator alert center. subject_user_id is a plain nullable INTEGER
-- with NO foreign key: this lets a late callback use
--   INSERT INTO admin_alerts (...) SELECT ... FROM users WHERE id=?
-- so the insert atomically no-ops when the subject user has already been
-- deleted (linearizing late callbacks against account deletion). The actual
-- suppression SQL is implemented by Phase 4 track T; this rail only provisions
-- the column and the supporting index.
CREATE TABLE IF NOT EXISTS admin_alerts (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	kind            TEXT NOT NULL,
	message         TEXT NOT NULL DEFAULT '',                      -- bounded, sanitized
	ref             TEXT NOT NULL DEFAULT '',
	subject_user_id INTEGER,                                        -- NO FK (see above): atomic late-callback suppression hook
	created_at      INTEGER NOT NULL,
	resolved        INTEGER NOT NULL DEFAULT 0,
	resolved_at     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_admin_alerts_created ON admin_alerts(created_at);
CREATE INDEX IF NOT EXISTS idx_admin_alerts_subject_user ON admin_alerts(subject_user_id);
CREATE INDEX IF NOT EXISTS idx_admin_alerts_unresolved ON admin_alerts(resolved, created_at);

-- ===== site_config ==========================================================
-- Runtime key/value configuration surfaced via the administrator station.
CREATE TABLE IF NOT EXISTS site_config (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL DEFAULT 0
);
-- Known keys (seeded/maintained by handlers in later rails):
--   site_name, default_locale,
--   default_endpoint_limit            global endpoint-count cap,
--   default_rpm_per_user              global per-user RPM default,
--   global_rpm                        site-wide RPM cap,
--   default_per_endpoint_concurrency  global per-endpoint concurrency cap (keyed by normalized base_url),
--   egress_global_concurrency          egress global concurrency gate,
--   discord_guild_id                  registration gate (blank => registration paused),
--   discord_role_id                   registration gate (blank => registration paused; BOTH guild+role must match to allow new registration),
--   alert_prefs_*                     administrator alert-center preferences.
`
