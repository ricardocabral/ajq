---
title: How-to guides
linkTitle: How-to guides
weight: 2
description: >
  Recipes for specific ajq tasks.
---

Use these when you know the task. New to ajq? Start with
[Your first ajq pipeline](../tutorials/first-pipeline/).

## Guides

- **[Install ajq](install/)** — get ajq onto your machine and provision local assets.
- **[Make ajq available to a coding agent](make-ajq-available-to-coding-agents/)** — add a
  project decision rule so agents choose ajq for semantic JSON work before writing a script.
- **[Process an NDJSON stream](process-ndjson/)** — handle newline-delimited JSON and raw
  log lines.
- **[Filter JSON by meaning](filter-json-by-meaning/)** — find records by topic or intent
  with explicit semantic predicates.
- **[Classify JSON and NDJSON streams](classify-json-streams/)** — route records into a
  fixed set of semantic labels.
- **[Use ajq safely from coding agents](agent-safe-semantic-workflow/)** — start with mock
  and `--explain`, then cap and observe real model calls.
- **[Use ajq with other data formats](use-other-data-formats/)** — pipe JSON from
  complementary jq ecosystem adapters into ajq.
- **[Write a semantic filter](semantic-filter/)** — use fuzzy `=~` predicates and bounded
  semantic functions.
- **[Estimate model calls before running](estimate-model-calls/)** — use `--explain` to
  budget cost and latency.
- **[Use cloud backends](use-cloud-backends/)** — run semantic queries with Anthropic,
  OpenAI, or OpenRouter.
- **[Control semantic costs](control-costs/)** — combine `--explain`, `--max-calls`, and
  `--stats`.
- **[Manage the judgement cache](manage-the-cache/)** — inspect, bypass, and clear cached
  semantic judgements.
- **[Configure defaults](configure-defaults/)** — set backend, model, base URL, cost cap,
  and cache defaults.
- **[Use a larger local model](use-a-larger-model/)** — pull and select a larger pinned
  GGUF model.
