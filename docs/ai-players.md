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

Personalization starts off. You can share eligible personal history with all your opponents in that game, limit it to each opponent or turn it off. Separate memory follows the official style and difficulty or the identity of a named private program across upgrades. Eligible samples continue to accumulate during the 30-day window. Enabling memory later applies to new matches.

Global history requires separate, explicit consent at the start of a game. Only normally completed human games against an official opponent can contribute. The official program can read anonymized complete game records and aggregate counts. Private games, technical substitutions, execution failures, cancelled and abandoned games are excluded. A private program may receive only its author's permitted personal history. Changes to either preference apply at the next start.

The host selects at most 100 personal and 1,000 compatible global records per match. Unique uncompressed records share a 128 MiB budget across all decisions and reconnects. The account export includes your preferences, retained games and the sources and diagnostics of your own programs. Deleting the account removes these records and their history links.

## Private programs

Download the kit from the game to get the C++17 template, public sample input, catalog, JSON header, license and local build instructions. Upload one UTF-8 source file of up to 256 KiB. A successful submission passes a public sample decision before becoming the selected private version. On failure you can inspect the diagnostic and keep playing with the previous ready version. You can switch back to a ready version or delete an unused program.

Each player may keep eight named programs per game. The current version and versions pinned by unfinished matches remain available. Other versions are pruned to the newest eight at upload and expire after 30 days. Storage per game and author is bounded to 4 MiB of source and 32 MiB of compiled modules. The built-in version is available with the main server alone.

### Optional compilation and execution

On a Linux host, install the optional `nonbiri-companion-worker` binary alongside the executable `scripts/companion-worker.sh` launcher. Use a dedicated unprivileged service account with rootless Podman, a user systemd manager and delegated cgroups. Preload the WASI SDK image at the digest in the compiler source under that account. Set `NONBIRI_COMPANION_WORKER` to the launcher's absolute path in the service environment. The server can run with this setting omitted.

Build the companion worker with `CGO_ENABLED=0 go build ./cmd/nonbiri-companion-worker`. The compiler container has no network and receives only the submission and public SDK in a read-only mount. Compilation is serialized with a 90-second budget. Execution uses a separate WASI interpreter process, 128 MiB of guest memory and a 384 MiB process-group cap. The module can read its decision, public rule catalog and authorized history within the same decision budget. A guest cannot access the application's files, database or credentials.

## HTTP

For `bidding`, `likes` and `gwent`, the player base is `/api/games/{game}/companion`. Use the player's authenticated session and the normal station mutation protections.

| Method and suffix | Purpose |
| --- | --- |
| GET (base), PUT /preferences | Resume the current game and manage memory and global consent. |
| POST /matches, GET /matches/{id} | Start a free match or read its current seat view. |
| POST /matches/{id}/actions, POST /matches/{id}/abandon | Submit the current window's action or end the game. |
| GET /history, GET /matches/{id}/replay | Read the player's retained games and replay a step. |
| GET /kit | Download the game's C++17 starter kit. |
| GET /programs | Read upload availability, the official version and your named programs. Add `name` and `version` to read your own source. |
| POST /programs | Upload `{name, source}` and receive the version and sample status. |
| POST /programs/activate, POST /programs/delete | Select a ready version with `{name, version}` or remove a private program with `{name}`. |

The administrator base is `/admin/api/games/{game}/companion`. GET lists the enabled state and official versions. POST `/settings` accepts `{enabled}`. POST `/versions` compiles and tests an official source upload. POST `/activate` selects a ready version for future games. Version 0 selects the built-in program. Source and management mutations require an `Idempotency-Key`.

Old ticketed challenge sessions and first-clear receipts remain readable under their existing permissions and retention periods. Their opponents and strategy editor are retired.
