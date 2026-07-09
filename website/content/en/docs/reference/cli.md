---
title: "Command-line interface"
linkTitle: "CLI"
weight: 1
description: >
  ajq invocation, global flags, subcommands, and exit codes.
---

## Synopsis

```text
ajq [query] [flags]
ajq [command]
```

ajq reads input from standard input, applies one jq/semantic `query`, and writes results
to standard output. Errors are written to standard error and produce a non-zero exit code.

`query` is a single argument. ajq desugars `=~`/`!~` to `sem_match(...)`, parses with
[gojq](https://github.com/itchyny/gojq), and executes through the selected backend only
when semantic operators require one.

## Global flags

| Flag | Short | Description |
|---|---|---|
| `--backend <name>` | | Select the semantic backend: `anthropic`, `local`, `mock`, `ollama`, `openai`, or `openrouter`. |
| `--base-url <url>` | | Set the HTTP base URL for backends that use one (`local`, `ollama`, `openai`, `openrouter`). For `openai`/`openrouter`, custom URLs must use HTTPS unless the host is loopback (`127.0.0.1`, `localhost`, or `[::1]`). |
| `--cloud` | | Select the Anthropic cloud backend; equivalent to `--backend anthropic`. |
| `--compact-output` | `-c` | Emit compact JSON output. |
| `--exit-status` | `-e` | Set the exit status based on the last output value. |
| `--explain` | | Print the deterministic/semantic execution plan and exit without executing the query. |
| `--max-calls <N>` | | Maximum post-dedup backend judgements before aborting; `0` means unlimited. Paid backends default to `100`; local/Ollama/mock default to unlimited. |
| `--model <id-or-alias>` | | Semantic model id or alias for the selected backend. |
| `--no-cache` | | Disable persistent on-disk judgement cache reads and writes for this run. |
| `--null-input` | `-n` | Use a single `null` input value instead of reading stdin. |
| `--raw-input` | `-R` | Read each input line as a string. |
| `--raw-output` | `-r` | Emit strings without JSON quoting. |
| `--stats` | | Print run statistics to stderr after a successful run. |
| `--version` | `-v` | Print the version and exit. |
| `--help` | `-h` | Print usage and exit. |

See [Input and output modes](../io-modes/) for framing and formatting details, [Backends](../backends/)
for backend-specific defaults, and [Configuration](../configuration/) for env/config
precedence.

## Subcommands

| Command | Description |
|---|---|
| `ajq cache status` | Print persistent judgement cache location, entry count, and bytes. |
| `ajq cache clear` | Delete persistent judgement cache entries and report what was freed. |
| `ajq daemon status` | Print the warm local daemon status. |
| `ajq daemon stop` | Stop the local daemon if running; idempotent. |
| `ajq models list` | Print the pinned local model catalog with active/installed markers, sizes, and RAM notes. |
| `ajq models pull <name>` | Download a checksum-pinned catalog model into the ajq cache. |
| `ajq models use <name>` | Persist `model = "<name>"` to config after verifying the model is installed. |
| `ajq provision` | Download or locate the local `llama-server` engine and default GGUF model. |
| `ajq provision --check` | Report provisioning status and exit non-zero if assets are missing, without downloading. |

The root help also includes Cobra's generated `completion` and `help` commands.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. With `--exit-status`, also means the last output value was truthy. |
| `1` | With `--exit-status`: the last output value was `false` or `null`. Otherwise a general error. |
| `4` | With `--exit-status`: the query produced no output. |
| non-zero | A parse, runtime, configuration, backend, provisioning, or I/O error occurred. |

The `--exit-status` codes match jq's convention.

## Behavior notes

- If no semantic operators are present, ajq constructs no backend, makes no network calls,
  and spawns no daemon.
- The managed `local` backend starts `llama-server` on loopback with a per-daemon
  `--api-key`; ajq stores the bearer key beside the daemon PID file in the cache
  directory with `0600` permissions and sends it only to the managed daemon.
- A healthy process already listening on the default managed local address is treated as
  a port conflict unless it has ajq's managed PID/key files. To use a server you started
  yourself, pass `--base-url` (or set `AJQ_BASE_URL`/`base_url`); that explicit server is
  trusted by user intent and is not given ajq's managed daemon key.
- `--cloud` with a conflicting explicit `--backend` is a CLI error.
- `--backend openai` and `--backend openrouter` send API keys on each request, so custom
  `--base-url` values must be `https://`; `http://` is accepted only for loopback
  OpenAI-compatible proxies such as `127.0.0.1`, `localhost`, or `[::1]`.
- `--explain` on a pure-jq query validates the query and prints a byte-stable pure-jq
  report without reading stdin.
- `--explain` on a semantic query prints the static plan and, when valid stdin is supplied,
  mock-path estimates without contacting a real backend.
- `--stats` writes only the summary to stderr; query output remains on stdout.

## Related

- [Backends](../backends/) — backend defaults and required settings.
- [Configuration](../configuration/) — config/env/flag precedence.
- [`--explain` output](../explain-output/) — plan report fields.
