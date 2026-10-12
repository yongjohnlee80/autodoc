# Your own commands, in Lua

The command line runs commands written in Lua: **:** opens it in Vim's Normal mode, **Ctrl+;** in any mode, and **Search › Command line…** where a terminal cannot send Ctrl+;. `:table` and `:mermaid` are AutoDoc's own; yours go in **`~/.config/autodoc/lua/`**, beside `config.toml`, as `*.lua` files.

**Load order.** At start AutoDoc's scripts load, then yours, in file-name order. A command named like one already loaded replaces it: yours can replace AutoDoc's, and a later file an earlier one's. A file that fails to load is a notification; the others still load. Restart AutoDoc to load an edited file.

```lua
-- :upper [line] — upper-case a line, the cursor's by default
autodoc.command{
  name = "upper", aliases = {"up"}, args = "[line]", help = "upper-case a line",
  run = function(ed, args, line)
    local n = tonumber(args[1]) or ed:cursor()
    ed:replace(n, 1, n, #ed:line(n) + 1, ed:line(n):upper())
    autodoc.notify("upper-cased line " .. n)
  end,
}
```

**`name`** is what you type, **`aliases`** are other names for it, and **`args`** and **`help`** are what Tab shows. **`run`** gets the editor, `ed`; the words after the command, `args`; and the rest of the line as typed, `line`.

**Any command, by its id.** `autodoc.run("todos.open")` runs a command of AutoDoc's or yours (`autodoc.run("lua.upper", "3")`) and returns `ok, err`; F1 lists every id under COMMANDS.

**The editor, `ed`.** Positions are 1-based: line 1, column 1 is the first character. A range runs from its start up to, not including, its end: `ed:text(2, 1, 2, 6)` is line 2's first five characters. `ed:cursor()` and `ed:selection()` say where you are; `ed:insert`, `ed:replace` and `ed:delete` edit; `ed:find(term)` searches; `ed:run(":12")` runs another command. Whatever one command edits is **one undo**: `u` takes it all back.

**The sandbox.** Scripts get Lua's base, table, string and math libraries: no `os`, no `io`, no files, no `require`. A script that fails, or runs past two seconds, is stopped; its error shows in the prompt, and AutoDoc carries on.

**The full tutorial**, step by step from `:hello` to a `:link` that asks the daemon, is `docs/lua/tutorial-vim-commands.md` in AutoDoc's repository.
