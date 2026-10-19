# The Gacha Simulator

## Integration and content maintenance

`internal/gacha` owns the catalog, rules, balances, transactions and HTTP API;
`web/src/user/activities/gacha` implements the activity presentation. Card
and theme IDs are stable. Catalog and `presets.json` content versions are separate
from the database schema. Adding cards, pool templates or a new period of an
existing activity type requires no DDL; new persistent semantics still require a
registered migration.

`SyncCatalog` inserts missing built-in templates only. It preserves administrator
edits, does not open pools and does not reset collection, eligibility, pity or
claim records. Published theme membership and accepted pool rules remain fixed.
The rules and configuration contracts below are authoritative for both API and UI.

## Entry and admission

Activity entrances are hidden by default. Administrators can show them from the activity settings. This display switch does not stop scheduled pools or disable the activity URL. The permanent pool, catalogue and mystery shop stay open. Limited pools have individual schedules. When entrances are enabled, the main banner appears below Cyber Loan under permanent activities. Each open limited pool also appears under limited activities, with its cover, featured cards and opening dates, and links directly to that pool. Draft, paused, future and ended pools have no entrance. Open **Activities → The Gacha Simulator**; administrators configure it from **Activity management**. Site login, account restrictions and maintenance still apply.

Cards are cosmetic collectibles. They cannot be transferred, traded, sold or converted into credits. One-time theme completion rewards grant tickets, credits and cumulative donation credit. Donation credit itself is not a spendable balance.

## Getting started

Opening the activity for the first time grants ten standard tickets. The first permanent ten-pull guarantees at least one SSR. Single pulls leave this guarantee available. Two independent daily check-in buttons grant one standard ticket and one limited ticket. Both can be claimed on the same day; missed days cannot be claimed later. An existing standard-ticket check-in does not use the limited-ticket entitlement. Days and calendar-month limits use the configured site time zone.

One ticket buys one pull; ten-pulls cost ten tickets. By default, a ticket costs 10,000 credits:

| Ticket | Direct purchase |
| --- | --- |
| Standard | Game credits first, then general credits |
| Limited | General credits only |

Purchases are one-way. The exchange dialog shows the price and accepts 1–100 tickets per transaction. A changed price must be reviewed before submitting again.

**Perpetual Intelligence** contains all 35 permanent cards. Limited pools contain all permanent cards plus their selected limited cards. Shop-exclusive cards never appear in a pool.

## Cards and collection

The first eleven themes each have five cards: ChatGPT, Claude, GLM, DeepSeek and Gemini. Each SP theme contains one character card.

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
| 星月神话 · ChatGPT | SP | Limited |
| 逍遥游 · DeepSeek | SP | Limited |
| 镜海相逢 · Gemini | SP | Limited |
| 琥珀誓章 · Claude | SP | Limited |
| 与你私奔 · Claude | SP | Limited |
| 偷心预告 · GLM | SP | Limited |

The catalogue shows all 61 cards and collection status. Owned cards open a full illustration and show the first acquisition time. SSR and SP illustrations have a looping animation, pause control and static fallback. SP also animates its background in coordination with the character. N and R draws reveal automatically in order. SR, SSR and SP encounters wait for a click or the Continue button while their artwork keeps moving; SSR and SP use full-screen scenes. First-acquisition SSR/SP encounters cannot be skipped or closed until all their lines have appeared. Skip reaches the complete results once those encounters finish, without another request. Reduced motion retains the full static first encounter and its lines. Ten-pulls share one card board: five by two on desktop and four, four, two on phones. SR focuses within that board; SSR/SP charge before a full-screen close-up. New SP cards have a short prelude and a delayed second voice line; reunions use their own line. Results keep all ten cards visible and animate duplicate shard conversion. When enough tickets remain and the original pool is open, Draw again repeats the same quantity through the normal draw flow. Sound effects, music, supported-device vibration, card-back rarity hints and reduced motion have separate controls. Turning hints off keeps unrevealed backs identical. Pausing or hiding the page preserves remaining scene time. Owned-card appreciation includes bilingual character lines. Owning an SSR/SP card also unlocks both first-encounter and reunion replay, including cards acquired from the shop or a reward. Replays may be closed immediately and do not spend tickets or change collection, pity or shards.

Completing a theme immediately unlocks its horizontal illustration: all five cards for an ordinary theme, or its single card for an SP theme. Claim its reward separately, once per account: one standard ticket and 10,000 game credits for a permanent theme, or one limited ticket and 10,000 general credits for any other theme, including shop-only and SP themes. Every theme also grants ten cumulative donation credit. All reward components are granted together; retries cannot grant them twice. Published themes keep their original membership. New cards use new themes; new permanent cards join all applicable pools without resetting progress.

## Rates and guarantees

Default base rates are N 55%, R 35%, SR 9% and SSR 1%. SR-or-better hard pity is ten pulls; SSR hard pity is sixty pulls. SSR resets both counters; SR resets the SR counter. At the SR threshold, the SSR base chance still applies and the remaining probability becomes SR. There is no soft pity.

