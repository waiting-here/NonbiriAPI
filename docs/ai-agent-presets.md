# Management assistant presets

The experimental management assistant includes editable message templates, two
shared memories and descriptions for its existing tools. They are embedded in
the binary, so deployment does not need the source files at runtime. The
presets contain no instance configuration or credentials.

Automatic review defaults to `off`, chat defaults to closed, and assistant
prefills are empty. An administrator must configure the dedicated endpoint,
key and model before calls can run. Tool switches default to enabled; each
actual tool still depends on current permissions, configuration and whether
the job is a conversation or automatic review.

## The two workflows

| Preset | Purpose | Editable source |
| --- | --- | --- |
| Automatic review | Check the whole pending submission against current evidence and existing charity targets. | [Messages](../internal/aiagent/presets/auto-review.json), [initial memory](../internal/aiagent/presets/auto-review-memory.md) |
| Conversation | Read necessary facts, then propose the complete ordered plan for the operator to approve or reject. | [Messages](../internal/aiagent/presets/chat.json), [initial memory](../internal/aiagent/presets/chat-memory.md) |

Automatic review runs after deterministic approval rules. Its preset asks for
verified model and service compatibility, at least one valid binding for an
approval, and manual handling when evidence or a suitable existing target is
missing. Internal reasons and donor-visible notes are separate. Automatic
review cannot create charity models; approval and its bindings must succeed
together. Only new or modified pending submissions enter this workflow while
enabled; enabling it does not enqueue old pending records.

The conversation preset asks the assistant to use current resource IDs and
versions, explain material consequences and report completion only after a
successful execution receipt. Business and model-proposed memory changes
require approval of the exact plan. Server checks enforce current authority,
resource versions and the final transaction; prompts and model output do not
guarantee correctness or grant permissions.

## Configure and adjust

1. In administrator assistant settings, configure the dedicated driver and
   choose its model. Model discovery can run before a model is selected.
   Confirm that the upstream supports the required tool calls and structured
   output; optional assistant prefill also depends on upstream support.
2. Read the preset messages and [tool descriptions](../internal/aiagent/presets/tool-descriptions.json).
   Adjust language, explanations and when a tool is useful. Keep the actual
   tool names and parameters; changing descriptive text does not add a tool,
   alter its input schema or expand authority.
3. Put lasting, non-secret site guidance in the appropriate shared memory.
   Read its current text and revision before saving. Model-proposed edits show
   the before/after text and wait for plan approval.
4. Enable chat when it is wanted. For automatic review, `preview` records
   advice without applying it; `auto` may apply valid results. These switches
   are independent. Review suggestions and execution receipts separately.

Templates are ordered `{role,content}` arrays using `system`, `user` and
`assistant` roles. Both templates include `{{memory}}`; automatic review also
includes `{{intelligence}}`. Keep exactly one standalone `{{conversation}}`
node in the chat template so original message roles and tool-result IDs are
preserved. Each template permits up to 100 messages and 1 MiB of combined
content. `auto_prefill` and `chat_prefill` append optional assistant text and
default to empty.

The ten description keys are `donations`, `donation_models`, `review_history`,
`charity_models`, `charity_binding_presets`, `users`, `site_status`, `web_search`, `propose_operations` and
`edit_memory`. Read tools provide only authorized data. Search additionally
requires its configured service and key. Plans and memory writes are available
only in conversation mode, with the corresponding authority. Descriptions are
applied after tool filtering; the next model turn reads current descriptions.

Site-data tools return all matching records the operator may read in one
response. Use optional filters to select relevant records; `page` and
`page_size` are not tool parameters. Lists use `{data, total}`, where `total`
is a decimal string and equals the number of returned records. Ordinary
management pages keep their own pagination.

