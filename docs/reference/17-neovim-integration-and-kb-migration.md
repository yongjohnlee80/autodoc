# Chapter 17: Neovim Integration & KB Migration

AutoDoc is designed from the ground up so that its standalone terminal UI (`autodoc --ui`) and its Neovim plugin (`autodoc.nvim`) share the exact same background daemon (`autodoc --serve`), the same SQLite + vector indexes, the same recent-files history, and the same `.todo-list/` task stores. Whether you write code in Neovim and keep a project knowledge base beside it, or switch fluidly between the TUI and Neovim, both clients speak the same msgpack-RPC contract over the daemon's Unix domain socket.

This chapter covers configuring `require("autodoc").setup()`, how generated contract bindings (`lua/autodoc/contracts.lua`) guarantee zero-cost lazy startup, using interactive pickers with live retrieval stage toggles, pinning documents across the 6-slot floating Markdown previewer, operating the docked KB drawer, verifying your setup with `:checkhealth autodoc`, and executing reversible v2 knowledge-base migrations with `:AutodocKbMigrate`.

---

## 17.1 Zero-Cost Setup and Generated Contract Bindings

Calling `require("autodoc").setup(opts)` during Neovim startup performs no synchronous I/O and spawns no subprocesses. The daemon connection is opened lazily on the first command, picker, or `BufWritePost` event that needs it—and if `autodoc --serve` is not yet running, the binary starts it automatically in the background.

```lua
-- init.lua or lazy.nvim plugin spec
require("autodoc").setup({
  bin = nil,            -- path to autodoc binary; defaults to plugin build, then $PATH
  config = nil,         -- custom config.toml path; defaults to ~/.config/autodoc/config.toml
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

Rather than hand-maintaining RPC wrappers that drift out of sync with the Go daemon, `lua/autodoc/contracts.lua` is generated directly from the daemon's `sys.contracts` schema (`go generate`). Every domain (`doc`, `search`, `graph`, `index`, `workspace`, `todos`, `embedding`, `ranker`, `preference`) is exposed with exact parameter arity checks, automatic `BufWritePost` re-indexing (`session.on_write`), and shared recent-file tracking (`BufReadPost`).

![Neovim RPC Architecture and 6-Stage KB Migration Flow](neovim-rpc-and-migration-flow.mmd)

---

## 17.2 Interactive Pickers and Live Retrieval Stage Toggles

When `snacks.picker` is available (with automatic fallback to `vim.ui.input` + `vim.ui.select`), `autodoc.nvim` provides low-latency pickers over your selected knowledge base (`:AutodocSelect` or `<leader>mw`):

- **Hybrid Search (`:AutodocSearch [query]`, `<leader>mf` or `<leader>fk`)**: Debounces keystrokes by 150 ms (`M.DEBOUNCE_MS = 150`) and queries `search.query(workspace, q, {limit = 50, stages = ...})`. The picker title dynamically reports which pipeline stages ran and why any stage was skipped (for example, `KB GlobalKB · lexical + semantic + rerank` or `lexical only: semantic skipped: no embedding provider`).
- **Live Stage Toggles (`<M-l>`, `<M-s>`, `<M-r>`)**: Inside `:AutodocSearch`, pressing `Alt+l`, `Alt+s`, or `Alt+r` toggles **lexical** (FTS5 BM25), **semantic** (vector cosine similarity), and **rerank** (cross-encoder) on the fly and immediately refreshes the results. The picker enforces the invariant that at least one retriever (`lexical` or `semantic`) remains active, since a ranker only re-orders candidate hits.
- **Direct Preview Handoff (`<M-p>`)**: Pressing `Alt+p` on any search hit prompts for one of the six floating preview slots (`1`, `2`, `3`, `a`, `s`, `d`) and renders the target document directly into that floating window without disturbing your current code split.
- **File, Recent, and Graph Pickers**:
  - `:AutodocFiles` (`<leader>mF`, file finder): Fuzzy-finds documents in the selected KB by path and title.
  - `:AutodocRecent` (`<leader>mr`): Lists recently opened KB documents, synchronized live with the AutoDoc TUI.
  - `:AutodocRelations` (`<leader>ml`) and `:AutodocBacklinks`: Explores frontmatter relations (`superseded_by`, `related`, `pr`, `adr`), outgoing wikilinks/Markdown links, and incoming backlinks, while `:AutodocBack` (`<leader>mo`) jumps back along your traversal stack.

---

## 17.3 The Docked Drawer (`:AutodocDrawer`) and 6-Slot Floating Previews

### The Docked KB Drawer (`:AutodocDrawer` / `<leader>mk`)

`:AutodocDrawer` opens a `winfixbuf`-protected side panel powered by `lua/autodoc/views/host.lua` and `panel.lua`. From the drawer you can browse the workspace's document tree, inspect the active file's heading outline and symbols, review and filter `.todo-list/` tasks (`:AutodocTodos` / `autodoc.todos`), and press `s` to switch the active KB workspace. Opening any item from the drawer targets your primary editor window (`panel.editor_target_winid()`), never overwriting the drawer buffer itself.

### 6-Slot Floating Markdown Previews (`autodoc.preview`)

Absorbing the workflow of `md-harpoon` (ADR 1791210485), `autodoc.preview` gives you six persistent floating Markdown slots arranged ergonomically on the home and number rows: **`1`, `2`, `3`** across the top and **`a`, `s`, `d`** below.

- **Render vs. Focus**: Pressing `<leader>m!` / `<leader>m@` / `<leader>m#` / `<leader>mA` / `<leader>mS` / `<leader>mD` (`:AutodocPreviewRender {slot}`) pins the current Markdown buffer into slot `1`/`2`/`3`/`a`/`s`/`d`. Pressing `<leader>m1`..`<leader>md` (`:AutodocPreviewFocus {slot}`) toggles or focuses that slot, restoring the exact line and column where you left the cursor.
- **Per-Project Persistence and Live Re-Render**: Slot pins and cursor coordinates persist across Neovim sessions via `auto-core` state (`autodoc.preview` namespace) and automatically follow Git worktree switches (`worktree:switched`). Buffer-backed slots re-render live on `TextChanged`/`TextChangedI`; file-backed slots re-render when `core.file:modified` fires.
- **In-Float Navigation and Browser Export**: Inside any preview float, `q` or `<Esc>` closes the window, `gx` follows the Markdown link or `#heading` anchor under the cursor inside the same slot, and `B` (`:AutodocPreviewBrowser`) exports the document with rendered Mermaid SVGs directly to your system web browser.

