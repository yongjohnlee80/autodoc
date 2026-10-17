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
2. **Spawn on Miss**: If the socket does not exist or refuses connection, the client spawns `autodoc --serve` as a detached background process and polls with exponential backoff until the socket becomes ready.
3. **Internal Protocol Handshake (`sys.hello`)**: Upon connecting, the client library automatically sends an internal `sys.hello` frame (`[{"protocol": 25, "name": "cli"}]`). The daemon replies with `{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}`. If a binary upgrade occurred while an older daemon was still running, the client detects the version/protocol mismatch, issues `sys.shutdown` to the stale daemon, and seamlessly starts a fresh daemon matching the new binary.

### Core Daemon Modules

The daemon exposes a structured namespace of RPC methods organized by domain module (`autodoc --contracts`):

- **`sys.*`**: Session negotiation and shutdown (`sys.hello`, `sys.shutdown` — internal to client libraries), edition and verb capability discovery (`sys.capabilities`), live configuration/lifecycle event streaming (`sys.events`), and self-describing protocol catalog inspection (`sys.contracts`).
- **`workspace.*`**: Multi-workspace lifecycle (`workspace.list`, `workspace.add`, `workspace.rename`, `workspace.remove`) and store-backed policy configuration (`workspace.configure`, `workspace.set_patterns`, `workspace.set_schema`, `workspace.section_size`, `workspace.embedding_policy`, `workspace.set_provider`, `workspace.set_text_extensions`, `workspace.focus`, `workspace.of`, `workspace.vars`, `workspace.set_var`).
- **`doc.*`**: Versioned document reading (`doc.read`, `doc.read_raw`), optimistic-concurrency writing (`doc.write`, `doc.write_raw`), heading outline extraction (`doc.outline`), frontmatter schema validation (`doc.validate`), and atomic file operations (`doc.rename`, `doc.remove`).
- **`index.*`**: Real-time watcher and embedding queue status (`index.status`), frontmatter-projected document listing (`index.documents`, `index.list`), incremental cursor change feeds (`index.changes`), single-file re-indexing (`index.reindex`), and vector model inspection/cleanup (`index.models`, `index.purge_model`).
- **`search.*`, `symbols.*`, `embedding.*`, & `ranker.*`**: Hybrid lexical/semantic/reranked retrieval (`search.query`), polyglot code symbol lookup (`symbols.find`, `symbols.doc`), and store-backed embedding/reranker provider management (`embedding.*`, `ranker.*`).
- **`graph.*`**: Outgoing links (`graph.links`), incoming backlinks (`graph.backlinks`), $N$-hop neighborhood subgraphs (`graph.neighborhood`), broken-link diagnostics (`graph.unresolved`), and live unsaved target resolution (`graph.resolve`).
- **`todos.*`**: The structured task engine (`todos.list`, `todos.get`, `todos.create`, `todos.update`, `todos.status`, `todos.assign`, `todos.archive`, `todos.delete`, `todos.refresh`, `todos.vars`, `todos.set_var`, `todos.changes`, `todos.ack`) backed by `.todo-list/` Markdown+YAML files and `core/todo/sqlitejournal`.

---

## 1.4 The Five AutoDoc Surfaces

AutoDoc provides five specialized surfaces tailored to distinct environments and workflows. Because state lives in the daemon and on disk, you can run multiple surfaces simultaneously against the same workspace—for example, keeping `autodoc --gui` open on a secondary monitor for live diagram previews while editing code and querying notes from `autodoc.nvim` or `autodoc --call` in your terminal.

![Illustrative Resident Memory Profile Across Surface Architectures](memory-footprint-comparison.chart.json)

*Note: The chart above illustrates the architectural memory difference between native compiled Go/Gio surfaces (which share a single background `autodoc --serve` daemon) and desktop applications that bundle a dedicated Chromium + Node.js runtime.*

