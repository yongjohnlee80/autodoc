# AutoDoc

AutoDoc indexes and searches a tree of Markdown notes, and follows the files as they change. The files
are canonical: the index is derived from them, lives outside the tree, and can always be rebuilt.

It is one binary with the modes as flags, after [AutoDB](https://github.com/yongjohnlee80/autodb):
`autodoc --serve` is the daemon, and the TUI (`--ui`), the Web-UI (`--web-ui`) and later AutoVim and a
native GUI are its clients, all over one msgpack-RPC API on a 0600 unix socket.

**Status: early.** What exists so far is the core the daemon is built on:

| Package | What it does |
| --- | --- |
| `core/config` | reads `$XDG_CONFIG_HOME/autodoc/config.toml`, and resolves the socket and the state directory |
| `core/workspace` | opens a workspace: its root as a `golib/vfs` filesystem, its include/exclude patterns, and the single-instance lease on its index store |
| `core/follow` | keeps a workspace's index following its root |
| `core/index` | the index store (one SQLite file per workspace), its indexer, the link graph, and search |
| `core/embed` | the optional embedding provider: Ollama, or any OpenAI-compatible endpoint |

The RPC server, the TUI and the Web-UI follow.

## Configuration

```toml
# $XDG_CONFIG_HOME/autodoc/config.toml
[server]
socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
state_dir = ""              # default: $XDG_STATE_HOME/autodoc

[follow]
poll_interval = "2s"        # the watch fallback's listing interval

[[workspace]]
name = "kb"                 # the handle every API call names
root = "~/notes"            # one root per workspace
include = ["**/*.md"]       # default
exclude = [".git/**"]       # default
```

Patterns are root-relative globs: each `/`-separated segment is a `path.Match` pattern, and `**`
matches any number of whole segments. An unknown setting is an error, so a misspelling is reported.

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
