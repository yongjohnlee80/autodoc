# Chapter 7: Frontmatter Schemas and Validation

Markdown documents in an engineering knowledge base carry structured metadata—document type, lifecycle status, creation date, author, supersession links, and PR verdicts—inside their leading YAML frontmatter block (`---`). Without a formal schema, frontmatter quickly drifts: one author writes `status: done`, another writes `status: completed`, and a third forgets `created` altogether, breaking automated queries.

AutoDoc (`core/schema`) solves metadata drift without turning your text editor into a rigid database form:

- **Declarative YAML Schemas (`_schema/frontmatter.yaml`):** Supports both flat (`version: 1`) and discriminated polymorphic (`version: 2`) workspace schemas up to `256 KiB` and `256` declared fields (`core/schema/schema.go`).
- **Non-Blocking Live Diagnostics:** Validation runs continuously as you type in the TUI/GUI editor and on every file change in the indexer. Problems appear in a live diagnostic banner above the page and in `index.status` (`diagnosed`), **never blocking a file save** or excluding a document's prose from search. The editor checks a file against its own workspace's schema: a file left open from another workspace is never judged by the schema of the workspace in use.
- **Automatic Search Facet Registration:** Every field declared in the workspace schema is automatically indexed as a typed search facet (`type:adr`, `verdict:approved`, `-status:superseded`).

![Schema version 2 inheritance hierarchy from BaseFrontmatter to discriminated document types](schema-v2-inheritance.mmd)

---

## 1. Schema `version: 1` (Flat) vs. Schema `version: 2` (Discriminated Types)

A workspace binds its schema file via `Go › Manage workspaces… › Edit…` or the `workspace.set_schema` RPC verb. The form suggests `<root>/.autodoc/schema.yaml`; a knowledge base typically uses `_schema/frontmatter.yaml`.

### Schema `version: 1`: Flat Workspace Metadata

A `version: 1` schema validates every Markdown file in the workspace against a single flat `frontmatter:` field map. It is ideal for homogeneous note collections or runbook repositories:

```yaml
version: 1
strict: false          # true: report fields the schema does not declare
frontmatter:
  type:    {type: string, enum: [note, adr, review], required: true}
  status:  {type: string, enum: [draft, active, archived], default: active}
  tags:    {type: list, item_type: string}
  created: {type: date}          # YYYY-MM-DD, or an RFC 3339 time (its date is the facet)
  count:   {type: integer}
```

The types are `string`, `integer`, `number`, `boolean`, `date` and `list` (of `item_type`, strings by default; a lone value is a list of one). A required field has no default.

A fuller one, with a writer order and stricter fields:

```yaml
# .autodoc/schema.yaml (Schema Version 1)
version: 1
strict: true
order: [type, status, created, tags, abstract]
frontmatter:
  type:     {type: string, required: true, enum: [note, runbook, postmortem]}
  status:   {type: string, enum: [draft, active, archived], default: draft}
  created:  {type: date, required: true}
  tags:     {type: list, item_type: string, required: true}
  abstract: {type: string, required: true}
  severity: {type: integer, enum: [1, 2, 3]}
```

### Schema `version: 2`: Polymorphic Document Types (`discriminator: type`)

In a multi-artifact engineering knowledge base, an Architecture Decision Record (`adr`) requires `number` and allows `status: [proposed, accepted, rejected, superseded, deprecated]`, whereas a Code Review (`review`) requires `reviewer` and `subject` and restricts `verdict` to `[approved, approved_with_notes, change_requested, rejected, commented]`.

Schema `version: 2` expresses this cleanly in a single file using three top-level sections:

1. **`discriminator: type`:** Names a `string` field of `common` whose value selects the document's specialized type definition.
2. **`common:` (Base Shared Fields):** Declares fields shared by every document in the workspace (`type`, `status`, `created`, `tags`, `abstract`, `updated`, `author`, `sources`, `related`, `supersedes`, `superseded_by`, `amends`).
3. **`types.<name>:` (Per-Type Specializations):** Declares fields specific to each discriminator value and optionally narrows shared `common` fields.

**Version 2 declares fields by document type**, still in one file that validates every document. The `discriminator` names a string field of `common`; a file is checked against `common` merged with the `types` entry its value names:

```yaml
version: 2
strict: false
discriminator: type            # the field whose value picks the type
common:                        # every document
  type:    {type: string, required: true}
  status:  {type: string, enum: [draft, proposed, accepted, approved], default: draft}
  tags:    {type: list}
types:                         # merged over common when type has this value
  adr:
    number:  {type: string, required: true}
    status:  {type: string, enum: [proposed, accepted], default: proposed}
  review:
    verdict: {type: string, enum: [approved, rejected], required: true}
  note:                        # common alone
```