Default long-run consolidated rates including hard pity are approximately N 51.317544%, R 32.656619%, SR 13.817568% and SSR 2.208269%. The one-time newcomer guarantee is separate. Each pool's rules dialog shows its current configuration.

In limited pools, the default featured share within each SR/SSR tier is 50%. A permanent hit makes the next hit of that same tier featured. SR and SSR guarantees are independent. A pool without featured cards of a particular tier preserves that tier's guarantee. Selection is uniform within the chosen rarity and permanent/featured group.

The six SP templates keep the 1% SSR-tier base rate and sixty-pull hard pity. An SSR-tier result has a 15% chance to be the featured SP. After two consecutive permanent SSR results, the next SSR-tier result is guaranteed to be the featured SP; an earlier SP resets the miss count. SR and lower results do not reset it. These templates share a separate rule group from the ordinary 50% featured SSR pools. The four newer SP templates feature only their corresponding SP card; their SR results come from the permanent pool and preserve existing featured-SR progress. Templates do not create or publish live pools and catalog synchronization preserves saved administrator edits. From empty progress, the SP guarantee requires at most 180 pulls. SP is a display rarity, not a fifth initial probability tier.

Limited pools with exactly the same rate and guarantee rules, including the miss threshold, share counters and featured guarantees across periods. Different rules have independent progress. Names, artwork and selected limited cards do not define the group. The permanent pool has independent counters.

Changing permanent rules preserves progress. A closer pity threshold takes effect immediately; a farther threshold cannot postpone the protected current cycle. The next cycle uses the new threshold.

## Mystery shop

Each duplicate card grants one shard of its rarity. Duplicate SP cards grant one SSR shard; there is no separate SP shard. Default permanent offers are:

| Cost | Reward | Limit |
| --- | --- | --- |
| 20 N shards | 1 standard ticket | 20 standard tickets per calendar month, combined with the R offer |
| 10 R shards | 1 standard ticket | Same combined limit |
| 5 SR shards | 1 limited ticket | 10 limited tickets per calendar month, combined with the SSR offer |
| 1 SSR shard | 2 limited tickets | Same combined limit; room for both tickets is required |
| 30 N + 15 R shards | One 口袋应援团 card | Once per card; unavailable if owned |

While a limited pool is open, its limited SR cards cost ten SR shards each and SSR cards cost three SSR shards each. SP cards cost nine SSR shards each. A card shared by multiple open pools appears once. It remains available while at least one containing pool is open. Paused, future and ended pools do not make cards available. Offers are once per card and unavailable for owned cards, including on reruns. Redemptions do not alter pity.

The **量大管饱 (Bulk bargains)** group offers ten standard tickets or ten limited tickets for 10,000 credits per bundle. Each account may buy each bundle once per calendar day, with independent limits. Both bundles use game credits first and cover any remainder with general credits. Daily availability resets at midnight in the configured site time zone. The charge, ten-ticket grant and daily limit are committed together. Failed purchases consume neither credits nor availability. These fixed offers do not use the configurable single-ticket prices or monthly shard-exchange limits.

Price and monthly-cap changes affect future exchanges. They do not refund past purchases, reclaim rewards or reset usage.

## Administration

Administrators may publish any number of limited pools and reuse saved templates. Initial templates combine 夏夜花火, 童话剧场, 星月神话 or 逍遥游 with the five 万象化身 cards. Templates do not automatically publish pools.

A pool has a name, built-in cover, schedule, featured card selection and rules. Once opened, its start time, rules and roster are fixed. Administrators may rename it, change its cover, pause/resume it or extend its end time. Different configurations use new pools.

Base probabilities use 0.01% increments and total 100%. The SR threshold is 1–100; SSR is at least the SR threshold and at most 1,000. Consolidated probabilities are calculated from the actual hard-pity algorithm. The SSR featured guarantee supports one or two consecutive misses. Ticket prices, shard prices, separate SSR/SP card prices and monthly limits are configurable.

## Site appearance

Obtaining an SP card by drawing, shop redemption or an administrator reward immediately unlocks its matching appearance. It remains available after the pool ends. The site does not select it automatically: choose **Appearance** in the user site and apply an unlocked design. The existing light, dark or system preference chooses its matching palette and background. Each design preserves page layout and provides responsive background cropping; game scenes retain their own style.

With a non-default appearance selected, **View background** in the top bar hides the interface for appreciation. **Return to page** or Escape restores the page and its current input state. Reduced-motion preferences still apply.

An appearance stays out of the selector until its card has been offered in an opened pool or the account owns it. Scheduling a future pool or pausing it before opening does not reveal the design. Previously offered appearances can remain visible as locked choices.

The selected appearance is saved per account. Unlocks derive from owned cards, independently of draw-history expiry. Reading or selecting an appearance grants no tickets or activity rewards.

## Heartfelt moments

Every account can choose **Heartfelt moments** in Appearance. Its default is the standard pool cover. Open **Collection → Heartfelt moments** to select an owned card, an unlocked theme illustration, any standard cover, or a limited cover whose pool has actually opened. A limited cover remains available after its pool closes; drafts, future schedules and pools paused before opening do not unlock it. Completing a theme unlocks its illustration without claiming the theme reward.

