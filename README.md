# AutoDoc

AutoDoc indexes and searches a tree of Markdown notes, and follows the files as they change. The files
are canonical: the index is derived from them, lives outside the tree, and can always be rebuilt.

It is one binary with the modes as flags, after [AutoDB](https://github.com/yongjohnlee80/autodb):
`autodoc --serve` is the daemon, and the TUI (`--ui`), the Web-UI (`--web-ui`) and later AutoVim and a
native GUI are its clients, all over one msgpack-RPC API on a 0600 unix socket.

**Status: early.** The daemon serves, and the TUI edits; the Web-UI is next.

| Package | What it does |
| --- | --- |
| `core/config` | reads `$XDG_CONFIG_HOME/autodoc/config.toml`, and resolves the socket, the state directory and the store |
| `core/store` | the one store: every workspace and its index, in one SQLite file, read and written through golib `dao` (one declaration per table), with the lease that makes one daemon its only server |
| `sql/deployments` | the store's schema as numbered SQL scripts, applied by golib `dao/deploy` (see `docs/ops/schema-scripts.md`) |
| `core/workspace` | opens a workspace's root as a `golib/vfs` filesystem, with its include/exclude patterns |
| `core/follow` | keeps a workspace's index following its root |
| `core/index` | one workspace's index in the store, its indexer, the link graph, and search |
| `core/embed` | the embedding providers' clients: Ollama, or any OpenAI-compatible endpoint, metered |
| `core/docs` | reads and writes notes for AutoDoc's own apps, conditional on the version the writer read |
| `rpc` | the msgpack-RPC API: a projection of core, with no logic of its own |
| `tui` | the terminal UI: search, the notes, a Vim-keyed editor, backlinks; its screen written in QML |
| `internal/daemon` | the daemon's workspaces: each served by an indexer and a follower, added, renamed and removed while it runs |
| `cmd/autodoc` | the binary: `--serve` is the daemon, `--ui` the TUI, `--call` one verb as JSON |

## Configuration

```toml
# $XDG_CONFIG_HOME/autodoc/config.toml
[server]
socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
state_dir = ""              # default: $XDG_STATE_HOME/autodoc (the daemon's log)
data_dir = ""               # default: $XDG_DATA_HOME/autodoc (the store, autodoc.db)

[follow]
poll_interval = "2s"        # the watch fallback's listing interval
```

A missing file is every default. An unknown setting is an error, so a misspelling is reported.

**Workspaces are not configured here.** They are kept in the store: add, rename and delete them in
the TUI (`Go › Manage workspaces…`) or with `workspace.add`. A workspace has a name (what `--ui` and
every API call take), a root directory, and include and exclude patterns (`**/*.md` and `.git/**` by
default). Patterns are root-relative globs: each `/`-separated segment is a `path.Match` pattern, and
`**` matches any number of whole segments. A config file that still has a `[[workspace]]` section is
refused with a message saying so.

**Nor are the embedding providers.** They are kept in the store too, their API keys sealed, and
added and chosen in the TUI's AI models dialog (see [Semantic search](#semantic-search-and-embedding-models)).
A config file with an `[embedding]` section is refused with a message saying so.

## The daemon

```sh
autodoc --serve             # or: autodoc --serve --config path/to/config.toml
```

It listens on its unix socket, mode 0600: the file is the access control, so there is no login
locally. It opens the store, takes its lease, and serves every workspace in it. A second `--serve`
on the same socket exits, reporting the instance that answers there; one on another socket over the
same store is refused, since the lease is taken.

Every client speaks one API. A session starts with `sys.hello({protocol})`, and until then only
`sys.hello` answers. Every other verb takes the workspace name first:

| Group | Verbs |
| --- | --- |
| `sys` | `hello`, `shutdown` |
| `workspace` | `list`, `add(name, root, include?, exclude?)`, `rename(name, to)`, `remove(name)` (the index goes; the files stay) |
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
autodoc --ui                # attaches to the daemon (starting it when nothing answers), in the last workspace used
autodoc --ui kb             # in workspace kb; a name the daemon does not have is refused, with the names it has
```

The screen is a page and nothing else: the note, 120 columns wide and centred, the ruler at its
right edge. With no note open, the page is an untitled draft: type into it (`i`), and `Ctrl+S`
names and saves it as a note. Everything else comes when it is asked for:

- **The menu bar** hides until `F10` or an `Alt+letter` brings it up.
- **The explorer** (`SPC e`): every workspace's folders and notes, as a tree. **The links**
  (`SPC l`): the notes linking to this one. Each opens over the page, which does not move, from
  the side the editor's preferences name; `Escape` or its key again closes it.
- **The status line** (`SPC t`): the editor's mode, the workspace, and the note, with `[+]` while
  it has unsaved changes. It shows while the TUI is not connected, whatever the preference says.
- **The pickers** (search, open, new note, add a workspace) share one layout: the fields over the
  list on the left, the note under the cursor on the right, the buttons beneath. The search runs
  as it is typed, and its preview is at the hit, the words marked.

| Key | Does |
| --- | --- |
| `Space`, `Ctrl+Space` | the leader card (Space in Vim's Normal mode; Ctrl+Space in any editor mode): a key runs its command (`e`, `l`, `/`, `o`, `k`, `,`, `a` …) |
| `Ctrl+G`, `SPC /`, `SPC SPC` | search the workspace, by words and meaning |
| `/`, `n`, `N` | find a word in the pane with the keyboard (the page, the explorer, the links), then again forward and back (Normal mode) |
| `Ctrl+O`, `Ctrl+N`, `Ctrl+S` | open a note, new note, save |
| `Ctrl+W` | switch workspace; its `Manage…` (or `Go › Manage workspaces…`) adds, renames and deletes them |
| `Ctrl+h` `j` `k` `l` | in Normal mode, to the open panel on that side, and back to the page |
| `F1`, `F10`, `Ctrl+Q` | help, the menu bar, quit |

**Options** is the menu for the editor:

- **Editor mode:** Vim (modal: `i` types, `Esc` goes back to Normal) or Text (modeless, an ordinary
  text editor's keys). `SPC k` switches between them.
- **Theme:** Dark, Light, Mono or Retro.
- **Editor preferences…** (`SPC ,`): the editor mode, the page's width (applied as it is typed), the
  theme, whether the menu bar hides and the status line shows, and the side each panel opens from.

**System**, right of Options, is the menu for the backend:

- **AI models…** (`SPC a`): the embedding providers on the left, and the one under the cursor's
  usage by day and latest calls on the right (see below).
- **Restart backend…** stops the daemon, and the TUI starts the autodoc installed in its place, so
  an update takes effect without quitting. The question says which version runs and which one
  starts. Indexing and embedding carry on where they stopped.

The preferences and the AI models are kept in the daemon's store, so they are the same whichever
workspace is open.

**Notifications.** What happens — a save, a workspace added, the connection, indexing — shows in
a corner as a toast, saying how long ago it came ("now", "15s ago"). Up to three show at once, the
newest nearest the corner; the rest wait and show in turn. A finished one stays 3 seconds (1 to 10
in Preferences), a task's progress stays until the task ends, and Preferences moves them to any
corner. View › Notifications… (`SPC h`) lists every one, with its time. What the page says — a
find's result, the editor mode — is the status line's, not a notification.

**The page** wraps long lines at its width, and can number its lines in a dimmed gutter: View ›
Wrap long lines and Line numbers, or Preferences. `?` in Normal mode shows the Vim keys in a card
at the bottom right; the page keeps the keyboard, and `?` again closes it.

About names the author, Yong Sung John Lee, and the license, Apache 2.0 (NOTICE).

The status line ends with semantic search's state: a green dot and "semantic search" while a
provider answers, a red one and "lexical search" while it doesn't (none in use, a model switch
under way, or the provider not answering). While the provider embeds, a spinner turns beside a bar
of the sections covered.

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

The index is in the store, one SQLite file for every workspace, with each workspace's rows keyed by
it. One connection writes, and any number read, each from one snapshot:

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
- **A new model fills in the background, and search is by words meanwhile.** The old model goes
  offline when the new one is chosen: its server is told to unload it, so the two are never loaded
  together. The answer says the semantic side is `switching` until the new model covers every chunk.
- **Without a provider, or when the query cannot be embedded, search stays lexical** and says so.

**From a shell, or an AI agent.** `autodoc --call <verb> '<JSON array of parameters>'` calls any
verb of the daemon (starting it when nothing answers) and prints the result as JSON:

```sh
autodoc --call workspace.list
autodoc --call search.query '["kb", "storage decision", {"paths": ["adrs"], "limit": 10}]'
```

[AGENTS.md](AGENTS.md) tells an AI agent how to search with it: the verbs, the query syntax, the
filters (`paths`, `tags`, `mode`, `limit`) and the errors.

## Semantic search and embedding models

Semantic search finds a note by what it means, not only by the words it shares with the query. A
search for "why did we pick SQLite" also finds the note that says "we chose an embedded database",
and "login bug" finds "authentication fails". Exact words still count: the two are fused, and a
note that matches both the words and the meaning ranks highest.

**How it works.** An embedding model turns each section of a note into a vector, a list of numbers
that places the text by meaning. A query is turned into a vector the same way, and the sections
whose vectors are closest are the matches.

**Vectors are made once, and kept.**

- **A note is parsed only when its file changes,** never at search time. On a restart, an unchanged
  file is skipped by its version (size, modification time, inode).
- **A section's vector is kept in the store,** keyed by its text's hash: an edit re-embeds only the
  sections it changed, and two identical passages share one vector. Each workspace keeps its own.
- **A search reads what is stored.** The only model call it makes is for the query itself.
- **Switching models embeds everything once more.** The old model goes offline at once, unloaded
  from its server, and search is by words until the new one covers every section; then it takes
  over. The old model's vectors stay until purged, so switching back is instant.

**Providers.** Semantic search is off until a provider is chosen in the TUI's AI models
(`System › AI models…`). A
provider is one of three kinds, with the model it embeds with:

- **Ollama (local):** a server on this machine or the network, `http://localhost:11434` by
  default. It takes no key.
- **Ollama Cloud:** `https://ollama.com`, with the account's API key, which it requires. Its
  models are those the cloud serves: many are chat models (GLM, gpt-oss), which cannot embed.
- **OpenAI-compatible:** any endpoint that serves `/v1/embeddings`, with a key if it takes one.

The store keeps any number of them, and one is in use; switching is a choice in a list, with no
restart. The model list is the provider's own (Ollama's models, or the endpoint's `/v1/models`).

- **An API key is sealed in the store,** with AES-256-GCM under the store's key: a file beside the
  store, `autodoc.db.key`, made 0600 the first time a key is kept, and refused if others can read
  it. A copied store without that file opens no key. A key is never shown again, only whether a
  provider has one.
- **Each provider keeps its usage and a log:** requests, texts, the tokens it counted, failures and
  usage-limit refusals by day, and its last 200 calls. A provider at its limit (HTTP 429) or
  refusing its key (401, 403) is named as such, so you can switch to another.

**A model's input limit.** Each section is its own request, with no conversation and no memory
between requests, so nothing builds up and nothing needs flushing. What matters is the length of
one section against the model's input limit (8,192 tokens for `nomic-embed-text`, 512 for
`mxbai-embed-large`):

