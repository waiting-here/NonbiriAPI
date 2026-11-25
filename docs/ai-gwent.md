# AI Gwent

AI Gwent is a two-player card game at `/games/gwent`, with OpenAI, DeepSeek,
Claude and Gemini factions. Players build a deck, exchange opening cards and
compete across up to three rounds. The card catalog and in-game descriptions
define individual abilities.

The original interface includes 16 standard presets, random decks, the card
catalog and up to eight custom decks per faction. Custom decks stay in this
browser under the signed-in account and can be imported or exported in the deck
editor. They are not part of the server's account export; deleting the account
clears its local decks in the browser performing the deletion.

The Go server owns the rules, shuffled decks, hidden hands and decisions. Human games give 20 seconds for opening exchanges, 30 for normal turns and 15 for intermediate choices. Three consecutive normal-turn timeouts lose a human match.

In companion play you face the built-in opponent. You can require an AI faction or leave it open. Each side builds its deck independently before playing. The game enforces the same seat visibility at every decision and replay step. Players can close the page and return while the service is running. A program failure, illegal move or 20-second execution timeout ends the match.

## Administration and settlement

The game and its Standard mode start disabled. The initial ticket is 5,000 credits;
administrators configure it and the three deductions in **Games**. Each deduction
defaults to 1%, for the platform, welfare pool and Thursday pool respectively.

Player matches use Bidding Duel's shared prize-pool rules: each participant funds the
accepted ticket, game credits first, and the winner receives the remaining pool
as general credits. A draw or system cancellation refunds original payment sources
without deductions. Terms freeze on admission; later settings do
not reprice a match. PvP has no separate fixed win reward or newcomer task.

Companion matches are free and do not change Elo or leaderboard totals. First-clear receipts and settled rewards from retired paid challenges keep their historical meaning. [Companion play, memory and consent](ai-players.md)

The 7-day and 30-day boards rank wins, then win rate, then earlier achievement.
An independent Elo record starts at 1,500 and uses K=32 with the standard 400-point
expectation curve. Both players' changes use their pre-match ratings and commit
once with the result. Cancelled matches do not affect rating. Ratings are absent
from user pages and public boards, but are included in the owner's account export.

## Extension and lifecycle

The shared duel service supplies queues, payments, history and deletion. Gwent
adds sequential decision windows and resumable effect choices. Actions must match
the current seat, revision and decision window; the server never sends an opponent's
hidden hand or deck order. Startup cancels unfinished matches and waiting entries, including those from the earlier challenges. System cancellation refunds original payment sources or releases outstanding holds. Previously committed results remain available.

Routes use `/api/games/gwent` for catalog, state, queue, actions, surrender,
history, randomness proofs and `leaderboard?window=7d|30d`. Companion games and owner-only replays use `/api/games/gwent/companion`. Administrator history
and ZIP export use `/admin/api/games/gwent/history`. They retain the shared
[duel authorization and history rules](duel-games.md).

Account export adds `gwent`, including owner-safe match records and competitive
results. Recent histories and competitive results last 30 days. Anonymous competitive
game traces omit identity, dates, payment sources and ratings. The current Elo aggregate
lasts with the account. Account deletion removes its rating and result links
without altering the other participant's financial result.

The native engine and scorer live in `internal/game/gwent`. The game-independent
[AI interface](ai-players.md) leaves observation and action validation with the
game, so a future scorer, neural model or remote provider can make decisions
without changing payment or lifecycle code.
