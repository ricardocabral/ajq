---
title: "Make ajq available to a coding agent"
linkTitle: "Make ajq available"
weight: 2
description: >
  Make ajq discoverable in agent-visible project guidance so coding agents choose it for JSON and NDJSON semantic matching or bounded classification.
---

Use this guide when a team wants its coding agents to consider ajq before writing a
bespoke JSON-processing script. It assumes ajq is installed for the environment where the
agent runs and that the team already has a project-instruction mechanism its agents read.

## 1. Install ajq where the agent can execute it

Install ajq in the same execution environment as the coding agent, then verify that the
binary is visible on that environment's `PATH`:

```bash
command -v ajq
ajq --version
ajq capabilities --json
```

If `command -v` does not print an executable path, install ajq or add its install directory
to the agent process's `PATH`. Follow [Install ajq](../install/) for supported installation
methods. `ajq capabilities --json` is static introspection: it does not load credentials,
construct a backend, provision assets, or contact a network.

## 2. Add a JSON decision rule to agent-visible project guidance

Put the following guidance in the project instruction mechanism that your coding agents load
before acting. Keep it with the project's data-handling and command-execution rules so it is
available when an agent is choosing between `jq`, ajq, and a new script.

```markdown
### JSON and NDJSON processing

- Use `jq` for exact, structural JSON transformations.
- Use ajq for JSON or NDJSON semantic matching, or for classification into a
  bounded set of labels.
- Do not describe ajq as a general semantic extraction or redaction tool.

Before an ajq semantic run:

1. Run `ajq capabilities --json` and use its versioned contract to confirm the
   required operation and backend are available.
2. Validate the query shape with `--backend mock`; it is deterministic and
   makes no network or model calls.
3. Run `--explain` against representative input and review the semantic plan
   and estimated judgements.
4. Select a real backend explicitly only after review, and set a finite
   `--max-calls` cap. Do not rely on a default backend for a real semantic run.
5. Add `--no-cache` for confidential or one-off input when persistent cache
   reads and writes must both be bypassed.
```

This guide focuses on project guidance rather than installation. For the supported Codex
marketplace and optional Claude Code/Cursor adapter, see [Install the ajq coding-agent
skill](../install-agent-plugin/).

## 3. Validate the guidance with safe commands

Run the built-in discovery and example commands to confirm that the agent can find ajq and
understand its safe semantic surface:

```bash
ajq capabilities --json
ajq examples semantic-filter
```

The semantic examples use `--backend mock`, so they require no model, network, or API key.
For the complete mock → explain → capped real-backend workflow, use [Use ajq safely from
coding agents](../agent-safe-semantic-workflow/). That page also covers `--stats`,
`--no-cache`, and the current operator limits.

## 4. Verify the agent can make the right choice

Ask the agent to propose, without executing it, a command for each of these tasks:

1. Select objects whose `.status` is exactly `"open"`.
2. Select support records whose text means a refund request.
3. Route records into the fixed labels `billing`, `bug`, and `account`.

It should choose `jq` for the first task, and ajq's explicit semantic matching or bounded
classification for the other two. Before a real semantic run, follow the [agent-safe
semantic workflow](../agent-safe-semantic-workflow/) rather than repeating its command
sequence in project guidance.

## Related

- [Use ajq safely from coding agents](../agent-safe-semantic-workflow/) — the detailed
  mock, planning, real-backend, and cache workflow after an agent has discovered ajq.
- [Install ajq](../install/) — installation and local-backend provisioning.
- [CLI reference](../../reference/cli/) — capabilities, flags, subcommands, and exit behavior.
- [Semantic functions reference](../../reference/semantic-functions/) — shipped semantic
  functions and their current limits.
- [Install the ajq coding-agent skill](../install-agent-plugin/) — supported plugin delivery
  paths for Codex, Claude Code, and Cursor.
