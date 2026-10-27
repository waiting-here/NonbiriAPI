# Services, endpoints and keys

A service groups upstream endpoints and saved keys. Each endpoint names a
complete base URL and protocol. New or edited submissions
explicitly authorize that protocol's complete native-service set. Grouping entries
does not authorize a credential at another destination.
The same key can be used by several endpoints within its own service without
re-entering its secret. Users select each destination explicitly. Other services
cannot read, select or reference that key. API routes retain their existing
connection and credential names.

Connection lists show the primary entry's address and note. Search includes the
name and note of the connection and the addresses and notes of all its entries.
Untouched names generated during an upgrade use the original entry's note or
hostname for display. Custom names and stored metadata remain unchanged.

## Set up and continue

The default form requires an address and API key, suggests an editable service
name, and selects OpenAI-compatible. More keys can be added before saving.
Protocol selection, mainstream presets and special list formats remain available
under advanced setup. The form submits the full service set for each protocol;
finer service choices are made when configuring model bindings.
Choose **Add and fetch models** once to run the required steps. These remain separate authorized API
operations: if a later step fails, already-created resources stay available.
Resolve an unknown response with the retained request before resuming the next
step; do not create a second service to retry.

After saving, the service opens its model lists. A single endpoint and key are
selected automatically. Multiple endpoints and keys have explicit selectors;
each endpoint/key pair has its own fetched and manual model lists. A preset can
supply model mappings. Donation starts with the selected
entry, and the donor chooses the keys and authorized lifetime to share. Existing
entry IDs and credentials remain editable through the same connection workspace.

## Configure entries and models

An OpenAI-compatible entry's protocol set includes Chat Completions, Responses,
embeddings, images, audio and the other supported native services. Select the
services used by each upstream model in its binding. Rerank keeps an advanced
format setting: generic, Cohere, Jina, Voyage or SiliconFlow; choose the provider's
actual format. The form starts with generic when no other format is selected.
Sharing a `/rerank` path does not make these payload formats interchangeable. Gateway
v3 and v4 each offer text, image generation, embeddings and rerank. Select the
upstream's actual version; there is no automatic probing or version fallback.

System One is a separate **Typesafe System One** protocol in **Add entry**.
Its service is `/v1/systemone`; the **Decisions** native service in the
OpenAI-compatible protocol uses `/v1/decisions`. Choose the applicable service
when configuring a model binding. These services are not converted into each other.

For an upstream serving both Chat and Responses at one base URL, one
OpenAI-compatible entry can serve both. Bindings select Chat Completions and
Responses independently, including models that support only one of them. For a
provider whose models need different protocols, create the required entries in one connection,
attach the same credential explicitly and select the applicable entries on each
model. Model identifiers may differ between entries and must be supplied for
each target.

Personal model configuration offers one primary **Add model sources** action.
Its visible service filter narrows endpoints, then users select a key and its
models. Selections persist across endpoint/key changes, filters and pages.
Each selected key/model pair becomes an independent binding. Already-bound pairs
are marked, and manual model entry remains available for the selected pair.

Advanced call combinations retain ordered targets within one logical binding.
Existing multi-target bindings keep their original order, routing and retry
semantics. New bindings start with the currently
authorized native services selected; editing a binding preserves its saved subset.
Keep at least one service that can serve the target model through the existing
routing matrix. Extra authorized services may remain selected. For each request,
the platform prefers a native service that can represent the request, then considers the
existing bounded conversions. It selects one target in that binding before
applying the model's routing strategy. More targets do not add retry slots or
increase a binding's weight. Existing protocol, body and transport checks still
apply; a service declaration does not promise arbitrary lossless conversion.
See [direct inference](direct-inference.md) for the supported operation matrix.