### 1. Terminal TUI (`autodoc --ui`)

Running `autodoc --ui` (or `autodoc --ui <workspace-name>`) opens the full-screen terminal interface inside any modern terminal emulator (Kitty, Alacritty, WezTerm, Ghostty, tmux, or over SSH). Built on a custom zero-allocation cell buffer renderer, `--ui` delivers a distraction-free **120-column centered page editor** with full Vim modal editing (`Normal`, `Insert`, `Visual`, `Command`) alongside an optional Modeless mode for users who prefer standard CUA shortcuts. Surrounding the editor are dockable panels for `Files`, `Outline`, `Relations`, `Todos`, `Results`, and embedded `TerminalTabs`.

### 2. Native Desktop GUI (`autodoc --gui`)

Invoking `autodoc --gui` (or `autodoc --gui <workspace-name>`) launches the native, GPU-accelerated desktop application powered by Gio (`gioui.org`). It shares 100% of the editor buffer engine, keybindings, leader chords, and panel layout with `--ui`, while adding crisp vector typography and **Rendered View** (`Ctrl+P`). In `--gui`, standalone block embeds—including `.mmd` Mermaid diagrams, `.chart.json` Vega-Lite charts, `.csv` data tables, and raster/vector images—render natively inline without spawning a headless browser. Clicking any rendered diagram or chart jumps directly into an inline source editor for that companion file.

### 3. Neovim Integration (`autodoc.nvim`)

For developers who live inside Neovim, `autodoc.nvim` connects directly to `autodoc --serve` over Neovim's native msgpack-RPC channel. It provides instant workspace-wide fuzzy search, frontmatter-aware completion, `[[wikilink]]` follow and backlink pickers, floating document previews, and full integration with the `.todo-list/` task store—all without leaving your code buffers.

### 4. CLI and AI Agent RPC (`autodoc --call`)

Scripts, shell pipelines, and autonomous AI coding agents interact with AutoDoc headlessly via `autodoc --call <verb> '<json-args>'`:

```bash
# Query top 5 accepted ADRs matching "sqlite wal" in the GlobalKB workspace
autodoc --call search.query '["GlobalKB", "sqlite wal type:adr status:accepted", {"limit": 5}]'

# Inspect the Markdown heading outline of an ADR
autodoc --call doc.outline '["GlobalKB", "adrs/0042-sqlite-wal-lease.md"]'

# List open high-priority tasks in JSON for an automated agent workflow
autodoc --call todos.list '["GlobalKB", {"status": "open", "priority": "high"}]'
```

Because `autodoc --call` reuses the warm daemon socket, invocations complete in a few milliseconds without reloading the index, making it ideal for agent tool calls, Git pre-commit hooks, and CI validation gates.

### 5. Enterprise Server (`autodoc --server`)

When an organization wants to index live PostgreSQL tables alongside documentation for multi-tenant retrieval-augmented generation (RAG), `autodoc --server` provides a dedicated HTTP/RPC service backed by PostgreSQL (`TSVECTOR` lexical search + `pgvector` semantic indexing) driven by declarative `.view` contracts.

![Feature Matrix Across AutoDoc Surfaces](surface-feature-matrix.csv)

---

## 1.5 Summary of Architectural Guarantees

- **Zero Lock-In**: Plain `.md` files with YAML frontmatter and `.todo-list/*.md` records remain readable with `cat`, `grep`, `git`, or any text editor forever.
- **Zero Write Contention**: Single-writer `flock` lease plus SQLite WAL in `core/store` guarantees that simultaneous `--ui`, `--gui`, `nvim`, and `--call` clients never clash.
- **Gapless External Sync**: The watch-before-scan state machine in `core/follow` ensures `git checkout`, `git rebase`, or external agent edits are reconciled without scan-to-watch race windows.
- **Native Interactivity**: Native Go + Gio GPU rendering and binary msgpack-RPC deliver high-frame-rate typing and scrolling without a bundled browser runtime.
- **Offline Determinism**: Native `.mmd` and `.chart.json` parsers plus self-contained HTML export (`autodoc --export html`) ensure diagrams, charts, and exported pages work 100% air-gapped with zero CDN calls.

