# Chapter 9: Code Symbol and Declaration Indexing

Technical documentation rarely lives apart from the source code it describes. Architecture Decision Records cite Go interfaces (`search.Chunker`), frontend guides reference TypeScript types and React components, data engineering notes point to Python pipelines, and systems runbooks reference Rust structs and traits.

Rather than treating source files as flat plain text or requiring heavyweight language servers (LSP) to be running in the background, AutoDoc ships a built-in **declaration-level code chunker** (`core/registrations/builtin.go` and `golib/search/chunk/code`). It partitions **Go** (`.go`), **TypeScript / JavaScript** (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`), **Python** (`.py`), and **Rust** (`.rs`) files at declaration boundaries, pairs verbatim code bodies with declaration-focused embedding surfaces (`breadcrumb + doc + signature`), and exposes custom compile-time chunker hooks (`app.Options.Chunkers`) and plain-text extensions (`workspace.set_text_extensions`).

![Architecture of AutoDoc's declaration-level code chunker dispatch and SQLite chunk index](symbol-extractor-dispatch.mmd)

---

## 1. Declaration-Level Code Chunking (`golib/search/chunk/code`)

When the workspace indexer (`core/index`) encounters a registered code extension, it routes the file to its language-specific `search.Chunker`:

### 1. Exact Go AST Chunker (`.go`, `code-go-2+text-5`)

Because Go's standard library includes `go/parser`, `go/ast`, and `go/token`, AutoDoc parses `.go` files into a complete Abstract Syntax Tree:

- **File Header Unit:** Captures build tags, package doc comments, and the `import` block as the file's opening structural unit.
- **Functions and Methods:** Extracts each top-level `func` and receiver method with its exact 1-based `line_start` / `line_end`, byte offsets (`byte_start` / `byte_end`), attached doc comment (`// Search answers a query...`), and normalized signature.
- **Types and Interfaces:** Extracts each `type` specification (`struct`, `interface`, type definitions, and aliases), splitting grouped `type ( ... )` blocks into per-type units when needed.
- **Constants and Variables:** Extracts `const` and `var` declarations and groups. For embeddings, a `const` or `var` embeds its names, type, and values (truncating large composite literals after the first three elements, e.g. `[][3]int{{0x0, 0xf, 0}, …}`, and representing function literals by their signature).
- **Graceful Parse Fallback:** If a `.go` file is mid-edit and does not parse, the Go chunker automatically falls back to `chunk.Text` (`+text-5`), ensuring the file remains indexed and searchable at all times.

### 2. String-Aware Scanner Chunkers for TypeScript/JS, Python, and Rust

For TypeScript/JavaScript, Python, and Rust, `golib/search/chunk/code` uses fast, string-and-comment-aware scanners where braces or keywords inside strings, template literals, raw strings, char literals, and comments never start or end a unit:

- **TypeScript & JavaScript (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs` — `code-ts-2`):** Extracts functions, classes and their methods, interfaces, type aliases, and exported `const` arrow functions, keeping JSDoc blocks (`/** ... */`) and decorators (`@Component`) attached to their declarations.
- **Python (`.py` — `code-py-2`):** Tracks indentation scopes to extract `def`, `async def`, and `class` declarations along with their methods, preserving leading `@decorator` lines and triple-quoted docstrings (`"""..."""`).
- **Rust (`.rs` — `code-rs-2`):** Extracts `fn`, `struct`, `enum`, `union`, `trait`, and `impl` blocks together with their associated functions and methods, preserving `///` doc comments and `#[...]` attributes.

![Illustrative declaration chunking throughput across built-in code chunkers and plain-text fallback](symbol-extraction-throughput.chart.json)

*Note: The chart above shows an illustrative relative throughput profile comparing AST-based Go chunking, scanner-based TypeScript/Python/Rust declaration chunkers, and plain-text fallback chunking.*

---

## 2. The Partition Invariant and Two-Surface Embedding Design

Every built-in code chunker in `golib/search/chunk/code` obeys a strict lossless partition invariant across the file:

1. **Contiguous, Non-Overlapping Spans:** Chunk byte spans ascend and never overlap, only whitespace lies outside them, and `Body == string(Doc.Text[ByteStart:ByteEnd])`. Detached comments, preprocessor directives, and top-level glue statements become header or glue chunks so no line of code is ever dropped from lexical search.
2. **Two Surfaces (`Body` vs. `Embed`):**
   - **`Body` (Lexical FTS5 & Display):** Holds the verbatim source code of the declaration (`Doc.Text[ByteStart:ByteEnd]`), enabling exact identifier and literal searches in FTS5.
   - **`Embed` (Semantic Vector Surface):** Constructed strictly from the **breadcrumb, doc comment, and declaration signature**—never the raw function body implementation. When you run a semantic query like *"how do we reclaim unused model vectors in batches"*, the vector embedding for `func (s *Store) reclaim(ctx context.Context, fp string, write writeFn)` and its doc comment scores a clean semantic hit without noise from loop variables.
3. **Bounded Embeddings & Oversized Declarations:** No `Embed` text ever exceeds the chunk's token budget (long doc comments are trimmed while keeping the signature whole). When an oversized function or class exceeds the token budget, the chunker splits it at statement boundaries: every fragment's `breadcrumb` retains the enclosing signature, and continuation fragments embed the signature alongside their local comments and the identifiers they declare and call.

![Built-in code chunkers, derived document extractors, and custom extension mechanisms in AutoDoc](supported-languages-and-kinds.csv)

---

## 3. Configuring a Workspace to Index Source Trees Alongside Docs

By default, a newly created AutoDoc workspace includes `**/*.md`, `**/*.txt`, `**/*.yaml`, and `**/*.yml` and excludes `.git/**` and `**/node_modules/**`. You can enable code indexing either in the UI or via `workspace.configure` / `workspace.set_patterns`.

### Enabling Code File Types in the TUI / GUI

1. Open `Go › Manage workspaces…` (`Ctrl+W` then `Manage…`), select your workspace, and choose **Edit…** (or open `Options › Editor preferences… › File types…`).
2. Toggle the **Code** row on. AutoDoc automatically adds the code include patterns and appends `vendor/**`, `target/**`, `dist/**`, and `build/**` to the workspace's `exclude` list so third-party dependencies and compiled artifacts are never walked or watched.

### Configuring `include`, `exclude`, and `text_extensions` via RPC

For repositories where documentation (`docs/`, `adrs/`) lives beside source code (`core/`, `rpc/`, `sql/`, `lua/`), configure `include`, `exclude`, and `text_extensions` atomically with `workspace.configure`:

```sh
# Configure workspace "autodoc-repo" to index Markdown, YAML, built-in code (.go, .rs, .py, .ts, .js),
# and custom UTF-8 text extensions (.sql, .proto, .lua)
autodoc --call workspace.configure '[
  "autodoc-repo",
  {
    "include": [
      "**/*.md",
      "**/*.yaml",
      "**/*.yml",
      "**/*.{go,rs,py,ts,tsx,js,jsx,mjs,sql,proto,lua}"
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
    "text_extensions": [".sql", ".proto", ".lua", ".conf"]
  }
]'
```

### Understanding `text_extensions` vs. Registered Code Chunkers (`text_collisions`)

- **`text_extensions` (`workspace.set_text_extensions`):** Declares additional UTF-8 plain-text file extensions (such as `.sql`, `.proto`, `.lua`, `.toml`, `.rst`, `.ini`) that AutoDoc should read and chunk via `chunk.Text`.
- **No Sniffing:** AutoDoc never guesses whether an unknown binary file is text; only built-in types, registered code chunkers, derived document formats (`.docx`, `.htm`, `.html`), and declared `text_extensions` are read.
- **Collision Protection (`text_collisions`):** Built-in code extensions (`.go`, `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.py`, `.rs`) and custom chunkers registered via `app.Options.Chunkers` (`ADR 0216`) take precedence over plain-text extensions. Attempting to pass a registered code extension to `workspace.set_text_extensions` is refused with `-32602 rpc.invalid_params`, and `workspace.list` reports any historical overlap in `text_collisions`.

---

## 4. Querying Code Declarations and Inspecting Registered Chunkers

Once code files are indexed, `search.query` searches across both Markdown documentation and code declarations simultaneously, or you can scope `paths` to specific source packages.

### 1. Searching Code Declarations with `search.query`

Every code hit returned by `search.query` includes the declaration's `breadcrumb` (carrying the package/type and signature), 1-based `line_start` and `line_end`, and byte offsets:

```sh
# Search Go declarations under core/index for unused model vector reclamation
autodoc --call search.query '[
  "autodoc-repo",
  "reclaim unused model vectors in batches",
  {
    "paths": ["core/index"],
    "limit": 5
  }
]'
```

Example hit in the response:

```json
{
  "path": "core/index/reclaim.go",
  "breadcrumb": "core/index/reclaim.go › func (x *Indexer) Sweep(ctx context.Context) ([]Reclaimed, error)",
  "snippet": "// Sweep reclaims vectors of models no workspace uses...",
  "line_start": 144,
  "line_end": 151,
  "byte_start": 4120,
  "byte_end": 4388,
  "relevance": "high",
  "via": ["lexical", "semantic", "rerank"]
}
```

### 2. Inspecting Registered Chunkers and Derived Formats (`sys.capabilities`)

You can inspect the active daemon's registered code chunker versions and derived format extractors at any time via `sys.capabilities`:

```sh
# Inspect registered chunkers (.go, .ts, .py, .rs, plus any custom chunkers) and formats (.docx, .html)
autodoc --call sys.capabilities '[]'
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

Because multiple AutoDoc builds can share a single SQLite store (`autodoc.db`), a community binary that attaches to a store containing `.dsl` chunks produced by a custom build **never overwrites or drops those chunks**. Instead, it marks those files as **held** (`Hit.Hold` set to `"current"`, `"stale"`, or `"unchecked"`, and counted under `held` in `index.status`), keeping every declaration searchable across builds.

---

[← Previous: Chapter 08: Knowledge Graphs & ADR Lifecycle](./08-knowledge-graphs-and-adr-lifecycle.md) | [Index](./README.md) | [Next: Chapter 10: Todo Task Engine →](./10-todo-task-engine.md)
