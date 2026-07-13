---
title: "Iterative-harvest prototype evaluation"
linkTitle: "Iterative-harvest evaluation"
weight: 35
description: >
  Reproducible evidence and the no-go decision for the non-default iterative-harvest prototype.
---

## Decision: no-go for productionization

The iterative-harvest implementation is an internal test and benchmark prototype. It is
**not** a CLI mode, is not selected by default, and must not be enabled for users.
The existing `three-phase-windowed` executor remains the default and unsupported query
shapes continue to use their existing windowed or planner-required interleaved fallback.

This is a no-go even though the pruning workload clears its judgement-saving target. A
reachable error case found during characterization has different behavior from the current
windowed executor: after an earlier gate rejects a value, a backend result/schema/transport
error for that pruned descendant is dispatched and returned by permissive windowed harvest,
but is intentionally not dispatched by iterative harvest or interleaved execution. That
changes the reachable error, cache, and exit contract. Reducing calls by dispatching the
unreachable descendant would defeat the prototype's purpose. Until a separate design
resolves that semantic/error boundary, the required current-window parity gate is not met.

The deterministic no-prune control also exceeded both overhead limits on the recorded
machine, so performance alone would not support productionization.

## Reproduce the fake evidence

Run the fixed, network-free paired protocol from the repository root:

```sh
make bench-iterative-evidence
# equivalent: go test -count=1 -run TestIterativeFakeEvidence -v ./internal/bench
```

The command uses 21 timed repetitions after one discarded warm-up per executor and rotates
the order of `three-phase-windowed`, `iterative-harvest`, and the `user-stream`
interleaved reference. Every sample checks selected executor mode, output parity, and the
controlled backend's expected post-dedup call and batch oracle before accepting timing.
It emits a schema-versioned JSON record containing every sample and its min/median/max
latency summary.

The controlled fake corpus has fixed input, query, window, and answers:

| Workload | Purpose | Windowed → iterative post-dedup calls |
|---|---|---:|
| `high-prune` | one of eight values survives gate one | 16 → 9 |
| `low-prune` | seven of eight values survive gate one | 16 → 15 |
| `no-prune` | every value survives the same two-gate chain | 16 → 16 |
| `repeated-cache-hit` | duplicate values with a deliberately pre-warmed shared store | 0 → 0 |
| `enum-gate` | literal-label `sem_classify` gate followed by match | 6 → 4 |
| `multi-window` | three NDJSON frames with a one-byte window budget | 6 → 5 |

The saving metric is always `windowed PostDedupBackendCalls - iterative
PostDedupBackendCalls`; harvested counts are not compared because staged and permissive
harvest deliberately mean different things.

### Memory and allocation method

For each timed fake sample the runner calls `runtime.GC`, records the baseline memory
statistics, runs the complete engine operation (compile, framing, cache, fake backend, and
discarded output), and samples `HeapAlloc` every 100 microseconds until completion. The
reported peak retained memory is the sampled `HeapAlloc` high-water above that post-GC
baseline, not final heap size. The report also records `Mallocs` and `TotalAlloc` deltas
per sample. This makes the memory unit and reset policy explicit; it is a process-local
comparison rather than a claim about a model server's memory.

## Recorded fake result

On 2026-07-13, Apple M3 Pro (darwin/arm64, Go test runner), the fixed protocol reported:

- `high-prune`: 7 avoided judgements, **43.75%** reduction, passing the required >=25%
  judgement reduction threshold.
- `no-prune`: **72.94%** median latency overhead (limit <=15%) and **76.60%** median
  peak-retained-memory overhead (limit <=25%), both failing. Exact timing is expected to
  vary by machine; the command above is the authoritative repeatable evidence.
- The allocation-aware engine benchmark independently showed the same fake-overhead shape:
  at 200ms benchtime, no-prune windowed was about 146µs/op, 217KB/op, 2720 allocs/op;
  iterative was about 260µs/op, 377KB/op, 4829 allocs/op.

These are fake executor costs, not model latency. Optional local-model evidence is
informational only and cannot replace the deterministic gate:

```sh
make bench-iterative-real
```

It requires `AJQ_BENCH_REAL=1` plus the existing provisioned `llama-server` and model,
then runs the same chained workload through complete windowed and iterative engine paths
with alternating mode order and output-parity checks. It skips cleanly when the explicit
opt-in or assets are unavailable. On the recorded machine it was run with the provisioned
Qwen 2.5 1.5B Q5 model; model answers happened to pass all first gates, so both paths made
16 calls and its latency is deliberately not used for a threshold decision.

## What a separate redesign would need

Do not turn this result into a production flag. A new issue/design would first need to
settle the pruned-descendant backend-error/cache/exit contract and restore the required
parity. Only after that should it propose a public mode selection policy. It would also
need a truthful `--stats`/`--explain` surface that names the selected staged mode, stage
batches, actual post-dedup calls, avoided judgements, cache hits, and why any query fell
back. The supported corpus would remain intentionally narrow: a linear stable-value chain
of `sem_match` gates and bounded literal enum `sem_classify == label` gates. Control flow,
value operators, generators, bindings, nested queries, non-literal inputs, and all other
ambiguous shapes must keep their existing executor fallback.
