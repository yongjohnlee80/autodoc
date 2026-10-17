# Chapter 2: Quickstart and Workspaces

Getting productive with AutoDoc takes less than sixty seconds. Because AutoDoc treats your existing directory of Markdown files as canonical, initializing a workspace never alters, renames, or reformats your notes unless you explicitly save changes or invoke a document operation.

This chapter walks through installing the `autodoc` binary, launching your first interactive session, configuring multi-workspace policies (`workspace.add`), understanding how the **watch-before-scan** engine (`core/follow`) guarantees zero missed edits during Git branch switches, and inspecting live index health (`index.status`).

---

## 2.1 Installing AutoDoc

AutoDoc ships as a single statically linked binary containing the daemon (`--serve`), the terminal UI (`--ui`), the native GPU desktop interface (`--gui`), the headless RPC client (`--call`), and the offline HTML exporter (`--export`).

### Package Managers (`brew` and `mise`)

On macOS and Linux, install the prebuilt release binary via Homebrew or `mise`:

```bash
# Homebrew (macOS & Linux)
brew install yongjohnlee80/tap/autodoc

# mise-en-place tool version manager
mise use --global github:yongjohnlee80/autodoc@latest
```

### Building from Source (`go install`)

If you have Go 1.25 or newer installed, you can compile AutoDoc directly from source. Building with the default tags includes both the terminal UI and the native Gio GUI:

```bash
# Install with full TUI, GUI, and FTS5 SQLite support
CGO_ENABLED=1 go install -tags "sqlite_fts5" github.com/yongjohnlee80/autodoc/cmd/autodoc@latest

# Verify binary version and supported features
autodoc --version
```

> [!TIP]
> On Linux systems using Wayland or X11, `autodoc --gui` dynamically loads `libwayland-client`, `libxkbcommon`, and Vulkan/EGL drivers at runtime when launched in GUI mode, while `autodoc --ui` and `autodoc --call` run cleanly on headless servers without any display libraries installed.

---

## 2.2 First Launch: `autodoc .`

To open any directory of Markdown notes immediately, navigate to the folder and run `autodoc .`:

```bash
cd ~/Notes/engineering-kb
autodoc .
```

When you run `autodoc .` for the first time on a directory, four things happen automatically in under a quarter of a second:

1. **Daemon Bootstrap**: If `autodoc --serve` is not yet running, the client starts it in the background and connects over the `0600` Unix domain socket.
2. **Ad-Hoc Workspace Registration**: If the directory is not already registered as a named workspace, AutoDoc registers it using the directory's basename (e.g., `engineering-kb`) with sensible default globs (`include: ["**/*.md"]`).
3. **Watch-Before-Scan Indexing**: The daemon attaches filesystem watches, scans all `.md` files and `.todo-list/` YAML tasks, extracts YAML frontmatter, splits prose into heading-aligned chunks, and populates the SQLite FTS5 index.
4. **Interactive Workspace Open**: The terminal TUI opens with the **Files** tree docked on the left, the **120-column page editor** centered in the middle, and the **Outline** and **Relations** inspector ready on the right.

To launch the native desktop window instead of the terminal TUI, pass `--gui`:

```bash
autodoc --gui ~/Notes/engineering-kb
```

---

## 2.3 Registering and Configuring Workspaces

While `autodoc .` is convenient for quick exploration, most users and teams maintain one or more persistent named workspaces—for example, a personal `GlobalKB` alongside per-repository architecture folders. Workspaces can be configured declaratively in `~/.config/autodoc/config.toml` or dynamically at runtime via the `workspace.*` RPC family.

### Registering a Workspace via CLI (`workspace.add`)

You can register or update a workspace from any terminal or setup script using `autodoc --call`:

```bash
autodoc --call workspace.add '[{
  "name": "GlobalKB",
  "root": "/home/johno/.config/nvim/.auto-agents-config/kb",
  "include": ["**/*.md"],
  "exclude": [".git/**", "archive/raw-dumps/**", ".todo-list/**"],
  "section_tokens": 384,
  "abstract_chunk": true,
  "demote_superseded": true,
  "embedding_policy": "auto",
  "todo_search": true,
  "schema": "_schema/frontmatter.yaml"
}]'
```

