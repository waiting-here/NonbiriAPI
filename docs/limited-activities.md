# Limited-time activities

Limited-time activities use separate pages under `/activities/{activity_key}`. Permanent activities keep their existing APIs and rules.

The first activity key is `picture-book`. It starts hidden and has no opening or closing time. Both timestamps must be configured, with the opening strictly before the closing, before new exchanges or tasks can be accepted. The interval includes the opening time and excludes the closing time. Times are Unix seconds; the administration form explicitly displays UTC.

Hiding an activity removes its directory entry. An authenticated user who knows its link can still view it. Participation additionally requires an active user account, an open schedule, complete execution configuration, and no activity pause or site maintenance. Administrator-station accounts cannot participate. Reopening preserves existing balances, records and cumulative exchange totals.

## Directory and configuration

| Method | Path | Authority |
| --- | --- | --- |
| GET | `/api/limited-activities` | User session |
| GET | `/api/limited-activities/{key}` | User session |
| GET | `/admin/api/limited-activities/{key}` | Administrator session |
| PUT | `/admin/api/limited-activities/{key}` | Administrator session, CSRF and Idempotency-Key |

The directory is an array containing only visible activities. A detail response has:

```json
{
  "key": "picture-book",
  "name": "喵帕斯的绘本",
  "visible": false,
  "starts_at": null,
  "ends_at": null,
  "paused": false,
  "revision": "1",
  "status": "unconfigured",
  "module_config": {
    "paper_price": "1000",
    "brush_price": "10000",
    "brush_cap": "10",
    "brush_exchanged": "0",
    "brush_remaining": "10"
  }
}
```

Status is one of `unconfigured`, `unavailable`, `scheduled`, `open`, `paused`, or `ended`. An open time range alone does not make an unconfigured execution engine available.

The PUT body includes every common field, an expected revision and only editable module settings:

```json
{
  "expected_revision": "1",
  "visible": false,
  "starts_at": null,
  "ends_at": null,
  "paused": false,
  "module_config": {
    "paper_price": "1000",
    "brush_price": "10000",
    "brush_cap": "10"
  }
}
```

Prices are positive general-credit amounts with up to three fractional digits. The maximum unit price is 9000000000000 credits. The cap is a nonnegative decimal integer string. Stock counters in GET responses are derived and cannot be submitted as editable settings. A stale revision returns `conflict`. Successful changes create an immutable configuration revision; old exchange receipts retain their original price and revision.

For picture-book, pausing the activity or entering site maintenance cancels its undispatched tasks and refunds their charges. Tasks already dispatched continue settlement. Natural closing time stops new exchanges and new tasks, while previously accepted tasks continue. Execution and queue endpoints belong to the activity's execution module.

## Wallet and exchange

| Method | Path | Authority |
| --- | --- | --- |
| GET | `/api/limited-activities/picture-book/wallet` | User session |
| POST | `/api/limited-activities/picture-book/exchange` | User session, CSRF and Idempotency-Key |

Wallet responses contain `general`, `sketch_paper`, and `sketch_brush` as exact decimal strings. Activity currency balances are whole units; general credits allow three fractional digits. Reading a wallet does not create activity accounts.

An exchange body is:

```json
{"asset":"sketch_paper","quantity":"2"}
```

Only `sketch_paper` and `sketch_brush` are accepted. Quantity is a positive whole number encoded as a string. The default cost is 1000 general credits per sheet or 10000 per brush. Exchanges cannot be reversed and the two activity currencies cannot be exchanged for one another.

The general-credit deduction, activity-currency issuance, cumulative counter and receipt commit in one transaction. A failed exchange changes none of them. Values must fit the ledger's signed 128-bit magnitude after conversion to milliunits; multiplication is exact and overflow is rejected.

The brush cap means the **total ever exchanged**, initially 10. Reducing it below the total already exchanged stops subsequent exchanges without taking existing balances away. Task refunds restore user balances but do not replenish this global capacity. Reopening, pausing and account deletion do not reset the cumulative total. Paper has no configured global cap.

