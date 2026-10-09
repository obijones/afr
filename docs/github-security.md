# GitHub repository security settings

GitHub settings are not stored in the repository, so this page records how
`obijones/afr` is configured and why. Reviewed 2026-10-09 against GitHub's
[secure use reference for Actions](https://docs.github.com/en/actions/reference/security/secure-use).

## Actions

| Setting | Value | Why |
| --- | --- | --- |
| Default workflow token permissions | read | Least privilege; the workflow also sets `contents: read` |
| Workflows can approve pull requests | off | A workflow must not approve its own changes |
| Allowed actions | GitHub-owned only (`actions/*`) | afr uses only `actions/checkout` and `actions/setup-go` |
| Require actions pinned to a full commit SHA | on | A SHA is the only immutable reference; tags can move |
| Workflows from fork pull requests | off; no tokens or secrets to forks | Untrusted code never runs with repository access |
| Access from other repositories | none | |
| Secrets, variables, environments | none | CI needs none |

In the workflow (`.github/workflows/ci.yml`): actions pinned by SHA with the release in a
comment, `persist-credentials: false` on checkout, a 15-minute job timeout, superseded
runs cancelled, no `pull_request_target`, and no untrusted input in `run:` scripts.
`.github/dependabot.yml` keeps the pins and Go modules current. `.github/CODEOWNERS`
covers `/.github/`.

## Repository

| Setting | Value | Why |
| --- | --- | --- |
| Dependabot alerts | on | Vulnerable Go modules or actions are reported |
| Dependabot security updates | on | Fix PRs are opened automatically |
| Delete branch on merge | on | Merged branches cannot collect stray commits |
| Forking | allowed (cannot change) | GitHub only allows changing this on org-owned private repos; only collaborators can fork a personal private repo |

## Not available on this plan

- **Branch protection and rulesets** (require a PR and passing CI on `main`, block direct
  pushes, enforce CODEOWNERS): need GitHub Pro or a public repository.
- **Secret scanning and push protection**: paid for private repositories.

Until then, the working rule is: never push to `main`; every change goes through a
feature branch and a PR the owner reviews and merges.
