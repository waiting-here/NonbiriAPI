# Limited image generation activity

The picture-book activity uses a dedicated administrator-configured upstream.
Image generation is available only inside this activity; ordinary personal and
charity API models do not expose image-generation requests.

## Configuration and charging

Administrators configure the endpoint, encrypted activity key, HTTP request
limit, concurrent generation limit, and a declarative protocol adapter. Model
discovery creates a private catalog. A model becomes available only after an
administrator assigns its public name, supported parameter rules, and price.
User responses contain local model identifiers and approved parameter rules;
they never contain the upstream endpoint, credentials, actual model identifiers,
upstream task identifiers, adapter, or raw upstream errors.

Prices are whole quantities of draft paper and brushes per requested image.
Both currencies are reserved together when the task is accepted. At least one
currency must have a positive price. The total is the model price multiplied by
the requested image count. A complete, valid terminal response containing at
least one valid image charges that entire total, including partial success.
A malformed or truncated response does not establish successful generation.

The submission includes the model revision the user reviewed. A stale revision
is rejected before a charge. Accepted tasks keep their original price and
execution configuration. Disabling a model cancels its queued tasks and refunds
them; tasks already sent upstream finish under their original configuration.

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

Prompts and execution parameters exist only in server memory. A service restart
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

The activity memory budget defaults to 512 MiB and can be configured up to
4 GiB. Each executing task reserves 256 MiB for bounded response and conversion
buffers. Each image is limited to 32 MiB, each task to 64 MiB of image bytes,
and each generation JSON response to 96 MiB. Dimensions are limited to
64 million pixels. Queue payloads share this budget and may use at most one
quarter of it. Slow image readers retain their memory charge until they finish;
new tasks do not evict a result still inside its promised pickup window.

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
