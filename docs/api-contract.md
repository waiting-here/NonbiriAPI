# NonbiriAPI HTTP API Contract (`v1.0.0-rc.3`)

- Status: **source prerelease contract**. Deployment status is specific to each instance.
- Scope: the OpenAI-compatible ingress routes are `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/embeddings`. Chat supports OpenAI-compatible, Anthropic-compatible and native AI SDK Gateway v3 upstreams; embeddings support OpenAI-compatible and the strict Gateway text subset. There is no public Anthropic-native or rerank API.
- Authority: this document reflects the current source route registry, strict request/response types, stable error catalog, and contract tests. A future wire change requires a changelog entry; undocumented database fields never enter an API response automatically. Image generation is available only through the session-authenticated limited activity, not ordinary `/v1/images/generations` or personal/charity model routes.

## 1. Shared wire rules

### 1.1 Stations and authentication

| Station | Host | Authentication |
| --- | --- | --- |
| User | configured public host | user session for `/api/*`, except the steward automation routes in §6.3; CallerKey Bearer for `/v1/*` and those automation routes |
| Administrator | distinct configured admin host | administrator session for `/admin/api/*` |

Host selection is a security boundary. A route on the wrong host is `404 not_found`, and a user, administrator, or steward credential never changes station. `GET /healthz` is an anonymous liveness probe on both hosts and returns `{"status":"ok"}` without opening the database.

User and administrator session cookies are host-only, HttpOnly, SameSite=Lax, and Secure on HTTPS. Unsafe cookie-authenticated methods require the same validated origin (and compatible Fetch Metadata when supplied). `/v1/*` accepts only `Authorization: Bearer nbk_<secret>`; an upstream credential is never a CallerKey.

Account export/deletion and selected administrator legal-hold/user actions require a short-lived, single-use elevation capability bound to the active session. Current administrator/session authority and steward level are revalidated in sensitive read/write transactions. Levels 1–4 may be automatic; level 5 is a manually appointed trainee and level 6 a full steward. Administrators appoint or remove level 6; administrators and full stewards appoint or remove level 5. Full stewards cannot manage themselves, other level-6 users or administrators. Trainees have only the scoped charity permissions in §6.4.

#### Browser cross-origin access

The three exact public model routes support CORS: `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/embeddings`. Responses, including authentication, maintenance, validation, rate-limit and upstream errors and streaming responses, carry `Access-Control-Allow-Origin: *`. Browser clients supply their CallerKey explicitly in `Authorization` and use the default fetch credentials mode or `credentials: 'omit'`; `credentials: 'include'` is not supported. `Access-Control-Allow-Credentials` is never enabled. `Retry-After` is exposed to browser code.

A valid `OPTIONS` preflight needs no CallerKey and returns an empty `204` before maintenance, authentication or request admission. It does not call an upstream, consume caller limits, reserve credits or create a call log. It permits only the exact route's method, echoes validated requested header **names** (including Authorization, Content-Type and SDK metadata headers), and permits browser preflight caching for 600 seconds. It includes `Cache-Control: no-store` and `Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers`. Origin is not reflected; opaque browser origins such as `null` use the same wildcard permission.

Preflight Origin must be a single nonempty value of at most 2,048 bytes. Requested header names must form one comma-separated list of at most 4,096 bytes and 64 names, each at most 128 ASCII HTTP-token characters. Malformed preflight headers or query strings return `400 invalid_request`; unsupported methods return `405 method_not_allowed`. Ordinary OPTIONS without `Access-Control-Request-Method` retains the normal method rejection. Unknown, encoded or trailing-slash paths receive no CORS permission. Host validation still runs first, and all actual calls retain the usual CallerKey, authorization, routing and accounting checks.

Session, administrator and steward automation routes have no cross-origin permission. The cookie API's existing same-origin protection is unchanged. A failed browser preflight prevents the actual model request from being sent, so it cannot appear in user call logs.

### 1.2 Strict JSON, scalars, pagination, and replay

- Control-plane request objects reject unknown or duplicate fields, trailing JSON, invalid UTF-8, control characters, invalid enum case, and values outside the documented range. GET/HEAD requests reject bodies. Query parameters are closed, single-valued sets. OpenAI-compatible model requests retain bounded extension fields under the operation-specific rules below.
- Times are UTC Unix seconds as JSON numbers in `0..253402300799`. JSON booleans are true booleans. Omitted, `null`, empty, and zero are distinct.
- Credit amounts are canonical decimal strings: `0|[1-9][0-9]*` with an optional 1–3 decimal places. A leading `-` is permitted only for fields explicitly described as signed. Floating point is never used for accounting.
- Revisions, generations, sequences, exact counts, and values that may exceed the JSON safe-integer range are canonical decimal strings.
- New opaque IDs use a documented prefix plus 22 canonical raw-base64url characters. Important prefixes include `ann_`, `op_`, `req_`, `clm_`, `pol_`, `thu_`, `fb_`, `ll_`, `rpsq_`, `rps_`, `rpc_`, `rpt_`, `iss_`, `lgh_`, `sse_`, `gle_`, `dbs_`, `dbt_`, `dbe_`, `scn_`, and `mch_`.
- List endpoints retain the legacy `cursor` and `limit` mode and return `{data:[...],next_cursor:null|string}` unless a section states a different closed envelope. Numbered mode is selected by `page` or `page_size`; `page` is a canonical positive decimal from 1 through 2147483647 and `page_size` is 10, 20, 50, or 100, defaulting to 1 and 20. The modes are mutually exclusive, repeated/unknown values are rejected, and numbered responses add the route's `pagination` metadata with `next_cursor:null`; an excessive page clamps to the last page (empty results use page 1 of 1). Count, filters, decision time, and rows come from one authorized read snapshot. Cursors are authenticated, route/owner/filter-bound, expiring, and at most 512 bytes. A malformed, expired, or cross-scope cursor is `400 invalid_request`.
- State-changing routes require `Idempotency-Key` unless they are authentication/session/elevation/logout, CallerKey plaintext mutation, OpenAI-compatible model calls, an explicitly business-unique ACK/lease/check-in/tutorial mutation, alert set-state, or the per-item steward binding automation in §6.3. Control replay keys contain 22–128 URL-safe ASCII characters. Same key and same canonical request replay the stored status/body; the same key with a different request is `409 conflict`. Replay records last 24 hours.
- Dynamic API responses and every error carry `Cache-Control: no-store`.

Shared numbered-page metadata is `{page:string,page_size:number,total_items:string,total_pages:string}`. `page`, `total_items`, and `total_pages` use exact canonical decimal strings; only `total_items` may be zero. Nested `attempt_pagination` and `materials_pagination` use the same shape. Credit history retains its separately documented top-level pagination envelope.

### 1.3 Error envelope

```json
{
  "error": {
    "code": "conflict",
    "source": "platform",
    "message": "[NonbiriAPI] resource revision changed"
  }
}
```

`code`, `source`, and `message` are always present. `source` is `upstream` only for code `upstream`; it is `platform` otherwise. Platform messages receive the prefix exactly once. Messages are bounded to 1,024 UTF-8 bytes. Personal and charity upstream errors may include `upstream_code`, extracted from the upstream `code` or `type` and restricted to 1–64 ASCII letters, digits, underscores, dots, colons or hyphens. A bounded integer code is represented as a string. The optional `diag` remains limited to explicitly allowed owner-visible failures, sanitized to at most 4,096 bytes; charity errors omit it. Platform and Debug errors omit both context fields.

| HTTP | Stable code |
| ---: | --- |
| 400 | `invalid_request`, `content_too_short` |
| 401 | `unauthorized` |
| 403 | `forbidden`, `elevated_required`, `feature_disabled`, `insufficient_credits`, `charity_suspended`, `checkin_cap_reached` |
| 404 | `not_found` |
| 405 | `method_not_allowed` |
| 409 | `conflict`, `already_checked_in`, `debug_live_cancelled` |
| 413 | `payload_too_large` |
| 422 | `resource_limit_exceeded`, `debug_dry_run_intercepted`, `debug_live_result_captured` |
| 423 | `resource_locked` |
| 429 | `rate_limited` |
| 502 | `upstream` |
| 503 | `maintenance`, `service_unavailable`, `unbound_model` |
| 504 | `upstream` for an upstream timeout |
| 500 | `internal` |

Before a response starts, personal and charity calls preserve actual upstream error statuses in 400–599, with `code=upstream` and `source=upstream`. Transport or protocol failures use 502, with timeouts using 504. An HTTP success containing an explicit error envelope is also a failure and uses 502. After an SSE response starts, its HTTP status stays 200; an upstream failure emits one bounded `data: {"error":...}\n\n` frame and never fabricates `[DONE]`.

Recognizable JSON errors and plain-text errors retain a useful message after removing source URLs, domains, IP addresses, reflected credentials, private upstream model names and request identity markers. Common JSON, percent and HTML character encodings are normalized before removal. Only selected message/code fields are exposed to callers; upstream headers and arbitrary nested diagnostics are never forwarded. Empty, unreadable, oversized, HTML, malformed or ambiguous error bodies use a generic message. Sanitization may also omit a machine code or message that cannot be safely represented. Restricted original-error capture is a separate projection, capped at 1 MiB per event as described in §11; its raw bytes are never substituted for the caller's safe error envelope.

### 1.4 Database and export versions

