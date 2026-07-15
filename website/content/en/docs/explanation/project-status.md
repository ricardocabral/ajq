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
persistent judgement cache, byte-budgeted semantic windows, an explicit low-latency
semantic stream mode, and checksummed release archives with a no-sudo install script.
Further streaming optimizations remain planned. The iterative-harvest experiment is an internal-only no-go prototype, not a shipped mode; default windowed execution is unchanged.

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
- **Three-phase executor and stream selection** — harvest / resolve / execute with
  deduplication and cache identity based on op, spec, model, and canonical value. Supported
  semantic NDJSON and raw streams default to complete-frame byte-budgeted windows (256 KiB
  by default), preserving source order without retaining the complete stream. `--stream`
  selects low-latency inline execution instead when first-frame latency outweighs window
  batching and cross-frame pre-resolve deduplication; identity and `--max-calls` semantics
  remain unchanged. Pure-jq and planner-required inline paths stay streaming.
- **Local inference** — a lazy `llama-server` daemon with idle timeout and
  `ajq daemon status|stop`; local requests use bounded parallelism while preserving result
  ordering.
- **First-run provisioning and model management** — `ajq provision` installs or locates the
  local engine and default model; `ajq models list|pull|use` manages larger pinned catalog
  models.
- **Additional backends** — Ollama, OpenAI, OpenRouter, and Anthropic (`--cloud`) are
  registered with structured-output constraints, provider-specific credential rules, and
  bounded ordered batch concurrency. Provider requests remain sequential by default;
  explicit OpenAI-compatible/Anthropic concurrency is capped at two and Ollama at four.
- **Config and cost controls** — backend/model/base-url selection, TOML config, env vars,
  `--backend-concurrency`, `--max-calls`, and `--stats` are shipped. Paid backends default
  to a 100-call cap.
- **Persistent judgement cache** — successful semantic judgements are stored under
  `<cache>/judgements/`, can be bypassed with `--no-cache`, inspected with
  `ajq cache status`, and removed with `ajq cache clear`.
- **Release packaging** — GoReleaser builds checksummed archives and the install script
  verifies `checksums.txt`. The release workflow publishes the Homebrew cask to the
  public [`ricardocabral/tap`](https://github.com/ricardocabral/homebrew-tap) tap.
  Windows MSI packaging is implemented and CI-validated, but the MSI is not yet
  released. WinGet remains unavailable until a future MSI release completes Microsoft
  validation and merge and has public clean-install smoke evidence.

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
| **4 — Scale & chunking** | Byte-budgeted complete-frame windows and explicit `--stream` low-latency inline execution for supported semantic streams, persistent cache, and bounded local parallelism are shipped. The iterative-harvest experiment is [an internal-only no-go](../iterative-harvest-evaluation/): it has no user flag and does not change the default executor. Further streaming optimizations remain planned. | 🟡 Partial |
| **5 — Polish & distribution** | Models subcommand, release archives/install script, and the Homebrew cask published to the public `ricardocabral/tap` tap are shipped. Windows MSI packaging is implemented and CI-validated but remains unreleased; WinGet is unavailable until a future MSI release completes Microsoft validation and merge and has public clean-install smoke evidence. Standalone build, GPU auto-detect, richer vocabulary, and additional package managers remain planned. | 🟡 Partial |

## Follow along

Development happens in the open at
[github.com/ricardocabral/ajq](https://github.com/ricardocabral/ajq). Issues and PRs are
welcome.
