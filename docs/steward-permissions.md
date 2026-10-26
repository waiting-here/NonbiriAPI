# Steward permissions

Administrators and level-6 stewards can configure each level-5 steward separately.
An unconfigured level-5 account keeps the defaults below. Granting every action
and full donation scope enables available level-6 workflows while preserving actual level 5. Permissions
do not promote an account or change administrator-only operations.

## Live authority and account hierarchy

Reads and mutations check the current policy in the same transaction as the
protected operation. Accepted background scans and discovery work also recheck
authority before completing protected work. Revocation applies to existing
browser sessions and CallerKeys; signing in again is unnecessary.

User mutations and managed CallerKey reads/revocation require a target whose
effective level is strictly below the actor's actual level. A level-5 steward
therefore acts on levels 1–4 and cannot assign level 5 or 6. Level 6 acts on levels
1–5 and cannot assign level 6. User-list and detail read visibility retain their
existing broader scope. Level 5 can never edit another steward's permissions.

Administrator-only operations remain exclusive: donation-credit changes,
Discord login policy, blacklist removal, disabling maintenance, abuse-audit
configuration changes, price-template maintenance, request-adaptation writes,
model affinity TTL, and general site administration. Stewards gain no account
export/deletion or legal-hold management route.

## Read and replace a policy

Browser sessions use either prefix:

- `GET|PUT /admin/api/users/{id}/steward-permissions`
- `GET|PUT /api/steward/users/{id}/steward-permissions`

Only an administrator or actual level-6 steward can target a current level-5
account. GET accepts no query or body and returns
`{revision,donation_scope,permissions,limits}`. PUT accepts no query, requires the usual
`Idempotency-Key`, and replaces the complete policy with
`{expected_revision,donation_scope,permissions,limits}`. It returns the updated policy.
The scope, every boolean and every limit listed below are required. Unknown or
missing keys,
null values and noncanonical or out-of-range limits are rejected. Revision is
a canonical decimal string; `"0"` means no stored policy. A stale revision or
conflicting idempotency request returns 409. Reuse the identical request and key
when the result is unknown.

`GET /api/me` includes `user.steward_permissions` with this complete shape.
Level 6 receives all actions, maximum limits and full donation scope; levels 1–4
receive false actions, zero limits and no donation scope. Stored settings survive
a level change but apply only while
the account is level 5. Permission changes do not replace browser sessions.

Missing grants and configured numeric-bound violations return HTTP 403 with
`code=steward_permission_denied`. Clients should refresh the policy and keep
the signed-in session. Authentication expiry and loss of the steward role retain
their existing distinct errors.

## Actions and defaults

There are 51 independent boolean permissions.

| Permission | Allows | Level-5 default |
|---|---|---|
| `users.read` | User list/detail, deleted account records | true |
| `users.profile` | Language and endpoint quota | false |
| `users.limits` | RPM and concurrency admission limits | true |
| `users.level` | Assign lower user levels | false |
| `users.balance` | General balance adjustment | false |
| `users.game_balance` | Game balance adjustment | false |
| `users.ban` | Ban lower-level users | true |
| `users.unban` | Unban or replace an active ban | false |
| `users.ban_permanent` | Permanent ban | true |
| `users.caller_keys.read` | Read nonsecret CallerKey status | true |
| `users.caller_keys.revoke` | Revoke CallerKey | true |
| `users.loans.read` | Read loans | true |
| `users.penalties.read` | Read penalties/evidence | true |
| `users.donations.read` | Read complete target donation history | true |
| `endpoints.read` | Read endpoint overview and related users | false |
| `endpoints.tags` | Update endpoint tags | false |
| `blacklist.read` | Read blacklist/events | false |
| `blacklist.add` | Add blacklist entry | false |
| `donations.review` | Review pending donations/uses | true |
| `donations.read_all` | Full donor/reviewer identity and physical-key projection | false |
| `donations.force_reject` | Force reject approved donation | false |
| `donations.handle` | Handling queue and processed marker | true |
| `donations.manage_keys` | Manage donated keys/failure/quotas | true |
| `donations.discover` | Discover donated key models | true |
| `donations.recurring` | Read/update recurring quotas | true |
| `charity.read` | Read charity models/bindings/candidates | true |
| `charity.manage` | Edit model role policy, bindings and manual key catalogs | true |
| `charity.create` | Create charity models including initial settings/pricing | false |
| `charity.delete` | Delete charity models | false |
| `charity.model_settings` | Edit non-price model settings; read request-adaptation configuration | false |
| `charity.pricing` | Edit pricing, discounts, recurring discounts and token reserve | false |
| `charity.non_mainstream` | Use non-mainstream charity model targets | false |
| `charity.presets` | Read/update/apply channel binding presets | false |
| `charity.backoff` | Read/update endpoint backoff policy | false |
| `charity.price_templates.read` | Read price templates | false |
| `logs.read` | Request log list/detail/export | true |
| `logs.unredacted` | Full upstream diagnostic text | false |
| `diagnostics.read` | Independent diagnostics and detailed request diagnostics | true |
| `pressure.read` | Runtime pressure | true |
| `access_events.read` | Access events and summary | false |
| `risk.read` | Abuse audit users/shared IP/config/scan results | false |
| `risk.manage` | Abuse scan creation/cancel and client rules | false |
| `announcements.read` | Announcement management reads | false |
| `announcements.manage` | Draft/preview/edit/publish/withdraw/delete announcements | false |
| `maintenance.read` | Maintenance state | false |
| `maintenance.enable` | Enable maintenance | false |
| `egress.read` | Cloudflare route configuration reads | false |
| `automation` | Steward CallerKey automation ingress | false |
| `ai.chat` | Use the operator's management-assistant conversation | true |
| `ai.review.read` | Read automatic-review history and details | false |
| `ai.memory.edit` | Read and edit the two shared assistant memories | false |

