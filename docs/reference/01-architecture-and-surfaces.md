# Chapter 1: Architecture and Surfaces

AutoDoc is a local-first, file-canonical knowledge base engine, modal technical editor, and hybrid retrieval system written in Go. Unlike proprietary cloud wikis that trap prose inside opaque databases or heavy Electron wrappers that consume hundreds of megabytes of RAM per window, AutoDoc separates **canonical plain-text storage** on disk from a **high-speed derived index** served over a single local daemon.

Whether you author Architecture Decision Records (ADRs) in the terminal TUI (`autodoc --ui`), inspect live Mermaid diagrams and Vega-Lite telemetry charts in the GPU-accelerated native GUI (`autodoc --gui`), navigate cross-repository links inside Neovim (`autodoc.nvim`), automate knowledge workflows from AI agents (`autodoc --call`), or scale across engineering teams (`autodoc --server`), every surface speaks the same protocol and operates on the same underlying Markdown files.

---

## 1.1 The File-Canonical Philosophy

AutoDoc indexes and searches a tree of Markdown files, and follows the files as they change. The files
are canonical: the index is derived from them, lives outside the tree, and can always be rebuilt.

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
2. **External Editor Harmony**: You can edit a file with `nvim`, `sed`, `awk`, or an AI coding assistant while `autodoc --ui` or `autodoc --gui` is open. The daemon's filesystem follower (`core/follow`) detects the atomic rename or write within milliseconds, re-parses only the changed sections, and records the change for the connected clients, which pull it (`index.changes`, §1.3).
3. **Disposable, Reconstructible Index**: A workspace's index in the SQLite store (`autodoc.db`) is a derived cache containing parsed frontmatter fields, section chunks, FTS5 lexical postings, wikilink edges, and vector embeddings. AutoDoc can rebuild the same index state from your Markdown files and `.todo-list/` YAML records at any time. The store also keeps what is not derived from the files: the workspaces and the embedding providers (§1.6).

![AutoDoc Daemon and Surface Topology](daemon-topology.mmd)

### Canonical Write Path vs. Read Path

When you save a buffer in `autodoc --ui` or `autodoc --gui` (or invoke `doc.put` over RPC), AutoDoc never writes to the SQLite index first. Instead, it follows a strict two-phase canonical pipeline:

1. **Atomic Filesystem Commit**: The document module (`core/doc`) serializes the YAML frontmatter and Markdown body to a sibling temporary file (`.autodoc-tmp-<rand>`) in the target directory, flushes file data with `fsync`, and atomically replaces the target `.md` file via `os.Rename`.
2. **Synchronous Index Ingestion**: Immediately after the rename succeeds—or when `core/follow` observes an external process performing a write—the indexer computes the SHA-256 content digest, extracts structural chunks and `[[wikilinks]]`, and commits the derived rows to the SQLite WAL store.

---

## 1.2 Single-Writer Lease and SQLite WAL Store (`core/store`)

A common failure mode in local knowledge tools occurs when multiple processes—such as a background CLI script, a running GUI window, and an editor plugin—attempt to write to an embedded SQLite database simultaneously, resulting in `SQLITE_BUSY` lock contention or corrupted state.

AutoDoc eliminates multi-process write contention in `core/store` through two mechanisms:

