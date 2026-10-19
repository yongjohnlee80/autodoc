# The AutoDoc Reference Book

New to AutoDoc? Start with the short [tutorials](../tutorials/README.md); this book is the reference they point to.

Welcome to the **AutoDoc User Reference Book**—an 18-chapter, hands-on guide and architectural reference covering every surface of AutoDoc: the terminal editor (`autodoc --ui`), the native GPU desktop window (`autodoc --gui`), the Neovim integration (`autodoc.nvim`), the AI agent CLI (`autodoc --call`), the Lua & out-of-process plugin ecosystem (Protocols 1–8, the plugin UI kits, and `autodoc-autodb`), and the headless multi-tenant PostgreSQL RAG server (`autodoc --server`).

---

## Designed to Be Read Inside AutoDoc (or Exported to the Web)

Every chapter in `docs/reference/` is written in AutoDoc's native rich Markdown dialect (`core/export/embed.go`). Instead of static ASCII art or external image links, each chapter `<nn>-<slug>.md` sits alongside a companion `<nn>-<slug>/` resource directory containing live, editable visual assets:

- **Mermaid Diagrams (`.mmd`)**: Rendered natively as GPU vector graphics in `autodoc --gui` (`Ctrl+T` Rendered View / `SPC H` Preview HTML) and via bundled Mermaid in `autodoc --export` HTML.
- **Vega-Lite Charts (`.chart.json`)**: Rendered natively on the Gio canvas by `golib/gui/vegalite` in `autodoc --gui` and via Vega-Lite in HTML exports.
- **Structured CSV Tables (`.csv`)**: Rendered as formatted tables in both `--gui` Rendered View and HTML exports.

Open this folder (`docs/reference/`) as a workspace in `autodoc --gui` and press **`Ctrl+T`** on any chapter to explore the diagrams, charts, and tables live—or click any embedded diagram or chart in Rendered View to inspect and edit its underlying `.mmd` or `.chart.json` source inline.

![AutoDoc Reference Book Learning Path](book-learning-path.mmd)

![Embedded Visual Assets Across the 5 Parts of the Reference Book](chapter-visual-assets.chart.json)

---

## Complete Chapter Index

![Complete 18-Chapter Reference Book Index](chapter-index.csv)

---

## Part-by-Part Guide

### Part I — Foundations & Surface Mastery
1. **[Chapter 01: What Is AutoDoc? Architecture & File-Canonical Guarantee](./01-architecture-and-surfaces.md)**
   How AutoDoc keeps your Markdown and YAML files 100% canonical on disk while deriving a real-time SQLite WAL index shared across `--ui`, `--gui`, `autodoc.nvim`, and `--call`.
2. **[Chapter 02: 5-Minute Quickstart & Workspace Registration](./02-quickstart-and-workspaces.md)**
   Installing the single binary, registering your first workspace, tuning `include`/`exclude`/`section_tokens`, and understanding how the `watch-before-scan` follower converges on external Git edits.
3. **[Chapter 03: Mastering the Modal Editor, Panels & Navigation](./03-modal-editor-and-panels.md)**
   Navigating the 120-column distraction-free page, switching between Vim modal editing and Modeless mode, managing multi-buffer tabs and splits, folding headings, and docking panels (`Files`, `Outline`, `Relations`, `Todos`, `Results`, `TerminalTabs`).
4. **[Chapter 04: Rich Authoring — Mermaid (`.mmd`), Vega-Lite (`.chart.json`), CSVs & Export](./04-rich-authoring-embeds-and-export.md)**
   Using the `<note-stem>/` companion resource directory convention to embed native vector diagrams, quantitative charts, CSV tables, and self-contained data-URI images—plus exporting offline HTML pages (`autodoc --export`).

### Part II — Search, Schemas & Knowledge Graphs
5. **[Chapter 05: The 3-Stage Hybrid Search Pipeline (`Lexical` + `Semantic` + `Rerank`)](./05-hybrid-search-pipeline.md)**
   Inside AutoDoc's retrieval funnel: FTS5 BM25 with structural signals, 1-bit quantized + float32 vector recall, Reciprocal Rank Fusion (RRF), cross-encoder reranking, facet filter syntax, and honest stage reporting.
6. **[Chapter 06: Configuring Local & Cloud Embedding and Reranker Models](./06-configuring-embeddings-and-rerankers.md)**
   Wiring local Ollama (`qwen3-embedding`) and TEI (`BAAI/bge-reranker-v2-m3`) or cloud providers (`OpenAI`, `Vertex AI`) in the store (`embedding.add`, `ranker.add`), switching models live in the background, and automatic orphan vector reclamation.
7. **[Chapter 07: Frontmatter Schemas (`v1` & `v2`), Live Validation & Search Facets](./07-frontmatter-schemas-and-validation.md)**
   Declaring `_schema/frontmatter.yaml` with `version: 2` (`discriminator: type`, `base` fields, and per-type rules), reading non-blocking live editor diagnostics, and turning YAML fields into queryable search facets.
