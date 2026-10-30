# Connect Codex to the management assistant

An administrator can connect one Codex account to the instance for administrator
conversations and individually selected stewards. This is a separate source from
the [ChatGPT connection](chatgpt-agent.md). The Go server performs device-code
login and HTTP/SSE inference itself; no external CLI or helper process is needed.

## Connect and assign

1. Open administrator AI settings and start Codex device-code login.
2. Open the displayed official verification page, enter the one-time code and
   complete the account/workspace authorization there.
3. Wait for the local connection status to become connected. Add a Codex driver,
   load its available models and select a model.
4. Assign the driver to administrator chat or to each intended steward
   individually. The steward must also retain the existing AI workspace and
   tool permissions.

Only administrators can start/cancel login, disconnect, configure the driver or
assign it. Codex cannot be selected as the global default, the all-steward
default or the automatic-review source. It is absent from personal and charity
model catalogs and public API routing. Removing an individual assignment restores
the normal source-selection rules; it does not grant inherited Codex access.

Only one pending login is held in memory, with a displayed expiry. Cancel it
when it is no longer needed; restart requires a new pending login. A successfully
saved connection persists encrypted. The browser reads local status and never
receives access or refresh tokens. The one-time device code belongs only on the
official authorization page.

Account/workspace eligibility and model access are determined upstream. Real
Business-account authorization and inference have not yet been verified; local
fixtures do not establish that access. A successful local status is not evidence
of a recent successful upstream authentication, model discovery or quota read.

## Usage and limits

The connected card provides administrator-only quota and usage information.
An individually assigned steward can use the model within existing permissions
but cannot read the account's quota, manage its connection or retrieve these
operation diagnostics.

Official quota comes from the account's read-only usage endpoint. The card shows
the plan and workspace name only when supplied, each available quota bucket and
window, reported percentages, window length and reset time in the site timezone.
Missing values remain unavailable. Windows are not assumed to be five hours or
one week, and percentages are not converted to a request or token allowance.
Explicit credits, units and unlimited states retain their upstream meaning.
When a reset time passes, the display waits for a fresh query instead of assuming
that the allowance has reset. Credential expiry in connection details is separate
from subscription expiry and quota reset.

The separate local summary counts the preceding 24 hours of retained Codex
requests and their reported input, output, cache and reasoning tokens, including
the number with unknown usage. It covers **all Codex connections previously used
by this instance**, not just the currently connected account. It excludes other
providers and public/charity calls. Request cleanup can reduce this summary
because its provider attribution depends on retained request records; independent
assistant usage totals remain unchanged. Open the filtered request records from
the card to inspect the same provider and time interval.

Opening the active card reads quota when its data is stale. Queries share a
30-second server cache and overlapping reads are combined; **Refresh** requests
a fresh result without background polling. A failed refresh keeps the last valid
quota marked as stale and offers a local retry. Disconnecting or replacing the
connection clears that cache, cancels related reads and rejects late results.
These reads do not enable Reserve, purchase credits or redeem quota resets.

The native read uses the fixed official `/backend-api/wham/usage` HTTP endpoint.
The [official account rate-limit documentation](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt)
describes the semantics; the implemented HTTP mapping follows the
[pinned upstream source](https://github.com/openai/codex/blob/c92700d2993707ee16eb63b35a1cdd7fcc563f69/codex-rs/backend-client/src/client/rate_limit_resets.rs).
It does not run an App Server process or expose its RPC as a site API.

## Model discovery and failure details

Loading models reads the saved source configuration. A failed pull keeps the
draft, selected model and last successful catalogue with its update time; it
does not switch sources or bindings. A valid empty catalogue is distinct from a
failed pull. Retry or cancel the local operation without discarding the editor.

Connection, catalogue and quota failures show a safe summary and expandable
details: operation, source, occurrence time, local diagnostic ID, known failure
stage, elapsed time, separate site/upstream status, safe upstream error code,
request correlation and Retry-After when available. Copy the sanitized details
when investigating. No received response is shown as unknown upstream status,
not inferred from the site's HTTP error. Tokens, device codes and raw OAuth
responses are excluded.

Follow a reported waiting period before retrying a rate-limited request.
Upstream authentication rejection does not sign the administrator out of this
site. A rejected access token permits one serialized refresh and retry; 403,
429, timeout and parse failures do not start a refresh loop. Reconnect when the
connection is explicitly reported disconnected or its credentials cannot be
recovered, rather than treating every upstream error as a login failure.

Safe operation diagnostics are administrator-only, kept for up to 30 days and
bounded to the newest 1,000 records. They are separate from inference-request
history and its manual cleanup. If saving the diagnostic fails, the current
error still includes a local ID and basic details, marked as not saved. The
[API contract](api-contract.md#administrator-operation-diagnostics) documents
retrieval and response fields.

## Requests and continuation

The source uses a fixed official endpoint with `stream:true` and `store:false`.
It supports text, function tools and results, and native reasoning history for
stateless continuation. Compatible native history stays with its driver, model
and connection; switching sources does not forward incompatible opaque state.
A valid completed response may omit repeated output when matching completed
items already supply it. EOF or an item-done event alone is not completion.

The local context window and output reserve guide history budgeting. Codex does
not send the API drivers' output-token limit, temperature or top-p. Optional
parameters are limited to reasoning effort `none|minimal|low|medium|high|xhigh`,
reasoning summary `auto|concise|detailed`, text verbosity `low|medium|high`,
`parallel_tool_calls`, `prompt_cache_key` and service tier
`auto|default|priority`. Actual model support remains an upstream constraint.

Each model call uses the shared outbound, cancellation, queue and global limits,
and appears in existing administrator assistant request history. Failure does
not silently switch providers or replay an interrupted stream. Source selection
does not expand tool permissions or replace approval of a proposed write plan.

## Disconnect and storage

Refresh is serialized and saves rotated credentials atomically. A normal refresh
keeps connection identity; replacement and disconnect invalidate the previous
identity. Disconnect clears active local credentials, cancels related active
requests and prevents queued work from using the old connection. It does not
erase retained conversations, memories, usage or request history.

The connection is an administrator asset included in complete protected backups
and excluded from personal account export 31. Pending device codes are not
persisted. Connection-management records retain safe outcomes, not codes, tokens
or authentication response bodies. See the [API contract](api-contract.md#codex-device-code-connection)
and [lifecycle rules](data-lifecycle-checklist.md#management-assistant-assets).
