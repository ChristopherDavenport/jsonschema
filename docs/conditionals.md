# Conditional and combinator keywords in generated code

How the code generator handles `allOf`, `if`/`then`/`else`, `not`, and
`dependentSchemas`: what it emits, exactly which schema shapes it recognizes,
where it stops, and what to do about each case.

Every snippet below is real output of the generator in this repository (verified
against `main` at the commit that introduced this revision). The behavioral
claims — including the traps in [§6](#6-known-gaps-and-traps) — were checked by
generating the schema, compiling the result, and running it.

Related code: `gotype/analyze.go` (`allOfEmbeddable`, `embeddableMember`,
`allOfBlocker`, `buildStruct`) and `gen/emit.go` (`unenforced`, `needsFallback`,
`ifHandled`, `ifBlocker`, `isSimpleStringMatch`, `isRequiredOnly`,
`emitIfThenElse`, `emitDelegatingValidate`, `emitTypeDoc`).
Pinning tests in `gen/gen_test.go`: `TestGeneratedAllOf`,
`TestGeneratedDiscriminator`, `TestGeneratedEngineFallback` for the paths that
are handled, `TestConservativeShapes` for the near misses that must not be.

## 1. Summary

| Keyword | Default (`-engine-fallback` off) | With `-engine-fallback` |
|---|---|---|
| `allOf` of struct-shaped object schemas | struct embedding, each part's `Validate` called | same (inline; flag has no effect) |
| `allOf` with any other member | not enforced, `NOTE` comment | `Validate` delegates to the engine (struct declarations only — [§5.3](#53-what-the-fallback-does-not-cover)) |
| string-discriminator `if`/`then`/`else` ([§4](#4-inline-path-2-string-discriminator-ifthenelse)) | inline `if`/`else` on the tag field | same (inline; flag has no effect) |
| any other `if`/`then`/`else` | not enforced, `NOTE` comment | `Validate` delegates to the engine |
| `not` | not enforced, `NOTE` comment | `Validate` delegates to the engine |
| `dependentSchemas` | not enforced, `NOTE` comment | `Validate` delegates to the engine |
| `dependentRequired` | inline presence checks | same (inline) |
| `oneOf` / `anyOf` | sealed interface + `Unmarshal<T>` dispatcher | same (inline) |

Every declaration that uses one of those four keyword families without enforcing
it carries a `NOTE` — on the `Validate` method for a struct, on the type
declaration otherwise. The engine **fallback**, by contrast, only ever applies to
struct declarations. Read [§5.3](#53-what-the-fallback-does-not-cover) for what
neither mechanism covers.

## 2. The core tension

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
anonymous subschema. There is no nominal Go type for "matches this subschema",
so the output would be a compiled interpreter: verbose, unreviewable, and no
better than *calling* the interpreter.

So two shapes — the two whose faithful Go form is something a person would have
written anyway — are mirrored inline, and everything else is either flagged
(default) or delegated to the engine (opt-in).

| Keyword | Faithful inline form | Divergence | Status |
|---|---|---|---|
| `allOf` of struct-shaped objects | `type X struct { A; B }` | **Low** | inline |
| `if` = `const`/`enum` string tag, `then`/`else` add `required` | a Go `if`/`else` on that field | **Low** | inline |
| general `if` / `then` / `else` | a `satisfiesIf() bool` running full validation | **High** | fallback |
| general `not` | a `matches() bool`, negated | **High** | fallback |
| `dependentSchemas` | a predicate per dependent subschema | **High** | fallback |

## 3. Inline path 1: `allOf` as struct embedding

### Recognition rule

`gotype/analyze.go:allOfEmbeddable` accepts the composition when **all** of:

- `allOf` is non-empty, and
- the schema has no `oneOf` and no `anyOf` (those win and produce an interface), and
- every member — directly or through a `$ref` that resolves within the document —
  is a schema that will be generated as a Go **struct** (`gotype.embeddableMember`):
  an object schema with at least one declared property, carrying no `enum`,
  `const`, `oneOf`, `anyOf`, or `x-go` type override.

The struct requirement matters because only a struct promotes its fields when
embedded. An object schema with no declared properties becomes a `map` alias, and
embedding a named map would give it a JSON name of its own (`{"Dict": {…}}`)
rather than merging its keys into the parent — so such a member disqualifies the
composition instead.

A member whose `$ref` cannot be resolved (e.g. a remote document that was not
supplied) makes the whole composition non-embeddable.

When a member blocks embedding, `allOfBlocker` records which one and why, and the
generated `NOTE` names it: `allOf: member 2 is an object schema with no declared
properties (a dictionary)`, `member 1 is not an object schema`, `member 2 is a
oneOf/anyOf union`, `member 2: $ref "…" does not resolve`, and so on.

### What is emitted

```json
{
  "type": "object", "required": ["own"], "properties": {"own": {"type": "string"}},
  "allOf": [{"$ref": "#/$defs/a"}],
  "$defs": {"a": {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}}}}
}
```

```go
type Root struct {
	A
	Own string `json:"own"`
}

func (x *Root) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &x.A); err != nil { // each part sees the whole object
		return err
	}
	for _, k := range []string{"own"} { // this schema's own required
		if _, ok := raw[k]; !ok {
			return fmt.Errorf("missing required property %q", k)
		}
	}
	type shadow struct {
		Own string `json:"own"`
	}
	var sh shadow
	if err := json.Unmarshal(data, &sh); err != nil {
		return err
	}
	x.Own = sh.Own
	return nil
}

func (x *Root) Validate() error {
	if err := x.A.Validate(); err != nil {
		return err
	}
	return nil
}
```

Three properties worth naming:

- Each part is decoded from the **full** object, so each part enforces its own
  `required` and fills its own promoted fields. `TestGeneratedAllOf` pins this.
- The parent's own `properties`/`required` coexist with the embedded parts.
- `Validate` calls each part's `Validate`, so the parts' field-level assertions
  (`minLength`, bounds, …) are enforced. Every embeddable member is a struct, so
  every part has one.

`allOf` inside a property works the same way — the property gets its own named
type (`RootRec` for `properties.rec`) which does the embedding.

### Boundaries

| Shape | What happens |
|---|---|
| Two members declaring the same property | compiles, but **breaks JSON marshaling** — see [§6.1](#61-overlapping-allof-members-drop-the-shared-property-from-marshaling) |
| A member that is `{"type":"object"}` with no `properties` (a dictionary) | refused. The parent falls to the default `NOTE` / engine fallback |
| A member that is a scalar, array, `oneOf`, `enum`/`const`, boolean schema, or an `x-go` type | refused, same as above |
| Unresolvable `$ref` member | refused, same as above |

"Falls to the default `NOTE` / engine fallback" means: if the parent schema is
object-shaped it keeps its own fields and gets the usual struct treatment
([§5.1](#51-default-honest-gap-plus-a-note) / [§5.2](#52--engine-fallback-complete-conformance-for-struct-types));
if it is not (e.g. `allOf` of two string schemas), it becomes `type Root any`
carrying a `NOTE` and no `Validate` — see [§5.3](#53-what-the-fallback-does-not-cover).

## 4. Inline path 2: string-discriminator `if`/`then`/`else`

### Recognition rule

`gen/emit.go:ifBlocker` walks these rules in order and returns the first one that
fails; `ifHandled` is "no rule failed". The failing rule is also the text that
lands in the generated `NOTE`, shown in the right-hand column:

1. `if` is an object schema whose **only** keyword is `properties`. Any
   `required`, `type`, `$ref`, `not`, nested `if`, or
   `allOf`/`anyOf`/`oneOf` in the `if` disqualifies it.
   → `if constrains more than properties` (or `if does not constrain any
   property`, `if is a boolean schema`, `then/else with no if`)
2. Every property inside `if.properties` matches a string value and **nothing
   else** (`isSimpleStringMatch`): a string `const` or an all-string `enum`,
   optionally restating `"type": "string"`. A non-string `const`, a mixed-type
   `enum`, both `const` and `enum` together, or any further assertion on the tag
   (`minLength`, `pattern`, `$ref`, `format`, …) disqualifies the shape. Pure
   annotations (`title`, `description`, `default`, `examples`, `$comment`,
   `deprecated`) are ignored, since they never constrain.
   → `if property "kind" is not a plain string const/enum match`
3. Each of those property names exists as a field on the generated struct whose
   Go type is exactly `string` — not a named type, not a slice or map.
   → `if property "kind" is not a declared property` /
   `… is not a plain Go string field`
4. `then` and `else`, when present, constrain **nothing but `required`**, with a
   non-empty list (`isRequiredOnly`).
   → `then constrains more than a non-empty required`
5. Every property named in `then`/`else` `required` exists as a field on the
   struct. A name with no field could not be checked at all, so the whole
   conditional is refused rather than partly enforced.
   → `then requires "value", which is not a declared property`

`then`/`else` may be omitted; `else` alone is fine.

### What is emitted

```json
{
  "type": "object",
  "properties": {
    "kind": {"type": "string"}, "tier": {"type": "string"},
    "value": {"type": "string"}, "note": {"type": "string"}
  },
  "if": {"properties": {"kind": {"enum": ["secret", "token"]}, "tier": {"const": "gold"}}},
  "then": {"required": ["value"]},
  "else": {"required": ["note"]}
}
```

```go
func (x *Root) Validate() error {
	if (x.Kind == nil || *x.Kind == "secret" || *x.Kind == "token") && (x.Tier == nil || *x.Tier == "gold") {
		if x.Value == nil {
			return fmt.Errorf("\"value\" is required here")
		}
	} else {
		if x.Note == nil {
			return fmt.Errorf("\"note\" is required here")
		}
	}
	return nil
}
```

Note the `x.Kind == nil ||` term. `properties` does **not** require presence, so
an absent tag *satisfies* the `if` and the `then` branch applies. The generated
code reproduces that, which is the part hand-written code usually gets wrong.
Multiple tag properties are ANDed; multiple `enum` values are ORed.

When the tag is `required` in the enclosing schema the field is a non-pointer and
the nil term disappears:

```go
// "required": ["kind"] → Kind string
if x.Kind == "secret" {
	if x.Value == nil {
		return fmt.Errorf("\"value\" is required here")
	}
}
```

`if` with no `then` and no `else` is still "handled", and emits a dead branch:

```go
if x.Kind == nil || *x.Kind == "secret" {
	// no additional requirements
}
```

### Near misses — shapes that look handled but are not

Each of these falls out of the inline path and lands on the default `NOTE` (or
the engine fallback when enabled). The `NOTE` itself names the rule that failed,
so this table is mostly background — reach for it when you want to know *why* the
rule exists:

| Schema | Why it is rejected |
|---|---|
| `"if": {"required": ["kind"], "properties": {"kind": {"const": "x"}}}` | `if` carries a keyword other than `properties` (rule 1) |
| `"if": {"properties": {"n": {"const": 1}}}` | non-string tag (rule 2) |
| `"if": {"properties": {"kind": {"const": "x", "minLength": 99}}}` | extra assertion on the tag; mirroring only the comparison would drop it (rule 2) |
| `"properties": {"kind": {"enum": ["secret","public"]}}` + `if` on `kind` | the field's Go type is the generated enum `RootKind`, not `string` (rule 3) — see below |
| `"then": {"properties": {"value": {"minLength": 3}}}` | `then` constrains more than `required` (rule 4) |
| `"then": {}` or `"then": {"required": []}` | `required` is empty (rule 4) |
| `"then": {"required": ["value"]}` where `value` is not in `properties` | no field to check it on (rule 5) |
| `if`/`then` on a schema that is not a struct (scalar root, enum, `oneOf`) | there is no struct to attach checks to; the `NOTE` goes on the type instead ([§5.3](#53-what-the-fallback-does-not-cover)) |

`"if": {"properties": {"kind": {"type": "string", "const": "x"}}}` **is** accepted:
a `"type": "string"` alongside a string `const` restates what the `const` already
implies, so nothing is lost.

The enum row is the most common surprise, because writing the tag as an `enum` is
the natural thing to do:

```json
{"properties": {"kind": {"enum": ["secret", "public"]}, "value": {"type": "string"}},
 "if": {"properties": {"kind": {"const": "secret"}}}, "then": {"required": ["value"]}}
```

`kind` becomes `*RootKind` (a named string enum with its own `Validate`), rule 3
fails, and the conditional is not enforced inline — you get the `NOTE`. Options,
in order of preference: model the whole thing as `oneOf`
([recipe R1](#r1-preferred-rewrite-a-tagged-union-as-oneof)), declare the tag as
plain `{"type": "string"}` (losing the enum constants), or enable
`-engine-fallback`.

### `x-go: {"pointer": false}` on a tag field changes the semantics

Forcing a non-pointer on an *optional* tag makes absence indistinguishable from
`""`, and the generated condition drops its nil term:

```go
// "kind": {"type": "string", "x-go": {"pointer": false}}
if x.Kind == "secret" { … }   // absent kind no longer enters the branch
```

Per the spec an absent `kind` *does* satisfy `if`, so this diverges. Either leave
the tag as a pointer, or make it `required` (where non-pointer is faithful).

## 5. The two modes for everything else

### 5.1 Default: honest gap plus a `NOTE`

The type and an inline `Validate` are still generated for every keyword that
mirrors cleanly; the unmirrored keyword is skipped and the method carries a
comment that names it — and, for the shapes with several recognition rules, which
rule was missed:

```go
// Validate reports whether x satisfies the constraints this type mirrors inline.
//
// NOTE: it does not enforce:
//   - if/then/else: then requires "value", which is not a declared property
//
// Regenerate with -engine-fallback, or validate with the jsonschema engine.
func (x *Root) Validate() error {
	return nil
}
```

The list is per keyword, so a schema using two of them says so:

```go
// NOTE: it does not enforce:
//   - not
//   - dependentSchemas ("kind", "size")
```

A declaration that has no mirrored `Validate` to carry the warning — an alias, an
enum, a `oneOf`/`anyOf` interface — gets it on the type instead, with the tail
adjusted because the flag cannot help there:

```go
// Root is generated from its JSON Schema.
//
// NOTE: nothing generated for this type enforces:
//   - allOf: member 2 is an object schema with no declared properties (a dictionary)
//
// -engine-fallback does not cover a non-struct type, so validate values of
// this type with the jsonschema engine.
type Root any
```

The `NOTE` lists only what is actually unenforced, never the whole family. A
schema with both a discriminator `if` and a `not` gets the inline discriminator
checks *and* a `NOTE` that mentions `not` alone:

```go
// NOTE: it does not enforce:
//   - not
func (x *Root) Validate() error {
	if x.Kind == nil || *x.Kind == "secret" { // the if/then IS enforced
		if x.Value == nil {
			return fmt.Errorf("\"value\" is required here")
		}
	}
	return nil // only the `not` is missing
}
```

The messages come from `emitter.unenforced`, which is also what `needsFallback`
is defined in terms of — so a keyword can never be silently skipped without
appearing in the list, and the `if`/`then`/`else` diagnosis (`ifBlocker`) is the
same function that decides whether to mirror it.

Output in this mode depends only on the standard library and `xvalid`.

### 5.2 `-engine-fallback`: complete conformance for struct types

`Validate` becomes a delegating call, and one shared helper block is emitted
per file:

```go
func (x *Item) Validate() error {
	return validateAgainstSchema("https://example.com/s.json#/$defs/item", x)
}

const schemaBaseURI = "https://example.com/s.json"

var schemaDocument = "{\"type\":\"object\",…}"   // the input document, verbatim
var schemaCompiler = sync.OnceValue(func() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	_ = c.AddResource(schemaBaseURI, []byte(schemaDocument))
	return c
})

func validateAgainstSchema(location string, v any) error {
	s, err := schemaCompiler().Compile(location)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	return s.Validate(decoded)
}
```

Facts about this mode:

- **Per-declaration, not per-file.** Only types that actually need it delegate;
  every other type keeps its inline `Validate`. Types on an inline path
  (discriminator `if`, embeddable `allOf`) are *unaffected* by the flag.
- **The location is the subschema's canonical URI**, so a `$defs` entry
  validates against exactly its own subschema (`…#/$defs/item`).
- **`$id` is honored.** A document with `"$id": "https://other.example/x.json"`
  generated with `-base-uri https://example.com/s.json` delegates to
  `https://other.example/x.json#` while registering the document under the
  retrieval URI; both resolve, so this works.
- **The compiler is built once** (`sync.OnceValue`) and reused.
- **Only the input document is embedded.** A `$ref` to a *remote* document is
  not bundled, and the failure surfaces at runtime the first time the engine
  evaluates it:
  `/a: loader: cannot resolve $ref "https://remote.example/r.json#"`. There is no
  exported hook to add resources to the generated compiler — bundle remote refs
  into one document before generating ([recipe R6](#r6-bundle-remote-refs-before-generating)).
- **Cost:** a dependency on the `jsonschema` package, the schema text in your
  binary, and a marshal + decode round trip per `Validate` call.

### 5.3 What the fallback does *not* cover

This is the sharpest edge in the whole feature, so it is worth stating plainly:
**`-engine-fallback` only rewrites `Validate` methods of struct declarations,
and only for `not`, `dependentSchemas`, non-discriminator `if`/`then`/`else`, and
non-embeddable `allOf`.**

Two consequences:

1. **Non-struct declarations are flagged but never enforced.** If a schema's Go
   type is an alias, an enum, or a `oneOf`/`anyOf` interface, there is no
   mirrored `Validate` to rewrite, so the flag changes nothing; the keyword is
   reported by the `NOTE` on the type declaration and otherwise dropped. `allOf`
   of two string schemas is the smallest example — with or without the flag, the
   entire output is:

   ```go
   // Root is generated from its JSON Schema.
   //
   // NOTE: nothing generated for this type enforces:
   //   - allOf: member 1 is not an object schema
   //
   // -engine-fallback does not cover a non-struct type, so validate values of
   // this type with the jsonschema engine.
   type Root any
   ```

   Same for `not` on a scalar root, or an `if`/`then` alongside a `oneOf`. Reach
   for [recipe R4](#r4-gate-the-boundary-with-the-engine-on-raw-bytes) for these.

2. **Keywords outside those four families are never covered either.** The
   generator does not currently enforce `patternProperties`, `propertyNames`,
   `minProperties`/`maxProperties`, `contains`/`minContains`/`maxContains`,
   `unevaluatedProperties`/`unevaluatedItems`, or the `content*` keywords, and
   `needsFallback` does not consider them — so they produce neither a `NOTE` nor
   a delegating `Validate`. A schema whose only constraint is `minProperties: 2`
   generates a `Validate` that returns `nil` and claims to satisfy the schema.

For anything in this list, validate with the engine at the boundary
([recipe R4](#r4-gate-the-boundary-with-the-engine-on-raw-bytes)). The engine
itself is at 100% on the required suite; only the *generated mirror* has these
gaps.

## 6. Known gaps and traps

Two remain. Both are current behavior, verified by generating, compiling, and
running the output.

### 6.1 Overlapping `allOf` members drop the shared property from marshaling

```json
{"allOf": [{"$ref": "#/$defs/a"}, {"$ref": "#/$defs/b"}],
 "$defs": {"a": {"type": "object", "required": ["id"], "properties": {"id": {"type":"string"}, "x": {"type":"string"}}},
           "b": {"type": "object", "properties": {"id": {"type":"string"}, "y": {"type":"string"}}}}}
```

`Root` embeds both `A` and `B`, each with an `ID` field at depth 1. Go's
embedding rules make the promoted name ambiguous: `x.ID` does not compile (you
must write `x.A.ID`), and `encoding/json` **omits the field entirely** from
output. Decoding works — both `A.ID` and `B.ID` are filled — but the generated
type does not round-trip through its own marshaler:

```
unmarshal {"id":"1","x":"xx","y":"yy"}  →  A.ID="1", B.ID="1"
marshal                                 →  {"x":"xx","y":"yy"}          // id gone
re-unmarshal that output                →  error: missing required property "id"
```

Under `-engine-fallback` this is worse than cosmetic: validation marshals the
value first, so the dropped property looks absent to the engine and any
`required`/`dependentSchemas` mentioning it fails.

**Workarounds:** keep `allOf` members disjoint (the clean, common case — hoist
shared properties into the parent schema instead of repeating them in members);
or rename one side with `x-go: {"name": …}`; or don't compose with `allOf` here.

### 6.2 `-engine-fallback` validates the Go value, not the document

The delegating `Validate` marshals `x` and validates the result. Anything the Go
type cannot represent, or that `encoding/json` alters, is invisible to — or
misrepresented for — the engine.

**Unknown properties are dropped**, even with `additionalProperties: true`,
because a struct with declared `properties` has nowhere to keep them:

```json
{"type": "object", "properties": {"a": {"type": "string"}},
 "additionalProperties": true, "not": {"required": ["ghost"]}}
```

```
input {"a":"x","ghost":1}  →  marshals to {"a":"x"}  →  Validate() == nil
```

The document violates `not`, and `Validate` says it is fine. This is a **false
accept**.

**`omitempty` erases empty-but-present values.** Every optional field is tagged
`omitempty`, so an empty slice or map marshals away:

```json
{"type": "object", "properties": {"tags": {"type":"array","items":{"type":"string"}}, "kind": {"type":"string"}},
 "dependentSchemas": {"kind": {"required": ["tags"]}}}
```

```
input {"kind":"k","tags":[]}  →  marshals to {"kind":"k"}
Validate() → missing required property "tags"      // false reject
```

Workaround: drop `omitempty` on that field by overriding the tag through
`x-go`, which replaces the generated `json` tag wholesale:

```json
{"tags": {"type": "array", "items": {"type": "string"}, "x-go": {"extraTags": ["json:tags"]}}}
```

```
input {"kind":"k","tags":[]}  →  marshals to {"kind":"k","tags":[]}  →  Validate() == nil
```

(Optional *scalars* are pointers, so `nil` vs `""` survives correctly; the
problem is specific to slices and maps, which have no pointer wrapper.)

**Numbers are safe.** The helper decodes with `UseNumber()`, so integer/precision
assertions (`multipleOf`, large `int64`) behave. Tuples (`prefixItems`) and
`oneOf` interface fields marshal back to their correct JSON shapes.

Rule of thumb: `-engine-fallback` is exact when the Go type is a faithful
representation of the document — every property declared, no free-form extras,
no ambiguous embedding. When it isn't, validate the raw bytes instead
([recipe R4](#r4-gate-the-boundary-with-the-engine-on-raw-bytes)).

## 7. Recipes

### R1 (preferred): rewrite a tagged union as `oneOf`

Most `if`/`then` schemas are a tagged union written the hard way. Writing it as
`oneOf` of closed variants gets you *more* than the conditional would: real
variant types, exhaustive handling, and full enforcement with no engine
dependency.

```json
{
  "oneOf": [{"$ref": "#/$defs/secret"}, {"$ref": "#/$defs/public"}],
  "$defs": {
    "secret": {"type": "object", "required": ["kind", "value"],
               "properties": {"kind": {"const": "secret"}, "value": {"type": "string"}}},
    "public": {"type": "object", "required": ["kind"],
               "properties": {"kind": {"const": "public"}, "note": {"type": "string"}}}
  }
}
```

```go
type Root interface{ isRoot() }

func UnmarshalRoot(data []byte) (Root, error) // trial-decodes each variant, enforces exactly-one
```

```
{"kind":"secret","value":"s"}  →  *Secret
{"kind":"secret"}              →  error: Root: value matches no variant
{"kind":"public"}              →  *Public
```

The `const` on each `kind` becomes a one-value enum type with its own `Validate`,
which is what makes the dispatcher discriminate correctly.

### R2: keep a conditional on the inline path

If you want to keep `if`/`then`, hold it to the recognized shape
([§4](#recognition-rule-1)). Miss any of these and you get the `NOTE`, not a
half-enforced `Validate`:

- `if` contains only `properties`.
- Each tag property has `const` (string) or `enum` (all strings) and no other
  assertion (`"type": "string"` alongside a `const` is fine).
- The tag field's Go type is plain `string` — so declare it `{"type": "string"}`,
  not an `enum`, and don't override it with `x-go: {"type": …}`.
- Leave the tag as a pointer, or make it `required`.
- `then`/`else` contain only a non-empty `required`.
- Every property named in `then`/`else` `required` is declared in `properties`.

### R3: turn on `-engine-fallback`

```
jsonschema-gen -engine-fallback -base-uri https://example.com/s.json -o schema.gen.go schema.json
```

or in the YAML config:

```yaml
engineFallback: true
baseURI: https://example.com/s.json
```

Right choice when the types are faithful to the document ([§6.2](#62--engine-fallback-validates-the-go-value-not-the-document)) and the schema
is self-contained. Read [§5.3](#53-what-the-fallback-does-not-cover) for what it
still won't cover.

### R4: gate the boundary with the engine on raw bytes

The completely general answer, and the only one immune to the round-trip issues:
validate the incoming document, then decode. This covers every keyword the
engine supports — including the ones no mode of the generator handles
(`minProperties`, `unevaluatedProperties`, …).

```go
//go:embed schema.json
var schemaBytes []byte

type Gate struct{ s *jsonschema.Schema }

func NewGate() (*Gate, error) {
	s, err := jsonschema.Compile(schemaBytes)
	if err != nil {
		return nil, err
	}
	return &Gate{s: s}, nil
}

// Decode validates data against the schema, then unmarshals it into out.
func (g *Gate) Decode(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if err := g.s.Validate(raw); err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
```

```
{"a":"x","ghost":1}  →  /: value must not match the subschema   // caught, unlike §6.2
{"a":"x"}            →  nil
```

Use `jsonschema.NewCompiler()` + `AddResource` per document (and
`AddAndCompile`) when the schema spans multiple files, and `AssertFormat(true)`
if you want `format` enforced.

### R5: add what's missing in your own type

Generated code is a package you import, so wrap it where you need extra rules:

```go
type Item schemagen.Item // or embed it

func (i *Item) Validate() error {
	if err := (*schemagen.Item)(i).Validate(); err != nil {
		return err
	}
	// the part the generator flagged with NOTE, hand-written once:
	if i.Kind != nil && *i.Kind == "secret" && i.Rotation == nil {
		return fmt.Errorf("secret items need a rotation policy")
	}
	return nil
}
```

Reasonable when exactly one type has one unmirrored keyword and you don't want
the engine dependency. Note that generated parent types call the *generated*
child `Validate`, not your wrapper, so wrap at the outermost type you actually
validate.

### R6: bundle remote refs before generating

`-engine-fallback` embeds only the input document. If it references other
documents, inline those subschemas into `$defs` (or generate without the flag and
use [R4](#r4-gate-the-boundary-with-the-engine-on-raw-bytes) with a compiler you
feed every resource). Otherwise the delegating `Validate` fails at runtime with a
`$ref` resolution error rather than a validation error.

## 8. Why it is split this way

Making the engine fallback **opt-in** is what makes the feature worth having: the
default output stays idiomatic and dependency-light with a documented gap, and
users who need conformance on conditional schemas get it without forcing the
engine dependency on everyone. The two inline paths exist because their faithful
Go form is the form a person would have written by hand — struct embedding and an
`if` on a tag field. Everything past that point would be an interpreter in
disguise, and calling the real one is strictly better.

The runtime engine remains available directly for anything the generated mirror
does not cover, and passes the official test suite's required set at 100%.
