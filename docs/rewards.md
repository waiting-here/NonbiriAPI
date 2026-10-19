# Rewards and redemption codes

`internal/rewards` owns campaign eligibility, transactional delivery and receipts;
`internal/antiabuse` owns redemption windows and penalties. The admin and user
surfaces use session HTTP endpoints below. Campaigns, claims and control-mutation
receipts participate in the shared lifecycle; no CallerKey route is added.

## Bundles and eligibility

A bundle can combine general credits, game credits, sketch paper, paintbrushes, standard/limited gacha tickets, rarity shards, cards, and Lake Notes coins, experience, equipment, bait and debris. Direct donation-credit rewards are excluded. Game results, captures and temporary round resources are not reward items.

Credits support three decimal places; other assets use whole units and their existing capacity limits. Duplicate cards become matching-rarity shards (SP becomes SSR). Duplicate equipment with no conversion rule is skipped; other items still arrive. Bait overflow follows the existing coin conversion. Receipts list grants, conversions and skipped quantities. Capacity failures roll back the entire account's bundle.

Eligibility supports nested **all**, **AND**, **OR** and **NOT** groups. Comparisons support equality, inequality and ordered thresholds for effective account level, cumulative donation credit, registration time, completed days since registration, ban status, credit balances and item quantities. Thresholds do not spend assets. With no condition, a broadcast includes banned accounts. Account login restrictions still apply to code redemption.

## Site-wide delivery

Preview the bundle and eligible count, then confirm. Confirmation freezes the actual eligible accounts at that moment. Delivery proceeds directly, with persistent progress and an atomic bundle per account. New registrations and later eligibility changes do not expand the list. Deleted accounts are skipped. Failed accounts appear in the campaign's delivery details and can be retried; successful recipients are never credited twice.

## Codes

Enter a code or leave it blank to generate one. Codes contain 4–64 ASCII letters, digits, hyphens or underscores, ignore case and trim surrounding whitespace. Each account can successfully claim each code once. An optional quota limits the total successful recipients. An optional start is inclusive; an optional end is exclusive. Empty limits are unlimited.

A code's rewards and eligibility are fixed at creation. Administrators can rename it, enable/disable it or change quota and dates. Copy its configuration to create a different bundle. Lowering the quota below the amount already claimed blocks new claims without reclaiming rewards.

A failed claim consumes no quota. Retry an uncertain response with the same
`Idempotency-Key` and body to recover its original outcome without a second grant
or abuse count. A deliberate new submission uses a new key. Normal account
restrictions and maintenance checks still apply. Code attempts are additionally
limited to twelve per account per minute.

## Redemption abuse rules

The default rule bans an account for 24 hours after five counted failures within
a rolling 24 hours. Invalid/malformed and already-claimed codes count immediately.
For an existing code that is disabled, not yet open, expired, exhausted or fails
eligibility, the first business refusal for that account/code in the window does
not count; later new submissions count. Capacity failures do not count. Success
does not reset prior failures. The three administrator settings are documented in
[configuration](configuration.md); threshold zero disables this rule.

Windows and the initial grace fact survive restart and retain unexpired identity
continuity after account deletion. Penalties reuse existing session/key revocation,
game cancellation, history and end/retention rules. Evidence contains the rule,
count and time range, never the submitted code. Same-key retries do not count;
a different body with a retained key conflicts.

## Records and account lifecycle

Successful receipts and claim eligibility remain for the account lifetime, independently of ordinary credit-detail retention. Account export schema 22 includes personal receipts under `rewards`, without code text, eligibility configuration, other recipients or internal retry hashes. Deletion removes personal reward records; aggregate campaign counts and consumed quota remain. A newly registered account is a different account for code eligibility.

## Session HTTP API

These endpoints use the appropriate station session and same-origin protections. They are not CallerKey or steward automation APIs.

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/api/rewards/assets` | Asset names; card metadata is limited to owned cards |
| GET | `/api/rewards/history?cursor=…` | Own successful receipts, 50 per page |
| POST | `/api/rewards/redeem` | Submit `{"code":"EXAMPLE"}` with a stable retry key |
| GET | `/admin/api/rewards?cursor=…` | Campaigns and delivery counts, 50 per page |
| GET | `/admin/api/rewards/assets` | Full configurable asset catalog |
| POST | `/admin/api/rewards/preview` | Preview current eligibility |
| POST | `/admin/api/rewards/create` | Create a code or confirm a broadcast |
| POST | `/admin/api/rewards/update` | Change code metadata and limits using the current revision |
| POST | `/admin/api/rewards/retry` | Retry a broadcast's failed accounts using `{"id":"…"}` |
| GET | `/admin/api/rewards/failures?campaign_id=…&cursor=…` | Administrator-only failure details, 50 per page |

Creation and redemption require `Idempotency-Key`. Preserve the key and complete body when retrying an uncertain result. Bodies are limited to 32 KiB; unknown fields and duplicate JSON keys are rejected. Bundles accept up to 32 distinct asset entries. Condition trees accept 64 nodes, a maximum depth of eight below the root, and up to sixteen children per group; NOT takes one child. Amounts are decimal strings.

Example creation body:

```json
{
  "kind": "code",
  "title": "Community gift",
  "code": "COMMUNITY-GIFT",
  "enabled": true,
  "quota": 100,
  "rewards": [
    {"asset": "general", "quantity": "100.125"},
    {"asset": "gacha:limited", "quantity": "1"}
  ],
  "conditions": {
    "op": "and",
    "children": [
      {"op": "compare", "field": "level", "compare": "gte", "value": "2"},
      {"op": "not", "children": [
        {"op": "compare", "field": "banned", "compare": "eq", "value": "true"}
      ]}
    ]
  }
}
```

Fields are `level,donation,registered_at,registered_days,banned,asset`; asset comparisons also supply the catalog `asset` identifier. Numeric operators are `eq,ne,gt,gte,lt,lte`; booleans support only `eq,ne`. Registration timestamps and schedule boundaries are Unix seconds. No-condition eligibility is `{"op":"all"}`.

Successful redemption responses include the receipt and `already_claimed`: `false` for a new grant, `true` when returning an earlier claim. The user interface identifies an earlier claim and shows its original time and rewards. This response flag is not stored in receipt history or account exports. A new already-claimed submission also counts toward the abuse window; use history reads to review previous rewards without submitting the code.

Business refusals use HTTP 200 with `{"rejection":"unavailable"}`, `conditions` or `capacity`; unavailable combines unknown, disabled, out-of-window and exhausted codes. A submission that triggers a ban returns `{"rejection":"banned","banned_until":1800086400}` with its actual Unix deadline; the revoked session cannot make subsequent authenticated requests. Other errors use the standard error envelope.
