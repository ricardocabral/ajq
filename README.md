# ajq

`ajq` is `jq` for the AI era: a stream processor that keeps ordinary jq byte-deterministic and calls a language model only for explicit semantic operations such as fuzzy matching, classification, scoring, and normalization.

## Install

Use Homebrew, the release script for prebuilt archives, or Go source:

```bash
brew install --cask ricardocabral/tap/ajq
curl -fsSL https://raw.githubusercontent.com/ricardocabral/ajq/main/scripts/install.sh | sh
# manual download: https://github.com/ricardocabral/ajq/releases/latest
go install github.com/ricardocabral/ajq/cmd/ajq@latest
```

The Homebrew cask is published to the public `ricardocabral/tap` tap by the
release workflow.

## Status

| Area | What works today |
| --- | --- |
| Backends | Six semantic backends ship: `local`, `mock`, `ollama`, `openai`, `openrouter`, and Anthropic via `--cloud` / `--backend anthropic`. |
| Cost controls | `--explain` estimates model calls, `--max-calls` caps post-dedup judgements, and paid/cloud backends default to a 100-call guardrail. |
| Persistent cache | Semantic judgements are stored on disk under the ajq cache directory; `--no-cache` disables reads/writes for sensitive runs. |
| Local provisioning | `ajq provision` downloads or locates the llama.cpp engine and default GGUF model for `--backend local` on supported platforms. |
| Model management | `ajq models list|pull|use` manages checksum-pinned local GGUF catalog models. |
| Determinism contract | Pure jq paths stay byte-reproducible; semantic results are schema-constrained and cache-keyed by backend/model/spec/value. |

## Usage

```bash
# Help and version
ajq --help
ajq --version

# Pure jq over JSON stays deterministic
printf '{"users":[{"name":"Ada"}]}' | ajq -r '.users[].name'
# Ada

# Semantic filter with the deterministic mock backend (no model, network, or API key)
printf '[{"id":1,"msg":"please keep this"},{"id":2,"msg":"drop it"}]' \
  | ajq --backend mock -c '.[] | select(.msg =~ "keep") | .id'
# 1

# Inspect semantic plan and estimated backend calls before running
printf '[{"msg":"refund demanded"}]' \
  | ajq --backend mock --explain '.[] | select(.msg =~ "angry/frustrated") | .msg'
```

Run `ajq provision` once before using `--backend local`; then the same semantic queries can run against the managed local llama.cpp backend.

## Docs

Everything beyond the quick start lives on the website:

- Install details: <https://ricardocabral.github.io/ajq/docs/how-to/install/>
- First pipeline tutorial: <https://ricardocabral.github.io/ajq/docs/tutorials/first-pipeline/>
- Semantic functions reference: <https://ricardocabral.github.io/ajq/docs/reference/semantic-functions/>
- CLI reference: <https://ricardocabral.github.io/ajq/docs/reference/cli/>
- Split execution and determinism: <https://ricardocabral.github.io/ajq/docs/explanation/split-execution/> and <https://ricardocabral.github.io/ajq/docs/explanation/determinism/>

## Contributor verification

```bash
make test
make build
make website-build
```

`make bench-phase2` runs the CI-safe benchmark harness with the deterministic mock backend. Real local-inference benchmarks are opt-in and require provisioned assets; see the website docs and `internal/bench` package for details.

## License

MIT. See [LICENSE](LICENSE).
