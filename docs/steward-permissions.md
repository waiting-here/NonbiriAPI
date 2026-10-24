# Steward permissions

Administrators and level-6 stewards can configure each level-5 steward separately.
An unconfigured level-5 account keeps the defaults below. Granting every action
enables available level-6 workflows while preserving actual level 5. Permissions
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
`{revision,permissions,limits}`. PUT accepts no query, requires the usual
`Idempotency-Key`, and replaces the complete policy with
`{expected_revision,permissions,limits}`. It returns the updated policy.
Every boolean and limit listed below is required. Unknown or missing keys,
null values and noncanonical or out-of-range limits are rejected. Revision is
a canonical decimal string; `"0"` means no stored policy. A stale revision or
conflicting idempotency request returns 409. Reuse the identical request and key
when the result is unknown.

`GET /api/me` includes `user.steward_permissions` with this complete shape.
Level 6 receives all actions and maximum limits; levels 1–4 receive false actions
and zero limits. Stored settings survive a level change but apply only while
the account is level 5. Permission changes do not replace browser sessions.

Missing grants and configured numeric-bound violations return HTTP 403 with
`code=steward_permission_denied`. Clients should refresh the policy and keep
the signed-in session. Authentication expiry and loss of the steward role retain
their existing distinct errors.

## Actions and defaults

There are 46 independent boolean permissions.

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
| `blacklist.read` | Read blacklist/events | false |
| `blacklist.add` | Add blacklist entry | false |
| `donations.review` | Review pending donations/uses | true |
| `donations.read_all` | Full donation list/detail and identity projection | false |
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
| `charity.non_mainstream` | Manage sources/models outside existing mainstream scope | false |
| `charity.presets` | Read/update binding presets and batch key bindings | false |
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
- Applying a binding preset or a key-to-model batch requires both
  `charity.presets` and `charity.manage`. Preset reads and edits require
  `charity.presets`.
- Creating and approving a donation through automation requires
  `automation`, `donations.manage_keys` and `donations.review`.
  Automated binding discovery also requires `donations.discover` and the
  binding action, within the current model/source scope.
- A model PATCH checks its supplied fields: `role_policy` requires
  `charity.manage`; pricing, discounts and `token_reserve_credits` require
  `charity.pricing`; other editable settings require
  `charity.model_settings`. Creation uses `charity.create`.

Without `charity.non_mainstream`, model and key management retain the default
mainstream scope and required model context. Existing non-mainstream keys already
bound to an authorized mainstream model retain that scoped management path.
Granting wider source scope does not reveal private donor identity:
`donations.read_all` controls that projection independently. Revoking a read
or write action is effective even when wider source scope remains enabled.

Without `logs.unredacted`, level-5 upstream diagnostic text retains IPv4 and
non-mainstream DNS redaction; separately authorized source IP and resource
fields retain their existing projection. This flag does not itself grant log or
diagnostic reads. Known-ID legally held reads require the corresponding current
read grant and record the actual steward identity; they do not grant hold
management or expand ordinary list/export retention.

## Storage, export and deletion

Schema 36 stores one policy per account and a separate change audit. Policies
persist until account deletion. Audits contain previous/next policies, revisions,
time and the editor reference; bounded retention cleanup removes entries at
90 days. Target-account deletion cascades to its policy and audit history;
deleting an editor clears that editor reference without deleting another
account's history. Audit labels retain the actual acting level.

Account export 30 includes the owner's `steward_permissions` with
`effective`, nullable `stored` and retained `history`. Each history entry
contains `previous,next,created_at`; editor identities are excluded. Existing
export size/collection limits apply. See [database upgrades](database-upgrades.md)
and [data lifecycle](data-lifecycle-checklist.md).
