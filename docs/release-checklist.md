# Candidate validation and deployment

Keep evidence tied to the candidate commit/tree, relevant inputs and lock hashes,
toolchain, command, environment, time and real exit status. Reuse matching results.
Schema, DTO, root wiring, dependencies and lifecycle changes invalidate the
corresponding integration evidence. This checklist does not assert that a check
has passed.

## During development

- Update implementation, affected tests, shared typed fixtures and independent
  expectations in the same logical change. Copy and style changes need only
  proportionate validation.
- Use the shared risk plan for Go packages, race, frontend tests, browser specs
  and real-backend suites. Unknown impact selects complete coverage. Run affected
  browser tests before review. Commands and scope rules live in
  [CONTRIBUTING](../CONTRIBUTING.md#required-checks).
- Check test discovery after additions, deletions and renames. Timing data only
  balances work; missing timings must never exclude a test.

## Stable candidate

- Validate fresh schema 17, the exact rc.7 source conversion, target equality,
  rollback/cancellation, re-entry and zero-write unsupported-source rejection.
  Preserve credentials, operator settings, legal overrides, balances, game saves,
  activities and receipts. Exercise successive stable migrations; see
  [database upgrades](database-upgrades.md).
- Prove catalog/template/period additions need no DDL and preserve administrator
  edits, historical eligibility, pity and claims. Recheck exports and deletion
  for every new user-associated record.
- Cover ownership, secrets, SSRF, original-input abuse priority, ledger atomicity,
  cancellation, bans and deletion races in the affected paths. Redemption tests
  distinguish invalid/already-claimed codes, first business refusals, later
  repeats, rolling-window boundaries and idempotent retries.
- Complete ordinary Go, populated conversion/upgrade, the applicable full race
  catalog, frontend unit/type/lint/build, browser smoke and real-backend suites.
  Retain required Go/Web check names and failure/cancellation propagation.
  Complete dependency, license, vulnerability and CodeQL checks; an unavailable
  scan is not a pass.
- Inspect desktop/mobile, light/dark and keyboard flows. Cover same-account
  session refresh during gacha entry, account switching, short/long SSE outages,
  delayed LinkLink responses, hidden-content loading and stale deployment assets.
  Preserve approved game designs, animations and reduced-motion fallbacks.
- Compare startup, SQL count, allocation and response timings with fixed inputs.
  Record CI queue, setup, build, test and runner usage from normal runs. Reduced
  effective coverage is not a performance improvement.
- Build clean Windows/amd64 and Linux/amd64 pure-Go dist artifacts. Record exact
  revision, tree, toolchain, build flags and SHA256; verify isolated startup and
  embedded resources. One frontend build supplies both stations, notices and
  the hash manifest.
- Review the cumulative diff, generated output, LF and sensitive-file exclusions.
  Reconcile API, configuration, lifecycle, deployment and developer documentation.

## Integration and production

1. Push one PR to protected `master`. Inspect existing/running candidate checks
   before scheduling any full run; a full PR run is sufficient. Merge normally
   after the required checks. If master advances, merge it and verify the affected
   inputs without rewriting shared history.
2. Compare the merged tree with the tested candidate and reuse valid evidence.
   Verify the independently triggered master security checks. Build the trusted
   final artifact from the exact merge commit; a tag is not required.
3. Follow the [deployment procedure](deployment.md): validate source compatibility,
   rehearse changed storage behavior on a consistent copy, close admission, stop,
   take one complete recovery snapshot, convert if required, clean deprecated
   environment keys and start the target once.
4. Verify local identity/readiness, then both public stations and changed pages
   alongside about 60 seconds of observation. After reopening, preserve newly
   accepted data; never automatically overwrite it with an old snapshot.
5. Remove completed validation copies and duplicate artifacts. Retain the two
   latest eligible ordinary recovery sets and any independently required sets.
   Record the deployed revision, artifact checksum, checks and recovery references.

Deployment, tags, GitHub releases and publishing instance legal text are separate
actions. The v1.0.0 preparation deploys an exact commit without a tag or GitHub
Release. The maintained instance legal draft is revised for the deployed behavior;
its publication follows the existing administrator workflow.
