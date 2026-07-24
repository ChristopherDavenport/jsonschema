# jsonschema

[![CI](https://github.com/ChristopherDavenport/jsonschema/actions/workflows/ci.yml/badge.svg)](https://github.com/ChristopherDavenport/jsonschema/actions/workflows/ci.yml)

A complete [JSON Schema](https://json-schema.org/) toolkit for Go: a spec-conformant
**runtime validator** plus a **code generator** that emits idiomatic, self-validating Go
types.

It parses draft **2020-12** (canonical), **2019-09**, and **draft-07**, normalizing all of
them to a single internal model. Correctness is anchored to the official
[JSON Schema Test Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite).

## Why

The existing Go generators produce plain structs and quietly ignore most of the interesting
spec — `oneOf`, `if`/`then`/`else`, `dependentRequired`, `patternProperties`, `uniqueItems`,
tuples, `const`, and real `format` validation all silently no-op. The mature runtime
validators, on the other hand, only validate generic decoded `any`, never your typed structs.

`jsonschema` closes that gap. One validation **engine** is the conformance-anchored source of
truth; the code generator emits typed `Validate()` methods that mirror it keyword-for-keyword,
so generated code cannot drift from the spec.

## Layout

| Package | Purpose |
|---|---|
| `jsonschema` | Compile a schema and validate decoded `any` (the runtime validator + conformance target). |
| `jsonschema/ir` | Internal schema model — a `Bool \| Object` sum type carrying every 2020-12 keyword. |
| `jsonschema/dialect` | Detect `$schema` and normalize 2019-09 / draft-07 onto the canonical model. |
| `jsonschema/loader` | Load JSON/YAML, resolve `$id`/`$ref`/`$anchor`/`$defs`, remote refs, cycles. |
| `jsonschema/gotype` | Map the model to a Go type model. |
| `jsonschema/gen` | Emit Go source: types + `Validate()` + custom (un)marshalers. |
| `jsonschema/xvalid` | Zero-dependency runtime helpers the generated code calls. |
| `cmd/jsonschema-gen` | The command-line generator. |

## Coverage vs. omissis/go-jsonschema

Legend: ✅ validated · ⚙️ typed only · ❌ ignored/absent.

| Feature | `jsonschema` (this) | omissis/go-jsonschema |
|---|:--:|:--:|
| `type`, `properties`, `required`, `enum` | ✅ | ✅ |
| `const` | ✅ | ✅ (string/num/bool) |
| numeric (`minimum`…`multipleOf`) | ✅ | ✅ |
| string (`minLength`/`maxLength`/`pattern`) | ✅ | ✅ |
| `minItems`/`maxItems` | ✅ | ✅ |
| `uniqueItems` | ✅ (validate + **generate**) | ❌ |
| `minProperties`/`maxProperties` | ✅ | ❌ |
| `dependentRequired` | ✅ (validate + **generate**) | ❌ |
| `oneOf` (interface + variants) | ✅ (validate + **generate**) | ❌ |
| `anyOf` / `not` | ✅ | partial |
| `allOf` (validate + **generate** via embedding) | ✅ | partial |
| `if` / `then` / `else` (discriminator generated inline; rest via fallback) | ✅ | ❌ |
| `dependentSchemas` (validate; generate via fallback) | ✅ | ❌ |
| `patternProperties` | ✅ | ❌ |
| `propertyNames` | ✅ | ❌ |
| `prefixItems` / tuples | ✅ (validate + **generate**) | ❌ |
| `contains` / `min`/`maxContains` | ✅ | ❌ |
| `unevaluatedProperties` / `unevaluatedItems` | ✅ | ❌ |
| Boolean subschemas (`true`/`false`) | ✅ | partial |
| `format` assertion (uuid/email/uri/…) | ✅ (opt-in) | ❌ |
| nested `$ref`, remote refs, recursion | ✅ | partial |
| `$dynamicRef` / `$recursiveRef` dynamic scope | ✅ | ❌ |
| meta-schema self-validation (bundled) | ✅ | ❌ |

## Usage

Validate at runtime:

```go
s, _ := jsonschema.Compile(schemaBytes)
err := s.Validate(decodedInstance) // decodedInstance is any from encoding/json
```

Generate self-validating Go types:

```sh
go run ./cmd/jsonschema-gen -package person -root Person -assert-format person.schema.json > person.go
```

Configuration can also come from a YAML file (flags override its values):

```yaml
# gen.yaml
package: person
rootName: Person
assertFormat: true
input: person.schema.json
output: person.go
```

```sh
go run ./cmd/jsonschema-gen -config gen.yaml
```

The generated code depends only on the standard library and this module's
`xvalid` helper package. Each struct gets an `UnmarshalJSON` that enforces
required properties and a `Validate() error` whose inline checks mirror the
validation engine.

## Status

The runtime validator passes the JSON Schema Test Suite required set at **100%**
for 2020-12, 2019-09, and draft-07 — including full `$dynamicRef` resolution,
offline meta-schema self-validation, and vocabulary-aware keyword gating (see
[CONFORMANCE.md](./CONFORMANCE.md)).

The code generator handles objects, enums, `$ref`, scalars, arrays, `format`,
required-field enforcement via `UnmarshalJSON`, nested validation, numeric
bounds/`multipleOf`, `uniqueItems`, `dependentRequired`, `additionalProperties`
dictionaries (`map[string]T`), `prefixItems` tuples (array-shaped
`(Un)MarshalJSON`), the `x-go` type-override extension, and **`oneOf`/`anyOf` as
sealed marker interfaces** with generated variant dispatch (`Unmarshal<Name>`).
### Conditional and combinator keywords

The generated `Validate` mirrors the engine inline for type, presence,
scalar/array assertions, `format`, `uniqueItems`, `dependentRequired`,
`oneOf`/`anyOf`, and nested validation. Two combinator/conditional cases are also
emitted as idiomatic Go:

- **`allOf`** of object schemas → struct embedding (`type X struct { A; B }`),
  with each part enforcing its own `required` and `Validate`.
- **A string-discriminator `if`** (`{"if":{"properties":{"kind":{"const":"x"}}},
  "then":{"required":[…]}}`) → a plain `if x.Kind == … { … }`.

The remaining cases — general `if`/`then`/`else`, `dependentSchemas`, and `not` —
would require re-implementing the validator as generated boolean predicates,
which stops looking like idiomatic Go. For those there are two modes:

- **Default:** the type and a `Validate` method are still generated, but these
  keywords are not enforced; a `NOTE` comment is emitted above the method. The
  generated package depends only on the standard library and `xvalid`.
- **`-engine-fallback`:** for any type that uses one of these keywords, `Validate`
  delegates to the embedded schema evaluated by the runtime engine — full
  conformance, at the cost of a dependency on the `jsonschema` package and a
  marshal round-trip. The generated *types* stay idiomatic; only their `Validate`
  changes. This is opt-in per generation.

Either way, you can always validate with the runtime engine directly:

```go
s, _ := jsonschema.Compile(schemaBytes)
err := s.Validate(decodedInstance)
```

See [docs/conditionals.md](./docs/conditionals.md) for the full design rationale
and the tradeoff analysis.

### The `x-go` extension

Steer generation per-schema without leaving the document:

```json
{
  "type": "string",
  "format": "date-time",
  "x-go": { "type": "time.Time", "import": "time" }
}
```

`x-go` fields: `type` (Go type to use verbatim), `import` (its package path),
`name` (override the generated identifier), `pointer` (force/forbid a pointer),
and `extraTags` (additional `key:value` struct tags).

## License

MIT — see [LICENSE](./LICENSE).
