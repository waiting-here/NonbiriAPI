# NonbiriAPI HTTP API Contract (v1.0.0-alpha.1 + unreleased amendments)

- Status: **v1.0.0-alpha.1 release contract with explicitly marked unreleased amendments (security hardening and alpha.2 product work)**
- Scope: v1.0.0-alpha.1 surface only. `/v1/chat/completions` and `/v1/models` are the only
  OpenAI-compatible exit endpoints in alpha.1; embeddings / images / audio / files /
  moderation / batch are deferred past the alpha.
- Authority: this document describes the surface shipped by tag `v1.0.0-alpha.1`, aligned to the frozen requirements, accepted credential/egress/data-lifecycle decisions, and the emitted stable-code set of `internal/httperr`. Text explicitly labeled **Unreleased security amendment** describes this development branch and is not a claim about the published tag. Documentation errata may correct an omitted or misstated shipped field without changing runtime behavior; other changes to endpoint paths, JSON fields, stable codes, auth behavior, cache policy, or diagnostic limits require a subsequent release contract and changelog entry.

## 1. Conventions

### 1.1 Stations and hosts

Two physically host-isolated stations share one binary:

| Station | Host | Serves |
|---|---|---|
| User station | `<userHost>` (derived from the configured site origin) | the OpenAI-compatible exit (`/v1/*`), normal-user self-service API (`/api/*`), the user SPA |
| Admin station | `<adminHost>` (`admin.` prefix of, or env override on, `<userHost>`) | administrator API (`/admin/api/*`), the admin SPA |

Host selection is a security control (trusted-proxy forwarding, distinct-host enforcement,
client-IP/CIDR validation) and is enforced before station routing. The contracts below assume a
request arrived at the correct station host. A request reaching the wrong host is not
authorized to reach another station's API.

`GET /healthz` is an unauthenticated liveness probe on both hosts; it never touches the database and returns `{"status":"ok"}`.

`GET /api/config` on the user host and `GET /admin/api/config` on the admin host are unauthenticated, `no-store` bootstrap endpoints. They return only `site_name`, `site_logo_url`, `default_locale`, the four bounded legal override texts, `legal_authoritative_locale`, `maintenance_mode`, and `registration_open`. Operational limits, Discord gate IDs, alert preferences, and secrets are never projected. Other methods return `method_not_allowed`.

### 1.2 Authentication schemes

