# core/schema

A workspace's frontmatter schema (ADR 0212 §5): the fields its Markdown files declare, their types,
and which are required. One validator serves the daemon, which indexes typed facets and
diagnostics, and the editor, which checks unsaved text, so the two never disagree. Validation never
rewrites a file. Version 1 declares every field under `frontmatter`; version 2 declares `common`
fields and per-type fields picked by a `discriminator` (ADR 1791209945 §4.3). The file format is in
the package comment (`doc.go`).

## Loading a schema

| Name | What |
| --- | --- |
| `Parse(src)` | reads a schema file into a `*Schema`, or an `*Error` |
| `Schema` | a parsed, valid schema: `Version`, `Strict`, `Fields` (every declared field, in order), `Discriminator` (`""` in version 1) |
| `Field` | one declared field: `Name`, `Type`, `ItemType`, `Required`, `Enum`, `Default`, `Managed`, `Items` (a map item's fields), `Line` |
| `Type` | a field's type: `String`, `Integer`, `Number`, `Boolean`, `Date`, `List`, and `Map` (an `item_type` only) |
| `Schema.Field(name)` | the declaration of `name`, and whether it is declared |
| `Schema.Declared(name)` | whether the schema declares `name`; a nil schema declares nothing |
| `Schema.Order()` | the order a writer emits fields in, a copy; nil when the schema gives none |
| `Version`, `MinVersion` | the newest (2) and oldest (1) schema version this build reads |
| `MaxSize`, `MaxFields` | the largest schema file (256 KiB) and the most fields it may declare (256) |
| `Error` | a schema file that is not a valid schema: `Line` (1-based; 0 for none) and `Msg` |
| `ErrTooLarge` | the `*Error` for a schema file over `MaxSize` |

## Validating

| Name | What |
| --- | --- |
| `Schema.ValidateFile(src)` | checks a Markdown file's frontmatter, parsing the file as the indexer does |
| `Schema.Validate(fm, present)` | checks frontmatter text (the lines between the fences); `present` says the file has any |

A nil `*Schema` still reports frontmatter that is not well-formed YAML or not a mapping.

## Results and diagnostics

| Name | What |
| --- | --- |
| `Result` | `Facets` and `Diagnostics`, in source order |
| `Diagnostic` | one problem: `Field`, `Line` (the file's, 1-based), `Rule`, `Message` |
| `RuleYAML` | the frontmatter is not well-formed YAML; nothing in it is checked |
| `RuleMapping` | the frontmatter is not a mapping of fields |
| `RuleRequired` | a required field is missing or empty |
| `RuleType` | a value is not of its declared type |
| `RuleEnum` | a value is not one its enum allows |
| `RuleUnknown` | a strict schema does not declare the field |
| `RuleUnknownType` | a version 2 schema names no such type; the document is checked against common alone |

## Facets

| Name | What |
| --- | --- |
| `Facet` | one typed value of a declared field, what a `field:value` filter matches; a list has one per item |
| `Schema.FacetValue(field, text)` | a filter's text read as the facet value it matches, or why the field's type cannot hold it |
| `ParseDate(s)` | a frontmatter date as a `date` field admits it: `YYYY-MM-DD` or RFC 3339 |

`Parse` and `ParseDate` are safe for concurrent use. No method changes a `*Schema`, so any number
of goroutines may validate against one, as long as none writes its exported fields (`Fields`, and
a `Field`'s `Enum`, `Default` and `Items`, which share the Schema's arrays). Never change
`ErrTooLarge`.
