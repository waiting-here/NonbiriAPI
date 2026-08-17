# GitHub repository settings

These settings live in GitHub and cannot be applied by `git push` or stored completely in the repository. Apply them after the repository is available at `github.com/waiting-here/NonbiriAPI`.

## Private vulnerability reporting

1. Open **Settings → Code security and analysis**.
2. Under **Private vulnerability reporting**, click **Enable**.
3. Keep repository notifications enabled for `@waiting-here`.

`SECURITY.md` remains useful for deployment guidance, but GitHub's private reporting form is the preferred vulnerability channel once enabled.

## Protect `master`

Use **Settings → Branches** (or a repository ruleset) and create a rule for `master` with:

- require a pull request before merging;
- require the `Go checks` and `Web checks` status checks;
- block force pushes and branch deletion;
- require conversation resolution;
- do not permit bypassing the rule unless an emergency recovery path is intentionally retained.

For a single-maintainer repository, requiring an approving review can make the owner unable to merge their own pull requests. Start with required status checks and no approval count, or add a trusted second maintainer before requiring one approval.

## Recommended repository options

- Keep **Allow auto-merge** disabled until the checks and review process are familiar.
- Enable Dependabot version updates using the committed `.github/dependabot.yml`.
- Enable secret scanning and push protection if the repository plan provides them.
- Set the default branch to `master` and publish alpha.1 as a pre-release.
- Keep Actions permissions at the workflow default of read-only contents; do not add deployment secrets to the CI workflow.

## Release and access hygiene

- Do not put VPS credentials, OAuth secrets, master keys, database files or private trial URLs in repository settings, issues or workflow logs.
- Add release and signing permissions only when a separate release workflow is introduced.
- Review `CODEOWNERS` whenever maintainers change.
