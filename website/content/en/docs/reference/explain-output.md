---
title: "--explain output"
linkTitle: "--explain output"
weight: 4
description: >
  Every field in the ajq explain v1 report.
---

`--explain` prints a stable plan report and exits without executing the query. It does not
contact a provider or local model.

## Pure-jq report

```text
ajq explain v1
query: ".users[] | select(.active) | .name"
execution: pure-jq deterministic
deterministic: yes
model_calls: 0
backend_calls: 0
byte_reproducible: yes
stdin: ignored
```

| Field | Meaning |
|---|---|
| `query` | The query as parsed. |
| `execution` | `pure-jq deterministic` — no semantic call sites. |
| `deterministic` | `yes`. |
| `model_calls` / `backend_calls` | `0`. |
| `byte_reproducible` | `yes`. |
| `stdin` | `ignored` — pure-jq explain does not read input. |

## Semantic report

```text
ajq explain v1
query: ".[] | select(sem_match(.msg; \"urgent\")) | .msg"
execution: semantic split plan
deterministic: no
model_calls: input-dependent
backend_calls: input-dependent
byte_reproducible: cache-dependent
stdin: harvested for estimates
planned_call_sites: 1
semantic_predicates: 1
semantic_values: 0
estimates:
  estimate_status: available
  static_call_sites: 1
  input_frames: 1
  harvested_judgements: 3
  post_dedup_judgements: 2
  mock_judge_batches: 1
  over_harvest_bound: post_dedup_judgements == mock distinct judgements; may be a safe superset of execute-needed judgements
subgraphs:
  deterministic: jq outside semantic call sites
  semantic: 1 planned call site(s)
semantic_plan:
  - call_id: 1
    op: "sem_match"
    kind: "predicate"
    value_expr: ".msg"
    specs: ["urgent"]
    source_range: 13:38
    gated: n/a
    execution: 3-phase
    subgraph: semantic
```

### Header fields

| Field | Meaning |
|---|---|
| `execution` | `semantic split plan` — the query has semantic call sites. |
| `deterministic` | `no`. |
| `model_calls` / `backend_calls` | `input-dependent`. |
| `byte_reproducible` | `cache-dependent` — deterministic phases are byte-reproducible; semantic answers depend on backend/cache state. |
| `stdin` | `harvested for estimates` when valid stdin is used for an estimate. |
| `planned_call_sites` | Total semantic call sites found in the static plan. |
| `semantic_predicates` / `semantic_values` | Counts of predicate-kind and value-kind ops. |

### `estimates` block

Present when valid stdin was supplied and the mock harvest path can estimate the query.

| Field | Unit / meaning |
|---|---|
| `estimate_status` | `available`, or `unavailable` with a stable reason. |
| `static_call_sites` | Semantic call sites in the static plan; not input cardinality. |
| `input_frames` | Number of input frames read for the estimate. |
| `harvested_judgements` | Pre-dedup semantic judgements collected by the mock harvest pass. |
| `post_dedup_judgements` | Distinct judgements after deduplication — the estimated backend-call count before considering persistent-cache hits in a later real run. |
| `mock_judge_batches` | `Backend.Judge` batch invocations in the deterministic mock path. |
| `over_harvest_bound` | Notes that post-dedup judgements may be a safe superset of execute-needed judgements; it never underestimates needed decisions. |

### `semantic_plan` entries

| Field | Meaning |
|---|---|
| `call_id` | Deterministic planner call-site ID. |
| `op` | The `sem_*` operator. |
| `kind` | `predicate` or `value`. |
| `value_expr` | The jq expression producing the judged value. |
| `specs` | Ordered literal spec arguments. |
| `source_range` | Byte offsets into the desugared query for the call site, or `unavailable`. |
| `gated` | For value ops: whether the result flows into a pruning gate; `n/a` for predicates. |
| `execution` | Planner-selected execution mode such as `3-phase` or `interleaved`. |
| `subgraph` | `semantic`. |

## When estimates are unavailable

If stdin is empty or invalid, or the query uses a semantic execution shape the current
executor cannot safely estimate/execute, the static plan is still printed and
`estimate_status` is marked unavailable with a reason. Unbounded value operators such as
`sem_extract`, `sem_score`, and `sem_redact` are examples of shapes with current execution
limits.

## Related

- [Estimate model calls before running](../../how-to/estimate-model-calls/) — using these numbers.
- [Control semantic costs](../../how-to/control-costs/) — cap and inspect real runs.
- [The three-phase executor](../../explanation/three-phase-executor/) — harvest, deduplication, and cache behavior.
