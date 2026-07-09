---
title: "Architecture"
linkTitle: "Architecture"
weight: 4
description: >
  The components of ajq and how data flows between them.
---

ajq is a small pipeline of components.

## Data flow

```text
stdin → [Reader/Framer] → [Desugar] → [gojq.Parse] → [Planner] ──→ [Explain]
                                                          │
                                              [3-phase Executor]
                                               ├─ gojq (deterministic)
                                               └─ Semantic Executor → [Backend iface]
                                                                          ├─ mock
                                                                          ├─ local daemon
                                                                          ├─ ollama
                                                                          ├─ openai / openrouter
                                                                          └─ anthropic cloud
[Grammar/Schema builder] → GBNF / json_schema ─────────────────────────────┘
stdout ← [Assembler / schema-invariance guard]
```

## The components

| Component | Responsibility |
|---|---|
| **Reader / Framer** | Stream framing: single JSON, NDJSON, and raw lines (awk-mode). Byte-budget windowing for very large inputs is planned (Phase 4). |
| **Desugar** | Rewrites `=~` / `!~` into `sem_match` calls with a jq-aware lexer. |
| **Planner** | Exhaustive AST walk that discovers every semantic call site → a `Plan` used for `--explain`, validation, and cost estimation. |
| **Executor** | Runs the plan in three phases (harvest / resolve / execute), with interleaved fallback for gated unbounded value ops. |
| **Semantic Executor** | Deduplicates judgements, consults the persistent judgement cache when enabled, builds each op's prompt and schema, and records run stats. |
| **Backend** | Pluggable inference. Shipped backends: `mock`, `local` (llama-server daemon), `ollama`, `openai`, `openrouter`, and `anthropic`/`--cloud`. |
| **Grammar / schema builder** | Turns an op's return type / enum into a JSON Schema and, for the local engine, GBNF grammar constraints. |
| **Assembler** | Reassembles the output stream and enforces schema invariance. |

## The `Backend` seam

Inference sits behind a deliberately small interface, so backends are swappable without
touching the planner or executor:

```go
type Backend interface {
    Judge(ctx, batch []Judgement) ([]Result, error) // grammar/schema-constrained
    Warm(ctx) error
}
```

A judgement carries the op, kind, return type, schema/enum constraints, specs, model ID,
and the canonical value. Results carry the value plus a per-item error. Resolve enforces
stable ordering, `len(results) == len(batch)`, return/schema validation, and only caches
successful items. The deterministic `mock` backend powers tests and `--explain` estimates;
`local`, Ollama, OpenAI-compatible, and Anthropic backends preserve the same structural
contract through GBNF or provider JSON-schema/structured-output mechanisms.

## Inference model

The local backend runs `llama-server` (llama.cpp) as a warm, lazily-spawned daemon —
chosen because ajq stays a pure-Go binary (no CGo, clean cross-compilation) *and* avoids
the 1.5–3 s cold model-load per invocation that would make a "filter" unusable. Outputs
are constrained by a JSON Schema / GBNF grammar derived from each op's schema, so
structural correctness is a cross-backend invariant (see
[the determinism contract](../determinism/)).

Remote and alternate-local backends share that same seam: Ollama uses its native
`/api/chat` endpoint with `format` JSON schema, OpenAI/OpenRouter use OpenAI-compatible
`/v1/chat/completions`, and Anthropic uses Messages API structured outputs. Backend
selection, model identity, base URL, cost caps, and persistent-cache behavior are resolved
by the CLI/env/config layer before execution starts.

## Where to go next

- [Split execution](../split-execution/) — the idea the architecture serves.
- [The three-phase executor](../three-phase-executor/) — the executor in detail.
- [Project status](../project-status/) — the shipped capabilities at a glance.
