# v1.0.0-alpha.1 release checklist

This is an operator-facing preparation checklist for the first public GitHub release. It is not a substitute for the security, deployment, or legal review.

## Repository and versioning

- [x] Confirm the canonical GitHub URL: `https://github.com/waiting-here/NonbiriAPI` and SSH remote `git@waiting-here:waiting-here/NonbiriAPI.git`.
- [x] Set the Go module path to `github.com/waiting-here/NonbiriAPI`.
- [x] Set the copyright holder to `waiting-here`; document AGPL-3.0 in `NOTICE` and the README.
- [ ] Align application/frontend version metadata with `v1.0.0-alpha.1`.
- [ ] Review the final diff on `master`; keep the release tag annotated and immutable.

## Documentation and legal

- [x] English and Chinese README skeletons.
- [x] Changelog skeleton.
- [x] Environment variable example.
- [x] systemd/VPS deployment guide and unit example.
- [x] Security, contribution, conduct, and support guidance.
- [ ] Replace operator-specific placeholders in README, privacy, terms, and support text.
- [ ] Confirm effective date, operator identity, contact channel, jurisdiction, and data-processing disclosures.
- [ ] Review third-party notices for the final frontend and Go release artifacts.
- [ ] Publish the exact corresponding source commit/archive with every binary, satisfying the AGPL network-service source offer.

## Build and verification

- [ ] Run `scripts/check-go.sh` and `scripts/race-check.sh` from a clean checkout.
- [ ] Run frontend typecheck, lint, build, and manifest/notices generation.
- [ ] Build with `npm ci` followed by `CGO_ENABLED=0 go build -tags dist -trimpath`.
- [ ] Verify the binary embeds the real admin and user bundles, not the development stubs.
- [ ] Run dependency, vulnerability, license, and secret scans.
- [ ] Run a staging deployment with real reverse-proxy headers, disposable Discord OAuth credentials, and a disposable upstream.
- [ ] Verify backup/restore with the same master key in an isolated database path.
- [ ] Test graceful shutdown, restart, rollback, retention cleanup, export, and account deletion.
- [ ] Record SHA256 checksums and, if available, SBOM/provenance/signatures.

## GitHub settings

- [x] Add a CI workflow with read-only contents permissions.
- [ ] Add a release-artifact workflow with least-privilege permissions.
- [ ] Enable branch protection for `master`.
- [ ] Enable private vulnerability reporting, secret scanning, and dependency update tooling where available.
- [x] Add issue forms and pull-request template.
- [x] Add code ownership for `@waiting-here`.
- [ ] Configure repository description, topics, license metadata, default branch, and release as a pre-release.
- [ ] Enable Dependabot, private vulnerability reporting, secret scanning, and branch protection in GitHub settings; see `docs/github-settings.md`.

## Deployment decisions still needed

- Supported VPS distribution and CPU architectures.
- [x] Alpha.1 deployment artifact: source-first systemd deployment; Docker is deferred.
- [ ] Decide whether to publish optional prebuilt binaries for operator convenience.
- Public user/admin hostnames and reverse-proxy implementation.
- Database backup location, retention, and restore owner.
- [x] No pre-alpha database migration guarantee is needed; alpha.1 is the first release.
