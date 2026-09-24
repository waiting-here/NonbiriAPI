# Limited image generation activity

The picture-book activity uses one dedicated administrator-configured endpoint
and key. Image generation is available only inside this activity; ordinary
personal and charity API models do not expose image-generation requests.

## Configuration and charging

Administrators configure the endpoint, encrypted activity key, HTTP request
limit, concurrent generation limit, and a declarative protocol adapter. Model
discovery creates a private catalog. A model becomes available only after an
administrator assigns its public name, supported parameter rules, and price.
User responses contain local model identifiers and approved parameter rules;
they never contain the upstream endpoint, credentials, actual model identifiers,
upstream task identifiers, adapter, or raw upstream errors.

The activity starts hidden with no opening period. Hidden activities remain
accessible by their direct link; participation still requires an open period
and a ready backend. Reopening preserves balances and exchange history.
The default exchange prices are 1,000 general points for one draft paper and
10,000 for one brush. Paper has no supply cap. The initial brush cap is ten
cumulative exchanges across all users. Reducing that cap never removes existing
balances; task refunds restore the user's balance without restoring exchange
supply. Neither activity currency can be exchanged back or into the other.

The common activity write requires `expected_revision`, `visible`, `starts_at`,
`ends_at`, `paused` and `module_config`. Times are Unix seconds with start before
end, or both null for an unconfigured period. For this activity, `module_config`
contains `paper_price` and `brush_price` as positive general-point strings with
up to three decimal places, and `brush_cap` as a nonnegative whole-unit string.

Upstream configuration uses the following required integer fields:

| Field | Initial value | Allowed range |
| --- | --- | --- |
| `rpm` | Must be configured | 1–10,000 |
| `concurrency` | Must be configured | 1–32 |
| `per_user_limit` | 1 unfinished task | 1–100 |
| `global_limit` | 100 unfinished tasks | 1–10,000 |
| `queue_timeout_seconds` | 1,800 | 60–86,400 |
| `execution_timeout_seconds` | 1,800 | 60–86,400 |
| `memory_budget_mib` | 512 | 512–4,096 |

Administrator writes include the current `expected_revision`. The upstream
write also requires `base_url`, `secret`, `image_origins`, and `adapter`.
Use `secret: {"mode":"keep"}` to retain an existing key, or
`secret: {"mode":"replace","value":"..."}` to replace it. Reads expose only
`secret_set`, never the key. Keep real endpoints, keys, model IDs and adapter
mappings in the administrator configuration; do not publish them in examples
or repository files. Upstream saves return `{revision}` and model saves return
`{id,revision}`. Refetch the authoritative configuration after saving or replaying
a save. These request bodies are limited to 1 MiB. Model writes require
`expected_revision`, `display_name`, `description`, `enabled`, `price`,
`parameters`, `combinations` and `mapping`.

Prices are whole quantities of draft paper and brushes per requested image:
`price: {"paper":"2","brush":"1"}`. Both currencies are reserved together when
the task is accepted. At least one currency must have a positive price.
The total is the model price multiplied by the requested image count.
A complete, valid terminal response containing at least one valid image charges
that entire total, including partial success. A malformed or truncated response
does not establish successful generation.

The submission includes the model revision the user reviewed. A stale revision
is rejected before a charge. Accepted tasks keep their original price and
execution configuration. Disabling a model cancels its queued tasks and refunds
them; tasks already sent upstream finish under their original configuration.

## Declarative adapter and public parameters

The adapter contains `discovery`, `submit`, optional `poll`, and `response`.
Discovery and polling use GET; generation uses POST. Paths are bounded relative
paths, with one `{task_id}` placeholder permitted only in the polling path.
Mappings use JSON Pointers, a model destination, declared parameter destinations
and scalar constants. A model may override the complete submission mapping;
an empty model mapping inherits the upstream mapping. No scripts, expressions,
dynamic headers or user-supplied destination paths are evaluated.

This fictional adapter illustrates the schema; it does not identify an upstream:

