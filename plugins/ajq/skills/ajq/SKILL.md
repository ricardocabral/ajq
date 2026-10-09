---
name: ajq
description: Route JSON or NDJSON semantic filtering, semantic grep, and bounded fixed-label classification tasks to ajq. Use when matching meaning needs an explicit model boundary; do not use for ordinary structural jq or unsupported general extraction or redaction.
---

# ajq

Use ajq when a JSON or NDJSON task needs fuzzy semantic matching or assigning a
value to a small, fixed set of labels. Keep ordinary jq work in jq: it remains
byte-deterministic and never contacts an AI backend.

## Prerequisites

This skill requires shell access and an installed `ajq` executable. Check with
`command -v ajq` before running the workflow. If shell access or the executable
is unavailable, explain what is missing and link the
[installation guide](https://ricardocabral.github.io/ajq/docs/how-to/install/).
Do not report a command as executed when you could not run it.

## Before choosing ajq

- Use ordinary jq for exact fields, structural transforms, arithmetic, sorting,
  grouping, and exact predicates.
- Do not use ajq for general semantic extraction or redaction. Those standalone
  operations are not supported.
- Keep model use visible. Never introduce a semantic operator or backend without
  saying so to the user.

## Safe workflow

1. Discover the installed contract before composing a query:

   ```bash
   ajq capabilities --json
   ```

2. Build and check the structural jq pipeline first. It needs no backend:

   ```bash
   printf '[{"id":1,"msg":"refund requested"}]' \
     | ajq -c '.[] | {id, msg}'
   ```

3. Check the semantic query shape with the deterministic, no-network mock
   backend. Mock validates command and split-execution shape; it does not judge
   production semantic quality:

   ```bash
   printf '[{"id":1,"msg":"refund requested"}]' \
     | ajq --backend mock -c '.[] | select(.msg =~ "refund request") | {id, msg}'
   ```

4. Inspect the plan and estimated post-dedup judgements before selecting a real
   backend. `--explain` does not run a provider:

   ```bash
   printf '[{"msg":"refund requested"},{"msg":"profile updated"}]' \
     | ajq --explain '.[] | select(.msg =~ "refund request") | .msg'
   ```

5. Once the user has authorized model use, choose an explicit backend and finite `--max-calls`
   cap. Use `--stats` to report the actual work. Never put an API key in a
   command, prompt, or repository file:

   ```bash
   printf '[{"msg":"refund requested"}]' \
     | ajq --backend local --max-calls 10 --stats \
         -c '.[] | select(.msg =~ "refund request") | .msg'
   ```

For bounded classification, supply the labels in the query. This NDJSON example
uses the same no-network mock backend:

```bash
printf '{"id":1,"text":"billing question"}\n{"id":2,"text":"bug report"}\n' \
  | ajq --backend mock -c '{id, route: sem_classify(.text; "billing"; "bug"; "feature")}'
```

Report the actual output and relevant plan or stats. Identify mock results as
execution-shape checks, not real semantic judgments. If a command fails, report
the error and correct the query or explain the missing prerequisite.

Use `--no-cache` for sensitive or one-off values when cache reads and writes
are inappropriate. For backend-specific setup and data-handling controls, see
the [agent-safe workflow](https://ricardocabral.github.io/ajq/docs/how-to/agent-safe-semantic-workflow/)
and [privacy policy](https://ricardocabral.github.io/ajq/privacy/).
