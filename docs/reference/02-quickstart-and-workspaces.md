# Chapter 2: Quickstart and Workspaces

Getting productive with AutoDoc takes less than sixty seconds. Because AutoDoc treats your existing directory of Markdown files as canonical, initializing a workspace never alters, renames, or reformats your notes unless you explicitly save changes or invoke a document operation.

This chapter walks through installing the `autodoc` binary, launching your first interactive session, configuring multi-workspace policies (`workspace.add`), understanding how the **watch-before-scan** engine (`core/follow`) guarantees zero missed edits during Git branch switches, and inspecting live index health (`index.status`).

---

## 2.1 Installing AutoDoc

AutoDoc ships as a single binary containing the daemon (`--serve`), the terminal UI (`--ui`), the native GPU desktop interface (`--gui`, in the builds with the GUI: see [The GUI Build](#the-gui-build-cgo)), the headless RPC client (`--call`), and the offline HTML exporter (`--export`).

New to AutoDoc? The [tutorials](../tutorials/README.md) start here, a few minutes each; the
[Reference Book](README.md) has the whole story.

| Platform | Install |
| --- | --- |
| Linux, macOS, with Go 1.26.9+, the GUI | `GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Any, with Go 1.26.9+, the TUI | `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| macOS, Linux | [mise](https://mise.jdx.dev): `mise use -g github:yongjohnlee80/autodoc` (the GUI build) |
| Linux, a server or container | mise, the TUI build: `mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-amd64-tui.tar.gz]'` (`autodoc-*-linux-arm64-tui.tar.gz` on arm64) |
| macOS, Linux | [Homebrew](https://brew.sh): `brew install yongjohnlee80/tap/autodoc` (on Linux, the TUI build) |
| Linux, macOS | A [release archive](https://github.com/yongjohnlee80/autodoc/releases/latest) (`amd64` and `arm64`, with SHA-256 checksums) |
| Windows | Use [WSL2](https://learn.microsoft.com/windows/wsl/install) and any Linux method. A native Windows build is not published yet. |

Use `autodoc --gui` to open the native GUI. It shares the same daemon and store as `autodoc --ui`
and `autodoc --call`. `--gui` is the flag you run it with; `-tags gui` is the build tag that builds the
GUI into the binary. On Linux, mise and the plain release archive are the GUI build, and so is
`go install -tags gui`; the `-tui` archive, the Linux Homebrew formula and a `go install` without
`-tags gui` are the TUI without GUI support.

The enterprise search server (`autodoc --server`, [docs/enterprise](../enterprise/README.md)) is
in its own build: a `linux-<arch>-server` release archive, `make build-server`, or `-tags server`.
The other builds leave it out, and say so when asked for `--server`.

### Package Managers (`brew` and `mise`)

Each installs the one `autodoc` binary. Homebrew and mise install the release's binaries, and Homebrew
pins each to the SHA-256 the release published: nothing is compiled on your machine, so neither needs
Xcode. Start it with `autodoc --ui`: the TUI starts the daemon when nothing answers.

**Which build mise installs.** mise picks the release archive that matches the machine:
- On macOS that is the `darwin-<arch>` archive, which has the GUI.
- On Linux it is the plain `linux-<arch>` archive, the GUI build. It links the window system's
  libraries (EGL, Wayland, X11), so it starts only where they are installed, even for `autodoc --ui`.
  Over SSH to a desktop it runs. On a server or in a container without them, name the `-tui`
  archive, the terminal build:

  ```sh
  mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-amd64-tui.tar.gz]'   # amd64
  mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-arm64-tui.tar.gz]'   # arm64
  ```

### Building from Source (`go install`)

If you have Go 1.26.9 or newer installed, you can compile AutoDoc directly from source. `go install` builds from the tagged source, and `autodoc --version` then reports the module's
version, as a release binary does.

```bash
# The terminal UI
go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest

# Verify binary version and supported features
autodoc --version
```

**`go install` on macOS** compiles with cgo, because the daemon watches workspace roots with the
system's FSEvents: it needs the Command Line Tools (`xcode-select --install`; not the whole of Xcode).
Without them the build stops at `xcrun: error: invalid active developer path`. Either install them,
or build without cgo — `CGO_ENABLED=0 go install …` — and the daemon polls the roots instead of
watching them: it works, but a change is noticed only after a scan of the root. On Linux, the
TUI's `go install` needs no C compiler (the daemon uses inotify, and the store is pure Go).

**The GUI with `go install`** needs `-tags gui`, on every platform: without it, `go install` builds
the TUI alone, and `autodoc --gui` says so. With it, the build uses cgo, so it needs a C compiler
and, on Linux, the window system's development libraries ([The GUI](#the-gui-build-cgo) lists them):

```sh
GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest
```

`GOBIN` puts the binary in `~/.local/bin` (keep it on your `PATH`) rather than in `$(go env GOPATH)/bin`.

### Building from a Checkout (`make`)

Go 1.26.9. `make build` links no cgo: the daemon and the TUI. `make build-gui` adds the GUI (`--gui`,
golib's gui on Gio), the one part that needs cgo; on macOS `make build` is `make build-gui`.

```sh
git clone https://github.com/yongjohnlee80/autodoc && cd autodoc && make build   # → bin/autodoc
```

`make build` stamps the version from `git describe`. The tests and the checks to run are in
[CONTRIBUTING.md](../../CONTRIBUTING.md#running-the-checks). Before sending a change, read
[CONTRIBUTING.md](../../CONTRIBUTING.md).

### The GUI Build (cgo)

The GUI needs cgo, and it is the only part that does:

- **macOS:** `make build` includes it. The release binary has it too. (cgo is already needed on
  macOS, for the FSEvents watcher.)
- **`go install`:** add `-tags gui` (see [Install](#building-from-source-go-install)).
- **Linux:** `make build` is the static TUI alone, with no cgo. `make build-gui` adds the GUI,
  which needs the window system's development libraries:
  - Debian/Ubuntu: `libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev
    libgles2-mesa-dev libegl1-mesa-dev libxcursor-dev libxfixes-dev`;
  - Arch: `wayland libx11 libxkbcommon libxkbcommon-x11 libxcursor libxfixes mesa`.

  Gio's Vulkan backend is optional and is not in that list: the GUI draws through EGL/OpenGL by
  default (a Wayland window tries EGL first unless the app asks for `app.PreferVulkan`, and X11
  keeps Vulkan off entirely), so a build without it is a complete GUI. `make build-gui` probes for
  `vulkan/vulkan.h` and builds `-tags gui,novulkan` when it is absent, saying so. Install
  `libvulkan-dev` (Debian/Ubuntu) or `vulkan-headers` (Arch) to compile it in. `VULKAN=1` requires
  it and fails naming the package; `VULKAN=0` leaves it out even where it is installed.

  The releases carry both: `autodoc-<tag>-linux-<arch>` (the GUI) and
  `autodoc-<tag>-linux-<arch>-tui` (static, the TUI). The Neovim plugin's `build.lua` installs the
  GUI one when it runs in a graphical session (`WAYLAND_DISPLAY` or `DISPLAY`), else the `-tui` one;
  `AUTODOC_GUI=1` or `0` chooses.

A build without the GUI says so when asked for `--gui`, and how to get one.

> [!TIP]
> On Linux, the GUI build links `libEGL`, `libwayland-*`, `libxkbcommon(-x11)`, `libX11(-xcb)`, `libXcursor` and `libXfixes`, so it does not start without them, even for `autodoc --ui` or `autodoc --call`. On a headless server or in a container, install the `-tui` build (or `make build`): it is static and needs no display libraries.

---

## 2.2 First Launch: `autodoc --ui` and `autodoc --gui`

AutoDoc uses explicit mode flags (`--ui` for the terminal interface, `--gui` for the native Gio desktop window). To register your first workspace and open it immediately:

```bash
# Register a workspace named "engineering-kb" (starts the daemon automatically if needed)
autodoc --call workspace.add '["engineering-kb", "/home/me/Notes/engineering-kb", ["**/*.md"], [".git/**"]]'

# Open the workspace in the terminal TUI (or omit the name to open the last-used workspace)
autodoc --ui engineering-kb
```

When you run `autodoc --call workspace.add` followed by `autodoc --ui engineering-kb` (or add the workspace interactively inside `autodoc --ui` with `Go › Manage workspaces…`), four things happen automatically:

1. **Daemon Bootstrap**: If `autodoc --serve` is not yet running, the client starts it in the background and connects over the `0600` Unix domain socket.
2. **Store-Backed Workspace Registration**: AutoDoc persists the workspace definition (`engineering-kb`) inside its SQLite store (`autodoc.db`) with your include and exclude globs.
3. **Watch-Before-Scan Indexing**: The daemon attaches filesystem watches, scans all matching `.md` files and `.todo-list/` tasks, extracts YAML frontmatter, splits prose into heading-aligned chunks, and populates the SQLite FTS5 index.
4. **Interactive Workspace Open**: The terminal TUI opens on the page: the **120-column page editor**, centred. The **Files**, **Outline** and **Relations** panels come when they are asked for (Chapter 3).

To launch the native desktop window instead of the terminal TUI, pass `--gui`:

```bash
autodoc --gui engineering-kb
```

### Choosing the Workspace, and Recording Input (`--debug`)

```sh
autodoc --ui                # attaches to the daemon (starting it when nothing answers), in the last workspace used
autodoc --ui kb             # in workspace kb; a name the daemon does not have is refused, with the names it has
autodoc --ui --debug         # also record the event stream to autodoc-debug-<date>.jsonl, for an input or IME bug report
autodoc --ui --debug=r.jsonl # the same, to r.jsonl
```

`--debug` is `--ui` and `--gui`'s: without one of them it is refused. It records the input and
event stream to one JSONL file — the window's platform events and IME state when there is a
window, the TUI's own trace in both modes — in the order everything happened, so one file reads
platform → IME → TUI. **The file contains everything typed, passwords included**: it is written
only where the flag says, never anywhere by default. How to record, and how a recording becomes
the test that reproduces a bug on any machine, is in [docs/filing-input-and-ime-bugs.md](../filing-input-and-ime-bugs.md).

### Profiling Memory and CPU (`--profile`, System › Profiling)

A profile shows where a slow or growing session spends its memory and CPU. Start one at launch, or
in the middle of a session that has already gone slow, without restarting it:

```sh
autodoc --gui --profile          # the window, profiled from launch into autodoc-profile-<date>-gui
autodoc --serve --profile=DIR    # the daemon, into DIR
autodoc --profile-fetch cpu DIR  # a 30-second CPU profile from a running monitor, into DIR
```

**System › Profiling** starts and stops the same monitors while the program runs:

- **Window** profiles this window's process, and **Backend** the daemon (through `sys.profile`, a
  local peer's alone). Each is checked while it runs, and the status line shows `◉ profiling`, so
  a monitor left on is seen. A window launched with `--profile` shows **Window** checked.
- **Capture CPU profile (30 s)** writes a CPU profile of whichever runs into its folder, and a
  notice names the file. `go tool pprof <file>` reads it.
- **Open profile folder** opens the last monitor's folder in the file manager.

A monitor started from the menu writes to `autodoc-profile-<mode>-<pid>` in the runtime directory
(`$XDG_RUNTIME_DIR`, else the per-user temporary directory on macOS, else the state directory), a
path short enough for the monitor's socket. A process has one monitor: a start while one runs keeps
it, and the program stops it at exit however it started.

Each monitor samples the Go runtime's memory into `samples.jsonl`, writes a heap profile and the
allocation sites holding the most each time the live heap grows, and serves its profiles on
`pprof.sock`. Go samples allocations from the process's start, so heap profiles taken late still
cover the whole session; a CPU profile covers only its own 30 seconds. Samples and heap profiles
hold allocation stacks, never a document's text, but a CPU or goroutine profile can hold the
program's pprof labels: **share those as you would a log**.

---

## 2.3 Registering and Configuring Workspaces

Workspaces in AutoDoc are stored inside the SQLite database (`autodoc.db`) rather than in `config.toml`, allowing you to add, rename, reconfigure and delete workspaces live from the TUI (`Go › Manage workspaces…`), the GUI, Neovim, or the CLI (`workspace.add` and `workspace.configure`) without restarting the daemon.

### What a Workspace Is: a Name, a Root and Patterns

A workspace has a name (what `--ui` and
every API call take), a root directory, and include and exclude patterns. New workspaces include
`**/*.md`, `**/*.txt`, `**/*.yaml`, and `**/*.yml`, and exclude `.git/**` and `**/node_modules/**`
by default. An excluded directory is never walked or watched. A workspace
added before node_modules joined the defaults, still with `.git/**` alone, moves to the current
default when the daemon starts; a workspace whose patterns were chosen keeps them. Patterns are
root-relative globs: each `/`-separated segment is a `path.Match` pattern, and
`**` matches any number of whole segments. Bounded brace alternatives such as `**/*.{md,txt}` are
expanded before matching. Existing workspaces keep their stored include patterns.

### Registering and Configuring a Workspace via CLI (`workspace.add` and `workspace.configure`)

`workspace.add` takes up to four positional arguments `[name, root, include?, exclude?]`, and `workspace.configure` updates any combination of workspace settings atomically in a single map:

```bash
# 1. Register the GlobalKB workspace with initial include/exclude globs
autodoc --call workspace.add '[
  "GlobalKB",
  "/home/me/kb",
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

### A Document's Resources

A design doc often shows files beside it, as `![flow](plan/flow.mmd)`:
diagrams, tables, pages. Include the ones that hold text, and they are searched with it and tied to
it:

```
**/*.md   **/*.mmd   **/*.csv   **/*.html
```

- Mermaid (`.mmd`) and CSV (`.csv`) are read as plain text, and HTML as the text derived from the
  page. So is source code AutoDoc highlights (`.sql`, `.go`, `.py`, …): it opens as code, with no
  rendered view. Leave pictures and other binary files out: a file AutoDoc does not know is read as
  Markdown.
- `![alt](path)` is an embed of the workspace path it names, as `![[./path]]` is: the resource's
  backlinks name the document that shows it. It is a link only to a note or to a file the
  workspace indexes, so a picture left out is not a missing link.

### The Manage Workspaces Dialog (`Go › Manage workspaces…`)

`Go › Manage workspaces…` lists the workspaces by title and directory on the left, and the settings
of the one under the cursor on the right. **Its settings are one dialog with two tabs**, opened by
Add…, Edit… or Advanced…; Ctrl+PageUp and Ctrl+PageDown switch tabs, and Enter in a field saves:

- **Edit**: the title; the root (typed or browsed to when the workspace is added, then fixed: add
  another directory as a workspace of its own); the frontmatter schema; the file types; and the
  include and exclude lists, semicolon-separated root-relative patterns (a blank include matches no
  files). Markdown, plain text and YAML are toggled here, and your own text types (`.log`, `.rst`,
  …) are entered here: each is read as UTF-8 plain text by declaration (never sniffed) and admitted
  with an include unless the rules already admit it. **Code** (`.go`, `.ts`, `.tsx`, `.js`, `.jsx`,
  `.mjs`, `.py`, `.rs`) and **documents** (`.docx`, `.htm`, `.html`) are turned on in their own
  rows: every build reads them. Code is cut a chunk per declaration and embedded by its signature
  and doc comment; turning it on also writes `vendor/**`, `target/**`, `dist/**` and `build/**` to
  the excludes. A document is read as its text: a DOCX's paragraphs, headings, lists and tables; an
  HTML page's content, without its scripts, styles, navigation or footer (only its `main` or
  `article` when it has one). `.doc`, `.odt` and `.pdf` are read only by a build with a deriver for
  them (see [Builds of your own](09-code-symbol-indexing.md)). None of these can be declared as text: a
  text type a build before this one stored for `.html` is dropped from the field with a note, and
  HTML stays included, as a document. `Options › Editor preferences… › File types…` opens this tab
  for the workspace in use.
- **Advanced**: the section size, the embedding policy and the embedding provider; and, where the
  edition offers them, the databases: a **source** (postgres or sqlite) that the workspace's `.view`
  files read, with the arguments they take, and the **destination** its index is kept in, the local
  store or postgres with pgvector (with its schema and vector index, hnsw or ivfflat). A
  connection's DSN is sealed in the store and never shown again: the dialog shows where it points,
  and a blank DSN keeps the stored one.

Save sends what changed in one `workspace.configure`, which saves all of it or none: a refused
setting leaves every setting as it was, and the dialog opens again as it was typed with the reason.

### Daemon Configuration (`config.toml`)

Because workspaces and sealed provider keys live in the SQLite store (`core/config/config.go` explicitly rejects unknown keys or legacy `[[workspace]]` / `[embedding]` sections so typos are caught at startup), `~/.config/autodoc/config.toml` is reserved strictly for daemon-level `[server]`, `[follow]`, and `[embedding_queue]` settings:

```toml
# $XDG_CONFIG_HOME/autodoc/config.toml
[server]
socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
state_dir = ""              # default: $XDG_STATE_HOME/autodoc (the daemon's log)
data_dir = ""               # default: $XDG_DATA_HOME/autodoc (the store, autodoc.db)

[follow]
poll_interval = "2s"        # the listing interval when a root cannot be watched

[embedding_queue]
max_inflight = 1            # requests to the shared provider, 1 or 2
```

A missing file is every default.

The embedding providers are kept in the store too, their API keys sealed, and
added and chosen in the TUI's Search models dialog (see [Semantic search](06-configuring-embeddings-and-rerankers.md)).

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

The files change under AutoDoc at any time: an editor, git, an agent. `core/follow` makes the index
converge on the root's content, and treats every event as a hint that names a path:

- **It watches first, then scans.** The start scan runs after the watch is up, so a change made during
  the scan is already in the watch stream.
- **It never has a silent gap.** Where the driver has no watch, or the watch fails at setup, it polls.
  A watch that ends is set up again, with a full scan.
- **A subtree it cannot read is unknown, not empty.** Its indexed documents are kept, it is rescanned
  with backoff until it reads, and the status names it.
- **Degraded still converges.** If neither the watch nor the poll can be set up (the root itself
  unreadable), it scans the whole root every poll interval and keeps retrying the setup.

![Watch-Before-Scan Workspace Boot Sequence](workspace-boot-sequence.mmd)

### How Watch-Before-Scan Converges Without Gaps

1. **Attach Watches First**: Before reading a single Markdown file's contents, `core/follow` walks the directory hierarchy to register recursive OS watch descriptors (`inotify` on Linux, `FSEvents` on macOS) and begins buffering incoming filesystem events in memory.
2. **Baseline Snapshot Scan**: While filesystem events accumulate in the buffer, the scanner walks the tree, compares each file against the SQLite store, and hands any added or changed file to the indexer.
3. **Coalesced Buffer Drain**: Once the baseline scan finishes, `core/follow` drains the buffered watch events. Multiple rapid events on the same path (such as Neovim writing a temp file and executing `rename(2)` over the target `.md` file) are coalesced by path.
4. **Version Idempotency**: Each coalesced path is reconciled against the store. If the baseline scan already indexed that exact file version, the buffered event is a no-op; if a concurrent edit landed after the scanner visited that path, the follower hands the path to the indexer again, which indexes the newer version.

The follower never writes the index. It hands paths to the indexer, the store's one writer, which
decides from each file's current state whether to delete, skip or re-index it.

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
  - An agent stays in the workspace it was started in when you switch the editor to another one: its `$AUTODOC_WORKSPACE` and `$AUTODOC_ROOT` don't change, and nothing is typed into its terminal.
    - `autodoc --editor editor.workspace` tells it the editor's workspace, and the one switch since it last asked: A → B → C is told as A → C. It needs no permission.
    - A Claude Code agent is told with your next message. AutoDoc adds a `UserPromptSubmit` hook running `autodoc --workspace-notice` to the agent folder's `.claude/settings.json`, keeping anything else in that file. Other agent CLIs are asked by their guide to call `editor.workspace` before acting on a message.
    - `editor.document` marks a buffer from another workspace `foreign_workspace`.

---

[← Previous: Chapter 01: Architecture & Surfaces](./01-architecture-and-surfaces.md) | [Index](./README.md) | [Next: Chapter 03: Modal Editor & Panels →](./03-modal-editor-and-panels.md)

