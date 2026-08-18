# v1.0.0-alpha.1 release checklist

This is an operator-facing preparation checklist for the first public GitHub release. It is not a substitute for the security, deployment, or legal review.

## Repository and versioning

- [x] Confirm the canonical GitHub URL: `https://github.com/waiting-here/NonbiriAPI` and SSH remote `git@waiting-here:waiting-here/NonbiriAPI.git`.
- [x] Set the Go module path to `github.com/waiting-here/NonbiriAPI`.
- [x] Set the copyright holder to `waiting-here`; document AGPL-3.0 in `NOTICE` and the README.
- [ ] Align the frontend package, API contract, changelog, and the replacement `v1.0.0-alpha.1` tag after the final dependency and test fixes; the Go binary version is identified by the tag and VCS metadata.
- [ ] Review the final diff on `master`; create the annotated release tag only after the required CI checks pass, then keep it immutable.

## Documentation and legal

- [x] English and Chinese README skeletons.
- [x] Changelog skeleton.
- [x] Environment variable example.
- [x] systemd/VPS deployment guide and unit example.
- [x] Security, contribution, conduct, and support guidance.
- [x] Publish deployer-safe README, privacy, terms, and support templates; instance-specific legal details remain an operator prerequisite before accepting real users.
- [ ] Confirm effective date, operator identity, contact channel, jurisdiction, and data-processing disclosures.
- [x] Generate and review the frontend third-party notices for the source release.
- [x] Alpha.1 is source-only; the annotated tag identifies the exact corresponding source archive, so no binary source-offer attachment is required.

## Build and verification

- [x] Run `scripts/check-go.sh` and `scripts/race-check.sh` from the release checkout.
- [x] Run frontend typecheck, lint, build, and manifest/notices generation.
- [x] Run `npm ci` followed by `CGO_ENABLED=0 go build -tags dist -trimpath`.
- [x] Verify a `-tags dist` binary embeds the real admin and user bundles, not the development stubs.
- [ ] Run dependency, vulnerability, and license scans; run gitleaks against both Git history and the pending diff with the repository's narrow fixture allowlist.
- [ ] Run a staging deployment with real reverse-proxy headers, disposable Discord OAuth credentials, and a disposable upstream.
- [ ] Verify backup/restore with the same master key in an isolated database path.
- [ ] Test graceful shutdown, restart, rollback, retention cleanup, export, and account deletion.
- [ ] Record SHA256 checksums and, if available, SBOM/provenance/signatures.

## GitHub settings

- [x] Add a CI workflow with read-only contents permissions.
- [x] No release-artifact workflow is required for the alpha.1 source-only release; prebuilt artifacts are deferred.
- [x] Enable branch protection for `master` with the required `Go checks` and `Web checks` statuses and no force pushes/deletion.
- [x] Private vulnerability reporting is enabled by the repository owner; remaining GitHub security features can be enabled incrementally.
- [x] Add issue forms and pull-request template.
- [x] Add code ownership for `@waiting-here`.
- [ ] Configure repository description, topics, license metadata, default branch, and release as a pre-release.
- [x] Enable Dependabot version updates and complete the branch-protection settings described in `docs/github-settings.md`.
- [ ] Enable and verify secret scanning and push protection if supported by the repository plan.

## Deployment decisions still needed

- Supported VPS distribution and CPU architectures.
- [x] Alpha.1 deployment artifact: source-first systemd deployment; Docker is deferred.
- [x] Defer optional prebuilt binaries; alpha.1 is source-first.
- Public user/admin hostnames and reverse-proxy implementation.
- Database backup location, retention, and restore owner.
- [x] No pre-alpha database migration guarantee is needed; alpha.1 is the first release.
