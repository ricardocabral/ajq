---
title: Documentation
linkTitle: Docs
weight: 20
description: >
  Tutorials, how-to guides, reference material, and design notes for semantic jq:
  fuzzy JSON filters, bounded classification, deterministic jq execution, and
  explicit model-backed operators.
menu:
  main:
    weight: 20
no_list: true
---

Everything you need to understand, evaluate, and use **ajq**.

{{% pageinfo color="primary" %}}
**ajq is usable today.** The deterministic jq spine, semantic planning and execution,
local inference (`--backend local`/`mock`), Ollama/OpenAI-compatible/Anthropic backends,
cost controls, persistent judgement cache, local asset provisioning, model management, and
release archives with a checksum-verifying install script are shipped. Scale-out/windowing
remains a roadmap item. For the full picture, see
[Project status](explanation/project-status/).
{{% /pageinfo %}}

## Start here

<div class="row">
<div class="col-md-6 mb-4">

### 🎓 [Tutorials](tutorials/)

Run your first ajq pipeline and inspect how semantic JSON filters are planned.

</div>
<div class="col-md-6 mb-4">

### 🛠️ [How-to guides](how-to/)

Install ajq, process JSON/NDJSON streams, write fuzzy filters, classify records, use
cloud or local models, control costs, manage the cache, and configure defaults.

</div>
<div class="col-md-6 mb-4">

### 📚 [Reference](reference/)

CLI flags, configuration, backends, exit codes, I/O modes, semantic functions, and
`--explain` fields.

</div>
<div class="col-md-6 mb-4">

### 💡 [Explanation](explanation/)

Split execution, the determinism contract, the executor, architecture, and project status.

</div>
</div>

## Not sure where to look?

- **"How do I get it running?"** → [Tutorial: Your first ajq pipeline](tutorials/first-pipeline/)
- **"How do I connect a provider?"** → [Use cloud backends](how-to/use-cloud-backends/)
- **"How do I avoid surprise spend?"** → [Control semantic costs](how-to/control-costs/)
- **"What does flag `--foo` do?"** → [Reference](reference/)
- **"Why is it designed this way?"** → [Explanation](explanation/)
