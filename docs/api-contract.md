# NonbiriAPI HTTP API Contract (`v1.0.0-beta.1`)

- Status: **v1.0.0-beta.1 release contract**.
- Scope: the only OpenAI-compatible ingress routes are `GET /v1/models` and `POST /v1/chat/completions`. OpenAI-compatible and Anthropic-compatible upstream connectors sit behind that ingress; there is no public Anthropic-native API.
- Authority: this document reflects the production route registry, strict request/response types, stable error catalog, and contract tests. A future wire change requires a changelog entry; undocumented database fields never enter an API response automatically.

## 1. Shared wire rules

### 1.1 Stations and authentication

| Station | Host | Authentication |
| --- | --- | --- |
| User | configured public host | user session for `/api/*`; CallerKey Bearer for `/v1/*` |
| Administrator | distinct configured admin host | administrator session for `/admin/api/*` |

Host selection is a security boundary. A route on the wrong host is `404 not_found`, and a user, administrator, or steward credential never changes station. `GET /healthz` is an anonymous liveness probe on both hosts and returns `{"status":"ok"}` without opening the database.

User and administrator session cookies are host-only, HttpOnly, SameSite=Lax, and Secure on HTTPS. Unsafe cookie-authenticated methods require the same validated origin (and compatible Fetch Metadata when supplied). `/v1/*` accepts only `Authorization: Bearer nbk_<secret>`; an upstream credential is never a CallerKey.

Account export/deletion and selected administrator legal-hold/user actions require a short-lived, single-use elevation capability bound to the active session. Live level-5 steward permission is resolved again for every request and in the final mutation transaction.

### 1.2 Strict JSON, scalars, pagination, and replay

- Request objects reject unknown or duplicate fields, trailing JSON, invalid UTF-8, control characters, invalid enum case, and values outside the documented range. GET/HEAD requests reject bodies. Query parameters are closed, single-valued sets.
- Times are UTC Unix seconds as JSON numbers in `0..253402300799`. JSON booleans are true booleans. Omitted, `null`, empty, and zero are distinct.
- Credit amounts are canonical decimal strings: `0|[1-9][0-9]*` with an optional 1–3 decimal places. A leading `-` is permitted only for fields explicitly described as signed. Floating point is never used for accounting.
- Revisions, generations, sequences, exact counts, and values that may exceed the JSON safe-integer range are canonical decimal strings.
- New opaque IDs use a documented prefix plus 22 canonical raw-base64url characters. Important prefixes include `ann_`, `op_`, `req_`, `clm_`, `pol_`, `thu_`, `fb_`, `ll_`, `rpsq_`, `rps_`, `rpc_`, `rpt_`, `iss_`, `lgh_`, `sse_`, `gle_`, `dbs_`, `dbt_`, `dbe_`, and `mch_`.
- List endpoints use `cursor` and `limit` and return `{data:[...],next_cursor:null|string}` unless a section states a different closed envelope. Cursors are authenticated, route/owner/filter-bound, expiring, and at most 512 bytes. A malformed, expired, or cross-scope cursor is `400 invalid_request`.
- State-changing routes require `Idempotency-Key` unless they are authentication/session/elevation/logout, CallerKey plaintext mutation, an explicitly business-unique ACK/lease/check-in/tutorial mutation, or alert set-state. A key is 1–128 bytes. Same key and same canonical request replay the stored status/body; the same key with a different request is `409 conflict`. Replay records last 24 hours.
- Dynamic API responses and every error carry `Cache-Control: no-store`.

### 1.3 Error envelope

```json
{
  "error": {
    "code": "conflict",
    "source": "platform",
    "message": "[NonbiriAPI] resource revision changed",
    "upstream_code": "optional-safe-code",
    "diag": "optional bounded diagnostic"
  }
}
```

`code`, `source`, and `message` are always present. `source` is `upstream` only for code `upstream`; it is `platform` otherwise. Platform messages receive the prefix exactly once and are bounded to 1,024 UTF-8 bytes. `upstream_code` (printable ASCII, at most 64 bytes) and `diag` (sanitized, at most 4,096 bytes) appear only on explicitly allowed owner-visible upstream failures. Public charity, platform, and Debug errors omit them.

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
| 504 | `upstream` for an owner-visible upstream timeout |
| 500 | `internal` |

An owner-visible upstream 4xx may retain its HTTP status. Other upstream failures use 502, with timeouts using 504. A started SSE response emits one bounded `data: {"error":...}\n\n` frame and never fabricates `[DONE]`.

### 1.4 Database and export versions

