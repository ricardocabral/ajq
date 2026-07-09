---
title: "Write a semantic filter"
linkTitle: "Semantic filter"
weight: 3
description: >
  Use fuzzy =~ predicates and bounded semantic functions in jq pipelines.
---

Use a semantic filter when literal matching with `test()` or `==` is too brittle. The
examples below use `--backend mock` so you can verify the command shape without a model or
API key; switch to `--backend local`, `--cloud`, or another backend for real judgement.

## Fuzzy match with `=~`

The `=~` operator asks the backend whether a value matches a description. Use it inside
`select`:

```bash
printf '[{"id":1,"feedback":"urgent"},{"id":2,"feedback":"other"}]' \
  | ajq --backend mock -c '.[] | select(.feedback =~ "urgent") | .id'
```

Output with the mock backend:

```text
1
```

`.feedback =~ "urgent"` desugars to `sem_match(.feedback; "urgent")`. Negate with `!~`:

```bash
printf '[{"id":1,"feedback":"urgent"},{"id":2,"feedback":"other"}]' \
  | ajq --backend mock -c '.[] | select(.feedback !~ "urgent") | .id'
```

## Match raw log lines

In raw-input mode, each input line is `.`:

```bash
printf 'panic stack trace\nok\n' \
  | ajq --backend mock -R -r 'select(. =~ "stack trace")'
```

Output:

```text
panic stack trace
```

## Classify into fixed labels

`sem_classify(value; "a"; "b"; …)` returns exactly one of the labels you provide:

```bash
printf '{"id":1,"text":"billing question"}' \
  | ajq --backend mock -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Output with the mock backend:

```json
{"id":1,"route":"billing"}
```

Use a fixed label set when downstream jq should route records deterministically by shape.

## Stay with shipped execution shapes

The current executor fully supports predicate matching (`=~` / `sem_match`) and bounded
classification (`sem_classify`). Unbounded value operators such as `sem_extract`,
`sem_score`, and `sem_redact` are visible in the planner but still have execution limits;
avoid them in production recipes until their fallback support ships.

## Check the plan first

Before running a semantic query on a paid or local model backend, inspect the estimate:

```bash
printf '[{"feedback":"urgent"},{"feedback":"urgent"},{"feedback":"other"}]' \
  | ajq --explain '.[] | select(.feedback =~ "urgent") | .feedback'
```

Then use [Control semantic costs](../control-costs/) to set a cap and inspect stats.

## Account for the persistent cache

Successful semantic judgements are cached by op/spec/model/value. A second run over the
same values may skip backend calls and report cache hits. Use [Manage the judgement
cache](../manage-the-cache/) when you need to inspect, bypass, or clear those entries.

## Related

- [Semantic functions reference](../../reference/semantic-functions/) — shipped function forms and current limitations.
- [Backends reference](../../reference/backends/) — choose a real backend.
