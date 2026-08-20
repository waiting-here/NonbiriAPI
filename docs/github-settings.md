# GitHub repository settings

These settings live in GitHub and cannot be applied by `git push` or stored completely in the repository. Apply them after the repository is available at `github.com/waiting-here/NonbiriAPI`.

## Private vulnerability reporting

Private vulnerability reporting is enabled. Periodically verify it under **Settings → Code security and analysis**, test that the repository's private advisory form is reachable, and keep owner/security notifications enabled.

`SECURITY.md` remains useful for deployment guidance; GitHub's private reporting form is the preferred vulnerability channel.

## Protect `master`

Use **Settings → Branches** (or a repository ruleset) and create a rule for `master` with:

- require a pull request before merging;
- require the `Go checks` and `Web checks` status checks;
- block force pushes and branch deletion;
- require conversation resolution;
- do not permit bypassing the rule for routine or emergency changes.

Direct updates to `master` are disabled: all changes, including emergency fixes, must arrive through a pull request from another branch. Keep local `master` aligned with `origin/master`; do not locally merge a feature branch into `master` and then try to push it.

For a single-maintainer repository, requiring an approving review can make the owner unable to merge their own pull requests. Start with required status checks and no approval count, or add a trusted second maintainer before requiring one approval.

## Recommended repository options

- Keep **Allow auto-merge** disabled until the checks and review process are familiar.
- Enable Dependabot version updates using the committed `.github/dependabot.yml`.
- Enable secret scanning and push protection if the repository plan provides them.
- Keep the default branch as `master`; `v1.0.0-alpha.1` is already published as a pre-release. Mark future alpha releases as pre-releases as well.
- Keep Actions permissions at the workflow default of read-only contents; do not add deployment secrets to the CI workflow.

## Release and access hygiene

- Do not put VPS credentials, OAuth secrets, master keys, database files or private trial URLs in repository settings, issues or workflow logs.
- Add release and signing permissions only when a separate release workflow is introduced.
- Review `CODEOWNERS` whenever maintainers change.
- Discussions are currently disabled; do not direct users there unless the feature and moderation/support policy are deliberately enabled.