The database remains Generation 2: SQLite `application_id=0x4E425249` and `user_version=2`. Supported sources include the complete rc.2 maintenance database at `db959c64674afc531046a63066de0464725d439c` and the administration maintenance database at `84018acbd594765c563cc0ee4083d206e0bd6a77`. Extensions and role migration are atomic; unknown or partial structures are rejected before source writes. Existing identities, balances, settled charges, saved games, configuration and legal overrides remain intact. Old manual level 5 becomes level 6; former model level-5 admission moves to level 6 and new level-5 admission initially copies level 4. Historical audit roles and idempotent receipts keep their original meaning and wire values; current GETs show current authority and masks. New input/output counters never invent a split of old total Tokens. Older binaries reject the new manifest; rollback requires the complete matching stopped snapshot. See [deployment compatibility](deployment.md#database-compatibility-and-version-changes).

Account export `schema_version=10` is independent of SQLite `user_version`.

### 1.5 Display time context

Ordinary user pages display and edit timestamps in the browser's local time zone. Administrator and steward pages use the configured fixed site offset; one notice per form or paired-time group identifies it and warns when it differs from the browser. Saved values and API fields remain UTC Unix seconds. An unchanged field preserves its original instant, including seconds not displayed by a minute-precision control.

`GET /admin/api/time-context` and `GET /api/steward/time-context` accept no query or body and return `{mode:"site",offset_minutes:480}`. The offset is an integer from -720 through 840 in 30-minute steps; `null` means the site time zone is not configured. The latter route requires a currently effective level-5 or level-6 steward session; administrators use the administrator host. Reads revalidate current session and role in the same transaction as the setting. No CallerKey permission or other configuration fields are added. Unavailable or unconfigured context disables time-dependent editing without silently substituting UTC or browser time.

The existing `GET /api/time-zones` and `GET /admin/api/time-zones` return the embedded zone registry. `GET {prefix}/time/resolve?local=YYYY-MM-DDTHH:mm:ss&time_zone=...` resolves an ordinary local wall clock through that registry, including daylight-saving gaps and repeats. Site-offset editing uses a fixed offset and has no daylight-saving ambiguity. Recurring quota time zones and explicitly defined activity business calendars retain their separate semantics.

## 2. OpenAI-compatible ingress

### 2.1 `GET /v1/models`

Returns the caller's currently routable personal models plus currently available charity models in the OpenAI list envelope:

```json
{"object":"list","data":[{"id":"provider/model","object":"model","created":1788000000,"owned_by":"provider"}]}
```

The list is sorted by external model ID. It never contains physical endpoint, key, binding, donation, price, or donor data. A charity model appears only while its feature gate is open, the caller's current effective level is allowed, and at least one real candidate is currently usable.

### 2.2 `POST /v1/chat/completions`

The request is the OpenAI Chat Completions shape. Its body limit defaults to 10 MiB and is configured by administrators with `model_request_body_limit_mib` (integer 1–64 MiB). One MiB is 1,048,576 bytes; exactly the configured size is accepted, and one extra byte returns 413 `payload_too_large`. Each request keeps one configuration snapshot across parsing and forwarding; saved changes apply to new requests. Proxy limits can impose an earlier rejection. `model` is a required opaque platform model name (at most 133 Unicode runes). `stream` selects OpenAI-compatible SSE. `max_tokens` and `max_completion_tokens` may be absent or null; if both are integers they must be equal and in `1..2147483647`. For OpenAI and Anthropic, a caller-supplied safety identifier is replaced by the existing server-generated user-and-origin pseudonym. Gateway does not send that safety field; its separate optional cost-attribution tag is controlled only by the administrator and defaults off.

Admission order is fixed: CallerKey/account, one user-wide concurrency permit, global/user RPM, strict body/model policy, Debug interception, model/capability resolution, candidate selection, credential/egress checks, then dispatch. A refused concurrency permit creates no RPM hit, candidate access, reservation, or penalty. One logical request retains one permit and one 1200-second aggregate deadline across retries and streaming. Upstream response headers may take up to 900 seconds; receiving them does not end the remaining output budget.

Short charity requests return `400 content_too_short` with the actual and minimum character counts. The server applies the current optional penalty, temporary ban, and charity suspension settings before any reservation or upstream attempt. Their rejection log and any credit penalty commit together; `X-Request-ID` identifies that log. Rejected requests count once with zero upstream tokens. The separate automatic RPM-ban window counts only charity requests denied by the site's per-user RPM limit. Personal resource calls, global RPM, concurrency limits, shared EndpointKey limits and upstream `429` responses do not contribute. Successfully intercepted Debug dry runs retain their zero-accounting and zero-log behavior.

On a per-user RPM denial, the server can attribute a charity violation only after decoding the reserved `[公益]` model namespace from a valid bounded chat or embedding request. This read is limited to the configured model-request body maximum and two seconds, or the earlier request deadline; at most 16 such reads run at once, without queuing. An unreadable or malformed body, or exhausted classification capacity, keeps the rate-limit rejection without adding an automatic-ban event. This does not select a candidate, access a credential, reserve credit or send upstream; admitted requests retain the order above.

Personal models use `ordered` or `random` routing. Request capability filtering happens before a credential is decrypted. Silent retry may advance only while no response-body byte has been committed. A clean EOF is never success: non-streaming requires a complete valid response, and streaming requires a valid protocol terminator and `[DONE]`. Client disconnect cancels upstream work.

`openai-compatible` appends `/chat/completions` to the configured versioned base. `anthropic-compatible` appends `/messages`, translates the strict supported subset, and sends only the required Anthropic key/version/content headers. The Anthropic subset supports system/developer and user/assistant text, HTTPS or bounded image data parts, OpenAI function tools and matched tool results, `temperature`, `top_p`, stop strings, tool choice, parallel-tool indication, and streaming usage. Lossy or unsupported fields make that candidate incompatible; they are never silently dropped. If neither token-limit field is supplied, Anthropic uses the nullable administrator default or the built-in 65,536 fallback. The fallback is not a cap on explicit values.

Usage is normalized into uncached input, cache-write input, cache-read input, and output. OpenAI-compatible streams may send cumulative snapshots: fixed input buckets and nondecreasing output replace the previous value without being added together. Missing/null snapshots preserve the last credible value. Tool flattening emits only the last usage frame after finish and before `[DONE]`. Invalid, negative, regressing, contradictory, or overflowing usage marks the entire request usage unknown rather than fabricating numbers. Literal and semantic response guards block reflection of the exact credential, including across bounded JSON/SSE fragments; this is defense in depth, not general data-loss prevention.

The OpenAI-only physical-key policy `force_store_false` overwrites/inserts top-level `store:false`; an upstream may ignore or reject it. The logical-model policy `flatten_tool_calls` converts bounded validated tool calls to text and restores only complete matched history. Both default off, and flattening is permitted only when every binding is OpenAI-compatible.

Charity names use the reserved `[公益]` prefix. Charity admission checks the feature and caller state and uses only approved, enabled, unexpired donation keys whose physical claim and catalog binding are still valid. After eligibility and Connector capability filtering, at most 100 candidates follow the model's `ordered`, `random`, or `expiry_weighted` strategy. Ordered routing uses saved binding order; uniform random gives each eligible connection equal weight; expiry weighting uses `8/4/2/1` for remaining lifetimes of at most 1 day, 7 days, 30 days, or longer/unlimited. Random strategies use a CSPRNG rejection sampler. The attempt order is frozen without replacement before any logical-request, ledger, reservation, or claim write, and entropy failure is `503 service_unavailable` with zero writes. Caller credit is reserved before dispatch. Each attempt rechecks expiry/capacity but never redraws or adds candidates.

Charity resource identities remain private. A failed call may expose the sanitized upstream message, machine code and HTTP error status described above, but never the donation/key/base URL/upstream-model identity, private diagnostic, or retry count. A later candidate rejected before dispatch does not replace an earlier dispatched upstream failure. Successful-response detection, retries and settlement are unchanged by error reporting.

Charity credit is reserved before dispatch and charged only after the upstream returns a validated successful payload: a complete valid JSON response or the first valid success frame in a stream. HTTP headers, heartbeat comments, empty/invalid responses and error responses alone do not qualify. A failed attempt with no successful output consumes no donation price/call/token quota and earns no donation reward; its entire caller reservation is refunded if no other attempt started successfully. Actual dispatches still count toward RPM and failure tracking. Once output starts, interruption or client disconnect does not undo the charge: known usage is charged at its full frozen price and discount, even when it exceeds the caller reservation. Any difference is debited at settlement and may leave the caller's general-credit balance negative. Unknown usage retains the frozen conservative reserve and discount, capped at the caller reservation. Retries aggregate billable usage only from attempts with successful output. This applies to live diagnostics and buffered tool conversion as well. A minimal durable start marker lets recovery apply the same rule without storing response content.

### 2.3 `POST /v1/embeddings`

Uses the same CallerKey authentication, host boundary, admission order, shared concurrency/RPM, retry deadline, donation eligibility, and error envelope as chat. No query, trailing slash, encoded path, compressed body, or streaming response is supported. This is the single entry point for personal and charity embeddings; charity names keep the reserved `[公益]` prefix.

The table below describes ingress validation and OpenAI-compatible forwarding. Gateway uses the stricter text-only subset in `2.4: unsupported fields exclude that candidate before credential access or reservation, even when they are valid for another connector.

| Field | Accepted values and forwarding |
| --- | --- |
| `model` | Required platform model name, at most 133 Unicode runes. Each attempt substitutes its bound upstream model. |
| `input` | A nonempty string, a nonempty array of nonempty strings, a nonempty Token ID array, or a nonempty array of nonempty Token ID arrays. Batches contain at most 2048 inputs; mixed types, nulls and empty elements are rejected. Whitespace text is preserved. |
| Token ID | JSON integer in `0..2147483647`; fractional, exponent, negative and string forms are rejected. A single Token ID sequence is limited by request bytes, not the batch-item maximum. |
| `encoding_format` | Omitted means `float`; otherwise exactly `float` or `base64`. Null is invalid. The service does not transcode vectors. |
| `dimensions` | Optional JSON integer in `1..2147483647`; null is invalid. The value is forwarded and the returned dimension must match. |
| `user` | Optional string, at most 512 Unicode runes without control characters. For OpenAI-compatible upstreams, always inserted or overwritten with the server's user-and-canonical-origin-scoped pseudonym. |
| `stream` | Optional `false` is forwarded; `true`, null and other values are invalid. |
| Other fields | For OpenAI-compatible upstreams, bounded valid JSON is preserved semantically for the upstream to interpret. If `safety_identifier` is supplied it is overwritten with the same pseudonym; otherwise it is not added. |

Chat and embedding bodies share the configured model-request limit (default 10 MiB), with JSON depth at most 64, at most 1024 top-level fields and field names at most 256 Unicode runes. Duplicate keys, invalid UTF-8, trailing JSON and invalid standard fields are rejected. The platform does not tokenize text or infer a model-specific input limit. Rewritten OpenAI requests are bounded to the configured ingress limit for that request plus 16 KiB for server-generated fields.

An OpenAI-compatible connector appends `/embeddings` to the configured base: `https://provider.example/v1` becomes `https://provider.example/v1/embeddings`, while a bare host becomes `/embeddings`. It neither inserts `/v1` nor accepts a separate full-path override. Models have no purpose field, and `GET /v1/models` does not certify embedding support. The same model may be called through either operation. Candidate filtering excludes unsupported connector types, including Anthropic, before key access; the upstream decides whether its specific model supports embeddings.

The chat-only `force_store_false` and `flatten_tool_calls` policies do not alter embeddings. An explicitly supplied nonstandard `store` field is passed through. Embeddings are exempt from the charity minimum-content check and its penalty; all other identity, feature, level, expiry, rate, concurrency, credit and quota rules still apply, including charity per-user RPM violation attribution.

A successful OpenAI-compatible upstream response requires HTTP 200 and a complete valid non-streaming JSON list within 32 MiB, or the smaller shared egress limit. Its `data` contains exactly one embedding for every input, with unique complete indexes `0..N-1`. Data order and indexes are retained. Float vectors contain only finite numbers. Base64 vectors use standard padded encoding of nonempty little-endian IEEE754 float32 data with finite values. All vectors have the same nonzero dimension and must match an explicit `dimensions`. Invalid or partial vectors, duplicate fields, contradictory structure and successful HTTP error envelopes fail with `502 upstream`; timeouts use 504.

The public response contains only `object,data,model,usage?`; each data item contains only `object,index,embedding`. The model is the caller's platform model name. Private upstream names, diagnostics and unknown fields are discarded. OpenAI-compatible upstream usage is trustworthy only when `prompt_tokens` and `total_tokens` are equal JSON integers in `0..2147483647`. Explicit zero remains zero. Invalid or missing usage does not discard valid vectors, but the public response omits `usage` and accounting records unknown usage. Text length, Token ID count and vector dimension are never substituted for reported usage.

Personal calls incur no site credit charge. Charity per-request pricing charges one successful logical request, including a batch. Token pricing places all reported input tokens in the uncached-input bucket; cache and output buckets are zero, so vector dimensions incur no output-token charge. Unknown usage uses the accepted per-model or inherited global reserve and discount under the existing conservative settlement rule; per-request pricing keeps its fixed price. Unknown usage earns no donor reward. Donation call limits count a batch once; unknown Token usage follows its frozen quota reserve.

The service validates and sanitizes the full response, then durably records successful output before writing the caller body. A failed attempt without that success evidence consumes no charity price or donation call/token/credit quota; actual dispatches still count toward RPM. After the success marker, cancellation or a downstream write failure does not reverse the charge and cannot trigger retry. Recovery, account deletion and late completion retain the existing accounting rules.

Debug dry capture makes no upstream request, credit reservation or usage log. Live capture performs the real call and settlement while suppressing vectors from the caller; it returns the existing 422 captured result or 409 cancellation. Input may exist only in the bounded current Debug memory trace. Vectors are not saved in Debug, logs, account exports or the database.

### 2.4 Native AI SDK Gateway v3 compatibility

The connector type is `ai-sdk-gateway-v3`. The base URL is the complete native mount, for example `https://provider.example/api/gateway/v3/ai`; the connector appends `/language-model`, `/embedding-model` or `/config`. It uses Bearer credentials, `ai-gateway-auth-method: api-key` and `ai-gateway-protocol-version: 0.0.1`. Language calls add specification version `3`, model ID and streaming headers; embedding calls add specification version `3` and `ai-model-id`. The implementation follows [AI SDK gateway 3.0.66 at the pinned ai@6.0.116 source](https://github.com/vercel/ai/tree/035d5ad533901406c81cfe58494d6ea8579d68d4/packages/gateway), not the later v4 protocol.

| Capability | Gateway v3 boundary |
| --- | --- |
| Chat | Multi-turn system/user/assistant text; JSON and incremental SSE |
| Parameters | `max_tokens`, temperature, top-p, top-k, stop strings, presence/frequency penalties and safe-integer seed |
| Tools | Function definitions, auto/none/required/named selection, streamed arguments, matched caller-supplied results; the server never executes tools |
| Images | HTTP(S) URLs and bounded PNG/JPEG/WebP/GIF data URLs; omitted or auto detail only; the server does not download images |
| Embeddings | String or string-array input, float vectors; no token-ID input, dimensions, base64 output or caller identity field |
| Discovery | Native `/config`; explicit language and embedding entries only, full IDs retained; unknown/absent model types are skipped and manual entries remain available |

Developer roles, named messages, unknown fields, arbitrary `providerOptions`, `max_completion_tokens`, parallel-tool flags and any non-equivalent option exclude this connector before credentials or reservations. The proven neutral options `n:1`, `logprobs:false`, empty `logit_bias` and `response_format:{"type":"text"}` are accepted. Other compatible candidates remain eligible; if none remain the request fails explicitly. OpenAI-only store/flatten settings do not acquire Gateway semantics. Provider warnings about ignored options, malformed tool arguments, invalid event order, missing native finish, truncated streams and cancellation cannot produce successful completion. Missing or ambiguous usage remains unknown and uses the existing conservative accounting rules.

Administrator setting `gateway_user_attribution_enabled` defaults to `false` (the API uses JSON booleans; storage uses `0|1`). Every subsequent Gateway chat/embedding dispatch reads the current value; a dispatched body is immutable. When enabled, only the server supplies `providerOptions.gateway.user`, a keyed pseudonym scoped to the user and final canonical gateway origin. It permits correlation for **cost attribution**, not a claim that a safety identifier was applied. When disabled, the field is absent. Caller input cannot override it or supply gateway credentials, fallback models or provider-routing options. Debug adds optional boolean `gateway_user_attribution_sent`; no label value is exposed. Existing OpenAI/Anthropic identity behavior is unchanged.

Native reasoning text is returned separately as `reasoning_content` in the assistant message or stream delta; it is never merged into answer text. This output extension does not authorize unsupported reasoning fields in incoming messages. Streams are validated by bounded native events and a complete terminal sequence, including closure of reasoning and tool blocks, independently of the upstream's advertised content type. JSON responses are limited to 8 MiB and streams to 32 MiB, or the smaller shared egress limit. Gateway embedding usage accepts nonnegative signed-64-bit token counts; absent usage remains unknown.

Runable with SillyTavern 1.19.0 has been verified for non-streaming and streaming chat, a function-tool round trip and image input. Native model discovery returned language and embedding entries alongside other model types, which were skipped. The tested Runable native `/embedding-model` endpoint returned HTTP 404; embedding translation and accounting have passed simulated HTTP tests, but successful Runable embedding execution has not been demonstrated. This does not establish support or lack of support on other Gateway deployments. Model catalog prices are separate from Runable credits; a zero catalog price does not establish a free request.

Catalog prices are upstream metadata and do not certify actual gateway credits or model capability. Provider-specific validation and exceptions are recorded in the release notes; a model-list entry does not certify every operation.

## 3. User identity, account, resources, and logs

All routes in this section require a user session unless marked anonymous.

Request-log and Debug `route_kind` values are `openai_chat_completions`, `charity_chat_completions`, `model_discovery`, `openai_embeddings`, and `charity_embeddings`; Debug accepts the four model-call kinds only. Log clients using a closed enum must accept the two embedding values. Existing model/status/time filters, pagination, role-specific projections and exports include both operations. Personal and charity embedding records retain their corresponding privacy boundary; they do not store input or vectors.

| Method and path | Request / response |
| --- | --- |
| `GET /api/config` | Anonymous safe bootstrap: site name/logo, donation notices, four legal overrides, authoritative locale, maintenance/registration state, announcement epoch. |
| `GET /api/auth/discord/start` | Anonymous; optional server-issued return route; 302 to Discord after IP admission. |
| `GET /api/auth/discord/callback` | OAuth `code,state`; atomically signs in or creates an allowed account, then redirects to the bound route. A forbidden account instead receives a no-store redirect to `/access-denied`, without a new session; ordinary API authorization failures remain JSON 403 responses. |
| `GET /api/session`, `GET /api/me` | Current `UserEnvelope`; the shapes are identical. |
| `PATCH /api/me` | `{lang?,game_profile_public?,charity_profile_public?}` with at least one field; returns `UserEnvelope`. |
| `POST /api/auth/logout` | Clears the user session; 204. Available during maintenance. |
| `POST /api/auth/elevate` | Starts the bound two-step elevation flow. |
| `GET /api/me/usage` | Four-bucket and request usage summary. |
| `GET /api/caller-key` | Returns null or `{display,created_at,updated_at,generation}`, plus `X-Nonbiri-CallerKey-Generation`; the body generation must match the header. `display` is masked and cannot be used for calls, and GET never returns the full secret. |
| `POST /api/caller-key/regenerate` | Accepts `{expected_generation}`; on success returns `{secret,metadata}` once, with `metadata.generation = expected_generation + 1`. The full secret is present only in this successful response; generate a replacement again if it was not saved. |
| `GET /api/home/game-summary` | Safe resumable-game and pending-result route identities. No arbitrary URL is returned. |
| `GET /api/logs` | Filters `model,error_code,status,from,to`; legacy `cursor,limit` or numbered `page,page_size`; returns owner rows and numbered `pagination`. |
| `GET /api/logs/{id}` | Legacy `attempt_cursor,attempt_limit` or numbered `attempt_page,attempt_page_size`; numbered detail returns `attempt_pagination`. |
| `GET /api/logs/options` | Closed query; returns retained owner model-name options. |
| `GET /api/issues` | Required `state=current|closed`, plus legacy `cursor,limit` or numbered `page,page_size`; returns the owner's bounded issue page and numbered `pagination`. |
| `POST /api/account/export` | Fresh elevation; bounded schema-v9 JSON attachment. |
| `POST /api/account/delete` | Fresh elevation and confirmation; synchronous coordinated deletion; 204. |

`balance` always means general credits; `game_balance` is the separate signed game-credit wallet. Both use exact decimal strings. New accounts start both at zero. API calls, donations and Thursday use general credits. Administrators can adjust either wallet, including below zero; cumulative donation credit remains nonnegative and is a separate lifetime statistic.

`UserEnvelope` contains safe identity/profile, raw and effective resource limits, balances, resolved level/display name, language, suspension/ban state and independent game/charity public preferences. `automatic_restrictions` is an array of safe `{kind,reason_code,reason,started_at,ends_at}` summaries, without thresholds, counters or evidence. It contains no manual-level provenance, credential, session token, or internal ledger encoding.

### 3.1 Endpoints and keys

| Method and path | Request / response |
| --- | --- |
| `GET /api/endpoint-create-options` | `{base_connector_types,mainstream_channels}`; channels are active and enabled. |
| `GET /api/endpoints` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode optionally accepts `q,connector_type,source,state` and returns `Endpoint` with `pagination`. |
| `POST /api/endpoints` | Strict union `{source:"mainstream",channel_id,note,enabled}` or `{source:"custom",connector_type,base_url,note,enabled}`; returns 201. |
| `GET /api/endpoints/{id}` | One owner endpoint. |
| `PATCH /api/endpoints/{id}` | `{note?,enabled?,expected_revision}`; origin and Connector are immutable. |
| `DELETE /api/endpoints/{id}` | `{expected_revision}`; 204. Report locks return `resource_locked`. |
| `GET /api/endpoints/{id}/keys` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode optionally accepts `q,enabled,donated,suspension_state` and returns safe `EndpointKey` with `pagination`. |
| `POST /api/endpoints/{id}/keys` | `{secret,note,enabled,force_store_false,ownership_confirmed:true,max_concurrency?,max_rpm?}`; returns 201 safe metadata. |
| `PATCH /api/endpoints/{id}/keys/{keyId}` | `{note?,enabled?,force_store_false?,max_concurrency?,max_rpm?,expected_revision}`. |
| `DELETE /api/endpoints/{id}/keys/{keyId}` | `{expected_revision}`; claim-first deletion; 204. |
| `GET /api/endpoints/{id}/keys/{keyId}/bindings` | Numbered `page,page_size` only (default 1/20), with optional exact `upstream_model_id` (1–512 Unicode code points); returns the owner-safe binding page and `pagination`. |
| `GET /api/endpoints/{id}/keys/{keyId}/models` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode optionally accepts `source=automatic|manual`; returns discovery `evidence`, automatic/manual entries and numbered `pagination`. |
| `POST /api/endpoints/{id}/keys/{keyId}/models/refresh` | Idempotency key; 202 accepted operation and evidence. |
| `POST /api/endpoints/{id}/keys/{keyId}/models/manual` | Batch of `{upstream_model_id,provider}`; returns entries. |
| `PATCH /api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}` | Pair revision plus complete binding replacements; returns changed entry and affected model snapshots. |
| `DELETE /api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}` | Pair revision plus replacements; 204. |

`Endpoint` includes `id,connector_type,base_url,note,enabled,revision,key_count,origin,created_at,updated_at`. `origin` is `{kind:"custom"}` or `{kind:"mainstream",channel_id,name}`. Channel category/revision are administrator-only. A mainstream create re-reads the active enabled channel and copies its immutable snapshot in the final transaction; later channel edits never rewrite an endpoint. A custom URL is never guessed to be mainstream.

`EndpointKey` exposes display fragments, note, enabled state, `force_store_false`, safe suspension state, revision, and times; never plaintext/ciphertext/fingerprint. Discovery evidence is the closed `unknown|checking|succeeded|failed` union with a safe failure class. Successful empty discovery is explicit and distinct from failure.

OpenAI-compatible discovery validates every original entry before merging duplicate normalized model IDs. The first entry's provider metadata, including an empty value, and response order are retained; a later different provider does not cause a conflict. Existing surrounding-whitespace normalization applies. Limits remain 1 MiB per response, 1,000 original entries, 512 characters per model ID and 128 per provider. Invalid fields or credential reflections in a duplicate entry still reject the entire response.

Manual catalog `provider` remains the wire/storage name for optional display metadata. The user-station form labels it **Note (optional)**. It does not contribute to the exact upstream model ID or binding identity; personal/charity model naming uses a separate provider field.

`EndpointKey` also returns numeric `max_concurrency` and `max_rpm`. Both accept whole numbers from 0 to 2147483647; 0 disables that key's additional limit. Omitted creation values default to 0; omitted patch values remain unchanged. Null, strings, negative numbers, fractions and out-of-range values are rejected. Only the owner can edit them, using the existing revision and idempotency contract. Existing site and endpoint safeguards still apply. Numbered endpoint, key, and model rows add the corresponding `browse` summary; legacy cursor rows and single-item/mutation responses keep the existing resource shape. Endpoint/key `q` filters are single-value literal text searches of at most 128 Unicode code points (and no more than 512 UTF-8 bytes), with controls rejected.

All personal, charity and live diagnostic model calls using the same EndpointKey share these limits across bindings. Discovery requests use their separate safeguards. Concurrency includes admitted work until its attempt finishes or is canceled, including the full streaming lifetime. RPM uses a rolling 60-second window with server-second precision. Undispatched claims reserve a slot until dispatch or release; a released undispatched attempt returns the slot. A dispatch consumes one slot even on failure, and each retry counts separately. Pending work cannot age out of its reservation. Lowering limits affects new admissions without canceling already admitted work. Recent dispatch counts survive process restarts.

A limited key is skipped without contacting the upstream, charging for that attempt, or recording an upstream failure. Selection continues in the configured order even when silent retry is off. If no attempt dispatched and at least one candidate was key-limited, exhaustion returns HTTP 429 `rate_limited` and releases the request reservation. If an earlier attempt dispatched, its actual result and ordinary settlement remain authoritative.

Administrator and level-6 steward donation-key projections include read-only `max_concurrency`/`max_rpm`; both are null after the physical key is gone. Their shared binding menu/candidate/binding `source` includes the current integer limits. These fields do not expand donation ownership or expose private owner notes, and management mutation bodies reject them. Ordinary charity callers do not receive underlying key configuration. Owner resource exports include the values; key deletion cascades to its configuration. Historical idempotent responses may omit these additive fields; an authoritative read returns current values.

### 3.2 Personal models and bindings

| Method and path | Request / response |
| --- | --- |
| `GET /api/models` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode accepts `q,provider,route_strategy,connection_state`; response includes `pagination` and the owner models. |
| `POST /api/models` | Provider/model, strategy, retry and flatten policy; returns 201 model. |
| `GET /api/models/{id}` | One owner model. |
| `PATCH /api/models/{id}` | Partial business fields plus `expected_revision`. |
| `DELETE /api/models/{id}` | `{expected_revision}`; 204. |
| `GET /api/models/{id}/binding-candidates` | `endpoint_id,key_id,source=automatic|manual,q` plus legacy `cursor,limit` or numbered `page,page_size`; safe candidate page, with numbered `pagination`. |
| `GET /api/models/{id}/bindings` | Complete bindings plus `binding_revision`. |
| `POST /api/models/{id}/bindings/batch` | Expected binding revision and selections; all-or-nothing 201. |
| `PUT /api/models/{id}/bindings/order` | Exact current ID permutation and expected binding revision. |
| `DELETE /api/models/{id}/bindings/{bId}` | Expected binding revision; returns the complete new binding set. |

Provider/model parts are bounded opaque strings and form the external `provider/model` name. `[公益]` is reserved. Binding DTOs contain only owner-safe endpoint/key display data, Connector type, upstream model ID, and order.

Numbered resource filters combine with AND, and counts and rows use the same bounded read snapshot. Endpoint `q` searches the base URL, note and mainstream channel name; `source=mainstream|custom`, `state=available|endpoint_disabled|no_keys|no_usable_key`, and a registered `connector_type` are optional. Key `q` searches only displayed fragments and notes; `enabled` and `donated` accept `true|false`, while `suspension_state=none|security_processing` is independent of donation membership. Model `q` searches the full model name and every binding's upstream ID without duplicate rows; `provider` is exact, `route_strategy=ordered|random`, and `connection_state=available|unavailable|unconfigured`. Empty optional choices, unknown/repeated filters and unsupported combinations are rejected. Omit a filter to clear it. Sizes remain 10/20/50/100, default 20, with out-of-range pages clamped. Legacy cursor behavior remains unchanged and does not acquire these new filters.

### 3.3 Credit history

`GET /api/credits/history` returns the signed-in user's credit history. Optional single-value filters include `asset_type=general|game|sketch_paper|sketch_brush|all` (default general), a supported ledger category, `direction=income|expense`, and Unix-second `from,to` using `[from,to)`. Categories include check-ins, onboarding, welfare, Thursday, each game, API/charity/donation, administrator adjustments, penalties, loans, picture-book operations and inactivity. `page` is a positive decimal integer, default 1; `page_size` is 10, 20, 50, or 100, default 20. An out-of-range page returns the last page.

The response is `{data:[{asset_type,operation_id,line,kind,delta,created_at,request_id}],page,page_size,total,total_pages,anchor,current_balance,game_balance,server_now}`. Amounts and page/count fields are exact decimal strings; `line`, `page_size`, and times are bounded numbers. Only nonzero changes to the current user's selected wallets appear. One operation can have a general line and a game line, each with its own amount and line number; they are never combined into one amount. Reasons use the stable ledger `kind`; private management notes, actor identities, source IDs, other wallets, and historical post-balances are omitted. `current_balance` and `game_balance` are the current general and game balances, including changes after the browsing anchor. The user page requests all assets initially; changing the asset filter resets its page and anchor.

Pass the returned nullable `anchor` operation ID to subsequent pages to keep newer entries from shifting the result set. The server checks the anchor, total and page against the same selected owner wallets. Omit it to refresh. Related `request_id` is present only for the caller's own API/charity entries and short-request penalties whose request log is still ordinarily viewable; donor rewards always return null. Expired logs do not remove credit history. The user page is `/credits`; `/logs?request_id=...` opens the existing owner-checked log detail. Draft-paper and brush deltas are whole-unit strings and are never formatted as fractional general/game points or combined across assets. Activity exchange, reservation, charge, refund and inactivity-decay entries have distinct labels. The balance summary fields above remain general/game only; the activity wallet endpoint provides the activity balances.

## 4. Donations, charity capability, and public reports

### 4.1 Donations and charity models

| Method and path | Request / response |
| --- | --- |
| `GET /api/donations` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode also accepts `status,q` and returns donation summaries with `pagination`. |
| `GET /api/donations/{id}/keys` | Numbered `page,page_size` only; owner key-level donation projection with `pagination`. |
| `GET /api/donations/{id}` | Owner detail with key states and review history. |
| `POST /api/donations` | `{description,keys:[{endpoint_key_id,expires_at}],ownership_authorized:true}`; 1–100 unique keys; returns 201. |
| `PATCH /api/donations/{id}` | Pending-only `{description,expected_revision}`. |
| `POST /api/donations/{id}/withdraw` | Pending `{expected_revision}`. |
| `POST /api/donations/{id}/terminate` | Approved `{expected_revision,confirmation}`. |
| `GET /api/charity/models` | Without query parameters: `{state,models,donation_intake,server_now}`. Use `view=catalog` for the paginated web directory described below. |

Creation and pending edits require a non-whitespace donor description, with the existing 1,024-code-point / 4,096-byte limit and LF normalization. Historical blank descriptions remain readable, reviewable and usable; review notes may still be empty. Creation also requires the explicit boolean `discord_public_thanks`, with no default. It records permission for an administrator or full steward to thank the donor manually in Discord. The value cannot change after submission. Old null values mean unconfirmed, cannot be backfilled, and are not consent. A pending edit cannot change this choice; omission preserves it. No automated Discord message is sent.

Each donation key has immutable `authorized_expires_at` and an effective `expires_at`, equal at creation. A reviewer may shorten the effective expiry or restore it only up to the donor's authorization; an unlimited effective value is permitted only when the authorization is unlimited. One key expiring removes only that key's membership and bindings and blocks new claims. The donation becomes expired only when its last live key ends. Accepted claims and reservations complete normally.

Administrator and current level-6 steward donation projections contain the same management information. `owner` contains `user_id,discord_id,display_name` for any surviving donor; an absent owner remains null. `reviewer` contains `user_id` and the recorded actor `role`, with a null ID after that actor's identity is removed. Manual review may have an empty reason; automatic approval has no reviewer and an empty reason. Detailed management key sources include channel category/revision when recorded. Ordinary owner projections remain separate. Historical idempotent responses may lack newly authorized information; the current detail read provides the authoritative projection without rewriting stored receipts.

Every owner-visible key carries `safe_source`: a custom Connector/base URL or a mainstream channel/name/Connector/base URL. Internal source IDs, channel category/revision, report fingerprints, secrets, and management notes are excluded. A donation containing only keys from one immutable mainstream channel is approved atomically at creation. A fully custom donation remains pending. Mixed custom/mainstream or multiple-channel submissions are `invalid_request`.

`CharityCapability.state` is `feature_disabled|no_models|no_candidates|available`; `donation_intake` is `open|closed`. Only models allowed for the caller's current level and with a usable candidate are listed. Charity candidates exclude the caller's own donated keys unless the caller is currently level 6; personal calls are unchanged. Level-6 self-donation calls receive normal rewards and cumulative donation credit. Each model contains `id,provider,model,full_name,pricing,discount,recent_success`. Pricing is a tagged union:

- `per_request`: canonical base and current discounted user price;
- `per_token`: canonical base and current price for uncached input, cache-write input, cache-read input, and output.

`discount` contains `enabled,percent,start_at,end_at`; the interval is `[start_at,end_at)`, nullable at either end. Effective prices use the same integer ceiling rule as billing. `server_now` is the single decision time used for availability and promotion status. No donated-resource identity is exposed.

The web directory uses `GET /api/charity/models?view=catalog`. Optional single-value parameters are `q` (at most 128 Unicode code points and 512 UTF-8 bytes, no NUL), `allowed_for_me=true|false`, `allowed_level=1|2|3|4|5|6`, `currently_available=true|false`, `page`, and `page_size`. Omit a filter to apply no restriction; the filters are combined as an intersection. The web UI initially sends `allowed_for_me=true` and `currently_available=true`, while omitting `allowed_level`. It rejects cursor/limit and unknown or repeated parameters. Page numbers are canonical decimal strings from 1 to 2147483647; sizes are 10, 20, 50, or 100, with defaults 1 and 20. The response is `{models,pagination,donation_intake,server_now}`. Pagination contains decimal-string `page,total_items,total_pages` and integer `page_size`; empty results use page 1 of 1, and an excessive requested page is clamped to the last page.

Catalog entries contain the six capability fields plus `public_description,enabled,allowed_levels,level_allowed,currently_available,availability`. They include unavailable models so users can understand the reason. Availability is `feature_disabled|model_disabled|level_denied|no_usable_key|available`, in that precedence. `currently_available` reports the current charity resource state independently of the caller's level; `availability` remains the caller-facing explanation. Search, count, ordered page, pricing and availability share one read-only snapshot and a five-second query budget. No donor rewards, bindings, physical resources or private notes appear in the directory.

Administrator and steward model create/patch bodies accept `allowed_levels` and `public_description` within a 16 KiB request limit. Levels are unique integers 1–6, returned sorted. Omitted creation fields mean all six levels and an empty description; omitted patch fields remain unchanged. An explicit empty array blocks every ordinary caller, including L6. Null and duplicate/out-of-range levels are invalid. Descriptions are plain text, at most 1,024 code points and 4,096 UTF-8 bytes after CRLF-to-LF normalization; controls other than LF and TAB, including lone CR, are rejected. These fields use the existing model revision and idempotency rules.

Management model detail, create, and patch DTOs also carry the top-level `token_reserve_credits` field as a decimal string or `null`. It accepts canonical credit values from `0.001` through `9000000000000` inclusive, with no more than three fractional digits; zero, negative, exponent-form, overprecise, out-of-range, and non-string values are invalid. On create, omission or `null` inherits the global Token reserve. On PATCH, omission preserves the existing override and `null` clears it back to inheritance. The field is effective only for `per_token` pricing: `per_request` keeps its per-request price reserve, and an override is retained when switching back to Token. Runtime preflight, candidate availability, and billing use the model value or the global `charity_token_reserve_milli`; the accepted value is frozen for an admitted request, so in-flight and recovery snapshots remain unchanged. This is a management-only field and is absent from ordinary catalog/API projections and owner exports.

Level permission is rechecked at admission, claim and immediately before the dispatch marker. A denied level returns `403 forbidden`; an unavailable model returns `404 not_found`. A blocked unsent attempt releases its reservations. Already dispatched attempts finish under their accepted accounting, while later sends and retries must pass current access rules. Debug dry and live calls follow the same caller-level restriction.

`recent_success` contains `window_start,as_of,success,failure,cancelled,sample_count,rate,insufficient_sample,capture_started_at`. It measures completed logical requests actually dispatched in the preceding 24 hours: a retry that finally succeeds counts once, undispatched refusals do not count, and cancellations are listed separately outside the denominator. `rate` is a number from 0 to 1 or null when there is no sample. Fewer than 20 samples is explicitly insufficient; zero samples never implies 100% success. Coverage start is visible and any cache is at most 60 seconds old.

### 4.2 Donation failure-streak reset

Each key's `failure_disable_threshold` is a canonical decimal U128 string, default `"10"` on creation and migration. Owners, administrators and current stewards can independently `PATCH {prefix}/donations/{id}/keys/{keyId}/failure-policy`, using `/api`, `/admin/api` or `/api/steward` respectively. The body is `{expected_revision,failure_disable_threshold}` with `Idempotency-Key`; the safe response is `{donation_id,donation_key_id,failure_disable_threshold,failure_streak,failure_disabled,revision}`.

Saving retains the current count and generation and atomically recalculates only error-disablement: `0` clears it; a positive threshold disables when the count is at least that threshold, otherwise clears it. Zero still counts failures and success clears the count. Ordered in-flight results use the latest saved threshold; old-generation completions cannot change current state. Manual disablement, expiry, withdrawal, bans, quotas and ownership checks continue to apply. Pages persistently warn when zero is entered or saved, without extra confirmation. Each real save has a no-secret review audit; replay repeats no write or alarm. Only a transition into error-disablement alerts; clearing and later disabling can alert again.

Owners can `POST /api/donations/{id}/keys/{keyId}/failure-streak-reset` with `{expected_revision}` and `Idempotency-Key`. Only their own approved, unexpired, active donated key is eligible. The response is `{donation_id,key_id,revision,failure_streak:"0"}`. Missing ownership returns 404; stale or ineligible state returns 409.

Administrators and current stewards can `POST {prefix}/donation-keys/failure-streak-reset`, where the prefix is `/admin/api` or `/api/steward`. The strict body is `{items:[{donation_id,key_id,expected_revision}]}`, with 1–100 unique keys and one expected revision per donation. The response is `{results:[{donation_id,key_id,status,revision}],counts:{processed,reset,skipped}}` in input order. Status is `reset|conflict|ineligible|not_found`; counts are decimal strings and revision is a string or null. A donation's revision advances once per reset key; every result in that donation reports the final revision from the batch. A stale donation skips all its submitted keys; an ineligible key does not prevent eligible keys in other donations from succeeding. Replays require current authority. These control requests have the shared 256 KiB body limit.

Reset clears only the consecutive-failure disabling state and starts a new failure generation. It preserves usage, quotas, expiry, bindings and manual disablement. Late callbacks from the previous generation cannot rebuild the cleared streak. Each actual reset records a no-secret review audit.

Before processing all matching results, `POST {prefix}/donation-keys/failure-streak-reset/selection` accepts `{selection:{view,...filters},cursor:null|string}`. The cursor is required; start with null. Views are `donations|donation_keys|sources|source_keys`. Donation filters are `q,status,handling`; `donation_keys` requires only `donation_id`; source filters are `q,scope,handling`; source-key filters additionally require `source_key` and accept `idle`. These match the corresponding list filters, with no pagination fields. Each call has a five-second budget, scans at most 100 saved key IDs and returns `{items:[{donation_id,key_id,expected_revision}],next_cursor}`; an empty item page may still have a next cursor. The signed cursor fixes the upper key-ID boundary, role, actor and filters for one hour. This read-only operation writes no selection, job or replay record. The browser gathers the fixed selection before sending batches of at most 100, yields between steps, supports interruption and retains the exact batch and idempotency key when a response is uncertain.

#### Managed model discovery

Administrators and current stewards have three web entry points: one available donated key, all available keys in one donation, or all available keys across all donations. The latter two ignore list filters and pagination. These are Cookie-session management routes, with prefixes `/admin/api` and `/api/steward`; they do not expand CallerKey automation scopes.

| Method and suffix | Request / response |
| --- | --- |
| `POST /donation-keys/models/refresh/selection` | Strict `{donation_id:null|string,cursor:null|string}`, both fields required; no query or idempotency key. Null donation selects all donations. Returns `{items:[{donation_id,key_id}],next_cursor}`. |
| `POST /donations/{id}/keys/{keyId}/models/refresh` | No body or query; requires `Idempotency-Key`. Returns 202 `{operation_id,evidence}` with `evidence.state="checking"` and the accepted discovery revision. |
| `GET /donations/{id}/keys/{keyId}/models/discovery` | No body or query. Returns only the existing safe discovery evidence union; does not replace `/models`, which lists related charity models. |

All IDs, revisions and result counts are canonical decimal strings. Evidence contains `state,revision,result,safe_class,observed_at,count`: `unknown` has null result/time/count and class `none`; `checking` has a start time but null result/count; `succeeded` has `empty|nonempty`, time and count (0–1,000), class `none`; `failed` has time, null result/count and one of `auth|rate_limit|timeout|protocol|transport|interrupted`. Operation IDs are opaque `op_` identifiers. Responses are non-cacheable; no credentials, private endpoint notes or raw upstream diagnostics are returned.

Eligibility requires an approved donation, live membership, no expiry or terminal state, enabled endpoint/physical key/donation key, no safety suspension or failure disablement, a live unbanned donor, and unexhausted total quotas. Existing public model bindings are not required. Selection, acceptance, credential dispatch and catalog publication recheck current authority and eligibility in their respective transactions. Wrong parent IDs return 404; unavailable keys return 409 `resource_locked`; another active discovery or a changed idempotent request returns 409. Session or role loss returns 401/403. Invalid input returns 400; unavailable shared capacity returns 503. A valid replay requires current authority and does not send another upstream request.

Selection and evidence reads have a five-second budget. Selection scans at most 100 candidate IDs per page; an empty page may still have a next cursor. The signed cursor binds the actor, role, donation scope and initial maximum key ID for one hour. Follow `next_cursor` until null without adding new keys above that boundary. Each accepted check uses the shared discovery worker (four concurrent, 32 admitted, five-minute lifetime), connector timeouts and outbound limits. No donation quota, game credit or reward is consumed; failure counters are unchanged.

The browser streams selection pages and runs one key at a time, polling every 1.5 seconds. It pauses after 60 pending observations or an uncertain response, keeping the exact idempotency key and accepted revision in memory. Resume reads accepted work or replays the same uncertain request. Permission loss and leaving the page stop further client scheduling; accepted server work may finish, and whole batches do not resume across page reloads. A newer discovery revision is reported as superseded. Results distinguish success (including zero models), failure, ineligibility and conflicts, retaining only the latest 100 detail rows. Successful discovery replaces automatic entries; failure preserves the previous automatic catalog, while manual entries and existing bindings remain unchanged. Discovery audit, request-log retention and account lifecycle follow the existing rules.

### 4.3 Credential-theft reports

`POST /api/reports/credential-theft` is anonymous or user-authenticated, requires an idempotency key, and accepts strict `{connector_type,base_url,secret,note}`. A structurally valid request always returns the same 202 body and `X-Nonbiri-Report-Accepted: 1`, whether or not a match exists. Matching and non-matching responses share the configured timing window. Validation and rate-limit errors remain real errors.

Indexing searches live endpoint keys first, then donation tombstones whose irreversible fingerprint is still inside its 90-day post-end match window. Live matches win and case-local targets deduplicate. Approval after the physical key disappeared is a safe no-op for deletion while the case decision and safe lineage still complete. Report material, source IP material, fingerprints, and target lineage are administrator-only and are excluded from account exports.

## 5. Announcements, activities, check-in, and games

### 5.1 Announcements and activities

| Method and path | Request / response |
| --- | --- |
| `GET /api/announcements` | Legacy `cursor,limit` or numbered `page,page_size`; published summary page with numbered `pagination`. |
| `GET /api/announcements/{id}` | Localized rendered detail using the fixed safe Markdown profile. |
| `GET /api/activities` | Complete safe activity snapshot with site totals and the caller's state. |
| `POST /api/activities/welfare/claims` | Idempotency key; one admitted daily welfare claim. |
| `GET /api/activities/thursday` | Current Thursday view. |
| `POST /api/activities/thursday/contributions` | Idempotency key plus `{period_id,expected_revision}`; one fixed contribution. |
| `GET /api/checkin` | General credits: `{enabled:false}` or `{enabled:true,asset_type:"general",checked_in_today,balance,award_min,award_max,balance_cap}`. |
| `POST /api/checkin` | Bodyless, once per account/site day; `{award,balance,asset_type:"general"}`. |
| `GET /api/checkin/game` | Independent game-credit check-in, same status fields with `asset_type:"game"`. |
| `POST /api/checkin/game` | Bodyless, independently once per account/site day; `{award,balance,asset_type:"game"}`. |
| `GET /api/events` | Shared user SSE for activities and RPS; see §8. |
| `GET /api/games` | Complete game availability, `balance,game_balance`, tutorial state, prices, timers, and mode configuration. |

Both check-ins share the site day and timezone, but have independent modes, awards and balance caps. Level-gated mode requires actual level 3 or above. A positive cap rejects a check-in when that wallet already reaches it; the sampled reward is not truncated. A zero cap removes this threshold. A zero reward still records a successful check-in; a disabled or rejected request does not. A duplicate returns `already_checked_in`. The game defaults are disabled, 40,000–60,000 credits per award and a 250,000-credit cap. The four keys `game_checkin_mode`, `game_checkin_award_min_milli`, `game_checkin_award_max_milli`, `game_credits_cap_milli` belong to site-config and its catalog/batch validation. HTTP amounts use credit strings even for keys ending in `_milli`. Daily statistics retain `checkins` for general and add `game_checkins`; a person checking in twice still counts once as an active user.

Welfare uses the signed game wallet plus unspent game credits held in fishing, RPS queues and RPS seats, and Bidding Duel / Turn-based Battle Minigame (Test) queues and seats. General credits and API reservations do not affect eligibility. The general-credit pool funds an equal game-credit award; `WelfareView` adds `asset_type:"game",pool_asset_type:"general"`. Claim responses retain the general `balance` and add `game_balance` plus those asset markers. An award of zero consumes no daily claim, and a historical general-credit claim still prevents another claim that day.

The home page shows at most three visible announcement summaries, with pinned items first and then publish time descending. Hiding an item affects only the current browser home page; it does not remove the item from the full announcements page.

Announcement bodies use a fixed Markdown subset: paragraphs, headings h2–h4, emphasis, lists, blockquotes, inline/fenced code, and HTTP(S) links. Raw HTML, images, embedded media, forms, frames, scripts, styles, and unsafe schemes are rejected by the same parser used for preview, publish, and read.

### 5.2 Shared game payments and newcomer awards

New game entries spend positive game credits first, then positive general credits. A negative wallet does not reduce the other wallet's available funds. `payment:{general,game}` contains canonical credit strings. Releases of an unused entry return each part to its original wallet. Game payouts and the 22 once-only newcomer awards use general credits. LinkLink has no ordinary monetary payout.

`GET /api/games` returns six game configurations and `onboarding`. Fishing, LinkLink and RPS retain their nine once-only tasks and 17,000 general-credit awards. Bidding adds `complete_tier_1:1000`, `complete_tier_2:2000`, `complete_tier_3:5000`, `first_win:2000`. Likes adds `quick_complete:1000`, `quick_win:2000`, `standard_complete:5000`, `standard_win:10000`. Blackjack adds `complete:1000`, `first_win:2000`, `first_bust:3000`, `first_21:4000`, `first_natural_21:5000`. There are 22 tasks totalling 60,000 credits. Completion, financial settlement, all qualifying awards and ranking contributions commit together; simultaneous completion/win or natural-21 qualifications stack. Bidding/Likes surrender disqualifies the surrendering player's completion; the other player can qualify normally. Blackjack completion includes pushes or all busts, and first win is per hand; split 21 is not natural. System cancellation, restart cancellation and local teaching never award credits. No historical finished matches are scanned. All accounts can qualify through normal play after upgrade, and completed tasks survive game-history expiry and cannot be earned again. RPS automatic timeout choices count as normal completion; LinkLink requires a cleared board. Each card disappears once all that game's tasks are complete.

### 5.3 Pond Fishing

| Method and path | Request / response |
| --- | --- |
| `POST /api/games/fishing/batches` | Idempotency key plus `{bait,count:1|10}`; 200 committed result or 202 settlement state. |
| `GET /api/games/fishing/state` | One settlement-pending/recovery state, one oldest unacknowledged result, and `has_more_unrevealed`. |
| `POST /api/games/fishing/batches/{id}/ack` | Business-unique render acknowledgement; 204. |
| `POST /api/games/fishing/batches/{id}/recover` | Idempotent owner recovery of the same exhausted batch; 200 or 202. |
| `GET /api/games/fishing/leaderboard` | `board=single|recent_single|total`; top 20 plus the caller when eligible. |

A start creates all 1 or 10 CSPRNG outcomes and the reservation atomically. A failed settlement retains the same batch and outcomes for automatic recovery; it never redraws or charges again. After ten consecutive failures, only owner recovery continues. Committed or released batches replay without another economic write. Acknowledgement affects presentation only. Terminal batches/outcomes are retained for 30 days; the personal best has the account lifetime.

Version 2 freezes the three configured integer basis-point rates at admission. Each is 0–9,999 and their sum must be below 10,000; fresh defaults are 100 each. For each outcome, each cut is floored separately to a millicredit before the three cuts are subtracted. Ten catches sum their individual cuts. Platform, welfare and Thursday cuts reach their corresponding general-credit accounts in the same settlement. Gross fields `reward,payout_total` remain; `net_reward,net_payout_total,rake:{platform,welfare,thursday}` add the net amounts. Results also include both balances and the payment split. The rolling payout board sums net winnings. Fresh gross RTP defaults to 100% for every bait; upgrades preserve configured values, including 90%/88%. Version 1 settles with its old no-rake rules.

The length boards preserve the original `species_key` and `size_cm`. A result outcome or length-board row also contains `blue_fat_fish_length_cm:null|string`: it is `null` for an ordinary or historical catch, otherwise a canonical decimal integer of at most 128 digits and at least 201. Clients must keep this value as a string, display the Easter-egg name together with the original legendary species, and use it as the displayed length. The original `size_cm` still describes the draw used to calculate the reward. `total` rows retain their existing credits-only shape.

`single` is the lifetime largest-length board and has `window_start:null`. `recent_single` is the largest length per user among settlements strictly after `window_start=query_now-2592000`; `total` remains the rolling 30-day payout board. Expired catches leave the recent length board even before physical cleanup. Length ties use the earlier catch time and the same stable private tie key as the lifetime board. Anonymous preferences and current account eligibility apply independently to every read. ACK changes neither board. Upgrade backfill includes only retained complete settled batches with their matching original rank facts; unavailable historical catches are not reconstructed.

After generating the original complete batch, each legendary catch independently has a 10% Easter-egg chance. An Easter egg starts at 201 cm; each additional centimetre has a 99% continuation chance, so `P(length >= 201+n)=0.99^n`. There is no gameplay length ceiling and the original legendary payout remains unchanged. Random-source, cancellation, work-budget or representation failures reject the complete start before reservation; they never clamp a length, redraw only part of the batch or change the payout rate.

### 5.4 LinkLink

New sessions use a server-generated solvable board and rules version 2. Sizes, prices, matching geometry and deadlines remain unchanged. Saved version-1 sessions retain their original boards, score rules and free automatic deadlock reshuffling until they finish.

| Method and path | Request / response |
| --- | --- |
| `POST /api/games/linklink/sessions` | Idempotency key plus `{spec}`; 201 new game or 200 existing active game. |
| `GET /api/games/linklink/session` | `null`, active `LinkLinkState`, or the latest retained `LinkLinkSummary`. |
| `POST /api/games/linklink/sessions/{id}/matches` | `{expected_revision,first:{row,col},second:{row,col},include_path?:boolean}`; returns authoritative state or terminal summary. |
| `POST /api/games/linklink/sessions/{id}/abandon` | `{expected_revision,confirmation}`; returns terminal summary. |
| `POST /api/games/linklink/sessions/{id}/lease` | `{lease_id}`; returns the lease expiry. |
| `POST /api/games/linklink/sessions/{id}/hint` | Idempotency key and `{expected_revision}`; consumes one opportunity and returns state with `hint` or `reshuffled`. |
| `GET /api/games/linklink/leaderboard` | Required `spec=6x8|8x8|10x10&window=7d|30d`; six independent boards. |

With `include_path:true`, a successful match also returns `match_path:[{row,col},…]`: two to four vertices of the server-approved connection, starting at `first` and ending at `second`. Coordinates can use the single outer ring (row −1 through rows, column −1 through columns). This optional field is retained only in the existing idempotency receipt and is replayed unchanged. Omitted or false preserves the original response shape and request identity. Current, start, and abandon responses do not include a path.

The board is server-generated and solvable. A legal connection travels through empty cells and at most one perimeter ring with no more than two turns. Completion, timeout, and abandonment delete the active board/lease in the terminal transaction and retain only a 30-day summary. The absolute deadline does not move after ordinary disconnect or process downtime.

Version 2 starts with 2, 3 or 5 shared hint/refresh opportunities for 6×8, 8×8 or 10×10. A hint returns one legal pair and its path. If no pair exists, the same operation reshuffles only remaining tiles. Either action consumes one opportunity; repeated or concurrent requests cannot consume it twice. There is no automatic reshuffle in version 2. At zero opportunities a deadlock can only time out or be abandoned. State and summaries include `opportunities_initial,opportunities_remaining`; old sessions show zero. Successful completion adds 100 score points per unused opportunity; timeout adds none and abandonment has a null score. Ordinary matches update only their returned game state; the browser can accept the next pair while the previous connection animation finishes.

Each leaderboard returns `{spec,window_days,window_start,as_of,rules_version:2,rows,me}`. Rows contain decimal-string `rank,score`, Unix-second `achieved_at`, the existing private/public `identity` union and `is_me`. Only version-2 completed summaries inside the requested window count, with one best score per user. Higher scores rank first; equal scores use the earliest achievement, with a stable private random key only for the same second. The top 20 are returned in `rows`; `me` is the caller's eligible row only when outside that set, otherwise null. Banned, deleted and administrator accounts are excluded and profile visibility is resolved at read time. No board, tile layout, hidden key or internal user ID is exposed.

### 5.5 Three-player rock-paper-scissors

| Method and path | Request / response |
| --- | --- |
| `POST /api/games/rps/tutorial/seen` | Account-level business-unique acknowledgement; 204. |
| `GET /api/games/rps/state` | Strict `idle|queue|session|pending_result` home union. |
| `POST /api/games/rps/queue` | Idempotency key plus `{mode,device_token,deathmatch_confirmed}`; 202. |
| `DELETE /api/games/rps/queue/{id}` | `{expected_revision}` plus idempotency key; 204. |
| `POST /api/games/rps/sessions/{id}/actions` | `{phase_seq,expected_revision,action,payload}` plus idempotency key. |
| `POST /api/games/rps/sessions/{id}/lease` | `{lease_id}`; returns the lease expiry. |
| `POST /api/games/rps/pending-result/ack` | `{session_id}`; business-unique 204. |
| `GET /api/games/rps/leaderboard` | `mode=quick|standard|deathmatch&board=profit_rate|net_profit`; top 20 plus caller. |

The service is authoritative for queueing, phases, deadlines, hidden gestures, defaults, cuts, settlement, result acknowledgement, and both leaderboards. `RPSHomeState` contains exactly one branch. Unrevealed gestures are omitted entirely from other viewers' responses. Every state/action carries the current revision, phase sequence, and identity epoch needed to replace stale client state. A pending private result blocks another queue entry until acknowledged and has no age expiry; shared summaries/ranking facts last 30 days. Fun statistics last for the account lifetime and cannot be drilled down to individual games.

Version 2 retains `own_buy_in,own_cash_out` and adds nullable `own_buy_in_general,own_buy_in_game,own_returned_general` to private results. Queues carry `payment:{general,game}`; the viewer's own seat carries `funding:{buy_in_general,buy_in_game,current_general,game_remaining}`. Each round spends the seat's remaining game-funded amount first; winnings become general-funded chips. At normal final settlement, unused game-funded chips convert to general credits and are included in cash-out. Queue cancellation and administrative refunds preserve original sources. Legacy results with unrecorded funding details use null rather than invented amounts.

### 5.6 Bidding Duel and Turn-based Battle Minigame (Test)

Use `game=bidding|likes` in the paths below. All mutations require an idempotency key; retry an uncertain request with the same body and key, then fetch state. Request bodies are strict JSON, at most 32 KiB, and action objects at most 16 KiB. Responses are `no-store`. IDs are opaque, revisions and phase sequences are canonical decimal strings, and times are Unix seconds. A stale price/fee/catalog hash is a conflict before reservation.

| Method and path | Request / response |
| --- | --- |
| `GET /api/games/{game}/state` | `{server_now,queue,current,latest_result}`; current or queue is owner-scoped, and the latest retained result may also be present. |
| `POST /api/games/{game}/queue` | `{mode,expected_terms_hash,device_token,loadout?}`; `loadout:{role,harness?,skills}` is required for Likes and forbidden for Bidding. Bidding modes: `tier1`, `tier2`, `tier3`; Likes modes: `quick`, `standard`. Returns a bounded queue receipt. |
| `DELETE /api/games/{game}/queue/{id}` | `{expected_revision}`; original-asset refund. |
| `POST /api/games/{game}/sessions/{id}/actions` | `{phase_seq,action}`. Bidding uses `{kind:"joker",use:boolean}` or `{kind:"bid",card:integer}`; Likes uses `{kind:"plan",plan:{purchases,main,extra}}`. |
| `POST /api/games/{game}/sessions/{id}/surrender` | `{phase_seq}`; loss under the frozen entry terms. |
| `GET /api/games/{game}/history` | Owner's last 30 days; signed `cursor` and `limit` (default 20, maximum 100). |
| `GET /api/games/{game}/history/{id}` | Owner-scoped terminal detail. |
| `GET /api/games/{game}/history/{id}/rounds` | Signed `cursor`, `limit` default 5 / maximum 10; complete settled rounds, bounded to 8 MiB. |
| `GET /api/games/likes/sessions/{id}/rounds` | Same paging, for the participant's already settled rounds of the current match. |
| `GET /api/games/likes/catalog` | Versioned public character, skill, buff, harness and mode rules; contains no selected opponent loadout. |

One queue/active slot exists per user and game. The two games can run concurrently. Queues expire after 120 seconds and cap at 4096 per game. Matching requires the same mode and frozen terms, excludes equal device tokens, prefers different source IPs when possible, and assigns seats randomly. Neither device nor IP comparison values leave the server.

Only the losing entry funds a decided match's fees, with each basis-point cut floored separately. The winner receives their original payment split back and the loser's net entry as general credits. A draw or system cancellation refunds both original splits. Platform, welfare and Thursday destinations, escrow, result and terminal operation commit atomically. Surrender is a normal loss. Disconnection does not pause deadlines. Bans, deletion and process startup cancel unfinished new-game sessions; periodic recovery advances live work without treating it as a restart.

Both players submit against the same `phase_seq`; locking one side does not invalidate the other side through a shared revision check. Bidding has 13 rounds, 10-second available-joker decisions and 20-second simultaneous bids. Both cards stay hidden until resolution; a missing bid uses the smallest hand card. Ordinary game history does not list unused reward-deck order, but the separately disclosed terminal randomness proof allows reconstruction of the complete reward decks, including unused cards.

Likes has 20-second planning followed by an event-paced `settlement` phase. Public configuration uses `settlement_seconds:0` to mean variable duration. A round's state, events, draws, payment and terminal facts commit before presentation. `resolution:{round,started_at,ends_at,summary}` is shared by both viewers and retained in the final summary. The summary includes `timeline:[{stage,duration_ms,event_ids}]`, with individually timed steps grouped into seven ordered semantic stages. Each event appears exactly once; opposing actions can share a step while consecutive casts have separate steps. Summed step durations equal `(ends_at-started_at)*1000`, with no fixed overall duration cap. The existing event count and response-size limits still apply. Steps reference server-authored score parts, resource snapshots and success/failure reasons. Older persisted summaries without a timeline retain their original five-second duration. `round_start:{round,started_at,events}` supplies replenishment facts. Clients display those facts without recomputing rules. Final wallet settlement does not wait for animation or a client acknowledgement. Expired animations are not replayed after reconnect, and the next planning period starts with a full 20 seconds.

Normal manual plans require a main skill. A still-stunned player can skip; a plan whose affordable purchases cleanse stun must select a main skill. Automatic timeout plans use the existing automatic rules. Shared-energy overload compares both frozen declared quotes after shopping: an exact fit succeeds; on excess demand the battery empties and only positive-quote players overload, lose their cast payment/effects and keep their shopping. A zero-quote opponent resolves normally without collateral status clearing. Flash checks energy independently. See [the game guide](duel-games.md) and the mode catalog for all rules and presentation behavior.

An `overload` event may include `data.shortage:{payment,resources}`. Payment is `energy`, `mix`, `api` or `sub`; each bounded resource item contains `{resource,required,available}` with nonnegative integer quantities captured at the failed payment, before later restoration. Resource identifiers are `energy`, `burst`, `sub` and `api`; at most three unique items occur. Shared energy uses an unseated event and retains the existing affected-player flags. Personal shortages use that player's seat; mixed payment lists burst and API, adding total subscription quota when it is limiting. Subscription-only payment lists its actually insufficient quotas. Existing `reason` fields are unchanged. Older records may omit this object: clients show a generic overload instead of inferring a past shortage from present balances. No fees, legal-plan validation or result rules change.

The optional browser-local teaching match uses bundled engine-generated fixtures. It does not call queue, action, payment, result or reward write endpoints and does not create server history. Copying its loadout only fills the lobby form. Live status continues polling; an actual queue or game takes precedence over teaching.

During play, all states, round pages, errors and personal exports exclude unrevealed opposing skills. The final Likes result reveals both complete loadouts. Player cursors bind the user, game, scope and snapshot and expire after one hour. A record becomes unavailable exactly at 30 days, before physical cleanup if necessary.

### 5.7 Game randomness

Authenticated `GET /api/games/{game}/randomness/{id}` is available for `fishing`, `linklink`, `rps`, `bidding`, `likes` and `blackjack`. No query parameters or request body are accepted. The bounded, no-store response is `{proof: null | object}`; `null` identifies a retained game created before proof support. Multi-stage games return only the opening commitment until the server commits a terminal state. Terminal proofs, including cancelled games, disclose the seed and transcript; they allow reconstruction of the complete Bidding reward decks or Blackjack shoe, including unused cards. Ordinary game projections omitting those cards do not imply that their order remains secret after disclosure. Ownership, maintenance continuation and the 30-day terminal access boundary follow the parent game; current Blackjack spectators may read the current table only. The 2 MiB proof bound permits up to 65,664 samples across 1,024 purpose streams. See the [complete protocol, phase matrix and verifier](game-randomness.md). No game seed is reused for gesture encryption, authentication, cursors or other games.

### 5.8 Blackjack

The independent nine-seat module uses `/api/games/blackjack`. All responses are `no-store`. Mutations require the existing `Idempotency-Key` header; repeat uncertain requests with the exact same key and body. JSON is strict, limited to 4 KiB, with no unknown, duplicate or case-aliased keys. IDs are opaque, revisions and positions are decimal strings, amounts are decimal credit strings, and timestamps are Unix seconds.

| Method and suffix | Request / response |
| --- | --- |
| `GET /state` | Server time, phase/deadline/next round, current configuration/hash, waiting count, public table and nullable owner entry/seat. |
| `POST /queue` | `{stake,config_hash}`. `201` new or `200` existing entry: `{id,position,state}`. Freezes terms and reserves the game-first payment atomically. |
| `DELETE /queue/{id}` | Empty body. `204` original-asset refund for a waiting or undealt own entry. |
| `POST /sessions/{id}/actions` | `{hand,revision,action}`; hand `0|1`, own hand revision, action `hit|stand|double|split`. `202` receipt `{session_id,batch_at,hand,revision}`. |
| `POST /sessions/{id}/emotes` | `{emote}` where emote is `hello|nice|wow|good_luck|thanks|gg`. `204`; seated users only, rate limited. |
| `GET /history` | Owner's settled games from the last 30 days. `limit` defaults to 20, maximum 50; optional signed `cursor`. `{items,next_cursor}`. |
| `GET /history/{id}` | Own `{summary,table}` including payment composition and public final facts; ownership checked. |

The table starts at each server :00 and :30: 5 seconds of seating, 20 of decisions and 5 of results. Early completion extends the display to the original next-round boundary. Waiting positions survive rounds and restarts; completed participants must explicitly rejoin. One accepted action per seat per second uses the requesting hand's revision. Additions reserve before the batch applies, and insufficient funds leave the hand unchanged. Other seats never cause a hand-version conflict. The public projection never includes the shoe, dealer hole card before reveal, pending intents, participant identities or another user's funding sources.

Owner `payment` is `{general,game}` for all reserved additions. Public hand settlement amounts are decimal integer **milli-credit** strings: `stake_milli,gross_milli,platform_milli,welfare_milli,thursday_milli,net_milli`, plus `outcome`. Each hand is independently rounded and then summed; all normal net returns, including pushes and principal, are general credits. Cancelled tables include public per-seat total `refunds` without funding sources. See [full bilingual rules](blackjack.md).

`GET/PATCH /admin/api/games/config` adds `blackjack:{enabled,min_stake,max_stake,stake_step,default_stake,rake_bp:{platform,welfare,thursday}}`. Credit fields are decimal strings and basis points are integers. The defaults are disabled, 1,000–50,000, step 1,000, default 5,000 and three 100 bp fees. Positive stake values must align to the minimum/step; the `max_stake` ceiling is `140625000000` credits. Each player can invest at most four times the base stake and receive at most eight times the base stake before fees. Nine-seat table totals use decimal strings without truncating them to the per-operation amount limit. Running counts include Blackjack waiting entries and active tables.

Administrator-only `/admin/api/games/blackjack/history` and `.../history/{id}` accept `dataset=recent|anonymous` (default recent); list pages use the same 20/50 limits. Recent details include retained participant IDs, payment composition and operation references. Anonymous details contain only rules version, outcome and public card/settlement facts, with independent archive IDs and no absolute timestamps or emotes. Stewards cannot read these histories.

`POST /admin/api/games/blackjack/history/export` accepts `{dataset,cursor?}` and returns `{format:"blackjack-history/v1",dataset,items,next_cursor}` in bounded batches of at most 10 records. Clients may save each successful batch as NDJSON and resume only from its returned cursor. Signed cursors expire after one hour and bind the account/session, dataset, limit, endpoint kind, high-water mark and page position. Exporting or reading history does not pause play.

Restart cancels only the unfinished table and refunds original assets, preserving waiters and committed outcomes. Maintenance/closure releases waiters and undealt seats while dealt games finish. Ban stops a seat's actions and auto-stands it. Deletion detaches identity and continues settlement without cancelling other players; unavailable proceeds use the corresponding external asset account and cannot recreate a wallet. Queue game-credit reserves remain part of welfare assets. Account export v10 includes `blackjack:{current,history}` and safe per-game `randomness` proofs while retaining all previous fields and existing 10,000-row/16-MiB bounds.

### 5.9 Loans and leaderboards

Loans are an optional activity, disabled by default. `GET /api/activities` adds `loan:{enabled,available,reason,tiers}`; reasons are `disabled|ineligible|negative_balance|available`. Closed activities retain readable history.

| Method and path | Request / response |
| --- | --- |
| `POST /api/activities/loan/quote` | Strict `{tier:"1"|"2"|"3"}`; no financial write. |
| `POST /api/activities/loan` | Strict `{quote_token}` and `Idempotency-Key`; 201 immutable receipt. |
| `GET /api/activities/loans` | Owner history, `page,page_size`, default 1/20; sizes 10/20/50/100. |
| `GET /admin/api/users/{id}/loans`, `GET /api/steward/users/{id}/loans` | Authorized read-only history with the same receipt fields. |
| `GET /api/charity/leaderboard` | Cumulative positive donation credit, `page`, fixed 20 rows. |
| `GET /api/games/leaderboards/charity` | Rolling seven-day net game spending, no query fields. |
| `GET /api/games/leaderboards/net-profit`, `GET /api/games/fishing/net-profit`, `GET /api/games/blackjack/net-profit` | Rolling seven-day actual net profit, no window selector. |
| `GET /api/games/bidding/leaderboard`, `GET /api/games/blackjack/leaderboard` | `window=7d|30d|history`, default 7d. |

A quote includes `principal,a,b,nominal,disbursed,fee,repayment,interest,general_before,general_after,game_before,game_after,quote_token,expires_at,config_revision,as_of`. All coefficients and amounts are canonical strings. A receipt replaces quote expiry/token with `loan_id,operation_id,sequence,created_at`, retaining the terms, configuration revision and actual before/after balances. These accounting fields remain on the wire; the user page displays only Principal, Fee, Game credits received, Interest and General credits deducted. History is ordered by creation time and sequence descending. Bodies are limited to 4 KiB and tokens to 2,048 bytes.

Quotes are owner-bound and expire after 60 seconds. Expiry or changed configuration returns 409 and requires a fresh quote and confirmation. Balance estimates may change before acceptance; the final transaction checks the current general balance is at least zero, then credits game funds and immediately debits general repayment, which may make that wallet negative. Default borrowing of 10,000 adds 9,000 game credits and subtracts 13,000 general credits, including a 1,000 game fee and 3,000 general interest. A general balance of zero can borrow once. Game funds remain usable when general funds are negative. The same key and body replay the receipt; a different key cannot reuse the quote nonce. Loans do not change donation credit, activity pools or game rankings.

Boards return `as_of,statistics_start,window,rows,me`, plus `pagination` for charity. Rows contain `rank,amount,is_me,identity`; identity is `{kind:"anonymous"}` or `{kind:"public",display_name,avatar_url}`. Charity paginates all positive totals. The game boards return the top 20 and a separate `me` only for an eligible caller below them. Charity visibility uses `charity_profile_public`, default false and independent of game visibility. A currently banned user remains ranked anonymously with no avatar; administrators are excluded.

Game charity sums actual spending minus all net returns across six games, combining both currencies 1:1; losses and wins offset. Bidding counts positive pre-fee profit per match, while Blackjack sums positive pre-fee profit per hand. Principal, loans and newcomer awards are excluded. New statistics begin at the fixed installation/upgrade time without reconstructing prior finished games. Rolling windows are `(as_of-duration,as_of]`; expired contributions are advanced before returning a consistent result. Incomplete bounded catch-up returns retryable 503. Higher positive amounts rank first, then earlier achievement of that current amount; returning from 200 to 100 establishes a new achievement time.

The three additional net-profit boards reuse retained seven-day net game facts: final returned value minus actual stakes, including fees, with losses offsetting wins. General and game credits combine 1:1 only for this game statistic. Non-game issuance, loans, administrator adjustments and inactivity decay do not contribute. Only positive totals qualify; top-20, own-rank, anonymity and tie behavior follow the existing game charity board. Existing historical/30-day Bidding and Blackjack positive-profit boards remain separate.

Fishing's administrator configuration also controls the probability that a legendary catch becomes the blue fat fish: default 10%, range 0–100% in 0.01% steps. New accepted batches capture the setting; legendary species, length and reward rules are unchanged.

## 6. Debug and level-6 steward surfaces

### 6.1 Debug

| Method and path | Request / response |
| --- | --- |
| `GET /api/debug/session` | Current metadata or `null`. |
| `POST /api/debug/session` | Starts dry mode or returns the current session; 201/200. |
| `PUT /api/debug/session/mode` | `{mode,expected_revision,live_confirmation}`. |
| `POST /api/debug/session/stop` | `{expected_revision,confirm_inflight}`. |
| `POST /api/debug/session/replace` | `{expected_revision,confirm_inflight}`; creates a new dry session. |
| `GET /api/debug/events` | Debug SSE v2. |

Dry mode intercepts before upstream dispatch. Live mode captures bounded safe protocol and timing observations while omitting credentials and disallowed request/response/header material. Stop or replace can cancel the one live in-flight request at a single linearization point. Sessions are memory-only, bound to the user session, expire after one hour, and end on restart, logout, ban, deletion, or loss of authorization.

### 6.2 Steward

The browser routes below require a currently effective L6 user session on the user host. Reads and final mutations recheck that role in their transaction. Log and donation management expose the same information as administrator views; ordinary user projections and unrelated administrator capabilities remain separate. The three dedicated CallerKey route families are described separately in §6.3.

| Surface | Routes |
| --- | --- |
| Logs | `GET /api/steward/logs`, `GET /api/steward/logs/{id}`, `GET /api/steward/logs/export.csv`, `GET /api/steward/logs/export.json` |
| Maintenance | `GET /api/steward/maintenance`, `POST /api/steward/maintenance/enable` |
| Shared donation management | `GET /api/steward/donations`, `GET /api/steward/donations/{id}`, `POST /api/steward/donations/{id}/review`, `PATCH /api/steward/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |
| Users | `GET /api/steward/users`; `GET|PATCH /api/steward/users/{id}`; `POST /api/steward/users/{id}/ban`; `POST /api/steward/users/{id}/unban` |
| Announcements | The eight routes in §7.3 with `/api/steward/announcements` replacing `/admin/api/announcements` |
| Failure reset | Shared batch and selection routes in §4.2 |

Full stewards can review and manage charity settings across donations. They can enable but cannot disable maintenance. They have no report, legal-hold, account-export or account-deletion route and cannot change cumulative donation credit or grant level 6. They can appoint/remove level-5 trainees. Known-ID donation and request-log details under an active legal hold are available to both full management roles, with a separate steward read audit. Shared management does not widen an account's owner export.

Both management prefixes (`/admin/api` and `/api/steward`) expose read-only `GET {prefix}/donations/{id}/keys/{keyId}/models` and `GET {prefix}/donations/{id}/keys/{keyId}/models/{modelId}/bindings`. These require the appropriate current browser session, accept numbered `page,page_size` only (default 1/20), and recheck role and donation/key parentage in the read transaction. Model rows are `{model_id,full_name,enabled,binding_count,available_binding_count}`, deduplicated across upstream bindings. Expanded bindings are `{binding_id,upstream_model_id,ord,state}`. Every still-associated binding is included, with state precedence `ended|expired|pending|feature_disabled|model_disabled|disabled|suspended|available|unavailable`. They return no secrets or private owner metadata and grant no mutation or CallerKey access.

Penalty history uses `GET {prefix}/users/{id}/penalties`, `GET {prefix}/users/{id}/penalties/{caseId}`, and `GET {prefix}/users/{id}/penalties/{caseId}/actions/{actionId}/evidence`. Lists accept `page,page_size,type=deduction|ban|charity_suspend,state=active|ended`; detail/evidence accept pagination only. They share existing administrator/steward target-user read permissions and recheck authority in the read snapshot. The list is `{data,pagination,legacy_details_unavailable}`; detail is `{case,actions:{data,pagination}}`. Cases contain `id,kind,reason_code,started_at,ends_at,ended_at,state,result`. Actions add safe request/operation links, actor ID, previous/current end times and an evidence count. Evidence is `{rules,statistics,members:{data,pagination}}`, whose members contain only violation kind, time, request link, numeric content count and link availability. No request body or credential is stored here. Ordinary user summaries and account export exclude these rule/statistical/evidence projections. Earlier restrictions keep their original reason without invented case evidence.

Authenticated pre-handler rejections create one bounded request log with `phase=pre_handler`, `rejection_stage,rejection_reason,request_method,request_path`, zero attempts, usage and call fee. Ordinary handled requests use `phase=handler`; log lists and exports accept that exact phase filter. Independent penalty ledger entries are not call fees. Applicable violation windows survive restart, retain already-executed action state and expire by their configured logical window. Active cases remain until ended; ended cases and deduction records remain 90 days. Request links stop working at the ordinary 30-day boundary. Account deletion clears user cases, durable windows and cached state.

Both management prefixes (`/admin/api` and `/api/steward`) provide `GET {prefix}/donations/badge`, with no query or body. The no-store response is `{pending_count,server_now}` with an exact decimal-string count. Only logically active donations with pending handling count; expiration is reflected even before cleanup runs.

`POST {prefix}/donations/{id}/handling/processed` accepts `{expected_handling_revision}` and an idempotency key, with a 16 KiB body limit. It returns `{donation_id,handling}`. Processing is a shared action with one winner, not a per-person read flag. Approval, model binding and key edits do not mark it processed. Handling has `state=legacy|pending|processed|closed`, its own decimal-string `revision`, and nullable `processed_at,processed_by_role,closed_at,closed_reason`. Only actor role and time are public. Terminal pending donations close automatically; closed reasons are `rejected|withdrawn|terminated|expired|member_removed|account_deleted`. Existing processed or legacy handling remains unchanged at termination.

Donation review is independent of its current terminal status. Approved donations retain `review_result=approve` after expiry or termination, including automatic approvals without a reviewer. Donations that expire or are withdrawn before review retain null review fields. A removed reviewer does not erase the original review result. Contradictory state/review combinations remain invalid.

Management donation lists accept `status=pending|approved|rejected|deleted|expired`, `handling=legacy|pending|processed|closed`, and `q` alongside legacy `cursor,limit` or numbered `page,page_size`, and bind cursors to their filters. `handling` is not accepted on the owner list. Search uses donation ID and description, with the same 128-code-point/512-byte/no-NUL limit. Each management key adds decimal-string `binding_count` and boolean `idle`. All actual bindings count, including bindings to disabled models; zero bindings means idle. Owner donation DTOs and exports omit handling and these management fields. Historical management receipts add safe handling/count fields when replayed without rewriting the stored immutable result.

Administrator and steward log list/detail/export entries contain nullable `user_id` and `caller_identity: {discord_nickname,discord_id}`. Caller identity is present only for charity requests with a surviving caller account. Both members are nullable, with UTF-8 byte limits of 256 and 128 respectively. The name uses the current guild nickname, falling back to the stored username, from the latest synced profile. Unlinked/deleted callers produce null. Both roles can read known-ID details retained by an active legal hold; ordinary lists and exports remain limited to 30 days. This grants no legal-hold management permission.

Each management attempt displays its persisted nullable `endpoint_key_id` routing snapshot, including separate IDs for retries. It never returns the key secret. The logical request's `usage.charge` is the authoritative total charge shown in both list and detail. Attempt `usage.charge` remains a compatibility zero, not an independently settled fee; the page does not present it as a charge. CSV and JSON exports share the management row fields and existing 10,000-row/16 MiB all-or-error limits. CSV additionally includes `caller_discord_nickname,caller_discord_id` with spreadsheet-safe escaping. Steward export reads recheck authority in the export snapshot, and all exports are no-store.

`GET /api/steward/logs` accepts `user_id,endpoint_key_id,error_code,status,from,to,endpoint_base_url,upstream_model` plus legacy `cursor,limit` or numbered `page,page_size`; `GET /api/steward/logs/{id}` accepts the corresponding `attempt_cursor,attempt_limit` or `attempt_page,attempt_page_size`. The numbered list/detail responses carry `pagination`/`attempt_pagination`. Export accepts the same filters without pagination; cursors remain bound to role, actor and filters.

| Method and path | Request / response |
| --- | --- |
| `GET /api/steward/charity-models` | `q,enabled` plus legacy `cursor,limit` or numbered `page,page_size`; steward-safe model page. |
| `POST /api/steward/charity-models` | Complete provider/model, enabled, tagged pricing, discount and flatten policy, with optional `token_reserve_credits`; 201. |
| `GET /api/steward/charity-models/{id}` | Steward-safe model detail. |
| `PATCH /api/steward/charity-models/{id}` | `expected_revision` plus a non-empty partial business-field patch, including the optional `token_reserve_credits` override. |
| `DELETE /api/steward/charity-models/{id}` | `{expected_revision,confirmation}`. |
| `GET /api/steward/charity-models/{id}/binding-candidates` | `donation_id,donation_key_id,source=automatic|manual,q` plus legacy `cursor,limit` or numbered `page,page_size`; safe candidate page. |
| `GET /api/steward/charity-models/{id}/bindings` | Complete ordered bindings and `binding_revision`. |
| `POST /api/steward/charity-models/{id}/bindings/batch` | `{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}`. |
| `PUT /api/steward/charity-models/{id}/bindings/order` | `{expected_binding_revision,order}` with the exact current binding-ID permutation. |
| `DELETE /api/steward/charity-models/{id}/bindings/{bindingId}` | `{expected_binding_revision}`. |
| `GET /api/steward/donations/{id}/keys` | Numbered `page,page_size` only; steward donation-key page with `pagination`. |
| `GET /api/steward/donation-sources` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`), `handling=pending`. |
| `GET /api/steward/donation-sources/{source_key}/keys` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`), `handling=pending`, `idle=yes|no`. |

### 6.3 Steward CallerKey automation

`GET|PATCH /api/steward/automation/donation-key-failure-policy` uses the same current L6, CallerKey, lifecycle, maintenance and four-global/one-per-user concurrency gates. GET accepts only `donation_id` and `donation_key_id` query parameters and no body. PATCH accepts only those IDs, `expected_revision` and `failure_disable_threshold`, requires `Idempotency-Key`, and returns the safe policy receipt described in `4.2. Its 30-second bound and shared mutation limits apply. Policy management follows ordinary retained donation-management visibility; it cannot access or modify another user's private credentials. Unknown fields, noncanonical values and forbidden methods fail; stale revision gives 409, missing/unavailable objects 404 and revoked authority 401/403. Current authority and object visibility are checked before replay.

`POST /api/steward/automation/donations` atomically creates one custom endpoint and 1–100 owned keys, submits them in one donation and approves it as the calling steward. It requires `Idempotency-Key` and returns 201 `{endpoint_id,donation_id,keys:[{endpoint_key_id,donation_key_id}]}`. Explicit `connector_type` and URL are required; OpenAI-compatible, Anthropic-compatible and AI SDK Gateway v3 custom sources are supported. Optional per-key settings include recurring limits and `failure_disable_threshold` (canonical U128 string, default `"10"`). Channel IDs and ownership-confirmation fields are not accepted by this dedicated operation.

`POST /api/steward/automation/model-bindings` accepts `{charity_model_id,donation_key_ids,upstream_model_id,manual?}` and processes only the caller's own donation keys. Automatic mode requires this request's successful fresh discovery; manual mode ensures an exact entry with empty new metadata, preserving existing metadata. Repeated identical bindings succeed without reordering. The response is `{charity_model_id,results:[{donation_key_id,status,code?,message?}]}`, with `success|failed|incomplete` in input order. At least one success gives 200; zero successes gives 504 for any incomplete item, otherwise 422. This endpoint has no whole-batch replay receipt.

All automation routes require Bearer CallerKey, current L6 permission and final-transaction generation/ownership checks; cookies cannot replace the CallerKey. They are not listed in the user-station UI. Requests have 256 KiB and 16,384 total-field limits; responses have a 64 KiB limit. At most four automation requests run at once, one per steward, in addition to shared discovery admission limits. Creation is bounded to 30 seconds, binding to 60 seconds and each discovery to 15 seconds. Whole-operation timeout uses 504 `service_unavailable`; per-item result codes additionally include `discovery_failed`, `model_not_found` and `incomplete`. These result envelopes are distinct from the shared error envelope. See the administrator's [complete calling rules](steward-automation.md) for all fields, defaults, limits and retry semantics.

### 6.4 Trainee steward scope

Level-5 sessions may list and maintain only charity models whose administrator/L6-controlled `is_mainstream` flag is true. They cannot create/delete models, edit pricing, allowed levels, public descriptions, parameter exclusions or that flag. Removing the flag immediately removes trainee management authority. User management, donation review, global logs, announcements, maintenance, audit systems, activity configuration and CallerKey automation remain unavailable to trainees.

Within an authorized model, trainees may bind eligible mainstream-source keys, reorder/remove bindings, discover models, and edit charity enablement, `safe_note`, expiry, cumulative/recurring limits, Token reservations and failure state. The immutable donation-time source determines mainstream eligibility. Unbound mainstream candidates may receive these charity edits too. Keys already bound by a full manager remain manageable even when they have a non-mainstream source; after removal, a trainee cannot newly bind such a key. Physical endpoint/secret settings, personal-use settings and physical RPM/concurrency remain outside this scope.

Key/source/discovery routes that need a model context require `charity_model_id` for a trainee. Full stewards may omit it. The server checks the current model and key relation again before reads, dispatch and writes; a supplied ID is not authorization by itself. Key-level charity settings are shared by all bindings, and the UI exposes the affected count and authorized model summaries before changes. Scoped key projections include `safe_note`, `donation_note` and nullable `approval_note` as text, without a secret or unrelated private resource fields.

## 7. Administrator API

The administrator login shell reads anonymous `GET /admin/api/branding` on the administrator host. Its closed response contains only `site_name` and `site_logo_url`, matching the public user-site identity. It rejects query/body input and uses `Cache-Control: no-store`. `/admin/api/config` and every administrative operation retain their existing authentication requirements.

Both administrator and steward charity model projections include `route_strategy`: `ordered`, `random`, or `expiry_weighted`. Creation may omit it to keep the expiry-weighted default; a patch may omit it to leave the setting unchanged. Explicit null, empty, and unknown values are invalid. Ordered routing uses saved binding order; uniform random gives every eligible connection equal weight; expiry-weighted routing uses weights 8/4/2/1 for expiry within 1/7/30 days/later, with unlimited expiry weighted 1. Each request freezes a candidate order without replacement. Strategy changes use the model revision and existing idempotency rules.

Each role also has `GET {prefix}/charity-models/{id}/binding-donations` and `GET {prefix}/charity-models/{id}/binding-donations/{donationId}/keys`, where `{prefix}` is `/admin/api` or `/api/steward`. Both retain legacy `cursor,limit` and accept numbered `page,page_size`; numbered responses include `pagination` and `next_cursor:null`. Donation menu entries contain only `{id,description,key_count}`. Key menu entries contain only `{donation_key_id,source,note}`; `source` has the same safe address, connector and masked fragments as binding candidates, and `note` is the reviewed shared note. They list approved, unexpired shared resources with at least one unbound catalog model. Cursors are bound to the role, actor, model, and donation. These menus never expose donor account identity or private endpoint/key notes and do not expand the steward's donation management permissions.

All routes below are available only on the administrator host with an administrator session. Mutations use idempotency and expected revisions as specified by §1.2, and sensitive final transactions reauthenticate the actor.

### 7.1 Session, configuration, users, and observability

| Surface | Routes and notes |
| --- | --- |
| Session | `POST /admin/api/login`, `POST /admin/api/logout`, `GET /admin/api/session`, `POST /admin/api/auth/elevate` |
| Bootstrap/config | `GET /admin/api/config`; `GET|PATCH /admin/api/site-config`; `GET /admin/api/site-config/catalog`; `PATCH /admin/api/site-config/{key}` |
| Users | `GET /admin/api/users`; `GET|PATCH /admin/api/users/{id}`; `POST /admin/api/users/{id}/ban`; `POST /admin/api/users/{id}/unban` |
| Usage/activity | `GET /admin/api/usage`; `GET /admin/api/activity`; `GET /admin/api/overview/endpoints` |
| Logs | `GET /admin/api/logs`; `GET /admin/api/logs/{id}`; `GET /admin/api/logs/export.csv`; `GET /admin/api/logs/export.json` |
| Alerts | `GET /admin/api/alerts`; `POST /admin/api/alerts/{id}/resolve` with explicit set-state body |
| Maintenance | `GET /admin/api/maintenance`; `POST /admin/api/maintenance/enable`; `POST /admin/api/maintenance/disable` |

User lists additionally accept `level=1|2|3|4|5|6` and exact `user_id` (canonical positive signed-64-bit decimal string). ID, text, level and status filters are combined with AND before pagination and counting; cursor identity includes every filter. Stewards can read non-administrator users, including themselves and other L6 users, but can mutate only other currently effective L1–L5 accounts. Actor and target authority are checked again in the final transaction before any replay or write. A hidden administrator target is 404; disallowed target or operation is 403.

User limits use decimal strings or null: `endpoint_limit` is "0"–"10000", `rpm_limit` is "1"–"4096", and `concurrency_limit` is "1"–"100000". Effective limit fields are strings. In profile PATCH, omission preserves a value and null inherits its default. Invalid input returns 400 with the affected field and range. Manual level can be null or a JSON integer; full stewards may set 1–5 on eligible other users, while only administrators may set level 6. The economy PATCH has `{mode:"economy",expected_revision,target,direction,amount,reason}`; targets are `balance|game_balance|donation_credit`, direction is `increase|decrease`, and amount is a positive credit string. Both wallets may become negative; stewards cannot use the donation-credit target.

The generic site-config patch rejects maintenance, announcement epoch, and all activity/game economic keys; those use their typed domain routes. User patch is a tagged operation for profile, limits/level, or economy and never returns credentials. Log exports retain their fixed privacy projections and spreadsheet-safe encoding.

The administrator list reads retain cursor mode and also accept numbered `page,page_size` with the shared metadata, except where noted: `GET /admin/api/users` accepts `is_banned=true|false`, `q`, and `level=1|2|3|4|5|6`; `GET /admin/api/usage?group_by=user` is paginated, while `group_by=site` is a complete non-paginated snapshot and rejects all page/cursor parameters; `GET /admin/api/activity` is paginated; and `GET /admin/api/overview/endpoints` accepts `q` and is paginated. Numbered endpoint-overview rows contain at most three user previews; the complete group can be read with `GET /admin/api/overview/endpoints/users?base_url=<exact>&page=<page>&page_size=<size>`, which is numbered-only and returns `{data,next_cursor:null,pagination}`. `base_url` is an exact canonical value, not a pattern.

`GET /admin/api/logs` accepts `error_code,status,from,to,user_id,endpoint_key_id,endpoint_base_url,upstream_model` plus cursor or numbered page parameters; `GET /admin/api/logs/{id}` accepts `attempt_cursor,attempt_limit` or `attempt_page,attempt_page_size` and returns `attempt_pagination` in numbered mode. `GET /admin/api/alerts` accepts `resolved=true|false` plus either pagination mode. Log exports accept the same role-scoped non-pagination filters but reject `cursor,limit,page,page_size`.

The optional management filter `endpoint_key_id` is a canonical positive decimal physical-key ID string (1–9223372036854775807, no leading zeros). It matches saved attempt snapshots across personal and charity calls, including failed attempts before a retry selects another key. Each logical request appears once. List, cursor/numbered pagination, CSV and JSON export use the same filter; cursors are bound to this key as well as other filters. Deleted physical keys remain queryable through retained snapshots, subject to existing log retention. Missing historical IDs cannot be inferred from key fragments, endpoint URLs or model names. Only administrators and level-6 stewards may use this filter; personal log routes reject it and level-5 stewards cannot access management logs. No new request content is collected.

Administrator and level-6 steward log rows, details and management exports include optional `charity_model` for charity calls: the requested public model name recorded with the logical request, independent of each attempt's `upstream_model_id`. Missing snapshots omit the field. Self-use model names retain their existing private projection; ordinary-user and level-5 permissions are unchanged. CSV adds the spreadsheet-safe `charity_model` column.

`PATCH /admin/api/site-config` accepts the closed body `{expected_revision:string,values:object}` with 1–64 known generic configuration keys and a 256 KiB request limit. Values use the catalog's scalar types. It validates the combined configuration and commits all changes with one site revision increment and one idempotency receipt. Stale revisions return `409`; invalid combinations return an actionable dependency error. Unchanged values do not advance the revision. The response is `{revision:string,changed_keys:string[]}`; clients may read the complete configuration after saving. The existing single-key route remains available.

### 7.2 Mainstream channels

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/mainstream-channels` | `state=active|retired|all` plus legacy `cursor,limit` or numbered `page,page_size`; administrator page. |
| `POST /admin/api/mainstream-channels` | `{name,category,connector_type,base_url,enabled}`; 201. |
| `GET /admin/api/mainstream-channels/{id}` | Complete channel DTO. |
| `PATCH /admin/api/mainstream-channels/{id}` | Partial business fields plus `expected_revision`. |
| `DELETE /admin/api/mainstream-channels/{id}` | `{expected_revision,confirmation:"retire"}`; irreversible retirement. |

Category is `subscription|api_platform`. At most 100 active channels may be enabled. Endpoint creation copies a channel snapshot; later edits or retirement never rewrite existing endpoints.

### 7.3 Announcements, pools, activities, and games

| Surface | Routes |
| --- | --- |
| Announcements | Exact route family in the table below |
| Pools | `GET /admin/api/pools`; `POST /admin/api/pools/{poolId}/adjustments` |
| Activities | `GET|PATCH /admin/api/activities/config`; `GET /admin/api/activities/thursday`; `PUT /admin/api/activities/thursday/next`; `POST /admin/api/activities/thursday/{periodId}/resume` |
| Games | `GET /admin/api/games/active-counts`; `GET|PATCH /admin/api/games/config` |

Both management roles use the same announcement handler and final-transaction role checks. Create requires `title_zh,body_zh,title_en,body_en,severity,pinned,dismissible`; optional `expires_at` is a Unix-second integer or null. A language may be empty, but a published announcement requires at least one complete title/body pair. Titles allow 160 Unicode characters, each body 64 KiB, severity is `info|warning|important`, and pin/dismiss flags are booleans. PATCH omission preserves fields and `expires_at:null` removes expiry. Withdrawal/deletion reasons allow 1,024 characters and 4,096 UTF-8 bytes. Revision fields are positive decimal strings; all mutations require the usual idempotency key. Actor identity is removed from audits after 90 days and the no-content announcement audit lasts 365 days.

Announcement mutations return a bounded receipt and the detail is fetched separately. Published content and drafts are isolated. Activity/game configuration reads a complete typed snapshot, merges a strict patch, validates all dependent values and checked arithmetic, then commits atomically. Existing accepted work retains its frozen configuration.

The activity master switch pauses admission while preserving each activity's switch. Thursday may remain enabled after its last period settles; this idle state does not block public configuration, administrator branding, unrelated settings or startup. A change from effectively disabled to enabled still requires a configured, open or settling Thursday period in the same configuration transaction.

The administrator page automatically schedules a new Thursday period for the next Thursday at 00:00 Beijing time (UTC+8), lasting 24 hours. On Thursday itself, a new period targets the following week. The page generates the date-based `period_key` and matching `opens_at`; neither requires manual input. Editing an existing configured period preserves its scheduled date. The existing `PUT /admin/api/activities/thursday/next` still receives both fields and validates the Beijing Thursday window, revision and activity state on the server. Saving the period and enabling the activity remain separate actions.

Thursday `literature` accepts empty text or up to 1,024 Unicode characters and 4,096 UTF-8 bytes. LF newlines and tabs are allowed; other control characters are rejected. CRLF input is normalized to LF before idempotency comparison and storage, and responses use LF. The user activity response exposes `thursday.next` only when both the activity master switch and Thursday switch are enabled. Its fields are `period_id`, `opens_at`, `closes_at`, `literature`, `entry`, `per_user_limit` and `pool_balance`. The page shows the current period before a separate next-period preview. Contributions remain restricted to the current open period.

`GET /admin/api/pools` accepts `pool_type=welfare|thursday` and `state=open|closed`, plus legacy `cursor,limit` or numbered `page,page_size`; the response keeps the pool page envelope and adds `pagination` only in numbered mode. The page filters are independent and exact.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/announcements` | `state,severity` plus legacy `cursor,limit` or numbered `page,page_size`; administrator page with numbered `pagination`. |
| `POST /admin/api/announcements` | Complete bilingual draft fields, severity, pin/dismiss policy and nullable expiry; 201 receipt. |
| `GET /admin/api/announcements/{id}` | Administrator draft/published detail. |
| `PATCH /admin/api/announcements/{id}` | `expected_revision` plus a non-empty partial draft patch. |
| `POST /admin/api/announcements/{id}/preview` | `expected_revision` plus an optional bilingual title/body patch; safe rendered preview. |
| `POST /admin/api/announcements/{id}/publish` | `{expected_revision}`. |
| `POST /admin/api/announcements/{id}/withdraw` | `{expected_revision,reason}`. |
| `DELETE /admin/api/announcements/{id}` | `{expected_revision,confirmation:"DELETE",reason}`; permanent delete. |

### 7.4 Donations and charity models

| Surface | Routes |
| --- | --- |
| Donations | `GET /admin/api/donations`; `GET /admin/api/donations/{id}`; `GET /admin/api/donations/{id}/keys`; `GET /admin/api/donation-sources`; `GET /admin/api/donation-sources/{source_key}/keys`; `POST /admin/api/donations/{id}/review`; `PATCH /admin/api/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |

Review and key-management requests include expected revisions and the complete effective limits/expiry needed for that decision. Authorized donation-key projections add `donation_note` and nullable `approval_note` beside `safe_note`, without exposing a secret. Binding/candidate projections otherwise retain their safe resource boundary. Models support per-request or four-bucket per-token prices, rewards, promotions, visibility, flattening, ordered bindings and the existing 100-result management statistic; the public 24-hour `recent_success` is a separate calculation. Administrators and authorized stewards can open a binding's key while preserving the originating model and page.

Administrator/full-steward model configuration adds `is_mainstream` (initially false) and `excluded_request_fields`. The latter is a unique list of at most 32 case-sensitive top-level names, each matching `[A-Za-z_][A-Za-z0-9_]*` with 1–64 characters. Nested paths and wildcards are not supported. The protected names are `model,messages,input,stream,stream_options,tools,tool_choice,functions,function_call,response_format,encoding_format`. A logical charity request snapshots this policy and removes allowed listed fields before optional-parameter validation and protocol conversion; personal calls remain unchanged. The pricing form can fill all donor-reward prices from half the undiscounted user prices, rounding down to 0.001 credit. It changes the form only and leaves subsequent editing available.

Administrator and level-6 steward model-management DTOs expose the optional `token_reserve_credits` override described in §4.1; it is a decimal credit string or `null`, and blank/inherited values do not appear in ordinary user catalog, OpenAI-compatible model, or owner export projections.

Administrator donation and source reads use the management filters and pagination contract above: donation lists accept `status`, `handling`, and `q` in either legacy or numbered mode, while source groups and source-key lists are numbered-only with `q`, `scope`, `handling`, and (for keys) `idle`.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/charity-models` | `q,enabled` plus legacy `cursor,limit` or numbered `page,page_size`; administrator model page. |
| `GET /admin/api/donations/{id}/keys` | Numbered `page,page_size` only; administrator-safe donation-key page with `pagination`. |
| `GET /admin/api/donation-sources` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`), `handling=pending`. |
| `GET /admin/api/donation-sources/{source_key}/keys` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`), `handling=pending`, `idle=yes|no`. |
| `POST /admin/api/charity-models` | Complete provider/model, enabled, tagged pricing, discount and flatten policy, with optional `token_reserve_credits`; 201. |
| `GET /admin/api/charity-models/{id}` | Administrator model detail. |
| `PATCH /admin/api/charity-models/{id}` | `expected_revision` plus a non-empty partial business-field patch, including the optional `token_reserve_credits` override. |
| `DELETE /admin/api/charity-models/{id}` | `{expected_revision,confirmation}`. |
| `GET /admin/api/charity-models/{id}/binding-candidates` | `donation_id,donation_key_id,source=automatic|manual,q` plus legacy `cursor,limit` or numbered `page,page_size`; safe candidate page. |
| `GET /admin/api/charity-models/{id}/bindings` | Complete ordered bindings and `binding_revision`. |
| `POST /admin/api/charity-models/{id}/bindings/batch` | `{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}`. |
| `PUT /admin/api/charity-models/{id}/bindings/order` | `{expected_binding_revision,order}` with the exact current binding-ID permutation. |
| `DELETE /admin/api/charity-models/{id}/bindings/{bindingId}` | `{expected_binding_revision}`. |