A successful exchange returns `receipt`, `wallet` and `supply`. The receipt contains the local `operation_id`, activity key, configuration revision, asset, quantity, unit price, total cost, ledger sequence and creation time. It does not contain other users or upstream execution information.

## Replay and data handling

Every mutation requires a 22–128-character URL-safe ASCII `Idempotency-Key`. Retry an uncertain response with the same key and the same operation. A matching completed request returns its original receipt; a different payload with that key returns `conflict`. Replay uses the existing 24-hour control-mutation window and rechecks the current session and account authority. A banned account or revoked session cannot retrieve a receipt through replay.

Responses use `Cache-Control: no-store`. Requests reject unknown or duplicate JSON fields, unsupported query parameters and oversized bodies. Balances and monetary counters must never be parsed through floating-point arithmetic.

Account export includes the owner's exchange receipts and exact balances. Exchange receipts follow long-term ledger retention. Account deletion removes their owner association while retaining anonymous accounting facts and cumulative global totals. Configuration history similarly removes an administrator's user reference when that account is deleted.

## Lake Notes

`lake-notes` is independent of Pond Fishing. It starts hidden, with no open schedule and all four exchange directions disabled. An administrator must configure the activity envelope and publish a finite period. Hiding removes the directory entry; it is not the participation gate. The activity and period must both be open, with no pause or maintenance.

The server saves coins, experience, equipment, bait, catches, collections, contracts and one current cast per account. The full profile continues across devices and later periods; old browser-local coins or saves are not imported. The original gameplay includes equipment/loadouts, skills, bait, locations, fishing, treasure/debris, sales and contracts. Closed periods retain progress and coins. Read-only profile/rule access remains available when play is closed.

Entry charges **general credits once per period**, with the price shown before confirmation. The same period does not charge again after a device change, fee edit, pause or reopening. A new period has its own entitlement. Coins are separate from all site wallets; entry and exchanges update their receipts and ledgers atomically.

### Exact exchanges

Each period independently configures `coins_to_general`, `general_to_coins`, `coins_to_game`, and `game_to_coins`. All start disabled. Each enabled direction has exact positive integer `source_amount` and `target_amount`. Coin-side amounts are whole coins; credit-side amounts are **millicredits** (1 credit = 1,000 millicredits). `source_amount` and `target_amount` define one configured whole lot. `quantity` is a positive integer **number of lots**. The quote multiplies both lot amounts exactly; it does not reduce or silently round them. No rounding, remainder loss or partial debit occurs.

Quote first, then confirm with its period/profile revisions. Price, balance or state changes require a fresh quote; the server never substitutes a changed price. Pause, maintenance, closing or an absent open period stops every direction while preserving coins and site balances. Coins and credits have no cash redemption or user-to-user transfer.

| User method and suffix | Body / result |
| --- | --- |
| `GET /profile` | `{readonly,revision,rules_id,profile,wallet,period,entitlement,cast}`. Wallet fields are `general_milli,game_milli`. |
| `GET /rules` | `{rules_id,catalog}`; bounded public gameplay catalog, no private reward plan. |
| `POST /entry` | `{period_id,expected_period_revision}` → `{receipt,profile}`. |
| `POST /exchange/quote` | `{direction,quantity,period_id}` → exact source/target amounts, configured lots, wallet and period/profile revisions. No idempotency key or charge. |
| `POST /exchange` | Quote input plus `expected_period_revision,expected_profile_revision` → `{receipt,profile}`. |
| `POST /actions` | Typed action plus `expected_profile_revision` → `{profile,coin_delta}`. |
| `POST /casts` | `{expected_profile_revision}` → `{cast,profile}`. |
| `GET /casts/{id}` | Owner's public cast projection. |
| `POST /casts/{id}/checkpoint` | `{generation,expected_revision,from_tick,to_tick,initial_held,edges:[{tick,held}]}` → `{cast,profile}`. |
| `POST /casts/{id}/pause`, `POST /casts/{id}/resume` | `{generation,expected_revision}` → `{cast,profile}`. |