```json
{
  "discovery": {"method":"GET","path":"/catalog","items_pointer":"/items","id_pointer":"/name"},
  "submit": {
    "method":"POST",
    "path":"/render",
    "mapping": {
      "model_pointer":"/engine",
      "parameters":{"prompt":"/text","n":"/count","size":"/canvas"},
      "constants":[]
    },
    "receipt":{"indicator_pointer":"/queued","indicator_value":true}
  },
  "poll": {"method":"GET","path":"/work/{task_id}"},
  "response": {
    "task_id_pointer":"/ticket",
    "state_pointer":"/phase",
    "working_states":["rendering"],
    "success_states":["complete"],
    "failure_states":["rejected"],
    "images_pointer":"/pictures",
    "base64_pointer":"/bytes"
  }
}
```

Optional `submit.receipt` separates submission receipts from polling states.
Its indicator is compared exactly with a configured boolean or nonempty string
of at most 128 UTF-8 bytes. A matching indicator and a nonempty safe task ID
accept an asynchronous receipt without requiring a state field. Alternatively,
a complete, nonempty image array without a receipt or task ID can be a
synchronous result. Image decoding and validation still determine success.
Explicit failure states, conflicting receipt/state/image facts, malformed JSON,
and oversized image arrays cannot be treated as successful images.
The indicator, task ID, image and state pointer trees must not overlap.
Polling continues to use the configured explicit state lists; unknown states
are retried within the original execution deadline. Without `submit.receipt`,
the existing common state decoder handles both submission and polling.

Image extraction uses `images_pointer` with `base64_pointer` and/or
`url_pointer` relative to each image item. URL image downloads require
an explicitly configured origin in `image_origins`, at most eight entries.
All requests still use the shared egress policy. Adapter JSON is limited to
256 KiB, pointer/path text to 512 bytes, and JSON nesting to 32 levels.

Public model rules support `prompt`, `negative_prompt`, `n`, `size`,
`aspect_ratio`, `resolution`, `seed`, `steps`, `guidance` and `quality`.
A rule declares support, requirement, scalar type, bounds, step, enum and/or
default. String lengths use `utf8_bytes`, `unicode_scalars` or `utf16_units`.
Allowed combinations restrict tuples of declared values. Rules, defaults,
enums and combinations are validated by the server. `n` is an integer from
1 to 16, subject to tighter model limits; an omitted value uses the model's
configured default, or one when no default is configured.
User submissions are limited to 256 KiB; prompt and negative prompt together
are limited to 64 KiB.

A supported string `size` may additionally declare independent dimensions:

```json
{
  "key":"size","supported":true,"required":false,
  "type":"string","length_unit":"utf8_bytes",
  "dimensions":{
    "format":"width_height",
    "width":{"minimum":120,"maximum":920,"step":40},
    "height":{"minimum":150,"maximum":950,"step":50}
  },
  "default":"200x250"
}
```

Each axis requires integer `minimum`, `maximum` and `step` from 1 to 65,536;
minimum must not exceed maximum. Steps start at each axis's own minimum.
Values remain strings in canonical `WIDTHxHEIGHT` form: positive decimal
integers, lowercase `x`, no spaces, leading zeros, signs or exponents.
Length, enum and combination restrictions still apply. The public schema
contains only these approved constraints, not private metadata or mappings.

## API endpoints

Use the session authentication, station and CSRF requirements in the
[API contract](api-contract.md). Activity execution uses user sessions; private
configuration and recovery require an administrator. Activity mutations require
one `Idempotency-Key` and JSON content. Retry an uncertain site request with the
same key and payload. This never authorizes a second upstream generation POST.

In the table, `U` means `/api/limited-activities/picture-book` and `A` means
`/admin/api/limited-activities/picture-book`.

| Access | Method and route | Result or purpose |
| --- | --- | --- |
| User | `GET /api/limited-activities` | Visible activity directory |
| User | `GET U` | Activity status and exchange supply |
| User | `GET U/wallet` | General points, draft paper and brushes |
| User | `POST U/exchange` | `{asset,quantity}`; receipt, wallet and supply |
| User | `GET U/models` | Public model page |
| User | `GET U/queue` | Queue totals and the caller's positions |
| User | `GET U/tasks` | Caller's recent task page |
| User | `POST U/tasks` | `{model_id,expected_model_revision,prompt,...}`; `{task}` |
| User | `GET U/tasks/{id}` | Task state and billing result |
| User | `POST U/tasks/{id}/cancel` | Empty object; `{task}` |
| User | `GET U/tasks/{id}/images/{index}` | Original image bytes; zero-based index |
| Administrator | `GET / PUT A` | Opening period, visibility, pause and exchange configuration |
| Administrator | `GET / PUT A/upstream` | Private upstream configuration |
| Administrator | `GET A/upstream/controls` | Current and older physical upstream controls |
| Administrator | `POST A/upstream/resume` | `{control_id,expected_revision,reason}`; `{control}` |
| Administrator | `GET A/models` | Private discovered/configured model page |
| Administrator | `GET / PUT A/models/{id}` | Model rules, mapping and price |
| Administrator | `POST A/models/refresh` | Empty object; `{operation}` |
| Administrator | `GET A/models/refresh/{id}` | Discovery operation state |

