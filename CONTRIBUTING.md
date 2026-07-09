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

Before tagging, run the release smoke checklist:

```bash
scripts/release_smoke.sh
```

By default the checklist stays hermetic: it runs the standard gates, mock semantic
smoke, a non-downloading `ajq provision --check`, release snapshot packaging,
installer snapshot smoke, and the website build with offline npm installation.
Live verification is opt-in and only runs when the matching environment variables
are set:

- `AJQ_PROVISION_LIVE=1` runs the live pinned llama-server engine download test.
- `AJQ_MODELS_LIVE=1` runs a real catalog model pull; set `AJQ_MODELS_LIVE_MODEL`
  to override the default model.
- `AJQ_LOCAL_LIVE=1 AJQ_LOCAL_BASE_URL=... AJQ_LOCAL_MODEL=...` runs local
  backend live conformance against an already-running llama-server.
- `AJQ_OLLAMA_LIVE=1` runs the Ollama CLI smoke; set `AJQ_OLLAMA_MODEL` or let
  the test use the first installed Ollama model.
- `AJQ_CONFORMANCE_LIVE=1` runs live backend conformance for local, Ollama,
  OpenAI, and Anthropic backends where their required provider env vars are set
  (`AJQ_LOCAL_BASE_URL`/`AJQ_LOCAL_MODEL`, `AJQ_OLLAMA_MODEL`,
  `OPENAI_API_KEY`/`AJQ_OPENAI_MODEL`, `ANTHROPIC_API_KEY`/optional
  `AJQ_ANTHROPIC_MODEL`; `AJQ_OPENAI_BASE_URL` may override OpenAI's base URL).
- `AJQ_OPENROUTER_LIVE=1 OPENROUTER_API_KEY=... AJQ_OPENROUTER_MODEL=...` runs
  an OpenRouter CLI smoke with `--max-calls 1`.
- `AJQ_ANTHROPIC_LIVE=1 ANTHROPIC_API_KEY=...` runs the Anthropic CLI smoke.

The underlying manual gate is:

```bash
make test
make lint
shellcheck scripts/install.sh scripts/install_test.sh scripts/release_smoke.sh
make release-snapshot
scripts/install_test.sh
make website-build
```

The Homebrew tap publish path uses the public `ricardocabral/homebrew-tap` repository
(`ricardocabral/tap`) and `HOMEBREW_TAP_GITHUB_TOKEN` secret. If that secret is not
available, GoReleaser skips tap upload and writes the generated cask under
`dist/homebrew/Casks/ajq.rb` for manual publishing.

## Repository security settings

Maintainers should keep the following GitHub settings aligned with the in-repo security posture:

- [ ] Enable GitHub Private Vulnerability Reporting so researchers can use **Security** → **Report a vulnerability** instead of opening public issues.
- [ ] Enable secret scanning and push protection for the repository.
- [ ] Protect `main` with a branch protection rule or ruleset that requires pull requests and required CI checks before merge.
- [ ] Scope `HOMEBREW_TAP_GITHUB_TOKEN` as a fine-grained personal access token limited to `ricardocabral/homebrew-tap` contents access required for publishing.
- [ ] Scope `WINGET_TOKEN` to the minimum permissions needed for the `winget-pkgs` fork publishing flow.
- [ ] Enable Dependabot alerts for repository dependencies.
