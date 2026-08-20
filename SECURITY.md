# Security Policy

## Supported versions

Only the latest published release and the current development branch are expected to receive security fixes while the project is in alpha. Older alpha builds may contain known or breaking security changes; update after taking a verified database backup.

## Reporting a vulnerability

Please do not disclose an unpatched vulnerability in a public issue, discussion, log, screenshot, or pull request.

Use GitHub's [Private vulnerability reporting form](https://github.com/waiting-here/NonbiriAPI/security/advisories/new), which is enabled for this repository. If GitHub makes that form unavailable, use a private maintainer channel from the repository profile; do not fall back to a public issue with vulnerability details. Do not include live credentials, caller keys, master keys, production databases, or personal data.

A useful report includes:

- affected commit or release;
- deployment shape and relevant configuration category, without secret values;
- a minimal reproduction or safe proof of concept;
- impact, prerequisites, and whether data or credentials may be exposed;
- a proposed mitigation, if known.

Maintainers should acknowledge a report, reproduce it in an isolated environment, assess severity, prepare a fix and regression test, and coordinate disclosure with the reporter. Timelines may vary during the alpha.

## Deployment security baseline

Operators should:

- run the service as a dedicated unprivileged account;
- keep `admin.env`, the master-key file, database, backups, and logs out of the Git working tree;
- use TLS at the public reverse proxy and configure only its actual addresses in `NONBIRI_TRUSTED_PROXY_CIDRS`;
- expose the administrator host separately and restrict it at the network or identity-provider layer where possible;
- use unique administrator credentials and rotate caller/upstream keys after suspected exposure;
- verify backups and test restoration before upgrades;
- avoid putting secrets in URLs, notes, issue text, shell history, or diagnostic output.

The project is an alpha release. A passing test suite is not a guarantee that every deployment, reverse proxy, upstream provider, or operational process is secure.
