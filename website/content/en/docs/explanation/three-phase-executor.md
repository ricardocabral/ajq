---
title: "The three-phase executor"
linkTitle: "Three-phase executor"
weight: 3
description: >
  Harvest, resolve, execute — and why deduplication comes for free.
---

ajq runs supported semantic queries in three phases. For NDJSON and raw streams, the
unit of work is a bounded window of complete frames rather than the complete stream.

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

## Complete-frame windows

Supported three-phase streams are grouped by source bytes into windows before phase 1.
The default is 262144 bytes (256 KiB), configurable by `--window-bytes`,
`AJQ_WINDOW_BYTES`, or TOML `window_bytes` with flag > environment > file > default
precedence. Each window harvests all of its frames, resolves its deduplicated cache misses
once, then executes and emits frames in source order. Persistent cache entries can be
reused by later windows.

The window boundary never splits a JSON value or raw line. The executor retains the
current window, one complete-frame lookahead, and bounded framing buffers, rather than the
whole input. A frame larger than the budget is processed as one valid oversized window;
the configured budget therefore does not cap the memory needed for an individual record.

A larger window can eliminate more nearby duplicate judgements and reduce backend batches,
but it waits to harvest and resolve all frames in that window before emitting its first
result. Choose a smaller positive budget when first-record latency is more important than
cross-frame deduplication.

## User-selected inline execution

`--stream` gives supported semantic plans an explicit low-latency alternative to windows.
It resolves an uncached judgement inline as each frame reaches the semantic operation, so a
first result can emit before a later frame arrives. This preserves input order, the
backend/model/spec/value cache identity, persistent cache reads and writes, schema
validation, cancellation, output and exit behavior, and the run-global `--max-calls` cap.
The cap still aborts before backend call N+1; frames successfully completed before that
boundary may already have been written.

The price is no window-wide harvest, backend batch, or cross-frame pre-resolve
deduplication. Inline runs may require more backend round trips and, with caching disabled,
may resolve matching judgements in separate frames more than once. Cache hits still avoid
backend calls. Default windows remain the better choice for throughput and batching;
choose `--stream` when latency is more valuable. `--stats` distinguishes
`three-phase-windowed` from `user-stream`, and `--explain --stream` explicitly reports
that harvest/dedup estimates are unavailable without reading stdin.

Pure-jq ignores `--stream` and remains deterministic. Queries that already require
interleaving retain their planner-selected inline behavior, so the flag does not override
that selection. Neither inline path uses semantic windows.

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
a safe superset. Unbounded values do not have a finite safe set, so ajq does not pretend
every shape can be harvested safely.

In 0.0.1, unbounded support is deliberately narrow:

- `sem_score` is supported by the three-phase executor as a `sort_by(...)` key, where ajq
  can harvest placeholder values for the sort key and then execute with resolved scores.
- `sem_norm` is supported by the three-phase executor as a `group_by(...)` key, using the
  same placeholder pattern for grouping.
- When an unbounded value result feeds a gate, such as `select(sem_score(.review;
  "positivity") > 0.8)`, ajq can choose an interleaved fallback. That mode resolves values
  as the jq program reaches them instead of providing a harvest/dedup estimate up front.
- Standalone `sem_extract` and `sem_redact` currently fail as unsupported in three-phase
  execution.

This means docs and scripts should distinguish "unsupported in three-phase harvest" from
"always unsupported." Gated unbounded value operators are not all rejected; supported
fallback shapes still obey backend selection, cache identity, and `--max-calls`, but they
do not have the same up-front estimate as the three-phase path.

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
