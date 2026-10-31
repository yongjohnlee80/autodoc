# Neovim decisions

How AutoDoc works inside Neovim. The plugin, `autodoc.nvim`, lives in this repository under
`lua/autodoc/`. It talks to the same daemon as the TUI, owns a knowledge-base drawer and a picker,
and carries six floating Markdown previews. It depends on auto-core for its transport, state and
panels, and works with auto-finder when that is installed. Each section below is one decision: what
was decided, why, and where it lives in the code.

## The Lua client lives with the daemon

AutoDoc's Neovim side is a client of the daemon in the same repository, so the Lua and the binary
ship from one commit.

- `client.lua` speaks msgpack-RPC over auto-core's transport: a probe hello first, then the hello at
  the plugin's protocol, then `sys.capabilities.verbs`, the verbs the session may call.
- `lifecycle.lua` finds the binary (the configured one, then the plugin's own build, then `PATH`)
  and asks it where the daemon is (`autodoc --print-endpoint`, with `--ensure` to start one). The
  plugin never spawns a daemon of its own.
- `session.lua` holds the one client, the selected KB, the change-feed cursors and the reindex of a
  saved KB file.
- The Go side reads no Neovim state: every daemon feature the plugin uses is a generic one the TUI
  and `--call` use too.

In the code: `lua/autodoc/client.lua`, `lua/autodoc/lifecycle.lua`, `lua/autodoc/session.lua`,
`lua/autodoc/api.lua`

## A protocol range per session

The daemon is shared by every client of a user, and is often another build than the plugin. So the
daemon serves a range of protocols, `MinProtocol` to `Protocol`, and a session keeps the protocol it
declared: a verb added after it answers as an unknown method, and every shape it knew keeps its
meaning. The TUI is still held to the daemon's exact protocol.

When the plugin is newer than the daemon, it offers once per daemon instance to restart the daemon
as the plugin's build, and asks first, since other clients share it. It doesn't offer again after a
restart it made: a daemon that still refuses then is an old binary, which an update mends.

In the code: `rpc/server.go`, `lua/autodoc/client.lua`, `lua/autodoc/session.lua`

## The KB drawer

The `kb` drawer is a pure renderer over the plugin's session: it never dials the daemon or decides
which KB is primary itself. It has the sections Knowledge Base, Embedding Models, Reranker and
Relations, each with a `▼/▶ Header (count)` line, collapse state kept in auto-core state, and one
keymap set that dispatches on the row's kind.

- Knowledge Base lists the workspaces and their documents (from `index.documents`). `s` selects the
  KB to search, `P` makes a KB the project's primary (stored by auto-core, after you confirm), and
  `A` adds a location, offering to scaffold the KB layout.
- Relations follows the KB file in the editor: its links, backlinks and relation fields.
- The daemon never pushes, so while the drawer is visible a timer pulls `sys.events`, and
  `index.changes` for each expanded KB. A hidden drawer pulls nothing.
- Each `new(profile)` is an instance with its own buffer, rows and timer; the host registry builds
  and disposes it.

In the code: `lua/autodoc/views/drawer.lua`, `lua/autodoc/views/host.lua`, `lua/autodoc/views/panel.lua`

## Searching the selected KB

`<leader>fk` searches the selected KB live. With snacks.picker it is a custom source: the query is
debounced, each hit is the file at its `line_start`, and the title says how the answer was made (the
reply's stages). `<M-l>`, `<M-s>` and `<M-r>` toggle lexical, semantic and rerank, and a retriever
always stays on, since a ranker only re-orders what the retrievers find. `<M-p>` sends a hit to a
preview slot. Without snacks it falls back to `vim.ui.input` and `vim.ui.select` with the same
request.

In the code: `lua/autodoc/picker.lua`

## Hosts register with the drawer

The drawer can be shown by more than one surface: auto-finder's `kb` section when that plugin is
installed (priority 100), and AutoDoc's own panel when it is not (priority 0). So hosts register
themselves with a registry the plugin owns, and the plugin opens and focuses the drawer in whichever
one wins, without requiring auto-finder.

- A provider is `{id, priority, profile, available(), mount(view, release), focus(), close()}`.
  The winner is picked at each explicit open, never at setup, since load order is not guaranteed.
- The registry, not the provider, builds and disposes the view, so there is at most one mounted
  drawer. `mount` and `focus` return the window they used, and the registry checks that it shows the
  view's buffer.
- A handoff closes the old owner and disposes its view before the winner mounts. Every provider call
  runs under `pcall`; a failed mount reports, and never falls back silently to another host.
- The self-host's id and priority are reserved, so no provider can replace the fallback a standalone
  install relies on. Ask the registry whether a host is registered rather than keeping a flag.
- auto-finder's facade addresses the drawer module, which re-exports the registry, so the facade is
  the same for AutoDoc's drawer and the database plugin's.

In the code: `lua/autodoc/views/host.lua`, `lua/autodoc/views/drawer.lua`, `tests/composition.lua`

## One view per instance with a fixed profile

The view a host mounts is an instance with a fixed contract: `get_buffer(winid)` builds the buffer on
first call and reuses it after, `bufnr()` is nil before the first build and after disposal,
`on_focus` and an idempotent `dispose()`, the only teardown. The profile (buffer name, filetype,
buffer variable, the editor window to open items in) is chosen before mount and never changed
after, because those are set while the buffer is made. Each instance owns its own buffer, so two
panels never share one.

In the code: `lua/autodoc/views/drawer.lua`

## Closing a panel releases its mount

A mount can end from either side, and both meet in one idempotent teardown. The registry ends one by
calling the provider's `close` and then disposing the view. A host ends one, as when `q` closes its
panel, by calling the `release` it was handed at mount, which disposes the view without calling back
into a surface that is already closing. A `release` from a mount that has since been replaced does
nothing. AutoDoc's own panel, named `autodoc` (a panel name merges into any existing panel of that
name), disposes its section registry on close and then calls `release`; without that, the next open
would focus a dead window.

In the code: `lua/autodoc/views/panel.lua`, `lua/autodoc/views/host.lua`

## Markdown preview slots

`autodoc.preview` replaces the standalone md-harpoon plugin, whose workflow it takes over with no
dependency beyond auto-core and nothing carried over: no old pins, verb names or command names.

- Six floats, `1 2 3` across the top and `a s d` below, each showing one document and remembering
  where the cursor was. Pins persist per project in auto-core state (namespace `autodoc.preview`)
  and follow worktree switches.
- A buffer-backed slot re-renders on edits; a file-backed slot when its file changes.
- The file finder lists Markdown files under the current directory, in a KB or not. Searching a KB
  is `<leader>fk`.
- The verbs are `preview.attach`, `preview.view` and `preview.browser`, and the commands
  `:AutodocPreview*`.

In the code: `lua/autodoc/preview/init.lua`, `lua/autodoc/preview/pins.lua`,
`lua/autodoc/preview/find.lua`, `tests/preview_spec.lua`

## A trimmed Markdown renderer

The floats use AutoDoc's own small renderer, not a Markdown plugin. It draws only what the floats
need: headings, emphasis and inline code through treesitter's Markdown highlighting, lists and task
boxes, block quotes and GitHub callouts, fenced code, box-drawn tables, rules, the frontmatter as one
header line, and links shown as their text with `gx` to follow them. A Mermaid block keeps its source
with a hint that `B` opens it drawn in the browser, and an image is a placeholder with its alt text.
The source buffer is never touched: the renderer writes only to the float's scratch buffer, with
extmarks, virtual text and highlights.

In the code: `lua/autodoc/preview/render/init.lua`

## The browser view goes through the export

`B` opens a slot's document in the browser through the Go side. The buffer's current text, saved or
not, is written to a snapshot in a private per-session directory (mode 0700), then
`autodoc --export html --base <the buffer's directory>` renders it, and the system opener shows it.
`--base` rewrites relative links to `file://` paths against the original location, so the page's
links point at the real files rather than the temp directory; without it, the source file's own
directory is the base. Mermaid is drawn offline by the page itself, and images stay stripped, as the
export always does. The directory is removed when Neovim exits.

In the code: `lua/autodoc/preview/browser.lua`, `core/export/export.go`
