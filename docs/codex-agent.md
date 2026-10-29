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
of an unused subscription allowance.

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
