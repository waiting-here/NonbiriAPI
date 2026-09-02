# NonbiriAPI HTTP API Contract (`v1.0.0-alpha.3`)

- Status: **published contract for the `v1.0.0-alpha.3` prerelease** (2026-08-28).
- Scope: `/v1/chat/completions` and `/v1/models` remain the only public OpenAI-compatible ingress endpoints. OpenAI-compatible and Anthropic-compatible upstream connectors sit behind that ingress; there is no public Anthropic-native API. Embeddings, images, audio, files, moderation, and batch remain deferred.
- Authority: this document defines the alpha.3 wire contract and is aligned with the integrated implementation, contract tests, accepted security/data-lifecycle decisions, and the emitted stable-code set of `internal/httperr`. Documentation errata may correct an omitted or misstated field without changing runtime behavior; other changes to endpoint paths, JSON fields, stable codes, auth behavior, cache policy, or diagnostic limits require a subsequent contract and changelog entry.

## 1. Conventions

### 1.1 Stations and hosts

Two physically host-isolated stations share one binary:

| Station | Host | Serves |
|---|---|---|
| User station | `<userHost>` (derived from the configured site origin) | the OpenAI-compatible ingress (`/v1/*`), normal-user self-service API (`/api/*`), the user SPA |
| Admin station | `<adminHost>` (`admin.` prefix of, or env override on, `<userHost>`) | administrator API (`/admin/api/*`), the admin SPA |

Host selection is a security control (trusted-proxy forwarding, distinct-host enforcement,
client-IP/CIDR validation) and is enforced before station routing. The contracts below assume a
request arrived at the correct station host. A request reaching the wrong host is not
authorized to reach another station's API.

`GET /healthz` is an unauthenticated liveness probe on both hosts; it never touches the database and returns `{"status":"ok"}`.

`GET /api/config` on the user host and `GET /admin/api/config` on the admin host are unauthenticated, `no-store` bootstrap endpoints. They return only `site_name`, `site_logo_url`, the four bounded legal override texts, `legal_authoritative_locale`, `maintenance_mode`, `registration_open`, and the read-only `announcement_epoch`. Operational limits, Discord gate IDs, alert preferences, and secrets are never projected. Other methods return `method_not_allowed`.

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
  favor of the code-derived default. *(Introduced in `v1.0.0-alpha.2`.)*
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
  at the refusal and `resource` is the stable resource name (`endpoint`, `endpoint_key`,
  `model`, `binding`). They are omitted by every other code.

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
| `unbound_model` | 503 | resolved a platform model with no currently usable binding chain (for example a zero-binding draft or bindings whose endpoint/key/cache availability predicates fail); a user issue is recorded. Bindings rejected only because the request cannot be represented by their Connector produce `invalid_request` instead |
| `upstream` | 502 | upstream error after the commit boundary, a single attempt's upstream error with silent retry off, or all bindings exhausted under silent retry |
| `service_unavailable` | 503 | internal service unavailability, or the server-side maintenance gate refusing user-station API and `/v1/*` while `maintenance_mode` is on (§6) |
| `resource_limit_exceeded` | 422 | a per-parent resource count reached the configured cap: endpoints per user (the explicit user value when non-null, otherwise `default_endpoint_limit`), endpoint keys per endpoint (`default_endpoint_key_limit`), platform models per user (`default_model_limit`), or bindings per model (`default_binding_limit`); the envelope carries `limit` (the effective cap) and `resource` (the stable name); the count and cap are read inside the same transaction as the insert so a concurrent add cannot breach the cap; existing rows are retained |
| `insufficient_credits` | 403 | the caller's 悠哉积分 balance cannot cover the action: an administrator credit adjustment that would push the balance below its floor, or a charity routing admission whose pre-reserve debit fails the conditional balance check (the refusal happens strictly before any upstream dispatch); `source` is `platform` |
| `feature_disabled` | 403 | stable code for a feature gate; `source` is `platform`; the check-in rail (§3.11) is the wired trigger: every unavailable cause — timezone not configured, check-in disabled, a level-gated denial, or a corrupt configuration — is the identical envelope (code, status, message, source) so the reason is never revealed |
| `charity_suspended` | 403 | the caller's charity eligibility is currently suspended (`charity_suspended_until` in the future); charity routing refuses before any reservation or dispatch, while everything outside the charity rail keeps working; `source` is `platform` |
| `content_too_short` | 400 | a charity request's counted message text is shorter than `charity_min_chars`; the message contains the bounded rune counts; `source` is `platform` |
| `already_checked_in` | 409 | the user already checked in on the current site-local day (今日已签到); the day was consumed by the earlier successful check-in, not by this attempt; `source` is `platform` |
| `checkin_cap_reached` | 403 | the user's credit balance has reached the configured check-in threshold (`credits_cap_milli`) at an effective level below the bypass (≥3); the refusal does NOT consume the day; the message contains 悠哉积分已达签到上限; `source` is `platform` |

*The maintenance-gate meaning of `service_unavailable` and the
`insufficient_credits` / `feature_disabled` / `charity_suspended` business triggers were
introduced in `v1.0.0-alpha.2`.*

### 1.7 Database generation and startup gate

