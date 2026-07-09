# Contributing

Thanks for helping improve `ajq`.

## Development

- Keep changes focused and small.
- Add or update tests for behavior changes.
- New or changed semantic backends must invoke `internal/backend/conformance` so the cross-backend schema-invariance contract stays enforced.
- Document relevant local validation before opening a pull request.
- Run `make lint` before opening a pull request; it uses the repository `.golangci.yml` config and must stay clean alongside `make test`.
- GitHub Actions workflows are pinned to commit SHAs; Dependabot keeps action pins and dependency manifests current.

## Pull requests

- Explain the user-visible change and why it is needed.
- Link related issues when available.
- Include any relevant command output in the PR description.

## Releases

Maintainers cut releases from `v*` tags. The release workflow runs tests and lint, then
uses GoReleaser to publish checksummed archives with `ajq --version` stamped from the tag.
Pull requests touching release packaging run a GoReleaser snapshot dry run.

Before tagging, run the local packaging gate:

```bash
make test
make lint
shellcheck scripts/install.sh scripts/install_test.sh
make release-snapshot
```

The Homebrew tap publish path requires the external `ricardocabral/homebrew-ajq` repository
and `HOMEBREW_TAP_GITHUB_TOKEN` secret. If that secret is not available, GoReleaser skips
tap upload and writes the generated formula under `dist/homebrew/Formula/ajq.rb` for
manual publishing.

## Repository security settings

Maintainers should keep the following GitHub settings aligned with the in-repo security posture:

- [ ] Enable GitHub Private Vulnerability Reporting so researchers can use **Security** → **Report a vulnerability** instead of opening public issues.
- [ ] Enable secret scanning and push protection for the repository.
- [ ] Protect `main` with a branch protection rule or ruleset that requires pull requests and required CI checks before merge.
- [ ] Scope `HOMEBREW_TAP_GITHUB_TOKEN` as a fine-grained personal access token limited to `ricardocabral/homebrew-ajq` contents access required for publishing.
- [ ] Scope `WINGET_TOKEN` to the minimum permissions needed for the `winget-pkgs` fork publishing flow.
- [ ] Enable Dependabot alerts for repository dependencies.