- **A text longer than the model's context is refused** (HTTP 400, 413 or 422). Ollama refuses it
  too, even when asked to truncate. The indexer narrows the batch to the one text refused, sets it
  aside for an hour, and embeds the rest. The status shows it as refused, and the section is still
  found by its words.
- **An Ollama provider has a context window,** 8,192 tokens unless you change it in the provider's
  form, and sent as `num_ctx` with every request. The server loads the model at that size, and the
  size decides its memory: left to the server's own default (`OLLAMA_CONTEXT_LENGTH`, or the
  model's full context), `qwen3-embedding:4b` took 12.4 GB of GPU memory at 40,960 tokens against
  4.6 GB at 8,192. A larger window admits longer sections, at that cost.
- **Sections are split at headings,** so most are well under any limit.

**Which model.** An embedding model, not a chat model. A chat model such as `gpt-oss-20b` produces
text, not vectors: it appears in Ollama's model list, but AutoDoc embeds a probe text before it
switches, and a model that cannot embed is refused, the one in use staying. For a local provider,
pull one of these:

| model | input limit | |
| --- | --- | --- |
| `nomic-embed-text` | 8,192 tokens | small and fast; a good default |
| `mxbai-embed-large` | 512 tokens | stronger, on shorter sections |
| `snowflake-arctic-embed2` | 8,192 tokens | multilingual |
| `qwen3-embedding:4b` | 40,960 tokens | strong; 2,560 dimensions, about 3× snowflake's time |

They are small beside a chat model and run alongside one.

## Building

Go 1.25, `CGO_ENABLED=0`: the daemon, the TUI and the Web-UI link no cgo. The later native GUI is a
separate binary.

Tests run through `autodoc-test.sh` (beside the repository, as golib's and AutoDB's harnesses are):

```sh
autodoc-test.sh run --worktree . --race
```

## License

Apache License 2.0; see [LICENSE](LICENSE).
