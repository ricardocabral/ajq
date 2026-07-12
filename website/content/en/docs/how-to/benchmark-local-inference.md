---
title: "Benchmark local inference"
linkTitle: "Benchmark local inference"
weight: 13
description: >
  Capture versioned, reproducible local-model benchmark reports before publishing ajq performance figures.
---

# Benchmark local inference

Use this workflow to collect local-model measurements that can support a public performance
claim. You need a clean checkout, a provisioned `llama-server` and GGUF model, and enough disk
space for small JSON reports.

## 1. Record deterministic split-execution overhead

Run the mock harness first:

```sh
make bench-phase2
```

It measures the full ajq split-execution path with the deterministic mock backend. Treat these
results as regression data for parser, planner, cache, and serialization overhead; they do not
measure model inference latency or semantic quality.

## 2. Capture five independent local-model runs

Start from a clean checkout so the report can name the exact source revision:

```sh
git status --short
```

The command should print nothing. Then run five cold-start measurements and save each raw report
under the website's static benchmark directory:

```sh
AJQ_BENCH_MACHINE="Apple M3 Pro (Metal)" \
AJQ_BENCH_REPORT_DIR=website/static/benchmarks/YYYY-MM-DD-machine \
BENCH_RUNS=5 \
make bench-phase2-real
```

The runner stops the managed daemon before each run, records cold start, one warm judgement,
sequential and bounded-parallel batches, and a cache replay. It writes one non-overwriting JSON
file per successful run. Each report includes the actual model-file SHA-256, model size,
llama-server version, Go version, source revision, runtime architecture, and parallel-slot count.
It does not write daemon API keys or raw model responses.

## 3. Publish a bounded claim

Inspect all five reports before adding a website figure. State the median and range, link the
exact report directory, and name the workload, model hash, hardware label, and server version.
Do not turn mock-harness timings into model-latency claims, and do not generalize one machine's
measurements into a guarantee for every model or input.

If the checkout is dirty, commit the implementation first and rerun the collection. A report
marked with a dirty revision is useful for local diagnosis but is not publishable reference data.

## Current reference run

The website's current reference figures are the median of five clean runs on an Apple M3 Pro
(Metal) with llama.cpp 9910 and the 1.5B Q5_K_M model pinned to
`b4666107…5896f8c`. The workload is a 64-record `sem_match` array with eight post-dedup
judgements. Inspect the [five raw JSON reports](https://github.com/ricardocabral/ajq/tree/main/website/static/benchmarks/2026-07-12-m3-pro)
before comparing them with different hardware, models, or data shapes.

## Related

- [Estimate model calls before running](../estimate-model-calls/) for workload-specific call and
  cost estimates.
- [Control semantic costs](../control-costs/) for production call limits and cache controls.
