# Contributing

Thanks for helping improve `ajq`.

## Development

- Keep changes focused and small.
- Add or update tests for behavior changes.
- New or changed semantic backends must invoke `internal/backend/conformance` so the cross-backend schema-invariance contract stays enforced.
- Document relevant local validation before opening a pull request.
- Run `make lint` before opening a pull request; it uses the repository `.golangci.yml` config and must stay clean alongside `make test`.
- GitHub Actions workflows are pinned to commit SHAs; Dependabot keeps action pins and dependency manifests current.

## Reproducible lint checks

The required lint version is **golangci-lint v2.9.0**, with **Go 1.26.4** from
`go.mod`. Use this pair for required checks even if a newer Go or linter is on
your `PATH`. `make lint` rejects other linter versions before analysis. CI and
release checks use the same pin; changing it is a separate reviewed tooling
upgrade. Keep `Makefile`, `.golangci.yml`, and both workflow pins aligned.

Download the v2.9.0 archive for your platform from the
[official release](https://github.com/golangci/golangci-lint/releases/tag/v2.9.0)
and install Go 1.26.4 before running checks. For a separately installed linter:

```bash
GOTOOLCHAIN=go1.26.4 make lint GOLANGCI_LINT=/path/to/golangci-lint
bash scripts/lint_test.sh
```

Tool installation may require network access; verification with installed tools
and cached Go modules needs no cloud credentials or model assets.

The v2.9.0 release binary is built with Go 1.26.0 and supports the module's Go
1.26 language version. The exact Go 1.26.4 / v2.9.0 pair was verified clean for
[issue #101](https://github.com/ricardocabral/ajq/issues/101). Per the
[upstream compatibility policy](https://golangci-lint.run/docs/welcome/faq/),
the linter must support the Go version used to analyze the project. A newer
local Go toolchain can require a linter upgrade even when `go.mod` is unchanged.
In particular, v2.13.2 built with Go 1.27.0 could not decode this workstation's
Go 1.27.3 export data; use the declared toolchain instead of treating those
typechecking errors as source defects.

### Newer gosec review (#101)

At base `851b5c6`, v2.9.0 with Go 1.26.4 reported zero findings.
Separately, v2.13.2 (built with Go 1.27.0), analyzing with Go 1.26.10,
reported the seven findings below. These are contextual false positives;
review found no untrusted remote input crossing these boundaries.

| Rule | Location | Classification and disposition |
| --- | --- | --- |
| G101 | `internal/backend/oai/conformance_test.go`, backend literal | Synthetic credential for a scripted loopback server; environment field holds a variable name. Narrow G101 annotation. |
| G101 | `internal/backend/oai/oai_test.go`, `baseBackend` | Synthetic credential used by `httptest`, with bearer-header assertions. Narrow G101 annotation. |
| G101 | `internal/backend/oai/live_conformance_test.go`, backend literal | Environment-variable name, not a credential value. The actual credential comes from the environment in an opt-in test. Narrow G101 annotation. |
| G703 | `internal/bench/realbench.go`, `fileExists` | Read-only stat of an operator-selected local asset. External asset paths are intentional; annotate G703 and test explicit paths, missing files, and directories. |
| G703 | `internal/daemon/config.go`, `defaultFileExists` | Read-only stat of an operator-selected engine path. Config/env overrides intentionally allow files outside the cache; annotate G703 and test discovery with real files. |
| G703 | `internal/cli/models_test.go`, invalid-config fixture | The test sets the environment path to its own temporary directory. Write through the local path variable to make ownership explicit. Existing assertions check that diagnostics hide the synthetic secret sentinel. |
| G204 | `internal/daemon/daemon.go`, `defaultStarter` | The operator trusts the engine selected through local config/env, validated bundle metadata, legacy cache, or PATH. Separate argv elements go directly to `exec.CommandContext`; annotate G204 and verify literal arguments with a real child process. |

No security rule is disabled globally. Do not restrict explicit local overrides
to the cache or rewrite argv to silence a heuristic. Downloaded archives and
recorded bundle paths retain their separate containment validation and negative
traversal tests. Local config, environment, cache, and PATH must be controlled by
the operator choosing to run the engine.

To compare a newer analyzer, invoke its binary directly with a supported Go
toolchain (`golangci-lint version` records the build version). Keep that result
separate from the required `make lint` gate and review any new findings before
updating the pin.

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
