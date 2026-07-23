---
title: "Control semantic costs"
linkTitle: "Control costs"
weight: 6
description: >
  Estimate semantic work, cap backend calls, and inspect run statistics.
---

Use this workflow before running semantic queries on a paid backend.

## 1. Estimate calls with `--explain`

Start with [Estimate model calls before running](../estimate-model-calls/) for the
representative-input command and the meaning of `post_dedup_judgements`. Budget against
that field: repeated judgements are deduplicated before backend calls, while a later real
run may have additional persistent-cache hits.

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

See the [backends reference](../../reference/backends/#paid-backend-defaults) for default
caps. Use `--max-calls 0` only when you intentionally want no cap. `--backend-concurrency`
changes only how many requests from an already-approved batch can be in flight; it does
not change this count or reserve extra calls. Keep paid providers at the sequential default
of `1` while establishing a rate-limit budget, then use at most `2` only if the provider's
account limits and retry capacity allow it.

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

The stats show harvested work, post-dedup backend calls, cache hits, and elapsed time. The
full field contract is in the [CLI reference](../../reference/cli/); this page focuses on
the budgeting workflow:

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
