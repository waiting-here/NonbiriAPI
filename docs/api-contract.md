# NonbiriAPI HTTP API Contract (v1.0.0-alpha.1 + unreleased security amendments)

- Status: **v1.0.0-alpha.1 release contract with explicitly marked unreleased hardening**
- Scope: v1.0.0-alpha.1 surface only. `/v1/chat/completions` and `/v1/models` are the only
  OpenAI-compatible exit endpoints in alpha.1; embeddings / images / audio / files /
  moderation / batch are deferred past the alpha.
- Authority: the base contract aligns to tag `v1.0.0-alpha.1`, its frozen requirements, the
  accepted credential / egress / data-lifecycle decisions, and the emitted stable-code set
  of `internal/httperr`. Text explicitly labeled **Unreleased security amendment** describes
  the current development branch and is not a claim about the published tag. Other changes
  to endpoint paths, JSON fields, stable codes, auth behavior, cache policy, or diagnostic
  limits require a subsequent release contract and changelog entry.

## 1. Conventions

### 1.1 Stations and hosts

Two physically host-isolated stations share one binary:

| Station | Host | Serves |
|---|---|---|
| User station | `<userHost>` (derived from the configured site origin) | the OpenAI-compatible exit (`/v1/*`), normal-user self-service API (`/api/*`), the user SPA |
| Admin station | `<adminHost>` (`admin.` prefix of, or env override on, `<userHost>`) | administrator API (`/admin/api/*`), the admin SPA |

Host selection is a security control (trusted-proxy forwarding, distinct-host enforcement,
client-IP/CIDR validation); it is implemented in a later rail. The contracts below assume a
request already arrived at the correct station host. A request reaching the wrong host is not
authorized to reach another station's API.

`GET /healthz` is an unauthenticated liveness probe on both hosts; it never touches the
database and returns `{"status":"ok"}`.

### 1.2 Authentication schemes

