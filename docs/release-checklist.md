# Release checklist

Bind evidence to the candidate commit/tree, relevant input and lock hashes,
toolchain, command, environment, time and real exit status. Reuse matching green
results. Changed schema, DTOs, root wiring, dependencies or lifecycle invalidate
the corresponding integration evidence; isolated copy/style changes affect their
own checks. This checklist does not itself assert a pass.

## Source and compatibility

- Freeze scope and synchronize README, changelog, package metadata, HTTP contract,
  configuration, game-module and lifecycle documentation.
- Verify Generation 2 identity, eleven exact predecessor paths, atomic rollback on
  injected failure, unknown/partial schema zero-write rejection and second-start
  no-op. Build the exact beta.3 source to create a populated synthetic fixture;
  exercise the target on Linux, with old API reservations and all three games.
- Preserve original account/entry IDs, settled charges, saved game rules, configured
  RTP, security roots and custom legal text. New game wallets start at zero.
- Verify that old version-1 games drain while new admissions always select version
  2; never infer unrecorded historical payment sources.
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
- Verify export v6 and synchronous deletion across both assets, holds, game state,
  rankings and permanent newcomer completions, including both late-write orders.
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
twelve concurrent shards and verifies that every package or split test runs exactly
once. Timing hints affect balance, not coverage; verbose results expose individual
test durations for later rebalancing. Require all shards and aggregate Go/Web/CodeQL
checks; daily affected-package race defaults to one shuffled round.

- Check clean installation, `go mod verify`, current `govulncheck` and `npm audit`,
  dependency licenses/notices and redacted credential scans. Record findings and
  dispositions; an unavailable scan is not a pass.
- Confirm pinned Actions and real embedded bundles. Build Windows/amd64 and the
  final Linux/amd64 pure-Go artifact, record VCS/tree/toolchain and SHA256.
- Compare complete first-party JS/CSS gzip totals with an exact beta.3 clean build
  using the same tools; each station may grow by at most 64 KiB.
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