---

## 1.6 Deep Dive: Daemon Lifecycle, Configuration, and Introspection

While `autodoc --serve` manages its own lifecycle automatically during normal usage, understanding how the daemon initializes its runtime directories, handles signals, and exposes capability contracts is helpful when debugging custom integrations or running headless agent fleets.

### Runtime Directory Layout

AutoDoc follows the XDG Base Directory Specification across Linux and macOS:

- **Daemon Configuration (`$XDG_CONFIG_HOME/autodoc/config.toml`)**: Holds daemon-level `[server]` (`socket`, `state_dir`, `data_dir`), `[follow]` (`poll_interval`), and `[embedding_queue]` (`max_inflight`) settings (`core/config/config.go`). Workspace registrations and sealed embedding/reranker provider credentials live inside the SQLite store (`autodoc.db`) rather than in `config.toml`.
- **State & Socket (`$XDG_RUNTIME_DIR/autodoc.sock` / `$XDG_STATE_HOME/autodoc/`)**:
  - `autodoc.sock`: The `0600` msgpack-RPC Unix domain socket.
- **Store Directory (`$XDG_DATA_HOME/autodoc/`)**:
  - `autodoc.db`, `autodoc.db-wal`, `autodoc.db-shm`: The SQLite WAL database files storing workspaces, chunks, links, vectors, and sealed provider configurations.
  - `autodoc.lock` & `lease-info`: The exclusive `flock(2)` single-writer lease and active endpoint record held by the running daemon.

### Inspecting Daemon Capabilities and Contracts (`sys.capabilities` and `sys.contracts`)

You can query the daemon's active edition features, custom chunker/deriver registrations, ranker state, and callable msgpack-RPC verb catalog at any time:

```bash
# Inspect the daemon's active capabilities, ranker status, and allowed verbs
autodoc --call sys.capabilities '[]'

# Query the self-describing contract table for a specific domain (or run `autodoc --contracts` offline)
autodoc --call sys.contracts '["sys.*"]'
```

Example `sys.capabilities` response:

```json
{
  "databases": false,
  "registrations": {
    "chunkers": {},
    "formats": {},
    "fingerprint": ""
  },
  "ranker": {
    "supplied": false,
    "model": "tei-bge-m3"
  },
  "verbs": [
    "doc.outline",
    "doc.read",
    "doc.validate",
    "doc.write",
    "graph.backlinks",
    "graph.links",
    "index.status",
    "search.query",
    "sys.capabilities",
    "sys.contracts",
    "todos.list",
    "workspace.list"
  ]
}
```

### Graceful Shutdown and Socket Recovery

When `autodoc --serve` receives `SIGTERM`, `SIGINT`, or a `sys.shutdown` RPC call:

1. It stops accepting new RPC requests and cancels background embedding workers cleanly.
2. It flushes all pending `core/follow` watcher events and runs `PRAGMA wal_checkpoint(TRUNCATE)` on every open workspace SQLite store so `index.db-wal` is folded completely into `index.db`.
3. It unlinks `autodoc.sock` and releases the `index.db.lock` advisory file lease before exiting with code `0`.

If a previous daemon process was ever terminated abruptly via `SIGKILL` (`kill -9`), the next client invocation detects that `autodoc.sock` refuses connection, verifies that `index.db.lock` is unheld, unlinks the stale socket file, and boots a fresh daemon automatically—recovering the WAL journal cleanly via SQLite's built-in crash recovery.

---

[← Previous: Book Index](./README.md) | [Index](./README.md) | [Next: Chapter 02: Quickstart & Workspaces →](./02-quickstart-and-workspaces.md)