| Scheme | Where used | Carrier |
|---|---|---|
| CallerKey Bearer | platform exit `/v1/*` | `Authorization: Bearer nbk_<secret>` (the user's single per-user call key) |
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

A normal user holds exactly one caller key (`nbk_` prefix, no scope binding). Regeneration
invalidates the previous key the instant the new row is written; the plaintext is shown once
at generation time and never persisted in a recoverable form (only an irretrievable SHA-256
hash and head/tail display fragments are stored).

A banned user's caller key is treated as invalid at request time, so a banned key fails
platform-exit auth with `unauthorized`. Banning also keeps the user's sessions out of further
use.

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
{ "error": { "code": "<stable>", "message": "<human, bounded>",
             "diag": "<optional, bounded upstream context>", "request_id": "<optional>" } }
```

- `code` is a stable machine identifier. Codes may be added; an existing one is never
  redefined for a new meaning. Unknown codes map to HTTP 500 internally.
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
  `binding`). They are omitted by every other code.

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

Codes below are the emitted set; HTTP status is derived from the code.

| code | HTTP | Used for |
|---|---|---|
| `internal` | 500 | unexpected server error |
| `invalid_request` | 400 | malformed request body, invalid argument, validation failure |
| `unauthorized` | 401 | missing / invalid / banned caller key or session |
| `forbidden` | 403 | valid identity, not allowed for this resource |
| `not_found` | 404 | unknown model, endpoint, key, model, binding, or resource |
| `conflict` | 409 | uniqueness violation or illegal state transition |
| `method_not_allowed` | 405 | path matched but method did not |
| `rate_limited` | 429 | per-user RPM, global RPM, concurrency gate exhausted, or the per-client-IP OAuth start admission throttle (login start / elevation start) penalizing a caller |
| `payload_too_large` | 413 | request exceeds the configured size bound |
| `unbound_model` | 503 | resolved a platform model that has no usable binding (zero-binding draft / all bindings filtered out); a user issue is recorded |
| `upstream` | 502 | upstream error after the commit boundary, a single attempt's upstream error with silent retry off, or all bindings exhausted under silent retry |
| `service_unavailable` | 503 | internal service unavailability |
| `resource_limit_exceeded` | 422 | a per-parent resource count reached the configured cap: endpoint keys per endpoint (`default_endpoint_key_limit`), platform models per user (`default_model_limit`), or bindings per model (`default_binding_limit`); the envelope carries `limit` (the effective cap) and `resource` (the stable name); the count and cap are read inside the same transaction as the insert so a concurrent add cannot breach the cap; existing rows are retained |

## 2. Platform exit — `/v1/*` (CallerKey Bearer)

Auth: `Authorization: Bearer nbk_<secret>`. The secret is the caller's per-user call key
(never an upstream key). All responses are `no-store`.

### 2.1 `POST /v1/chat/completions`

OpenAI-compatible chat completions, streaming and non-streaming.

Request body (OpenAI Chat Completions shape; call parameters are passed through verbatim —
parameters the caller did not send are **not** injected, so the upstream applies its own
defaults):

| Field | Required | Notes |
|---|---|---|
| `model` | yes | a platform model full name `provider/model`; resolved against the caller's models only |
| `stream` | no | `true` selects SSE; omitted/`false` selects the single JSON response |
| other fields | no | forwarded as-is (incl. `stream_options.include_usage` when the caller sets it) |

Server-side behavior (frozen contract; implementation in a later rail):

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
4. Inject the OpenAI `safety_identifier` field = a stable, irreversible hash of the calling
   user's id, for upstream per-user risk attribution.

Response — non-stream (`stream` false/omitted): standard OpenAI Chat Completions response,
including `usage` (`prompt_tokens`, `completion_tokens`, `total_tokens`) when the upstream
returned it. HTTP 200 alone is not success; success requires a complete, protocol-valid body.

Response — stream (`stream: true`): `Content-Type: text/event-stream`. A sequence of
`data: <json chunk>\n\n` frames shaped as OpenAI Chat Completions chunks, terminated by
`data: [DONE]\n\n`. When the caller asked for usage and the upstream supports it, an accurate
`usage` is taken from the terminal chunk. HTTP 200 is not success: an explicit `[DONE]` frame
(or a clean protocol termination) is required; an incomplete stream is never synthesized as a
success. If the client disconnects, the cancellation is propagated upstream and concurrent
slots / reservations are released; any unreadable `usage` is recorded with the
`usage_unknown` flag rather than fabricated token values.

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
| `forbidden` | 403 | caller identity valid but not permitted (e.g., not the key owner) |
| `not_found` | 404 | `model` did not resolve to one of the caller's platform models |
| `rate_limited` | 429 | per-user RPM, global RPM, or concurrency exhausted |
| `payload_too_large` | 413 | request body exceeds the configured size bound |
| `unbound_model` | 503 | the resolved model has no usable binding (zero-binding draft / all bindings filtered out) — a user issue is recorded |
| `upstream` | 502 | upstream error after the commit boundary, single-attempt upstream error with silent retry off, or all bindings exhausted under silent retry |
| `internal` | 500 | unexpected failure |

(within an already-started stream, an error is emitted as an SSE error frame rather than a
fresh HTTP error envelope, because the response status and headers have already been written.)

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
| `GET` | `/api/session` | user session | — | `200 {user:{id,username,avatar,lang,is_banned,endpoint_limit,rpm_limit,created_at}}` | `unauthorized` |
| `POST` | `/api/auth/logout` | user session | — | `204`; clears the session cookie | `unauthorized` |

Registration gate (Discord): registration is allowed only when both the configured
`discord_guild_id` and `discord_role_id` match the Discord identity. If either is blank,
registration is paused (the callback returns `service_unavailable` for a new identity),
while already-registered users can still complete the same OAuth login. The start endpoint
may still issue a short-lived state so an existing user can log in. The exact scope/guild/
nickname policy is finalized when Discord is wired.

### 3.2 Two-step elevation (for self-service export / delete)

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `POST` | `/api/auth/elevate` | user session | — | initiates a fresh Discord re-authorization for an elevated-action capability (HMAC state, short TTL); on completion the session carries an elevated capability until it expires / is consumed. The same per-client-IP admission throttle as login start is applied (shared instance, keyed by ClientIP) before a state is issued | `unauthorized`, `service_unavailable`; `rate_limited` (429, with `Retry-After`) when the shared per-client-IP admission limit is exceeded |

Self-service destructive endpoints (`3.8`) require an **active elevated capability**. The
carrier is the header `X-Elevated-Token: <token>` (a short-lived, single-use bound capability).
A request without / with an expired / with an already-consumed token is `403 forbidden`
(`elevated_required`).

### 3.3 Profile & usage

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/me` | user session | — | `200` user object (id, username, avatar, lang, is_banned, blocked_reason if any, endpoint_limit, rpm_limit, created_at) | `unauthorized` |
| `PATCH` | `/api/me` | user session | `{lang?, rpm_limit?}` | `200` updated user object (`rpm_limit` is clamped to the global cap; clearing sends null to restore global default semantics within the user self-tunability band) | `invalid_request`, `unauthorized` |
| `GET` | `/api/me/usage` | user session | — | `200 {total_requests, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests}` | `unauthorized` |

Self-tunable `rpm_limit` is bounded by the global cap; it cannot exceed it. A null
user-level limit means the global default applies.

`PATCH /api/me` accepts **only** `lang` (`"zh"` or `"en"`) and `rpm_limit`
(non-negative integer, or `null` to restore the global default); any other field
(`endpoint_limit`, `is_banned`, usage, id, …) is rejected with `invalid_request`
by the strict decoder (unknown fields, trailing tokens, and oversized bodies are
also rejected). The response is the same `{user:{…}}` envelope as `GET /api/me`,
with `rpm_limit` already clamped to the global cap. This route is session-only:
neither an admin session nor a caller key can reach it.

### 3.4 Caller key

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/caller-key` | user session | — | `200 {display:"nbk_<head4>…<tail4>", created_at, updated_at}` — head/tail fragments only; the secret is never returned | `unauthorized` |
| `POST` | `/api/caller-key/regenerate` | user session | — | `200 {secret:"nbk_…"}` — the full plaintext is returned **once**; the previous key is invalidated instantly and the new hash/display fragments are persisted in place | `unauthorized`, `rate_limited` |

### 3.5 Endpoints

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints` | user session | — | `200` array of `{id, connector_type, base_url, note, enabled, model_fetch_failed, model_fetch_failed_at, created_at, updated_at}` (key secrets never appear) | `unauthorized` |
| `POST` | `/api/endpoints` | user session | `{base_url, connector_type?, note?, enabled?}` | `201` created endpoint; `base_url` is canonicalized (scheme/host lowercased, trailing dot removed, userinfo/query/fragment removed, redundant slashes collapsed; an explicitly typed port is preserved while an implicit default is omitted from persistence but normalized in the internal origin key) | `invalid_request`, `unauthorized`, `conflict`, `resource_limit_exceeded` (endpoint cap reached: `min(global_default, user_limit)`) |
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
allowed. Empty objects and patches whose supplied values are all unchanged are
`invalid_request`. Automatic model fetching runs only after an actual same-origin
path change or a disabled-to-enabled endpoint transition; note-only changes,
representation-only URL changes, and enabled-to-disabled transitions do not
fetch. A rejected update is atomic and never queues a fetch.

### 3.6 Endpoint keys & fetched models

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints/{id}/keys` | user session | — | `200` array of `{id, display_head, display_tail, note, enabled, created_at, updated_at}`; display fragments are the first/last few runes of the secret so listings never decrypt; the secret and its ciphertext are never returned | `unauthorized`, `not_found` |
| `POST` | `/api/endpoints/{id}/keys` | user session | `{secret, note?, enabled?}` | `201` created key metadata `{id, display_head, display_tail, note, enabled, created_at, updated_at}` (`secret` is sealed with AES-256-GCM before persistence and never returned); triggers a model fetch for this key when enabled | `invalid_request`, `unauthorized`, `not_found`, `payload_too_large`, `resource_limit_exceeded` |
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
is not applicable (caller key is separate); when an endpoint key is deleted/disabled, its
cache rows and bindings no longer participate in routing (immediate invalidation by cascade).

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
| `POST` | `/api/models` | user session | `{provider, model, route_strategy?, silent_retry?}` | `201` created model; `provider`/`model` are free strings that allow `/` (each ≤64 runes, no control chars, no leading/trailing whitespace); the external name is `provider/model` and uniqueness is enforced on that full name; `route_strategy` defaults to `ordered` and only `ordered`/`random` are accepted; `silent_retry` is an optional boolean that defaults to `false` (fail fast) and, when `true`, silently tries the next binding after a pre-commit upstream failure until success or exhaustion (it never retries after any response byte is committed) | `invalid_request`, `unauthorized`, `conflict` (full-name collision), `resource_limit_exceeded` |
| `GET` | `/api/models/{id}` | user session | — | `200` the model object | `unauthorized`, `not_found` |
| `PATCH` | `/api/models/{id}` | user session | `{provider?, model?, route_strategy?, silent_retry?}` | `200` updated model (changing `provider/model` recomputes `full_name` and may collide) | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
| `DELETE` | `/api/models/{id}` | user session | — | `204`; cascades to its bindings | `unauthorized`, `not_found` |
| `GET` | `/api/models/{id}/bindings` | user session | — | `200` array ordered by `ord`: `{id, endpoint_key_id, upstream_model_id, ord}` | `unauthorized`, `not_found` |
| `POST` | `/api/models/{id}/bindings` | user session | `{endpoint_key_id, upstream_model_id, ord?}` | `201` created binding; `endpoint_key_id` must belong to one of the user's enabled endpoints and the key itself must be enabled; `upstream_model_id` must exist in that key's fetched cache; `ord` defaults to `0` and must be within `[0, 1000000]` | `invalid_request`, `unauthorized`, `not_found`, `conflict` (duplicate `(model_id, endpoint_key_id, upstream_model_id)`), `resource_limit_exceeded` |
| `PATCH` | `/api/models/{id}/bindings/{bId}` | user session | `{ord?, upstream_model_id?}` | `200` updated binding; only `ord`/`upstream_model_id` are mutable (the endpoint key never changes through this path); the resulting `upstream_model_id` must still exist in the binding key's fetched cache and the key/endpoint must still be enabled | `invalid_request`, `unauthorized`, `not_found`, `conflict` (duplicate triple after update) |
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
| `POST` | `/api/account/export` | user session + elevated | — | `200` JSON export package (user info, endpoint metadata, models, caller-key metadata, usage totals, log summary) **without any plaintext upstream secret**; download bound to a short token | `forbidden`, `unauthorized`, `rate_limited`, `internal` |
| `POST` | `/api/account/delete` | user session + elevated | `{confirm:"DELETE"}` | `204`; deletes the account and all linked data (endpoints, keys, models, bindings, request logs, caller key, orphan alerts/callbacks); late callbacks against a deleted account are atomically suppressed server-side so the insert no-ops; the current session is invalidated | `forbidden`, `unauthorized`, `conflict`, `internal` |

The export package excludes upstream secret material by policy; it carries enough metadata to
be usable as a portable account snapshot. The exact field list is finalized with the export
rail but the scope (no plaintext secret) is frozen here.

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
| `POST` | `/admin/api/auth/elevate` | admin session | `{password}` | grants an elevated-action capability (admin re-inputs password as the second factor), used by §4.2 destructive actions; capability consumed at use | `unauthorized`, `forbidden` |

### 4.2 User management

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/users` | admin session | query: pagination + filters | `200` array of `{id, username, avatar, discord_id, is_banned, banned_reason, endpoint_limit, rpm_limit, usage totals, created_at}` (no caller-key secret) | `unauthorized`, `forbidden` |

The users list returns the `{data:[…], has_more}` envelope. Pagination: `page`
(1-based, default 1) and `page_size` (clamped to `[1,100]`, default 20); the
`is_banned=true|false` filter narrows to banned/active users. Unknown, repeated,
or malformed query parameters are `invalid_request`. The administrator row is
never a candidate, and the projection never includes caller-key or session
material.
| `GET` | `/admin/api/users/{id}` | admin session | — | `200` the user object with full usage metadata | `unauthorized`, `forbidden`, `not_found` |
| `PATCH` | `/admin/api/users/{id}` | admin session | `{endpoint_limit?, rpm_limit?, lang?}` | `200` updated user; `endpoint_limit`/`rpm_limit` nullable, NULL restores the global default; `rpm_limit` bounded by the global cap | `invalid_request`, `unauthorized`, `forbidden`, `not_found` |

`PATCH /admin/api/users/{id}` accepts only `endpoint_limit` (integer `≥0`),
`rpm_limit` (integer `≥1`, rejected above the current global per-user cap — the
administrator raises that cap via `default_rpm_per_user` first), and `lang`
(`"zh"`/`"en"`). An explicit `null` clears a limit back to the global default;
an absent field is unchanged. Unknown fields, trailing tokens, an empty body,
and oversized bodies are `invalid_request`; the administrator row is
`forbidden`; a missing user is `not_found`.
| `POST` | `/admin/api/users/{id}/ban` | admin session | `{reason?}` | `204`; sets `is_banned` + `banned_reason`; the user's caller key and sessions are invalidated immediately | `unauthorized`, `forbidden`, `not_found` |
| `POST` | `/admin/api/users/{id}/unban` | admin session | — | `204`; clears `is_banned`/`banned_reason` | `unauthorized`, `forbidden`, `not_found` |

Ban performs the flag update, the session deletion, and the caller-key deletion
in one transaction, so request-time session auth and platform-exit caller-key
auth are invalidated atomically (`reason` is optional, bounded to 1024 bytes,
control-character-free). Unban does not recreate a caller key: the user must
generate a new one.
| `DELETE` | `/admin/api/users/{id}` | admin session + elevated | — | `204`; same cleanup guarantees as §3.8 self-service delete, plus the duplicated-by-orphan-alerts atomic suppression hook | `forbidden`, `unauthorized`, `not_found`, `internal` |

### 4.3 Global logs, usage, overview

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/logs` | admin session | query: user, time range, model, status, pagination | `200` array of request-log rows (metadata only — never content): `{id, user_id, model, endpoint_key_id, upstream_model_id, status_code, duration_ms, started_at, completed_at, prompt_tokens, completion_tokens, total_tokens, usage_unknown, error_code, error_diag}` | `unauthorized`, `forbidden` |
| `GET` | `/admin/api/usage` | admin session | query: aggregation (site-wide, by user) | `200` aggregate usage totals | `unauthorized`, `forbidden` |
| `GET` | `/admin/api/overview/endpoints` | admin session | — | `200` array of `{id, user_id, connector_type, base_url, note, enabled, key_count, created_at, updated_at}` — **metadata only, no plaintext key** | `unauthorized`, `forbidden` |
| `GET` | `/admin/api/overview/models` | admin session | — | `200` array of `{id, user_id, provider, model, full_name, route_strategy, silent_retry, binding_count, created_at}` across all users | `unauthorized`, `forbidden` |

Plaintext upstream secrets never appear in any admin overview, log, or usage response.

### 4.4 Site configuration

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/site-config` | admin session | — | `200` object of known keys → values (see below) | `unauthorized`, `forbidden` |
| `PATCH` | `/admin/api/site-config/{key}` | admin session | `{value}` | `200` updated value | `invalid_request`, `unauthorized`, `forbidden`, `not_found` |

Known `site_config` keys (the authoritative key set is enforced by the handler):

- `site_name`, `default_locale`
- `default_endpoint_limit` — global endpoint-count cap default
- `default_endpoint_key_limit` — global per-endpoint key-count cap default
- `default_model_limit` — global per-user platform-model cap default
- `default_binding_limit` — global per-model binding-count cap default
- `default_rpm_per_user` — global per-user RPM default
- `global_rpm` — site-wide RPM cap
- `default_per_endpoint_concurrency` — global per-endpoint concurrency cap (keyed by normalized `base_url`)
- `egress_global_concurrency` — egress global concurrency gate
- `discord_guild_id`, `discord_role_id` — registration gate (both must match to allow new registration; either blank pauses registration)
- `alert_prefs_*` — administrator alert-center preferences

Values are typed: integer keys (`default_endpoint_limit` `[0,10000]`; the
resource-count caps `default_endpoint_key_limit`, `default_model_limit`,
`default_binding_limit` `[1,10000]`; RPM keys `[1,4096]`, matching the shared
limiter's event-store ceiling; concurrency keys `[1,100000]`) accept only JSON
integers, text keys only JSON strings (bounded,
no control characters), and `default_locale` only `"zh"`/`"en"`. `null` is
rejected (clearing a per-user limit is expressed through the user-level NULL
semantics; blanking a text gate value uses `""`). `GET` returns the effective
typed value for every known key (documented defaults when unset) and never
projects an unknown stored row; `PATCH` of an unknown key is `not_found`.
`PATCH` responds `200 {key, value}` with the applied typed value.

Runtime config changes do not require a restart: `default_rpm_per_user` /
`global_rpm` are applied to the shared flow-control controller and
`default_per_endpoint_concurrency` / `egress_global_concurrency` to the shared
egress gate through the runtime apply hook. A failed runtime apply fails closed
(DB untouched); a persistence failure after a successful apply reverts the
runtime singleton to its previous value, so DB and runtime cannot drift. The
resource-count caps (`default_endpoint_limit`, `default_endpoint_key_limit`,
`default_model_limit`, `default_binding_limit`) have no runtime singleton: each
create reads the cap inside its own transaction, so a change takes effect on the
next create without a restart.

### 4.5 Admin alerts

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/alerts` | admin session | query: `resolved` filter (`true`/`false`), `before_id` keyset cursor, `limit` | `200` array of `{id, kind, message, ref, subject_user_id, created_at, resolved, resolved_at}` | `unauthorized`, `forbidden`, `invalid_request` |
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
- **Retention maintenance**: request logs are periodically cleaned past 30 days; expired
  idle/absolute session rows are purged at startup and every six hours.
- **Ownership isolation**: every read/write path checks `user_id`; cross-user resources never
  enter the candidate set for routing or listing.
- **Deterministic routing**: when a full name cannot be resolved to exactly this user's model,
  the request is rejected (`not_found` / `unauthorized` as applicable), never guessed.
- **Atomic late-callback handling**: account deletion and late callbacks are linearized by an
  atomic conditional write, never by a read-then-write.