- A file whose type the schema does not name is diagnosed (`unknown type "memo"`, rule `unknown_type`) and checked against `common` alone.
- `strict: true` reports the fields the file's own type does not declare: a `verdict` on an ADR.

A production knowledge base's version 2 schema:

```yaml
# _schema/frontmatter.yaml (Schema Version 2)
version: 2
strict: false
discriminator: type
common:
  type:          {type: string, required: true, enum: [adr, adr-rationale, convention, playbook, reference, synthesis, review, pr, note, about, kb]}
  status:        {type: string, required: true}
  created:       {type: date, required: true}
  tags:          {type: list, required: true}
  abstract:      {type: string, required: true}
  title:         {type: string}
  updated:       {type: date}
  author:        {type: string}
  sources:       {type: list}
  related:       {type: list}
  supersedes:    {type: list}
  superseded_by: {type: list}
  amends:        {type: list}
types:
  adr:
    status:      {type: string, enum: [proposed, accepted, rejected, superseded, deprecated]}
    number:      {type: string, required: true}
    revision:    {type: integer}
    decided_by:  {type: string}
  review:
    status:      {type: string, enum: [in-progress, completed]}
    reviewer:    {type: string, required: true}
    subject:     {type: string, required: true}
    verdict:     {type: string, enum: [approved, approved_with_notes, change_requested, rejected, commented]}
    pr:          {type: integer}
    head:        {type: string}
  synthesis:
    status:      {type: string, enum: [draft, final, superseded]}
    kind:        {type: string, enum: [analysis, investigation, lesson, bug-fix, benchmark, incident, design]}
  convention:
    status:      {type: string, enum: [draft, active, retired]}
    area:        {type: string}
    applies_to:  {type: list}
```

### Type Narrowing Invariants in `core/schema`

When `core/schema/schema.go` merges `types.<name>` over `common`, it enforces strict covariance rules at schema load time:

- **Aliases:** a field with an `enum` may name legacy spellings of its values, `aliases: {changes_requested: change_requested}`. An alias validates with no diagnostic and is faceted as its value, so `verdict:change_requested` finds both spellings. An alias can't be a value of the enum itself, and each names one of its values.
- **Exempt Paths:** a top-level `exempt` list of root-relative globs, as `include` and `exclude` take them, names files the schema doesn't judge. Such a file keeps its facets and has no diagnostics. The KB schema AutoDoc ships exempts `raw/**` (immutable source), `_templates/**` (AutoDoc's own), `archive/**` (retired), and two tools' directories, `.auto-agents/**` and `.junie/**`.
- **Narrowing Only:** A specialized type may narrow a `common` field's `enum` to a subset, promote an optional `common` field to `required: true`, or supply a type-specific `default`.
- **No Widening or Type Mutation:** A type may **never** change a field's scalar `type` (for example, `number: integer` in `adr` and `number: string` in `pr` is rejected with the offending line number) or add enum values disallowed by `common`.
- **Safe Schema Reloads:** Editing `_schema/frontmatter.yaml` immediately re-validates all workspace documents without re-embedding their text vectors. If an edit introduces a syntax error or violates a narrowing rule, AutoDoc reports the line number in `workspace.list` (`schema.error`, `schema.line`) and keeps the last valid schema active until you fix the file.

![Distribution of documents across Schema v2 types in a production engineering KB](kb-document-type-distribution.chart.json)

---

## 2. Field Types, Constraints, and Diagnostic Rules

AutoDoc resolves frontmatter using the YAML 1.2 Core schema (`golib/yaml`) and checks every field against its declared type and constraints.

![Complete reference of frontmatter schema field types, constraints, and diagnostic rule codes](schema-field-types-reference.csv)

### Notable Type Behaviors

- **Automatic List Promotion:** For any field declared as `type: list`, a single scalar in YAML (`tags: sqlite`) is automatically treated as a one-element list (`["sqlite"]`) for both validation and facet indexing.
- **Structured Map Lists (`item_type: map`):** A `list` field can declare `item_type: map` with an `items:` schema map to validate nested object arrays inside frontmatter.
- **Canonical Writer Ordering (`order`):** When a schema declares a top-level `order: [type, status, created, ...]` list, AutoDoc's frontmatter writer emits keys in that exact sequence.
- **Defaults Answer Facets Without Touching Disk:** When an optional field declares `default: active`, documents omitting that field answer `status:active` in facet queries without AutoDoc ever rewriting your Markdown files on disk.

---

## 3. Live Editor Banner and Workspace-Wide Validation (`doc.validate` & `index.documents`)

