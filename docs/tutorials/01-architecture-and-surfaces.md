# Chapter 1: Architecture and Surfaces

AutoDoc is a local-first, file-canonical knowledge base engine, modal technical editor, and hybrid retrieval system written in Go. Unlike proprietary cloud wikis that trap prose inside opaque databases or heavy Electron wrappers that consume hundreds of megabytes of RAM per window, AutoDoc separates **canonical plain-text storage** on disk from a **high-speed derived index** served over a single local daemon.

Whether you author Architecture Decision Records (ADRs) in the terminal TUI (`autodoc --ui`), inspect live Mermaid diagrams and Vega-Lite telemetry charts in the GPU-accelerated native GUI (`autodoc --gui`), navigate cross-repository links inside Neovim (`autodoc.nvim`), automate knowledge workflows from AI agents (`autodoc --call`), or scale across engineering teams (`autodoc --server`), every surface speaks the same protocol and operates on the same underlying Markdown files.

---

## 1.1 The File-Canonical Philosophy

The foundational invariant of AutoDoc is simple: **your files on disk are the single source of truth**. Every note, architecture decision record, runbook, review, and synthesis document lives as a standard UTF-8 Markdown file (`.md`) with YAML frontmatter in your regular filesystem directory tree. Similarly, every project task lives as an explicit YAML file inside your workspace's `.todo-list/` directory (`open/`, `deferred/`, `completed/`, `archived/`).

```yaml
---
type: adr
id: "0042"
title: "Single-Writer SQLite WAL Lease for Local Daemon"
status: accepted
created: 2026-10-01
tags: [architecture, storage, sqlite, daemon]
abstract: "Enforces a single-writer lock over the derived SQLite WAL index while allowing lock-free concurrent readers across TUI, GUI, Neovim, and CLI surfaces."
---
```

Because files on disk are canonical, AutoDoc never requires an import or export step to interoperate with standard Unix tools:

1. **Git-Native Versioning**: You branch, diff, blame, cherry-pick, and merge your knowledge base using standard `git` workflows. AutoDoc never hides metadata in sidecar binary blobs that cause merge conflicts.
2. **External Editor Harmony**: You can edit a file with `nvim`, `sed`, `awk`, or an AI coding assistant while `autodoc --ui` or `autodoc --gui` is open. The daemon's filesystem follower (`core/follow`) detects the atomic rename or write within milliseconds, re-parses only the changed sections, and broadcasts incremental buffer and outline updates to all connected UI clients.
3. **Disposable, Reconstructible Index**: The SQLite database (`index.db`) is strictly a derived cache containing parsed frontmatter fields, section chunks, FTS5 lexical postings, wikilink edges, and vector embeddings. If you delete the index directory entirely, AutoDoc deterministically rebuilds the exact same index state from your Markdown files and `.todo-list/` YAML records on the next startup.

![AutoDoc Daemon and Surface Topology](daemon-topology.mmd)

### Canonical Write Path vs. Read Path

When you save a buffer in `autodoc --ui` or `autodoc --gui` (or invoke `doc.put` over RPC), AutoDoc never writes to the SQLite index first. Instead, it follows a strict two-phase canonical pipeline:

1. **Atomic Filesystem Commit**: The document module (`core/doc`) serializes the YAML frontmatter and Markdown body to a sibling temporary file (`.autodoc-tmp-<rand>`) in the target directory, flushes file data with `fsync`, and atomically replaces the target `.md` file via `os.Rename`.
2. **Synchronous Index Ingestion**: Immediately after the rename succeeds—or when `core/follow` observes an external process performing a write—the indexer computes the SHA-256 content digest, extracts structural chunks and `[[wikilinks]]`, and commits the derived rows to the SQLite WAL store.

---

## 1.2 Single-Writer Lease and SQLite WAL Store (`core/store`)

A common failure mode in local knowledge tools occurs when multiple processes—such as a background CLI script, a running GUI window, and an editor plugin—attempt to write to an embedded SQLite database simultaneously, resulting in `SQLITE_BUSY` lock contention or corrupted state.

AutoDoc eliminates multi-process write contention in `core/store` through two mechanisms:

