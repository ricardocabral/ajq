---
title: "Project status"
linkTitle: "Project status"
weight: 5
description: >
  The capabilities that ship today, and how they fit together.
---

ajq has shipped its deterministic jq spine, semantic planning and supported semantic
execution, local inference, bounded-parallel local daemon transport, Phase 3
backend/cloud controls, first-run local asset provisioning, local model management, the
persistent judgement cache, byte-budgeted semantic windows, and checksummed release
archives with a no-sudo install script. Richer streaming capabilities remain planned.

## What ajq does today

- **Deterministic stream framing** — single JSON, NDJSON, and raw-line (`-R`) input;
  `-n`, `-c`, `-r`, and `-e` follow jq conventions.
- **Pure-jq execution** through [gojq](https://github.com/itchyny/gojq) — pure jq queries
  are deterministic and byte-oriented.
- **`--explain`** — the byte-stable `ajq explain v1` report for pure-jq and semantic
  queries, including harvest/dedup estimates when input is supplied.
- **Semantic query planning** — an AST walk discovers `sem_*` call sites and is guarded by
  the runtime `fired ⊆ planned` invariant.
- **`=~` / `!~` desugaring** — a jq-aware lexer rewrites fuzzy match syntax into
  `sem_match(...)` calls.
- **Supported semantic execution** — `sem_match` and bounded `sem_classify` run through the
  split executor. `sem_score` is supported as a `sort_by(...)` key, `sem_norm` is
  supported as a `group_by(...)` key, and gated unbounded value use can run through
  interleaved fallback. Standalone `sem_extract` and `sem_redact` are registered but
  currently fail as unsupported in three-phase execution.
- **Three-phase executor** — harvest / resolve / execute with deduplication and cache
  identity based on op, spec, model, and canonical value. Supported semantic NDJSON and
  raw streams use complete-frame byte-budgeted windows (256 KiB by default), preserving
  source order without retaining the complete stream; pure-jq and interleaved paths stay
  streaming.
- **Local inference** — a lazy `llama-server` daemon with idle timeout and
  `ajq daemon status|stop`; local requests use bounded parallelism while preserving result
  ordering.
- **First-run provisioning and model management** — `ajq provision` installs or locates the
  local engine and default model; `ajq models list|pull|use` manages larger pinned catalog
  models.
- **Additional backends** — Ollama, OpenAI, OpenRouter, and Anthropic (`--cloud`) are
  registered with structured-output constraints and provider-specific credential rules.
- **Config and cost controls** — backend/model/base-url selection, TOML config, env vars,
  `--max-calls`, and `--stats` are shipped. Paid backends default to a 100-call cap.
- **Persistent judgement cache** — successful semantic judgements are stored under
  `<cache>/judgements/`, can be bypassed with `--no-cache`, inspected with
  `ajq cache status`, and removed with `ajq cache clear`.
- **Release packaging** — GoReleaser builds checksummed archives and the install script
  verifies `checksums.txt`. The release pipeline publishes a Homebrew cask to the
  public `ricardocabral/tap` tap.

## Roadmap

Development proceeds by dependency. Phases 0–3 have shipped, along with selected Phase 4
and Phase 5 work pulled forward. Remaining roadmap items are scale and distribution
polish.

| Phase | Focus | Status |
|---|---|---|
| **0 — Deterministic spine** | CLI, framing, pure-jq wrapper, `--explain`, golden harness. | ✅ Shipped |
| **1 — Split-execution core** | Planner, desugar, semantic predicates, bounded classification, guarded executor. | ✅ Shipped with explicit unbounded value-op limits |
| **2 — Local inference** | `llama-server` backend, daemon lifecycle, GBNF/schema constraints, provisioning. | ✅ Shipped |
| **3 — Backends & cloud** | Ollama, OpenAI/OpenRouter, Anthropic, config/env selection, cost controls. | ✅ Shipped |
| **4 — Scale & chunking** | Byte-budgeted complete-frame windows for supported three-phase semantic streams, persistent cache, and bounded local parallelism are shipped. Richer streaming remains planned. | 🟡 Partial |
| **5 — Polish & distribution** | Models subcommand, release archives/install script, and Homebrew tap publishing are shipped; standalone build, GPU auto-detect, richer vocabulary, and additional package managers remain planned. | 🟡 Partial |

## Follow along

Development happens in the open at
[github.com/ricardocabral/ajq](https://github.com/ricardocabral/ajq). Issues and PRs are
welcome.
