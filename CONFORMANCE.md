# Conformance

The validation engine is tested against the official
[JSON Schema Test Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite),
vendored as a git submodule under `third_party/`. Run it with:

```sh
go test -run TestConformance -v ./
# fail the build on any discrepancy:
JSONSCHEMA_STRICT=1 go test -run TestConformance ./
```

## Current pass rates (required test set)

| Draft | Passing |
|---|---|
| 2020-12 | 1299 / 1299 (100%) |
| 2019-09 | 1259 / 1259 (100%) |
| draft-07 | 927 / 927 (100%) |

The `TestConformance` test fails on **any** regression from a full pass. All of
the following are implemented:

- Full `$dynamicRef` / `$recursiveRef` dynamic-scope resolution.
- Offline meta-schema self-validation (meta-schemas bundled under `metaschema/`).
- Vocabulary-aware keyword gating: a dialect whose `$vocabulary` omits the
  validation, applicator, or unevaluated vocabulary has those keywords treated as
  annotations (ignored) rather than assertions.
- An ECMA-262 regex adapter (`xvalid.CompilePattern`) that rewrites the long
  Unicode property names RE2 spells differently (`\p{Letter}` → `\p{L}`).

## Remaining limitation

Go's `regexp` is RE2, so `pattern` constructs that RE2 lacks *entirely* —
lookaround and backreferences — are still unsupported. These do not appear in the
required test set (they live under `optional/`) and are an inherent,
ecosystem-wide limitation of every Go validator. The `optional/format/*` suite is
also not run by default, since `format` is annotation-only unless assertion is
enabled.
