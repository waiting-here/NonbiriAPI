# Service connections

A connection groups upstream entries, saved credentials and model directories.
Each entry names a complete base URL, protocol and explicitly selected native
services. Grouping entries does not authorize a credential at another destination.
The same credential can be attached to several entries without re-entering its
secret. Users must select each destination explicitly.

Connection lists show the primary entry's address and note. Search includes the
name and note of the connection and the addresses and notes of all its entries.
Untouched names generated during an upgrade use the original entry's note or
hostname for display. Custom names and stored metadata remain unchanged.

## Configure entries and models

An OpenAI-compatible entry can select Chat Completions, Responses, embeddings,
images, audio and other supported native services independently. Rerank also
requires its actual format: generic, Cohere, Jina, Voyage or SiliconFlow. Sharing
a `/rerank` path does not make these payload formats interchangeable. Gateway
v3 and v4 each offer text, image generation, embeddings and rerank. Select the
upstream's actual version; there is no automatic probing or version fallback.

System One is a separate **Typesafe System One** protocol in **Add entry**.
Its service is `/v1/systemone`; the **Decisions** choice under OpenAI-compatible
entries represents `/v1/decisions`. These services are not converted into each other.

For an upstream serving both Chat and Responses at one base URL, select both
services on one entry. A model may use a smaller subset. For a provider whose
models need different protocols, create the required entries in one connection,
attach the same credential explicitly and select the applicable entries on each
model. Model identifiers may differ between entries and must be supplied for
each target.

A logical binding contains ordered targets. For each request, the platform
prefers a native service that can represent the request, then considers the
existing bounded conversions. It selects one target in that binding before
applying the model's routing strategy. More targets do not add retry slots or
increase a binding's weight. Existing protocol, body and transport checks still
apply; a service declaration does not promise arbitrary lossless conversion.
See [direct inference](direct-inference.md) for the supported operation matrix.

A model directory is independent of inference entries. It has its own complete
base URL, list format and selected credential version. This allows several
protocol entries to share one model-list endpoint. A directory refresh does not
send a paid inference request. Recognized Gateway model types and Gemini methods
provide service suggestions; an ordinary OpenAI model ID alone does not prove
service support. Unknown entries require an explicit selection when binding.
Saved models and bindings survive a failed refresh or credential rotation.

Mainstream presets may suggest entries, a directory and per-model mappings.
Existing entry IDs are retained when editing; save new entries before assigning
model mappings to them. Updating a preset does not change existing destinations,
expand grants or approve donations.

When authorizing a credential for a newly created mainstream entry, its suggested
directory is selected by default. Saving reuses a compatible directory or creates
one for that credential and associates it with the entry's key. Clear the option
to authorize without it, or select an existing compatible directory. Custom
entries and previously saved authorizations keep their existing behavior.

If a key has no directory, its model controls offer a catalog selection action.
Move an ungrouped entry into a connection first, then select a directory for the
same credential version. The directory management link can add one if needed.
Manual model entries remain available without a directory. Importing or connecting
a model reuses an existing manual entry with the same upstream model ID under
the selected key; its existing metadata is preserved.

While creating a custom entry, entering an address can show every matching
available mainstream entry, including its protocol, native services, discovery
suggestion and model mappings. Matching uses the complete normalized URL,
including scheme, effective port and path, without contacting the provider.
Choose **Use this preset** to select that entry alone. Compatible service choices
are retained; if none match, choose the services to use. Switching back to
**Custom** preserves the address, protocol, note and service choices. Saving
remains a separate action. Empty or unavailable suggestions do not block custom
creation, and adopting a preset does not attach credentials or approve donations.

## Credentials and donations

Changing a secret creates a new immutable version. Personal grants and current
model directories move to the new version. Approved charity uses retain their
previous version while a replacement awaits review. The owner can pause an old
use or version immediately. A replacement is never approved merely because the
old secret or the provider was previously approved.

A donation shares explicitly selected services and, optionally, an exact model
directory revision using the same credential version. Reviewers approve or
reject each use. A use may have both an approved version and a pending replacement;
partial rejection does not withdraw the still-approved version. The parent
status alone therefore does not describe every service's availability.

Donors submit the keys and services they want to share. Administrators or authorized
stewards then name and bind the charity models. If model discovery is unavailable,
the donor can include model IDs in the public donation description, without secrets.
Personal model entries do not automatically become charity models.

New mainstream donation drafts select all of the key's available services and a
compatible directory by default. The donor can deselect individual services or
the directory before submitting. Removing and reselecting a key in the same draft
preserves those choices. Existing donations are not expanded automatically.

Approving an inference use does not share its model directory. Managed model
refreshes require a directory explicitly shared by the donor; a personal
directory is never used as a substitute. A missing or paused sharing grant
returns `423 discovery_not_shared`. Other availability gates, such as expiry,
quota or cooldown, retain `423 resource_locked`; neither response alone means
that the upstream credential is invalid.

Administrators and level-6 stewards can force-reject a donation that has approved
uses, including when its parent retains the pending status. The action requires
a reason and complete review material, stops new charity dispatches and preserves
normal settlement for requests already sent. Legacy whole-donation approval or
rejection cannot override per-service approvals; review pending uses individually
or use the authorized force-rejection action for the whole donation.

Owners can add another service to the same donated key or explicitly resubmit
a rejected or withdrawn use. These actions create pending review material and
preserve the original key, budget, consumed amounts and reservations. They do
not replace a currently pending or approved use. Expanding a personal entry's
service selection does not automatically donate the new service.

Failure streaks, backoff and Retry-After for new calls are independent for each
use, entry, native service and exact credential version. All uses of a donated
key still consume its existing limits. Existing key-wide recovery gates are
preserved during upgrades. No cross-entry quota pool is created, and one failed
service is not treated as proof that the whole credential is unusable.

Deleting an entry removes that entry's authorization and bindings while keeping
independent credentials. Deleting a credential revokes its grants. Already
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
| Charity bindings | Existing administrator/steward model binding routes add ordered targets backed by the selected services' approved uses. |

Batch binding and target changes offer compact receipts. Read the paginated
resource again for complete materials rather than embedding them in a replay
response. A stale revision returns a conflict; refresh before constructing a new
intent. Omitted service fields on legacy APIs preserve their documented original
scope and never imply every newly supported service.
