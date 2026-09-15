# Game modules

The backend assembles games explicitly at build time. Each module owns its rules,
configuration codec, HTTP handlers, persistent state, rankings, and recovery work.
The registry is sealed before listeners open; invalid declarations or conflicting
routes stop startup.

`internal/game` contains shared contracts and the start limiter. `game/host`
coordinates configuration transactions, registered projections, runtime startup,
and shutdown. `game/builtin` is the production catalog and contains the audited
financial adapters. `game/compat` holds the existing named configuration DTOs.
Fishing rules remain separate from its persistent runtime.

Configuration codecs declare their keys and defaults, validate partial updates,
and return fresh projections. The host applies a multi-game update in one
transaction, advances the shared revision once, and stores the idempotent response.
Configured availability and runtime readiness are separate capabilities.

All games finish validation and recovery before any worker starts. The host owns
one shared start limiter and closes modules in reverse registration order. Modules
close only their own workers and memory. A failed constructor or startup phase
does not leave an active game worker behind.

Homepage summaries, user configuration, and administrative active counts use
module projections from the same read transaction. Modules validate their own
states; the host checks declared identifiers, resource prefixes, bounds, and
ordering. Reading these projections does not acknowledge results or advance games.

Game commands retain their existing authorization, idempotency, and transaction
boundaries. Financial ports expose only the operations registered for each game.
Their adapters select the existing closed ledger constructors and verify source
resources and participants. Modules cannot submit arbitrary ledger plans or
mutate account balances directly. Capacity reservations, state changes, and
ledger entries commit together.

Account export and deletion borrow the coordinator's transaction through a bound
module capability. Commit and abort finalizers preserve lease and event cleanup.
Retention and recovery receive explicit limits and deadlines. The fixed export
envelope remains owned by the central lifecycle service; legacy game sections are
assembled at its compatibility boundary.

Adding a game requires a descriptor, codec, factory, routes, observation and
lifecycle capabilities, plus registration in the production catalog. Persistent
objects and export sections are added through the central schema and lifecycle
registration points. A new financial operation requires an audited ledger
constructor and a corresponding module port. Modules do not perform schema
migrations or register themselves through package initialization.

New Fishing, LinkLink and RPS entries use their registered version-2 rules.
Recovery selects saved version-1 or version-2 rules for accepted old work.
Bidding Duel and Likes Battle each use independent rules version 1, with their
own queues and user slots. Caller input cannot downgrade a new game. The game host
projects both wallets and immutable newcomer-task metadata, while the terminal
transaction consumes the corresponding reward hold and records the once-only
completion. Modules retain source payment amounts for refunds and never infer
historical funding that was not recorded.

The two-player service shares admission, ledger ports, simultaneous phase locks,
bounded workers, safe history and lifecycle behavior. Rules remain in independent
Bidding and Likes engines. Likes commits a round atomically before its five-second
presentation; clients consume the server's structured events and replenish from
the next round's facts. A process startup cancels unfinished two-player games;
later periodic recovery only advances live deadlines. Neither presentation nor a
client acknowledgement controls final accounting.

Both new games have empty newcomer-task lists. Their personal export sections
are added through the central version-7 envelope. Thirty-day complete histories
become anonymous long-term traces through one transactional migration, and the
administrator exporter uses bounded pages without copying opponent identities
into ordinary user exports.