These assistant defaults apply to accounts without a saved custom policy.
Upgrading a custom policy adds all three assistant grants as false and preserves
its existing choices. Chat also requires the administrator's chat switch and a
configured driver. Assistant tools and approved plans retain every underlying
action grant, target hierarchy, donation scope and numeric limit. Complete
model-request logs, usage, archives and assistant configuration are
administrator-only; see the [assistant contract](api-contract.md#78-experimental-ai-assistant).

## Limits

All six values are canonical nonnegative decimal integer strings. Level-5
defaults and level-6 maximums are equal. A false action still prevents its use.
Zero permits no positive amount. Resetting a user quota to null is permitted
only when the resolved site default fits the configured limit. An endpoint
quota of zero means unlimited and requires the maximum endpoint allowance.
Permanent bans use their separate boolean rather than a duration allowance.

| Limit | Unit | Default and maximum |
| --- | --- | --- |
| `users.endpoint_limit_max` | Endpoints | `"10000"` |
| `users.rpm_limit_max` | Requests/minute, all RPM fields | `"4096"` |
| `users.concurrency_limit_max` | Requests, both concurrency fields | `"100000"` |
| `users.balance_adjustment_max_milli` | 0.001 general point per adjustment | `"9000000000000000"` |
| `users.game_balance_adjustment_max_milli` | 0.001 game point per adjustment | `"9000000000000000"` |
| `users.ban_duration_max_seconds` | Seconds | `"316224000"` |

## Combined actions and source scope

Permissions remain independent when an operation has several effects:

- Adding an existing account to the blacklist also requires `users.ban` and
  `users.ban_permanent`; replacing its active ban also needs `users.unban`.
- Ordinary binding options and key-to-model batch bindings require `charity.manage`.
  Applying a channel binding preset requires both `charity.presets` and
  `charity.manage`. Preset reads and edits require `charity.presets`.
- Creating and approving a donation through automation requires
  `automation`, `donations.manage_keys` and `donations.review`.
  Automated binding discovery also requires `donations.discover` and the
  binding action, within the current model/source scope.
- A model PATCH checks its supplied fields: `role_policy` requires
  `charity.manage`; pricing, discounts and `token_reserve_credits` require
  `charity.pricing`; other editable settings require
  `charity.model_settings`. Creation uses `charity.create`.

## Donation scope and endpoint access

`donation_scope` is required and accepts one of four values. The permission editor
presents these as four labeled slider positions. Scope never grants an action by
itself.

| Value | Donation resources available with the relevant action |
| --- | --- |
| `none` | No steward donation browsing, handling or mutations. |
| `standard` | At least one unended, unexpired key, and every such key has a saved mainstream-channel source, an effective `mainstream` endpoint tag, or a current binding to a mainstream charity model. This is the level-5 default. |
| `enhanced` | Standard scope plus the entire donation when any of its endpoints has any management tag. Tagged donations need no model selector for direct maintenance. |
| `full` | The level-6 donation resource scope. This is the level-6 default. |

A paused, failed or pending key still participates in the standard all-key check;
ended and expired keys do not. Qualification applies to the whole donation for
review, handling, discovery and maintenance. There is no separate pending-review
exception or add-versus-maintain rule. An optional model selector checks the
target model; it cannot qualify an otherwise ineligible donation.

A tag matches the canonical endpoint URL saved on a donation key. One matching
endpoint includes every key in that donation. Removing the last matching tag
immediately removes the enhanced-only scope; final writes, retries and background
completion recheck current tags and policy. Predicates run before list counts and
pagination. Missing action grants return `steward_permission_denied`; resources
outside the current range retain their not-found behavior.

`donations.read_all` only controls identity and physical-key fields within an
already authorized resource. It does not widen donation scope. The independent
`users.donations.read` grant permits safe retained target-user history even when
scope is `none`, without key controls or credentials. `charity.non_mainstream`
only expands eligible charity model targets; binding still needs both an eligible
model and an eligible donated source. A current mainstream-model binding can
qualify a custom-source key. Removing its last qualifying binding can remove the
whole donation from standard scope if that key is still live.

Successful approve/reject receipts remain replayable for the same actor and input
after the final pending use is resolved, while current grants and resource scope
still permit access. For an already-completed review of a terminal donation,
standard scope checks every retained key against its original mainstream source,
current qualifying binding or current effective mainstream tag; enhanced scope
also allows any current effective tag. A binding removed by termination no longer
qualifies. This receipt-only exception does not authorize ordinary historical
reads. Identity fields use the current `read_all` grant.
Completed receipts report their original outcome; they do not execute again or
grant broader resource access. Final writes and protected background completion
recheck current scope, tags and resource revisions.

Level 6 can use the shared endpoint and endpoint-policy pages. Level 5 receives
neither endpoint action by default. `endpoints.read` allows
`GET /api/steward/overview/endpoints` and
`GET /api/steward/overview/endpoints/users`;
`endpoints.tags` allows `PATCH /api/steward/overview/endpoints/tags` and
`PUT`/`DELETE /api/steward/overview/endpoints/tag-rules`.
`GET /api/steward/overview/endpoints/tag-rules` requires `endpoints.read`.
The payloads, filters and pagination match the administrator routes.
`charity.backoff` controls endpoint-backoff options, policy reads and updates;
`egress.read` independently allows reading loaded Cloudflare route configuration.
There is no route-configuration write API.

Endpoint tags support any combination of `abusive_third_party`,
`community_charity`, `noncommercial_gateway`, `user_gateway` (User-built gateway),
`steward_gateway` (Steward-built gateway), `invalid_entry` (Incorrect entry),
`mainstream` (Mainstream channels and derivatives), `mysterious` (Mysterious channel)
and `other_third_party` (Other third party).
Existing tags remain valid. Tags do not grant credential access or inference use.

Persistent `*.example.com` rules include `example.com` and every subdomain.
Rules run from broad to specific: add accumulates tags, while remove clears
earlier tags. A more specific add can add tags after a clear. Matched endpoints
cannot be tagged manually; the page identifies the matching rules. Original
manual tags are retained and participate again after the applicable clear rule
is removed. Tag and rule edits use the same existing permission, including when
they add mainstream qualification. Revocation affects current resource scope;
it does not rewrite previous approvals, bindings or donation provenance.

New submissions and explicit resubmissions whose whole resource set qualifies
as mainstream may be approved automatically, subject to existing forced-review
rules. Editing a tag never approves an existing pending submission by itself.

Without `logs.unredacted`, level-5 upstream diagnostic text retains IPv4 and
non-mainstream DNS redaction; separately authorized source IP and resource
fields retain their existing projection. This flag does not itself grant log or
diagnostic reads. Known-ID legally held reads require the corresponding current
read grant and record the actual steward identity; they do not grant hold
management or expand ordinary list/export retention.

## Storage, export and deletion

The policy tables introduced in schema 36 store one policy, including donation scope, per account and a separate
change audit. Scope shares the same revision, CAS and idempotency rules. Policies
persist until account deletion. Audits contain previous/next policies, revisions,
time and the editor reference; bounded retention cleanup removes entries at
90 days. Target-account deletion cascades to its policy and audit history;
deleting an editor clears that editor reference without deleting another
account's history. Audit labels retain the actual acting level.

Account export 30 includes the owner's `steward_permissions` with
`effective`, nullable `stored` and retained `history`, including donation scope.
Each history entry
contains `previous,next,created_at`; editor identities are excluded. Existing
export size/collection limits apply. See [database upgrades](database-upgrades.md)
and [data lifecycle](data-lifecycle-checklist.md).
