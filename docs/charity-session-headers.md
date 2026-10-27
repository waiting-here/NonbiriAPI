# Managed session headers

Personal and charity models offer the same **Managed session headers** fields in model settings. The configuration is saved with the model. **Request adaptation** has a separate save and groups fixed/forwarded request headers and request-body settings. Saving one form does not save the other.

Each configured header receives a generated value in one of these formats. Header names need not contain “session”.

| `format` | Value |
| --- | --- |
| `uuid_v4` | Random UUID v4; the default. |
| `hex_32` | 32 lowercase hexadecimal characters. |
| `opencode_session` | `ses_`, 12 hexadecimal time/counter characters in descending time order, and 14 random alphanumeric characters. |
| `opencode_message` | `msg_`, 12 hexadecimal time/counter characters in ascending time order, and 14 random alphanumeric characters. |
| `opencode_part` | `prt_`, with the same ascending layout as message IDs. |

Values are isolated by user, model and header name, with separate personal and charity scopes. With a positive idle lifetime, concurrent requests share the value. An in-progress request keeps its session available; after the last sent request finishes, the idle lifetime begins. The default is five minutes. A new request within that window reuses the value.

**An idle lifetime of 0 generates a new value for every logical request.** Internal retries of that request reuse its selected value. Values are not reused from another request, but no format guarantees absolute uniqueness.

A failure, timeout or cancellation refreshes a positive idle lifetime if request headers were written to the upstream. Local rejection and failed connection setup do not refresh it. Values and active uses are persisted in SQLite; startup recovery releases unfinished uses. A still-valid positive-lifetime value survives a restart. This improves reuse opportunities without guaranteeing that an upstream retains its cache.

Client-abuse checks and configured penalties run on the original inbound headers before any replacement. See the [request admission standard](request-admission.md), which also applies to future request transformations.

Managed values replace same-named fixed and forwarded headers. They cannot override authentication, cookies, protocol, proxy or connection-control headers. Normal client-header forwarding does not gain permission to forward session headers.

## Configuration API

Owner personal-model create, PATCH and detail DTOs and authorized management charity-model DTOs include `session_headers`. Existing ownership, model-setting authority, revision checks, strict JSON parsing and idempotency rules apply. Steward changes require the applicable `charity.create` or `charity.model_settings` grant and current source/model scope. Authorized CallerKey model reads include the saved configuration under their existing scope; this feature adds no CallerKey write route. Steward charity request adaptation remains read-only even though model settings can be edited.

```json
{
  "session_headers": [
    {"name":"X-Session-ID","idle_seconds":300,"format":"uuid_v4"},
    {"name":"X-Message-ID","idle_seconds":0,"format":"opencode_message"}
  ]
}
```

This is a model-body fragment. PATCH still requires `expected_revision` and `Idempotency-Key`.

Each item requires `name`; omitted `idle_seconds` and `format` default to 300 and `uuid_v4`. The browser's blank idle field also uses 300. Idle lifetime is an integer from 0 to 86,400 seconds. At most 16 headers are allowed; names are ASCII HTTP tokens up to 128 bytes, normalized to lowercase and unique without regard to case. Unknown fields, null values, invalid formats, protected names and invalid lifetimes return 400.

Creation without the field disables the feature. PATCH omission preserves it; an empty array clears it. Responses return canonical configuration, including defaults, never generated values. Changing a header's format or lifetime selects a new value for subsequent requests while existing requests retain their selected value. Removing a header stops new requests from receiving it.

## Data lifecycle

Session values are temporary upstream correlation identifiers, not authentication credentials. They are not returned through configuration or public model APIs, logged, exposed in raw error diagnostics, or included in account-content exports. They may allow the selected upstream to associate requests during the session.

Account export 31 includes personal models' saved `session_headers` entries (`name,idle_seconds,format`). Shared charity-model configuration and generated values are excluded; personal charity consumption retains its existing aggregate.

Expired inactive values are removed by bounded maintenance. Deleting the user or model removes its cache and active-use records. Late completions update only surviving records and cannot recreate deleted data or extend a replacement value.
