# Limited image generation activity

The picture-book activity uses one dedicated administrator-configured endpoint
and key. Image generation is available only inside this activity; ordinary
personal and charity API models do not expose image-generation requests.

## Configuration and charging

The picture-book activity uses a single administrator-configured connection to its
supported upstream image-service specification. Administrators set the endpoint
and encrypted key, enable or disable discovered models, and set each model's
prices. Model discovery supplies the catalog, supported parameters, and size
capabilities automatically. Administrators do not configure protocol details,
request or response formats, parameter catalogs, or mappings. User responses
contain local model identifiers and model-specific supported parameters; they
never contain the endpoint, credentials, upstream model or task identifiers, or
raw upstream errors.

The activity starts hidden with no opening period. Hidden activities remain
accessible by their direct link; participation still requires an open period and
a ready backend. Reopening preserves balances and exchange history. The default
exchange prices are 1,000 general points for one draft paper and 10,000 for one
brush. Paper has no supply cap. The initial brush cap is ten cumulative
exchanges across all users. Reducing that cap never removes existing balances;
task refunds restore the user's balance without restoring exchange supply.
Neither activity currency can be exchanged back or into the other.

The common activity write requires `expected_revision`, `visible`,
`starts_at`, `ends_at`, `paused`, and `module_config`. Times are Unix seconds
with start before end, or both null for an unconfigured period. For this
activity, `module_config` contains `paper_price` and `brush_price` as positive
general-point strings with up to three decimal places, and `brush_cap` as a
nonnegative whole-unit string.

The upstream write at `PUT A/upstream` requires `expected_revision`,
`base_url`, and `secret`. Use `{"mode":"keep"}` to retain the existing key, or
`{"mode":"replace","value":"..."}` to replace it. Reads expose
`secret_set`, never the key. The service manages request limits and integration
details; they are not writable fields. Upstream saves return `{revision}`.
Refetch authoritative state after saving or replaying a save.

Model writes at `PUT A/models/{id}` require `expected_revision`, `enabled`,
and `price`; `pricing` is optional. `price` gives whole-unit draft-paper and
brush amounts per image, for example:

~~~json
{"paper":"2","brush":"1"}
~~~

At least one currency must have a positive price. When present, `pricing`
contains `default`, `fallback`, `tiers`, and `sizes`. The `default` pair must
match `price`. Each `tiers` item contains a discovered tier with paper and
brush amounts; each `sizes` item contains width, height, and its paper and brush
amounts. An exact width and height price takes precedence over a matching tier.
If neither applies, `fallback` set to `default` uses the default pair; setting
it to `unavailable` rejects the unmatched selection. Pricing does not make an
unsupported size available. If `pricing` is omitted for an existing model, its
tiers, exact-size prices, and fallback are retained while `price` updates its
default. For a new model, `price` supplies the default.

A successful task reserves both currencies at the selected per-image price
times the requested image count. A complete, valid terminal response with at
least one valid image charges the accepted total, including partial success.
An invalid or truncated response does not establish successful generation.

The no-charge `POST /api/limited-activities/picture-book/quote` resolves a
selection and returns `model_revision`, `pricing_revision`,
`effective_selection`, `unit`, `total`, `basis`, and `price_key`. It creates no
task and sends no generation request. Submission includes the model revision
and `expected_pricing_revision` the user reviewed. If price or capability
changes before acceptance, the server returns `409 refresh_required` so the
user can review a fresh quote. Accepted tasks retain their original revisions,
selected price, and execution configuration. Disabling a model cancels its
queued tasks and refunds them; tasks already dispatched finish under their
original configuration.

### Automatic model catalog and readiness

`POST A/models/refresh` refreshes the discovered catalog. The administrator
model list reports `capability_readiness` and `capability_issues`;
`capability_issues` is always an array of safe
`{model_id,field_path,code,safe_message}` items. It also returns read-only
`capability_revision`, `pricing_revision`, `parameters`, `combinations`,
`size_capability`, `catalog_type`, and `missing` facts. Unconfigured models
report capability and pricing revisions as `"0"`. Administrators cannot edit
these capability or parameter fields. A newly discovered model with unsupported
or incomplete metadata remains pending and cannot be newly enabled for quotes
or tasks. Existing valid model settings are preserved across refreshes.

The user model list exposes each model's available parameters and size
capability. Task requests may include `prompt`, `negative_prompt`, `n`,
`size`, `aspect_ratio`, `resolution`, `seed`, `steps`, `guidance`, and
`quality`; availability and accepted values vary by model and come from the
model list. Unsupported fields or values are rejected.

