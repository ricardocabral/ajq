# Changelog

All notable changes to ajq are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
for published `vMAJOR.MINOR.PATCH` tags.

Until ajq reaches `v1.0.0`, minor releases may include breaking changes. Breaking
changes must still be called out explicitly under `Changed`, `Removed`, or
`Security`.

Write entries for users and operators, not as a commit log. Update
`[Unreleased]` for user-visible CLI behavior, semantic execution behavior,
backend compatibility, packaging, installer, documentation, release, or security
changes. Use the standard section names `Added`, `Changed`, `Deprecated`,
`Removed`, `Fixed`, and `Security`, omitting empty sections.

## [Unreleased]

### Added

- Added a Diataxis how-to for making ajq available to coding agents through
  project guidance, including JSON-routing, safe semantic-validation, and
  cache-handling instructions.
- Added a public, native Codex marketplace containing the `ajq` routing skill
  for semantic JSON/NDJSON filtering and bounded classification. The same
  package includes an optional `npx plugins` adapter for Claude Code and Cursor.
- Documented plugin installation, fresh-workspace verification, CI provisioning,
  and the explicit mock → explain → capped-backend safety workflow.
- Opt-in real local-inference benchmark runs can now write versioned JSON reports with the
  actual GGUF SHA-256, runtime and source revision, optional hardware label, and measured
  workload details.
- Added the first reviewed fresh-session `none` versus `local-guidance` routing baseline for
  Codex GPT-5. It is recorded as one paired observation, not a general discovery-rate claim.

### Fixed

- Semantic cache entries now distinguish `--base-url` endpoints, preventing
  cached judgements from one compatible deployment being reused by another
  deployment serving a model with the same name.
- The real local-inference benchmark now authenticates its internal benchmark requests to the
  managed daemon, allowing authenticated benchmark runs to complete.
- Removed website inference-latency figures that lacked versioned source reports; the site now
  links to the reproducible benchmark workflow instead.

### Security

- HTTP-status error messages from OpenAI-compatible, Ollama, and Anthropic
  backends no longer include raw provider response bodies; they report only the
  status code and, when available, structured error type or code fields.

## [0.0.3] - 2026-07-10

### Fixed

- Pinned the release workflow's golangci-lint setup to the validated version
  used by CI, avoiding a failed release when the upstream latest installer
  publishes mismatched checksums.

## [0.0.2] - 2026-07-10

### Added

- Added an agent-discovery CLI surface: `ajq examples [topic]` provides
  categorized copy-pasteable safe workflows; `ajq capabilities --json` is a
  static versioned contract that does not load configuration or initialize a
  backend; and `ajq models list --json`, `ajq cache status --json`, and
  check-only `ajq provision --check --json` provide versioned local-state
  probes without parsing human output. Missing provisioning readiness still
  emits its document and exits 1. Root and discovery-command help include the
  deterministic `--backend mock` probe and `--explain` plan workflow.
- Added problem-first how-to guides for filtering JSON by meaning, classifying
  JSON/NDJSON streams, and using ajq safely from coding agents.
- Added a how-to guide for using ajq with JSON produced by complementary jq
  ecosystem adapters such as `jc`, `yq`/`xq`, and `fq`.
- Added canonical and structured website metadata, explicit robots/sitemap
  discovery, and curated `/llms.txt` and `/llms-full.txt` assets for
  machine-facing documentation, including a safe agent workflow and public-only
  expanded context.

## [0.0.1] - 2026-07-09

### Added

- Initial public release of `ajq`, a jq-like CLI that keeps ordinary jq
  execution byte-deterministic and calls model backends only for explicit
  semantic operations.
- Deterministic JSON, NDJSON, raw-input, and null-input execution through
  `gojq`, including jq-style output modes and exit-status behavior.
- Semantic planning, split execution, `--explain` output, model-call estimates,
  deduplicated judgements, persistent judgement caching, and `--stats` runtime
  counters.
- `sem_match` and bounded `sem_classify`, plus jq-aware `=~` and `!~` sugar for
  fuzzy filters.
- Limited unbounded value support for `sem_score` in `sort_by(...)`, `sem_norm`
  in `group_by(...)`, and gated unbounded value operations through interleaved
  fallback.
- Semantic backends for deterministic mock runs, managed local llama.cpp,
  Ollama, OpenAI, OpenRouter, and Anthropic.
- Cost controls for semantic work, including `--max-calls`, `--no-cache`,
  cache-aware planning, and a default 100-call cap for paid/cloud backends.
- Local provisioning with `ajq provision` for the llama.cpp engine and default
  GGUF model, plus `ajq models list|pull|use` for checksum-pinned catalog
  models.
- Checksummed release archives, a checksum-verifying install script, Homebrew
  cask publishing to `ricardocabral/tap`, and public website
  documentation for install, quick-start, CLI, semantic functions, split
  execution, cache, provisioning, and backends.

### Changed

- Pure jq paths are intentionally separate from semantic execution: they do not
  construct backends, make network calls, or spawn the local daemon.
- `sem_extract` and `sem_redact` are registered and visible to the planner, but
  standalone three-phase execution currently fails loudly as unsupported instead
  of producing unsafe placeholder output.
- Scale-out windowing and a top-level `--stream` mode remain planned for later
  releases.

### Security

- Install and provisioning flows verify checksums and reject unsafe archive paths
  before extracting local assets.
- API keys for paid/cloud providers are environment-only and are not required for
  pure jq or mock-backend runs.