---

## 17.4 Scripting `autodoc.nvim` from Your Own Neovim Config

Because `autodoc.nvim` exposes every daemon domain through `require("autodoc.verbs")` and `require("autodoc.session")`, you can build custom Neovim statusline components, telescope/snacks actions, or keymaps that query the AutoDoc daemon asynchronously without blocking Neovim's main loop:

```lua
-- Example: Asynchronous Neovim keymap that validates the current KB buffer on <leader>mv
vim.keymap.set("n", "<leader>mv", function()
  local session = require("autodoc.session")
  local verbs   = require("autodoc.verbs")
  local path    = vim.fn.expand("%:p")

  session.workspace_for_file(path, function(ws, rel_path)
    if not ws then
      vim.notify("Current buffer is not inside an AutoDoc workspace", vim.log.levels.WARN)
      return
    end
    verbs.doc.validate(ws, rel_path, function(err, res)
      if err then
        vim.notify("doc.validate error: " .. tostring(err), vim.log.levels.ERROR)
        return
      end
      if not res.errors or #res.errors == 0 then
        vim.notify("Schema valid (" .. (res.type or "doc") .. ")", vim.log.levels.INFO)
      else
        vim.notify(string.format("%d schema warning(s): %s", #res.errors, res.errors[1].message), vim.log.levels.WARN)
      end
    end)
  end)
end, { desc = "Validate current document against AutoDoc workspace schema" })
```

---

## 17.5 Diagnostics with `:checkhealth autodoc` and `:AutodocMaintenance`

Run `:checkhealth autodoc` at any time to verify the binary path, daemon socket connectivity, protocol version parity between `autodoc.nvim` and `autodoc --serve`, active workspace health, and optional dependencies (`snacks.picker`, `auto-core`). If you upgrade the plugin or build a new binary, `:AutodocMaintenance` (`<leader>mX`) offers three immediate actions: `versions` (compare plugin vs. running daemon), `restart` (gracefully restart the background daemon), and `install` (rebuild/install the Go binary).

---

## 17.6 Safe, Reversible KB Migrations (`:AutodocKbMigrate`)

When upgrading an older knowledge base (with `shared/` and `agents/<name>/` trees) to the v2 schema-validated layout (ADR 1791209946 §8: top-level `adrs/`, `conventions/`, `playbooks/`, `reference/`, `synthesis/`, `reviews/<reviewer>/`, `prs/`, `notes/`, `archive/`, `_schema/`, and `_templates/`), `:AutodocKbMigrate` executes a deterministic, checksummed **6-stage pipeline** with full pre-image rollback:

![Typical Execution Time Across :AutodocKbMigrate Stages](kb-migration-step-duration.chart.json)

1. **Stage 1 — Inventory & SHA-256 Hashing**: Scans every file under the KB root and queries `todos.stores` for external `.todo-list/` stores referencing the KB. Computes the pre-migration SHA-256 of every file and resolves deterministic destination paths (`M.dest`), disambiguating any filename collisions with `--from-<old-parent-slug>`.
2. **Stage 2 — Schema & Template Scaffold**: Prepares `_schema/frontmatter.yaml`, folder `ABOUT.md` files, and `_templates/<type>.md` definitions without overwriting immutable `raw/` or archived files.
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

![Neovim Commands, Keymappings, and Lua Entry Points Reference](neovim-commands-and-mappings.csv)

---

[← Previous: Chapter 16: AutoDB Live SQL Workbench](./16-autodb-live-sql-workbench.md) | [Index](./README.md) | [Next: Chapter 18: Enterprise PostgreSQL RAG Server →](./18-enterprise-postgres-rag-server.md)