Alpha.3 uses a fresh-only SQLite generation identified by raw-header `application_id=0x4E425249` and `user_version=1`; export `schema_version` is unrelated. A database path is accepted only when the main/WAL/SHM set is completely absent or when the existing set passes owner/file-shape, header, protected read-only snapshot, schema/foreign-key/index, and contextual v2 credential-envelope validation. Old, empty, unknown-generation, corrupt, unsafe, or unexpected sources are rejected before any source write, writable SQLite open, WAL change, worker, or listener. There is no runtime `ALTER`, repair, v1→v2 credential rewrite, or data import. A fresh database atomically seeds maintenance on and registration/games/Fishing off. See [deployment.md](deployment.md#alpha3-fresh-only-database-and-version-changes).

## 2. Platform ingress — `/v1/*` (CallerKey Bearer)

Auth: `Authorization: Bearer nbk_<secret>`. The secret is the caller's per-user call key
(never an upstream key). All responses are `no-store`.

### 2.1 `POST /v1/chat/completions`

OpenAI-compatible chat completions, streaming and non-streaming.

Request body uses the OpenAI Chat Completions shape. Ordinary call parameters are preserved and omitted parameters use upstream defaults; the routing `model`, server-authoritative `safety_identifier`, and streaming usage option are the explicit exceptions described below:

| Field | Required | Notes |
|---|---|---|
| `model` | yes | a non-empty opaque platform model full name: personal `provider/model` names are at most 129 Unicode runes, charity `[公益]provider/model` names are at most 133, and caller input is capped at 133; valid UTF-8, no control characters/DEL, and no leading/trailing Unicode whitespace; resolved against the caller's models only |
| `stream` | no | `true` selects SSE; omitted/`false` selects the single JSON response |
| `max_tokens`, `max_completion_tokens` | no | absent or JSON `null` means unspecified; either one valid integer in `[1,2147483647]` is accepted; if both are valid they must be equal. OpenAI attempts keep an unspecified limit absent; Anthropic attempts use the raw administrator default, or 65536 when that raw value is null. The fallback is not a cap |
| other fields | no | preserved, except caller `safety_identifier` is overwritten and streaming requests authoritatively merge `stream_options.include_usage=true` while retaining other `stream_options` members |

Server-side behavior (`v1.0.0-alpha.3`):

1. Authenticate the CallerKey and recheck the account state.
2. Acquire one user-wide in-flight permit. A null `concurrency_limit` uses 5; an explicit value is in `[1,100000]`. All CallerKeys, personal/charity routes, streams, and silent retries for that logical request share the one permit. A refusal is `429 rate_limited` without `Retry-After` and creates no RPM hit/penalty, request parse, model/candidate selection, credential access, DNS/egress, or charity reservation.
3. Reserve global/user RPM. If this fails, release the concurrency permit; only a real per-user RPM denial feeds the existing RPM anti-abuse callback.
4. Apply the 1 MiB body, strict JSON, protocol field, logical-model, and immutable model-policy validation. A Debug dry-run branches only after these common gates.
5. Resolve `model` to one of the caller's platform or visible charity models and filter bindings by request capability before decrypting or dialing. Cross-user resources never enter the candidate set. A mixed model may retain both Connector types; an unsupported request field can remove Anthropic candidates while leaving capable OpenAI candidates. When no binding can represent the request, the server returns `invalid_request` before any credential is decrypted or outbound request begins.
6. Select a binding from the compatible set by the model's `route_strategy`:
   - `ordered` — try bindings by `ord` ascending; advance only after a failure.
   - `random` — pure random pick per call (stateless).
7. Retry boundary = **committing no response-body bytes to the client yet**:
   - Failure before the boundary (connection / DNS / upstream error status with nothing
     streamed): by default return the error immediately; if the model's silent-retry option is
     on, record a user issue and try the next binding until success or exhaustion.
   - Failure after the boundary (streaming already started, first byte already sent): **no
     retry**; end the current stream with an error.
   Non-stream requests can retry up to the boundary, same option/default.
   Route resolution, all attempts, and backoff share one five-minute aggregate deadline; a
   candidate set never multiplies the single-attempt timeout into a longer logical request.
8. Inject the connector-specific hidden safety pseudonym for upstream risk attribution. OpenAI uses `safety_identifier`; Anthropic uses `metadata.user_id`. Caller values cannot override it.

The concurrency permit remains held until non-stream completion, verified stream termination, cancellation, panic recovery, or any error path. Dynamic decreases do not cancel existing permits; new calls are refused until active usage falls below the new value, while increases take effect immediately. Silent retry never takes a second permit.

**Safety-pseudonym behavior:** the service sends only `nbu_v3_` followed by the
52-character RFC 4648 base32 (without padding) encoding of an HMAC-SHA-256 digest. The process
derives one dedicated 32-byte key from the Vault with purpose
`nonbiriapi:safety-identifier:v3`. The HMAC message is the exact UTF-8 bytes of that purpose,
then the canonical-origin byte length as an unsigned four-byte big-endian integer, the origin
bytes, and the positive consumer user id as exactly eight big-endian bytes.

The canonical origin comes from the final, revalidated dispatch target through
`egress.CanonicalEndpointTarget`: exactly `scheme://canonical-host:effective-port`, with no
path. A path-only change therefore preserves the pseudonym; a scheme, host, effective-port,
deployment master-key, or consumer change produces a different value. Personal and charity
attempts by the same consumer use the same pseudonym only when they reach the same origin;
the charity donor identity remains relevant to credential ownership but never to this value.
The 59-character result contains neither the raw id nor a publicly verifiable unkeyed digest.
A caller-supplied `safety_identifier` is always overwritten. Invalid/non-canonical origin or
missing/closed factory state fails closed before credential decryption or dialing, and no v2 or
enumerable v1 identifier is sent as a fallback.

The derived key exists only in process memory: it is not persisted, logged, returned by the
platform API, or sent upstream. One shared factory serves both routing rails; shutdown stops
their new/in-flight work before best-effort clearing the retained key copy. The v3 rollout
intentionally rotates both the published alpha.1 value and the development-only v2 value once.
Upstream risk history keyed only by either previous value will not automatically carry over.
After that cutover, retaining the same deployment master key preserves continuity only inside
the same consumer + canonical-origin scope.

#### Upstream Connector contract

`openai-compatible` targets append `/chat/completions` to the configured API base and retain the validated OpenAI request/response behavior. `anthropic-compatible` targets append `/messages`, send only `x-api-key`, `anthropic-version: 2023-06-01`, JSON content headers, and the egress allowlist, and translate the response back to the public OpenAI shape. Model discovery uses `/models` (100 per page, at most 20 pages, 512-byte cursor, 1,000 models/1 MiB total). The configured base includes its version path, so a base ending in `/v1` never becomes `/v1/v1/...`.

An Anthropic candidate accepts only this strict public subset: `model`, `messages`, the two token-limit fields, `stream`, `temperature` and `top_p` in `[0,1]`, one stop string or 1–4 stop strings (≤256 bytes each), OpenAI function tools (≤128), `tool_choice` (`none|auto|required` or one named function), boolean `parallel_tool_calls`, `stream_options.include_usage`, and the platform-reserved safety identifier. Unknown or lossy fields such as `n`, `store`, penalties, seed, logprobs, response format, reasoning/vendor extensions, or caller metadata make that candidate incompatible rather than being silently discarded.

System/developer text is joined into Anthropic top-level `system`. User text and HTTPS or valid `data:image/{jpeg|png|gif|webp};base64,...` image blocks are supported. Assistant tool calls map to `tool_use`; a following tool result must match an outstanding real call id. Tool names match `[A-Za-z0-9_-]{1,64}`, arguments must be a JSON object for Anthropic translation, and orphan, duplicate, missing, or reordered tool results are rejected before egress. Unknown roles/content blocks, caller metadata, remotely fetched images, audio, and lossy fields are rejected.

Non-stream success requires one complete Anthropic message. Stop reasons map as follows: `end_turn|stop_sequence→stop`, `max_tokens|model_context_window_exceeded→length`, `tool_use→tool_calls`, and `refusal→content_filter`; an absent, unknown, or `pause_turn` terminal reason is an upstream protocol failure. Anthropic usage maps directly to the four authoritative buckets: ordinary input, cache creation input, cache-read input, and output. Invalid, negative, regressing, or overflowing usage becomes unknown instead of being guessed.

Streaming validates the Anthropic event/type pair and the full message/content-block state machine. `ping` is ignored; unknown outer events are safely ignored within bounds, but unknown content blocks are not. `message_delta.usage` is cumulative and must not regress. Only a valid `message_stop` emits the public terminal finish chunk, optional terminal usage chunk, and `[DONE]`; clean EOF never synthesizes success. A caller disconnect cancels the upstream immediately.

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

#### OpenAI-only experimental policies

`force_store_false` belongs to one physical OpenAI-compatible endpoint key and defaults to false. Only that key's owner can write it; donation/admin/steward projections may show it read-only. After the selected key is finally revalidated, a true value overwrites the unique top-level `store` value in place or deterministically inserts `store:false`. Retries on the same key reuse the value; a different key is revalidated independently. Anthropic keys do not expose the field, and true on a non-OpenAI key is `invalid_request`. An upstream may ignore `store:false` or reject the field, so the option is not a zero-retention guarantee.

`flatten_tool_calls` belongs to a personal or charity logical model and defaults to false. A personal owner can write it; only an administrator or a live level-5 steward can write the charity-model value. A donor has no model-level write permission merely because their key is bound. True requires all active bindings to be OpenAI-compatible; enabling or restoring an Anthropic binding is rejected in the same transaction.

When enabled, a valid structured call is removed from caller-visible `tool_calls`, `finish_reason:"tool_calls"` becomes `"stop"`, and the original assistant content is followed by fixed text blocks:

```text
<mx_tool name="write_file" id="call_abc123">
RAW_ARGUMENTS
</mx_tool>
```

The server never executes the tool, invents an id, appends `<mx_tool_result>`, or fabricates success. It restores history only when one assistant message contains complete, real, unique, immediately paired `<mx_tool>`/`<mx_tool_result>` blocks whose ids and tool names match the request tools; the message is all-or-none, and malformed, partial, conflicting, or excessive text remains ordinary text. Limits are 32 pairs/calls, 64 KiB per arguments/result, 256 KiB aggregate, 128-byte id, and 64-byte name. Literal reserved closing tags, duplicate ids/indexes, mixed indexed/unindexed calls, or structural conflicts fail safely. Streamed tool fragments are buffered only within these bounds and emitted after valid protocol termination so stream and non-stream final text are equivalent. When disabled, the existing OpenAI path is not scanned or transformed.

Both controls are marked Experimental and need no confirmation. Flattening changes protocol shape and can break normal tool workflows; it is only for an explicit text-compatibility requirement.

**Charity namespace (`[公益]`-prefixed `model`, additive).** A `model` starting with the reserved
prefix `[公益]` never resolves against personal models: it routes through the charity rail against
administrator-curated charity models backed by donated endpoint keys. The two namespaces are
disjoint by construction. Charity resolution requires the site-wide `charity_enabled` switch, a
non-suspended caller, and at least one live candidate chain (approved+enabled donation, enabled
donation key with a physical claim, enabled endpoint/key, fetched-model cache hit); every chain
element is re-verified inside one SELECT immediately before each dispatch attempt. One logical
call takes exactly one user credit pre-reserve; retries across donated keys atomically swap only
the donated key's reserve. Per-donation-key concurrency/RPM/usage-cap limits gate each candidate
after selection and before dispatch; a refused key moves to the next candidate and never feeds
any auto-ban statistic; when every key refuses, the exit is `rate_limited`. The first legal
response-body byte is the accounting commit boundary; usage settlement uses the price/discount
snapshot taken at reservation time and can drive the caller's balance negative or cross the
donated key's cap by frozen design.

Stable error codes at this endpoint:

| code | HTTP | When |
|---|---|---|
| `unauthorized` | 401 | missing / invalid / banned caller key |
| `forbidden` | 403 | request reached a forbidden station/security boundary; model ownership itself is resolved only inside the authenticated caller's tenant |
| `not_found` | 404 | `model` did not resolve to one of the caller's platform models (a disabled or absent `[公益]` model is indistinguishable from this) |
| `rate_limited` | 429 | per-user RPM, global RPM, or concurrency exhausted; for `[公益]` models also when every donated key refused its per-key admission |
| `payload_too_large` | 413 | request body exceeds the configured size bound |
| `unbound_model` | 503 | the resolved model has no currently usable binding chain (zero-binding draft or failed endpoint/key/cache availability predicates) — a user issue is recorded; for `[公益]` models, no donated candidate chain is currently usable or the per-token reserve price is unconfigured (fail closed). Connector capability incompatibility is `invalid_request`, not `unbound_model` |
| `upstream` | 502 | upstream error after the commit boundary, single-attempt upstream error with silent retry off, or all bindings exhausted under silent retry |
| `feature_disabled` | 403 | `[公益]` routing while the site-wide charity switch is off |
| `charity_suspended` | 403 | `[公益]` routing while the caller's charity eligibility is suspended |
| `insufficient_credits` | 403 | `[公益]` pre-reserve debit failed the conditional balance check (never dispatched) |
| `content_too_short` | 400 | `[公益]` pre-dispatch message text is shorter than `charity_min_chars`; only Unicode runes in `messages[].content` strings and typed text content parts count; no tokenization or personal request is inspected |
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
- **Charity projection (additive)**: while the site-wide `charity_enabled` switch is on, every
  enabled `[公益]…` charity model that has at least one usable donated-candidate chain is appended
  after the caller's own models (same entry shape; `owned_by` carries the curated provider name).
  While the switch is off the projection is empty — never an error.

Stable error codes: `unauthorized` (401); `rate_limited` (429); `internal` (500).

## 3. User station — `/api/*` (user session cookie)

Auth: a valid user session cookie. Banned users are denied. All responses are `no-store`.

### 3.1 Auth & session

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/auth/discord/start` | none | — | `302` to Discord's authorize URL; sets the OAuth `state` cookie (HMAC, short TTL, HttpOnly, SameSite). Before a state is issued, a per-client-IP admission throttle (configurable via site_config `oauth_start_rate_*`; `oauth_start_rate_limit=0` disables it) is applied as a second layer behind the reverse-proxy per-IP limit | `rate_limited` (429, with `Retry-After`) when the per-client-IP admission limit is exceeded; `service_unavailable` (503) if the Discord end is misconfigured or the admission throttle's bounded entry store is full (fail-closed, never evicts a live state) |
| `GET` | `/api/auth/discord/callback` | OAuth `state` cookie | query: `code`, `state` | on success: sets the user session cookie and `302` to the user SPA; on mismatch/failure: `400 invalid_request` / `401 unauthorized` | `invalid_request`, `unauthorized`, `conflict`, `service_unavailable` |
| `GET` | `/api/session` | user session | — | `200 {user:{id,username,avatar,avatar_url,guild_nick,guild_avatar_url,lang,is_banned,blocked_reason?,banned_until,charity_suspended_until,endpoint_limit,effective_endpoint_limit,rpm_limit,effective_rpm_limit,concurrency_limit,effective_concurrency_limit,credits,donation_credit,effective_level,manual_level,game_profile_public,created_at}}`; the three raw limits are nullable and their `effective_*` fields select only the raw value or documented fallback (concurrency fallback 5), not independent global gates; `banned_until`/`charity_suspended_until` are nullable unix seconds; `credits` is the single spendable canonical milli-credit balance and may carry `-`; `donation_credit` is a cumulative donor-reward statistic, not a second wallet; `effective_level` is resolved for this request; `manual_level` is nullable and display/state data only | `unauthorized` |
| `POST` | `/api/auth/logout` | user session | — | `204`; clears the session cookie | `unauthorized` |

Registration gate (Discord): registration is allowed only when `registration_open` is true and both the configured `discord_guild_id` and `discord_role_id` match the Discord identity. For a new identity, closed registration returns `302` to `/registration-closed`; a blank or invalid guild/role configuration returns `service_unavailable`, and a valid configuration that the identity does not match returns `unauthorized`. Already-registered users can still complete the same OAuth login regardless of the registration gate. The start endpoint
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
| `PATCH` | `/api/me` | user session | `{lang?, game_profile_public?}` | `200` same updated `{user:{…}}` envelope; at least one field is required; `lang` is exactly `zh` or `en`, `game_profile_public` is boolean | `invalid_request`, `unauthorized` |
| `GET` | `/api/me/usage` | user session | — | `200 {total_requests, total_uncached_input_tokens, total_cache_write_input_tokens, total_cache_read_input_tokens, total_output_tokens, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests}`; the four token buckets are the authoritative counters, the legacy prompt/completion totals remain compatibility mirrors | `unauthorized` |
| `GET` | `/api/logs` | user session | optional single-valued `model`, `error_code`, `status`, `from`, `to`, `page`, `page_size` | `200 {data:[…], has_more}`; each metadata-only row of the session principal's own requests is `{id, route_kind, model, endpoint_key_id, key_note, endpoint_note, endpoint_base_url, upstream_model_id, status_code, duration_ms, started_at, completed_at, uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens, prompt_tokens, completion_tokens, total_tokens, usage_unknown, error_code, error_source, error_diag, attempt_id}`; `key_note`/`endpoint_note` are the current notes of the caller's own resources and are empty once a resource is deleted; `endpoint_base_url` is the dispatch-time snapshot; rows with `route_kind='charity'` served through a donated key blank `endpoint_key_id`, `endpoint_base_url`, and `upstream_model_id` so donated resources are never exposed to the consumer | `invalid_request`, `unauthorized`, `internal` |
| `GET` | `/api/logs/options` | user session | — (any query parameter is `invalid_request`) | `200 {models:[…]}` — distinct non-empty platform model names from the caller's own retained logs, ascending, bounded server-side; the client may fuzzy-search this list locally but the server never substring-searches logs | `invalid_request`, `unauthorized`, `internal` |

A null stored endpoint/RPM/concurrency limit means its documented fallback applies. Normal users cannot change these limits.

User-station log filters match exactly: `model` against the caller's own platform model full name, `error_code` against the stable stored code, `status` in `[100,599]`, and `from`/`to` as non-negative Unix seconds (`started_at >= from`, `started_at < to`). Ownership is part of the SQL: only the session principal's own rows are ever reachable, and no response carries another user's resources.

`PATCH /api/me` accepts one or both of `lang` (`"zh"`/`"en"`) and `game_profile_public` (boolean); any other field (`rpm_limit`, `endpoint_limit`, `concurrency_limit`, `is_banned`, usage, id, …), an empty body, trailing JSON, or an oversized body is rejected with `invalid_request` by the strict decoder. Setting the game preference false immediately removes all identity fields from future leaderboard rows without modifying historical results. The response is the same `{user:{…}}` envelope as `GET /api/me`. This route is session-only:
neither an admin session nor a caller key can reach it.

### 3.4 Caller key

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/caller-key` | user session | — | `200 {display:"nbk_<head4>…<tail4>", created_at, updated_at}` — head/tail fragments only; the secret is never returned | `unauthorized`, `not_found`, `internal` |
| `POST` | `/api/caller-key/regenerate` | user session | — | `200 {secret:"nbk_…"}` — the full plaintext is returned **once**; the previous key is invalidated instantly and the new hash/display fragments are persisted in place | `unauthorized`, `internal` |

### 3.5 Endpoints

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints` | user session | query: optional `limit`, `cursor` | `200 {data:[…],next_cursor}` where each endpoint is `{id, connector_type, base_url, note, enabled, revision, key_count, created_at, updated_at}` (key secrets never appear) | `invalid_request`, `unauthorized` |
| `POST` | `/api/endpoints` | user session | `{base_url, connector_type?, note?, enabled?}` | `201` created endpoint; `base_url` is canonicalized (scheme/host lowercased, trailing dot removed, userinfo/query/fragment removed, redundant slashes collapsed; an explicitly typed port is preserved while an implicit default port is omitted from display/persistence but normalized in the internal origin key) | `invalid_request`, `unauthorized`, `conflict`, `resource_limit_exceeded` (explicit user endpoint cap when non-null, otherwise the site default) |
| `GET` | `/api/endpoints/{id}` | user session | — | `200` the endpoint object | `unauthorized`, `not_found` |
| `PATCH` | `/api/endpoints/{id}` | user session | `{base_url?, note?, enabled?}` | `200` updated endpoint; an empty or wholly unchanged patch is rejected; when any key exists, `base_url` may change only within the same canonical origin (scheme + canonical host + effective port) | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
| `DELETE` | `/api/endpoints/{id}` | user session | — | `204`; cascades to its keys, their fetched-model cache, and their bindings (immediate invalidation) | `unauthorized`, `not_found` |

Multiple endpoints may share the same canonical `base_url` (no uniqueness on
`base_url`); each keeps independent note/enabled state. The endpoint-count cap is the
non-null user override or, when raw is null, `default_endpoint_limit`; when the current count reaches the cap, new
endpoints are rejected (`resource_limit_exceeded`) while existing ones are retained. The admin can set a
per-user endpoint limit anywhere in `[0,10000]` and clear it (NULL restores the global default). An explicit value may exceed the default and is not clamped.

`connector_type` defaults to `openai-compatible` and is validated against the
authoritative connector registry; unknown types are rejected with
`invalid_request` and never silently fall back to another protocol. The
connector type is immutable after creation (a `PATCH` carrying `connector_type`
is rejected). The closed registry supports `openai-compatible` and `anthropic-compatible`;
unknown values never fall back. Both use a versioned API base URL, but their discovery and message paths, credentials, capability rules, and response validators remain connector-specific.

Model-discovery state is scoped to an endpoint key and exposed through that
key's catalog `evidence` object. Endpoints do not carry a duplicated discovery-failure
flag. Failed evidence contains only a closed safe class; bounded user issues are derived
from the same authority and never contain upstream response content.

**Credential/origin security behavior:** Endpoint updates enforce the credential/origin boundary in the same database
transaction as the update and key-existence check. An endpoint with any key
(enabled or disabled) cannot move to another canonical origin; delete all keys,
change the origin, then add new keys. A path change within the same origin is
allowed. Persisted endpoint credentials use a contextual `nbsec:v2` AES-256-GCM
envelope authenticated against purpose, version, user id, endpoint id, key id,
and the egress-canonical origin. Creating a key allocates its row id and seals
the credential in the same database transaction. Alpha.3 startup validates every current row against the exact context through its protected read-only snapshot; any v1, unknown, malformed, orphaned, or wrong-context row prevents startup without rewriting it. Runtime fetch and
forwarding accept only v2 under the exact context and never retry v1. Empty
objects and patches whose supplied values are all unchanged are
`invalid_request`. Automatic model fetching runs only after an actual same-origin
path change or a disabled-to-enabled endpoint transition; note-only changes,
representation-only URL changes, and enabled-to-disabled transitions do not
fetch. A rejected update is atomic and never queues a fetch.

### 3.6 Endpoint keys & fetched models

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/endpoints/{id}/keys` | user session | — | `200` array of `{id, display_head, display_tail, note, enabled, force_store_false?, created_at, updated_at}`; `force_store_false` is present only for OpenAI-compatible keys; display fragments are the first/last few runes of the secret so listings never decrypt; the secret and its ciphertext are never returned | `unauthorized`, `not_found` |
| `POST` | `/api/endpoints/{id}/keys` | user session | `{secret, note?, enabled?, force_store_false?}` | `201` created key metadata in the same safe shape (`force_store_false` defaults false and true is OpenAI-only); `secret` is sealed with contextual AES-256-GCM after its uncommitted row id is allocated and before the same transaction commits; plaintext is never a SQL value or response; triggers model fetch when enabled | `invalid_request`, `unauthorized`, `not_found`, `payload_too_large`, `resource_limit_exceeded` |
| `PATCH` | `/api/endpoints/{id}/keys/{keyId}` | user session | `{note?, enabled?, force_store_false?}` | `200` updated safe key metadata; only the owner can write the experimental boolean, and the secret remains immutable (rotation is delete + add) | `invalid_request`, `unauthorized`, `not_found` |
| `DELETE` | `/api/endpoints/{id}/keys/{keyId}` | user session | — | `204`; cascades to its fetched-model cache and bindings | `unauthorized`, `not_found` |
| `GET` | `/api/endpoints/{id}/keys/{keyId}/models` | user session | query: optional `limit`, `cursor` | `200 {evidence,automatic_entries,manual_entries,next_cursor}` for that (Endpoint, Key) combo; catalog entries use the closed `CatalogEntry` wire and `evidence` is the authoritative closed discovery state; a missing, cross-user, or wrong-endpoint combo is indistinguishable `not_found` | `invalid_request`, `unauthorized`, `not_found` |
| `POST` | `/api/endpoints/{id}/keys/{keyId}/models/refresh` | user session | — | `202 {operation_id,evidence}` once the idempotent discovery operation is accepted; `evidence.state` is `checking`, and the authoritative catalog supplies its eventual closed result | `unauthorized`, `not_found`, `conflict`, `rate_limited`, `service_unavailable` |

The per-endpoint key-count cap is `default_endpoint_key_limit` (default 20,
administrator-adjustable at runtime; see §4.4). When the count of keys on one endpoint
reaches the cap, a new key is refused with `resource_limit_exceeded` (422) carrying `limit`
and `resource`=`endpoint_key`; existing keys are retained. The count and the cap are read
inside the same transaction as the insert, so a concurrent add cannot breach the cap.

`force_store_false` is a strict JSON boolean: strings, numbers, and null are rejected. Anthropic-compatible create accepts false/omission for closed-shape compatibility but never exposes the field; true is `invalid_request`. A donated key remains the donor owner's physical key, so that owner can change the option directly. Admin and level-5 charity management views may display it read-only and have no write route.

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
protocol failures (HTTP status outside 2xx, truncated or malformed JSON, empty/
over-long/control-containing model ids) are not successes. They preserve the last committed
automatic catalog, atomically publish failed discovery evidence with a closed `safe_class`,
and derive a bounded `model_discovery` user issue for the endpoint key. Issue text never
contains the key, full response, or endpoint URL. HTTP 200 alone never counts as success;
duplicate model identifiers are preserved as distinct source entries and compiled into the
pair support count.

### 3.7 Models & bindings

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/models` | user session | — | `200` array of `{id, provider, model, full_name, route_strategy, silent_retry, flatten_tool_calls, binding_count, created_at, updated_at}` | `unauthorized` |
| `POST` | `/api/models` | user session | `{provider, model, route_strategy?, silent_retry?, flatten_tool_calls?}` | `201` created model; `provider`/`model` are free strings that allow `/` (each ≤64 runes, no control chars, no leading/trailing whitespace); the external name is `provider/model` and uniqueness is enforced on that full name; `route_strategy` defaults to `ordered`; `silent_retry` defaults false; `flatten_tool_calls` is a strict experimental boolean defaulting false, may be true for a zero-binding draft, and requires every existing/new binding to be OpenAI-compatible; a `provider` starting with reserved `[公益]` is rejected | `invalid_request`, `unauthorized`, `conflict` (full-name collision), `resource_limit_exceeded` |
| `GET` | `/api/models/{id}` | user session | — | `200` the model object | `unauthorized`, `not_found` |
| `PATCH` | `/api/models/{id}` | user session | `{provider?, model?, route_strategy?, silent_retry?, flatten_tool_calls?}` | `200` updated model (changing `provider/model` recomputes `full_name` and may collide); enabling flatten validates all bindings in the same transaction; reserved `[公益]` provider names are rejected | `invalid_request`, `unauthorized`, `not_found`, `conflict` |
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

A normal model may mix OpenAI-compatible and Anthropic-compatible bindings; request capability filtering precedes the route strategy. The exception is `flatten_tool_calls=true`: creating, restoring, or updating any binding so it resolves to an Anthropic endpoint is rejected atomically. A draft with no bindings may enable flatten and then accepts only OpenAI-compatible bindings.

A model may be saved with zero bindings (a draft). Calling a draft model over `/v1/chat/completions`
yields **503** (see §2.1). `provider`/`model` are stored and matched only as opaque strings
(never split into path segments for routing; never interpolated into SQL), so `/` has no
injection surface.

Bounded fields: `provider`/`model` are each ≤64 runes, must be valid UTF-8, must not contain
control characters, and must not start or end with whitespace (Unicode-aware); interior
whitespace and `/` are allowed. `upstream_model_id` is an opaque string bounded to ≤512 runes
with the same character rules; it must match a row of the key's fetched cache exactly, so a
client can never bind an arbitrary string. `ord` is an integer in `[0, 1000000]`. `silent_retry` and `flatten_tool_calls`
are booleans; a non-boolean value (e.g. the string `"true"`) is rejected with `invalid_request`,
and an unknown field is rejected by `DisallowUnknownFields`. These bounds are enforced
server-side on every create and patch; the unique `(user_id, full_name)` and
`(model_id, endpoint_key_id, upstream_model_id)` constraints are the backstop.

### 3.8 Account self-service (two-step required)

Both endpoints require `X-Elevated-Token` from §3.2.

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `POST` | `/api/account/export` | user session + elevated | — | `200` direct `application/json` attachment containing the bounded schema-v3 whitelist: user raw/effective limits, guild-profile snapshot, game-profile preference and level/economy state; endpoint/key metadata including `force_store_false`; personal models/bindings including `flatten_tool_calls`; caller-key display metadata; usage/log summary; credit ledger; daily activity; check-ins; owner donations; consumer charity summaries; donor-reward summaries; and the owner's game settlements/rounds/outcomes/best. Secret/ciphertext, Debug capture, RNG entropy, retry internals, idempotency hashes, shared charity-model configuration, and cross-party identity are excluded | `elevated_required`, `unauthorized`, `rate_limited`, `payload_too_large`, `internal` |
| `POST` | `/api/account/delete` | user session + elevated | `{confirm:"DELETE"}` | `204`; first invalidates session/CallerKey admission and cancels/forgets user forwarding, concurrency, game, and Debug state, then deletes all linked account rows including game and economy data; late callbacks are atomically suppressed and cannot resurrect the account | `elevated_required`, `unauthorized`, `conflict`, `internal` |

The export package excludes upstream secret material and cross-party identities by policy; it
carries enough metadata to be usable as a portable account export, but it is not an operator rollback snapshot. The package uses
`schema_version: 3` and an explicit server-side field whitelist; future database columns do
not enter it automatically. Each collection is capped at 10,000 rows and the complete JSON at 8 MiB; exceeding a bound fails closed rather than returning a truncated package. Donation rows contain safe submission/review metadata only;
consumer reservation summaries omit donated-resource identifiers, while donor-reward
summaries omit consumer identity.
The user section carries `credits` (the single spendable balance) and `donation_credit` (the cumulative donor-reward statistic, not a second wallet) as canonical decimal strings, and the level state as small integers (`manual_level` nullable — `null`
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

The `v1.0.0-alpha.2` co-management prefix for effective level ≥ 5 users, served on the **user station**
with the ordinary user session cookie. This contract lands the authorization frame and the
full-site log co-management route:

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
- **Full-site logs**: `GET /api/steward/logs` serves the level-5 full-site request-log
  projection. It reuses the administrator log shape and the
  bounded `page`/`page_size` + filter parameters, but applies the steward de-privacy
  projection: on `route_kind='charity'` rows the donor's `endpoint_key_id`, `endpoint_base_url`
  and `upstream_model_id` are blanked, because a level-5 steward co-manages site-wide
  log/activity, not donor resources. The steward therefore sees the consumer's `user_id`,
  `route_kind`, `endpoint_base_url`/`upstream_model_id` (for personal rows), `status_code`,
  token buckets and bounded `error_diag` — never a user-chosen platform model name, a note,
  a Discord identity, or any donated resource. Exact-match `endpoint_base_url` and
  `upstream_model` filters are evaluated against this already de-privatized projection, so
  guessing a donor URL or upstream model cannot reveal that a charity row exists. No bulk
  CSV/JSON export is exposed here.
- **Current state**: the donation review surface (`/api/steward/donations…`, §4.6) is mounted;
  charity model management is available under the same prefix (`/api/steward/charity-models…`, §4.6);
  full-site logs are available at `/api/steward/logs`. Sub-handlers receive only an opaque
  steward identity (user id + role); they never see another user's rows by construction of
  those projections.

### 3.11 Daily check-in

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/checkin` | user session | — (any query parameter is `invalid_request`) | while unavailable: `200 {"enabled":false}` and NOTHING else (no reason, no day, no range — timezone-unset is indistinguishable from every other disabled cause); while available: `200 {"enabled":true,"checked_in_today":bool,"credits":string,"award_min_milli":string,"award_max_milli":string,"credits_cap_milli":string}` — all amounts canonical decimal milli-credit strings (`credits` may carry `-`). The status read performs no writes and never triggers the lazy level promotion | `invalid_request`, `unauthorized`, `internal` |
| `POST` | `/api/checkin` | user session | — (any body or query parameter is `invalid_request`; the client never supplies the award or the day) | `200 {"award_milli":string,"credits":string}` — the drawn award and the new post-award balance, both canonical decimal strings | `invalid_request`, `unauthorized`, `feature_disabled`, `already_checked_in`, `checkin_cap_reached`, `forbidden`, `not_found`, `internal` |

Behavior:

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

### 3.12 Charity donations — `/api/donations` (user station)

*(Introduced in `v1.0.0-alpha.2`.)*

One donation = one owned endpoint plus at least one key. The review decision (approve/reject)
covers the WHOLE donation; the per-key concurrency/RPM/cap limits are attributes of the
donation-key rows (§L). All economic values are canonical decimal milli-credit strings;
no response ever contains secret material or key notes.

| Method | Path | Auth | Body / query | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/api/donations` | user session | strict single-valued `page` (from 1) / `page_size` (`[1,100]`, default 20); any other parameter is `invalid_request` | `200 {data:[donation], total, has_more}` (keys/reviews omitted) | `invalid_request`, `unauthorized`, `internal` |
| `GET` | `/api/charity/models` | user session | — (any query parameter is `invalid_request`) | `200 {data:[…]}` while the site-wide charity switch is on: every enabled `[公益]…` model with `{id, provider, model, full_name, enabled, pricing_mode, prices:{ten original milli-credit strings plus five current_* effective user-price strings under the discount valid at read time}, discount:{percent, enabled, start_at?, end_at?}, success_samples, success_count (last-100 protocol-success ring buffer), available, availability_reason (`ok`/`no_candidate`)}`; a disabled switch returns an empty list; the projection never carries a donated-key id or base URL. Display only: routing and billing are always recomputed server-side at reservation time | `unauthorized`, `internal` |
| `POST` | `/api/donations` | user session | `{description (required, ≤1024 runes), expires_at? (positive unix seconds or null = never), existing_endpoint:{endpoint_id, key_ids[], keys?:[{endpoint_key_id, max_concurrency?, rpm_limit?}]}}` OR `new_endpoint:{connector_type?, base_url, note?, enabled?, keys:[{secret, note?, force_store_false?, max_concurrency?, rpm_limit?}]}` — exactly one form; `force_store_false` follows the owner-only OpenAI key rules in §3.6 | `201` donation with keys | `invalid_request`, `unauthorized`, `feature_disabled` (while `donation_accept_enabled` is off), `not_found`, `conflict` (a selected physical key is already claimed by another active donation), `payload_too_large` (secret too long / body cap), `resource_limit_exceeded` (endpoint/key caps), `internal` |
| `GET` | `/api/donations/{id}` | user session | — | `200` donation + `keys[]` + `reviews[]` (append-only audit of the caller's own submission) | `invalid_request`, `unauthorized`, `not_found`, `internal` |
| `PATCH` | `/api/donations/{id}` | user session | pending-only owner edit: `{description?, expires_at? (null clears), keys?:{key_ids[], limits?[]}}`; empty body is `invalid_request` | `200` updated donation | `invalid_request`, `unauthorized`, `conflict` (already reviewed/deleted, or a replacement key is claimed elsewhere), `not_found`, `internal` |
| `DELETE` | `/api/donations/{id}` | user session | — (any query parameter is `invalid_request`) | `204` soft delete: claims released, status `deleted`, audit entry appended | `invalid_request`, `unauthorized`, `conflict` (already deleted), `not_found`, `internal` |

Behavior:

- **Intake gate**: POST alone requires `donation_accept_enabled`; a closed switch returns
  `feature_disabled` (403) and never blocks review, listing, editing, or deletion of existing
  donations.
- **Nested creation is one transaction**: the `new_endpoint` form creates the personal endpoint,
  seals every fresh secret contextually, inserts the donation keys and acquires all claims in a
  single transaction; ANY failure — including a claim conflict — rolls back the whole submission,
  leaving no orphan endpoint, no residual ciphertext, no half-created donation.
- **Physical key claim**: while the donation is pending, ALL of its keys hold claims; after
  approval only enabled keys do. The claim is an INSERT into `donation_key_claims` whose primary
  key on the physical key decides conflicts atomically; a conflicting claim aborts the whole
  operation with `conflict`. Nothing is ever merged and per-key limits are never stacked.
- **Per-key limits**: `max_concurrency` is integer `[0,100000]` and `rpm_limit` `[0,4096]`; `0` means unlimited and is not a fallback to any site/user/endpoint value. Donation creation/replacement normalizes absent or JSON null to 0. Reviewer partial updates use absent/null as unchanged and require explicit 0 to remove a limit. These gates throttle only charity routing, never the donor's own use
  of their key. `credits_usage_cap_milli`/`credits_used_milli`/`credits_reserved_milli` are set
  by reviewers and projected as canonical decimal strings.
- **Endpoint/key deletion guard**: deleting an endpoint or key referenced by a pending or
  approved+enabled donation fails with `conflict` (§3.5/§3.6); account deletion converges claims
  first and then cascades.
- **Review surface**: administrators use `/admin/api/donations…` and level-5 stewards
  `/api/steward/donations…` (§4.6); both share one service layer with per-frame identity.

### 3.13 Debug Hub

The control plane accepts only the current user-station login session and uses the normal same-origin/CSRF/session checks. The `/v1/chat/completions` data plane remains CallerKey-only. Every response and SSE stream is `no-store`; control bodies are limited to 1 KiB.

| Method | Path | Body | Success |
|---|---|---|---|
| `GET` | `/api/debug/session` | — | `{active:false}` or metadata for the one session bound to this user and browser login session; never captured content |
| `POST` | `/api/debug/session` | — | `201` new session metadata; replaces and zeroizes an older session; mode is always `dry` |
| `DELETE` | `/api/debug/session` | — | `204`; closes/zeroizes dry or live state, after which CallerKey requests use ordinary real forwarding |
| `POST` | `/api/debug/session/live-challenge` | — | one-time `{confirmation_id,...}` bound to the Debug session, login session, and generation; expires in 60 seconds |
| `PUT` | `/api/debug/session/mode` | `{mode:"dry"}` or `{mode:"live",confirmation_id}` | current metadata; live consumes one valid challenge, while dry is immediate |
| `GET` | `/api/debug/events` | `Accept: text/event-stream`; optional bounded `Last-Event-ID` header | event-version 1 stream for the current bound session; no query session id |

Metadata contains opaque `id`, `generation`, `mode`, creation/idle/absolute expiry, connection state, last event id, and public limits. The frontend displays a fresh accessible confirmation before each dry→live transition; direct API use still requires the one-time challenge. Confirmation is never remembered or stored.

An active session attaches within 30 seconds. Disconnect immediately downgrades live to dry and allows a 30-second dry-only reconnect; returning to live requires another confirmation. Idle lifetime is 10 minutes, absolute lifetime one hour, heartbeat 15 seconds, and each user has one session with at most two SSE subscribers. Process caps are 64 active sessions/128 MiB; a user ring is at most 4 MiB, 32 traces, and 128 retained events; one trace is 768 KiB. Raw caller, structured messages/tools, parameters, effective summary, and response preview are capped at 64/128/64/64/256 KiB. Capacity refuses start with `service_unavailable`; it never evicts another active user to make room.

Dry still performs CallerKey/account, concurrency-before-RPM, 1 MiB strict JSON, public-model, and model-policy checks, but it performs **no** Connector capability filtering, candidate selection, Vault access, secret decryption, DNS/egress, charity reservation, usage accounting, persistent request log, or product activity. It returns a valid non-stream or stream Chat Completions response with the fixed message `NonbiriAPI debug dry run: no request was sent upstream.`, no usage, and `X-Nonbiri-Debug-Mode: dry-run`.

Events are allowlisted `session_snapshot`, `trace_upsert`, `gap`, and `session_end`, with monotonic `seq`/trace `revision`. They expose ordered caller JSON/messages/tools/parameters and caller-visible status/response only after case-insensitive secret-field redaction and all normal response transforms. They never capture network headers, CallerKey/cookies, upstream credential/ciphertext, real safety pseudonym, base URL/origin, physical endpoint/key/binding/donation id, donor identity, or raw internal/upstream error. Live observer delivery is non-blocking and preserves caller status/header/body/flush/cancel semantics; Debug SSE disconnect or slowness never cancels or backpressures the real request.

Captured content exists only in bounded process memory and the current page component. It never enters the database, ordinary logs, alerts, export, backup, URLs, browser storage/cache, service workers, or telemetry. Close/replacement/expiry, logout, ban, deletion, invalid login binding, shutdown, or account lifecycle cleanup zeroizes it. If the active-session identity is uncertain the request falls back to dry; a confirmed absence means ordinary real forwarding.

### 3.14 Games and Pond Fishing

All game user APIs accept only a user session. JSON is strict and bounded; amounts are canonical milli-credit strings. `credits` is the one spendable balance used by charity routing and games, while game ledger events always keep `donation_credit_delta=0` so cumulative donor-reward statistics are unchanged.

| Method | Path | Request | Success / behavior |
|---|---|---|---|
| `GET` | `/api/games` | — | `{master_enabled,credits,game_profile_public,games:[{id:"fishing",version:1,enabled,params:{baits:[{id,price}],rtp_percent,treasure_multipliers}}]}` |
| `POST` | `/api/games/fishing/rounds` | header `Idempotency-Key` (1–128 bytes), body `{bait:"worm"|"lure"|"premium"}` | `200 {round_id,game_id,game_version,bait,price,credits,state,created_at,auto_settle_at,idempotent_replay}`; round, server outcome, entry debit, settlement, and activity update are one transaction. Same key+request replays; same key+different bait is `conflict` |
| `GET` | `/api/games/fishing/state` | — | at most one `pending_round` without outcome plus the earliest `unrevealed_result` and `has_more_unrevealed` |
| `POST` | `/api/games/fishing/rounds/{round_id}/settle` | no body | `200` typed outcome `{round_id,game_id,game_version,bait,price,species_key,tier,size_cm,is_junk,is_treasure,meter,credits_won,credits,settled_at,idempotent_replay}`; replay is idempotent |
| `POST` | `/api/games/fishing/rounds/{round_id}/ack` | no body | `204` first/replay; records reveal separately from economic settlement |
| `GET` | `/api/games/fishing/leaderboard?board=single|total` | exactly one optional `board`, default `single` | Top 20 plus optional `me`; `single` ranks best size, `total` sums committed payouts from the last 30 days |

Starting requires both game switches, adequate balance, no other pending round, and the per-user start limiter. Closing a switch blocks only new starts. A paid pending round automatically becomes due after 120 seconds; the worker scans every 30 seconds in batches of 100 and retries failures with bounded exponential backoff up to one hour. Pending rows are never age-deleted. Terminal round/settlement/outcome rows are removed in bounded maintenance batches strictly 30 days after `settled_at`; best and game ledger rows remain for the account lifetime.

The outcome and settlement are server-authoritative and use cryptographic random sampling; the client chooses bait and animates only. No catch or economic return is guaranteed. Level 4 never changes probability, price, payout, or admission.

Leaderboard identity is anonymous by default: such rows omit `display_name`, `avatar_url`, and `level4_badge` entirely. When a user opts in, the server dynamically projects the current guild nickname/username, a reconstructed static 64px PNG URL under `https://cdn.discordapp.com/`, and `level4_badge:true` when the current effective level is at least 4. No profile snapshot or stable anonymous id is stored in game results, and the server does not proxy/cache the avatar. Disabled, banned, admin, and deleted users do not participate.

Stable errors: invalid bait/body/idempotency is `400 invalid_request`; disabled start and insufficient balance are `403 feature_disabled` / `insufficient_credits`; another pending round, mismatched idempotency replay, or illegal settle/ack state is `409 conflict`; start admission is `429 rate_limited`; missing/wrong-owner/expired round is indistinguishable `404 not_found`; unavailable game storage/worker is `503 service_unavailable` or the shared `500 internal` boundary.

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
| `GET` | `/admin/api/users` | admin session | query: `page`, `page_size`, `is_banned?`, `q?` | `200 {data:[…], has_more}`; each row includes identity/ban state, `endpoint_limit/effective_endpoint_limit`, `rpm_limit/effective_rpm_limit`, `concurrency_limit/effective_concurrency_limit`, language, usage totals, `credits_balance`, cumulative `donation_credit_balance`, level state, and timestamps | `invalid_request`, `unauthorized`, `forbidden` |
| `GET` | `/admin/api/users/{id}` | admin session | — | `200` the same user shape with full usage metadata | `unauthorized`, `forbidden`, `not_found` |
| `PATCH` | `/admin/api/users/{id}` | admin session | profile mode `{endpoint_limit?, rpm_limit?, concurrency_limit?, lang?, level?}` **or** economy mode `{credits?, donation_credit?, operation_id, reason}` — never both in one request | `200` updated user; profile mode: explicit null clears a limit to its fallback (or resets `level` to automatic); economy mode returns the post-application spendable balance and cumulative statistic as `credits_balance` / `donation_credit_balance` | `invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `insufficient_credits` |
| `POST` | `/admin/api/users/{id}/ban` | admin session | `{reason?, duration_seconds?}`; absent or null duration = permanent, positive integer = lazy expiry deadline in seconds | `204`; atomically sets the ban (clearing `auto_banned`), deletes sessions, and deletes the caller key | `unauthorized`, `forbidden`, `not_found`, `invalid_request` |
| `POST` | `/admin/api/users/{id}/unban` | admin session | — | `204`; clears the ban/reason/deadline but does not recreate a caller key | `unauthorized`, `forbidden`, `not_found` |
| `DELETE` | `/admin/api/users/{id}` | admin session + `X-Elevated-Token` | — | `204`; same cleanup and late-write suppression guarantees as §3.8 | `elevated_required`, `unauthorized`, `forbidden`, `not_found`, `internal` |

Pagination is 1-based: `page` defaults to 1 and `page_size` defaults to 20 and clamps to `[1,100]`. Unknown, repeated, or malformed query parameters are `invalid_request`. The administrator row is never a list candidate and no response includes caller-key or session material.

`PATCH` accepts two disjoint modes. Profile mode accepts only `endpoint_limit` (`0..10000`), `rpm_limit` (`1..4096`), `concurrency_limit` (`1..100000`), `lang` (`zh`/`en`), and `level` (`1..5` or `null`). Explicit `null` restores the corresponding endpoint/RPM/concurrency fallback or resets `level` to automatic; absent means unchanged and `0` is invalid for RPM/concurrency. Explicit limits may be lower, equal to, or higher than defaults and are never clamped by them. `level` is the manual override: a stored `1..5` wins over the automatic level (level 5 — the steward capability — is reachable only this way), a reset returns the user to the persisted automatic high-water mark, and the override never disturbs that mark itself. Setting `level` on the administrator row is `forbidden`. Every row carries raw and effective limit fields; effective values select only raw-or-fallback and do not include independent global RPM/egress gates.

Economy mode adjusts `credits` (the signed, single spendable balance) and/or `donation_credit` (the cumulative donor-reward statistic, not a second wallet) as canonical decimal-string **increments** — never target values. Either delta requires a 1..128-character non-system `operation_id` and a non-empty bounded `reason`. The adjustment and append-only ledger row commit together; retrying one operation id returns the first result. The modes never mix. Cumulative donor reward cannot go below zero; spendable credits may legitimately become negative through frozen settlement semantics. Check-in and game awards use `credits`, and natural game ledger events always apply zero `donation_credit` delta.

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

Level-5 stewards reach a separate full-site log list at `GET /api/steward/logs` on the user station (§3.10): the same metadata-only row shape and the same bounded filter/paging parameters, with `endpoint_base_url`, `upstream_model_id`, and `endpoint_key_id` blanked on `route_kind='charity'` rows so no donated resource is identifiable. The `endpoint_base_url` and `upstream_model` exact-match predicates are applied to that visible projection, not to hidden donor columns; a guessed donor value therefore cannot select or count a charity row. There is no steward export.

The CSV/JSON exports are not paginated: a selection exceeding the finite row bound, or a rendered payload exceeding the byte bound, fails closed with `payload_too_large` — output is never silently truncated. Every CSV cell is sanitized before rendering: a leading `=`, `+`, `-`, `@`, TAB, CR, LF, BOM, or Unicode whitespace character gets a single-quote prefix so spreadsheet applications never interpret it as a formula.

The activity endpoint pages site-local day rollups with the same 1-based paging rules (`page_size` default 20, clamp `[1,100]`). A day key is the unix seconds of that day's local midnight under the explicitly configured fixed site offset (`site_timezone_offset_minutes`); no day key is ever generated while the offset is unset. Successful product activity means a protocol-successful API request, a successful check-in, a successfully created paid game round, or an administrator console configuration write — protocol failures and pre-dispatch failures never count. Both activity tables are retained for 400 days and cleaned by the bounded maintenance sweep; when an account is deleted, every affected day's site rollup is recomputed from the surviving per-user rows inside the deletion transaction, so no deleted contribution survives.

The endpoint overview groups endpoints by their already-stored canonical base_url (it never re-normalizes or widens the stored value), so cross-user sharing, same-user duplicates, path differences, and port differences are visible exactly as the egress concurrency gate sees them. Ordering is stable — groups by base_url ascending, expandable user entries by user_id ascending — so offset paging never drifts. `filter` is a literal substring matched against the stored base_url; LIKE metacharacters never widen it. `enabled_count` is how many of that user's endpoints in the group are enabled. No overview response carries an endpoint/key note, username, Discord identifier, connector secret, or ciphertext.

Plaintext upstream secrets never appear in any admin overview, log, or usage response.

### 4.4 Site configuration

| Method | Path | Auth | Body | Response | Stable codes |
|---|---|---|---|---|---|
| `GET` | `/admin/api/site-config` | admin session | — | `200` object of known keys → values (see below) | `unauthorized`, `forbidden` |
| `GET` | `/admin/api/site-config/catalog` | admin session | — | `200 {data:[…]}` sorted authority entries with key/group/type, bilingual title/description/unit, nullable/null-writable, raw default, effective fallback, min/max/step/allowed values, zero/null/empty semantics, independent gates, and write endpoint | `unauthorized`, `forbidden` |
| `PATCH` | `/admin/api/site-config/{key}` | admin session | `{value}` | `200` updated value | `invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict` (a `level_threshold_*_milli` write whose enabled chain is not strictly increasing, or a `checkin_award_*_milli` write whose resulting pair has min > max) |

Known `site_config` keys (the authoritative key set is enforced by the handler):

- `site_name`, `site_logo_url`, `announcement_epoch` (fresh-database, read-only opaque ID)
- `legal_privacy_override_zh`, `legal_privacy_override_en`, `legal_terms_override_zh`, `legal_terms_override_en`, `legal_authoritative_locale`
- `default_endpoint_limit` — global endpoint-count cap default
- `default_endpoint_key_limit` — global per-endpoint key-count cap default
- `default_model_limit` — global per-user platform-model cap default
- `default_binding_limit` — global per-model binding-count cap default
- `default_rpm_per_user` — global per-user RPM default
- `global_rpm` — site-wide RPM cap
- `default_per_endpoint_concurrency` — global per-endpoint concurrency cap (keyed by normalized `base_url`)
- `egress_global_concurrency` — egress global concurrency gate
- `anthropic_default_max_tokens` — optional integer `[1,2147483647]`; raw null uses 65536 for an Anthropic attempt whose caller omitted both limit fields. The fallback is not a clamp and PATCH null restores it
- `discord_guild_id`, `discord_role_id` — registration gate (both must match to allow new registration; either blank pauses registration)
- `oauth_start_rate_limit`, `oauth_start_rate_window_seconds`, `oauth_start_rate_penalty_seconds` — in-process per-client-IP OAuth/elevation-start admission
- `maintenance_mode`, `registration_open` — public boolean site-state toggles; fresh alpha.3 explicitly seeds true/false respectively
- `site_timezone_offset_minutes` — nullable integer, multiple of 30 in `[-720,+840]`; JSON `null` (the default) means "not configured" and is distinct from an explicit `0` (UTC). While unset, site-day-key features are force-disabled behind the ordinary `feature_disabled` error without revealing why. Once any check-in or activity row exists the value is frozen: every further write — including rewriting the identical value or after those rows are cleaned up — is refused with `conflict`. Clearing it with JSON `null` is always rejected
- `level_threshold_2_milli`, `level_threshold_3_milli`, `level_threshold_4_milli` — automatic promotion thresholds on cumulative `donation_credit`, which is a statistic rather than spendable balance. Enabled thresholds must strictly increase; writes validate atomically. The automatic level is an only-ever-raised high-water mark with no downgrade in alpha.3
- `checkin_mode` — the check-in three-way switch (§3.11), exactly `enabled` / `level_gated` / `disabled`; the default is `disabled`. An unknown stored value reads as `disabled` (fail closed, never an implicit enabled state)
- `checkin_award_min_milli`, `checkin_award_max_milli` — the daily check-in award range bounds (§3.11), as canonical non-negative decimal milli-credit strings; defaults `"40000000"` / `"60000000"` (4–6 USD equivalent). The pair is cross-validated: PATCHing either bound validates `min <= max` against the other key's current value in ONE transaction, so a rejected pair is `conflict` and nothing is written. Both write paths record the administrator console write as product activity in that same transaction
- `credits_cap_milli` — the check-in admission threshold (§3.11) as a canonical non-negative decimal milli-credit string; default `"250000000"` (25 USD equivalent). `0` = no threshold. It gates check-in admission only (effective level ≥ 3 bypasses) and never truncates an awarded amount
- `charity_enabled` — boolean, default `false`; the charity system master switch. While off, no new charity routing happens and the price table is hidden; in-flight reservations still settle
- `donation_accept_enabled` — boolean, default `false`; gates NEW donation submissions only (§3.12); review/routing of existing donations is unaffected
- `charity_token_reserve_milli` — nullable canonical positive decimal milli-credit string; **JSON `null` (the default) means "not configured"** and keeps every per-token charity model disabled (fail closed) — it can never be mistaken for an explicit `0`, and PATCH rejects both `null` and non-positive values
- `rpm_ban_threshold`, `rpm_ban_window_seconds`, `rpm_ban_duration_seconds` — integer RPM-violation window settings; defaults `5`, `86400`, `86400`; threshold `0` disables automatic RPM bans
- `charity_min_chars`, `charity_violation_deduct_milli`, `charity_violation_ban_seconds` — charity-only short-content guard and optional per-violation penalty; defaults `20`, `"0"`, `0`; `charity_min_chars=0` disables detection and the deduction may drive credits negative
- `charity_violation_window_seconds`, `charity_violation_ban_threshold`, `charity_violation_window_ban_seconds` — bounded in-memory short-content window settings; defaults `86400`, `0`, `0`; threshold/duration `0` disables the window ban
- `charity_suspend_window_seconds`, `charity_suspend_threshold`, `charity_suspend_duration_seconds` — bounded in-memory window settings for charity-only suspension; defaults `86400`, `0`, `0`; threshold/duration `0` disables suspension
- `games_enabled`, `game_fishing_enabled` — boolean start gates, both fresh-seeded false; closing them never blocks settlement of an existing paid round
- `game_fishing_bait_worm_price_milli`, `game_fishing_bait_lure_price_milli`, `game_fishing_bait_premium_price_milli` — canonical positive amount strings, defaults `"2500000"`, `"5000000"`, `"7500000"`
- `game_fishing_rtp`, `game_fishing_rtp_premium` — integers `[0,100]`, defaults 90/88
- `game_fishing_treasure_bottle_mult`, `game_fishing_treasure_clover_mult`, `game_fishing_treasure_shell_mult` — integers `[1,1000]`, defaults 2/3/5
- `alert_prefs_*` — bounded administrator alert-center preference namespace

Values are typed: integer keys accept only JSON integers, booleans only JSON booleans, and amounts canonical decimal strings with no exponent, sign decoration, leading zeros, or whitespace. The four legal overrides accept bounded multiline text up to 65,536 UTF-8 bytes and preserve newline/tab bytes through save/read/re-save. `anthropic_default_max_tokens` is the only optional integer that accepts PATCH null; `charity_token_reserve_milli` is optional on read but rejects null/zero on PATCH; the nullable site timezone distinguishes raw null from explicit UTC zero but cannot be cleared after write. Other keys reject null. `GET /admin/api/site-config` returns the typed editor projection: required keys use their documented effective default when no valid stored row exists, while nullable keys remain null. Thus an unset `anthropic_default_max_tokens` is not replaced with 65536 in this response; that effective fallback is catalog metadata and is applied only while compiling an Anthropic attempt. Unknown stored rows are never projected. The catalog is the authority for fallback and null/zero semantics. Generic PATCH of any game key returns `conflict`; games use the all-or-nothing endpoint in §4.7. `PATCH` of an unknown key is `not_found`.
`PATCH` responds `200 {key, value}` with the applied typed value. Anti-abuse windows are process-local, bounded, and intentionally absent from exports; a restart discards their event history.

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

### 4.6 Donation review & charity model management (admin + steward)

*(Introduced in `v1.0.0-alpha.2`.)*

Administrators reach these routes under `/admin/api/…` with an admin session; level-5 stewards
reach the SAME operations under `/api/steward/…` on the user station (§3.10, live-resolved
effective level ≥ 5). Both frames share one service layer; authorization is re-validated
server-side on every mutation and every operation appends an entry to the donation's
append-only review audit. There is no admin-side creation of donations on behalf of users.

| Method | Path | Body | Response | Stable codes |
|---|---|---|---|---|
| `GET` | `/donations` | strict `page`/`page_size`, optional single `status` (`pending/approved/rejected/deleted`) | `{data:[donation], total, has_more}` | `unauthorized`/`forbidden` (frame), `invalid_request`, `internal` |
| `GET` | `/donations/{id}` | — | full donation + keys + review history | `not_found`, `invalid_request` |
| `PATCH` | `/donations/{id}` | `{action: approve\|reject\|enable\|disable\|update, note?, expires_at?, keys?:[{id, max_concurrency?, rpm_limit?, credits_usage_cap_milli? (decimal string), enabled?}]}` | updated donation | `conflict` (wrong current state or a claim conflict), `invalid_request`, `not_found` |
| `DELETE` | `/donations/{id}` | — | `204` soft delete (claims released, audit entry appended) | `conflict` (already deleted), `not_found` |
| `GET`/`POST` | `/charity-models` , `/charity-models/{id}` | see below; create accepts strict `flatten_tool_calls?` | charity model rows + last-100 success rate + experimental boolean | standard set |
| `PATCH`/`DELETE` | `/charity-models/{id}` | partial fields including `flatten_tool_calls?` | updated / `204` | `conflict` (duplicate routing key or incompatible binding), `not_found` |
| `GET`/`POST` | `/charity-models/{id}/bindings` ; `PATCH`/`DELETE` …`/bindings/{bindingId}` | binding CRUD | binding rows with display fragments only | `not_found` (any candidate-predicate failure is indistinguishable from a missing row), `conflict` (duplicate triple) |

Behavior:

- **Whole-donation decisions**: approve requires status `pending` and defaults the donation to
  enabled; reject requires `pending`; enable/disable require `approved`. Reviewers may adjust the
  review-controlled fields at any decision — expiry, per-key limits, per-key `credits_usage_cap_milli`
  (string wire), and per-key enabled flag; disabling a key releases its claim in the same
  transaction, enabling re-acquires it (`conflict` when another active donation holds it).
- **Review history is never hard-deleted**: DELETE is soft; only account-lifecycle cascade
  removes audit rows.
- **Charity models**: `full_name = '[公益]'provider'/'model'` is derived server-side and UNIQUE;
  exactly one pricing table is interpretable (the non-current table's fields are zeroed on
  write); prices/rewards are non-negative integer milli-credit values (per-request fixed price,
  or per-million-token for each of the four buckets); `discount_percent ∈ [0,100]` (100 =
  original price, 0 = free) with an optional `[start,end)` interval that yields the original
  price before start/at-or-after end or while disabled.
- **Flatten permission**: only the administrator or a live effective-level-5 steward can write a shared charity model's `flatten_tool_calls`. A donor can edit only their own donated physical key and does not acquire model ownership. True validates all current bindings as OpenAI-compatible; an Anthropic binding cannot be added/restored while true. Donation-key `force_store_false` appears to reviewers read-only and remains writable only by its physical owner.
- **Token reserve fail closed**: creating OR enabling a `per_token` model while
  `charity_token_reserve_milli` is unset/non-positive fails with `conflict` — checked in the
  same transaction as the write.
- **Binding candidate predicate** (verified inside the INSERT..SELECT itself, never
  read-then-write): the charity model must be enabled; the donation approved+enabled and
  unexpired; the donation key enabled WITH a live claim; the physical endpoint/key alive and
  enabled; the upstream id present in that key's fetched cache.

### 4.7 Game configuration

`GET /admin/api/games/config` returns one typed snapshot:

```json
{
  "master_enabled": false,
  "fishing": {
    "enabled": false,
    "bait_prices": {"worm":"2500000","lure":"5000000","premium":"7500000"},
    "rtp_percent": {"standard":90,"premium":88},
    "treasure_multipliers": {"bottle":2,"clover":3,"shell":5}
  }
}
```

`PATCH /admin/api/games/config` accepts a same-shape partial object; absent members are unchanged, but an empty request is rejected. In one transaction the server reads the complete current raw/effective snapshot, merges all touched values, compiles all three bait engines, validates fixed junk probability, probability closure, scale `[0.1,3]`, and every checked int64 payout/addition, then writes all touched rows or none. Only an admin session can use this API; level-5 stewards and donors cannot. Generic site-config PATCH refuses all game keys so the economic validation cannot be bypassed.

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
- **Request/response content is never persisted by NonbiriAPI**: `request_logs` and ordinary logs hold metadata and bounded diagnostics only. A bound Debug session may show redacted, bounded content temporarily in process/current-page memory under §3.13, but never writes it to DB/log/alert/export/backup/browser storage. The selected OpenAI-compatible, Anthropic-compatible, or donor-provided upstream receives request content and may retain or process it under its own policy; `store:false` cannot guarantee otherwise.
- **Retention maintenance**: request logs are cleaned past 30 days; expired sessions and resolved alerts older than 30 days are purged at startup and every six hours. Terminal game round/settlement/outcome rows are cleaned 30 days after `settled_at`, pending paid rounds are never age-cleaned, and game best/ledger/profile preference remain for the account lifetime. Due game settlement runs before retention. Pending alerts are bounded by caps/deduplication and are not age-purged.
- **Ownership isolation**: every read/write path checks `user_id`; cross-user resources never
  enter the candidate set for routing or listing.
- **Deterministic routing**: when a full name cannot be resolved to exactly this user's model,
  the request is rejected (`not_found` / `unauthorized` as applicable), never guessed.
- **Atomic late-callback handling**: account deletion and late callbacks are linearized by an
  atomic conditional write, never by a read-then-write.
- **Lifecycle admission gate**: ban/delete first blocks new login-session and CallerKey admissions, invalidates credentials as applicable, cancels and drains the user's current public calls, and forgets concurrency/game/Debug state before the account mutation. A late forward/game/Debug callback cannot resurrect the user, balance, result, or public identity.

## 6. Server-side maintenance gate

*(Introduced in `v1.0.0-alpha.2`.)*

`maintenance_mode` is a server-side authoritative admission gate, not a front-end-only notice. A fresh alpha.3 database seeds it on and seeds registration/games/Fishing off; the operator must deliberately apply legal/configuration state and open the gates.
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
