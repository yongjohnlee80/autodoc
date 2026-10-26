# Chapter 14: Lua Scripting & Custom Commands

AutoDoc embeds a deterministic, sandboxed Lua 5.1 runtime (`gopher-lua`) directly inside the terminal UI so you can author custom `:` commands, modal keybindings, interactive fuzzy pickers, and asynchronous workspace workflows without compiling external binaries. In fact, AutoDoc's built-in `:table` formatter and `:mermaid` scaffolding commands are written in this exact Lua environment.

This chapter covers where scripts and shareable packages live, how the 2-second execution sandbox and single-step undo batching protect your notes, how to manipulate buffers using grapheme-aware coordinates, and how to combine interactive UI pickers with capability-gated daemon RPCs. For a step-by-step beginner walkthrough, see `docs/lua/tutorial-vim-commands.md`; for the exhaustive method catalog, cross-reference `docs/lua/reference.md`.

---

## 14.1 Where Scripts Live, Load Order, and Hot Reloading

Personal Lua scripts live in `~/.config/autodoc/lua/` (or `$XDG_CONFIG_HOME/autodoc/lua/`, or alongside the configuration file passed to `--config`). At startup, AutoDoc initializes a fresh Lua state, loads its built-in scripts (`table.lua`, `mermaid.lua`), and then evaluates every `*.lua` file in your `lua/` directory in lexicographical filename order.

- **Replacing Built-In Commands**: Every command in AutoDoc has both a catalog ID (such as `terminal.open` or `lua.table`) and one or more `:` names. When your script registers a command whose name matches an existing `:` command, your command claims that `:` name while leaving the built-in command's catalog ID intact.
- **Instant Reloading (`:lua-reload`)**: Running `:lua-reload`—or simply saving any file inside `~/.config/autodoc/lua/` from within AutoDoc's editor—discards the current Lua state, drops old event handlers and keybindings, and reloads all scripts cleanly from disk. Any in-flight daemon callback belonging to the discarded generation is safely ignored when its reply arrives.

---

## 14.2 The 2-Second Sandbox and Single-Step Atomic Undo

Because Lua scripts execute on AutoDoc's UI loop, the runtime enforces two strict invariants: **isolation** and **bounded execution**.

![Lua Sandbox Execution Model](lua-sandbox-execution-model.mmd)

### Sandbox Boundaries

Scripts receive Lua 5.1's `base`, `string`, `table`, and `math` libraries plus the `autodoc` global table. System escape vectors—including `os`, `io`, `package`, `coroutine`, `debug`, `dofile`, `loadfile`, and network sockets—are absent from the environment. For system timestamps and formatting, scripts use `autodoc.now()` (UTC seconds with millisecond precision) and `autodoc.date(format, t)`. When you need arbitrary subprocesses, network sockets, or database drivers, write an out-of-process plugin instead (Chapter 15).

### The 2-Second Execution Deadline

Every script load, command invocation (`run`), argument completion (`complete`), event handler (`autodoc.on`), and asynchronous daemon callback is governed by a strict **2-second context deadline**. If an accidental infinite loop runs past 2,000 milliseconds, the VM halts execution and surfaces `<command>: the script ran past 2s and was stopped`. In normal operation, Lua commands complete in a fraction of a millisecond to a few milliseconds:

![Typical Lua Command Execution Time vs 2000ms Sandbox Cap](lua-execution-budget.chart.json)

### Single-Step Atomic Undo

Every buffer mutation performed during a command's `run` function—or inside an asynchronous callback—is automatically coalesced into a **single undo group**. Even if your script calls `ed:replace` fifty times across a Markdown table or invokes `ed:run` to chain another command, pressing `u` in Normal mode reverts the entire script's effect in one step.

---

## 14.3 Registering `:commands`, Completions, and Modal Keymaps

You register custom Ex commands with `autodoc.command` (aliased conceptually to `autodoc.command` in personal scripts and packages) and bind keys with `autodoc.map` (`autodoc.keymap`).

