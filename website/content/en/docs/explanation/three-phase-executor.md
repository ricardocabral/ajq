---
title: "The three-phase executor"
linkTitle: "Three-phase executor"
weight: 3
description: >
  Harvest, resolve, execute — and why deduplication comes for free.
---

ajq runs supported semantic queries in three phases:

## The three phases

```text
Phase 1  HARVEST   pure gojq run; semantic ops in COLLECT mode gather every
                   (op, spec, value) they observe. Permissive returns.
Phase 2  RESOLVE   dedup collected values, consult/write the judgement cache,
                   and resolve misses through the selected backend.
Phase 3  EXECUTE   pure gojq run; semantic ops in LOOKUP mode return resolved
                   values from cache. Deterministic given those cached values.
```

Phases 1 and 3 are pure gojq. Phase 2 is the isolated semantic boundary: it deduplicates,
checks cache identity, and calls the backend only for misses.

## Why two deterministic passes

Harvesting first, then executing, buys three things:

- **Deduplication.** All values a query will judge are known before backend calls, so
  identical judgements collapse to one decision.
- **Backend-level scheduling.** Resolve hands the backend the distinct judgement set. The
  local daemon can process bounded-parallel requests while preserving result ordering.
- **Reproducibility of structure.** Execute is a pure function of input plus resolved
  semantic judgements, so jq output shaping remains stable.

The cost is running gojq twice, which is negligible compared with model latency.

## Cache identity

A semantic judgement is keyed by `(op, spec, model-id, canonical-value)`. The canonical
value is a deterministic encoding that preserves scalar types, sorts object keys, keeps
array order, and normalizes JSON numbers. The model id is part of the key, so switching
models is a clean miss.

The cache has an in-memory front and, unless disabled, a persistent disk backing under
`<cache>/judgements/`. That persistent backing is why a second process can reuse a
successful judgement from an earlier run.

## Over-harvest: correct, sometimes wasteful

In harvest, a predicate cannot yet know the model's answer, so it returns permissively so
that downstream operators see a superset of possible inputs. This is correct because it
never misses a value that should be judged, but it may collect a value that execute later
prunes. Deduplication and the persistent cache reduce the cost of repeated over-harvested
values.

## Gated value operators

A value-producing operator whose result feeds a pruning gate needs special care. Bounded
enums such as `sem_classify` can harvest all possible labels, so downstream survivors are
a safe superset. Unbounded values such as extraction, free-form redaction, and scoring do
not have a finite safe set; the current executor reports unsupported shapes instead of
silently producing wrong output.

An execute-phase cache miss is a loud error. That backstop turns planner or cache gaps into
failures rather than silent data loss.

## Guarding the planner

The executor relies on the static planner finding every semantic call site. ajq asserts
`fired ⊆ planned` at runtime: every semantic operator that fires must appear in the static
plan. A violation aborts before spending money and names the missed operation.

## Related

- [Split execution](../split-execution/) — the idea this engine implements.
- [The determinism contract](../determinism/) — what reproducibility means here.
- [`--explain` output](../../reference/explain-output/) — how harvest/dedup estimates are reported.
