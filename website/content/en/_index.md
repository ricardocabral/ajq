---
title: ajq — semantic jq for JSON streams
description: >
  Semantic jq for messy JSON/NDJSON: filter by meaning, classify streams, and
  keep pure jq deterministic with explicit model calls.
---

{{< blocks/cover title="ajq" image_anchor="top" height="full" color="dark" >}}
<div class="mx-auto">
  <p class="cover-tagline h3 mt-2 mb-3">Filter JSON by meaning and route JSONL by intent.</p>
  <p class="lead">Use explicit semantic predicates when <code>jq</code> knows the structure and the wording varies,<br>with ordinary jq execution remaining deterministic.</p>

  <div class="cover-code">
<span class="tok-comment"># fuzzy JSON filter: keep jq deterministic, make only the predicate semantic</span><br>
cat tickets.json | ajq --backend local <span class="tok-op">'</span>.[] | select(.msg <span class="tok-sem">=~</span> <span class="tok-op">"angry/frustrated"</span>) | .id<span class="tok-op">'</span>
  </div>

  <div class="mt-4">
    <a class="btn btn-lg btn-primary me-3 mb-4" href="docs/">
      Get started <i class="fas fa-arrow-alt-circle-right ms-2"></i>
    </a>
    <a class="btn btn-lg btn-secondary me-3 mb-4" href="https://github.com/ricardocabral/ajq">
      GitHub <i class="fab fa-github ms-2"></i>
    </a>
  </div>
  <p class="lead mt-2" style="font-size:1rem;opacity:.75">Install a release binary, or fall back to Go source:</p>
  <div class="cover-code">
curl -fsSL https://raw.githubusercontent.com/ricardocabral/ajq/main/scripts/install.sh | sh<br>
go install github.com/ricardocabral/ajq/cmd/ajq@latest
  </div>

  <p class="lead mt-2" style="font-size:1rem;opacity:.75">One Go binary · local + cloud backends · persistent cache</p>
</div>
{{< blocks/link-down color="light" >}}
{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}
**ajq** brings semantic matching and bounded classification to JSON streams, using
the jq language you already know. It runs the ordinary parts of your query through a real
jq engine and only calls a language model for explicit fuzzy operators, one small field
value at a time.

So most of your pipeline stays byte-for-byte reproducible, and what you pay tracks how
many fuzzy decisions you actually make, not how big your input is.
{{% /blocks/lead %}}