```lua
-- ~/.config/autodoc/lua/adr_stamp.lua
autodoc.command{
  name = "adr-new",
  aliases = {"adr"},
  args = "<status>",
  help = "insert schema-valid ADR YAML frontmatter at the top of the note",
  complete = function(ed, word, args, cmd)
    return {"proposed", "accepted", "superseded", "deprecated"}
  end,
  run = function(ed, args, line, cmd)
    if ed:read_only() then
      error("cannot insert frontmatter into a read-only document", 0)
    end
    local status = args[1] or "proposed"
    local today = autodoc.date("%Y-%m-%d")
    local header = table.concat({
      "---",
      "type: adr",
      "status: " .. status,
      "created: " .. today,
      "tags: [architecture]",
      'abstract: ""',
      "---",
      "",
    }, "\n")
    ed:insert_at(1, 1, header)
    if cmd.bang then
      ed:run("w")
    end
    autodoc.notify("Inserted ADR frontmatter (" .. status .. ")")
  end,
}

-- Bind in Normal mode and on the SPC u user Lua leader card:
autodoc.map{ mode = "normal", keys = "<C-a>", command = "adr-new accepted" }
autodoc.map{ mode = "leader", keys = "a", command = "adr-new", title = "Insert ADR frontmatter" }
-- In the right-click menu, under an "ADR" submenu, only on Markdown files (the Lua reference's
-- "Keys and menus" has every field: when, a live title, the row's handle):
autodoc.menu{ where = "editor", menu = "ADR", files = {"*.md"}, title = "Insert &Frontmatter", command = "adr-new" }
```

### Error Reporting Hygiene

When raising user-facing validation errors inside `run`, pass level `0` to `error("message", 0)`. Passing `0` suppresses the filename and line number prefix (`adr_stamp.lua:10:`), displaying a clean message in the command prompt while keeping the user's typed command line open for editing.

---

## 14.4 Buffer Manipulation and Grapheme-Aware Coordinates

Inside `run(ed, args, line, cmd)` and daemon callbacks, the editor handle `ed` (along with multi-buffer handles returned by `autodoc.buffers()` and `autodoc.buffer(n)`, the buffer manipulation surface often referred to as `autodoc.buf.*`) provides direct access to lines, selections, and search.

### Grapheme Clusters vs. Byte Lengths

AutoDoc's editor positions are **1-based** (`line`, `col`) where every column is a **UTF-8 grapheme cluster** (a visible character such as `é` or an emoji), whereas Lua's `#str` and `string.sub` count raw bytes. Always use `autodoc.text.columns(s)`, `autodoc.text.sub(s, i, j)`, and `autodoc.text.chars(s)` when computing column offsets on lines that may contain non-ASCII text.

- **Reading Lines**: `ed:lines()` returns a 1-indexed table of all lines (`autodoc.buf.lines` equivalent), `ed:line(n)` returns a single line without its trailing newline, and `ed:value()` returns the entire buffer string.
- **Mutating Ranges**: `ed:insert(text)` inserts at the cursor; `ed:insert_at(line, col, text)` inserts at an explicit position; `ed:replace(l1, c1, l2, c2, text)` replaces a half-open range `[start, end)`; and `ed:set_value(text)` (`autodoc.buf.set_lines` full-buffer equivalent) replaces the whole buffer as one undo step.
- **Chaining Exact Coordinates**: In Vim Normal mode, the visual cursor rests *on* the last character of a line rather than past it. Edit methods (`ed:insert`, `ed:replace`) return the exact `(line, col)` immediately following the inserted text—always chain sequential edits using those returned coordinates rather than re-querying `ed:cursor()`.

```lua
-- ~/.config/autodoc/lua/toggle_check.lua
autodoc.command{
  name = "toggle-check",
  help = "toggle Markdown checkbox [- [ ] / - [x]] on the current line or visual selection",
  run = function(ed)
    local l1, _, l2, _ = ed:selection()
    if not l1 then
      l1 = ed:cursor()
      l2 = l1
    end
    local toggled = 0
    for line_no = l1, l2 do
      local text = ed:line(line_no)
      local prefix, state, rest = text:match("^(%s*[-*+]%s+%[)([ xX])(%].*)$")
      if prefix then
        local next_state = (state == " ") and "x" or " "
        local new_line = prefix .. next_state .. rest
        ed:replace(line_no, 1, line_no, autodoc.text.columns(text) + 1, new_line)
        toggled = toggled + 1
      end
    end
    if toggled == 0 then
      error("no Markdown checkbox found on selected line(s)", 0)
    end
  end,
}
autodoc.map{ mode = "normal", keys = "<C-t>", command = "toggle-check" }
autodoc.map{ mode = "visual", keys = "<C-t>", command = "toggle-check" }
```

---

## 14.5 Interactive Fuzzy Pickers and Daemon RPC (`autodoc.call`)

Lua scripts never block the UI thread waiting for the AutoDoc daemon. Whether you call `autodoc.call(verb, params, cb)` (the generic CLI/daemon RPC bridge `autodoc.cli`) or a domain helper generated from `sys.contracts` (`autodoc.search.query`, `autodoc.doc.read`, `autodoc.todos.list`), the call returns immediately and invokes `cb(result, err)` on the UI loop once the daemon replies.

