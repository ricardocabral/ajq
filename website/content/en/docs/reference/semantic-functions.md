---
title: "Semantic functions"
linkTitle: "Semantic functions"
weight: 3
description: >
  The shipped semantic vocabulary, =~/!~ sugar, arities, return types, and current limits.
---

Semantic operators are registered as jq functions, so gojq parses them inside ordinary jq
constructs. ajq adds only the `=~` and `!~` surface sugar before parsing.

## Function vocabulary

| Function | Form | Kind | Returns | Execution status |
|---|---|---|---|---|
| `sem_match` | `sem_match(value; "spec")` | predicate | boolean | Shipped |
| `sem_classify` | `sem_classify(value; "a"; "b"; …)` | bounded value | one label from the given labels | Shipped |
| `sem_extract` | `sem_extract(value; "what")` | unbounded value | string | Registered, but standalone three-phase execution currently reports unsupported |
| `sem_score` | `sem_score(value; "spec")` | unbounded value | number | Limited: supported in `sort_by(...)` three-phase placeholder mode and in gated/interleaved fallback contexts |
| `sem_norm` | `sem_norm(value; "canonicalization spec")` | unbounded value | string | Limited: supported in `group_by(...)` three-phase placeholder mode and in gated/interleaved fallback contexts |
| `sem_redact` | `sem_redact(value; "redaction spec")` | unbounded value | string | Registered, but standalone three-phase execution currently reports unsupported |

The **spec** is a literal string in the query. Stream **data** is structurally fenced by
the selected backend's output constraints.

## Kinds

| Kind | Meaning |
|---|---|
| Predicate | Returns `true` or `false`; typically used in `select` or `if`. |
| Bounded value | Returns one of a finite set declared at the call site. |
| Unbounded value | Returns a string or number with no finite output set. These shapes need bounded executor contexts or interleaved fallback; unsupported forms fail loudly. |

## Variadic implicit `.`

Every operator accepts both an explicit value and an implicit one. When the value argument
is omitted, it defaults to `.`:

```jq
sem_match(.field; "spec")   # explicit value
sem_match("spec")           # value defaults to .
```

The implicit form is what makes raw-line mode ergonomic.

## The `=~` and `!~` sugar

| Surface | Desugars to |
|---|---|
| `X =~ "spec"` | `sem_match(X; "spec")` |
| `X !~ "spec"` | `sem_match(X; "spec") | not` |

The desugaring is performed by a jq-aware lexer, not a regular expression.

```jq
.users[] | select(.feedback =~ "angry/frustrated") | .id
# ≡
.users[] | select(sem_match(.feedback; "angry/frustrated")) | .id
```

## Return-type discipline

Each semantic operator has a fixed output type: boolean, enum label, string, or number.
Backends constrain model output with GBNF grammar or provider structured-output/schema
features, so downstream jq receives a value of the expected shape when execution succeeds.

For `sem_classify`, the enum is exactly the label arguments at the call site:

```jq
sem_classify(.text; "billing"; "bug"; "feature")
```

That expression returns one of `"billing"`, `"bug"`, or `"feature"`.

## Scenario matrix

| Scenario | Invocation | Status |
|---|---|---|
| Fuzzy filter | `select(.msg =~ "auth failure")` | Shipped |
| Raw-line fuzzy filter | `select(. =~ "stack trace")` with `-R` | Shipped |
| Routing / labeling | `{route: sem_classify(.text; "billing"; "bug"; "feature")}` | Shipped |
| Semantic sort key | `sort_by(sem_score(.review; "positivity"))` | Limited, supported three-phase placeholder mode |
| Semantic grouping key | `group_by(sem_norm(.company; "canonical name"))` | Limited, supported three-phase placeholder mode |
| Gated semantic score | `select(sem_score(.review; "positivity") > 0.8)` | Limited, uses interleaved fallback |
| Typed extraction | `sem_extract(.raw; "years")` | Registered but currently unsupported in standalone three-phase execution |
| Semantic redaction | `.notes |= sem_redact(.; "PII")` | Registered but currently unsupported in standalone three-phase execution |

`sem_score` and `sem_norm` are not general-purpose enrichment operators in 0.0.1. Use
`sem_score` as a `sort_by(...)` key and `sem_norm` as a `group_by(...)` key when you want
the three-phase executor. When an unbounded value result is used to prune control flow,
such as a score comparison inside `select` or `if`, ajq may choose an interleaved fallback
instead of the harvest/resolve/execute path. That fallback is bounded by the same backend,
cache, and `--max-calls` controls, but its call count is not available as a three-phase
harvest estimate.

## Current grammar scope

There is no custom jq grammar. `@classify(...)`-style `@` forms and infix transform
operators such as `~>` do not parse in gojq and are not shipped. Use the function-core
forms above.

## Related

- [Write a semantic filter](../../how-to/semantic-filter/) — task recipes using shipped forms.
- [Split execution](../../explanation/split-execution/) — why ajq keeps jq as the parser.
