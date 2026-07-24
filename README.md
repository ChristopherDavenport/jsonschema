# jsonschema

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
| `anyOf` / `allOf` / `not` | ✅ | partial |
| `if` / `then` / `else` | ✅ | ❌ |
| `dependentSchemas` | ✅ | ❌ |
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
### Generator limitations

The generated `Validate` mirrors the engine for type, presence, scalar/array
assertions, `format`, `uniqueItems`, `dependentRequired`, `oneOf`/`anyOf`, and
nested validation. It does **not yet** enforce the in-place conditional and
combinator keywords — `if`/`then`/`else`, `dependentSchemas`, `not`, and
`allOf` — because faithfully mirroring an arbitrary subschema against a nominal
Go type is substantially harder than the value-level checks. When a schema uses
one of these, the generator still emits the types and a `Validate` method, and
adds a `NOTE` comment above that method.

Until generated enforcement lands (a likely approach is a small hybrid that
defers just these keywords to the embedded schema + runtime engine), validate
such documents with the runtime engine for full coverage:

```go
s, _ := jsonschema.Compile(schemaBytes)
err := s.Validate(decodedInstance)
```

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