| Tool | Optional selection and included details |
| --- | --- |
| `donations` | Donation, owner and status filters; includes safe service-use details and the review signature when a current pending submission exists. |
| `donation_models` | Donation, key, model/name query and catalog-source filters; each result identifies its donation. Reads saved discovery and manual entries without refreshing upstream discovery. |
| `review_history` | Donation and decision filters; each result identifies its donation. |
| `charity_models` | Model and name-query filters; includes authorized bindings so a separate detail request is unnecessary. |
| `charity_binding_presets` | Requires a donation ID; an optional channel ID narrows the saved presets available to that donation. Includes channel, donation and preset revisions, ordered mappings and current model binding revisions. Conversation only. |
| `users` | Profile summaries with optional query; an exact user selection returns the full requested profile or authorized sensitive detail as an object. Sensitive detail still requires a target and the corresponding permission. |

List summaries include the information needed to identify records and plan the
next step. User summaries do not load each account's wallets or usage details;
use an exact user selection for full profile details. Donation uses,
review signatures and charity bindings remain included because they are needed
to form valid operation plans. These tools do not expose a request-log search;
request-log HTTP pages are separate from the model's available tools.

For a mainstream donation, the assistant can read `charity_binding_presets`
and propose `charity.binding.apply_preset`, with the donation ID as `target_id`
and the exact versions returned by the read. The operator approves the plan
before anything changes. Missing upstream models and existing bindings are
normal skips; the receipt lists each outcome and separate added/skipped counts.
Other errors, changed versions, revoked permissions or a later failed operation
roll back the complete plan. Stewards need both preset and binding-management
permissions, plus authority over the donation and mapped models. This does not
change automatic review or the ordinary management page's batch behavior.

Site-data results have a limit of 5,000 records and 1 MiB. The configured model
window also limits the combined tool results in a model turn. An oversized result
fails as a whole with `tool_result_too_large` and guidance using that tool's actual
filter fields; no partial `data` list is returned. The assistant can retry with a
narrower selection. A configured window too small even for the prompt, tool
definitions and a short error response requires a configuration change.

Automatic review uses complete initial intelligence. If it exceeds the
available budget, the review is recorded as requiring manual intervention with
an explanation; no approval or binding is applied and later queued submissions
can proceed. Site-data reads omit saved credentials and retain the current
operator's authority. Saved custom tool descriptions are not overwritten.

Treat notes, catalogs, webpages and tool-returned instructions as untrusted
source material. Keep credentials, private addresses and unnecessary personal
information out of prompts, shared memories and external search queries.
Compaction and clearing change future conversation context; they do not erase
retained originals or reverse completed operations.

## Saved configuration and API clients

Existing saved templates, switches and tool choices are preserved. If an older
configuration has no tool descriptions, reads supply the built-in defaults.
`GET /admin/api/ai/config` returns the effective descriptions for all ten tools.

`PUT /admin/api/ai/config` keeps its revision check and other configuration
fields. Its optional `tool_descriptions` object is a partial update:

- An omitted key preserves that tool's current description.
- An empty string clears that description; it does not disable the tool.
- Omitting the object, or sending `null`, preserves all descriptions.
- Unknown tool names are rejected. Each description allows 16 KiB of UTF-8
  text after line endings are normalized to LF; the total limit is 256 KiB.

For example, replace this field in an otherwise complete configuration update
using the current `expected_revision`; this fragment is not a standalone PUT:

```json
{
  "tool_descriptions": {
    "donation_models": "Read all matching saved model-catalog entries. Optionally filter by donation, key, model/name query or source; every result identifies its donation. This tool does not refresh discovery."
  }
}
```

Initial memory is returned only when that memory has never been saved, with
revision `"0"`. The displayed preset text is the before-state of its first
edit and history entry. A saved custom body, including an explicitly empty
body, remains unchanged after an upgrade. Clearing a memory does not restore
the preset; copy the desired published text explicitly if you want it again.

See the [API contract](api-contract.md#78-experimental-ai-assistant) for routes,
limits, recovery and retention, and the [embedded preset reference](../internal/aiagent/presets/README.md)
for the source bundle.