### Non-Blocking Live Editor Banner

A task in a todo store is not a document type, so the workspace's schema never checks it: the todo format does, the way `todos.list` reads it. A task the format can't read has the diagnostic `todo-malformed`, and each entry of its `errors[]` (an `adr` that names no file, say) has `todo-<code>` on the field it names. Its facets are the format's own fields, so `status:open` and `priority:high` filter tasks.

When you open or edit a Markdown file in the TUI or GUI (`autodoc --ui` / `--gui`), AutoDoc validates the buffer's YAML frontmatter on every keystroke against the workspace's active schema. If a required field is missing or an enum value is misspelled (`status: aproved`), a diagnostic banner appears directly above the top ruler showing the line number, field name, and rule violation—while allowing `Ctrl+S` saves at any time.

### Pre-Flight Validation via `doc.validate`

AI agents and CI hooks can validate draft Markdown content before or after writing to disk by calling `doc.validate`. Because `doc.validate` accepts raw `content` bytes, you can check unsaved buffers:

```sh
# Validate an unsaved ADR draft against workspace "kb"'s v2 schema
autodoc --call doc.validate '[
  "kb",
  "adrs/0220-schema-facets.md",
  "---\ntype: adr\nstatus:invalid_state\ncreated: 2026-10-17\ntags: [kb]\n---\n# ADR 0220\n"
]'
```

Response:

```json
{
  "diagnostics": [
    {
      "field": "status",
      "line": 3,
      "rule": "enum",
      "message": "status must be one of: proposed, accepted, rejected, superseded, deprecated"
    },
    {
      "field": "abstract",
      "line": 1,
      "rule": "required",
      "message": "missing required field: abstract"
    },
    {
      "field": "number",
      "line": 1,
      "rule": "required",
      "message": "missing required field: number"
    }
  ]
}
```

### Auditing All Diagnosed Documents Across a Workspace

To audit an entire knowledge base for frontmatter drift, check `diagnosed` in `index.status` and list the offending documents with `index.documents` passing `"diagnosed": true` (or `"missing": ["abstract"]`):

```sh
# 1. Attach or update the workspace's frontmatter schema
autodoc --call workspace.set_schema '["kb", "_schema/frontmatter.yaml"]'

# 2. Check total diagnosed document count in index.status
autodoc --call index.status '["kb"]'

# 3. List every document in "kb" that has one or more schema diagnostics
autodoc --call index.documents '[
  "kb",
  {
    "diagnosed": true,
    "sort": "updated",
    "limit": 50
  }
]'

# 4. Find documents missing specific optional fields (e.g. "author" or "updated")
autodoc --call index.documents '[
  "kb",
  {
    "missing": ["author", "updated"],
    "paths": ["adrs"]
  }
]'
```

**Paging.** A listing is answered a page at a time: `more` says whether documents follow, and `next` is where the following page starts. Pass `next` back unread as `after`, with the same `sort` and filters, until `more` is false. From protocol 35, `next` is an opaque cursor, a string (`""` when no more follow), for every session whatever protocol it declared: the listing orders and filters in the store and resumes after the last document it returned, so a page costs the same at the end of a large workspace as at its start, and documents added or removed before it don't shift the pages after it. A cursor is good only for the sort and filters it came from; another one, or an integer offset (what `next` was before protocol 35), is refused as invalid params.

---

## 4. How Schema Fields Become Indexed Search Facets

In `core/schema/schema.go:68-71`, `Schema.Fields` constructs the ordered union of `common` and every `types.<name>` map. The search engine (`core/index`) registers this union as the workspace's facet vocabulary (`Schema.Declared(name)`):

1. **Zero Extra Configuration:** The moment you add `verdict: {type: string, enum: [approved, rejected]}` under `types.review` in `_schema/frontmatter.yaml`, both `verdict:approved` inline query tokens and `{"facets": {"verdict": ["approved"]}}` RPC filters become active across the workspace.
2. **Type-Aware Normalization:** Integer fields (`pr: 249` or `pr: 0xf9`), boolean flags, and ISO/RFC-3339 dates (`created: 2026-10-17`) are normalized to their canonical typed representation at index time and query parse time.
3. **Scoped Type Matching:** Filtering by a type-specific field such as `verdict:approved` naturally narrows results to documents whose discriminator type declares and populates `verdict`.

---

[← Previous: Chapter 06: Configuring Embeddings & Rerankers](./06-configuring-embeddings-and-rerankers.md) | [Index](./README.md) | [Next: Chapter 08: Knowledge Graphs & ADR Lifecycle →](./08-knowledge-graphs-and-adr-lifecycle.md)

