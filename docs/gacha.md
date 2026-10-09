# The Gacha Simulator

The activity entry, permanent pool, catalogue and mystery shop stay open. Limited pools have individual schedules. Open **Activities → The Gacha Simulator**; administrators configure it from **Activity management**. Site login, account restrictions and maintenance still apply.

Cards are cosmetic collectibles. They cannot be transferred, traded, sold or converted into credits. Collection rewards grant tickets and cumulative donation credit; donation credit is not a spendable balance.

## Getting started

Opening the activity for the first time grants ten standard tickets. The first permanent ten-pull guarantees at least one SSR. Single pulls leave this guarantee available. A separate manual daily check-in grants one standard ticket. Days and calendar-month limits use the configured site time zone.

One ticket buys one pull; ten-pulls cost ten tickets. By default, a ticket costs 10,000 credits:

| Ticket | Direct purchase |
| --- | --- |
| Standard | Game credits first, then general credits |
| Limited | General credits only |

Purchases are one-way. The exchange dialog shows the price and accepts 1–100 tickets per transaction. A changed price must be reviewed before submitting again.

**Perpetual Intelligence** contains all 35 permanent cards. Limited pools contain all permanent cards plus their selected limited cards. Shop-exclusive cards never appear in a pool.

## Cards and collection

Each theme has five cards: ChatGPT, Claude, GLM, DeepSeek and Gemini.

| Theme | Rarity | Acquisition |
| --- | --- | --- |
| 悠哉日常 | N | Permanent |
| 今日摸鱼 | N | Permanent |
| 雨后漫游 | R | Permanent |
| 一日店长 | R | Permanent |
| 花庭来信 | SR | Permanent |
| 云端远游 | SR | Permanent |
| 本源回响 | SSR | Permanent |
| 万象化身 | SR | Limited |
| 夏夜花火 | SSR | Limited |
| 童话剧场 | SSR | Limited |
| 口袋应援团 | SR | Shop only |

The catalogue shows all 55 cards and collection status. Owned cards open a full illustration and show the first acquisition time. SSR illustrations have a looping animation, pause control and static fallback. Sound effects, music and reduced motion have separate controls.

Collecting all five cards in a theme immediately unlocks its group illustration. Claim its reward separately, once per account: one standard ticket for a permanent theme or one limited ticket for another theme, plus ten cumulative donation credit. Published themes keep their original five cards. New cards use new themes; new permanent cards join all applicable pools without resetting progress.

## Rates and guarantees

Default base rates are N 55%, R 35%, SR 9% and SSR 1%. SR-or-better hard pity is ten pulls; SSR hard pity is sixty pulls. SSR resets both counters; SR resets the SR counter. At the SR threshold, the SSR base chance still applies and the remaining probability becomes SR. There is no soft pity.

Default long-run consolidated rates including hard pity are approximately N 51.317544%, R 32.656619%, SR 13.817568% and SSR 2.208269%. The one-time newcomer guarantee is separate. Each pool's rules dialog shows its current configuration.

In limited pools, the default featured share within each SR/SSR tier is 50%. A permanent hit makes the next hit of that same tier featured. SR and SSR guarantees are independent. A pool without featured cards of a particular tier preserves that tier's guarantee. Selection is uniform within the chosen rarity and permanent/featured group.

Limited pools with exactly the same rate and guarantee rules share counters and featured guarantees across periods. Different rules have independent progress. Names, artwork and selected limited cards do not define the group. The permanent pool has independent counters.

Changing permanent rules preserves progress. A closer pity threshold takes effect immediately; a farther threshold cannot postpone the protected current cycle. The next cycle uses the new threshold.

## Mystery shop

Each duplicate card grants one shard of its rarity. Default permanent offers are:

| Cost | Reward | Limit |
| --- | --- | --- |
| 20 N shards | 1 standard ticket | 20 standard tickets per calendar month, combined with the R offer |
| 10 R shards | 1 standard ticket | Same combined limit |
| 5 SR shards | 1 limited ticket | 10 limited tickets per calendar month, combined with the SSR offer |
| 1 SSR shard | 2 limited tickets | Same combined limit; room for both tickets is required |
| 30 N + 15 R shards | One 口袋应援团 card | Once per card; unavailable if owned |

While a limited pool is open, its limited SR cards cost ten SR shards each and SSR cards cost three SSR shards each. A card shared by multiple open pools appears once. It remains available while at least one containing pool is open. Paused, future and ended pools do not make cards available. Offers are once per card and unavailable for owned cards, including on reruns. Redemptions do not alter pity.

Price and monthly-cap changes affect future exchanges. They do not refund past purchases, reclaim rewards or reset usage.

## Administration

Administrators may publish any number of limited pools and reuse saved templates. Initial templates combine 夏夜花火 or 童话剧场 with the five 万象化身 cards. Templates do not automatically publish pools.

A pool has a name, built-in cover, schedule, featured card selection and rules. Once opened, its start time, rules and roster are fixed. Administrators may rename it, change its cover, pause/resume it or extend its end time. Different configurations use new pools.

Base probabilities use 0.01% increments and total 100%. The SR threshold is 1–100; SSR is at least the SR threshold and at most 1,000. Consolidated probabilities are calculated from the actual hard-pity algorithm. Ticket prices, shard prices and monthly limits are configurable.

## Records and HTTP access

Draw history retains the most recent 180 days. Collection, first-acquisition times, pity and claimed/redeemed states remain while the account exists. Ticket/shard accounting remains independently of draw-detail expiry. Account export schema 15 includes these records. Deletion removes personal activity state and clears user and retry-key associations from retained accounting/configuration events.

These routes use station sessions and existing same-origin checks; they are not CallerKey automation endpoints:

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/api/gacha/state` | Personal balances, collection, pity, pools and prices |
| GET | `/api/gacha/history?cursor=…` | Personal draw history, 50 receipts per page |
| POST | `/api/gacha/actions` | Enter, check in, draw, buy, redeem or claim |
| GET | `/admin/api/gacha` | Administrator configuration |
| POST | `/admin/api/gacha` | Save settings, a pool or a template |

POST requires `Idempotency-Key`. Retry uncertain operations with the same key and identical body. Confirmed retries never repeat charges or rewards. Expired draw receipts report completion without a new draw. Price-dependent actions include the displayed `revision`; administrator edits include `expected_revision`. Unknown fields and oversized bodies are rejected.

Example action bodies:

```json
{"action":"draw","pool_id":"permanent","quantity":10}
{"action":"buy","ticket":"permanent","quantity":10,"revision":1}
{"action":"redeem","product":"ticket-n","revision":1}
{"action":"claim","theme":"a"}
```

## Standalone local preview

From `web/`, run `npm run dev:gacha`, or `npm run build:gacha` followed by `npm run preview:gacha`. Production activity components use isolated browser-local sample data. Only this activity is included, with fresh-account and complete-collection modes, light/dark themes and Chinese/English text. The preview does not connect to a live account or spend real site credits.

See the [artwork and audio notices](../web/public/assets/gacha/NOTICE.md).
