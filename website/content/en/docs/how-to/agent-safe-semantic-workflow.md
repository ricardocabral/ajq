---
title: "Use ajq safely from coding agents"
linkTitle: "Agent-safe workflow"
weight: 5
description: >
  A safe workflow for agents that need semantic jq queries: start with mock and --explain, cap backend calls, and avoid unsupported extraction claims.
---

Coding agents often discover ajq while trying to answer a concrete data-cleaning
question: "find the records that mean X" or "route these JSON lines into labels". Use
this workflow when an agent, script, or CI job needs semantic matching while keeping the
jq parts deterministic and reviewable.

## 1. Keep ordinary jq deterministic

First write the structural jq pipeline without semantic calls. This step should not need a
backend:

```bash
printf '[{"id":1,"msg":"refund requested"},{"id":2,"msg":"profile updated"}]' \
  | ajq -c '.[] | {id, msg}'
```

Only add semantic operators once the field selection and output shape are correct.

## 2. Add semantics with the mock backend

Use `--backend mock` for the first semantic run. It is deterministic, in-process, and does
not use network access, cloud credentials, or local model assets:

```bash
printf '[{"id":1,"msg":"refund requested"},{"id":2,"msg":"profile updated"}]' \
  | ajq --backend mock -c '.[] | select(.msg =~ "refund request") | {id, msg}'
```

Expected output with the mock backend:

```json
{"id":1,"msg":"refund requested"}
```

For routing tasks, keep outputs bounded with `sem_classify`:

```bash
printf '{"id":1,"msg":"billing question"}\n{"id":2,"msg":"bug report"}\n' \
  | ajq --backend mock -c '{id, route: sem_classify(.msg; "billing"; "bug"; "account")}'
```

## 3. Explain before executing with a model

Before a real backend run, inspect the plan and estimated judgement count:

```bash
printf '[{"msg":"refund requested"},{"msg":"refund requested"},{"msg":"profile updated"}]' \
  | ajq --explain '.[] | select(.msg =~ "refund request") | .msg'
```

Review the semantic call sites, specs, and `post_dedup_judgements`. The estimate path uses
the mock backend and does not contact a provider.

## 4. Cap and observe the real run

When the estimate is acceptable, switch only the backend-related flags:

```bash
# Managed local backend after `ajq provision`
printf '[{"msg":"refund requested"},{"msg":"profile updated"}]' \
  | ajq --backend local --max-calls 10 --stats \
      -c '.[] | select(.msg =~ "refund request") | .msg'

# Anthropic cloud backend, using ANTHROPIC_API_KEY from the environment
printf '[{"msg":"refund requested"},{"msg":"profile updated"}]' \
  | ajq --cloud --max-calls 10 --stats \
      -c '.[] | select(.msg =~ "refund request") | .msg'
```

`--max-calls` is the hard budget. `--stats` reports harvested judgements, post-dedup
backend calls, cache hits, and elapsed time on stderr after a successful run. Paid/cloud
backends default to a 100-call cap, but agents should still set an explicit cap for the
current task.

## 5. Reuse or bypass the cache deliberately

Successful semantic judgements are cached by backend, model, operation, spec, and
canonical value. This improves repeated agent runs over the same data, but sensitive or
one-off workflows may prefer `--no-cache` to avoid cache reads and writes:

```bash
printf '[{"msg":"refund requested"}]' \
  | ajq --backend mock --no-cache -c '.[] | select(.msg =~ "refund request") | .msg'
```

Use cache commands when an agent needs to inspect or clear local state:

```bash
ajq cache status
ajq cache clear
```

## Guardrails for generated docs and prompts

- Do say ajq is useful for fuzzy JSON filtering, semantic grep over JSON/NDJSON, and
  bounded classification.
- Do not claim standalone semantic extraction or redaction is supported today.
- Do not hide model calls behind pure jq examples; semantic work should always be visible
  in the query and backend flags.
- Do not put API keys in commands, logs, or checked-in files. Paid backend credentials come
  from environment variables.

## Related

- [Filter JSON by meaning](../filter-json-by-meaning/) — a focused semantic filter recipe.
- [Classify JSON and NDJSON streams](../classify-json-streams/) — bounded labels for streaming records.
- [`--explain` output reference](../../reference/explain-output/) — plan and estimate fields.
- [Configuration reference](../../reference/configuration/) — flags, environment variables, config files, and credential policy.
- [Semantic functions reference](../../reference/semantic-functions/) — shipped semantic vocabulary and current limits.
