# Release checklist

Bind evidence to the candidate commit/tree, input and lock hashes, toolchain,
command, environment, time and real exit status. Reuse matching green results.
Schema, DTO, root wiring, dependency or lifecycle changes invalidate their
integration evidence; isolated copy and style changes affect their own checks.
This checklist does not itself assert a pass.

## Source and compatibility

- Synchronize version metadata, bilingual README/changelog, API, configuration,
  lifecycle, game, legal and deployment documentation.
- Validate the formal rc.1 source at `bd6198ceccb59dc8b8e0143831a94e94340235d1`
  to rc.2 upgrade. Generate populated samples with that exact old code. Include
  wide and negative balances, manual donation-credit adjustments, historical
  empty descriptions, bans, active/terminal games, reward holds, nine Blackjack
  seats and a waiter, saved catalogs, configuration and credentials.
- Check exact fresh/upgrade manifest equality, injected-failure rollback,
  repeated startup, unknown/partial schema zero-write rejection and old-binary
  rejection. Unreleased intermediate schemas are outside this guarantee.
- Preserve old account/entry IDs, settled charges, saved rules, configured games,
  security roots and legal overrides. Initialize loans disabled; derive valid
  quick stakes from existing limits. Reconstruct charity achievement from the
  ledger and initialize the game-statistics start once. Do not invent historical
  rewards, penalties or contributions.
- Check matching complete-snapshot restore. Never connect external SQLite to an
  online production database.

## Financial, control and lifecycle acceptance

- Verify 22 once-only newcomer tasks, multi-award atomicity, reward-capacity
  release, natural-21 stacking, surrender/system-cancel boundaries and deletion
  races. Awards and loans must not inflate game ranking contributions.
- Verify exact integer loan quotes, owner/expiry/config binding, maximum and
  invalid coefficients, idempotent replay, negative General Credit results,
  independent Game Credit availability, four-entry conservation and rollback.
- Verify four leaderboards, independent privacy preferences, banned anonymity,
  signed loss aggregation, positive-profit rules, logical 7/30-day expiry,
  tie ordering, immutable statistics start and bounded catch-up.
- Verify each authenticated pre-handler rejection is recorded once with no
  call fee; retain rule snapshots, manual endings and authorized traceability.
  Durable violation windows must survive restart without repeating measures.
  Check 90-day case and 30-day request-link boundaries.
- Verify character passives, simultaneous step snapshots, per-layer resistance,
  SOTA derivation, full/partial outcomes, deterministic proof replay and saved
  old catalogs. Run the ten-round local tutorial with the current engine and
  its actual 66–61 result, without real game or financial writes.
- Verify Blackjack :00/:30 rounds with 5/20/5 stages, early settlement extending
  presentation without advancing the next round, skipped-time recovery,
  3×3 seating, quick-stake validation/migration and select-before-queue behavior.
- Verify Bidding suits by seat; combined resource filters and equal count/row
  predicates; safe key reverse lookup including all still-associated states;
  required new donation descriptions with historical blank compatibility.
- Verify v9 owner-only exports, all-or-error size/row budgets, rollback on
  incomplete ranking catch-up, synchronous deletion and both late-write orders.
  Lazy game finalization, financial facts and rankings must agree in one snapshot.
- Keep prior ownership, egress, secrets, financial, randomness and game contracts
  intact. Reuse unchanged evidence and supplement candidate-specific gaps.

## Build and checks

Daily work runs affected checks. Stable candidates run the complete Go and web
gates; final Linux/race evidence may come from cumulative PR CI, supplemented by
local Windows and platform-specific checks.

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
npm --prefix web run test:e2e
scripts/check-go.sh
scripts/check-upgrade.sh
scripts/race-check.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags dist -trimpath -o nonbiriapi-linux-amd64 .
```

Run each command where its evidence is needed. One web build generates both
stations, notices and hashes. Preserve the full CI matrix: all twelve race
shards and aggregate Go/Web/CodeQL checks. Verify current dependency integrity,
license notices and available vulnerability scans; record actual findings and
unavailable scans. Never classify unavailable evidence as a pass.

- Build Windows/amd64 and Linux/amd64 pure-Go dist artifacts. Record revision,
  clean VCS, tree, toolchain, build parameters and SHA256; validate isolated
  startup and embedded resources.
- Cover new pages and changed flows in real Chromium at phone/desktop sizes,
  Chinese/English and light/dark themes, including keyboard and reduced motion.
- Validate sound separately with actual listening. Six-game feedback must use
  authoritative events, capped follow-up layers, distinct resistance outcomes,
  batch coalescing, music recovery and no historical replay on reconnect.
- Review the cumulative diff, LF, sensitive paths, executable bits and generated
  output. Retain exact command status and scope when reusing earlier results.

## PR, release and deployment

- Push one candidate PR to protected `master`, integrate any master advance
  without rewriting shared history, and require full exact-candidate CI.
  Save the successful run's original PR association record and checksum before
  normal protected merge.
- Synchronize local master and verify its tree equals the tested candidate.
  Create annotated `v1.0.0-rc.2` and a GitHub source prerelease at that merge
  commit, without public precompiled attachments.
- Once the tag is visible, create the final trusted Linux artifact from a clean
  independent checkout. Reuse matching business-test evidence, record its full
  revision and checksum, and verify the deployment source and target.
- Preserve current production data, keys, configuration, site name and legal
  overrides; loans stay disabled by default. A maintained local legal draft does
  not publish instance policy.
- Reuse valid CI, artifact and upgrade evidence. Supplement isolated
  startup/storage/finance validation where the schema or real deployment
  requires it. Do not rerun complete builds and tests on the VPS by default.
- Close admission, drain, stop, create one complete recovery snapshot, switch
  and start the target. Use the instance's measured health budget and stop
  polling as soon as health succeeds.
- After local health, reopen and check both stations, login boundaries and
  changed pages alongside about 60 seconds of runtime observation.
  Restore only within the helper's pre-commit boundary; after commit or opening,
  preserve newly accepted data and respond to the actual failure.
- Remove temporary processes and verification copies; retain the two eligible
  latest ordinary recovery sets and existing independent/exception sets.
- Deliver PR/release links, unified release/production revision, artifact checksum,
  acceptance results, recovery location and the legal draft awaiting manual
  publication.