### Declarative Configuration (`config.toml`)

To persist workspace policies across machines in your dotfiles repository, define `[workspaces.<name>]` blocks in `~/.config/autodoc/config.toml`:

```toml
default_workspace = "GlobalKB"

[workspaces.GlobalKB]
root = "/home/johno/.config/nvim/.auto-agents-config/kb"
include = ["**/*.md"]
exclude = [".git/**", "node_modules/**", ".todo-list/**"]
section_tokens = 384
abstract_chunk = true
demote_superseded = true
embedding_policy = "auto"
todo_search = true
schema = "_schema/frontmatter.yaml"

[workspaces.MonorepoDocs]
root = "/home/johno/Source/Projects/platform/docs"
include = ["adrs/**/*.md", "runbooks/**/*.md", "rfcs/**/*.md"]
exclude = ["**/draft-scratch/**"]
graph_only = false
embedding_policy = "off"
```

![Complete Workspace Settings Reference](workspace-settings-reference.csv)

### Deep Dive into Key Workspace Policies

- **`section_tokens` (Default: `384`)**: Rather than slicing Markdown files at arbitrary character offsets that sever code blocks or tables in half, AutoDoc's structural chunker splits documents along Markdown heading boundaries (`#`, `##`, `###`). If a single section exceeds `section_tokens`, the chunker subdivides at paragraph or fenced-block boundaries while preserving the full ancestor heading breadcrumb (e.g., `ADR-0042 > 1.2 Single-Writer Lease`) on every chunk.
- **`abstract_chunk` (Default: `true`)**: When enabled, AutoDoc synthesizes a special chunk `0` for every document that concatenates the document's `title`, `tags`, and YAML frontmatter `abstract`. During hybrid retrieval, matches in the abstract chunk receive a structural boost, ensuring that high-level conceptual queries surface the authoritative overview document first.
- **`demote_superseded` (Default: `true`)**: Mature engineering knowledge bases keep historical ADRs and retired runbooks for auditability (`status: superseded`, `status: deprecated`, or `status: archived`). With `demote_superseded = true`, these documents remain fully searchable and navigable via `[[wikilinks]]`, but their search rank score is multiplied by a decay factor (`0.35x`) so active documents always outrank retired ones unless you explicitly filter for `status:superseded`.
- **`graph_only` (Default: `false`)**: For massive auxiliary repositories where you only need fast `[[wikilink]]` resolution, backlink discovery, and frontmatter validation without full-text or vector chunk storage, setting `graph_only = true` reduces index disk usage by over 80%.
- **`embedding_policy` (`"off"` | `"auto"` | `"eager"`)**: Controls how the background embedding pool interacts with your workspace. In `"auto"` mode, lexical FTS5 indexing commits immediately on every save, while dense vector embeddings are computed asynchronously during idle windows. Set `"off"` on battery-constrained laptops or air-gapped environments where deterministic BM25 + graph ranking is preferred.

---

## 2.4 The Watch-Before-Scan Follower (`core/follow`)

A classic subtle bug in file-watching indexers is the **scan-to-watch race window**: if an indexer scans a directory tree first and attaches `fsnotify` / `inotify` watches afterward, any file modified by `git checkout`, `git rebase`, or an AI agent inside already-scanned subdirectories before the watcher attaches is silently missed until the next full restart.

AutoDoc prevents this race in `core/follow` by enforcing a strict **Watch-Before-Scan** state machine whenever a workspace boots or a new subdirectory appears.

![Watch-Before-Scan Workspace Boot Sequence](workspace-boot-sequence.mmd)

### How Watch-Before-Scan Converges Without Gaps

