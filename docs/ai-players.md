# AI players

Bidding Duel supports four local strategy templates: Balanced (均衡), Pot-first
(争池), Patient (蓄势) and Comeback (追分). They describe styles, not difficulty
levels or promised win rates. Each has a disabled, zero-price player ready for
configuration in **Games → AI players and strategies**.

Enable the games master switch, Bidding Duel, AI challenges and the desired
players. PvP tier switches do not control AI challenges. Waiting costs nothing;
the match charges its frozen ticket when admitted. The [game guide](duel-games.md#ai-challenges)
covers rewards, refunds and personal memory.

## Strategies and versions

A player binds one immutable strategy version. Saving a strategy creates another
version; assign it explicitly to players that should use it. Accepted queues and
active matches retain their policy and prices. Disabling a player, strategy or
AI entry rejects new entries and cancels waiting entries; active games finish
under their accepted terms. Changing a name, price or policy preserves first-clear
eligibility. **Create a new challenge** deliberately creates another reward
opportunity for every player.

The editor starts from a template and exposes these parameters:

| Parameter | Range | Meaning |
| --- | --- | --- |
| `hand_value` | 0–1.5 | Higher values preserve stronger remaining cards |
| `urgency` | 0–1 | More emphasis on catching up |
| `exploration` | 0–0.15 | Random choice among near-best candidates |
| `memory_weight` | 0–0.85 | Maximum influence of cross-match samples |
| `joker_cost` | 0–1 | Higher values preserve the Joker |
| `endgame_guard` | boolean | Prefer a certain win in the final two rounds |

Up to eight ordered rules can each have one to four conditions, with `all` or
`any` matching. Only the first matching rule applies. Fields are `phase`
(0 Joker, 1 bid), `round` (1–13), `cards` (1–13), `pool` (0–208), and `lead`
(−208–208). Operators are `lt,lte,eq,gte,gt`; phase uses equality only. A rule can
override any parameter and filter to `lower_half`, `upper_half` or `non_losing`
(cannot lose to an opponent's remaining card). An empty filter restores the
unfiltered set. Certain-win protection precedes filtering and exploration.

Preview runs the same evaluator on one of four fixed public situations, without
personal memory or any charge. It reports scores, final selection probabilities,
the matched rule and empty-filter recovery. These probabilities are not win rates.

The source uses visible hands, revealed rewards and bids. It does not receive
future reward order or an opponent's locked bid. Cross-match features count
eligible human bids by relative hand position; forced single-card choices,
timeouts and cancelled matches do not teach preferences. Recent revealed bids
also inform the current match when cross-match memory is off.

## HTTP routes

All routes use the station/session, origin, strict JSON, idempotency and error
rules in the [API contract](api-contract.md). There is no CallerKey or steward
write access to these routes. Reads reject bodies and query parameters. POSTs
require an `Idempotency-Key` except the read-only preview.

| Method and path | Body / result |
| --- | --- |
| `GET /api/games/bidding/ai` | `{enabled,bots:[{terms,terms_hash,completed,memory_enabled,memory_samples}]}` |
| `POST /api/games/bidding/ai/preference` | `{bot_id,memory_enabled}` → 200, same shape |
| `POST /api/games/bidding/queue` | `{mode:"ai",bot_id,expected_terms_hash}` → 202 `{queue_id,revision,deadline}`; no device token |
| `DELETE /api/games/bidding/queue/{aiq_id}` | `{expected_revision:"1"}` → 204; unpaid cancellation |
| `GET /admin/api/games/bidding/ai` | `{settings,policies,bots,presets,scenarios}` |
| `POST /admin/api/games/bidding/ai/settings` | `{enabled,revision}` → updated settings |
| `POST /admin/api/games/bidding/ai/policies` | Strategy body below → saved policy/version |
| `POST /admin/api/games/bidding/ai/bots` | Player body below → saved player |
| `POST /admin/api/games/bidding/ai/preview` | `{definition,scenario}` → `{candidates,matched_rule,filter_empty,memory_weight}` |

Settings revisions and expected revisions are decimal strings. Creation uses
`id:"",expected_revision:"0"`; edits use the returned ID/revision. Names have
1–64 Unicode code points, descriptions 0–512; both are trimmed and single-line.
Prices are nonnegative credit strings with up to three decimals, at most
9,000,000,000,000 credits.

A policy write is
`{id,expected_revision,name,description,enabled,definition}`.
Its definition is `{schema:"bidding-local/v1",parameters,rules}`; each rule is
`{match,conditions:[{field,operator,value}],override,filter}`.
Definitions are at most 16 KiB. There are at most 100 policies and 1,000 versions
per policy. A policy response adds `revision,version,source_id,schema_id`.

A player write is
`{id,expected_revision,name,description,enabled,policy_id,policy_version,ticket,
first_reward,memory_days,memory_games,new_challenge}`.
There are at most 100 players. Memory defaults to 30 days/30 games, with ranges
1–30/1–100. A player response replaces `expected_revision,new_challenge` with
`revision,challenge_id`. IDs use `bot_`, `aip_`, `aic_` and `aiq_` prefixes.

Offer terms include the existing duel fields plus `economy:"ai_challenge"` and
`ai:{bot_id,bot_name,description,revision,challenge_id,rules_key,policy_id,
policy_version,source_id,policy_schema,first_reward,memory_days,memory_games}`.
Settings, identity, balance and accepted terms are rechecked at admission. A
changed hash or revision returns `409 conflict`; insufficient credit rejects
admission without a payment; full queues return `resource_limit`. A failed queue
appears in state as `ai_queue_error`: `expired`, `closed`,
`account_unavailable`, `insufficient_credits`, `server_restart`.
The failure receipt lasts 120 seconds; a new accepted entry replaces it.

AI waiting is FIFO, capped at 64 entries and 120 seconds. Up to ten AI matches
run at once; one user cannot occupy both AI and PvP for the same game. The local
source has two compute slots, a 250 ms budget including waiting, and a 500 ms
submission margin before the game deadline. Source timeout/failure/invalid action
uses the game's legal automatic choice. Technical fallback does not refund or
remove otherwise valid first-clear eligibility.

State and personal history extend the existing duel API with `economy`, `ai` and
`action_sources`; each source includes round, phase, seat, canonical action,
origin and optional failure. Precise phase sequence/time are omitted from
anonymous archives. Current hidden opposing actions are not disclosed. Origins
are `human,ai,rule,timeout,fallback`; missing historical provenance is `unknown`.
The AI view contains accepted terms, the frozen memory setting/sample count,
first-clear status and reward. Free matches have no fabricated ledger operation.
Account export v13 includes preferences, eligible samples, used summaries and
first-clear records; see the [lifecycle checklist](data-lifecycle-checklist.md#ai-challenge-data).

## Extending decision sources

`internal/game/ai` defines a game-independent capability, request/window/result
protocol and bounded pool. `internal/game/bidding` adapts visible observations,
legal actions, memory and local policies. The duel runtime owns admission,
authoritative submission and settlement.

A source implements `ID`, `Supports` and `Decide(context, Request)`. It need not
return scores or text. Protocol 1 currently registers `choice/v1`, where a source
selects a candidate ID mapped to a canonical legal action. Native sources use
typed observations; remote serialization belongs to their adapter. Future action
schemas may express parameters, targets or positions without enumerating every
combination.

Windows identify match, actor and choice token separately from the display phase
or whole-match revision. Multiple actors and repeated choices within one named
phase can have independent windows. A child choice keeps the game deadline and
releases the previous source's capacity before submission. Each adapter supplies
its versioned visible observation and, where needed, public rules/definitions.
Only the authoritative game runtime can accept an action.

Use a separate bounded pool and budget for slow remote sources. Cancellation,
game deadlines and legal fallbacks still apply; an unreturned source keeps its
compute slot after fallback. Model credentials, outbound networking, parsing,
token budgets and conversation caches belong to a future source integration.
They are not game actions, wallet fields or required save data. No external
model source is currently installed.