### 7.5 Recurring donation-key limits

`GET /admin/api/donations/{id}/keys/{keyId}/recurring-limits` and the matching `/api/steward/` path return `{donation_id,key_id,donation_revision,server_now,rules}`. The owner-only `/api/donations/{id}/keys/{keyId}/recurring-limits` path reads the same safe rule projection for the caller's own donation. Administrator/owner GET accepts no query or body; trainee steward calls require the charity_model_id query scope. Administrators, current level-6 stewards and scoped level-5 trainees can PUT the complete set on their management paths; ordinary owners cannot write it. Pending keys may be configured, while ended or expired keys cannot.

PUT requires `Idempotency-Key`, has a 16 KiB body limit, and accepts exactly `{expected_revision,rules}`. Up to 16 rules are replaced atomically under the donation revision; the response is `{donation_id,key_id,donation_revision}`. Each input explicitly contains all eight fields: `{id,mode,interval,alignment,time_zone,week_starts_on,metric,limit}`. New IDs are `null`; saved IDs are canonical `qlr_` opaque IDs. Omitted old IDs remove those rules. Duplicate IDs or IDs belonging to another key are rejected.

`mode` is `reset|sliding`; `interval` is `1h|5h|day|week|month`. Reset alignment is `first_success|calendar`, and calendar disallows `1h` and `5h`. `1h` and `5h` always mean 3,600 and 18,000 actual seconds, including across daylight-saving transitions. They support first-success reset and sliding windows. Sliding alignment is `null`. `week_starts_on` is 1–7 only for calendar weeks, otherwise `null`. `time_zone` comes from the server's time-zone registry. `metric` is `calls|tokens|input_tokens|output_tokens|credits`. Limits, used, reserved and remaining are canonical U128 decimal strings; credits use at most three fractional digits with no redundant trailing zeroes and must fit U128 after conversion to millicredits. A zero limit blocks new charity calls.

