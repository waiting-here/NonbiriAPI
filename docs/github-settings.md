# GitHub repository settings

These settings live in GitHub and cannot be applied by `git push` or stored completely in the repository. Use this reference when reviewing the repository's protection, security and release settings.

## Private vulnerability reporting

Private vulnerability reporting is enabled. Periodically verify it under **Settings → Code security and analysis**, test that the repository's private advisory form is reachable, and keep owner/security notifications enabled.

`SECURITY.md` remains useful for deployment guidance; GitHub's private reporting form is the preferred vulnerability channel.

## Protect `master`

Use **Settings → Branches** (or a repository ruleset) and create a rule for `master` with:

- require a pull request before merging;
- require the `Go checks`, `Web checks`, and aggregate `CodeQL` status checks;
- block force pushes and branch deletion;
- require conversation resolution;
- do not permit bypassing the rule for routine or emergency changes.

Direct updates to `master` are disabled: all changes, including emergency fixes, must arrive through a pull request from another branch. Keep local `master` aligned with `origin/master`; do not locally merge a feature branch into `master` and then try to push it.

For a multi-part version, maintainers may use a version integration branch (for example, `codex/dev-v1.0.0-beta.4`), merge locally reviewed short-lived branches into it, and open one final pull request from that integration branch to `master`. The integration branch must remain buildable after each merge; local per-change review and gates are still required because the final pull request is not a substitute for incremental review.

Pull requests run a conservative routine CI scope. Manual dispatch defaults to full verification. After merging, verify that the resulting `master` tree matches the passing PR tree before reusing its evidence for release. A changed tree requires checks for the affected inputs. CodeQL keeps its independent triggers; removing redundant post-merge CI does not remove required PR checks or authorize direct updates.

`Go checks` requires ordinary build/vet/tests and populated upgrades, the live
race preparation, and every dynamically scheduled risk race shard.
`Web checks` requires unit/types/lint, an independent station build, and both
browser suites. Browser jobs consume the immutable build artifact ID and can
start while unit checks continue. The picture-book, audit, management, Fat Fish
and Lake Notes integration suites reuse one compiled test binary, with separate
processes and databases.

Routine PR runs use a conservative live dependency closure. Unknown input
falls back to full verification; manual dispatch defaults to full. Full
coverage runs all ordinary tests and the complete concurrency-risk catalog.
Exact reviewed exclusions in `internal/citools/raceplan/exclusions.json`
retain serial schema/upgrade/scale and pure-rule tests in ordinary coverage;
new names stay in race and removed/renamed exclusions fail policy validation.
Timing hints affect balance, never membership. The timing file records the Linux measurements and immutable run/commit source
used to calibrate scheduling; template and catalog changes can alter those
costs. Refresh estimates from actual complete CI logs, and report missing or
obsolete weights without changing live coverage. A reusable ordinary-built empty
database and race catalog are prepared once per run; each test gets an isolated
database copy and still exercises production Open validation. Fresh, upgrade
and recovery tests continue to use their actual startup paths.

Aggregate jobs run after failure or cancellation and accept a skipped child
only when the live plan records it as inapplicable. Keep the protected names
`Go checks`, `Web checks`, and the full-coverage confirmation steps stable.
Re-runs consume this run's immutable artifact IDs instead of resolving a build
or database template by a reusable name.

For a single-maintainer repository, requiring an approving review can make the owner unable to merge their own pull requests. Start with required status checks and no approval count, or add a trusted second maintainer before requiring one approval.

## Recommended repository options

- Keep **Allow auto-merge** disabled until the checks and review process are familiar.
- Enable Dependabot version updates using the committed `.github/dependabot.yml`.
- Keep CodeQL default setup enabled for Go, JavaScript/TypeScript, Python, and GitHub Actions. Require the aggregate result gate, review each alert against the exact data flow, and dismiss only a narrowly verified false positive rather than excluding the query globally.
- Enable secret scanning and push protection if the repository plan provides them.
- Keep the default branch as `master`; publish every alpha, beta or release candidate as a pre-release. A development branch is not published until its authorized tag and release steps are complete.
- Keep Actions permissions at the workflow default of read-only contents; do not add deployment secrets to the CI workflow.
- Pin every third-party and GitHub-authored Action to a reviewed full commit SHA and retain the corresponding release tag in a comment for maintainability.

## Release and access hygiene

- Do not put VPS credentials, OAuth secrets, master keys, database files or private trial URLs in repository settings, issues or workflow logs.
- Add release and signing permissions only when a separate release workflow is introduced.
- Review `CODEOWNERS` whenever maintainers change.
- Discussions are currently disabled; do not direct users there unless the feature and moderation/support policy are deliberately enabled.
