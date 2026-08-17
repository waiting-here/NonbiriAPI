# v1.0.0-alpha.1 release checklist

This is an operator-facing preparation checklist for the first public GitHub release. It is not a substitute for the security, deployment, or legal review.

## Repository and versioning

- [ ] Confirm the canonical GitHub HTTPS URL and SSH remote.
- [ ] Set the Go module path to the canonical repository path, if the project is intended to be imported as a Go module.
- [ ] Decide the copyright holder and final SPDX/license wording.
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

- [ ] Add CI and release workflows with least-privilege permissions.
- [ ] Enable branch protection for `master`.
- [ ] Enable private vulnerability reporting, secret scanning, and dependency update tooling where available.
- [ ] Add issue forms, pull-request template, and code ownership.
- [ ] Configure repository description, topics, license metadata, default branch, and release as a pre-release.

## Deployment decisions still needed

- Supported VPS distribution and CPU architectures.
- Whether Docker/container artifacts are required in alpha.1 or systemd is sufficient.
- Public user/admin hostnames and reverse-proxy implementation.
- Database backup location, retention, and restore owner.
- Whether pre-alpha databases need any migration guarantee.