`POST A/models/check` validates a reduced model draft and a sample task
selection against local configuration. It returns `valid`, `issues`,
`effective_parameters`, `effective_selection`, and a quote without contacting
the upstream service or charging currency. `POST A/models/batch` accepts up
to 50 model settings in `{models:[{id,input}]}` and validates the full batch
before saving; any issue leaves every model unchanged. Unsaved administrator
edits remain in page memory and prompt before switching models or leaving the
page.

## API endpoints

Use the session authentication, station, and CSRF requirements in the
[API contract](api-contract.md). Activity execution uses user sessions; private
configuration and recovery require an administrator. Activity mutations require
one `Idempotency-Key` and JSON content. Retry an uncertain site request with the
same key and payload. This never authorizes a second generation request.

In the table, `U` means `/api/limited-activities/picture-book` and `A` means
`/admin/api/limited-activities/picture-book`.

| Access | Method and route | Result or purpose |
| --- | --- | --- |
| User | `GET /api/limited-activities` | Visible activity directory |
| User | `GET U` | Activity status and exchange supply |
| User | `GET U/wallet` | General points, draft paper, and brushes |
| User | `POST U/exchange` | `{asset,quantity}`; receipt, wallet, and supply |
| User | `GET U/models` | Public model page and automatically discovered capabilities |
| User | `POST U/quote` | Resolve the selection and exact price without reserving or generating |
| User | `GET U/queue` | Queue totals and the caller's positions |
| User | `GET U/tasks` | Caller's recent task page |
| User | `POST U/tasks` | `{model_id,expected_model_revision,expected_pricing_revision,prompt,...}`; `{task}` |
| User | `GET U/tasks/{id}` | Task state and billing result |
| User | `POST U/tasks/{id}/cancel` | Empty object; `{task}` |
| User | `GET U/tasks/{id}/images/{index}` | Original image bytes; zero-based index |
| Administrator | `GET / PUT A` | Opening period, visibility, pause, and exchange configuration |
| Administrator | `GET / PUT A/upstream` | Private endpoint and key settings |
| Administrator | `GET A/upstream/controls` | Current and older physical upstream controls |
| Administrator | `POST A/upstream/resume` | `{control_id,expected_revision,reason}`; `{control}` |
| Administrator | `GET A/models` | Private discovered model catalog and readiness |
| Administrator | `GET A/models?type=all&page=1&page_size=20` | Numbered catalog, including unconfigured and missing models |
| Administrator | `GET / PUT A/models/{id}` | Read model facts; write enablement and prices |
| Administrator | `POST A/models/check` | Local draft validation and no-charge preview |
| Administrator | `POST A/models/batch` | Atomic validation and save of at most 50 model settings |
| Administrator | `POST A/models/refresh` | Empty object; `{operation}` |
| Administrator | `GET A/models/refresh/{id}` | Discovery operation state |

The administrator catalog uses `type=image|unknown|other|all`, optional
`q`, `configured`, `enabled`, and `catalog_revision`, and `page` 1–1000
with `page_size` 20, 50, or 100; it returns
`{data,total,revision,page,page_size}`. The default catalog type is `image`.
Other model, task, and control lists use `page_size` (1–100, default 20) and
optional `cursor`, returning `{data,next_cursor}`. Task status reads return a
bare task; submit and cancel return a `task` wrapper. Currency amounts and
revisions are decimal strings. Exchange `asset` is `sketch_paper` or
`sketch_brush` and `quantity` is a positive whole-unit string.

The Renge cover accompanies a four-frame waiting animation that advances every
two seconds per frame. The animation respects reduced-motion settings and can
pause manually or while the tab is in the background. This does not change the
browser's existing refresh schedule or stop server-side task processing. The
server schedules the first upstream status query six seconds after the receipt,
then schedules each normal query six seconds after handling the previous response.
A valid bounded upstream `Retry-After` that requests a longer
wait can extend either interval; the shared request limit, original deadline,
and stored wait across restart still apply.

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

A generation submission is sent at most once. Under ordinary conditions, the
server schedules the first status query six seconds after the receipt and each
subsequent query six seconds after handling the previous response. A valid,
bounded upstream `Retry-After` that requests a longer
wait may extend either delay, including the initial delay from the submission
receipt. The original execution deadline and shared activity request limit still
apply; a wait already stored for an accepted task is resumed after restart.
Status queries never repeat generation submission.

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
