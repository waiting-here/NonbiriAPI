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
Bidding Duel and Turn-based Battle Minigame (Test) each use independent rules version 1, with their
own queues and user slots. Caller input cannot downgrade a new game. The game host
projects both wallets and immutable newcomer-task metadata, while the terminal
transaction consumes the corresponding reward hold and records the once-only
completion. Modules retain source payment amounts for refunds and never infer
historical funding that was not recorded.

The two-player service shares admission, ledger ports, simultaneous phase locks,
bounded workers, safe history and lifecycle behavior. Rules remain in independent
Bidding and Likes engines. Likes commits a round atomically before its event-paced
presentation; clients consume the server's structured events and replenish from
the next round's facts. A process startup cancels unfinished two-player games;
later periodic recovery only advances live deadlines. Neither presentation nor a
client acknowledgement controls final accounting.

Bidding and Likes have empty newcomer-task lists. Their personal export sections
are included in the central version-8 envelope. Thirty-day complete histories
become anonymous long-term traces through one transactional migration, and the
administrator exporter uses bounded pages without copying opponent identities
into ordinary user exports.

Blackjack uses a separate multi-seat module, not the two-player service. A single
table follows server minutes with 15 seconds for seating, 30 for decisions and 15
for results. Up to nine players are admitted from a persistent FIFO queue, with
at most 4,096 waiters. Queue entry and each split or double reserve actual payment
sources atomically; normal per-hand returns become General Credits after the
frozen fees, while cancellation refunds the original sources. The game starts
disabled and adds no newcomer rewards. Completed players explicitly rejoin at
the tail. Restart cancels only an unsettled table and preserves unseated waiters;
maintenance releases waiters and undealt seats while dealt tables finish. Bans
auto-stand dealt hands; deletion removes identity and finishes those hands without
cancelling other seats or recreating a wallet. Export v8 includes owner-safe queue,
current-table and recent-history records; 30-day records become administrator-only
anonymous facts without payment sources or emotes.

All six modules use the shared, versioned randomness protocol. The proof store
has seven explicit cascading parent references for five resource families;
LinkLink and RPS move a proof from live state to their retained terminal summary.
Active projections and account exports expose only commitment metadata. Blackjack
reveals after all table decisions end and the result commits; Bidding, Likes,
RPS and LinkLink reveal after the whole game ends. Intermediate Likes settlement
does not reveal the seed. Instant Fishing batches expose a proof with the result.
Proofs follow owner access and parent retention, with terminal access limited to
30 days; current Blackjack spectators do not gain history access. Seeds and
commitments are excluded from anonymous archives. See the
[randomness protocol and verification guide](game-randomness.md).