- **Single-Writer Lease (`flock`)**: Only the active daemon process (`autodoc --serve`) holds the exclusive `flock(2)` on a sidecar lease file beside the store, named for the store's device and inode (`.autodoc-lease-<device>-<inode>`, `core/store/lease.go`). Because the lease names the database rather than the path spelling, a symlink or hardlink to the same `autodoc.db` resolves to the same lease.
- **Write-Ahead Logging (`PRAGMA journal_mode=WAL`)**: The SQLite store is configured in WAL mode with `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, and bounded WAL autocheckpointing (`wal_autocheckpoint=1000`). Inside the daemon, a dedicated writer coordinator serializes index batch commits (document upserts, chunk replacements, link graph rebuilds, and vector writes) while multiple read workers execute concurrent snapshot queries (`search.query`, `doc.get`, `graph.backlinks`) without blocking either the writer or one another.

### Content-Hash Deduplication

When any document changes on disk, `core/store` compares the incoming file's SHA-256 digest against `documents.content_hash` and compares each individual section's hash against `chunks.chunk_hash`:

- **Document-Level Short-Circuit**: If a file is touched (`touch note.md`) or saved without changes, `documents.content_hash` matches and indexing finishes in `< 0.1 ms` with zero SQLite writes.
- **Chunk-Level Embedding Reuse**: When you edit a single paragraph in a 20-section engineering design doc, the 19 untouched sections keep their identical `chunk_hash` values. Their existing dense vector embeddings are preserved in place, so the embedding worker only computes vectors for the 1 modified section.

---

## 1.3 The msgpack-RPC Daemon (`autodoc --serve`)

At the center of AutoDoc's runtime architecture is `autodoc --serve`, a lightweight background daemon that manages workspace watchers, the SQLite WAL store, and the hybrid ranking pipeline.

It is one binary with the modes as flags, after [AutoDB](https://github.com/yongjohnlee80/autodb):
`autodoc --serve` is the daemon, and the TUI (`--ui`), the native GUI (`--gui`), Neovim
([autodoc.nvim](18-neovim-integration-and-kb-migration.md)), plugins and `autodoc --call` are its clients, all over one msgpack-RPC API
on a 0600 unix socket. It also keeps the workspaces' todo lists (Todos › Open todos…, the `todos.*`
verbs).

```sh
autodoc --serve             # or: autodoc --serve --config path/to/config.toml
```

It listens on its unix socket, mode 0600: the file is the access control, so there is no login
locally. It opens the store, takes its lease, and serves every workspace in it, then records where
it serves (the store's lease-info, beside the store). A second `--serve` that finds this store's
daemon already serving, on the same socket or another config's, says where and exits 0; anything
else on its socket, or a lease it cannot account for, is refused.

All surfaces communicate with the daemon over a user-private Unix domain socket named `autodoc.sock`, created with strict `0600` filesystem permissions. Unless `server.socket` is set in `config.toml`, the daemon resolves its directory in this order (`core/config/config.go`, `runtimeDir`):

1. **`$XDG_RUNTIME_DIR`** → `$XDG_RUNTIME_DIR/autodoc.sock` (typical on Linux, e.g. `/run/user/1000/autodoc.sock`).
2. **macOS only:** the per-user `$TMPDIR` → `$TMPDIR/autodoc.sock`.
3. **Otherwise:** the default XDG state directory (`Server{}.StateDirPath()`) → `$XDG_STATE_HOME/autodoc/autodoc.sock` (else `~/.local/state/autodoc/autodoc.sock`; `server.state_dir` does not move the default socket).

The socket is the daemon's only access control, so both ends check who is at the other end (`internal/peercred`):

- The daemon binds only in a folder that is yours (or root's) and that neither its group nor others can write. A `server.socket` in a shared folder such as `/tmp` is refused, sticky bit or not: another user could bind that path first, and a client would talk to their process. Every default above passes.
- The socket file is created `0600` by the bind itself, not narrowed by a `chmod` afterwards.
- The daemon drops any connection from a process of another user, and AutoDoc's own clients (`--call`, the TUI and GUI, `--editor`) refuse a daemon run by another user. Both read the kernel's record of the peer (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS), not the file's mode.
- A client started for a store refuses a daemon whose hello leaves out `store_id` (every daemon from protocol 13 says it).

The wire protocol uses **MessagePack-RPC** (`msgpack-RPC`), providing compact binary framing, sub-millisecond serialization overhead, and native compatibility with Neovim's built-in `vim.fn.sockconnect("pipe", ...)` and `vim.rpcrequest` APIs.

![Client Auto-Start and msgpack-RPC Handshake Sequence](client-rpc-flow.mmd)

### Transparent Auto-Start and Protocol Handshake

You rarely need to launch `autodoc --serve` manually. Whenever you invoke an interactive or CLI surface (`autodoc --ui`, `autodoc --gui`, `autodoc --call`, or `autodoc.nvim`), the client library executes a deterministic bootstrap sequence:

1. **Dial Socket**: The client attempts to connect to the local Unix domain socket.
2. **Spawn on Miss**: If the socket does not exist or refuses connection, the client spawns `autodoc --serve` as a detached background process and polls with exponential backoff until the socket becomes ready.
3. **Internal Protocol Handshake (`sys.hello`)**: Upon connecting, the client library automatically sends an internal `sys.hello` frame (`[{"protocol": 28, "name": "cli"}]`). The daemon replies with `{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}`. If a binary upgrade occurred while an older daemon was still running, and the older daemon refuses the new build's protocol, the client keeps it as stale, for a restart (`autodoc --print-endpoint --restart`, §1.6).

Every client speaks one API. A session starts with `sys.hello({protocol, name})`, and until then
only `sys.hello` answers. The daemon serves a range of protocols (`min_protocol` to its own): a
session keeps the one it declared, and a verb added after it answers as an unknown method.

### Following Changes: the Server Only Answers

The server only answers: it never sends a notification. A client follows changes by pulling
`index.changes` from a cursor. When that cursor has expired, it takes `index.status`'s cursor, re-lists
with `index.list`, and resumes `index.changes` from that cursor. Errors carry a code that says what
to do next: re-list, merge a conflict, read after a write that landed, and so on.

### Core Daemon Modules

The daemon exposes a structured namespace of RPC methods organized by domain module (`autodoc --contracts`):

[AGENTS.md](../../AGENTS.md)'s tables list every verb with its params, its answer, the protocol it arrived
in and its error codes; they are generated from the verbs' contracts, which `autodoc --contracts`
prints as JSON with no daemon (`--format md` for the tables), and `sys.contracts` answers on a
connection.

- **`sys.*`**: Session negotiation and shutdown (`sys.hello`, `sys.shutdown` — internal to client libraries), edition and verb capability discovery (`sys.capabilities`), live configuration/lifecycle events, which clients poll (`sys.events`), self-describing protocol catalog inspection (`sys.contracts`), and the daemon's own memory monitor, started and stopped at runtime by a local peer (`sys.profile`, internal; System › Profiling › Backend).
- **`workspace.*`**: Multi-workspace lifecycle (`workspace.list`, `workspace.add`, `workspace.rename`, `workspace.remove`) and store-backed policy configuration (`workspace.configure`, `workspace.set_patterns`, `workspace.set_schema`, `workspace.section_size`, `workspace.embedding_policy`, `workspace.set_provider`, `workspace.set_text_extensions`, `workspace.focus`, `workspace.of`, `workspace.vars`, `workspace.set_var`).
- **`doc.*`**: Versioned document reading (`doc.read`, `doc.read_raw`), optimistic-concurrency writing (`doc.write`, `doc.write_raw`), heading outline extraction (`doc.outline`), frontmatter schema validation (`doc.validate`), and atomic file operations (`doc.rename`, `doc.remove`).
- **`index.*`**: Real-time watcher and embedding queue status (`index.status`), frontmatter-projected document listing (`index.documents`, `index.list`), incremental cursor change feeds (`index.changes`), single-file re-indexing (`index.reindex`), and vector model inspection/cleanup (`index.models`, `index.purge_model`).
- **`search.*`, `embedding.*`, & `ranker.*`**: Hybrid lexical/semantic/reranked retrieval across documentation and code declarations (`search.query`) and store-backed embedding/reranker provider management (`embedding.*`, `ranker.*`).
- **`graph.*`**: Outgoing links (`graph.links`), incoming backlinks (`graph.backlinks`), $N$-hop neighborhood subgraphs (`graph.neighborhood`), broken-link diagnostics (`graph.unresolved`), and live unsaved target resolution (`graph.resolve`).
- **`todos.*`**: The structured task engine (`todos.list`, `todos.get`, `todos.add`, `todos.update`, `todos.status`, `todos.assign`, `todos.remove`, `todos.refresh`, `todos.vars`, `todos.set_var`, `todos.changes`, `todos.ack`) backed by `.todo-list/` Markdown+YAML files and `core/todo/sqlitejournal`.

---

## 1.4 The Five AutoDoc Surfaces

AutoDoc provides five specialized surfaces tailored to distinct environments and workflows. Because state lives in the daemon and on disk, you can run multiple surfaces simultaneously against the same workspace—for example, keeping `autodoc --gui` open on a secondary monitor for live diagram previews while editing code and querying notes from `autodoc.nvim` or `autodoc --call` in your terminal.

![Illustrative Resident Memory Profile Across Surface Architectures](memory-footprint-comparison.chart.json)

*Note: The chart above illustrates the architectural memory difference between native compiled Go/Gio surfaces (which share a single background `autodoc --serve` daemon) and desktop applications that bundle a dedicated Chromium + Node.js runtime.*

### 1. Terminal TUI (`autodoc --ui`)

Running `autodoc --ui` (or `autodoc --ui <workspace-name>`) opens the full-screen terminal interface inside any modern terminal emulator (Kitty, Alacritty, WezTerm, Ghostty, tmux, or over SSH). Built on a custom zero-allocation cell buffer renderer, `--ui` delivers a distraction-free **120-column centered page editor** with full Vim modal editing (`Normal`, `Insert`, `Visual`, `Command`) alongside an optional Modeless mode for users who prefer standard CUA shortcuts. Surrounding the editor are dockable panels for `Files`, `Outline`, `Relations`, `Todos`, `Results`, and embedded `TerminalTabs`.

### 2. Native Desktop GUI (`autodoc --gui`)

Invoking `autodoc --gui` (or `autodoc --gui <workspace-name>`) launches the native, GPU-accelerated desktop application powered by Gio (`gioui.org`). It shares 100% of the editor buffer engine, keybindings, leader chords, and panel layout with `--ui`, while adding crisp vector typography and **Rendered View** (`Ctrl+T`). In `--gui`, standalone block embeds—including `.mmd` Mermaid diagrams, `.chart.json` Vega-Lite charts, `.csv` data tables, and raster/vector images—render natively inline without spawning a headless browser. Clicking any rendered diagram or chart jumps directly into an inline source editor for that companion file.

**The renderer.** Options › Renderer chooses what draws the window:

- **Auto**, the default: Vulkan where the window can have it (on Linux under Wayland), OpenGL where it can't. On macOS the window draws through Metal either way.
- **OpenGL**: OpenGL always, for a machine whose Vulkan driver misbehaves.

The window chooses its renderer as it opens, so a change applies at the next start. The preference is `tui.gui.renderer` (`auto` or `opengl`), and the terminal build ignores it.

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
- **Socket (`autodoc.sock`)**: The `0600` msgpack-RPC Unix domain socket, at `server.socket` if set, else `$XDG_RUNTIME_DIR/autodoc.sock`, else (macOS) `$TMPDIR/autodoc.sock`, else `$XDG_STATE_HOME/autodoc/autodoc.sock` (`~/.local/state/autodoc/autodoc.sock`; resolved via `Server{}.StateDirPath()`).
- **State Directory (`server.state_dir`, else `$XDG_STATE_HOME/autodoc/`, else `~/.local/state/autodoc/`)**: Where the daemon's log lives.
- **Store Directory (`server.data_dir`, else `$XDG_DATA_HOME/autodoc/`, else `~/.local/share/autodoc/`)**:
  - `autodoc.db`, `autodoc.db-wal`, `autodoc.db-shm`: The SQLite WAL database files storing workspaces, chunks, links, vectors, and sealed provider configurations.
  - `autodoc.db.key`: The `0600` key that seals the store's secrets (connection DSNs, provider and ranker API keys). Back it up apart from the database: a store without it keeps everything but those secrets ([backing up and restoring](../ops/backup-and-restore.md)).
  - `.autodoc-lease-<device>-<inode>`: The sidecar file the running daemon holds an exclusive `flock(2)` on — the single-writer lease, named for the store's device and inode (`core/store/lease.go`).
  - `.autodoc-lease-<device>-<inode>.json`: The lease-info record (`store_id`, `store_path`, `addr`, `pid`, `instance`, `version`, `protocol`, `min_protocol`, `since`) a client reads to find the daemon serving this store, even when it was started with a different config and socket.

### Finding, Restarting, Checking and Compacting the Daemon (`--print-endpoint`, `--check-store`, `--compact`)

```sh
autodoc --print-endpoint            # unix<TAB><socket>: the config's, or the one serving its store
autodoc --print-endpoint --ensure   # the same, starting the daemon first when nothing answers
autodoc --print-endpoint --restart  # stop the daemon serving the store, start this build in its place
autodoc --check-store               # check the store (integrity, foreign keys, sealed secrets): JSON, exit 1 on a problem
autodoc --compact                   # with the daemon stopped: reclaim unused vectors, rewrite the store
```

`--check-store` asks the daemon serving the store to check it (`sys.check`), or, with none, opens
the store to check it without writing. The daemon also runs SQLite's `quick_check` each time it
opens the store (for a second at most, the rest in the background, and whole after a crash or
before schema scripts), and does not serve a damaged one; see [backing up and restoring](../ops/backup-and-restore.md).

`--restart` stops whatever daemon serves the store, of any protocol: it says `sys.hello` at that
daemon's own protocol, then `sys.shutdown`, both frozen across protocols. It waits for the
process to exit, so nothing starts while the store's lease is still held. Meanwhile it holds the
other clients back from starting one of their own. A daemon serving another store is refused and
left alone. With nothing serving, `--restart` only starts.

A client that dials the socket itself asks the binary where it is, so it never repeats the rules.

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

1. It stops accepting new RPC requests and cancels background embedding workers.
2. It closes the SQLite store; SQLite folds the WAL back into `autodoc.db` on a clean close.
3. It unlinks `autodoc.sock` **only if the path is still the socket this process bound** (`removeIfStillOurs` in `app/socket.go`), so a shutting-down daemon never deletes a successor's socket. Closing the process releases the `flock` on `.autodoc-lease-<device>-<inode>`.

If a previous daemon was terminated abruptly via `SIGKILL` (`kill -9`), the kernel releases its `flock` with the process, so the next daemon can take the lease. When that daemon binds the socket and finds a leftover `autodoc.sock` that **refuses connections** (`ECONNREFUSED`: no listener), it treats the file as a crashed daemon's, removes it, and binds a fresh one (`listen` in `app/socket.go`). Any other dial outcome — an answer, a timeout, or permission denied — leaves the path alone, since a live listener can produce those too. SQLite's built-in crash recovery replays the WAL when the store reopens.

---

[← Previous: Book Index](./README.md) | [Index](./README.md) | [Next: Chapter 02: Quickstart & Workspaces →](./02-quickstart-and-workspaces.md)