Image model, task and control lists use `page_size` (1–100, default 20) and
optional `cursor`, returning `{data,next_cursor}`. Task status reads return a bare
task; submit/cancel return a `task` wrapper. Currency amounts and revisions are
decimal strings. Exchange `asset` is `sketch_paper` or `sketch_brush` and
`quantity` is a positive whole-unit string.

## Queue, limits, and uncertain results

The global queue is first in, first out. Users see queue totals and positions
for their own tasks. They cannot see another participant's identity. New tasks
are rejected before charging when an admission, unfinished-task, or memory limit
is reached. Each task holds one generation slot from dispatch until a confirmed
upstream terminal result.

Generation submissions, status queries, model discovery, and image downloads
share the activity's HTTP request limit. Changing configuration does not reset
the limit for the same physical endpoint and key. Existing tasks retain the
identity and configuration under which they were accepted. The administrator
control list includes older identities with unresolved slots.

A generation submission is sent at most once. The server polls an accepted
upstream task with bounded backoff, preserving the original execution deadline.
Failed status queries never cause another generation submission.

When a submission receipt is lost or the execution deadline expires without a
known result, new generation dispatches for that upstream identity pause. At
the deadline the task is refunded and shown as having an unknown result.
Read-only cleanup may continue for up to five additional minutes. This cleanup
does not change the refund, send another generation request, or deliver a late
image. The identity stays protected until an administrator explicitly confirms
recovery using the current control revision and a reason. That action does not
recharge an earlier task.

Only queued tasks can be withdrawn. Manual activity pause and site maintenance
cancel all queued tasks and refund their reservations. Already dispatched tasks
continue to query and settle. Natural activity end prevents new exchanges and
tasks but allows accepted tasks to finish.

## Memory, recovery, and privacy

The execution copy of prompts and parameters exists only in server memory.
The site does not proactively log request bodies; an upstream error that echoes
request content can nevertheless retain that content in restricted raw error
diagnostics under the [data retention rules](data-lifecycle-checklist.md).
A service restart
refunds queued tasks whose payload was lost. Known upstream task identifiers
resume status queries. A dispatch with no confirmed identifier remains uncertain
and is never submitted again.

Images are validated as PNG, JPEG, or WebP and retained only in memory for a
ten-minute pickup window. They are not written to disk, the database, or success
logs. The browser receives the original bytes through authenticated site routes.
Closing the page, service restart, or cache expiry can lose the result; a
successfully completed task is not refunded for that loss. An image already
loaded into the browser can remain available there until the user leaves the
page. There is no automatic browser persistence.

Each executing task reserves 256 MiB for bounded response and conversion buffers.
Each image is limited to 32 MiB, each task to 64 MiB of image bytes,
and each generation JSON response to 96 MiB. Returned image dimensions are
limited to 64 million pixels. Queue payloads share the activity memory budget
and may use at most one quarter of it. Slow image readers retain their memory
charge until they finish; new tasks do not evict a result still inside its
promised pickup window.

Banning an account cancels its queued tasks. Existing executions settle, but
the account cannot obtain their results. Account deletion removes personal task
data and preserves only the minimum anonymous execution information needed to
release an upstream slot. Late responses cannot recreate the deleted account
or its images.

Users can view thirty days of task status and charges or refunds. Their export
contains those safe task facts and activity accounting records, without prompts,
images, execution parameters, internal upstream identifiers, or administrator
diagnostics. Accounting records follow the existing ledger policy. Failure
diagnostics and submission source information are restricted to administrators
and full stewards and follow the site's bounded thirty-day diagnostic retention.
