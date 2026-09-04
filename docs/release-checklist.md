# Release checklist

Use this checklist for every release candidate. It is not evidence that an item passed: record results in the release process, keep real secrets/private deployment details out of the repository, and do not create a tag until every required item is complete. This reusable checklist supersedes the one-time alpha.1 checklist, which remains in the archived alpha.1 task record for historical reference.

## Scope, version, and compatibility

- [ ] Freeze the release requirements and API/behavior compatibility statement.
- [ ] Update version references in the changelog, package metadata, README, API contract, and deployment examples.
- [ ] Document connector/API limitations, Generation 2 identity (`application_id=0x4E425249`, `user_version=2`), the zero-write rejection matrix, downgrade limits, all four deployment-helper entry classes, and complete-snapshot rollback procedure.
- [ ] Confirm every public behavior in the release notes is implemented and tested; do not describe planned features as shipped.
- [ ] Review dependencies, generated notices, license obligations, and source-availability requirements.
- [ ] Confirm the candidate is source-first, the release target is Linux/amd64, and any convenience binary was built from the exact candidate commit with `CGO_ENABLED=0 -tags dist -trimpath`.

## Data lifecycle and legal

- [ ] For every new user-associated table/column, complete [data-lifecycle-checklist.md](data-lifecycle-checklist.md): export, delete, retention, privacy, late writes, and tests.
- [ ] Verify account export never includes plaintext/ciphertext secrets, OAuth tokens, caller-key plaintext, or request/response content.
- [ ] Verify account deletion and late callbacks remain atomically linearized.
- [ ] Update the embedded Chinese/English privacy and terms templates for shipped data processing and permissions.
- [ ] Confirm both languages cover OpenAI-compatible, Anthropic-compatible, and donor-provided third-party processing; the non-guarantee of `store:false`; tool-call flattening risk; Debug dry/live memory-only capture; server-authoritative game randomness/accounting; the single spendable credit balance versus cumulative donor reward; anonymous/public leaderboard identity and Discord CDN avatar behavior; donation-key secret cleanup versus the separate 90-day report fingerprint; export schema v4; and fresh destructive cutover/snapshot retention.
- [ ] Round-trip all four legal overrides with multiline Chinese/English, tabs, LF/CRLF, multibyte text, and a near-65,536-byte value through save → GET → refresh → re-save. Verify anonymous legal pages at 390 px, keyboard-only, and screen-reader semantics.
- [ ] Obtain the instance owner's explicit approval of the effective production text, then verify the four values and authoritative locale after applying them to the fresh database; a technical consistency review is not legal approval.
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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags dist -trimpath -o nonbiriapi-linux-amd64 .
```

- [ ] Confirm the frontend build regenerated `web/THIRD_PARTY_NOTICES.md` from the clean install and the diff is expected.
- [ ] Confirm every GitHub Action reference is pinned to a reviewed full commit SHA with its release tag recorded in a comment.
- [ ] Confirm the `-tags dist` binary embeds both real station bundles rather than development placeholders.
- [ ] Inspect `go version -m ./nonbiriapi-linux-amd64`; record source commit, toolchain, target OS/architecture, and SHA256.
- [ ] Run static/dependency/vulnerability/license scans and gitleaks against both history and the exact release diff.
- [ ] If publishing binaries, produce the decided checksums, SBOM/provenance/signatures and complete license/source offer; otherwise state source-only clearly.

## Security and integration

- [ ] Independently review authentication, ownership, station isolation, egress, secret handling, response bounds, stream termination/cancellation, rate/concurrency limits, and no-store behavior.
- [ ] Run focused race/shuffle/attack regressions for changed security, accounting, deletion, or callback paths.
- [ ] Exercise non-streaming, streaming, cancellation, abnormal EOF, malformed usage, retry boundaries, and client disconnects against a disposable upstream.
- [ ] Exercise OpenAI and Anthropic Connector fixtures, capability rejection, cumulative streaming usage and terminal events; verify `max_tokens`/`max_completion_tokens` absent/null/equal/conflicting behavior and the nullable 65,536 Anthropic fallback.
- [ ] Verify user-concurrency-before-RPM ordering and every release path under race; a concurrency denial must create no RPM hit, candidate selection, credential access, charity reservation, or penalty.
- [ ] Verify experimental-policy ownership and disabled byte-equivalence, bounded flatten/reverse-flatten streaming equivalence, Debug dry zero-egress and live observer transparency, all three games' idempotency/recovery/retention/privacy, report tombstone/lineage behavior, per-key donation expiry, and export schema v4/deletion lifecycle.
- [ ] Verify real Discord OAuth with disposable credentials and the intended registration gate.
- [ ] Verify the complete TLS/reverse-proxy/real-IP path, both host boundaries, SSE buffering/timeouts, and unauthenticated admission limits.
- [ ] Check that logs, errors, alerts, CSV/JSON exports, HTML/text rendering, and generated artifacts contain no secrets or private deployment data.

## Fresh database, backup, and staging

- [ ] Stop writes and take a protected complete snapshot: database and applicable WAL/SHM sidecars, exact release, environment/configuration, master key, unit, manifest, and checksums.
- [ ] Restore that snapshot in an isolated path with the same master key and verify its release/schema/config/key/unit match before relying on it.
- [ ] Test the fresh/current/legacy/empty/corrupt/unknown-generation/sidecar matrix and prove every rejected source is byte-, identity-, size-, and mtime-unchanged with no new source-side WAL/SHM.
- [ ] Test the default interactive deployment, `--restore-snapshot`, `--destructive-fresh-deploy`, and `snapshot inventory|import|delete` flows, including TTY-only confirmations, cancellation, incompatible/missing snapshot refusal, and failure restoration. Do not perform the rehearsal against production.
- [ ] Test restart recovery, game settlement/retention cleanup, complete-snapshot rollback/downgrade, and the documented absence of any binary-only downgrade path.
- [ ] Test graceful shutdown, same-generation restart with a compatible previous release, complete-snapshot version rollback, account export/deletion, and any new scheduled maintenance; never substitute a binary-only downgrade.
- [ ] Run a staging soak with representative concurrency and verify resource/memory/connection cleanup.
- [ ] Record which vulnerability, dependency, license, credential, SBOM, provenance, and signing checks actually ran and which remain explicitly deferred; do not mark a deferred gate as passed.

## Repository and publication

- [ ] Review the final `git diff`, tracked generated files, executable bits, LF endings, and sensitive-file status.
- [ ] Before any push, verify the SSH host alias, expected GitHub account, `origin`, remote `master`, and a dry-run of the exact integration-branch refspec.
- [ ] If a version integration branch was used, review the complete merge-base-to-head diff and commit history, then open its single final pull request to the protected default branch.
- [ ] Confirm required `Go checks`, `Web checks`, and aggregate `CodeQL` checks pass on the exact release commit and branch protection remains active; verify every configured CodeQL language analysis completed successfully and review any alert individually.
- [ ] Confirm private vulnerability reporting is reachable and no deployment secrets are present in Actions/release settings.
- [ ] Prepare release notes with fresh cutover/upgrade, backup, compatibility or migration limits, complete-snapshot rollback, known limitations, and checksums/artifacts as applicable.
- [ ] Create an annotated immutable tag only after owner authorization and green CI; never move a published tag silently.
- [ ] Mark alpha/beta releases as pre-releases, verify the public release page, and perform post-publication smoke/download checks.