Lifetime `limits.tokens`, `limits.input_tokens` and `limits.output_tokens` are independent nullable nonnegative integer strings; all configured dimensions constrain admission. Automation uses `tokens_limit`, `input_tokens_limit` and `output_tokens_limit`. The input/output limit and reserve scalar fields fit signed 64-bit nonnegative integers. Enabling a split lifetime or recurring limit requires both `input_token_reserve` and `output_token_reserve`; each may be zero, but their sum must be 1–9223372036854775807. That sum is the total reservation. Complete normalized usage replaces the reservation exactly once; missing usage uses the configured split reservation. Legacy total-only keys may keep `token_reserve` and report unknown split usage instead of guessing. Input includes uncached input, cache writes and cache reads once each; total equals input plus output. Existing totals and recurring periods survive upgrade, new split counters start at the new capture boundary, and new rules start on activation. These limits do not implement TPM.

Each rule view adds `{used,reserved,remaining,state,period_start,period_end,next_transition_at}`. Remaining is clamped at zero. State is `limited` if no capacity remains, otherwise `waiting_first_success` for an unstarted first-success period, or `available`; it is not a guarantee that a particular request fits. Period and transition fields are nullable Unix seconds. Natural periods expose the later of the calendar boundary and rule activation as their start. Sliding rules have no fixed reset; an unproven next transition is `null`.

