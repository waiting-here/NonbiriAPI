# Contributing to NonbiriAPI

NonbiriAPI is distributed under AGPL-3.0. Contributions use the same license.

## Before opening a change

1. Check existing issues and discussions for related work.
2. For a security issue, follow [SECURITY.md](SECURITY.md) instead of opening a public issue.
3. Keep one logical change per pull request where practical.
4. Do not include credentials, databases, exported account data, browser profiles, or private deployment files.
5. Update affected tests, shared fixtures, API examples, documentation and verification scope with the implementation. Data-lifecycle changes also require privacy and export/deletion updates.

## Development environment

- Go 1.26.9.
- Node.js 22.22.3 or newer and npm 12.0.1 for the frontend.
- Bash is required for the repository scripts (`set -euo pipefail` is used). On Windows, use Git Bash.

The frontend dependencies are not part of the Go module. Run npm commands from the repository root with `--prefix web` or from `web/`.

## Required checks

Install the locked frontend dependencies and Chromium before browser checks:

```sh
npm --prefix web ci
cd web && npm exec -- playwright install chromium
```

From the repository root, inspect and execute the current change's verification
plan. An omitted or unavailable base selects the complete checks.

```sh
bash scripts/check-risk.sh --base origin/master --plan-only
bash scripts/check-risk.sh --base origin/master
```

The plan includes staged, unstaged and untracked inputs, reverse Go dependencies,
concurrency risks, browser specs and real-backend suites. Known API modules select
their browser contracts; shared or unknown inputs retain full coverage. Use
`--component go|race|web|browser` for an individual layer. `web` builds both
stations and runs its selected browser tests; `browser` reuses an existing build.
`GO`, `NPM` and `CC` can select installed tool executables.

Maintain tests during development: add the regression with the behavior change,
update typed response fixtures when a DTO changes, and run the affected browser
contracts before handing over the change. Keep assertions independent of the
implementation. Pure prose or style changes only need checks for their actual
impact. New browser modules belong in the small module map in `riskplan`; unknown
modules remain covered by the full fallback.

Stable integration points and final candidates use:

```sh
bash scripts/check-risk.sh --mode full
```

`check-go.sh` remains the full build/vet/test entry, `check-upgrade.sh` checks
populated conversion, and `race-check.sh` runs the concurrency-risk catalog.
CI retains the required `Go checks` and `Web checks` summaries. Browser smoke
runs in two whole-spec shards with one worker each; real-backend suites run in
two isolated groups, sharing only the immutable compiled fixture and station
build. Every selected child must succeed, including fixture preparation.

The complete race gate derives its package and test catalog from the checked-out
source. `scripts/race-timings.json` only balances work across the sixteen CI
shards; stale or missing timings must never omit a test. Keep fresh-database,
upgrade, rollback and lock tests on their actual initialization paths. Tests
that only exercise constraints may copy a validated schema image into a private
database, provided mutations and cleanup cannot affect another test.

Each CI run records queue/setup/build/test times and runner-minutes separately.
Race logs and browser JSON measurements are retained for three days. To refresh
balancing hints from downloaded measurements:

```sh
node scripts/update-verification-timings.mjs /path/to/artifacts RUN_URL COMMIT
```

Review the generated timing changes. Test discovery defines coverage; timing
files only define placement. Do not start another full workflow while the same
candidate is running. A successful PR run whose Go and Web summaries both
confirm full coverage supplies final verification evidence. Use manual full
verification only when that coverage is missing or its source/tree, lockfile,
toolchain or relevant input identity has changed. Preserve the PR's required
checks when reusing evidence across a merge.

For a release-like binary, build the frontend first and then run:

```sh
CGO_ENABLED=0 go build -tags dist -o nonbiriapi .
```

Do not hide a command's exit status behind a truncating pipeline. Do not terminate processes by name when testing locally; stop the specific process you started.

## Performance comparisons

Use synthetic inputs and an isolated machine window for before/after comparisons:

```sh
CGO_ENABLED=0 go test ./internal/charityrouting -run '^$' -bench BenchmarkCatalogRecentSuccess -benchmem
NONBIRI_LOAD_TEST=1 CGO_ENABLED=0 go test ./internal/app -run '^TestApplicationCatalogLoad$' -count=1 -v -timeout=10m
NONBIRI_LOAD_TEST=1 NONBIRI_LOAD_SCALE=production CGO_ENABLED=0 go test ./internal/app -run '^TestApplicationSyntheticLoad$' -count=1 -v -timeout=10m
```

The catalog benchmark varies page size and history size outside setup timing.
The opt-in application test uses private temporary databases and a loopback
upstream with the real authentication, routing, game workers and settlement
paths. It compares no model traffic, 50/100/200 long requests, a short-response
burst and slow readers with cancellation. It retains default admission and
egress budgets; offered, admitted and upstream-active counts are distinct.
Page probes offer 15 requests/second against a fixed clock, including all three
directory entrances. Slow responses do not reduce the planned arrival rate.
The catalog experiment separately offers 15/30 RPS for 30 seconds and bursts of
25/50 concurrent directory requests. Its fixture has 220 models, 600 bindings,
70,000 terminal fishing batches and 385,000 corresponding outcomes; the
`production` scale selects that same fixture for the streaming experiment.
All records are synthetic. Bindings share one approved donation key, so these
tests characterize this fixed workload rather than every production resource mix.

`LOAD_RESULT` and `CATALOG_LOAD_RESULT` JSON report page/model percentiles,
planned/submitted/completed requests, status/rejection counts, both pools' wait
deltas, process CPU time, Go heap/allocation data and recovery. The catalog result
also records submission lag. `last_window_operations` includes background work
since the observer's last minute drain, so it is not necessarily the entire run.
CPU timestamps are cumulative process totals; subtract before from after to
exclude fixture preparation. Pool wait time sums all waiters and can exceed wall
time. Check completion bursts separately from steady
streams, and retain failed admissions when comparing throughput. The test checks
protocol completion and releases of claims/reservations; it has no machine-speed
pass threshold and is skipped by ordinary CI. Bind results to source, toolchain,
configuration and hardware; these local measurements do not establish production
capacity. For the fixed standard catalog workload, evaluate each directory at
P95 <= 1 second and P99 <= 2 seconds with no unexpected timeout or 5xx. The double
workload reports headroom, without becoming a production capacity promise.
Runtime aggregate interpretation is in [deployment.md](docs/deployment.md).

## Security and data boundaries

Every new user-associated field must have an export, deletion, retention, and privacy decision. Preserve ownership checks, station isolation, encrypted secret storage, bounded diagnostics, shared egress protection, stream cancellation, and no-store behavior. Never add plaintext secrets to logs, errors, tests that resemble production credentials, URLs, frontend query caches, or persistent browser storage.

Normalize display responses once at the API boundary: require the fields the
view uses, preserve exact numeric strings, and project only known fields. Ignore
unused response extensions instead of failing the page or forwarding them into
forms. Account balances can be negative; clamp each wallet separately only when
calculating spendable funds. Render server-calculated totals and settlements
without deriving them again. Keep strict schemas for request inputs, executable
adapter declarations, randomness proofs and deterministic game protocols, plus
identity, visibility and state checks that affect available actions.

## Commit and pull request guidance

`master` is protected and PR-only; never commit or push directly to it, including for emergency fixes. Maintainers may designate a version integration branch, merge reviewed short-lived work branches into it locally, and open one final GitHub pull request to `master` after the complete version passes its gates. Contributors should confirm the intended base branch before starting work.

Use a concise public commit subject such as:

```text
feat: add endpoint health summary
fix: reject invalid caller key input
```

Pull requests should explain the behavior change, security/data impact, migration or rollback implications, tests run, and any operator documentation changes. When frontend dependencies change, regenerate and review the tracked `web/THIRD_PARTY_NOTICES.md`; build/hash manifests under ignored `web/dist*` are local release-verification artifacts and are not committed.
