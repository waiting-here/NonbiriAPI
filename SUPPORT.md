# Support

NonbiriAPI is an alpha self-hosted project. Before asking for help:

1. Read [README.md](README.md) and [docs/deployment.md](docs/deployment.md).
2. Check the service journal and the bounded application diagnostic, removing secrets before sharing anything.
3. Confirm the relevant Host, TLS/reverse-proxy, database, master-key, and environment-file settings.
4. Reproduce with the smallest safe configuration possible.

Use GitHub Issues for reproducible bugs and documentation problems. Use discussions or the project support channel for usage questions when those features are enabled for the repository. Use [SECURITY.md](SECURITY.md) for vulnerabilities; do not publish sensitive security details in an issue.

Never attach:

- `admin.env`, master-key files, caller keys, upstream keys, or cookies;
- production SQLite databases or account exports;
- unredacted logs, screenshots, browser profiles, or request headers;
- private Discord or upstream provider information.
