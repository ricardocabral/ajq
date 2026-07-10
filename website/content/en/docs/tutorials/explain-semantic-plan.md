---
title: "See how ajq plans a semantic query"
linkTitle: "2. Plan a semantic query"
weight: 2
description: >
  Write your first fuzzy predicate and watch ajq split it into deterministic and
  semantic parts with --explain.
---

Write a fuzzy match and inspect the plan. `--explain` does not contact a model, so you can
inspect and cost any query before running it for real.

Requires ajq on your `PATH` (see [Install ajq](../../how-to/install/)).

## Step 1 — Write a fuzzy predicate

Ordinary jq can test text with `test()` or `==`, but only *literally*. ajq adds a fuzzy
operator, `=~`, that asks a model "does this value match this description?". Here's a
stream of three messages, and a query that keeps the ones that read as *urgent*:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --explain '.[] | select(.msg =~ "urgent") | .msg'
```

`--explain` prints the **plan** instead of running the query.

## Step 2 — Read the plan

You'll see output like this:

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

Important lines:

### The query was rewritten

```text
query: ".[] | select(sem_match(.msg; \"urgent\")) | .msg"
```

You wrote `.msg =~ "urgent"`, but the plan shows `sem_match(.msg; "urgent")`. The `=~`
operator is **surface sugar**: ajq desugars it into a plain jq function call before
planning. Everything semantic is ultimately a `sem_*` function.

### ajq found the semantic node

```text
semantic_plan:
  - call_id: 1
    op: "sem_match"
    kind: "predicate"
    value_expr: ".msg"
    specs: ["urgent"]
```

There is exactly **one** semantic call site — the `sem_match` on `.msg`. Everything else
in your query (`.[]`, `select`, `.msg` projection) is deterministic jq. ajq has split the
pipeline into a deterministic majority and a single fuzzy decision.

### Deduplication is already visible

```text
harvested_judgements: 3
post_dedup_judgements: 2
```

Your input had three items, but two of them have the same `.msg` value (`"urgent"`). ajq
would collect **3** values, deduplicate them to **2** distinct ones, and only ever ask
the model about those 2. Identical values cost **one** decision — this is how ajq keeps
cost tied to the number of *distinct fuzzy decisions*, not the size of your input.

## Step 3 — Change the input and watch the estimate move

Add another distinct message and re-run:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"},{"msg":"ping the on-call"}]' \
  | ajq --explain '.[] | select(.msg =~ "urgent") | .msg'
```

Now `harvested_judgements` becomes `4` and `post_dedup_judgements` becomes `3`: one new
distinct value to judge.

## Result

- `=~` is fuzzy matching; ajq desugars it to `sem_match(...)`.
- `--explain` shows the plan without contacting a model — safe to run anytime.
- ajq splits every query into a deterministic part and semantic call sites.
- Deduplication means identical values are judged once.

## Running it for real

Drop the `--explain` flag and select the deterministic mock backend so the tutorial stays
network-free:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock '.[] | select(.msg =~ "urgent") | .msg'
```

Output:

```json
"urgent"
"urgent"
```

Use `--backend local` or a cloud backend after following the matching how-to guide.
`--explain` stays the way to inspect and budget a query before you spend real model calls.

## Next

- Solve a concrete fuzzy search in [Filter JSON by meaning](../../how-to/filter-json-by-meaning/).
- Learn the full fuzzy vocabulary in the [semantic functions reference](../../reference/semantic-functions/).
- Understand the machinery in [The three-phase executor](../../explanation/three-phase-executor/).
- See how to read every field in the [`--explain` output reference](../../reference/explain-output/).
