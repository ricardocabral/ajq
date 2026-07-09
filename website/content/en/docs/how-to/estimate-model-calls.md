---
title: "Estimate model calls before running"
linkTitle: "Estimate model calls"
weight: 4
description: >
  Use --explain to budget cost and latency before executing a semantic query.
---

Semantic queries cost one backend judgement per *distinct* value they judge. Use
`--explain` with representative input to estimate that number before you run a paid or
slow backend.

## Run `--explain` with real input

Pipe a sample of your data into `--explain`. ajq runs a deterministic mock harvest over
the input to estimate work — it never contacts a provider or local model:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --explain '.[] | select(.msg =~ "urgent") | .msg'
```

## Read the estimate block

```text
estimates:
  estimate_status: available
  static_call_sites: 1
  input_frames: 1
  harvested_judgements: 3
  post_dedup_judgements: 2
  mock_judge_batches: 1
```

The fields you'll budget against:

| Field | Meaning |
|---|---|
| `harvested_judgements` | Total values collected before deduplication. |
| `post_dedup_judgements` | Distinct semantic judgements after deduplication — the estimated backend-call count. |
| `mock_judge_batches` | How many backend batch calls the mock estimate path used. |
| `static_call_sites` | Number of semantic call sites in the query, not input size. |

`post_dedup_judgements` is the number to compare with `--max-calls`. Paid backends default
to a 100-call cap; local, Ollama, and mock default to unlimited.

## Turn the estimate into a cap

If the estimate is acceptable, set a cap when you run the real backend:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock --max-calls 2 '.[] | select(.msg =~ "urgent") | .msg'
```

For the full estimate → cap → stats workflow, see
[Control semantic costs](../control-costs/).

## Estimate for your whole dataset

The estimate is based on the input you pass to `--explain`. To project a full run, either
pipe in the full dataset or sample representative records and extrapolate from your
distinct-value ratio. Because ajq deduplicates by value, repeated values cost less than
new values.

## Remember the persistent cache

`--explain` estimates the semantic work implied by the input and query. A real run may
issue fewer backend calls if matching judgements are already in the persistent cache. Use
`--stats` on the real run to see `cache_hits` and `post_dedup_backend_calls`.

## When estimates are unavailable

If the input is empty or invalid, or the query selects a currently unsupported semantic
execution shape, `--explain` keeps the static plan and marks `estimate_status` unavailable
with a stable reason. The plan (call sites, ops, specs) is still shown.

## Related

- [`--explain` output reference](../../reference/explain-output/) — every field, in full.
- [Control semantic costs](../control-costs/) — enforce a cap and inspect stats.
- [The three-phase executor](../../explanation/three-phase-executor/) — why deduplication works.
