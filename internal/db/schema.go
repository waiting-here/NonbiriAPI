// Package db owns the SQLite persistence layer and the generation-one schema.
//
// The DDL below is the single authoritative source used both to initialize a
// fresh database and to derive the exact startup manifest. It is deliberately
// not idempotent: generation-one databases never run DDL again after creation.
//
// Time fields are INTEGER unix seconds. Booleans are INTEGER 0/1.
package db

const generationOneSchema = `
-- ===== users ================================================================
-- Regular users (Discord OAuth) plus a single administrator row configured via
-- environment (discord_id NULL for the admin). is_banned drives instant
-- invalidation of a banned user's caller key at request time.
CREATE TABLE users (
	id                           INTEGER PRIMARY KEY AUTOINCREMENT,
	discord_id                   TEXT UNIQUE,                       -- NULL for the administrator (env-configured, no Discord identity)
	username                     TEXT NOT NULL DEFAULT '',
	avatar                       TEXT NOT NULL DEFAULT '',
	guild_nick                   TEXT NOT NULL DEFAULT '',
	guild_avatar_url             TEXT NOT NULL DEFAULT '',
	is_admin                     INTEGER NOT NULL DEFAULT 0,
	is_banned                    INTEGER NOT NULL DEFAULT 0,         -- instant invalidation: a banned user's caller key fails request-time auth
	banned_reason                TEXT NOT NULL DEFAULT '',
	banned_until                 INTEGER,                           -- while is_banned=1: NULL = permanent ban, non-NULL = unix-seconds deadline; a due deadline is lifted lazily by an atomic conditional UPDATE on read (logged, never alerted)
	auto_banned                  INTEGER NOT NULL DEFAULT 0,        -- whether the most recent effective ban was rule-driven (1) or manual (0); every manual ban clears it
	charity_suspended_until      INTEGER,                           -- charity-eligibility suspension deadline (unix seconds); lifted lazily on read exactly like banned_until
	endpoint_limit               INTEGER,                           -- nullable admin per-user endpoint-count cap override; NULL = global default; clearing to NULL restores the default
	rpm_limit                    INTEGER,                           -- nullable explicit per-user RPM (1..4096); NULL = current site default
	concurrency_limit            INTEGER,                           -- nullable explicit in-flight request cap; NULL = built-in 5
	game_profile_public          INTEGER NOT NULL DEFAULT 0 CHECK(game_profile_public IN (0,1)),
	total_requests               INTEGER NOT NULL DEFAULT 0,
	total_prompt_tokens          INTEGER NOT NULL DEFAULT 0,        -- compatibility mirror: sum of the three input buckets
	total_completion_tokens      INTEGER NOT NULL DEFAULT 0,        -- compatibility mirror: output bucket
	total_unknown_usage_requests INTEGER NOT NULL DEFAULT 0,        -- truncated/no-usage request count; no token values are fabricated
	credits                       INTEGER NOT NULL DEFAULT 0,        -- signed consumption balance in milli-credits; may go negative (settled over-reservation, admin-configured penalties); deliberately NO non-negative CHECK
	donation_credit               INTEGER NOT NULL DEFAULT 0,        -- cumulative donor-reward balance in milli-credits; kept non-negative at the application layer (a result below 0 is rejected)
	level                         INTEGER,                           -- nullable manual level override; NULL = automatic (auto_level high-water mark applies); non-NULL is 1..5 and is administrator-set (level 5 is manual-only); the administrator row never carries a level
	auto_level                    INTEGER NOT NULL DEFAULT 1,        -- persistent automatic-level high-water mark, 1..4; raised lazily by a conditional UPDATE when donation_credit reaches an enabled threshold; there is NO downgrade path in alpha.2 (threshold raises and negative corrections never lower it)
	total_uncached_input_tokens    INTEGER NOT NULL DEFAULT 0,      -- four-bucket accumulator (authoritative)
	total_cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,      -- four-bucket accumulator (authoritative)
	total_cache_read_input_tokens  INTEGER NOT NULL DEFAULT 0,      -- four-bucket accumulator (authoritative)
	total_output_tokens            INTEGER NOT NULL DEFAULT 0,      -- four-bucket accumulator (authoritative)
	lang                         TEXT NOT NULL DEFAULT '',          -- UI preference 'zh'|'en'
	created_at                   INTEGER NOT NULL,
	updated_at                   INTEGER NOT NULL,
	CHECK(endpoint_limit IS NULL OR endpoint_limit BETWEEN 0 AND 10000),
	CHECK(rpm_limit IS NULL OR rpm_limit BETWEEN 1 AND 4096),
	CHECK(concurrency_limit IS NULL OR concurrency_limit BETWEEN 1 AND 100000)
);
CREATE INDEX idx_users_created ON users(created_at);

-- ===== sessions =============================================================
-- Opaque session tokens are looked up by hash. Idle TTL renews on activity,
-- capped by an immutable absolute lifetime. OAuth state is carried on the row;
-- the in-flight handshake is finalized by the Phase 2 auth track.
CREATE TABLE sessions (
	token_hash          TEXT PRIMARY KEY,                          -- lookup key (hash of the opaque token)
	user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	oauth_state         TEXT NOT NULL DEFAULT '',
	last_seen_at        INTEGER NOT NULL,                          -- idle-TTL tracking
	expires_at          INTEGER NOT NULL,                          -- idle expiry, renewed on activity (sliding)
	absolute_expires_at INTEGER NOT NULL,                          -- absolute lifetime cap (created_at + absolute TTL), immutable
	created_at          INTEGER NOT NULL,
	cred_gen            TEXT NOT NULL DEFAULT ''                   -- opaque credential-generation fingerprint (admin sessions only); empty for user sessions. Rotating the administrator password (env change + restart) changes the fingerprint, so a stale admin session no longer matches and is rejected/deleted at the next request. A plain restart with the same password leaves it unchanged.
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_absolute ON sessions(absolute_expires_at);

-- ===== caller_keys ==========================================================
-- Exactly one caller key per user (user_id is the primary key). Regeneration
-- replaces key_hash/display_* in place, so the previous key immediately stops
-- matching lookups. The plaintext is shown once at generation time and is
-- never persisted in a recoverable form (no encrypted copy): only an
-- irretrievable SHA-256 hash plus the head/tail display fragments are stored.
CREATE TABLE caller_keys (
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
CREATE TABLE endpoints (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	connector_type TEXT NOT NULL DEFAULT 'openai-compatible',      -- validated against the authoritative connector registry; unknown types are rejected with no silent protocol fallback
	base_url       TEXT NOT NULL,                                  -- normalized canonical value
	note           TEXT NOT NULL DEFAULT '',
	enabled        INTEGER NOT NULL DEFAULT 1,
	model_fetch_failed    INTEGER NOT NULL DEFAULT 0,              -- fetch flag: set when a model fetch for any of this endpoint's keys failed; cleared by the next successful fetch
	model_fetch_failed_at INTEGER NOT NULL DEFAULT 0,              -- unix seconds of the last failed model fetch; 0 = never failed
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL,
	CHECK(connector_type IN ('openai-compatible','anthropic-compatible'))
);
CREATE INDEX idx_endpoints_user ON endpoints(user_id);

-- ===== endpoint_keys ========================================================
-- One endpoint may hold multiple keys, each independently noted/enabled. The
-- upstream secret is persisted only as a versioned AES-256-GCM envelope;
-- plaintext is never a SQL value. display_head/display_tail carry the first
-- and last few runes of the secret so list views never have to decrypt: the
-- ciphertext stays in the row and is read only by the forwarding rail.
CREATE TABLE endpoint_keys (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	endpoint_id       INTEGER NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
	encrypted_secret TEXT NOT NULL,                                -- nbsec:v2:aes-256-gcm:<nonce>:<ciphertext-and-tag> (raw base64url parts); context-bound; plaintext is never a SQL value
	display_head     TEXT NOT NULL DEFAULT '',                     -- first 4 runes of the upstream secret; persisted so listings never decrypt (renders "xxxx…yyyy")
	display_tail     TEXT NOT NULL DEFAULT '',                     -- last 4 runes of the upstream secret; empty when the secret is too short to reveal a tail without re-covering it
	note             TEXT NOT NULL DEFAULT '',
	enabled          INTEGER NOT NULL DEFAULT 1,
	force_store_false INTEGER NOT NULL DEFAULT 0 CHECK(force_store_false IN (0,1)),
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);
CREATE INDEX idx_endpoint_keys_endpoint ON endpoint_keys(endpoint_id);

-- ===== policy_audits ======================================================
-- Append-only strategy-policy transitions. Resource ids are intentionally
-- untyped integers (the resource may be deleted); actor ownership is kept
-- nullable so account deletion preserves the audit row while removing the
-- actor identity. Writers append rows in the same transaction as the policy
-- mutation and never persist request content, credentials, or tool payloads.
CREATE TABLE policy_audits (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	actor_user_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
	actor_role      TEXT NOT NULL CHECK(actor_role IN ('owner','admin','level5')),
	resource_type   TEXT NOT NULL CHECK(resource_type IN ('endpoint_key','model','charity_model')),
	resource_id     INTEGER NOT NULL CHECK(resource_id > 0),
	policy          TEXT NOT NULL CHECK(policy IN ('force_store_false','flatten_tool_calls')),
	old_value       INTEGER NOT NULL CHECK(old_value IN (0,1)),
	new_value       INTEGER NOT NULL CHECK(new_value IN (0,1)),
	created_at      INTEGER NOT NULL
);
CREATE INDEX idx_policy_audits_resource ON policy_audits(resource_type, resource_id, id);
CREATE INDEX idx_policy_audits_actor ON policy_audits(actor_user_id, id);
CREATE TRIGGER policy_audits_no_update
BEFORE UPDATE ON policy_audits
WHEN NOT (
	OLD.actor_user_id IS NOT NULL AND NEW.actor_user_id IS NULL
	AND NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.actor_user_id)
	AND OLD.actor_role = NEW.actor_role
	AND OLD.resource_type = NEW.resource_type
	AND OLD.resource_id = NEW.resource_id
	AND OLD.policy = NEW.policy
	AND OLD.old_value = NEW.old_value
	AND OLD.new_value = NEW.new_value
	AND OLD.created_at = NEW.created_at
)
BEGIN
	SELECT RAISE(ABORT, 'policy_audits is append-only');
END;
CREATE TRIGGER policy_audits_no_delete
BEFORE DELETE ON policy_audits
BEGIN
	SELECT RAISE(ABORT, 'policy_audits is append-only');
END;

-- ===== fetched_models =======================================================
-- Upstream model cache keyed by (Endpoint, Key) combo: different keys on the
-- same endpoint may legitimately return different model lists. Dropping an
-- endpoint_key cascades to its cache rows (immediate invalidation).
CREATE TABLE fetched_models (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	endpoint_key_id   INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
	upstream_model_id TEXT NOT NULL,                               -- untrusted upstream identifier; bounded/validated at fetch time
	provider          TEXT NOT NULL DEFAULT '',
	fetched_at        INTEGER NOT NULL,
	status            TEXT NOT NULL DEFAULT 'ok',
	UNIQUE(endpoint_key_id, upstream_model_id)
);
CREATE INDEX idx_fetched_models_key ON fetched_models(endpoint_key_id);

-- ===== models ===============================================================
-- A user-defined platform model. The external/routing name is the full
-- "provider/model" string; uniqueness is enforced on that full name (by way of
-- the derived full_name column) so two different provider/model splits that
-- produce the same external name collide and are rejected, eliminating field
-- pair collisions. provider and model are free strings that allow '/'.
CREATE TABLE models (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider       TEXT NOT NULL,                                  -- free string, allows '/'
	model          TEXT NOT NULL,                                  -- free string, allows '/'; length/control-char validation in handler
	full_name      TEXT NOT NULL,                                  -- routing key = provider || '/' || model
	route_strategy TEXT NOT NULL DEFAULT 'ordered',
	silent_retry   INTEGER NOT NULL DEFAULT 0,                    -- explicit retry switch: 0 = fail fast (default, never silently retries), 1 = on a pre-commit upstream failure, try the next binding until success or exhaustion; never retries after any response byte is committed
	flatten_tool_calls INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL,
	UNIQUE(user_id, full_name),
	CHECK(route_strategy IN ('ordered','random')),
	CHECK(silent_retry IN (0,1)),
	CHECK(flatten_tool_calls IN (0,1)),
	CHECK(full_name = provider || '/' || model)
);
CREATE INDEX idx_models_user ON models(user_id);
CREATE INDEX idx_models_user_fullname ON models(user_id, full_name);

-- ===== model_bindings =======================================================
-- Binds a platform model to an upstream (EndpointKey, upstream_model_id). ord
-- orders the "ordered" strategy. Deleting an endpoint_key cascades to its
-- bindings so they no longer participate in routing. A platform model may keep
-- zero bindings (a draft); calling it yields a 503 with a user-issue hint.
CREATE TABLE model_bindings (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	model_id          INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
	endpoint_key_id   INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE,
	upstream_model_id TEXT NOT NULL,
	ord               INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER NOT NULL,
	UNIQUE(model_id, endpoint_key_id, upstream_model_id)
);
CREATE INDEX idx_model_bindings_model ON model_bindings(model_id, ord);

-- ===== request_logs =========================================================
-- Metadata only: no request/response content is persisted (privacy policy).
-- Usage accumulators are written in the same transaction as the log row; the
-- usage_unknown flag counts a request without inventing token values.
-- Token values use the neutral mutually exclusive four-bucket form
-- (uncached_input/cache_write/cache_read/output); the legacy prompt/completion/
-- total columns remain compatibility mirrors (prompt = sum of the three input
-- buckets). Rows written before the four-bucket schema carried no cache split:
-- their historical prompt/completion totals were backfilled into
-- uncached_input_tokens/output_tokens with cache buckets zeroed, and that
-- projection is NEVER valid for retroactive billing of cache-differentiated
-- pricing. route_kind/endpoint_base_url/error_source/charity_reservation_id and
-- the charge columns are provisioned here; later rails write their business
-- values, until then rows carry the neutral defaults.
-- attempt_id is a random opaque correlation id generated by the forwarding
-- stage per attempt (never client input); the partial unique index below
-- gives the persistence layer explicit at-most-once semantics: a retried or
-- duplicated hook delivery for the same attempt cannot double-insert or
-- double-accumulate.
CREATE TABLE request_logs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	attempt_id        TEXT,                                        -- random opaque correlation id; NULL for rows written without one
	user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	model             TEXT NOT NULL DEFAULT '',                    -- platform model full_name
	endpoint_key_id   INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,  -- nullable (routing may fail before key selection); key deletion nulls the ref but keeps the log
	upstream_model_id TEXT NOT NULL DEFAULT '',
	route_kind        TEXT NOT NULL DEFAULT 'personal',            -- 'personal' | 'charity'; charity business routing writes it in a later rail
	endpoint_base_url TEXT NOT NULL DEFAULT '',                    -- bounded canonical base-URL snapshot at dispatch; written by the personal forward path, charity in a later rail
	status_code       INTEGER NOT NULL DEFAULT 0,
	duration_ms       INTEGER NOT NULL DEFAULT 0,
	started_at        INTEGER NOT NULL,
	completed_at      INTEGER NOT NULL DEFAULT 0,
	uncached_input_tokens    INTEGER NOT NULL DEFAULT 0,           -- four-bucket per-request value (authoritative)
	cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,           -- four-bucket per-request value (authoritative)
	cache_read_input_tokens  INTEGER NOT NULL DEFAULT 0,           -- four-bucket per-request value (authoritative)
	output_tokens            INTEGER NOT NULL DEFAULT 0,           -- four-bucket per-request value (authoritative)
	prompt_tokens     INTEGER NOT NULL DEFAULT 0,                  -- compatibility mirror: sum of the three input buckets
	completion_tokens INTEGER NOT NULL DEFAULT 0,                  -- compatibility mirror: output bucket
	total_tokens      INTEGER NOT NULL DEFAULT 0,                  -- compatibility mirror: prompt + completion
	usage_unknown     INTEGER NOT NULL DEFAULT 0,                   -- truncated/no-usage flag
	error_source      TEXT NOT NULL DEFAULT 'platform',             -- 'platform' | 'upstream'; backfilled from error_code='upstream', business writes land in a later rail
	charity_reservation_id INTEGER,                                 -- nullable correlation id, deliberately NO FK (log 30d vs reservation 400d retention)
	original_charge   INTEGER NOT NULL DEFAULT 0,                   -- charity pre-discount price, milli-credits; later rail
	user_charge       INTEGER NOT NULL DEFAULT 0,                   -- actual user payment, milli-credits; later rail
	donor_reward      INTEGER NOT NULL DEFAULT 0,                   -- donor reward accrued, milli-credits; later rail
	error_code        TEXT NOT NULL DEFAULT '',                    -- stable error code
	error_diag        TEXT NOT NULL DEFAULT ''                     -- bounded (<=4096) sanitized upstream diagnostic
);
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id, started_at);
CREATE INDEX idx_request_logs_started ON request_logs(started_at);  -- retention cleanup (30 days)
CREATE UNIQUE INDEX idx_request_logs_attempt ON request_logs(attempt_id) WHERE attempt_id IS NOT NULL;  -- at-most-once accounting

-- ===== user_issues ==========================================================
-- A per-user issue center (e.g. fetch failures, upstream errors). ref links
-- the issue to an endpoint id or model full_name.
CREATE TABLE user_issues (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL,
	message     TEXT NOT NULL DEFAULT '',                           -- bounded, sanitized
	ref         TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	resolved    INTEGER NOT NULL DEFAULT 0,
	resolved_at INTEGER
);
CREATE INDEX idx_user_issues_user_created ON user_issues(user_id, created_at);
CREATE INDEX idx_user_issues_unresolved ON user_issues(resolved, created_at);

-- ===== admin_alerts =========================================================
-- Administrator alert center. subject_user_id is a plain nullable INTEGER
-- with NO foreign key: this lets a late callback use
--   INSERT INTO admin_alerts (...) SELECT ... FROM users WHERE id=?
-- so the insert atomically no-ops when the subject user has already been
-- deleted (linearizing late callbacks against account deletion). The actual
-- suppression SQL is implemented by Phase 4 track T; this rail only provisions
-- the column and the supporting index.
CREATE TABLE admin_alerts (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	kind            TEXT NOT NULL,
	message         TEXT NOT NULL DEFAULT '',                      -- bounded, sanitized
	ref             TEXT NOT NULL DEFAULT '',
	subject_user_id INTEGER,                                        -- NO FK (see above): atomic late-callback suppression hook
	created_at      INTEGER NOT NULL,
	resolved        INTEGER NOT NULL DEFAULT 0,
	resolved_at     INTEGER
);
CREATE INDEX idx_admin_alerts_created ON admin_alerts(created_at);
CREATE INDEX idx_admin_alerts_subject_user ON admin_alerts(subject_user_id);
CREATE INDEX idx_admin_alerts_unresolved ON admin_alerts(resolved, created_at);

-- ===== user_activity_daily =================================================
-- Per-user daily product-activity aggregation behind the administrator
-- activity screen. day is the site-local day key: the unix seconds of that
-- day's local midnight under the explicitly configured fixed site offset
-- (see timezone.go). A day key is never generated while the offset is unset,
-- so no row can ever exist that a later offset change would re-bucket.
-- product_active marks a day on which the user did at least one successful
-- API request, check-in, or console write; protocol failures and pre-dispatch
-- failures never count. The four token buckets mirror request_logs' neutral
-- mutually exclusive form. game_active/game_rounds are reserved columns for a
-- future game rail: alpha.2 never writes a non-zero value into them.
CREATE TABLE user_activity_daily (
	day            INTEGER NOT NULL,                                -- site-local day key (unix seconds of local midnight); no implicit-UTC fallback
	user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	product_active INTEGER NOT NULL DEFAULT 0 CHECK(product_active IN (0,1)),
	api_requests   INTEGER NOT NULL DEFAULT 0,
	uncached_input_tokens    INTEGER NOT NULL DEFAULT 0,
	cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens            INTEGER NOT NULL DEFAULT 0,
	checkins       INTEGER NOT NULL DEFAULT 0,
	console_writes INTEGER NOT NULL DEFAULT 0,
	game_active    INTEGER NOT NULL DEFAULT 0 CHECK(game_active IN (0,1)),  -- reserved; alpha.2 keeps 0
	game_rounds    INTEGER NOT NULL DEFAULT 0,                      -- reserved; alpha.2 keeps 0
	updated_at     INTEGER NOT NULL,
	PRIMARY KEY (day, user_id)
);
CREATE INDEX idx_user_activity_user ON user_activity_daily(user_id);

-- ===== site_activity_daily ==================================================
-- Site-wide daily rollup of user_activity_daily. The counters are the exact
-- sum over that day's per-user rows and are recomputed from the surviving
-- rows inside the same transaction whenever a user is deleted, so a deleted
-- account can never leave a contribution behind. distinct_product_users
-- counts users whose row had product_active=1 that day; it is suppressed to
-- JSON null at the API boundary when it is below the k-anonymity threshold.
-- game_active/game_rounds stay 0 in alpha.2 (reserved).
CREATE TABLE site_activity_daily (
	day            INTEGER PRIMARY KEY,                             -- site-local day key, same semantics as user_activity_daily.day
	product_active INTEGER NOT NULL DEFAULT 0 CHECK(product_active IN (0,1)),
	api_requests   INTEGER NOT NULL DEFAULT 0,
	uncached_input_tokens    INTEGER NOT NULL DEFAULT 0,
	cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens            INTEGER NOT NULL DEFAULT 0,
	checkins       INTEGER NOT NULL DEFAULT 0,
	console_writes INTEGER NOT NULL DEFAULT 0,
	game_active    INTEGER NOT NULL DEFAULT 0 CHECK(game_active IN (0,1)),  -- reserved; alpha.2 keeps 0
	game_rounds    INTEGER NOT NULL DEFAULT 0,                      -- reserved; alpha.2 keeps 0
	distinct_product_users INTEGER NOT NULL DEFAULT 0,
	updated_at     INTEGER NOT NULL
);

-- ===== credit_ledger =======================================================
-- Append-only accounting ledger. Every balance change on users.credits /
-- users.donation_credit commits in the SAME transaction as its ledger row, so
-- a balance can never drift from its audit trail. operation_id is the global
-- idempotency key: a retried write returns the first result instead of
-- applying twice (UNIQUE backs the in-transaction replay check). System
-- operation ids derive from unforgeable internal reservation/check-in
-- identities and always carry the reserved "sys." namespace prefix; client-
-- supplied operation ids (administrator adjustments) must never use that
-- namespace, so the two id spaces cannot collide or overwrite each other.
-- actor_user_id records which administrator made an adjustment (nullable,
-- SET NULL on that account's deletion); reservation_id is a plain correlation
-- id without a FK by design (the reservations table lands with charity routing
-- and outlives individual retention windows differently).
CREATE TABLE credit_ledger (
	id                    INTEGER PRIMARY KEY AUTOINCREMENT,
	operation_id          TEXT NOT NULL UNIQUE,
	user_id               INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	actor_user_id         INTEGER REFERENCES users(id) ON DELETE SET NULL,
	kind                  TEXT NOT NULL CHECK(kind IN ('admin_adjustment','checkin_award','charity_reserve','charity_release','charity_settlement','donor_reward','anti_abuse_penalty','game_reserve','game_settlement','game_release')),
	credits_delta         INTEGER NOT NULL DEFAULT 0,
	donation_credit_delta INTEGER NOT NULL DEFAULT 0,
	credits_after         INTEGER NOT NULL,                      -- balance snapshot after this entry; authoritative for replays
	donation_credit_after INTEGER NOT NULL,
	reservation_id        INTEGER,                               -- nullable correlation id, deliberately NO FK
	game_settlement_id    TEXT CHECK(game_settlement_id IS NULL OR length(game_settlement_id) BETWEEN 1 AND 64),
	reason                TEXT NOT NULL DEFAULT '',              -- bounded human-readable reason (admin adjustments); never secret material
	created_at            INTEGER NOT NULL,
	CHECK(reservation_id IS NULL OR game_settlement_id IS NULL)
);
CREATE INDEX idx_credit_ledger_user ON credit_ledger(user_id, id);

-- ===== checkins =============================================================
-- One check-in per user per site-local day (frozen §2.4). day is the site-local
-- day key: the unix seconds of that local midnight under the explicitly
-- configured fixed site offset (see timezone.go). UNIQUE(user_id, day) IS the
-- one-per-day rule: the write path never pre-checks "already checked in
-- today" (no read-then-write); a race is decided by the constraint and
-- consumes neither the day nor the award. award is the actual granted amount
-- in milli-credits (non-negative, drawn uniformly from the configured
-- inclusive [min,max] range at insert time; the client never supplies it).
-- operation_id is the ledger idempotency key derived from the unforgeable
-- (user, day) identity in the reserved system namespace; the check-in row,
-- the balance update and its credit_ledger row commit in one transaction.
-- Rows live for the account lifecycle (no time-based retention sweep).
CREATE TABLE checkins (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	day          INTEGER NOT NULL,                               -- site-local day key (unix seconds of local midnight); no implicit-UTC fallback
	award        INTEGER NOT NULL CHECK(award >= 0),            -- actual granted amount, milli-credits; drawn in the inclusive [min,max] range at insert time
	operation_id TEXT NOT NULL UNIQUE,                           -- "sys.checkin.<userID>.<day>"; the row's ledger idempotency key
	created_at   INTEGER NOT NULL,
	UNIQUE(user_id, day)
);

-- ===== donations ===========================================================
-- One donation = one owned endpoint plus one or more keys offered for charity
-- routing. The review decision (approve/reject) covers the WHOLE donation;
-- per-key limits live on donation_keys. status is a closed enum enforced by
-- CHECK and re-validated server-side; deleted is the soft-delete terminal
-- state that keeps the bounded audit history. endpoint_id uses SET NULL so a
-- terminal record keeps only its safe base-URL snapshot after the underlying
-- resource is gone; the snapshot never contains note or secret material.
CREATE TABLE donations (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	endpoint_id         INTEGER REFERENCES endpoints(id) ON DELETE SET NULL,
	endpoint_base_url   TEXT NOT NULL,                       -- bounded canonical base-URL snapshot at submission time
	status              TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','deleted')),
	enabled             INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
	description         TEXT NOT NULL,                       -- required donor statement (bounded, control-free)
	review_note         TEXT NOT NULL DEFAULT '',            -- bounded reviewer note addressed to the donor
	reviewed_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	reviewed_by_role    TEXT NOT NULL DEFAULT '' CHECK(reviewed_by_role IN ('','admin','level5')),
	expires_at          INTEGER,                             -- nullable unix seconds; NULL = never expires
	reviewed_at         INTEGER,
	created_at          INTEGER NOT NULL,
	updated_at          INTEGER NOT NULL
);
CREATE INDEX idx_donations_user ON donations(user_id);
CREATE INDEX idx_donations_status ON donations(status);

-- ===== donation_keys ========================================================
-- Per-key charity attributes of one donation. The three limits are per-key by
-- design (frozen §L): limits are properties of the CHARITY USE of a key, not
-- of the key itself, so a donor's own requests are never throttled by them.
-- cap/used/reserved are milli-credit economic values; 0 cap = unlimited.
-- display_head/display_tail are persisted safe fragments so listings never
-- decrypt; they never reconstruct a secret. UNIQUE(donation_id,
-- endpoint_key_id) makes a duplicate selection of the same physical key inside
-- one donation impossible. endpoint_key_id uses SET NULL so terminal history
-- survives the underlying key's deletion without keeping any secret material.
CREATE TABLE donation_keys (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	donation_id       INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE,
	endpoint_key_id   INTEGER REFERENCES endpoint_keys(id) ON DELETE SET NULL,
	display_head      TEXT NOT NULL DEFAULT '',
	display_tail      TEXT NOT NULL DEFAULT '',
	max_concurrency   INTEGER NOT NULL DEFAULT 0 CHECK(max_concurrency >= 0),   -- 0 = unlimited
	rpm_limit         INTEGER NOT NULL DEFAULT 0 CHECK(rpm_limit >= 0),          -- 0 = unlimited
	credits_usage_cap INTEGER NOT NULL DEFAULT 0 CHECK(credits_usage_cap >= 0), -- milli-credits; 0 = unlimited
	credits_used      INTEGER NOT NULL DEFAULT 0 CHECK(credits_used >= 0),
	credits_reserved  INTEGER NOT NULL DEFAULT 0 CHECK(credits_reserved >= 0),
	enabled           INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL,
	UNIQUE(donation_id, endpoint_key_id)
);
CREATE INDEX idx_donation_keys_donation ON donation_keys(donation_id);

-- ===== donation_reviews =====================================================
-- Append-only review audit. Rows are never hard-deleted while the donation
-- exists; only the account-lifecycle cascade removes them. reviewer_role ''
-- marks a system-driven transition (the lazy expiry disable).
CREATE TABLE donation_reviews (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	donation_id      INTEGER NOT NULL REFERENCES donations(id) ON DELETE CASCADE,
	reviewer_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	reviewer_role    TEXT NOT NULL DEFAULT '' CHECK(reviewer_role IN ('','admin','level5')),
	action           TEXT NOT NULL CHECK(action IN ('approve','reject','enable','disable','update','delete')),
	note             TEXT NOT NULL DEFAULT '',
	created_at       INTEGER NOT NULL
);
CREATE INDEX idx_donation_reviews_donation ON donation_reviews(donation_id, id);

-- ===== donation_key_claims ==================================================
-- The active physical-key claim: at most ONE donation_key row may hold a given
-- physical endpoint_key while its donation is pending or approved+enabled.
-- The PRIMARY KEY on endpoint_key_id IS the atomic claim: acquisition is a
-- constrained INSERT decided by SQLite, never a read-then-write. RESTRICT (not
-- CASCADE) makes an inconsistent physical-key deletion fail loudly; the
-- normal in-use guard refuses the delete earlier, and the special account-
-- deletion transaction removes claims before the cascade.
CREATE TABLE donation_key_claims (
	endpoint_key_id INTEGER PRIMARY KEY REFERENCES endpoint_keys(id) ON DELETE RESTRICT,
	donation_key_id INTEGER NOT NULL UNIQUE REFERENCES donation_keys(id) ON DELETE CASCADE,
	claimed_at      INTEGER NOT NULL
);

-- ===== charity_models =======================================================
-- A charity model exposed through donated capacity. full_name carries the
-- fixed '[公益]' prefix and is UNIQUE: the charity namespace can never collide
-- with a personal model name. pricing_mode selects exactly one price table;
-- the non-current table's fields must stay 0 (CHECKs keep them non-negative;
-- the service enforces zeroing) so no field ever has two meanings.
-- discount_percent 100 = original price, 0 = free; the interval is [start,end)
-- and only applies while discount_enabled=1.
CREATE TABLE charity_models (
	id                     INTEGER PRIMARY KEY AUTOINCREMENT,
	provider               TEXT NOT NULL,
	model                  TEXT NOT NULL,
	full_name              TEXT NOT NULL UNIQUE,
	enabled                INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
	pricing_mode           TEXT NOT NULL CHECK(pricing_mode IN ('per_request','per_token')),
	request_user_price     INTEGER NOT NULL DEFAULT 0 CHECK(request_user_price >= 0),
	request_donor_reward   INTEGER NOT NULL DEFAULT 0 CHECK(request_donor_reward >= 0),
	uncached_user_price    INTEGER NOT NULL DEFAULT 0 CHECK(uncached_user_price >= 0),     -- milli-credits per million tokens
	cache_write_user_price INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_user_price >= 0),
	cache_read_user_price  INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_user_price >= 0),
	output_user_price      INTEGER NOT NULL DEFAULT 0 CHECK(output_user_price >= 0),
	uncached_donor_reward    INTEGER NOT NULL DEFAULT 0 CHECK(uncached_donor_reward >= 0),
	cache_write_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_donor_reward >= 0),
	cache_read_donor_reward  INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_donor_reward >= 0),
	output_donor_reward      INTEGER NOT NULL DEFAULT 0 CHECK(output_donor_reward >= 0),
	discount_percent  INTEGER NOT NULL DEFAULT 100 CHECK(discount_percent BETWEEN 0 AND 100),
	discount_start_at INTEGER,
	discount_end_at   INTEGER,
	discount_enabled  INTEGER NOT NULL DEFAULT 0 CHECK(discount_enabled IN (0,1)),
	flatten_tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(flatten_tool_calls IN (0,1)),
	created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL,
	CHECK(full_name = '[公益]' || provider || '/' || model)
);

-- ===== charity_model_bindings ===============================================
-- Binds a charity model to a donated (donation_key, upstream model) pair.
-- Candidate validity (approved+enabled donation, enabled key, live claim,
-- live endpoint/key, fetched upstream id, not expired) is re-verified in SQL
-- on every write and every future routing read; the FK cascades keep stale
-- rows from surviving resource deletion.
CREATE TABLE charity_model_bindings (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	charity_model_id  INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
	donation_key_id   INTEGER NOT NULL REFERENCES donation_keys(id) ON DELETE CASCADE,
	upstream_model_id TEXT NOT NULL,
	ord               INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER NOT NULL,
	UNIQUE(charity_model_id, donation_key_id, upstream_model_id)
);
CREATE INDEX idx_charity_bindings_model ON charity_model_bindings(charity_model_id, ord);

-- ===== charity_model_stats / charity_model_outcomes =========================
-- Last-100-outcome ring buffer per charity model (frozen §J.5): O(1) writes
-- and O(1) success-rate reads, never an aggregation over request_logs.
-- outcomes holds one row per slot (success bit only); stats carries the
-- rolling counters. Both cascade with their model.
CREATE TABLE charity_model_stats (
	model_id      INTEGER PRIMARY KEY REFERENCES charity_models(id) ON DELETE CASCADE,
	next_slot     INTEGER NOT NULL DEFAULT 0 CHECK(next_slot BETWEEN 0 AND 99),
	sample_count  INTEGER NOT NULL DEFAULT 0 CHECK(sample_count BETWEEN 0 AND 100),
	success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count BETWEEN 0 AND 100)
);

CREATE TABLE charity_model_outcomes (
	model_id   INTEGER NOT NULL REFERENCES charity_models(id) ON DELETE CASCADE,
	slot       INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 99),
	success    INTEGER NOT NULL CHECK(success IN (0,1)),
	created_at INTEGER NOT NULL,
	PRIMARY KEY (model_id, slot)
);

-- ===== charity_reservations ================================================
-- One row per logical charity call (frozen implementation contract §2.8).
-- attempt_id is a random opaque correlation id generated per invocation and
-- UNIQUE: a repeated settlement/recovery callback can never apply twice.
-- The pricing/discount snapshot is taken at creation; later configuration
-- changes never affect an in-flight settlement, and recovery reads ONLY these
-- persisted values (never current site_config). donor_user_id uses SET NULL so
-- terminal rows keep the consumer's summary after the donor account is gone;
-- a late reward can never resurrect a deleted donor (the reward write is an
-- atomic no-op against a missing user). charity_model_id/donation_key_id use
-- SET NULL for the same reason. All amounts and token counts are non-negative
-- integers (milli-credits / tokens); there is no float anywhere.
CREATE TABLE charity_reservations (
	id                       INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id                  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	donor_user_id            INTEGER REFERENCES users(id) ON DELETE SET NULL,
	charity_model_id         INTEGER REFERENCES charity_models(id) ON DELETE SET NULL,
	donation_key_id          INTEGER REFERENCES donation_keys(id) ON DELETE SET NULL,
	attempt_id               TEXT NOT NULL UNIQUE,
	model_snapshot           TEXT NOT NULL DEFAULT '',      -- bounded full_name snapshot at creation
	base_url                 TEXT NOT NULL DEFAULT '',      -- bounded canonical base-URL snapshot of the dispatched endpoint (safe metadata)
	upstream_model           TEXT NOT NULL DEFAULT '',
	state                    TEXT NOT NULL CHECK(state IN ('reserved','dispatched','committed','released')),
	pricing_mode             TEXT NOT NULL CHECK(pricing_mode IN ('per_request','per_token')),
	discount_percent         INTEGER NOT NULL CHECK(discount_percent BETWEEN 0 AND 100),
	request_user_price       INTEGER NOT NULL DEFAULT 0 CHECK(request_user_price >= 0),
	request_donor_reward     INTEGER NOT NULL DEFAULT 0 CHECK(request_donor_reward >= 0),
	uncached_user_price      INTEGER NOT NULL DEFAULT 0 CHECK(uncached_user_price >= 0),   -- milli-credits per million tokens
	cache_write_user_price   INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_user_price >= 0),
	cache_read_user_price    INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_user_price >= 0),
	output_user_price        INTEGER NOT NULL DEFAULT 0 CHECK(output_user_price >= 0),
	uncached_donor_reward    INTEGER NOT NULL DEFAULT 0 CHECK(uncached_donor_reward >= 0),
	cache_write_donor_reward INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_donor_reward >= 0),
	cache_read_donor_reward  INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_donor_reward >= 0),
	output_donor_reward      INTEGER NOT NULL DEFAULT 0 CHECK(output_donor_reward >= 0),
	token_reserve_milli      INTEGER NOT NULL DEFAULT 0 CHECK(token_reserve_milli >= 0),    -- global per-token reserve snapshot used at creation
	user_reserved            INTEGER NOT NULL DEFAULT 0 CHECK(user_reserved >= 0),
	key_reserved             INTEGER NOT NULL DEFAULT 0 CHECK(key_reserved >= 0),
	original_charge          INTEGER NOT NULL DEFAULT 0 CHECK(original_charge >= 0),        -- pre-discount actual/settled price, milli-credits
	user_charge              INTEGER NOT NULL DEFAULT 0 CHECK(user_charge >= 0),            -- discounted actual payment, milli-credits
	donor_reward             INTEGER NOT NULL DEFAULT 0 CHECK(donor_reward >= 0),
	usage_uncached_input_tokens    INTEGER NOT NULL DEFAULT 0 CHECK(usage_uncached_input_tokens >= 0),
	usage_cache_write_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(usage_cache_write_input_tokens >= 0),
	usage_cache_read_input_tokens  INTEGER NOT NULL DEFAULT 0 CHECK(usage_cache_read_input_tokens >= 0),
	usage_output_tokens            INTEGER NOT NULL DEFAULT 0 CHECK(usage_output_tokens >= 0),
	usage_unknown            INTEGER NOT NULL DEFAULT 0 CHECK(usage_unknown IN (0,1)),
	created_at               INTEGER NOT NULL,
	dispatched_at            INTEGER,
	finalized_at             INTEGER,
	updated_at               INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_charity_reservations_attempt ON charity_reservations(attempt_id);
CREATE INDEX idx_charity_reservations_state_created ON charity_reservations(state, created_at);
CREATE INDEX idx_charity_reservations_user ON charity_reservations(user_id);
CREATE INDEX idx_charity_reservations_key_state ON charity_reservations(donation_key_id, state);

-- ===== game_settlements =====================================================
-- One economic record per server-authoritative game round. Terminal rows are
-- retained by settled time in the paired round; pending rows are never aged
-- out. Retry metadata contains only a closed error class, never response text.
CREATE TABLE game_settlements (
	id                 TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
	user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	game_type          TEXT NOT NULL CHECK(length(game_type) BETWEEN 1 AND 32),
	game_version       INTEGER NOT NULL CHECK(game_version >= 1),
	state              TEXT NOT NULL CHECK(state IN ('reserved','committed','released')),
	entry_milli        INTEGER NOT NULL CHECK(entry_milli >= 0),
	payout_milli       INTEGER NOT NULL CHECK(payout_milli >= 0),
	auto_settle_at     INTEGER NOT NULL,
	next_attempt_at    INTEGER,
	attempt_count      INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 2147483647),
	last_attempt_at    INTEGER,
	last_error_class   TEXT NOT NULL DEFAULT '' CHECK(last_error_class IN ('','busy','transient','internal')),
	created_at         INTEGER NOT NULL,
	finalized_at       INTEGER,
	updated_at         INTEGER NOT NULL,
	UNIQUE(id, user_id, game_type, game_version),
	CHECK(auto_settle_at >= created_at),
	CHECK(
	  (state='reserved' AND finalized_at IS NULL AND next_attempt_at IS NOT NULL)
	  OR
	  (state IN ('committed','released') AND finalized_at IS NOT NULL AND next_attempt_at IS NULL)
	)
);
CREATE INDEX idx_game_settlements_due ON game_settlements(state, next_attempt_at, id);
CREATE INDEX idx_game_settlements_user_created ON game_settlements(user_id, created_at, id);

-- ===== game_rounds ==========================================================
-- Idempotency keys are persisted only as SHA-256 digests. The partial unique
-- index is the final guard for one unfinished round per user and game.
CREATE TABLE game_rounds (
	id                   TEXT PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 64),
	user_id              INTEGER NOT NULL,
	game_type            TEXT NOT NULL CHECK(length(game_type) BETWEEN 1 AND 32),
	game_version         INTEGER NOT NULL CHECK(game_version >= 1),
	settlement_id        TEXT NOT NULL UNIQUE,
	start_key_hash       BLOB NOT NULL CHECK(typeof(start_key_hash)='blob' AND length(start_key_hash)=32),
	start_request_hash   BLOB NOT NULL CHECK(typeof(start_request_hash)='blob' AND length(start_request_hash)=32),
	event_seq            INTEGER NOT NULL DEFAULT 1 CHECK(event_seq >= 1),
	created_at           INTEGER NOT NULL,
	settled_at           INTEGER,
	revealed_at          INTEGER,
	updated_at           INTEGER NOT NULL,
	UNIQUE(user_id, game_type, start_key_hash),
	FOREIGN KEY(settlement_id, user_id, game_type, game_version)
	  REFERENCES game_settlements(id, user_id, game_type, game_version)
	  ON DELETE CASCADE,
	CHECK(settled_at IS NULL OR settled_at >= created_at),
	CHECK(revealed_at IS NULL OR (settled_at IS NOT NULL AND revealed_at >= settled_at))
);
CREATE UNIQUE INDEX idx_game_rounds_one_pending
ON game_rounds(user_id, game_type)
WHERE settled_at IS NULL;
CREATE INDEX idx_game_rounds_user_created
ON game_rounds(user_id, game_type, created_at, id);
CREATE INDEX idx_game_rounds_retention
ON game_rounds(settled_at, id)
WHERE settled_at IS NOT NULL;
CREATE INDEX idx_game_rounds_unrevealed
ON game_rounds(user_id, game_type, settled_at, created_at, id)
WHERE settled_at IS NOT NULL AND revealed_at IS NULL;

-- ===== game_fishing_outcomes ===============================================
-- The server-generated outcome is strongly typed. Derived flags and economic
-- values are intentionally not duplicated in this row.
CREATE TABLE game_fishing_outcomes (
	round_id     TEXT PRIMARY KEY REFERENCES game_rounds(id) ON DELETE CASCADE,
	bait         TEXT NOT NULL CHECK(bait IN ('worm','lure','premium')),
	species_key  TEXT NOT NULL CHECK(length(species_key) BETWEEN 1 AND 64),
	tier         TEXT NOT NULL CHECK(tier IN ('junk','small','regular','big','giant','legend','treasure')),
	size_cm      INTEGER NOT NULL CHECK(size_cm BETWEEN 0 AND 200),
	CHECK(
	  (tier='junk' AND species_key IN
	    ('boot','seaweed','plastic_bag','branch','old_tire','glasses','phone_case','fry'))
	  OR
	  (tier='small' AND species_key IN
	    ('whitebait','gudgeon','horse_mouth','smelt','loach'))
	  OR
	  (tier='regular' AND species_key IN
	    ('crucian','tilapia','yellow_catfish','ayu','stream_carp'))
	  OR
	  (tier='big' AND species_key IN
	    ('common_carp','snakehead','catfish','mandarin_fish','rainbow_trout'))
	  OR
	  (tier='giant' AND species_key IN
	    ('grass_carp','silver_carp','bighead_carp','black_carp','japanese_eel'))
	  OR
	  (tier='legend' AND species_key IN ('yellowcheek','taimen','koi'))
	  OR
	  (tier='treasure' AND species_key IN ('bottle','clover','shell'))
	),
	CHECK(
	  (tier IN ('junk','treasure') AND size_cm=0)
	  OR (tier='small'   AND size_cm BETWEEN 5 AND 25)
	  OR (tier='regular' AND size_cm BETWEEN 15 AND 35)
	  OR (tier='big'     AND size_cm BETWEEN 30 AND 80)
	  OR (tier='giant'   AND size_cm BETWEEN 60 AND 150)
	  OR (tier='legend'  AND size_cm BETWEEN 100 AND 200)
	)
);

-- ===== game_fishing_best ===================================================
-- Best catches outlive the round retention window; deleting the round only
-- clears its correlation pointer while account deletion cascades the record.
CREATE TABLE game_fishing_best (
	user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	round_id     TEXT REFERENCES game_rounds(id) ON DELETE SET NULL,
	species_key  TEXT NOT NULL CHECK(species_key IN (
	  'whitebait','gudgeon','horse_mouth','smelt','loach',
	  'crucian','tilapia','yellow_catfish','ayu','stream_carp',
	  'common_carp','snakehead','catfish','mandarin_fish','rainbow_trout',
	  'grass_carp','silver_carp','bighead_carp','black_carp','japanese_eel',
	  'yellowcheek','taimen','koi'
	)),
	tier         TEXT NOT NULL CHECK(tier IN ('small','regular','big','giant','legend')),
	size_cm      INTEGER NOT NULL CHECK(size_cm BETWEEN 5 AND 200),
	caught_at    INTEGER NOT NULL,
	CHECK(
	  (tier='small' AND species_key IN
	    ('whitebait','gudgeon','horse_mouth','smelt','loach'))
	  OR
	  (tier='regular' AND species_key IN
	    ('crucian','tilapia','yellow_catfish','ayu','stream_carp'))
	  OR
	  (tier='big' AND species_key IN
	    ('common_carp','snakehead','catfish','mandarin_fish','rainbow_trout'))
	  OR
	  (tier='giant' AND species_key IN
	    ('grass_carp','silver_carp','bighead_carp','black_carp','japanese_eel'))
	  OR
	  (tier='legend' AND species_key IN ('yellowcheek','taimen','koi'))
	),
	CHECK(
	  (tier='small' AND size_cm BETWEEN 5 AND 25)
	  OR (tier='regular' AND size_cm BETWEEN 15 AND 35)
	  OR (tier='big' AND size_cm BETWEEN 30 AND 80)
	  OR (tier='giant' AND size_cm BETWEEN 60 AND 150)
	  OR (tier='legend' AND size_cm BETWEEN 100 AND 200)
	)
);
CREATE INDEX idx_game_fishing_best_rank
ON game_fishing_best(size_cm DESC, caught_at ASC, user_id ASC);

-- ===== site_config ==========================================================
-- Runtime key/value configuration surfaced via the administrator station.
CREATE TABLE site_config (
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
