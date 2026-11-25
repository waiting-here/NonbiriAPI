# Game companions

Open Bidding Duel, Likes Duel or AI Gwent and choose companion play. Each game includes one local opponent. Companion matches are free and have no first-clear rewards, Elo or leaderboard entries. Human matches continue to use their game's own admission and settlement rules.

Finish or abandon the current game before starting another of the same kind. The game retains both seats' locked actions while the service is running. You can leave the page and return on another day. The AI may lock its choice while you think. The server reveals simultaneous choices together. A service restart cancels unfinished games. An execution timeout, invalid move or program failure ends that game and releases the seat.

## Decisions and replay

Both sides use the same seat and phase visibility as human opponents. For Gwent, you can leave the AI faction open or require one. In Likes, you can require a role, harness or particular skills. During setup the AI sees its own constraints, the public rules and authorized history. Once play starts, the server validates each move against the current window before accepting it.

| Game | Program input and output |
| --- | --- |
| Bidding | Revealed rewards, pot, hands remaining and public bids. Decide whether to double your reward with the joker or submit one remaining card. |
| Likes | Quick or standard catalog, visible resources and skills. Choose a legal loadout, then a complete plan with purchases, a main skill, payment and any additional skills in order. |
| Gwent | Faction constraint, public catalog, then your hand, public board and current choices. Choose a deck, initiative, exchange, play, leader, pass or card-effect target as prompted. |

The protocol is `nonbiri.decision/1`. Each request includes the game, mode, rules hash, decision ID, seat, phase, view, action space, recent revealed moves and private match memory. Each reply identifies the same decision and contains one action. Match memory is limited to 8 KiB per program. Two execution slots serve all three games. Each decision has 20 seconds after it starts, including setup and any history reads. Waiting for a slot and waiting for a human use separate states.

Your history shows how a completed or interrupted game ended. Step through the replay from your seat. Each match uses the program version selected when it began. The same decision interface can be used by future model adapters. The shipped opponents run locally.

## Memory and consent

Personalization starts off. You can allow all companions in the same game to use your eligible history, limit it to the same opponent, or turn it off. Separate memory follows the official style and difficulty. Eligible games accumulate during the 30-day retention window. Changing your preference applies to new matches.

Contributing to global history requires separate, explicit consent when a match starts. Only normally completed games against a built-in opponent qualify. Abandoned or cancelled games and games with a technical substitution or failure are excluded. Official companions can read deidentified eligible records and aggregate counts.

Each match selects up to 100 personal and 1,000 compatible global records. Overlapping records count once toward the shared 128 MiB uncompressed limit. Your account export includes your preferences, retained games and actions. Account deletion removes these records and their history links.

## Program availability

All three games use the built-in opponent. In v1.4.3 the global upload switch and the individual switches for Bidding, Likes and Gwent are fixed off. The player station hides upload controls. The administrator station shows the switches and the official program controls disabled, with the reason next to them. The companion play setting remains available.

## HTTP

For `bidding`, `likes` and `gwent`, the player base is `/api/games/{game}/companion`. Use the player's authenticated session and the normal station mutation protections.

| Method and suffix | Purpose |
| --- | --- |
| GET (base), PUT /preferences | Resume the current game and manage memory and global consent. |
| POST /matches, GET /matches/{id} | Start a free match or read its current seat view. |
| POST /matches/{id}/actions, POST /matches/{id}/abandon | Submit the current window's action or end the game. |
| GET /history, GET /matches/{id}/replay | Read the player's retained games and replay a step. |
| GET /programs | Read the built-in version and the game's upload availability. |

The administrator base is `/admin/api/games/{game}/companion`. GET reports `upload_global_enabled`, `upload_game_enabled` and effective `upload_available`, all `false` in this release, alongside the game's companion settings. POST `/settings` takes `{enabled}` and an `Idempotency-Key` to turn companion play on or off.

Old ticketed challenge sessions and first-clear receipts remain readable under their existing permissions and retention periods. Their opponents and strategy editor are retired.