1. **Attach Watches First**: Before reading a single Markdown file's contents, `core/follow` walks the directory hierarchy to register recursive OS watch descriptors (`inotify` on Linux, `kqueue`/`FSEvents` on macOS) and begins buffering incoming filesystem events in memory.
2. **Baseline Snapshot Scan**: While filesystem events accumulate in the ring buffer, the scanner walks the tree, computes each file's SHA-256 content hash, and commits any files whose hash differs from the SQLite store.
3. **Coalesced Buffer Drain**: Once the baseline scan finishes, `core/follow` drains the buffered watch events. Multiple rapid events on the same path (such as Neovim writing a `.swp` file, writing a temp file, and executing `rename(2)` over the target `.md` file) are debounced over a 15 ms window and coalesced by path.
4. **Content-Hash Idempotency**: Each coalesced path is checked against the store's SHA-256 digest. If the baseline scan already indexed that exact file version, the buffered event is a no-op; if a concurrent edit landed after the scanner visited that path, the follower immediately re-indexes the newer version.

![Workspace Indexing Throughput: Initial Scan vs Incremental Watch](indexing-throughput.chart.json)

As the throughput chart illustrates, cold lexical scanning indexes **1,000 Markdown files in ~220 ms** and **5,000 files in under 1 second**, while incremental `core/follow` updates complete in **4–9 ms** regardless of total workspace size.

---

## 2.5 Inspecting Index and Watcher Health (`index.status`)

You can verify that your workspace is synchronized, inspect chunk counts, and confirm whether background vector embeddings have caught up at any time using `index.status`:

```bash
autodoc --call index.status '["GlobalKB"]'
```

The daemon returns a structured health snapshot:

```json
{
  "workspace": "GlobalKB",
  "root": "/home/johno/.config/nvim/.auto-agents-config/kb",
  "state": "idle",
  "watcher_active": true,
  "watched_dirs": 42,
  "documents_total": 618,
  "chunks_total": 3492,
  "links_total": 1840,
  "todos_total": 74,
  "embeddings_pending": 0,
  "schema_errors": 0,
  "last_scan_ms": 134,
  "last_event_at": "2026-10-17T06:04:12Z"
}
```

### Forcing a Clean Rebuild (`index.rebuild`)

If you ever change a structural workspace policy—such as adjusting `section_tokens` from `384` to `512` or toggling `abstract_chunk`—you can trigger a non-blocking re-chunk and rebuild of the workspace index without restarting your open editor sessions:

```bash
autodoc --call index.rebuild '["GlobalKB", {"rechunk": true}]'
```

During `index.rebuild`, the SQLite WAL store continues serving read queries from the previous snapshot until the rebuild transaction commits atomically.

---

## 2.6 Multi-Workspace Workflows and Scoped Queries

Engineers frequently work across multiple knowledge scopes at once: a personal or team-wide `GlobalKB` containing cross-cutting conventions and ADRs, alongside a repository-local `docs/` workspace checked into the current Git worktree.

AutoDoc's daemon tracks an arbitrary number of simultaneous workspaces in a single `autodoc --serve` process:

```bash
# List all registered workspaces and their current watcher state
autodoc --call workspace.list '[]'

# Update an existing workspace's section token budget and exclude globs
autodoc --call workspace.update '["MonorepoDocs", {
  "section_tokens": 512,
  "exclude": [".git/**", "vendor/**", "dist/**"]
}]'

# Detach a temporary workspace without touching any Markdown files on disk
autodoc --call workspace.remove '["ScratchNotes"]'
```

### Environment Variables for Agent and Editor Integration

When running inside automated environments (such as `auto-agents.nvim` or CI runners), AutoDoc respects standard environment variables so scripts do not need to hardcode workspace names or paths:

- **`$AUTODOC_WORKSPACE`**: Default workspace name used when `--workspace` is omitted on CLI flags.
- **`$AUTO_AGENTS_KB_ROOT`**: Canonical path to the active knowledge base root directory.
- **`$AUTODOC_SOCKET`**: Override path for the Unix domain socket (useful for isolated integration tests that spin up an ephemeral daemon instance).
