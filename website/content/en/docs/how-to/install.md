---
title: "Install ajq"
linkTitle: "Install ajq"
weight: 1
description: >
  Install ajq and provision the local backend assets.
---

ajq is a single Go binary. Pure jq queries work immediately after the binary is on your
`PATH`. Semantic queries need an explicitly selected backend such as `mock`, `local`,
`ollama`, `openai`, `openrouter`, or `--cloud`.

## Install with the script

The install script downloads the matching release archive for your OS/architecture,
verifies it against `checksums.txt`, and installs without sudo into `~/.local/bin` (or
`$AJQ_INSTALL_DIR`).

```bash
curl -fsSL https://raw.githubusercontent.com/ricardocabral/ajq/main/scripts/install.sh | sh
```

Pin a version or install directory if needed:

```bash
AJQ_VERSION=v0.1.0 AJQ_INSTALL_DIR="$HOME/bin" sh scripts/install.sh
```

## Install from a release archive

Download the archive for your platform from
[GitHub Releases](https://github.com/ricardocabral/ajq/releases), verify its SHA-256
digest against `checksums.txt`, then copy the `ajq` binary somewhere on your `PATH`.

Release archives are named like:

- `ajq_<version>_Darwin_arm64.tar.gz`
- `ajq_<version>_Linux_x86_64.tar.gz`
- `ajq_<version>_Windows_x86_64.zip`

## Verify a download

Checksum verification and provenance verification are separate checks. The install
script verifies the downloaded archive against `checksums.txt`; it does not run
GitHub attestation verification for you.

For release assets published after provenance attestations are enabled, install
the GitHub CLI and verify a downloaded archive or `checksums.txt` with:

```bash
gh attestation verify <downloaded-archive-or-checksums.txt> --repo ricardocabral/ajq
```

For manual downloads, also compare the archive SHA-256 digest with the matching
entry in `checksums.txt`.

## Build from source

With the Go toolchain ([Go](https://go.dev/dl/) 1.26 or newer):

```bash
go install github.com/ricardocabral/ajq/cmd/ajq@latest
```

Or from a checkout:

```bash
git clone https://github.com/ricardocabral/ajq.git
cd ajq
go build -o ajq ./cmd/ajq
install -m 0755 ajq /usr/local/bin/ajq   # or ~/bin, etc.
```

## Verify the binary

Run:

```bash
ajq --version
ajq --help
```

`ajq --help` should list the semantic backends and the shipped `cache`, `daemon`,
`models`, and `provision` subcommands.

## Provision local semantic assets

Skip this section if you only use pure jq queries, `--backend mock`, Ollama, or cloud
backends.

`--backend local` needs a `llama-server` engine and an installed GGUF model under the ajq
cache. Provision them explicitly:

```bash
ajq provision
ajq provision --check
```

`ajq provision` auto-installs checksum-pinned engine bundles on supported platforms
(`darwin/arm64`, `linux/amd64`, and `linux/arm64`) and downloads the checksum-pinned
default model. It also reuses assets you already have, including explicit overrides,
cache entries, a legacy `<cache>/bin/llama-server`, or a `llama-server` on `PATH`.

### Agent readiness probe

Use the status-only JSON probe when an agent needs to decide whether it can use
the managed local backend. A missing asset deliberately exits 1 **after** writing
its complete JSON result, so retain stdout instead of using a shell pipeline that
discards it:

```bash
set +e
ajq provision --check --json > ajq-readiness.json
status=$?
set -e
cat ajq-readiness.json
# status=0 means ready; status=1 means inspect .actions and provision explicitly.
```

The document includes `ready`, engine/model presence and local paths, and ordered
`actions`. It only inspects configuration, filesystem, and PATH state: it does
not download assets, start a daemon, or contact a backend. `--json` is valid only
with `--check`; use `ajq provision` separately to install the requested assets.

## Homebrew status

The release pipeline publishes a Homebrew cask to the public
[`ricardocabral/tap`](https://github.com/ricardocabral/homebrew-tap) tap.
After the first release is published, install ajq with:

```bash
brew install --cask ricardocabral/tap/ajq
```

The install script, manual release archives, and source builds remain available for
systems where Homebrew is not the preferred installer.

## Next steps

- [Use cloud backends](../use-cloud-backends/) for Anthropic, OpenAI, and OpenRouter.
- [Use a larger local model](../use-a-larger-model/) after local provisioning.
- [Configure defaults](../configure-defaults/) if you do not want to pass backend flags on every command.
