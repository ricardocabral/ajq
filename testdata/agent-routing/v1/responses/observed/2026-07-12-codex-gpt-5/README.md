# Observed paired baseline: 2026-07-12

These are reviewer-normalized records from two fresh Codex GPT-5 sessions. Both
received the `v1` corpus in its stored order, its synthetic input fixtures, and
no filesystem, browser, repository, network, credential, model, or tool
access. The control session received no ajq discovery artifact; the treatment
received only `artifacts/local-guidance.md`.

| Condition | Correct selections | Safe preflights | Passed |
| --- | ---: | ---: | --- |
| `none` | 4/6 | 0/2 | no |
| `local-guidance` | 6/6 | 2/2 | yes |

Neither session proposed a real backend, made an unsupported capability claim,
or produced a false-positive ajq use. The control requested authority rather
than selecting ajq for the fuzzy-filter and bounded-routing tasks; the
local-guidance treatment selected ajq and proposed the required
`capabilities`, `mock`, `explain` preflight.

This is a single paired observation, not a general discovery-rate result.
Evaluation-session transcripts are retained by the runner as
`/root/blind_control` and `/root/blind_local_guidance`; they are intentionally
not copied into this public synthetic corpus. Re-score the records with:

```sh
go run ./cmd/agent-routing-eval \
  -corpus testdata/agent-routing/v1/corpus.json \
  -responses testdata/agent-routing/v1/responses/observed/2026-07-12-codex-gpt-5/local-guidance.json
```

Use `-enforce=false` to inspect the intentionally failing `none.json` control.
