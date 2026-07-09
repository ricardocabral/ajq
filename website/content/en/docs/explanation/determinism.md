---
title: "The determinism contract"
linkTitle: "Determinism contract"
weight: 2
description: >
  What ajq's byte-reproducibility guarantees, and what it deliberately doesn't.
---

ajq separates deterministic structure from semantic judgement.

## The contract

> The deterministic pipeline is **byte-reproducible**. Semantic judgements are
> **structurally** guaranteed, but their values are model- and cache-dependent.

Those are different promises.

### Deterministic parts are byte-reproducible

Every part of a query that is not a semantic operator runs through pure gojq. Given the
same input, it produces the same output bytes. There is no model, no sampling, and no
network in that path.

This is why pure-jq queries and pure-jq `--explain` reports are reproducible, and why the
jq skeleton around a semantic query remains stable.

### Semantic parts are structurally guaranteed

A model's answer to "is this urgent?" can differ across model versions or providers. ajq
does not promise semantic value reproducibility across those changes. It promises the
shape of the answer: `sem_match` returns a boolean, and `sem_classify` returns one of the
labels declared at the call site.

That shape is enforced by GBNF grammar for the local `llama-server` backend and by
structured-output/schema mechanisms for Ollama, OpenAI-compatible, and Anthropic backends.
Downstream jq can rely on the type even when the judgement value changes.

## Why caching does not change the contract

ajq caches successful semantic judgements by
`(op, spec, model-id, canonical-value)`. The cache has an in-memory front for a run and a
persistent on-disk backing when caching is enabled. A repeated value under the same model
identity can therefore resolve to a cache hit instead of another backend call.

Caching improves cost and latency, but it does not turn semantic answers into a universal
truth. A different model identity is a different cache key, so model upgrades become clean
misses rather than silent reuse. `--no-cache` disables persistent judgement cache reads and
writes for a run.

## The practical upshot

- Diff two runs of a pure jq pipeline: the bytes match.
- Repeated semantic values with the same op/spec/model identity can be served from cache.
- Change the model or provider: output structure remains valid, but judgement values may
  change and cache keys miss.
- Clear or bypass the cache: ajq may ask the backend again, while preserving the same
  structural contract.

## Related

- [Split execution](../split-execution/) — why most of a pipeline is deterministic.
- [The three-phase executor](../three-phase-executor/) — where cache and deduplication fit.
- [Manage the judgement cache](../../how-to/manage-the-cache/) — task recipe for cache control.