A model list belongs to one endpoint/key pair. Its advanced settings can specify
a different complete base URL and list format, using that pair's credential
version. Even when two endpoints use the same key and list URL, their fetched
and manual lists are independent. A list refresh does not
send a paid inference request. Recognized Gateway model types and Gemini methods
provide service suggestions; an ordinary OpenAI model ID alone does not prove
service support. Check the upstream model's actual support when choosing a binding
subset; suggestions do not expand its authorization or conversion capabilities.
Saved models and bindings survive a failed refresh or credential rotation.
A failed fetch retains the previous successful list and timestamp. A successful
fetch reports added/removed counts and replaces only fetched entries, including
when the new result is empty. Manual entries are never removed by a refresh.

Mainstream presets may suggest entries, a directory and per-model mappings.
Existing entry IDs are retained when editing; save new entries before assigning
model mappings to them. Updating a preset does not change existing destinations,
expand grants or approve donations.

Saving a key normally creates its pair's list settings automatically. Explicitly
opting out keeps the pair manual-only; **Model list settings** can enable fetching
later or change the pair's list address and format. Users do not create or link a
separate discovery source. Manual model entries remain available without a
fetched list and support entering one model ID per line. Importing or connecting
a model reuses an existing manual entry with the same upstream model ID under
the selected key; its existing metadata is preserved.

While creating a custom entry, entering an address can show every matching
available mainstream entry, including its protocol, native services, discovery
suggestion and model mappings. Matching uses the complete normalized URL,
including scheme, effective port and path, without contacting the provider.
Choose **Use this preset** to select that entry alone. Saving submits the selected
protocol's full service set. Switching back to **Custom** preserves the address,
protocol and note. Saving remains a separate action. Empty or unavailable suggestions do not block custom
creation, and adopting a preset does not attach credentials or approve donations.

## Key changes and donations

Changing a secret creates a new immutable version. Personal grants and current
model directories move to the new version. Approved charity uses retain their
previous version while a replacement awaits review. The owner can pause an old
use or version immediately. A replacement is never approved merely because the
old secret or the provider was previously approved.

A new donation or donor-edited submission authorizes the selected protocol's
complete service set. Donors choose keys and lifetime, while administrators or
authorized stewards choose the native services used by each charity binding.
Existing donations retain their old authorization until the donor submits an
edit; upgrades and page loads never widen old grants or approved snapshots.

Managers may fetch donated model directories for review and maintenance, without
a separate directory-sharing consent. The approval page submits one approve or
return decision, with an optional note, for the entire current pending submission.
The donation revision and complete pending set are checked in one transaction:
all decisions, audit and receipt commit together or nothing changes. Recover an
unknown result with the original request; after a definite version conflict,
reread the complete set before deciding again.

A service may still have an approved version and a pending replacement.
Returning the replacement preserves the approved version. The parent status
alone therefore does not describe every service's availability. Ordinary approval
does not create charity bindings; model configuration selects their targets and
service subsets afterward.

If model discovery is unavailable, the donor can include model IDs in the public
donation description, without secrets. Personal model entries do not
automatically become charity models.

Managed model refreshes work for pending and approved donations, including
existing donations without a separately shared directory. They use the pending
credential version when a replacement awaits review, otherwise the approved
version. A matching configured directory is reused, including a mainstream
provider's special model-list endpoint; otherwise the entry's standard model-list
endpoint is used. Directory reads never approve inference access or replace an
approved credential with a pending one. Expiry, withdrawal, disabled credentials
and other applicable availability checks still apply. A `423 resource_locked`
response alone does not mean the upstream credential is invalid.

Administrators and stewards with `donations.force_reject` can force-reject a donation that has approved
uses within their current donation scope, including when its parent retains the pending status. The action requires
a reason and complete review material, stops new charity dispatches and preserves
normal settlement for requests already sent. Legacy whole-donation approval or
rejection cannot override existing per-service approvals. Review the full pending
submission through `pending-review`, or use the authorized force-rejection action
for the whole donation.

Review results retain the service type and masked key label for each decision.
After a successful approval, reviewers with current maintenance access can
continue to charity-model configuration with that donation, key and service
selected. They can adjust the selection before saving; reviewers without that
access receive an explanation. A list refresh failure retries the read, not
the already-completed approval.

