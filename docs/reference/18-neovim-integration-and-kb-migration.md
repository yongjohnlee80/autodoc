# Chapter 18: Neovim Integration & KB Migration

AutoDoc is designed from the ground up so that its standalone terminal UI (`autodoc --ui`) and its Neovim plugin (`autodoc.nvim`) share the exact same background daemon (`autodoc --serve`), the same SQLite + vector indexes, the same recent-files history, and the same `.todo-list/` task stores. Whether you write code in Neovim and keep a project knowledge base beside it, or switch fluidly between the TUI and Neovim, both clients speak the same msgpack-RPC contract over the daemon's Unix domain socket.

This chapter covers configuring `require("autodoc").setup()`, how generated contract bindings (`lua/autodoc/contracts.lua`) guarantee zero-cost lazy startup, using interactive pickers with live retrieval stage toggles, pinning documents across the 6-slot floating Markdown previewer, operating the docked KB drawer, verifying your setup with `:checkhealth autodoc`, and executing reversible v2 knowledge-base migrations with `:AutodocKbMigrate`.

---

## 18.1 Zero-Cost Setup and Generated Contract Bindings

### Installing with lazy.nvim

The repository is also a Neovim plugin (Neovim 0.12+). It talks to the same daemon over its socket, and needs [auto-core.nvim](https://github.com/yongjohnlee80/auto-core.nvim):

```lua
{
  "yongjohnlee80/autodoc",
  build = "build.lua", -- the release binary for this version, under bin/; else make build
  dependencies = { "yongjohnlee80/auto-core.nvim" },
  opts = {
    bin = nil,     -- the autodoc binary; default: the plugin's own, then PATH
    config = nil,  -- autodoc's config.toml; default: the binary's own
    keys = false,  -- true maps the <leader>m group below (and <leader>fk)
    preview = {},  -- the preview's options; false leaves it off
    offer_restart = true, -- offer to restart a daemon older than the plugin
  },
}
```

`build.lua` (which lazy.nvim also runs on its own) installs `bin/autodoc` from the release built for the plugin's tag, after checking it against the published SHA-256. That needs no Go, and on macOS the binary is built with cgo, so it keeps the FSEvents watcher. On a checkout that is not a release tag, offline, or on a platform without a release binary, it runs `make build`, which needs Go.

**Use one binary per machine.** The TUI is the `autodoc` on your PATH. When the shared daemon refuses a TUI of another build, the TUI restarts the daemon as itself. Make the TUI the plugin's binary instead: `ln -sf <plugin>/bin/autodoc ~/.local/bin/autodoc`. `:AutodocMaintenance versions` shows all three versions (the daemon's, the plugin's and the one on PATH) and gives that command when they differ.

### Keys

| Key | Command | |
| --- | --- | --- |
| `<leader>mf` | `:AutodocSearch [query]` | search the selected KB (also `<leader>fk`) |
| `<leader>mF` | `:AutodocFiles` | a document of the selected KB by name |
| `<leader>mr` | `:AutodocRecent` | the files opened last, newest first, across KBs |
| `<leader>ml` | `:AutodocRelations` | what this file is connected to, by kind (`:AutodocBacklinks`: the documents linking to it) |
| `<leader>mo` | `:AutodocBack` | back to the document opened before |
| `<leader>mk` | `:AutodocDrawer` | the kb drawer |
| `<leader>mw` | `:AutodocSelect [workspace]` | choose the KB to search |
| `<leader>mX` | `:AutodocMaintenance [restart\|install\|versions]` | restart the daemon, install the binary again, versions |
| `<leader>mp` | `:AutodocPreviewFind` | a Markdown file under the cwd into a preview slot |
| `<leader>m1` … `md`, `m!` … `mD`, `mc`, `mb` | | the preview's slots: focus, render, close all, the browser |

### Connecting to the Daemon

Calling `require("autodoc").setup(opts)` during Neovim startup performs no synchronous I/O and spawns no subprocesses. The first call that needs the daemon asks the binary where it is (`autodoc --print-endpoint --ensure`), which starts it when nothing serves the store, so loading the plugin costs nothing. A daemon serving a different store is refused, not used. A daemon older than the plugin refuses it. The plugin then offers to restart that daemon as the plugin's own build (`--print-endpoint --restart`), once per daemon. It doesn't offer again after a restart of its own: a daemon that still refuses then means the binary itself is older.

```lua
-- init.lua or lazy.nvim plugin spec
require("autodoc").setup({
  bin = nil,            -- path to autodoc binary; defaults to plugin build, then $PATH
  config = nil,         -- autodoc's config.toml; defaults to the binary's own
  keys = true,          -- map the default <leader>m* and <leader>fk keybindings
  preview = {           -- configure the 6-slot floating Markdown previewer
    keys = true,
    debounce_ms = 150,
  },
  offer_restart = true, -- prompt once to restart a running daemon older than this plugin
  offer_link = true,    -- offer to register an unlinked primary KB from auto-core
  popup = true,         -- add "Go to related…" and "Back" to the right-click mouse menu
})
```

### Generated Contract Bindings (`lua/autodoc/contracts.lua`)

Rather than hand-maintaining RPC wrappers that drift out of sync with the Go daemon, `lua/autodoc/contracts.lua` is generated directly from the daemon's `sys.contracts` schema (`go generate`). Every domain (`doc`, `search`, `graph`, `index`, `workspace`, `todos`, `embedding`, `ranker`, `preference`) is exposed with exact parameter arity checks, automatic `BufWritePost` re-indexing (`session.on_write`), and shared recent-file tracking (`BufReadPost`). Saving a file in a KB reindexes it at once, without waiting for the daemon's watch.

![Neovim RPC Architecture and 6-Stage KB Migration Flow](neovim-rpc-and-migration-flow.mmd)

---

## 18.2 Interactive Pickers and Live Retrieval Stage Toggles

When `snacks.picker` is available (with automatic fallback to `vim.ui.input` + `vim.ui.select`), `autodoc.nvim` provides low-latency pickers over your selected knowledge base (`:AutodocSelect` or `<leader>mw`):

- **Hybrid Search (`:AutodocSearch [query]`, `<leader>mf` or `<leader>fk`)**: Debounces keystrokes by 150 ms (`M.DEBOUNCE_MS = 150`) and queries `search.query(workspace, q, {limit = 50, stages = ...})`. The picker title dynamically reports which pipeline stages ran and why any stage was skipped (for example, `KB GlobalKB · lexical + semantic + rerank` or `lexical only: semantic skipped: no embedding provider`). It searches the selected KB, which defaults to the project's primary KB, matched by its root when auto-core records no workspace name. A hit opens at its line, and `<M-p>` sends it to a preview slot.
- **Live Stage Toggles (`<M-l>`, `<M-s>`, `<M-r>`)**: Inside `:AutodocSearch`, pressing `Alt+l`, `Alt+s`, or `Alt+r` toggles **lexical** (FTS5 BM25), **semantic** (vector cosine similarity), and **rerank** (cross-encoder) on the fly and immediately refreshes the results. The picker enforces the invariant that at least one retriever (`lexical` or `semantic`) remains active, since a ranker only re-orders candidate hits.
- **Direct Preview Handoff (`<M-p>`)**: Pressing `Alt+p` on any search hit prompts for one of the six floating preview slots (`1`, `2`, `3`, `a`, `s`, `d`) and renders the target document directly into that floating window without disturbing your current code split.
- **File, Recent, and Graph Pickers**:
  - `:AutodocFiles` (`<leader>mF`, file finder): Fuzzy-finds documents in the selected KB by path and title.
  - `:AutodocRecent` (`<leader>mr`): Lists recently opened KB documents, synchronized live with the AutoDoc TUI. The TUI keeps the same list (its `tui.recent` preference), and a KB file you open in Neovim joins it while a session is up.
  - `:AutodocRelations` (`<leader>ml`) and `:AutodocBacklinks`: Explores frontmatter relations (`superseded_by`, `related`, `pr`, `adr`), outgoing wikilinks/Markdown links, and incoming backlinks, while `:AutodocBack` (`<leader>mo`) jumps back along your traversal stack. `:AutodocBacklinks` lists the documents that link to the current file; `:AutodocRelations` lists them with every other relation.
- **Relations and back.** `:AutodocRelations` (`<leader>ml`) lists what the current file is connected to in a picker, each row showing its kind. Opening one, from the picker or the drawer, remembers the file you left: `:AutodocBack` (`<leader>mo`) returns to it, as the TUI's `SPC b b` does. With `keys = true` (or `popup = true`), the right-click menu has **AutoDoc: Go to related…** and **AutoDoc: Back** on a KB file.

---

## 18.3 The Docked Drawer (`:AutodocDrawer`) and 6-Slot Floating Previews

### The Docked KB Drawer (`:AutodocDrawer` / `<leader>mk`)

`:AutodocDrawer` opens a `winfixbuf`-protected side panel powered by `lua/autodoc/views/host.lua` and `panel.lua`. It lists the workspaces and their documents, which follow the files as they change, along with the embedding models and the reranker. With [auto-finder.nvim](https://github.com/yongjohnlee80/auto-finder.nvim) installed it opens in auto-finder's panel; otherwise in its own. `?` shows its keys: `s` selects the KB to search, `P` makes a KB this project's primary (stored by auto-core, and only after you confirm), and `A` adds a location, offering to scaffold the KB layout when the folder has no `AGENTS.md`. Its **Relations** section follows the KB file in the editor, as the TUI's `SPC l` drawer does: superseded by, supersedes, sources, cited by, amends, amended by, related, adr, links and backlinks, then the relations that name nothing and the supersession loops. `<CR>` opens a row, and `d` adds the files two links away. Opening any item from the drawer targets your primary editor window (`panel.editor_target_winid()`), never overwriting the drawer buffer itself.

### Linking the Primary KB

auto-core's first-run import records the project's primary KB by its root alone. auto-agents gives a spawned agent `$AUTODOC_WORKSPACE` only when that record names a workspace. So when the workspaces are first listed and the primary names none, the plugin offers to link it, once per session. If a workspace already serves that folder, it is linked. Otherwise the folder is added under a name you confirm (`AutoVimKB` for AutoVim's global KB) and then linked. "Don't ask again" is remembered per folder. `:AutodocLinkKb` asks on demand, and `offer_link = false` turns the offer off.

### 6-Slot Floating Markdown Previews (`autodoc.preview`)

Absorbing the workflow of `md-harpoon` (ADR 1791210485), `autodoc.preview` gives you six persistent floating Markdown slots arranged ergonomically on the home and number rows: **`1`, `2`, `3`** across the top and **`a`, `s`, `d`** below.

Use `:AutodocPreviewRender <slot>`, `:AutodocPreviewRenderPath <slot> <path>`, `:AutodocPreviewFind` (`<leader>mp`), `:AutodocPreviewFocus <slot>`, `:AutodocPreviewCloseAll`, and `:AutodocPreviewBrowser` (the document as HTML, through `autodoc --export html`).

- **Render vs. Focus**: Pressing `<leader>m!` / `<leader>m@` / `<leader>m#` / `<leader>mA` / `<leader>mS` / `<leader>mD` (`:AutodocPreviewRender {slot}`) pins the current Markdown buffer into slot `1`/`2`/`3`/`a`/`s`/`d`. Pressing `<leader>m1`..`<leader>md` (`:AutodocPreviewFocus {slot}`) toggles or focuses that slot, restoring the exact line and column where you left the cursor.
- **Per-Project Persistence and Live Re-Render**: Slot pins and cursor coordinates persist across Neovim sessions via `auto-core` state (`autodoc.preview` namespace) and automatically follow Git worktree switches (`worktree:switched`). Buffer-backed slots re-render live on `TextChanged`/`TextChangedI`; file-backed slots re-render when `core.file:modified` fires.
- **In-Float Navigation and Browser Export**: Inside any preview float, `q` or `<Esc>` closes the window, `gx` follows the Markdown link or `#heading` anchor under the cursor inside the same slot, and `B` (`:AutodocPreviewBrowser`) exports the document with rendered Mermaid SVGs directly to your system web browser. In a float, a Mermaid block is marked, and `B` opens the document in the browser, where it is drawn; `gx` opens the link or image under the cursor.

---

## 18.4 Scripting `autodoc.nvim` from Your Own Neovim Config

`autodoc.nvim`'s public API is `require("autodoc.api")`. Its calls to the daemon (`search`, `documents`,
`workspaces`, `add_location`, and `todos.*`) are asynchronous: `cb(result, err)`, with `err` as `{code, message}`,
called on Neovim's main loop. The rest are not calls to the daemon:

- `drawer_open`, `drawer_toggle` and `drawer_focus` show the kb drawer in whichever host is active, and
  call back `cb(ok, value)`, as the host registry does;
- `primary()`, `selected()`, `select(name)` and `set_primary(ws, confirmed)` answer at once: the project's
  primary KB, the selected KB's name, nothing, and `ok, err`.

Build your own keymaps, statusline components or picker actions on the calls without blocking the editor:

```lua
-- Example: <leader>mv searches the selected KB for the word under the cursor, and names the top hit
vim.keymap.set("n", "<leader>mv", function()
  local word = vim.fn.expand("<cword>")
  require("autodoc.api").search(word, { limit = 5 }, function(res, err)
    if err then
      vim.notify("autodoc: " .. err.message, vim.log.levels.ERROR)
      return
    end
    if not res.hits or #res.hits == 0 then
      vim.notify("autodoc: no hits for " .. word)
      return
    end
    local hit = res.hits[1]
    vim.notify(hit.path .. "  " .. (hit.breadcrumb or ""))
  end)
end, { desc = "Search the KB for the word under the cursor" })
```

`search` takes `search.query`'s options (`limit`, `stages`, `tags`, `paths`, `facets`) and a workspace name
last; without one it searches the selected KB, else the project's primary KB.

### Todos from Lua

`require("autodoc.todos")`, also `require("autodoc.api").todos`, calls the daemon's `todos.*` verbs. Each takes the verb's params in order, then `cb(result, err)`. The first param of most is where the store is: a workspace name, or `{ root = "/abs/project" }` (`stores` and `set_var` take none).

```lua
require("autodoc.todos").list("kb", {}, function(listing, err) … end)
```

`autodoc --contracts 'todos.*'` lists the verbs; the functions are bound from the same contracts, generated into `lua/autodoc/contracts.lua`. A workspace's store is AutoDoc's from its first write, `todos.add` making it if it is missing. Only a store released with `todos.release` answers `-32077` (`todos.unclaimed`), and `todos.claim` lets AutoDoc write it again.

---

## 18.5 Diagnostics with `:checkhealth autodoc` and `:AutodocMaintenance`

Run `:checkhealth autodoc` at any time to verify the binary path, daemon socket connectivity, protocol version parity between `autodoc.nvim` and `autodoc --serve`, active workspace health, and optional dependencies (`snacks.picker`, `auto-core`). If you upgrade the plugin or build a new binary, `:AutodocMaintenance` (`<leader>mX`) offers three immediate actions: `versions` (the daemon's, the plugin's and the one on PATH), `restart` (gracefully restart the background daemon), and `install` (install the binary again).

---

## 18.6 Safe, Reversible KB Migrations (`:AutodocKbMigrate`)

When upgrading an older knowledge base (with `shared/` and `agents/<name>/` trees) to the v2 schema-validated layout (ADR 1791209946 §8: top-level `adrs/`, `conventions/`, `playbooks/`, `reference/`, `synthesis/`, `reviews/<reviewer>/`, `prs/`, `notes/`, `archive/`, `_schema/`, and `_templates/`), `:AutodocKbMigrate` executes a deterministic, checksummed **6-stage pipeline** with full pre-image rollback.

The KB migration (`:AutodocKbMigrate [root]`) moves a KB to the current layout. It first saves a full copy of every file it will touch, so `--undo` restores the KB without git, and `--forget` deletes that copy.

![Typical Execution Time Across :AutodocKbMigrate Stages](kb-migration-step-duration.chart.json)

1. **Stage 1 — Inventory & SHA-256 Hashing**: Scans every file under the KB root and queries `todos.stores` for external `.todo-list/` stores referencing the KB. Computes the pre-migration SHA-256 of every file and resolves deterministic destination paths (`M.dest`), disambiguating any filename collisions with `--from-<old-parent-slug>`.
2. **Stage 2 — Schema & Template Scaffold**: Prepares `_schema/frontmatter.yaml`, folder `ABOUT.md` files (`_ABOUT.md` in the `_` folders), and `_templates/<type>.md` definitions without overwriting immutable `raw/` or archived files.
3. **Stage 3 — Frontmatter Normalization**: Rewrites legacy `type` values (`bug-fix` $\rightarrow$ `type: synthesis`, `kind: bug-fix`; `code-review` $\rightarrow$ `type: review`) and normalizes synonym keys (`reviewed_commit` $\rightarrow$ `head`, `related-adrs` $\rightarrow$ `related`) to pass `_schema/frontmatter.yaml`.
4. **Stage 4 — Link Re-Relativization & Folder Move**: Parses Markdown links and wikilinks outside fenced code blocks, resolves each target against the file's *old* location, and rewrites the relative path from its *new* location. Also updates `$KB_ROOT` references inside `.todo-list/` task files.
5. **Stage 5 — Re-Index & Schema Validation**: Triggers a daemon re-index and validates every migrated document against `doc.validate`.
6. **Stage 6 — Pre-Image Verification & Snapshot Commit**: Before `--apply` touches a single byte in your KB, it writes and verifies a complete pre-image backup under `stdpath("state") .. "/autodoc/migrations/<id>/"`. If any write or check fails mid-apply, the migration automatically restores the pre-image. When the KB is a Git repository, `--apply` stages and commits the migrated paths locally.

```vim
" 1. Run a non-destructive dry run (writes migration-manifest.json, migration.diff, summary.txt):
:AutodocKbMigrate

" 2. Inspect migration.diff, then apply the checksummed manifest:
:AutodocKbMigrate --apply

" 3. Roll back completely to the pre-image if needed, or purge the backup once satisfied:
:AutodocKbMigrate --undo
:AutodocKbMigrate --forget
```

### How Pre-Image Checksums Prevent Drift Between Dry-Run and Apply

If any file in the KB or linked `.todo-list/` store is modified after `:AutodocKbMigrate` generates the dry-run manifest and before you run `:AutodocKbMigrate --apply`, Stage 6 detects the SHA-256 mismatch before writing a single file and aborts cleanly—requiring a fresh dry-run so no concurrent edit is ever overwritten. Likewise, `:AutodocKbMigrate --undo` verifies the post-apply SHA-256 of every migrated file before restoring the pre-image backup, guaranteeing zero data loss if you edited a document after migrating.

### The KB Layout and `_ABOUT.md`

The KB layout is the one the drawer's scaffold writes. Each folder's `ABOUT.md` says what belongs there. The `_` folders use `_ABOUT.md` instead, because on a case-insensitive filesystem (macOS's default) `_templates/ABOUT.md` and `_templates/about.md` (the `about` type's template) are the same file. A KB an older scaffold wrote keeps the old names. Rename them on a Linux host and commit: `git mv _templates/ABOUT.md _templates/_ABOUT.md` and `git mv _schema/ABOUT.md _schema/_ABOUT.md`. A macOS clone then checks out both files cleanly.

![Neovim Commands, Keymappings, and Lua Entry Points Reference](neovim-commands-and-mappings.csv)

---

[← Previous: Chapter 17: Notebooks in Markdown — The Jupyter Plugin](./17-jupyter-notebooks-in-markdown.md) | [Index](./README.md) | [Next: Chapter 19: Enterprise PostgreSQL RAG Server →](./19-enterprise-postgres-rag-server.md)

