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