All current recurring rules and total limits apply together to charity calls, including charity live calls. Personal calls, personal live calls, discovery and dry previews do not consume these rules. Claims reserve one call, the current per-key Token fallback and the existing undiscounted price reservation. Dispatch atomically rechecks current configuration and replaces its own old reservations. Calls already dispatched finish under their captured rules; later sends recheck the new set. Changing only a limit or display order preserves usage; changing mode, interval, alignment, zone, week start or metric starts a new counting period for subsequent sends without altering historical total usage or balances.

Only validated successful output establishes the durable first-success time. Calls become used once at that checkpoint; tokens and credits remain reserved until terminal settlement records their full actual or existing conservative amount. Usage exceeding the reservation is never truncated. No validated output means zero recurring consumption. If every unsent candidate is blocked by quota, the caller receives `429 rate_limited`; storage-capacity exhaustion returns `503 service_unavailable` and a deduplicated administrator alert. These failures do not count as upstream failures or disclose physical resources. Later admission failures do not replace the outcome of an earlier dispatched attempt.

### 7.6 Duel history and bulk export

Only an administrator can call these endpoints; L6 stewards are refused. For each `game=bidding|likes`, use `GET /admin/api/games/{game}/history`, `GET .../history/{id}`, `GET .../history/{id}/rounds`, and `POST .../history/export`. Choose `dataset=recent|anonymous` explicitly. Lists accept `mode,rules_version,outcome,from,to,cursor,limit`; time filters apply only to recent records, and outcome filters are `normal|draw|system_cancelled`. Page limits are 100 for lists and 10 for round detail.