| Scheme | Where used | Carrier |
|---|---|---|
| CallerKey Bearer | platform exit `/v1/*` | `Authorization: Bearer nbk_<secret>` (the user's at-most-one active call key) |
| User session | user station `/api/*` | opaque session cookie (Secure, HttpOnly, SameSite), idle + absolute TTL |
| Admin session | admin station `/admin/api/*` | opaque admin session cookie (same attributes) |
| Elevated (two-step) | self-service export / delete; destructive admin actions | short-lived elevated-action capability bound to an active session, obtained via a two-step endpoint |

User session cookies are host-only and use `Path=/api`; administrator session cookies are
host-only and use `Path=/admin`. Both are Secure when the fixed configured site origin (or
trusted edge protocol context) is HTTPS, HttpOnly, and SameSite=Lax. OAuth state cookies are
host-only, HttpOnly, SameSite=Lax, short-lived, and scoped to `/api/auth/discord`.

Unsafe methods on the cookie-authenticated `/api/*` and `/admin/api/*` surfaces also pass a
same-origin browser boundary. An `Origin` header must match the validated request scheme,
host, and effective port; Fetch Metadata, when present, must report `same-origin`. A request
carrying a cookie but neither signal is refused. This closes sibling-host CSRF because
SameSite is site-scoped, not origin-scoped. The Bearer-authenticated `/v1/*` surface is not
subject to this cookie boundary.

A normal user may hold at most one active caller key (`nbk_` prefix, no scope binding). It is created on explicit generation; regeneration invalidates the previous key the instant the new row is written; the plaintext is shown once
at generation time and never persisted in a recoverable form (only an irretrievable SHA-256
hash and head/tail display fragments are stored).

A banned user's caller key is treated as invalid at request time, so a banned key fails
platform-exit auth with `unauthorized`. Banning also keeps the user's sessions out of further
use.

A ban may be permanent or carry an expiry deadline (`banned_until`, unix seconds). A ban whose
deadline has passed counts as not banned at every authentication check and is lifted lazily by
an atomic conditional update on read (logged, never alerted); a permanent ban never lifts
itself. Banning always deletes the user's sessions and caller key in the same transaction, so
an expiring temporary ban does not resurrect old credentials: the user signs in and generates
a new caller key.

### 1.3 Cache policy

`Cache-Control: no-store` is the default for the whole API: every JSON success written through
the shared response helper and every error envelope carries it. It is mandatory (and not
overridable) for all key / secret / configuration endpoints so a secret or a stale config can
never be cached. Hashed static SPA assets keep the file server's default caching; the SPA
`index.html` and SPA route fallbacks are served `no-store` so a previous binary's bundle can
never shadow a redeploy.

### 1.4 Stable error envelope

Every error response is a single JSON object:

```json
{ "error": { "code": "<stable>", "source": "platform|upstream", "message": "<human, bounded>",
             "diag": "<optional, bounded upstream context>", "request_id": "<optional>" } }
```

- `code` is a stable machine identifier. Codes may be added; an existing one is never
  redefined for a new meaning. Unknown codes map to HTTP 500 internally.
- `source` is always present and is exactly `platform` or `upstream`. It is derived at the
  single wire sink from the code and locked to it — `upstream` for `upstream`, `platform` for
  every other stable code (and the unknown-code fallback) — so a caller never has to infer
  origin from a message prefix, and a hand-constructed error can neither omit `source` nor
  forge a different attribution than its code. An explicit caller-set `source` can confirm but
  never override the code-derived value: an upstream code is always `upstream` and a platform
  code is always `platform`; any explicit value that disagrees (or is invalid) is dropped in
  favor of the code-derived default. *(Unreleased security amendment: the `source` field and
  its locked-to-code derivation are not part of the published `v1.0.0-alpha.1` contract.)*
- `message` is bounded to 1000 runes and stripped of all C0 control characters and DEL; it
  carries a short human-safe summary and **never** raw upstream identifiers or text.
- `diag` is bounded to 4096 bytes (the untrusted-upstream diagnostic resource limit) by
  the shared `internal/diagnostic` boundary. Invalid UTF-8 is repaired (runs of invalid bytes
  collapse to one U+FFFD); CR, LF, and TAB become spaces so untrusted text cannot forge log
  lines; other C0 control characters and DEL are stripped; truncation ends on a rune boundary
  and is marked. It is omitted when empty. It is the only place raw upstream identifiers/error
  text may appear, and only after that shared bounding and sanitization. Frontends render
  `diag` as text, never as HTML or formula-interpreted content.
- `limit` and `resource` are optional fields carried only by
  `resource_limit_exceeded` (§1.6): `limit` is the effective integer cap in effect
  at the refusal and `resource` is the stable resource name (`endpoint_key`, `model`,
  `endpoint`, `endpoint_key`, `model`, `binding`). They are omitted by every other code.

### 1.5 Layered diagnostic recording

Diagnostics are recorded at two layers:

- **Edge response** (`error.diag`, see above): bounded/sanitized upstream context surfaced to
  the caller (operator or user where appropriate).
- **Persistence** (`request_logs.error_code`, `request_logs.error_diag`): every forwarded request
  that crosses the response-byte accounting boundary logs its stable `error_code` and bounded
  `error_diag` (≤4096 bytes) in the same transaction as its usage accumulators; failures before
  any response byte are not accounted or persisted. `request_logs` holds metadata only — no
  request or response content is ever persisted (privacy policy).

Logging additionally redacts by field name at the structured logger: keys and token fields are
matched by a `_key` / token-name pattern and never emitted in plaintext.

### 1.6 Stable code set

Codes below are the emitted set; HTTP status is derived from the code. Route tables emphasize normal/domain failures; shared `internal`, `service_unavailable`, `method_not_allowed`, station `forbidden`, and body-size failures may also arise at their corresponding cross-cutting boundaries.

| code | HTTP | Used for |
|---|---|---|
| `internal` | 500 | unexpected server error |
| `invalid_request` | 400 | malformed request body, invalid argument, validation failure |
| `unauthorized` | 401 | missing / invalid / banned caller key or session |
| `forbidden` | 403 | valid identity, not allowed for this resource |
| `not_found` | 404 | unknown model, endpoint, key, binding, or resource |
| `conflict` | 409 | uniqueness violation or illegal state transition |
| `method_not_allowed` | 405 | path matched but method did not |
| `rate_limited` | 429 | per-user RPM, global RPM, concurrency gate exhausted, or the per-client-IP OAuth start admission throttle (login start / elevation start) penalizing a caller |
| `payload_too_large` | 413 | request or generated export exceeds the configured size bound |
| `elevated_required` | 403 | destructive/export action lacks a valid, unexpired, unused capability bound to the current session |
| `unbound_model` | 503 | resolved a platform model that has no usable binding (zero-binding draft / all bindings filtered out); a user issue is recorded |
| `upstream` | 502 | upstream error after the commit boundary, a single attempt's upstream error with silent retry off, or all bindings exhausted under silent retry |
| `service_unavailable` | 503 | internal service unavailability, or the server-side maintenance gate refusing user-station API and `/v1/*` while `maintenance_mode` is on (§6) |
| `resource_limit_exceeded` | 422 | a per-parent resource count reached the configured cap: endpoints per user (effective minimum of `default_endpoint_limit` and the user override), endpoint keys per endpoint (`default_endpoint_key_limit`), platform models per user (`default_model_limit`), or bindings per model (`default_binding_limit`); the envelope carries `limit` (the effective cap) and `resource` (the stable name); the count and cap are read inside the same transaction as the insert so a concurrent add cannot breach the cap; existing rows are retained |
| `insufficient_credits` | 403 | reserved stable code for the economy rail (悠哉积分不足); `source` is `platform`; the business trigger is wired in a later phase |
| `feature_disabled` | 403 | stable code for a feature gate; `source` is `platform`; the check-in rail (§3.11) is the wired trigger: every unavailable cause — timezone not configured, check-in disabled, a level-gated denial, or a corrupt configuration — is the identical envelope (code, status, message, source) so the reason is never revealed |
| `charity_suspended` | 403 | reserved stable code for a charity suspension; `source` is `platform`; the business trigger is wired in a later phase |
| `already_checked_in` | 409 | the user already checked in on the current site-local day (今日已签到); the day was consumed by the earlier successful check-in, not by this attempt; `source` is `platform` |
| `checkin_cap_reached` | 403 | the user's credit balance has reached the configured check-in threshold (`credits_cap_milli`) at an effective level below the bypass (≥3); the refusal does NOT consume the day; the message contains 悠哉积分已达签到上限; `source` is `platform` |

*(Unreleased security amendment: the `service_unavailable` maintenance-gate meaning and
the `insufficient_credits` / `feature_disabled` / `charity_suspended` reserved codes are not
part of the published `v1.0.0-alpha.1` contract; no business trigger is wired yet.)*

## 2. Platform exit — `/v1/*` (CallerKey Bearer)

Auth: `Authorization: Bearer nbk_<secret>`. The secret is the caller's per-user call key
(never an upstream key). All responses are `no-store`.

### 2.1 `POST /v1/chat/completions`

OpenAI-compatible chat completions, streaming and non-streaming.

Request body uses the OpenAI Chat Completions shape. Ordinary call parameters are preserved and omitted parameters use upstream defaults; the routing `model`, server-authoritative `safety_identifier`, and streaming usage option are the explicit exceptions described below:

| Field | Required | Notes |
|---|---|---|
| `model` | yes | a platform model full name `provider/model`; resolved against the caller's models only |
| `stream` | no | `true` selects SSE; omitted/`false` selects the single JSON response |
| other fields | no | preserved, except caller `safety_identifier` is overwritten and streaming requests authoritatively merge `stream_options.include_usage=true` while retaining other `stream_options` members |

Server-side behavior (shipped contract):

1. Resolve `model` to one of the caller's platform models → its binding set. Cross-user
   resources never enter the candidate set.
2. Select a binding by the model's `route_strategy`:
   - `ordered` — try bindings by `ord` ascending; advance only after a failure.
   - `random` — pure random pick per call (stateless).
3. Retry boundary = **committing no response-body bytes to the client yet**:
   - Failure before the boundary (connection / DNS / upstream error status with nothing
     streamed): by default return the error immediately; if the model's silent-retry option is
     on, record a user issue and try the next binding until success or exhaustion.
   - Failure after the boundary (streaming already started, first byte already sent): **no
     retry**; end the current stream with an error.
   Non-stream requests can retry up to the boundary, same option/default.
   Route resolution, all attempts, and backoff share one five-minute aggregate deadline; a
   candidate set never multiplies the single-attempt timeout into a longer logical request.
4. Inject the OpenAI `safety_identifier` field for upstream per-user risk attribution.

**Unreleased security amendment:** the development branch sends only the versioned form
`nbu_v2_` followed by the 52-character RFC 4648 base32 (without padding) encoding of an
HMAC-SHA-256 digest. The process derives a dedicated 32-byte key from the Vault with purpose
`nonbiriapi:safety-identifier:v2`; the HMAC message is that fixed purpose followed by the
calling user's positive int64 id as exactly eight big-endian bytes. The resulting 59-character
pseudonym is stable for the same deployment key and user, differs across users or deployment
keys, and contains neither the raw id nor a publicly verifiable unkeyed digest. A caller-supplied
`safety_identifier` is overwritten, and no legacy identifier is also sent.

The derived key exists only in process memory: it is not persisted, logged, returned by the
platform API, or sent upstream. Missing or invalid key injection fails application wiring; once
the forwarding service begins shutdown it admits no new operations, waits for in-flight
operations, then best-effort clears its retained key copy. This rollout intentionally rotates
all identifiers emitted by the published alpha.1 implementation once. Upstream risk history
keyed only by the previous value will not automatically carry over, because there is no dual-send
or legacy fallback. After that cutover, retaining the same deployment master key preserves
identifier continuity; a deployment using a different master key produces a different pseudonym.

Response — non-stream (`stream` false/omitted): standard OpenAI Chat Completions response,
including `usage` (`prompt_tokens`, `completion_tokens`, `total_tokens`) when the upstream
returned it. HTTP 200 alone is not success; success requires a complete, protocol-valid body.

Response — stream (`stream: true`): `Content-Type: text/event-stream`. A sequence of
`data: <json chunk>\n\n` frames shaped as OpenAI Chat Completions chunks, terminated by
`data: [DONE]\n\n`. When the caller asked for usage and the upstream supports it, an accurate
`usage` is taken from the terminal usage chunk (which arrives after the last content chunk,
before `[DONE]`, so the stream is always read to termination). HTTP 200 is not success: an explicit `[DONE]` frame is required; an incomplete stream is never synthesized as a
success. If the client disconnects, the cancellation is propagated upstream and concurrent
slots / reservations are released; any unreadable `usage` is recorded with the
`usage_unknown` flag rather than fabricated token values.

Usage accounting (internal): when the upstream returns a protocol-valid `usage` object, it is
normalized into mutually exclusive non-negative token buckets — uncached input
(`prompt_tokens − cached_tokens − cache_write_tokens`), cache write
(`prompt_tokens_details.cache_write_tokens`, 0 when absent), cache read
(`prompt_tokens_details.cached_tokens`), and output (`completion_tokens`). A present but
malformed `usage` (negative or non-integer token values, cache sub-items exceeding the prompt
total, contradictory repeated values) degrades the whole request's usage to `usage_unknown`;
no token value is ever fabricated. The caller-visible wire shapes are unchanged.

Before a response crosses the client boundary, upstream key plaintext and ciphertext are
checked both as literal wire bytes and as decoded JSON string channels. The semantic check
keeps bounded rolling state per normalized JSON path, so splitting a credential across
successive `delta.content`, tool-argument, or content-part chunks is rejected before the
completing fragment is written. This guard is a layer of defense-in-depth, not a general
data-loss-prevention filter: it matches only the exact known credential material handed to
the upstream in the same request (and its close JSON/SSE fragments). It does not detect a
key that was encoded, truncated, or otherwise transformed, and it does not detect arbitrary
other sensitive data the upstream may return. A caller key is therefore still handled as a
sensitive credential and protected by the reverse proxy, storage, and logging boundary; the
guard is one layer, not a guarantee.

Stable error codes at this endpoint:

| code | HTTP | When |
|---|---|---|
| `unauthorized` | 401 | missing / invalid / banned caller key |
| `forbidden` | 403 | request reached a forbidden station/security boundary; model ownership itself is resolved only inside the authenticated caller's tenant |
| `not_found` | 404 | `model` did not resolve to one of the caller's platform models |
| `rate_limited` | 429 | per-user RPM, global RPM, or concurrency exhausted |
| `payload_too_large` | 413 | request body exceeds the configured size bound |
| `unbound_model` | 503 | the resolved model has no usable binding (zero-binding draft / all bindings filtered out) — a user issue is recorded |
| `upstream` | 502 | upstream error after the commit boundary, single-attempt upstream error with silent retry off, or all bindings exhausted under silent retry |
| `internal` | 500 | unexpected failure |

(within an already-started stream, an error is emitted as an SSE error frame rather than a
fresh HTTP error envelope, because the response status and headers have already been written.
The SSE error frame carries the same stable `{"error":{"code","source","message"}}` shape as
the JSON envelope — `source` is derived at the shared wire sink, not from a message prefix —
and never carries a free-form upstream body, the legacy `type` field, or the terminal `[DONE]`
marker; an incomplete stream is never synthesized as a success.)

### 2.2 `GET /v1/models`

Lists the caller's own platform models (not upstream models), in the OpenAI `list` shape.

Response (`200`, `no-store`):

```json
{ "object": "list",
  "data": [ { "id": "provider/model", "object": "model",
              "created": 1700000000, "owned_by": "provider" } ] }
```

- `id` is the platform model full name `provider/model` (the exact routing key).
- `owned_by` is the model's `provider`.
- Only the caller's models appear; cross-user resources are never candidates.

Stable error codes: `unauthorized` (401); `rate_limited` (429); `internal` (500).

## 3. User station — `/api/*` (user session cookie)

Auth: a valid user session cookie. Banned users are denied. All responses are `no-store`.

### 3.1 Auth & session

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/auth/discord/start` | none | — | `302` to Discord's authorize URL; sets the OAuth `state` cookie (HMAC, short TTL, HttpOnly, SameSite). Before a state is issued, a per-client-IP admission throttle (configurable via site_config `oauth_start_rate_*`; `oauth_start_rate_limit=0` disables it) is applied as a second layer behind the reverse-proxy per-IP limit | `rate_limited` (429, with `Retry-After`) when the per-client-IP admission limit is exceeded; `service_unavailable` (503) if the Discord end is misconfigured or the admission throttle's bounded entry store is full (fail-closed, never evicts a live state) |
| `GET` | `/api/auth/discord/callback` | OAuth `state` cookie | query: `code`, `state` | on success: sets the user session cookie and `302` to the user SPA; on mismatch/failure: `400 invalid_request` / `401 unauthorized` | `invalid_request`, `unauthorized`, `conflict`, `service_unavailable` |
| `GET` | `/api/session` | user session | — | `200 {user:{id,username,avatar,avatar_url,guild_nick,guild_avatar_url,lang,is_banned,blocked_reason?,banned_until,charity_suspended_until,endpoint_limit,rpm_limit,credits,donation_credit,effective_level,manual_level,created_at}}`; `banned_until`/`charity_suspended_until` are nullable unix seconds; `credits`/`donation_credit` are canonical decimal milli-credit strings (`credits` may carry `-`); `effective_level` is the server-authoritative level resolved for this request (a due automatic promotion is lazily persisted by that same resolution); `manual_level` is the nullable manual override (`null` = automatic) and is display/state data only — capability decisions are made server-side per use | `unauthorized` |
| `POST` | `/api/auth/logout` | user session | — | `204`; clears the session cookie | `unauthorized` |

Registration gate (Discord): registration is allowed only when `registration_open` is true and both the configured `discord_guild_id` and `discord_role_id` match the Discord identity. If either ID is blank or registration is closed, registration is paused (the callback returns `service_unavailable` for a new identity),
while already-registered users can still complete the same OAuth login. The start endpoint
may still issue a short-lived state so an existing user can log in. The default OAuth scope is `identify guilds.members.read`; an override must retain guild-member access for new registration. The bounded guild nickname/avatar snapshot is refreshed on login when membership data is available; Discord access tokens are not persisted.

### 3.2 Two-step elevation (for self-service export / delete)

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `POST` | `/api/auth/elevate` | user session | — | `200 {authorization_url}` starts fresh Discord re-authorization (HMAC state, short TTL); the callback places a short-lived, non-HttpOnly, SameSite capability cookie so the SPA can move it into the destructive request header. The same per-client-IP admission throttle as login start applies | `unauthorized`, `service_unavailable`; `rate_limited` (429, with `Retry-After`) when the shared per-client-IP admission limit is exceeded |

Self-service destructive endpoints (`3.8`) require an **active elevated capability**. The
carrier is the header `X-Elevated-Token: <token>` (a short-lived, single-use bound capability).
A request without / with an expired / with an already-consumed token is `403 elevated_required`.

### 3.3 Profile & usage

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/me` | user session | — | same `{user:{…}}` envelope and fields as `/api/session` | `unauthorized` |
| `PATCH` | `/api/me` | user session | `{lang}` | `200` same updated `{user:{…}}` envelope; `lang` is exactly `zh` or `en` | `invalid_request`, `unauthorized` |
| `GET` | `/api/me/usage` | user session | — | `200 {total_requests, total_uncached_input_tokens, total_cache_write_input_tokens, total_cache_read_input_tokens, total_output_tokens, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests}`; the four token buckets are the authoritative counters, the legacy prompt/completion totals remain compatibility mirrors | `unauthorized` |
| `GET` | `/api/logs` | user session | optional single-valued `model`, `error_code`, `status`, `from`, `to`, `page`, `page_size` | `200 {data:[…], has_more}`; each metadata-only row of the session principal's own requests is `{id, route_kind, model, endpoint_key_id, key_note, endpoint_note, endpoint_base_url, upstream_model_id, status_code, duration_ms, started_at, completed_at, uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens, prompt_tokens, completion_tokens, total_tokens, usage_unknown, error_code, error_source, error_diag, attempt_id}`; `key_note`/`endpoint_note` are the current notes of the caller's own resources and are empty once a resource is deleted; `endpoint_base_url` is the dispatch-time snapshot | `invalid_request`, `unauthorized`, `internal` |
| `GET` | `/api/logs/options` | user session | — (any query parameter is `invalid_request`) | `200 {models:[…]}` — distinct non-empty platform model names from the caller's own retained logs, ascending, bounded server-side; the client may fuzzy-search this list locally but the server never substring-searches logs | `invalid_request`, `unauthorized`, `internal` |

A null stored user-level RPM limit means the administrator's global default applies. Normal users cannot change it.

User-station log filters match exactly: `model` against the caller's own platform model full name, `error_code` against the stable stored code, `status` in `[100,599]`, and `from`/`to` as non-negative Unix seconds (`started_at >= from`, `started_at < to`). Ownership is part of the SQL: only the session principal's own rows are ever reachable, and no response carries another user's resources.

`PATCH /api/me` accepts **only** a present `lang` field (`"zh"` or `"en"`); any other field (`rpm_limit`, `endpoint_limit`, `is_banned`, usage, id, …), an empty body, trailing JSON, or an oversized body is rejected with `invalid_request` by the strict decoder. The response is the same `{user:{…}}` envelope as `GET /api/me`. This route is session-only:
neither an admin session nor a caller key can reach it.

### 3.4 Caller key

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/caller-key` | user session | — | `200 {display:"nbk_<head4>…<tail4>", created_at, updated_at}` — head/tail fragments only; the secret is never returned | `unauthorized`, `not_found`, `internal` |
| `POST` | `/api/caller-key/regenerate` | user session | — | `200 {secret:"nbk_…"}` — the full plaintext is returned **once**; the previous key is invalidated instantly and the new hash/display fragments are persisted in place | `unauthorized`, `internal` |

### 3.5 Endpoints

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints` | user session | — | `200` array of `{id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at}` (key secrets never appear) | `unauthorized` |
| `POST` | `/api/endpoints` | user session | `{base_url, connector_type?, note?, enabled?}` | `201` created endpoint; `base_url` is canonicalized (scheme/host lowercased, trailing dot removed, userinfo/query/fragment removed, redundant slashes collapsed; an explicitly typed port is preserved while an implicit default port is omitted from display/persistence but normalized in the internal origin key) | `invalid_request`, `unauthorized`, `conflict`, `resource_limit_exceeded` (endpoint cap reached: `min(global_default, user_limit)`) |
| `GET` | `/api/endpoints/{id}` | user session | — | `200` the endpoint object | `unauthorized`, `not_found` |
| `PATCH` | `/api/endpoints/{id}` | user session | `{base_url?, note?, enabled?}` | `200` updated endpoint; an empty or wholly unchanged patch is rejected; when any key exists, `base_url` may change only within the same canonical origin (scheme + canonical host + effective port) | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
| `DELETE` | `/api/endpoints/{id}` | user session | — | `204`; cascades to its keys, their fetched-model cache, and their bindings (immediate invalidation) | `unauthorized`, `not_found` |

Multiple endpoints may share the same canonical `base_url` (no uniqueness on
`base_url`); each keeps independent note/enabled state. The endpoint-count cap is
`min(global_default, user_endpoint_limit)`; when the current count reaches the cap, new
endpoints are rejected (`resource_limit_exceeded`) while existing ones are retained. The admin can set a
per-user endpoint limit and clear it (NULL restores the global default).

`connector_type` defaults to `openai-compatible` and is validated against the
authoritative connector registry; unknown types are rejected with
`invalid_request` and never silently fall back to another protocol. The
connector type is immutable after creation (a `PATCH` carrying `connector_type`
is rejected). In v1.0.0-alpha.1 the registry supports only `openai-compatible`;
later versions extend the registry in one place.

`model_fetch_failed` / `model_fetch_failed_at` are the endpoint's bounded fetch
flag: set (with a unix-seconds timestamp) whenever an upstream model fetch for
any of its keys failed, cleared by the next successful fetch. The flag is
state only — never a diagnostic or any upstream content.

**Unreleased security amendment:** Endpoint updates enforce the credential/origin boundary in the same database
transaction as the update and key-existence check. An endpoint with any key
(enabled or disabled) cannot move to another canonical origin; delete all keys,
change the origin, then add new keys. A path change within the same origin is
allowed. Persisted endpoint credentials use a contextual `nbsec:v2` AES-256-GCM
envelope authenticated against purpose, version, user id, endpoint id, key id,
and the egress-canonical origin. Creating a key allocates its row id and seals
the credential in the same database transaction. Startup upgrades valid
`nbsec:v1` rows before any listener or worker starts; one invalid or unknown row
rolls back the whole credential batch and prevents startup. Runtime fetch and
forwarding accept only v2 under the exact context and never retry v1. Empty
objects and patches whose supplied values are all unchanged are
`invalid_request`. Automatic model fetching runs only after an actual same-origin
path change or a disabled-to-enabled endpoint transition; note-only changes,
representation-only URL changes, and enabled-to-disabled transitions do not
fetch. A rejected update is atomic and never queues a fetch.

### 3.6 Endpoint keys & fetched models

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints/{id}/keys` | user session | — | `200` array of `{id, display_head, display_tail, note, enabled, created_at, updated_at}`; display fragments are the first/last few runes of the secret so listings never decrypt; the secret and its ciphertext are never returned | `unauthorized`, `not_found` |
| `POST` | `/api/endpoints/{id}/keys` | user session | `{secret, note?, enabled?}` | `201` created key metadata `{id, display_head, display_tail, note, enabled, created_at, updated_at}` (`secret` is sealed with contextual AES-256-GCM after its uncommitted row id is allocated and before the same transaction commits; plaintext is never a SQL value and is never returned); triggers a model fetch for this key when enabled | `invalid_request`, `unauthorized`, `not_found`, `payload_too_large`, `resource_limit_exceeded` |
| `PATCH` | `/api/endpoints/{id}/keys/{keyId}` | user session | `{note?, enabled?}` | `200` updated key `{id, display_head, display_tail, note, enabled, created_at, updated_at}` (the secret is not mutable here; key rotation is delete + add) | `invalid_request`, `unauthorized`, `not_found` |
| `DELETE` | `/api/endpoints/{id}/keys/{keyId}` | user session | — | `204`; cascades to its fetched-model cache and bindings | `unauthorized`, `not_found` |
| `GET` | `/api/endpoints/{id}/keys/{keyId}/models` | user session | — | `200` array of `{upstream_model_id, provider, fetched_at, status}` for that (Endpoint, Key) combo; empty `[]` when the combo has no cache rows; a missing, cross-user, or wrong-endpoint combo is indistinguishable `not_found` | `unauthorized`, `not_found` |
| `POST` | `/api/endpoints/{id}/keys/{keyId}/models/refresh` | user session | — | `202` (no body) once the manual fetch is queued; the fetch runs on a bounded worker pool and its outcome is never echoed back; on success the cache for that combo is replaced; on failure the cache is cleared, the endpoint is flagged, and a bounded user issue is recorded | `unauthorized`, `not_found`, `rate_limited`, `service_unavailable` |

The per-endpoint key-count cap is `default_endpoint_key_limit` (default 20,
administrator-adjustable at runtime; see §4.4). When the count of keys on one endpoint
reaches the cap, a new key is refused with `resource_limit_exceeded` (422) carrying `limit`
and `resource`=`endpoint_key`; existing keys are retained. The count and the cap are read
inside the same transaction as the insert, so a concurrent add cannot breach the cap.

The fetched-model cache is keyed per **(Endpoint, Key)** combo: different keys on the same
endpoint may legitimately return different upstream lists. The server fetches automatically
for the endpoint changes described above and after an enabled key is added, and only manually
thereafter (no background refresh). Fetched upstream model identifiers are treated as untrusted: bounded in size and
count, validated to prevent over-long names polluting logs/DOM/DB. When a key is regenerated
is not applicable (caller key is separate); when an endpoint key is disabled, its cached rows/bindings remain stored but are filtered from routing; deleting it removes cache rows/bindings by cascade.

Refresh gates: a refresh is accepted only for an enabled endpoint/key owned by the caller; a
disabled or missing combo is `not_found` (indistinguishable) and never triggers upstream work.
The `rate_limited` code covers both the per-user manual refresh frequency limit (bounded
sliding window) and the bounded fetch pool being full; a refresh during shutdown is
`service_unavailable`. Each (Endpoint, Key) combo has at most one pending/in-flight fetch
(duplicate refreshes merge), so repeated refreshes never spawn unbounded work. Upstream and
protocol failures (HTTP status outside 2xx, truncated or malformed JSON, duplicate/empty/
over-long/control-containing model ids) are not successes: the combo cache is cleared, the
endpoint's `model_fetch_failed` flag is set, and a bounded, sanitized user issue (`kind`
`model_fetch_failed`, `ref` the endpoint id) is written atomically with the flag. Issue text
is a stable short message plus a bounded diagnostic fragment; it never contains the key, the
full response, or the endpoint URL. HTTP 200 alone never counts as success.

### 3.7 Models & bindings

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/models` | user session | — | `200` array of `{id, provider, model, full_name, route_strategy, silent_retry, binding_count, created_at, updated_at}` | `unauthorized` |
| `POST` | `/api/models` | user session | `{provider, model, route_strategy?, silent_retry?}` | `201` created model; `provider`/`model` are free strings that allow `/` (each ≤64 runes, no control chars, no leading/trailing whitespace); the external name is `provider/model` and uniqueness is enforced on that full name; `route_strategy` defaults to `ordered` and only `ordered`/`random` are accepted; `silent_retry` is an optional boolean that defaults to `false` (fail fast) and, when `true`, silently tries the next binding after a pre-commit upstream failure until success or exhaustion (it never retries after any response byte is committed); a `provider` starting with the reserved charity prefix `[公益]` is rejected with `invalid_request` and a message quoting the prefix (the namespace belongs to charity models) | `invalid_request`, `unauthorized`, `conflict` (full-name collision), `resource_limit_exceeded` |
| `GET` | `/api/models/{id}` | user session | — | `200` the model object | `unauthorized`, `not_found` |
| `PATCH` | `/api/models/{id}` | user session | `{provider?, model?, route_strategy?, silent_retry?}` | `200` updated model (changing `provider/model` recomputes `full_name` and may collide); a `provider` starting with the reserved charity prefix `[公益]` is rejected with `invalid_request` and a message quoting the prefix | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
| `DELETE` | `/api/models/{id}` | user session | — | `204`; cascades to its bindings | `unauthorized`, `not_found` |
| `GET` | `/api/models/{id}/bindings` | user session | — | `200` array ordered by `ord`: `{id, endpoint_key_id, endpoint_base_url, endpoint_note?, endpoint_key_display_head?, endpoint_key_display_tail?, endpoint_key_note?, upstream_model_id, ord}` | `unauthorized`, `not_found` |
| `POST` | `/api/models/{id}/bindings` | user session | `{endpoint_key_id, upstream_model_id, ord?}` | `201` created binding in the same enriched shape; `endpoint_key_id` must belong to one of the user's enabled endpoints and the key itself must be enabled; `upstream_model_id` must exist in that key's fetched cache; `ord` defaults to `0` and must be within `[0, 1000000]` | `invalid_request`, `unauthorized`, `not_found`, `conflict` (duplicate `(model_id, endpoint_key_id, upstream_model_id)`), `resource_limit_exceeded` |
| `PUT` | `/api/models/{id}/bindings/order` | user session | `{order:[binding_id,…]}` | `200` the full enriched binding list in the requested order; the array must contain every current binding exactly once and only bindings owned through this model | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
| `PATCH` | `/api/models/{id}/bindings/{bId}` | user session | `{ord?, upstream_model_id?}` | `200` updated enriched binding; only `ord`/`upstream_model_id` are mutable (the endpoint key never changes through this path); the resulting `upstream_model_id` must still exist in the binding key's fetched cache and the key/endpoint must still be enabled | `invalid_request`, `unauthorized`, `not_found`, `conflict` (duplicate triple after update) |
| `DELETE` | `/api/models/{id}/bindings/{bId}` | user session | — | `204` | `unauthorized`, `not_found` |

Platform-model and binding counts are capped per parent: at most `default_model_limit`
models per user (default 100) and `default_binding_limit` bindings per model (default 50),
both administrator-adjustable at runtime (see §4.4). When the count reaches the cap a new
row is refused with `resource_limit_exceeded` (422) carrying `limit` and `resource`
(`model` or `binding`); existing rows are retained. The count and cap are read inside the
same transaction as the insert, so a concurrent add cannot breach the cap.

A model may be saved with zero bindings (a draft). Calling a draft model over `/v1/chat/completions`
yields **503** (see §2.1). `provider`/`model` are stored and matched only as opaque strings
(never split into path segments for routing; never interpolated into SQL), so `/` has no
injection surface.

Bounded fields: `provider`/`model` are each ≤64 runes, must be valid UTF-8, must not contain
control characters, and must not start or end with whitespace (Unicode-aware); interior
whitespace and `/` are allowed. `upstream_model_id` is an opaque string bounded to ≤512 runes
with the same character rules; it must match a row of the key's fetched cache exactly, so a
client can never bind an arbitrary string. `ord` is an integer in `[0, 1000000]`. `silent_retry`
is a boolean; a non-boolean value (e.g. the string `"true"`) is rejected with `invalid_request`,
and an unknown field is rejected by `DisallowUnknownFields`. These bounds are enforced
server-side on every create and patch; the unique `(user_id, full_name)` and
`(model_id, endpoint_key_id, upstream_model_id)` constraints are the backstop.

### 3.8 Account self-service (two-step required)

Both endpoints require `X-Elevated-Token` from §3.2.

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `POST` | `/api/account/export` | user session + elevated | — | `200` direct `application/json` attachment containing the bounded whitelist package (user info, endpoint/key metadata, models/bindings, caller-key display metadata, usage totals, log summary), without plaintext/ciphertext upstream secrets | `elevated_required`, `unauthorized`, `rate_limited`, `payload_too_large`, `internal` |
| `POST` | `/api/account/delete` | user session + elevated | `{confirm:"DELETE"}` | `204`; deletes the account and all linked data (endpoints, keys, models, bindings, request logs, caller key, alerts/callbacks); late callbacks against a deleted account are atomically suppressed server-side so the insert no-ops; the current session is invalidated | `elevated_required`, `unauthorized`, `conflict`, `internal` |

The export package excludes upstream secret material by policy; it carries enough metadata to
be usable as a portable account snapshot. The package uses `schema_version: 2` and an explicit server-side field whitelist; future database columns do not enter it automatically.
The user section carries the economy balances as canonical decimal strings (`credits`,
`donation_credit`) and the level state as small integers (`manual_level` nullable — `null`
means automatic — and `auto_level`, the persisted automatic high-water mark); no
violation-window or threshold-configuration data is exported.

### 3.9 User issues

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/issues` | user session | query: `resolved`, `before_id`, `limit` | `200 {data:[…], has_more}` — one page of `{id, kind, message, ref, created_at, resolved, resolved_at}` (newest first; `resolved_at` absent while unresolved) | `unauthorized` |
| `POST` | `/api/issues/{id}/resolve` | user session | — | `204`; one-way idempotent ack (repeating it keeps the original `resolved_at` and still succeeds) | `unauthorized`, `not_found` |

- **Ownership**: the issue center is session-only by construction. Every query carries the
  session principal's `user_id` as a mandatory SQL predicate; cross-user issues never enter the
  candidate set, and a resolve of a missing or cross-user issue is an indistinguishable
  `not_found`. Caller keys, admin sessions, and header-carried identities never authorize these
  routes, and there is no write path through which a user can fabricate an issue.
- **Bounded projection**: `kind` ≤ 128 runes, `message` ≤ 1024 runes (human-safe, free of raw
  upstream text), `ref` ≤ 256 runes. All three pass the shared diagnostic boundary as the final
  sink on read as well as write: no secret, ciphertext, request/response content, raw body,
  or full endpoint URL is ever projected, and no control characters or line forgery can reach
  the wire. Frontends render these fields as plain text.
- **Pagination**: keyset pagination by `id` (rows strictly older than `before_id`), `limit`
  clamped into `[1, 100]` (default 100), `resolved` accepts exactly `true`/`false` (omitted =
  both states). `has_more` is explicit; unknown, repeated, or malformed parameters are
  `invalid_request`.
- **Lifecycle**: issues cascade with account deletion; a late issue write against a deleted
  user is an atomic no-op (see §3.8 / §5 atomic late-callback handling), and a resolve racing
  a delete linearizes through the single writer — it can neither resurrect a deleted issue
  nor strand an unresolved one. The administrator alert center (`admin_alerts`) is a
  separate surface and is never reached through these routes.

### 3.10 Level-5 steward prefix — `/api/steward/*`

The frozen co-management prefix for effective level ≥ 5 users, served on the **user station**
with the ordinary user session cookie. This contract lands the authorization frame only:

- **Live authorization**: every request re-resolves the server-authoritative effective level
  and requires ≥ 5; nothing is cached. A demotion or a manual-level reset revokes an existing
  session on its very next request (`403 forbidden`). Level 5 is reachable only through an
  administrator manual override (automatic levels stop at 4).
- **Station/session boundary**: requests arriving on the admin station (or an unknown
  station) are refused before any identity is consulted, and an administrator session is
  never accepted — the steward capability is not an administrator capability and cannot be
  borrowed in either direction.
- **Envelope ordering**: an unauthenticated caller gets `401 unauthorized`; an authenticated
  caller below level 5 gets `403 forbidden` — including for sub-paths that have no business
  route — so the frame never reveals which steward routes exist. A level-5 caller hitting an
  unregistered sub-path gets the ordinary `404 not_found` envelope.
- **Current state**: no business route is registered yet (steward logs and donation reviews
  land with their own rails). Sub-handlers receive only an opaque steward identity
  (user id + role); they never see another user's rows by construction of those future
  projections.

### 3.11 Daily check-in

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/checkin` | user session | — (any query parameter is `invalid_request`) | while unavailable: `200 {"enabled":false}` and NOTHING else (no reason, no day, no range — timezone-unset is indistinguishable from every other disabled cause); while available: `200 {"enabled":true,"checked_in_today":bool,"credits":string,"award_min_milli":string,"award_max_milli":string,"credits_cap_milli":string}` — all amounts canonical decimal milli-credit strings (`credits` may carry `-`). The status read performs no writes and never triggers the lazy level promotion | `invalid_request`, `unauthorized`, `internal` |
| `POST` | `/api/checkin` | user session | — (any body or query parameter is `invalid_request`; the client never supplies the award or the day) | `200 {"award_milli":string,"credits":string}` — the drawn award and the new post-award balance, both canonical decimal strings | `invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `internal` |

Behavior (frozen §I, implementation contract §2.4/§6.3):

- **One per day, by constraint**: the site-local day key is computed from the explicitly
  configured fixed offset; one check-in per user per day is enforced by the database
  `UNIQUE(user_id, day)` constraint inside a single transaction that also applies the
  award to `credits`, appends the `checkin_award` ledger row and records the check-in in
  the product-activity rollup. A duplicate attempt returns `already_checked_in` (409)
  and writes nothing.
- **Three-way switch**: `checkin_mode` is `enabled`, `level_gated` (only effective
  level ≥ 3 may check in), or `disabled` (the default). While the site timezone is not
  configured the feature behaves exactly like `disabled` — the user sees the identical
  `feature_disabled` (403) envelope with the identical message in every refused cause.
- **Cap as a threshold, not a truncation**: `credits_cap_milli` = 0 means no threshold
  (never a global switch-off — the switch is `checkin_mode`); otherwise a user whose
  `credits` balance has reached the cap is refused `checkin_cap_reached` (403) WITHOUT
  consuming the day, unless the effective level is ≥ 3 (bypass). An admitted award may
  push the balance above the cap; it is never truncated. The cap only gates check-in
  admission, never charity consumption.
- **Random award**: a uniform integer in the inclusive `[award_min_milli,
  award_max_milli]` range (default 40,000,000–60,000,000), drawn server-side with
  crypto/rand rejection sampling (no modulo bias); a corrupt range fails closed as the
  same `feature_disabled` envelope instead of fabricating a draw.
- **No streaks, no make-up check-ins**: the table records date and award only; there is
  no streak, location, or device data anywhere, and no token detection.

## 4. Admin station — `/admin/api/*` (admin session cookie)

Auth: a valid admin session cookie. The administrator is env-configured (single row,
`discord_id` null) and cannot self-register. All responses are `no-store`.

Administrator sessions are bound to the current administrator credential
generation: rotating the administrator password (changing the env value and
restarting the process) invalidates every existing admin session at its next
request, so a leaked or retired password cannot keep an old long-lived admin
session alive. A plain restart with the same password keeps admin sessions
alive; only an actual password rotation revokes them. The binding is an opaque
fingerprint of the password (never the password itself), so the database holds
no password material.

### 4.1 Admin auth & elevation

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `POST` | `/admin/api/login` | none | `{username, password}` | `200 {admin:{username}}` sets admin session cookie; throttled against brute force (configurable backoff window) | `unauthorized`, `rate_limited` |
| `POST` | `/admin/api/logout` | admin session | — | `204` clears the cookie | `unauthorized` |
| `GET` | `/admin/api/session` | admin session | — | `200 {admin:{username}}` | `unauthorized` |
| `POST` | `/admin/api/auth/elevate` | admin session | `{password}` | `200 {token, expires_at}` grants a session-bound, single-use elevated capability for §4.2 destructive actions; send it in `X-Elevated-Token` | `invalid_request`, `unauthorized`, `forbidden`, `rate_limited`, `internal` |

### 4.2 User management

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/users` | admin session | query: `page`, `page_size`, `is_banned?`, `q?` | `200 {data:[…], has_more}`; each row is `{id, username, avatar, discord_id, is_banned, banned_reason, banned_until, auto_banned, charity_suspended_until, endpoint_limit, rpm_limit, lang, total_requests, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests, credits_balance, donation_credit_balance, level, auto_level, created_at}` | `invalid_request`, `unauthorized`, `forbidden` |
| `GET` | `/admin/api/users/{id}` | admin session | — | `200` the same user shape with full usage metadata | `unauthorized`, `forbidden`, `not_found` |
| `PATCH` | `/admin/api/users/{id}` | admin session | profile mode `{endpoint_limit?, rpm_limit?, lang?, level?}` **or** economy mode `{credits?, donation_credit?, operation_id, reason}` — never both in one request | `200` updated user; profile mode: explicit null clears a limit to the global default (or resets `level` to automatic); economy mode: the balances AFTER the (first) application are returned as `credits_balance` / `donation_credit_balance` | `invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `insufficient_credits` |
| `POST` | `/admin/api/users/{id}/ban` | admin session | `{reason?, duration_seconds?}`; absent or null duration = permanent, positive integer = lazy expiry deadline in seconds | `204`; atomically sets the ban (clearing `auto_banned`), deletes sessions, and deletes the caller key | `unauthorized`, `forbidden`, `not_found`, `invalid_request` |
| `POST` | `/admin/api/users/{id}/unban` | admin session | — | `204`; clears the ban/reason/deadline but does not recreate a caller key | `unauthorized`, `forbidden`, `not_found` |
| `DELETE` | `/admin/api/users/{id}` | admin session + `X-Elevated-Token` | — | `204`; same cleanup and late-write suppression guarantees as §3.8 | `elevated_required`, `unauthorized`, `forbidden`, `not_found`, `internal` |

Pagination is 1-based: `page` defaults to 1 and `page_size` defaults to 20 and clamps to `[1,100]`. Unknown, repeated, or malformed query parameters are `invalid_request`. The administrator row is never a list candidate and no response includes caller-key or session material.

`PATCH` accepts two disjoint modes. Profile mode accepts only `endpoint_limit` (integer `≥0`), `rpm_limit` (integer `≥1` and no greater than `default_rpm_per_user`), `lang` (`zh`/`en`), and `level` (integer `1..5` or `null`). Explicit `null` clears either limit or resets `level` to automatic; an absent field is unchanged. `level` is the manual override: a stored `1..5` wins over the automatic level (level 5 — the steward capability — is reachable only this way), a reset returns the user to the persisted automatic high-water mark, and the override never disturbs that mark itself. Setting `level` on the administrator row is `forbidden` (administrators are excluded from the level system). Every user row projects `level` (nullable) and `auto_level` (the persisted automatic high-water mark, `1..4`); the effective level is resolved server-side per use and never projected as a list field.

Economy mode adjusts the user's milli-credit balances: `credits` (signed consumption balance) and/or `donation_credit` (cumulative donor reward) are canonical decimal-string **increments** applied server-side — never target balances, so a caller can never name an after-value. Either delta requires `operation_id` (an idempotency key of 1..128 ASCII token characters that must not use the reserved `sys.` system namespace) and a non-empty bounded `reason` (≤1024 runes). The adjustment and its append-only audit ledger entry commit in one transaction; retrying with the same `operation_id` returns the FIRST application's result instead of applying twice. The economy and profile modes must never be mixed in one request (`invalid_request`). A `donation_credit` result below zero is refused with `conflict`; the consumption balance may legitimately go negative through settlement paths. Responses use the names `credits_balance` / `donation_credit_balance` so a delta can never be confused with the resulting balance; both are canonical decimal strings everywhere they appear.

A ban reason is optional, bounded to 1024 bytes, and control-character-free. Ban invalidation is transactional. Unban restores access eligibility only; the user must generate a new caller key.

Ban durations: presets are expressed as seconds (`3600` / `86400` / `604800` / `2592000`); any positive integer up to ten years is accepted as a custom duration. Every manual ban clears the stored `auto_banned` provenance flag; rule-driven bans set it. The user list/detail projections expose `banned_until`, `auto_banned`, and `charity_suspended_until` (nullable unix seconds).

### 4.3 Global logs, usage, overview

| Method | Path | Auth | Query | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/logs` | admin session | optional single-valued `user_id`, `endpoint_base_url`, `upstream_model`, `error_code`, `status`, `from`, `to`, `page`, `page_size` | `200 {data:[…], has_more}`; each metadata-only row is `{id, user_id, route_kind, endpoint_base_url, endpoint_key_id, upstream_model_id, status_code, duration_ms, started_at, completed_at, uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens, prompt_tokens, completion_tokens, total_tokens, usage_unknown, error_code, error_source, error_diag, attempt_id}`; `endpoint_base_url` is the dispatch-time snapshot. The user-chosen platform model name and all notes are never selected | `invalid_request`, `unauthorized`, `forbidden`, `internal` |
| `GET` | `/admin/api/logs/export.csv` | admin session | same filters as `/admin/api/logs` minus paging (paging parameters are `invalid_request`) | `200` unpaginated CSV (header + one row per log, oldest first) with every cell sanitized against spreadsheet formula injection; over-bound selections fail closed with `payload_too_large` instead of truncating | `invalid_request`, `unauthorized`, `forbidden`, `payload_too_large`, `internal` |
| `GET` | `/admin/api/logs/export.json` | admin session | same filters as `/admin/api/logs` minus paging | `200 {data:[…]}` — the same frozen row shape as the list endpoint, unpaginated with the same fail-closed bounds | `invalid_request`, `unauthorized`, `forbidden`, `payload_too_large`, `internal` |
| `GET` | `/admin/api/usage` | admin session | `group_by=site` or `group_by=user` (default `site`); bounded `limit` applies to user grouping | site: `200 {total_requests, total_uncached_input_tokens, total_cache_write_input_tokens, total_cache_read_input_tokens, total_output_tokens, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests}` (four buckets authoritative, legacy totals mirrors); user: `200 {users:[{user_id,…totals}]}` | `invalid_request`, `unauthorized`, `forbidden`, `internal` |
| `GET` | `/admin/api/activity` | admin session | optional single-valued `page`, `page_size` | `200 {enabled, data:[…], has_more}`; each site-local day bucket is `{day, product_active, api_requests, uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens, checkins, console_writes, game_active, game_rounds, distinct_product_users}`, newest day first. `distinct_product_users` is JSON `null` when fewer than 5 distinct users were active that day (never conflated with `0`); request/token/check-in/console aggregates always stay numeric. There is no user-level drill-down. While the site timezone offset is unset the response is `{enabled:false, data:[], has_more:false}` (activity collection is disabled) | `invalid_request`, `unauthorized`, `forbidden`, `internal` |
| `GET` | `/admin/api/overview/endpoints` | admin session | optional single-valued `page`, `page_size`, `filter` | `200 {data:[…], has_more}`; each row groups one stored canonical base_url: `{base_url, user_count, endpoint_count, key_count, users:[{user_id, endpoint_count, key_count, enabled_count}]}` | `invalid_request`, `unauthorized`, `forbidden`, `payload_too_large`, `internal` |

Log paging is 1-based offset paging; `page_size` defaults to 20 and clamps to `[1,100]`. `from`/`to` are non-negative Unix seconds and `status` is `[100,599]`. Unknown, repeated, overlong, or malformed parameters are `invalid_request`. Filter semantics are frozen as exact equality: `user_id`, `endpoint_base_url` (against the stored dispatch snapshot), `upstream_model`, and `error_code` (an unknown code simply matches nothing). The administrator projection shows which actual endpoint base URL and upstream model served a request — never the user-chosen platform model name or any endpoint/key note. There is no user-station export.

The CSV/JSON exports are not paginated: a selection exceeding the finite row bound, or a rendered payload exceeding the byte bound, fails closed with `payload_too_large` — output is never silently truncated. Every CSV cell is sanitized before rendering: a leading `=`, `+`, `-`, `@`, TAB, CR, LF, BOM, or Unicode whitespace character gets a single-quote prefix so spreadsheet applications never interpret it as a formula.

The activity endpoint pages site-local day rollups with the same 1-based paging rules (`page_size` default 20, clamp `[1,100]`). A day key is the unix seconds of that day's local midnight under the explicitly configured fixed site offset (`site_timezone_offset_minutes`); no day key is ever generated while the offset is unset. Successful product activity means a protocol-successful API request, a successful check-in, or an administrator console configuration write — protocol failures and pre-dispatch failures never count. Both activity tables are retained for 400 days and cleaned by the bounded maintenance sweep; when an account is deleted, every affected day's site rollup is recomputed from the surviving per-user rows inside the deletion transaction, so no deleted contribution survives.

The endpoint overview groups endpoints by their already-stored canonical base_url (it never re-normalizes or widens the stored value), so cross-user sharing, same-user duplicates, path differences, and port differences are visible exactly as the egress concurrency gate sees them. Ordering is stable — groups by base_url ascending, expandable user entries by user_id ascending — so offset paging never drifts. `filter` is a literal substring matched against the stored base_url; LIKE metacharacters never widen it. `enabled_count` is how many of that user's endpoints in the group are enabled. No overview response carries an endpoint/key note, username, Discord identifier, connector secret, or ciphertext.

Plaintext upstream secrets never appear in any admin overview, log, or usage response.

### 4.4 Site configuration

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/site-config` | admin session | — | `200` object of known keys → values (see below) | `unauthorized`, `forbidden` |
| `PATCH` | `/admin/api/site-config/{key}` | admin session | `{value}` | `200` updated value | `invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict` (a `level_threshold_*_milli` write whose enabled chain is not strictly increasing, or a `checkin_award_*_milli` write whose resulting pair has min > max) |

Known `site_config` keys (the authoritative key set is enforced by the handler):

- `site_name`, `site_logo_url`, `default_locale`
- `legal_privacy_override_zh`, `legal_privacy_override_en`, `legal_terms_override_zh`, `legal_terms_override_en`, `legal_authoritative_locale`
- `default_endpoint_limit` — global endpoint-count cap default
- `default_endpoint_key_limit` — global per-endpoint key-count cap default
- `default_model_limit` — global per-user platform-model cap default
- `default_binding_limit` — global per-model binding-count cap default
- `default_rpm_per_user` — global per-user RPM default
- `global_rpm` — site-wide RPM cap
- `default_per_endpoint_concurrency` — global per-endpoint concurrency cap (keyed by normalized `base_url`)
- `egress_global_concurrency` — egress global concurrency gate
- `discord_guild_id`, `discord_role_id` — registration gate (both must match to allow new registration; either blank pauses registration)
- `oauth_start_rate_limit`, `oauth_start_rate_window_seconds`, `oauth_start_rate_penalty_seconds` — in-process per-client-IP OAuth/elevation-start admission
- `maintenance_mode`, `registration_open` — public boolean site-state toggles
- `site_timezone_offset_minutes` — nullable integer, multiple of 30 in `[-720,+840]`; JSON `null` (the default) means "not configured" and is distinct from an explicit `0` (UTC). While unset, site-day-key features are force-disabled behind the ordinary `feature_disabled` error without revealing why. Once any check-in or activity row exists the value is frozen: every further write — including rewriting the identical value or after those rows are cleaned up — is refused with `conflict`. Clearing it with JSON `null` is always rejected
- `level_threshold_2_milli`, `level_threshold_3_milli`, `level_threshold_4_milli` — the automatic level-promotion thresholds on the cumulative `donation_credit` balance, as canonical non-negative decimal milli-credit strings (`"0"`, the default, disables that level's automatic promotion). The enabled (>0) subset must be strictly increasing in level order; a disabled middle level is allowed and produces a skip (e.g. `0` / `0` / `"50000000"` promotes a qualifying user straight to level 4). The chain cross-validation and the write share one transaction, so a rejected combination (equal or decreasing enabled values) is `conflict` and nothing is written. A threshold change never recomputes any stored level: the automatic level is a persisted only-ever-raised high-water mark, lazily promoted on the next authoritative resolution, and there is no automatic downgrade in alpha.2 (raising a threshold or negatively correcting `donation_credit` leaves existing marks untouched)
- `checkin_mode` — the check-in three-way switch (§3.11), exactly `enabled` / `level_gated` / `disabled`; the default is `disabled`. An unknown stored value reads as `disabled` (fail closed, never an implicit enabled state)
- `checkin_award_min_milli`, `checkin_award_max_milli` — the daily check-in award range bounds (§3.11), as canonical non-negative decimal milli-credit strings; defaults `"40000000"` / `"60000000"` (4–6 USD equivalent). The pair is cross-validated: PATCHing either bound validates `min <= max` against the other key's current value in ONE transaction, so a rejected pair is `conflict` and nothing is written. Both write paths record the administrator console write as product activity in that same transaction
- `credits_cap_milli` — the check-in admission threshold (§3.11) as a canonical non-negative decimal milli-credit string; default `"250000000"` (25 USD equivalent). `0` = no threshold. It gates check-in admission only (effective level ≥ 3 bypasses) and never truncates an awarded amount
- `alert_prefs_*` — bounded administrator alert-center preference namespace

Values are typed: integer keys (`default_endpoint_limit` `[0,10000]`; resource-count caps `[1,10000]`; RPM keys `[1,4096]`; concurrency keys `[1,100000]`; OAuth limit `[0,1000]`, window `[1,3600]`, penalty `[0,3600]`) accept only JSON integers. Boolean keys accept only JSON booleans. The amount keys (`level_threshold_*_milli`, `checkin_award_min_milli`, `checkin_award_max_milli`, `credits_cap_milli`) accept only canonical non-negative decimal strings (no exponent, `+`, leading zeros, whitespace, or negative values) and project as JSON strings — `"0"` for the level thresholds and each key's documented default for the check-in keys while unset or manually corrupted. `checkin_mode` accepts exactly the member strings of its three-way switch. Single-line text is bounded/control-free; the four legal overrides accept bounded multiline text (≤65536 bytes, preserving newline/tab while rejecting other controls); `default_locale` is exactly `zh`/`en`, and `legal_authoritative_locale` also permits blank. `site_timezone_offset_minutes` accepts only a canonical JSON integer within its range and projects as JSON `null` while unset or manually corrupted. `null` is rejected for every other key (clearing a per-user limit is expressed through the user-level NULL
semantics; blanking a text gate value uses `""`). `GET` returns the effective
typed value for every known key (documented defaults when unset; `null` for the
nullable timezone key) and never
projects an unknown stored row; `PATCH` of an unknown key is `not_found`.
`PATCH` responds `200 {key, value}` with the applied typed value.

The display/legal values and both public toggles are available through the strict unauthenticated bootstrap projection in §1.1; operational limits, Discord IDs, and alert preferences are not.

Runtime config changes do not require a restart: `default_rpm_per_user` /
`global_rpm` are applied to the shared flow-control controller and
`default_per_endpoint_concurrency` / `egress_global_concurrency` to the shared
egress gate through the runtime apply hook. A failed runtime apply fails closed
(DB untouched); a persistence failure after a successful apply reverts the
runtime singleton to its previous value, so DB and runtime cannot drift. The
resource-count caps (`default_endpoint_limit`, `default_endpoint_key_limit`,
`default_model_limit`, `default_binding_limit`) have no runtime singleton: each
create reads the cap inside its own transaction, so a change takes effect on the
next create without a restart. The same per-transaction authoritative read
applies to the level thresholds and the check-in keys (`checkin_mode`,
`checkin_award_*_milli`, `credits_cap_milli`): every check-in admission reads
its own consistent snapshot, so a change takes effect on the next check-in
without a restart.

### 4.5 Admin alerts

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/alerts` | admin session | query: `resolved` filter (`true`/`false`), `before_id` keyset cursor, `limit` | `200 {data:[{id, kind, message, ref, subject_user_id, created_at, resolved, resolved_at}], has_more}` | `unauthorized`, `forbidden`, `invalid_request` |
| `POST` | `/admin/api/alerts/{id}/resolve` | admin session | optional `{"resolved": bool}`; absent body or empty body means `true` (ack) | `200` updated `{id, kind, message, ref, subject_user_id, created_at, resolved, resolved_at}` | `unauthorized`, `forbidden`, `invalid_request`, `not_found`, `payload_too_large` |

`subject_user_id` has no foreign key on purpose: late callbacks against a deleted user are
suppressed atomically (`INSERT … SELECT … WHERE EXISTS`), so the alert row is written only
when the subject still exists. Alerts carry bounded, sanitized `message`/`ref`; pending
(unresolved) alerts are deduplicated per `(kind, ref, subject)` and capped per kind
(`MaxAdminAlertsPerKind`), so a repeating event source cannot flood the center. Listing is
keyset-paginated newest-first (`id` descending, `before_id` exclusive) with a clamped page
size; `resolved_at`/`subject_user_id` are `null` when absent. `resolve` is idempotent: a
repeated call for the same target state succeeds and keeps the original `resolved_at`;
resolving an alert with `false` reopens it and clears `resolved_at`.

## 5. Cross-cutting requirements

- **No plaintext upstream secret** is ever returned, listed, logged, exported, or surfaced in
  an error envelope. Literal and semantic response guards also reject a secret split across
  multiple JSON/SSE fragments. Only head/tail display fragments appear in a key view. These
  guards are an exact-match defense-in-depth layer for the known upstream credential only;
  they are not a general data-loss-prevention filter and do not detect encoded, truncated,
  or otherwise transformed material, or arbitrary other sensitive upstream data.
- **`Cache-Control: no-store`** is mandatory and default on every JSON response and every
  error envelope, and is the only cache policy permitted for key/secret/config endpoints.
- **Stable codes** are the sole machine-facing error identifier; HTTP status is derived from
  them.
- **Layered, bounded, sanitized diagnostics**: upstream identifiers/text live only in `diag`
  (≤4096 bytes) and in `request_logs.error_diag` (≤4096 bytes), bounded, UTF-8-repaired, and
  control-character-normalized (no CR/LF/TAB, so no line forgery) by the shared
  `internal/diagnostic` boundary; `message` (≤1000 runes) is human-safe and free of raw
  upstream text. Frontends render all such fields as text.
- **Request/response content is never persisted**: `request_logs` and any log entry hold
  metadata and bounded diagnostics only (privacy policy).
- **Retention maintenance**: request logs are periodically cleaned past 30 days; expired idle/absolute session rows and resolved alerts older than 30 days are purged at startup and every six hours. Pending alerts are bounded by caps/deduplication and are not age-purged.
- **Ownership isolation**: every read/write path checks `user_id`; cross-user resources never
  enter the candidate set for routing or listing.
- **Deterministic routing**: when a full name cannot be resolved to exactly this user's model,
  the request is rejected (`not_found` / `unauthorized` as applicable), never guessed.
- **Atomic late-callback handling**: account deletion and late callbacks are linearized by an
  atomic conditional write, never by a read-then-write.

## 6. Server-side maintenance gate

*(Unreleased security amendment: this section is not part of the published
`v1.0.0-alpha.1` contract; it describes the current development branch.)*

`maintenance_mode` is a server-side authoritative admission gate, not a front-end-only notice.
It is an atomically live-applied process-wide boolean: a toggle through the admin site-config
endpoint takes effect for the very next request, including for already-issued user sessions
and caller keys, and survives a restart by loading the persisted value at startup. The gate
is checked after the host/station edge and before any auth or business logic, so it cannot
be bypassed by holding a valid credential.

While on, the user-station API and the OpenAI-compatible exit are refused with a stable
`503 service_unavailable` envelope (`source: platform`, `Cache-Control: no-store`) for every
path except a strict allowlist:

- `/healthz` (both stations);
- the public site-config endpoints (`/api/config`, `/admin/api/config`) — so the user SPA can
  learn maintenance is on and render the notice, and the admin login screen can render before
  signing in;
- `/api/auth/logout` — so an already-logged-in user can end their session.

Everything else under `/api/*` (session, `me`, OAuth start/callback/elevate, endpoints,
models, issues, account export/delete, caller-key management) and all of `/v1/*` returns
`503`. The admin station (`/admin/api/*`) is never routed through the gate, so an operator can
always sign in, inspect configuration, and toggle maintenance off. The front-end maintenance
notice (shown from the public `maintenance_mode` value) is consistent with the server-side
`503`: both report the site as temporarily unavailable.

A live toggle applies the value to the runtime singleton first and then persists
it; a persistence failure reverts the runtime singleton to its previous value
(or the frozen canonical default when no prior row existed) so database and
runtime cannot drift; a failed runtime apply leaves both unchanged (fail
closed). Concurrent patches to the same key are serialized per handler so their
apply and persist orders cannot interleave and drift the two apart.
