---
title: "Control semantic costs"
linkTitle: "Control costs"
weight: 6
description: >
  Estimate semantic work, cap backend calls, and inspect run statistics.
---

Use this workflow before running semantic queries on a paid backend.

## 1. Estimate calls with `--explain`

Run the query with representative input and `--explain`:

```bash
printf '[{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --explain '.[] | select(.msg =~ "urgent") | .msg'
```

Read `post_dedup_judgements` in the estimate block:

```text
estimates:
  estimate_status: available
  static_call_sites: 1
  input_frames: 1
  harvested_judgements: 2
  post_dedup_judgements: 2
```

That number is the count to budget against. Repeated values are deduplicated before the
backend is called.

## 2. Add a hard cap with `--max-calls`

Set the cap at or below your budget. ajq aborts before issuing the call that would cross
it:

```bash
printf '[{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock --max-calls 1 '.[] | select(.msg =~ "urgent") | .msg'
```

With two distinct judgements and a cap of one, the run fails safely:

```text
ajq: error: query ".[] | select(.msg =~ \"urgent\") | .msg" runtime error in frame 1: max calls cap exceeded: cap 1, run needs 2 post-dedup backend judgements; aborting before issuing backend call 2.
```

Paid backends (`anthropic`, `openai`, and `openrouter`) default to `--max-calls 100`.
Use `--max-calls 0` only when you intentionally want no cap.

## 3. Inspect a successful run with `--stats`

After choosing a cap, run with `--stats` to print a summary to standard error:

```bash
printf '[{"msg":"urgent"},{"msg":"urgent"},{"msg":"other"}]' \
  | ajq --backend mock --stats '.[] | select(.msg =~ "urgent") | .msg'
```

The data still goes to standard output:

```json
"urgent"
"urgent"
```

The stats show harvested work, post-dedup backend calls, cache hits, and elapsed time:

```text
ajq stats:
  input_frames: 1
  semantic_call_sites: 1
  harvested_judgements: 3
  post_dedup_backend_calls: 2
  cache_hits: 0
  elapsed: ...
```

For paid models in ajq's pricing table, stats also include an estimated USD cost. Treat it
as a budgeting estimate, not a provider bill.

## Related

- [`--explain` output reference](../../reference/explain-output/) — every estimate field.
- [Backends reference](../../reference/backends/) — which backends have a paid default cap.
- [Manage the judgement cache](../manage-the-cache/) — reduce repeat backend calls.