A donor edit or resubmission creates the required full-protocol pending material
while preserving the key's existing budget, consumed amounts and reservations.
Previously approved versions remain effective until explicitly replaced or
withdrawn. Updating a personal entry alone does not approve additional charity
authorization.

Failure streaks, backoff and Retry-After for new calls are independent for each
use, entry, native service and exact credential version. All uses of a donated
key still consume its existing limits. Existing key-wide recovery gates are
preserved during upgrades. No cross-entry quota pool is created, and one failed
service is not treated as proof that the whole credential is unusable.

Removing a key from one endpoint revokes only that endpoint's use. Deleting an
endpoint removes its authorization and bindings while keeping the service's
keys available to its other endpoints. Replacing, disabling or deleting an
entire key affects all its uses in that service, with the affected endpoints and
charity uses shown in its settings. Deleting a service revokes its endpoints,
keys and uses. Already
accepted work retains only the references required for safe completion and
settlement; cleanup releases unused encrypted versions. See the
[data lifecycle](data-lifecycle-checklist.md) for export and retention details.

## API families

New connection APIs encode identifiers and revisions as canonical decimal
strings; existing resources retain their documented numeric fields. Mutations retain the
existing ownership checks, revision checks and `Idempotency-Key` behavior.
Collections support bounded numbered pagination; connection collections also
retain signed cursor pagination. Keep the same request and idempotency key when
a mutation result is unknown, then refresh the current resource.

Connection responses include `display_name` and `primary_entry` in addition to
the stored `name`. The primary entry is `null` or `{id, base_url, note}` for the
first entry in the connection. Use `name` when editing and `display_name` when
presenting the connection. Previously stored idempotent responses may omit the
new fields; clients can fall back to `name` and no primary entry. Collection `q`
matches connection names/notes and any owned entry's URL/note before pagination.

| Resource | Routes and behavior |
|---|---|
| Connections | `/api/connections` and `/api/connections/{id}`; list, create, read, edit and delete. |
| Entries | Existing `/api/endpoints` and key routes add `connection_id`, `native_services` and explicit credential references. Secret values are never returned in lists. |
| Credentials | `/api/connections/{id}/credentials`, `/api/credentials/{id}` and `/api/credentials/{id}/versions`; safe metadata, enablement, rotation and version control. `/impact` lists the affected entries, donation uses and discovery sources. |
| Directories | `/api/connections/{id}/discovery-sources`, `/api/discovery-sources/{id}`, `/refresh` and `/models`; independent source settings and discovery evidence. |
| Personal bindings | Existing binding routes support `targets`. `PUT /api/models/{id}/bindings/{bId}/targets` replaces their ordered selection while retaining stable IDs for unchanged key/model pairs. |
| Donation uses | `/api/donations/{id}/uses`; owner list and explicit submission. Each use supports owner enablement, pending cancellation and permitted failure reset. Administrator/steward routes provide scoped material and per-use review. |
| Whole-submission review | Administrator/steward `POST {prefix}/donations/{id}/pending-review` checks the complete versioned pending set and commits one atomic decision. Legacy per-use routes retain their own contracts. |
| Charity bindings | Existing administrator/steward model binding routes add ordered targets backed by the selected services' approved uses. |

Batch binding and target changes offer compact receipts. Read the paginated
resource again for complete materials rather than embedding them in a replay
response. A stale revision returns a conflict; refresh before constructing a new
intent. Omitted service fields on legacy APIs preserve their documented original
scope and never imply every newly supported service.

An omitted `discovery_source_id` when creating an endpoint key creates its default
pair-scoped source. Explicit `null` opts out. Sources expose `endpoint_key_id`;
cross-service credential/source references are unavailable, and attaching a source
already scoped to another key returns a conflict. An endpoint with keys cannot
be moved to another service. Discovery evidence may include `last_success_at`
and string-valued `added_count` / `removed_count`.

Schema 40 explicitly separates legacy cross-service credentials and shared
catalogs while retaining endpoint/key IDs, bindings, budgets and approved/pending
donation material. It does not run implicitly at startup. See
[database upgrades](database-upgrades.md) for the required offline upgrade path.