- **Single-Writer Lease (`flock`)**: Only the active daemon process (`autodoc --serve`) acquires the exclusive advisory file lock (`index.db.lock`) permitting write transactions against the workspace store.
- **Write-Ahead Logging (`PRAGMA journal_mode=WAL`)**: The SQLite store is configured in WAL mode with `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, and bounded WAL autocheckpointing (`wal_autocheckpoint=1000`). Inside the daemon, a dedicated writer coordinator serializes index batch commits (document upserts, chunk replacements, link graph rebuilds, and vector writes) while multiple read workers execute concurrent snapshot queries (`search.query`, `doc.get`, `graph.backlinks`) without blocking either the writer or one another.

### Content-Hash Deduplication

When any document changes on disk, `core/store` compares the incoming file's SHA-256 digest against `documents.content_hash` and compares each individual section's hash against `chunks.chunk_hash`:

- **Document-Level Short-Circuit**: If a file is touched (`touch note.md`) or saved without changes, `documents.content_hash` matches and indexing finishes in `< 0.1 ms` with zero SQLite writes.
- **Chunk-Level Embedding Reuse**: When you edit a single paragraph in a 20-section engineering design doc, the 19 untouched sections keep their identical `chunk_hash` values. Their existing dense vector embeddings are preserved in place, so the embedding worker only computes vectors for the 1 modified section.

---

## 1.3 The msgpack-RPC Daemon (`autodoc --serve`)

At the center of AutoDoc's runtime architecture is `autodoc --serve`, a lightweight background daemon that manages workspace watchers, the SQLite WAL store, the hybrid ranking pipeline, and the Lua plugin host.

All surfaces communicate with the daemon over a user-private Unix domain socket created with strict `0600` filesystem permissions:

- **Linux**: `$XDG_RUNTIME_DIR/autodoc/autodoc.sock` (fallback: `~/.local/state/autodoc/autodoc.sock`)
- **macOS**: `~/Library/Application Support/autodoc/autodoc.sock`

The wire protocol uses **MessagePack-RPC** (`msgpack-RPC`), providing compact binary framing, sub-millisecond serialization overhead, and native compatibility with Neovim's built-in `vim.fn.sockconnect("pipe", ...)` and `vim.rpcrequest` APIs.

![Client Auto-Start and msgpack-RPC Handshake Sequence](client-rpc-flow.mmd)

### Transparent Auto-Start and Protocol Handshake

You rarely need to launch `autodoc --serve` manually. Whenever you invoke an interactive or CLI surface (`autodoc --ui`, `autodoc --gui`, `autodoc --call`, or `autodoc.nvim`), the client library executes a deterministic bootstrap sequence:

1. **Dial Socket**: The client attempts to connect to the local Unix domain socket.
2. **Spawn on Miss**: If the socket does not exist or refuses connection, the client spawns `autodoc --serve` as a detached background process and polls with exponential backoff (starting at 5 ms) until the socket becomes ready.
3. **Version Handshake (`sys.hello`)**: Upon connecting, the client sends a `sys.hello` RPC containing its build version and protocol revision:

```bash
autodoc --call sys.hello '[{"client": "cli", "protocol_rev": 4}]'
```

```json
{
  "ok": true,
  "version": "0.9.4",
  "protocol_rev": 4,
  "pid": 48219,
  "uptime_sec": 14820,
  "workspaces": ["GlobalKB", "MonorepoDocs"]
}
```

If a binary upgrade occurred while an older daemon was still running, the client detects the `protocol_rev` mismatch, issues `sys.shutdown` to the stale daemon, and seamlessly starts a fresh daemon matching the new binary.

### Core Daemon Modules

The daemon exposes a structured namespace of RPC methods organized by domain module:

- **`sys.*`**: Daemon health (`sys.ping`, `sys.hello`, `sys.status`), method introspection (`sys.methods`), and graceful lifecycle control (`sys.shutdown`).
- **`workspace.*`**: Multi-workspace registration (`workspace.add`, `workspace.list`, `workspace.update`, `workspace.remove`) and per-workspace policy inspection.
- **`doc.*`**: Canonical document reading (`doc.get`, `doc.list`, `doc.sections`), atomic writing (`doc.put`, `doc.patch`), frontmatter validation (`doc.validate`), and template instantiation (`doc.create`).
- **`index.*`**: Real-time watcher health (`index.status`), manual re-indexing (`index.rebuild`), and queue inspection for pending vector embeddings.
- **`search.*` & `rank.*`**: Lexical BM25 search, dense vector similarity, Reciprocal Rank Fusion (`RRF`), frontmatter predicate filtering (`type:adr status:accepted`), and section-level snippet extraction.
- **`graph.*`**: Outgoing wikilinks, incoming backlinks, 2-hop neighborhood traversal, broken-link diagnostics, and orphan document detection.
- **`todos.*`**: The 12-verb structured task engine (`list`, `show`, `list_dirs`, `get_dir`, `add`, `update`, `status`, `assign`, `archive`, `remove`, `refresh`, `set_dir`) backed by `.todo-list/` YAML files.

---

## 1.4 The Five AutoDoc Surfaces

AutoDoc provides five specialized surfaces tailored to distinct environments and workflows. Because state lives in the daemon and on disk, you can run multiple surfaces simultaneously against the same workspace—for example, keeping `autodoc --gui` open on a secondary monitor for live diagram previews while editing code and querying notes from `autodoc.nvim` or `autodoc --call` in your terminal.

![Resident Memory Footprint Comparison Across Surfaces](memory-footprint-comparison.chart.json)

### 1. Terminal TUI (`autodoc --ui` or `autodoc`)

Running `autodoc` (or explicitly `autodoc --ui`) opens the full-screen terminal interface inside any modern terminal emulator (Kitty, Alacritty, WezTerm, Ghostty, tmux, or over SSH). Built on a custom zero-allocation cell buffer renderer, `--ui` delivers a distraction-free **120-column centered page editor** with full Vim modal editing (`Normal`, `Insert`, `Visual`, `Command`) alongside an optional Modeless mode for users who prefer standard CUA shortcuts. Surrounding the editor are dockable panels for `Files`, `Outline`, `Relations`, `Todos`, `Results`, and embedded `TerminalTabs`.

### 2. Native Desktop GUI (`autodoc --gui`)

Invoking `autodoc --gui` launches the native, GPU-accelerated desktop application powered by Gio (`gioui.org`). It shares 100% of the editor buffer engine, keybindings, leader chords, and panel layout with `--ui`, while adding crisp vector typography and **Rendered View** (`Ctrl+P`). In `--gui`, standalone block embeds—including `.mmd` Mermaid diagrams, `.chart.json` Vega-Lite charts, `.csv` data tables, and raster/vector images—render natively inline without spawning a headless browser. Clicking any rendered diagram or chart jumps directly into an inline source editor for that companion file.

### 3. Neovim Integration (`autodoc.nvim`)

For developers who live inside Neovim, `autodoc.nvim` connects directly to `autodoc --serve` over Neovim's native msgpack-RPC channel. It provides instant workspace-wide fuzzy search, frontmatter-aware completion, `[[wikilink]]` follow and backlink pickers, floating document previews, and full integration with the `.todo-list/` task store—all without leaving your code buffers.

### 4. CLI and AI Agent RPC (`autodoc --call`)

Scripts, shell pipelines, and autonomous AI coding agents interact with AutoDoc headlessly via `autodoc --call <method> '<json-args>'`:

```bash
# Query top 5 accepted ADRs matching "sqlite wal" in the GlobalKB workspace
autodoc --call search.query '["GlobalKB", "sqlite wal type:adr status:accepted", {"limit": 5}]'

