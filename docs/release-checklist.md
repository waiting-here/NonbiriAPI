# Release checklist

Bind evidence to the candidate commit/tree, relevant input and lock hashes,
toolchain, command, environment, time and real exit status. Reuse matching green
results. Changed schema, DTOs, root wiring, dependencies or lifecycle invalidate
the corresponding integration evidence; isolated copy/style changes affect their
own checks. This checklist does not itself assert a pass.

## Source and compatibility

- Freeze scope and synchronize README, changelog, package metadata, HTTP contract,
  configuration, game-module and lifecycle documentation.
- Verify Generation 2 identity, atomic rollback on injected failure,
  unknown/partial schema zero-write rejection and second-start no-op.
  The deployed beta.4 source is the upgrade baseline for this target. Build that
  exact source to generate populated fixtures, then exercise the target on Linux
  with existing API reservations, wallets and games. Validate the final 117-table
  manifest; unreleased intermediate schemas are outside this upgrade gate.
- Preserve original account/entry IDs, settled charges, saved game rules, configured
  RTP, security roots and custom legal text. Existing game wallets remain unchanged; only older sources without them receive zero game wallets. Bidding, Likes and Blackjack start disabled on sources without their configuration.
- Verify that saved old games retain their rules, new Fishing/LinkLink/RPS admissions use version 2 and Bidding/Likes/Blackjack use their own version 1; never infer unrecorded historical payment sources or fabricate proofs for old games.
- Verify matching complete-snapshot restore and old-binary rejection of the new
  schema. Do not open an online production database with external SQLite.

## Financial, control and lifecycle acceptance

- Verify per-asset conservation, independent check-ins, game-welfare eligibility,
  mixed payments, original refunds, RPS per-round sources, actual charity charges
  above reserve, unknown versus zero usage and historical terminal replay.
- Verify Fishing per-outcome cuts, net rankings and preserved old RTP; all nine
  atomic once-only newcomer rewards and their capacity reservation/release.
- Verify LinkLink hints/deadlock refresh, shared 2/3/5 counters, no automatic v2
  refresh, completion bonuses, six boards, privacy, per-user best and earliest
  achievement ordering. Measure queries against 100,000 summaries.
- Verify role matrices in the final transaction, target promotion and actor
  demotion, limit strings/inheritance, levels, dual-wallet adjustments, shared
  announcement actions and no-content audits.
- Verify owner/management failure resets, generation-safe late callbacks,
  complete selection before bounded batches, interruption and exact uncertain
  replay. Do not introduce persistent background jobs for browser batching.
- Verify multiline Thursday text/preview and complete validation before model-ID
  deduplication.
- Verify export v8 and synchronous deletion across both assets, holds, game state,
  rankings and permanent newcomer completions, including both late-write orders.
- Verify Blackjack's single nine-seat table, fixed-minute stages, persistent FIFO,
  withdrawal/replacement cutoff and requeue behavior. Cover all 50 default stakes,
  game/general/mixed payment, atomic split/double additions, independently rounded
  per-hand fees, normal General Credit returns and original-source cancellation.
  Check maintenance, bans, deletion, restart before/after settlement and welfare
  asset accounting without restoring deleted accounts or cancelling other seats.
- Verify every table rule and fixed-seed strategy simulations over at least one
  million tables, including one/nine players and each seat. Report confidence
  bounds and actual rounding; do not infer negative expectation from random play.
- Verify six-game commitments, whole-game disclosure, deterministic replay and
  source/proof tamper rejection. Active status, logs, history, errors and export
  must omit unrevealed seeds, plans and future draws. Verify parent cleanup,
  owner-only history and removal of proof fingerprints from anonymous archives.
- Synchronize bilingual embedded privacy/terms and administrator/steward calling
  instructions. Keep instance custom legal overrides intact unless their
  replacement is explicitly authorized.

## Build and checks

Use a clean candidate checkout. Daily work runs affected tests; stable points run
the full Go gate. Final Linux Go/race evidence may come from the cumulative PR CI,
with local Windows and missing platform checks supplementing it.

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
npm --prefix web run test:e2e
scripts/check-go.sh
scripts/race-check.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags dist -trimpath -o nonbiriapi-linux-amd64 .
```

Run each command only where its evidence is needed. One frontend build generates
both stations, notices and hashes. CI divides the live race-test catalog across
twelve concurrent shards. Each shard uses a bounded four-command worker pool;
split packages are partitioned by timing weights and unsplit packages run one
package per command. It verifies that every package or split test runs exactly
once. Timing hints affect balance, not coverage; verbose results expose individual
test durations for later rebalancing. Require all shards and aggregate Go/Web/CodeQL
checks. The protected master is validated through pull requests; after merge,
reuse evidence for the identical tree and use manual dispatch when a rerun is
needed. Daily affected-package race defaults to one shuffled round.

- Check clean installation, `go mod verify`, current `govulncheck` and `npm audit`,
  dependency licenses/notices and redacted credential scans. Record findings and
  dispositions; an unavailable scan is not a pass.
- Confirm pinned Actions and real embedded bundles. Build Windows/amd64 and the
  final Linux/amd64 pure-Go artifact, record VCS/tree/toolchain and SHA256.
- Compare complete first-party JS/CSS gzip totals with an exact beta.4 clean build
  using the same tools; the user station may grow by at most 256 KiB and the
  administrator station by 96 KiB.
- Verify all three new games with real participant sessions, full match results and
  sealed per-asset accounting. Exercise 4096 queues, large history datasets and
  bounded export/worker transactions. Check the 127 art slots, simultaneous
  settlement, resource refill feedback, uncapped numeric counters and reduced
  motion at phone and desktop sizes. Unfinished final presentation must not
  delay or repeat wallet settlement.
- Include nine Blackjack players and spectators, public-card privacy, split-hand
  controls, reconnect de-duplication, and proof verification/download in all six
  games. Verify the dashboard endpoint total with 101 shared users and the large
  existing numbered-page fixture while retaining the legacy response limit.
- Cover actual HTTP financial/control flows and representative Chinese/English,
  light/dark, desktop/mobile combinations. Play LinkLink through ordinary UI,
  including a 10×10 board at 320 px; measure latency without extra anticheat.
- Review final diff, LF text, generated files, executable bits and sensitive paths.
  Reuse unchanged auth, egress, secret and protocol evidence; supplement only
  gaps created by this candidate.

## Candidate and deployment

- Verify origin and protected master, integrate any master advance, audit the
  cumulative diff and push one candidate PR. Require exact-candidate CI success.
- Prepare one trusted final Linux artifact. Production reuses validated evidence
  and checks that artifact's hash instead of repeating full Go/race/web/build.
- Close admission, drain, stop, create one complete snapshot and start the target
  once. Reuse valid isolated upgrade evidence; additional rehearsal requires a
  concrete startup/storage/accounting gap.
- After local health, reopen and perform public-page acceptance alongside about
  60 seconds of process/error observation. Do not overwrite newly accepted data
  automatically with the old snapshot.
- Remove temporary verification copies/processes and retain the two most recent
  successful ordinary-switch recovery sets; preserve independent backups,
  explicit exceptions and unresolved incident sets.
- Deliver candidate identity, PR/check status, production identity, acceptance
  results and practical test areas. Merge, tag and release require the owner's
  later testing decision.
