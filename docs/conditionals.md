# Conditional and combinator keywords in generated code

This note records why `if`/`then`/`else`, `dependentSchemas`, `not`, and `allOf`
are handled the way they are in the code generator, and the tradeoff between
JSON Schema conformance and idiomatic Go.

## The core tension

Most JSON Schema keywords map cleanly onto a typed Go struct:

- `type`, `format`, `minLength`, `minimum`, `enum` → a check on a field.
- `required` → presence enforcement in `UnmarshalJSON`.
- `properties`, `$ref` → field types.
- `oneOf`/`anyOf` → a sealed interface with variant dispatch.

These are *value-shaped*: the check reads a field and compares it. Generated
code stays readable and looks hand-written.

The in-place conditional and combinator keywords are different. They apply an
**arbitrary subschema** to the **whole instance** and combine the boolean
result:

- `if S` — does the value match `S`? Then apply `then`, else `else`.
- `not S` — the value must *not* match `S`.
- `dependentSchemas: {p: S}` — if property `p` is present, the value must match `S`.
- `allOf: [A, B]` — the value must match every one of `A`, `B`.

To mirror `if S` inline we would have to answer "does this typed value satisfy
`S`?" for an arbitrary `S` — i.e. emit a boolean predicate that runs the full
validation algorithm over the typed value and returns a `bool` instead of an
`error`. That is the validation engine, re-emitted as generated code, once per
anonymous subschema.

## How idiomatic is inline mirroring, keyword by keyword?

| Keyword | Faithful inline form | Divergence | Status |
|---|---|---|---|
| `allOf` of object schemas | struct embedding: `type X struct { A; B }` | **Low** | **inline** |
| `if` = `const`/`enum` string discriminator, `then`/`else` add `required` | a Go `if`/`else` on that field | **Low** | **inline** |
| general `if` / `then` / `else` | a `satisfiesIf() bool` predicate running full validation | **High** | fallback |
| general `not` | a `matches() bool` predicate, negated | **High** | fallback |
| `dependentSchemas` | a predicate for the dependent subschema | **High** | fallback |

The two low-divergence rows are now emitted inline (see below). The high-
divergence cases share one property: they need the answer to "does an arbitrary
value match an arbitrary subschema?" There is no nominal Go type that represents
that question, so the generated code would become a compiled interpreter —
verbose, hard to read, and offering nothing over just *calling* the interpreter.
Those stay on the engine fallback by design.

## What we do

The two low-divergence cases are emitted inline as idiomatic Go:

- **`allOf`** of object schemas becomes struct embedding. `type X struct { A; B }`
  promotes both parts' fields; `UnmarshalJSON` decodes each part from the full
  object (so each part enforces its own `required`), and `Validate` calls each
  part's `Validate`.
- **A string-discriminator `if`** (`{"if": {"properties": {"kind": {"const":
  "secret"}}}, "then": {"required": ["value"]}}`) becomes a plain conditional:
  `if x.Kind == nil || *x.Kind == "secret" { if x.Value == nil { … } }`, matching
  the spec's "`properties` does not require presence" semantics.

For everything else, we refuse to emit the high-divergence code and instead offer
two honest modes, selected per generation:

1. **Default** — generate the type and an inline `Validate` for the keywords that
   mirror cleanly. Emit a `NOTE` comment when a type uses an unmirrored keyword.
   The package depends only on the standard library and `xvalid`. The cost is
   that `Validate` is *incomplete* for those types (documented, not silent — the
   `NOTE` says so).

2. **`-engine-fallback`** — for any type that uses an unmirrored keyword,
   `Validate` marshals the value and validates it against the embedded schema via
   the runtime engine. Correctness is complete; the *types* stay idiomatic; only
   `Validate` changes to a delegating call. The cost is a dependency on the
   `jsonschema` package, an embedded copy of the schema document, and a marshal
   round-trip per call.

Compare the two for `{"if": {"properties": {"kind": {"const": "secret"}}}, "then": {"required": ["value"]}}`:

```go
// Default: clean, dependency-light, but does not enforce if/then.
func (x *Item) Validate() error { return nil } // + a NOTE comment

// -engine-fallback: clean type, delegating Validate, full conformance.
func (x *Item) Validate() error { return validateAgainstSchema("#", x) }
```

Making the fallback **opt-in** is what makes the feature worth having: the
default stays idiomatic and dependency-light with an honest gap, and users who
need conformance on conditional schemas get it without forcing the engine
dependency on everyone. The runtime engine is always available directly, too
(`jsonschema.Compile(...).Validate(...)`), and passes the test suite at 100%.

## Boundaries of the inline handling

The inline paths are deliberately conservative — when a schema falls outside the
recognized shape, it drops to the default/​fallback behavior rather than emitting
something subtly wrong:

- `allOf` embedding requires every member to be an object schema (or a `$ref` to
  one). Overlapping field names across members marshal per Go's embedding rules
  (ambiguous promoted fields are dropped from marshaling); disjoint compositions
  are the clean, common case.
- The discriminator `if` requires the `if` to be `properties` of `const`/`enum`
  **string** matches and the `then`/`else` to constrain only `required`. A
  numeric discriminator, a nested `then`, or any other keyword falls through to
  the fallback.

Everything else remains on the engine fallback by design.
