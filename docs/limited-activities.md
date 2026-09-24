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

Pausing an activity or entering site maintenance cancels its undispatched tasks and refunds their charges. Tasks already dispatched continue settlement. Natural closing time stops new exchanges and new tasks, while previously accepted tasks continue. Execution and queue endpoints belong to the activity's execution module.

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
