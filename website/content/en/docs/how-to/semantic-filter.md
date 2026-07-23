---
title: "Write a semantic filter"
linkTitle: "Semantic filter"
weight: 3
description: >
  A concise operator guide for fuzzy predicates and bounded semantic filters.
---

Use a semantic filter when literal matching with `test()` or `==` is too brittle. For a
complete problem-first walkthrough, including mock validation, real backends, estimates,
and cache behavior, see [Filter JSON by meaning](../filter-json-by-meaning/).

## Core forms

The `=~` operator asks the selected backend whether a value matches a description. Use it
inside `select`:

```jq
select(.feedback =~ "urgent")
```

It desugars to `sem_match(.feedback; "urgent")`. Negate it with `!~`:

```jq
select(.feedback !~ "urgent")
```

In raw-input mode, each line is `.` and the implicit-value form is convenient:

```bash
producer | ajq --backend mock -R -r 'select(. =~ "stack trace")'
```

For bounded routing, use `sem_classify` with the labels in the query:

```jq
{route: sem_classify(.text; "billing"; "bug"; "feature")}
```

The result is exactly one of those labels. The full classification recipe is in
[Classify JSON and NDJSON streams](../classify-json-streams/).

## Execution limits

`sem_match` and `sem_classify` are shipped across semantic execution contexts. The
unbounded value operators have narrower contracts: `sem_score` is supported as a
`sort_by(...)` key and in gated interleaved fallback, `sem_norm` as a `group_by(...)` key
and in gated fallback, while standalone `sem_extract` and `sem_redact` fail loudly in
three-phase execution. The [semantic functions reference](../../reference/semantic-functions/)
is the canonical availability table.

## Before a real run

Select a backend explicitly. Use `--backend mock` to validate query shape without a model
or network, then use `--explain` with representative input before a real run. Set a finite
`--max-calls` cap and use `--stats` when you need run accounting; see [Control semantic
costs](../control-costs/).

Successful judgements use the persistent cache by default. Use `--no-cache` when a
one-off or confidential workflow must bypass cache reads and writes; see [Manage the
judgement cache](../manage-the-cache/).

## Related

- [Filter JSON by meaning](../filter-json-by-meaning/) — complete fuzzy-filter workflow.
- [Classify JSON and NDJSON streams](../classify-json-streams/) — bounded labels.
- [Semantic functions reference](../../reference/semantic-functions/) — forms and availability.
- [Backends reference](../../reference/backends/) — backend requirements and defaults.
