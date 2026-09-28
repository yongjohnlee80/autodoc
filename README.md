# AutoDoc

AutoDoc indexes and searches a tree of Markdown notes, and follows the files as they change. The files
are canonical: the index is derived from them, lives outside the tree, and can always be rebuilt.

It is one binary with the modes as flags, after [AutoDB](https://github.com/yongjohnlee80/autodb):
`autodoc --serve` is the daemon, and the TUI (`--ui`), the Web-UI (`--web-ui`) and later AutoVim and a
native GUI are its clients, all over one msgpack-RPC API on a 0600 unix socket.

**Status: early.** The daemon serves, and the TUI edits; the Web-UI is next.

| Package | What it does |
| --- | --- |
| `core/config` | reads `$XDG_CONFIG_HOME/autodoc/config.toml`, and resolves the socket and the state directory |
| `core/workspace` | opens a workspace: its root as a `golib/vfs` filesystem, its include/exclude patterns, and the single-instance lease on its index store |
| `core/follow` | keeps a workspace's index following its root |
| `core/index` | the index store (one SQLite file per workspace), its indexer, the link graph, and search |
| `core/embed` | the optional embedding provider: Ollama, or any OpenAI-compatible endpoint |
| `core/docs` | reads and writes notes for AutoDoc's own apps, conditional on the version the writer read |
| `rpc` | the msgpack-RPC API: a projection of core, with no logic of its own |
| `tui` | the terminal UI: search, the notes, a Vim-keyed editor, backlinks; its screen written in QML |
| `cmd/autodoc` | the binary: `--serve` is the daemon, `--ui` the TUI |

## Configuration

```toml
# $XDG_CONFIG_HOME/autodoc/config.toml
[server]
socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
state_dir = ""              # default: $XDG_STATE_HOME/autodoc

[follow]
poll_interval = "2s"        # the watch fallback's listing interval

[embedding]                 # optional: without it, search is lexical
provider = "ollama"         # or "openai", for any OpenAI-compatible endpoint
model = "snowflake-arctic-embed"  # e.g.; there is no default model
base_url = ""               # default: http://localhost:11434 (ollama), https://api.openai.com (openai)
api_key_env = ""            # openai: the environment variable that holds the key

[[workspace]]
name = "kb"                 # the handle every API call names
root = "~/notes"            # one root per workspace
include = ["**/*.md"]       # default
exclude = [".git/**"]       # default
```

Patterns are root-relative globs: each `/`-separated segment is a `path.Match` pattern, and `**`
matches any number of whole segments. An unknown setting is an error, so a misspelling is reported.

An API key never goes in the file: `api_key_env` names the environment variable that holds it.

## The daemon

```sh
autodoc --serve             # or: autodoc --serve --config path/to/config.toml
```

It listens on its unix socket, mode 0600: the file is the access control, so there is no login
locally. It opens every workspace and takes a lease on its index. A workspace another instance
already serves is reported `busy`, and the rest are served. A second `--serve` on the same socket
exits, reporting the instance that answers there.

Every client speaks one API. A session starts with `sys.hello({protocol})`, and until then only
`sys.hello` answers. Every other verb takes the workspace name first:

| Group | Verbs |
| --- | --- |
| `sys` | `hello`, `shutdown` |
| `workspace` | `list` |
| `search` | `query(ws, q, {limit, mode, tags, paths})` |
| `index` | `status`, `list(ws, after, limit)`, `changes(ws, since, limit)`, `reindex(ws, path)`, `purge_model` |
| `graph` | `links`, `backlinks`, `neighborhood(ws, path, depth)`, `unresolved` |
| `doc` | `read`, `write(ws, path, content, version)`, `rename`, `remove(ws, path, version)` |

The server only answers: it never sends a notification. A client follows changes by pulling
`index.changes` from a cursor. When that cursor has expired, it takes `index.status`'s cursor, re-lists
with `index.list`, and resumes `index.changes` from that cursor. Errors carry a code that says what
to do next: re-list, merge a conflict, read after a write that landed, and so on.

## The TUI

```sh
autodoc --ui                # attaches to the daemon, and starts it when nothing answers
```

The screen has four parts:

- **The notes pane (left):** the workspace's notes, or a search's hits.
- **The note (centre):** a Vim-keyed editor.
- **Its backlinks (below the note).**
- **The status line:** the editor's mode, the workspace, and the note, with `[+]` while it has
  unsaved changes.

| Key | Does |
| --- | --- |
| `Ctrl+G`, `/` | search (`/` in Normal mode) |
| `Ctrl+O`, `Ctrl+N`, `Ctrl+S` | open a note, new note, save |
| `Ctrl+W` | switch workspace |
| `Alt+1` `Alt+2` `Alt+3` | the notes pane, the editor, the backlinks |
| `F1`, `F10`, `Ctrl+Q` | help, the menu, quit |

A save writes only over the version the note was opened at. If the note changed on disk since, the
TUI asks: keep editing, reload the disk's version, or overwrite it with yours. Opening, switching or
quitting over unsaved changes asks first, too.

The screen is QML, under `tui/qml/`. `autodoc --ui --dev tui/qml` reads it from disk and follows
edits to it.

## Following the files

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

The follower never writes the index. It hands paths to the indexer, the store's one writer, which
decides from each file's current state whether to delete, skip or re-index it.

## The index

Each workspace has one SQLite file in the state directory. One goroutine writes it, and any number
read it, each from one snapshot:

- **Notes are chunked by heading.** Chunks are about 350 estimated tokens, and each carries its breadcrumb
  (the title and the headings above it). An edit writes only the chunks it changed: each document has
  generations, and a reader sees the old one or the new one, never a mix.
- **Frontmatter is metadata:** the title, tags and aliases. Inline `#tags` count too.
- **Links are resolved per workspace, as Obsidian does.** A link reaches the note whose path is its name.
  Failing that, it reaches the one note whose file name, path suffix or alias it is, and of several,
  the one nearest the root. A tie leaves it unresolved. Links resolve again whenever a note that could
  be their target appears, goes, or changes its aliases.
- **A change log feeds clients' incremental sync.** It is kept for at least 7 days and 100 000 changes.

## Search

Search is lexical (SQLite FTS5, BM25) with no model at all. Every word of a query is a word: nothing in
it is FTS syntax, and a `*` ending the last word is a prefix.

- **Title, breadcrumb and tags weigh more than the body.**
- **Links and tags lift a note after fusion.** A note linked from other notes, or tagged with a query
  word, ranks higher.
- **At most three hits come from one note.**

With an embedding provider, search is hybrid. A 1-bit code scan over the chunks is rescored with the
float vectors, and its results are fused with BM25 by reciprocal rank.

- **Embedding is asynchronous and per document.** A note half embedded answers lexically until all of
  its chunks have vectors, and the answer says the semantic side is `partial`.
- **A new model fills in the background.** The old one keeps answering until the new one covers every
  chunk.
- **Without a provider, or when the query cannot be embedded, search stays lexical** and says so.

## Building

Go 1.25, `CGO_ENABLED=0`: the daemon, the TUI and the Web-UI link no cgo. The later native GUI is a
separate binary.

Tests run through `autodoc-test.sh` (beside the repository, as golib's and AutoDB's harnesses are):

```sh
autodoc-test.sh run --worktree . --race
```

## License

Apache License 2.0; see [LICENSE](LICENSE).
