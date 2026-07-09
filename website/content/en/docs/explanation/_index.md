---
title: Explanation
linkTitle: Explanation
weight: 4
description: >
  How ajq works and why it's designed this way.
---

Design notes for ajq's execution model, reproducibility guarantees, and architecture.

- **[Split execution](split-execution/)** — the core idea: keep jq deterministic, call a
  model only for the fuzzy operators.
- **[The determinism contract](determinism/)** — what "byte-reproducible" guarantees, and
  what it deliberately doesn't.
- **[The three-phase executor](three-phase-executor/)** — harvest, resolve, execute, and
  why deduplication is free.
- **[Architecture](architecture/)** — the components and how data flows between them.
- **[Project status](project-status/)** — the shipped capabilities and how the pieces fit together.
