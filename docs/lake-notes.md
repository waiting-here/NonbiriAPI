# Lake Notes

Lake Notes is a permanent, free fishing game with four locations, 96 fish, equipment, skills, bait, collections and contracts. The server stores progress across devices. Old activity saves, balances and payment receipts remain available; no new activity entry fee is charged. Browser-local prototype saves are not imported.

Administrators configure `lakenotes` through `PATCH /admin/api/games/config`, using the shared `expected_revision`. Its fields are `enabled` and `exchanges`. Fresh installations start closed. An upgrade preserves availability for a visible, unpaused legacy activity. All four exchange directions start disabled after migration.

## Exact exchanges

`coins_to_general`, `general_to_coins`, `coins_to_game` and `game_to_coins` each have `enabled,source_amount,target_amount`. Amounts are decimal strings: whole coins on the coin side, millicredits on the credit side (1 credit = 1,000 millicredits). They define one whole lot. The positive integer `quantity` selects the number of lots; no rounding or partial debit occurs.

Quote before confirming. The quote contains settings and profile revisions. Changed rates or balances require a fresh quote. Closing the game or enabling maintenance pauses casts and stops exchanges while preserving progress. Coins and credits have no cash redemption or user-to-user transfer.

## Session API

Routes use the cookie session at `/api/games/lake-notes`. Mutations require the existing Origin/CSRF boundary and an `Idempotency-Key`.

| Method and suffix | Body or result |
| --- | --- |
| `GET /profile` | `readonly,revision,rules_id,profile,wallet,settings,cast` |
| `GET /rules` | Rule identity and compiled catalog |
| `POST /actions` | Typed `action`, action fields and `expected_profile_revision`; `buy_bait` accepts optional integer `quantity` (default 1, maximum 999), purchasing the affordable available amount |
| `POST /exchange/quote` | `direction,quantity`; returns exact lots, totals, `settings_revision,profile_revision` |
| `POST /exchange` | Quote inputs plus `expected_settings_revision,expected_profile_revision` |
| `POST /casts` | `expected_profile_revision`; starts one authoritative cast |
| `GET /casts/{id}` | Confirmed cast and profile |
| `POST /casts/{id}/checkpoint` | `generation,expected_revision,from_tick,to_tick,initial_held,edges:[{tick,held}]` |
| `POST /casts/{id}/pause` or `/resume` | `generation,expected_revision` |

The frontend animates the shared deterministic rules immediately; the server verifies inputs and commits rewards. Cast leases stop offline progress. Resume takes control of the saved cast without resampling its encounter. Read-only state and pause remain available during maintenance.

Past periods are read-only at `GET /admin/api/games/lake-notes/periods?page=1&page_size=20`. Existing casts and exchange receipts may reference a source period. New exchanges reference a settings revision.

## Recovery and data

Replay lasts 24 hours and rechecks account authorization. Retry the original request after a lost response; reload state after a conflict. A profile and current or paused cast follow the account lifetime. Terminal summaries last 30 days; minimal identities needed by live replay receipts remain until those expire.

Account export includes the safe profile, casts and entry/exchange receipts. Private encounter/reward randomness and internal checkpoints are excluded. Deletion removes personal game data and links; settled anonymous ledger facts follow the site's financial retention policy.
