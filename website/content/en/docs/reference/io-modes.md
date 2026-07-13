---
title: "Input and output modes"
linkTitle: "I/O modes"
weight: 2
description: >
  How ajq frames input and formats output.
---

## Input framing

ajq selects an input framing based on flags and the shape of stdin.

| Mode | Flag | Behavior |
|---|---|---|
| Auto (default) | — | Auto-detects a single JSON value or a stream of top-level / NDJSON JSON values. |
| Null input | `-n`, `--null-input` | Ignores stdin; supplies one `null` input frame. |
| Raw input | `-R`, `--raw-input` | Reads each stdin line as a string, excluding the line terminator. `.` is the line. |

In the default mode, a stream of multiple top-level JSON values (whitespace- or
newline-separated, i.e. NDJSON/JSON Lines) is framed independently, with no whole-stream
buffering. Pure-jq and planner-required interleaved semantic queries execute and emit one
frame at a time. Supported three-phase semantic queries instead group complete frames into
configured byte-budgeted windows, resolve each window once, then emit their frames in
original order. The window retains only its frames plus bounded framing lookahead; a record
larger than the budget forms one oversized window and is never split. This still allows
inputs larger than available memory.

For a supported semantic query, `--stream` selects that same per-frame inline behavior
instead of default windows so a result need not wait for a later frame. It trades
window-wide batching and cross-frame pre-resolve deduplication for low latency, without
changing cache identity or the run-global `--max-calls` cap. See
[Process NDJSON](../../how-to/process-ndjson/) for selection guidance and examples.

## Output formatting

| Mode | Flag | Behavior |
|---|---|---|
| Pretty JSON (default) | — | Indented, human-readable JSON. |
| Compact JSON | `-c`, `--compact-output` | Single-line JSON, no extra whitespace. |
| Raw output | `-r`, `--raw-output` | String results are written without surrounding quotes; non-string results are unaffected. |

`-c` and `-r` can be combined, and both may be combined with any input mode.

## Examples

```bash
# Single JSON value
printf '{"a":1}\n' | ajq -c '.a + 1'
# 2

# NDJSON, processed independently per frame
printf '{"a":1}\n{"a":2}\n' | ajq -c '.a, (.a + 10)'
# 1
# 11
# 2
# 12

# Null input — build output from scratch
printf '{"ignored":true}\n' | ajq -n -c '{generated: true}'
# {"generated":true}

# Raw input + raw output
printf 'error: disk full\n' | ajq -R -r 'ascii_upcase'
# ERROR: DISK FULL
```

## Related

- [How to process an NDJSON stream](../../how-to/process-ndjson/) — task-oriented recipes.
- [Command-line interface](../cli/) — the complete flag list.