Cards use their theme’s static background and an independent foreground. SSR and SP retain character animation; other cards, illustrations and covers are static. SP selections also point to their dedicated full site appearance. One responsive layout serves desktop and mobile, with reduced-motion fallback. The account remembers the chosen artwork when another appearance is selected.

## Records and HTTP access

Draw history retains the most recent 180 days. Collection, first-acquisition times, pity and claimed/redeemed states remain while the account exists. Ticket/shard accounting remains independently of draw-detail expiry. Account export schema 20 includes these records and the appearance selection, saved artwork key, unlocked designs and selection timestamp. Deletion removes personal activity state and clears user and retry-key associations from retained accounting/configuration events.

These routes use station sessions and existing same-origin checks; they are not CallerKey automation endpoints:

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/api/gacha/entry` | Entry visibility, currently open limited pool entrances and whether this account has visited; no welcome reward is granted |
| GET | `/api/gacha/state` | Personal balances, collection, visible themes, pity, pools and prices |
| GET | `/api/gacha/history?cursor=…` | Personal draw history, 50 receipts per page |
| POST | `/api/gacha/actions` | Enter, check in, draw, buy, redeem or claim |
| GET | `/api/appearance` | Selected appearance, remembered artwork and available artwork keys |
| POST | `/api/appearance` | Replace selection and optionally the Heartfelt moments artwork |
| GET | `/admin/api/gacha` | Administrator configuration |
| POST | `/admin/api/gacha` | Save settings, a pool or a template |

Themes consisting entirely of limited cards stay hidden until a pool containing their cards has opened. Past openings continue to count after a pool ends or is paused; already owned cards remain visible. Scheduled pools paused before opening do not reveal their themes.

Administrator settings accept an optional `entry_enabled` boolean, alongside rules, shop and the expected revision. Omitting it preserves the current switch; new installations default to false.

Gacha POST routes require `Idempotency-Key`. Retry uncertain operations with the same key and identical body. Confirmed retries never repeat charges or rewards. Expired draw receipts report completion without a new draw. Price-dependent actions include the displayed `revision`; administrator edits include `expected_revision`. Unknown fields and oversized bodies are rejected.

Appearance POST accepts `selected` (`default`, `heart-moment`, `celestial` or `wandering`) and an optional `moment` artwork key, with a 1 KiB body limit. It is an idempotent desired-state update and does not require an idempotency key. The visible catalogue is derived at read time and is excluded from account exports. Both appearance routes return `selected,moment,moments,unlocked,visible,updated_at`, reject query parameters and use `Cache-Control: no-store`. Locked designs are forbidden; malformed or unknown selections are invalid requests.

The state `redeemed` array contains redeemed product IDs: card IDs remain for the lifetime of the account; `bulk-permanent` and `bulk-limited` appear only when bought on the current site-calendar day. Another purchase of that bundle on the same day returns `{"rejection":"daily-limit"}`. Replaying a successful request after midnight returns the current state without purchasing again or consuming the new day's allowance. Exports include each bundle's latest claim timestamp and retained purchase operations; deletion follows the existing activity lifecycle.

Example action bodies:

| Action | Body |
| --- | --- |
| Draw | `{"action":"draw","pool_id":"permanent","quantity":10}` |
| Buy tickets | `{"action":"buy","ticket":"permanent","quantity":10,"revision":1}` |
| Redeem | `{"action":"redeem","product":"ticket-n","revision":1}` |
| Buy a daily bundle | `{"action":"redeem","product":"bulk-permanent","revision":1}` or `{"action":"redeem","product":"bulk-limited","revision":1}` |
| Claim a theme reward | `{"action":"claim","theme":"a"}` |

`GET /api/appearance` returns `selected`, `moment`, `moments`, `unlocked`, `visible` and `updated_at`. Artwork keys are `card:<card-id>`, `illustration:<theme-id>` or `cover:<cover-id>`. `POST /api/appearance` accepts `{ "selected": "heart-moment", "moment": "card:g-chatgpt" }`; omit `moment` to keep the remembered artwork. A supplied artwork requires `selected=heart-moment`. The server validates account access and collection/opening eligibility in the same transaction. Repeating a selection leaves its timestamp unchanged. Reads do not enter the activity or grant tickets.

## Standalone local preview

From `web/`, run `npm run dev:gacha`, or `npm run build:gacha` followed by `npm run preview:gacha`. Production activity components use isolated browser-local sample data. Only this activity is included, with fresh-account and complete-collection modes, light/dark themes, unlockable appearances, mixed SSR/SP and SR ten-pull samples, single-card and SP-reunion samples, and Chinese/English text. The preview does not connect to a live account or spend real site credits.

See the [artwork and audio notices](../web/public/assets/gacha/NOTICE.md).

New cards follow the [card artwork and presentation standard](gacha-art.md), including their rarity-specific animation, theme illustration and SP appearance requirements.