{{% blocks/section color="white" type="row" %}}
{{% blocks/feature icon="fa-bolt" title="Deterministic core" %}}
Real `jq` semantics, powered by [`gojq`](https://github.com/itchyny/gojq). Most of every
pipeline is byte-reproducible: the same input gives you the same bytes on every run. Pure
jq paths never contact AI backends; only explicit semantic operators reach for a model.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-wand-magic-sparkles" title="Semantic operators" %}}
`select(.x =~ "spec")` and `sem_classify` are jq-shaped operators for fuzzy filters and
labels. `sem_score` and `sem_norm` are available only in supported contexts, while
standalone `sem_extract` and `sem_redact` are registered but unsupported. There is no new
grammar to learn, so semantic predicates compose with ordinary jq pipelines.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-microchip" title="Local-first and cheap" %}}
A provisioned ~1&nbsp;GB local model runs fuzzy decisions on your machine via
`--backend local`, while `--cloud` selects the shipped Anthropic backend. Identical field
values get deduplicated and written to the persistent judgement cache, so a second run can
replay repeated decisions without another model call.
{{% /blocks/feature %}}
{{% /blocks/section %}}

{{% blocks/section color="light" %}}
<div class="text-center">

## How split execution works

</div>

A real `ajq` query is mostly plain `jq`. Only the explicit *fuzzy* operator needs a model:

```text
cat data.json | ajq --backend local '.users[] | select(.feedback =~ "angry/frustrated") | .id'
                      └────────┬───────┘  └────────────┬────────────┘  └┬┘
                      deterministic path       semantic predicate       proj
                      (pure gojq)              (LLM, per value)          (pure gojq)
```

ajq parses your query, runs everything deterministic through jq in process, and only calls
the model on the smallest slices of data it can get away with. Because the model sees one
field value at a time, the context stays tiny and a 1.5B model is plenty. There's more in
[Split execution](docs/explanation/split-execution/).
{{% /blocks/section %}}

{{% blocks/section color="white" %}}
<div class="text-center">

## How ajq compares

</div>

<div class="compare-table-wrap">
<table class="compare">
  <thead>
    <tr>
      <th></th>
      <th class="col-ajq">ajq</th>
      <th><a href="https://github.com/simonw/llm-jq">llm-jq</a></th>
      <th>jq / awk</th>
      <th>Ad-hoc LLM script</th>
      <th>grep / ripgrep</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>Fuzzy, semantic matching</td>
      <td class="col-ajq yes">Yes</td>
      <td>Prompted jq</td>
      <td class="no">No</td>
      <td class="yes">Yes</td>
      <td class="no">No</td>
    </tr>
    <tr>
      <td>Deterministic, byte-reproducible core</td>
      <td class="col-ajq yes">Yes</td>
      <td>After generation</td>
      <td class="yes">Yes</td>
      <td class="no">No</td>
      <td class="yes">Yes</td>
    </tr>
    <tr>
      <td>Structured JSON in and out</td>
      <td class="col-ajq yes">Yes</td>
      <td class="yes">Yes</td>
      <td class="yes">jq only</td>
      <td>DIY</td>
      <td class="no">Lines only</td>
    </tr>
    <tr>
      <td>Cost tracks fuzzy decisions, not input size</td>
      <td class="col-ajq yes">Yes</td>
      <td>One prompt + sample</td>
      <td>n/a</td>
      <td class="no">No, per row</td>
      <td>n/a</td>
    </tr>
    <tr>
      <td>Runs locally, no API key</td>
      <td class="col-ajq yes">Local backend</td>
      <td>Depends on LLM</td>
      <td class="yes">Yes</td>
      <td class="no">Usually cloud</td>
      <td class="yes">Yes</td>
    </tr>
    <tr>
      <td>Structural output guarantee (schema, enum)</td>
      <td class="col-ajq yes">GBNF-constrained</td>
      <td>Generated jq</td>
      <td class="yes">Exact</td>
      <td class="no">Prompt-hope</td>
      <td class="yes">Exact</td>
    </tr>
    <tr>
      <td>One-line pipeline ergonomics</td>
      <td class="col-ajq yes">Yes</td>
      <td class="yes">Yes</td>
      <td class="yes">Yes</td>
      <td class="no">No</td>
      <td class="yes">Yes</td>
    </tr>
  </tbody>
</table>
</div>

Here's the gap ajq fills. `grep`, `awk`, and `jq` are deterministic but literal. `llm-jq`
uses an LLM to write a jq program from your prompt, then runs that program over the data.
A hand-rolled LLM script can be fuzzy, but it pays per row, drifts over time, and leaves
you to plumb the JSON yourself. ajq keeps jq's ergonomics and adds explicit fuzzy JSON
filters and classification, deduplicated and cached.
{{% /blocks/section %}}

{{% blocks/section color="light" %}}
<div class="text-center">

## Performance

</div>

<p class="text-center lead-tight">Local-model latency depends on the model, runtime, hardware,
and repeated-value ratio. These are medians from five independent cold-start runs; see the raw
reports before comparing them with your own workload.</p>

<div class="bench-grid">
  <div class="bench-card">
    <div class="bench-num">~611ms</div>
    <div class="bench-label">median cold start, including model load and first readiness check</div>
  </div>
  <div class="bench-card">
    <div class="bench-num">~86ms</div>
    <div class="bench-label">median warm latency for one fuzzy judgement</div>
  </div>
  <div class="bench-card">
    <div class="bench-num">~11.9/s</div>
    <div class="bench-label">median sequential throughput for 8 distinct judgements</div>
  </div>
  <div class="bench-card">
    <div class="bench-num">~12.8/s</div>
    <div class="bench-label">median bounded-parallel throughput (4 local slots)</div>
  </div>
  <div class="bench-card">
    <div class="bench-num">~14.7&micro;s</div>
    <div class="bench-label">median cached replay of the same judgement batch</div>
  </div>
</div>

<div class="bench-note">
Reference run: Apple M3&nbsp;Pro (Metal), llama.cpp&nbsp;9910, Qwen2.5-1.5B&nbsp;Q5_K_M
(SHA-256 <code>b4666107&hellip;5896f8c</code>), 64-record <code>sem_match</code> array with
8 post-dedup judgements. Cold start ranged from 611–916&nbsp;ms; warm latency from
84–95&nbsp;ms. These are workload-specific measurements, not a product guarantee.
<a href="https://github.com/ricardocabral/ajq/tree/main/website/static/benchmarks/2026-07-12-m3-pro">Inspect the five raw JSON reports</a>.
</div>
{{% /blocks/section %}}

{{% blocks/section color="dark" type="row" %}}
{{% blocks/feature icon="fab fa-github" title="It's open source" url="https://github.com/ricardocabral/ajq" %}}
ajq is MIT-licensed and built in the open. Issues, ideas, and pull requests are all
welcome.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-book" title="Read the docs" url="docs/" %}}
Walkthroughs, task recipes, reference pages, and design notes for ajq.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-download" title="Install it now" url="docs/how-to/install/" %}}
Install with the release script or `go install`, then run `ajq provision` to fetch the
default model and locate a `llama-server` engine — no API key required for local work.
{{% /blocks/feature %}}
{{% /blocks/section %}}
