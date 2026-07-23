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

A stream of top-level JSON values (one per line, or concatenated) is framed as independent
input values. Pure-jq and planner-required inline semantic queries process and emit one
frame at a time. Supported three-phase semantic queries use the byte-budgeted windows
described below, so their output may wait for the current window to resolve:

```bash
printf '{"a":1}\n{"a":2}\n' | ajq -c '.a, (.a + 10)'
# 1
# 11
# 2
# 12
```

There is no whole-stream buffering, so all of these modes handle inputs larger than memory.

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
resolved. Use a smaller positive budget when lower first-record latency matters but
window-wide batching and deduplication are still useful:

```bash
# Keep semantic windows near 64 KiB for this invocation.
producer | ajq --backend local --window-bytes 65536 \
  'select(.message =~ "payment failure")'
```

### Choose `--stream` for first-frame latency

Use `--stream` when a supported semantic pipeline must resolve and emit each frame without
waiting for a window to fill:

```bash
producer | ajq --backend local --stream \
  'select(.message =~ "payment failure")'
```

`--stream` selects inline semantic execution for queries that otherwise use three-phase
windows. It preserves input order, cache reads/writes and identity, schema validation,
cancellation, output/exit behavior, and the run-global `--max-calls` cap. An uncached
judgement resolves inline; a persistent cache hit still makes no backend call.

The latency trade-off is deliberate: inline execution has no window-wide backend batching
or cross-frame pre-resolve deduplication. It can therefore make more backend round trips
(and, with `--no-cache`, repeat a judgement in separate frames), while default windows can
reduce those calls at the cost of waiting for the window. `--stats` names the selected mode
and trade-off; `--explain --stream` reports that its harvest/dedup estimate is unavailable
without consuming stdin.

Pure-jq queries ignore `--stream` and remain deterministic. Queries the planner already
requires to interleave keep that existing inline behavior; `--stream` does not change their
selection. `--window-bytes` does not affect either inline path.

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
