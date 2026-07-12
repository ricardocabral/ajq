---
title: "Install the ajq coding-agent skill"
linkTitle: "Install the coding-agent skill"
weight: 6
description: >
  Install the ajq skill through the native Codex marketplace or the optional Claude Code and Cursor adapter.
---

Install the ajq skill when an agent should route semantic JSON or NDJSON
filtering and bounded classification work to ajq. Install `ajq` itself first so
the skill can run its safe discovery and mock checks.

## Install for Codex

Add the public ajq marketplace and install its plugin:

```bash
codex plugin marketplace add ricardocabral/ajq
codex plugin add ajq@ajq
```

For a reproducible environment, pin the marketplace to an ajq release tag that
contains this plugin. Replace `vX.Y.Z` with that release tag:

```bash
codex plugin marketplace add ricardocabral/ajq --ref vX.Y.Z
codex plugin add ajq@ajq
```

Start a new Codex task after installation. Confirm that Codex can see the
plugin before relying on it:

```bash
codex plugin list
```

## Share it in a local workspace

When a team has a checkout of this repository, point Codex at that checkout
instead of a remote ref:

```bash
codex plugin marketplace add /path/to/ajq
codex plugin add ajq@ajq
```

The marketplace artifact lives in `.agents/plugins/marketplace.json` and the
plugin itself in `plugins/ajq`. Keep both paths together when sharing a branch
or archive.

## Provision it in CI

Use a CI-specific `CODEX_HOME`, add the marketplace, and install the plugin
before starting Codex. Pin a release tag in reproducible jobs:

```bash
export CODEX_HOME="$RUNNER_TEMP/codex-home"
codex plugin marketplace add ricardocabral/ajq --ref vX.Y.Z
codex plugin add ajq@ajq
codex plugin list
```

Installing the skill does not select a semantic backend or grant access to a
model. The skill begins with `ajq capabilities --json`, `--backend mock`, and
`--explain`; any real backend still needs an explicit choice and finite
`--max-calls` value.

## Optional adapter for Claude Code and Cursor

The third-party `plugins` installer can discover the same package from this
repository:

```bash
npx plugins add ricardocabral/ajq
```

Its current targets are Claude Code and Cursor. It is not the native Codex
installation path; use the Codex marketplace commands above for Codex. Preview
what the adapter finds without installing it:

```bash
npx plugins discover ricardocabral/ajq
```

After installing through either path, use the skill's workflow: capabilities,
mock, explain, then an explicitly capped real backend. See the
[agent-safe semantic workflow](../agent-safe-semantic-workflow/) for commands
and current operator limits.
