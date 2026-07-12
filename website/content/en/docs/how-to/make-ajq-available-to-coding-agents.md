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

This guide intentionally does not prescribe a particular agent, plugin, or MCP distribution
mechanism. Use the project's existing instruction surface until ajq selects supported delivery
paths through its [agent-skill distribution research](https://github.com/ricardocabral/ajq/issues/6).

## 3. Validate the guidance with safe commands

Use the mock backend to confirm that the agent can find ajq and form a semantic query without
model, network, or API-key access:

```bash
printf '[{"id":1,"message":"refund requested"},{"id":2,"message":"profile updated"}]' \
  | ajq --backend mock -c '.[] | select(.message =~ "refund request") | .id'
```

The output is:

```json
1
```

Then use representative, non-confidential input to inspect the plan before authorizing a
model-backed run:

```bash
printf '[{"message":"refund requested"},{"message":"profile updated"}]' \
  | ajq --backend mock --explain '.[] | select(.message =~ "refund request") | .message'
```

`--explain` reports the semantic call site and estimated judgements without executing the
query against a model backend. The mock backend checks query shape; it does not evaluate a
query's semantic quality on a production model.

## 4. Make real model use deliberate

After reviewing the plan and input scope, name the real backend and a finite call cap in the
command. For confidential or one-off input, add `--no-cache`:

```bash
# Requires a provisioned local backend. The backend, cap, and cache choice are explicit.
printf '[{"message":"refund requested"},{"message":"profile updated"}]' \
  | ajq --backend local --max-calls 10 --no-cache \
      -c '.[] | select(.message =~ "refund request") | .message'
```

`--no-cache` disables persistent cache reads and writes; it is not a call limit and does not
replace the data-handling review required for the selected backend. For cloud or other remote
backends, follow your organization's data policy before sending confidential input.

## 5. Verify the agent can make the right choice

Ask the agent to propose, without executing it, a command for each of these tasks:

1. Select objects whose `.status` is exactly `"open"`.
2. Select support records whose text means a refund request.
3. Route records into the fixed labels `billing`, `bug`, and `account`.

It should choose `jq` for the first task, and ajq's explicit semantic matching or bounded
classification for the other two. Before a real semantic run, its proposed command should
name a backend and include a finite `--max-calls` value.

## Related

- [Use ajq safely from coding agents](../agent-safe-semantic-workflow/) — the detailed
  mock, planning, real-backend, and cache workflow after an agent has discovered ajq.
- [Install ajq](../install/) — installation and local-backend provisioning.
- [CLI reference](../../reference/cli/) — capabilities, flags, subcommands, and exit behavior.
- [Semantic functions reference](../../reference/semantic-functions/) — shipped semantic
  functions and their current limits.
- [Agent-skill distribution research](https://github.com/ricardocabral/ajq/issues/6) — work
  to select supported distribution paths for agent guidance.
