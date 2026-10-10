# Managed charity session headers

A charity model can configure **Managed session headers** in its model settings. Each configured header receives a cryptographically random UUID v4 or 32-character lowercase hexadecimal value. Header names need not contain “session”.

Values are isolated by user, charity model and header name. Concurrent requests share the value, and retries keep the value already selected for their logical request. An in-progress request keeps its session available; after the last sent request finishes, the idle lifetime begins. The default is five minutes. A new request within that window reuses the value.

A failure, timeout or cancellation refreshes the lifetime if request headers were written to the upstream. Local rejection and failed connection setup do not refresh it. Values and active uses are persisted in SQLite; startup recovery releases unfinished uses. A still-valid value survives a restart. This improves reuse opportunities without guaranteeing that an upstream retains its cache.

Client-abuse checks and configured penalties run on the original inbound headers before any replacement. See the [request admission standard](request-admission.md), which also applies to future request transformations.

Managed values replace same-named fixed and forwarded headers. They cannot override authentication, cookies, protocol, proxy or connection-control headers. Normal client-header forwarding does not gain permission to forward session headers.

## Configuration API

Administrator and full-steward model create, PATCH and detail DTOs include `session_headers`. Existing model-setting authority, revision checks, strict JSON parsing and idempotency rules apply. Trainee permissions do not expand. CallerKey automation can read model configuration under its existing scope; it gains no new write route.

```json
{
  "session_headers": [
    {"name":"X-Session-ID","idle_seconds":300,"format":"uuid_v4"},
    {"name":"X-Cache-Key","idle_seconds":600,"format":"hex_32"}
  ]
}
```

This is a model-body fragment. PATCH still requires `expected_revision` and `Idempotency-Key`.

Each item requires `name`; omitted `idle_seconds` and `format` default to 300 and `uuid_v4`. Idle lifetime is an integer from 1 to 86,400 seconds. At most 16 headers are allowed; names are ASCII HTTP tokens up to 128 bytes, normalized to lowercase and unique without regard to case. Unknown fields, null values, invalid formats, protected names and invalid lifetimes return 400.

Creation without the field disables the feature. PATCH omission preserves it; an empty array clears it. Responses return canonical configuration, including defaults, never generated values. Changing a header's format or lifetime selects a new value for subsequent requests while existing requests retain their selected value. Removing a header stops new requests from receiving it.

## Data lifecycle

Session values are temporary upstream correlation identifiers, not authentication credentials. They are not returned through configuration or public model APIs, logged, exposed in raw error diagnostics, or included in account-content exports. They may allow the selected upstream to associate requests during the session.

Expired inactive values are removed by bounded maintenance. Deleting the user or model removes their cache and active-use records. Late completions update only surviving records and cannot recreate deleted data or extend a replacement value. Account exports retain the existing personal charity-consumption aggregate.