The database remains Generation 2: SQLite `application_id=0x4E425249` and `user_version=2`. Only a completely absent main/WAL/SHM set or a validated Generation 2 set is accepted. Empty, alpha/Generation 1, unknown, corrupt, structurally unexpected, unsafe, or anomalous-sidecar sources are rejected before a writable open or source-side change. Four exact earlier Generation 2 manifests are supported: before charity routing, before per-key request limits, before successful-response checkpoints, and the complete beta.1 schema. After read-only validation, one transaction adds the missing tables, indexes, and default sidecar rows, validates the complete current manifest, and preserves existing business data and custom legal settings. No historical successful-response evidence, recurring usage, or game presentation values are invented. Alpha/Generation 1 requires a fresh database; arbitrary schema repair and old-generation import remain unsupported. See the [deployment compatibility matrix](deployment.md#database-compatibility-and-version-changes).

Account export `schema_version=5` is independent of SQLite `user_version`.

## 2. OpenAI-compatible ingress

### 2.1 `GET /v1/models`

Returns the caller's currently routable personal models plus currently available charity models in the OpenAI list envelope:

```json
{"object":"list","data":[{"id":"provider/model","object":"model","created":1788000000,"owned_by":"provider"}]}
```

The list is sorted by external model ID. It never contains physical endpoint, key, binding, donation, price, or donor data. A charity model appears only while its feature gate is open, the caller's current effective level is allowed, and at least one real candidate is currently usable.

### 2.2 `POST /v1/chat/completions`

The request is the OpenAI Chat Completions shape, bounded to 1 MiB. `model` is a required opaque platform model name (at most 133 Unicode runes). `stream` selects OpenAI-compatible SSE. `max_tokens` and `max_completion_tokens` may be absent or null; if both are integers they must be equal and in `1..2147483647`. A caller-supplied safety identifier is overwritten with a server-generated user-and-canonical-origin-scoped pseudonym.

Admission order is fixed: CallerKey/account, one user-wide concurrency permit, global/user RPM, strict body/model policy, Debug interception, model/capability resolution, candidate selection, credential/egress checks, then dispatch. A refused concurrency permit creates no RPM hit, candidate access, reservation, or penalty. One logical request retains one permit and one 1200-second aggregate deadline across retries and streaming. Upstream response headers may take up to 900 seconds; receiving them does not end the remaining output budget.

Short charity requests return `400 content_too_short` with the actual and minimum character counts. The server applies the current optional penalty, temporary ban, and charity suspension settings before any reservation or upstream attempt. Their rejection log and any credit penalty commit together; `X-Request-ID` identifies that log. Rejected requests count once with zero upstream tokens. The separate automatic RPM-ban window counts only charity requests denied by the site's per-user RPM limit. Personal resource calls, global RPM, concurrency limits, shared EndpointKey limits and upstream `429` responses do not contribute. Successfully intercepted Debug dry runs retain their zero-accounting and zero-log behavior.

On a per-user RPM denial, the server can attribute a charity violation only after decoding the reserved `[公益]` model namespace from a valid bounded chat request. This read is limited to the existing 1 MiB body maximum and two seconds, or the earlier request deadline; at most 16 such reads run at once, without queuing. An unreadable or malformed body, or exhausted classification capacity, keeps the rate-limit rejection without adding an automatic-ban event. This does not select a candidate, access a credential, reserve credit or send upstream; admitted requests retain the order above.

Personal models use `ordered` or `random` routing. Request capability filtering happens before a credential is decrypted. Silent retry may advance only while no response-body byte has been committed. A clean EOF is never success: non-streaming requires a complete valid response, and streaming requires a valid protocol terminator and `[DONE]`. Client disconnect cancels upstream work.

`openai-compatible` appends `/chat/completions` to the configured versioned base. `anthropic-compatible` appends `/messages`, translates the strict supported subset, and sends only the required Anthropic key/version/content headers. The Anthropic subset supports system/developer and user/assistant text, HTTPS or bounded image data parts, OpenAI function tools and matched tool results, `temperature`, `top_p`, stop strings, tool choice, parallel-tool indication, and streaming usage. Lossy or unsupported fields make that candidate incompatible; they are never silently dropped. If neither token-limit field is supplied, Anthropic uses the nullable administrator default or the built-in 65,536 fallback. The fallback is not a cap on explicit values.

Usage is normalized into uncached input, cache-write input, cache-read input, and output. OpenAI-compatible streams may send cumulative snapshots: fixed input buckets and nondecreasing output replace the previous value without being added together. Missing/null snapshots preserve the last credible value. Tool flattening emits only the last usage frame after finish and before `[DONE]`. Invalid, negative, regressing, contradictory, or overflowing usage marks the entire request usage unknown rather than fabricating numbers. Literal and semantic response guards block reflection of the exact credential, including across bounded JSON/SSE fragments; this is defense in depth, not general data-loss prevention.

The OpenAI-only physical-key policy `force_store_false` overwrites/inserts top-level `store:false`; an upstream may ignore or reject it. The logical-model policy `flatten_tool_calls` converts bounded validated tool calls to text and restores only complete matched history. Both default off, and flattening is permitted only when every binding is OpenAI-compatible.

Charity names use the reserved `[公益]` prefix. Charity admission checks the feature and caller state and uses only approved, enabled, unexpired donation keys whose physical claim and catalog binding are still valid. After eligibility and Connector capability filtering, at most 100 candidates follow the model's `ordered`, `random`, or `expiry_weighted` strategy. Ordered routing uses saved binding order; uniform random gives each eligible connection equal weight; expiry weighting uses `8/4/2/1` for remaining lifetimes of at most 1 day, 7 days, 30 days, or longer/unlimited. Random strategies use a CSPRNG rejection sampler. The attempt order is frozen without replacement before any logical-request, ledger, reservation, or claim write, and entropy failure is `503 service_unavailable` with zero writes. Caller credit is reserved before dispatch. Each attempt rechecks expiry/capacity but never redraws or adds candidates.

Charity upstream details are private: after a real dispatch, failure is the fixed `502 upstream` charity projection. The client never receives the donation/key/base URL/upstream-model identity, raw status, diagnostic, or retry count.

Charity credit is reserved before dispatch and charged only after the upstream returns a validated successful payload: a complete valid JSON response or the first valid success frame in a stream. HTTP headers, heartbeat comments, empty/invalid responses and error responses alone do not qualify. A failed attempt with no successful output consumes no donation price/call/token quota and earns no donation reward; its entire caller reservation is refunded if no other attempt started successfully. Actual dispatches still count toward RPM and failure tracking. Once output starts, interruption or client disconnect does not undo the charge: known usage is priced normally, and unknown usage follows the frozen conservative reserve and discount. Retries aggregate billable usage only from attempts with successful output. This applies to live diagnostics and buffered tool conversion as well. A minimal durable start marker lets recovery apply the same rule without storing response content.

## 3. User identity, account, resources, and logs

All routes in this section require a user session unless marked anonymous.

| Method and path | Request / response |
| --- | --- |
| `GET /api/config` | Anonymous safe bootstrap: site name/logo, donation notices, four legal overrides, authoritative locale, maintenance/registration state, announcement epoch. |
| `GET /api/auth/discord/start` | Anonymous; optional server-issued return route; 302 to Discord after IP admission. |
| `GET /api/auth/discord/callback` | OAuth `code,state`; atomically signs in or creates an allowed account, then redirects to the bound route. |
| `GET /api/session`, `GET /api/me` | Current `UserEnvelope`; the shapes are identical. |
| `PATCH /api/me` | `{lang?,game_profile_public?}` with at least one field; returns `UserEnvelope`. |
| `POST /api/auth/logout` | Clears the user session; 204. Available during maintenance. |
| `POST /api/auth/elevate` | Starts the bound two-step elevation flow. |
| `GET /api/me/usage` | Four-bucket and request usage summary. |
| `GET /api/caller-key` | Null or safe metadata and the generation header. |
| `POST /api/caller-key/regenerate` | `{expected_generation}`; returns plaintext once plus metadata and increments generation. |
| `GET /api/home/game-summary` | Safe resumable-game and pending-result route identities. No arbitrary URL is returned. |
| `GET /api/logs` | Filters `model,error_code,status,from,to,cursor,limit`; returns owner rows. |
| `GET /api/logs/{id}` | `attempt_cursor,attempt_limit`; returns owner detail. |
| `GET /api/logs/options` | Closed query; returns retained owner model-name options. |
| `GET /api/issues` | Required `state=current|closed`, plus `cursor,limit`; returns the owner's bounded issue page. |
| `POST /api/account/export` | Fresh elevation; bounded schema-v5 JSON attachment. |
| `POST /api/account/delete` | Fresh elevation and confirmation; synchronous coordinated deletion; 204. |

`UserEnvelope` contains safe identity/profile, raw and effective resource limits, balances, resolved level/display name, language, suspension/ban state, and game-public preference. It contains no manual-level provenance, credential, session token, or internal ledger encoding.

### 3.1 Endpoints and keys

| Method and path | Request / response |
| --- | --- |
| `GET /api/endpoint-create-options` | `{base_connector_types,mainstream_channels}`; channels are active and enabled. |
| `GET /api/endpoints` | `cursor,limit`; page of `Endpoint`. |
| `POST /api/endpoints` | Strict union `{source:"mainstream",channel_id,note,enabled}` or `{source:"custom",connector_type,base_url,note,enabled}`; returns 201. |
| `GET /api/endpoints/{id}` | One owner endpoint. |
| `PATCH /api/endpoints/{id}` | `{note?,enabled?,expected_revision}`; origin and Connector are immutable. |
| `DELETE /api/endpoints/{id}` | `{expected_revision}`; 204. Report locks return `resource_locked`. |
| `GET /api/endpoints/{id}/keys` | `cursor,limit`; page of safe `EndpointKey`. |
| `POST /api/endpoints/{id}/keys` | `{secret,note,enabled,force_store_false,ownership_confirmed:true,max_concurrency?,max_rpm?}`; returns 201 safe metadata. |
| `PATCH /api/endpoints/{id}/keys/{keyId}` | `{note?,enabled?,force_store_false?,max_concurrency?,max_rpm?,expected_revision}`. |
| `DELETE /api/endpoints/{id}/keys/{keyId}` | `{expected_revision}`; claim-first deletion; 204. |
| `GET /api/endpoints/{id}/keys/{keyId}/models` | Cursor page with discovery `evidence`, automatic and manual entries. |
| `POST /api/endpoints/{id}/keys/{keyId}/models/refresh` | Idempotency key; 202 accepted operation and evidence. |
| `POST /api/endpoints/{id}/keys/{keyId}/models/manual` | Batch of `{upstream_model_id,provider}`; returns entries. |
| `PATCH /api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}` | Pair revision plus complete binding replacements; returns changed entry and affected model snapshots. |
| `DELETE /api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}` | Pair revision plus replacements; 204. |

`Endpoint` includes `id,connector_type,base_url,note,enabled,revision,key_count,origin,created_at,updated_at`. `origin` is `{kind:"custom"}` or `{kind:"mainstream",channel_id,name}`. Channel category/revision are administrator-only. A mainstream create re-reads the active enabled channel and copies its immutable snapshot in the final transaction; later channel edits never rewrite an endpoint. A custom URL is never guessed to be mainstream.

`EndpointKey` exposes display fragments, note, enabled state, `force_store_false`, safe suspension state, revision, and times; never plaintext/ciphertext/fingerprint. Discovery evidence is the closed `unknown|checking|succeeded|failed` union with a safe failure class. Successful empty discovery is explicit and distinct from failure.

`EndpointKey` also returns numeric `max_concurrency` and `max_rpm`. Both accept whole numbers from 0 to 2147483647; 0 disables that key's additional limit. Omitted creation values default to 0; omitted patch values remain unchanged. Null, strings, negative numbers, fractions and out-of-range values are rejected. Only the owner can edit them, using the existing revision and idempotency contract. Existing site and endpoint safeguards still apply.

All personal, charity and live diagnostic model calls using the same EndpointKey share these limits across bindings. Discovery requests use their separate safeguards. Concurrency includes admitted work until its attempt finishes or is canceled, including the full streaming lifetime. RPM uses a rolling 60-second window with server-second precision. Undispatched claims reserve a slot until dispatch or release; a released undispatched attempt returns the slot. A dispatch consumes one slot even on failure, and each retry counts separately. Pending work cannot age out of its reservation. Lowering limits affects new admissions without canceling already admitted work. Recent dispatch counts survive process restarts.

A limited key is skipped without contacting the upstream, charging for that attempt, or recording an upstream failure. Selection continues in the configured order even when silent retry is off. If no attempt dispatched and at least one candidate was key-limited, exhaustion returns HTTP 429 `rate_limited` and releases the request reservation. If an earlier attempt dispatched, its actual result and ordinary settlement remain authoritative.

Administrator and level-5 steward donation-key projections include read-only `max_concurrency`/`max_rpm`; both are null after the physical key is gone. Their shared binding menu/candidate/binding `source` includes the current integer limits. These fields do not expand donation ownership or expose private owner notes, and management mutation bodies reject them. Ordinary charity callers do not receive underlying key configuration. Owner resource exports include the values; key deletion cascades to its configuration. Historical idempotent responses may omit these additive fields; an authoritative read returns current values.

### 3.2 Personal models and bindings

| Method and path | Request / response |
| --- | --- |
| `GET /api/models` | `cursor,limit`; page of owner models. |
| `POST /api/models` | Provider/model, strategy, retry and flatten policy; returns 201 model. |
| `GET /api/models/{id}` | One owner model. |
| `PATCH /api/models/{id}` | Partial business fields plus `expected_revision`. |
| `DELETE /api/models/{id}` | `{expected_revision}`; 204. |
| `GET /api/models/{id}/binding-candidates` | `endpoint_id,key_id,source,q,cursor,limit`; safe candidate page. |
| `GET /api/models/{id}/bindings` | Complete bindings plus `binding_revision`. |
| `POST /api/models/{id}/bindings/batch` | Expected binding revision and selections; all-or-nothing 201. |
| `PUT /api/models/{id}/bindings/order` | Exact current ID permutation and expected binding revision. |
| `DELETE /api/models/{id}/bindings/{bId}` | Expected binding revision; returns the complete new binding set. |

Provider/model parts are bounded opaque strings and form the external `provider/model` name. `[公益]` is reserved. Binding DTOs contain only owner-safe endpoint/key display data, Connector type, upstream model ID, and order.

### 3.3 Credit history

`GET /api/credits/history` returns the signed-in user's credit history. Optional single-value filters are `category=checkin|welfare|thursday|fishing|linklink|rps|api|charity|donation|admin|penalty`, `direction=income|expense`, and Unix-second `from,to` using `[from,to)`. `page` is a positive decimal integer, default 1; `page_size` is 20, 50, or 100, default 20. An out-of-range page returns the last page.

The response is `{data:[{operation_id,line,kind,delta,created_at,request_id}],page,page_size,total,total_pages,anchor,current_balance,server_now}`. Amounts and page/count fields are exact decimal strings; `line`, `page_size`, and times are bounded numbers. Only nonzero changes to the current user's wallet appear. Reasons use the stable ledger `kind`; private management notes, actor identities, source IDs, other wallets, and historical post-balances are omitted. `current_balance` is the balance at this read, including any changes after the browsing anchor.

Pass the returned nullable `anchor` operation ID to subsequent pages to keep newer entries from shifting the result set. The server checks that the anchor belongs to this wallet. Omit it to refresh. Related `request_id` is present only for the caller's own API/charity entries and short-request penalties whose request log is still ordinarily viewable; donor rewards always return null. Expired logs do not remove credit history. The user page is `/credits`; `/logs?request_id=...` opens the existing owner-checked log detail. This read creates no new stored data and leaves export/deletion/retention rules unchanged.

## 4. Donations, charity capability, and public reports

### 4.1 Donations and charity models

| Method and path | Request / response |
| --- | --- |
| `GET /api/donations` | `cursor,limit`; owner donation page. |
| `GET /api/donations/{id}` | Owner detail with key states and review history. |
| `POST /api/donations` | `{description,keys:[{endpoint_key_id,expires_at}],ownership_authorized:true}`; 1–100 unique keys; returns 201. |
| `PATCH /api/donations/{id}` | Pending-only `{description,expected_revision}`. |
| `POST /api/donations/{id}/withdraw` | Pending `{expected_revision}`. |
| `POST /api/donations/{id}/terminate` | Approved `{expected_revision,confirmation}`. |
| `GET /api/charity/models` | Without query parameters: `{state,models,donation_intake,server_now}`. Use `view=catalog` for the paginated web directory described below. |

Each donation key has immutable `authorized_expires_at` and an effective `expires_at`, equal at creation. A reviewer may shorten the effective expiry or restore it only up to the donor's authorization; an unlimited effective value is permitted only when the authorization is unlimited. One key expiring removes only that key's membership and bindings and blocks new claims. The donation becomes expired only when its last live key ends. Accepted claims and reservations complete normally.

Management projections identify a reviewer with `role: admin|steward`. A manual review may have an empty reason. Automatic approval has no reviewer and uses an empty reason. The steward projection shows `owner` only for the steward's own donation; otherwise it is null. A reviewer's `user_id` is present only when it is that steward's own ID; another reviewer's role remains visible with a null ID.

Every owner-visible key carries `safe_source`: a custom Connector/base URL or a mainstream channel/name/Connector/base URL. Internal source IDs, channel category/revision, report fingerprints, secrets, and management notes are excluded. A donation containing only keys from one immutable mainstream channel is approved atomically at creation. A fully custom donation remains pending. Mixed custom/mainstream or multiple-channel submissions are `invalid_request`.

`CharityCapability.state` is `feature_disabled|no_models|no_candidates|available`; `donation_intake` is `open|closed`. Only models allowed for the caller's current level and with a usable candidate are listed. Each model contains `id,provider,model,full_name,pricing,discount`. Pricing is a tagged union:

- `per_request`: canonical base and current discounted user price;
- `per_token`: canonical base and current price for uncached input, cache-write input, cache-read input, and output.

`discount` contains `enabled,percent,start_at,end_at`; the interval is `[start_at,end_at)`, nullable at either end. Effective prices use the same integer ceiling rule as billing. `server_now` is the single decision time used for availability and promotion status. No donated-resource identity is exposed.

The web directory uses `GET /api/charity/models?view=catalog`. Optional single-value parameters are `q` (at most 128 Unicode code points and 512 UTF-8 bytes, no NUL), `allowed_for_me=true|false`, `page`, and `page_size`. It rejects cursor/limit and unknown or repeated parameters. Page numbers are canonical decimal strings from 1 to 2147483647; sizes are 10, 20, 50, or 100, with defaults 1 and 20. The response is `{models,pagination,donation_intake,server_now}`. Pagination contains decimal-string `page,total_items,total_pages` and integer `page_size`; empty results use page 1 of 1, and an excessive requested page is clamped to the last page.

Catalog entries contain the six capability fields plus `public_description,enabled,allowed_levels,level_allowed,availability`. They include unavailable models so users can understand the reason. Availability is `feature_disabled|model_disabled|level_denied|no_usable_key|available`, in that precedence. Search, count, ordered page, pricing and availability share one read-only snapshot and a five-second query budget. No donor rewards, bindings, physical resources or private notes appear in the directory.

Administrator and steward model create/patch bodies accept `allowed_levels` and `public_description` within a 16 KiB request limit. Levels are unique integers 1–5, returned sorted. Omitted creation fields mean all five levels and an empty description; omitted patch fields remain unchanged. An explicit empty array blocks every ordinary caller, including L5. Null and duplicate/out-of-range levels are invalid. Descriptions are plain text, at most 1,024 code points and 4,096 UTF-8 bytes after CRLF-to-LF normalization; controls other than LF and TAB, including lone CR, are rejected. These fields use the existing model revision and idempotency rules.

Level permission is rechecked at admission, claim and immediately before the dispatch marker. A denied level returns `403 forbidden`; an unavailable model returns `404 not_found`. A blocked unsent attempt releases its reservations. Already dispatched attempts finish under their accepted accounting, while later sends and retries must pass current access rules. Debug dry and live calls follow the same caller-level restriction.

### 4.2 Credential-theft reports

`POST /api/reports/credential-theft` is anonymous or user-authenticated, requires an idempotency key, and accepts strict `{connector_type,base_url,secret,note}`. A structurally valid request always returns the same 202 body and `X-Nonbiri-Report-Accepted: 1`, whether or not a match exists. Matching and non-matching responses share the configured timing window. Validation and rate-limit errors remain real errors.

Indexing searches live endpoint keys first, then donation tombstones whose irreversible fingerprint is still inside its 90-day post-end match window. Live matches win and case-local targets deduplicate. Approval after the physical key disappeared is a safe no-op for deletion while the case decision and safe lineage still complete. Report material, source IP material, fingerprints, and target lineage are administrator-only and are excluded from account exports.

## 5. Announcements, activities, check-in, and games

### 5.1 Announcements and activities

| Method and path | Request / response |
| --- | --- |
| `GET /api/announcements` | `cursor,limit`; published summary page. |
| `GET /api/announcements/{id}` | Localized rendered detail using the fixed safe Markdown profile. |
| `GET /api/activities` | Complete safe activity snapshot with site totals and the caller's state. |
| `POST /api/activities/welfare/claims` | Idempotency key; one admitted daily welfare claim. |
| `GET /api/activities/thursday` | Current Thursday view. |
| `POST /api/activities/thursday/contributions` | Idempotency key plus `{period_id,expected_revision}`; one fixed contribution. |
| `GET /api/checkin` | `{enabled:false}` or availability, daily state, balance, award, and cap values. |
| `POST /api/checkin` | Business-unique per account/site day; `{award,balance}`. |
| `GET /api/events` | Shared user SSE for activities and RPS; see §8. |
| `GET /api/games` | Complete game availability, balance, tutorial state, prices, timers, and mode configuration. |

Announcement bodies use a fixed Markdown subset: paragraphs, headings h2–h4, emphasis, lists, blockquotes, inline/fenced code, and HTTP(S) links. Raw HTML, images, embedded media, forms, frames, scripts, styles, and unsafe schemes are rejected by the same parser used for preview, publish, and read.

### 5.2 Pond Fishing

| Method and path | Request / response |
| --- | --- |
| `POST /api/games/fishing/batches` | Idempotency key plus `{bait,count:1|10}`; 200 committed result or 202 settlement state. |
| `GET /api/games/fishing/state` | One settlement-pending/recovery state, one oldest unacknowledged result, and `has_more_unrevealed`. |
| `POST /api/games/fishing/batches/{id}/ack` | Business-unique render acknowledgement; 204. |
| `POST /api/games/fishing/batches/{id}/recover` | Idempotent owner recovery of the same exhausted batch; 200 or 202. |
| `GET /api/games/fishing/leaderboard` | `board=single|total`; top 20 plus the caller when eligible. |

A start creates all 1 or 10 CSPRNG outcomes and the reservation atomically. A failed settlement retains the same batch and outcomes for automatic recovery; it never redraws or charges again. After ten consecutive failures, only owner recovery continues. Committed or released batches replay without another economic write. Acknowledgement affects presentation only. Terminal batches/outcomes are retained for 30 days; the personal best has the account lifetime.

### 5.3 LinkLink

| Method and path | Request / response |
| --- | --- |
| `POST /api/games/linklink/sessions` | Idempotency key plus `{spec}`; 201 new game or 200 existing active game. |
| `GET /api/games/linklink/session` | `null`, active `LinkLinkState`, or the latest retained `LinkLinkSummary`. |
| `POST /api/games/linklink/sessions/{id}/matches` | `{expected_revision,first:{row,col},second:{row,col},include_path?:boolean}`; returns authoritative state or terminal summary. |
| `POST /api/games/linklink/sessions/{id}/abandon` | `{expected_revision,confirmation}`; returns terminal summary. |
| `POST /api/games/linklink/sessions/{id}/lease` | `{lease_id}`; returns the lease expiry. |

With `include_path:true`, a successful match also returns `match_path:[{row,col},…]`: two to four vertices of the server-approved connection, starting at `first` and ending at `second`. Coordinates can use the single outer ring (row −1 through rows, column −1 through columns). This optional field is retained only in the existing idempotency receipt and is replayed unchanged. Omitted or false preserves the original response shape and request identity. Current, start, and abandon responses do not include a path.

The board is server-generated and solvable. A legal connection travels through empty cells and at most one perimeter ring with no more than two turns. Completion, timeout, and abandonment delete the active board/lease in the terminal transaction and retain only a 30-day summary. The absolute deadline does not move after ordinary disconnect or process downtime.

### 5.4 Three-player rock-paper-scissors

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

## 6. Debug and level-5 steward surfaces

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

Level-5 steward routes are user-host routes and require a currently effective L5 user session. Final mutations recheck that role in the write transaction. List/detail DTOs are separately compiled allowlists and do not inherit administrator fields.

| Surface | Routes |
| --- | --- |
| Logs | `GET /api/steward/logs`, `GET /api/steward/logs/{id}` |
| Maintenance | `GET /api/steward/maintenance`, `POST /api/steward/maintenance/enable` |
| Shared donation management | `GET /api/steward/donations`, `GET /api/steward/donations/{id}`, `POST /api/steward/donations/{id}/review`, `PATCH /api/steward/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |

Stewards can review and manage charity settings across donations. They can enable but cannot disable maintenance. They have no report route, legal-hold route, account-export route, user-account mutation route, or administrator audit identity. Held-only donation history remains administrator-only; shared management does not widen an account's owner export.

Both management prefixes (`/admin/api` and `/api/steward`) provide `GET {prefix}/donations/badge`, with no query or body. The no-store response is `{pending_count,server_now}` with an exact decimal-string count. Only logically active donations with pending handling count; expiration is reflected even before cleanup runs.

`POST {prefix}/donations/{id}/handling/processed` accepts `{expected_handling_revision}` and an idempotency key, with a 16 KiB body limit. It returns `{donation_id,handling}`. Processing is a shared action with one winner, not a per-person read flag. Approval, model binding and key edits do not mark it processed. Handling has `state=legacy|pending|processed|closed`, its own decimal-string `revision`, and nullable `processed_at,processed_by_role,closed_at,closed_reason`. Only actor role and time are public. Terminal pending donations close automatically; closed reasons are `rejected|withdrawn|terminated|expired|member_removed|account_deleted`. Existing processed or legacy handling remains unchanged at termination.

Management donation lists accept `status`, `handling`, and `q` alongside cursor/limit, and bind cursors to their filters. Search uses donation ID and description, with the same 128-code-point/512-byte/no-NUL limit. Each management key adds decimal-string `binding_count` and boolean `idle`. All actual bindings count, including bindings to disabled models; zero bindings means idle. Owner donation DTOs and exports omit handling and these management fields. Historical management receipts add safe handling/count fields when replayed without rewriting the stored immutable result.

Steward charity log list/detail entries add nullable `caller_identity: {discord_nickname,discord_id}`. Both members are nullable, with UTF-8 byte limits of 256 and 128 respectively. The name uses the current guild nickname, falling back to the stored username. Data comes from the account's latest synced profile, not a historical identity snapshot. Unlinked/deleted callers produce null, the existing 30-day visibility limit still applies, and stewards gain no log export.

| Method and path | Request / response |
| --- | --- |
| `GET /api/steward/charity-models` | `q,enabled,cursor,limit`; steward-safe model page. |
| `POST /api/steward/charity-models` | Complete provider/model, enabled, tagged pricing, discount and flatten policy; 201. |
| `GET /api/steward/charity-models/{id}` | Steward-safe model detail. |
| `PATCH /api/steward/charity-models/{id}` | `expected_revision` plus a non-empty partial business-field patch. |
| `DELETE /api/steward/charity-models/{id}` | `{expected_revision,confirmation}`. |
| `GET /api/steward/charity-models/{id}/binding-candidates` | `donation_id,donation_key_id,source=automatic|manual,q,cursor,limit`; safe candidate page. |
| `GET /api/steward/charity-models/{id}/bindings` | Complete ordered bindings and `binding_revision`. |
| `POST /api/steward/charity-models/{id}/bindings/batch` | `{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}`. |
| `PUT /api/steward/charity-models/{id}/bindings/order` | `{expected_binding_revision,order}` with the exact current binding-ID permutation. |
| `DELETE /api/steward/charity-models/{id}/bindings/{bindingId}` | `{expected_binding_revision}`. |

## 7. Administrator API

The administrator login shell reads anonymous `GET /admin/api/branding` on the administrator host. Its closed response contains only `site_name` and `site_logo_url`, matching the public user-site identity. It rejects query/body input and uses `Cache-Control: no-store`. `/admin/api/config` and every administrative operation retain their existing authentication requirements.

Both administrator and steward charity model projections include `route_strategy`: `ordered`, `random`, or `expiry_weighted`. Creation may omit it to keep the expiry-weighted default; a patch may omit it to leave the setting unchanged. Explicit null, empty, and unknown values are invalid. Ordered routing uses saved binding order; uniform random gives every eligible connection equal weight; expiry-weighted routing uses weights 8/4/2/1 for expiry within 1/7/30 days/later, with unlimited expiry weighted 1. Each request freezes a candidate order without replacement. Strategy changes use the model revision and existing idempotency rules.

Each role also has `GET {prefix}/charity-models/{id}/binding-donations` and `GET {prefix}/charity-models/{id}/binding-donations/{donationId}/keys`, where `{prefix}` is `/admin/api` or `/api/steward`. Both use `cursor,limit` pagination. Donation menu entries contain only `{id,description,key_count}`. Key menu entries contain only `{donation_key_id,source,note}`; `source` has the same safe address, connector and masked fragments as binding candidates, and `note` is the reviewed shared note. They list approved, unexpired shared resources with at least one unbound catalog model. Cursors are bound to the role, actor, model, and donation. These menus never expose donor account identity or private endpoint/key notes and do not expand the steward's donation management permissions.

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

The generic site-config patch rejects maintenance, announcement epoch, and all activity/game economic keys; those use their typed domain routes. User patch is a tagged operation for profile, limits/level, or economy and never returns credentials. Log exports retain their fixed privacy projections and spreadsheet-safe encoding.

`PATCH /admin/api/site-config` accepts the closed body `{expected_revision:string,values:object}` with 1–64 known generic configuration keys and a 256 KiB request limit. Values use the catalog's scalar types. It validates the combined configuration and commits all changes with one site revision increment and one idempotency receipt. Stale revisions return `409`; invalid combinations return an actionable dependency error. Unchanged values do not advance the revision. The response is `{revision:string,changed_keys:string[]}`; clients may read the complete configuration after saving. The existing single-key route remains available.

### 7.2 Mainstream channels

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/mainstream-channels` | `state=active|retired|all,cursor,limit`; administrator page. |
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

Announcement mutations return a bounded receipt and the detail is fetched separately. Published content and drafts are isolated. Activity/game configuration reads a complete typed snapshot, merges a strict patch, validates all dependent values and checked arithmetic, then commits atomically. Existing accepted work retains its frozen configuration.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/announcements` | `state,severity,cursor,limit`; administrator page. |
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
| Donations | `GET /admin/api/donations`; `GET /admin/api/donations/{id}`; `POST /admin/api/donations/{id}/review`; `PATCH /admin/api/donations/{id}/keys/{keyId}` |
| Charity models | Exact route family in the table below |

Review and key-management requests include expected revisions and the complete effective limits/expiry needed for that decision. Administrator donation keys add authorized expiry and an administrator-safe provenance snapshot. Charity candidates and bindings omit donor identity, private notes, endpoint-key IDs, and secrets. A model supports per-request or four-bucket per-token prices, donor rewards, a bounded promotion interval, visibility, flattening, rolling success over the most recent 100 completed calls, and ordered bindings.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/charity-models` | `q,enabled,cursor,limit`; administrator model page. |
| `POST /admin/api/charity-models` | Complete provider/model, enabled, tagged pricing, discount and flatten policy; 201. |
| `GET /admin/api/charity-models/{id}` | Administrator model detail. |
| `PATCH /admin/api/charity-models/{id}` | `expected_revision` plus a non-empty partial business-field patch. |
| `DELETE /admin/api/charity-models/{id}` | `{expected_revision,confirmation}`. |
| `GET /admin/api/charity-models/{id}/binding-candidates` | `donation_id,donation_key_id,source=automatic|manual,q,cursor,limit`; safe candidate page. |
| `GET /admin/api/charity-models/{id}/bindings` | Complete ordered bindings and `binding_revision`. |
| `POST /admin/api/charity-models/{id}/bindings/batch` | `{expected_binding_revision,selections:[{donation_key_id,upstream_model_id}]}`. |
| `PUT /admin/api/charity-models/{id}/bindings/order` | `{expected_binding_revision,order}` with the exact current binding-ID permutation. |
| `DELETE /admin/api/charity-models/{id}/bindings/{bindingId}` | `{expected_binding_revision}`. |

### 7.5 Recurring donation-key limits

`GET /admin/api/donations/{id}/keys/{keyId}/recurring-limits` and the matching `/api/steward/` path return `{donation_id,key_id,donation_revision,server_now,rules}`. The owner-only `/api/donations/{id}/keys/{keyId}/recurring-limits` path reads the same safe rule projection for the caller's own donation. GET accepts no query or body. Administrator and current level-5 stewards can also PUT the complete set on their management paths; ordinary owners cannot write it. Pending keys may be configured, while ended or expired keys cannot.

PUT requires `Idempotency-Key`, has a 16 KiB body limit, and accepts exactly `{expected_revision,rules}`. Up to 16 rules are replaced atomically under the donation revision; the response is `{donation_id,key_id,donation_revision}`. Each input explicitly contains all eight fields: `{id,mode,interval,alignment,time_zone,week_starts_on,metric,limit}`. New IDs are `null`; saved IDs are canonical `qlr_` opaque IDs. Omitted old IDs remove those rules. Duplicate IDs or IDs belonging to another key are rejected.

`mode` is `reset|sliding`; `interval` is `5h|day|week|month`. Reset alignment is `first_success|calendar`, and calendar disallows `5h`. Sliding alignment is `null`. `week_starts_on` is 1–7 only for calendar weeks, otherwise `null`. `time_zone` comes from the server's time-zone registry. `metric` is `calls|tokens|credits`. Limits, used, reserved and remaining are canonical U128 decimal strings; credits use at most three fractional digits with no redundant trailing zeroes and must fit U128 after conversion to millicredits. A zero limit blocks new charity calls.

Each rule view adds `{used,reserved,remaining,state,period_start,period_end,next_transition_at}`. Remaining is clamped at zero. State is `limited` if no capacity remains, otherwise `waiting_first_success` for an unstarted first-success period, or `available`; it is not a guarantee that a particular request fits. Period and transition fields are nullable Unix seconds. Natural periods expose the later of the calendar boundary and rule activation as their start. Sliding rules have no fixed reset; an unproven next transition is `null`.

All current recurring rules and total limits apply together to charity calls, including charity live calls. Personal calls, personal live calls, discovery and dry previews do not consume these rules. Claims reserve one call, the current per-key Token fallback and the existing undiscounted price reservation. Dispatch atomically rechecks current configuration and replaces its own old reservations. Calls already dispatched finish under their captured rules; later sends recheck the new set. Changing only a limit or display order preserves usage; changing mode, interval, alignment, zone, week start or metric starts a new counting period for subsequent sends without altering historical total usage or balances.

Only validated successful output establishes the durable first-success time. Calls become used once at that checkpoint; tokens and credits remain reserved until terminal settlement records their full actual or existing conservative amount. Usage exceeding the reservation is never truncated. No validated output means zero recurring consumption. If every unsent candidate is blocked by quota, the caller receives `429 rate_limited`; storage-capacity exhaustion returns `503 service_unavailable` and a deduplicated administrator alert. These failures do not count as upstream failures or disclose physical resources. Later admission failures do not replace the outcome of an earlier dispatched attempt.

### 7.6 Reports and legal holds

| Method and path | Request / response |
| --- | --- |
| `GET /admin/api/reports/badge` | Pending badge count. |
| `GET /admin/api/reports` | State/filter cursor page. |
| `GET /admin/api/reports/{id}` | Case detail and safe material metadata. |
| `GET /admin/api/reports/{id}/targets` | Target cursor page; each target includes canonical `donation_match_count`. |
| `GET /admin/api/reports/{id}/targets/{targetId}/donations` | Lineage cursor page. |
| `POST /admin/api/reports/{id}/approve` | Material/target versions, reason, and confirmation. |
| `POST /admin/api/reports/{id}/reject` | Material/target versions and reason. |
| `POST /admin/api/reports/{id}/resume` | Restarts an approved-processing checkpoint. |
| `GET|POST /admin/api/legal-holds` | Filtered list or elevated create. |
| `GET /admin/api/legal-holds/{id}` | Elevated hold detail. |
| `POST /admin/api/legal-holds/{id}/release` | Elevated release. |

Lineage items are exactly `{donation_id,donation_key_id,donation_status,key_state,expires_at,ended_reason,ended_at}`. They exclude donor identity, descriptions, safe notes, fingerprint, source key ID, and provenance. Legal holds cover only the documented object roots, never identity/session/account deletion, last at most 365 days, and do not extend an object's original retention clock.

## 8. Server-sent events

`GET /api/events` uses protocol version 1 for the closed channels `activities|rps` and frame types `snapshot|delta|gap`. Snapshot and delta frames contain a complete authoritative snapshot, not a patch. Every frame is at most 64 KiB. RPS frames are participant-only and carry the matching revision and identity epoch; a higher epoch replaces the complete cached seat/event view. Gap reasons are `process_restart|ring_expired|ring_evicted|slow_consumer` and are immediately followed by a snapshot.

`Last-Event-ID` replay works only for the same account, process, and five-minute/512-KiB ring. Event IDs and rings are memory-only. `: heartbeat` comments create no event ID. Revocation disconnects immediately. During maintenance, only a continuation with an already valid game lease may reconnect to its one bound session.

`GET /api/debug/events` is Debug protocol version 2. It has a separate session generation and event sequence, supports same-generation replay, emits explicit gap/snapshot and terminal `session_end`, sends a comment heartbeat every 15 seconds, closes after the session ends, and excludes upstream/request/header/body secrets and disallowed payload fields.

## 9. Export, privacy, and account deletion

Export v5 contains the user's safe identity/profile, effective level, endpoints and key metadata, endpoint origins, discovery evidence, catalogs, personal models/bindings, CallerKey metadata/generation, issues, wallet and private ledger slice, welfare/Thursday participation, donation and per-key status/limits/usage/authorized-effective expiry/safe source, charity-consumer summary, and the documented game state and retained results. It includes `schema_version:5` and the generation time. Each donated key adds `recurring_limits`, containing the current safe RuleView values from that same export snapshot; it excludes internal receipts, buckets and claim identities. Private RPS result and own-seat exports include nullable `own_buy_in` and `own_cash_out`; unrecorded values remain `null`.

Secrets, ciphertext, fingerprints, request/response bodies, report data, IP material, other identities, complete pool ledgers, announcement copies, anti-collusion values, workers/checkpoints/replay internals, management notes, channel category/revision, internal source IDs, and administrator/steward audit material are excluded. If the result exceeds 16 MiB or any collection exceeds 10,000 rows, export fails atomically with 413; it is never silently truncated.

Account deletion is synchronous. It revokes credentials, removes private projections, releases undispatched reservations, transfers accepted shared work to deidentified settlement destinations, removes leaderboard/public identity, scrubs donation-private fields, and transfers the remaining wallet balance to the external balancing account in one coordinated boundary. Late workers and callbacks can finish only from a persisted handoff and cannot recreate the account, wallet, credentials, identity, or private aggregates.

## 10. Maintenance, recovery, and retention

Maintenance mode rejects new public inference, OAuth admission, ordinary pages, reports, activities, queues, and games. Health, safe configuration, logout, administrator control, workers, and work already accepted before the switch remain available. A game continuation additionally requires the same user/session and a valid pre-existing lease; it cannot start a game, queue, list, or open a new lease after losing that authority.

Recovery runs before listeners open. Recovery of unfinished API requests drains once per process before accepting traffic; periodic maintenance cannot settle live requests as abandoned after a restart. Accepted requests, donation claims, report indexing/deletion, Thursday settlement, Fishing settlement, LinkLink deadlines, and RPS matching/phases/terminal processing resume from persisted state and exact-once operation identities. Non-terminal records are not removed by age. SQLite WAL/checkpoint restart is supported for a valid Generation 2 database; cross-version recovery is outside this release.

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
| Fishing terminal batches/outcomes | 30 days after settlement |
| LinkLink terminal summary | 30 days |
| RPS shared summary/rank facts | 30 days; pending private result until acknowledgement; fun totals for account lifetime |
| Announcement audit without content | 365 days; actor identity removed after 90 days |
| Administrator/steward domain audit and maintenance event material | 400 days; maintenance actor identity removed after 90 days |
| Legal-hold metadata and no-content audit | 400 days after release/expiry; a hold itself ends within 365 days |

Expired endpoint secrets become unclaimable immediately and are physically removed within one hour after their last non-terminal claim releases them. Retention cleanup and holds preserve only their documented minimum object graph and never disclose additional data through an API.
