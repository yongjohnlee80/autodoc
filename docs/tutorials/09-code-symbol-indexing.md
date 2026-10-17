# Chapter 9: Code Symbol Indexing

Technical documentation rarely lives apart from the source code it describes. Architecture Decision Records cite Go interfaces (`search.Chunker`), SQL migration playbooks reference DDL tables (`CREATE TABLE document`), and API guides document Protobuf services and TypeScript types.

Rather than treating source files as flat plain text or requiring heavyweight language servers (LSP) to be running for every language, AutoDoc includes a fast, zero-dependency **polyglot code declaration indexer** (`core/symbols` and `golib/search/chunk/code`). It extracts top-level and member declarations across **18 programming and schema languages**, cuts declaration-aligned chunks (`signature + doc comment`), and exposes both structured symbol lookups (`symbols.find`, `symbols.doc`) and hybrid semantic code search.

![Architecture of AutoDoc's polyglot symbol extractor dispatch and SQLite symbol index](symbol-extractor-dispatch.mmd)

---

## 1. Dual-Engine Extraction Architecture (`core/symbols`)

When the workspace indexer encounters a source code file, it routes the file by extension through a two-tier extraction architecture:

### 1. Exact Go AST Parser (`go/token` + `go/ast`)

Because Go's standard library ships a production compiler frontend (`go/parser`, `go/ast`, `go/token`), AutoDoc parses `.go` files into a complete Abstract Syntax Tree:

- **Functions and Methods (`func`, `method`):** Extracts the exact function name, normalized parameter and return signature, 1-based start/end line numbers, attached doc comment (`// Search answers a query...`), and—for methods—the exact receiver type (`*Indexer`, `Store`, `fieldSet`).
- **Types and Interfaces (`struct`, `interface`, `type`):** Captures `type Name struct`, `type Name interface`, and type definitions/aliases, including grouped `type ( ... )` blocks.
- **Constants and Variables (`const`, `var`):** Extracts exported and package-level declarations from both standalone and parenthesized `const ( ... )` / `var ( ... )` blocks.
- **Syntax Error Resilience:** Even if a `.go` file is mid-edit and contains a partial syntax error, `go/parser` recovers partial declarations up to the broken node so the file's existing symbols remain searchable.

### 2. Zero-Dependency Polyglot Line Extractors (17 Languages)

For the remaining 17 languages, AutoDoc runs state-aware, zero-regex-backtracking line scanners that strip block comments and string literals, track brace/indentation nesting scopes, attach preceding doc-comment blocks (`///`, `/** ... */`, `"""`, `--`), and extract declarations in microseconds per file:

- **Systems Languages:** **Rust** (`.rs`), **C / C++** (`.c`, `.h`, `.cc`, `.cpp`, `.hpp`), **Zig** (`.zig`), **Odin** (`.odin`)
- **Application & JVM Languages:** **TypeScript / JavaScript** (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`), **Python** (`.py`), **Java** (`.java`), **Kotlin** (`.kt`, `.kts`), **Swift** (`.swift`), **C#** (`.cs`), **Scala** (`.scala`, `.sc`)
- **Dynamic & Functional Languages:** **Ruby** (`.rb`), **PHP** (`.php`), **Lua** (`.lua`), **Elixir** (`.ex`, `.exs`)
- **Data & Interface Schemas:** **SQL** (`.sql` — `CREATE TABLE`, `CREATE VIEW`, `CREATE FUNCTION`, `CREATE INDEX`, `CREATE TRIGGER`, `CREATE TYPE`) and **Protobuf** (`.proto` — `message`, `service`, `rpc`, `enum`)

![Symbol extraction throughput in symbols per millisecond across Go AST and polyglot extractors](symbol-extraction-throughput.chart.json)

---

## 2. Supported Languages, Extensions, and Symbol Kinds

Every extracted symbol is normalized into a uniform schema row in SQLite (`name`, `kind`, `line`, `line_end`, `signature`, `receiver`, `doc`) and paired with a declaration-scoped search chunk.

![Complete reference of all 18 supported languages, file extensions, and extracted symbol kinds](supported-languages-and-kinds.csv)

### How Declaration Chunking Differs from Markdown Chunking

Unlike Markdown files (which split at `# Heading` boundaries targeting `section_tokens`), code files are chunked **one chunk per declaration**:

- **Breadcrumb:** Set to `<path> › <receiver>.<name>` (or `<path> › <name>`), so every search hit immediately identifies the enclosing struct, class, trait, or Protobuf service.
- **Embedding Text:** Constructed from the declaration's **doc comment + full signature** rather than raw implementation loop bodies. When you run a semantic query like *"how do we reclaim unused model vectors in batches"*, the embedding for `func (s *Store) reclaim(ctx context.Context, fp string, write writeFn)` and its doc comment scores a direct semantic hit.
- **Editor View:** Opening a code hit in the TUI or GUI jumps directly to the declaration's `line_start` with syntax highlighting and read/write editing (with no Markdown rendered view).

---

## 3. Configuring a Workspace to Index Source Trees Alongside Docs

By default, a newly created AutoDoc workspace includes `**/*.md`, `**/*.txt`, `**/*.yaml`, and `**/*.yml` and excludes `.git/**` and `**/node_modules/**`. You can enable code indexing either in the UI or via `workspace.configure` / `workspace.set_patterns`.

### Enabling Code File Types in the TUI / GUI

1. Open `Go › Manage workspaces…` (`Ctrl+W` then `Manage…`), select your workspace, and choose **Edit…** (or open `Options › Editor preferences… › File types…`).
2. Toggle the **Code** row on. AutoDoc automatically adds the code include patterns and appends `vendor/**`, `target/**`, `dist/**`, and `build/**` to the workspace's `exclude` list so third-party dependencies and compiled artifacts are never walked or watched.

### Configuring `include`, `exclude`, and `text_extensions` via RPC

For polyglot repositories where documentation (`docs/`, `adrs/`) lives beside source code (`core/`, `rpc/`, `sql/`, `lua/`), configure `include`, `exclude`, and `text_extensions` atomically with `workspace.configure`:

```sh
# Configure workspace "autodoc-repo" to index Markdown, YAML, Mermaid, CSV, and polyglot code
autodoc --call workspace.configure '[
  "autodoc-repo",
  {
    "include": [
      "**/*.md",
      "**/*.yaml",
      "**/*.yml",
      "**/*.mmd",
      "**/*.csv",
      "**/*.{go,rs,py,ts,tsx,js,lua,sql,proto,zig,odin}"
    ],
    "exclude": [
      ".git/**",
      "**/node_modules/**",
      "vendor/**",
      "target/**",
      "dist/**",
      "build/**",
      "**/*_test.go"
    ],
    "text_extensions": [".conf", ".env.example", ". service"]
  }
]'
```

### Understanding `text_extensions` vs. Registered Code Chunkers (`text_collisions`)

- **`text_extensions` (`workspace.set_text_extensions`):** Declares custom UTF-8 plain-text file extensions (such as `.log`, `.rst`, `.ini`, `.toml`) that AutoDoc should read and chunk as plain text.
- **No Sniffing:** AutoDoc never guesses whether an unknown binary file is text; only built-in types, registered code chunkers, derived document formats, and declared `text_extensions` are read.
- **Collision Protection (`text_collisions`):** Built-in code extensions (and custom chunkers registered via `app.Options.Chunkers`, ADR 0216) take precedence over plain-text extensions. Attempting to pass a registered code extension to `workspace.set_text_extensions` is refused with `-32602 rpc.invalid_params`, and `workspace.list` reports any historical overlap in `text_collisions`.

---

## 4. Querying Code Declarations with `symbols.find` and `symbols.doc`

In addition to finding code chunks through `search.query`, AutoDoc provides dedicated symbol inspection verbs (`symbols.find` and `symbols.doc`) for fast structural navigation by humans and AI coding agents.

### 1. Listing All Declarations in a File (`symbols.doc`)

`symbols.doc` returns every extracted declaration in a single source file in top-to-bottom line order, similar to `doc.outline` for Markdown files:

```sh
# Inspect all Go declarations in core/index/reclaim.go
autodoc --call symbols.doc '["autodoc-repo", "core/index/reclaim.go"]'
```

Example response:

```json
{
  "path": "core/index/reclaim.go",
  "language": "go",
  "symbols": [
    {
      "name": "reclaimBatch",
      "kind": "var",
      "line": 27,
      "line_end": 27,
      "signature": "var reclaimBatch uint64 = 2000"
    },
    {
      "name": "writeFn",
      "kind": "type",
      "line": 30,
      "line_end": 30,
      "signature": "type writeFn func(ctx context.Context, fn func(context.Context, *store.Tx) error) error"
    },
    {
      "name": "Reclaimed",
      "kind": "struct",
      "line": 34,
      "line_end": 38,
      "signature": "type Reclaimed struct"
    },
    {
      "name": "reclaim",
      "kind": "method",
      "receiver": "*Store",
      "line": 56,
      "line_end": 103,
      "signature": "func (s *Store) reclaim(ctx context.Context, fp string, write writeFn) (Reclaimed, error)"
    },
    {
      "name": "Sweep",
      "kind": "method",
      "receiver": "*Indexer",
      "line": 144,
      "line_end": 151,
      "signature": "func (x *Indexer) Sweep(ctx context.Context) ([]Reclaimed, error)"
    }
  ]
}
```

### 2. Searching Declarations Across the Workspace (`symbols.find`)

`symbols.find` searches symbol names, receivers, and signatures across the entire workspace, with optional filters for `kind`, `language`, `receiver`, and `paths`:

```sh
# Find all methods on *Indexer or *Store matching "Sweep" or "reclaim"
autodoc --call symbols.find '[
  "autodoc-repo",
  "Sweep",
  {
    "kind": ["func", "method"],
    "paths": ["core/index"],
    "limit": 20
  }
]'

# Find all SQL table and view definitions under sql/deployments
autodoc --call symbols.find '[
  "autodoc-repo",
  "",
  {
    "kind": ["table", "view"],
    "paths": ["sql/deployments"]
  }
]'
```

---

## 5. Custom Compile-Time Chunkers (`app.Options.Chunkers`) and Held Files

Through `app.Main` (`ADR 0216`), custom builds of AutoDoc can register additional domain-specific `search.Chunker` implementations keyed by file extension:

```go
os.Exit(app.Main(context.Background(), os.Args[1:], app.Options{
    Version:  version,
    Chunkers: map[string]search.Chunker{".dsl": dslChunker},
}))
```

Because multiple AutoDoc builds share a single SQLite store (`autodoc.db`), a community binary that attaches to a store containing `.dsl` chunks produced by a custom build **never overwrites or drops those chunks**. Instead, it marks those files as **held** (`Hit.Hold` set to `"current"`, `"stale"`, or `"unchecked"`, and counted under `held` in `index.status`), keeping every declaration searchable across builds.
