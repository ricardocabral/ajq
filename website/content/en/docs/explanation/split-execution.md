---
title: "Split execution"
linkTitle: "Split execution"
weight: 1
description: >
  The core idea behind ajq — keep jq deterministic, call a model only for fuzzy operators.
---

## The observation

A real-world "AI data" query is mostly *not* fuzzy. Consider triaging support tickets:

```text
cat data.json | ajq --backend local '.users[] | select(.feedback =~ "angry/frustrated") | .id'
                      └────────┬───────┘  └────────────┬────────────┘  └┬┘
                      deterministic path       semantic predicate       proj
                      (pure gojq)              (LLM, per value)          (pure gojq)
```

Iterating the array, projecting `.id`, shaping the output — all of that is exact,
mechanical work that `jq` already does perfectly. Only one operator, `=~`, needs
judgement. The insight behind ajq is to treat that asymmetry as a first-class execution
strategy: **run the deterministic majority through a real jq engine, and call a model only
for the fuzzy operators, on the smallest possible slices of data.**

## Why it matters

Splitting execution this way has three consequences that a "send each row to an LLM"
script cannot match:

1. **Tiny context per decision.** The model sees one field value at a time, not the whole
   record and not your prompt scaffolding. A small (~1.5B-parameter) local model is enough
   to decide "does this text read as angry?". That's what makes an explicitly selected,
   provisionable local backend viable without an API key.

2. **Cost scales with fuzzy decisions, not input size.** You pay for the number of
   distinct judgements, not the number of bytes or rows. A 10,000-row file with lots of
   repeated values costs far fewer than 10,000 decisions (see
   [The three-phase executor](../three-phase-executor/)).

3. **The deterministic majority is byte-reproducible.** Because everything outside the
   fuzzy operators runs through pure jq, most of your pipeline produces identical bytes on
   every run. That reproducibility is the actual unique selling point — see
   [The determinism contract](../determinism/).

## Keeping jq as the real parser

ajq does **not** fork the jq grammar. Semantic operators are registered as ordinary jq
functions through gojq's `WithFunction` seam, so gojq parses and drives them natively:

```go
gojq.WithFunction("sem_match", 1, 2, func(input any, args []any) any { … })
```

The only surface syntax ajq adds is a thin, jq-aware desugaring of the infix `=~` / `!~`
operators into `sem_match(...)` calls. The jq parser can compose `sem_*` inside `select`,
`sort_by`, `group_by`, `|=`, arithmetic, and `reduce`, but execution support is deliberately
shape-constrained. See the [semantic functions reference](../../reference/semantic-functions/)
for the currently supported contexts and loud failures.

This was a deliberate, empirically-tested decision. gojq's existing grammar already
expresses every real scenario, so a custom grammar would add no expressiveness while
risking divergence from jq semantics. Richer surface forms like `@classify(...)` don't
parse in gojq and are deferred rather than forced.

## What the model is (and isn't) asked to do

The model is never asked to produce your output structure. It answers a single, narrow,
typed question — a boolean, one of a fixed set of labels, a number, or a short string —
and that answer is fenced by a grammar so it *cannot* be malformed. jq does all the
structural work around it. This division is what lets ajq promise structural correctness
regardless of how small the model is.

## Related

- [The three-phase executor](../three-phase-executor/) — how the split is actually run.
- [The determinism contract](../determinism/) — what reproducibility means here.
- [Semantic functions reference](../../reference/semantic-functions/) — the operator
  vocabulary.