Export JSON is `{dataset,selection:{mode?,rules_version?,outcome?,from?,to?},cursor:null|string}`. The first request fixes the selection and dataset; continuation must retain both. Responses are `{format,dataset,items,next_cursor,expired_skipped}` with typed match/round items. A match can continue across pages. One read page per administrator/game runs at a time, with at most 100 records, 8 MiB and a five-second transaction budget. Each record is at most 1 MiB. A one-hour signed cursor binds administrator, game, dataset, selection, snapshot high-water and current match/round position. No mutable SQL offset is used. Recent records that expire between pages are skipped and counted; lost authority, cursor expiry or malformed input fails explicitly. Repeating a valid page cannot duplicate a business settlement. The UI assembles bounded UTF-8 NDJSON files of at most 16 MiB and advances its resumable cursor only after saving each part.

Recent records include actual participant/payment information. Anonymous records retain complete rule traces but remove user references, original match/operation IDs, absolute times, payment sources and cross-match identity links. The administrator audit records actor, game, dataset, filter names, count and outcome, without exporting request content or cursor values into logs.

### 7.7 Reports and legal holds

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/reports/badge` | Pending badge count. |
| `GET /admin/api/reports` | Optional `status` plus legacy `cursor,limit` or numbered `page,page_size`; case page with numbered `pagination`. |
| `GET /admin/api/reports/{id}` | Optional `materials_cursor,materials_limit` or numbered `materials_page,materials_page_size`; case detail, bounded material page, and numbered `materials_pagination`. |
| `GET /admin/api/reports/{id}/targets` | Legacy `cursor,limit` or numbered `page,page_size`; target page, each target includes canonical `donation_match_count`. |
| `GET /admin/api/reports/{id}/targets/{targetId}/donations` | Legacy `cursor,limit` or numbered `page,page_size`; donation lineage page. |
| `POST /admin/api/reports/{id}/approve` | Material/target versions, reason, and confirmation. |
| `POST /admin/api/reports/{id}/reject` | Material/target versions and reason. |
| `POST /admin/api/reports/{id}/resume` | Restarts an approved-processing checkpoint. |
| `GET|POST /admin/api/legal-holds` | GET filters `state,object_kind` plus legacy `cursor,limit` or numbered `page,page_size`; POST is an elevated create. |
| `GET /admin/api/legal-holds/{id}` | Elevated hold detail. |
| `POST /admin/api/legal-holds/{id}/release` | Elevated release. |

Lineage items are exactly `{donation_id,donation_key_id,donation_status,key_state,expires_at,ended_reason,ended_at}`. They exclude donor identity, descriptions, safe notes, fingerprint, source key ID, and provenance. Legal holds cover only the documented object roots, never identity/session/account deletion, and last at most 365 days. The ordinary list/export window and new-report matching period do not change. An active hold can retain its object beyond ordinary expiry for authorized reads by known ID until the hold ends.

## 8. Server-sent events

`GET /api/events` uses protocol version 1 for the closed channels `activities|rps` and frame types `snapshot|delta|gap`. Snapshot and delta frames contain a complete authoritative snapshot, not a patch. Every frame is at most 64 KiB. RPS frames are participant-only and carry the matching revision and identity epoch; a higher epoch replaces the complete cached seat/event view. Gap reasons are `process_restart|ring_expired|ring_evicted|slow_consumer` and are immediately followed by a snapshot.

`Last-Event-ID` replay works only for the same account, process, and five-minute/512-KiB ring. Event IDs and rings are memory-only. `: heartbeat` comments create no event ID. Revocation disconnects immediately. During maintenance, only a continuation with an already valid game lease may reconnect to its one bound session.

`GET /api/debug/events` is Debug protocol version 2. It has a separate session generation and event sequence, supports same-generation replay, emits explicit gap/snapshot and terminal `session_end`, sends a comment heartbeat every 15 seconds, closes after the session ends, and excludes upstream/request/header/body secrets and disallowed payload fields.

## 9. Export, privacy, and account deletion

Version 10 adds top-level `limited_activities`, `image_tasks` and `inactivity`. Limited activities contain the safe general/paper/brush wallet projection and own exchange receipts; the existing game wallet remains separate. Image tasks contain only local ID, state, requested/actual image counts, times, billing state and two-currency charge/refund facts. Inactivity contains safe own activity timestamps and retained execution outcomes. No prompts, complete execution parameters, images, source facts, raw errors, risk rules, private adapter/configuration or upstream task identifiers are included.

Export v10 retains previous safe account, resource, wallet, activity, donation and game fields, including `game_onboarding_holds`, `loans`, `game_rankings` and `penalties`. The attachment is `nonbiriapi-account-export-v10.json`, with `schema_version:10` and `generated_at`. `user` adds `charity_profile_public` and nullable `donation_credit_achieved_at`, without internal tie sequence. `game_onboarding` includes `game_key,task_key,award,completed_at,operation_id`; pending qualifications contain only `id,game_key,task_key,created_at`, never internal capacity or parent/account IDs. Loan exports contain `loan_id,operation_id,created_at`, the terms and actual balances listed in §5.9, excluding quote tokens, nonces, internal sequence and configuration revision. `game_rankings` contains `statistics_start,totals,events`: totals contain `board,window,amount,achieved_at`; events contain `game,settled_at,loss,positive_profit` with expired amounts null. Charity contributions expire at seven days and profit events at 30 days; historical totals remain until account deletion. `penalties` contains safe reason codes, actual/expected times, state/result and actions, with owner request/operation links where available; it excludes rules, thresholds, counted members and management evidence. Active cases persist; ended cases and direct deductions expire after 90 days, while request links expire after 30 days. Every collection, and the combined actions across penalty cases, is capped at 10,000; the complete JSON is capped at 16 MiB. Exceeding either fails without truncation. Bounded ranking catch-up can return retryable 503. Game settlement and safe projections share one transaction, so exported balances and qualifications agree.

Blackjack contains the owner's safe current queue, payment composition and retained history; opposing identities, funding, hidden cards and equipment are excluded. `randomness` contains only owner-accessible proofs: active items disclose commitments, terminal proofs disclose seeds and bounded streams for independent verification, including reconstructed unused deck order. Proofs do not extend the parent's 30-day access. Endpoint/key metadata, safe origins, current recurring limits, check-ins and original game payment/rule fields retain their established projections. No raw keys, ciphertext, authentication tokens, Debug bodies or internal scheduling/audit data are exported.

Secrets, ciphertext, fingerprints, request/response bodies, report data, IP material, other identities, complete pool ledgers, announcement copies, anti-collusion values, workers/checkpoints/replay internals, management notes, channel category/revision, internal source IDs, and administrator/steward audit material are excluded. If the result exceeds 16 MiB or any collection exceeds 10,000 rows, export fails atomically with 413; it is never silently truncated.

Account deletion is synchronous. It revokes credentials, removes private projections, releases undispatched reservations, transfers accepted shared work to deidentified settlement destinations, removes leaderboard/public identity, scrubs donation-private fields, and clears all four wallet balances against their corresponding external balancing accounts in one coordinated boundary. Late workers and callbacks can finish only from a persisted handoff and cannot recreate the account, wallet, credentials, identity, or private aggregates.

## 10. Maintenance, recovery, and retention

Maintenance mode rejects new public inference, OAuth admission, ordinary pages, reports, activities, queues, and games. Health, safe configuration, logout, administrator control, workers, and work already accepted before the switch remain available. A game continuation additionally requires the same user/session and a valid pre-existing lease; it cannot start a game, queue, list, or open a new lease after losing that authority.

New Fishing batches, LinkLink sessions and RPS queues always use rules version 2; clients cannot choose an older version. Old version-1 state drains through its saved rules and funding, including after restart. Already terminal fees and game results are never recalculated under current prices or rules.

Recovery runs before listeners open. Recovery of unfinished API requests drains once per process before accepting traffic; periodic maintenance cannot settle live requests as abandoned after a restart. Accepted requests, donation claims, report indexing/deletion, Thursday settlement, Fishing settlement, LinkLink deadlines, and RPS matching/phases/terminal processing resume from persisted state and exact-once operation identities. Non-terminal records are not removed by age. SQLite WAL/checkpoint restart is supported for a valid Generation 2 database, including after an explicitly supported additive update. Unsupported schemas remain outside the recovery boundary.

Bidding Duel and Turn-based Battle Minigame (Test) retain complete participant-accessible terminal records for 30 days, then atomically migrate complete traces into long-term anonymous archives and remove the linked source. Deletion cancels active play and removes or de-identifies participant data in the same writer boundary. Anonymous records contain no original timestamps or cross-match identity mapping. These new-game rules do not extend the retention of the three older games.

Principal retention periods are:

| Data | Retention |
| --- | --- |
| Request/log identity and detailed log material | 30 days; minimal request/claim/accounting facts continue only while required by unsettled work or their domain deadline |
| Idempotency replays and accepted-operation technical records | 24 hours after a queryable domain result; non-terminal work remains |
| Closed issues | 90 days, at most 1,000 per account |
| Donation/review/reservation records | 400 days after terminal/finalized state |
| Recurring donation usage | Sliding aggregates at most 35 actual days; reset keeps the current period and older periods still required by unfinished work. Minimal unfinished receipts survive until settlement. No caller identity or request content is stored in these aggregates. |
| Report cases | 90 days from creation; approved processing may cross only until deletion completes |
| Donation report fingerprint | Until exactly 90 days after that key instance ends; the deadline never moves |
| Fishing terminal batches/outcomes and their display lengths | 30 days after settlement |
| Fishing recent-length facts | The matching 30-day ranking window, independent of ACK/outcome cleanup; lifetime best and its display length remain for account lifetime |
| LinkLink terminal summary | 30 days; six boards read the retained successful version-2 summaries |
| Independent check-ins and once-only game newcomer completions | Account lifetime; deleted with the account |
| Temporary newcomer reward-capacity holds | Until the associated accepted game settles or releases; never exported |
| RPS shared summary/rank facts | 30 days; pending private result until acknowledgement; fun totals for account lifetime |
| Announcement audit without content | 365 days; actor identity removed after 90 days |
| Administrator/steward domain audit and maintenance event material | 400 days; maintenance actor identity removed after 90 days |
| Legal-hold metadata and no-content audit | 400 days after release/expiry; a hold itself ends within 365 days |

Expired endpoint secrets become unclaimable immediately and are physically removed within one hour after their last non-terminal claim releases them. Retention cleanup and holds preserve only their documented minimum object graph and never disclose additional data through an API.

## 11. Restricted diagnostics and manual risk auditing

These browser-session reads require a current administrator on the administrator host or a current level-6 steward on the user host. Level 5, ordinary users and CallerKeys cannot read them, including for their own requests. Sensitive reads revalidate the actor and session in the same read transaction. They do not add these fields to personal exports or ordinary log exports.

| Method and path | Purpose |
| --- | --- |
| `GET {prefix}/logs/{id}/source` | Approved source facts for one authorized logical request. |
| `GET {prefix}/logs/{id}/attempts/{seq}/errors` | Failure events, including failed attempts before eventual success. |
| `GET {prefix}/logs/{id}/attempts/{seq}/errors/{event}` | Bounded original event body and capture state. |
| `GET {prefix}/diagnostics`, `GET {prefix}/diagnostics/{id}` | Model-discovery and image submit/query/discovery failure diagnostics. |
| `GET /admin/api/logs/diagnostic-capacity` | Raw-body capacity and omissions; administrator only. |
| `GET {prefix}/abuse-audit/users`, `GET {prefix}/abuse-audit/users/{id}` | Risk candidates and bounded supporting observations. |
| `GET {prefix}/abuse-audit/shared-ips` | Qualified shared-address observations. |
| `GET / POST {prefix}/abuse-audit/client-rules`; `PATCH / DELETE {prefix}/abuse-audit/client-rules/{id}` | Manual client-clue rules and evidence. |
| `POST / GET {prefix}/abuse-audit/client-scans` | Start/replay a frozen background scan; list the actor's ten most recent retained scans. |
| `GET {prefix}/abuse-audit/client-scans/{id}`; `GET .../{id}/results`; `POST .../{id}/cancel` | Read progress, page matching logical requests, or stop while retaining committed results. |
| `GET {prefix}/abuse-audit/config`; `PUT /admin/api/abuse-audit/config` | Shared read, administrator-only threshold changes. |
| `GET {prefix}/abuse-audit/access-events`, `GET {prefix}/abuse-audit/access-summary` | Authenticated auxiliary events and separate anonymous counts. |

`{prefix}` is `/admin/api` or `/api/steward`. Reads use bounded filters and pages, not an unrestricted header or body search. Access-event filters include `user_id,key_generation,path_kind,status_class,from,to,lookback_hours`, with `page_size` up to 100 and a separate opaque cursor. A maximum 30-day range and coverage/drop counters keep incomplete observations explicit. A zero ratio denominator is no sample, never an infinite risk score. `coverage.last_gap_at` is null when no capture gap has been recorded. Audit users, user details, shared IPs and access reads accept `lookback_hours` (integer 1–720), anchored to server time and mutually exclusive with `from`/`to`. With no time filter the default is 24 hours, except shared IPs use the configured window.

Original failure capture covers personal/charity calls, retries, explicit streaming error events, model discovery and the image activity. It captures the HTTP-decoded error entity or error event, not TLS/chunk/compression wire bytes. Each body is capped at 1 MiB; truncation is explicit. JSON has formatted and original views, other text is rendered as text, and non-UTF-8 bytes use a base64 representation without pretending replacement text is the original. Local refusals have the site's safe reason and no invented upstream body. Successful response bodies, normal stream content, request bodies and Authorization headers are not actively logged. An upstream can echo inputs or credentials in an error; those original bytes may be retained in this restricted store.

New image-discovery diagnostic entries optionally include `dispatch:{method:"GET",url,request_body:"",content_type:"",dispatched_at}`. This is the dispatched snapshot, not a URL reconstructed from later configuration. GET has no request body. Historical entries without a snapshot leave `dispatch` absent. The retained response, HTTP status, content type and omission/truncation state use the existing diagnostic fields and raw-body limits. No saved Authorization header or image-generation request payload is added.

Ordinary diagnostics and source facts expire after 30 days; existing explicit legal holds may retain their approved object graph longer. `request_error_body_budget_mib` defaults to 1024 and accepts 1–65536 whole MiB. It counts raw payload bytes, including held bodies, rather than SQLite/WAL overhead. At capacity, a new raw body is omitted in full and marked `capacity_exhausted`; safe summaries and API service continue. Existing unexpired bodies are not evicted. Lowering the budget below usage pauses new raw-body storage until capacity is available.

Source facts contain normalized full IP and provenance (`direct_peer`, `trusted_forwarded` or `peer_fallback`) plus approved client clues. User-Agent is limited to 2,048 bytes. Origin, Referer and HTTP-Referer retain only an origin, with user info/path/query/fragment removed, at most 512 bytes each. X-OpenRouter-Title and X-Title remain separate, at most 256 bytes each. X-Stainless-Lang, X-Stainless-Package-Version, X-Stainless-Runtime and X-Stainless-Runtime-Version are each limited to 128 bytes. The combined projection is at most 8 KiB and records truncation/duplicate quality. Untrusted forwarding headers cannot establish identity; fallback addresses are excluded from shared-IP findings. Self-reported headers can be absent, forged or rewritten by a relay.

The management rule editor provides populated, suspected-client presets: Tavo (`user_agent` prefix `Tavo/`), New API (`openrouter_title` equals `New API`) and One API (`legacy_title` equals `One API`). Title matching defaults to case-insensitive. New API and One API presets do not require a particular HTTP-Referer origin. These headers occur only on some forwarding paths; forks retaining them may match, while renamed or hidden markers require separate evidence-based custom rules. Presets are drafts until explicitly saved and enabled; they neither rewrite existing rules nor establish abuse or trigger automatic penalties.

### Persistent client scans

Creation accepts `{"request_token":"unique_attempt_token","lookback_hours":24,"kind":"total"}` or an explicit `from,to` epoch-second window instead of `lookback_hours`. Optional `model` is exact; `kind` is `total|self|charity|unclassified`. The range is at most 30 days. `request_token` is 16–64 ASCII letters, digits, `_` or `-`; retry the same token and identical body after a lost response. A different body with a retained token returns 409. Tokens expire with their scan; a token still awaiting expiry cleanup also returns 409. Creation returns 202, including an immediate empty/completed result. Current actor/session authority and CSRF are required; the token is not a credential.

Each `scn_` task freezes the resolved window, filters, enabled rule revisions and highest recorded log ID. One global worker processes at most 100 candidate source records per independent transaction, then applies kind/model/rule filters. Shutdown or cancellation preserves the last committed checkpoint; restart resumes. There is one unfinished task per actor, at most eight queued tasks and 32 retained tasks in total; capacity conflicts return 409. Each task lasts 24 hours and retains at most 100,000 unique logical-request references. Hitting that limit yields `limited` with `result_limit`; repeated storage failures yield `failed` with `scan_failed`, preserving committed results. No enabled rules produces an immediate completed scan with zero matches.

Scan metadata is `id,state,reason,from,to,kind,model,candidates,scanned,matched,rule_count,created_at,updated_at,expires_at`. States are `queued|running|completed|cancelled|limited|failed`; candidate/scanned counts are decimal strings, other counts are bounded JSON integers. Candidate counts cover the frozen source window before kind/model filtering. Result reads accept only `page` (1–2147483647, default 1) and `page_size` (10/20/50/100, default 20), returning `{scan,items,page,page_size,total_items,total_pages}` with decimal-string page/count fields. Out-of-range pages clamp to the last page. Totals describe currently retained matching references and remain provisional while scanning. Source deletion/retention may reduce them. Each result uses the existing source projection and frozen rule evidence, capped at 20 match details per request and 100 per page.

Cancellation accepts `{}` and is idempotent. Tasks are private to their creating actor and role, even between administrators; another actor gets 404. Permission loss invalidates all of that actor's scans, including completed ones, and restoration does not revive them. Account/source deletion removes related references; expiry and revocation cleanup use bounded batches. These transient management records are excluded from personal exports, never collect request bodies and do not change existing source retention or create automatic penalties. Legacy cursor-based client observations remain compatible.

Risk signals include both personal and charity calls. RPM and concurrency are measured at authoritative admission, associated with the logical request rather than retries; pre-parse observations remain unclassified rather than guessed. Defaults flag at least 80% of the effective user limit for five complete consecutive minutes. Concurrency uses occupancy time divided by 60,000 ms, with a separate peak. Missing coverage, quota changes or unlimited quotas break a sustained chain. Shared-IP defaults are at least three accounts over rolling 24 hours. Administrators may change the thresholds; full stewards may read them. Client rules use bounded equals/contains/prefix comparisons, at most eight AND clauses per rule and OR across rules, with suspected/confirmed status and evidence notes or links. Rules do not execute scripts or regex. A match is a review lead, not identity proof or an automatic penalty; existing configured anti-abuse penalties continue independently.

Auxiliary observation preserves existing responses for exact user-host requests: `GET /v1/models`, `GET /dashboard/billing/subscription`, `GET /dashboard/billing/usage`, their `/v1/dashboard/billing/...` variants, `GET /v1/sub2api/billing`, and `HEAD /v1/chat/completions`. Only a valid CallerKey associates an event with its actual user and generation; rotation/revocation/deletion clears key attribution, and queued writes revalidate it. Unauthenticated, invalid or barred requests contribute only anonymous minute counts, without an IP/header identity record. No query, request body, cookie or Authorization value is captured. These probes do not refresh activity or consume user model-call RPM. The queue is capped at 4,096 and retained authenticated events at one million; dropped coverage is visible. Image background polling and model discovery likewise do not inflate a user's model-call count.

## 12. Economy and inactivity administration

Only administrators may read `/admin/api/economy-audit/summary`, `/series`, `/channels` and `/operations`. General credits, game credits, draft paper and brushes are separate assets, never summed. Reports distinguish new issuance/permanent retirement from user income/expense/internal transfers, so refunds, releases and pool movements do not create false issuance. Stock separates positive available user balances, frozen balances, pools and platform holdings; negative balances are shown separately. Hour/day series use the site's business time zone. Reconstructable ledger history carries a coverage start and explicit gaps; no opening balance is invented.

Only administrators may `GET / PUT /admin/api/inactivity-policy`, `POST /admin/api/inactivity-policy/preview`, or read `/admin/api/inactivity-policy/runs` and `/audits`. Master policy, decay and protective ban start disabled with no inactive-day threshold. When enabled, bounded periodic execution uses current policy, role, activity and balances in one transaction. Administrators and levels 5/6 are exempt; donors are not. Qualifying activity is a successful login/API call, check-in, benefit claim or effective game/activity action. Passive rewards, page/background polling, failed actions and idempotent replays do not refresh activity.

Decay and protective ban have independent inactive-day thresholds. Positive available general/game balances can have separate percentage or fixed-amount decay, periods and retained floors. Frozen funds, negative balances and activity currencies are excluded. First enabling, re-enabling or tightening a policy grants at least seven days; existing accounts start observation at upgrade. A missed series of decay periods catches up at most once, and a simultaneously due protective ban wins without decay. Protective bans retain the account and balances and stop later decay; an administrator can unban and restart observation. Only this protective reason allows existing donated keys to remain usable with normal rewards while the donor's login and API remain blocked. Ordinary bans keep their existing donation restrictions.

## 13. Limited activities and image generation

Existing activities remain permanent activities. Limited activities have separate pages, administrator-only opening/visibility/pause settings and module-specific configuration. Hidden means omitted from the directory, not inaccessible by a known link. An unconfigured opening period means closed. Reopening preserves balances and history.

The picture-book activity's exact routes, strict public/private DTOs, declarative adapter, exchange prices, queue limits, billing and recovery rules are documented in [the image activity guide](image-activity.md). Its public models use only local IDs and approved parameters; real model IDs, endpoint, key, internal task identifiers and original failures stay out of ordinary user responses. No CallerKey image endpoint is added.

Submission atomically reserves both activity currencies at per-image price times requested count. Partial successful generation still charges the full accepted task; failure, unknown-result timeout or cancellation before dispatch refunds both currencies. Closing the page is not cancellation. Prompts and execution parameters remain in RAM; lost queued payloads refund after restart, known asynchronous tasks resume queries, and uncertain submissions are never sent twice. Original images have a ten-minute RAM pickup window; successful generation is not refunded if a result is lost or not downloaded. Natural activity end lets accepted tasks finish; manual pause/maintenance cancels undispatched tasks with refunds. Ban/deletion and model withdrawal follow the documented atomic cleanup rules. Safe task history lasts 30 days; accounting follows ledger retention and export v10.


### Administrator account protection

`GET /admin/api/alerts` accepts optional `kind` alongside `resolved` and either cursor or numbered pagination. Kind is a closed alert-kind value, including `account_deleted`; cursors are bound to both filters. `POST /admin/api/alerts/resolve` accepts `{"ids":["1","2"]}` with 1–100 distinct positive decimal ID strings. It atomically marks those alerts resolved, preserves previously resolved timestamps, and returns `{"resolved_count":2}`. A missing ID returns 404 without partial writes. All alert routes require an administrator session; writes retain same-origin/CSRF checks.

An `account_deleted` alert includes `account_deletion:{user_id,discord_id,general_balance,game_balance,donation_credit,sketch_paper,sketch_brush}`. IDs and exact point amounts are strings. The snapshot is captured after pending domain work is settled and before wallet zeroing, inside the account-deletion transaction. General or game balance below zero creates an unresolved alert; otherwise it starts resolved. The account link is detached, while the explicitly retained identity and snapshot survive deletion and resolution. No request body, credential, or other users' economic data is included.

Administrator-only blacklist routes are `GET /admin/api/blacklist` (optional exact Discord ID `q`, `page`, `page_size`), `POST /admin/api/blacklist` with `{"discord_id":"123456789012345678","reason":"Policy violation"}`, and `POST /admin/api/blacklist/{discordID}/remove` with no body. Discord IDs are canonical positive uint64 decimal strings; reasons are 1–1024 characters, at most 4096 UTF-8 bytes. Writes require `Idempotency-Key` and the usual administrator session and CSRF protection and return 204. Re-adding an ID updates its reason without changing its original creation time. List entries contain `discord_id,reason,created_at,user_id` (the current account ID or null).

Adding a blacklist entry and permanently banning an existing non-administrator account are atomic. Active game cancellation, session/key revocation and authority invalidation follow the existing permanent-ban rules. Registration checks the blacklist in its transaction; database guards also reject registration and temporary or removed bans while the identity remains listed. Unban or temporary-ban requests return 409 until the entry is removed. Removing an entry does not unban an account, restore sessions/keys, or erase historical alerts. Stewards of either level cannot manage the blacklist or read deletion alerts.

Deletion alerts follow the existing persistent administrator-alert storage: resolution is not deletion, and there is currently no automatic expiry. Blacklist entries persist until an administrator removes them. Both are management security material, excluded from personal exports.


Model-discovery status (`GET /admin/api/limited-activities/picture-book/models/refresh/{operationID}`) may include `http_status`, an integer from 100 through 599, on failed operations when retained diagnostic metadata is available. The field is omitted otherwise. This administrator-only projection does not return response bodies, provider messages, headers or credentials.
