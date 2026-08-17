## Summary

<!-- What changed and why? -->

## Security and data impact

- [ ] No authentication, ownership, egress, secret, stream, or station-boundary change.
- [ ] I documented any security impact and added/updated regression coverage.
- [ ] I documented export, deletion, retention, and privacy impact for new user data.
- [ ] No real credentials, databases, exports, cookies, or private deployment files are included.

## Verification

- [ ] `bash scripts/check-go.sh`
- [ ] `bash scripts/race-check.sh`
- [ ] `npm --prefix web run typecheck`
- [ ] `npm --prefix web run lint`
- [ ] `npm --prefix web run build`
- [ ] Browser or integration verification, if applicable.

## Documentation / operations

<!-- Mention API, README, privacy, deployment, migration, rollback, or changelog updates. -->
