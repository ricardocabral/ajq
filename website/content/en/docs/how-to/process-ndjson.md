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
