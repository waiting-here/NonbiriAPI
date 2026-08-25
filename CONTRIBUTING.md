# Contributing to NonbiriAPI

Thank you for contributing. NonbiriAPI is released under the GNU AGPL v3.0. By submitting a contribution, you agree that it may be distributed under the project's license.

## Before opening a change

1. Check existing issues and discussions for related work.
2. For a security issue, follow [SECURITY.md](SECURITY.md) instead of opening a public issue.
3. Keep one logical change per pull request where practical.
4. Do not include credentials, databases, exported account data, browser profiles, or private deployment files.
5. Update user-facing documentation, privacy text, API contract, and changelog when behavior or data lifecycle changes.

## Development environment

- Go 1.26.x.
- Node.js 22.22.3 or newer and npm 12.0.1 for the frontend.
- Bash is required for the repository scripts (`set -euo pipefail` is used). On Windows, use Git Bash.

The frontend dependencies are not part of the Go module. Run npm commands from the repository root with `--prefix web` or from `web/`.

## Required checks

Backend:

```sh
scripts/check-go.sh
scripts/race-check.sh
```

Frontend:

```sh
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

For a release-like binary, build the frontend first and then run:

```sh
CGO_ENABLED=0 go build -tags dist -o nonbiriapi .
```

Do not hide a command's exit status behind a truncating pipeline. Do not terminate processes by name when testing locally; stop the specific process you started.

## Security and data boundaries

Every new user-associated field must have an export, deletion, retention, and privacy decision. Preserve ownership checks, station isolation, encrypted secret storage, bounded diagnostics, shared egress protection, stream cancellation, and no-store behavior. Never add plaintext secrets to logs, errors, tests that resemble production credentials, URLs, frontend query caches, or persistent browser storage.

## Commit and pull request guidance

`master` is protected and PR-only; never commit or push directly to it, including for emergency fixes. Maintainers may designate a version integration branch, merge reviewed short-lived work branches into it locally, and open one final GitHub pull request to `master` after the complete version passes its gates. Contributors should confirm the intended base branch before starting work.

Use a concise public commit subject such as:

```text
feat: add endpoint health summary
fix: reject invalid caller key input
```

Pull requests should explain the behavior change, security/data impact, migration or rollback implications, tests run, and any operator documentation changes. When frontend dependencies change, regenerate and review the tracked `web/THIRD_PARTY_NOTICES.md`; build/hash manifests under ignored `web/dist*` are local release-verification artifacts and are not committed.