Suffixes use `/api/limited-activities/lake-notes`. User session and normal same-origin authorization are required; CallerKeys and administrator accounts cannot play. GETs accept no body/query. All POSTs except quote require `Idempotency-Key`; strict mutation bodies are limited to 16 KiB. Monetary values and revision/generation fields are decimal strings. Tick numbers and held booleans use their shown JSON types.

Actions are `buy_gear(id)`, `equip_gear(id,slot)`, `save_gear_loadout(index)`, `load_gear_loadout(index)`, `buy_bait(id)`, `select_bait(id)`, `sell_fish(fish_ids)`, `sell_all_fish`, `set_fish_lock(fish_ids,locked)`, `sell_debris(id)`, `sell_all_debris`, `switch_location(id)`, `rest`, `choose_skill(id)`, `respec`, `accept_contract(id)`, `cancel_contract(id)`, and `claim_contract(id)`. The body uses an `action` string and only that action's arguments. Server rules determine costs, eligibility and rewards; clients cannot upload arbitrary profile balances or rewards.

One controller advances a cast through at most 120 fixed-step ticks per checkpoint. The server replays the public motion and checks cumulative active time across pause/resume, devices, new controller generations and restart. A new generation does not reroll a fish or private reward plan, restart the time budget or duplicate a catch. Network recovery reconciles acknowledged state before continuing; input not yet confirmed is not a saved financial result. A six-second controller lease expires into a saved pause. Ending a period or activity pauses at its actual closing time; a later opening resumes saved play with a current entitlement.

### Administrator periods

Only the administrator station exposes `GET|POST /admin/api/limited-activities/lake-notes/periods` and `PUT .../periods/{id}`. Lists accept `page,page_size` (default 1/20, sizes 10/20/50/100) and return `{items,page,page_size,has_more}`. Writes require CSRF and `Idempotency-Key` and use:

```json
{
  "expected_revision": "0",
  "name": "Example season",
  "status": "draft",
  "starts_at": 1893456000,
  "ends_at": 1893542400,
  "entry_fee_milli": null,
  "exchanges": {
    "coins_to_general": {"enabled": false, "source_amount": "1", "target_amount": "1000"},
    "general_to_coins": {"enabled": false, "source_amount": "1000", "target_amount": "1"},
    "coins_to_game": {"enabled": false, "source_amount": "1", "target_amount": "1000"},
    "game_to_coins": {"enabled": false, "source_amount": "1000", "target_amount": "1"}
  }
}
```

Creation uses revision `"0"`; updates use the current period revision. Status is `draft|published|closed`. Published periods cannot overlap. A draft may leave `entry_fee_milli` null; publishing requires an explicit nonnegative millicredit string, and `"0"` means free. The common activity PUT keeps its usual envelope and accepts only `module_config:{}` for lake settings; fees and exchanges belong to periods. Unix-second schedules use `[starts_at,ends_at)`. Publishing a period does not enable an otherwise closed activity or an exchange direction.

### Recovery and privacy

Ordinary control replay lasts 24 hours and rechecks authorization. Conflict means reload/reconcile the saved state; a lost response means retry the original operation, not generate another fee, exchange or cast. The profile is bounded to 64 KiB, the public catalog to 256 KiB, and current cast controls/checkpoints have bounded capacity. Private encounter/reward randomness never enters public state, logs or export.

Completed cast summaries last 30 days; minimal identities needed by unexpired replay receipts can survive until that receipt expires. A current or paused cast and profile follow the account lifetime. Export includes the safe profile, casts, entry and exchange receipts. Deletion removes personal profile/cast/entry links and unrevealed material; settled anonymous credit-ledger facts remain under the existing financial policy. Late callbacks cannot restore the account or coins.
