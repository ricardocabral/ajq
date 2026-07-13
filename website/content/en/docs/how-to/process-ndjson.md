---
title: "Process an NDJSON stream"
linkTitle: "Process NDJSON"
weight: 2
description: >
  Handle newline-delimited JSON and raw log lines.
---

ajq auto-detects input framing. The common shapes:

## Single JSON value

A single top-level JSON value is processed as one input frame:

```bash
printf '{"a":1}\n' | ajq -c '.a + 1'
# 2
```

## NDJSON / JSON Lines

A stream of top-level JSON values (one per line, or concatenated) is processed **one frame
at a time** — each value flows through the query independently, and results are emitted as
they're produced:

```bash
printf '{"a":1}\n{"a":2}\n' | ajq -c '.a, (.a + 10)'
# 1
# 11
# 2
# 12
```

There's no whole-stream buffering in this mode, so it handles inputs larger than memory.

### Semantic NDJSON windows

For a supported three-phase semantic query, ajq groups **complete input frames** into
byte-budgeted windows before resolving semantic judgements. The default budget is 262144
bytes (256 KiB). ajq harvests every frame in one window, deduplicates and cache-resolves
its judgements once, then executes and emits those frames in their original order.

A window never splits a JSON value or raw line. ajq retains at most the current window,
one complete-frame lookahead, and bounded framing buffers—not the whole stream. A single
record larger than the budget is accepted as a one-frame oversized window, so that record
itself can require more memory than the configured budget.

Use a larger budget to find duplicates across more nearby records and reduce backend
batches; the trade-off is that output waits until its full window has been harvested and
resolved. Use a smaller positive budget when lower first-record latency matters:

```bash
# Keep semantic windows near 64 KiB for this invocation.
producer | ajq --backend local --window-bytes 65536 \
  'select(.message =~ "payment failure")'
```

Persistent cache entries remain reusable across windows. Pure-jq queries and semantic
queries that require interleaved execution keep their existing per-frame streaming paths;
this setting does not window them.

## Raw lines (awk mode)

To treat each input line as a plain string instead of JSON, use `-R` / `--raw-input`. The
whole line becomes `.`:

```bash
printf 'error: disk full\nok: healthy\n' | ajq -R 'select(test("error"))'
# "error: disk full"
```

Combine `-R` with `-r` to keep the output unquoted:

```bash
printf 'error: disk full\nok: healthy\n' | ajq -R -r 'ascii_upcase'
# ERROR: DISK FULL
# OK: HEALTHY
```

Raw mode is also where the implicit-`.` form of semantic operators shines — e.g.
`select(. =~ "stack trace")` matches log lines fuzzily. See
[Write a semantic filter](../semantic-filter/).

## Ignore stdin entirely

Use `-n` / `--null-input` to supply a single `null` frame and build output from scratch:

```bash
printf 'whatever' | ajq -n -c '{generated: true}'
# {"generated":true}
```

## Related

- [Classify JSON and NDJSON streams](../classify-json-streams/) — add bounded semantic labels to streaming records.
- [Filter JSON by meaning](../filter-json-by-meaning/) — fuzzy semantic selection over JSON fields.
- [Input and output modes reference](../../reference/io-modes/) — every framing and
  formatting flag.
