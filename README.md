# jsonschema

[![CI](https://github.com/ChristopherDavenport/jsonschema/actions/workflows/ci.yml/badge.svg)](https://github.com/ChristopherDavenport/jsonschema/actions/workflows/ci.yml)

If you consume or produce JSON that has a schema, `jsonschema` gives you two
things the Go ecosystem has been missing at once: a validator that actually
implements the **whole** spec — 100% of the official JSON Schema Test Suite for
draft 2020-12, 2019-09, and draft-07, including `$ref`/`$dynamicRef`, `oneOf`,
`if`/`then`/`else`, `unevaluated*`, `format`, and vocabularies — and a code
generator that turns a schema into **idiomatic Go types** whose `UnmarshalJSON`
and `Validate` enforce that schema for you. Point it at a schema and get structs,
enums, discriminated-union interfaces, embedded compositions, and typed
constraints, with no reflection, no hand-written validation, and no third-party
runtime dependency in the code it emits. Reach for the validator when you work
with dynamic data, the generator when you want compile-time types — or both, over
the same schema, backed by the same engine so they never disagree.

## Contents

- [Install](#install)
- [Runtime validation](#runtime-validation)
- [Code generation, from simple to complex](#code-generation-from-simple-to-complex)
- [Using the generated code](#using-the-generated-code)
- [The `x-go` extension](#the-x-go-extension)
- [Conditional and combinator keywords](#conditional-and-combinator-keywords)
- [Conformance](#conformance)
- [Coverage vs. omissis/go-jsonschema](#coverage-vs-omissisgo-jsonschema)
- [Package layout](#package-layout)

## Install

```sh
# the library (validator + generator API)
go get github.com/ChristopherDavenport/jsonschema

# the generator CLI
go install github.com/ChristopherDavenport/jsonschema/cmd/jsonschema-gen@latest
```

## Runtime validation

Compile a schema once, validate many decoded values. Instances are the shapes
`encoding/json` produces (`nil`, `bool`, `float64`/`json.Number`, `string`,
`[]any`, `map[string]any`); decode with `UseNumber` so numbers compare exactly.

```go
import (
	"bytes"
	"encoding/json"

	"github.com/ChristopherDavenport/jsonschema"
)

sch, err := jsonschema.Compile(schemaBytes)
if err != nil {
	return err
}

dec := json.NewDecoder(bytes.NewReader(dataBytes))
dec.UseNumber()
var v any
if err := dec.Decode(&v); err != nil {
	return err
}

if err := sch.Validate(v); err != nil {
	// err describes every failure, with JSON-Pointer instance locations.
}
```

For multi-document schemas, remote references, or format assertion, use a
`Compiler`:

```go
c := jsonschema.NewCompiler().AssertFormat(true)
_ = c.AddResource("https://example.com/root.json", rootBytes)
_ = c.AddResource("https://example.com/defs.json", defsBytes) // resolves a $ref
sch, err := c.Compile("https://example.com/root.json")
```

`format` is annotation-only by default (per spec); `AssertFormat(true)` turns it
into an assertion. `RegisterMetaSchemas()` bundles the official meta-schemas so a
document can validate itself against its `$schema`.

## Code generation, from simple to complex

Each example below is the generated output of `jsonschema-gen` (lightly trimmed —
repeated `UnmarshalJSON` boilerplate is elided where a prior example already
shows it, along with the godoc comment on each declaration). Every generated
type, method, and constant carries a godoc comment — the schema's `description`
(or `title`) when present, a sensible default otherwise — so `go doc` and
pkg.go.dev render usefully. Wire the generator into your build with `go:generate`:

```go
//go:generate jsonschema-gen -package user -root User -o user.gen.go user.schema.json
```

### 1. Objects and required fields

The foundation: an object becomes a struct; optional fields become pointers with
`omitempty`; required fields are enforced at decode time by a generated
`UnmarshalJSON` (since `encoding/json` cannot otherwise tell absent from zero).

```json
{
  "type": "object",
  "required": ["id", "email"],
  "properties": {
    "id":     { "type": "integer" },
    "email":  { "type": "string" },
    "active": { "type": "boolean" }
  }
}
```

```go
type User struct {
	Active *bool  `json:"active,omitempty"`
	Email  string `json:"email"`
	ID     int64  `json:"id"`
}

func (x *User) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, k := range []string{"email", "id"} {
		if _, ok := raw[k]; !ok {
			return fmt.Errorf("missing required property %q", k)
		}
	}
	type shadow struct {
		Active *bool  `json:"active,omitempty"`
		Email  string `json:"email"`
		ID     int64  `json:"id"`
	}
	var sh shadow
	if err := json.Unmarshal(data, &sh); err != nil {
		return err
	}
	x.Active, x.Email, x.ID = sh.Active, sh.Email, sh.ID
	return nil
}

func (x *User) Validate() error { return nil }
```

### 2. Constraints, formats, and enums

Assertions become inline checks in `Validate`; an `enum` becomes a named type
with typed constants and its own `Validate`. `-assert-format` emits `format`
checks that call the dependency-free `xvalid` helpers.

```json
{
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":   { "type": "string", "format": "uuid" },
    "name": { "type": "string", "minLength": 1, "maxLength": 80 },
    "age":  { "type": "integer", "minimum": 0, "maximum": 130 },
    "role": { "enum": ["admin", "user", "guest"] }
  }
}
```

```go
type Account struct {
	Age  *int64       `json:"age,omitempty"`
	ID   string       `json:"id"`
	Name string       `json:"name"`
	Role *AccountRole `json:"role,omitempty"`
}

// (UnmarshalJSON enforces the required id and name, as in example 1.)

func (x *Account) Validate() error {
	if x.Age != nil {
		if *x.Age < int64(0) {
			return fmt.Errorf("age: below minimum")
		}
		if *x.Age > int64(130) {
			return fmt.Errorf("age: above maximum")
		}
	}
	if ok, _ := xvalid.CheckFormat("uuid", x.ID); !ok {
		return fmt.Errorf("id: invalid uuid")
	}
	if utf8.RuneCountInString(x.Name) < 1 {
		return fmt.Errorf("name: too short")
	}
	if utf8.RuneCountInString(x.Name) > 80 {
		return fmt.Errorf("name: too long")
	}
	if x.Role != nil {
		if err := x.Role.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type AccountRole string

const (
	AccountRoleAdmin AccountRole = "admin"
	AccountRoleUser  AccountRole = "user"
	AccountRoleGuest AccountRole = "guest"
)

func (x AccountRole) Validate() error {
	switch x {
	case AccountRoleAdmin, AccountRoleUser, AccountRoleGuest:
		return nil
	}
	return fmt.Errorf("AccountRole: invalid value %v", x)
}
```

### 3. Arrays, maps, and references

`items` → a slice; `uniqueItems` → a dedup check; `additionalProperties` of a
type → a `map[string]T` dictionary; a `$ref` → the named type it points at, whose
`Validate` is called through the parent. A `pattern` compiles once into a
package-level variable.

```json
{
  "type": "object",
  "properties": {
    "tags":    { "type": "array", "items": {"type": "string"}, "uniqueItems": true },
    "labels":  { "type": "object", "additionalProperties": {"type": "string"} },
    "address": { "$ref": "#/$defs/address" }
  },
  "$defs": {
    "address": {
      "type": "object",
      "required": ["street"],
      "properties": {
        "street": {"type": "string"},
        "zip":    {"type": "string", "pattern": "^[0-9]{5}$"}
      }
    }
  }
}
```

```go
type Resource struct {
	Address *Address          `json:"address,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Tags    []string          `json:"tags,omitempty"`
}

func (x *Resource) Validate() error {
	if x.Address != nil {
		if err := x.Address.Validate(); err != nil {
			return err
		}
	}
	{
		arr := x.Tags
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr); j++ {
				if reflect.DeepEqual(arr[i], arr[j]) {
					return fmt.Errorf("tags: items are not unique")
				}
			}
		}
	}
	return nil
}

// Address is generated too, with its own required-enforcing UnmarshalJSON and:
func (x *Address) Validate() error {
	if x.Zip != nil {
		if !pattern0.MatchString(*x.Zip) {
			return fmt.Errorf("zip: does not match pattern")
		}
	}
	return nil
}

var pattern0 = xvalid.MustCompilePattern("^[0-9]{5}$")
```

### 4. `oneOf` — discriminated unions as interfaces

`oneOf`/`anyOf` become a **sealed marker interface** with a generated variant
type per branch and an `Unmarshal<Name>` that selects the matching one (and
enforces `oneOf`'s exactly-one rule). The parent decodes the field as raw JSON
and routes it through that dispatcher — so `encoding/json`, which cannot populate
an interface on its own, just works.

```json
{
  "type": "object",
  "required": ["payment"],
  "properties": {
    "payment": { "oneOf": [ {"$ref": "#/$defs/card"}, {"$ref": "#/$defs/bank"} ] }
  },
  "$defs": {
    "card": {"type": "object", "required": ["cardNumber"], "properties": {"cardNumber": {"type": "string"}}},
    "bank": {"type": "object", "required": ["iban"], "properties": {"iban": {"type": "string"}}}
  }
}
```

```go
type Order struct {
	Payment OrderPayment `json:"payment"`
}

type OrderPayment interface{ isOrderPayment() }

type Card struct {
	CardNumber string `json:"cardNumber"`
}
type Bank struct {
	Iban string `json:"iban"`
}

func (_ Card) isOrderPayment() {}
func (_ Bank) isOrderPayment() {}

func UnmarshalOrderPayment(data []byte) (OrderPayment, error) {
	var matches []OrderPayment
	{
		var v0 Card
		if err := json.Unmarshal(data, &v0); err == nil {
			if val, ok := any(&v0).(interface{ Validate() error }); !ok || val.Validate() == nil {
				matches = append(matches, &v0)
			}
		}
	}
	{
		var v1 Bank
		if err := json.Unmarshal(data, &v1); err == nil {
			if val, ok := any(&v1).(interface{ Validate() error }); !ok || val.Validate() == nil {
				matches = append(matches, &v1)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("OrderPayment: value matches no variant")
	default:
		return nil, fmt.Errorf("OrderPayment: value matches %d variants, want exactly one", len(matches))
	}
}

func (x *Order) UnmarshalJSON(data []byte) error {
	// ... required check ...
	type shadow struct {
		Payment json.RawMessage `json:"payment"`
	}
	var sh shadow
	if err := json.Unmarshal(data, &sh); err != nil {
		return err
	}
	if len(sh.Payment) > 0 {
		v, err := UnmarshalOrderPayment(sh.Payment)
		if err != nil {
			return err
		}
		x.Payment = v
	}
	return nil
}
```

Consuming it is a type switch:

```go
var o Order
_ = json.Unmarshal(data, &o)
switch p := o.Payment.(type) {
case *Card:
	useCard(p)
case *Bank:
	useBank(p)
}
```

### 5. `allOf` — composition as struct embedding

An `allOf` whose every member is generated as a struct — an object schema with
declared properties, directly or through a `$ref` — becomes Go struct embedding.
Each part decodes from the full object (so it enforces its own `required`), and
the composite `Validate` delegates to each part.

```json
{
  "allOf": [ {"$ref": "#/$defs/base"}, {"$ref": "#/$defs/audit"} ],
  "$defs": {
    "base":  {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}}},
    "audit": {"type": "object", "required": ["createdBy"], "properties": {"createdBy": {"type": "string"}}}
  }
}
```

```go
type Record struct {
	Base
	Audit
}

func (x *Record) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &x.Base); err != nil { // enforces Base's required id
		return err
	}
	if err := json.Unmarshal(data, &x.Audit); err != nil { // enforces Audit's required createdBy
		return err
	}
	return nil
}

func (x *Record) Validate() error {
	if err := x.Base.Validate(); err != nil {
		return err
	}
	if err := x.Audit.Validate(); err != nil {
		return err
	}
	return nil
}
```

`x.ID` and `x.CreatedBy` are promoted, so `Record` reads like one flat struct.
Members that would not be structs — a free-form dictionary, a scalar, a union —
are refused rather than embedded, since embedding those would change the JSON
shape; that composition falls to one of the two modes below.

### 6. `if`/`then`/`else` — conditional requirements

When the `if` is a string-value discriminator and the branches add `required`,
the condition becomes a plain Go conditional (matching the spec's rule that
`properties` alone does not require presence).

```json
{
  "type": "object",
  "properties": {
    "kind":   {"type": "string"},
    "secret": {"type": "string"}
  },
  "if":   {"properties": {"kind": {"const": "private"}}},
  "then": {"required": ["secret"]}
}
```

```go
func (x *Item) Validate() error {
	if x.Kind == nil || *x.Kind == "private" {
		if x.Secret == nil {
			return fmt.Errorf("\"secret\" is required here")
		}
	}
	return nil
}
```

The recognized shape is deliberately narrow: the tag is matched by a plain-string
`const`/`enum` and nothing else, its field's Go type is `string`, and the branches
carry only `required` naming declared properties. Anything outside that — a
numeric tag, an `enum`-typed field, a `then` with its own constraints — is refused
rather than half-mirrored, and falls to one of the two modes below, where the
generated `NOTE` names the rule it missed.

## Using the generated code

Generate into a package inside your module and import it like any other. The
`-package` flag names the package; the output file goes wherever you point `-o`:

```go
//go:generate jsonschema-gen -package schema -root Order -o schema/order.gen.go order.schema.json
```

```go
import "example.com/app/schema"
```

**Decode and validate.** The generated `UnmarshalJSON` enforces required
properties as it decodes; call `Validate` for everything else. The two are
separate on purpose — decoding tells you the shape is right, `Validate` tells you
the values are.

```go
var u schema.User
if err := json.Unmarshal(data, &u); err != nil {
	// e.g. missing required "email"
}
if err := u.Validate(); err != nil {
	// e.g. a constraint violation
}
```

**Construct values yourself.** The types are ordinary structs — build them with
literals. Optional fields are pointers, enums are typed constants, and `Validate`
/ `json.Marshal` work on values you make, not just ones you decode.

```go
name := "Ada"
role := schema.AccountRoleAdmin // typed enum constant
acct := schema.Account{
	ID:   "550e8400-e29b-41d4-a716-446655440000",
	Name: name,
	Role: &role, // optional field -> pointer
}
_ = acct.Validate() // <nil>
b, _ := json.Marshal(acct)
// {"id":"550e8400-e29b-41d4-a716-446655440000","name":"Ada","role":"admin"}
```

**Set a `oneOf` field.** Assign a concrete variant to the interface field — that
is all the marker interface asks of you. It marshals as its underlying object and
decodes back to the same concrete type, which you recover with a type switch. (If
you already hold the raw JSON for just that field, `UnmarshalOrderPayment(raw)`
returns the variant directly.)

```go
order := schema.Order{Payment: &schema.Card{CardNumber: "4111111111111111"}}
b, _ := json.Marshal(order)
// {"payment":{"cardNumber":"4111111111111111"}}

var back schema.Order
_ = json.Unmarshal(b, &back)
switch p := back.Payment.(type) {
case *schema.Card:
	useCard(p)
case *schema.Bank:
	useBank(p)
}
```

**Fill an `allOf` composition.** Embedded parts promote their fields, so you set
them directly and marshal to one flat object.

```go
var rec schema.Record
rec.ID = "r-1"        // promoted from Base
rec.CreatedBy = "ada" // promoted from Audit
_ = rec.Validate()
b, _ := json.Marshal(rec)
// {"id":"r-1","createdBy":"ada"}
```

All output comments above are the real results of running this code against the
generated types.

## The `x-go` extension

Steer generation per-schema, without leaving the document:

```json
{
  "type": "object",
  "properties": {
    "createdAt": { "type": "string", "format": "date-time", "x-go": {"type": "time.Time", "import": "time"} }
  }
}
```

`x-go` fields: `type` (Go type used verbatim), `import` (its package path), `name`
(override the generated identifier), `pointer` (force or forbid a pointer), and
`extraTags` (extra `key:value` struct tags, e.g. for another library).

## Conditional and combinator keywords

`allOf` (example 5) and string-discriminator `if`/`then`/`else` (example 6) are
generated as idiomatic Go. The remaining in-place keywords — general
`if`/`then`/`else`, `dependentSchemas`, and `not` — would require emitting an
arbitrary subschema as a boolean predicate, which amounts to re-implementing the
validator as generated code. For those there are two modes:

- **Default:** the type and a `Validate` are still generated, the keyword is not
  enforced, and a `NOTE` comment names exactly what was skipped — on the
  `Validate` method for a struct, on the type declaration for an alias, enum, or
  union interface. Output depends only on the standard library and `xvalid`.

  ```go
  // Validate reports whether x satisfies the constraints this type mirrors inline.
  //
  // NOTE: it does not enforce:
  //   - if/then/else: then requires "value", which is not a declared property
  //
  // Regenerate with -engine-fallback, or validate with the jsonschema engine.
  ```

  It lists only what is actually unenforced: a schema whose discriminator `if` is
  mirrored but that also uses `not` gets a `NOTE` naming `not` alone. The last
  line also says whether `-engine-fallback` would *resolve* the item — it cannot
  when the keyword constrains a property the Go type does not declare, since the
  delegating `Validate` sees only the marshaled value:

  ```go
  // -engine-fallback cannot enforce "not" here: it validates the marshaled value
  // of this type, which never carries "ghost". Validate the original document
  // with the jsonschema engine instead.
  ```
- **`-engine-fallback`:** `Validate` for such a type delegates to the embedded
  schema evaluated by the runtime engine — engine-grade conformance, at the cost
  of a dependency on the `jsonschema` package and a marshal round-trip. Where the
  round trip cannot carry what the keyword constrains, the delegating `Validate`
  carries a `NOTE` saying so instead of claiming conformance it cannot deliver.

The generator never half-mirrors a keyword: a schema that falls outside a
recognized shape gets the `NOTE`, not a `Validate` that quietly skips part of it.
Two limits are worth knowing before you rely on this: the fallback only rewrites
`Validate` on **struct** types, and it validates the marshaled Go value rather
than the original document. If you use either mode — or if a conditional you
expected to be enforced isn't — read
[docs/conditionals.md](./docs/conditionals.md): it gives the exact shapes each
inline path recognizes, the near misses, the two remaining gaps, and recipes for
working around them. Either way, the runtime engine is always available for
complete coverage.

The CLI takes these values as flags or from a YAML config (`-config gen.yaml`,
with flags overriding it):

```yaml
package: person
rootName: Person
assertFormat: true
input: person.schema.json
output: person.gen.go
```

## Conformance

The validator passes the official
[JSON Schema Test Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite)
required set at 100%, enforced as a CI gate:

| Draft | Passing |
|---|---|
| 2020-12 | 1299 / 1299 (100%) |
| 2019-09 | 1259 / 1259 (100%) |
| draft-07 | 927 / 927 (100%) |

This includes full `$dynamicRef`/`$recursiveRef` dynamic-scope resolution,
offline meta-schema self-validation, and vocabulary-aware keyword gating. See
[CONFORMANCE.md](./CONFORMANCE.md) for details and the one inherent limitation
(RE2 lacks lookaround/backreferences, shared by every Go regex validator).

## Coverage vs. omissis/go-jsonschema

Legend: ✅ validated & generated · ⚙️ typed only · ❌ ignored/absent.

| Feature | `jsonschema` (this) | omissis/go-jsonschema |
|---|:--:|:--:|
| `type`, `properties`, `required`, `enum` | ✅ | ✅ |
| `const`, numeric, string constraints | ✅ | ✅ |
| `uniqueItems`, `dependentRequired` | ✅ | ❌ |
| `min`/`maxProperties`, `min`/`maxContains` | ✅ | ❌ |
| `oneOf` (interface + variants) | ✅ | ❌ |
| `allOf` (struct embedding) | ✅ (embedding inline; rest via fallback) | partial |
| `anyOf` / `not` | ✅ | partial |
| `if` / `then` / `else` | ✅ (discriminator inline; rest via fallback) | ❌ |
| `dependentSchemas` | ✅ (validate; generate via fallback) | ❌ |
| `patternProperties`, `propertyNames` | ✅ | ❌ |
| `prefixItems` / tuples | ✅ | ❌ |
| `unevaluatedProperties` / `unevaluatedItems` | ✅ | ❌ |
| Boolean subschemas (`true`/`false`) | ✅ | partial |
| `format` assertion (uuid/email/uri/…) | ✅ (opt-in) | ❌ |
| nested `$ref`, remote refs, recursion | ✅ | partial |
| `$dynamicRef` / `$recursiveRef` | ✅ | ❌ |
| meta-schema self-validation (bundled) | ✅ | ❌ |

## Package layout

| Package | Purpose |
|---|---|
| `jsonschema` | Compile a schema and validate decoded `any` (the runtime validator + conformance target). |
| `jsonschema/ir` | Internal schema model — a `Bool \| Object` sum type carrying every 2020-12 keyword. |
| `jsonschema/dialect` | Detect `$schema` and normalize 2019-09 / draft-07 onto the canonical model. |
| `jsonschema/loader` | Load JSON, resolve `$id`/`$ref`/`$anchor`/`$defs`, remote refs, cycles. |
| `jsonschema/gotype` | Map the model to a Go type model. |
| `jsonschema/gen` | Emit Go source: types + `Validate` + custom (un)marshalers. |
| `jsonschema/xvalid` | Zero-dependency runtime helpers the generated code calls. |
| `jsonschema/metaschema` | The bundled official meta-schemas. |
| `cmd/jsonschema-gen` | The command-line generator. |

## License

MIT — see [LICENSE](./LICENSE). Bundled third-party content is attributed in
[NOTICE](./NOTICE).
