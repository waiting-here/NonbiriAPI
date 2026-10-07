# Steady Catch

Steady Catch is a single-player falling-object game at `/games/steady-catch`.
Move with the keyboard or touch controls, collect helpful objects and avoid harmful
ones. The game includes a browsable meme collection. Lasting 90 seconds with at least
one life and 600 points clears the game.

The module starts disabled. Administrators configure its ticket and one-time
first-clear reward in **Games**; both default to zero. Tickets use game credits
first, then general credits. The first clear pays game credits; other results have
no reward. Normal losses and abandonment do not refund tickets. System cancellation
returns the recorded payment sources. Accepted games retain their original amounts.

The 7-day and 30-day boards include all normally completed games, use each player's
best score in the window, and break ties by the earlier result. They follow the
shared public-profile preference. First-clear eligibility survives ordinary history
cleanup and follows the account-continuity policy.

## Runtime and API

The browser renders immediately from the same deterministic rules used by Go.
Only ordered inputs are submitted: the server checks the current revision, elapsed
time and legal movement, simulates the result and settles it atomically. Pausing
and reconnecting preserve the last accepted state. A session expires 30 minutes
after creation, including paused time; restart recovery pauses active play.

Authenticated browser routes under `/api/games/steady-catch`:

| Method and path | Purpose |
| --- | --- |
| `GET /catalog` | Rules and meme entries |
| `POST /sessions` | Start with `{}` and an `Idempotency-Key` |
| `GET /session` | Read or recover the current game |
| `POST /sessions/{id}/controls` | Revisioned input batches, pause, resume or abandonment |
| `GET /leaderboard?window=7d` | Rolling board; also accepts `30d` |

Control requests carry `revision`, `action`, `until_tick` and `inputs`, with a
64 KiB body limit. A stale revision returns a conflict; read the session before
continuing. These routes use the browser session and normal CSRF protection.

Account export includes `steadycatch.first_cleared` and the owner's sessions,
simulation seeds and accepted input batches. Completed personal records last
30 days; deletion removes them and any live session through the common transaction.
The implementation lives in `internal/game/steadycatch` and
`web/src/user/games/steady-catch`.