8. **[Chapter 08: Knowledge Graphs — Wikilinks, Backlinks & ADR Lifecycle Relations](./08-knowledge-graphs-and-adr-lifecycle.md)**
   Navigating `[[wikilinks]]` and typed architectural edges (`supersedes`, `superseded_by`, `amends`, `sources`, `adr`, `review`), using the Relations Panel (`SPC l`), demoting superseded ADRs, and auditing broken links (`graph.unresolved`).
9. **[Chapter 09: Code Symbol & Declaration Indexing (`golib/search/chunk/code`)](./09-code-symbol-indexing.md)**
   Indexing declarations across Go (`.go`), TypeScript/JavaScript (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`), Python (`.py`), and Rust (`.rs`) alongside custom chunkers (`app.Options.Chunkers`) and plain-text extensions (`text_extensions`).

### Part III — Tasks, Workflows & AI Agent Orchestration
10. **[Chapter 10: The `.todo-list/` Task Engine & Crash-Recoverable Journal](./10-todo-task-engine.md)**
    Managing tasks as canonical Markdown+YAML files backed by AutoDoc's 3-phase SQLite journal (`pending` → `staged` → `committed`), using the interactive Todos Panel (`SPC x`), and portable `$KB_ROOT` / `$PROJECT_ROOT` symbolic paths.
11. **[Chapter 11: Terminal Tabs & One-Key Agent Task Handoff (`SPC x` → `A`)](./11-terminal-tabs-and-agent-handoff.md)**
    Configuring embedded CLI coding agent tabs (`[[terminal.tabs]]`) and pressing `A` on any todo task to atomically mark it `in-progress`, assign it to `autodoc:<codename>`, and paste the structured task brief into the agent's live terminal.
12. **[Chapter 12: Wiring External AI Coding Agents (`--call`, `KB_OPERATIONS.md`, & Token Efficiency)](./12-ai-agent-integration-and-cli-rpc.md)**
    Teaching Claude Code, Gemini, and Codex to query AutoDoc via `--call`, follow the adaptive retrieval ladder, and read exact `[line_start, line_end]` spans—cutting context token usage by over 60% and achieving 94%–100% top-3 accuracy.
13. **[Chapter 13: Optimistic Concurrency, `watch-before-scan` & Safe Multi-Agent Writes](./13-concurrency-and-safe-multi-agent-writes.md)**
    How `stat-read-stat`, SHA-256 revision tokens, atomic `.part` renames, single-writer SQLite leases, and UI ticket invalidation prevent concurrent humans and AI agents from ever overwriting each other's work.

### Part IV — Programmability, Plugins & Neovim
14. **[Chapter 14: Sandboxed Lua Scripting (`~/.config/autodoc/lua/`)](./14-lua-scripting-and-custom-commands.md)**
    Writing custom `:commands`, modal keymaps, single-step undo buffer transformations, interactive `autodoc.ui.pick` workflows, and capability-gated async RPC scripts inside the 2-second Lua sandbox.
15. **[Chapter 15: The Out-of-Process Plugin System (Protocols 1–6) & Vetted Registry](./15-plugin-architecture-and-registry.md)**
    Building isolated child-process plugins in Go or Python across Protocols 1 to 8: modal dialogs, docked non-modal cards, document feeds with byte offsets, hosted paged tables (`[table]`), typed cell kinds, the pointer, the session slot, a plugin's Lua package, notifications, and the `plugin/card` and `plugin/decl` UI kits.
16. **[Chapter 16: Live Database Workbenches in Markdown — The AutoDB Plugin](./16-autodb-live-sql-workbench.md)**
    Running PostgreSQL, MySQL, and SQLite queries directly from `.sql` files and Markdown notes via `autodoc-autodb`, streaming session-pinned paged cursors into AutoDoc's 100k-row sortable Results table, and exporting CSV or Markdown tables.
17. **[Chapter 17: Neovim Integration (`autodoc.nvim`) & Reversible KB Migration](./17-neovim-integration-and-kb-migration.md)**
    Using AutoDoc's generated Neovim pickers, live stage toggles, relations drawer, 6-slot floating previewer, and the 6-step reversible `:AutodocKbMigrate` pipeline.

### Part V — Enterprise RAG Server (`autodoc --server`)
18. **[Chapter 18: Multi-Tenant PostgreSQL RAG Server (`autodoc --server` & `.view` Contracts)](./18-enterprise-postgres-rag-server.md)**
    Ingesting live PostgreSQL tables via declarative `.view` templates with keyset cursors and tenant advisory locks, serving snapshot-enforced `POST /v1/search` over `pgvector`, and evaluating self-hosted versus managed cloud RAG deployment scenarios.

---

**Next**: [Chapter 01: Architecture & Surfaces →](./01-architecture-and-surfaces.md)

