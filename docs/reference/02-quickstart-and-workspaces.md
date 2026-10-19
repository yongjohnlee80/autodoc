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

If you have Go 1.26.9 or newer installed, you can compile AutoDoc directly from source. The default build is the terminal UI; the native Gio GUI needs `-tags gui` (and so cgo, a C compiler, and on Linux the window system's development libraries):

```bash
# The terminal UI
go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest

# The terminal UI and the GUI
GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest

# Verify binary version and supported features
autodoc --version
```

> [!TIP]
> On Linux systems using Wayland or X11, `autodoc --gui` dynamically loads `libwayland-client`, `libxkbcommon`, and Vulkan/EGL drivers at runtime when launched in GUI mode, while `autodoc --ui` and `autodoc --call` run cleanly on headless servers without any display libraries installed.

---

## 2.2 First Launch: `autodoc --ui` and `autodoc --gui`

AutoDoc uses explicit mode flags (`--ui` for the terminal interface, `--gui` for the native Gio desktop window). To register your first workspace and open it immediately:

```bash
# Register a workspace named "engineering-kb" (starts the daemon automatically if needed)
autodoc --call workspace.add '["engineering-kb", "/home/johno/Notes/engineering-kb", ["**/*.md"], [".git/**"]]'

# Open the workspace in the terminal TUI (or omit the name to open the last-used workspace)
autodoc --ui engineering-kb
```

When you run `autodoc --call workspace.add` followed by `autodoc --ui engineering-kb` (or add the workspace interactively inside `autodoc --ui` by pressing `w` to open the Workspace Manager), four things happen automatically:

1. **Daemon Bootstrap**: If `autodoc --serve` is not yet running, the client starts it in the background and connects over the `0600` Unix domain socket.
2. **Store-Backed Workspace Registration**: AutoDoc persists the workspace definition (`engineering-kb`) inside its SQLite store (`autodoc.db`) with your include and exclude globs.
3. **Watch-Before-Scan Indexing**: The daemon attaches filesystem watches, scans all matching `.md` files and `.todo-list/` tasks, extracts YAML frontmatter, splits prose into heading-aligned chunks, and populates the SQLite FTS5 index.
4. **Interactive Workspace Open**: The terminal TUI opens with the **Files** tree docked on the left, the **120-column page editor** centered in the middle, and the **Outline** and **Relations** inspector ready on the right.

To launch the native desktop window instead of the terminal TUI, pass `--gui`:

```bash
autodoc --gui engineering-kb
```

---

## 2.3 Registering and Configuring Workspaces

Workspaces in AutoDoc are stored inside the SQLite database (`autodoc.db`) rather than in `config.toml`, allowing you to add, rename, and reconfigure workspaces live from the TUI (`w`), the GUI, Neovim, or the CLI (`workspace.add` and `workspace.configure`) without restarting the daemon.

### Registering and Configuring a Workspace via CLI (`workspace.add` and `workspace.configure`)

`workspace.add` takes up to four positional arguments `[name, root, include?, exclude?]`, and `workspace.configure` updates any combination of workspace settings atomically in a single map:

```bash
# 1. Register the GlobalKB workspace with initial include/exclude globs
autodoc --call workspace.add '[
  "GlobalKB",
  "/home/johno/.config/nvim/.auto-agents-config/kb",
  ["**/*.md"],
  [".git/**", "archive/raw-dumps/**"]
]'

# 2. Configure retrieval, schema, and embedding policies atomically
autodoc --call workspace.configure '[
  "GlobalKB",
  {
    "schema": "_schema/frontmatter.yaml",
    "section_tokens": 400,
    "abstract_chunk": true,
    "demote_superseded": true,
    "embedding_policy": "always",
    "todo_search": true,
    "graph_only": ["archive/**"]
  }
]'
```

### Daemon Configuration (`config.toml`)

Because workspaces and sealed provider keys live in the SQLite store (`core/config/config.go` explicitly rejects unknown keys or legacy `[workspace]` / `[embedding]` sections so typos are caught at startup), `~/.config/autodoc/config.toml` is reserved strictly for daemon-level `[server]`, `[follow]`, and `[embedding_queue]` settings:

```toml
[server]
# Optional custom paths; omit to use XDG defaults
# socket = "/run/user/1000/autodoc.sock"
# state_dir = "/home/johno/.local/state/autodoc"
# data_dir = "/home/johno/.local/share/autodoc"

[follow]
poll_interval = "2s"

[embedding_queue]
max_inflight = 1
```

![Complete Workspace Settings Reference](workspace-settings-reference.csv)

### Deep Dive into Key Workspace Policies

- **`section_tokens` (Default: `0`, which uses the `400`-token default)**: Rather than slicing Markdown files at arbitrary character offsets that sever code blocks or tables in half, AutoDoc's structural chunker splits documents along Markdown heading boundaries (`#`, `##`, `###`). If a single section exceeds `section_tokens`, the chunker subdivides at paragraph or fenced-block boundaries while preserving the full ancestor heading breadcrumb on every chunk.
- **`abstract_chunk` (Default: `false`)**: When enabled (`true`), AutoDoc synthesizes a dedicated chunk `0` for every document from its YAML frontmatter `abstract`. During hybrid retrieval, matches in the abstract chunk help surface high-level overview documents alongside specific section hits.
- **`demote_superseded` (Default: `false`)**: Mature engineering knowledge bases keep historical ADRs and retired runbooks for auditability (`status: superseded` / `superseded_by`). With `demote_superseded = true`, these documents remain fully searchable and navigable via `[[wikilinks]]`, while their search hits are ranked below their active successor's hits unless you filter explicitly for `status:superseded`.
- **`graph_only` (Default: `[]`)**: A list of glob patterns (such as `["archive/**"]`) whose matching files are indexed only for the `[[wikilink]]` and frontmatter relation graph, skipping full-text and vector chunk indexing (ADR 1792015212).
- **`embedding_policy` (`"always"` | `"when opened"` | `"never"`)**: Controls how the background embedding worker interacts with your workspace (`workspace.embedding_policy`). `"always"` (the default) embeds changed files in the background whenever the daemon is running; `"when opened"` defers background embedding until a client opens the workspace; and `"never"` keeps the workspace strictly lexical and graph-indexed.

---

## 2.4 The Watch-Before-Scan Follower (`core/follow`)

A classic subtle bug in file-watching indexers is the **scan-to-watch race window**: if an indexer scans a directory tree first and attaches `fsnotify` / `inotify` watches afterward, any file modified by `git checkout`, `git rebase`, or an AI agent inside already-scanned subdirectories before the watcher attaches is silently missed until the next full restart.

AutoDoc prevents this race in `core/follow` by enforcing a strict **Watch-Before-Scan** state machine whenever a workspace boots or a new subdirectory appears.

![Watch-Before-Scan Workspace Boot Sequence](workspace-boot-sequence.mmd)

### How Watch-Before-Scan Converges Without Gaps

1. **Attach Watches First**: Before reading a single Markdown file's contents, `core/follow` walks the directory hierarchy to register recursive OS watch descriptors (`inotify` on Linux, `kqueue`/`FSEvents` on macOS) and begins buffering incoming filesystem events in memory.
2. **Baseline Snapshot Scan**: While filesystem events accumulate in the buffer, the scanner walks the tree, compares each file against the SQLite store, and indexes any added or changed files.
3. **Coalesced Buffer Drain**: Once the baseline scan finishes, `core/follow` drains the buffered watch events. Multiple rapid events on the same path (such as Neovim writing a temp file and executing `rename(2)` over the target `.md` file) are coalesced by path.
4. **Version Idempotency**: Each coalesced path is reconciled against the store. If the baseline scan already indexed that exact file version, the buffered event is a no-op; if a concurrent edit landed after the scanner visited that path, the follower immediately indexes the newer version.

![Illustrative Scaling Profile: Full Tree Scan vs Single-File Watch Update](indexing-throughput.chart.json)

As the illustrative scaling curve above shows, initial workspace tree scanning scales linearly ($O(N)$) with the number of files in the repository, whereas incremental `core/follow` updates remain $O(1)$—re-chunking and committing only the single modified file regardless of total workspace size.

---

## 2.5 Inspecting Index and Watcher Health (`index.status`)

You can verify that your workspace is synchronized, inspect document and chunk counts, check frontmatter diagnostics, and confirm whether background vector embeddings have caught up at any time using `index.status`:

```bash
autodoc --call index.status '["GlobalKB"]'
```

The daemon returns the `statusJSON` snapshot (`rpc/index.go`):

```json
{
  "cursor": 1842,
  "oldest_retained": 1,
  "docs": 618,
  "chunks": 3492,
  "pending_jobs": 0,
  "unparsed_frontmatter": [],
  "diagnosed": 0,
  "failing": [],
  "held": 0,
  "held_stale": 0,
  "held_unchecked": 0,
  "following": {
    "mode": "watching",
    "error": "",
    "retrying": []
  },
  "embeddings": {
    "provider": "local-qwen-0.6b",
    "model": "ollama:qwen3-embedding:0.6b:1024",
    "target": "ollama:qwen3-embedding:0.6b:1024",
    "texts": 3492,
    "pending": 0,
    "semantic": "ready",
    "last_error": "",
    "refused": 0,
    "target_pending": 0,
    "target_refused": 0,
    "refused_texts": []
  }
}
```

### Re-Indexing a Specific File (`index.reindex`)

If you ever want to force the daemon to re-parse and re-index a specific document immediately via RPC, call `index.reindex` with the workspace name and workspace-relative path:

```bash
autodoc --call index.reindex '["GlobalKB", "adrs/0042-sqlite-wal-lease.md"]'
```

And if you adjust workspace-wide chunking or glob policies via `workspace.section_size` or `workspace.set_patterns`, the daemon automatically reconciles the affected files in the background while continuing to serve read queries from the SQLite WAL snapshot.

---

## 2.6 Multi-Workspace Workflows and Scoped Queries

Engineers frequently work across multiple knowledge scopes at once: a personal or team-wide `GlobalKB` containing cross-cutting conventions and ADRs, alongside a repository-local `MonorepoDocs` workspace checked into the current Git worktree.

AutoDoc's daemon tracks an arbitrary number of simultaneous workspaces in a single `autodoc --serve` process:

```bash
# List all registered workspaces and their current settings
autodoc --call workspace.list '[]'

# Update an existing workspace's include and exclude globs
autodoc --call workspace.set_patterns '[
  "MonorepoDocs",
  ["adrs/**/*.md", "runbooks/**/*.md", "rfcs/**/*.md"],
  [".git/**", "vendor/**", "dist/**"]
]'

# Update an existing workspace's section token budget and embedding policy
autodoc --call workspace.configure '[
  "MonorepoDocs",
  {
    "section_tokens": 512,
    "embedding_policy": "when opened"
  }
]'

# Detach a temporary workspace without touching any Markdown files on disk
autodoc --call workspace.remove '["ScratchNotes"]'
```

### Environment Variables for Agent and Editor Integration

When running inside automated environments (such as `auto-agents.nvim`, embedded `TerminalTabs`, or CI runners), convention files and shell scripts commonly use standard environment variables to pass workspace context:

- **`$AUTODOC_WORKSPACE`**: Workspace name passed by agent/terminal harnesses to CLI invocations (`autodoc --call <verb> '["'"$AUTODOC_WORKSPACE"'", ...]'`).
- **`$AUTO_AGENTS_KB_ROOT`**: Canonical path to the active knowledge base root directory in `auto-agents`.
- **`$AUTODOC_EDITOR`** / **`$AUTODOC_AGENT`**: Exported inside AutoDoc's embedded agent terminals so `autodoc --editor` can talk directly to the hosting AutoDoc editor buffers.

---

[← Previous: Chapter 01: Architecture & Surfaces](./01-architecture-and-surfaces.md) | [Index](./README.md) | [Next: Chapter 03: Modal Editor & Panels →](./03-modal-editor-and-panels.md)

