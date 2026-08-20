# Release checklist

Use this checklist for every release candidate. It is not evidence that an item passed: record results in the release process, keep real secrets/private deployment details out of the repository, and do not create a tag until every required item is complete. This reusable checklist supersedes the one-time alpha.1 checklist, which remains available in repository history.

## Scope, version, and compatibility

- [ ] Freeze the release requirements and API/behavior compatibility statement.
- [ ] Update version references in the changelog, package metadata, README, API contract, and deployment examples.
- [ ] Document connector/API limitations, database migration requirements, downgrade limits, and rollback procedure.
- [ ] Confirm every public behavior in the release notes is implemented and tested; do not describe planned features as shipped.
- [ ] Review dependencies, generated notices, license obligations, and source-availability requirements.

## Data lifecycle and legal

- [ ] For every new user-associated table/column, complete [data-lifecycle-checklist.md](data-lifecycle-checklist.md): export, delete, retention, privacy, late writes, and tests.
- [ ] Verify account export never includes plaintext/ciphertext secrets, OAuth tokens, caller-key plaintext, or request/response content.
- [ ] Verify account deletion and late callbacks remain atomically linearized.
- [ ] Update the embedded Chinese/English privacy and terms templates for shipped data processing and permissions.
- [ ] Require each operator to review effective date, identity/contact, jurisdiction, subprocessors, backups, and instance-specific legal overrides before onboarding users.

## Clean build and automated checks

Run from a clean release checkout and preserve every command's real exit status:

```sh
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
scripts/check-go.sh
scripts/race-check.sh
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

- [ ] Confirm the frontend build regenerated `web/THIRD_PARTY_NOTICES.md` from the clean install and the diff is expected.
- [ ] Confirm the `-tags dist` binary embeds both real station bundles rather than development placeholders.
- [ ] Inspect `go version -m ./nonbiriapi`; record source commit, toolchain, target OS/architecture, and SHA256.
- [ ] Run static/dependency/vulnerability/license scans and gitleaks against both history and the exact release diff.
- [ ] If publishing binaries, produce the decided checksums, SBOM/provenance/signatures and complete license/source offer; otherwise state source-only clearly.

## Security and integration

- [ ] Independently review authentication, ownership, station isolation, egress, secret handling, response bounds, stream termination/cancellation, rate/concurrency limits, and no-store behavior.
- [ ] Run focused race/shuffle/attack regressions for changed security, accounting, deletion, or callback paths.
- [ ] Exercise non-streaming, streaming, cancellation, abnormal EOF, malformed usage, retry boundaries, and client disconnects against a disposable upstream.
- [ ] Verify real Discord OAuth with disposable credentials and the intended registration gate.
- [ ] Verify the complete TLS/reverse-proxy/real-IP path, both host boundaries, SSE buffering/timeouts, and unauthenticated admission limits.
- [ ] Check that logs, errors, alerts, CSV/JSON exports, HTML/text rendering, and generated artifacts contain no secrets or private deployment data.

## Migration, backup, and staging

- [ ] Stop writes and take a protected SQLite backup including applicable WAL/SHM sidecars, or use a tested SQLite backup API.
- [ ] Restore the backup in an isolated path with the same master key and verify it before migration.
- [ ] Test forward migration, restart recovery, retention cleanup, and the documented rollback/downgrade path on staging data.
- [ ] Test graceful shutdown, restart, previous-binary rollback, account export/deletion, and any new scheduled maintenance.
- [ ] Run a staging soak with representative concurrency and verify resource/memory/connection cleanup.

## Repository and publication

- [ ] Review the final `git diff`, tracked generated files, executable bits, LF endings, and sensitive-file status.
- [ ] If a version integration branch was used, review the complete merge-base-to-head diff and commit history, then open its single final pull request to the protected default branch.
- [ ] Confirm required `Go checks` and `Web checks` pass on the exact release commit and branch protection remains active.
- [ ] Confirm private vulnerability reporting is reachable and no deployment secrets are present in Actions/release settings.
- [ ] Prepare release notes with upgrade, backup, migration, rollback, known limitations, and checksums/artifacts as applicable.
- [ ] Create an annotated immutable tag only after owner authorization and green CI; never move a published tag silently.
- [ ] Mark alpha/beta releases as pre-releases, verify the public release page, and perform post-publication smoke/download checks.
