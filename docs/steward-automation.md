# Steward automation

Administrators can give these instructions to selected level-5 stewards. These controls have no user-station menu or help entry. They run on the configured **user host**, using the steward's own site CallerKey:

```http
Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY
Content-Type: application/json
```

The caller must remain an active level-5 steward. Session cookies cannot replace the CallerKey. Revocation, rotation, bans, deletion and loss of steward permission are checked again before writes. These controls grant no authority over another donor's private endpoint, key or model catalog.

Creation and model binding accept only POST without query parameters. Failure-policy reads use GET with the two specified IDs; edits use PATCH without query parameters. Other methods, suffixes and path variants are rejected. JSON rejects unknown and duplicate fields. Each request is limited to 256 KiB, 100 keys, 16 recurring rules per key and 16,384 total object fields; responses are limited to 64 KiB. All controls share a maximum of four concurrent requests, with one per steward and no queue. A busy control returns HTTP 429. Maintenance admission and the existing resource, credential, discovery and outbound protections still apply. These management operations do not consume model-call credits or charity usage quotas.

## Create and approve one donation

`POST /api/steward/automation/donations`

Provide a fresh, unpredictable `Idempotency-Key` containing 22–128 ASCII letters, digits, `_` or `-`. Keep the key and the exact request for retries. Within 24 hours, the same caller, key and canonical JSON request return the original result; different input with that key returns 409. A retry still requires current steward authority. The returned IDs describe the original creation, not a fresh view of later resource changes.

```json
{
  "endpoint": {
    "connector_type": "openai-compatible",
    "base_url": "https://upstream.example.com/v1",
    "note": "Optional private endpoint note"
  },
  "description": "Donation description",
  "review_note": "Optional approval note",
  "keys": [
    {
      "secret": "REPLACE_WITH_UPSTREAM_KEY",
      "note": "Optional private key note",
      "safe_note": "Optional donation management note",
      "max_concurrency": 2,
      "max_rpm": 30,
      "recurring_limits": [
        {
          "mode": "reset",
          "interval": "1h",
          "alignment": "first_success",
          "time_zone": "Asia/Shanghai",
          "week_starts_on": null,
          "metric": "tokens",
          "limit": "100000"
        }
      ]
    }
  ]
}
```

Required fields are `endpoint.connector_type`, `endpoint.base_url`, a nonblank `description`, and one or more `keys` with a nonempty `secret`. Connector type must explicitly be `openai-compatible`, `anthropic-compatible` or `ai-sdk-gateway-v3`. All sources use the custom-URL form; channel IDs and the browser forms' ownership-confirmation fields are not accepted. Submitting the operation creates and donates resources owned by the caller.

The endpoint optionally accepts `note` and `enabled`. Each key accepts:

| Field | Default and meaning |
| --- | --- |
| `note` | Empty private owner note; at most 1,024 Unicode code points, as for endpoint notes. |
| `enabled` | `true`; physical-key switch. Endpoint `enabled` also defaults to `true`. |
| `force_store_false` | `false`; allowed only for OpenAI-compatible keys. Applies to chat requests as documented in the API contract. |
| `max_concurrency`, `max_rpm` | `0`; whole numbers through 2147483647. Zero adds no per-key limit. |
| `authorized_expires_at` | `null`, meaning permanent authorization; otherwise a future Unix second. |
| `expires_at` | Omission inherits authorized expiry. A supplied value may shorten it. Explicit `null` requires permanent authorization. |
| `price_limit`, `calls_limit`, `tokens_limit` | `null`, meaning no cumulative limit. Credits use canonical decimal strings with up to three fractional digits; calls and Tokens use canonical nonnegative integer strings. Zero is a real limit. |
| `token_reserve` | `0`; unknown-usage Token reserve, as a whole number through 2147483647. |
| `charity_enabled` | `true`; the donation-key switch, separate from physical-key `enabled`. |
| `failure_disable_threshold` | `"10"`; canonical unsigned 128-bit decimal string. `"0"` never disables this key for errors; failures are still counted. The maximum is `"340282366920938463463374607431768211455"`. |
| `safe_note` | Empty management note, at most 256 Unicode code points. It is separate from the private owner note. |
| `recurring_limits` | `[]`; zero to sixteen rules with the existing [recurring-limit semantics](api-contract.md#75-recurring-donation-key-limits). New rule `id` is omitted or `null`. Nullable alignment and week-start fields may be omitted when their mode does not need them. |

Descriptions and approval notes are limited to 1,024 Unicode code points. Keys are limited to 64 KiB each, within the total request budget. Secrets must be valid UTF-8 without control characters. Optional scalar fields cannot be `null` unless the table explicitly permits it; boolean and numeric fields do not accept strings. Exact existing domain limits still apply.

The server creates one new endpoint and all its keys, submits **one donation**, approves it as the caller, and saves all settings in a single transaction. The donation owner and recorded level-5 reviewer are that steward. All resources, encrypted secrets, review records, recurring rules and the replay receipt either commit together or roll back together. Duplicate secrets in one request are rejected. Independent new requests create new resources, even for an existing URL, subject to current resource limits and credential-review protections.

Success is HTTP 201:

```json
{
  "endpoint_id": "12",
  "donation_id": "34",
  "keys": [
    {"endpoint_key_id": "56", "donation_key_id": "78"}
  ]
}
```

IDs in `keys` follow the request order. The response contains no secret or display fragments. Failures use the normal error envelope with a safe reason and, when relevant, a field or operation such as `keys[1]` or `approval`. The operation has a 30-second limit. If a response is lost, retry with the same idempotency key and body.

## Discover or add a model, then bind keys

`POST /api/steward/automation/model-bindings`

```json
{
  "charity_model_id": "9",
  "donation_key_ids": ["78", "79"],
  "upstream_model_id": "exact-upstream-model-id",
  "manual": false
}
```

All fields except `manual` are required. The charity model must already exist. Resource IDs are positive decimal strings; the key list contains 1–100 unique donation-key IDs. The exact upstream model ID contains 1–512 Unicode code points with no surrounding whitespace or controls. `manual` defaults to `false`.

- **Automatic:** each key must successfully fetch its model list during this request, and that fresh list must contain the exact model ID. A cached list, manual entry or existing binding cannot stand in for discovery. A concurrent refresh that replaces the result makes that item's evidence stale.
- **Manual:** add the exact model ID to the caller's manual catalog if absent, then verify it before binding. There is no `provider` parameter: new entries get an empty note, and existing entries keep their note. The manual catalog's stored `provider` field is display metadata and never becomes part of the upstream model ID.

Every key must still be owned by the steward and belong to an approved, active donation. Foreign and nonexistent keys both return `not_found`. An identical binding counts as success and retains its order; new bindings append in input order. Existing successes remain if another key fails. Other managers' concurrent changes are preserved.

The call waits synchronously for at most 60 seconds. Each discovery, including waiting for shared worker capacity, has a 15-second limit. Cancellation stops pending outbound work. Unfinished items return `incomplete`; retry the desired items. No background batch or status-query endpoint is created. Bounded discovery-evidence cleanup may finish after cancellation, but it cannot create a late binding.

Unlike creation, this endpoint has no whole-request replay receipt and does not require `Idempotency-Key`. Repeating it performs fresh discovery or checks again, while reusing identical bindings. Do not assume a repeated request returns an identical result after upstream or resource changes.

```json
{
  "charity_model_id": "9",
  "results": [
    {"donation_key_id": "78", "status": "success"},
    {
      "donation_key_id": "79",
      "status": "failed",
      "code": "model_not_found",
      "message": "the current upstream model list does not contain the requested model"
    }
  ]
}
```

The result order always matches the input. At least one success gives HTTP 200. If none succeeds, the response is 504 when any item is incomplete, otherwise 422. Invalid whole requests return 400; a missing target model returns 404; entry authorization failures return 401 or 403. Per-item `discovery_failed` includes a safe authentication, rate-limit, timeout, protocol, transport or interruption reason; raw upstream diagnostics are never exposed. If authority is lost during processing, subsequent items fail without further upstream requests.

## Read or edit one donation key's failure policy

`GET /api/steward/automation/donation-key-failure-policy?donation_id=34&donation_key_id=78` accepts exactly these two positive decimal-string IDs and no body. A current steward may manage any donation visible in the steward management pages; this does not grant access to its owner's private resources or model catalog.

GET and a successful PATCH return HTTP 200 with the same safe shape:

```json
{
  "donation_id": "34",
  "donation_key_id": "78",
  "failure_disable_threshold": "10",
  "failure_streak": "12",
  "failure_disabled": true,
  "revision": "3"
}
```

`revision` belongs to the donation. Fetch it before an edit, then send:

```http
PATCH /api/steward/automation/donation-key-failure-policy
Idempotency-Key: REPLACE_WITH_A_UNIQUE_RANDOM_KEY
```

```json
{
  "donation_id": "34",
  "donation_key_id": "78",
  "expected_revision": "3",
  "failure_disable_threshold": "0"
}
```

All four body fields are required strings. The threshold has the same U128 range as creation; no signs, leading zeros, fractions, exponents or `null`. IDs and revisions fit positive signed 64-bit integers. Never convert these strings through JavaScript `Number`.

Saving keeps the count and generation, immediately sets error-disablement when a positive threshold is reached, and clears that flag otherwise. Zero keeps counting errors but never disables for them. Success still clears the count. Manual disabling, expiry, withdrawal, bans and quotas are unchanged. An in-flight result uses the threshold saved when its result is folded; results from an older generation remain isolated. No second confirmation is required; browser pages display a persistent warning for zero.

PATCH uses the same 24-hour idempotency rules as creation, with a 30-second operation limit. Repeating the original key and body returns the original policy snapshot without another revision, audit or alert. Current permission and visibility are checked before replay. A stale revision or a reused key with a different request returns 409; re-read after a definite revision conflict and use a new key for the newly intended edit. An unknown result must first be resolved by retrying the original request. Invalid input returns 400, missing or no-longer-visible records 404, oversized input 413, and authentication or authority failures 401/403. Capacity and maintenance failures follow the shared controls.

Creation, discovery and policy edits use existing account, donation, catalog, quota, review and replay records. Safe exports include the threshold. Policy audits contain no key secrets; alerts occur once per transition into error-disablement. Existing account export, deletion, anonymization and retention rules apply; no new category of persisted request content is introduced.