# Validate a draft document against the workspace's frontmatter schema
autodoc --call doc.validate '["GlobalKB", "adrs/0042-sqlite-wal-lease.md"]'

# List open high-priority tasks in JSON for an automated agent workflow
autodoc --call todos.list '[{"status": "open", "priority": "high"}]'
```

Because `autodoc --call` reuses the warm daemon socket, invocations complete in **2–5 milliseconds** with a 14 MB memory footprint, making it ideal for agent tool calls, Git pre-commit hooks, and CI validation gates.

### 5. Enterprise Server (`autodoc --server`)

When a knowledge base grows to span dozens of repositories and hundreds of engineers, `autodoc --server` provides a stateless HTTP/RPC aggregation tier backed by PostgreSQL (`TSVECTOR` lexical search + `pgvector` HNSW semantic indexing). Individual workspaces remain Git-canonical, while CI webhooks or branch followers stream updates into the shared Postgres cluster for cross-organization retrieval-augmented generation (RAG) and federated search.

![Feature Matrix Across AutoDoc Surfaces](surface-feature-matrix.csv)

---

## 1.5 Summary of Architectural Guarantees

- **Zero Lock-In**: Plain `.md` files with YAML frontmatter and `.todo-list/*.yaml` records remain readable with `cat`, `grep`, `git`, or any text editor forever.
- **Zero Write Contention**: Single-writer `flock` lease plus SQLite WAL in `core/store` guarantees that simultaneous `--ui`, `--gui`, `nvim`, and `--call` clients never clash.
- **Gapless External Sync**: The watch-before-scan state machine in `core/follow` ensures `git checkout`, `git rebase`, or external agent edits appear in `< 20 ms` without race windows.
- **Sub-16ms Interactivity**: Native Go + Gio GPU rendering and binary msgpack-RPC deliver 60–120 FPS typing and scrolling at a fraction of Electron's memory footprint.
- **Offline Determinism**: Native `.mmd` and `.chart.json` parsers plus self-contained HTML export ensure diagrams, charts, and exported pages work 100% air-gapped with zero CDN calls.

---

## 1.6 Deep Dive: Daemon Lifecycle, Logging, and Diagnostics

While `autodoc --serve` manages its own lifecycle automatically during normal usage, understanding how the daemon initializes its runtime state directory, handles signals, and exposes diagnostic telemetry is helpful when debugging custom integrations or running headless agent fleets.

### Runtime Directory Layout

AutoDoc follows the XDG Base Directory Specification across Linux and macOS:

- **Configuration (`$XDG_CONFIG_HOME/autodoc/config.toml`)**: Declarative workspace definitions, editor preferences (`mode = "vim"` or `"modeless"`), theme overrides, and embedding provider settings.
- **State & Socket (`$XDG_STATE_HOME/autodoc/`)**:
  - `autodoc.sock`: The `0600` msgpack-RPC Unix domain socket.
  - `daemon.pid`: Advisory PID record of the active `autodoc --serve` instance.
  - `daemon.log`: Structured JSON-lines operational log rotated automatically at 10 MiB.
- **Derived Index Cache (`$XDG_CACHE_HOME/autodoc/workspaces/<workspace-hash>/`)**:
  - `index.db`, `index.db-wal`, `index.db-shm`: The SQLite WAL database files.
  - `index.db.lock`: The exclusive `flock(2)` single-writer lease held by the active daemon.

### Inspecting Live Daemon Telemetry (`sys.status` and `sys.methods`)

You can query the daemon's active client connections, memory allocation, open SQLite WAL frames, and registered RPC methods at any time:

```bash
# Inspect live daemon resource usage and active connections
autodoc --call sys.status '[]'

# Enumerate all registered msgpack-RPC verbs across modules
autodoc --call sys.methods '[]'
```

Example `sys.status` response:

```json
{
  "pid": 48219,
  "version": "0.9.4",
  "protocol_rev": 4,
  "goroutines": 18,
  "heap_alloc_mb": 22.4,
  "sys_rss_mb": 38.1,
  "connected_clients": 3,
  "wal_checkpoint_pages": 128,
  "active_workspaces": ["GlobalKB", "MonorepoDocs"]
}
```

### Graceful Shutdown and Socket Recovery

When `autodoc --serve` receives `SIGTERM`, `SIGINT`, or a `sys.shutdown` RPC call:

1. It stops accepting new RPC requests and cancels background embedding workers cleanly.
2. It flushes all pending `core/follow` watcher events and runs `PRAGMA wal_checkpoint(TRUNCATE)` on every open workspace SQLite store so `index.db-wal` is folded completely into `index.db`.
3. It unlinks `autodoc.sock` and releases the `index.db.lock` advisory file lease before exiting with code `0`.

If a previous daemon process was ever terminated abruptly via `SIGKILL` (`kill -9`), the next client invocation detects that `autodoc.sock` refuses connection, verifies that `index.db.lock` is unheld, unlinks the stale socket file, and boots a fresh daemon automatically—recovering the WAL journal cleanly via SQLite's built-in crash recovery.
