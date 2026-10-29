# NonbiriAPI HTTP API Contract

Service, endpoint, service-owned key, model-list and donation semantics are described in [service connections](service-connections.md). Existing endpoint and single-target clients retain their original explicit scope.

- Status: **v1.3.0 source contract**. Check an instance's deployed build before using routes or configuration added by an update.
- Scope: model discovery and direct HTTP/SSE text, image, audio, embedding, moderation, rerank, Decisions and System One inference. See the [operation and connector matrix](direct-inference.md) for exact paths, conversion limits and the excluded persistent/realtime APIs. There is no Anthropic-native public ingress.
- Authority: this document reflects the current source route registry, strict request/response types, stable error catalog, and contract tests. A future wire change requires a changelog entry; undocumented database fields never enter an API response automatically. The session-authenticated picture-book activity has its own configuration and accounting, separate from the public image-generation model route.
- Release boundary: new source behavior is listed in the [changelog](../CHANGELOG.md). This document does not assert that an instance has deployed it.

## 1. Shared wire rules

### 1.1 Stations and authentication

| Station | Host | Authentication |
| --- | --- | --- |
| User | configured public host | user session for `/api/*`, except the finite personal/steward automation routes in §3.4/§6.3 and the temporary denial read below; CallerKey Bearer for `/v1/*` and those automation routes |
| Administrator | distinct configured admin host | administrator session for `/admin/api/*` |

Host selection is a security boundary. A route on the wrong host is `404 not_found`, and a user, administrator, or steward credential never changes station. `GET /healthz` is an anonymous liveness probe on both hosts and returns `{"status":"ok"}` without opening the database.

`GET /readyz` is an anonymous readiness probe. Once the HTTP listener is bound and serving, it returns HTTP 200 with the fixed JSON `{"status":"ready"}` only when fully initialized, or HTTP 503 with `{"status":"not_ready"}` during shutdown or after a critical application worker fails. Before initialization and listener binding, requests may be unreachable. Opening the database alone does not make the application ready. The response does not expose internal readiness details.

User and administrator session cookies are host-only, HttpOnly, SameSite=Lax, and Secure on HTTPS. Unsafe cookie-authenticated methods require the same validated origin (and compatible Fetch Metadata when supplied). `/v1/*` accepts only `Authorization: Bearer nbk_<secret>`; an upstream credential is never a CallerKey.

Account export/deletion and selected administrator legal-hold/user actions require a short-lived, single-use elevation capability bound to the active session. Current administrator/session authority and steward level are revalidated in sensitive read/write transactions. Levels 1–4 may be automatic; level 5 is a manually appointed trainee and level 6 a full steward. Administrators appoint or remove level 6; administrators and full stewards appoint or remove level 5. Full stewards cannot manage themselves, other level-6 users or administrators. Level-5 defaults retain the scoped charity permissions in §6.4 and site-wide log reads in §11; per-account grants may change these actions. Without `logs.unredacted`, upstream error details redact IPv4 and non-mainstream endpoint DNS identifiers. See [steward permissions](steward-permissions.md).

An incorrect password at `POST /admin/api/auth/elevate` returns `403 elevated_required`; the active session remains valid. A missing or expired session returns `401 unauthorized`.

#### Account authenticator verification

Ordinary accounts at levels 1–6 may enable TOTP. Administrators can require it
independently for each effective level with `mfa_required_level_1` through
`mfa_required_level_6`; all six default to false. An enrolled account requires
verification even when its level policy is off. The administrator station keeps
its separate authentication.

An unverified enrolled or required account receives `403 mfa_required` from
browser account routes, including `/api/session`, `/api/me`, CallerKey controls
and steward pages. No profile or balance accompanies the challenge. OAuth,
logout and the five exact MFA routes below remain available. CallerKey model
calls and the finite CallerKey automation routes keep their existing authority;
they cannot manage MFA.

| Method / path | Body | Result |
| --- | --- | --- |
| GET `/api/auth/mfa` | none | `{enrolled,required,verified,verified_until}` |
| POST `/api/auth/mfa/setup` | `{}` | `{secret,otpauth_uri,expires_at}` |
| POST `/api/auth/mfa/confirm` | `{code}` | MFA status |
| POST `/api/auth/mfa/verify` | `{code}` | MFA status |
| POST `/api/auth/mfa/disable` | `{code}` | MFA status |

These routes accept the current user cookie, no query parameters, and return
`Cache-Control: no-store`. Times are UTC Unix seconds; `verified_until` is null
without a valid proof. Setup lasts ten minutes and belongs to the current
session. Only pending setup exposes its seed, for a local QR code or manual
entry; an enrolled seed is never returned. Codes use six digits, SHA-1 and a
30-second step with one neighboring step in each direction. A consumed step
cannot be reused. Five failed attempts within fifteen minutes block further
attempts for fifteen minutes with `429 rate_limited` and `Retry-After`; changing
sessions or restarting setup does not clear this account-wide budget.

Successful verification grants a fixed seven-day proof for that browser session.
Session renewal does not extend it; logout, replacement and reset revoke it.
Required accounts cannot disable MFA. Optional accounts must supply a fresh code
to disable it. `mfa_invalid_code` is 403 and `mfa_setup_expired` is 409. The browser
retains unfinished forms during a challenge and never automatically replays a
failed mutation. Private streams also stop when their authorization expires.

Administrators read enrollment with `GET /admin/api/users/{id}/mfa` and reset it
with `POST /admin/api/users/{id}/mfa/reset`, an empty body and a fresh
`X-Elevated-Token`. Both return `{enrolled:boolean}`. Reset removes enrollment and
all target browser sessions; the user signs in again and follows current policy.
Administrator targets are rejected. CallerKey credentials remain unchanged.

`DELETE /admin/api/users/{id}` requires a fresh administrator capability, an `Idempotency-Key`, and `{"expected_revision":"<user revision>","confirmation":"DELETE"}`. It rejects administrator targets and stale revisions, drains accepted requests, and runs the account cleanup workflow. Success returns `204`; an identical retry with fresh verification replays that result without repeating deletion. Reusing the key for another target or revision returns `409 conflict`.

#### Browser cross-origin access

The five exact public model routes support CORS: `GET /v1/models`, `POST /v1/chat/completions`, `POST /v1/embeddings`, `POST /v1/images/generations`, and `POST /v1/decisions`. Responses, including authentication, maintenance, validation, rate-limit and upstream errors and streaming responses, carry `Access-Control-Allow-Origin: *`. Browser clients supply their CallerKey explicitly in `Authorization` and use the default fetch credentials mode or `credentials: 'omit'`; `credentials: 'include'` is not supported. `Access-Control-Allow-Credentials` is never enabled. `Retry-After` is exposed to browser code.

A valid `OPTIONS` preflight needs no CallerKey and returns an empty `204` before maintenance, authentication or request admission. It does not call an upstream, consume caller limits, reserve credits or create a call log. It permits only the exact route's method, echoes validated requested header **names** (including Authorization, Content-Type and SDK metadata headers), and permits browser preflight caching for 600 seconds. It includes `Cache-Control: no-store` and `Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers`. Origin is not reflected; opaque browser origins such as `null` use the same wildcard permission.

Preflight Origin must be a single nonempty value of at most 2,048 bytes. Requested header names must form one comma-separated list of at most 4,096 bytes and 64 names, each at most 128 ASCII HTTP-token characters. Malformed preflight headers or query strings return `400 invalid_request`; unsupported methods return `405 method_not_allowed`. Ordinary OPTIONS without `Access-Control-Request-Method` retains the normal method rejection. Unknown, encoded or trailing-slash paths receive no CORS permission. Host validation still runs first, and all actual calls retain the usual CallerKey, authorization, routing and accounting checks.

Session, administrator and steward automation routes have no cross-origin permission. The cookie API's existing same-origin protection is unchanged. A failed browser preflight prevents the actual model request from being sent, so it cannot appear in user call logs.

### 1.2 Strict JSON, scalars, pagination, and replay

- Control-plane request objects reject unknown or duplicate fields, trailing JSON, invalid UTF-8, control characters, invalid enum case, and values outside the documented range. GET/HEAD requests reject bodies. Query parameters are closed, single-valued sets. OpenAI-compatible model requests retain bounded extension fields under the operation-specific rules below.
- Times are UTC Unix seconds as JSON numbers in `0..253402300799`, unless a section explicitly uses milliseconds. JSON booleans are true booleans. Omitted, `null`, empty, and zero are distinct.
- Credit amounts are canonical decimal strings: `0|[1-9][0-9]*` with an optional 1–3 decimal places. A leading `-` is permitted only for fields explicitly described as signed. Floating point is never used for accounting.
- Revisions, generations, sequences, exact counts, and values that may exceed the JSON safe-integer range are canonical decimal strings.
- New opaque IDs use a documented prefix plus 22 canonical raw-base64url characters. Important prefixes include `ann_`, `op_`, `req_`, `clm_`, `pol_`, `thu_`, `fb_`, `ll_`, `rpsq_`, `rps_`, `rpc_`, `rpt_`, `iss_`, `lgh_`, `sse_`, `gle_`, `dbs_`, `dbt_`, `dbe_`, `scn_`, and `mch_`.
- List endpoints retain the legacy `cursor` and `limit` mode and return `{data:[...],next_cursor:null|string}` unless a section states a different closed envelope. Numbered mode is selected by `page` or `page_size`; `page` is a canonical positive decimal from 1 through 2147483647 and `page_size` is 10, 20, 50, or 100, defaulting to 1 and 20. The modes are mutually exclusive, repeated/unknown values are rejected, and numbered responses add the route's `pagination` metadata with `next_cursor:null`; an excessive page clamps to the last page (empty results use page 1 of 1). Count, filters, decision time, and rows come from one authorized read snapshot. Cursors are authenticated, route/owner/filter-bound, expiring, and at most 512 bytes. A malformed, expired, or cross-scope cursor is `400 invalid_request`.
- State-changing routes require `Idempotency-Key` unless they are authentication/session/elevation/logout, CallerKey plaintext mutation, OpenAI-compatible model calls, an explicitly business-unique ACK/lease/check-in/tutorial mutation, alert set-state, the per-item steward binding automation in §6.3, the assistant operations in §7.8, or management-history cleanup in §7.9. Control replay keys contain 22–128 URL-safe ASCII characters. Same key and same canonical request replay the stored status/body; the same key with a different request is `409 conflict`. Replay records last 24 hours.
- Dynamic API responses and every error carry `Cache-Control: no-store`.

Shared numbered-page metadata is `{page:string,page_size:number,total_items:string,total_pages:string}`. `page`, `total_items`, and `total_pages` use exact canonical decimal strings; only `total_items` may be zero. Nested `attempt_pagination` and `materials_pagination` use the same shape. Credit history retains its separately documented top-level pagination envelope.

### 1.3 Error envelope

API clients receive the JSON envelope below. Browser document navigation that
explicitly prefers HTML receives a localized error page with safe recovery
links and the same HTTP status. This includes sign-in and OAuth callback
failures, session requirements and station navigation errors. Fetch/XHR,
non-document resources and JSON-preferred requests retain their existing
responses; `/v1` inference errors always remain machine-readable. Error pages
do not reflect OAuth codes, state, provider descriptions or callback query
strings. Both presentations retain `Cache-Control: no-store` and relevant
`Retry-After` or `Allow` headers.

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

Platform error messages follow `Accept-Language` (`zh` or `en`, including regional variants and quality weights); unsupported or missing preferences use English. Both stations send their selected language. Stable codes, source, HTTP status and retry semantics do not change. Upstream messages are sanitized but not translated. This applies to platform errors emitted inside an active SSE response as well as ordinary JSON errors.

Ordinary user display projections replace valid IPv4 address strings with `[redacted]`. Current administrators and level-6 stewards receive complete saved content through their authorized log, audit, diagnostic and history routes and existing exports, without display redaction or text truncation. Level-5 stewards with the corresponding read grants can read site-wide logs and diagnostics; without `logs.unredacted`, their upstream error bodies and error-text projections replace IPv4 addresses with `[redacted]`. Source IPs, endpoint addresses, other authorized fields and IPv6 remain unchanged. Collection limits, retention, role restrictions and safe JSON/CSV/text encoding remain. Editable endpoint addresses, IP-rule values and normal model output retain their original values.

Request logs additionally redact non-mainstream endpoint DNS identifiers. Ordinary user lists, details and JSON/CSV exports apply this to existing free-text display fields, including endpoint addresses, notes, model names and error text. Without `logs.unredacted`, level-5 site-wide logs apply it only to upstream diagnostic text and error bodies; their separately authorized endpoint/model/source fields remain unchanged. Administrators and stewards with `logs.unredacted` retain saved content. An exact connector type and complete base-URL match in the currently active, enabled mainstream channel registry exempts that attempt from DNS redaction, including older requests. Disabling, retiring or changing that channel removes the old address exemption. The service uses the saved attempt endpoint, never unrelated users’ endpoints.

The hostname and its non-generic DNS labels are matched without case distinctions and replaced with `[redacted]`. Letters or digits adjoining a label prevent a partial-word match; hyphens and underscores can delimit a match. Generic infrastructure/suffix labels are excluded from bare-label matching. This is a bounded DNS-identifier rule, not public-suffix or Unicode-alias inference. Existing IPv4 rules still apply to mainstream channels. Stored records and actual model responses are unchanged.

| HTTP | Stable code |
| ---: | --- |
| 400 | `invalid_request`, `content_too_short` |
| 401 | `unauthorized` |
| 403 | `forbidden`, `elevated_required`, `mfa_required`, `mfa_invalid_code`, `feature_disabled`, `insufficient_credits`, `charity_suspended`, `checkin_cap_reached` |
| 404 | `not_found` |
| 405 | `method_not_allowed` |
| 409 | `conflict`, `mfa_setup_expired`, `refresh_required`, `already_checked_in`, `debug_live_cancelled` |
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

The database remains Generation 2 (`application_id=0x4E425249`, `user_version=2`), with current schema 42. Normal startup initializes an absent database or opens the exact current structure. Older, newer, unknown or incomplete storage is rejected before migration, repair or recovery writes. A schema mismatch reports the required and readable actual version and the explicit upgrade entry point. The matching standalone `nonbiri-upgrade-v1` accepts exact registered schema 16–26 and 33–41 sources; a deployer must preserve a matching recovery set before deliberately invoking `--apply`. Services, startup scripts and deployment helpers must not apply it implicitly. Upgrades are one-way, without downgrade or reverse migration. Schema versions need not change with every software patch.

`maintenance schema-info --json` reads compiled metadata without loading configuration or opening a database. Its version-1 descriptor reports `schema_version=42`, `minimum_schema_version=42`, `startup_policy="exact"` and `upgrade_tool="nonbiri-upgrade-v1"`; `source_schema_versions`, `source_manifest_hashes` and `offline_conversions` are empty arrays. The standalone tool's `--schema-info --json` instead lists its supported conversions. On an exact current database, startup still validates structure, configuration and the key-check singleton, then recovers unfinished work. Credentials are decrypted when used; full historical and credential audits use offline `maintenance verify`. See [database upgrades](database-upgrades.md) for accepted sources and backup/recovery boundaries.

Current account export `schema_version=31` is independent of SQLite `user_version`; its filename is `nonbiriapi-account-export-v31.json`. See §9 for its safe projections. This contract describes the source tree, not an instance's release or deployment status.

### 1.5 Display time context

Ordinary user pages display and edit timestamps in the browser's local time zone. Administrator and steward pages use the configured fixed site offset; one notice per form or paired-time group identifies it and warns when it differs from the browser. Saved values and API fields remain UTC Unix seconds. An unchanged field preserves its original instant, including seconds not displayed by a minute-precision control.

`GET /admin/api/time-context` and `GET /api/steward/time-context` accept no query or body and return `{mode:"site",offset_minutes:480}`. The offset is an integer from -720 through 840 in 30-minute steps; `null` means the site time zone is not configured. The latter route requires a currently effective level-5 or level-6 steward session; administrators use the administrator host. Reads revalidate current session and role in the same transaction as the setting. No CallerKey permission or other configuration fields are added. Unavailable or unconfigured context disables time-dependent editing without silently substituting UTC or browser time.

The existing `GET /api/time-zones` and `GET /admin/api/time-zones` return the embedded zone registry. `GET {prefix}/time/resolve?local=YYYY-MM-DDTHH:mm:ss&time_zone=...` resolves an ordinary local wall clock through that registry, including daylight-saving gaps and repeats. Site-offset editing uses a fixed offset and has no daylight-saving ambiguity. Recurring quota time zones and explicitly defined activity business calendars retain their separate semantics.

## 2. OpenAI-compatible ingress

The [direct inference contract](direct-inference.md) lists all 13 connector types,
16 model operations, native HTTP/SSE formats, bounded Chat/Responses conversion,
System One, rerank and excluded persistent/realtime resources. The operation
registry is closed; this is not an arbitrary upstream path proxy.

### 2.1 `GET /v1/models`

Returns the caller's currently routable personal models plus currently available charity models in the OpenAI list envelope:

```json
{"object":"list","data":[{"id":"provider/model","object":"model","created":1788000000,"owned_by":"provider"}]}
```

The list is sorted by external model ID. It never contains physical endpoint, key, binding, donation, price, or donor data. A charity model appears only while its feature gate is open, the caller's current effective level is allowed, and at least one real candidate is currently usable.

`GET /v1/models/{model}` returns one exact currently routable platform model in
the same object shape, or 404 when unavailable. It retains the list's ownership
and visibility checks.

The charity portion is evaluated in one read-only snapshot using a single
decision time. Personal models remain owner-scoped. These are immediate reads
with `Cache-Control: no-store`, without a complete-list cache or TTL. Candidate
and model ceilings retain their existing errors; a failed query does not become
an empty list. See [database access](database-access.md).

### 2.2 `POST /v1/chat/completions`

The request is the OpenAI Chat Completions shape. Its body limit defaults to 10 MiB and is configured by administrators with `model_request_body_limit_mib` (integer 1–64 MiB). One MiB is 1,048,576 bytes; exactly the configured size is accepted, and one extra byte returns 413 `payload_too_large`. Each request keeps one configuration snapshot across parsing and forwarding; saved changes apply to new requests. Proxy limits can impose an earlier rejection. `model` is a required opaque platform model name (at most 133 Unicode runes). `stream` selects OpenAI-compatible SSE. `max_tokens` and `max_completion_tokens` may be absent or null; if both are integers they must be equal and in `1..2147483647`. For OpenAI and Anthropic, a caller-supplied safety identifier is replaced by the existing server-generated user-and-origin pseudonym. Gateway does not send that safety field; its separate optional cost-attribution tag is controlled only by the administrator and defaults off.

Admission order is fixed: CallerKey/account, one permit from the user's operation-class concurrency pool, global/user RPM, strict body/model policy, Debug interception, model/capability resolution, candidate selection, credential/egress checks, then dispatch. A refused concurrency permit creates no RPM hit, candidate access, reservation, or penalty. One logical request retains one permit and one 1200-second aggregate deadline across retries and streaming. Upstream response headers may take up to 900 seconds; receiving them does not end the remaining output budget. Embeddings, rerank, System One and Decisions use the light pool; all other model operations use the heavy pool. Each pool combines personal and charity calls. Charity RPM windows are also independent by class, while global RPM remains shared; see §7.1 for defaults and overrides.

Short charity requests return `400 content_too_short`; the minimum-length calculation is unchanged. The platform-owned message starts with `[NonbiriAPI]` and explains that the minimum input length protects shared resources and asks for a complete request, in English or Chinese according to `Accept-Language`; it does not echo input content. The server applies the current optional penalty, temporary ban, and charity suspension settings before any reservation or upstream attempt. Their rejection log and any credit penalty commit together; `X-Request-ID` identifies that log. Rejected requests count once with zero upstream tokens. The separate automatic RPM-ban window counts only charity requests denied by the site's per-user RPM limit. Personal resource calls, global RPM, concurrency limits, shared EndpointKey limits and upstream `429` responses do not contribute. Successfully intercepted Debug dry runs retain their zero-accounting and zero-log behavior.

On a per-user RPM denial, the server can attribute a charity violation only after decoding the reserved `[公益]` model namespace from a valid bounded request for a registered operation, including supported multipart requests. This read is limited to the configured model-request body maximum and two seconds, or the earlier request deadline; at most 16 such reads run at once, without queuing. An unreadable or malformed body, or exhausted classification capacity, keeps the rate-limit rejection without adding an automatic-ban event. This does not select a candidate, access a credential, reserve credit or send upstream; admitted requests retain the order above.

Personal models use `ordered`, `random`, or `cache_balanced` routing; the default remains `ordered`. Cache balancing first tries a currently eligible physical key associated with the same owner and personal model during the preceding 300 seconds. Otherwise it selects from the lowest recent physical-key load, using a uniform random candidate order for ties. Selection and pending-load reservation share the final claim transaction. Personal models do not apply donation-expiry weights or expose a configurable affinity lifetime. Request capability filtering happens before a credential is decrypted. Silent retry follows the existing pre-output boundary; heartbeat comments alone do not count as output. A clean EOF alone is never success: non-streaming requires a complete valid response, chat streaming requires its valid protocol terminator and `[DONE]`, and image streaming requires the completed-image events described in §2.5. Client disconnect cancels upstream work.

Personal and charity models expose `transport_rule`. Existing and new models default to `passthrough`; this field applies only to Chat Completions and leaves embeddings, image generation and Decisions unchanged. The caller's `stream` format remains authoritative. Caller mode, upstream mode and model policy are frozen before reservation and retained across retries.

| `transport_rule` | Upstream and caller behavior |
| --- | --- |
| `passthrough` | Upstream uses the caller's stream mode. |
| `force_non_stream` | Upstream returns JSON. An SSE caller receives 20-second idle comments while waiting, then standard completion chunks and termination. |
| `force_stream` | Upstream returns SSE. A JSON caller receives one completion only after valid stream termination. |

Conversion preserves representable tools, reasoning, refusal, finish reason and usage within the existing 8 MiB completion-JSON and 32 MiB cumulative-stream limits; invalid or oversized conversion fails explicitly. Nonstream callers receive real HTTP errors without an early 200 or whitespace keepalive. Forcing SSE upstream does not keep a JSON caller connection active: client or proxy idle timeouts may still end it before the result. Disconnect immediately cancels upstream. Buffering is separate from caller delivery and does not exempt upstream consumption from charity settlement.

`openai-compatible` appends `/chat/completions` to the configured versioned base. `anthropic-compatible` appends `/messages`, translates the strict supported subset, and sends only the required Anthropic key/version/content headers. The Anthropic subset supports system/developer and user/assistant text, HTTPS or bounded image data parts, OpenAI function tools and matched tool results, `temperature`, `top_p`, stop strings, tool choice, parallel-tool indication, and streaming usage. Lossy or unsupported fields make that candidate incompatible; they are never silently dropped. If neither token-limit field is supplied, Anthropic uses the nullable administrator default or the built-in 65,536 fallback. The fallback is not a cap on explicit values.

For OpenAI-compatible SSE, an invalid first string completion ID is replaced with one stable generated ID for that upstream attempt. A valid ID is preserved; non-string IDs and malformed protocol frames still fail. Later chunks of a normalized attempt use the same generated ID, including tool-flattened output. Exact-credential reflection checks run on the original frame before ID normalization; reasoning, tools, usage and terminal validation retain their existing behavior.

