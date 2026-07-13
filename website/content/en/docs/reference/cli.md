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
| `--window-bytes <N>` | | Maximum source bytes per supported three-phase semantic window; positive integer, default `262144`. Has no execution effect for pure-jq or interleaved queries. |
| `--version` | `-v` | Print the version and exit. |
| `--help` | `-h` | Print usage and exit. |

See [Input and output modes](../io-modes/) for framing and formatting details, [Backends](../backends/)
for backend-specific defaults, and [Configuration](../configuration/) for env/config
precedence.

## Help-first agent probe

For machine consumers, begin with `ajq capabilities --json`, before examples
or query execution. It is static introspection: it does not load config or
credentials, construct a backend, start a daemon, inspect/provision assets, or
contact the network. Its versioned `schema_version: "1"` document is the stable
contract for input/output modes, semantic-function availability, backend
defaults, cost/cache/provisioning notes, safety guarantees, and discovery
commands. The plain `ajq capabilities` summary, `ajq_version`, and human
backend descriptions are informational build text; all other documented v1
fields and enum values are stable. Consumers must use `schema_version` as the
contract discriminator and ignore unknown fields and future enum values for
compatible v1 builds.

After capabilities discovery, run `ajq examples` for categorized, copy-pasteable
safe workflows. `ajq examples [topic]` can show one of `pure-jq`,
`semantic-filter`, `explain`, `classification`, or `ndjson`; its semantic
commands explicitly use `--backend mock` and therefore need no model, network,
or API key. It is the source of the detailed snippets; this reference keeps
only the workflow overview. The root help shows the same three safe first workflows:

```bash
# Pure jq remains deterministic and does not construct a backend.
printf '{"users":[{"name":"Ada"}]}' | ajq -r '.users[].name'

# --backend mock is the safe semantic-agent probe: deterministic, no model,
# no network access, and no API key.
printf '[{"id":1,"msg":"please keep this"}]' \
  | ajq --backend mock -c '.[] | select(.msg =~ "keep") | .id'

# Review the semantic plan and estimated backend calls before execution.
printf '[{"msg":"refund demanded"}]' \
  | ajq --backend mock --explain '.[] | select(.msg =~ "angry/frustrated") | .msg'
```

The `mock` backend is appropriate for checking query shape and split-execution
behavior; it is not a substitute for evaluating a query's semantic quality on
a production model. `--explain` exits before query execution and does not
contact a model backend.

Discovery command help also includes safe inspection examples: `ajq provision
--check` reports missing local assets without downloading them, `ajq models
list` shows local model availability, `ajq cache status` inspects the local
judgement cache, and `ajq daemon status` checks daemon state without starting a
model.

## JSON state probes

Coding agents can request versioned local-state documents with command-local
`--json` flags. Every document has `schema_version: "1"`, is one JSON document
on stdout, and has a trailing newline. Consumers should use the schema version,
ignore unknown future fields/enums, and keep stdout even when a readiness check
has a non-zero exit status.

- `ajq models list --json` returns `active` and ordered `models`. `active.state`
  is `catalog`, `path_like`, or `unknown`; `name` occurs only for `catalog` and
  `path` only for `path_like`. Each catalog row contains `name`, `active`,
  `installed`, `filename`, `path`, `size_bytes`, and `ram`.
- `ajq cache status --json` returns `availability` (`available` or
  `unavailable`), `path`, `entries`, and `bytes`. An unavailable local cache
  also has `error: "status_unavailable"` and exits non-zero; it never exposes a
  raw filesystem error.
- `ajq provision --check --json` returns `platform`, `ready`, `engine`,
  `model`, and ordered `actions`. Assets contain identity, `present`, `path`,
  and `source` when present; sources include `override`, `bundle`,
  `legacy_cache`, `path`, `cache`, and `unknown`. Missing readiness produces a
  complete document then exits 1. Actions are engine-first: `provision` runs
  `ajq provision`, and a selected non-default model can add `models_pull` with
  `ajq models pull <name>`. `--json` requires `--check`; `ajq provision --json`
  is rejected and never starts provisioning.

These probes only read local configuration, filesystem, and PATH state. They do
not construct a backend, contact a provider, download assets, or start a daemon.
They intentionally omit credentials, prompts, provider responses, catalog URLs,
and checksums.

## Subcommands

| Command | Description |
|---|---|
| `ajq cache status [--json]` | Print persistent judgement cache status, or its versioned machine-readable probe. |
| `ajq cache clear` | Delete persistent judgement cache entries and report what was freed. |
| `ajq capabilities [--json]` | Print informational static metadata, or the versioned machine-readable capability contract for agents. |
| `ajq daemon status` | Print the warm local daemon status. |
| `ajq examples [topic]` | Print categorized safe examples; semantic examples explicitly use `--backend mock`. |
| `ajq daemon stop` | Stop the local daemon if running; idempotent. |
| `ajq models list [--json]` | Print the pinned local model catalog, or its versioned machine-readable probe. |
| `ajq models pull <name>` | Download a checksum-pinned catalog model into the ajq cache. |
| `ajq models use <name>` | Persist `model = "<name>"` to config after verifying the model is installed. |
| `ajq provision` | Download or locate the local `llama-server` engine and default GGUF model. |
| `ajq provision --check [--json]` | Report provisioning status (or its versioned JSON) and exit non-zero if assets are missing, without downloading. |

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
- `--stats` writes only the summary to stderr; query output remains on stdout. Its window
  fields are `execution_mode` (`pure-jq`, `three-phase-windowed`, or `interleaved`),
  `window_bytes`, `window_count`, and `oversized_window_count`. The three numeric window
  fields are zero outside `three-phase-windowed` mode.
- `--window-bytes` applies only to supported three-phase semantic execution. It forms
  complete-frame windows, never buffers the entire input, and accepts a record larger than
  the budget as a one-frame oversized window.

## Related

- [Backends](../backends/) — backend defaults and required settings.
- [Configuration](../configuration/) — config/env/flag precedence.
- [`--explain` output](../explain-output/) — plan report fields.