### Guarding Against Concurrent Edits with `ed:revision()`

Because the user can type while an asynchronous daemon RPC or `autodoc.ui.pick` modal is open, always capture `local rev = ed:revision()` before initiating an async operation if your callback intends to mutate specific line/column coordinates:

```lua
-- ~/.config/autodoc/lua/cite_search.lua
autodoc.command{
  name = "cite",
  args = "<query>",
  help = "search the workspace and insert a Markdown link at the cursor",
  run = function(ed, args, line)
    local ws = autodoc.current_workspace()
    if not ws then error("open a workspace first", 0) end
    if line == "" then error("usage: :cite <search query>", 0) end

    local start_rev = ed:revision()
    local cur_line, cur_col = ed:cursor()

    autodoc.search.query(ws, line, { limit = 15 }, function(res, err)
      if err then
        autodoc.notify("cite search failed: " .. err)
        return
      end
      if not res or #res.hits == 0 then
        autodoc.notify("cite: no hits for '" .. line .. "'")
        return
      end

      local items = {}
      for _, h in ipairs(res.hits) do
        items[#items + 1] = {
          label = h.path .. " — " .. (h.breadcrumb or ""),
          detail = h.snippet or "",
          value = h,
        }
      end

      autodoc.ui.pick({ title = "Insert Citation (" .. line .. ")", items = items }, function(choice, pick_err)
        if pick_err or not choice then return end
        if ed:revision() ~= start_rev then
          autodoc.notify("cite: buffer changed while picking; aborted edit")
          return
        end
        local link = string.format("[%s](%s)", choice.breadcrumb ~= "" and choice.breadcrumb or choice.path, choice.path)
        ed:insert_at(cur_line, cur_col, link)
      end)
    end)
  end,
}
```

---

## 14.6 Shareable Packages (`package.toml`) and Capability Grants

While personal `lua/*.lua` files run with full access to the `autodoc` table, third-party or shared scripts live in subdirectories `~/.config/autodoc/lua/<name>/` containing `init.lua` and a `package.toml` manifest.

```toml
# ~/.config/autodoc/lua/todo-helper/package.toml
name = "todo-helper"
version = "1.2.0"
description = "Interactive todo triage and quick-filing commands"
requires = ["workspace.read", "workspace.write", "commands"]
```

A package can also declare requirements in code via `autodoc.requires{"workspace.read", "workspace.write"}`. Each package runs in its own isolated global environment (`_G`, `string`, `table`, `math` copies) and is **capability-gated**:

- `workspace.read`: Required for read-only daemon verbs (`doc.read`, `search.query`, `graph.backlinks`, `todos.list`).
- `workspace.write`: Required for mutating daemon verbs (`doc.write`, `todos.add`, `todos.status`, `todos.update`).
- `settings`: Required for workspace, embedding provider, ranker, and preference mutations.
- `commands`: Required for `autodoc.run`, `ed:run`, `autodoc.map`, and `autodoc.menu`.
- `clipboard` and `agents`: Required for `autodoc.clipboard.*` and `autodoc.agents.*`.

When a user installs a package via `:lua-install <name or git-url>` (a name is the registry's package of that name) or drops it into `lua/<name>/`, AutoDoc prompts once to grant the declared capabilities. Any ungranted call returns `nil, "needs <capability>: not granted"` without executing.

![Lua API Namespace Reference](lua-api-namespace-reference.csv)

---

## 14.7 The Lua Shell, Cards, and `:usage`

`:lua` opens a **Lua shell** in a tab of the terminal panel. An expression typed there shows its value, a statement runs, and a line that stops mid-statement asks for more with `..`. The shell's globals are its own, `ed` is the page, and `print` writes into the shell. `:lua <chunk>` runs one chunk and shows its values as a notification.

A script can show a **card** beside the page with `autodoc.ui.card{title, text, every, tick}`: Markdown, refreshed by `tick` while the card is open, with a `vega-lite` fence drawn as a chart in the window (`--gui`). AutoDoc's own `:usage` is a card of the daemon's and the app's memory and CPU, read through `sys.stats` and `autodoc.app.stats()`, and of the workspace's index.

---

[← Previous: Chapter 13: Concurrency & Safe Multi-Agent Writes](./13-concurrency-and-safe-multi-agent-writes.md) | [Index](./README.md) | [Next: Chapter 15: Plugin Architecture & Registry →](./15-plugin-architecture-and-registry.md)