Native Chat retains its compatible valid-choice plus `[DONE]` completion rule; it does not newly require a nonempty finish reason on every candidate. This does not relax the terminal requirements of Responses, Anthropic, Gemini or Gateway. Forced-stream JSON aggregation decodes and rejoins valid audio data fragments, accumulates transcript text and retains the audio ID and expiry metadata within the existing response limits. It does not concatenate arbitrary metadata strings. Anthropic thinking and opaque continuation histories use the bounded [content-protocol extensions](content-protocols.md#anthropic-thinking-and-continuation); its optional empty-choices usage frame requires `stream_options.include_usage:true`.

Usage is normalized into uncached input, cache-write input, cache-read input, and output. OpenAI-compatible streams may send cumulative snapshots: fixed input buckets and nondecreasing output replace the previous value without being added together. Missing/null snapshots preserve the last credible value. Tool flattening emits only the last usage frame after finish and before `[DONE]`. Invalid, negative, regressing, ambiguous, or internally inconsistent individual buckets mark usage unknown rather than fabricating numbers. A redundant upstream total that disagrees with valid individual buckets does not invalidate those buckets: billing uses the buckets and privileged request logs record `usage_total_mismatch`. The marker remains set if any attempt or cumulative snapshot observed a mismatch; a later consistent snapshot cannot erase it. Literal and semantic response guards block reflection of the exact credential, including across bounded JSON/SSE fragments; this is defense in depth, not general data-loss prevention.

The OpenAI-only physical-key policy `force_store_false` overwrites/inserts top-level `store:false`; an upstream may ignore or reject it. The logical-model policy `flatten_tool_calls` converts bounded validated tool calls to text and restores only complete matched history. Both default off. Flattening supports OpenAI-compatible, Anthropic-compatible, Gateway v3/v4 and Responses bindings, including streaming and complete matched history. Gemini candidates reject flattening before admission because the text representation cannot preserve their tool signatures. It uses at most 32 tool calls, 64 KiB per argument and 256 KiB combined arguments; malformed, unmatched, truncated or ambiguous calls fail rather than fabricating text. It does not execute tools or enable unsupported legacy function protocols.

Charity names use the reserved `[公益]` prefix. Charity admission checks the feature and caller state and uses only approved, enabled, unexpired donation keys whose physical claim and catalog binding are still valid. After eligibility and Connector capability filtering, at most 100 candidates follow the model's `ordered`, `random`, `expiry_weighted`, or `cache_balanced` strategy. Ordered routing uses saved binding order; uniform random gives each eligible connection equal weight. Expiry weighting favors nearer expiry using the documented remaining-lifetime calculation. Cache balancing considers the physical key's recent dispatched count and a bounded same-user/model affinity, without promising an upstream cache hit. Random strategies use a CSPRNG rejection sampler. The attempt order is frozen without replacement before any logical-request, ledger, reservation, or claim write, and entropy failure is `503 service_unavailable` with zero writes. Caller credit is reserved before dispatch. Each attempt rechecks expiry/capacity but never redraws or adds candidates.

Charity resource identities remain private. A failed call may expose the sanitized upstream message, machine code and HTTP error status described above, but never the donation/key/base URL/upstream-model identity, private diagnostic, or retry count. A later candidate rejected before dispatch does not replace an earlier dispatched upstream failure. Successful-response detection, retries and settlement are unchanged by error reporting.

Charity credit is reserved before dispatch. Receiving upstream HTTP 200 stops retries and resets the key's failure streak, including when a later protocol, read or delivery error occurs. Those errors remain logged and sanitized for the caller; HTTP acceptance does not establish protocol completion.

A physical upstream stream becomes billable when its HTTP 200 headers arrive, including empty streams and subsequent errors. Nonstream requests become billable on a complete valid response. Known usage uses the frozen price and discount, even when the charge exceeds the caller reservation; settlement may leave a negative general-credit balance. Missing usage uses the frozen conservative reserve and discount, capped at the caller reservation, and earns no donor reward. Billable requests also consume donation call and token quotas. Before that boundary, failed attempts consume no such quota or caller credit; dispatches still count toward RPM. This applies to live diagnostics, tool conversion and buffered transport conversion, even if a JSON caller receives no final response. Disconnect cancels upstream immediately. A durable checkpoint records acceptance for recovery without storing response content.

For nonstream OpenAI-compatible chat only, the exact one-layer `{success:true,data:<valid completion>}` success wrapper is accepted and the inner standard completion is returned. False success, error/conflicting/duplicate fields, recursive wrappers and invalid inner responses fail. This does not add wrapper support to SSE, embeddings, Anthropic or Gateway. HTTP 200 alone does not validate a JSON response.

### 2.3 `POST /v1/embeddings`

Uses the same CallerKey authentication, host boundary, admission order, shared concurrency/RPM, retry deadline, donation eligibility, and error envelope as chat. No query, trailing slash, encoded path, compressed body, or streaming response is supported. This is the single entry point for personal and charity embeddings; charity names keep the reserved `[公益]` prefix.

The table below describes ingress validation and OpenAI-compatible forwarding. Gateway uses the stricter text-only subset in [Section 2.4](#24-native-ai-sdk-gateway-v3v4-compatibility): unsupported fields exclude that candidate before credential access or reservation, even when they are valid for another connector.

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

Chat, embedding and image-generation bodies share the configured model-request limit (default 10 MiB), with JSON depth at most 64, at most 1024 top-level fields and field names at most 256 Unicode runes. Duplicate keys, invalid UTF-8, trailing JSON and invalid standard fields are rejected. The platform does not tokenize text or infer a model-specific input limit. Rewritten OpenAI requests are bounded to the configured ingress limit for that request plus 16 KiB for server-generated fields.

An OpenAI-compatible connector appends `/embeddings` to the configured base: `https://provider.example/v1` becomes `https://provider.example/v1/embeddings`, while a bare host becomes `/embeddings`. It neither inserts `/v1` nor accepts a separate full-path override. The platform model must enable `embeddings` in `model_types`; `GET /v1/models` alone does not certify embedding support. A model may enable multiple operations. Candidate filtering excludes unsupported connector types, including Anthropic, before key access; the upstream decides whether its specific model supports embeddings.

The chat-only `transport_rule`, `force_store_false` and `flatten_tool_calls` policies do not alter embeddings. An explicitly supplied nonstandard `store` field is passed through. Embeddings are exempt from the charity minimum-content check and its penalty; all other identity, feature, level, expiry, rate, concurrency, credit and quota rules still apply, including charity per-user RPM violation attribution.

A successful OpenAI-compatible upstream response requires HTTP 200 and a complete valid non-streaming JSON list within 32 MiB, or the smaller shared egress limit. Its `data` contains exactly one embedding for every input, with unique complete indexes `0..N-1`. Data order and indexes are retained. Float vectors contain only finite numbers. Base64 vectors use standard padded encoding of nonempty little-endian IEEE754 float32 data with finite values. All vectors have the same nonzero dimension and must match an explicit `dimensions`. Invalid or partial vectors, duplicate fields, contradictory structure and successful HTTP error envelopes fail with `502 upstream`; timeouts use 504.

The public response contains only `object,data,model,usage?`; each data item contains only `object,index,embedding`. The model is the caller's platform model name. Private upstream names, diagnostics and unknown fields are discarded. OpenAI-compatible upstream usage is trustworthy only when `prompt_tokens` and `total_tokens` are equal JSON integers in `0..2147483647`. Explicit zero remains zero. Invalid or missing usage does not discard valid vectors, but the public response omits `usage` and accounting records unknown usage. Text length, Token ID count and vector dimension are never substituted for reported usage.

Personal calls incur no site credit charge. Charity per-request pricing charges one successful logical request, including a batch. Token pricing places all reported input tokens in the uncached-input bucket; cache and output buckets are zero, so vector dimensions incur no output-token charge. Unknown usage uses the accepted per-model or inherited global reserve and discount under the existing conservative settlement rule; per-request pricing keeps its fixed price. Unknown usage earns no donor reward. Donation call limits count a batch once; unknown Token usage follows its frozen quota reserve.

The service validates and sanitizes the full response, then durably records successful output before writing the caller body. A failed attempt without that success evidence consumes no charity price or donation call/token/credit quota; actual dispatches still count toward RPM. After the success marker, cancellation or a downstream write failure does not reverse the charge and cannot trigger retry. Recovery, account deletion and late completion retain the existing accounting rules.

Debug dry capture makes no upstream request, credit reservation or usage log. Live capture performs the real call and settlement while suppressing vectors from the caller; it returns the existing 422 captured result or 409 cancellation. Input may exist only in the bounded current Debug memory trace. Vectors are not saved in Debug, logs, account exports or the database.

<a id="24-native-ai-sdk-gateway-v3-compatibility"></a>

### 2.4 Native AI SDK Gateway v3/v4 compatibility

Select `ai-sdk-gateway-v3` or `ai-sdk-gateway-v4` explicitly. Configure the complete native base URL, for example `https://provider.example/native/v3/ai` or `https://provider.example/native/v4/ai`. The connector only appends the paths below; it never inserts or replaces a version path, probes a paid model, or retries another protocol version. Existing v3 configurations retain their version. The protocol mappings follow pinned [Gateway 3.0.219](https://github.com/vercel/ai/tree/9ef21a875714952fdcf10d8b4e1ced5144041108/packages/gateway) and [Gateway 4.0.121](https://github.com/vercel/ai/tree/e6230e43ba9def164550eea562c457e23be9efa6/packages/gateway) source; SDK package versions, URL paths and model specification versions are distinct.

Inference uses POST JSON, Bearer credentials, `ai-gateway-auth-method: api-key` and `ai-gateway-protocol-version: 0.0.1` in both versions. The selected connector supplies specification value `3` or `4`:

| Service | Native suffix | Specification header | Model header |
| --- | --- | --- | --- |
| Text | `/language-model` | `ai-language-model-specification-version` | `ai-language-model-id` |
| Image generation | `/image-model` | `ai-image-model-specification-version` | `ai-model-id` |
| Embeddings | `/embedding-model` | `ai-embedding-model-specification-version` | `ai-model-id` |
| Rerank | `/reranking-model` | `ai-reranking-model-specification-version` | `ai-model-id` |

Text also sends `ai-language-model-streaming:true|false`; model IDs and that streaming choice are carried by headers, not a native body `model` or `stream` field.

| Capability | Gateway v3/v4 boundary |
| --- | --- |
| Text | Multi-turn system/user/assistant text, JSON and incremental SSE; bounded inline Responses conversion through Chat, not native compact/input-token endpoints |
| Parameters | Output-budget aliases preserve the number; v3 uses verified model reasoning profiles, while v4 maps supported effort values to common `reasoning` |
| Prompt cache | Exact-target Anthropic root-native or explicit-end mapping, text/tool-definition markers and tool-result text arrays; optional 5m/1h lifetime |
| Tools | Function definitions, supported tool choices, streamed arguments and matched caller-supplied results; the server never executes tools |
| Chat image input | HTTP(S) URLs and bounded PNG/JPEG/WebP/GIF data URLs; omitted/auto detail only; no server image download. Supported native file data is a string in v3 and a tagged `url`/`data` object in v4 |
| Image generation | Common `prompt`, `n`, numeric `size`, integer `seed` (including zero) and supported `aspect_ratio`; output defaults to `b64_json`. Explicit URL output, `size:auto`, image streaming/partial images and unmapped provider fields such as `quality`/`style` reject before dispatch |
| Embeddings | Text/string-array input, float or locally encoded float32 little-endian base64 output; no token-ID input or caller identity field. Only v4 accepts common `dimensions` and checks returned dimensions; v3 rejects it |
| Rerank | Query, homogeneous text/object documents, one of `top_n`/`top_k` mapped to `topN`, and local `return_documents` recovery from unique valid indexes; preserves order and finite scores. Unmapped vendor fields reject |
| Discovery | GET `/config`; language, image, embedding and reranking entries with the configured specification version. Unknown/absent model types are skipped; manual entries remain available |

Gateway image output has no URL-hosting fallback. Embeddings use only valid `usage.tokens`; image output uses valid common input/output/total counts without inventing modality buckets. Standard Gateway rerank has no usage: metadata, cost and document counts do not become tokens or search units. Existing per-request pricing and conservative missing-usage settlement remain in force. Directory entries establish neither deployment availability nor support for every option. Gateway Decisions, audio, video and persistent-resource services are not added.

Role-policy output must fit the native system/user/assistant/tool roles. Named messages, unknown fields, undeclared `providerOptions`, parallel-tool flags and non-equivalent options exclude this connector before credentials or reservations. The proven neutral options `n:1`, `logprobs:false`, empty `logit_bias` and `response_format:{"type":"text"}` are accepted. Other compatible candidates remain eligible; if none remain the request fails explicitly. The physical-key OpenAI no-store setting remains OpenAI-only; Gateway uses explicit model-scoped storage policy. Existing model tool flattening and request adaptation remain available. Documented provider warnings are advisory. Malformed warnings, tool arguments, invalid event order, missing native finish, truncated streams and cancellation cannot produce successful completion. Rejected stream events have bounded private diagnostics available to administrators and level-5/6 stewards; level-5 error details redact IPv4 addresses and non-mainstream endpoint DNS identifiers. Missing or ambiguous usage remains unknown and uses the existing conservative accounting rules.

Output-budget aliases accept integers from 1 to 2147483647, with null treated as absent and equal dual values accepted. Conflicting values reject. In v3, explicit `reasoning_effort` requires an administrator-configured exact target capability. In v4, supported values map to common `reasoning`; a configured profile still restricts its allowed levels. Explicit `store` and `cache_control` retain their exact-target policy requirements in both versions; other unknown fields remain strict. Bounded Responses conversion requires `store:false` and an explicit target storage policy that accepts it (`openai` or the deliberately lossy `omit_false`); it does not enable stored resources or arbitrary Responses parameters. See [Gateway model controls](gateway-model-controls.md) for provider profiles, native mappings, configuration and storage limitations.

Gateway declarations are stored in the database and managed at `/admin/api/gateway-model-capabilities`. Each request reads one exact-target snapshot before preflight and reservation; retries retain it. Ordinary and flattening models can share a target. Existing environment declarations import once during upgrade. Subsequent restarts retain database edits and deletions.

With exact-target `cache:"anthropic"`, root `cache_control` remains native `providerOptions.anthropic.cacheControl`. With `cache:"anthropic_explicit"`, it lowers onto the final eligible prompt block, including a tool result. Text and tool-definition markers retain their positions. `type` must be `ephemeral`, with optional `ttl:"5m"|"1h"`; omitted TTL is 5m. An identical final marker merges; conflicting TTLs, more than four effective points, an unrepresentable position or a 1h point after a 5m point in the effective provider order reject before dispatch. Any caller marker overrides an automatic adaptation default. Tool messages accept arrays of text blocks; only a marker on the final block can map faithfully to the native tool-result position. Intermediate markers reject rather than move. Cache usage exposes `cached_tokens` and `cache_write_tokens` inside `prompt_tokens_details`, retaining `cache_creation_tokens` as an alias. All counts come from reported upstream usage.

Accepted streaming Chat Completions send an SSE comment after 20 idle seconds, including upstream-header waits and pre-output retries. These comments are transport activity only. A final failure after HTTP starts ends with one SSE error; cancellation and the request deadline stop the upstream and keepalive worker. Non-streaming responses remain JSON, including when upstream SSE is forced; they receive no early success status or keepalive body. Gateway accepts valid protocol-confirmed empty content-filter/refusal termination in both caller modes while retaining actual usage. Ordinary empty or incomplete responses remain invalid.

Administrator setting `gateway_user_attribution_enabled` defaults to `false` (the API uses JSON booleans; storage uses `0|1`). Every subsequent Gateway v3/v4 text, image, embedding or rerank dispatch reads the current value; a dispatched body is immutable. When enabled, only the server supplies `providerOptions.gateway.user`, a keyed pseudonym scoped to the user and final canonical gateway origin. It permits correlation for **cost attribution**, not a claim that a safety identifier was applied. When disabled, the field is absent. Caller input cannot override it or supply gateway credentials, fallback models or provider-routing options. Debug adds optional boolean `gateway_user_attribution_sent`; no label value is exposed. Existing OpenAI/Anthropic identity behavior is unchanged.

In Chat output, native reasoning text is returned separately as `reasoning_content` in the assistant message or stream delta; it is never merged into answer text. Assistant histories accept this field as native reasoning parts. Bounded Responses conversion emits actual text in reasoning items with empty summaries and accepts those generated items on replay, alongside generated output text and refusal parts. Reasoning or signed replies retain ordered text/reasoning/tool-call parts in `extra_content.gateway.content`; clients must return the extension together with the unchanged visible message. JSON emits it on the message, SSE once on the final delta, and Responses attaches it to the first generated output item. Complete histories are checked against visible content, reasoning and tool-call arguments before native-order replay.

The closed per-part `providerMetadata` mapping is `google|googleVertex|vertex.thoughtSignature`, `anthropic.signature|redactedData`, and `openai|azure.itemId|reasoningEncryptedContent`. Replay changes the container to native `providerOptions` and retains those exact provider keys. Other provider options, credentials and routing controls are rejected in incoming continuation extensions. Each opaque string is bounded to 1 MiB and the extension to 4,096 parts within the complete request/response limits. Missing metadata cannot be reconstructed from readable reasoning; untagged text replay alone does not guarantee lossless continuation on a provider requiring signatures. Generated opaque source bindings and their routing constraints are described in [continuation histories](content-protocols.md#continuation-source-and-history-integrity).

Streams require valid native finish and closed reasoning/text/tool blocks, independently of advertised content type. Nonsemantic `raw` events are ignored, and `[DONE]` is accepted only after valid native finish. Neither substitutes for completion. Text/rerank JSON is limited to 8 MiB, embeddings to 32 MiB, image JSON to 64 MiB and text streams to 32 MiB, or the smaller shared egress limit. Unknown semantic output, unsupported files/custom parts/reasoning files and nonempty citations cannot be discarded to report success; null/empty citations are accepted. Gateway embedding usage accepts nonnegative signed-64-bit token counts; absent usage remains unknown.

Earlier v3 checks of Runable with SillyTavern 1.19.0 verified for non-streaming and streaming chat, a function-tool round trip and image input. Those checks returned language and embedding discovery entries; the older implementation skipped other model types. The tested Runable native `/embedding-model` endpoint returned HTTP 404; embedding translation and accounting have passed simulated HTTP tests, but successful Runable embedding execution has not been demonstrated. These historical checks do not verify the new v4, image or rerank paths, or establish availability on other Gateway deployments. Model catalog prices are separate from Runable credits; a zero catalog price does not establish a free request.

Catalog prices are upstream metadata and do not certify actual gateway credits or model capability. Provider-specific validation and exceptions are recorded in the release notes; a model-list entry does not certify every operation.

### 2.5 `POST /v1/images/generations`

Uses the same CallerKey, exact-path, CORS, admission, cancellation, retry and accounting boundaries as chat. OpenAI family endpoints with the `images_generations` service authorized append `/images/generations` to the configured versioned base URL; Gateway uses its bounded image subset and `/image-model` path described above. The platform model must enable `images_generations`. Unsupported model types reject before request acceptance or key claims with `400 invalid_request` and “model does not support this request type”; this does not increase key failures.

Requests require the platform `model` and a nonempty string `prompt`. `n` accepts 1–10 (default 1), `partial_images` 0–3, `output_compression` 0–100, and `stream` a boolean (default false); optional null values use defaults. String parameters such as `size`, `quality`, `background`, `output_format`, `moderation`, `response_format` and `style` are forwarded for the upstream model to validate. Other bounded JSON fields are retained; the server substitutes the bound model and its scoped caller pseudonym in `user` and any supplied `safety_identifier`. Chat role/transport/flattening/store policies and the charity text-length penalty do not apply.

Non-streaming success requires HTTP 200, a valid `created` timestamp and exactly `n` images in `data`, each containing a nonempty valid `b64_json` or HTTP(S) `url`. Standard output fields and `revised_prompt` are retained; vendor metadata is omitted. The platform does not fetch URLs or decode images into a disk cache. `stream:true` accepts standard `image_generation.partial_image` and `image_generation.completed` SSE events. Exactly `n` valid completed events establish protocol success; the caller receives each image event and its reported usage. Fewer completed images, clean EOF, `[DONE]`, malformed output and partial-only streams do not establish success.

Image JSON responses, cumulative SSE input, individual SSE lines and events are bounded to **64 MiB**, or the smaller shared egress limit. The shared default is 64 MiB; existing chat and embedding caps remain unchanged. Streaming uses a one-event queue and a 64 KiB read buffer. Large responses still require bounded transient validation and projection buffers; administrators should size shared concurrency for available memory.

Valid `usage.input_tokens` and `usage.output_tokens` map to uncached input and output accounting. Multi-image streams sum the usage of all completed images with checked arithmetic; a missing/invalid usage report or overflow makes the whole request usage unknown. Missing/invalid usage uses the existing conservative settlement rule. Charity pricing remains the model's configured per-request or token pricing: an `n>1` request consumes one call, not one call per image. An upstream streaming HTTP 200 consumes reserved quota and resets the failure streak even if a later event fails; nonstream charging still requires a valid response. Invalid HTTP 200 image bodies are not saved as raw diagnostics; explicit upstream errors retain the existing restricted diagnostic exception. Debug supports both image caller modes with the same owner-only bounded RAM request capture and safe outcomes, without storing generated images.

### 2.6 Translated Chat connectors and `POST /v1/decisions`

`google-gemini` serves `/v1/chat/completions` through GenerateContent with API-key header authentication. For OpenAI family types (`openai-compatible`, `openai-chat-completions`, `openai-responses`, `openai-decisions`), the effective authorized `native_services` selection determines the wire. Matching native Chat/Responses services are preferred, followed by the supported bounded conversion. Compact/input-token counting, embeddings, images, Decisions and other direct services require their own selected authorization; legacy omission retains each type's old subset. Type names alone do not disable an explicitly authorized service. The platform model must separately enable the corresponding operation, and the upstream must support it. No upstream failure triggers protocol probing. Their text, image, function-tool, reasoning continuation and streaming subsets are specified in [content protocols](content-protocols.md) and [direct inference](direct-inference.md#chat-and-responses-conversion). Unsupported fields reject the candidate before dispatch. Continuation fields retain their protocol and permitted source and survive both forced transport modes.

`POST /v1/decisions` uses an authorized native Decisions service with an independent JSON request and response. Required fields are `model,input,questions`; 1–400 predicate/choice/score questions are supported, with 2–10 levels for scores. Question instructions accept a string or nonempty text/image/JSON content array. Shared JSON input is limited to user messages. Each `input_json.data` is an object or object array with at most 32 container levels, 10,000 values and 256 KiB of compact UTF-8 JSON; nested JSON types and numbers are preserved. Shared and question images count together toward the 128-image limit. Optional cache fields and safety identity follow the native contract; question content and JSON parts cannot carry cache breakpoints. `stream:false` is accepted and stripped locally; true/null and unknown fields are rejected. The response validates every positional answer against its question and exposes the platform model. Refusals are valid answers. Enable model type `decisions` and authorize that native service on the OpenAI family endpoint, credential and binding. Chat, Gemini and Responses wire protocols do not convert this operation.

Decisions uses the existing CallerKey, ownership, admission, original-input client-rule auditing, shared egress, quotas, pricing, reservation, settlement and account lifecycle. One request is one logical call regardless of question count; no per-question billing unit is introduced. Chat length penalties, transport conversion and tool flattening do not apply. Debug dry mode returns the existing 422 captured result without traffic or accounting. Route kinds are `openai_decisions` and `charity_decisions`. See [the complete compatibility contract](content-protocols.md#decisions) for input variants, answer checks, usage and failure semantics.

## 3. User identity, account, resources, and logs

All routes in this section require a user session unless marked anonymous.

Request-log and Debug model-call `route_kind` values use `openai_` or `charity_` followed by one of the 16 operation names in §3.4, including `responses_compact`, `responses_input_tokens`, `rerank` and `systemone`. Discovery retains `model_discovery`. Debug accepts registered model-call kinds only. Log clients using a closed enum must handle the complete set. Existing model/status/time filters, pagination, role-specific projections and exports include every registered operation. Personal and charity records retain their corresponding privacy boundary; these ordinary request logs do not store prompts, vectors, uploaded files or generated media. The separate administrator-only assistant request history in §7.8 deliberately retains complete model input and responses.

Request and attempt `usage` may add optional `search_units`, `modalities` and
`pricing`. Search units are decimal strings with at most six fractional digits
and remain available independently of `usage_unknown` for tokens. Modality
counts are decimal strings: `input` has `uncached_input,cache_write_input,
cache_read_input`, each with `text,image,audio,video`; `output` has those four
modality fields. Invalid or missing sides are omitted with a bounded
`input_fallback`/`output_fallback`. `pricing` has `mode` and, for token pricing,
`input_mode,output_mode` (`aggregate|modality`) plus optional side fallbacks.
The closed reasons and rounding rules are in [usage pricing](charity-context-pricing.md).
CSV exports add spreadsheet-safe `search_units,modalities,pricing` columns.
Older rows can omit these fields; absence is not a reported zero. Ordinary
user logs show caller pricing only, never donor reward decisions.

Request logs and JSON/CSV exports include nullable `requested_streaming` (the caller's validated original mode before upstream overrides) and nullable `automatic_actions`. Actions are `short_content_deduction`, `short_content_ban`, `short_content_suspension`, `rpm_ban`, `client_rule_ban` and `context_penalty`. The list records effects in the same transaction as their application; a matched rule or ordinary rejection alone is not a punishment. An empty array means no recorded automatic action; null means historical information is unavailable. Missing or invalid request bodies can leave the mode null. These safe facts share existing log access, export, deletion and retention rules and reveal no rule definitions or request body.

Request-log list/detail and JSON exports may include safe `rejection_detail:{field,reason,suggestion?}` for a recorded pre-handler refusal or a platform resource refusal before any upstream attempt. Administrators, authorized stewards and the ordinary caller viewing **their own** log receive the same server-authored check and corrective suggestion, subject to existing display privacy. For example:

```json
{
  "rejection_detail": {
    "field": "stream",
    "reason": "expected a boolean or null",
    "suggestion": "Use a supported streaming value and valid stream options."
  }
}
```

These fields describe validated field names, type/format/capability requirements and bounded length facts; they never echo submitted values, request bodies, credentials or raw upstream diagnostics. Typed protocol-conversion refusals retain their actual safe field/reason instead of being flattened to a generic `invalid_request`. CSV exports append `rejection_field,rejection_detail,rejection_suggestion`; the CSV `rejection_detail` cell contains the reason text. Existing `rejection_stage,rejection_reason,caller_error_code` remain stable and separate.

API error messages include the available safe field, reason and next step, localized for a supported `Accept-Language`. A model with no usable resource for the requested operation returns `503 unbound_model`; a healthy resource for an unrelated operation does not make the request invalid. Temporary shared capacity limits retain `429 rate_limited`. Healthy resources that cannot represent the requested features still return a specific `400 invalid_request`. These refusals do not dispatch an upstream request or charge for one.

An older log without a recorded cause omits `rejection_detail` and leaves these CSV cells empty. Clients should say that the detailed cause was not recorded, never infer one from the route, streaming flag or error code. Generic validation fallbacks state that the specific cause was not recorded and suggest sharing the request ID for investigation. These safe diagnostics follow ordinary request-log ownership, retention and export rules and do not open the restricted raw-error/header routes in §11.

Personal requests reserve no general credits and remain available with a negative general-credit balance. Their zero-value accounting lifecycle and resource limits still apply; charity admission retains its balance requirements.

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
| `POST /api/account/export` | Fresh elevation; bounded JSON attachment. The export schema version is independent of the database version. |
| `POST /api/account/delete` | Fresh elevation and confirmation; synchronous coordinated deletion; 204. |

`balance` always means general credits; `game_balance` is the separate signed game-credit wallet. Both use exact decimal strings. New accounts start both at zero. API calls, donations and Thursday use general credits. Administrators can adjust either wallet, including below zero; cumulative donation credit remains nonnegative and is a separate lifetime statistic.

`UserEnvelope` contains safe identity/profile, raw and effective resource limits, balances, resolved level/display name, language, suspension/ban state and independent game/charity public preferences. `automatic_restrictions` is an array of safe `{kind,reason_code,reason,started_at,ends_at}` summaries, without thresholds, counters or evidence. It contains no manual-level provenance, credential, session token, or internal ledger encoding.

### Request failure observations

Request detail separates `request.caller_result_class,caller_status,caller_error_code`
(the final recorded result) from optional
`delivery:{http_status?,bytes,failed,failure?}`, which describes actual writes to
the immediate client connection. HTTP 200 may already have been sent before a
later failure. An absent delivery fact remains unknown; a successful proxy write
does not prove that the final client consumed the response.

Failed attempts may include `error_diagnostics` with `category,phase,code,message`,
bounded `causes` and `secondary` observations, timing and protocol facts.
Optional `started_at_ms,failed_at_ms,completed_at_ms,first_body_at_ms` use Unix
milliseconds; budget and elapsed values are durations. Observed upstream status,
headers received, first-body time, received byte/event counts, forwarded bytes,
body EOF, protocol completion and valid-usage presence are separate facts.
Optional media/encoding, retry-after seconds, budget/limit values and phase
timings are included only when recorded. The first captured cause is retained;
later distinct failures do not replace it.

These snapshots are limited to 16 KiB, with at most eight causes and 512 bytes
per safe description. Correlation entries contain `source,name,value`; permitted
names are `x-request-id,request-id,x-correlation-id,traceparent,cf-ray`, with at
most eight entries and 256 bytes per value. They are observations from the named
source, not credentials or proof of end-to-end completion. URL query/user-info,
secrets, raw error strings, stack traces, SQL and local paths are not added.

Administrator/steward request-log lists and their existing exports accept exact
`error_category` and `error_phase` filters for retained attempt diagnostics.
Full structured facts load with detail, not every list row. Owner details keep
their existing safe projection; charity source identities remain private.
Assistant request details and independent image/discovery diagnostics use the
same optional `error_diagnostics` shape under their existing access rules.
Missing older evidence is not inferred from current configuration.

Model discovery HTTP 202 means the refresh task was accepted. Its detail opens
the service-call evidence; absence of a confirmed HTTP response is stated
explicitly. A received 200 still requires successful catalog parsing.

Structured observations retain existing ownership, redaction, retention and
cleanup rules. They do not grant access to restricted raw bodies/headers in
§11 or add diagnostics to personal account export 31. That export retains only
its existing safe request summaries. Diagnostics do not change retries,
settlement, penalties or protocol completion.

### Temporary denied-sign-in reasons

After verified Discord OAuth refuses sign-in for an active ban or blacklist, `GET /api/auth/access-denied-reasons` uses a separate HttpOnly read-only cookie valid for five minutes. It creates no session or CallerKey authority. With no body and only an optional opaque `cursor`, it returns `{restricted,items,next_cursor?}` containing current reasons and applicable times, at most 20 items and 128 KiB per page. A lifted or expired restriction no longer appears. It omits operator identity and internal audit IDs; automatic reasons are localized from typed metadata, while manual and historical original text remains unchanged. Another Discord identity cannot use this grant.

### 3.1 Endpoints and keys

| Method and path | Request / response |
| --- | --- |
| `GET /api/endpoint-create-options` | `{base_connector_types,native_service_options,mainstream_channels}`; active, enabled channels and their current presets. Optional `match_base_url` adds `matching_mainstream_entries`. |
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

Connector types include `openai-compatible`, `anthropic-compatible`, `ai-sdk-gateway-v3`, `ai-sdk-gateway-v4`, `google-gemini`, `openai-responses` and `openai-decisions`. The same URL may be registered separately for different protocols, including mainstream channels. Protocol and encrypted key context remain separate; no URL-based protocol guessing is performed. See [content protocols](content-protocols.md) for operation and discovery limits.

`Endpoint` includes `id,connector_type,base_url,note,enabled,revision,key_count,origin,created_at,updated_at`. `origin` is `{kind:"custom"}` or `{kind:"mainstream",channel_id,name}`. Channel category remains administrator-only; creation options expose the current channel `revision` for an explicit preset selection. A mainstream create re-reads the active enabled channel and copies its immutable snapshot in the final transaction; later channel edits never rewrite an endpoint. Custom creation retains its custom origin.

The optional creation-options query accepts one `match_base_url` of at most 4096 bytes. Unknown or repeated parameters, request bodies, userinfo, query strings and fragments return `400 invalid_request`. Both the supplied address and registered entries use the outbound boundary's pure URL normalizer; matching compares scheme, canonical host, effective port and complete path without DNS or upstream requests. Only enabled entries in the returned active, enabled channels can match. No parameter preserves the ordinary response; a supplied parameter adds `matching_mainstream_entries:[{channel_id,entry_id}]`, including `[]` for no matches. The input address is not included in this projection. The response retains owner-session authentication and `Cache-Control: no-store`; the administrator creation-options route does not accept this query.

The custom-entry form shows all matching presets' existing details after a bounded debounce. Suggestions neither change its draft nor submit a resource. Explicit adoption selects only that channel entry and the intersection of its services with the user's selection; an empty intersection requires a new choice. The normal mainstream create includes `channel_entry_id`, the current `channel_revision` and the selected `native_services`, with the usual idempotency/revision checks. A conflict refreshes the options before a new intent; an unavailable suggestion query still permits custom creation. Adoption does not attach credentials or approve a donation.

`EndpointKey` exposes display fragments, note, enabled state, `force_store_false`, safe suspension state, revision, and times; never plaintext/ciphertext/fingerprint. Discovery evidence is the closed `unknown|checking|succeeded|failed` union with a safe failure class. Successful empty discovery is explicit and distinct from failure.

OpenAI-compatible discovery validates every original entry before merging duplicate normalized model IDs. The first entry's provider metadata, including an empty value, and response order are retained; a later different provider does not cause a conflict. Existing surrounding-whitespace normalization applies. Limits remain 1 MiB per response, 1,000 original entries, 512 characters per model ID and 128 per provider. Invalid fields or credential reflections in a duplicate entry still reject the entire response.

Manual catalog `provider` remains the wire/storage name for optional display metadata. The user-station form labels it **Note (optional)**. It does not contribute to the exact upstream model ID or binding identity; personal/charity model naming uses a separate provider field.

`EndpointKey` also returns numeric `max_concurrency` and `max_rpm`. Both accept whole numbers from 0 to 2147483647; 0 disables that key's additional limit. Omitted creation values default to 0; omitted patch values remain unchanged. Null, strings, negative numbers, fractions and out-of-range values are rejected. The owner can edit them using the existing revision and idempotency contract. Authorized donation managers can edit the same shared values under the dual-revision contract below. Existing site and endpoint safeguards still apply. Numbered endpoint, key, and model rows add the corresponding `browse` summary; legacy cursor rows and single-item/mutation responses keep the existing resource shape. Endpoint/key `q` filters are single-value literal text searches of at most 128 Unicode code points (and no more than 512 UTF-8 bytes), with controls rejected.

All personal, charity and live diagnostic model calls using the same EndpointKey share these limits across bindings. Discovery requests use their separate safeguards. Concurrency includes admitted work until its attempt finishes or is canceled, including the full streaming lifetime. RPM uses a rolling 60-second window with server-second precision. Undispatched claims reserve a slot until dispatch or release; a released undispatched attempt returns the slot. A dispatch consumes one slot even on failure, and each retry counts separately. Pending work cannot age out of its reservation. Lowering limits affects new admissions without canceling already admitted work. Recent dispatch counts survive process restarts.

A limited key is skipped without contacting the upstream, charging for that attempt, or recording an upstream failure. Selection continues in the configured order even when silent retry is off. If no attempt dispatched and at least one candidate was key-limited, exhaustion returns HTTP 429 `rate_limited` and releases the request reservation. If an earlier attempt dispatched, its actual result and ordinary settlement remain authoritative.

Endpoint owners may configure a separate `GET/PUT /api/endpoints/{id}/request-adaptation` resource. A GET returns a `revision` and five partitions: `forward_headers`, `fixed_headers`, `body_defaults`, `body_forced`, and `native_extension_paths`. The list partitions return `{mode,values}`; map partitions return `{mode,values:{name_or_pointer:{has_value,value?,mask?,sensitive_paths?}}}`. Fixed header and body values remain encrypted at rest. Authorized configuration GETs return a saved `value` only when that item has no response-protection markers, preserving its original JSON type, including `null`, `false`, zero, empty strings, arrays and objects. A whole-value or leaf-path marker omits the entire item's `value`, including unmarked siblings, while retaining presence, mask and marker metadata. PUT receipts, their persistent idempotency records and replays, and safe account exports omit these values; read the configuration GET after saving. A PUT requires `Idempotency-Key` and a body containing canonical string `expected_revision` plus at least one partition. Omitted partitions remain unchanged; explicit `null` is invalid. For example, `{"expected_revision":"0","forward_headers":{"mode":"replace","values":["X-Client-Tag"]}}` opts in to forwarding that one client header. Map edits use `{"action":"keep"}`, `{"action":"clear"}`, or `{"action":"replace","value":...}`; a displayed mask is never a replacement value. Endpoint sections use `replace`; a charity binding can use `inherit` or replace its model's entire partition.

Map edits may additionally set `sensitive_paths` to protect explicitly selected secret strings from response reflection. `[""]` selects the configured string itself (the only choice for fixed headers); body values may use relative JSON Pointers such as `["/credentials/0/token"]` to select nonempty string leaves in an object or array. Numbers, booleans, null and whole objects cannot be marked. Omitting the field preserves an existing value's markers for both `keep` and `replace`; `[]` clears them. `clear` removes the value and its markers and must omit this field. Partition inheritance/replacement carries the markers with its values. Existing configurations stay unmarked. Each stored and effective configuration supports at most 16 selected strings totaling 16 KiB; existing configuration/header/body limits still apply.

Only marked values applied to the request and present in the final upstream headers or JSON string values enter the existing response guards. Unused defaults, overwritten values and fields discarded during protocol conversion do not add detectors. Header secrets use HTTP whitespace normalization. Ordinary JSON and streaming responses share this protection across all supported connector operations. Detection stops the response; it does not add stream buffering, so previously emitted fragments cannot be recalled. The guard detects literal and decoded string reflection, not arbitrary transformations such as encryption or custom encodings. Existing restricted raw upstream-error diagnostics retain their authorized original-byte behavior. Markers are returned as metadata and exported with the owner's configuration structure; secret values remain excluded. Storage uses the existing encrypted document, with no schema migration.

Personal model owners use `GET/PUT /api/models/{id}/request-adaptation` with the same configuration-read rules, revision, idempotency and size limits. Its five partitions default to `inherit` from the endpoint selected for each attempt. A `replace` partition replaces that entire endpoint section; empty replacement disables it. Optional `excluded_request_fields` is an array of up to 32 top-level parameter names (64 ASCII characters each), case-sensitive; omission preserves and an empty array clears. Exclusions are pinned to the owner model before optional field validation and stay fixed across retries. Required routing, input and response protocol fields cannot be excluded. Exclusions conflicting with this model's body rules reject on save; conflicts with inherited endpoint rules reject before request acceptance. These personal settings never flow into charity calls.

In the editor, header names have no slash or colon: `User-Agent` with plain-text value `MyClient/1.0`. Body paths start with a slash: `/temperature` with JSON value `0.7`, or `/reasoning_effort` with JSON string `"low"`. Nested paths use `/thinking/budget_tokens`. Defaults fill absent fields; forced values replace them. Exclusion names have no slash. Declared native paths allow supported custom fields through Anthropic/Gateway conversion. Abuse checks always inspect the original incoming headers before any replacement.

The complete configuration is limited to 64 KiB and depth 16, at most 64 header names and 128 body paths. Each header name is at most 128 bytes, each value 4,096 bytes, added headers 16 KiB in total, and each JSON Pointer at most 512 bytes. Protected protocol/authentication headers and server-owned model, safety, stream, usage, tool structure, routing, accounting and credential fields cannot be overridden. Header matching is case-insensitive; an unlisted client header is never forwarded. Defaults fill only absent object fields, forced values replace their leaves, and the resulting request is validated again before dispatch. Unsupported native fields require an explicit declared path for Anthropic-compatible or Gateway v3 conversion; no adaptation expands picture-book or discovery request bodies. A 409 means the revision or effective rules conflict; refetch before retrying with a new body. The same key and identical body replay under the existing control-plane rules.

Authorized donation-key projections include numeric `max_concurrency,max_rpm` and decimal-string `key_limits_revision`; all three are null after the physical key is gone. Their shared binding menu/candidate/binding `source` includes the current integer limits. Management PATCH accepts either or both limits, as integers 0–2147483647, plus required `expected_key_limits_revision` whenever either is present and the ordinary donation `expected_revision`. Null is invalid for these new fields. Omitted limits stay unchanged; 0 adds no per-key limit. The final transaction compares both donation and physical-key revisions, updates the shared `endpoint_key_limits`, advances both revisions and records safe old/new limit values in the donation audit. An owner edit to the physical key can therefore produce a 409; refetch both revisions. Suspended physical resources return `resource_locked`. Administrators and stewards with `donations.manage_keys` may use this capability; the whole-donation scope in §6.4 applies. Standard, enhanced and full scopes permit direct maintenance of qualifying donations; an optional model selector cannot expand source authority. The shared physical limits affect personal and charity calls using the key. The mutation grants no secret, endpoint or private-note access. Ordinary charity callers do not receive underlying key configuration. Owner resource exports include the values; key deletion cascades to its configuration. Historical idempotent responses may omit these additive fields; an authoritative read returns current values.

### 3.2 Personal models and bindings

| Method and path | Request / response |
| --- | --- |
| `GET /api/models` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode accepts `q,provider,route_strategy,connection_state`; response includes `pagination` and the owner models. |
| `POST /api/models` | Provider/model, strategy, retry, flatten, role, `transport_rule` and optional `session_headers`; returns 201 model. |
| `GET /api/models/{id}` | One owner model. |
| `GET/PUT /api/models/{id}/request-adaptation` | Owner-only encrypted request settings, with independent revision and idempotency. |
| `PATCH /api/models/{id}` | Partial business fields plus `expected_revision`. |
| `DELETE /api/models/{id}` | `{expected_revision}`; 204. |
| `GET /api/models/{id}/binding-candidates` | `endpoint_id,key_id,source=automatic|manual,q` plus legacy `cursor,limit` or numbered `page,page_size`; safe candidate page, with numbered `pagination`. |
| `GET /api/models/{id}/bindings` | Complete bindings plus `binding_revision`. |
| `POST /api/models/{id}/bindings/batch` | Expected binding revision and selections; all-or-nothing 201. |
| `PUT /api/models/{id}/bindings/order` | Exact current ID permutation and expected binding revision. |
| `DELETE /api/models/{id}/bindings/{bId}` | Expected binding revision; returns the complete new binding set. |

Personal-model create/PATCH/detail DTOs include `session_headers`, using the same fields and defaults as charity models. Owners configure only their own models; charity writes require administrator or the applicable `charity.create`/`charity.model_settings` grant. Header configuration is saved with model settings; request adaptation has its own save and revision. PATCH omission preserves the array, `[]` clears it and null rejects. Generated values never appear in these DTOs. See [managed headers](charity-session-headers.md) for zero idle lifetime, all five formats and export boundaries.

Personal and management charity model DTOs include `cloudflare_proxy_enabled`
and the read-only `cloudflare_proxy_mode` (`worker|legacy|direct`). The boolean
defaults to true for personal models and false for charity models, including
existing records on upgrade. Create omission uses that default; patch omission
preserves the saved setting. Explicit null or a non-boolean is invalid. Personal
owners can edit their own setting through existing authorized model routes;
charity writes require administrator or the applicable `charity.create`/
`charity.model_settings` grant. Scoped reads do not themselves authorize changes. Existing body budgets, CSRF, revisions
and idempotency apply. Public charity capability/catalog DTOs omit both fields.
Idempotent model responses retain the original operation's saved boolean and
revision, while recomputing the deployment mode from the running process. Older
receipts without the boolean use the same personal/charity defaults, including
models nested in manual-catalog update responses; stored receipts are unchanged.

The mode is the actual process configuration, independent of the model switch:
an enabled model uses the generic Worker first, otherwise a matching legacy
mapping, otherwise direct egress. A disabled model always uses direct egress.
Requests capture the boolean at admission and do not change route mid-request.
Proxy failures never retry directly or increase donated-key failure counts.
Mode is a configuration fact, not a network
health test. See [Cloudflare forwarding](cloudflare-egress.md) for the loaded
configuration API, deployment and discovery behavior.

Personal model DTOs include `transport_rule`. POST omission defaults to `passthrough`; PATCH omission preserves the saved rule. Explicit values are `passthrough|force_non_stream|force_stream`; null, empty and unknown values return 400. Existing revision/idempotency requirements apply. See §2.2 for behavior and timeout limits.

New personal models default to `route_strategy:cache_balanced` when omitted. Explicit `ordered|random|cache_balanced` values remain supported; existing models and PATCH omission preserve their saved strategy.

Numbered endpoint responses include `browse.mainstream_categories`, a unique array containing `subscription` and/or `api_platform`. It matches the endpoint's connector type and canonical base URL against current active, enabled mainstream channels. An unmatched endpoint has an empty array. Channel edits, disablement and retirement are reflected on the next read; the endpoint's creation `origin` remains unchanged.

Provider/model parts are bounded opaque strings and form the external `provider/model` name. `[公益]` is reserved. Binding DTOs contain only owner-safe endpoint/key display data, Connector type, upstream model ID, and order.

Deleting a binding compacts the remaining order positions. An order update uses the exact current binding-ID permutation, so each surviving binding keeps its identity while positions change.

Numbered resource filters combine with AND, and counts and rows use the same bounded read snapshot. Endpoint `q` searches the base URL, note and mainstream channel name; `source=mainstream|custom`, `state=available|endpoint_disabled|no_keys|no_usable_key`, and a registered `connector_type` are optional. Key `q` searches only displayed fragments and notes; `enabled` and `donated` accept `true|false`, while `suspension_state=none|security_processing` is independent of donation membership. Model `q` searches the full model name and every binding's upstream ID without duplicate rows; `provider` is exact, `route_strategy=ordered|random|cache_balanced`, and `connection_state=available|unavailable|unconfigured`. Empty optional choices, unknown/repeated filters and unsupported combinations are rejected. Omit a filter to clear it. Sizes remain 10/20/50/100, default 20, with out-of-range pages clamped. Legacy cursor behavior remains unchanged and does not acquire these new filters.

### 3.3 Credit history

`GET /api/credits/history` returns the signed-in user's credit history. Optional single-value filters include `asset_type=general|game|sketch_paper|sketch_brush|all` (default general), a supported ledger category, `direction=income|expense`, and Unix-second `from,to` using `[from,to)`. Categories include check-ins, onboarding, welfare, Thursday, each game, API/charity/donation, administrator adjustments, penalties, loans, picture-book operations and inactivity. `page` is a positive decimal integer, default 1; `page_size` is 10, 20, 50, or 100, default 20. An out-of-range page returns the last page.

The response is `{data:[{asset_type,operation_id,line,kind,delta,created_at,request_id}],page,page_size,total,total_pages,anchor,current_balance,game_balance,server_now}`. Amounts and page/count fields are exact decimal strings; `line`, `page_size`, and times are bounded numbers. Only nonzero changes to the current user's selected wallets appear. One operation can have a general line and a game line, each with its own amount and line number; they are never combined into one amount. Reasons use the stable ledger `kind`; private management notes, actor identities, source IDs, other wallets, and historical post-balances are omitted. `current_balance` and `game_balance` are the current general and game balances, including changes after the browsing anchor. The user page requests all assets initially; changing the asset filter resets its page and anchor.

Pass the returned nullable `anchor` operation ID to subsequent pages to keep newer entries from shifting the result set. The server checks the anchor, total and page against the same selected owner wallets. Omit it to refresh. Related `request_id` is present only for the caller's own API/charity entries and short-request penalties whose request log is still ordinarily viewable; donor rewards always return null. Credit history and the ledger section of account exports include only operations with `created_at > server_now - 30*86400`, even before physical maintenance runs. Older details are compressed and removed every six hours; pending/held evidence stays internal until releasable. Current balances and existing business entitlements are preserved. The user page is `/credits`; `/logs?request_id=...` opens the existing owner-checked log detail. Draft-paper and brush deltas are whole-unit strings and are never formatted as fractional general/game points or combined across assets. Activity exchange, reservation, charge, refund and inactivity-decay entries have distinct labels. The balance summary fields above remain general/game only; the activity wallet endpoint provides the activity balances.

### 3.4 Personal CallerKey automation

Use your **site CallerKey**, not an upstream provider key, on the configured user host. These dedicated routes accept active ordinary users, including level 5 and level 6, for their own resources only. Cookies and administrator passwords cannot replace the CallerKey. Rotation, revocation, ban, deletion, maintenance and current resource ownership are checked again before reads, writes and replay. Other session APIs do not acquire Bearer authentication or cross-origin permission.

Create the endpoint and personal platform model first, through the ordinary resource pages. You can start or stop the optional connection guide at any time; charity access does not depend on completing it. Stopping keeps resources already created, clears secret input and stops subsequent steps. A failed model-list check does not verify or invalidate a manually entered model name.

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/automation/endpoints` | Owned endpoint page. |
| GET | `/api/automation/endpoints/{id}` | Owned endpoint metadata. |
| GET | `/api/automation/endpoints/{id}/keys` | Safe owned key page. |
| GET | `/api/automation/endpoints/{id}/keys/{keyId}` | Safe key metadata under that endpoint. |
| GET | `/api/automation/models` | Owned personal model page. |
| GET | `/api/automation/models/{id}` | Owned model metadata, including `role_policy`. |
| GET | `/api/automation/models/{id}/bindings` | Existing connection page and current source state. |
| POST | `/api/automation/endpoints/{id}/keys/batch-import` | Ordered per-key import results. |
| POST | `/api/automation/models/{id}/bindings/batch` | Ordered per-key append results. |

All GETs have no body or `Idempotency-Key`. Single-object reads have no query. Lists accept only `q,page,page_size`: `q` has at most 200 Unicode code points; page defaults to 1 and size to 20, with sizes 10/20/50/100. Lists return `{data,pagination}` with the numbered metadata in §1.2. IDs and revisions are positive int64 decimal strings. Reads are bounded to five seconds and 256 KiB; oversized pages fail instead of truncating. Necessary names, owner notes, addresses, settings, revisions and connection state are readable; secrets, ciphertext, fingerprints and private review material are not.

Find target IDs before writing:

```bash
curl --fail-with-body "$USER_ORIGIN/api/automation/endpoints?q=Personal&page=1&page_size=20" \
  -H "Authorization: Bearer $CALLER_KEY"
curl --fail-with-body "$USER_ORIGIN/api/automation/models?page=1&page_size=20" \
  -H "Authorization: Bearer $CALLER_KEY"
```

#### Import keys into an existing endpoint

Both POSTs require JSON content type and one unpredictable 22–128-character URL-safe ASCII `Idempotency-Key`. Save the complete ordered input and key securely for retries. A fictional `import.json`:

```json
{
  "ownership_confirmed": true,
  "keys": [
    {"secret": "fictional-upstream-key-a", "note": "Primary", "enabled": true},
    {"secret": "fictional-upstream-key-b", "max_concurrency": 2, "max_rpm": 60}
  ]
}
```

```bash
curl --fail-with-body "$USER_ORIGIN/api/automation/endpoints/42/keys/batch-import" \
  -H "Authorization: Bearer $CALLER_KEY" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $IMPORT_OPERATION_KEY" --data-binary @import.json
```

`ownership_confirmed:true` and 1–100 ordered `keys` are required. Each `secret` is nonempty UTF-8, at most 64 KiB, without control characters; its exact spelling is preserved. Optional fields are `note` (empty, at most 1,024 code points), `enabled` (true), `force_store_false` (false; OpenAI-compatible only), `max_concurrency` and `max_rpm` (integer 0–2147483647, default 0 for no additional key limit). Null, unknown fields, duplicate JSON properties and wrong types reject the whole body before business writes. Business failures in valid items return per-item results. The total body limit remains 256 KiB, so 100 large secrets may require smaller batches.

An identical stored secret in this owned endpoint returns the smallest existing key ID with `outcome:"existing"`. It preserves that key's enabled state, notes, limits, catalog and bindings, even when the new item asks for different settings. Existing duplicates use no new capacity. Other endpoints and accounts do not participate. Import does not discover models, donate keys or make a paid model call.

```json
{
  "endpoint_id": "42",
  "results": [
    {"index": 0, "status": "success", "outcome": "created", "endpoint_key_id": "81"},
    {"index": 1, "status": "success", "outcome": "existing", "endpoint_key_id": "82"},
    {"index": 2, "status": "failed", "code": "resource_limit_exceeded", "message": "configured resource limit reached"}
  ]
}
```

#### Append connections to an existing personal model

A fictional `bindings.json`:

```json
{
  "endpoint_key_ids": ["81", "82"],
  "upstream_model_id": "example/model",
  "catalog_mode": "manual"
}
```

```bash
curl --fail-with-body "$USER_ORIGIN/api/automation/models/23/bindings/batch" \
  -H "Authorization: Bearer $CALLER_KEY" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $BINDING_OPERATION_KEY" --data-binary @bindings.json
```

One request fixes one existing personal model, one upstream name (1–512 code points) and 1–100 unique `endpoint_key_ids` from owned endpoints. `catalog_mode` is `manual|refresh`, default `manual`. New connections append without replacing, removing or reordering existing ones or changing the model's settings. Endpoint/model creation and automatic enabling are outside this API.

An existing `(model,key,upstream)` connection returns `outcome:"existing"` and its `binding_id`, without discovery, catalog additions or order changes. This confirms configuration, even if its source is now disabled; it does not promise that the connection can currently serve calls. New connections must pass existing eligibility, locks, capacity, connector and model-policy checks.

Manual mode adds a missing owner manual candidate and that connection together; a failed connection leaves no new manual entry. It does not verify upstream availability. Refresh mode uses real model discovery, at most 15 seconds per item, and requires that fresh result to contain the exact name. Failure, empty results or a missing name do not fall back to manual. The final write checks the same credential/configuration and discovery revision. A completed discovery can remain even when its subsequent connection fails.

```json
{
  "model_id": "23",
  "results": [
    {"index": 0, "endpoint_key_id": "81", "status": "success", "outcome": "existing", "binding_id": "91"},
    {"index": 1, "endpoint_key_id": "82", "status": "incomplete", "code": "incomplete", "message": "No result is confirmed; use the same idempotency key to check and continue."}
  ]
}
```

#### Partial results and recovery

Always inspect `results`, including on HTTP 200. At least one success returns 200; all confirmed business failures return 422; no success with an incomplete item returns 504. These result envelopes are separate from the ordinary error envelope. Whole-body/target errors use 400/401/403/404, changed input under the same key gives 409, busy/capacity gives 429, and an oversized body gives 413.

Each confirmed item commits independently. Within a fixed 24-hour window, the original key and complete ordered input return confirmed success/failed items unchanged and continue only unfinished items. Changing target or content under the same key conflicts before business writes. Correct a confirmed failed item and submit only that failed subset as a new operation. A dropped response, cancellation or timeout does not prove rollback: first retry the original key and body. After expiry, read the actual resources before deciding to start another operation. Restart does not automatically resend secrets or start remaining items. Results never echo input secrets or notes.

Import has a 30-second total budget, append 60 seconds and results at most 64 KiB. Personal and charity automation reads and writes share **four concurrent requests globally and one per user**, without a queue. At most 1,000 active batches per user and 10,000 globally may be registered; capacity rejects new registrations but permits existing retries. No model-call credits or charity usage are charged. Revocation or loss of permission stops further work while preserving already committed items.

### Model operation types

Personal and charity model create/PATCH/GET, authorized lists, automation reads, the public charity catalog and personal resource exports include `model_types`: a nonempty array of registered operation names. Values are `chat_completions,responses,embeddings,images_generations,decisions,responses_compact,responses_input_tokens,completions,images_edits,images_variations,audio_speech,audio_transcriptions,audio_translations,moderations,rerank,systemone`. Chat Completions and Responses are independent choices: an explicit single value is saved and returned unchanged, and only enabled platform operations may be called. Creation without the field still enables both; PATCH omission preserves the selection. The schema-37 upgrade normalizes older stored interface bits once to preserve their effective dual-interface support, including legacy selections stored with either Chat or Responses alone; new schema-37 choices remain exact on later starts. Other services may be selected independently together. Duplicate or unknown values, empty arrays and explicit null are invalid. Native-service routing and bounded protocol conversion remain independent of this selection and cannot enable a disabled platform operation. The final candidate snapshot rechecks the requested operation before request acceptance, key claims or reservations; admitted requests and retries retain that snapshot. This does not alter the earlier freeze point for role and transport policies. Enabling a platform type does not add that capability to an unsupported connector or upstream model.

Existing ownership, administrator/steward scope, revision and idempotency rules apply. CallerKey automation only reads this setting and does not gain model-setting write access; level-5 stewards retain their existing limited policy permissions.

### 3.5 Model message-role policy

Personal and managed charity model create/PATCH/GET carry `role_policy`. Create or migrated records default to `{"default_action":"native","rules":{}}`; omitted PATCH fields retain their value and explicit null is invalid. It belongs to each model, uses its revision/idempotency contract and does not grant CallerKey model-setting writes.

```json
{
  "role_policy": {
    "default_action": "native",
    "rules": {"developer": "system", "critic": "user"}
  }
}
```

Actions are `native|passthrough|system|user|assistant|reject`. Native keeps the connector's existing handling; passthrough preserves the role only if that connector can express it. The three named-role actions change only the role; reject refuses the request before dispatch. Unmatched ordinary roles use the default. Rules do not chain. At most 32 case-sensitive names are allowed, each 1–64 code points and at most 256 UTF-8 bytes, with no controls or surrounding whitespace. `system,user,assistant,tool,function` cannot be configured. Canonical settings are limited to 8 KiB; unknown/duplicate fields or actions fail.

Native preserves existing differences: OpenAI-compatible forwards its supported roles, Anthropic-compatible merges legal developer text into system, and Gateway v3/v4 rejects developer. Explicit passthrough never silently maps or drops an unsupported role. Each logical request captures one model policy, converts once before candidate selection and uses that snapshot for retries, Debug and streaming. Message order, content and other fields stay intact; protocol validation still applies. Tool/function messages and messages with tool-call associations remain under the independent tool/flattening protocol. Role rejection or unsupported conversion does not count as a donated key's upstream failure. Changing a role can change how the provider interprets the message.

### 3.6 Resource-operation lookup

The owner-session route `POST /api/resource-operation-status` accepts `{"operation_key":"<original Idempotency-Key>"}` with no query. It is a read-only lookup: no new `Idempotency-Key` header is needed. The body limit is 16 KiB and the operation key follows the 22–128-character format in §1.2. This session route does not accept CallerKeys.

HTTP 200 returns `status:"recorded"|"in_progress"|"not_recorded"|"expired"`. Recorded/in-progress results also contain `stage` and `result`. Stages are `endpoint`, `key`, `catalog_refresh`, `catalog_manual`, `model` and `binding_batch`; results contain only the relevant `endpoint_id`, `endpoint_key_id`, `model_id`, `catalog_entry_ids`, `binding_ids` or discovery `operation_id`. Discovery remains in progress while its accepted operation is pending. These receipts identify committed work; read the referenced resources to determine their current state.

Receipts use the original fixed 24-hour replay window. Missing or expired receipts cannot establish rollback, and a completed discovery receipt does not establish upstream availability. A key reused in multiple live replay namespaces returns `409 conflict`. The lookup rechecks the current account and ownership and returns no secrets or request body. Invalid input is 400, an oversized body is 413, and normal session/account errors apply.

## 4. Donations, charity capability, and public reports

### 4.1 Donations and charity models

| Method and path | Request / response |
| --- | --- |
| `GET /api/donations` | Legacy `cursor,limit` or numbered `page,page_size`; numbered mode also accepts `status,review_status,q,view=current|history|hidden|all` and returns donation summaries with `pagination`. |
| `GET /api/donations/{id}/keys` | Numbered `page,page_size` only, optional `view=current|ended|all`; owner key-level donation projection with `pagination`. |
| `GET /api/donations/{id}` | Owner detail with key states and review history. |
| `POST /api/donations` | `{description,keys:[{endpoint_key_id,expires_at}],ownership_authorized:true}`; 1–100 unique keys; returns 201. |
| `PATCH /api/donations/{id}` | Pending-only `{description,expected_revision}`. |
| `POST /api/donations/{id}/withdraw` | Pending `{expected_revision}`. |
| `POST /api/donations/{id}/terminate` | Approved `{expected_revision,confirmation}`. |
| `GET /api/charity/models` | Without query parameters: `{state,models,donation_intake,server_now}`. Use `view=catalog` for the paginated web directory described below. |

Donation list and detail projections include `use_summary:{total,pending,approved,rejected,withdrawn,review_status}`. Counts are canonical decimal strings. `pending` and `approved` count current pointers independently; one use can have both. `rejected` and `withdrawn` count uses whose latest submitted revision has that result, including a rejected replacement whose previous approval remains valid. `review_status` is a display-only value: `pending|approved_pending|approved|partially_approved|rejected|withdrawn|mixed|none`. Pending with any existing approval is `approved_pending`; otherwise pending takes priority, then all/partial approval, all rejected, all withdrawn, or mixed completed outcomes. `none` means there are no uses. It never replaces the actual donation `status` or its authorization and termination checks.

Numbered owner, administrator and steward donation lists accept optional `review_status` to filter the displayed state. It uses those review states except `none`, plus `deleted|expired` for lifecycle results. Real terminal state and logical expiry take precedence; donations without uses fall back to their lifecycle status. Existing `status` filtering remains compatible. All steward list filters combine with the whole-donation resource predicate in §6.4; standard scope has no separate pending-use filter exception. Identity projection does not expand that range.

Administrator and steward sessions retain `POST {prefix}/donations/{id}/uses/review` for at most 100 explicit items per request. Each item names the stable use, `expected_revision,expected_review_revision`, decision and visible review note; its existing per-item results and replay contract remain unchanged.

The ordinary approval page uses `POST {prefix}/donations/{id}/pending-review` for one decision on the **entire current pending submission**. The prefix is `/admin/api` or `/api/steward`; steward callers require `donations.review` and the whole-donation scope in §6.4. This cookie-session route requires `Idempotency-Key`, accepts no query and has a 256 KiB JSON body limit. Read the donation revision and every page of `GET {prefix}/donations/{id}/uses?state=pending&page=1&page_size=100` without a key filter before forming the request:

```json
{
  "expected_revision": "8",
  "decision": "approve",
  "reason": "Reviewed the current submission",
  "uses": [
    {"use_id": "11", "expected_revision": "3", "expected_review_revision": null},
    {"use_id": "12", "expected_revision": "2", "expected_review_revision": "1"}
  ]
}
```

`decision` is `approve|reject`; `reason` is the visible review note. IDs and revisions are canonical positive decimal strings, and `expected_review_revision` is the current body-review revision or null. The unique `uses` list must exactly match the complete pending set, with at most 1900 items within the body limit. Do not submit `key_settings`: the server derives approval settings from the pending snapshot and existing key settings. A changed donation revision, use revision or pending set returns 409 and applies nothing.

Success returns HTTP 200 with the current role's donation projection, not an item-result array. All use decisions, donation state, audit and replay receipt commit together or all roll back. Rejecting a replacement retains the previous approved version; forced revocation remains a separate authorized operation. Ordinary approval does not create a charity binding; subsequent configuration selects the models and services.

After an uncertain response, replay the original path, body and idempotency key. After a definite revision conflict, reread the entire pending set before forming a new decision. A failed refresh after success retries only the read. Existing `/review`, `/uses/review` and CallerKey creation/approval routes keep their own input and atomicity contracts; sequential item batches do not provide the whole-submission guarantee.

Creation and pending edits require a non-whitespace donor description, with the existing 1,024-code-point / 4,096-byte limit and LF normalization. Historical blank descriptions remain readable, reviewable and usable; review notes may still be empty. Creation also requires the explicit boolean `discord_public_thanks`, with no default. It records permission for an administrator or full steward to thank the donor manually in Discord. The value cannot change after submission. Old null values mean unconfirmed, cannot be backfilled, and are not consent. A pending edit cannot change this choice; omission preserves it. No automated Discord message is sent.

Each donation key has immutable `authorized_expires_at` and an effective `expires_at`, equal at creation. A reviewer may shorten the effective expiry or restore it only up to the donor's authorization; an unlimited effective value is permitted only when the authorization is unlimited. One key expiring removes only that key's membership and bindings and blocks new claims. The donation becomes expired only when its last live key ends. Accepted claims and reservations complete normally.

Administrator and steward donation projections with `donations.read_all` contain the same management information. `owner` contains `user_id,discord_id,display_name` for any surviving donor; an absent owner remains null. `reviewer` contains `user_id` and the recorded actor `role`, with a null ID after that actor's identity is removed. Manual review may have an empty reason; automatic approval has no reviewer and an empty reason. Detailed management key sources include channel category/revision when recorded. Ordinary owner projections remain separate. Historical idempotent responses may lack newly authorized information; the current detail read provides the authoritative projection without rewriting stored receipts.

Management donation lists `data[].sources[]`, key pages `data[].safe_source` and details `keys[].safe_source` additionally expose optional `category:"subscription"|"api_platform"` from the submitted mainstream-source snapshot, and optional `endpoint_tags:string[]` from the address's current effective endpoint tags, including domain rules. Custom sources have no category; empty tags are omitted. Tags use the existing nine-value endpoint-tag registry. List source groups, the first-three preview and `source_count` keep their existing meaning; each group uses its existing representative key snapshot and that address's tags, without merging tags from other addresses. Existing L5 identity projection and donation scope remain in force.

Every owner-visible key carries `safe_source`: a custom Connector/base URL or a mainstream channel/name/Connector/base URL. Internal source IDs, channel category/revision, report fingerprints, secrets, and management notes are excluded. A donation containing only keys from one immutable mainstream channel is approved atomically at creation. A fully custom donation remains pending. Mixed custom/mainstream or multiple-channel submissions are `invalid_request`.

`CharityCapability.state` is `feature_disabled|no_models|no_candidates|available`; `donation_intake` is `open|closed`. Only models allowed for the caller's current level and with a usable candidate are listed. Charity candidates exclude the caller's own donated keys unless the caller is currently level 6; personal calls are unchanged. Level-6 self-donation calls receive normal rewards and cumulative donation credit. Each model contains `id,provider,model,full_name,model_types,pricing,discount,recurring_discount,effective_discount,recent_success`. Pricing is a tagged union:

- `per_request`: canonical base `user_price_milli`, configured fixed-percentage `discounted_user_price_milli`, and current payable `current_user_price_milli`;
- `per_search_unit`: the same scalar fields, in milli-credits per reported search unit; quantities retain exact millionth-unit precision independently of token usage;
- `per_token`: corresponding `user_prices_milli`, `discounted_user_prices_milli` and `current_user_prices_milli` objects for uncached input, cache-write input, cache-read input, and output. The irrelevant mode fields are null.

Token pricing may additionally expose `user_modalities_milli`,
`discounted_user_modalities_milli` and `current_user_modalities_milli`, including
within an optional long-context tier. Each has `input_enabled,output_enabled`
and configured `text,image,audio,video` four-price entries. Input/output
classification and missing-price fallback are independent. Donor prices and
decisions are excluded. See [usage and context pricing](charity-context-pricing.md).

`discount` contains `enabled,percent,start_at,end_at`; the interval is `[start_at,end_at)`, nullable at either end. Enabling it pauses the recurring schedule even outside the fixed window, where full price applies. `recurring_discount` supplies a time zone, default payable percentage and at most 16 weekly periods. `effective_discount` contains `source,percent,next_change_at`; the latter is the next actual percentage change as a Unix second or null. Effective prices use the same integer ceiling rule as billing. `server_now` is the single decision time used for availability and pricing. Existing `discounted_*` fields use the configured fixed percentage regardless of the active window; consumers should use `current_user_*` for current prices. No donated-resource identity is exposed. See [weekly configuration, precedence, DST and frozen settlement](charity-discounts.md).

The complete capability response shares one read-only snapshot, including current
level, switches, models, availability and batched recent-success statistics.
Browsing excludes expired keys by time without advancing their stored lifecycle
state. Background maintenance and business paths continue that work. All model
directory responses remain `no-store` and have no complete-list cache or TTL.

The web directory uses `GET /api/charity/models?view=catalog`. Optional single-value parameters are `q` (at most 128 Unicode code points and 512 UTF-8 bytes, no NUL), `allowed_for_me=true|false`, `allowed_level=1|2|3|4|5|6`, `currently_available=true|false`, `model_type` (one registered operation name), `page`, and `page_size`. Omit a filter to apply no restriction; the filters are combined as an intersection. The web UI initially sends `allowed_for_me=true` and `currently_available=true`, while omitting `allowed_level` and `model_type`. Model-type filtering matches any model that includes the selected operation and applies before counting and pagination; Chat and Responses aliases select the shared service. It rejects cursor/limit and unknown or repeated parameters. Page numbers are canonical decimal strings from 1 to 2147483647; sizes are 10, 20, 50, or 100, with defaults 1 and 20. The response is `{models,pagination,donation_intake,server_now}`. Pagination contains decimal-string `page,total_items,total_pages` and integer `page_size`; empty results use page 1 of 1, and an excessive requested page is clamped to the last page.

Catalog entries contain the capability model fields plus `public_description,enabled,allowed_levels,level_allowed,currently_available,availability`. They include unavailable models so users can understand the reason. Availability is `feature_disabled|model_disabled|level_denied|no_usable_key|available`, in that precedence. `currently_available` reports the current charity resource state independently of the caller's level; `availability` remains the caller-facing explanation. Ordinary active donor bans exclude the donor's bindings from resource availability; a permanent protective-inactivity ban remains exempt. Unbanning or expiry restores eligible supply without rebuilding bindings, and other eligible donors can keep the model available. The same donor rule applies to runtime candidates and management key-model availability. Search, count, ordered page, pricing and availability share one read-only snapshot and a five-second query budget. No donor rewards, bindings, physical resources or private notes appear in the directory.

Administrator and steward model create/patch bodies accept `allowed_levels` and `public_description` within a 16 KiB request limit. Levels are unique integers 1–6, returned sorted. Omitted creation fields mean all six levels and an empty description; omitted patch fields remain unchanged. An explicit empty array blocks every ordinary caller, including L6. Null and duplicate/out-of-range levels are invalid. Descriptions are plain text, at most 1,024 code points and 4,096 UTF-8 bytes after CRLF-to-LF normalization; controls other than LF and TAB, including lone CR, are rejected. These fields use the existing model revision and idempotency rules.

Management model detail, create, and patch DTOs also carry the top-level `token_reserve_credits` field as a decimal string or `null`. It accepts canonical credit values from `0.001` through `9000000000000` inclusive, with no more than three fractional digits; zero, negative, exponent-form, overprecise, out-of-range, and non-string values are invalid. On create, omission or `null` inherits the global Token reserve. On PATCH, omission preserves the existing override and `null` clears it back to inheritance. The field is effective for `per_token` and `per_search_unit`: `per_request` keeps its per-request price reserve, and the override remains stored when inactive. Runtime preflight, candidate availability, and billing use the model value or the global `charity_token_reserve_milli`; the accepted value is frozen for an admitted request, so in-flight and recovery snapshots remain unchanged. Token-mode output reservation also considers the highest enabled user output price across base and long-context aggregate/modality prices. This is a management-only field and is absent from ordinary catalog/API projections and owner exports.

Pricing optionally includes independent `long_context` and `context_penalty` configurations. Their thresholds use reported input across ordinary, cache-write and cache-read buckets. Full alternate prices receive existing discounts; the one-time penalty does not. Request acceptance freezes both options. See [context pricing fields, replacement semantics and billing rules](charity-context-pricing.md).

Model configuration also accepts optional `session_headers` for generated upstream session values. See the [field defaults, authority, lifetime and header-precedence contract](charity-session-headers.md). Public catalogue and capability DTOs do not expose this management configuration.

Administrator and steward model writes with the applicable create/pricing grant accept top-level `recurring_discount`.
Creation defaults to disabled; PATCH omission preserves it and an explicit object
replaces it completely. Level-5 defaults do not include pricing changes; an explicit
create/pricing grant is required within the current model scope.
Management model projections return the configuration, `effective_discount` and
`server_now`; list entries share one decision time. The existing revision,
idempotency and 16 KiB body limits apply.

Level permission is rechecked at admission, claim and immediately before the dispatch marker. A denied level returns `403 forbidden`; an unavailable model returns `404 not_found`. A blocked unsent attempt releases its reservations. Already dispatched attempts finish under their accepted accounting, while later sends and retries must pass current access rules. Debug dry and live calls follow the same caller-level restriction.

`recent_success` contains `window_start,as_of,success,failure,cancelled,sample_count,rate,insufficient_sample,capture_started_at`. It measures completed logical requests actually dispatched in the preceding 24 hours: a retry that finally succeeds counts once, undispatched refusals do not count, and cancellations are listed separately outside the denominator. `rate` is a number from 0 to 1 or null when there is no sample. Fewer than 20 samples is explicitly insufficient; zero samples never implies 100% success. Coverage start is visible and any cache is at most 60 seconds old.

### 4.1.1 Owner donation history

An explicit owner-list `view` selects numbered mode. Omission preserves legacy
`all`. `current` means logically live pending/approved donations; `history` means
unhidden terminal or logically expired donations; `hidden` means hidden history;
`all` includes hidden rows. An owner key page defaults to `all`; `current` and
`ended` partition keys by their current logical membership/expiry.

`PUT /api/donations/{id}/history` takes `{"hidden":true}` or false and returns
`{donation_id,owner_hidden_at}`, with a nullable Unix-second timestamp. Only the
owner's terminal history may be hidden or restored. It changes a private display
preference, never deletes a donation, advances its business revision or restarts
its 400-day terminal retention. Management views do not expose this preference.

`POST /api/donations/history/batch` requires all four fields:

```json
{"action":"hide","status":null,"q":"","cursor":null}
```

`action` is `hide|restore`; `status` is null or `rejected|deleted|expired`. Each
step scans at most 100 candidates and returns decimal-string
`changed_count,scanned_count` and nullable `next_cursor`. Continue with the same
action/filter and returned cursor, even when no rows changed. A one-hour cursor
freezes time and donation/review/preference-ID boundaries; later donations,
terminal transitions and preference changes are not silently absorbed. Current
owner authority is checked on each step. Both routes use browser sessions,
strict JSON and ordinary mutation idempotency. Retain the original body/key on
uncertain results; use a fresh key only for the next cursor step. Canceling stops
future steps and keeps committed preferences. Export 22 includes all owned
donations and their `owner_hidden_at`, including hidden history.

### 4.1.2 Temporary donated-key cooldown

On any upstream HTTP status, valid `Retry-After` or `X-Retry-After` values
temporarily remove that donated key from charity selection until
`rate_limited_until` (nullable Unix seconds). This does not change personal-call
limits. The latest valid deadline across both names and repeated values wins;
an HTTP date's comma is not a value separator. A later observation cannot shorten
an existing cooldown. Basic unavailable states take precedence over
`rate_limited`. An elapsed deadline immediately stops limiting without a reset
or administrator action.

| Header | Accepted values |
| --- | --- |
| `Retry-After` | Nonnegative integer waiting seconds or an HTTP date. Integers are never interpreted as Unix timestamps. |
| `X-Retry-After` | HTTP date; 0–999999999 as waiting seconds; exactly ten digits at least 1000000000 as Unix seconds; exactly thirteen digits at least 1000000000000 as Unix milliseconds, rounded up to seconds. Other numeric lengths are not guessed. |

Invalid, negative, empty or overflowing values do not establish a hint. A valid
zero or past deadline records that this response carried a retry hint without
creating active cooldown. A non-200 response carrying such a hint is neutral for
consecutive failures; errors still return and log normally. Initial HTTP 200
resets the streak, including later protocol/read failures. These counter rules
do not establish protocol success or alter quota/credit settlement. Claim-bound
observations survive recovery; another attempt's cooldown is not evidence that
this attempt carried a valid hint. Existing cancellation, manual enablement,
approval, expiry, donor restrictions and quota rules still apply.

### 4.1.3 Smart donated-key backoff

Smart backoff automatically applies to charity calls without per-key setup. It keeps
the normal consecutive-failure policy: a recognized quota error leaves the
current streak unchanged. Verified caller-parameter errors have the separate
neutral treatment below. Initial HTTP 200 still resets it, including later
read or protocol failure. A valid retry header, including zero or a past deadline,
takes precedence over error classification.

Authorized key projections and failure-policy responses include:

| Field | Shape |
| --- | --- |
| `smart_backoff` | Null, or `{state,reason,source,retry_at}`; `retry_at` is Unix seconds. |

The former `smart_backoff_profile` and `smart_backoff_rules` fields are removed
from creation, policy updates, responses and account exports. Requests containing
them fail strict validation. Remove the fields from clients. Additional recognition
is configured per endpoint through the management API below. Personal calls and
model discovery do not enable this feature.

Classification uses bounded, complete structured error JSON from a non-200
response without a valid retry header. Built-ins require evidence of recoverable
periodic quota exhaustion: recognized business codes with matching quota context
or explicit quota/reset wording. HTTP status alone, generic quota words, billing
failures, expired plans, permission failures, context limits, ordinary RPM/TPM
limits and overload are insufficient.
Unknown, malformed or truncated responses keep the existing failure policy.
Successful content is never searched.

Without a reliable reset time, a short or unknown quota period waits 15 minutes,
then 30, then 60 after successive quota failures from actual trial requests,
capped at 60 minutes. Explicit weekly and monthly exhaustion waits six and twelve
hours respectively. A reliable future reset time with an explicit time zone is preferred.
All supported periods are recognized; elapsed waiting never implies a longer quota.

State is `waiting|ready|probing`; reason is
`five_hour_quota|day_quota|week_quota|month_quota|quota`, and source is
`builtin|custom_rule`. Additional endpoint rules use `custom_rule`; existing
historical episodes retain their recorded source. These fields contain no raw
error or internal claim ID.
At the deadline, the next eligible real charity request atomically claims one
trial across all models using the key. No traffic means no probe. The trial's
initial HTTP 200 clears the smart episode before a stream completes.
Ordinary trial failures count normally and reuse the current interval;
cancellation or unknown recovery does not count or advance the interval.
Old in-flight results cannot clear or advance a newer episode.

The upgrade preserves active smart episodes and in-flight trial facts. New
endpoint policies start empty; retired per-key settings are not restored. Changing the failure threshold or resetting
the failure streak preserves smart state and header cooldown. `rate_limited_until` remains
header-only; smart `waiting` and `probing` also project
`charity_state:"rate_limited"`. Ready state still requires every ordinary
ownership, approval, enablement, expiry, quota and admission check.

Donation key read responses include optional `service_statuses` for current approved
services. Each entry contains `use_id`, `native_service`, `state`,
`failure_streak`, `failure_disabled`, nullable `retry_after_until`, and nullable
`smart_backoff` with the state/reason/source/retry time described above.
Service state is `available|disabled|review_required|failure_disabled|rate_limited|unavailable`.
It describes that service's authorization and health; shared key quotas and
lifecycle remain in `charity_state`. Detail and filter projections use the same
key-state calculation. One paused service does not disable other healthy services.
Old key-level `streak` and cooldown fields retain their key-level meaning.
Only the current approved credential version contributes service failures;
discovery success does not recover a paused inference service. Failure suspension
requires the existing authorized service reset, which preserves cooldowns.
Recovery probes expose `probing` with a real recorded retry time, never a sentinel
date. These safe status fields contain no upstream error body or credential.
Mutation receipts and account exports omit the live service snapshot; refresh a
read endpoint after changing health controls.

#### Endpoint recognition rules

Administrators and stewards with `charity.backoff` configure an exact `connector_type`
plus normalized complete `base_url`. All charity keys at that endpoint share
rules, including later donations. Different protocols and paths remain separate.
Valid retry headers and recognized built-in errors take priority; otherwise the
first matching additional rule applies.

Each endpoint accepts at most eight ordered `{kind,value,period}` entries.
`kind:"code"` exactly matches `error.code` or `error.type`; value contains
1–64 ASCII letters, digits or `_.:-`. `kind:"message"` matches a case-sensitive
literal substring of `error.message`; value is trimmed and accepts 1–160 Unicode
characters without control or format characters. No regular expressions are supported. Period is
`auto` (default), `five_hour`, `day`, `week` or `month`. Auto uses explicit
period evidence or the ordinary unknown-period schedule.

For HTTP 402 with `{"error":{"code":"insufficient_credits","message":"Insufficient credits"}}`,
an operator can configure `{"kind":"code","value":"insufficient_credits","period":"auto"}`.
HTTP 402 alone does not trigger backoff.

Use the corresponding web session and prefix `/admin/api` or `/api/steward`:

- `GET {prefix}/endpoint-backoff-options`: no query/body; returns
  `{connector_types:[]}` from the connector registry.
- `GET {prefix}/endpoint-backoff-policies?q=&page=1&page_size=20`: standard
  `{data,next_cursor:null,pagination}` numbered page. Rows contain
  `connector_type,base_url,revision,rule_count,key_count`; counts and revisions
  are decimal strings. Search accepts up to 128 Unicode characters. Includes
  donated and explicitly configured endpoints.
- `GET {prefix}/endpoint-backoff-policy?connector_type=...&base_url=...`:
  returns `{connector_type,base_url,revision,rules}`; an unconfigured endpoint
  has revision `"0"` and empty rules.
- `PUT {prefix}/endpoint-backoff-policy`: strict JSON
  `{connector_type,base_url,expected_revision,rules}`, a 36 KiB body limit, CSRF and
  `Idempotency-Key` required. Returns saved detail. Revision conflicts return
  409; preserve drafts and reread before resubmitting. Unknown outcomes retry
  with the same key and body. Replay rechecks current permission.

Only supported connectors and valid bases can be configured; no upstream request
is made. Saving empty rules clears recognition while retaining the revision.
Changes affect future dispatches only, preserving current waits, trials and
failure streaks. In-flight requests finish using their dispatch snapshot.
A matched custom rule leaves the existing failure count unchanged. Ordinary
ordinary users and CallerKeys cannot manage these policies; level-5 sessions require `charity.backoff`.

### 4.1.4 Verified caller-parameter errors

A narrowly verified caller-parameter error leaves the donated key's consecutive-failure count unchanged: it neither increments nor clears it. The request still fails and retains its normal sanitized error, diagnostics, retry and accounting behavior. HTTP 400/422 alone, generic `invalid_request_error` or message wording cannot establish this result. Existing retry-header and quota-backoff rules retain priority.

Recognition requires a complete, bounded `application/json` response with no duplicate keys, an `error` object, a nonempty string message, and exact string `type`, `code` and `param`. The message is not used for classification. HTTP 400/422 with `type:"invalid_request_error"` accepts codes `invalid_value`, `invalid_type`, `unsupported_parameter` and `unsupported_value`. HTTP 400 also accepts the exact `type:"BadRequest"` and `code:"InvalidParameter"` pair used by Ark. This pair still requires an eligible named parameter; missing or empty `param`, code suffixes and HTTP 422 do not qualify.

The exact top-level parameter allowlist is `temperature`, `top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `seed`, `stop`, `n`, `logprobs`, `top_logprobs`, `logit_bias`, `max_tokens`, `max_completion_tokens`, `max_output_tokens`, `reasoning_effort`, `reasoning`, `verbosity`, `response_format`, `dimensions`, `encoding_format`, `size`, `quality`, `style`, `output_format`, `output_compression`, `partial_images`, `aspect_ratio`, `resolution`, `steps`, `cfg_scale`, `negative_prompt`, `async`, `speed` and `language`. Extension names only expand error recognition; they do not add asynchronous task or result-retrieval support.

Only supported native OpenAI JSON paths can supply the proof. The named parameter must come from the original caller and have the same raw JSON value in the final upstream body. Request adaptation, additional or managed headers, native extensions and protocol conversion exclude recognition. Changing the reported parameter's raw JSON value, even by equivalent normalization, also excludes it. Nested/unknown parameters, model/resource/identity/store/service-tier errors, other protocols, rerank and multipart are outside this classification. Authorization, billing, platform, network and ambiguous failures retain their existing handling. Personal calls are unchanged.

The same completion transaction saves a minimal claim-bound receipt before accepting the neutral outcome. The receipt contains only its claim reference, HTTP status and recognized code, with no parameter name/value, body or upstream message. It follows the claim lifecycle and is excluded from account export.

### 4.2 Donation failure-streak reset

Each key's `failure_disable_threshold` is a canonical decimal U128 string, default `"10"` on creation and migration. Owners, administrators and current stewards can independently `PATCH {prefix}/donations/{id}/keys/{keyId}/failure-policy`, using `/api`, `/admin/api` or `/api/steward` respectively. The strict body requires `{expected_revision,failure_disable_threshold}` with `Idempotency-Key`. The safe response includes `donation_id,donation_key_id,failure_disable_threshold,failure_streak,failure_disabled,revision` and nullable `smart_backoff` (§4.1.3). New donations need no smart-backoff configuration.

Saving retains the current count and generation and atomically recalculates error-disablement: `0` clears it; a positive threshold disables when the count reaches that threshold. Zero still records failures. Upstream HTTP 200 or a protocol-complete success clears the count, regardless of later errors. Without either, a non-200 response with a valid retry hint, an error recognized by its smart-backoff policy, or a narrowly verified caller-parameter error leaves the count unchanged without clearing it; other upstream response/protocol/DNS/connect/timeout failures increase it; caller cancellation, downstream failure, Debug stop, platform rejection and internal failure leave it unchanged. A confirmed upstream failure still counts after a later cancellation. Recovery uses a persisted HTTP 200 acceptance checkpoint when available; an unknown outcome is neutral. Fees and quotas follow the billing rules above. Ordered in-flight results use the latest saved threshold; old-generation completions cannot change current state. Manual disablement, expiry, withdrawal, bans, quotas and ownership checks continue to apply. Pages warn when zero is entered or saved, without extra confirmation. Each real save has a no-secret review audit; replay repeats no write or alarm. Only a transition into error-disablement alerts; clearing and later disabling can alert again.

Owners can `POST /api/donations/{id}/keys/{keyId}/failure-streak-reset` with `{expected_revision}` and `Idempotency-Key`. Only their own approved, unexpired, active donated key is eligible. The response is `{donation_id,key_id,revision,failure_streak:"0"}`. Missing ownership returns 404; stale or ineligible state returns 409.

Administrators and current stewards can `POST {prefix}/donation-keys/failure-streak-reset`, where the prefix is `/admin/api` or `/api/steward`. The strict body is `{items:[{donation_id,key_id,expected_revision}]}`, with 1–100 unique keys and one expected revision per donation. The response is `{results:[{donation_id,key_id,status,revision}],counts:{processed,reset,skipped}}` in input order. Status is `reset|conflict|ineligible|not_found`; counts are decimal strings and revision is a string or null. A donation's revision advances once per reset key; every result in that donation reports the final revision from the batch. A stale donation skips all its submitted keys; an ineligible key does not prevent eligible keys in other donations from succeeding. Replays require current authority. These control requests have the shared 256 KiB body limit.

Reset clears only the consecutive-failure disabling state and starts a new failure generation. It preserves usage, quotas, expiry, bindings, manual disablement, header cooldown and smart backoff. Late callbacks from the previous generation cannot rebuild the cleared streak. Each actual reset records a no-secret review audit.

Before processing all matching results, `POST {prefix}/donation-keys/failure-streak-reset/selection` accepts `{selection:{view,...filters},cursor:null|string}`. The cursor is required; start with null. Views are `donations|donation_keys|sources|source_keys`. Donation filters are `q,status,review_status,handling`; `donation_keys` requires only `donation_id`; source filters are `q,scope,handling`; source-key filters additionally require `source_key` and accept `idle`. These match the corresponding list filters, with no pagination fields. Each call has a five-second budget, scans at most 100 saved key IDs and returns `{items:[{donation_id,key_id,expected_revision}],next_cursor}`; an empty item page may still have a next cursor. The signed cursor fixes the upper key-ID boundary, role, actor and filters for one hour. This read-only operation writes no selection, job or replay record. The browser gathers the fixed selection before sending batches of at most 100, yields between steps, supports interruption and retains the exact batch and idempotency key when a response is uncertain.

#### Managed model discovery

Administrators and current stewards have three web entry points: one available donated key, all available keys in one donation, or all available keys across all donations. The latter two ignore list filters and pagination. These are Cookie-session management routes, with prefixes `/admin/api` and `/api/steward`; they do not expand CallerKey automation scopes.

| Method and suffix | Request / response |
| --- | --- |
| `POST /donation-keys/models/refresh/selection` | Strict `{donation_id:null|string,cursor:null|string}`, both fields required; no query or idempotency key. Null donation selects all donations. Returns `{items:[{donation_id,key_id}],next_cursor}`. |
| `POST /donations/{id}/keys/{keyId}/models/refresh` | No body or query; requires `Idempotency-Key`. Returns 202 `{operation_id,evidence}` with `evidence.state="checking"` and the accepted discovery revision. |
| `GET /donations/{id}/keys/{keyId}/models/discovery` | No body or query. Returns only the existing safe discovery evidence union; does not replace `/models`, which lists related charity models. |

All IDs, revisions and result counts are canonical decimal strings. Evidence contains `state,revision,result,safe_class,observed_at,count`: `unknown` has null result/time/count and class `none`; `checking` has a start time but null result/count; `succeeded` has `empty|nonempty`, time and count (0–1,000), class `none`; `failed` has time, null result/count and one of `auth|region|rate_limit|timeout|protocol|transport|interrupted|unavailable`. Operation IDs are opaque `op_` identifiers. Responses are non-cacheable; no credentials, private endpoint notes or raw upstream diagnostics are returned.

`region` means the upstream rejected the server's region; it does not describe the browser's location. Gemini's exact HTTP 400 `FAILED_PRECONDITION` location error maps to this class, while a structured `API_KEY_INVALID` error maps to `auth`. Unknown HTTP 400 responses remain `protocol`; 401/403 mean authentication or permission failure, 429 means quota/rate limiting, and 5xx responses mean `unavailable`. Connectors retain bounded private failure diagnostics through the existing management-only capture. User setup and endpoint/key model pages share localized explanations, retry and manual-model entry paths. Adding a model manually does not change upstream access restrictions.

Eligibility requires an approved donation, live membership, no expiry or terminal state, enabled endpoint/physical key/donation key, no safety suspension or failure disablement, a live unbanned donor, and unexhausted total quotas. Existing public model bindings are not required. Selection, acceptance, credential dispatch and catalog publication recheck current authority and eligibility in their respective transactions. Wrong parent IDs return 404; unavailable keys return 409 `resource_locked`; another active discovery or a changed idempotent request returns 409. Session or role loss returns 401/403. Invalid input returns 400; unavailable shared capacity returns 503. A valid replay requires current authority and does not send another upstream request.

Selection and evidence reads have a five-second budget. Selection scans at most 100 candidate IDs per page; an empty page may still have a next cursor. The signed cursor binds the actor, role, donation scope and initial maximum key ID for one hour. Follow `next_cursor` until null without adding new keys above that boundary. Each accepted check uses the shared discovery worker (four concurrent, 32 admitted, five-minute lifetime), connector timeouts and outbound limits. No donation quota, game credit or reward is consumed; failure counters are unchanged.

The browser streams selection pages and runs one key at a time, polling every 1.5 seconds. It pauses after 60 pending observations or an uncertain response, keeping the exact idempotency key and accepted revision in memory. Resume reads accepted work or replays the same uncertain request. Permission loss and leaving the page stop further client scheduling; accepted server work may finish, and whole batches do not resume across page reloads. A newer discovery revision is reported as superseded. Results distinguish success (including zero models), failure, ineligibility and conflicts, retaining only the latest 100 detail rows. Successful discovery replaces automatic entries; failure preserves the previous automatic catalog, while manual entries and existing bindings remain unchanged. Discovery audit, request-log retention and account lifecycle follow the existing rules.

#### Forced rejection and renewed review

Administrator and steward session review routes with `donations.force_reject` accept `decision:"force_reject"` with the current `expected_revision` and a nonblank reason, without `key_settings`, for any approved donation. Administrator, level-5, level-6, automatic and unknown historical approval origins are eligible. Required credential-review material must remain available. This stops all new charity dispatches for its members, including retries, while preserving the owner's personal resources, already dispatched settlement and settled rewards. Level-5 defaults permit pending review but do not include force rejection.

The same secret text remains subject to manual review across accounts, addresses, connector types and deleted/resubmitted applications. Matching material is private and has no automatic expiry. A subsequent approval must include each affected member's current `expected_review_revision` and explicitly enable it to clear that secret's requirement; approval with the member disabled, ordinary enabling or limit edits do not clear it. Concurrent revision changes conflict before clearance. Minimal revision evidence remains after clearance. Restore the stable encrypted review material with the matching database/master key; never regenerate it to bypass requirements.

#### Managed manual model candidates

Both management prefixes expose `POST {prefix}/donations/{id}/keys/{keyId}/models/manual` with `{"expected_manual_catalog_revision":"1","entries":["example/model"]}` and `DELETE .../models/manual/{entryId}` with `{"expected_manual_catalog_revision":"2"}`. Both require the existing session, CSRF and idempotency key. Add 1–100 model-name strings; normalization and duplicates follow existing catalog rules. The read-only `/models` response adds `candidates,candidates_pagination,manual_catalog_revision`; candidates are the authorized automatic/manual union with source and verification facts.

Manual entries belong to the donation member, not its owner's personal catalog. Adding an entry does not discover, verify, bind, enable or dispatch it. Refresh success/empty/failure preserves managed manual entries. Disabled but nonterminal members may retain metadata management; ended/removed members cannot. Steward changes require `charity.manage`. Standard donation scope requires an authorized mainstream `charity_model_id` selector. Enhanced tagged donations and full scope allow direct key maintenance; `charity.non_mainstream` only changes eligible model targets. New CallerKey reads below expose only safe authorized member data.

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
| `GET /api/checkin/game` | Game-credit check-in, same status fields with `asset_type:"game"`. |
| `POST /api/checkin/game` | Bodyless, once per account/site day, subject to the daily choice setting; `{award,balance,asset_type:"game"}`. |
| `GET /api/events` | Shared user SSE for activities and RPS; see §8. |
| `GET /api/games` | Complete game availability, `balance,game_balance`, tutorial state, prices, timers, and mode configuration. |

Both check-ins share the site day and timezone, but have independent modes, awards and balance caps. Level-gated mode requires actual level 3 or above. A positive cap rejects a check-in when that wallet already reaches it; the sampled reward is not truncated. A zero cap removes this threshold. A zero reward still records a successful check-in; a disabled or rejected request does not. A duplicate returns `already_checked_in`. The game defaults are disabled, 40,000–60,000 credits per award and a 250,000-credit cap. The four keys `game_checkin_mode`, `game_checkin_award_min_milli`, `game_checkin_award_max_milli`, `game_credits_cap_milli` belong to site-config and its catalog/batch validation. HTTP amounts use credit strings even for keys ending in `_milli`. Daily statistics retain `checkins` for general and add `game_checkins`; a person checking in twice still counts once as an active user.

Administrators can set the boolean `checkin_mutually_exclusive` through site-config; it defaults to false when absent. When true, a person may complete either general or game check-in per site day, even if both individual modes are enabled. Both status routes may add `mutually_exclusive:true`; enabled statuses add `blocked_by_other_checkin:true` when the other type has consumed the day's choice. Omitted flags mean false. `checked_in_today` continues to describe the requested type. A conflicting POST returns `409 already_checked_in`; the check and ledger mutation share one transaction. Same-Discord eligibility survives account deletion and re-registration until the original site-day boundary. Enabling the option counts earlier successful check-ins that day, including zero awards, and never removes existing rewards. Disabling it restores independent eligibility, without allowing a second claim of the same type. User pages show the choice rule and refresh both statuses after a claim or conflict.

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
| `GET /api/games/likes/loadouts` | `{capacity:10,slots:[{slot,revision,mode,loadout:{role,harness,skills},updated_at}]}` for this account's saved Custom presets; empty slots are omitted. |
| `PUT /api/games/likes/loadouts/{slot}` | `{expected_revision,mode,loadout}`; save or explicitly overwrite one of slots 1–10 and return the saved item. |

One queue/active slot exists per user and game. The two games can run concurrently. Queues expire after 120 seconds and cap at 4096 per game. Matching requires the same mode and frozen terms, excludes equal device tokens, prefers different source IPs when possible, and assigns seats randomly. Neither device nor IP comparison values leave the server.

Custom presets are private to the signed-in account. Ordinary users and level-5 or level-6 stewards can read or change only their own presets; no administrator or automation route exposes another account's presets. The endpoints require an authenticated user session and same-origin requests. The write also requires `Idempotency-Key`; its slot must be 1–10, `mode` must be `quick` or `standard`, and `loadout` uses the same current validation as queue entry. It saves the role, nullable harness and ordered skills. Saving into an empty slot requires `expected_revision:"0"`; a successful first save returns HTTP 200 with revision `"1"`. Later saves increment the stored decimal-string revision. A stale `expected_revision` returns 409. Repeating an uncertain save with the same key and body is idempotent. The saved copy is not changed if a later catalog no longer accepts it; loading then reports the validation problem instead of silently substituting a loadout. Loading only fills pre-match choices and never queues or charges.

Only the losing entry funds a decided match's fees, with each basis-point cut floored separately. The winner receives their original payment split back and the loser's net entry as general credits. A draw or system cancellation refunds both original splits. Platform, welfare and Thursday destinations, escrow, result and terminal operation commit atomically. Surrender is a normal loss. Disconnection does not pause deadlines. Bans, deletion and process startup cancel unfinished new-game sessions; periodic recovery advances live work without treating it as a restart.

Both players submit against the same `phase_seq`; locking one side does not invalidate the other side through a shared revision check. Bidding has 13 rounds, 10-second available-joker decisions and 20-second simultaneous bids. Both cards stay hidden until resolution; a missing bid uses the smallest hand card. Ordinary game history does not list unused reward-deck order, but the separately disclosed terminal randomness proof allows reconstruction of the complete reward decks, including unused cards.

The current Likes catalog gives both modes 30-second planning periods for new matches, followed by an event-paced `settlement` phase. Exact historical schema-15 and previous schema-16 catalog snapshots retain their original 20-second planning duration for verification; persisted in-progress deadlines are never rewritten. Public configuration uses `settlement_seconds:0` to mean variable duration. A round's state, events, draws, payment and terminal facts commit before presentation. `resolution:{round,started_at,ends_at,summary}` is shared by both viewers and retained in the final summary. The summary includes `timeline:[{stage,duration_ms,event_ids}]`, with individually timed steps grouped into seven ordered semantic stages. Each event appears exactly once; opposing actions can share a step while consecutive casts have separate steps. Summed step durations equal `(ends_at-started_at)*1000`, with no fixed overall duration cap. The existing event count and response-size limits still apply. Steps reference server-authored score parts, resource snapshots and success/failure reasons. Older persisted summaries without a timeline retain their original five-second duration. `round_start:{round,started_at,events}` supplies replenishment facts. Clients display those facts without recomputing rules. Final wallet settlement does not wait for animation or a client acknowledgement. Expired animations are not replayed after reconnect, and the next planning period uses the complete duration in the match's saved catalog: 30 seconds for current matches and 20 seconds for older matches.

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

The table starts at each server :00 and :30: 5 seconds of seating, 20 of decisions and 5 of results. Early completion extends the display to the original next-round boundary. Waiting positions survive rounds and restarts; completed participants must explicitly rejoin. One accepted action per seat per second uses the requesting hand's revision. Additions reserve before the batch applies, and insufficient funds leave the hand unchanged. Other seats never cause a hand-version conflict. The public table fact never includes the shoe, dealer hole card before reveal, pending intents or another user's funding sources. The separate `realtime_identities` projection shows each occupied seat's current display name and safe avatar URL to seated players and spectators, even when the player's leaderboard profile is private. Waiting users are not exposed; deletion removes the live identity. These names and avatars are absent from retained facts, personal history, anonymous archives and account export.

Owner `payment` is `{general,game}` for all reserved additions. Public hand settlement amounts are decimal integer **milli-credit** strings: `stake_milli,gross_milli,platform_milli,welfare_milli,thursday_milli,net_milli`, plus `outcome`. Each hand is independently rounded and then summed; all normal net returns, including pushes and principal, are general credits. Cancelled tables include public per-seat total `refunds` without funding sources. See [full bilingual rules](blackjack.md).

`GET/PATCH /admin/api/games/config` adds `blackjack:{enabled,min_stake,max_stake,stake_step,default_stake,rake_bp:{platform,welfare,thursday}}`. Credit fields are decimal strings and basis points are integers. The defaults are disabled, 1,000–50,000, step 1,000, default 5,000 and three 100 bp fees. Positive stake values must align to the minimum/step; the `max_stake` ceiling is `140625000000` credits. Each player can invest at most four times the base stake and receive at most eight times the base stake before fees. Nine-seat table totals use decimal strings without truncating them to the per-operation amount limit. Running counts include Blackjack waiting entries and active tables.

Administrator-only `/admin/api/games/blackjack/history` and `.../history/{id}` accept `dataset=recent|anonymous` (default recent); list pages use the same 20/50 limits. Recent details include retained participant IDs, payment composition and operation references. Anonymous details contain only rules version, outcome and public card/settlement facts, with independent archive IDs and no absolute timestamps or emotes. Stewards cannot read these histories.

`POST /admin/api/games/blackjack/history/export` accepts `{dataset,cursor?}` and returns `{format:"blackjack-history/v1",dataset,items,next_cursor}` in bounded batches of at most 10 records. Clients may save each successful batch as NDJSON and resume only from its returned cursor. Signed cursors expire after one hour and bind the account/session, dataset, limit, endpoint kind, high-water mark and page position. Exporting or reading history does not pause play.

Restart cancels only the unfinished table and refunds original assets, preserving waiters and committed outcomes. Maintenance/closure releases waiters and undealt seats while dealt games finish. Ban stops a seat's actions and auto-stands it. Deletion detaches identity and continues settlement without cancelling other players; unavailable proceeds use the corresponding external asset account and cannot recreate a wallet. Queue game-credit reserves remain part of welfare assets. Account export schema 22 includes `blackjack:{current,history}` and safe per-game `randomness` proofs while retaining all previous fields and existing 10,000-row/16-MiB bounds.

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
| `GET /api/games/leaderboards/net-profit`, `GET /api/games/fishing/net-profit`, `GET /api/games/blackjack/net-profit`, `GET /api/games/bidding/net-profit` | Rolling seven-day actual net profit, no window selector. |
| `GET /api/games/bidding/leaderboard`, `GET /api/games/blackjack/leaderboard` | `window=7d|30d|history`, default 7d. |

A quote includes `principal,a,b,nominal,disbursed,fee,repayment,interest,general_before,general_after,game_before,game_after,quote_token,expires_at,config_revision,as_of`. All coefficients and amounts are canonical strings. A receipt replaces quote expiry/token with `loan_id,operation_id,sequence,created_at`, retaining the terms, configuration revision and actual before/after balances. These accounting fields remain on the wire; the user page displays only Principal, Fee, Game credits received, Interest and General credits deducted. History is ordered by creation time and sequence descending. Bodies are limited to 4 KiB and tokens to 2,048 bytes.

Quotes are owner-bound and expire after 60 seconds. Expiry or changed configuration returns 409 and requires a fresh quote and confirmation. Balance estimates may change before acceptance; the final transaction checks the current general balance is at least zero, then credits game funds and immediately debits general repayment, which may make that wallet negative. Default borrowing of 10,000 adds 9,000 game credits and subtracts 13,000 general credits, including a 1,000 game fee and 3,000 general interest. A general balance of zero can borrow once. Game funds remain usable when general funds are negative. The same key and body replay the receipt; a different key cannot reuse the quote nonce. Loans do not change donation credit, activity pools or game rankings.

Boards return `as_of,statistics_start,window,rows,me`, plus `pagination` for charity. Rows contain `rank,amount,is_me,identity`; identity is `{kind:"anonymous"}` or `{kind:"public",display_name,avatar_url}`. Charity paginates all positive totals. The game boards return the top 20 and a separate `me` only for an eligible caller below them. Charity visibility uses `charity_profile_public`, default false and independent of game visibility. A currently banned user remains ranked anonymously with no avatar; administrators are excluded.

Game charity sums actual spending minus all net returns across six games, combining both currencies 1:1; losses and wins offset. Bidding counts positive pre-fee profit per match, while Blackjack sums positive pre-fee profit per hand. Principal, loans and newcomer awards are excluded. New statistics begin at the fixed installation/upgrade time without reconstructing prior finished games. Rolling windows are `(as_of-duration,as_of]`; expired contributions are advanced before returning a consistent result. Incomplete bounded catch-up returns retryable 503. Higher positive amounts rank first, then earlier achievement of that current amount; returning from 200 to 100 establishes a new achievement time.

The additional net-profit boards reuse retained seven-day net game facts: final returned value minus actual stakes, including fees, with losses offsetting wins. General and game credits combine 1:1 only for this game statistic. Non-game issuance, loans, administrator adjustments and inactivity decay do not contribute. Only positive totals qualify; top-20, own-rank, anonymity and tie behavior follow the existing game charity board. Existing historical/30-day Bidding and Blackjack positive-profit boards remain separate.

Fishing's administrator configuration also controls the probability that a legendary catch becomes the blue fat fish: default 10%, range 0–100% in 0.01% steps. New accepted batches capture the setting; legendary species, length and reward rules are unchanged.

### 5.10 Fat Fish limited activity

Fat Fish is a session-authenticated limited activity under `/api/limited-activities/fat-fish`; it is not a `/v1/*` API or an ordinary game-center route. Only the environment administrator can edit content under `/admin/api/limited-activities/fat-fish`. Ordinary users and level-5/6 stewards have no editor authority. Mutations use same-origin/CSRF protection, strict JSON, a 22–128-character `Idempotency-Key`, and no query parameters; GETs have no body. A repeated key and identical request return its receipt, while a changed request conflicts. Responses and errors use `no-store` and the shared error envelope.

The player and level editor share one continuous field-and-workbench space. The editor can place tools within the opening scene or on the surrounding bench. The opening layout stays fixed during preview; after a challenge starts, the player can drag pieces between them. The board preserves its proportions and stays centered, fits narrow screens, and offers optional enlargement and panning.

In the player, press N to cycle tool selection, use arrow keys to move a selected tool, hold Shift for smaller moves, and press Delete to return it to the bench. In the editor, arrow keys move the selection, Shift makes smaller adjustments, Delete or Backspace removes the selected object, Esc clears selection or cancels placement, Ctrl/Command+Z undoes, and Ctrl/Command+Shift+Z redoes. Piece orientation stays fixed. Updated workspace presentation and default geometry for newly added or converted tools do not rewrite already published immutable versions.

| User method and suffix | Request and result |
| --- | --- |
| `GET /periods?page=1`; `GET /periods/{p}`; `GET /periods/{p}/nodes/{n}` | Visible period page, period detail, and the caller's eligible node/progress. A hidden ineligible node omits its content and exact configuration. The period list uses `page` only and returns `{items,page,page_size,has_more}`. |
| `POST /periods/{p}/nodes/{n}/unlock` | `{ "expected_revision":"1" }`; returns the caller's node `Progress`. One successful General-credit unlock persists for that period and node. |
| `POST /challenges/prepare` | `{ "period_id":"ffp_…", "node_id":"ffn_…", "expected_revision":"1", "tab_capability_hash":"<64 lowercase hex>" }`; returns a prepared `ChallengeView`, including `seed_commit` and `prepare_until_ms`, without seed or charge. |
| `POST /challenges/{id}/start` | `{ "tab_capability":"<original capability>" }`; returns the same challenge with the locked version, seed and server timestamps to its authorized tab. This is when the ticket is charged. |
| `GET /challenges/current`; `GET /challenges/{id}` | Safe own state. The original tab supplies `X-FatFish-Tab-Capability` to read seed and recovery material; another tab can only inspect safe state or abandon. |
| `POST /challenges/{id}/submit` | `{ "tab_capability":"<original capability>", "inputs":[], "terminal_tick":0 }`; the actual `inputs` array carries the complete bounded operation trace. Returns HTTP 202 while `verifying`, otherwise HTTP 200 with the retained challenge/result. Client score is never accepted as authority. |
| `POST /challenges/{id}/abandon` | `{ "tab_capability":"" }` is sufficient for the authenticated owner; a tab may include its own capability. Returns the terminal challenge and does not refund a started ticket. |
| `GET /history?page=1&limit=20`; `GET /periods/{p}/leaderboard?page=1&page_size=20&node_id=ffn_…` | Own 30-day terminal history uses `limit` of 20, 50 or 100 and `{items,page,page_size,has_more}`. Whole-period or optional node leaderboard uses `page_size` of 20, 50 or 100 and `{period_id,node_id?,final,page,page_size,total,rows}`. |

Each immutable level version records `engine_version` and `scoring_version`. New levels and the eight bundled examples use `engine_version=3` and `scoring_version=1`; versions 1 and 2 remain supported. Version 3 uses nominal side turning of 3.6 rad/s and front/both-side turning of 4.32 rad/s, with the existing ±5% variation, 60 Hz/two substeps and 72 px/s translation. Existing immutable versions, active challenges, retained history, and period node bindings keep the version they reference. Updating the application does not convert a draft or switch a node. To move an older draft, an administrator explicitly converts it to version 3 in the existing editor, saves and publishes the immutable version, playtests that version, then manually selects it for a node. Conversion does not publish or switch a node by itself.

Collection `page` is 1–1,000,000; period, level and version collections have a fixed page size of 20. A period has at most 128 nodes; a node binds an immutable content version and current revision. `amounts` contains canonical nonnegative General-credit strings `unlock_cost,ticket_price,first_clear_reward,star_rewards[3]`; game credits, paper and brushes cannot pay these charges. A period's `state` (`draft|open|closed`), `visible`, `paused` and `past_public` are separate controls. A closed period with `past_public=false` does not expose its public detail or board, while a caller can still read their authorized retained history. Rankings expose only current public identity or an anonymous projection, never another player's private result.

Visible period node summaries may include `condition_hint`, a user-safe explanation of the visible unlock conditions and whether they are met. Hidden prerequisites are represented only by a generic hidden-prerequisite hint; their identities and exact conditions are not exposed.

Prepare lasts 60 seconds without a charge. Start fixes `start_at_ms` at acceptance plus 3,000 ms, and `submit_until_ms` at the game's end plus 30 minutes; late delivery does not restart the clock. The original tab holds a 32-byte capability in session storage and resumes from its bounded local input record. A copied or new tab has no continuation authority. The first timely submit fixes one input digest; another digest returns 409. The input is at most 4 MiB within a 4 MiB + 1 KiB request envelope and is kept only in bounded verification memory. At most two verifications run with six waiting; full capacity returns 429 with `Retry-After: 1` before acceptance. Verification has a five-second deadline. After a crash, the same digest can be resubmitted by the original tab within ten minutes of its recorded receipt; this is not a new-play grace period. A confirmed platform verification fault refunds the original ticket; an invalid or late client submission does not. Validated settlement alone updates best scores and once-only rewards.

| Administrator method and suffix | Strict body / result |
| --- | --- |
| `GET /levels`, `/levels/{id}`, `/levels/{id}/versions`, `/versions/{id}`, `/levels/{id}/export` | Drafts, immutable versions and safe JSON export. Collection GETs use only `page` and `{items,page,page_size,has_more}`. |
| `POST /levels`, `POST /levels/import`, `PUT /levels/{id}` | `{title,description,draft,expected_revision?}`; creation omits the revision, update requires it. JSON draft is bounded and validated as inert content. |
| `POST /levels/{id}/versions`; `POST /levels/validate` | `{expected_revision}` publishes an immutable version; `{level:<level JSON>}` validates without publishing. |
| `DELETE /levels/{id}` | `{"expected_revision":"1"}` and required `Idempotency-Key`; returns HTTP 200 `{id,revision,deleted_at}` (deleted_at is Unix seconds). |
| `GET /periods`, `/periods/{id}`, `/periods/{id}/nodes/{node}`, `/periods/{id}/validate` | Period/node details and publishability/reachability evidence; collection uses `page`. |
| `POST /periods`, `PUT /periods/{id}` | `{title,description,visible,paused,past_public,starts_at,ends_at,expected_revision?}`; update requires the current revision. |
| `POST /periods/{id}/nodes`, `PUT /periods/{id}/nodes/{node}` | `{title,description,map_x,map_y,order,version_id,condition,hidden_until_eligible,amounts,expected_revision?,expected_period_revision}`; node update requires its current revision. |
| `POST /periods/{id}/publish`, `POST /periods/{id}/close`, `POST /periods/{id}/reopen` | `{expected_revision}`; publish checks version playtests, graph reachability and in-progress entitlement. |
| `GET /playtests?version_id=ffv_…`, `GET /playtests/{id}`; `POST /playtests`, `POST /playtests/{id}/start`, `POST /playtests/{id}/submit` | Version-bound no-charge playtests. The version-filtered list returns at most 100 recent records without pagination. Prepare uses `{version_id,tab_capability_hash}`; start and submit use the same capability and submit body shape as the user challenge. |
| `POST /challenges/{id}/cancel` | `{reason:"<bounded administrator reason>"}`; explicitly cancels and refunds an unsettled started ticket. |

Deleting a level requires administrator authority, its current revision, and an idempotency key. A stale revision conflicts; an identical replay returns the original receipt. Deletion removes the editable source from the library and prevents new period nodes or playtests from binding its versions. Published immutable versions, existing activity-node references, accepted challenges, and retained scores keep their original versions.

The editor uses General credits only for formal user play; playtests never charge, reward or rank. User unlock/prepare/start/abandon and administrator state/cancel/playtest control bodies are capped at 4 KiB; level create/import/update bodies at 256 KiB + 8 KiB, level validation at 256 KiB + 4 KiB, period bodies at 16 KiB and node bodies at 48 KiB. Submit uses the separate 4 MiB + 1 KiB envelope above. State-change and cancellation results are HTTP 200, as are successful content mutations; an accepted verifying submit is HTTP 202. Validation and admission failures use the shared 400/401/403/404/409/429/503 codes. New periods cannot overwrite an earlier period's progress, and content with formal results cannot be changed in place. Pausing, hiding, maintenance, and normal closing stop new admission as specified by the activity state; they do not silently erase an accepted game's original deadline. Administrator cancellation, ban and administrator/system deletion refund an unsettled ticket; explicit abandon and self-deletion do not. None refunds a past unlock. Terminal summaries and seed commitments expire after 30 days; per-period unlock/progress/best score and once-only reward claims persist for the account lifetime, subject to deletion and the minimum cross-account anti-duplicate reward fact. Full inputs and per-tick replay are never stored server-side.

The administrator workspace can save the clicked draft snapshot, create its immutable version and prepare a no-charge playtest without publishing a period. Starting remains manual. A local terminal result submits for server verification automatically; failed or uncertain submission remains recoverable. Version rows show sequence, save time and playtest result; the content hash remains available in details.

`GET /admin/api/limited-activities/fat-fish/playtests/current` returns the current own nonterminal playtest or null. `POST .../playtests/{id}/abandon` accepts `{"expected_revision":"1"}` and the usual idempotency key, terminating only that administrator's playtest without charge, reward or passed proof. `GET|PUT .../periods/{id}/layout` uses a separate layout revision; PUT is bounded to 32 KiB. Node condition edits preserve all/any, passed, star/count thresholds and reachable cycles; visual layout never changes access conditions.

Existing content is preserved by startup and the rc.7 conversion. Historical cleanup entry points are no longer part of the running application; retained cleanup receipts remain valid.

## 6. Debug and steward surfaces

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

The browser routes below require a currently effective level-5 or level-6 user session on the user host and the corresponding live action grant. Level 6 has all steward actions; level 5 uses per-account defaults or configuration. Reads and final mutations recheck current authority in their transaction. See the [51-action permission catalog, limits and composite checks](steward-permissions.md). Source scope and private donor projection are independent. The dedicated charity CallerKey routes are in §6.3; personal routes are in §3.4.

| Surface | Routes |
| --- | --- |
| Logs | `GET /api/steward/logs`, `GET /api/steward/logs/{id}`, `GET /api/steward/logs/export.csv`, `GET /api/steward/logs/export.json` |
| Maintenance | `GET /api/steward/maintenance`, `POST /api/steward/maintenance/enable` |
| Shared donation management | `GET /api/steward/donations`, `GET /api/steward/donations/{id}`, `POST /api/steward/donations/{id}/review`, `POST /api/steward/donations/{id}/pending-review`, `PATCH /api/steward/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |
| Users | `GET /api/steward/users`; `GET|PATCH /api/steward/users/{id}`; `POST /api/steward/users/{id}/ban`; `POST /api/steward/users/{id}/unban`; `GET|PUT /api/steward/users/{id}/steward-permissions` (actual L6 only) |
| Announcements | The eight routes in §7.3 with `/api/steward/announcements` replacing `/admin/api/announcements` |
| Failure reset | Shared batch and selection routes in §4.2 |
| Endpoints | `GET /api/steward/overview/endpoints`, `GET /api/steward/overview/endpoints/users`, `GET /api/steward/overview/endpoints/tag-rules` (`endpoints.read`); `PATCH /api/steward/overview/endpoints/tags`, `PUT`/`DELETE /api/steward/overview/endpoints/tag-rules` (`endpoints.tags`) |
| Endpoint policies | Backoff options, policy reads/updates (`charity.backoff`); loaded Cloudflare route reads (`egress.read`) |
| AI assistant | Shared `/api/steward/ai` conversation, automatic-review and memory routes in §7.8, with their corresponding grants |

Authorized stewards can review and manage charity settings across donations according to their grants and source scope. They can enable but cannot disable maintenance, and have no report, legal-hold management, account-export or account-deletion route. They cannot change cumulative donation credit or grant level 6. User mutations and managed CallerKey reads/revocations require a target below the actor's actual level. Only actual level 6 may appoint/remove level-5 trainees or configure their permissions. Known-ID donation and request-log details under an active legal hold require the corresponding live read grant and record the exact steward identity in a separate read audit. Shared management does not widen owner exports.

Both management prefixes (`/admin/api` and `/api/steward`) expose read-only `GET {prefix}/donations/{id}/keys/{keyId}/models` and `GET {prefix}/donations/{id}/keys/{keyId}/models/{modelId}/bindings`. These require the appropriate current browser session, accept numbered `page,page_size` only (default 1/20), and recheck role and donation/key parentage in the read transaction. Model rows are `{model_id,full_name,enabled,binding_count,available_binding_count}`, deduplicated across upstream bindings. Expanded bindings are `{binding_id,upstream_model_id,ord,state}`. Every still-associated binding is included, with state precedence `ended|expired|pending|feature_disabled|model_disabled|disabled|suspended|available|unavailable`. They return no secrets or private owner metadata and grant no mutation or CallerKey access.

Penalty history uses `GET {prefix}/users/{id}/penalties`, `GET {prefix}/users/{id}/penalties/{caseId}`, and `GET {prefix}/users/{id}/penalties/{caseId}/actions/{actionId}/evidence`. Lists accept `page,page_size,type=deduction|ban|charity_suspend,state=active|ended`; detail/evidence accept pagination only. They share existing administrator/steward target-user read permissions and recheck authority in the read snapshot. The list is `{data,pagination,legacy_details_unavailable}`; detail is `{case,actions:{data,pagination}}`. Cases contain `id,kind,reason_code,started_at,ends_at,ended_at,state,result`. Actions add safe request/operation links, actor ID, previous/current end times and an evidence count. Evidence is `{rules,statistics,members:{data,pagination}}`, whose members contain only violation kind, time, request link, numeric content count and link availability. No request body or credential is stored here. Ordinary user summaries and account export exclude these rule/statistical/evidence projections. Earlier restrictions keep their original reason without invented case evidence.

Authenticated pre-handler rejections create one bounded request log with `phase=pre_handler`, `rejection_stage,rejection_reason,request_method,request_path`, zero attempts, usage and call fee. Ordinary handled requests use `phase=handler`; log lists and exports accept that exact phase filter. Independent penalty ledger entries are not call fees. Applicable violation windows survive restart, retain already-executed action state and expire by their configured logical window. Active cases remain until ended; ended cases and deduction records remain 90 days. Request links stop working at the ordinary 30-day boundary. Account deletion clears user cases, durable windows and cached state.

Both management prefixes (`/admin/api` and `/api/steward`) provide `GET {prefix}/donations/badge`, with no query or body. The no-store response is `{pending_count,server_now}` with an exact decimal-string count. Only logically active donations with pending handling count; expiration is reflected even before cleanup runs.

`POST {prefix}/donations/{id}/handling/processed` accepts `{expected_handling_revision}` and an idempotency key, with a 16 KiB body limit. It returns `{donation_id,handling}`. Processing is a shared action with one winner, not a per-person read flag. Approval, model binding and key edits do not mark it processed. Handling has `state=legacy|pending|processed|closed`, its own decimal-string `revision`, and nullable `processed_at,processed_by_role,closed_at,closed_reason`. Only actor role and time are public. Terminal pending donations close automatically; closed reasons are `rejected|withdrawn|terminated|expired|member_removed|account_deleted`. Existing processed or legacy handling remains unchanged at termination.

Donation review is independent of its current terminal status. Approved donations retain `review_result=approve` after expiry or termination, including automatic approvals without a reviewer. Donations that expire or are withdrawn before review retain null review fields. A removed reviewer does not erase the original review result. Contradictory state/review combinations remain invalid.

Management donation lists accept `status=pending|approved|rejected|deleted|expired`, `handling=legacy|pending|processed|closed`, `q`, exact donor `user_id` and exact donor `discord_id`, alongside legacy `cursor,limit` or numbered `page,page_size`. Numbered lists additionally accept `review_status`. Filters combine with AND before counting and pagination and bind cursor identity. Donor `user_id` is a canonical positive signed-64-bit decimal string; donor `discord_id` is 1–20 decimal digits with no leading zero. Discord filtering resolves the current donor account; it does not search immutable request-log origins. Empty, repeated or noncanonical donor filters fail. Owner lists reject donor and handling filters. Search uses donation ID and description with the existing 128-code-point/512-byte/no-NUL limit. Each management key adds decimal-string `binding_count` and boolean `idle`; all bindings count, including disabled models. Owner DTOs and exports omit these management fields. Historical receipts add safe handling/count fields on replay without rewriting the immutable result.

`GET {prefix}/users/{userId}/donations` and `GET {prefix}/users/{userId}/donations/{id}` expose complete retained target-user donation history through a safe summary. The prefix is `/admin/api` or `/api/steward`; steward reads require `users.donations.read`, independently of donation scope, including `none`. Lists accept numbered `page,page_size` and `status,review_status,q`; detail accepts no query. Owner-hidden rows remain included with `owner_hidden_at`. Pending, approved and retained terminal records are available independently of the ordinary steward donation-scope predicate. The summary includes safe donation/use/key counts and handling state, with no key controls or credential material. Without `donations.read_all`, donor/reviewer identity remains redacted. Reads are no-store and share a five-second budget. This route does not widen key-management authority or the 400-day terminal retention period.

Administrator and steward log list/detail/export entries contain nullable `user_id` and `caller_identity: {discord_nickname,discord_id}`. Current display-name identity is present only for charity requests with a surviving caller account. Separate management fields `origin_user_id,origin_discord_id,origin_deleted,origin_unknown` preserve the request's original known identity for both personal and charity requests; an optional `history_record_id` links an authorized deleted-account record. Missing historical identity remains unknown. Both members are nullable, with UTF-8 byte limits of 256 and 128 respectively. The name uses the current guild nickname, falling back to the stored username, from the latest synced profile. Unlinked/deleted callers produce null current display identity; this does not erase the immutable original identity fields. Both roles can read known-ID details retained by an active legal hold; ordinary lists and exports remain limited to 30 days. This grants no legal-hold management permission.

Each management attempt displays its persisted nullable `endpoint_key_id` routing snapshot, including separate IDs for retries. It never returns the key secret. The logical request's `usage.charge` is the authoritative total charge shown in both list and detail. Attempt `usage.charge` remains a compatibility zero, not an independently settled fee; the page does not present it as a charge. CSV and JSON exports share the management row fields and existing 10,000-row/16 MiB all-or-error limits. CSV additionally includes `caller_discord_nickname,caller_discord_id` with spreadsheet-safe escaping. Steward export reads recheck authority in the export snapshot, and all exports are no-store.

`GET /api/steward/logs` accepts `user_id,discord_id,endpoint_key_id,error_code,status,from,to,endpoint_base_url,upstream_model,charity_model,usage_total_mismatch,phase` plus legacy `cursor,limit` or numbered `page,page_size`; `GET /api/steward/logs/{id}` accepts the corresponding `attempt_cursor,attempt_limit` or `attempt_page,attempt_page_size`. The numbered list/detail responses carry `pagination`/`attempt_pagination`. Export accepts the same filters without pagination; cursors remain bound to role, actor and filters.

Log reads have a five-second budget, including connection waits. Timeouts and canceled reads return `503 service_unavailable`; callers may narrow the filters and retry the read without resending the original model request. Read-only queries use a bounded two-connection pool, with current session, role and MFA authority checked in the same snapshot. Reads that record access to legally held diagnostics retain a write transaction.

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
| `GET /api/steward/donation-sources` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`; browser default `all`), `handling=pending`. |
| `GET /api/steward/donation-sources/{source_key}/keys` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`; browser default `all`), `handling=pending`, `idle=yes|no`. |

Handling uses the ordinary donation list/detail and the processed POST above, with `donations.handle` plus the same whole-donation scope as review and maintenance. The former `/api/steward/donation-handling` read route is no longer registered. Processing marks the whole donation, including all keys; current lifecycle, pending handling state, revision and replay checks still apply. A losing concurrent revision returns 409. Handling permission does not grant unrelated browsing, review, model creation, pricing or template actions.

### 6.3 Steward CallerKey automation

`GET|PATCH /api/steward/automation/donation-key-failure-policy` uses current steward `automation` and corresponding key-action grants, CallerKey, lifecycle, maintenance and four-global/one-per-user concurrency gates. GET accepts only `donation_id` and `donation_key_id` query parameters and no body. PATCH requires those IDs, `expected_revision` and `failure_disable_threshold`, requires `Idempotency-Key`, and returns the safe policy receipt described in §4.2. Its 30-second bound and shared mutation limits apply. This fixed failure-policy route has no model selector and uses the same whole-donation predicate as other key management: standard requires all live keys to qualify; enhanced additionally accepts a currently tagged donation; full permits the level-6 resource range. Policy management follows ordinary retained donation-management visibility; it cannot access or modify another user's private credentials. Unknown fields, noncanonical values and forbidden methods fail; stale revision gives 409, missing/unavailable objects 404 and revoked authority 401/403. Current authority and object visibility are checked before replay.

`POST /api/steward/automation/donations` atomically creates one custom endpoint and 1–100 owned keys, submits them in one donation and approves it as the calling steward. It requires `Idempotency-Key` and returns 201 `{endpoint_id,donation_id,keys:[{endpoint_key_id,donation_key_id}]}`. Explicit `connector_type` and URL are required; custom sources use the closed connector registry in [direct inference](direct-inference.md). Optional per-key settings include recurring limits and `failure_disable_threshold` (canonical U128 string, default `"10"`). Smart backoff is automatic and accepts no per-key configuration (§4.1.3). Channel IDs and ownership-confirmation fields are not accepted by this dedicated operation.

`POST /api/steward/automation/model-bindings` accepts `{charity_model_id,donation_key_ids,upstream_model_id,manual?}` and processes only the caller's own donation keys. Automatic mode requires this request's successful fresh discovery; manual mode ensures an exact entry with empty new metadata, preserving existing metadata. Repeated identical bindings succeed without reordering. The response is `{charity_model_id,results:[{donation_key_id,status,code?,message?}]}`, with `success|failed|incomplete` in input order. At least one success gives 200; zero successes gives 504 for any incomplete item, otherwise 422. This endpoint has no whole-batch replay receipt.

All charity automation routes require Bearer CallerKey, current `automation` and corresponding action grants, and final-transaction generation/ownership checks; cookies cannot replace the CallerKey. They are not listed in the user-station UI. Requests have 256 KiB and 16,384 total-field limits; responses have a 64 KiB limit. At most four personal/charity automation requests run at once, one per user, in addition to shared discovery admission limits. Creation is bounded to 30 seconds, binding to 60 seconds and each discovery to 15 seconds. Whole-operation timeout uses 504 `service_unavailable`; per-item result codes additionally include `discovery_failed`, `model_not_found` and `incomplete`. These result envelopes are distinct from the shared error envelope. See the administrator's [complete calling rules](steward-automation.md) for all fields, defaults, limits and retry semantics.

#### Authorized charity read allowlist

A current steward CallerKey with `automation` and the corresponding read grants may use these GETs under `/api/steward/automation`: `/donations`, `/donations/{id}`, `/donations/{id}/keys`, `/donations/{id}/keys/{keyId}`, `/donations/{id}/keys/{keyId}/catalog`, `/charity-models`, `/charity-models/{id}`, `/charity-models/{id}/bindings`, and `/charity-models/{id}/binding-candidates`. GET donations shares the old POST path by method; it does not change creation semantics.

Lists use only `q,page,page_size`, the 200-code-point search limit, 1/20 defaults and sizes 10/20/50/100; single objects have no query. There is no body or idempotency key. Responses are `{data,pagination}` for lists, bounded to five seconds/256 KiB, and no-store. This includes current authorized donation/member metadata, exact donation-key/upstream IDs, safe notes, source types/addresses, limits/failure state, model settings including `role_policy`, catalog source/verification facts and connection availability. It excludes donor account profiles/private notes, non-donated private resources, plaintext/ciphertext, fingerprints/review hashes, fixed authentication headers and raw diagnostics.

Level-5 automation is disabled by default. Explicit `automation` permission enables only the existing allowlist, with the corresponding action grants and current model/source scope still required. Creation/approval composes `donations.manage_keys` and `donations.review`; automated binding discovery requires `donations.discover` and binding authority. Revoking any required grant stops new work and replay; workers recheck authority before protected completion. Administrator passwords do not authorize these routes. Reads and writes share the four-global/one-per-user nonqueued admission with personal automation. The older query-based failure-policy GET remains an exception to the list-query rules.

### 6.4 Configurable steward scope

Level-5 accounts retain their actual identity, target hierarchy and individual
action grants. Their default donation scope is `standard`; separate grants
enable model creation/deletion, settings, pricing, non-mainstream targets and
other workflows. Administrator-only actions remain exclusive. See the
[complete permission catalog](steward-permissions.md).

`donation_scope` qualifies a whole donation, including its keys:

| Scope | Resource qualification |
| --- | --- |
| `none` | No steward donation browsing or operation. |
| `standard` | At least one unended, unexpired key, and **every** such key has a saved mainstream-channel source, an effective `mainstream` endpoint tag, or a current binding to a mainstream charity model. |
| `enhanced` | Standard qualification, or any key's canonical endpoint URL has a current management tag. |
| `full` | The level-6 donation resource range, subject to ordinary lifecycle/retention checks. |

Pending, paused and failed keys still participate in the all-key check; approval
or availability does not decide source qualification. Ended/expired keys do not
participate in that standard check. One qualifying key never opens a donation
whose other live key is unqualified. Enhanced tagging qualifies the whole donation,
not only its tagged key.

Browsing, approval, handling, key maintenance, discovery, binding candidates and
automation apply the same current donation predicate before counts/pages or
protected writes. There is no pending-review exception or different add-versus-
maintain source rule. The action grant and business state are checked separately.
`users.donations.read` independently permits safe retained target-user history,
including at none. `donations.read_all` controls identity/physical-resource
projection only and never expands the resource range.

`charity.non_mainstream` expands eligible charity-model targets only. An optional
`charity_model_id` selects and checks a target; it is not required to maintain
an otherwise qualifying standard donation and cannot make an unqualified
donation eligible. Bindings require both source and target authority. Shared
physical-key changes still affect all bindings, while impact summaries show only
authorized models. Credentials and private owner settings remain outside stewardship.

Final transactions and protected worker completion recheck current session,
grants, tags, scope, target authority and resource revisions. Successful retries
retain their recorded operation outcome under the applicable replay checks and
current identity projection; they do not run a new operation or grant broader
access. Removing a tag or grant affects subsequent access and pending work.

## 7. Administrator API

The administrator login shell reads anonymous `GET /admin/api/branding` on the administrator host. Its closed response contains only `site_name` and `site_logo_url`, matching the public user-site identity. It rejects query/body input and uses `Cache-Control: no-store`. `/admin/api/config` and every administrative operation retain their existing authentication requirements.

Both administrator and steward charity model projections include `route_strategy`: `ordered`, `random`, `expiry_weighted`, or `cache_balanced`. Creation defaults to `cache_balanced` when omitted; a patch may omit it to leave the setting unchanged. Existing models retain their saved strategy, including the historical expiry-weighted fallback. Explicit null, empty, and unknown values are invalid. Ordered routing uses saved binding order; uniform random gives every eligible connection equal weight. Expiry weighting uses exact reciprocal remaining lifetime, floored at 60 seconds for unexpired keys; a key without expiry receives the smallest positive weight among the expiring candidates, or equal weight when none expires. Each request freezes a candidate order without replacement; random-source failure is service unavailable and does not fall back to the first binding. Strategy changes use the model revision and existing idempotency rules.

`cache_balanced` also exposes `affinity_ttl_seconds` on administrator/steward model reads and management creates/patches. It defaults to 300, accepts integers 1–86400, and rejects null, zero and fractions; an omitted patch preserves it. An eligible same-user/same-public-model physical key is tried while its dispatch-based affinity remains valid. Otherwise the server groups eligible keys by their actual dispatched model requests in the preceding 300 seconds and draws among the least-loaded group using the same exact expiry weighting; it moves to the next group if the first cannot admit the request. This is a routing preference, not a provider cache hit or promise of lower charges. Real dispatch attempts across personal and public models share each physical key's load count, including ordered and random routes; discovery is excluded. Affinity remains separate for each owner/model and personal/public scope. Proven pre-transport failures retract their provisional count and association idempotently; actual upstream failures remain counted. Affinity and recent load survive a restart until their original expiry. Capacity exhaustion refuses new work before dispatch and charge rather than discarding still-valid affinity or load facts.

Each role also has `GET {prefix}/charity-models/{id}/binding-donations` and `GET {prefix}/charity-models/{id}/binding-donations/{donationId}/keys`, where `{prefix}` is `/admin/api` or `/api/steward`. Both retain legacy `cursor,limit` and accept numbered `page,page_size`; numbered responses include `pagination` and `next_cursor:null`. Donation menu entries contain only `{id,description,key_count}`. Key menu entries contain only `{donation_key_id,source,note}`; `source` has the same safe address, connector and masked fragments as binding candidates, and `note` is the reviewed shared note. They list approved, unexpired shared resources with at least one unbound catalog model. Cursors are bound to the role, actor, model, and donation. These menus never expose donor account identity or private endpoint/key notes and do not expand the steward's donation management permissions.

All routes below are available only on the administrator host with an administrator session. Mutations use idempotency and expected revisions as specified by §1.2, and sensitive final transactions reauthenticate the actor.

### 7.1 Session, configuration, users, and observability

| Surface | Routes and notes |
| --- | --- |
| Session | `POST /admin/api/login`, `POST /admin/api/logout`, `GET /admin/api/session`, `POST /admin/api/auth/elevate` |
| Bootstrap/config | `GET /admin/api/config`; `GET|PATCH /admin/api/site-config`; `GET /admin/api/site-config/catalog`; `PATCH /admin/api/site-config/{key}` |
| Users | `GET /admin/api/users`; `GET|PATCH /admin/api/users/{id}`; `POST /admin/api/users/{id}/ban`; `POST /admin/api/users/{id}/unban` |
| User CallerKeys | `GET /admin/api/users/{id}/caller-key`; `POST /admin/api/users/{id}/caller-key/revoke`; matching steward-session paths use `/api/steward`. |
| Usage/activity | `GET /admin/api/usage`; `GET /admin/api/activity`; `GET /admin/api/overview/endpoints` |
| Logs | `GET /admin/api/logs`; `GET /admin/api/logs/{id}`; `GET /admin/api/logs/export.csv`; `GET /admin/api/logs/export.json` |
| Alerts | `GET /admin/api/alerts`; `POST /admin/api/alerts/{id}/resolve` with optional `{resolved:boolean}` (omission resolves) |
| Maintenance | `GET /admin/api/maintenance`; `POST /admin/api/maintenance/enable`; `POST /admin/api/maintenance/disable` |

User lists additionally accept `level=1|2|3|4|5|6` and exact `user_id` (canonical positive signed-64-bit decimal string). Filters combine with AND before pagination/counting and bind cursor identity. Steward account, loan, penalty and deleted-account reads require their individual grants and retain existing read visibility. User mutations require a target strictly below the actor's actual effective level: L5 may act on L1–L4, L6 on L1–L5. Profile, inference limits, level, general/game balance, ban, permanent ban and unban actions have separate permissions and configured numeric bounds. Replacing an active ban requires unban authority; an expired ban does not prevent a new ban. Final transactions recheck authority before replay or writes. Hidden administrator targets return 404; forbidden targets return 403. Missing grants or numeric allowance violations return `403 steward_permission_denied` without invalidating the session. Ban request revisions, idempotency and 204 response remain unchanged.

`GET|PUT {prefix}/users/{id}/steward-permissions` is limited to administrators and actual level-6 stewards targeting current L5 accounts. GET returns `{revision,donation_scope,permissions,limits}`; PUT requires `Idempotency-Key` and full `{expected_revision,donation_scope,permissions,limits}`, returning the updated policy. The `donation_scope` value (`none|standard|enhanced|full`), all 51 booleans and six canonical decimal-string limits are required. Default L5 scope is standard, L6 full and ordinary accounts none. Stale revisions return 409; edits are atomic and never replace sessions. `GET /api/me` exposes `user.steward_permissions`. Stored configuration applies only at L5 and survives level changes. See the [complete schema and defaults](steward-permissions.md).

Managed CallerKey reads and revocation use the administrator host/session or the corresponding `/api/steward/users/{id}/caller-key` user-station session route, with existing same-origin, MFA and mutation safeguards. Administrators may target any non-administrator user. Current L5/L6 stewards may target only another user whose effective level is strictly lower than their own, for both metadata and revocation. Administrator accounts have no CallerKeys and return 404 through user management; disallowed steward targets return 403. No Bearer automation path is added.

The metadata GET accepts no query or body and returns `{user_id,generation,active,created_at,updated_at}` with `Cache-Control: no-store`. IDs and generations are canonical decimal strings; `generation` is nonnegative and at most 9223372036854775807. Times are Unix seconds, with `created_at:null` when no active key exists. Key text, hashes and display fragments are excluded. POST `/revoke` requires `Idempotency-Key` and exactly `{"expected_generation":"3"}`. An active key is removed and advances the generation, returning `200 {"generation":"4","revoked":true}`. An already-empty key with the expected generation returns `revoked:false` without changing its generation or timestamp. Browser sessions, account restrictions and upstream credentials remain unchanged; the owner may create another CallerKey.

Both current actor/target authority and generation checks occur in the transaction. Authority is checked before replay; an identical authorized replay returns the original receipt and never revokes a key created afterward. Reuse the same path, body and key when the result is unknown. A fresh stale-generation request or conflicting idempotency content returns 409: reread metadata and review the current target before a new intent. Exhausted active generations return `422 resource_limit_exceeded`. Revocation records the existing non-secret management mutation receipt, without a new audit collection or schema.

User limits use decimal strings or null: `endpoint_limit` is "0"–"10000"; `rpm_limit`, `light_rpm_limit` and `global_rpm_limit` are "1"–"4096"; `concurrency_limit` and `light_concurrency_limit` are "1"–"100000". Each has an `effective_` projection as a positive decimal string (endpoint limits may be zero). Existing RPM/concurrency fields select heavy requests; light charity RPM defaults to 30 and light concurrency to 15. Heavy concurrency defaults to 5. Global user RPM combines personal and charity calls across both classes and falls back to `global_rpm_per_user`. Embeddings, rerank, SystemOne and decisions are light by ingress operation. Each concurrency pool combines personal and charity calls. Administrators and strictly higher-level stewards with `users.limits` may edit the five inference limits within their configured bounds; mixed profile fields retain their existing permission requirements. Single and batch edits recheck the same target authority before replay or writes. These fields also appear in the account owner projection and export. In profile PATCH, omission preserves a value and null inherits its default. Invalid input returns 400 with the affected field and range. Manual level can be null or a JSON integer; stewards with `users.level` may assign only levels below their actual actor level, while only administrators may set level 6. The economy PATCH has `{mode:"economy",expected_revision,target,direction,amount,reason}`; targets are `balance|game_balance|donation_credit`, direction is `increase|decrease`, and amount is a positive credit string. Both wallets may become negative; stewards cannot use the donation-credit target.

The generic site-config patch rejects maintenance, announcement epoch, and all activity/game economic keys; those use their typed domain routes. User patch is a tagged operation for profile, limits/level, or economy and never returns credentials. Log exports retain their fixed privacy projections and spreadsheet-safe encoding.

The administrator list reads retain cursor mode and also accept numbered `page,page_size` with the shared metadata, except where noted: `GET /admin/api/users` accepts `is_banned=true|false`, `q`, and `level=1|2|3|4|5|6`; `GET /admin/api/usage?group_by=user` is paginated, while `group_by=site` is a complete non-paginated snapshot and rejects all page/cursor parameters; `GET /admin/api/activity` is paginated; and `GET /admin/api/overview/endpoints` accepts `q`, `tag`, and an exact positive `user_id`, combined with AND, and is paginated. The user filter selects base URLs used by that user; row totals and expanded user lists still cover all users of each base URL. Numbered endpoint-overview rows contain at most three user previews; the complete group can be read with `GET /admin/api/overview/endpoints/users?base_url=<exact>&page=<page>&page_size=<size>`, which is numbered-only and returns `{data,next_cursor:null,pagination}`. `base_url` is an exact canonical value, not a pattern.

`GET /admin/api/logs` accepts `error_code,status,from,to,user_id,discord_id,endpoint_key_id,endpoint_base_url,upstream_model,charity_model,usage_total_mismatch,phase` plus cursor or numbered page parameters; `GET /admin/api/logs/{id}` accepts `attempt_cursor,attempt_limit` or `attempt_page,attempt_page_size` and returns `attempt_pagination` in numbered mode. `GET /admin/api/alerts` accepts `resolved=true|false` plus either pagination mode. Log exports accept the same role-scoped non-pagination filters but reject `cursor,limit,page,page_size`.

The optional management `discord_id` filter is a canonical positive decimal string
from 1 through 18446744073709551615. It exactly matches the immutable
`origin_discord_id` recorded at request time, including retained deleted-account
logs. It intersects other filters, including `user_id`, and binds list counts,
cursors and exports to the same selection. Omission imposes no Discord filter;
empty, repeated, noncanonical or out-of-range values return 400. Personal log
routes reject this parameter. Do not convert it to a JavaScript number.

The `charity_model` filter on administrator and authorized level-5/6 steward log lists/exports matches a nonempty literal substring of at most 512 UTF-8 bytes against the platform model name saved at call time, ignoring ASCII case only. `%` and `_` are literal, and later model renames do not rewrite that name. All registered charity model operations can match. The predicate is identical for rows, counts, cursor identity and CSV/JSON exports and combines with existing filters and role scope; ordinary-user routes reject it.

Administrator and authorized level-5/6 steward request rows and attempt rows carry the top-level boolean `usage_total_mismatch`. The optional management filter accepts exactly `true` or `false`; omission includes both. It applies to lists, numbered counts, cursor identity and exports under the existing role scope. Ordinary-user routes reject this filter and do not expose the marker. Existing logs lack the original upstream total and migrate with no recorded mismatch; the new field does not retrospectively validate old totals. The displayed `usage.total_tokens` remains the exact sum of the normalized buckets, not the contradictory upstream total.

The optional management filter `endpoint_key_id` is a canonical positive decimal physical-key ID string (1–9223372036854775807, no leading zeros). It matches saved attempt snapshots across personal and charity calls, including failed attempts before a retry selects another key. Each logical request appears once. List, cursor/numbered pagination, CSV and JSON export use the same filter; cursors are bound to this key as well as other filters. Deleted physical keys remain queryable through retained snapshots, subject to existing log retention. Missing historical IDs cannot be inferred from key fragments, endpoint URLs or model names. Only administrators and level-5/6 stewards may use this filter; personal log routes reject it. No new request content is collected.

Administrator and level-5/6 steward log rows, details and management exports include optional `charity_model` for charity calls: the requested public model name recorded with the logical request, independent of each attempt's `upstream_model_id`. Missing snapshots omit the field. Self-use model names retain their existing private projection; ordinary-user permissions are unchanged. CSV adds the spreadsheet-safe `charity_model` column.

Management charity log rows and JSON/CSV exports also expose optional `charity_model_id` when the exact retained reservation still identifies an existing model. Each management attempt may expose the paired `donation_id` and `donation_key_id` through its exact retained dispatch claim. These decimal-string IDs support direct management links; they are not inferred from model names or physical-key snapshots. Missing or deleted relations have no navigation target. The destination rechecks current management authority and the whole-donation scope; a model link does not grant broader source access. Ordinary-user log projections and account exports do not gain these fields.

`PATCH /admin/api/site-config` accepts the closed body `{expected_revision:string,values:object}` with 1–64 known generic configuration keys and a 256 KiB request limit. Values use the catalog's scalar types. It validates the combined configuration and commits all changes with one site revision increment and one idempotency receipt. Stale revisions return `409`; invalid combinations return an actionable dependency error. Unchanged values do not advance the revision. The response is `{revision:string,changed_keys:string[]}`; clients may read the complete configuration after saving. The existing single-key route remains available.

Endpoint-overview tags support `abusive_third_party`, `community_charity`, `noncommercial_gateway` (Non-commercial aggregation gateway), `user_gateway` (User-built gateway), `steward_gateway` (Steward-built gateway), `invalid_entry` (Incorrect entry), `mainstream` (Mainstream channels and derivatives), `mysterious` (Mysterious channel) and `other_third_party` (Other third party). `tag=untagged` selects base URLs with no effective tags. `PATCH {prefix}/overview/endpoints/tags` accepts `{base_urls:string[],tag:string,add:boolean}` for 1–100 distinct canonical base URLs and the existing idempotency key. Prefixes are `/admin/api` and `/api/steward`; stewards require `endpoints.tags`. Multiple tags coexist, batches are atomic, and adding an existing tag or removing an absent tag is harmless. A batch containing an endpoint managed by a wildcard rule fails atomically with HTTP 409 `conflict`. Tags do not directly change routing, inference eligibility, bans or billing. Any effective tag includes the entire matching donation in an enhanced steward's resource scope; `mainstream` also qualifies the endpoint for the standard all-key check and automatic approval on a subsequent submission. Final operations recheck current tags and action grants.

Endpoint rows return `tags` (effective labels), `manual_tags` (retained original
manual labels) and `tag_rules` (matching `{pattern,action,tags}` rules ordered from
broad to specific). A nonempty `tag_rules` makes manual tag editing unavailable.

Persistent wildcard rules use `{prefix}/overview/endpoints/tag-rules`:

| Method | Input and result |
| --- | --- |
| `GET` | Optional `q`, `page`, `page_size`; numbered `{data,next_cursor:null,pagination}` with `{pattern,action,tags}` rows. Requires `endpoints.read` for stewards. |
| `PUT` | `{pattern,action:"add"\|"remove",tags:string[]}`; upserts one pattern. Add requires at least one known tag; remove requires an empty array. |
| `DELETE` | `{pattern}`; deletes the rule if present. |

Writes require the existing idempotency key and `endpoints.tags` grant and return
204. Use `*.example.com` to match `example.com` and any depth of subdomains;
hostname boundaries prevent matching `notexample.com`. Patterns are normalized
to lowercase without a trailing dot; protocols, ports, paths and IP literals are
not patterns. Existing and future endpoints match by hostname regardless of
protocol, port or path. Rules apply from broad to specific: add accumulates tags,
remove clears earlier effective tags, and a more specific add can add again.
Original manual tags remain stored. Deleting a rule recomputes effective tags
from the remaining rules and manual originals. Changes affect current scope but
do not rewrite approval history, bindings or donation source metadata.

A custom donation whose entire submitted resource set has mainstream-qualified
endpoints can use the existing automatic-approval path when newly submitted or
explicitly resubmitted, unless forced manual review applies. Adding a tag or rule
does not process an existing pending backlog.

Matching `GET /api/steward/overview/endpoints` and `GET /api/steward/overview/endpoints/users` require `endpoints.read` and share the administrator filters, paging and projections above. Level 6 has both endpoint actions; level 5 defaults to neither. Endpoint-policy reads/updates independently require `charity.backoff`, while loaded Cloudflare route reads require `egress.read`. No configuration write route is added.

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
| Donations | `GET /admin/api/donations`; `GET /admin/api/donations/{id}`; `GET /admin/api/donations/{id}/keys`; `GET /admin/api/donation-sources`; `GET /admin/api/donation-sources/{source_key}/keys`; `POST /admin/api/donations/{id}/review`, `POST /admin/api/donations/{id}/pending-review`; `PATCH /admin/api/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |

Review and key-management requests include expected revisions and the complete effective limits/expiry needed for that decision. Authorized donation-key projections add `donation_note` and nullable `approval_note` beside `safe_note`, without exposing a secret. Binding/candidate projections otherwise retain their safe resource boundary. Models support per-request, per-search-unit and four-bucket per-token prices, optional per-side modality prices/rewards, promotions, visibility, flattening, ordered bindings and the existing 100-result management statistic; the public 24-hour `recent_success` is a separate calculation. Administrators and authorized stewards can open a binding's key while preserving the originating model and page.

Charity model DTOs and authorized model create/PATCH requests include `transport_rule`, with the same enum, POST default, PATCH preservation and invalid/null rejection as personal models in §3.2. Existing role, revision and idempotency rules remain. It changes Chat Completions only.

Administrator/full-steward model configuration adds `is_mainstream` (initially false) and `excluded_request_fields`. The latter is a unique list of at most 32 case-sensitive top-level names, each matching `[A-Za-z_][A-Za-z0-9_]*` with 1–64 characters. Nested paths and wildcards are not supported. The protected names are `model,messages,input,stream,stream_options,tools,tool_choice,functions,function_call,response_format,encoding_format`. A logical charity request snapshots this policy and removes allowed listed fields before optional-parameter validation and protocol conversion; personal calls remain unchanged. The pricing form can fill all donor-reward prices from half the undiscounted user prices, rounding down to 0.001 credit. It changes the form only and leaves subsequent editing available.

Administrator and authorized steward model-management DTOs expose the optional `token_reserve_credits` override described in §4.1; it is a decimal credit string or `null`, and blank/inherited values do not appear in ordinary user catalog, OpenAI-compatible model, or owner export projections.

Administrator donation and source reads use the management filters and pagination contract above: donation lists accept `status`, `handling`, and `q` in either legacy or numbered mode, while source groups and source-key lists are numbered-only with `q`, `scope`, `handling`, and (for keys) `idle`.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/charity-models` | `q,enabled` plus legacy `cursor,limit` or numbered `page,page_size`; administrator model page. |
| `GET /admin/api/donations/{id}/keys` | Numbered `page,page_size` only; administrator-safe donation-key page with `pagination`. |
| `GET /admin/api/donation-sources` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`; browser default `all`), `handling=pending`. |
| `GET /admin/api/donation-sources/{source_key}/keys` | Numbered `page,page_size` only; filters `q,scope=active|all` (default `active`; browser default `all`), `handling=pending`, `idle=yes|no`. |
| `POST /admin/api/charity-models` | Complete provider/model, enabled, tagged pricing, discount and flatten policy, with optional `token_reserve_credits`; 201. |
| `GET /admin/api/charity-models/{id}` | Administrator model detail. |
| `PATCH /admin/api/charity-models/{id}` | `expected_revision` plus a non-empty partial business-field patch, including the optional `token_reserve_credits` override. |
| `DELETE /admin/api/charity-models/{id}` | `{expected_revision,confirmation}`. |
| `GET /admin/api/charity-models/{id}/binding-candidates` | `donation_id,donation_key_id,source=automatic|manual,q` plus legacy `cursor,limit` or numbered `page,page_size`; safe candidate page. |
| `GET /admin/api/charity-models/{id}/bindings` | Complete ordered bindings and `binding_revision`. |
| `POST /admin/api/charity-models/{id}/bindings/batch` | `{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}`. |
| `PUT /admin/api/charity-models/{id}/bindings/order` | `{expected_binding_revision,order}` with the exact current binding-ID permutation. |
| `DELETE /admin/api/charity-models/{id}/bindings/{bindingId}` | `{expected_binding_revision}`. |

Request-adaptation reads are `GET /admin/api/charity-models/{id}/request-adaptation` and `GET /admin/api/charity-models/{id}/bindings/{bindingId}/request-adaptation`, with matching `PUT` routes for administrators only. A steward with `charity.model_settings` can use the corresponding `/api/steward` configuration GET routes within current model scope, including a binding's effective partition sources; no steward PUT route exists. The independent canonical-string `expected_revision`, `Idempotency-Key`, five closed partitions, whole-partition `inherit|replace` binding choice, configuration GET projection, budgets and conflict rules are described in §3.1. A binding's `effective` projection shows which partition came from its model or binding and follows the same value-visibility rules: unmarked items are readable, and any protection marker hides the whole item. These six configuration GETs retain their existing owner/administrator/steward session and action gates; no CallerKey automation route is added. Only administrators can change these shared charity rules; donor-owned endpoint rules are not inherited into charity.

For administrator and authorized steward charity management, an existing binding may have `source_types:[]` when no current source confirms it. The binding remains readable and the page presents its source as unavailable. A new binding candidate requires at least one known source type (`automatic` or `manual`); empty, repeated, or unknown values are invalid. Deleting a charity binding compacts the order. Reordering preserves each binding ID and its binding-scoped request-adaptation settings and revision.

### 7.4.1 Price templates

Templates contain pricing settings, not model definitions. Administrators maintain
them; steward browser sessions with `charity.price_templates.read` can read enabled
templates. CallerKeys have no template access. Applying a template fills the open model
create/edit pricing form; it does not save the model. The next ordinary model
mutation checks its current authority, revision and values. Later template edits,
disablement or deletion never change models that previously copied it.

| Method and path | Contract |
| --- | --- |
| `GET /admin/api/charity-price-templates` | Numbered `page,page_size`, optional `q,state=enabled|disabled|all`; default state `enabled`. |
| `POST /admin/api/charity-price-templates` | Complete template; HTTP 201. |
| `GET /admin/api/charity-price-templates/{id}` | One template. |
| `PUT /admin/api/charity-price-templates/{id}` | Complete replacement plus `expected_revision`; HTTP 200. |
| `DELETE /admin/api/charity-price-templates/{id}` | `{expected_revision}`; HTTP 204. |
| `GET /api/steward/charity-price-templates` and `GET .../{id}` | Steward session with `charity.price_templates.read`; list state must be `enabled`; disabled detail returns 404. |

Lists return `{data,pagination}`. A template has `id,name,description,enabled,
revision,pricing,token_reserve_credits,discount,recurring_discount,created_at,updated_at`. IDs and
revisions are decimal strings; timestamps are Unix seconds. Names are trimmed,
1–80 Unicode code points without controls; descriptions are LF-normalized text
of at most 500 code points. There are at most 256 templates, including disabled
ones. Search uses `q` of at most 128 code points, with no NUL. Numbered pages use
the common 10/20/50/100 sizes and a five-second read budget.

POST/PUT require `name,description,enabled,pricing,token_reserve_credits,discount`;
`token_reserve_credits` must be present even when null. POST rejects
`expected_revision`; PUT and DELETE require it. Mutations use the ordinary
`Idempotency-Key`, strict JSON, current-authorization and replay contract. GETs
have no body; mutations have no query parameters. Optional `recurring_discount`
defaults to the disabled object on both POST and PUT. PUT is a complete
replacement, so omission clears any previously configured schedule.

| Copied field | Meaning and replacement behavior |
| --- | --- |
| `pricing.mode` | `per_request`, `per_token` or `per_search_unit`; replaces the form's billing mode. |
| `pricing.user_price,donor_reward` | Per-request or per-search-unit credit strings. Clear both when applying Token pricing. |
| `pricing.user_prices,donor_rewards` | Per-token four-bucket objects: `uncached_input,cache_write_input,cache_read_input,output`, in credits per million tokens. Clear both when applying request pricing. |
| `pricing.user_modalities,donor_modalities` | Optional independent input/output modality configurations in Token mode. Copy both booleans and every configured modality's four prices, including zero; null/absence clears the option. |
| `pricing.long_context` | Null/absent disables it and clears its threshold and alternate prices. Otherwise copy `input_threshold`, the active-mode user prices, optional donor reward override and modality configurations. Missing donor overrides preserve base reward behavior. Unavailable in search-unit mode. |
| `pricing.context_penalty` | Null/absent disables it and clears its values; otherwise copy `input_threshold,fine`. |
| `token_reserve_credits` | Null clears the form override to inherited global reserve; otherwise copy the canonical credit string. It remains stored but inactive in request mode. |
| `discount` | Replace all of `enabled,percent,start_at,end_at`, including false, zero and null; do not merge with stale form values. Percent is an integer 0–100; nullable times use Unix seconds and a configured start must precede the end. |
| `recurring_discount` | Replace `enabled,time_zone,default_percent,periods` completely, including false, zero and an empty array. Applying a template copies both discount configurations; it never merges them with previous form values. |

Prices, rewards, thresholds, reserve bounds and context behavior use the model's
existing validators and [context pricing contract](charity-context-pricing.md).
Inactive mode fields must be omitted or null in the complete pricing object.
For example, this is a complete request-pricing template creation body:

```json
{"name":"Request pricing","description":"","enabled":true,"pricing":{"mode":"per_request","user_price":"1","donor_reward":"0.5","long_context":null,"context_penalty":null},"token_reserve_credits":null,"discount":{"enabled":false,"percent":100,"start_at":null,"end_at":null}}
```

Template audits retain the template ID, action, previous/new revision, time and
actor link for 90 days; deleted actor identity becomes null. They do not store
price-field differences. Shared templates and audits are excluded from personal exports.

### 7.4.2 Binding selection and compact receipts

The [binding workflow contract](charity-bindings.md) defines existing-binding
markers, manual single-key batches spanning several charity models, and shared
channel presets. Those new batch routes return per-item skip results. The
per-model routes below retain their original behavior.

Both management prefixes expose `GET {prefix}/charity-models/{id}/binding-donations`
and `GET .../binding-donations/{donationId}/keys`, alongside binding candidates.
These retain their legacy cursor or numbered pagination protocols. The permitted
donation/key set and current model/source restrictions apply before counting and
paging; a supplied identifier does not grant access.

`POST {prefix}/charity-models/{id}/bindings/batch-receipt` takes the same
`{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}` as
the existing `/bindings/batch`. It returns HTTP 201 with only four decimal strings:

```json
{"model_id":"1","binding_revision":"4","added_count":"200","binding_count":"200"}
```

The request remains limited to 256 KiB, at most 256 selections and 256 total
bindings per model. Duplicate selections, existing duplicates, over-capacity or
any invalid selection roll back the whole batch. Only this compact route admits
up to 514 JSON object fields so real 200–256-item requests fit. It avoids the
64 KiB complete-response limit for long upstream IDs; the old route still returns
its complete response. Binding revisions, model/source authorization and current
session checks apply to both paths.

Keep the original key, route and ordered selection body after an uncertain
result. Reusing a key with the other route, changed contents or selection order
conflicts. A replay still checks current authority. After a successful compact
receipt, fetch bindings separately; a failed refresh does not justify adding the
same batch again with a fresh key.

### 7.4.3 Key-state filters

`key_state` accepts `available|disabled|suspended|exhausted|rate_limited|pending|
expired|ended|all`. Omission preserves `all`; an explicit empty or unknown value
is invalid. It applies to management donation source groups and source-key lists,
binding donation/key menus and binding-candidate queries. Source endpoints remain
numbered-only; binding selectors retain their existing cursor and numbered forms.
Group menus include a group only when at least one otherwise-visible key matches.
Filtering precedes count and pagination in the same snapshot.

State uses the same current projection as each key: terminal/expiry and base
unavailability win before temporary cooldown; an elapsed cooldown no longer
produces `rate_limited`. The filter never widens an endpoint's base set. Binding
candidates still require approved, unended donations, so `all` does not expose
pending or ended donations. All current source/model permission checks remain in force.
The new workbench sends `available` explicitly for its initial view.

Failure-reset `/selection` accepts the same `key_state` inside `sources` and
`source_keys` selectors. It is part of the cursor-bound selection, applies before
enumeration and appears in the selection summary; omitted `all` stays compatible.
The existing one-hour cursor, fixed key-ID boundary and per-step current-state
checks apply. A key changing state between steps may stop matching; this is not
a database snapshot across the whole multi-step operation.

### 7.5 Recurring donation-key limits

`GET /admin/api/donations/{id}/keys/{keyId}/recurring-limits` and the matching `/api/steward/` path return `{donation_id,key_id,donation_revision,server_now,rules}`. The owner-only `/api/donations/{id}/keys/{keyId}/recurring-limits` path reads the same safe rule projection for the caller's own donation. Administrator/owner GET accepts no query or body. Steward reads use the whole-donation scope in §6.4; `charity_model_id` is optional and cannot make an unqualified donation eligible. Administrators and stewards with `donations.recurring` within current scope can PUT the complete set on their management paths; ordinary owners cannot write it. Pending keys may be configured, while ended or expired keys cannot.

PUT requires `Idempotency-Key`, has a 16 KiB body limit, and accepts exactly `{expected_revision,rules}`. Up to 16 rules are replaced atomically under the donation revision; the response is `{donation_id,key_id,donation_revision}`. Rule fields are `{id,mode,interval,alignment,anchor_local,time_zone,week_starts_on,metric,limit}`. `anchor_local` is required for exact-time alignment; other modes accept omission or null. New IDs are `null`; saved IDs are canonical `qlr_` opaque IDs. Omitted old IDs remove those rules. Duplicate IDs or IDs belonging to another key are rejected.

`mode` is `reset|sliding`; `interval` is `1h|5h|day|week|month`. Reset alignment is `first_success|calendar|exact_time`; calendar and exact-time alignment disallow `1h` and `5h`. `1h` and `5h` always mean 3,600 and 18,000 actual seconds, including across daylight-saving transitions. They support first-success reset and sliding windows. Sliding alignment is `null`. `week_starts_on` is 1–7 only for calendar weeks, otherwise `null`. `time_zone` comes from the server's time-zone registry. `metric` is `calls|tokens|input_tokens|output_tokens|credits`. Limits, used, reserved and remaining are canonical U128 decimal strings; credits use at most three fractional digits with no redundant trailing zeroes and must fit U128 after conversion to millicredits. A zero limit blocks new charity calls.

Exact-time resets retain `anchor_local` as `YYYY-MM-DDTHH:mm:ss` in the rule time zone. Creating an exact rule or switching into exact-time alignment requires a future first reset; the initial partial period starts immediately. Later boundaries repeat daily, weekly or monthly from the original wall date. Short months clamp to their last day; later months recover the original day. Existing gap-forward and later-fold rules apply. Editing a limit preserves the anchor and epoch. Structural edits start a new epoch; dispatched requests retain their original settlement periods.

`GET /api/time/recurrence` and `GET /admin/api/time/recurrence` require the corresponding session and accept `anchor_local,interval,time_zone,after`. `after` is a nonnegative Unix second. The response `{transitions:[{instant,local,time_zone,offset_seconds,adjustment}]}` lists the next three resets strictly after that instant. The preview uses the rule zone and does not change any quota or contact an upstream.

Lifetime `limits.tokens`, `limits.input_tokens` and `limits.output_tokens` are independent nullable nonnegative integer strings; all configured dimensions constrain admission. Automation uses `tokens_limit`, `input_tokens_limit` and `output_tokens_limit`. The input/output limit and reserve scalar fields fit signed 64-bit nonnegative integers. Enabling a split lifetime or recurring limit requires both `input_token_reserve` and `output_token_reserve`; each may be zero, but their sum must be 1–9223372036854775807. That sum is the total reservation. Complete normalized usage replaces the reservation exactly once; missing usage uses the configured split reservation. Legacy total-only keys may keep `token_reserve` and report unknown split usage instead of guessing. Input includes uncached input, cache writes and cache reads once each; total equals input plus output. Existing totals and recurring periods survive upgrade, new split counters start at the new capture boundary, and new rules start on activation. These limits do not implement TPM.

Each rule view adds `{used,reserved,remaining,state,period_start,period_end,next_transition_at}`. Remaining is clamped at zero. State is `limited` if no capacity remains, otherwise `waiting_first_success` for an unstarted first-success period, or `available`; it is not a guarantee that a particular request fits. Period and transition fields are nullable Unix seconds. Natural periods expose the later of the calendar boundary and rule activation as their start. Sliding rules have no fixed reset; an unproven next transition is `null`.

All current recurring rules and total limits apply together to charity calls, including charity live calls. Personal calls, personal live calls, discovery and dry previews do not consume these rules. Claims reserve one call, the current per-key Token fallback and the existing undiscounted price reservation. Dispatch atomically rechecks current configuration and replaces its own old reservations. Calls already dispatched finish under their captured rules; later sends recheck the new set. Changing only a limit or display order preserves usage; changing mode, interval, alignment, anchor, zone, week start or metric starts a new counting period for subsequent sends without altering historical total usage or balances.

Recurring consumption uses the same durable billable-response checkpoint as ordinary donation accounting: upstream HTTP 200 acceptance for a stream, including empty or subsequently failed streams, or a valid non-stream response. Calls count once; tokens and credits settle with the full actual or existing conservative amount. First-success alignment follows that persisted checkpoint, including output buffered before a JSON caller response. Usage exceeding the reservation is never truncated. Without a billable response there is zero recurring consumption. If every unsent candidate is blocked by quota, the caller receives `429 rate_limited`; storage-capacity exhaustion returns `503 service_unavailable` and a deduplicated administrator alert. These failures do not count as upstream failures or disclose physical resources. Later admission failures do not replace the outcome of an earlier dispatched attempt.

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

### 7.8 Experimental AI assistant

The assistant supports dedicated driver profiles for OpenAI-compatible Chat Completions, an external Responses API and the instance's ChatGPT subscription connection. Sources are explicit: failures never switch provider or billing source automatically. Browser-session routes use `/admin/api/ai` for administrators and `/api/steward/ai` for stewards. CallerKeys cannot use these routes. Model output, tool results and proposed operations are untrusted data; they never confer permission.

Level 6 has the steward assistant capabilities. Level 5 needs `ai.chat` for its conversation, reading shared memories and reading/writing its own personal memory; `ai.review.read` permits automatic-review records. Shared-memory writes require an administrator, including when a retained steward policy contains `ai.memory.edit`. New default policies enable only `ai.chat`; earlier explicitly configured policies retain their saved grants. Configuration, complete model-request logs, usage summaries and other operators' archives are administrator-only. Ordinary users have no assistant route. Existing target hierarchy, donation scope, field grants, numeric allowances and administrator-only actions still apply to every tool and final write.

#### Chat, plans and memories

In this table, `{ai}` is either authenticated assistant prefix above.

| Method and path | Request / response |
| --- | --- |
| `GET {ai}/chat` | `{enabled,id,generation,messages,summary,job?,plans,has_older}`; the latest 100 active messages and their plans. Before a first conversation, IDs/generation are `"0"` and collections are empty. |
| `POST {ai}/chat/messages` | `{content,expected_generation,request_key}`; returns the durable job. `/compact`, optionally followed by focus text, requests context summarization. |
| `GET {ai}/chat/messages` | Optional `before_id`; returns `{items}`, at most 100 active messages in chronological order. Omit the cursor for the latest window. |
| `PUT {ai}/chat/messages/{id}` | Same body as sending; edits from that message and returns the replacement job. |
| `POST {ai}/chat/clear` | `{expected_generation}`; resets active context and returns `{}`. |
| `POST {ai}/chat/jobs/{id}/cancel` | Cancels the caller's job; returns `{}`. |
| `GET {ai}/chat/plans` | Optional positive `before_id`; omit it for the first page. Returns `{items,has_older}`, up to 100 plans in descending ID order. |
| `POST {ai}/chat/plans/{id}/decision` | `{approve,feedback,elevation_token?}`; returns the exact plan and its persisted outcome. |
| `GET {ai}/reviews`, `GET {ai}/reviews/{id}` | Numbered automatic-review page or one review, described below. |
| `GET {ai}/memories/{kind}` | `kind=chat\|auto_review`; returns `{kind,content,revision,updated_at,updated_by,writable}`; steward reads have `writable:false`. |
| `PUT /admin/api/ai/memories/{kind}` | Administrator-only `{content,expected_revision}`; compare-and-swap update returning the memory. The steward path rejects writes. |
| `GET {ai}/personal-memory`, `PUT {ai}/personal-memory` | Read or update the current operator's personal memory. PUT uses `{content,expected_revision}`; the memory adds `owner_id,writable`. |
| `GET /admin/api/ai/personal-memories` | Numbered owner page with optional `q`: exact owner ID or case-insensitive name substring, at most 128 UTF-8 bytes. Returns `{items,total,page,page_size,current_owner_id}`; items have `id,role,label`. |
| `GET /admin/api/ai/personal-memories/{owner}`, `PUT /admin/api/ai/personal-memories/{owner}` | Administrator-only cross-owner read/update; the same memory body and revision rules apply. |

Assistant IDs, generations, revisions, persisted counts and token totals are decimal strings; queue counters and configuration limits are JSON numbers. Assistant timestamps, including usage `as_of`, are UTC Unix **milliseconds**. Numbered assistant lists use `page=1..1000000` and `page_size=1..100`, default 1/20, and return `{items,total,page,page_size}`; `total` is a string while page and size are numbers. They do not use the shared `pagination` envelope or clamp an excessive page. Message and plan cursors are decimal IDs, not the opaque cursors used by other API families.

These assistant mutations use their documented request keys, revisions or persisted plan identity instead of the general `Idempotency-Key` header. Send/edit `content` must be nonblank and at most 4 MiB; `request_key` is 16–128 bytes. Keep the exact key, path and body after an uncertain response. The key is bound to the actual actor, operation, edited message when applicable, generation and content. An identical authorized replay returns the original job even after the edit archived its original message; conflicting reuse returns `409 conflict`. A new request with a stale generation also returns 409. Only one active chat job or pending confirmation may occupy a conversation.

`chat_enabled` is an administrator setting, default false. `GET /chat` first checks the caller's current conversation grant, then reports `enabled` only when that switch is on and the resolved driver has a model and its required endpoint/key or ChatGPT connection. New messages, edit/regenerate, compaction, model dispatch and pending-plan execution enforce the same gate; an unavailable chat returns `503 service_unavailable`. Turning it off cancels active chat work and supersedes unapproved plans. It preserves readable history, memories and execution records and does not change the independent automatic-review mode. Replaying a completed receipt does not restart work.

There is one current conversation per operator. Messages contain `id,role,content,created_at,active,generation` and optional `tool_calls,tool_call_id`. Jobs contain `id,kind,actor_id,actor_role,actor_label,conversation_id,generation,donation_id,state,request_id,error,created_at,updated_at,queue`. Job kinds are `chat|compact|auto_review`; states are `pending|running|waiting_confirmation|completed|failed|cancelled|interrupted`. Queue information is `{length,active,limit,position}`, with a one-based pending position and zero otherwise; it is an advisory snapshot.

Administrators can edit any active user/assistant message; stewards can edit only their latest user message. Editing archives that message and its following context, cancels active work and unapproved plans, increments the generation and regenerates from the replacement. Clear archives the active context without deleting original messages. Neither action reverses completed business operations. Context compaction keeps originals and complete recent tool-call/result pairs; editing summarized material invalidates the affected summary. Compaction uses the same request queue and token accounting.

A plan contains `id,job_id,state,operations,preview,tool_call_ids,result?,created_at,decided_at?`. Operations are ordered `{kind,target_id,input}` objects. Their corresponding previews contain `title,description,before?,after?,dangerous,requires_elevation`, including memory text before/after. Plan states are `pending|approved|rejected|failed|superseded`. A decision applies only to the exact persisted plan; retrying a completed decision reads its outcome rather than executing it again.

Model-proposed business and memory writes in conversation mode require human approval. The plan can combine existing donation review/maintenance, charity model/binding management, user profile/limits/economy/ban/delegation/CallerKey/authentication/deletion actions, shared- or personal-memory changes, and administrator-only management-history cleanup. Every operation retains its domain's authorization and expected versions. All business changes, memory changes, audit and the execution receipt commit in one transaction or all roll back. Model calls, discovery and human waiting take place outside that write transaction. Rejection or failure is recorded as a tool result so the conversation can continue; editing chat text cannot alter the actual audit.

Sensitive administrator actions require fresh elevation. Obtain it through the existing administrator elevation route and send `elevation_token` with approval; `403 elevated_required` preserves the pending plan for identity confirmation. Passwords, OTPs, session credentials and dedicated endpoint keys are never model-tool inputs. User deletion/authentication changes retain the existing lifecycle barriers and post-commit invalidation. Current session, role, grants, resource scope and revisions are rechecked before execution.

A shared memory with no saved row returns its embedded initial text with revision `"0"`; that text is the before-state for its first diff and history entry. Saved memory, including an explicitly empty body, is preserved. Memory PUT accepts at most 4 MiB. A stale `expected_revision` returns 409; clients must reread and compare rather than overwrite a newer edit. Personal memory starts empty at revision `"0"`. Stewards can access only their own personal memory; administrators can select current or retained owners, including those deleted or no longer stewards. Personal memories and their history survive those account changes. Memories and their change history remain independent of model-request log cleanup. Model-suggested memory edits require the same explicit plan decision as other writes. The conversation-only `edit_memory` tool uses `action=read|write` and `kind=chat|auto_review|personal`. Non-administrators are limited to their own personal memory; optional `owner_id` is administrator-only. Writes provide `content,expected_revision` and produce a plan.

#### Automatic review and administrator configuration

Automatic review processes only new or modified pending submissions recorded while enabled, after the existing deterministic approval rules. Enabling it never scans old pending donations, and no manual enqueue API exists. Results are bound to the reviewed material versions. `mode=off` is the default; `preview` saves advice without applying it; `auto` may apply a valid decision. Turning off stops further work and application. Changing auto to preview makes outstanding work reference-only; changing back never promotes earlier preview results into executable approvals.

A review is `{job,intelligence,result?,execution?,turns?}`. Both list and detail include `job.queue`. The result is `{decision,internal_reason,user_note,bindings}`, with `decision=approve|reject|manual`. Approval requires at least one valid binding to an **existing** charity model; reject/manual decisions require an empty bindings array. Each binding identifies `charity_model_id,expected_binding_revision,donation_key_id,upstream_model_id,native_services`. Approval and every binding succeed together or leave the business state unchanged; automatic review cannot create or alter charity models. Invalid or stale output records the failure for manual handling. Review evidence follows its associated donation/approval lifecycle and is unaffected by model-request log cleanup.

| Administrator route | Request / response |
| --- | --- |
| `GET /admin/api/ai/config` | `{revision,mode,chat_enabled,auto_template,chat_template,auto_prefill,chat_prefill,tools,tool_descriptions,search_url,search_has_key}`. |
| `PUT /admin/api/ai/config` | Complete configuration values plus `expected_revision`; optional `search_api_key` and partial `tool_descriptions` object. Returns the saved configuration. |
| `GET /admin/api/ai/driver` | `{revision,base_url,has_key,model,context_window,max_output_tokens,output_token_parameter,temperature?,top_p?,extra_parameters,concurrency,timeout_seconds}`. |
| `PUT /admin/api/ai/driver` | Editable driver fields plus `expected_revision`; optional `api_key`. Omit the read-only `revision,has_key` fields. Returns the saved settings. |
| `POST /admin/api/ai/driver/models` | Discovers `{models:[string,...]}` using saved endpoint/key settings; choosing a model is not a prerequisite. |

Configuration updates replace the submitted configuration except for the partial tool-description merge below, and use revision CAS; stale saves return 409. Omitted key fields retain the encrypted key, an empty string removes it, and a nonempty string replaces it. Reads expose only `has_key`/`search_has_key`, never saved key text. The driver key is not a user's endpoint key and is not charged through a user's credits.

#### Driver profiles and ChatGPT connection

The legacy `/driver` and `/driver/models` routes remain the Chat Completions
profile with ID `"0"`. Its concurrency setting is the shared process limit,
default 1, valid 1–128. Existing legacy configuration remains the fallback
when no binding is selected; a selected but unavailable profile fails instead
of falling back.

All routes below require a current administrator browser session. They have
no steward or CallerKey equivalent.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/ai/drivers` | `{profiles,bindings}`; profiles are `{id,name,settings}`. |
| `POST /admin/api/ai/drivers` | `{name,settings,expected_revision:"0",api_key?}`; returns the new profile. |
| `PUT /admin/api/ai/drivers/{id}` | The same profile update with its current `expected_revision`; returns the saved profile. |
| `DELETE /admin/api/ai/drivers/{id}` | `{expected_revision}`; returns `{}`. Bound profiles cannot be deleted. |
| `PUT /admin/api/ai/driver-bindings` | `{scope,actor_id,driver_id?,expected_revision}`; returns the binding. |
| `POST /admin/api/ai/drivers/{id}/models` | Read-only discovery using saved connection settings; returns `{models:[string,...]}`. |
| `GET /admin/api/ai/chatgpt` | Safe connection status and the separately scoped local usage described below. |
| `POST /admin/api/ai/chatgpt/target` | Optional `{new_account:boolean}`; returns an encrypted-import target. |
| `POST /admin/api/ai/chatgpt/import` | Local helper's encrypted envelope; returns safe connection status. |
| `POST /admin/api/ai/chatgpt/logout` | `{expected_revision}`; returns `{remote_revocation_confirmed:boolean}`. |
| `GET /admin/api/ai/codex` | Safe Codex connection and pending-login status. |
| `POST /admin/api/ai/codex/login` | `{expected_revision}`; starts device-code login and returns status. |
| `POST /admin/api/ai/codex/cancel` | `{login_id}`; cancels that pending login and returns status. |
| `POST /admin/api/ai/codex/logout` | `{expected_revision}`; disconnects the local Codex connection and returns status. |

Profile settings include `revision,driver_id,provider,base_url,has_key,model,
context_window,output_reserve_tokens,max_output_tokens,output_token_parameter,
temperature?,top_p?,extra_parameters,concurrency,timeout_seconds,
connection_revision`. Read revisions, IDs, connection revision and key-presence
flags are safe metadata, not credential text. Provider values are
`chat_completions`, `responses`, `chatgpt` and `codex`. Profile names are nonblank,
at most 200 UTF-8 bytes; model names allow at most 512 bytes. New profile
settings accept a context window of 1024–10000000, a positive local output
reserve strictly below the window and timeout 1–1200 seconds. A zero reserve
normalizes to the configured output limit or 4096. The legacy defaults remain
a 32768-token window, 4096-token output limit and 600-second timeout.

Bindings have `scope,actor_id,driver_id,revision`, with IDs and revisions as
decimal strings. Scopes are `default,auto_review,admin_chat,steward_chat`;
`actor_id:"0"` selects the workflow default, and only `steward_chat` accepts
an individual operator. Resolution is individual steward override, workflow
override, global default, then legacy profile 0. Null or omitted `driver_id`
removes an override; `"0"` explicitly selects the legacy profile. Profile and
binding writes use revision CAS. Omitted `api_key` retains a key only when
the provider stays the same; an empty key removes it, and changing provider
clears the former key unless a new appropriate key is supplied. Request
records capture the selected profile/provider and safe settings at admission;
later configuration changes do not rewrite that evidence.

For new profiles, external output limits must be below the local context
window; Chat Completions requires a positive limit, while external Responses
permits zero. Chat Completions sends one nonstreaming choice, with
`output_token_parameter=max_tokens|max_completion_tokens`, a positive output
limit, optional temperature 0–2 and top-p 0–1. External Responses uses streaming,
`store:false` and encrypted-reasoning inclusion; a zero `max_output_tokens`
omits its optional upstream output limit. ChatGPT uses the server's fixed
subscription resource, no dedicated API key, no `max_output_tokens`,
temperature or top-p. Its local window and output reserve guide compaction;
they are not account quotas or upstream hard limits. A complete Responses
stream requires `response.completed`; clean EOF alone is incomplete.
Cancellation reaches the active upstream. Provider/account changes do not
reuse incompatible native reasoning state.

`extra_parameters` is a JSON object of at most 32768 bytes. It cannot replace
transport, authentication, model, messages/input, tools or dedicated fields.
Responses and ChatGPT accept optional `reasoning` fields
`effort,summary,mode,context`, `text.verbosity`, `parallel_tool_calls`,
`service_tier` and `prompt_cache_key`; only external Responses accepts
`prompt_cache_retention`. Optional ChatGPT extras default to empty.
Reasoning effort is `none|minimal|low|medium|high|xhigh|max`, summary
`auto|concise|detailed`, mode `standard|pro`, context
`auto|all_turns|current_turn`; verbosity is `low|medium|high`.
Service tier is `auto|default|flex|priority|fast|ultrafast`, cache key is at
most 64 bytes, and retention is `in-memory|24h`. Unsupported fields fail
validation rather than being silently discarded.

One instance has one administrator-managed ChatGPT connection. The local
`nonbiri-chatgpt-login --target target.json --output connection.json
--host-id-file host-id.txt` helper performs the browser authorization and
produces the encrypted import file; no Codex runtime is required on the server
and no other application's credentials are reused. The target contains
`version,host_id,revision,public_key` and optional `client_id,subject,new_account`.
The helper envelope contains `version,host_id,revision,ephemeral_key,nonce,
ciphertext` and optional `new_account`; it is bound to the target instance
and revision. Preparing a new-account target preserves the active connection
until an accepted import replaces it. Saved credentials are encrypted and
refresh is serialized. Disconnect clears local credentials even when remote
revocation cannot be confirmed; inspect its returned boolean.

Status contains `connected,revision,host_id,account_label,expires_at,updated_at,
needs_login,official_usage,local_usage,last_limit_error`. `expires_at` is Unix
seconds and describes credential expiry, not a quota reset. `updated_at` and
the local-usage/error timestamps are Unix milliseconds.
`official_usage` currently reports `availability:"unavailable"`,
`reason:"not_exposed_by_public_api"` and an official `usage_url`.
`local_usage.scope` is `retained_completed_chatgpt_requests`; its
`window_start,window_end` bound the rolling 24-hour interval. String counts
are `requests,input_tokens,cached_input_tokens,cache_write_input_tokens,
output_tokens,reasoning_tokens,unknown_count`. These are retained site request
facts, including earlier connections; request cleanup reduces them. They do
not measure the whole subscription or a remaining quota. The independent
`/usage` totals below survive that cleanup. `last_limit_error` is null or
`{code:"subscription_sharing_usage_limit_exceeded",occurred_at}` from recognized
recent evidence; a generic 429 does not establish subscription exhaustion.

Templates are ordered `{role,content}` arrays with `system|user|assistant` roles, up to 100 messages and 1 MiB combined content each. Both include `{{memory}}`; the review template also includes `{{intelligence}}`. The chat template contains exactly one standalone `{{conversation}}` node preserving original roles and tool IDs. `{{interlocutor}}` inserts trusted current-operator JSON `{id,role,name}`; `{{personal_memory}}` inserts that operator's personal memory. The default chat template places both before the conversation. Macro expansion is single-pass; text inside memory does not become another macro or authority. Optional auto/chat prefill is appended as an assistant message. The closed `tools` map contains `donations,donation_models,review_history,charity_models,charity_binding_presets,users,site_status,web_search,propose_operations,edit_memory,management_history`; defaults enable all, while actual tools remain restricted by current human authority. Search uses the configured full endpoint URL, Bearer key and POST JSON query; external text never changes authorization.

`tool_descriptions` maps the same eleven tool names to editable text. GET returns every effective description. PUT accepts only known names and merges provided entries with the current effective map: omitted names remain unchanged, an empty string clears one description, and an omitted or null object preserves all descriptions. Each value is limited to 16 KiB of UTF-8 text after CRLF/CR normalization to LF; the total limit is 256 KiB. Descriptions are applied after tool enablement, role and workflow filtering, and a continuing tool loop reads current descriptions for its next model request. They do not alter names, parameter schemas, authorization or atomic execution.

Site-data model tools are independent of the HTTP management-page pagination above. `donations`, `donation_models`, `review_history`, `charity_models` and the `users` profile list return all authorized matching records as `{data,total}` when the result fits the tool budget; `total` is a decimal string equal to the result count. Their schemas expose optional filters, without `page` or `page_size`, and results have no `has_more`. Donation results include safe service-use details and a review signature when a current pending submission exists. Their `keys[].safe_source`, automatic-review intelligence's donation keys and each `donation_models` item carry the management `safe_source` category/effective-tag projection in §4. The catalog item's existing `source=manual|automatic` describes catalog evidence, not channel category. Catalog and review-history filters may omit the donation ID; each row identifies its donation. Charity model results include authorized bindings. User lists contain profile summaries; selecting an exact user returns the full requested object, and sensitive detail still requires that target and its existing permission. These tools do not search model-request logs. The separate administrator-only `management_history` tool is described in §7.9.

Lists are bounded to 5,000 records and 1 MiB, with an additional combined-turn budget derived from the configured context window and existing token estimate. An oversized result returns a small, retryable `tool_result_too_large` tool error with guidance naming that tool's supported filters, without partial data or an unauthorized count. The error participates in the tool conversation so the model can narrow its query. These checks do not silently shorten results or alter management HTTP pagination, permissions or saved custom descriptions. A prompt/tool configuration that cannot accommodate even the minimum error conversation fails explicitly.

The conversation-only `charity_binding_presets` tool takes required `donation_id`, optional opaque `channel_id` and optional channel-name `query`. Its bounded `{data,total}` result identifies available saved channel presets, donation key IDs, donation/channel/preset revisions and every mapped model's current binding revision. Custom-source donations can choose other channels' templates; candidates with matching canonical addresses and protocols have `channel.recommended:true` and sort first. The assistant may propose `charity.binding.apply_preset` through `propose_operations`, using the donation ID as `target_id` and `{channel_id,expected_channel_revision,expected_preset_revision,expected_donation_revision,models:[{charity_model_id,expected_binding_revision}]}`. Actual execution requires human confirmation, current `charity.presets` and `charity.manage` grants for stewards, current donation/model scope and matching revisions. Applying a template preserves source classification and donor authorization; mainstream keys retain their original source checks. Only `added`, `missing_model` and `already_bound` matrix outcomes succeed; the latter two are normal skips. Other outcomes or any later plan failure roll back the entire plan. The saved receipt includes the matrix plus string `added_count,missing_model_count,already_bound_count,skipped_count`. It does not perform network discovery or change automatic-review actions. Saved settings missing the new read-tool switch receive its enabled default; explicit choices remain unchanged.

Automatic-review intelligence includes the complete catalog for its pending submission when it fits. Initial intelligence that exceeds the hard or model-context budget is recorded as requiring manual review with a reason; no approval or binding is applied, and an oversized submission does not block later events. This does not silently remove model entries or enable reviews for previously excluded submissions.

The [embedded presets](ai-agent-presets.md) supply initial templates, shared memories and descriptions. Saved custom templates, descriptions, switches and explicit tool selections are preserved; missing entries receive built-in defaults. An exact prior built-in chat template or memory-tool description can refresh to the new built-in version. Automatic review stays off, chat stays closed and both prefills stay empty by default. Presets do not configure the driver or start model calls.

The shared FIFO limit applies to each model HTTP call, including compaction. Waiting for tools or a human decision releases that slot; another model turn joins the queue again. Actual calls also consume the global RPM and outbound concurrency budgets, with no personal RPM or credit charge. Exhausted global RPM waits for the next available window without holding an HTTP slot or database transaction. Restart recovery marks interrupted in-flight calls explicitly and does not automatically resend them; unstarted queued work can resume. Committed operation receipts prevent duplicate business execution.

#### Codex device-code connection

Codex is an independent fixed-endpoint source with one encrypted connection per
instance and no external CLI dependency. Only `admin_chat,actor_id:"0"` or
`steward_chat` with a positive, explicitly selected actor may bind this provider.
Global defaults, the all-steward default, automatic review, personal/charity
model catalogs and public API routes cannot use it. Existing AI/tool permissions
still apply to each operator.

The four Codex routes above require an administrator session. Status contains
`connected,revision,account_label,account_id,expires_at,updated_at,login?`.
Revision is a decimal string; expiry is Unix seconds and update time is Unix
milliseconds. Optional `login` contains `id,state,expires_at` and, when applicable,
`verification_url,user_code,error_code`. There is one bounded pending login in
memory; restart requires a new login. The administrator completes authorization
on the displayed official page. Access/refresh tokens and raw authentication
responses are never returned. Successfully connected credentials persist encrypted.

Codex sends `stream:true,store:false`, supports text/function-tool/native
reasoning history, and omits `max_output_tokens`, temperature and top-p.
Its extras are narrower than the API/ChatGPT settings above:
`reasoning.effort=none|minimal|low|medium|high|xhigh`,
`reasoning.summary=auto|concise|detailed`, `text.verbosity=low|medium|high`,
`parallel_tool_calls`, `prompt_cache_key` and
`service_tier=auto|default|priority`. A valid completed terminal may use already
completed matching output items when repeated output is absent; an item alone
or EOF cannot establish success. No interrupted stream is automatically replayed.

Refresh is serialized; normal renewal retains connection identity. Disconnect
or replacement invalidates the former connection and cancels related work.
The source shares outbound limits, request scheduling and administrator history.
No fallback source or broader tool authority is implied. Real Business-account
authorization and inference remain unverified; see the [connection guide](codex-agent.md).

#### Model-request history, cleanup and archives

These routes are administrator-only and separate from ordinary user request logs.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/ai/requests` | Numbered page; optional `source,kind,state,actor_id` filters. Adds decimal-string `cutoff_id`. |
| `GET /admin/api/ai/requests/{id}` | Complete saved request detail. |
| `POST /admin/api/ai/requests/cleanup` | Exactly one selection: nonempty `ids:[string,...]` (at most 1000) or positive `through_id`; optional matching filters. Returns string `deleted_count,skipped_count`. |
| `GET /admin/api/ai/usage` | `{total,last_24_hours,as_of}`, independent of retained request rows. |
| `GET /admin/api/ai/archives` | Numbered operator page with `{actor_id,role,label,updated_at}` items. |
| `GET /admin/api/ai/archives/{owner}/messages` | Optional `before_id`; `{items}` includes active and archived originals, up to 100 in chronological order. |
| `GET /admin/api/ai/archives/{owner}/plans` | Optional positive `before_id`, omitted for the first page; `{items,has_older}` includes retained plans. |
| `GET /admin/api/ai/archives/{owner}/results` | Numbered independent model-result page. |

Request filters are `source=auto_review|admin_chat|steward_chat`, `kind=model|compaction`, `state=pending|running|succeeded|failed|cancelled|interrupted` and an exact actor ID; omit a filter for all. Rows include `id,client_request_id,source,kind,actor_id,actor_role,actor_label,state,base_url,model,driver_id,provider,created_at,started_at?,completed_at?,consumed_at?,status_code,error_code,content_redacted,response_complete,usage`. Actor snapshots remain after account deletion. Detail adds safe resolved `driver_settings`, `queued_input`, the exact serialized `request_body` when prepared for dispatch, saved `response_body` and optional `response_body_base64` for non-UTF-8 content. A request cancelled before preparation may have an empty wire body while retaining its queued input. Incomplete/error/interrupted responses retain received content and an explicit completeness flag; no missing response is fabricated. Request/response capture remains bounded by transport limits. Authorization headers and the dedicated raw key are excluded; recognized key reflections are redacted.

Model-request logs have no automatic expiry. Cleanup is an explicit irreversible administrator action: freeze the list's `cutoff_id` and filters when confirming a range, or retain the exact selected IDs. Never replace that cutoff with a fresh value on retry. Newer rows are excluded. Only terminal requests whose reply or failure has been durably handed to the independent workflow records are eligible; pending/running or still-needed rows are skipped. Cleanup runs in bounded batches, so an interrupted call may already have removed some rows; retrying the same selection is safe but returned counts describe only that call.

Usage entries group by `source,kind,actor_id` and include actor role/label, string `requests`, and `usage:{input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown}`. `last_24_hours` covers the exact rolling 24-hour interval ending at `as_of`, not whole-hour buckets. Unknown or incomplete reporting is not zero usage. Historic totals and usage records survive request cleanup and account deletion.

Conversation originals, their model/tool results and execution receipts, shared and personal memories and memory history, and token statistics are independent administrator assets without automatic expiry. Request cleanup cannot erase them; clear/edit changes only the active context. Automatic-review evidence remains with its associated donation/approval records. These management assets are not added to ordinary account exports, and stewards cannot browse other operators' archives.

### 7.9 Management operation history

Management-operation history records management writes and their actual
outcomes from schema 41 onward; older events are not reconstructed. It is
separate from ordinary model-call logs, the assistant's request bodies and
conversation history. Administrator browser sessions alone may query details
or clear it. Stewards, ordinary users and CallerKeys have no history read or
cleanup endpoint, even when their authorized management writes are recorded.

Sources are `web,caller_key,ai_manual,ai_auto,mainstream_rule`. Trusted backend
context determines the source, executor, actor and authorizer; clients and
model arguments cannot select them. AI manual execution identifies the actual
approving operator and exact plan; automatic execution identifies its
configuration authorization and job, without implying individual human
approval. Human rejection remains a human action. Subjects are retained
`{id,name,role}` snapshots, not account foreign keys.

Outcomes are `succeeded,failed,rejected,no_change,partial,accepted,unknown`.
An accepted asynchronous operation is not complete; its later result extends
the same recorded operation. HTTP 2xx alone is not business success. Successful
business changes and their history commit together; failures are recorded
after rollback when storage is available. Authorized idempotent replay reads
the original result without a duplicate history entry. Reads, previews,
exports, model discovery, ordinary conversation messages/compaction/clear and
routine authentication/refresh are excluded. Administrator Codex connection
start/cancel/disconnect and its eventual login outcome are management actions;
only safe connection facts are recorded. Assistant settings and memory writes,
approved plans, asset cleanup and genuine management writes remain recorded.

Let `{history}` be `/admin/api/management-operations`.

| Method and path | Request / response |
| --- | --- |
| `GET {history}` | Filtered grouped or operation page. |
| `GET {history}/{id}` | One operation header and actual outcome. |
| `GET {history}/{id}/items` | `page,page_size`; `{items,total,page,page_size}`. |
| `POST {history}/cleanup/prepare` | `{filter,all}`; freezes and returns `{filter,all,cutoff_max_id}` without deleting. |
| `POST {history}/cleanup` | `{request_id,prepared}`; HTTP 202 with a durable cleanup job. |
| `GET {history}/cleanup/{id}/status` | The durable cleanup job and progress. |
| `POST {history}/cleanup/{id}/resume` | Requeues a failed job; HTTP 200 `{"status":"pending"}`. |

All responses use `Cache-Control:no-store`. Query strings are limited to
8192 bytes and reject unknown or repeated parameters. JSON bodies are limited
to 16 KiB and reject unknown fields. IDs, snapshot/cutoff IDs, totals, item
ordinals and accumulated counts are decimal strings. Timestamps, time filters
and group `bucket` values are Unix milliseconds; page and size are numbers.

List filters are `time_from,time_to,source,actor_id,authorizer_id,domain,action,
outcome,event_id,target_kind,target_id,group_key,search`, combined with AND.
The time interval is `[time_from,time_to)`; omit either open boundary.
Target ID requires target kind. `search` matches an exact event ID, actor
name, numeric operation ID, retained target/previous name, related object name
or ID, or changed field name. It is not substring/full-text search; older
records can match only facts actually retained.
Other string filters are exact. Domains include `management-operations`,
`endpoint-tags` and `endpoint-tag-rules`; their spelling is independent
of the corresponding HTTP path. Actor/authorizer/target IDs and search allow
192 bytes, and domain/action/target-kind/event/group tokens allow 160 bytes.

List controls are `view=groups|operations` (default groups), `page` (default
1, maximum 10000000), `page_size` (default 25, maximum 100) and
`snapshot_max_id`. Event, target, group or search filters select the operation
view. The result is `{view,operations,groups,total,page,page_size,snapshot_max_id}`;
`total` counts the selected view. Reuse the returned snapshot ID across pages
to exclude new operations. Existing rows can still finish or be cleared; this
is an insertion cutoff, not an immutable copy.

An operation contains `id,event_id,created_at,source,executor_kind,actor,
authorizer,authorization_ref?,domain,action,correlation_kind?,correlation_id?,
summary,outcome,code,targets,succeeded,skipped,failed,cleaning,target_name?`.
Items are paged separately as `{ordinal,action,target_kind,target_id,target_name,
outcome,code,changes,details}`. Changes contain `field,operation` and optional
`before,after,unit,truncated`; before/after are allowlisted strings from the
committed business values, not a copy of the incoming DTO. Existing operation
codes include `updated,cleared,created,deleted,enabled,disabled`.

New `details` contains `version:1` with optional `previous_name,state,related,
impacts,truncated`. Related samples contain `kind,id,name,state`, optional
`previous_name` and changes. Impacts contain `kind` and decimal-string
`created,updated,deleted,sampled,omitted` counts. Counts describe actual row
writes, not distinct objects or inferred downstream effects. Up to 32 related
objects are sampled; omissions remain explicit. Each change collection is
bounded to 64 fields and 16 KiB, each before/after value to 512 characters;
details are bounded to 64 KiB.

Names and changes come from the operation transaction and survive later
rename/deletion. Same-value submissions are not reported as new values. Large
text/secret changes retain only permitted change facts or length summaries.
Older records without these snapshots leave them unavailable, with no current
resource lookup presented as historical fact. History does not copy raw
request/response bodies, memory text, keys, passwords, tokens or full DTOs.

Groups contain `key,bucket,source,domain,action,actor,authorizer,outcome,
operations,targets,succeeded,skipped,failed`. The bucket is the start of an
hour in milliseconds; the requested time boundaries remain exact. Indexed
rollups support grouped browsing, with detail reads for cutoff/time edges.
Queries have a five-second budget. A budget failure returns
`422 query_budget_exceeded`; narrow filters and retry. Invalid input returns
`400 invalid_request`, inaccessible identity `403 forbidden`, missing
records `404 not_found`, and item reads during deletion
`409 cleanup_in_progress`.

#### Cleanup and assistant tools

Records have no automatic expiry. Explicit cleanup selects a nonempty
`filter` with `all:false`, or an empty filter with `all:true`. Freeze
`cutoff_max_id` before confirmation and submit that exact prepared value.
Use a stable opaque `request_id` of at most 128 permitted token bytes
(URL-safe alphanumeric, hyphen and underscore are suitable). Reusing it with
the same actor, filter and cutoff returns the same job; conflicting reuse is
invalid. These routes use that request identity, not `Idempotency-Key`.
New operations and the cleanup's own receipt are outside the frozen selection.

A job contains `id,created_at,updated_at,actor,filter,cutoff_max_id,state,
deleted_operations,deleted_items,skipped_active,error_code`; state is
`pending|running|completed|failed`. Deletion is irreversible and uses bounded
batches, including item batches within one large operation. Progress and
rollup updates persist across restarts; no full-database vacuum is performed
per batch. Active `accepted` operations awaiting genuine continuations are
skipped and counted, allowing other terminal records and later cleanup jobs
to finish. Administrators can repeat the selection after those tasks end.
`unknown` does not by itself count as active. This cleanup affects only
management history, leaving business data, conversations, memories,
model-request logs and usage accounting independent.

The conversation-only `management_history` tool is administrator-only.
It accepts `action=list|detail|items|cleanup_status`, optional nested
`filter`, `view,page,page_size,snapshot_max_id`, and string `id` for a
detail/item lookup or cleanup status. It uses the same permissions, bounds
and DTOs as these HTTP reads and does not itself create history.

An administrator can use `propose_operations` with
`kind:"management.cleanup"`, empty `target_id` and `input:{filter,all}`.
The server freezes the cutoff and previews the selection/count before one
human plan approval. Model input cannot supply the cutoff. Final execution
rechecks current administrator authority and queues a separately recorded
cleanup with the original AI approval attribution. The queued receipt is
not proof that deletion has finished; read its cleanup status.

## 8. Server-sent events

`GET /api/events` uses protocol version 1 for the closed channels `activities|rps` and frame types `snapshot|delta|gap`. Snapshot and delta frames contain a complete authoritative snapshot, not a patch. Every frame is at most 64 KiB. RPS frames are participant-only and carry the matching revision and identity epoch; a higher epoch replaces the complete cached seat/event view. Gap reasons are `process_restart|ring_expired|ring_evicted|slow_consumer` and are immediately followed by a snapshot.

`Last-Event-ID` replay works only for the same account, process, and five-minute/512-KiB ring. Event IDs and rings are memory-only. `: heartbeat` comments create no event ID. Revocation disconnects immediately. During maintenance, only a continuation with an already valid game lease may reconnect to its one bound session.

`GET /api/debug/events` is Debug protocol version 2. It has a separate session generation and event sequence, supports same-generation replay, emits explicit gap/snapshot and terminal `session_end`, sends a comment heartbeat every 15 seconds, closes after the session ends, and excludes upstream/request/header/body secrets and disallowed payload fields.

## 9. Export, privacy, and account deletion

`POST /api/account/export` requires elevated user-session authorization and returns `nonbiriapi-account-export-v31.json`, with `schema_version:31` and `generated_at`. All fields below describe that current format. Export versioning is independent of the database schema. Driver/ChatGPT/Codex credentials, assistant conversations, personal/shared memories and histories, model-request records and management-operation records remain administrator assets outside format 31.
Structured failure/delivery snapshots and restricted raw diagnostics are also
excluded; the export retains its existing safe request summaries.

`user` includes the five nullable inference limits and their effective values
from §7.1. The safe `charity` aggregate may include `usage_details` with optional
decimal-string `search_units` and `modalities`. Modalities use the same count
shape and closed classification reasons as request logs, aggregated strictly
across retained reservations; a missing report cannot be filled from another
request. This export contains no per-request price decisions, donor decisions,
model/endpoint/key identity or request content.

The top-level `steward_permissions` contains the owner's current `effective` policy, nullable `stored` configuration and retained `history`. Each history entry contains `previous,next,created_at` without editor identities. Policy changes are retained for 90 days by bounded cleanup; account deletion removes the target policy/history. Existing export bounds apply.

Connection metadata adds `connections,credentials,credential_versions,grants,discovery_sources,source_revisions,binding_targets,native_service_evidence,donation_uses`. These owner-scoped collections expose public versions and revision numbers, safe settings, review snapshots and current approved health. They exclude secret material, fingerprints, review HMACs and internal authorization identities. See [service connections](service-connections.md) for rotation, approval and deletion semantics.

Format 31 adds nullable `endpoint_key_id,last_success_at,added_count,removed_count`
to each exported discovery source. Counts are decimal strings; the timestamp is
UTC Unix seconds. Sources belong to one endpoint/key pair, and keys belong to
one service. Legacy cross-service references are split by the explicit schema-40
upgrade without changing saved binding, budget or donation-approval identities.

The top-level fields include `limited_activities`, `image_tasks` and `inactivity`. Limited activities contain the safe general/paper/brush wallet projection and own exchange receipts; the existing game wallet remains separate. Image tasks contain only local ID, state, requested/actual image counts, times, billing state and two-currency charge/refund facts. Inactivity contains safe own activity timestamps and retained execution outcomes. No prompts, complete execution parameters, images, source facts, raw errors, risk rules, private adapter/configuration or upstream task identifiers are included.

The export includes safe account, resource, wallet, activity, donation and game fields, including `game_onboarding_holds`, `loans`, `game_rankings` and `penalties`. `user` contains `charity_profile_public` and nullable `donation_credit_achieved_at`, without internal tie sequence. `game_onboarding` includes `game_key,task_key,award,completed_at,operation_id`; pending qualifications contain only `id,game_key,task_key,created_at`, never internal capacity or parent/account IDs. Loan exports contain `loan_id,operation_id,created_at`, the terms and actual balances listed in §5.9, excluding quote tokens, nonces, internal sequence and configuration revision. `game_rankings` contains `statistics_start,totals,events`: totals contain `board,window,amount,achieved_at`; events contain `game,settled_at,loss,positive_profit` with expired amounts null. Charity contributions expire at seven days and profit events at 30 days; historical totals remain until account deletion. `penalties` contains safe reason codes, actual/expected times, state/result and actions, with owner request/operation links where available; it excludes rules, thresholds, counted members and management evidence. Active cases persist; ended cases and direct deductions expire after 90 days, while request links expire after 30 days. Every collection, and the combined actions across penalty cases, is capped at 10,000; the complete JSON is capped at 16 MiB. Exceeding either fails without truncation. Bounded ranking catch-up can return retryable 503. Game settlement and safe projections share one transaction, so exported balances and qualifications agree.

Blackjack contains the owner's safe current queue, payment composition and retained history; opposing identities, funding, hidden cards and equipment are excluded. `randomness` contains only owner-accessible proofs: active items disclose commitments, terminal proofs disclose seeds and bounded streams for independent verification, including reconstructed unused deck order. Proofs do not extend the parent's 30-day access. Endpoint/key metadata, safe origins, current recurring limits, check-ins and original game payment/rule fields retain their established projections. No raw keys, ciphertext, authentication tokens, Debug bodies or internal scheduling/audit data are exported.

Secrets, ciphertext, fingerprints, request/response bodies, report data, IP material, other identities, complete pool ledgers, announcement copies, anti-collusion values, workers/checkpoints/replay internals, management notes, channel category/revision, internal source IDs, and administrator/steward audit material are excluded. If the result exceeds 16 MiB or any collection exceeds 10,000 rows, export fails atomically with 413; it is never silently truncated.

### Adaptation, continuity and activity projections

Pond-fishing safe outcomes add nullable `milk_crab_length_cm` alongside `blue_fat_fish_length_cm`; the two presentations are mutually exclusive. Exact lengths remain decimal strings. `milk_crab_chance_bps` and `blue_fish_chance_bps` are individual integer basis-point probabilities whose sum cannot exceed 10,000. The current export carries the safe milk-crab length projection; private randomness and configuration internals remain excluded.

The following fields are top-level (an excerpt, not a complete export):

```json
{
  "schema_version": 31,
  "request_adaptations": [],
  "continuity": [],
  "fat_fish": {"summaries": [], "progress": []}
}
```

The existing game projection also includes `likes.loadouts` when the account has at least one saved Custom preset. It contains the same `{slot,revision,mode,loadout:{role,harness,skills},updated_at}` items as the preset API; empty slots are omitted. This remains nested under the existing Likes game projection and adds no top-level export field.

Each `request_adaptations` entry identifies either `endpoint_id` or `model_id`, with `revision,modes,forward_headers,fixed_headers,body_defaults,body_forced,native_extension_paths` and optional nonempty `excluded_request_fields`. The mapping arrays contain only `{path,has_value,sensitive_paths?}`; no fixed or forced header/body **value**, encrypted configuration, native extension value, credential or upstream secret appears. `continuity` has only the current account's `kind,scope,window,state,expires_at` eligibility; it excludes old account IDs, Discord linkage, violation windows, abuse evidence and internal identity keys. A `fat_fish.summaries` entry has `id,period_id,node_id,version_id,engine_version,scoring_version,state,prepared_at_ms,started_at_ms,completed_at_ms,passed,stars,score_units,ticket_charge,ticket_refund,rewards,seed_commit,commitment_verified`. A `fat_fish.progress` entry has `period_id,node_id,unlocked_at,unlock_operation_id,passed,best_stars,best_score_units,best_at_ms,best_version_id`. Game times ending in `_ms` are UTC Unix milliseconds; nullable times and IDs remain null when absent. Scores and exact monetary amounts are decimal strings. No seed, full operation input, per-tick state, other player's identity or administrator playtest is exported.

Every collection has a 10,000-row bound, and `fat_fish.summaries` plus `fat_fish.progress` share **one** 10,000-row bound. The whole JSON remains capped at 16 MiB; an over-limit export returns 413 instead of a partial file. Domain adapters supply only these typed safe projections in the same authorized database transaction as the legacy balances and game state. Personal export never includes risk-scan tasks/results, source IP or header clues, alert context, worker checkpoints, request-adaptation audit history, or the minimum deleted-account/anti-duplicate identity records.

Account deletion is synchronous. It revokes credentials, removes private projections, releases undispatched reservations, transfers accepted shared work to deidentified settlement destinations, removes leaderboard/public identity, scrubs donation-private fields, and clears all four wallet balances against their corresponding external balancing accounts in one coordinated boundary. Late workers and callbacks can finish only from a persisted handoff and cannot recreate the account, wallet, credentials, identity, or private aggregates.

Deletion keeps a separate minimum security projection for administrator and steward review with `users.read`. Its original site/Discord IDs, known registration/deletion times and level, deletion source, then-effective ban and charity-pause state, four settled-before-zero wallet amounts, donation credit and automatic blacklist outcome are read-only; unavailable historical facts remain null or `unknown`, never inferred from a later account. It has no automatic expiry and is excluded from self-service export. The same Discord identity can retain minimum used-reward/once-only qualification and unexpired violation or rate-window facts after deletion, so re-registration does not reset them; those facts do not restore an old account, game progress, manual level or resource limits. A self-deletion that actually aborts an active Bidding Duel or Turn-based Battle match creates an administrator-only minimal event, cleared 90 days after it occurred. Blacklist entries and append-only reasons remain until an administrator removes the entry. Contact the instance operator for applicable rights requests; a new account cannot retrieve another account's historical management record through export.

### Collection, appearance and other games

`appearance` contains `selected,unlocked,moment,updated_at`; eligibility derives from collection ownership and pool history, while the derived `moments` catalogue is omitted. `rewards` contains personal successful receipts. `gacha` contains ticket/shard balances, collection, pity, claims, retained transactions and draw records, without retry-key digests. Model request adaptations include excluded parameter names and partition modes without saved values. `user` contains `discord_gate_policy`; personal models include `model_types` and accept `cache_balanced` as a `route_strategy`. Current personal models also include `role_policy`, `transport_rule` and the saved `cloudflare_proxy_enabled` preference; deployment mode and proxy secrets are excluded; saved Likes preset names are currently omitted from the account export. `lake_notes` contains `rules_id,profile_revision,profile,casts,entries,exchanges`, excluding private encounter/reward randomness and internal checkpoints. Safe cast entries include identity, nullable source period, rules/generation/revision, acknowledged tick, phase/pause and public motion state. Entry/exchange receipts retain exact amounts and financial references.

Export 31 also includes personal models' saved `session_headers` entries (`name,idle_seconds,format`). Generated header values and shared charity-model header configuration are excluded.

`steadycatch` contains the owner's first-clear status, retained sessions and accepted replay inputs. `gwent` uses the duel export with owner-only competitive results and the current Elo aggregate. Neither adds access to another player's private state. See [Steady Catch](steady-catch.md), [AI Gwent](ai-gwent.md) and the [lifecycle checklist](data-lifecycle-checklist.md) for routes and retention.

`personal_automation` is a top-level array (empty when there are no retained records). Each batch has `id,kind,target_id,item_count,created_at,expires_at,results`; results contain `index,status` and applicable safe `outcome,endpoint_key_id,binding_id,code,message`. Batches and their results share one combined 10,000-entry limit. It exports only retained safe batch identity/type/target/times and item outcomes; no input secrets/notes, request digests, original idempotency keys or private discovery snapshots are included. All existing 10,000-row collection and 16 MiB whole-file limits remain; over-limit export fails without truncation. Account deletion removes personal batches, their exact receipt associations, profile/current cast and resource policy, and cannot be undone by late work.

Self, administrator and inactivity deletion preserve request logs and necessary source facts until their original completion-plus-30-day deadline or applicable hold. After account deletion only authorized administrator/L5/L6 management can read unexpired logs; expired held logs retain their separate administrator or currently granted steward authorization and read audit; a new account with the same Discord ID cannot read or export the former account's logs. Immutable origin identity is not reconstructed from a later account. This is separate from the long-lived minimal deleted-account record.

Account deletion validates settled request/claim history together and detaches its account links in one batch. A naturally expired request log no longer prevents deletion. Active work still uses the existing settlement handoff; reservations, balances, retained log snapshots and rollback remain in the same deletion transaction.

## 10. Maintenance, recovery, and retention

Maintenance mode rejects new public inference, OAuth admission, ordinary pages, reports, activities, queues, and games. Health, safe configuration, logout, administrator control, workers, and work already accepted before the switch remain available. A game continuation additionally requires the same user/session and a valid pre-existing lease; it cannot start a game, queue, list, or open a new lease after losing that authority.

New Fishing batches, LinkLink sessions and RPS queues always use rules version 2; clients cannot choose an older version. Old version-1 state drains through its saved rules and funding, including after restart. Already terminal fees and game results are never recalculated under current prices or rules.

Ordinary startup keeps database ownership locking, current structure/configuration and constant-cost key checks, followed by unfinished request, ledger, game and account recovery. Historical backfills belong to the independent offline converter; catalog synchronization only fills missing content defaults. Retention proceeds in the background. Full historical and credential audits run separately with `nonbiriapi maintenance verify`, using the normal private environment and a stopped database or trusted consistent copy. Verification never initializes, repairs, migrates, starts workers or opens a listener; SQLite may create coordination files. Success prints `{"status":"verified"}`; failure exits nonzero. `maintenance schema-info --json` only describes the binary's supported schemas and loads neither environment nor database.

Recovery runs before listeners open. Recovery of unfinished API requests drains once per process before accepting traffic; periodic maintenance cannot settle live requests as abandoned after a restart. Accepted requests, donation claims, report indexing/deletion, Thursday settlement, Fishing settlement, LinkLink deadlines, and RPS matching/phases/terminal processing resume from persisted state and exact-once operation identities. Non-terminal records are not removed by age. SQLite WAL/checkpoint restart is supported for a valid Generation 2 database, including after an explicitly supported additive update. Unsupported schemas remain outside the recovery boundary.

Bidding Duel and Turn-based Battle Minigame (Test) retain complete participant-accessible terminal records for 30 days, then atomically migrate complete traces into long-term anonymous archives and remove the linked source. Deletion cancels active play and removes or de-identifies participant data in the same writer boundary. Anonymous records contain no original timestamps or cross-match identity mapping. These new-game rules do not extend the retention of the three older games.

Principal retention periods are:

| Data | Retention |
| --- | --- |
| Ordinary model-request/log identity and detailed log material | 30 days; minimal request/claim/accounting facts continue only while required by unsettled work or their domain deadline |
| Assistant full model-request logs | No automatic expiry; administrator-selected cleanup, skipping unfinished or still-needed rows (§7.8) |
| Assistant conversation originals, memories/history and token statistics | Independent administrator assets without automatic expiry; unaffected by model-log cleanup or operator account deletion |
| Assistant automatic-review evidence | Follows its associated donation/approval lifecycle; model-log cleanup does not delete it |
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

Management log and raw-diagnostic browser-session reads require a current administrator on the administrator host or a current level-5/6 steward on the user host. Steward abuse-audit routes require the corresponding `risk.read`, `risk.manage` or `access_events.read` grant; configuration writes remain administrator-only. Ordinary users and CallerKeys cannot use these management routes, including for their own requests. Sensitive reads revalidate the actor and session in the same read transaction. They do not add restricted fields to personal exports or ordinary log exports. The safe server-authored `rejection_detail` in §3 remains available through the caller's own log routes.

| Method and path | Purpose |
| --- | --- |
| `GET {prefix}/logs/{id}/headers` | Saved inbound request headers, fetched only when opened. |
| `GET {prefix}/logs/{id}/attempts/{seq}/headers` | Saved physical response-header collection for one attempt. |
| `GET {prefix}/logs/{id}/source` | Approved source facts for one authorized logical request. |
| `GET {prefix}/logs/{id}/attempts/{seq}/errors` | Failure events, including failed attempts before eventual success. |
| `GET {prefix}/logs/{id}/attempts/{seq}/errors/{event}` | Bounded original event body and capture state. |
| `GET {prefix}/diagnostics`, `GET {prefix}/diagnostics/{id}` | Model-discovery and image submit/query/discovery failure diagnostics. |
| `GET {prefix}/logs/pressure` | Anonymous global concurrency/RPM observations; administrator or level-5/6 browser session. |
| `GET {prefix}/logs/diagnostic-capacity` | Raw-body capacity and omissions; administrator or level-5/6 steward. |
| `GET {prefix}/abuse-audit/users`, `GET {prefix}/abuse-audit/users/{id}` | Risk candidates and bounded supporting observations. |
| `GET {prefix}/abuse-audit/shared-ips` | Qualified shared-address observations. |
| `GET / POST {prefix}/abuse-audit/client-rules`; `PATCH / DELETE {prefix}/abuse-audit/client-rules/{id}` | Manual client-clue rules and evidence. |
| `POST / GET {prefix}/abuse-audit/client-scans` | Start/replay a frozen background scan; list the actor's ten most recent retained scans. |
| `GET {prefix}/abuse-audit/client-scans/{id}`; `GET .../{id}/results`; `POST .../{id}/cancel` | Read progress, page matching logical requests, or stop while retaining committed results. |
| `GET {prefix}/abuse-audit/config`; `PUT /admin/api/abuse-audit/config` | Shared read, administrator-only threshold changes. |
| `GET {prefix}/abuse-audit/access-events`, `GET {prefix}/abuse-audit/access-summary` | Authenticated auxiliary events and separate anonymous counts. |

`{prefix}` is `/admin/api` or `/api/steward`. Reads use bounded filters and pages, not an unrestricted header or body search. Access-event filters include `user_id,key_generation,path_kind,status_class,from,to,lookback_hours`, with `page_size` up to 100 and a separate opaque cursor. A maximum 30-day range and coverage/drop counters keep incomplete observations explicit. A zero ratio denominator is no sample, never an infinite risk score. `coverage.last_gap_at` is null when no capture gap has been recorded. Audit users, user details, shared IPs and access reads accept `lookback_hours` (integer 1–720), anchored to server time and mutually exclusive with `from`/`to`. With no time filter the default is 24 hours, except shared IPs use the configured window.

Administrator and steward diagnostic reads with `logs.unredacted` preserve the saved bytes. Without that grant, for level 5, valid IPv4 addresses in error payloads are replaced with `[redacted]` and error-body responses include `ipv4_redacted:true`; other roles omit this flag. Valid UTF-8 bodies use text. Other bodies retain base64 encoding after the same replacement in the decoded bytes. Level-5 bodies also apply the non-mainstream DNS-identifier projection above, in either encoding; all other bytes are preserved. Copies and downloads use this role-authorized payload. Byte counts and truncation metadata describe the original stored capture. While an attempt is active, its exact claim supplies endpoint metadata for this projection. If neither a saved attempt nor its claim can identify the endpoint, level 5 receives the `[redacted]` placeholder for that body in the existing encoding. Authorized source-IP observations retain the saved address. Long previews mount when expanded and show the complete authorized text. Exact role/account changes clear prior diagnostic state and reject late responses; refreshing the same identity and role preserves current views.

Full-header GETs accept no body or query. The request response is
`{save_state,captured_at,http_status,bytes_saved,credentials_redacted,ipv4_redacted,headers}`;
each header is `{name,name_encoding,value,value_encoding}`. An attempt returns
`{save_state,responses}`, each response adding a one-based `response_index` to the
same header view. Repeated values remain separate. Encoding is `utf-8` or
`base64`; base64 preserves non-UTF-8 bytes rather than substituting text. Values
remain untrusted data. These are Go-parsed HTTP headers, not a packet capture:
wire casing/order and transport framing are not promised. The request includes
Host; response collections preserve separate discovery pages/polls when captured.

Authorization, cookies and recognized credential-header values are replaced by
`[redacted]` before encoding/storage. This is name-based credential protection,
not a promise that arbitrary upstream text contains no secrets. Administrators
and stewards with `logs.unredacted` receive all other saved bytes. For L5 without that grant, IPv4 addresses in both header names
and values are replaced before text/base64 encoding; the separate approved source
IP observation keeps its existing address. Ordinary callers have no full-header
route, even for their own requests. The new GETs reuse request/attempt authority,
retention and held-log access, and send `no-store`.

Each physical header capture is bounded at 1 MiB; a response collection supports
at most 20 physical responses and 8 MiB of decoded/encoded collection material.
`save_state` is `saved|too_large|unavailable|capacity_exhausted|not_recorded`;
absence or an over-limit collection is explicit, not a fabricated empty capture.
The independent `request_header_budget_mib` defaults to 1024, accepts 1–65536,
and counts encoded saved payload bytes including held headers. At capacity new
collections are omitted atomically; existing unexpired data is not evicted.
Lowering the budget pauses new storage until space is available. The diagnostic
capacity endpoint continues to describe the separate raw-error store; it does not
return a header-capacity subobject.
Headers follow their request root's original 30-day deadline or approved hold;
account deletion preserves only the same authorized deidentified request graph.
They are excluded from ordinary list/detail/export payloads and personal export.
The normalized source-fact origin fields below still omit paths; the separate
full-header capture can retain complete Origin/Referer values and other headers.

Original failure capture covers personal/charity calls, retries, explicit streaming error events, model discovery and the image activity. It captures the HTTP-decoded error entity or error event, not TLS/chunk/compression wire bytes. Each body is capped at 1 MiB; truncation is explicit. JSON has formatted and original views, other text is rendered as text, and non-UTF-8 bytes use a base64 representation without pretending replacement text is the original. Local refusals have the site's safe reason and no invented upstream body. Successful response bodies, normal stream content, request bodies and Authorization headers are not actively logged. An upstream can echo inputs or credentials in an error; those original bytes may be retained in this restricted store.

New image-discovery diagnostic entries optionally include `dispatch:{method:"GET",url,request_body:"",content_type:"",dispatched_at}`. This is the dispatched snapshot, not a URL reconstructed from later configuration. GET has no request body. Historical entries without a snapshot leave `dispatch` absent. The retained response, HTTP status, content type and omission/truncation state use the existing diagnostic fields and raw-body limits. No saved Authorization header or image-generation request payload is added.

Image transport failures before a valid HTTP response produce a synthetic diagnostic (`synthetic:true`, null `http_status`, content type `application/vnd.nonbiriapi.image-diagnostic+json`) with `{category:"image_transport_failed",reason,original_body_saved:false}`. The fixed reason is one of `dns_failed`, `tls_certificate_invalid`, `tls_protocol_failed`, `connection_failed`, `request_timeout`, `request_cancelled`, `redirect_blocked` or `request_failed`; raw transport errors, peer bytes and credentials are not saved. Discovery uses `upstream_failed` for these failures, or `execution_timeout` for a deadline, while rejected catalog data remains `invalid_result`. Image submission keeps its existing uncertain-receipt protection and is never automatically replayed. All image profiles negotiate HTTP/1.1 over TLS with normal certificate verification.

Ordinary diagnostics and source facts expire after 30 days; existing explicit legal holds may retain their approved object graph longer. `request_error_body_budget_mib` defaults to 1024 and accepts 1–65536 whole MiB. It counts raw payload bytes, including held bodies, rather than SQLite/WAL overhead. At capacity, a new raw body is omitted in full and marked `capacity_exhausted`; safe summaries and API service continue. Existing unexpired bodies are not evicted. Lowering the budget below usage pauses new raw-body storage until capacity is available.

Source facts contain normalized full IP and provenance (`direct_peer`, `trusted_forwarded` or `peer_fallback`) plus approved client clues. User-Agent is limited to 2,048 bytes. Origin, Referer and HTTP-Referer retain only an origin, with user info/path/query/fragment removed, at most 512 bytes each. X-OpenRouter-Title and X-Title remain separate, at most 256 bytes each. X-Stainless-Lang, X-Stainless-Package-Version, X-Stainless-Runtime and X-Stainless-Runtime-Version are each limited to 128 bytes. The combined projection is at most 8 KiB and records truncation/duplicate quality. Untrusted forwarding headers cannot establish identity; fallback addresses are excluded from shared-IP findings. Self-reported headers can be absent, forged or rewritten by a relay.

The management rule editor provides populated, suspected-client presets: Tavo (`user_agent` prefix `Tavo/`), New API (`openrouter_title` equals `New API`) and One API (`legacy_title` equals `One API`). Title matching defaults to case-insensitive. New API and One API presets do not require a particular HTTP-Referer origin. These headers occur only on some forwarding paths; forks retaining them may match, while renamed or hidden markers require separate evidence-based custom rules. Presets are drafts until explicitly saved and enabled; they neither rewrite existing rules nor establish abuse. A preset alone does not enable the separate administrator-controlled automatic-ban binding below.

Client rules combine up to eight conditions with AND; enabled rules are alternatives. Existing `equals`, `contains` and `prefix` conditions use `value` and `case_sensitive`. An observed-IP condition can instead use `{"field":"effective_ip","operator":"ip_in","values":["192.0.2.1","2001:db8::1"]}` to match any listed address. For `ip_in`, `value` must be empty or omitted and `case_sensitive` false or omitted; other operators do not accept a nonempty `values` array. The server accepts 1–64 IPv4/IPv6 addresses per list, trims surrounding whitespace, normalizes IPv6 and IPv4-mapped addresses, and removes duplicates while retaining first-seen order. Domain names, CIDRs, ports and zone IDs are rejected. A match requires a complete, valid `direct_peer` or `trusted_forwarded` source IP; the rule's remaining conditions must also match. The editor accepts newlines, commas or spaces between addresses. Rule bodies remain limited to 32 KiB and stored conditions to 16 KiB. Existing single-value rules and frozen scan snapshots retain their meaning; list conditions use the same matching logic in audits, scans and explicitly enabled automatic-ban bindings.

### Global pressure

`GET /admin/api/logs/pressure` requires a current administrator session on the administrator host. `GET /api/steward/logs/pressure` requires a current steward session with `pressure.read` on the user host. Current session, account, role and MFA rules are rechecked in a read transaction. Ordinary users and CallerKeys cannot use either route. Requests accept no query or body; responses are `Cache-Control: no-store`.

```json
{
  "rpm": {"current":12,"limit":600,"peak_24h":80,"peak_all":120},
  "concurrency": {"current":2,"limit":50,"peak_24h":8,"peak_all":15},
  "observed_at":1792540800,
  "started_at":1792454400,
  "sample_seconds":5
}
```

All values are JSON integers; times are UTC Unix seconds. `rpm` counts the site-wide inference admission window; `concurrency` measures occupancy of the shared outbound gate. These metrics exclude ordinary page HTTP visits and do not count inbound connections. Each metric reports the current observation, current configured global limit, approximate last-24-hour peak and maximum observed since `started_at`. The server samples every five seconds and retains bounded minute maxima. The edge minute can extend the 24-hour window by less than one minute. Peaks are observations, not an exact request history; no history is reconstructed from old logs. Admission counters record interval peaks in memory, without additional per-request writes or request-log scans.

Minute samples and all-time maxima persist about every 30 seconds and are restored after restart. Abrupt shutdown can lose unflushed observations, normally the last approximately 30 seconds and longer if persistence fails. Downtime adds no observations. The observation start and saved maxima do not reset at process restart.

The management log page refreshes every 15 seconds while in the foreground. Current/24-hour/all-time values use dark/medium/light fills, numeric labels and a legend, with the configured limit as the scale. Historical values above a reduced limit remain visible numerically while bars stop at full width. A transient refresh failure retains the last values with a stale warning; an initial failure displays unavailable. HTTP 401/403 hides the panel and stops its interval polling.

### Redemption-code bans

Authenticated code submissions use the existing automatic-ban system. By default,
five counted failures within a rolling 24 hours cause a 24-hour account ban.
Unknown, malformed and already-claimed codes count immediately. An existing code
that is disabled, outside its dates, exhausted or ineligible grants one uncounted
business refusal per account/code/window; later new submissions count. Successful
claims do not reset the window. Capacity errors and failed authentication do not
count. Retries with the same idempotency key and body replay the original outcome
without counting again. Rule configuration is listed in [configuration](configuration.md),
and request/retry details in [rewards](rewards.md).

### Automatic bans from new model requests

Administrator and steward rule create/update bodies with `risk.manage` may include `auto_ban:{enabled,duration_seconds}`. Both fields are required when the object is present; `duration_seconds` is an integer from 1 through 315360000, or `null` for a permanent ban. Omission creates no binding on a new rule and preserves the binding on update; explicit `null` removes an existing binding and is invalid during creation. The binding and rule share the rule revision. At most 100 rules can have a binding, including disabled bindings. Both roles can create, change, enable, disable or remove a binding, and edit/delete a bound rule. Session authority, level, ban state and revisions are rechecked in the write transaction; ordinary users and CallerKeys cannot manage rules. Steward changes retain actual actor level and applicable account-mutation authority. Binding changes record the acting user and rule changes retain the acting role.

A new, classified personal or charity model call can trigger an enabled rule and enabled binding. Existing bindings cover both kinds without reconfiguration. After authentication, request parsing and logical-model access checks, the automatic-ban check runs before content-length, credit-balance, candidate availability and Debug capture. A matching request cannot avoid the ban by having insufficient credits, short content or an active Dry session. The server rechecks rule revisions and account authority in the effects transaction before dispatch: the first matching call is rejected without an upstream request or charge, sessions/calling credentials are revoked and game cancellation/refunds use their existing transactional hooks. Multiple matches and an existing active ban take the latest expiry, with a permanent ban taking precedence. Historical scans and discovery calls do not trigger this action. Unauthenticated, malformed and inaccessible-model calls retain their entry checks; charity-only short-content and RPM penalties are unchanged.

The saved account-ban reason uses the configured rule name, for example `识别到违规第三方客户端特征：Tavo`. Names come from the final transaction's rule snapshot, with whitespace normalized; distinct matching names are sorted and joined with `、`. A long list has an omitted-name count within the existing 1024-character/4096-byte reason budget. Existing active reasons are preserved; if no combined reason fits, the prior reason takes precedence. No raw client header is copied into the reason. Renaming a rule later or replaying the same logical request does not rewrite an existing reason. The reason does not itself add a Discord blacklist entry; existing account-ban and self-deletion blacklist policies remain separate.

`GET /admin/api/abuse-audit/client-rule-ban-receipts/{request_id}` is administrator-only and returns `{request_id,user_id,source,rules,banned_until,created_at,expires_at}`. `user_id` is a decimal string, `source` is `client_rule`, `rules` contains every matched `{id,revision}` (at most 100), and `banned_until` is a Unix second or `null`. The [request admission standard](request-admission.md) requires original inbound client clues to be checked before header/body adaptation or business rejection. Receipts last 90 days, are removed with the account, and enforce one set of effects per logical request. Missing/expired receipts return 404; unauthorized roles receive 403. Rule identifiers remain management-only and are not added to ordinary caller errors or personal exports.

### Persistent client scans

Creation accepts `{"request_token":"unique_attempt_token","lookback_hours":24,"kind":"total"}` or an explicit `from,to` epoch-second window instead of `lookback_hours`. Optional `model` is exact; `kind` is `total|self|charity|unclassified`. The range is at most 30 days. `request_token` is 16–64 ASCII letters, digits, `_` or `-`; retry the same token and identical body after a lost response. A different body with a retained token returns 409. Tokens expire with their scan; a token still awaiting expiry cleanup also returns 409. Creation returns 202, including an immediate empty/completed result. Current actor/session authority and CSRF are required; the token is not a credential.

Each `scn_` task freezes the resolved window, filters, enabled rule revisions and highest recorded log ID. Two shared workers process client scans in transactions of at most 100 candidate source records, then apply kind/model/rule filters. Shutdown or cancellation preserves the last committed checkpoint; restart resumes. Shared scan limits are two unfinished tasks per actor, 20 unfinished and 200 retained tasks in total; capacity conflicts return 409. Each task lasts 24 hours and retains at most 100,000 unique logical-request references. Hitting that limit yields `limited` with `result_limit`; repeated storage failures yield `failed` with `scan_failed`, preserving committed results. No enabled rules produces an immediate completed scan with zero matches.

Scan metadata is `id,state,reason,from,to,kind,model,candidates,scanned,matched,rule_count,created_at,updated_at,expires_at`. States are `queued|running|completed|cancelled|limited|failed`; candidate/scanned counts are decimal strings, other counts are bounded JSON integers. Candidate counts cover the frozen source window before kind/model filtering. Result reads accept only `page` (1–2147483647, default 1) and `page_size` (10/20/50/100, default 20), returning `{scan,items,page,page_size,total_items,total_pages}` with decimal-string page/count fields. Out-of-range pages clamp to the last page. Totals describe currently retained matching references and remain provisional while scanning. Source deletion/retention may reduce them. Each result uses the existing source projection and frozen rule evidence, capped at 20 match details per request and 100 per page.

Cancellation accepts `{}` and is idempotent. Tasks are private to their creating actor and role, even between administrators; another actor gets 404. Permission loss invalidates all of that actor's scans, including completed ones, and restoration does not revive them. Account/source deletion removes related references; expiry and revocation cleanup use bounded batches. These transient management records are excluded from personal exports, never collect request bodies and do not change existing source retention or create automatic penalties. Legacy cursor-based client observations remain compatible.

### Full-range audit navigation

For current administrator or appropriately granted steward sessions, the following operations are registered under both role prefixes. The deployed `/client-scans` path above remains compatible; its `kind` is a call-kind filter and its result pages still accept 10/20/50/100. The new `/scans` path has a separate four-value result `kind` and only 20/50/100 page sizes.

| Method and path | Wire contract |
| --- | --- |
| `POST {prefix}/abuse-audit/scans` | Strict `{request_token,kind,from?,to?,lookback_hours?,call_kind?,model?,signal?}`. `kind` is `client_hits`, `users`, `shared_ips` or `user_ips`; `call_kind` is `total`, `self`, `charity` or `unclassified`. `signal` is `rpm` or `concurrency` only for `users`; `model` is only for `client_hits`. Returns HTTP 202 with the task object. Retry an uncertain creation with the same token and identical body. |
| `GET {prefix}/abuse-audit/scans` and `GET .../scans/{id}` | Actor-private recent tasks or one task. Each task has `kind,call_kind,status,scanned_candidates,matched,coverage,truncated_reason,changed` plus frozen window/filter/revision and prior compatible metadata. |
| `GET {prefix}/abuse-audit/scans/{id}/results?page=1&page_size=20` | `{scan,items,page,page_size,total_items,total_pages,coverage}`; page/count values are decimal strings. Items are source requests, user summaries or shared-IP groups according to `kind`. Only fully formed, published group rows count. Results while running are provisional and are not represented as a final total. |
| `POST {prefix}/abuse-audit/scans/{id}/cancel` | Strict `{}`; returns the resulting task. Already committed result pages remain readable until expiry, subject to source/privacy invalidation. |

Each task freezes a UTC interval, the highest source-log ID and relevant filters/rule or threshold revision. Two scan workers claim different tasks and process bounded batches (at most 500 candidates or two seconds). Limits are one million scanned candidates and 100,000 results per task, two running tasks per actor, 20 globally, 200 unexpired tasks globally, and 24 hours of task/result retention. A reached cap is explicitly partial/limited and includes a reason; it does not imply a complete scan. Cancels, restarts, permission loss, source retirement and account deletion respect committed checkpoints. A removed contributor removes associated result links and marks coverage changed; a pending group never becomes a stale published result. Scans neither create penalties nor prove a client identity.

The six navigation entries are client matches, user summary (all/high RPM/high concurrency), user request detail, shared IP, access events, and client rules. They support 20/50/100 page sizes, current/last/direct-page navigation, explicit total or range coverage, URL-preserved filters and a return to the same list state. `GET {prefix}/abuse-audit/users/{id}` and `GET .../access-events` use bounded synchronous pages with a fixed `from,to` and source watermark; changed source counts are reported rather than inventing earlier rows. Rules use a current content revision and reset to page one if it changes. Existing list/cursor forms remain available where documented; page/cursor modes cannot be mixed. All underlying target and return-page reads recheck the current administrator or steward session and the corresponding `risk.read`, `risk.manage` or `access_events.read` grant; CallerKeys gain no audit access.

Risk signals include both personal and charity calls. RPM and concurrency are measured at authoritative admission, associated with the logical request rather than retries; pre-parse observations remain unclassified rather than guessed. Defaults flag at least 80% of the effective user limit for five complete consecutive minutes. Concurrency retains actual occupancy time and peak counts. New total-minute observations additionally store nullable `saturation_micros_millis`: the time integral of the larger light/heavy pool utilization, scaled by 1,000,000 and capped at full utilization. Its range is 0–60,000,000,000 per minute; explicit zero is observed idle. Risk thresholds compare this integral with the configured percentage of a full minute, so one saturated pool remains visible. Historical null values use the previous occupancy-time/effective-limit calculation. Personal/charity category rows do not infer separate pool utilization. Missing coverage, quota changes or unlimited quotas break a sustained chain. Shared-IP defaults are at least three distinct Discord identities over rolling 24 hours. `user_ips` flags one Discord identity using at least three distinct trusted API addresses in a rolling 24-hour window, aggregating its old and current accounts. Administrator configuration adds `user_ip_window_hours` (1–720, default 24) and `user_ip_min_ips` (2–1000, default 3). Administrators may change the thresholds; full stewards may read them. Client rules use bounded equals/contains/prefix comparisons, at most eight AND clauses per rule and OR across rules, with suspected/confirmed status and evidence notes or links. Rules do not execute scripts or regex. A historical scan match is a review lead, not identity proof, and creates no penalty. The separate administrator-bound live model-call action above and existing configured anti-abuse penalties continue independently.

Auxiliary observation preserves existing responses for exact user-host requests: `GET /v1/models`, `GET /dashboard/billing/subscription`, `GET /dashboard/billing/usage`, their `/v1/dashboard/billing/...` variants, `GET /v1/sub2api/billing`, and `HEAD /v1/chat/completions`. Only a valid CallerKey associates an event with its actual user and generation; rotation/revocation/deletion clears key attribution, and queued writes revalidate it. Unauthenticated, invalid or barred requests contribute only anonymous minute counts, without an IP/header identity record. No query, request body, cookie or Authorization value is captured. These probes do not refresh activity or consume user model-call RPM. The queue is capped at 4,096 and retained authenticated events at one million; dropped coverage is visible. Image background polling and model discovery likewise do not inflate a user's model-call count.

Multi-address/shared-address scans use only canonical API sources with `direct_peer|trusted_forwarded` provenance, never `peer_fallback` or website sign-in addresses. Classified successes, failures, refusals and unclassified API observations remain distinct. Rolling windows include exact threshold endpoints; the selected scan interval remains `[from,to)`. A deleted account does not invalidate still-retained source references; true source expiry/hold release removes references and marks coverage changed. Same-Discord accounts count once on shared IPs. Tasks/results still expire after 24 hours and never issue penalties.

### Selected-account call-IP audit

Current administrators and stewards with `risk.manage` may start `POST {prefix}/abuse-audit/scans` with `kind: "user_ips"` and optional `user_id` as a canonical positive decimal string. For example:

```json
{"request_token":"example_target_ip_scan_01","kind":"user_ips","user_id":"123","lookback_hours":24,"call_kind":"total"}
```

The target must exist on first creation. Malformed IDs or a target on another scan kind return 400, an absent target 404, and reuse of a token with different conditions 409. Omission retains the existing site-wide identity/window scan.

Targeted scans use the exact original account ID and the selected [from,to) interval. They list each trusted call IP even if only one is present, without merging re-registered accounts or applying multi-IP risk thresholds. Website sign-ins and untrusted fallback addresses are excluded. Results carry the target `user_id`; each item uses the existing shared-IP structure with `users: 1`, call counts, first/last times and per-call-kind associations. Existing task ownership, pagination, limits, 24-hour expiry, source retirement and partial/changed coverage apply. Missing source evidence is reported rather than guessed. Scanning applies no penalty.

## 12. Economy and inactivity administration

Inactivity policy accepts optional `execution_time` in `HH:mm` form. Empty or omitted keeps the existing per-account due-time calculation. A configured clock uses the authoritative site timezone; configuration responses include `site_timezone_offset_minutes` when a clock is set. The timezone must be configured first and is pinned through the existing timezone freeze when the timed policy is enabled. Decay and protective bans become due at the first selected time at or after their inactivity threshold and grace deadline. Later decay uses the actual processing site's date plus `interval_days` at the selected time, so a late minute batch does not skip the following day. Changing the clock on an active rule grants at least seven days of grace. Execution remains bounded minute batches with at most one catch-up decay.

The preview body accepts `page` as a positive decimal string and `page_size` as 10, 20, 50 or 100 (default 20 in page mode). It returns `pagination: {page,page_size,total_items,total_pages}` using the same metadata types as other numbered collections. Count and rows use one transaction, with out-of-range pages clamped to the last page. Legacy cursor requests remain supported when `page` is absent; cursor and page mode cannot be combined. Preview records a management audit but never changes balances or applies bans.

Execution records (`/runs`) and configuration/preview audits (`/audits`) accept the same numbered query parameters when `page` is present. They return accurate totals and rows from one snapshot, ordered by descending insertion order, with out-of-range pages clamped to the last page. Numbered responses have a null `next_cursor`. Without `page`, the existing cursor protocol retains its default 100 and any integer `page_size` from 1 to 100. Supplying both `page` and `cursor`, even an empty cursor, is invalid. The two history panels keep independent page and page-size URL parameters and support refresh, previous/next, direct page selection and retry. Retention remains 400 days with account associations removed after 90 days.

Economy-audit metadata includes `detail_retained_from` for the 30-day transaction window. `/operations` excludes older details even when internally held. Non-personal aggregates remain available after ledger compaction. Historical summary, series and channel query edges that intersect compacted hours expand to complete site-hour boundaries; `range_adjusted: true` identifies an adjustment and metadata `from,to` reports the actual range. Recent ranges retain second precision.

Only administrators may read `/admin/api/economy-audit/summary`, `/series`, `/channels` and `/operations`. General credits, game credits, draft paper and brushes are separate assets, never summed. Reports distinguish new issuance/permanent retirement from user income/expense/internal transfers, so refunds, releases and pool movements do not create false issuance. Stock separates positive available user balances, frozen balances, pools and platform holdings; negative balances are shown separately. Hour/day series use the site's business time zone. Reconstructable ledger history carries a coverage start and explicit gaps; no opening balance is invented.

Only administrators may `GET / PUT /admin/api/inactivity-policy`, `POST /admin/api/inactivity-policy/preview`, or read `/admin/api/inactivity-policy/runs` and `/audits`. Master policy, decay and protective ban start disabled with no inactive-day threshold. When enabled, bounded periodic execution uses current policy, role, activity and balances in one transaction. Administrators and levels 5/6 are exempt; donors are not. Qualifying activity is a successful login/API call, check-in, benefit claim or effective game/activity action. Passive rewards, page/background polling, failed actions and idempotent replays do not refresh activity.

Decay and protective ban have independent inactive-day thresholds. Positive available general/game balances can have separate percentage or fixed-amount decay, periods and retained floors. Frozen funds, negative balances and activity currencies are excluded. First enabling, re-enabling or tightening a policy grants at least seven days; existing accounts start observation at upgrade. A missed series of decay periods catches up at most once, and a simultaneously due protective ban wins without decay. Protective bans retain the account and balances and stop later decay; an administrator can unban and restart observation. Only this protective reason allows existing donated keys to remain usable with normal rewards while the donor's login and API remain blocked. Ordinary bans keep their existing donation restrictions.

## 13. Limited activities and image generation

Existing activities remain permanent activities. Limited activities have
separate pages, administrator-only opening, visibility, and pause settings,
and module-specific configuration. Hidden means omitted from the directory, not
inaccessible by a known link. An unconfigured opening period means closed.
Reopening preserves balances and history.

The picture-book activity uses a built-in integration for its supported upstream
image-service specification. Administrators configure the endpoint and key,
enable or disable discovered models, and set each model's paper and brush
prices. Model capabilities and supported parameters are discovered
automatically; the administration API does not accept manual protocol,
request/response, mapping, or parameter-catalog definitions. Model settings
use `expected_revision`, `enabled`, and `price`, with optional `pricing` for
default, tier, and exact width/height prices.
`GET /admin/api/limited-activities/picture-book/models` exposes read-only
`capability_readiness`, `capability_issues`, `capability_revision`,
`pricing_revision`, `parameters`, `combinations`, `size_capability`,
`catalog_type`, and `missing` facts. `capability_issues` is always a safe
issue array. Unconfigured capability and pricing revisions are `"0"`;
unsupported new metadata remains pending. The public
`GET /api/limited-activities/picture-book/models` response provides local model
IDs, prices, and automatically supported parameter and size choices. Full
routes and field behavior are in [the image activity guide](image-activity.md).
Public models use local identifiers; real model IDs, the endpoint, key,
internal task identifiers, and original failures stay out of ordinary user
responses. This activity uses its own configuration and balances. CallerKey
image generation is a separate model operation described in §2.5.

The Renge cover and four-frame waiting animation are presentation only. The
animation advances every two seconds per frame and respects reduced-motion
settings, manual pause, and background-tab pause. Browser refresh remains on its
existing schedule. The server schedules the first upstream status query six
seconds after the receipt and each ordinary later query six seconds after
handling the previous response; a valid bounded
`Retry-After` requesting a longer wait may extend either delay. The shared
request limit, original execution deadline, and stored wait across restart still
apply.

Quotes are free previews. Submission atomically reserves both activity
currencies at the selected per-image price times the requested image count. If
price or capability changes before acceptance, the caller must review the
current quote and revisions. Accepted tasks retain their configuration and
price snapshots after later settings changes. Partial successful generation
still charges the full accepted task; failure, unknown-result timeout, or
cancellation before dispatch refunds both currencies. Closing the page is not
cancellation. Prompts and execution parameters remain in RAM; lost queued
payloads refund after restart, known asynchronous tasks resume queries, and
uncertain submissions are never sent twice. Original images have a ten-minute
RAM pickup window; successful generation is not refunded if a result is lost or
not downloaded. Natural activity end lets accepted tasks finish; manual pause
or maintenance cancels undispatched tasks with refunds. Ban, deletion, and
model withdrawal follow the documented atomic cleanup rules. Safe task history
lasts 30 days; accounting follows ledger retention and the current account export.

### Lake Notes API

Lake Notes is a permanent free minigame with four independently configured exchanges. Completing a location’s collection enables a manual, once-per-account claim of 50 cumulative donation credits, with no spendable credit or coin reward. `POST /api/games/lake-notes/collection-claims` accepts `{location}` with the normal session, CSRF and idempotency controls; permanent `profile.collectionClaims` is exported and deleted with the account. Fresh installations start closed; an open legacy activity stays available after upgrade, with all exchanges initially disabled. See [game settings and API](lake-notes.md).

### Account protection and historical records

`GET /admin/api/alerts` accepts optional `kind` alongside `resolved` and either cursor or numbered pagination. Kind is a closed alert-kind value, including `account_deleted`; cursors are bound to both filters. `POST /admin/api/alerts/resolve` accepts `{"ids":["1","2"]}` with 1–100 distinct positive decimal ID strings. It atomically marks those alerts resolved, preserves previously resolved timestamps, and returns `{"resolved_count":2}`. A missing ID returns 404 without partial writes. All alert routes require an administrator session; writes retain same-origin/CSRF checks.

**Rc.4 alert detail:** `GET /admin/api/alerts/{id}` returns `{alert,context_version,occurred_facts,targets,current_state,related_logs,resolution_kind}`. This is a read-only detail, with no request body or query. `alert` retains the compatible `kind,message,ref` and resolution fields; `context_version` is 1 for a recorded event context or 0 for a legacy row. Each event/current fact is `{key,value}`. A target is `{kind,id,available,status,unavailable_reason?}` and is at most eight per alert; the persisted context is at most 16 KiB. `related_logs` is null or `{endpoint_key_id,from,to,available,unavailable_reason?}` and uses the actual physical endpoint-key ID and an explicit UTC window. `resolution_kind` distinguishes manual, automatic-blacklist, recovered-worker and legacy outcomes when known. Current resource state is separately rechecked and can be unavailable after deletion, loss of authority or log expiry; old references are parsed only when their known format matches exactly. A `ref`, message or upstream string is data, never a navigation URL.

`GET /admin/api/alerts/targets/{kind}/{target_id}` is a separate administrator-only, read-only diagnostic for an **exact** target. The only accepted `kind` values are `issue`, `issue_user`, `fishing_batch` and `rps_session`; `target_id` must be the corresponding valid opaque ID, or a canonical positive user ID for `issue_user`. It accepts no query or body and returns HTTP 200 with `{kind,id,facts,related_issue_ids}` under `no-store`, rechecking administrator authority in the same bounded read transaction. Facts are fixed `{key,value}` safe operational strings: issue state/source/count/times, a fishing batch's retry/settlement stage, or an RPS session's current retry phase or retained terminal summary. `issue_user` returns the projection phase, counts and at most 20 retained issue IDs, **only** when an `issue_projection_incomplete` alert for that user exists. Invalid kind/ID is 400; an absent, expired or unqualified target is 404. No full game inputs, randomness, another player's identity, secret, free-form SQL/URL selector or mutation is exposed. Level-6 stewards have no route to this diagnostic.

| Closed alert kind | Safe detail and target when available |
| --- | --- |
| `account_deleted` | Exact deleted-account record, pre-zero economic snapshot and automatic-blacklist result. Administrator-only `alert_id` on a deleted-account card opens this exact detail; level 6 sees the safe card without the alert link. |
| `donation_failure_disabled` | Donation, donation key and actual physical endpoint key; new events include occurrence-time count/threshold/generation or policy revision and an endpoint-key log window. Old refs never borrow today's threshold. |
| `report_retry_exhausted`, `issue_projection_incomplete` | Exact report case or issue and its recorded failure stage. |
| `maintenance_enabled` | Recorded event/operator facts and currently available maintenance state. |
| `fishing_retry_exhausted`, `rps_terminal_retrying` | Exact fishing batch or RPS session and settlement phase when still available. |
| `worker_checkpoint_failed` | Lifecycle recovery/retention checkpoint, domain, stage, safe error class, last success and current retry state. This is not a SQLite WAL checkpoint. |
| `invariant_violation` | Fixed safe invariant category and an exact domain/operation only when captured. |
| `fetch_failed`, `forward_error`, `registration_rejected` | Historical discovery/request/rejection context only where a validated target still exists; an unknown legacy ref has no guessed link. |

The 12 kinds are a closed set. All links use fixed internal route mappings with fresh target authorization, preserve an allowlisted relative return path to the originating filter/page, and never route a level-6 steward into general administrator alerts. Worker/capacity alerts provide read-only phase/diagnostic context, not a generic rerun action. Creation and `resolved_at` are UTC seconds; `resolved_at` is the actual state-change time and is null while unresolved.

An `account_deleted` alert includes an `account_deletion` snapshot. Its identity and economic fields are `user_id`, `discord_id`, `general_balance`, `game_balance`, `donation_credit`, `sketch_paper` and `sketch_brush`; IDs and exact point amounts are strings. Current responses also include `snapshot_version`, `registered_at`, `deleted_at`, `effective_level`, `ban`, `charity_pause`, `source`, `actor_user_id`, `blacklist_action` and `blacklist_reason_codes`. Each penalty is `{state,active_at_deletion,reason,until}`. Historical snapshots use version 1 with unavailable times, level, actor and penalty facts represented by null or `unknown`; new snapshots use version 2. Version 1 responses may therefore contain the complete current field set. Clients also accept the earlier seven-field shape, while rejecting partial extended snapshots and invalid fields. These rules apply to lists, detail and single-alert resolution responses.

The economic snapshot is captured after pending domain work is settled and before wallet zeroing, inside the account-deletion transaction. General or game balance below zero creates an unresolved alert unless automatic blacklisting succeeds; successful automatic blacklisting resolves the alert in the same deletion transaction. Other deletion alerts start resolved. The account link is detached, while the explicitly retained identity and snapshot survive deletion and resolution. No request body, credential, or other users' economic data is included.

Administrators and stewards with `blacklist.read` can list entries and read append-only events; `blacklist.add` authorizes adding an eligible Discord ID through their respective `/admin/api/blacklist` and `/api/steward/blacklist` routes. `GET .../blacklist` accepts `q` (substring search), exact `discord_id`, `actor_kind` and `actor_user_id`, with either cursor/limit or numbered `page,page_size` (20, 50 or 100). `GET .../blacklist/{discordID}/events` returns the chronological reason events. `POST .../blacklist` accepts `{"discord_id":"123456789012345678","reason":"Policy violation"}`; the canonical positive uint64 Discord ID and nonblank reason of at most 2,000 Unicode characters are validated. Re-adding appends an event while preserving the first reason, actor and creation time. The current account, if any, is permanently banned in the same transaction. A steward may add an unregistered ID or an account strictly below the actor's actual level. Adding a current account also requires `users.ban` and `users.ban_permanent`; replacing its active ban requires `users.unban`. Actual actor labels include `steward5` and `steward6`, also accepted by the `actor_kind` filter. Only an administrator can call `POST /admin/api/blacklist/{discordID}/remove`. Writes require the usual session, CSRF and `Idempotency-Key` checks and return 204. Removal does not unban an account or restore sessions or keys.

`GET /admin/api/users` and `GET /api/steward/users` accept `account_state=all|active|deleted` and the documented user filters and pagination. The new numbered mode defaults to `all`; an unfiltered legacy cursor/limit request retains its active-account projection. A deleted row carries a read-only `deleted` projection; `GET .../users/deleted/{recordID}` returns that record. Historical fields include the known former site and Discord IDs, registration/deletion times, effective level, ban and charity-pause state, deletion source and available actor ID, pre-zero wallet amounts, donation credit and blacklist outcome. Unknown older values remain null or `unknown`. Only the administrator projection includes `alert_id`; steward access excludes administrator-only alert material and other administrators' protected records. Only administrators can list minimal self-deletion game abort events via `GET /admin/api/users/deletion-duel-aborts`, filtered by exact `discord_id` and paginated; those events expire after 90 days. A 6-level steward cannot use that route. None of these security records is in a user's self-service export.

Automatic self-deletion blacklisting during an effective ban records `试图通过删号逃避处罚`, followed by a new line and `封禁原因：` plus the reason captured at the start of the deletion transaction. A missing or blank reason adds no reason line. An expired ban does not supply a reason, even if a separate active charity suspension still triggers blacklisting. A concurrent negative general/game balance also retains the debt-evasion reason. Valid ban reasons are preserved in full, with line endings normalized to LF; older invalid or oversized note text is sanitized and bounded to the existing 2,000-character limit without changing the captured deletion snapshot. Existing entries keep their first reason, initiator and creation time; the new note is an append-only event. Note insertion, alert resolution, the snapshot, wallet cleanup and account deletion commit or roll back together. Administrator and authorized steward views render notes as text with line breaks; historical events are not rewritten.

Registration and ban changes recheck the blacklist within the identity transaction. A listed identity cannot register or be temporarily/unbanned until an administrator removes the entry. Blacklist entries persist until removal; deletion alerts follow their existing administrator-alert retention. Historical deleted-account projections do not expire automatically.


Model-discovery status (`GET /admin/api/limited-activities/picture-book/models/refresh/{operationID}`) may include `http_status`, an integer from 100 through 599, on failed operations when retained diagnostic metadata is available. The field is omitted otherwise. This administrator-only projection does not return response bodies, provider messages, headers or credentials.

### AI challenge extensions

Bidding Duel adds session-authenticated AI offers, preferences and unpaid admission, plus administrator-only player/strategy configuration and previews. Exact routes, DTOs, bounds and errors are specified in [AI players](ai-players.md). Existing duel state and history add optional `economy`, `ai` and `action_sources`; round records add `sources`. `mode=ai` uses `economy=ai_challenge`. Bot profiles have `kind=ai`; administrative participants have `kind=bot`. Free terminal records have an empty operation ID and ledger sequence `0`; pagination uses a separate persistent history order. Old signed history cursors must be restarted after this upgrade. Export v13 adds the AI families under the player's `bidding.ai`, with no private strategy or AI seed.

## Collectible-card activity

The session-authenticated card activity uses `/api/gacha/state`, `/api/gacha/history` and `/api/gacha/actions`; administrator configuration uses `/admin/api/gacha`. User site cosmetics use `GET/POST /api/appearance` and server-validated collection unlocks. See [The Gacha Simulator](gacha.md#records-and-http-access) for transaction, retry, revision and retention semantics.

Administrator pool statistics use three session-only GET routes:

- `/admin/api/gacha/statistics/pools` accepts numbered `page`/`page_size`, `q`
  (at most 128 characters) and `sort=recent|draws`; it returns `{items,pagination}`.
  Permanent comes first. Each pool has `id`, `name`, `name_en`, `kind`, nullable
  `starts_at`/`ends_at`, `total_draws`, `participant_count` and `available`.
  `available` means its configuration still exists, not that the pool is open.
- `/admin/api/gacha/statistics/pools/{id}/cards` accepts numbered pagination,
  `q` and `sort=count|rarity|name`; it returns
  `{pool,items,pagination,rarity_totals,top_cards}`. Card fields are `card_id`,
  `name`, `name_en`, nullable `rarity`, `count`, `participant_count` and
  `first_acquisition_count`. The two chart summaries always describe the whole
  pool: rarity rows contain `rarity`, `count`, `first_acquisition_count`; top
  cards are at most ten nonzero card rows. Table filtering does not change them.
- `/admin/api/gacha/statistics/pools/{id}/export?format=json|csv` downloads one
  complete authorized read snapshot, without search or page parameters. JSON
  contains `{pool,rarity_totals,cards}`. CSV repeats pool ID/name, total draws and
  participant count alongside each card's safe fields; formula prefixes are
  escaped. An output failure after streaming begins aborts the response.

Counts are exact canonical decimal strings. Every successful draw, including
duplicates, counts once; a ten-pull adds ten. Participants are distinct accounts
within the selected pool, or within that pool/card pair. A first acquisition is
a draw that adds a previously unowned card to the account's collection. Rewards,
codes and shop redemptions do not count as pool draws. Statistics outlive draw
detail retention and account deletion. They expose no participant identities and
do not enter personal exports. Stewards and ordinary users cannot read or export
them. Detached pool IDs retain their separate period and cannot be reused.

Mystery-shop card statistics use independent administrator-only GET routes:

- `/admin/api/gacha/statistics/shop/cards` accepts numbered pagination, `q`
  and `sort=count|rarity|name`. It returns
  `{summary,items,pagination,rarity_totals,top_cards}`; `summary` contains exact
  decimal-string `total_redemptions` and `participant_count`. Card and chart
  rows use the same fields as pool statistics. The shop rejects already-owned
  or previously-redeemed cards, so each card's quantity, participant count and
  first-acquisition count are equal under the current redemption rules.
- `/admin/api/gacha/statistics/shop/export?format=json|csv` downloads complete
  shop-card statistics independently of table filters/pages. JSON contains
  `{summary,rarity_totals,cards}`; CSV contains summary and card fields without
  pool columns. Charts always describe the whole shop card dataset.

Only successful card redemptions contribute. Ticket purchases, daily bundles,
shard-to-ticket exchanges, draws, codes and site-wide rewards do not contribute.
Shop and pool counters are separate, with the same atomic-write, exact-count,
administrator-only and lifetime aggregate semantics.

`redeem` accepts `product=bulk-permanent|bulk-limited` with the current shop
`revision`. Each grants ten tickets for 10,000 credits total, using game credits
first, once per product per user per site-calendar day. Debit, grant and purchase
claim commit atomically. A fresh repeat returns the existing action rejection
`daily-limit`. The snapshot's `redeemed` product IDs include these two products
only when purchased in the current day; existing card redemption IDs remain
lifetime claims. Reusing an earlier day's idempotency key returns current state
without purchasing again or consuming the new day's limit. Monthly ticket
exchange caps and single-ticket prices do not affect these bundles.

Reward receipts are included in the current account export under `rewards`. They contain the account’s successful site-wide and code rewards, quantities, duplicate conversions and skipped items, without code text, eligibility configuration, other recipients or retry hashes. Receipts remain for the account lifetime and are removed on deletion; campaign totals and used quota remain. See [rewards](rewards.md).
